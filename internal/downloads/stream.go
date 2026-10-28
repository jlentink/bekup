package downloads

import (
	"errors"
	"hash"
	"io"
	"net/http"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
)

// holdBack is how many bytes of a response are held back until the
// digests are checked: a corrupted or tampered artifact never reaches the
// client in full (the connection is aborted instead).
const holdBack = 64 << 10

// writeStall bounds a single write to the client (a stalled reader); the
// server's write_timeout is lifted for downloads.
const writeStall = 2 * time.Minute

// errTooLong reports a stream longer than its recorded size.
var errTooLong = errors.New("downloads: stream longer than recorded")

// digestReader hashes and counts what is read through it and fails once
// more than limit bytes arrived.
type digestReader struct {
	r     io.Reader
	h     hash.Hash
	n     int64
	limit int64
}

func (d *digestReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if n > 0 {
		d.h.Write(p[:n])
		d.n += int64(n)
		if d.n > d.limit {
			return n, errTooLong
		}
	}
	return n, err
}

// holdbackWriter writes to the client with a per-write deadline and keeps
// the last holdBack bytes until finish. started reports whether any byte
// (and so the status line) was sent. buf has a fixed capacity and is never
// reallocated: Write copies its input in pieces that fit, so no copy of
// plaintext is ever left behind in an abandoned array (wipe zeroes the one
// buffer).
type holdbackWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	buf     []byte
	hold    int
	stall   time.Duration
	started bool
}

// holdbackSlack is the room above the held-back tail: each emit sends at
// most this much.
const holdbackSlack = 64 << 10

func newHoldbackWriter(w http.ResponseWriter) *holdbackWriter {
	return newHoldbackWriterSize(w, holdBack, holdbackSlack)
}

func newHoldbackWriterSize(w http.ResponseWriter, hold, slack int) *holdbackWriter {
	return &holdbackWriter{w: w, rc: http.NewResponseController(w), hold: hold, stall: writeStall,
		buf: make([]byte, 0, hold+max(slack, 1))}
}

func (h *holdbackWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		// Copy what fits into the fixed buffer (never append past cap).
		n := copy(h.buf[len(h.buf):cap(h.buf)], p)
		h.buf = h.buf[:len(h.buf)+n]
		p = p[n:]
		written += n
		if over := len(h.buf) - h.hold; over > 0 {
			if err := h.emit(h.buf[:over]); err != nil {
				return written, err
			}
			// Keep the tail at the front of the same array.
			m := copy(h.buf, h.buf[over:])
			h.buf = h.buf[:m]
		}
	}
	return written, nil
}

func (h *holdbackWriter) emit(b []byte) error {
	h.started = true
	if err := h.rc.SetWriteDeadline(time.Now().Add(h.stall)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	_, err := h.w.Write(b)
	return err
}

// finish sends the held-back tail and flushes.
func (h *holdbackWriter) finish() error {
	if len(h.buf) > 0 {
		if err := h.emit(h.buf); err != nil {
			return err
		}
		h.buf = h.buf[:0]
	}
	if err := h.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// wipe zeroes the buffer (plain downloads hold database content).
func (h *holdbackWriter) wipe() {
	crypto.Zero(h.buf[:cap(h.buf)])
	h.buf = h.buf[:0]
}
