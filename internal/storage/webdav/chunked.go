package webdav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

// Chunked uploads of the Nextcloud flavor (D-222). Nextcloud's chunking
// v2 and ownCloud's "new chunking" (ownCloud 10, TransIP STACK) share the
// protocol bekup uses:
//
//  1. MKCOL /remote.php/dav/uploads/<user>/bekup-<hex> (with a Destination
//     header: Nextcloud then uses multipart uploads on object storage);
//  2. PUT .../bekup-<hex>/00001, 00002, ... (each chunk with a known
//     Content-Length, so PHP and proxy body limits never see the whole
//     dump; Nextcloud accepts the numbers 1..10000, ownCloud sorts the
//     names naturally);
//  3. MOVE .../bekup-<hex>/.file to the final path with Destination,
//     OC-Total-Length (the server checks the assembled size) and
//     "Overwrite: F" (Sabre's MOVE refuses an existing target with 412
//     before the chunking plugin assembles).
//
// A server without the upload endpoint (404, 403, 405, 501 on the MKCOL)
// falls back to the single streaming PUT for the destination's lifetime.

var (
	// errNoChunking: the server has no chunked-upload endpoint.
	errNoChunking = errors.New("webdav: chunked uploads are not available")
	// errEmptyUpload: the source had no bytes (no chunk to assemble).
	errEmptyUpload = errors.New("webdav: empty upload")
)

// chunkAttempts bounds the attempts of one chunk (network failures and
// 5xx answers; chunks are idempotent).
const chunkAttempts = 3

// startUpload creates the upload collection; errNoChunking when the server
// has none.
func (d *Destination) startUpload(ctx context.Context, up []string, dest string) error {
	const op = "start a chunked upload"
	code, _, err := d.do(ctx, op, request{method: "MKCOL", segs: up, coll: true, header: http.Header{"Destination": {dest}}}, nil)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusCreated, http.StatusOK, http.StatusNoContent:
		d.chunk.CompareAndSwap(chunkUnknown, chunkOn)
		return nil
	case http.StatusNotFound, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusConflict:
		if d.chunk.Swap(chunkOff) != chunkOff {
			d.s.logger.InfoContext(ctx, "webdav_chunked_upload_unavailable", "host", d.s.host, "status", code)
		}
		return errNoChunking
	}
	return statusErr(op, code)
}

// putChunked uploads r through the chunked-upload API and publishes it.
func (d *Destination) putChunked(ctx context.Context, lay *layout, final []string, r io.Reader, opts storage.PutOptions) (int64, []byte, error) {
	id, err := randomHex()
	if err != nil {
		return 0, nil, err
	}
	up := d.join(lay.uploads, []string{"bekup-" + id})
	dest := d.urlOf(final, false)
	if err := d.startUpload(ctx, up, dest); err != nil {
		return 0, nil, err
	}
	committed := false
	defer func() {
		if !committed {
			d.remove(ctx, up, true)
		}
	}()
	w := &chunkWriter{ctx: ctx, d: d, up: up, dest: dest, buf: make([]byte, 0, d.s.chunk)}
	n, sum, err := storage.CopyHashed(ctx, w, r, d.s.chunk*MaxChunks)
	if err != nil {
		return 0, nil, fmt.Errorf("webdav: chunked upload: %w", err)
	}
	if err := w.flush(); err != nil {
		return 0, nil, err
	}
	if w.next == 0 {
		// No chunk was uploaded (flush numbers the first one): Nextcloud
		// cannot assemble zero chunks, so Put falls back to one PUT.
		return 0, nil, errEmptyUpload
	}
	if err := storage.VerifySHA256(opts.ExpectedSHA256, sum); err != nil {
		return 0, nil, err
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	extra := http.Header{"Oc-Total-Length": {strconv.FormatInt(n, 10)}}
	if err := d.finalize(ctx, lay, d.join(up, []string{".file"}), final, extra); err != nil {
		return 0, nil, err
	}
	committed = true // the move consumed the upload collection
	// The server checked OC-Total-Length; check what it published, too.
	e, err := d.statObject(ctx, final)
	if err == nil && e.size == n {
		return n, sum, nil
	}
	if err == nil {
		// bekup just created this object (Overwrite: F): remove the bad
		// copy, even in no-delete mode (it is not a backup).
		d.remove(ctx, final, false)
		return 0, nil, storage.WithDetail(fmt.Errorf("%w: webdav: the server assembled %d of %d bytes", storage.ErrChecksumMismatch, e.size, n),
			"the server assembled "+storage.FormatSizes(e.size, n))
	}
	return 0, nil, err
}

// chunkWriter collects the copied bytes into chunks and uploads each full
// one (one chunk in memory).
type chunkWriter struct {
	ctx  context.Context
	d    *Destination
	up   []string
	dest string
	buf  []byte
	next int // number of the next chunk (1-based)
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		room := cap(w.buf) - len(w.buf)
		take := min(room, len(p))
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		written += take
		if len(w.buf) == cap(w.buf) {
			if err := w.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// flush uploads the buffered bytes as the next chunk (nothing when empty).
func (w *chunkWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	if w.next == 0 {
		w.next = 1
	}
	if w.next > MaxChunks {
		return storage.WithDetail(fmt.Errorf("%w: webdav: too many chunks", storage.ErrUnsupported),
			"the object needs more than "+strconv.Itoa(MaxChunks)+" chunks")
	}
	if err := w.d.putChunk(w.ctx, w.up, w.dest, w.next, w.buf); err != nil {
		return err
	}
	w.next++
	w.buf = w.buf[:0]
	return nil
}

// putChunk uploads one chunk, retrying network failures and 5xx answers
// (chunks are idempotent) and 423/503 like every other request.
func (d *Destination) putChunk(ctx context.Context, up []string, dest string, i int, data []byte) error {
	const op = "upload a chunk"
	segs := d.join(up, []string{chunkName(i)})
	failures, waits := 0, 0
	for {
		var last error
		resp, err := d.send(ctx, op, request{
			method: http.MethodPut, segs: segs, body: bytes.NewReader(data), length: int64(len(data)),
			header: http.Header{"Content-Type": {"application/octet-stream"}, "Destination": {dest}},
		})
		if err != nil {
			c := storage.Classify(err)
			if ctx.Err() != nil || (c != storage.CodeNetwork && c != storage.CodeTimeout) {
				return err
			}
			last = err
		} else {
			code := resp.StatusCode
			drain(resp)
			switch {
			case code == http.StatusCreated || code == http.StatusNoContent || code == http.StatusOK:
				return nil
			case retryable(code) && waits < len(retryDelays):
				if err := sleep(ctx, retryDelays[waits]); err != nil {
					return err
				}
				waits++
				continue
			case code >= 500:
				last = statusErr(op, code)
			default:
				return statusErr(op, code)
			}
		}
		failures++
		if failures >= chunkAttempts {
			return last
		}
		if err := sleep(ctx, time.Duration(failures)*500*time.Millisecond); err != nil {
			return err
		}
	}
}
