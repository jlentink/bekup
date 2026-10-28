package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

// The restore worker (task 4.2, D-166): River kind restore in the restore
// queue (never retried, IDs-only args). One attempt:
//
//  1. load the restore; anything but queued is left alone (a restore runs
//     at most once);
//  2. take a run slot for the target connection (restores share the
//     global and per-connection pool with backups, D-147/D-156); snooze
//     while none is free (a snooze is not an attempt);
//  3. re-check the target under a share lock (LockRestoreTargetTx: not
//     deleted, a temporary one not expired) and audit restore.started
//     (fail closed);
//  4. Stage (queued -> staging -> verifying, D-162);
//  5. re-check the engine against the signed manifest, pick the
//     EngineRestorer of the target's engine, move to restoring and stream
//     the verified plaintext into it;
//  6. Cleanup always; finish with a sanitized code, the log counts and
//     warning codes; audit restore.finished; emit the notification hook.
//
// A River cancel (or the queue timeout) cancels the context: the runner
// kills the restore container and the PostgreSQL restorer drops its
// temporary database, leaving an overwritten target untouched.

// RunSlots is the global / per-connection run semaphore (*runstate.Slots).
type RunSlots interface {
	TryAcquire(ctx context.Context, holder, connection uuid.UUID, kind runstate.SlotKind, l runstate.SlotLimits) (bool, error)
	Heartbeat(ctx context.Context, holder uuid.UUID) error
	Release(ctx context.Context, holder uuid.UUID) error
}

// TargetSource is the restore side of the connection service
// (*connections.Service).
type TargetSource interface {
	// LockRestoreTargetTx share-locks a usable restore target (permanent,
	// or temporary and unexpired) in q's transaction.
	LockRestoreTargetTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (connections.Connection, error)
	// RestoreTarget builds the runner target (an unexpired target);
	// release zeroes it.
	RestoreTarget(ctx context.Context, id uuid.UUID) (*runnerpb.Target, func(), error)
	// RunningRestoreTarget builds the runner target for a restore holding
	// a run slot on it (holder): an expired temporary target stays usable
	// while the slot is held, a deleted one never; release zeroes it.
	RunningRestoreTarget(ctx context.Context, id, holder uuid.UUID) (*runnerpb.Target, func(), error)
	// RecordRunFailure audits and alerts TLS pin / SSH host-key
	// mismatches of a runner call.
	RecordRunFailure(ctx context.Context, id uuid.UUID, op string, err error) error
}

// Defaults of the worker.
const (
	DefaultSlotRetry = 15 * time.Second
	finalTimeout     = 2 * time.Minute
	runOpRestore     = "restore"
)

// WorkerOptions configure NewWorker.
type WorkerOptions struct {
	Service *Service
	Slots   RunSlots
	Targets TargetSource
	Runner  runnerpb.RunnerServiceClient
	// Limits are workers.max_concurrent_runs / max_runs_per_connection.
	Limits runstate.SlotLimits
	// LogMaxLines / LogMaxLineBytes are the log caps
	// (workers.run_log_max_lines / run_log_max_line_bytes).
	LogMaxLines      int
	LogMaxLineBytes  int
	LogFlushInterval time.Duration
	// Notifier is the D-161 emit hook (nil: NoNotify).
	Notifier Notifier
	// Registry holds known secret values for redaction (nil:
	// logging.DefaultRegistry). The restore registers its target's
	// credentials for its duration.
	Registry *logging.Registry
	Logger   *slog.Logger
	// SlotRetry is the snooze while no slot is free.
	SlotRetry time.Duration
	// SlotHeartbeat overrides runstate.SlotHeartbeat (tests).
	SlotHeartbeat time.Duration
}

// Worker works restore jobs.
type Worker struct {
	river.WorkerDefaults[queue.RestoreArgs]
	o WorkerOptions
}

// NewWorker validates o.
func NewWorker(o WorkerOptions) (*Worker, error) {
	if o.Service == nil || o.Slots == nil || o.Targets == nil || o.Runner == nil {
		return nil, errors.New("restore: the worker needs Service, Slots, Targets and Runner")
	}
	if o.Limits.Global < 1 || o.Limits.PerConnection < 1 {
		return nil, errors.New("restore: worker slot limits must be positive")
	}
	if o.Notifier == nil {
		o.Notifier = NoNotify{}
	}
	if o.Registry == nil {
		o.Registry = logging.DefaultRegistry
	}
	if o.Logger == nil {
		o.Logger = o.Service.o.Logger
	}
	if o.SlotRetry <= 0 {
		o.SlotRetry = DefaultSlotRetry
	}
	if o.SlotHeartbeat <= 0 {
		o.SlotHeartbeat = runstate.SlotHeartbeat
	}
	return &Worker{o: o}, nil
}

// Register adds the worker to ws.
func Register(ws *queue.Workers, w *Worker) error { return queue.Register(ws, w) }

// errRestoreCanceled is the River cancel reason of a canceled restore.
var errRestoreCanceled = errors.New("restore: the restore was canceled")

func finalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finalTimeout)
}

// Work implements river.Worker. Every outcome is recorded on the restore
// row and returns nil (restores are never retried), except a cancel
// (river.JobCancel), a slot shortage (snooze) and a failure to record.
func (w *Worker) Work(ctx context.Context, job *river.Job[queue.RestoreArgs]) error {
	id := job.Args.RestoreID
	r, err := w.o.Service.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	log := w.o.Logger.With(slog.String("restore_id", id.String()), slog.String("job_id", r.JobID.String()))
	if r.Status != StatusQueued {
		log.InfoContext(ctx, "restore is no longer queued; nothing to do", slog.String("status", string(r.Status)))
		return nil
	}
	ok, err := w.o.Slots.TryAcquire(ctx, id, r.TargetConnectionID, runstate.SlotRestore, w.o.Limits)
	if err != nil {
		return w.failQueued(ctx, log, r, err)
	}
	if !ok {
		log.DebugContext(ctx, "restore waits for a concurrency slot")
		jitter := time.Duration(time.Now().UnixNano() % int64(w.o.SlotRetry/4+1))
		return river.JobSnooze(w.o.SlotRetry + jitter)
	}
	defer func() {
		fctx, cancel := finalCtx(ctx)
		defer cancel()
		if err := w.o.Slots.Release(fctx, id); err != nil {
			log.WarnContext(fctx, "releasing the run slot failed", slog.Any("error", err))
		}
	}()
	conn, err := w.lockTarget(ctx, r.TargetConnectionID)
	if err != nil {
		return w.failQueued(ctx, log, r, err)
	}
	if err := w.recheckPrivileges(ctx, log, r, conn); err != nil {
		return w.failQueued(ctx, log, r, err)
	}
	if err := w.audit(ctx, audit.ActionRestoreStarted, r, audit.Details{
		"target_engine": string(conn.Engine), "temporary_target": conn.Temporary, "attempt": job.Attempt,
	}); err != nil {
		log.ErrorContext(ctx, "recording restore.started failed", slog.Any("error", err))
		return w.failQueued(ctx, log, r, ErrAuditFailed)
	}
	w.o.Notifier.Emit(ctx, Notification{Event: NotifyStarted, RestoreID: id, JobID: r.JobID})
	e := &execution{w: w, r: r, conn: conn, log: log, holder: id, ownSlot: true, notify: w.o.Notifier}
	out, err := e.run(ctx)
	if err != nil {
		return err
	}
	if out.Status == StatusCanceled {
		return river.JobCancel(errRestoreCanceled)
	}
	return nil
}

// InternalResult is the outcome of RunInternal.
type InternalResult struct {
	Status Status
	// Code is the sanitized error code ("" on success).
	Code     string
	Warnings []string
}

// RunInternal works a queued internal restore (created by
// Service.CreateInternalTx for a test restore, task 4.6) in the caller's
// worker: the caller already holds the run slot for the target connection
// (holder) and keeps it alive. The restore goes through the same steps as
// Work (target re-check, restore.started audit, staging and verification,
// the engine restorer, restore.finished audit) with extra merged into both
// audit entries; it emits no restore notifications (the test restore emits
// its own). A canceled context ends it as canceled. The error is non-nil
// only when the outcome could not be recorded or the restore was not
// queued.
func (w *Worker) RunInternal(ctx context.Context, id, holder uuid.UUID, extra audit.Details) (InternalResult, error) {
	r, err := w.o.Service.Get(ctx, id)
	if err != nil {
		return InternalResult{}, err
	}
	if r.Status != StatusQueued {
		return InternalResult{}, ErrNotQueued
	}
	log := w.o.Logger.With(slog.String("restore_id", id.String()), slog.String("job_id", r.JobID.String()))
	conn, err := w.lockTarget(ctx, r.TargetConnectionID)
	if err != nil {
		status, code, ferr := w.finishUnstarted(ctx, log, r, err, NoNotify{})
		return InternalResult{Status: status, Code: code}, ferr
	}
	details := audit.Details{"target_engine": string(conn.Engine), "temporary_target": conn.Temporary, "attempt": 1}
	for k, v := range extra {
		details[k] = v
	}
	if err := w.audit(ctx, audit.ActionRestoreStarted, r, details); err != nil {
		log.ErrorContext(ctx, "recording restore.started failed", slog.Any("error", err))
		status, code, ferr := w.finishUnstarted(ctx, log, r, ErrAuditFailed, NoNotify{})
		return InternalResult{Status: status, Code: code}, ferr
	}
	e := &execution{w: w, r: r, conn: conn, log: log, holder: holder, notify: NoNotify{}, extra: extra}
	out, err := e.run(ctx)
	if err != nil {
		return InternalResult{}, err
	}
	if out.NotQueued {
		return InternalResult{}, ErrNotQueued
	}
	return out.InternalResult, nil
}

// lockTarget re-checks the target connection under a share lock.
func (w *Worker) lockTarget(ctx context.Context, id uuid.UUID) (connections.Connection, error) {
	var c connections.Connection
	err := db.Tx(ctx, w.o.Service.o.DB, func(q *sqlc.Queries) error {
		var err error
		c, err = w.o.Targets.LockRestoreTargetTx(ctx, q, id)
		return err
	})
	if errors.Is(err, connections.ErrNotFound) {
		return c, ErrTargetUnavailable
	}
	if err != nil {
		return c, fmt.Errorf("restore: lock the target connection: %w", err)
	}
	return c, nil
}

// failQueued fails a restore that never started (still queued).
func (w *Worker) failQueued(ctx context.Context, log *slog.Logger, r sqlc.Restore, cause error) error {
	status, _, err := w.finishUnstarted(ctx, log, r, cause, w.o.Notifier)
	if err != nil {
		return err
	}
	if status == StatusCanceled {
		return river.JobCancel(errRestoreCanceled)
	}
	return nil
}

// finishUnstarted records the failure (or cancel) of a restore that never
// started and reports the status and code.
func (w *Worker) finishUnstarted(ctx context.Context, log *slog.Logger, r sqlc.Restore, cause error, notify Notifier) (Status, string, error) {
	status := StatusFailed
	if ctx.Err() != nil && errors.Is(context.Cause(ctx), rivertype.ErrJobCancelledRemotely) {
		status, cause = StatusCanceled, context.Canceled
	}
	changed, err := w.o.Service.Finish(ctx, r.ID, status, cause)
	if err != nil {
		return status, "", fmt.Errorf("restore: record the failure of restore %s: %w", r.ID, err)
	}
	code := ErrorCode(cause)
	if status == StatusCanceled {
		code = CodeCanceled
	}
	log.WarnContext(context.WithoutCancel(ctx), "restore failed before it started", slog.String("code", code), slog.Any("error", cause))
	if changed {
		notify.Emit(context.WithoutCancel(ctx), Notification{Event: NotifyFailed, RestoreID: r.ID, JobID: r.JobID, Status: status, ErrorCode: code})
	}
	return status, code, nil
}

// audit records a restore entry (system actor, target the restore).
func (w *Worker) audit(ctx context.Context, action audit.Action, r sqlc.Restore, extra audit.Details) error {
	d := audit.Details{
		"job_id": r.JobID.String(), "artifact_id": r.ArtifactID.String(), "target_connection_id": r.TargetConnectionID.String(),
		"mode": string(r.Mode), "no_owner": r.NoOwner,
	}
	if r.TargetDbName != nil {
		d["target_database"] = *r.TargetDbName
	}
	if r.RequestedBy != nil {
		d["requested_by"] = r.RequestedBy.String()
	}
	for k, v := range extra {
		d[k] = v
	}
	actx, cancel := finalCtx(ctx)
	defer cancel()
	_, err := w.o.Service.o.Auditor.Record(actx, audit.Event{
		Actor: audit.SystemActor(), Action: action, TargetType: "restore", TargetID: r.ID.String(), Details: d,
	})
	return err
}

// execution is one started restore.
type execution struct {
	w    *Worker
	r    sqlc.Restore
	conn connections.Connection
	log  *slog.Logger
	// holder holds the run slot; ownSlot: this execution heartbeats it
	// (internal restores: the caller does).
	holder  uuid.UUID
	ownSlot bool
	notify  Notifier
	// extra is merged into the restore.finished audit entry.
	extra audit.Details

	logs  *runstate.LogWriter
	sink  *logSink
	mu    sync.Mutex
	unreg []func()
}

// runOutcome is what an execution recorded.
type runOutcome struct {
	InternalResult
	// NotQueued: another attempt started the restore; nothing recorded.
	NotQueued bool
}

// run stages, restores and records the outcome. The error is non-nil only
// when the outcome could not be recorded.
func (e *execution) run(parent context.Context) (runOutcome, error) {
	o := &e.w.o
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	e.logs = runstate.StartLogWriter(ctx, runstate.LogWriterOptions{
		Append: func(c context.Context, lines []runstate.LogLine) error {
			return o.Service.AppendLogs(c, e.r.ID, lines)
		},
		Scrub:         func(s string) string { return o.Registry.Scrub(logging.RedactCredentials(s)) },
		MaxLines:      o.LogMaxLines,
		MaxLineBytes:  o.LogMaxLineBytes,
		FlushInterval: o.LogFlushInterval,
		OnError: func(err error) {
			e.log.WarnContext(ctx, "storing restore log lines failed", slog.Any("error", err))
		},
	})
	e.sink = &logSink{w: e.logs}
	defer e.unregister()
	stop := func() {}
	if e.ownSlot {
		stop = e.heartbeat(ctx, cancel)
	}

	e.sink.Line(LogInfo, fmt.Sprintf("restore started (mode %s); staging and verifying the backup", e.r.Mode))
	var warnings []string
	err := e.restore(ctx, &warnings)
	stop()
	if errors.Is(err, ErrNotQueued) {
		// Another attempt started it (never happens with a unique job).
		e.logs.Close()
		e.log.WarnContext(ctx, "restore was started by someone else")
		return runOutcome{NotQueued: true}, nil
	}

	status, cause := e.classify(ctx, err)
	if cause != nil {
		e.securityEvent(parent, cause)
		e.sink.Line(LogError, "restore "+string(status)+": "+ErrorMessage(ErrorCode(cause)))
	} else {
		e.sink.Line(LogInfo, "restore succeeded")
	}
	lines, dropped := e.logs.Close()
	changed, ferr := o.Service.finish(parent, e.r.ID, status, cause, Outcome{LogLines: lines, LogLinesDropped: dropped, Warnings: warnings})
	if ferr != nil {
		return runOutcome{}, fmt.Errorf("restore: record restore %s: %w", e.r.ID, ferr)
	}
	code := ErrorCode(cause)
	if status == StatusCanceled {
		code = CodeCanceled
	}
	if !changed {
		// Stage finished it already (a failed staging): report what it
		// recorded.
		if r, gerr := o.Service.Get(context.WithoutCancel(parent), e.r.ID); gerr == nil {
			status, code = r.Status, ""
			if r.ErrorCode != nil {
				code = *r.ErrorCode
			}
		}
	}
	fin := audit.Details{
		"status": string(status), "error_code": code, "warnings": warnings, "log_lines": lines, "log_lines_dropped": dropped,
	}
	for k, v := range e.extra {
		fin[k] = v
	}
	if aerr := e.w.audit(parent, audit.ActionRestoreFinished, e.r, fin); aerr != nil {
		e.log.ErrorContext(context.WithoutCancel(parent), "recording restore.finished failed", slog.Any("error", aerr))
	}
	ev := NotifyFailed
	if status == StatusSucceeded {
		ev = NotifyCompleted
	}
	e.notify.Emit(context.WithoutCancel(parent), Notification{Event: ev, RestoreID: e.r.ID, JobID: e.r.JobID, Status: status, ErrorCode: code})
	e.log.InfoContext(context.WithoutCancel(parent), "restore finished", slog.String("status", string(status)), slog.String("code", code))
	return runOutcome{InternalResult: InternalResult{Status: status, Code: code, Warnings: warnings}}, nil
}

// restore runs the staged part; warnings collects the outcome's codes.
func (e *execution) restore(ctx context.Context, warnings *[]string) error {
	o := &e.w.o
	st, err := o.Service.Stage(ctx, e.r.ID)
	if err != nil {
		return err
	}
	defer st.Cleanup()
	e.sink.Line(LogInfo, fmt.Sprintf("backup staged and verified (%d bytes stored)", st.StoredSize()))
	req, restorer, err := e.request(st)
	if err != nil {
		return err
	}
	if err := st.BeginRestore(ctx); err != nil {
		return err
	}
	src, err := st.Open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := src.Close(); cerr != nil {
			e.log.DebugContext(ctx, "closing the staged dump failed", slog.Any("error", cerr))
		}
	}()
	res, err := restorer.Restore(ctx, req, src, e.sink)
	*warnings = append(*warnings, res.Warnings...)
	for _, w := range res.Warnings {
		e.sink.Line(LogWarn, "warning: "+w)
	}
	return err
}

// request checks the staged restore against its target and builds the
// engine request: the target's engine must equal the engine in the signed
// manifest; a globals artifact only restores as globals (no database), a
// database artifact only into a database.
func (e *execution) request(st *Staged) (EngineRequest, EngineRestorer, error) {
	m, a := st.Manifest(), st.Artifact()
	if m == nil {
		return EngineRequest{}, nil, ErrUnavailable
	}
	src := engine.Kind(m.Engine.Kind)
	if src != e.conn.Engine {
		e.sink.Line(LogError, fmt.Sprintf("the backup is a %s dump but the target runs %s", src, e.conn.Engine))
		return EngineRequest{}, nil, ErrEngineMismatch
	}
	req := EngineRequest{
		RestoreID: e.r.ID, Runner: e.w.o.Runner, Target: e.target, TargetEngine: e.conn.Engine,
		Mode: Mode(e.r.Mode), SourceEngine: src, SourceVersion: m.Engine.VersionKey, Format: st.Format(),
		NoOwner: e.r.NoOwner, OnNeutralized: e.neutralized,
	}
	switch a.Kind {
	case sqlc.ArtifactKindGlobals:
		if e.r.TargetDbName != nil || src != engine.KindPostgres {
			return EngineRequest{}, nil, fmt.Errorf("%w: globals restore", ErrUnsupported)
		}
		req.Kind = KindGlobals
	case sqlc.ArtifactKindDatabase:
		if e.r.TargetDbName == nil {
			return EngineRequest{}, nil, fmt.Errorf("%w: no target database", ErrUnsupported)
		}
		req.Kind, req.TargetDB = KindDatabase, *e.r.TargetDbName
	default:
		return EngineRequest{}, nil, fmt.Errorf("%w: artifact kind", ErrUnsupported)
	}
	if req.Format != pipeline.FormatCustom && req.Format != pipeline.FormatSQL {
		return EngineRequest{}, nil, fmt.Errorf("%w: dump format", ErrUnsupported)
	}
	raw := st.RawSize()
	if raw <= 0 {
		return EngineRequest{}, nil, fmt.Errorf("%w: the dump size is unknown", ErrUnsupported)
	}
	req.TotalBytes = uint64(raw)
	r, ok := restorerFor(e.conn.Engine)
	if !ok {
		return EngineRequest{}, nil, ErrNoRestorer
	}
	return req, r, nil
}

// neutralized audits the neutralization of the restored database before
// the swap or the success (restore.secdef_neutralized, fail closed; D-171).
// Nothing is recorded when no function had to be switched.
func (e *execution) neutralized(ctx context.Context, n Neutralized) error {
	if n.Count == 0 {
		return nil
	}
	d := audit.Details{"database": n.Database, "functions_neutralized": n.Count, "functions": slices.Clone(n.Functions),
		"functions_listed": len(n.Functions)}
	for k, v := range e.extra {
		d[k] = v
	}
	if err := e.w.audit(ctx, audit.ActionRestoreSecdefNeutralized, e.r, d); err != nil {
		e.log.ErrorContext(context.WithoutCancel(ctx), "recording restore.secdef_neutralized failed", slog.Any("error", err))
		return ErrAuditFailed
	}
	return nil
}

// target builds the runner target; the first build registers the
// credentials for redaction until the restore ends.
func (e *execution) target(ctx context.Context) (*runnerpb.Target, func(), error) {
	t, release, err := e.w.o.Targets.RunningRestoreTarget(ctx, e.r.TargetConnectionID, e.holder)
	if err != nil {
		var nr *connections.NotReadyError
		switch {
		case errors.As(err, &nr):
			e.sink.Line(LogError, "the target connection is not ready: "+nr.Reason)
			return nil, nil, fmt.Errorf("%w: %w", ErrTargetNotReady, err)
		case errors.Is(err, connections.ErrNotFound):
			return nil, nil, ErrTargetUnavailable
		default:
			return nil, nil, err
		}
	}
	e.mu.Lock()
	if e.unreg == nil {
		e.unreg = append(e.unreg, registerTargetSecrets(e.w.o.Registry, t))
	}
	e.mu.Unlock()
	return t, release, nil
}

func (e *execution) unregister() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, u := range e.unreg {
		u()
	}
	e.unreg = nil
}

// heartbeat refreshes the run slot until stopped; a lost slot stops the
// restore.
func (e *execution) heartbeat(ctx context.Context, cancel context.CancelCauseFunc) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(e.w.o.SlotHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				err := e.w.o.Slots.Heartbeat(ctx, e.holder)
				if errors.Is(err, runstate.ErrSlotLost) {
					e.log.ErrorContext(ctx, "the run slot expired; stopping the restore")
					cancel(runstate.ErrSlotLost)
					return
				}
				if err != nil && ctx.Err() == nil {
					e.log.WarnContext(ctx, "run slot heartbeat failed", slog.Any("error", err))
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-finished
	}
}

// classify maps the outcome to the terminal status and its cause: the
// restore context's end wins (River cancel = canceled; queue timeout,
// lost slot, shutdown = failed).
func (e *execution) classify(ctx context.Context, err error) (Status, error) {
	if ctx.Err() != nil {
		cause := context.Cause(ctx)
		switch {
		case errors.Is(cause, rivertype.ErrJobCancelledRemotely):
			return StatusCanceled, context.Canceled
		case errors.Is(cause, runstate.ErrSlotLost):
			return StatusFailed, runstate.ErrSlotLost
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return StatusFailed, context.DeadlineExceeded
		default:
			return StatusFailed, ErrInterrupted
		}
	}
	if err == nil {
		return StatusSucceeded, nil
	}
	e.log.WarnContext(ctx, "restore failed", slog.String("code", ErrorCode(err)), slog.Any("error", err))
	return StatusFailed, err
}

// securityEvent audits and alerts pin / host-key mismatches of the target.
func (e *execution) securityEvent(ctx context.Context, err error) {
	r := runnerpb.ErrorReason(err)
	if r != runnerpb.ReasonPinMismatch && r != runnerpb.ReasonSSHHostKey {
		return
	}
	if rerr := e.w.o.Targets.RecordRunFailure(context.WithoutCancel(ctx), e.r.TargetConnectionID, runOpRestore, err); rerr != nil {
		e.log.ErrorContext(context.WithoutCancel(ctx), "recording a connection security event failed", slog.Any("error", rerr))
	}
}

// logSink adapts the run-log writer (sanitize, redact, cap, batch) to
// LogSink.
type logSink struct {
	w *runstate.LogWriter
}

// Line implements LogSink (worker events).
func (s *logSink) Line(level, msg string) {
	s.w.Log(logLevel(level), runstate.StreamWorker, msg)
}

// runnerLine stores a runner log line with its stream.
func (s *logSink) runnerLine(l *runnerpb.LogLine) {
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
	s.w.Log(runstate.LevelInfo, stream, text)
}

func logLevel(level string) sqlc.RunLogLevel {
	switch level {
	case LogWarn:
		return runstate.LevelWarn
	case LogError:
		return runstate.LevelError
	default:
		return runstate.LevelInfo
	}
}
