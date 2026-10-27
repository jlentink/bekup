package runner

import (
	"context"
	"errors"
	"io"
	"sync"
)

// pump receives the chunk messages of a VerifyDump / Restore stream in one
// goroutine and feeds them into a pipe the container's stdin reads
// (backpressure: the next message is received only after the previous
// chunk was consumed). It is the only caller of the stream's Recv. When
// the client half-closes, Complete checks that exactly total_bytes
// arrived; any error closes the pipe with that error, so the executor
// kills the container instead of closing its stdin (a truncated input
// never looks complete).
type pump struct {
	pr   *io.PipeReader
	pw   *io.PipeWriter
	done chan struct{}
	err  error
	once sync.Once
}

// startPump runs recv until it returns io.EOF or an error. recv returns
// the next validated chunk; complete is called at io.EOF.
func startPump(ctx context.Context, recv func() ([]byte, error), complete func() error) *pump {
	pr, pw := io.Pipe()
	p := &pump{pr: pr, pw: pw, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		writeOK := true
		for {
			data, err := recv()
			if errors.Is(err, io.EOF) {
				err = complete()
				if err == nil {
					discardErr(pw.Close())
					return
				}
			}
			if err != nil {
				p.err = err
				discardErr(pw.CloseWithError(err))
				return
			}
			if ctx.Err() != nil {
				p.err = ctx.Err()
				discardErr(pw.CloseWithError(p.err))
				return
			}
			if writeOK {
				// After the consumer stopped (tool exited, run over) the
				// rest of the stream is still received and counted, so
				// the size check stays exact.
				if _, werr := pw.Write(data); werr != nil {
					writeOK = false
				}
			}
		}
	}()
	return p
}

// Reader is the container's stdin.
func (p *pump) Reader() io.Reader { return p.pr }

// Finish stops feeding the consumer and waits until the stream is fully
// received (or failed); it returns the receive / size error.
func (p *pump) Finish(ctx context.Context) error {
	p.once.Do(func() { discardErr(p.pr.Close()) })
	select {
	case <-p.done:
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Abort stops feeding the consumer without waiting (error paths: the
// handler returns, gRPC cancels the stream and the pump's Recv fails).
func (p *pump) Abort() {
	p.once.Do(func() { discardErr(p.pr.Close()) })
}

// drainAll reads every chunk in the calling goroutine (no container): f
// sees each validated chunk.
func drainAll(recv func() ([]byte, error), complete func() error, f func([]byte)) error {
	for {
		data, err := recv()
		if errors.Is(err, io.EOF) {
			return complete()
		}
		if err != nil {
			return err
		}
		f(data)
	}
}
