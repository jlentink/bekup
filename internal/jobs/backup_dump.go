package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

var (
	errDumpIncomplete = errors.New("jobs: the dump did not complete")
	errPipelineDone   = errors.New("jobs: the pipeline stopped reading")
	errDumpProtocol   = errors.New("jobs: the runner stream violated the protocol")
)

// dumpMsg is a DumpResponse or DumpGlobalsResponse.
type dumpMsg interface {
	GetChunk() *runnerpb.DataChunk
	GetLog() *runnerpb.LogLine
	GetResult() *runnerpb.DumpResult
	GetStats() *runnerpb.DumpStats
}

// relayResult is what the relay saw of the runner stream.
type relayResult struct {
	result   *runnerpb.DumpResult
	stats    *runnerpb.DumpStats
	bytes    uint64
	err      error // runner stream error
	writeErr error // the pipeline stopped reading
}

// complete reports a dump that ended with exit 0 and exactly the announced
// bytes.
func (r *relayResult) complete() bool {
	return r.err == nil && r.writeErr == nil && r.result != nil && r.result.GetExitCode() == 0 && r.result.GetBytes() == r.bytes
}

// verifier tees the raw dump into a streamed VerifyDump (header
// total_bytes 0, trailer with the total).
type verifier struct {
	s      grpc.ClientStreamingClient[runnerpb.VerifyDumpRequest, runnerpb.VerifyDumpResponse]
	broken bool
	err    error
	resp   *runnerpb.VerifyDumpResponse
}

func (e *runExec) startVerify(ctx context.Context) *verifier {
	v := &verifier{}
	s, err := e.w.o.Runner.VerifyDump(ctx)
	if err != nil {
		v.broken, v.err = true, err
		return v
	}
	v.s = s
	eng, err := runnerEngine(e.job.Engine)
	if err != nil {
		v.broken = true
		return v
	}
	if err := s.Send(&runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Header{Header: &runnerpb.VerifyDumpHeader{
		Ctx: e.runContext(), Engine: eng, VersionKey: e.job.VersionKey, Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE,
	}}}); err != nil {
		v.broken = true
	}
	return v
}

func (v *verifier) send(data []byte) {
	if v.broken {
		return
	}
	if err := v.s.Send(&runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: data}}}); err != nil {
		// io.EOF: the runner ended the call; CloseAndRecv has the status.
		v.broken = true
	}
}

// finish sends the trailer and waits for the verdict.
func (v *verifier) finish(total uint64) {
	if v.s == nil {
		return
	}
	if !v.broken {
		if err := v.s.Send(&runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Trailer{Trailer: &runnerpb.VerifyDumpTrailer{TotalBytes: total}}}); err != nil {
			v.broken = true
		}
	}
	v.resp, v.err = v.s.CloseAndRecv()
}

func runnerEngine(k engine.Kind) (runnerpb.Engine, error) {
	switch k {
	case engine.KindPostgres:
		return runnerpb.Engine_ENGINE_POSTGRES, nil
	case engine.KindMySQL:
		return runnerpb.Engine_ENGINE_MYSQL, nil
	case engine.KindMariaDB:
		return runnerpb.Engine_ENGINE_MARIADB, nil
	default:
		return runnerpb.Engine_ENGINE_UNSPECIFIED, fmt.Errorf("jobs: unknown engine %q", k)
	}
}

// runnerLog stores one runner log line.
func (e *runExec) runnerLog(l *runnerpb.LogLine) {
	stream := runstate.StreamRunner
	switch l.GetStream() {
	case runnerpb.LogStream_LOG_STREAM_STDOUT:
		stream = runstate.StreamStdout
	case runnerpb.LogStream_LOG_STREAM_STDERR:
		stream = runstate.StreamStderr
	default:
	}
	text := l.GetText()
	if l.GetTruncated() {
		text += " …[truncated by the runner]"
	}
	e.logs.Log(runstate.LevelInfo, stream, text)
}

// relay reads the runner stream: chunks go to the pipeline (pw) and, when
// verifying, to the verifier; log lines to the run log. pw is closed
// cleanly only after a complete result, so the pipeline never finalizes a
// dump the runner did not finish.
func (e *runExec) relay(recv func() (dumpMsg, error), pw *io.PipeWriter, ver *verifier, stop context.CancelFunc) relayResult {
	var rr relayResult
loop:
	for {
		m, err := recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			rr.err = err
			break
		}
		switch {
		case m.GetChunk() != nil:
			data := m.GetChunk().GetData()
			if rr.result != nil {
				rr.err = errDumpProtocol
				stop()
				break loop
			}
			rr.bytes += uint64(len(data))
			if _, werr := pw.Write(data); werr != nil {
				rr.writeErr = werr
				stop()
				break loop
			}
			if ver != nil {
				ver.send(data)
			}
		case m.GetLog() != nil:
			e.runnerLog(m.GetLog())
		case m.GetStats() != nil:
			// At most once and before the first chunk (task 3.10).
			if rr.stats != nil || rr.bytes > 0 || rr.result != nil {
				rr.err = errDumpProtocol
				stop()
				break loop
			}
			rr.stats = m.GetStats()
		case m.GetResult() != nil:
			if rr.result != nil {
				rr.err = errDumpProtocol
				stop()
				break loop
			}
			rr.result = m.GetResult()
		}
	}
	if rr.complete() {
		_ = pw.Close() //nolint:gosec // G104: closing a pipe writer never fails
	} else {
		_ = pw.CloseWithError(errDumpIncomplete)
	}
	return rr
}

// dumpItem dumps one database (or the globals), streams it through the
// pipeline to the live destinations while the raw stream is verified, and
// records the item and its artifacts. It returns a failure that must stop
// the run (target-level problems that every further dump would hit).
func (e *runExec) dumpItem(ctx context.Context, kind pipeline.EntryKind, db string, pos int) *runstate.Failure {
	it := &itemState{kind: kind, db: db, okDests: map[string]uuid.UUID{}}
	e.items = append(e.items, it)
	ikind := sqlc.RunItemKindDatabase
	if kind == pipeline.KindGlobals {
		ikind = sqlc.RunItemKindGlobals
	}
	item, err := e.o.Store.AddItem(ctx, e.run.Ref, ikind, db, pos)
	if err != nil {
		e.log.ErrorContext(ctx, "recording a run item failed", slog.Any("error", err))
		it.status, it.failure = runstate.ItemFailed, fail(CodeInternal, "the item could not be recorded")
		return nil
	}
	defer e.finishItem(ctx, item, it)
	e.logs.Logf(runstate.LevelInfo, "%s: dump started", it.label())

	format := pipeline.FormatSQL
	if kind == pipeline.KindDatabase && e.job.Engine == engine.KindPostgres {
		format = pipeline.FormatCustom
	}
	fileName, err := pipeline.ArtifactFileName(kind, db, format, e.enc.Mode)
	if err != nil {
		it.status, it.failure = runstate.ItemFailed, fail(CodeInternal, "no artifact name could be derived")
		return nil
	}
	key, err := pipeline.ArtifactKey("", e.job.Slug, e.ts, fileName)
	if err != nil {
		it.status, it.failure = runstate.ItemFailed, fail(CodeInternal, "no artifact key could be derived")
		return nil
	}
	targets := e.targets(key)
	if len(targets) == 0 {
		it.status, it.failure = runstate.ItemFailed, fail(CodeAllDestinations, "no destination is left in this run")
		return nil
	}

	session, err := e.o.Stats.Begin(ctx, StatsInput{
		RunID: e.run.RunID, JobID: e.job.ID, ItemID: item.ID, Engine: e.job.Engine, VersionKey: e.job.VersionKey, Kind: kind, Database: db,
		Deep: e.job.Verification == sqlc.JobVerificationDeep,
	})
	if err != nil {
		e.log.WarnContext(ctx, "capturing source statistics failed", slog.Any("error", err))
		it.warnings = append(it.warnings, runstate.Warning{Code: "stats_unavailable", Message: "source statistics could not be captured", Database: db})
		session = noStats{}
	}

	rr, pres, ver, perr := e.stream(ctx, kind, db, targets, session)
	dumpOK := rr.complete()
	verified := false
	if dumpOK && perr == nil {
		switch {
		case ver != nil:
			verified = ver.err == nil && ver.resp.GetVerified()
			for _, l := range ver.resp.GetLogs() {
				e.runnerLog(l)
			}
		default:
			verified = rr.result.GetVerified()
		}
	}
	if rr.result != nil {
		for _, w := range rr.result.GetWarnings() {
			// The stats list the non-transactional tables by name; the
			// runner's summary code would repeat them.
			if w == "non_innodb_tables" && hasStatsWarning(rr.stats, "non_transactional_tables") {
				continue
			}
			it.warnings = append(it.warnings, runnerWarning(w, db))
		}
		if rr.result.GetRolesWithoutPasswords() {
			e.logs.Logf(runstate.LevelWarn, "the globals were dumped without role passwords (--no-role-passwords fallback)")
		}
		if n := rr.result.GetDroppedLogLines(); n > 0 {
			e.logs.Logf(runstate.LevelWarn, "the runner dropped %d tool log lines (runner limits)", n)
		}
	}
	if st, err := session.End(ctx, DumpOutcome{OK: dumpOK && verified, Stats: rr.stats, Result: rr.result}); err != nil {
		e.log.WarnContext(ctx, "finishing source statistics failed", slog.Any("error", err))
	} else {
		it.stats = st.Stats
		it.warnings = append(it.warnings, st.Warnings...)
	}

	// Destination outcomes (only if the pipeline ran).
	for i := range pres.Destinations {
		d := &pres.Destinations[i]
		ds := e.dest(d.Name)
		if ds == nil {
			continue
		}
		if d.Outcome == pipeline.OutcomeOK {
			e.recordArtifact(ctx, item, it, ds, d, kind, verified)
			continue
		}
		if d.Reason == pipeline.ReasonAborted {
			continue // the dump or the run failed: not the destination's fault
		}
		ds.lost = true
		it.warnings = append(it.warnings, destinationWarning(db, ds.id.String(), string(d.Reason), d.Code))
		e.logs.Logf(runstate.LevelWarn, "%s: destination %s dropped (%s: %s)", it.label(), ds.id, d.Reason, d.Code.Message())
		e.log.WarnContext(ctx, "destination dropped", slog.String("destination_id", ds.id.String()), slog.String("reason", string(d.Reason)),
			slog.String("code", string(d.Code)), slog.Any("error", d.Err))
		e.reportDestination(ctx, ds.id, d.Code)
	}

	switch {
	case !dumpOK:
		it.status, it.failure = runstate.ItemFailed, e.dumpFailure(ctx, rr, kind)
		if f := stopFailure(ctx); f != nil && f.Code == CodeCanceled {
			it.status = runstate.ItemCanceled
		}
		e.logs.Logf(runstate.LevelError, "%s: %s", it.label(), it.failure.Message)
		if fatalForRun(it.failure) {
			return it.failure
		}
		return nil
	case perr != nil:
		it.status = runstate.ItemFailed
		switch {
		case stopFailure(ctx) != nil:
			it.failure = stopFailure(ctx)
			if it.failure.Code == CodeCanceled {
				it.status = runstate.ItemCanceled
			}
		case errors.Is(perr, pipeline.ErrAllDestinationsFailed):
			it.failure = fail(CodeAllDestinations, "every destination failed")
		default:
			e.log.ErrorContext(ctx, "the backup pipeline failed", slog.Any("error", perr))
			it.failure = fail(CodeInternal, "compression or encryption failed")
		}
		e.logs.Logf(runstate.LevelError, "%s: %s", it.label(), it.failure.Message)
		return nil
	case !verified:
		it.status = runstate.ItemFailed
		it.failure = fail(CodeVerifyFailed, "the dump failed verification; its stored objects are kept but flagged unverified and are not in the manifest")
		if f := stopFailure(ctx); f != nil {
			it.failure = f
			if f.Code == CodeCanceled {
				it.status = runstate.ItemCanceled
			}
		}
		if ver != nil && ver.err != nil {
			e.log.WarnContext(ctx, "verify call failed", slog.Any("error", ver.err))
		}
		e.logs.Logf(runstate.LevelError, "%s: %s", it.label(), it.failure.Message)
		return nil
	}
	it.raw, it.stored = pres.RawSize, pres.StoredSize
	it.rawSum, it.sum = pres.RawSHA256, pres.SHA256
	entry := pres.Entry(kind, db, fileName)
	it.entry = &entry
	it.status = runstate.ItemSuccess
	for _, d := range e.job.Destinations {
		if _, ok := it.okDests[d.ID.String()]; !ok {
			it.status = runstate.ItemPartial
		}
	}
	e.logs.Logf(runstate.LevelInfo, "%s: stored and verified (%d bytes raw, %d bytes stored, %d destination(s))",
		it.label(), pres.RawSize, pres.StoredSize, len(it.okDests))
	return nil
}

// stream runs Dump (or DumpGlobals), the verifier and the pipeline.
func (e *runExec) stream(ctx context.Context, kind pipeline.EntryKind, db string, targets []pipeline.Target, session StatsSession) (relayResult, pipeline.Result, *verifier, error) {
	dctx, dcancel := context.WithCancel(ctx)
	defer dcancel()
	target, release, err := e.o.Targets.Target(dctx, e.job.ConnectionID)
	if err != nil {
		return relayResult{err: err}, pipeline.Result{}, nil, nil
	}
	e.register(target)
	var recv func() (dumpMsg, error)
	op := runOpDump
	if kind == pipeline.KindGlobals {
		op = runOpDumpGlobals
		req := &runnerpb.DumpGlobalsRequest{Ctx: e.runContext(), Target: target}
		session.PrepareDump(nil, req)
		s, err := e.o.Runner.DumpGlobals(dctx, req)
		release()
		if err != nil {
			e.securityEvent(ctx, op, err)
			return relayResult{err: err}, pipeline.Result{}, nil, nil
		}
		recv = func() (dumpMsg, error) { return s.Recv() }
	} else {
		req := &runnerpb.DumpRequest{Ctx: e.runContext(), Target: target, Database: db}
		session.PrepareDump(req, nil)
		s, err := e.o.Runner.Dump(dctx, req)
		release()
		if err != nil {
			e.securityEvent(ctx, op, err)
			return relayResult{err: err}, pipeline.Result{}, nil, nil
		}
		recv = func() (dumpMsg, error) { return s.Recv() }
	}

	// PostgreSQL archives are verified with pg_restore in the runner's
	// offline container, fed with the raw stream while it is stored;
	// SQL dumps (MySQL/MariaDB, globals) carry the runner's trailer check
	// in the dump result.
	var ver *verifier
	if kind == pipeline.KindDatabase && e.job.Engine == engine.KindPostgres {
		ver = e.startVerify(dctx)
	}
	pr, pw := io.Pipe()
	done := make(chan relayResult, 1)
	go func() { done <- e.relay(recv, pw, ver, dcancel) }()
	spec := pipeline.Spec{
		Encryption: e.enc, Targets: targets,
		CompressionLevel: e.o.Pipeline.CompressionLevel, ChunkSize: e.o.Pipeline.ChunkSize,
		BufferChunks: e.o.Pipeline.BufferChunks, LagTimeout: e.o.Pipeline.LagTimeout,
	}
	pres, perr := pipeline.Run(dctx, pr, spec)
	// Unblock the relay if the pipeline stopped early, and stop the dump.
	_ = pr.CloseWithError(errPipelineDone) //nolint:gosec // G104: closing a pipe reader never fails
	if perr != nil {
		dcancel()
	}
	rr := <-done
	if rr.err != nil {
		e.securityEvent(ctx, op, rr.err)
	}
	if ver != nil {
		if rr.complete() && perr == nil {
			ver.finish(rr.bytes)
		} else {
			dcancel()
			ver.finish(rr.bytes)
		}
	}
	return rr, pres, ver, perr
}

// dumpFailure explains an incomplete dump.
func (e *runExec) dumpFailure(ctx context.Context, rr relayResult, kind pipeline.EntryKind) *runstate.Failure {
	if f := stopFailure(ctx); f != nil {
		return f
	}
	if rr.err != nil {
		if errors.Is(rr.err, errDumpProtocol) {
			return fail(CodeDumpIncomplete, "the runner stream was malformed")
		}
		if _, ok := status.FromError(rr.err); ok {
			return runnerFailure(ctx, rr.err)
		}
		return e.targetFailure(ctx, rr.err)
	}
	if rr.writeErr != nil {
		return fail(CodeAllDestinations, "every destination failed")
	}
	if rr.result == nil {
		return fail(CodeDumpIncomplete, "the dump ended without a result")
	}
	if rr.result.GetExitCode() != 0 {
		what := "pg_dump"
		switch {
		case kind == pipeline.KindGlobals:
			what = "pg_dumpall"
		case e.job.Engine != engine.KindPostgres:
			what = "the dump tool"
		}
		return fail(CodeDumpFailed, "%s exited with code %d (see the run log)", what, rr.result.GetExitCode())
	}
	return fail(CodeDumpIncomplete, "the dump size does not match the runner's count")
}

// fatalForRun reports target-level failures every further dump would hit.
func fatalForRun(f *runstate.Failure) bool {
	switch f.Code {
	case "target_unreachable", "target_auth_failed", "target_tls_failed", "tls_pin_mismatch",
		"ssh_host_key_mismatch", "ssh_auth_failed", "target_unsupported_server",
		CodeRunnerUnavailable, CodeTargetNotAllowed, "connection_not_ready", CodeCanceled, CodeTimeout,
		CodeInterrupted, CodeSlotLost:
		return true
	}
	return false
}

// recordArtifact stores the artifact row of a finalized object.
func (e *runExec) recordArtifact(ctx context.Context, item runstate.Item, it *itemState, ds *destState, d *pipeline.DestinationResult, kind pipeline.EntryKind, verified bool) {
	akind := sqlc.ArtifactKindDatabase
	if kind == pipeline.KindGlobals {
		akind = sqlc.ArtifactKindGlobals
	}
	id := item.ID
	a := runstate.Artifact{
		ItemID: &id, DestinationID: ds.id, Kind: akind, Key: d.Key.String(), Size: d.Info.Size, SHA256: d.Info.SHA256,
		VersionID: d.Info.VersionID, EncryptionMode: string(e.enc.Mode),
		PassphraseVersion: e.enc.PassphraseVersion, RecipientVersion: e.enc.RecipientVersion,
		Verified: verified, LockUntil: d.Info.RetainUntil,
	}
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	artID, err := e.o.Store.AddArtifact(fctx, e.run.Ref, a)
	if err != nil {
		e.log.ErrorContext(ctx, "recording an artifact failed", slog.String("destination_id", ds.id.String()), slog.Any("error", err))
		it.warnings = append(it.warnings, runstate.Warning{Code: CodeInternal, Message: "a stored object could not be recorded", Database: it.db, DestinationID: ds.id.String()})
		return
	}
	if verified {
		it.okDests[ds.id.String()] = artID
	}
}

// finishItem stores the item outcome.
func (e *runExec) finishItem(ctx context.Context, item runstate.Item, it *itemState) {
	res := runstate.ItemResult{Status: it.status, Failure: it.failure, Warnings: it.warnings, Verified: it.entry != nil}
	if it.entry != nil {
		res.RawSize, res.StoredSize = it.raw, it.stored
		res.RawSHA256, res.SHA256 = it.rawSum, it.sum
	}
	if res.Status == "" || res.Status == runstate.ItemRunning {
		res.Status = runstate.ItemFailed
	}
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	if err := e.o.Store.FinishItem(fctx, e.run.Ref, item.ID, res); err != nil {
		e.log.ErrorContext(ctx, "recording a run item failed", slog.Any("error", err))
	}
}

// hasStatsWarning reports a stats warning code.
func hasStatsWarning(st *runnerpb.DumpStats, code string) bool {
	for _, w := range st.GetWarnings() {
		if w.GetCode() == code {
			return true
		}
	}
	return false
}
