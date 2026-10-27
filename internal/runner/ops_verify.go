package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// logCollector keeps at most maxResultLogs tool lines for a unary result.
type logCollector struct {
	mu      sync.Mutex
	now     func() time.Time
	lines   []*runnerpb.LogLine
	dropped uint64
}

func (c *logCollector) add(s runnerpb.LogStream, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lines) >= maxResultLogs {
		c.dropped++
		return
	}
	text, truncated := strings.CutSuffix(text, truncatedMarker)
	c.lines = append(c.lines, &runnerpb.LogLine{Time: timestamppb.New(c.now()), Stream: s, Text: text, Truncated: truncated})
}

func (c *logCollector) Log(line string) { c.add(runnerpb.LogStream_LOG_STREAM_STDERR, line) }

func (c *logCollector) result() ([]*runnerpb.LogLine, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines, c.dropped
}

// verifyStream is the receiving side of a VerifyDump call.
type verifyStream interface {
	Context() context.Context
	Recv() (*runnerpb.VerifyDumpRequest, error)
}

// verifyDump implements VerifyDump: PostgreSQL archives are listed with
// pg_restore --list in an offline container (no network, no credentials);
// MySQL/MariaDB dumps and PostgreSQL globals are checked in Go on the
// streamed tail (completion trailer). Exactly total_bytes must arrive.
func (o *operations) verifyDump(stream verifyStream) (resp *runnerpb.VerifyDumpResponse, err error) {
	ctx := stream.Context()
	a := newAudit("VerifyDump", "")
	defer func() { o.audit(ctx, a, err) }()
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, status.Error(codes.InvalidArgument, "invalid request: header: the first message must be the header")
		}
		return nil, err
	}
	v, st, err := o.val.VerifyHeader(first)
	if err != nil {
		return nil, err
	}
	runID := first.GetHeader().GetCtx().GetRunId()
	a.runID, a.engine, a.version = runID, v.Kind, v.Entry.Key()
	recv := func() ([]byte, error) {
		m, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		return st.VerifyChunk(m)
	}
	logs := &logCollector{now: o.now}
	resp = &runnerpb.VerifyDumpResponse{}

	if v.Kind != engine.KindPostgres || v.Dump == runnerpb.DumpKind_DUMP_KIND_GLOBALS {
		w := &output{}
		if err := drainAll(recv, st.Complete, func(b []byte) { w.keepTail(b) }); err != nil {
			return nil, err
		}
		if v.Kind == engine.KindPostgres {
			resp.Verified = GlobalsTrailerOK(w.tail)
		} else {
			resp.Verified = engine.DumpTrailerOK(w.tail)
		}
		if !resp.Verified {
			logs.add(runnerpb.LogStream_LOG_STREAM_RUNNER, "the dump does not end with the tool's completion trailer (truncated or not a dump)")
		}
		resp.Logs, resp.DroppedLogLines = logs.result()
		return resp, nil
	}

	if o.exec == nil {
		return nil, errNoExecutor
	}
	eng, err := engine.ForKind(v.Kind)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	p := startPump(ctx, recv, st.Complete)
	var stdoutBytes uint64
	res, err := o.exec.Run(ctx, executor.RunSpec{
		RunID:   runID,
		Entry:   v.Entry,
		Offline: true,
		Command: func(executor.ClientEndpoint) (engine.Command, error) { return eng.VerifyDump(v.Entry) },
		Stdin:   p.Reader(),
		// pg_restore writes its script to /dev/null; any stdout is counted,
		// never returned (no database contents in results).
		Stdout:  func(b []byte) error { stdoutBytes += uint64(len(b)); return nil },
		Log:     logs.Log,
		Timeout: remaining(ctx),
	})
	if err != nil {
		return nil, runFailure(ctx, p, err)
	}
	if err := p.Finish(ctx); err != nil {
		return nil, streamStatus(err)
	}
	code := res.ExitCode
	a.exitCode = &code
	resp.ExitCode = int32(res.ExitCode) //nolint:gosec // exit codes are small
	resp.Verified = res.ExitCode == 0
	a.extra = append(a.extra, slog.Uint64("tool_stdout_bytes", stdoutBytes))
	resp.Logs, resp.DroppedLogLines = logs.result()
	resp.DroppedLogLines += uint64(max(res.Log.Dropped, 0))
	return resp, nil
}

// runFailure maps an executor error of a run fed by p. A stdin failure
// comes from the pump (invalid or incomplete stream, receive error): its
// error is reported. Otherwise the pump is stopped without draining.
func runFailure(ctx context.Context, p *pump, err error) error {
	if errors.Is(err, executor.ErrStdin) {
		if perr := p.Finish(ctx); perr != nil {
			return streamStatus(perr)
		}
	}
	p.Abort()
	return executor.Status(err)
}

// streamStatus maps a receive error of a client stream.
func streamStatus(err error) error {
	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "run canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, executor.ErrTimeout.Error())
	default:
		return status.Error(codes.Aborted, executor.ErrStdin.Error())
	}
}
