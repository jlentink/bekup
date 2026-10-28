package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// tailBytes is how much of the end of a dump is kept for the trailer
// checks (MySQL/MariaDB "-- Dump completed", pg_dumpall's "dump complete").
const tailBytes = 1024

// truncatedMarker is what the executor appends to a capped log line.
const truncatedMarker = " [truncated]"

// pgDumpallTrailer is the comment pg_dumpall writes after a complete dump.
var pgDumpallTrailer = []byte("-- PostgreSQL database cluster dump complete")

// GlobalsTrailerOK reports whether the tail of a pg_dumpall output holds
// the completion comment.
func GlobalsTrailerOK(tail []byte) bool { return bytes.Contains(tail, pgDumpallTrailer) }

// errNoExecutor is returned while the runner has no Docker executor.
var errNoExecutor = status.Error(codes.Unavailable, "executor: docker is not ready")

// output relays one container run to a stream: stdout chunks (≤ 256 KiB,
// executor.DefaultChunkBytes), stderr lines and runner messages. The
// executor calls Stdout and Log from one goroutine; the mutex also covers
// runner messages sent from the handler and the final message (Final):
// the executor may return while its output reader is still inside a
// blocked Send (drain timeout), so every Send on the stream happens under
// mu, and once the handler is done (abandoned) the reader sends nothing
// more. gRPC forbids concurrent Send calls on one stream.
type output struct {
	mu        sync.Mutex
	sendChunk func([]byte) error
	sendLog   func(*runnerpb.LogLine) error
	now       func() time.Time
	tail      []byte
	bytes     uint64
	sendErr   error
	// buffering holds stdout back (first DumpGlobals attempt) up to
	// maxGlobalsBuffer; overflow streams the buffer and everything after.
	buffering bool
	buf       []byte
	overflow  bool
	// permission is set when stderr shows a privilege error on the role
	// catalog (DumpGlobals fallback).
	permission bool
	// abandoned: the handler sent its final message or returned; nothing
	// else may be sent.
	abandoned bool
}

// errAbandoned is returned to a late output reader after the handler
// finished the stream.
var errAbandoned = errors.New("runner: stream finished")

// Final sends the stream's last message under the output lock and marks
// the output abandoned, so a still running output reader can never send
// concurrently with it or after it.
func (w *output) Final(send func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.abandoned {
		return errAbandoned
	}
	w.abandoned = true
	return send()
}

// Send sends a control message (the stats) under the output lock.
func (w *output) Send(send func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.abandoned {
		return errAbandoned
	}
	if w.sendErr != nil {
		return w.sendErr
	}
	if err := send(); err != nil {
		w.sendErr = err
		return err
	}
	return nil
}

// Abandon marks the output finished (every handler return path): it waits
// for a Send in progress and blocks every later one.
func (w *output) Abandon() {
	w.mu.Lock()
	w.abandoned = true
	w.mu.Unlock()
}

func newOutput(now func() time.Time, chunk func([]byte) error, log func(*runnerpb.LogLine) error) *output {
	return &output{sendChunk: chunk, sendLog: log, now: now}
}

// Stdout is executor.RunSpec.Stdout.
func (w *output) Stdout(b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.abandoned {
		return errAbandoned
	}
	if w.sendErr != nil {
		return w.sendErr
	}
	w.keepTail(b)
	if w.buffering && !w.overflow {
		if len(w.buf)+len(b) <= maxGlobalsBuffer {
			w.buf = append(w.buf, b...)
			return nil
		}
		w.overflow = true
		if err := w.flushLocked(); err != nil {
			return err
		}
	}
	return w.chunkLocked(b)
}

func (w *output) chunkLocked(b []byte) error {
	if w.abandoned {
		return errAbandoned
	}
	for len(b) > 0 {
		n := min(len(b), runnerpb.MaxChunkBytes)
		if err := w.sendChunk(b[:n]); err != nil {
			w.sendErr = err
			return err
		}
		w.bytes += uint64(n)
		b = b[n:]
	}
	return nil
}

// flushLocked streams the buffered output and zeroes the buffer.
func (w *output) flushLocked() error {
	if len(w.buf) == 0 {
		return nil
	}
	err := w.chunkLocked(w.buf)
	crypto.Zero(w.buf)
	w.buf = w.buf[:0]
	return err
}

// Flush streams buffered output (end of a buffered attempt).
func (w *output) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffering = false
	return w.flushLocked()
}

// Discard drops buffered output and resets the tail (the attempt is
// repeated).
func (w *output) Discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	crypto.Zero(w.buf)
	w.buf, w.buffering, w.permission = nil, false, false
	w.tail = w.tail[:0]
}

func (w *output) keepTail(b []byte) {
	if len(b) >= tailBytes {
		w.tail = append(w.tail[:0], b[len(b)-tailBytes:]...)
		return
	}
	w.tail = append(w.tail, b...)
	if over := len(w.tail) - tailBytes; over > 0 {
		w.tail = append(w.tail[:0], w.tail[over:]...)
	}
}

// Tail returns a copy of the last tailBytes of stdout.
func (w *output) Tail() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.tail)
}

// Bytes returns the streamed byte count.
func (w *output) Bytes() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes
}

// Log is executor.RunSpec.Log (lines are sanitized, capped and redacted by
// the executor).
func (w *output) Log(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffering && isRolePermissionError(line) {
		w.permission = true
	}
	text, truncated := strings.CutSuffix(line, truncatedMarker)
	w.logLocked(runnerpb.LogStream_LOG_STREAM_STDERR, text, truncated)
}

// Runner sends a message of the runner itself.
func (w *output) Runner(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logLocked(runnerpb.LogStream_LOG_STREAM_RUNNER, text, false)
}

func (w *output) logLocked(s runnerpb.LogStream, text string, truncated bool) {
	if w.sendErr != nil || w.sendLog == nil || w.abandoned {
		return
	}
	if err := w.sendLog(&runnerpb.LogLine{Time: timestamppb.New(w.now()), Stream: s, Text: text, Truncated: truncated}); err != nil {
		w.sendErr = err
	}
}

// Err returns the first send error.
func (w *output) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sendErr
}

// isRolePermissionError matches pg_dumpall's failure to read the role
// catalog as a non-superuser. The table name is not translated, so
// localized server messages match too.
func isRolePermissionError(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "pg_authid") || strings.Contains(l, "permission denied") ||
		strings.Contains(l, "must be superuser")
}

// dump implements Dump.
func (o *operations) dump(ctx context.Context, req *runnerpb.DumpRequest, send func(*runnerpb.DumpResponse) error) (err error) {
	a := newAudit("Dump", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	d, err := o.val.Dump(ctx, req)
	if err != nil {
		return err
	}
	a.target(d.Target)
	a.database = d.Database
	if o.exec == nil {
		return errNoExecutor
	}
	chk, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{needDB: []string{d.Database}, nonInnoDB: d.Database})
	if err != nil {
		o.logCause(ctx, "Dump", err)
		return targetStatus(err)
	}
	cw, err := compatWarnings(d.Entry, chk.version)
	if err != nil {
		return err
	}
	out := newOutput(o.now,
		func(b []byte) error {
			return send(&runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Chunk{Chunk: &runnerpb.DataChunk{Data: b}}})
		},
		func(l *runnerpb.LogLine) error {
			return send(&runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Log{Log: l}})
		})
	defer out.Abandon()
	// Statistics (task 3.10): captured before the container starts; the
	// PostgreSQL snapshot transaction stays open until the dump finished.
	var sc *statsCapture
	if d.Stats != nil {
		sc = o.captureStats(ctx, d, req.GetTarget())
		defer sc.close(ctx)
		if sc == nil {
			cw = append(cw, warnStatsUnavailable)
			out.Runner("source statistics could not be captured; the dump runs without them")
		} else {
			a.extra = append(a.extra, statsAudit(sc.msg)...)
			if err := out.Send(func() error {
				return send(&runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Stats{Stats: sc.msg}})
			}); err != nil {
				return status.Error(codes.Aborted, executor.ErrOutput.Error())
			}
		}
	}
	snapshot := ""
	if sc != nil {
		snapshot = sc.snapshot
	}
	rs := o.runSpec(req.GetCtx().GetRunId(), d.Entry, d.Target, req.GetTarget(), func(et engine.Target) (engine.Command, error) {
		return d.Engine.DumpSnapshot(d.Entry, et, d.Database, snapshot)
	}, chk.version)
	rs.Stdout, rs.Log, rs.Timeout = out.Stdout, out.Log, remaining(ctx)
	res, err := o.exec.Run(ctx, rs)
	sc.close(ctx)
	if err != nil {
		return targetStatus(err)
	}
	if res.PreambleFailed {
		out.Runner("the container could not set up its credential files")
	}
	result := &runnerpb.DumpResult{
		Bytes:           out.Bytes(),
		ExitCode:        int32(res.ExitCode), //nolint:gosec // exit codes are small
		Warnings:        dedup(append(chk.warnings, cw...)),
		DroppedLogLines: uint64(max(res.Log.Dropped, 0)),
	}
	// MySQL/MariaDB: exit 0 + the "-- Dump completed" trailer. PostgreSQL
	// custom-format archives are checked by VerifyDump (pg_restore --list).
	if d.Kind != engine.KindPostgres {
		result.Verified = res.ExitCode == 0 && engine.DumpTrailerOK(out.Tail())
	}
	code := int(result.ExitCode)
	a.exitCode, a.bytes = &code, &result.Bytes
	if err := out.Err(); err != nil {
		return status.Error(codes.Aborted, executor.ErrOutput.Error())
	}
	return out.Final(func() error {
		return send(&runnerpb.DumpResponse{Msg: &runnerpb.DumpResponse_Result{Result: result}})
	})
}

// dumpGlobals implements DumpGlobals (PostgreSQL): pg_dumpall
// --globals-only; when it fails reading the role catalog (non-superuser,
// managed services) it is retried once with --no-role-passwords. The first
// attempt's output is held back (bounded) so the retry never follows
// streamed data.
func (o *operations) dumpGlobals(ctx context.Context, req *runnerpb.DumpGlobalsRequest, send func(*runnerpb.DumpGlobalsResponse) error) (err error) {
	a := newAudit("DumpGlobals", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	t, err := o.val.DumpGlobals(ctx, req)
	if err != nil {
		return err
	}
	st, err := validate.StatsOptions(req.GetStats())
	if err != nil {
		return err
	}
	a.target(t)
	if o.exec == nil {
		return errNoExecutor
	}
	chk, err := o.precheck(ctx, t, req.GetTarget(), checkOpts{})
	if err != nil {
		o.logCause(ctx, "DumpGlobals", err)
		return targetStatus(err)
	}
	cw, err := compatWarnings(t.Entry, chk.version)
	if err != nil {
		return err
	}
	out := newOutput(o.now,
		func(b []byte) error {
			return send(&runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Chunk{Chunk: &runnerpb.DataChunk{Data: b}}})
		},
		func(l *runnerpb.LogLine) error {
			return send(&runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Log{Log: l}})
		})
	defer out.Abandon()
	if st != nil {
		if gs := o.captureGlobalStats(ctx, t, req.GetTarget(), st); gs == nil {
			cw = append(cw, warnStatsUnavailable)
			out.Runner("source statistics could not be captured; the dump runs without them")
		} else if err := out.Send(func() error {
			return send(&runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Stats{Stats: gs}})
		}); err != nil {
			return status.Error(codes.Aborted, executor.ErrOutput.Error())
		}
	}
	attempt := func(fallback bool) (executor.Result, error) {
		rs := o.runSpec(req.GetCtx().GetRunId(), t.Entry, t, req.GetTarget(), func(et engine.Target) (engine.Command, error) {
			primary, fb, err := t.Engine.DumpGlobals(t.Entry, et)
			if fallback {
				return fb, err
			}
			return primary, err
		}, chk.version)
		rs.Stdout, rs.Log, rs.Timeout = out.Stdout, out.Log, remaining(ctx)
		return o.exec.Run(ctx, rs)
	}

	out.buffering = true
	res, err := attempt(false)
	if err != nil {
		out.Discard()
		return targetStatus(err)
	}
	rolesNoPw := false
	out.mu.Lock()
	retry := res.ExitCode != 0 && !out.overflow && out.permission && out.sendErr == nil
	out.mu.Unlock()
	if retry {
		out.Discard()
		out.Runner("pg_dumpall cannot read role passwords with this user; retrying once with --no-role-passwords")
		o.log.InfoContext(ctx, "runner globals fallback",
			slog.String("run_id", req.GetCtx().GetRunId()), slog.String("reason", "role catalog not readable"))
		rolesNoPw = true
		if res, err = attempt(true); err != nil {
			return targetStatus(err)
		}
	} else if err := out.Flush(); err != nil {
		return status.Error(codes.Aborted, executor.ErrOutput.Error())
	}
	warns := append(slices.Clone(chk.warnings), cw...)
	if rolesNoPw {
		warns = append(warns, warnRolesNoPasswords)
	}
	result := &runnerpb.DumpResult{
		Bytes:                 out.Bytes(),
		ExitCode:              int32(res.ExitCode), //nolint:gosec // exit codes are small
		Verified:              res.ExitCode == 0 && GlobalsTrailerOK(out.Tail()),
		Warnings:              dedup(warns),
		DroppedLogLines:       uint64(max(res.Log.Dropped, 0)),
		RolesWithoutPasswords: rolesNoPw,
	}
	code := int(result.ExitCode)
	a.exitCode, a.bytes = &code, &result.Bytes
	a.extra = append(a.extra, slog.Bool("roles_without_passwords", rolesNoPw))
	if err := out.Err(); err != nil {
		return status.Error(codes.Aborted, executor.ErrOutput.Error())
	}
	return out.Final(func() error {
		return send(&runnerpb.DumpGlobalsResponse{Msg: &runnerpb.DumpGlobalsResponse_Result{Result: result}})
	})
}
