package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

var (
	// errDropped is returned to a dropped target's Put by its reader.
	errDropped = errors.New("pipeline: destination dropped")
	// errAborted is returned to every Put when the run fails.
	errAborted = errors.New("pipeline: run aborted")
	// errLag is the error recorded for lag drops.
	errLag = errors.New("pipeline: destination lagged beyond the threshold")
	// errShortRead: Put returned success without reading the whole stream.
	errShortRead = errors.New("pipeline: destination did not read the whole stream")
)

// chunk is an immutable slice of artifact bytes shared by every target.
// refs counts the holders (the producer while dispatching plus each target
// it was queued to); the fan-out's buffered counter drops when it reaches
// zero.
type chunk struct {
	b    []byte
	refs atomic.Int32
	fo   *fanout
}

func (c *chunk) release() {
	if c.refs.Add(-1) == 0 {
		c.fo.buffered.Add(-int64(len(c.b)))
	}
}

// fanout is the io.Writer at the end of the chain. It hashes and counts the
// stored bytes, cuts them into chunks and queues every chunk to each live
// target's bounded channel.
type fanout struct {
	ctx   context.Context
	spec  *Spec
	dests []*destWriter
	wg    sync.WaitGroup

	// cur, hash and n are written by the encoder chain only (zstd may
	// call Write from its own goroutine, but never concurrently).
	cur  []byte
	hash hash.Hash
	n    int64

	// errMu guards err, the sticky producer-side error; failCh is closed
	// when it is set, so a blocked dispatch returns at once.
	errMu  sync.Mutex
	err    error
	failCh chan struct{}

	// complete is set (before the channels are closed) only when every
	// byte was queued; readers return io.EOF only then.
	complete atomic.Bool
	closed   bool

	buffered atomic.Int64
	peak     atomic.Int64

	storedSize int64
	storedSum  []byte
}

func newFanout(ctx context.Context, spec *Spec) *fanout {
	f := &fanout{ctx: ctx, spec: spec, hash: sha256.New(), failCh: make(chan struct{})}
	for i := range spec.Targets {
		t := spec.Targets[i]
		dctx, cancel := context.WithCancel(ctx)
		d := &destWriter{
			idx: i, t: t, fo: f, ctx: dctx, cancel: cancel,
			ch:     make(chan *chunk, spec.BufferChunks),
			dropCh: make(chan struct{}),
		}
		d.lastProgress.Store(time.Now().UnixNano())
		f.dests = append(f.dests, d)
	}
	return f
}

func (f *fanout) start() {
	for _, d := range f.dests {
		f.wg.Go(d.run)
	}
}

// Write implements io.Writer for the encryption/compression chain.
func (f *fanout) Write(p []byte) (int, error) {
	if err := f.stickyErr(); err != nil {
		return 0, err
	}
	written := 0
	for len(p) > 0 {
		if f.cur == nil {
			f.cur = make([]byte, 0, f.spec.ChunkSize)
		}
		m := min(len(p), f.spec.ChunkSize-len(f.cur))
		f.cur = append(f.cur, p[:m]...)
		f.hash.Write(p[:m])
		f.n += int64(m)
		p = p[m:]
		written += m
		if len(f.cur) == f.spec.ChunkSize {
			b := f.cur
			f.cur = nil
			if err := f.dispatch(b); err != nil {
				f.fail(err)
				return written, f.stickyErr()
			}
		}
	}
	return written, nil
}

// fail sets the sticky error so later writes fail immediately.
func (f *fanout) fail(err error) {
	f.errMu.Lock()
	defer f.errMu.Unlock()
	if f.err == nil {
		f.err = err
		close(f.failCh)
	}
}

func (f *fanout) stickyErr() error {
	f.errMu.Lock()
	defer f.errMu.Unlock()
	return f.err
}

// cause maps an error surfaced by the encoder chain to the fan-out's own
// error when there is one (every target dropped, ctx ended).
func (f *fanout) cause(err error) error {
	if serr := f.stickyErr(); serr != nil {
		return serr
	}
	if cerr := f.ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%w: %w", ErrProcessing, err)
}

func (f *fanout) live() []*destWriter {
	out := make([]*destWriter, 0, len(f.dests))
	for _, d := range f.dests {
		if !d.dropped.Load() {
			out = append(out, d)
		}
	}
	return out
}

// dispatch queues b to every live target. A target whose buffer stays full
// for LagTimeout (one deadline shared by all targets for this chunk) is
// dropped; the others are not held up beyond the deadline.
func (f *fanout) dispatch(b []byte) error {
	live := f.live()
	if len(live) == 0 {
		return ErrAllDestinationsFailed
	}
	c := &chunk{b: b, fo: f}
	c.refs.Store(int32(len(live)) + 1) //nolint:gosec // G115: len(live) <= MaxTargets
	v := f.buffered.Add(int64(len(b)))
	if v > f.peak.Load() {
		f.peak.Store(v) // only the producer raises the peak
	}
	defer c.release() // the producer's reference

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	expired := false
	size := int64(len(b))
	for _, d := range live {
		d.pending.Add(size)
		select {
		case d.ch <- c:
			continue
		default:
		}
		if expired {
			d.pending.Add(-size)
			d.drop(ReasonLag, errLag)
			c.release()
			continue
		}
		if timer == nil {
			timer = time.NewTimer(f.spec.LagTimeout)
		}
		select {
		case d.ch <- c:
		case <-d.dropCh:
			d.pending.Add(-size)
			c.release()
		case <-timer.C:
			expired = true
			d.pending.Add(-size)
			d.drop(ReasonLag, errLag)
			c.release()
		case <-f.ctx.Done():
			d.pending.Add(-size)
			c.release()
			return f.ctx.Err()
		case <-f.failCh:
			d.pending.Add(-size)
			c.release()
			return f.stickyErr()
		}
	}
	if len(f.live()) == 0 {
		return ErrAllDestinationsFailed
	}
	return nil
}

// finish flushes the last chunk, signals EOF to every live target and
// waits for their Puts. While waiting, a target that has unread data but
// read nothing for LagTimeout is dropped.
func (f *fanout) finish() error {
	if len(f.cur) > 0 {
		b := f.cur
		f.cur = nil
		if err := f.dispatch(b); err != nil {
			f.abort(err)
			return err
		}
	}
	f.storedSize, f.storedSum = f.n, f.hash.Sum(nil)
	f.complete.Store(true)
	f.closeChannels()

	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }()
	since := time.Now()
	tick := max(f.spec.LagTimeout/8, time.Millisecond)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-f.ctx.Done():
			// No target may see EOF any more; Puts that already did may
			// still finalize a complete object.
			for _, d := range f.dests {
				d.drop(ReasonAborted, f.ctx.Err())
			}
			<-done
			return f.ctx.Err()
		case now := <-ticker.C:
			for _, d := range f.dests {
				if d.dropped.Load() || d.pending.Load() == 0 {
					continue
				}
				last := max(d.lastProgress.Load(), since.UnixNano())
				if now.UnixNano()-last > int64(f.spec.LagTimeout) {
					d.drop(ReasonLag, errLag)
				}
			}
		}
	}
}

// abort cancels every Put (no target may see EOF) and waits for them.
func (f *fanout) abort(err error) {
	f.fail(err)
	for _, d := range f.dests {
		d.drop(ReasonAborted, err)
	}
	f.closeChannels()
	f.wg.Wait()
}

func (f *fanout) closeChannels() {
	if f.closed {
		return
	}
	f.closed = true
	for _, d := range f.dests {
		close(d.ch)
	}
}

// results returns the per-target outcomes (after finish or abort).
func (f *fanout) results(ctx context.Context) []DestinationResult {
	out := make([]DestinationResult, 0, len(f.dests))
	for _, d := range f.dests {
		out = append(out, d.result(ctx))
	}
	return out
}

// destWriter feeds one target: a goroutine calls Put with a reader over
// the target's bounded chunk channel.
type destWriter struct {
	idx    int
	t      Target
	fo     *fanout
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan *chunk

	dropOnce   sync.Once
	dropCh     chan struct{}
	dropped    atomic.Bool
	dropReason DropReason
	dropErr    error

	pending      atomic.Int64 // queued bytes not yet read
	lastProgress atomic.Int64 // unix nanos of the last read
	sawEOF       atomic.Bool

	info   storage.ObjectInfo
	putErr error
	reader *chanReader
}

// drop marks the target dropped (first reason wins), cancels its Put and
// releases the chunks still queued to it.
func (d *destWriter) drop(reason DropReason, err error) {
	d.dropOnce.Do(func() {
		d.dropReason, d.dropErr = reason, err
		d.dropped.Store(true)
		close(d.dropCh)
		d.cancel()
	})
	if reason == ReasonAborted || reason == ReasonLag {
		// Called from the producer: drain what Put has not taken. The
		// target's goroutine drains the rest once Put returns.
		for {
			select {
			case c, ok := <-d.ch:
				if !ok {
					return
				}
				d.pending.Add(-int64(len(c.b)))
				c.release()
			default:
				return
			}
		}
	}
}

func (d *destWriter) run() {
	d.reader = &chanReader{d: d}
	d.info, d.putErr = d.t.Dest.Put(d.ctx, d.t.Key, d.reader, d.t.Options)
	d.reader.releaseCurrent()
	if d.putErr != nil {
		if err := d.fo.ctx.Err(); err != nil {
			d.drop(ReasonAborted, err)
		} else {
			d.drop(ReasonError, d.putErr)
		}
	}
	for c := range d.ch {
		d.pending.Add(-int64(len(c.b)))
		c.release()
	}
	d.cancel()
}

func (d *destWriter) result(ctx context.Context) DestinationResult {
	r := DestinationResult{Name: d.t.Name, Key: d.t.Key}
	var err error
	switch {
	case d.putErr != nil:
		err = d.putErr
	case !d.sawEOF.Load():
		err = errShortRead
	case d.info.Size != d.fo.storedSize || !bytes.Equal(d.info.SHA256, d.fo.storedSum):
		err = fmt.Errorf("%w: stored object differs from the streamed bytes", storage.ErrChecksumMismatch)
	}
	if err == nil {
		// Complete and verified, even if a lag drop raced with the
		// final read: the object is whole.
		r.Outcome, r.Code, r.Info = OutcomeOK, storage.CodeOK, d.info
		return r
	}
	r.Outcome, r.Err = OutcomeDropped, err
	r.Reason = d.dropReason
	if d.putErr == nil {
		// Put claimed success with a wrong or short object: remove it.
		r.Reason = ReasonVerify
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		_ = d.t.Dest.Delete(dctx, d.t.Key) //nolint:errcheck,gosec // best effort; the outcome already says dropped
		cancel()
	}
	if r.Reason == ReasonNone {
		r.Reason = ReasonError
	}
	switch r.Reason {
	case ReasonLag:
		r.Err, r.Code = errLag, storage.CodeTimeout
	case ReasonAborted:
		r.Err, r.Code = d.dropErr, storage.Classify(d.dropErr)
		if r.Code == storage.CodeInternal {
			r.Code = storage.CodeCanceled
		}
	case ReasonNone, ReasonError, ReasonVerify:
		r.Code = storage.Classify(err)
	}
	return r
}

// chanReader is the io.Reader handed to Put. It returns io.EOF only after
// the fan-out completed and only for a target that was not dropped; on
// abort or drop it returns an error, so the backend discards the upload.
type chanReader struct {
	d   *destWriter
	mu  sync.Mutex
	cur *chunk
	off int
}

func (r *chanReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.cur == nil {
		if r.d.dropped.Load() {
			return 0, errDropped
		}
		select {
		case c, ok := <-r.d.ch:
			if !ok {
				if r.d.fo.complete.Load() && !r.d.dropped.Load() {
					r.d.sawEOF.Store(true)
					return 0, io.EOF
				}
				return 0, errAborted
			}
			r.cur, r.off = c, 0
		case <-r.d.ctx.Done():
			return 0, r.d.ctx.Err()
		}
	}
	if r.d.dropped.Load() {
		r.releaseLocked()
		return 0, errDropped
	}
	n := copy(p, r.cur.b[r.off:])
	r.off += n
	r.d.pending.Add(-int64(n))
	r.d.lastProgress.Store(time.Now().UnixNano())
	if r.off == len(r.cur.b) {
		r.cur.release()
		r.cur = nil
	}
	return n, nil
}

func (r *chanReader) releaseCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseLocked()
}

func (r *chanReader) releaseLocked() {
	if r.cur != nil {
		r.d.pending.Add(-int64(len(r.cur.b) - r.off))
		r.cur.release()
		r.cur = nil
	}
}
