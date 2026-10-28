// Package digest holds the stream helpers every reader of a stored backup
// artifact shares: Reader hashes (SHA-256) and counts a stream and refuses
// more bytes than recorded, and CtxReader stops a stream once its context
// ends. It is a leaf package (standard library only) so offline tooling
// (internal/offline) can use it without pulling in the database or the
// storage backends.
package digest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
)

// ErrTooLong reports a stream longer than its recorded size.
var ErrTooLong = errors.New("artifactverify: stream longer than recorded")

// Reader hashes (SHA-256) and counts what is read through it and fails
// with ErrTooLong once more than its limit arrived. A negative limit
// disables the check.
type Reader struct {
	r     io.Reader
	h     hash.Hash
	n     int64
	limit int64
}

// NewReader wraps r with a limit of limit bytes (negative: unlimited).
func NewReader(r io.Reader, limit int64) *Reader {
	return &Reader{r: r, h: sha256.New(), limit: limit}
}

func (d *Reader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if n > 0 {
		d.h.Write(p[:n])
		d.n += int64(n)
		if d.limit >= 0 && d.n > d.limit {
			return n, ErrTooLong
		}
	}
	return n, err
}

// N is the number of bytes read so far.
func (d *Reader) N() int64 { return d.n }

// Sum is the SHA-256 of the bytes read so far.
func (d *Reader) Sum() []byte { return d.h.Sum(nil) }

// Matches reports whether exactly size bytes with the SHA-256 sum were
// read.
func (d *Reader) Matches(size int64, sum []byte) bool {
	return d.n == size && len(sum) == sha256.Size && bytes.Equal(d.Sum(), sum)
}

// CtxReader fails reads with the context's cause once ctx is done. When
// Mark is set, read errors other than io.EOF that are not caused by the
// context are wrapped with it (e.g. restore.ErrDestination).
type CtxReader struct {
	ctx  context.Context
	r    io.Reader
	mark error
}

// NewCtxReader wraps r; mark may be nil.
func NewCtxReader(ctx context.Context, r io.Reader, mark error) CtxReader {
	return CtxReader{ctx: ctx, r: r, mark: mark}
}

func (c CtxReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, context.Cause(c.ctx)
	}
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		if c.ctx.Err() != nil {
			return n, context.Cause(c.ctx)
		}
		if c.mark != nil {
			return n, fmt.Errorf("%w: %w", c.mark, err)
		}
	}
	return n, err
}
