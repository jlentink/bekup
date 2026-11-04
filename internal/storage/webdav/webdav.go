package webdav

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/storage"
)

// Destination is a WebDAV backup destination. See the package
// documentation for the semantics and the residual trust in the server.
type Destination struct {
	s    *settings
	pw   *crypto.Secret
	auth string // the Basic Authorization header value (see Close)
	hc   *http.Client
	slow *http.Client

	closed atomic.Bool

	// lay is the resolved resource layout (Nextcloud: needs the user id).
	lay   atomic.Pointer[layout]
	laySl chan struct{} // 1-slot semaphore serializing the resolution

	// probe is the overwrite probe's verdict (probeUnknown until a probe
	// finished); probeSl serializes probes.
	probe   atomic.Int32
	probeSl chan struct{}
	// chunk is the Nextcloud upload mode (chunkUnknown until known).
	chunk atomic.Int32

	// sizeDelays are the waits between the stored-size checks of a Put.
	sizeDelays []time.Duration
}

// Overwrite probe verdicts.
const (
	probeUnknown int32 = iota
	probeRefused       // the server refuses MOVE onto an existing file: usable
	probeIgnored       // the server replaced the file: never usable
)

// Upload modes of the Nextcloud flavor.
const (
	chunkUnknown int32 = iota
	chunkOn            // chunked uploads work
	chunkOff           // the server has no chunked-upload endpoint: single PUT
)

// UploadMode names how Put uploads.
type UploadMode string

// Upload modes reported by UploadMode.
const (
	UploadUnknown UploadMode = ""
	UploadSingle  UploadMode = "single"
	UploadChunked UploadMode = "chunked"
)

// layout are the resources of the destination.
type layout struct {
	// files is the destination root (base + path) and floor the number of
	// its leading segments that must exist already (never created).
	files []string
	floor int
	// uploads is the Nextcloud user's upload collection (nil: generic).
	uploads []string
	// uid is the Nextcloud/ownCloud user id whose files these are ("":
	// generic).
	uid string
}

var _ storage.Destination = (*Destination)(nil)

// Open validates cfg and returns a destination. It does not connect: the
// first operation (or Test) does. Open takes ownership of cfg.Password.
func Open(cfg Config) (*Destination, error) {
	s, err := cfg.validate()
	if err != nil {
		cfg.Password.Destroy()
		return nil, err
	}
	d := &Destination{
		s: s, pw: cfg.Password,
		laySl: make(chan struct{}, 1), probeSl: make(chan struct{}, 1),
		sizeDelays: sizeRetryDelays,
	}
	// HTTP needs the header as a string; see Close.
	raw := make([]byte, 0, len(s.user)+1+cfg.Password.Len())
	raw = append(append(append(raw, s.user...), ':'), cfg.Password.Bytes()...)
	d.auth = "Basic " + base64.StdEncoding.EncodeToString(raw)
	crypto.Zero(raw)
	d.newClients()
	if s.flavor == FlavorGeneric {
		d.lay.Store(&layout{files: d.join(s.basePath, s.dir.Segments()), floor: len(s.basePath)})
	}
	if s.scheme == "http" {
		s.logger.Warn("webdav_destination_plain_http", "reason", "dev_mode", "host", s.host)
	}
	return d, nil
}

// join concatenates segment lists into a new slice.
func (*Destination) join(parts ...[]string) []string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]string, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Capabilities implements storage.Destination.
func (d *Destination) Capabilities() storage.Capabilities {
	c := storage.Capabilities{
		Type:         "webdav",
		NoDeleteMode: d.s.noDelete,
		AtomicRename: true,
		// Known after the overwrite probe (the first Put or Test).
		ExclusiveCreate: d.probe.Load() == probeRefused,
	}
	if d.s.flavor == FlavorNextcloud && d.chunk.Load() != chunkOff {
		c.MaxObjectSize = d.s.chunk * MaxChunks
	}
	return c
}

// UploadMode reports how Put uploads (UploadUnknown until the first upload
// or probe of a Nextcloud destination).
func (d *Destination) UploadMode() UploadMode {
	if d.s.flavor == FlavorGeneric {
		return UploadSingle
	}
	switch d.chunk.Load() {
	case chunkOn:
		return UploadChunked
	case chunkOff:
		return UploadSingle
	}
	return UploadUnknown
}

// Close implements storage.Destination. It closes idle connections and
// destroys the password.
//
// Limitation: HTTP needs the Authorization header as a Go string, which
// cannot be zeroed; one copy lives while the destination is open (and in
// the transport's request buffers) and is left to the garbage collector.
func (d *Destination) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.hc.CloseIdleConnections()
	d.slow.CloseIdleConnections()
	d.pw.Destroy()
	d.auth = ""
	return nil
}

// check validates the common preconditions of a keyed operation.
func (d *Destination) check(ctx context.Context, key storage.Key) error {
	if d.closed.Load() {
		return storage.ErrClosed
	}
	if key.IsZero() {
		return fmt.Errorf("%w: empty key", storage.ErrInvalidKey)
	}
	return ctx.Err()
}

// acquire takes a 1-slot semaphore or gives up with ctx.
func acquire(ctx context.Context, sem chan struct{}) (func(), error) {
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// layout returns the resources, resolving the Nextcloud user id once.
func (d *Destination) layout(ctx context.Context) (*layout, error) {
	if l := d.lay.Load(); l != nil {
		return l, nil
	}
	release, err := acquire(ctx, d.laySl)
	if err != nil {
		return nil, err
	}
	defer release()
	if l := d.lay.Load(); l != nil {
		return l, nil
	}
	uid, err := d.userID(ctx)
	if err != nil {
		return nil, err
	}
	dav := d.join(d.s.basePath, []string{"remote.php", "dav"})
	userFiles := d.join(dav, []string{"files", uid})
	l := &layout{
		files:   d.join(userFiles, d.s.dir.Segments()),
		floor:   len(userFiles),
		uploads: d.join(dav, []string{"uploads", uid}),
		uid:     uid,
	}
	d.lay.Store(l)
	return l, nil
}

// UserID returns the Nextcloud/ownCloud user id whose files the
// destination addresses (resolved once from the current-user-principal;
// the login name when the server names none), or "" for the generic
// flavor. The destinations service records it for the location overlap
// check (D-222 amendment): files live below the user id, which may differ
// from the login name (e-mail or LDAP logins).
func (d *Destination) UserID(ctx context.Context) (string, error) {
	if d.closed.Load() {
		return "", storage.ErrClosed
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return "", err
	}
	return lay.uid, nil
}

// userID asks the server for the current user's principal
// (/remote.php/dav/principals/users/<id>/): Nextcloud and ownCloud address
// files by the user id, which may differ from the login name (e-mail or
// LDAP logins). Without a usable answer the username is used.
func (d *Destination) userID(ctx context.Context) (string, error) {
	const op = "find the Nextcloud/ownCloud user"
	segs := d.join(d.s.basePath, []string{"remote.php", "dav"})
	resp, err := d.send(ctx, op, request{
		method: "PROPFIND", segs: segs, coll: true, depth: "0", bounded: true,
		body: strings.NewReader(principalBody), length: int64(len(principalBody)),
		header: http.Header{"Content-Type": {"application/xml; charset=utf-8"}},
	})
	if err != nil {
		return "", err
	}
	code := resp.StatusCode
	if code != http.StatusMultiStatus {
		drain(resp)
		if code == http.StatusNotFound {
			return "", storage.WithDetail(fmt.Errorf("%w: webdav: no Nextcloud/ownCloud endpoint", storage.ErrNotFound),
				"there is no Nextcloud/ownCloud WebDAV endpoint (remote.php/dav) at this address; enter the server address or use the generic flavor")
		}
		return "", statusErr(op, code)
	}
	entries, err := parseMultistatus(resp.Body)
	drain(resp)
	if err != nil {
		return d.s.user, nil //nolint:nilerr // an unusable answer falls back to the login name
	}
	for i := range entries {
		if id, ok := principalUser(entries[i].princ); ok {
			return id, nil
		}
	}
	return d.s.user, nil
}

// principalUser extracts <id> from .../principals/users/<id>/.
func principalUser(href string) (string, bool) {
	segs, ok := hrefSegments(href)
	if !ok || len(segs) < 3 {
		return "", false
	}
	n := len(segs)
	if segs[n-3] != "principals" || segs[n-2] != "users" {
		return "", false
	}
	id := segs[n-1]
	if id == "" || len(id) > maxUserLen || ValidateUser(id) != nil {
		return "", false
	}
	return id, true
}

// partialName matches the temporary files of Put and the probe. Dot
// names are never valid key segments, so partials are never listed.
var partialName = regexp.MustCompile(`^\.[0-9a-f]{32}\.partial$`)

// uploadName matches bekup's Nextcloud upload collections.
var uploadName = regexp.MustCompile(`^bekup-[0-9a-f]{32}$`)

func randomHex() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webdav: temporary name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func newPartialName() (string, error) {
	h, err := randomHex()
	if err != nil {
		return "", err
	}
	return "." + h + ".partial", nil
}

// detached returns a short context for cleanups that must run even when
// the caller's context ended.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

// remove deletes a resource best effort (cleanups).
func (d *Destination) remove(ctx context.Context, segs []string, coll bool) {
	if d.closed.Load() {
		return
	}
	cctx, cancel := detached(ctx)
	defer cancel()
	_, _, err := d.do(cctx, "remove a temporary file", request{method: http.MethodDelete, segs: segs, coll: coll}, nil)
	discard(err) // best effort; CleanupPartials removes leftovers
}

// mkcolAll creates the collection segs and its missing parents, never one
// of the first floor segments (they must exist).
func (d *Destination) mkcolAll(ctx context.Context, segs []string, floor int) error {
	if len(segs) <= floor {
		return nil
	}
	const op = "create a folder"
	for attempt := 0; attempt < 3; attempt++ {
		code, _, err := d.do(ctx, op, request{method: "MKCOL", segs: segs, coll: true}, nil)
		if err != nil {
			return err
		}
		switch code {
		case http.StatusCreated, http.StatusOK, http.StatusNoContent:
			return nil
		case http.StatusMethodNotAllowed:
			return nil // exists (a file in the way fails the following request)
		case http.StatusForbidden, http.StatusLocked, http.StatusInternalServerError:
			// A concurrent MKCOL of the same folder answers so on some
			// servers (Apache): fine if the folder exists now.
			if e, serr := d.stat(ctx, op, segs, true); serr == nil && e.coll {
				return nil
			}
			return statusErr(op, code)
		case http.StatusConflict:
			if len(segs)-1 <= floor {
				return storage.WithDetail(fmt.Errorf("%w: webdav: the base folder is missing", storage.ErrNotFound),
					"the folder of the URL does not exist on the server (bekup creates only the folders below it)")
			}
			if err := d.mkcolAll(ctx, segs[:len(segs)-1], floor); err != nil {
				return err
			}
		default:
			return statusErr(op, code)
		}
	}
	return storage.WithDetail(storage.WithCode(storage.CodeInternal, errors.New("webdav: folder creation did not settle")),
		op+": the folder kept disappearing")
}

// Put implements storage.Destination: the bytes go to a hidden
// ".<hex>.partial" (generic: one PUT with chunked transfer encoding;
// Nextcloud: the chunked-upload API), are hashed with storage.CopyHashed
// and size-checked, and are published with MOVE "Overwrite: F", which the
// overwrite probe has shown the server to honor.
func (d *Destination) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if err := storage.CheckPutOptions(d.Capabilities(), opts); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := d.check(ctx, key); err != nil {
		return storage.ObjectInfo{}, err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := d.ensureProbe(ctx, lay); err != nil {
		return storage.ObjectInfo{}, err
	}
	final := d.join(lay.files, key.Segments())
	parent := final[:len(final)-1]
	if _, err := d.statObject(ctx, final); err == nil {
		return storage.ObjectInfo{}, storage.ErrAlreadyExists
	} else if !errors.Is(err, storage.ErrNotFound) {
		return storage.ObjectInfo{}, err
	}
	if err := d.mkcolAll(ctx, parent, lay.floor); err != nil {
		return storage.ObjectInfo{}, err
	}
	var (
		n   int64
		sum []byte
	)
	if lay.uploads != nil && d.chunk.Load() != chunkOff {
		n, sum, err = d.putChunked(ctx, lay, final, r, opts)
		if errors.Is(err, errNoChunking) || errors.Is(err, errEmptyUpload) {
			if errors.Is(err, errEmptyUpload) {
				r = bytes.NewReader(nil) // the source was empty
			}
			n, sum, err = d.putSingle(ctx, lay, final, r, opts)
		}
	} else {
		n, sum, err = d.putSingle(ctx, lay, final, r, opts)
	}
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	return storage.ObjectInfo{Key: key, Size: n, ModTime: time.Now().UTC(), SHA256: sum, ContentType: opts.ContentType}, nil
}

// errUploadAbandoned ends the source copy when the request ended first.
var errUploadAbandoned = errors.New("webdav: the upload request ended")

// putSingle uploads r to a partial with one streaming PUT and publishes it.
func (d *Destination) putSingle(ctx context.Context, lay *layout, final []string, r io.Reader, opts storage.PutOptions) (int64, []byte, error) {
	name, err := newPartialName()
	if err != nil {
		return 0, nil, err
	}
	parent := final[:len(final)-1]
	tmp := d.join(parent, []string{name})
	committed := false
	defer func() {
		if !committed {
			d.remove(ctx, tmp, false)
		}
	}()

	pr, pw := io.Pipe()
	type result struct {
		n   int64
		sum []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, sum, err := storage.CopyHashed(ctx, pw, r, 0)
		if err != nil {
			discard(pw.CloseWithError(err))
		} else {
			discard(pw.Close()) // closing a pipe writer always succeeds
		}
		done <- result{n, sum, err}
	}()
	resp, serr := d.send(ctx, "upload", request{
		method: http.MethodPut, segs: tmp, body: pr, length: -1,
		header: http.Header{"Content-Type": {"application/octet-stream"}},
	})
	code := 0
	if serr == nil {
		code = resp.StatusCode
		drain(resp)
	}
	discard(pr.CloseWithError(errUploadAbandoned)) // unblocks the copy if the server answered early
	res := <-done
	switch {
	case res.err != nil && errors.Is(res.err, storage.ErrSource):
		return 0, nil, fmt.Errorf("webdav: upload: %w", res.err)
	case ctx.Err() != nil:
		return 0, nil, fmt.Errorf("webdav: upload: %w", ctx.Err())
	case serr != nil:
		return 0, nil, serr
	case code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusOK:
		return 0, nil, statusErr("upload", code)
	case res.err != nil:
		return 0, nil, fmt.Errorf("webdav: upload: %w", res.err)
	}
	if err := storage.VerifySHA256(opts.ExpectedSHA256, res.sum); err != nil {
		return 0, nil, err
	}
	if err := d.storedSize(ctx, tmp, res.n); err != nil {
		return 0, nil, err
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if err := d.finalize(ctx, lay, tmp, final, nil); err != nil {
		return 0, nil, err
	}
	committed = true
	return res.n, res.sum, nil
}

// sizeRetryDelays are the waits between the stored-size checks of Put (5
// checks over about 3 s; servers backed by object or cloud storage may
// show a new file a moment late).
var sizeRetryDelays = []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond}

// storedSize checks that the partial holds all n bytes: a server that
// silently dropped the request body (some PHP setups with chunked
// transfer encoding store 0 bytes) is caught before the move.
func (d *Destination) storedSize(ctx context.Context, tmp []string, n int64) error {
	var (
		got     int64
		missing bool
	)
	for i := 0; ; i++ {
		e, err := d.stat(ctx, "check the uploaded file", tmp, false)
		switch {
		case err == nil && e.coll:
			return storage.WithDetail(fmt.Errorf("%w: webdav: the temporary file is a folder", storage.ErrChecksumMismatch),
				"the uploaded file was replaced on the server")
		case err == nil && e.size == n:
			return nil
		case err == nil:
			got, missing = e.size, false
		case errors.Is(err, storage.ErrNotFound):
			got, missing = 0, true
		default:
			return err
		}
		if got > n || i >= len(d.sizeDelays) {
			break
		}
		if err := sleep(ctx, d.sizeDelays[i]); err != nil {
			return err
		}
	}
	if missing {
		return storage.WithDetail(fmt.Errorf("%w: webdav: the temporary file of %d bytes is missing", storage.ErrChecksumMismatch, n),
			"the server did not show the uploaded file ("+storage.FormatSize(n)+" written)")
	}
	if got < 0 {
		return storage.WithDetail(fmt.Errorf("%w: webdav: the server reported no size", storage.ErrChecksumMismatch),
			"the server did not report the uploaded file's size")
	}
	return storage.WithDetail(fmt.Errorf("%w: webdav: the server stored %d of %d bytes", storage.ErrChecksumMismatch, got, n),
		"the server stored "+storage.FormatSizes(got, n)+" (if it is 0 bytes behind a PHP server, use the Nextcloud flavor)")
}

// finalize moves src to final with "Overwrite: F". A 412 is
// ErrAlreadyExists; a 204 means the server replaced an existing object
// despite the header (the probe passed earlier, so this is a server
// fault): the destination is marked unusable. A missing parent (a
// concurrent prune) is created again once.
func (d *Destination) finalize(ctx context.Context, lay *layout, src, final []string, extra http.Header) error {
	const op = "finalize the upload"
	h := http.Header{"Destination": {d.urlOf(final, false)}, "Overwrite": {"F"}}
	for k, v := range extra {
		h[k] = v
	}
	serverErrors := 0
	for attempt := 0; ; attempt++ {
		code, _, err := d.do(ctx, op, request{method: "MOVE", segs: src, header: h, slow: true, timeout: slowResponseTimeout}, nil)
		if err != nil {
			return err
		}
		if code >= 500 && serverErrors < len(finalizeRetryDelays) {
			// Servers backed by a database (ownCloud/Nextcloud on SQLite,
			// file locking) answer a concurrent move with a 5xx; once
			// the source is still there, the move is repeated (a target
			// that appeared meanwhile then answers 412).
			if _, serr := d.statObject(ctx, src); serr == nil {
				if err := sleep(ctx, finalizeRetryDelays[serverErrors]); err != nil {
					return err
				}
				serverErrors++
				continue
			}
		}
		switch code {
		case http.StatusCreated, http.StatusOK:
			return nil
		case http.StatusNoContent:
			d.probe.Store(probeIgnored)
			d.s.logger.ErrorContext(ctx, "webdav_overwrite_detected", "host", d.s.host)
			return errOverwriteIgnored()
		case http.StatusPreconditionFailed:
			return storage.ErrAlreadyExists
		case http.StatusConflict:
			if attempt == 0 {
				if err := d.mkcolAll(ctx, final[:len(final)-1], lay.floor); err != nil {
					return err
				}
				continue
			}
		}
		return statusErr(op, code)
	}
}

// finalizeRetryDelays are the pauses before repeating a move that failed
// with a server error while its source still exists.
var finalizeRetryDelays = []time.Duration{200 * time.Millisecond, 500 * time.Millisecond, time.Second}

// statObject returns the entry of a non-collection resource (ErrNotFound
// for a missing one or a collection).
func (d *Destination) statObject(ctx context.Context, segs []string) (*entry, error) {
	e, err := d.stat(ctx, "check the object", segs, false)
	if err != nil {
		return nil, err
	}
	if e.coll {
		return nil, storage.ErrNotFound
	}
	return e, nil
}

// Get implements storage.Destination. The length is checked against the
// PROPFIND size: a short or long stream fails with ErrChecksumMismatch
// (callers verify the SHA-256 on top).
func (d *Destination) Get(ctx context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	segs := d.join(lay.files, key.Segments())
	e, err := d.statObject(ctx, segs)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if e.size < 0 {
		return nil, storage.ObjectInfo{}, storage.WithDetail(storage.WithCode(storage.CodeInternal, errors.New("webdav: no size reported")),
			"the server did not report the object's size")
	}
	resp, err := d.send(ctx, "read the object", request{
		method: http.MethodGet, segs: segs, header: http.Header{"Accept-Encoding": {"identity"}},
	})
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone, http.StatusMethodNotAllowed:
		drain(resp)
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	default:
		code := resp.StatusCode
		drain(resp)
		return nil, storage.ObjectInfo{}, statusErr("read the object", code)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		drain(resp)
		return nil, storage.ObjectInfo{}, storage.WithDetail(fmt.Errorf("%w: webdav: encoded download", storage.ErrUnsupported),
			"the server sent the object with a content encoding although none was asked for")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != e.size {
		drain(resp)
		return nil, storage.ObjectInfo{}, storage.WithDetail(
			fmt.Errorf("%w: webdav: the server announced %d of %d bytes", storage.ErrChecksumMismatch, resp.ContentLength, e.size),
			"the server announced "+storage.FormatSizes(resp.ContentLength, e.size))
	}
	return &reader{ctx: ctx, body: resp.Body, want: e.size}, storage.ObjectInfo{Key: key, Size: e.size, ModTime: e.mod}, nil
}

// reader checks ctx before every read and enforces the expected length.
type reader struct {
	ctx  context.Context
	body io.ReadCloser
	want int64
	n    int64
}

func (r *reader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	m, err := r.body.Read(b)
	r.n += int64(m)
	if r.n > r.want {
		keep := max(int64(m)-(r.n-r.want), 0)
		return int(keep), storage.WithDetail(
			fmt.Errorf("%w: webdav: the server returned more than the expected %d bytes", storage.ErrChecksumMismatch, r.want),
			"the server returned more than the expected "+storage.FormatSize(r.want))
	}
	if errors.Is(err, io.EOF) && r.n != r.want {
		return m, storage.WithDetail(fmt.Errorf("%w: webdav: read %d of %d bytes", storage.ErrChecksumMismatch, r.n, r.want),
			"read "+storage.FormatSizes(r.n, r.want))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		if cerr := r.ctx.Err(); cerr != nil {
			return m, cerr
		}
		return m, storage.WithCode(storage.CodeNetwork, fmt.Errorf("webdav: read the object: %w", err))
	}
	return m, err
}

func (r *reader) Close() error { return r.body.Close() }

// Stat implements storage.Destination.
func (d *Destination) Stat(ctx context.Context, key storage.Key) (storage.ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return storage.ObjectInfo{}, err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	e, err := d.statObject(ctx, d.join(lay.files, key.Segments()))
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	return storage.ObjectInfo{Key: key, Size: max(e.size, 0), ModTime: e.mod}, nil
}

// Delete implements storage.Destination. Only a non-collection resource is
// deleted (a WebDAV DELETE of a collection is recursive): the resource is
// checked with PROPFIND first and the DELETE carries its strong ETag as
// If-Match when the server reported one.
func (d *Destination) Delete(ctx context.Context, key storage.Key) error {
	if d.s.noDelete {
		if d.closed.Load() {
			return storage.ErrClosed
		}
		if key.IsZero() {
			return fmt.Errorf("%w: empty key", storage.ErrInvalidKey)
		}
		return fmt.Errorf("%w: no-delete mode", storage.ErrDeleteNotPermitted)
	}
	if err := d.check(ctx, key); err != nil {
		return err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return err
	}
	segs := d.join(lay.files, key.Segments())
	return d.deleteObject(ctx, segs)
}

func (d *Destination) deleteObject(ctx context.Context, segs []string) error {
	const op = "delete the object"
	e, err := d.statObject(ctx, segs)
	if errors.Is(err, storage.ErrNotFound) {
		return nil // missing, or a collection bekup never deletes here
	}
	if err != nil {
		return err
	}
	strong := e.etag != "" && !strings.HasPrefix(e.etag, "W/")
	for attempt := 0; attempt < 2; attempt++ {
		var h http.Header
		if strong && attempt == 0 {
			h = http.Header{"If-Match": {e.etag}}
		}
		code, _, err := d.do(ctx, op, request{method: http.MethodDelete, segs: segs, header: h}, nil)
		if err != nil {
			return err
		}
		switch code {
		case http.StatusOK, http.StatusNoContent, http.StatusAccepted, http.StatusNotFound:
			return nil
		case http.StatusPreconditionFailed:
			// Some servers report another ETag in PROPFIND than they
			// compare: accept the delete without the condition only if the
			// resource is still the same object.
			again, err := d.statObject(ctx, segs)
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if again.size != e.size || !again.mod.Equal(e.mod) {
				return storage.WithDetail(fmt.Errorf("%w: webdav: the object changed", storage.ErrPermission),
					op+": the object changed on the server while it was deleted")
			}
			continue
		}
		return statusErr(op, code)
	}
	return statusErr(op, http.StatusPreconditionFailed)
}

// List implements storage.Destination: a depth-first walk with PROPFIND
// Depth 1 per collection (Depth infinity is often disabled), entries
// sorted by name with collections as name+"/", so keys come out in
// byte-wise order. Memory is bounded by the page size plus one listing per
// level.
func (d *Destination) List(ctx context.Context, prefix storage.Key, opts storage.ListOptions) (storage.ListPage, error) {
	if d.closed.Load() {
		return storage.ListPage{}, storage.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return storage.ListPage{}, err
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return storage.ListPage{}, err
	}
	limit := opts.PageSize
	if limit <= 0 || limit > storage.MaxListPageSize {
		limit = storage.MaxListPageSize
	}
	l := &lister{ctx: ctx, d: d, root: lay.files, after: opts.StartAfter.String(), limit: limit + 1}
	if err := l.walk(prefix.String()); err != nil {
		return storage.ListPage{}, err
	}
	page := storage.ListPage{Objects: l.out}
	if len(page.Objects) > limit {
		page.Objects = page.Objects[:limit]
		page.More = true
	}
	if n := len(page.Objects); n > 0 {
		page.Next = page.Objects[n-1].Key
	}
	return page, nil
}

type lister struct {
	ctx   context.Context
	d     *Destination
	root  []string
	after string
	limit int
	out   []storage.ObjectInfo
}

// readDir lists a collection (nil for a missing resource or a
// non-collection), sorted for the walk.
func (d *Destination) readDir(ctx context.Context, segs []string) ([]entry, error) {
	entries, err := d.propfind(ctx, "list a folder", segs, true, "1")
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil && len(segs) > 0 && (statusOf(err) == http.StatusBadRequest || statusOf(err) == http.StatusMethodNotAllowed) {
		// Some servers (Apache mod_dav) refuse a collection URL that
		// names a file: a file is no folder to list.
		if e, serr := d.stat(ctx, "list a folder", segs, false); serr == nil && !e.coll {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	kids, self, err := listChildren(entries, segs)
	if err != nil {
		return nil, storage.WithDetail(storage.WithCode(storage.CodeInternal, fmt.Errorf("webdav: list a folder: %w", err)),
			"list a folder: the server's listing names the folder differently than requested (letter case or Unicode normalization of the destination's path); check the path's spelling")
	}
	if self != nil && !self.coll {
		return nil, nil
	}
	slices.SortFunc(kids, func(a, b entry) int { return strings.Compare(sortName(&a), sortName(&b)) })
	return kids, nil
}

func sortName(e *entry) string {
	if e.coll {
		return e.name() + "/"
	}
	return e.name()
}

func splitKey(k string) []string {
	if k == "" {
		return nil
	}
	return strings.Split(k, "/")
}

func (l *lister) walk(keyPrefix string) error {
	list, err := l.d.readDir(l.ctx, l.d.join(l.root, splitKey(keyPrefix)))
	if err != nil {
		return err
	}
	for i := range list {
		e := &list[i]
		if len(l.out) >= l.limit {
			return nil
		}
		name := e.name()
		if storage.ValidateSegment(name) != nil {
			continue // dot names (partials), foreign names
		}
		key := name
		if keyPrefix != "" {
			key = keyPrefix + "/" + name
		}
		if len(key) > storage.MaxKeyLen {
			continue
		}
		if e.coll {
			sub := key + "/"
			if sub <= l.after && !strings.HasPrefix(l.after, sub) {
				continue // every key below is <= StartAfter
			}
			if strings.Count(sub, "/") >= storage.MaxSegments {
				continue
			}
			if err := l.walk(key); err != nil {
				return err
			}
			continue
		}
		if key <= l.after {
			continue
		}
		k, err := storage.ParseKey(key)
		if err != nil {
			continue
		}
		l.out = append(l.out, storage.ObjectInfo{Key: k, Size: max(e.size, 0), ModTime: e.mod})
	}
	return nil
}

// Test implements storage.Destination (storage.RunProbe; the first Put
// runs the overwrite probe).
func (d *Destination) Test(ctx context.Context, prefix storage.Key) storage.TestResult {
	return storage.RunProbe(ctx, d, prefix)
}

// errOverwriteIgnored is the error of a destination whose server ignores
// "Overwrite: F".
func errOverwriteIgnored() error {
	return storage.WithDetail(storage.WithCode(storage.CodeOverwriteIgnored,
		errors.New("webdav: the server ignores Overwrite: F")),
		"the server moved a file onto an existing one although bekup sent 'Overwrite: F'")
}

// chunkName is the name of chunk i (1-based, zero padded: Nextcloud
// accepts numbers 1..10000, ownCloud sorts names naturally).
func chunkName(i int) string {
	s := strconv.Itoa(i)
	if len(s) < 5 {
		s = strings.Repeat("0", 5-len(s)) + s
	}
	return s
}
