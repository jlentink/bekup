package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Runner calls shared by the engine restorers (tasks 4.2, 4.3). Every call
// builds a fresh runner target through EngineRequest.Target and releases
// (zeroes) it as soon as the request is on the wire.

// Defaults of the runner calls.
const (
	// streamChunk is the payload of one restore chunk (≤ runnerpb.MaxChunkBytes).
	streamChunk = 256 << 10
	// cleanupTimeout bounds a DropTempDatabase that runs after the restore's
	// context ended (cancel, timeout).
	cleanupTimeout = 2 * time.Minute
)

// errStreamProtocol: the runner sent something the protocol does not
// allow (a second result).
var errStreamProtocol = fmt.Errorf("%w: the runner stream violated the protocol", ErrRestoreIncomplete)

// runnerLineSink is implemented by the worker's sink: runner lines keep
// their stream (stdout/stderr/runner) in restore_logs.
type runnerLineSink interface {
	runnerLine(l *runnerpb.LogLine)
}

// StreamOptions describe one Restore call.
type StreamOptions struct {
	// Database to write ("" for globals).
	Database string
	Kind     runnerpb.DumpKind
	// NoOwner: pg_restore --no-owner (PostgreSQL database restores only).
	NoOwner bool
	// NoPrivileges: pg_restore --no-privileges (test-restore scratch
	// databases only; the runner refuses it elsewhere).
	NoPrivileges bool
}

// RunRestoreStream runs the runner's Restore with src as the dump: the
// header (target, database, source version key, exact size), then the
// chunks. The last chunk is held back until src ended cleanly, so a dump
// whose final verification fails (Staged.Open returns the error instead of
// io.EOF) never reaches the runner in full and the tool is killed instead
// of committing it. Runner log lines go to logs. It returns the runner's
// result for an exit code 0 with exactly TotalBytes delivered; otherwise
// an error matching ErrRestoreFailed / ErrRestoreIncomplete, the runner's
// gRPC status, or src's error.
func RunRestoreStream(ctx context.Context, req *EngineRequest, o StreamOptions, src io.Reader, logs LogSink) (*runnerpb.RestoreResult, error) {
	if req.TotalBytes == 0 {
		return nil, fmt.Errorf("%w: the dump size is unknown", ErrUnsupported)
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	target, release, err := req.Target(sctx)
	if err != nil {
		return nil, err
	}
	stream, err := req.Runner.Restore(sctx)
	if err != nil {
		release()
		return nil, err
	}
	err = stream.Send(&runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{
		Ctx: req.RunContext(), Target: target, Database: o.Database, SourceVersionKey: req.SourceVersion,
		Kind: o.Kind, NoOwner: o.NoOwner, NoPrivileges: o.NoPrivileges, TotalBytes: req.TotalBytes,
	}}})
	// The header is serialized by Send: the credentials can go.
	release()
	recvDone := make(chan recvResult, 1)
	go func() { recvDone <- receive(stream, logs) }()
	if err == nil {
		err = sendChunks(stream, src, req.TotalBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		cancel()
		<-recvDone
		return nil, err
	}
	// io.EOF from Send: the runner ended the call before it took the
	// whole dump; its status or result is in the receive and is checked
	// like any other (a result of an early end can never pass the byte
	// count).
	return checkResult(<-recvDone, req.TotalBytes, logs)
}

// checkResult accepts only a stream that ended with a result of exit code
// 0 after exactly total bytes.
func checkResult(rr recvResult, total uint64, logs LogSink) (*runnerpb.RestoreResult, error) {
	if f := rr.failure(); f != nil {
		return nil, f
	}
	res := rr.result
	if n := res.GetDroppedLogLines(); n > 0 {
		logs.Line(LogWarn, fmt.Sprintf("the runner dropped %d tool log lines (runner limits)", n))
	}
	if res.GetExitCode() != 0 {
		return res, fmt.Errorf("%w: exit code %d", ErrRestoreFailed, res.GetExitCode())
	}
	if res.GetBytesReceived() != total {
		return res, fmt.Errorf("%w: the runner received %d of %d bytes", ErrRestoreIncomplete, res.GetBytesReceived(), total)
	}
	return res, nil
}

// sendChunks streams src (exactly total bytes) holding back the last
// chunk until src returned io.EOF, then closes the send side. io.EOF from
// Send means the runner ended the call.
func sendChunks(stream interface {
	Send(*runnerpb.RestoreRequest) error
	CloseSend() error
}, src io.Reader, total uint64,
) error {
	bufs := [2][]byte{make([]byte, streamChunk), make([]byte, streamChunk)}
	var pending []byte
	var sent uint64
	cur := 0
	send := func(b []byte) error {
		if sent+uint64(len(b)) > total {
			return fmt.Errorf("%w: the dump is larger than recorded", ErrRestoreIncomplete)
		}
		sent += uint64(len(b))
		return stream.Send(&runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: b}}})
	}
	for {
		n, rerr := io.ReadFull(src, bufs[cur])
		if n > 0 {
			if pending != nil {
				if err := send(pending); err != nil {
					return err
				}
			}
			pending = bufs[cur][:n]
			cur ^= 1
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if pending == nil || sent+uint64(len(pending)) != total {
		return fmt.Errorf("%w: the dump size differs from the record", ErrRestoreIncomplete)
	}
	if err := send(pending); err != nil {
		return err
	}
	return stream.CloseSend()
}

// recvResult is what the receive loop saw.
type recvResult struct {
	result *runnerpb.RestoreResult
	err    error
}

func (r recvResult) failure() error {
	switch {
	case r.err != nil:
		return r.err
	case r.result == nil:
		return fmt.Errorf("%w: the runner stream ended without a result", ErrRestoreIncomplete)
	default:
		return nil
	}
}

// receive reads the runner's responses until the stream ends.
func receive(stream interface {
	Recv() (*runnerpb.RestoreResponse, error)
}, logs LogSink,
) recvResult {
	var rr recvResult
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return rr
		}
		if err != nil {
			rr.err = err
			return rr
		}
		switch {
		case m.GetLog() != nil:
			if rr.result != nil {
				rr.err = errStreamProtocol
				return rr
			}
			runnerLog(logs, m.GetLog())
		case m.GetResult() != nil:
			if rr.result != nil {
				rr.err = errStreamProtocol
				return rr
			}
			rr.result = m.GetResult()
		}
	}
}

// runnerLog forwards one runner log line.
func runnerLog(logs LogSink, l *runnerpb.LogLine) {
	if rs, ok := logs.(runnerLineSink); ok {
		rs.runnerLine(l)
		return
	}
	text := l.GetText()
	if l.GetTruncated() {
		text += " …[truncated by the runner]"
	}
	logs.Line(LogInfo, text)
}

// CreateDatabase creates an empty database on the target through the
// runner (it must not exist yet).
func CreateDatabase(ctx context.Context, req *EngineRequest, name string) error {
	target, release, err := req.Target(ctx)
	if err != nil {
		return err
	}
	r := &runnerpb.CreateDatabaseRequest{Ctx: req.RunContext(), Target: target, Database: name}
	_, err = req.Runner.CreateDatabase(ctx, r)
	release()
	return err
}

// RenameSwap replaces req.TargetDB with its restored temporary database
// (PostgreSQL; the runner derives the names from TempID and keeps the
// replaced database as <db>__bekup_old_<temp id>).
func RenameSwap(ctx context.Context, req *EngineRequest) error {
	target, release, err := req.Target(ctx)
	if err != nil {
		return err
	}
	r := &runnerpb.RenameSwapDatabaseRequest{Ctx: req.RunContext(), Target: target, Database: req.TargetDB, TempId: req.TempID()}
	_, err = req.Runner.RenameSwapDatabase(ctx, r)
	release()
	return err
}

// DropTempDatabase drops the restore's derived temporary database of the
// given role (restore staging or replaced old database). It runs on its
// own bounded context when ctx already ended (cancel, timeout), so a
// canceled restore still cleans up. It reports whether the database
// existed.
func DropTempDatabase(ctx context.Context, req *EngineRequest, role runnerpb.TempDatabaseRole) (bool, error) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	target, release, err := req.Target(dctx)
	if err != nil {
		return false, err
	}
	r := &runnerpb.DropTempDatabaseRequest{Ctx: req.RunContext(), Target: target, Database: req.TargetDB, TempId: req.TempID(), Role: role}
	resp, err := req.Runner.DropTempDatabase(dctx, r)
	release()
	if err != nil {
		return false, err
	}
	return resp.GetExisted(), nil
}

// Neutralized is the outcome of NeutralizeRestored (D-171).
type Neutralized struct {
	// Database is the hardened database.
	Database string
	// Count is the number of SECURITY DEFINER functions and procedures
	// switched to SECURITY INVOKER.
	Count int
	// Functions are the first of them (≤ 50 regprocedure signatures).
	Functions []string
}

// Bounds of a runner's NeutralizeRestoredDatabase answer.
const (
	maxNeutralizedFunctions = 50
	maxNeutralizedSignature = 256
)

// NeutralizeRestored hardens a freshly restored PostgreSQL database
// through the runner (D-171): every SECURITY DEFINER function and
// procedure becomes SECURITY INVOKER and PUBLIC loses CONNECT and
// TEMPORARY on it. kind selects the database: the overwrite's staging
// database (derived from req.TargetDB and the restore's temp id), the new
// database req.TargetDB, or the test-restore scratch database derived from
// the restore's temp id (req.TargetDB must be that scratch name). Call it
// after pg_restore succeeded and before the swap or the success is
// recorded; on an error the restore must fail (the target is untouched).
func NeutralizeRestored(ctx context.Context, req *EngineRequest, kind runnerpb.NeutralizeKind) (Neutralized, error) {
	r := &runnerpb.NeutralizeRestoredDatabaseRequest{Ctx: req.RunContext(), Kind: kind}
	switch kind {
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING:
		r.Database, r.TempId = req.TargetDB, req.TempID()
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE:
		r.Database = req.TargetDB
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH:
		r.TempId = req.TempID()
	default:
		return Neutralized{}, fmt.Errorf("%w: neutralize kind", ErrUnsupported)
	}
	target, release, err := req.Target(ctx)
	if err != nil {
		return Neutralized{}, err
	}
	r.Target = target
	resp, err := req.Runner.NeutralizeRestoredDatabase(ctx, r)
	release()
	if err != nil {
		return Neutralized{}, fmt.Errorf("%w: %w", ErrNeutralizeFailed, err)
	}
	out := Neutralized{Database: resp.GetDatabase(), Count: int(resp.GetNeutralized())}
	for _, f := range resp.GetFunctions() {
		if len(out.Functions) == maxNeutralizedFunctions {
			break
		}
		out.Functions = append(out.Functions, cleanSignature(f))
	}
	if out.Count < len(out.Functions) {
		out.Count = len(out.Functions)
	}
	return out, nil
}

// cleanSignature bounds a function signature from the runner and replaces
// control characters (it goes into logs and the audit log).
func cleanSignature(s string) string {
	b := make([]rune, 0, min(len(s), maxNeutralizedSignature))
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			r = '?'
		}
		n += utf8.RuneLen(r)
		if n > maxNeutralizedSignature {
			break
		}
		b = append(b, r)
	}
	return string(b)
}
