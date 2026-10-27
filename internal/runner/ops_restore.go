package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// restoreStream is a Restore call.
type restoreStream interface {
	Context() context.Context
	Recv() (*runnerpb.RestoreRequest, error)
	Send(*runnerpb.RestoreResponse) error
}

// restore implements Restore: the header is validated, the target checked
// (pin pre-flight, server version, the database must exist), the restore
// client chosen (max(dump client, server), D-036) and the data streamed
// into the engine's hardened restore command. A restore is never retried.
func (o *operations) restore(stream restoreStream) (err error) {
	ctx := stream.Context()
	a := newAudit("Restore", "")
	defer func() { o.audit(ctx, a, err) }()
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "invalid request: header: the first message must be the header")
		}
		return err
	}
	r, st, err := o.val.RestoreHeader(ctx, first)
	if err != nil {
		return err
	}
	h := first.GetHeader()
	rt := h.GetTarget()
	a.runID = h.GetCtx().GetRunId()
	a.target(r.Target)
	a.database = r.Database
	a.extra = append(a.extra, slog.String("kind", r.Dump.String()), slog.String("source_version_key", r.Source.Key()))
	if o.exec == nil {
		return errNoExecutor
	}
	co := checkOpts{}
	if r.Dump == runnerpb.DumpKind_DUMP_KIND_DATABASE {
		co.needDB = []string{r.Database}
	}
	chk, err := o.precheck(ctx, r.Target, rt, co)
	if err != nil {
		o.logCause(ctx, "Restore", err)
		return targetStatus(err)
	}
	entry, ws, err := o.val.Catalog().SelectRestoreClient(r.Kind, r.Source.ClientVersion(), chk.version)
	if err != nil {
		return runnerpb.ReasonUnsupportedServer.Status()
	}
	a.version = entry.Key()

	out := newOutput(o.now, func([]byte) error { return nil }, func(l *runnerpb.LogLine) error {
		return stream.Send(&runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Log{Log: l}})
	})
	defer out.Abandon()
	recv := func() ([]byte, error) {
		m, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		return st.RestoreChunk(m)
	}
	p := startPump(ctx, recv, st.Complete)
	rs := o.runSpec(a.runID, entry, r.Target, rt, func(et engine.Target) (engine.Command, error) {
		if r.Dump == runnerpb.DumpKind_DUMP_KIND_GLOBALS {
			return r.Engine.RestoreGlobals(entry, et)
		}
		return r.Engine.Restore(entry, et, r.Database, engine.RestoreOptions{NoOwner: r.NoOwner})
	}, chk.version)
	var stdoutBytes uint64
	// Tool stdout (psql / mysql echo) is counted, never returned: it may
	// echo dump contents.
	rs.Stdout = func(b []byte) error { stdoutBytes += uint64(len(b)); return nil }
	rs.Log, rs.Stdin, rs.Timeout = out.Log, p.Reader(), remaining(ctx)
	if r.Dump == runnerpb.DumpKind_DUMP_KIND_GLOBALS {
		// psql runs under the runner's own \restrict key (D-138); the
		// dump's \restrict / \unrestrict lines would end the restore.
		rs.Stdin = engine.StripRestrictLines(rs.Stdin)
	}
	res, err := o.exec.Run(ctx, rs)
	if err != nil {
		return runFailure(ctx, p, err)
	}
	if err := p.Finish(ctx); err != nil {
		return streamStatus(err)
	}
	if res.PreambleFailed {
		out.Runner("the container could not set up its credential files")
	}
	result := &runnerpb.RestoreResult{
		BytesReceived:   st.Received(),
		ExitCode:        int32(res.ExitCode), //nolint:gosec // exit codes are small
		Warnings:        dedup(append(chk.warnings, warningCodes(ws)...)),
		DroppedLogLines: uint64(max(res.Log.Dropped, 0)),
	}
	code := res.ExitCode
	recvd := result.BytesReceived
	a.exitCode, a.bytes = &code, &recvd
	a.extra = append(a.extra, slog.Uint64("tool_stdout_bytes", stdoutBytes))
	if err := out.Err(); err != nil {
		return status.Error(codes.Aborted, "output consumer failed")
	}
	return out.Final(func() error {
		return stream.Send(&runnerpb.RestoreResponse{Msg: &runnerpb.RestoreResponse_Result{Result: result}})
	})
}
