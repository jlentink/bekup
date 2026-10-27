package netguard

import (
	"crypto/tls"
	"io"
	"net/http"
	"time"
)

// HTTP transport limits.
const (
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
	maxResponseHeaderSize = 64 << 10
)

// NewHTTPTransport returns an http.Transport that dials only through the
// guard, never uses a proxy (a proxy would resolve the target itself and
// bypass the dial-time check), and requires TLS 1.2 or later. tlsCfg may
// be nil; it is cloned and its MinVersion raised to TLS 1.2 if lower.
func (g *Guard) NewHTTPTransport(tlsCfg *tls.Config) *http.Transport {
	var cfg *tls.Config
	if tlsCfg != nil {
		cfg = tlsCfg.Clone()
	} else {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            g.DialContext,
		TLSClientConfig:        cfg,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		IdleConnTimeout:        idleConnTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderSize,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    2,
	}
}

// NewHTTPClient returns a client over NewHTTPTransport that never follows
// redirects (a redirect yields an error wrapping ErrRedirect). timeout
// bounds the whole request including the body; 0 means no overall limit,
// so pass one unless the caller sets a context deadline.
func (g *Guard) NewHTTPClient(tlsCfg *tls.Config, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: g.NewHTTPTransport(tlsCfg),
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirect
		},
	}
}

// ReadAllLimited reads r to the end; more than limit bytes is
// ErrResponseTooLarge.
func ReadAllLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrResponseTooLarge
	}
	return b, nil
}

// LimitReader returns a reader that yields at most limit bytes of r and
// then fails with ErrResponseTooLarge if r has more.
func LimitReader(r io.Reader, limit int64) io.Reader {
	return &limitedReader{r: r, left: limit}
}

type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.left <= 0 {
		var probe [1]byte
		n, err := l.r.Read(probe[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}
