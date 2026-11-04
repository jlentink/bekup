package webdav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Response limits. Object downloads are streamed unlimited (Get checks the
// length against the PROPFIND size).
const (
	// maxDrain is how much of an error or ignored body is read before the
	// connection is closed.
	maxDrain = 64 << 10
	// maxPropfindBody bounds one PROPFIND answer (a Depth 1 listing of a
	// large collection stays far below it).
	maxPropfindBody = 16 << 20
	// maxStatusBody bounds status.php.
	maxStatusBody = 64 << 10
	// slowResponseTimeout bounds the wait for the answer to a finalizing
	// MOVE: Nextcloud and ownCloud assemble chunked uploads during it,
	// which takes minutes for multi-GB objects.
	slowResponseTimeout = 30 * time.Minute
	// responseHeaderTimeout bounds the wait for response headers after a
	// request (including a streamed upload body) was written.
	responseHeaderTimeout = 5 * time.Minute
)

// errPinMismatch: the presented leaf's SPKI is not the pinned one.
var errPinMismatch = errors.New("webdav: the server certificate does not match the pinned certificate")

// errNoCertificate: the server presented no certificate.
var errNoCertificate = errors.New("webdav: the server presented no certificate")

// tlsConfig is the client TLS configuration: TLS ≥ 1.2 against the system
// roots (default), only the CA bundle, or only the pinned certificate plus
// the leaf SPKI check (D-222). observe (may be nil) sees every verified
// leaf. Verification is never skipped: a pinned self-signed certificate
// verifies because it is the only root (host name and validity are still
// checked).
func (s *settings) tlsConfig(observe func(*x509.Certificate)) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.host}
	pin := s.pin
	switch {
	case pin != nil:
		pool := x509.NewCertPool()
		pool.AddCert(pin.cert)
		cfg.RootCAs = pool
	case s.roots != nil:
		cfg.RootCAs = s.roots
	}
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errNoCertificate
		}
		leaf := cs.PeerCertificates[0]
		if observe != nil {
			observe(leaf)
		}
		if pin != nil {
			sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(sum[:], pin.spki) != 1 {
				return errPinMismatch
			}
		}
		return nil
	}
	return cfg
}

// newClients builds the HTTP clients over the guard: hc for every request
// whose answer comes promptly, slow for the finalizing MOVE.
func (d *Destination) newClients() {
	mk := func(headerTimeout time.Duration) *http.Client {
		c := d.s.guard.NewHTTPClient(d.s.tlsConfig(nil), 0)
		if t, ok := c.Transport.(*http.Transport); ok {
			t.ResponseHeaderTimeout = headerTimeout
			// The transport decompresses gzip answers transparently only
			// when it asked for them; bekup asks for identity on downloads.
			t.DisableCompression = true
		}
		return c
	}
	d.hc = mk(responseHeaderTimeout)
	d.slow = mk(slowResponseTimeout)
}

// request is one HTTP request of the client.
type request struct {
	method string
	// segs are the decoded path segments below the host root.
	segs []string
	// coll adds a trailing slash (collections).
	coll   bool
	body   io.Reader
	length int64 // body length; -1 unknown (chunked transfer encoding)
	header http.Header
	// depth sets the Depth header ("" none).
	depth string
	// noAuth omits the Authorization header (status.php is outside the
	// configured base).
	noAuth bool
	// bounded applies the request timeout (or timeout, when set) to the
	// whole request (metadata requests); streamed transfers are bounded
	// by the caller's context.
	bounded bool
	timeout time.Duration
	// slow uses the client for finalizing moves.
	slow bool
}

// urlOf builds the absolute URL of a resource (segments escaped one by
// one).
func (d *Destination) urlOf(segs []string, coll bool) string {
	var b strings.Builder
	b.WriteString(d.s.scheme)
	b.WriteString("://")
	b.WriteString(d.s.hostPort)
	for _, s := range segs {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	if coll || len(segs) == 0 {
		b.WriteByte('/')
	}
	return b.String()
}

// send performs r. The caller closes the response body; for bounded
// requests the timeout ends when it is closed.
func (d *Destination) send(ctx context.Context, op string, r request) (*http.Response, error) {
	if d.closed.Load() {
		return nil, storage.ErrClosed
	}
	cancel := context.CancelFunc(func() {})
	if r.bounded {
		to := d.s.reqTO
		if r.timeout > 0 {
			to = r.timeout
		}
		ctx, cancel = context.WithTimeout(ctx, to)
	}
	body := r.body
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, r.method, d.urlOf(r.segs, r.coll), body)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("webdav: %s: build request: %w", op, err)
	}
	if r.body != nil {
		req.ContentLength = r.length
		if r.length == 0 {
			req.Body = http.NoBody
		}
	}
	for k, v := range r.header {
		req.Header[k] = v
	}
	if r.depth != "" {
		req.Header.Set("Depth", r.depth)
	}
	if !r.noAuth {
		req.Header.Set("Authorization", d.auth)
	}
	req.Header.Set("User-Agent", "bekup")
	c := d.hc
	if r.slow {
		c = d.slow
	}
	resp, err := c.Do(req)
	if err != nil {
		cancel()
		return nil, d.transportErr(ctx, op, err)
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelBody ends a bounded request's context when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// drain reads a little of an unused body and closes it.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	discard(err) // best effort: the connection is reused only if the body was read
	discard(resp.Body.Close())
}

// discard marks an error that is deliberately ignored (best-effort
// cleanups).
func discard(error) {}

// transportErr classifies a failed request: the caller's cancellation and
// source errors pass through; TLS failures get a code and a curated
// detail; everything else is one network class (like netguard.Normalize,
// so a test cannot be used as a port scanner).
func (d *Destination) transportErr(ctx context.Context, op string, err error) error {
	wrapped := fmt.Errorf("webdav: %s: %w", op, err)
	if errors.Is(err, storage.ErrSource) {
		return wrapped
	}
	if cerr := ctx.Err(); cerr != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		if errors.Is(cerr, context.DeadlineExceeded) {
			return storage.WithCode(storage.CodeTimeout, fmt.Errorf("%w: %w", cerr, wrapped))
		}
		return fmt.Errorf("%w: %w", cerr, wrapped)
	}
	if errors.Is(err, netguard.ErrRedirect) {
		return storage.WithDetail(storage.WithCode(storage.CodeNetwork, wrapped),
			"the server answered with a redirect, which bekup never follows; check the URL (https, the exact folder address, a trailing slash)")
	}
	if e := d.tlsErr(wrapped); e != nil {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return storage.WithCode(storage.CodeTimeout, wrapped)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return storage.WithCode(storage.CodeTimeout, wrapped)
	}
	return storage.WithCode(storage.CodeNetwork, wrapped)
}

// tlsErr classifies certificate failures (nil when err is none).
func (d *Destination) tlsErr(err error) error {
	if errors.Is(err, errPinMismatch) {
		return storage.WithCode(storage.CodeTLSPinMismatch, err)
	}
	var cv *tls.CertificateVerificationError
	if !errors.As(err, &cv) {
		return nil
	}
	if d.s.pin != nil {
		if len(cv.UnverifiedCertificates) > 0 {
			sum := sha256.Sum256(cv.UnverifiedCertificates[0].RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(sum[:], d.s.pin.spki) == 1 {
				// The pinned key, but the certificate is no longer valid
				// (expired, or reissued for other names).
				return storage.WithDetail(storage.WithCode(storage.CodeNetwork, err),
					"the pinned certificate is no longer valid (expired, or the host name in the URL no longer matches it); renew it on the server and pin it again")
			}
		}
		return storage.WithCode(storage.CodeTLSPinMismatch, err)
	}
	var (
		he x509.HostnameError
		ci x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &he):
		return storage.WithDetail(storage.WithCode(storage.CodeNetwork, err),
			"the server's TLS certificate does not name the host in the URL")
	case errors.As(err, &ci) && ci.Reason == x509.Expired:
		return storage.WithDetail(storage.WithCode(storage.CodeNetwork, err),
			"the server's TLS certificate has expired or is not yet valid")
	default:
		return storage.WithDetail(storage.WithCode(storage.CodeNetwork, err),
			"the server's TLS certificate is not trusted (self-signed or a private CA): pin it from the test result or add the CA bundle")
	}
}

// statusErr classifies an unexpected HTTP status of op. The detail names
// op (fixed text) and the status number only, never server text.
func statusErr(op string, code int) error {
	base := &httpStatusError{op: op, code: code}
	detail := op + " (HTTP " + strconv.Itoa(code) + ")"
	wrap := func(sentinel error) error { return fmt.Errorf("%w: %w", sentinel, base) }
	var err error
	switch {
	case code == http.StatusUnauthorized:
		err = wrap(storage.ErrPermission)
		detail += ": check the username and password (Nextcloud/ownCloud with two-factor authentication need an app password)"
	case code == http.StatusForbidden:
		err = wrap(storage.ErrPermission)
	case code == http.StatusNotFound, code == http.StatusGone:
		err = wrap(storage.ErrNotFound)
	case code == http.StatusConflict:
		err = wrap(storage.ErrNotFound)
		detail += ": a parent folder is missing"
	case code == http.StatusPreconditionFailed:
		err = wrap(storage.ErrAlreadyExists)
	case code == http.StatusRequestEntityTooLarge:
		err = wrap(storage.ErrUnsupported)
		detail += ": the server refused the upload's size (for Nextcloud and ownCloud use the Nextcloud flavor, which uploads in chunks)"
	case code == http.StatusInsufficientStorage:
		err = wrap(storage.ErrInsufficientSpace)
	case code == http.StatusMethodNotAllowed, code == http.StatusNotImplemented:
		err = wrap(storage.ErrUnsupported)
	case code == http.StatusLocked:
		err = storage.WithCode(storage.CodeNetwork, base)
		detail += ": the resource is locked on the server"
	case code == http.StatusRequestTimeout, code == http.StatusGatewayTimeout:
		err = storage.WithCode(storage.CodeTimeout, base)
	case code >= 500:
		err = storage.WithCode(storage.CodeNetwork, base)
	default:
		err = storage.WithCode(storage.CodeInternal, base)
	}
	return storage.WithDetail(err, detail)
}

// httpStatusError is an unexpected HTTP status (in every statusErr chain).
type httpStatusError struct {
	op   string
	code int
}

func (e *httpStatusError) Error() string { return "webdav: " + e.op + ": HTTP " + strconv.Itoa(e.code) }

// statusOf returns the HTTP status in err's chain (0 if none).
func statusOf(err error) int {
	var hs *httpStatusError
	if errors.As(err, &hs) {
		return hs.code
	}
	return 0
}

// retryable reports a status worth retrying after a short pause: 423 from
// a concurrent request holding a lock (golang.org/x/net/webdav and others
// lock the resources of every write for its duration) and 503.
func retryable(code int) bool {
	return code == http.StatusLocked || code == http.StatusServiceUnavailable
}

// retryDelays are the pauses between attempts of a retryable status.
var retryDelays = []time.Duration{
	20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond,
	400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond,
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends a bounded request with an optional small body, retrying
// 423/503, drains the answer and returns its status code and headers for
// the caller to interpret.
func (d *Destination) do(ctx context.Context, op string, r request, body []byte) (int, http.Header, error) {
	r.bounded = true
	for i := 0; ; i++ {
		if body != nil {
			r.body = bytes.NewReader(body)
			r.length = int64(len(body))
		}
		resp, err := d.send(ctx, op, r)
		if err != nil {
			return 0, nil, err
		}
		code, h := resp.StatusCode, resp.Header
		drain(resp)
		if retryable(code) && i < len(retryDelays) {
			if err := sleep(ctx, retryDelays[i]); err != nil {
				return 0, nil, err
			}
			continue
		}
		return code, h, nil
	}
}
