package restore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

// Scheduled test restores (task 4.6). Before anything else the worker
// re-probes the scratch target's role (D-068, D-170): a privileged or
// unknown role (or a failing probe) needs a stored admin acknowledgement
// whose reasons cover the current ones, else the test restore ends as
// error privileged_target without touching the target. It then takes the
// job's newest successful run with verified, signed database artifacts
// and, for every database of it (never the globals):
//
//  1. records an internal restore (CreateInternalTx: mode new_db, no
//     requester, the system as actor) into a new scratch database
//     bekup_test_<scratch id> on the job's designated scratch connection,
//     and the item row, in one transaction;
//  2. runs it through the restore worker in this process (RunInternal:
//     the same staging, verification against the signed manifest and
//     engine restorer as every restore; the test restore holds the run
//     slot). The PostgreSQL restorer creates the scratch database without
//     PUBLIC CONNECT/TEMPORARY and hardens it (NeutralizeRestored, kind
//     scratch: SECURITY DEFINER functions become SECURITY INVOKER) before
//     the restore reports success, so nothing below ever inspects an
//     unhardened copy (D-170); a failed hardening fails the restore;
//  3. asks the runner for the scratch database's statistics (ScratchStats,
//     the same level as the backup: PostgreSQL exact counts in the
//     pinned, hostile-safe session, MySQL/MariaDB estimates, D-152/D-153);
//  4. compares them with the item's backup_stats (CompareStats);
//  5. always drops the scratch database (DropScratchDatabase: the runner
//     derives the name from the scratch id, nothing else can be dropped),
//     also on failure, cancel and panic; a failed drop is audited, alerted
//     and retried by the schedule worker.
//
// The verdict (passed | warnings | failed | error | canceled), the counts
// and the per-database diffs are stored, audited (test_restore.finished)
// and emitted (test_restore.passed | warnings | failed, D-161).

// Test-restore errors.
var (
	// ErrTestRestoreActive: the job already has a queued or running test
	// restore.
	ErrTestRestoreActive = errors.New("restore: a test restore of this job is already queued or running")
	// ErrNoTestTarget: the job has no scratch target.
	ErrNoTestTarget = errors.New("restore: the job has no test-restore target")
)

// Test-restore statuses.
const (
	TRStatusQueued   = sqlc.TestRestoreStatusQueued
	TRStatusRunning  = sqlc.TestRestoreStatusRunning
	TRStatusPassed   = sqlc.TestRestoreStatusPassed
	TRStatusWarnings = sqlc.TestRestoreStatusWarnings
	TRStatusFailed   = sqlc.TestRestoreStatusFailed
	TRStatusError    = sqlc.TestRestoreStatusError
	TRStatusCanceled = sqlc.TestRestoreStatusCanceled
)

// Test-restore error codes (test_restores.error_code and
// test_restore_items.error_code; the restore codes of ErrorCode apply too).
const (
	CodeNoBackup          = "no_backup"
	CodeTestTargetChanged = "target_changed"
	CodeJobDeleted        = "job_deleted"
	CodeStatsUnavailable  = "stats_unavailable"
	CodeStatsInvalid      = "stats_invalid"
	CodeDatabaseErrors    = "database_errors"
	// CodeTestPrivilegedTarget: the scratch target's role is privileged or
	// unknown now and no stored admin acknowledgement covers it, or its
	// privileges could not be checked (D-068, D-170).
	CodeTestPrivilegedTarget = "privileged_target"
)

var testCodeMessages = map[string]string{
	CodeNoBackup:          "The job has no successful backup with verified, signed database artifacts to test.",
	CodeTestTargetChanged: "The job's test-restore target changed or was removed after the test restore was queued.",
	CodeJobDeleted:        "The job was deleted.",
	CodeStatsUnavailable:  "The statistics of the restored scratch database could not be read.",
	CodeStatsInvalid:      "The statistics of the restored scratch database were malformed.",
	CodeDatabaseErrors:    "At least one database could not be restored or checked; see the databases of this test restore.",
	CodeTestPrivilegedTarget: "The scratch connection's role is privileged or its privileges are unknown (or could not be checked) " +
		"and no admin acknowledgement covers it; an admin must designate the target again with the acknowledgement.",
	CodeAbandoned: "No worker finished the test restore within the restore timeout (worker crash or restart); " +
		"its scratch databases are dropped by the next retry.",
}

// TestErrorMessage is the fixed text of a test-restore error code.
func TestErrorMessage(code string) string {
	if m, ok := testCodeMessages[code]; ok {
		return m
	}
	return ErrorMessage(code)
}

// Limits of a test restore.
const (
	// maxTestDatabases bounds the databases tested per run.
	maxTestDatabases = 100
	// TestRestoreAuditTarget is the audit target type of test restores.
	TestRestoreAuditTarget = "test_restore"
	runOpTestRestore       = "test_restore"
)

// --- internal restores ----------------------------------------------------------

// InternalInput is a system-initiated restore into a scratch database.
type InternalInput struct {
	// ID is the restore id (its first 48 bits are the scratch id).
	ID                 uuid.UUID
	ArtifactID         uuid.UUID
	TargetConnectionID uuid.UUID
	// TargetDB must be the scratch name of ID (engine.ScratchDatabaseName).
	TargetDB string
	// RiverJobID is the test restore's River job (cancels reach both).
	RiverJobID int64
}

// CreateInternalTx records a queued internal restore (mode new_db, no
// requester, pg_restore --no-owner) of a database artifact into a scratch
// database, in q's transaction. The artifact must be restorable like for
// CreateTx; recipient-encrypted artifacts cannot be tested (bekup does not
// hold the identity: ErrIdentityRequired). It does not authorize: test
// restores are system work on a target designated with a fresh second
// factor (job.test_restore_edit).
func (s *Service) CreateInternalTx(ctx context.Context, q *sqlc.Queries, in InternalInput) (sqlc.Restore, error) {
	want, err := engine.ScratchDatabaseName(TempID(in.ID))
	if err != nil || in.TargetDB != want {
		return sqlc.Restore{}, fmt.Errorf("%w: the scratch database name must be derived from the restore id", ErrUnsupported)
	}
	row, err := q.GetDownloadArtifact(ctx, in.ArtifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Restore{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Restore{}, fmt.Errorf("restore: load artifact: %w", err)
	}
	a := &row.Artifact
	if err := checkArtifact(&row); err != nil {
		return sqlc.Restore{}, ErrUnavailable
	}
	if a.Kind != sqlc.ArtifactKindDatabase {
		return sqlc.Restore{}, fmt.Errorf("%w: only database artifacts are test-restored", ErrUnsupported)
	}
	if encryptionOf(a) == encRecipients {
		return sqlc.Restore{}, ErrIdentityRequired
	}
	if _, err := s.checkKeys(ctx, &row, nil); err != nil {
		if errors.Is(err, ErrKeyUnavailable) {
			return sqlc.Restore{}, err
		}
		return sqlc.Restore{}, fmt.Errorf("restore: check keys: %w", err)
	}
	scratch := in.TargetDB
	r, err := q.InsertRestore(ctx, sqlc.InsertRestoreParams{
		ID: in.ID, ArtifactID: a.ID, RunItemID: *a.RunItemID, JobID: a.JobID, TargetConnectionID: in.TargetConnectionID,
		TargetDbName: &scratch, Mode: sqlc.RestoreModeNewDb, NoOwner: true,
	})
	if err != nil {
		return sqlc.Restore{}, fmt.Errorf("restore: insert internal restore: %w", err)
	}
	if in.RiverJobID != 0 {
		if err := s.SetRiverJob(ctx, q, in.ID, in.RiverJobID); err != nil {
			return sqlc.Restore{}, err
		}
	}
	return r, s.publish(ctx, q, EventRestore, in.ID)
}

// --- notifications --------------------------------------------------------------

// Test-restore notification events (D-161). Errors and cancels are
// reported as failed with their status.
const (
	NotifyTestPassed   NotifyEvent = "test_restore.passed"
	NotifyTestWarnings NotifyEvent = "test_restore.warnings"
	NotifyTestFailed   NotifyEvent = "test_restore.failed"
)

// TestNotification is one test-restore event (identifiers and fixed codes
// only).
type TestNotification struct {
	Event         NotifyEvent
	TestRestoreID uuid.UUID
	JobID         uuid.UUID
	RunID         *uuid.UUID
	Status        sqlc.TestRestoreStatus
	ErrorCode     string
}

// TestNotifier is the emit hook for test-restore notifications (task 5.3
// delivers them; until then NoTestNotify). Emit must not block for long.
type TestNotifier interface {
	EmitTestRestore(ctx context.Context, n TestNotification)
}

// NoTestNotify discards test-restore notifications.
type NoTestNotify struct{}

// EmitTestRestore implements TestNotifier.
func (NoTestNotify) EmitTestRestore(context.Context, TestNotification) {}

// Alert kinds of test restores (logged at ERROR until task 5.5).
const (
	// AlertTestRestoreFailed: a test restore failed or could not complete.
	AlertTestRestoreFailed AlertKind = "run.test_restore_failed"
	// AlertScratchKept: a scratch database could not be dropped.
	AlertScratchKept AlertKind = "test_restore_scratch_kept"
)

// --- worker ---------------------------------------------------------------------

// TestRestoreWorkerOptions configure NewTestRestoreWorker.
type TestRestoreWorkerOptions struct {
	// Restores is the restore worker (its service, slots, targets, runner
	// and limits are shared).
	Restores *Worker
	// Notifier is the D-161 emit hook (nil: NoTestNotify; app wiring
	// passes the serve notifier, a no-op until task 5.3).
	Notifier TestNotifier
	// PerTableTimeout / Budget bound the scratch statistics (0: the
	// runner defaults). App wiring passes the backup statistics' values,
	// so both sides of the comparison run under the same limits.
	PerTableTimeout time.Duration
	Budget          time.Duration
	Logger          *slog.Logger
}

// TestRestoreWorker works test_restore jobs (restore queue, never retried).
type TestRestoreWorker struct {
	river.WorkerDefaults[queue.TestRestoreArgs]
	o TestRestoreWorkerOptions
}

// NewTestRestoreWorker validates o.
func NewTestRestoreWorker(o TestRestoreWorkerOptions) (*TestRestoreWorker, error) {
	if o.Restores == nil {
		return nil, errors.New("restore: the test-restore worker needs the restore worker")
	}
	if o.Notifier == nil {
		o.Notifier = NoTestNotify{}
	}
	if o.Logger == nil {
		o.Logger = o.Restores.o.Logger
	}
	return &TestRestoreWorker{o: o}, nil
}

// RegisterTestRestore adds the worker to ws.
func RegisterTestRestore(ws *queue.Workers, w *TestRestoreWorker) error { return queue.Register(ws, w) }

func (w *TestRestoreWorker) svc() *Service { return w.o.Restores.o.Service }

// itemOutcome is the verdict of one database.
type itemOutcome struct {
	status sqlc.TestRestoreStatus
	code   string
	db     string
}

// testOutcome is the verdict of a test restore.
type testOutcome struct {
	status sqlc.TestRestoreStatus
	code   string
	runID  *uuid.UUID
	items  []itemOutcome
	eng    engine.Kind
	// privileges is the target role's verdict read before the run (nil:
	// not checked).
	privileges *connections.Privileges
	acked      bool
}

// Work implements river.Worker.
func (w *TestRestoreWorker) Work(ctx context.Context, job *river.Job[queue.TestRestoreArgs]) error {
	rw := w.o.Restores
	id := job.Args.TestRestoreID
	tr, err := sqlc.New(w.svc().o.DB).GetTestRestore(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("restore: load test restore: %w", err)
	}
	log := w.o.Logger.With(slog.String("test_restore_id", id.String()), slog.String("job_id", tr.JobID.String()))
	if tr.Status != TRStatusQueued {
		log.InfoContext(ctx, "test restore is no longer queued; nothing to do", slog.String("status", string(tr.Status)))
		return nil
	}
	ok, err := rw.o.Slots.TryAcquire(ctx, id, tr.TargetConnectionID, runstate.SlotRestore, rw.o.Limits)
	if err != nil {
		return w.finish(ctx, log, tr, testOutcome{status: TRStatusError, code: CodeInternal})
	}
	if !ok {
		log.DebugContext(ctx, "test restore waits for a concurrency slot")
		jitter := time.Duration(time.Now().UnixNano() % int64(rw.o.SlotRetry/4+1))
		return river.JobSnooze(rw.o.SlotRetry + jitter)
	}
	defer func() {
		fctx, cancel := finalCtx(ctx)
		defer cancel()
		if err := rw.o.Slots.Release(fctx, id); err != nil {
			log.WarnContext(fctx, "releasing the run slot failed", slog.Any("error", err))
		}
	}()
	n, err := sqlc.New(w.svc().o.DB).StartTestRestore(ctx, id)
	if err != nil {
		return fmt.Errorf("restore: start test restore: %w", err)
	}
	if n == 0 {
		return nil
	}
	hctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := w.heartbeat(hctx, cancel, id, log)
	out := w.execute(hctx, log, tr, job.ID)
	stop()
	if hctx.Err() != nil && out.status != TRStatusCanceled {
		switch cause := context.Cause(hctx); {
		case errors.Is(cause, rivertype.ErrJobCancelledRemotely):
			out.status, out.code = TRStatusCanceled, CodeCanceled
		case errors.Is(cause, runstate.ErrSlotLost):
			out.status, out.code = TRStatusError, CodeSlotLost
		case errors.Is(hctx.Err(), context.DeadlineExceeded):
			out.status, out.code = TRStatusError, CodeTimeout
		default:
			out.status, out.code = TRStatusError, CodeInterrupted
		}
	}
	return w.finish(ctx, log, tr, out)
}

// heartbeat keeps the run slot alive until stopped.
func (w *TestRestoreWorker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, holder uuid.UUID, log *slog.Logger) func() {
	rw := w.o.Restores
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(rw.o.SlotHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				err := rw.o.Slots.Heartbeat(ctx, holder)
				if errors.Is(err, runstate.ErrSlotLost) {
					log.ErrorContext(ctx, "the run slot expired; stopping the test restore")
					cancel(runstate.ErrSlotLost)
					return
				}
				if err != nil && ctx.Err() == nil {
					log.WarnContext(ctx, "run slot heartbeat failed", slog.Any("error", err))
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

// execute checks the job and target, picks the run and tests every
// database.
func (w *TestRestoreWorker) execute(ctx context.Context, log *slog.Logger, tr sqlc.TestRestore, riverJobID int64) testOutcome {
	q := sqlc.New(w.svc().o.DB)
	job, err := q.GetJob(ctx, tr.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return testOutcome{status: TRStatusError, code: CodeJobDeleted}
	}
	if err != nil {
		log.ErrorContext(ctx, "loading the job failed", slog.Any("error", err))
		return testOutcome{status: TRStatusError, code: CodeInternal}
	}
	// The designation is re-checked: removing or changing the scratch
	// target stops test restores that were already queued.
	if job.TestRestoreConnectionID == nil || *job.TestRestoreConnectionID != tr.TargetConnectionID {
		return testOutcome{status: TRStatusError, code: CodeTestTargetChanged}
	}
	// Plain reads: the worker holds no transaction here (a FOR SHARE lock
	// would end with the statement). The run slot is held, and every
	// internal restore re-checks its target under a share lock itself.
	src, err := q.GetTestRestoreConnection(ctx, job.ConnectionID)
	if err != nil {
		return testOutcome{status: TRStatusError, code: CodeInternal}
	}
	dst, err := q.GetTestRestoreConnection(ctx, tr.TargetConnectionID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (dst.DeletedAt != nil || dst.Temporary)) {
		return testOutcome{status: TRStatusError, code: CodeTargetUnavailable}
	}
	if err != nil {
		return testOutcome{status: TRStatusError, code: CodeInternal}
	}
	if dst.Engine != src.Engine {
		return testOutcome{status: TRStatusError, code: CodeEngineMismatch}
	}
	eng := engine.Kind(dst.Engine)
	// D-068 / D-170: the target role is probed afresh before every test
	// restore; a gated verdict needs the stored admin acknowledgement.
	priv, perr := w.probePrivileges(ctx, tr)
	if perr != nil {
		if ctx.Err() != nil {
			return testOutcome{status: TRStatusCanceled, code: CodeCanceled, eng: eng}
		}
		log.WarnContext(ctx, "the scratch target's privileges could not be checked; refusing the test restore",
			slog.String("code", ErrorCode(perr)))
		unknown := connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"probe_failed"}}
		return testOutcome{status: TRStatusError, code: CodeTestPrivilegedTarget, eng: eng, privileges: &unknown}
	}
	acked := priv.Gated() && jobs.TestTargetCovered(&job, priv)
	if !jobs.TestTargetCovered(&job, priv) {
		log.WarnContext(ctx, "the scratch target's role is privileged or unknown without a covering admin acknowledgement; refusing the test restore",
			slog.String("target_privileges", string(priv.Status)), slog.Any("reasons", priv.Reasons))
		return testOutcome{status: TRStatusError, code: CodeTestPrivilegedTarget, eng: eng, privileges: &priv}
	}
	runID, err := q.PickTestRestoreRun(ctx, tr.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return testOutcome{status: TRStatusError, code: CodeNoBackup, eng: eng, privileges: &priv, acked: acked}
	}
	if err != nil {
		return testOutcome{status: TRStatusError, code: CodeInternal, eng: eng, privileges: &priv, acked: acked}
	}
	out := testOutcome{runID: &runID, eng: eng, privileges: &priv, acked: acked}
	if _, err := q.SetTestRestoreRun(ctx, sqlc.SetTestRestoreRunParams{ID: tr.ID, RunID: &runID}); err != nil {
		return testOutcome{status: TRStatusError, code: CodeInternal, eng: eng}
	}
	cands, err := q.ListTestRestoreCandidates(ctx, sqlc.ListTestRestoreCandidatesParams{RunID: runID, MaxRows: maxTestDatabases})
	if err != nil {
		out.status, out.code = TRStatusError, CodeInternal
		return out
	}
	if len(cands) == 0 {
		out.status, out.code = TRStatusError, CodeNoBackup
		return out
	}
	log.InfoContext(ctx, "test restore started", slog.String("run_id", runID.String()), slog.Int("databases", len(cands)))
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		out.items = append(out.items, w.item(ctx, log, tr, c, eng, riverJobID))
	}
	out.status, out.code = aggregate(out.items, ctx.Err() != nil)
	return out
}

// aggregate is the test restore's verdict: canceled when it was stopped,
// failed when any database failed the comparison, error when any could not
// be restored or checked, else warnings or passed.
func aggregate(items []itemOutcome, stopped bool) (sqlc.TestRestoreStatus, string) {
	var fail, errs, warn, canceled bool
	firstErr := ""
	for _, it := range items {
		switch it.status {
		case TRStatusFailed:
			fail = true
		case TRStatusError:
			errs = true
			if firstErr == "" {
				firstErr = it.code
			}
		case TRStatusWarnings:
			warn = true
		case TRStatusCanceled:
			canceled = true
		default:
		}
	}
	switch {
	case canceled || stopped:
		return TRStatusCanceled, CodeCanceled
	case fail:
		return TRStatusFailed, ""
	case errs:
		if len(items) == 1 && firstErr != "" {
			return TRStatusError, firstErr
		}
		return TRStatusError, CodeDatabaseErrors
	case warn:
		return TRStatusWarnings, ""
	default:
		return TRStatusPassed, ""
	}
}

func verdictStatus(v Verdict) sqlc.TestRestoreStatus {
	switch v {
	case VerdictPass:
		return TRStatusPassed
	case VerdictWarn:
		return TRStatusWarnings
	default:
		return TRStatusFailed
	}
}

// item tests one database.
func (w *TestRestoreWorker) item(ctx context.Context, log *slog.Logger, tr sqlc.TestRestore, c sqlc.ListTestRestoreCandidatesRow,
	eng engine.Kind, riverJobID int64,
) itemOutcome {
	svc := w.svc()
	dbName := ""
	if c.DbName != nil {
		dbName = *c.DbName
	}
	restoreID := uuid.New()
	scratchID := TempID(restoreID)
	scratch := engine.ScratchPrefix + scratchID
	log = log.With(slog.String("restore_id", restoreID.String()), slog.String("database", dbName))
	var itemID uuid.UUID
	err := db.Tx(ctx, svc.o.DB, func(q *sqlc.Queries) error {
		if _, err := svc.CreateInternalTx(ctx, q, InternalInput{
			ID: restoreID, ArtifactID: c.ArtifactID, TargetConnectionID: tr.TargetConnectionID, TargetDB: scratch, RiverJobID: riverJobID,
		}); err != nil {
			return err
		}
		it, err := q.InsertTestRestoreItem(ctx, sqlc.InsertTestRestoreItemParams{
			TestRestoreID: tr.ID, RunItemID: c.RunItemID, RestoreID: &restoreID, DbName: dbName, ScratchID: scratchID,
		})
		itemID = it.ID
		return err
	})
	if err != nil {
		// Nothing was created on the target: record the item as an error.
		code := ErrorCode(err)
		log.WarnContext(ctx, "the test restore of a database could not be started", slog.String("code", code), slog.Any("error", err))
		w.recordUnstarted(ctx, log, tr.ID, c.RunItemID, dbName, scratchID, code)
		return itemOutcome{status: TRStatusError, code: code, db: dbName}
	}
	finished := false
	defer func() {
		if !finished {
			// A panic between creating and finishing: drop, never keep.
			w.dropScratch(ctx, log, tr, scratchID, restoreID)
		}
	}()
	res, rerr := w.o.Restores.RunInternal(ctx, restoreID, tr.ID, audit.Details{"test_restore_id": tr.ID.String(), "scratch": true})
	var (
		diff   Diff
		status = TRStatusError
		code   string
	)
	switch {
	case rerr != nil:
		log.ErrorContext(ctx, "the internal restore could not be recorded", slog.Any("error", rerr))
		code = CodeInternal
	case res.Status == StatusCanceled:
		status, code = TRStatusCanceled, CodeCanceled
	case res.Status != StatusSucceeded:
		code = res.Code
		diff.RestoreStatus, diff.RestoreErrorCode = string(res.Status), res.Code
	default:
		status, code, diff.Comparison = w.compare(ctx, log, tr, c, eng, restoreID, scratchID, dbName)
	}
	dropped := w.dropScratch(ctx, log, tr, scratchID, restoreID)
	finished = true
	diff.ScratchKept = !dropped
	var cp *string
	if code != "" {
		cp = &code
	}
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	if _, err := sqlc.New(svc.o.DB).FinishTestRestoreItem(fctx, sqlc.FinishTestRestoreItemParams{
		ID: itemID, Status: status, ErrorCode: cp, Diff: encodeDiff(diff), ScratchDropped: dropped,
	}); err != nil {
		log.ErrorContext(fctx, "recording a test-restore database verdict failed", slog.Any("error", err))
	}
	log.InfoContext(fctx, "test restore of a database finished", slog.String("status", string(status)), slog.String("code", code))
	return itemOutcome{status: status, code: code, db: dbName}
}

// probePrivileges reads the scratch target role's verdict with one runner
// Probe (D-068): the same verdict the restore API and the designation use.
func (w *TestRestoreWorker) probePrivileges(ctx context.Context, tr sqlc.TestRestore) (connections.Privileges, error) {
	rw := w.o.Restores
	t, release, err := rw.o.Targets.RestoreTarget(ctx, tr.TargetConnectionID)
	if err != nil {
		return connections.Privileges{}, err
	}
	defer release()
	defer registerTargetSecrets(rw.o.Registry, t)()
	resp, err := rw.o.Runner.Probe(ctx, &runnerpb.ProbeRequest{Ctx: &runnerpb.RunContext{RunId: tr.ID.String()}, Target: t})
	if err != nil {
		w.securityEvent(ctx, tr.TargetConnectionID, err)
		return connections.Privileges{}, err
	}
	return connections.PrivilegesFromRunner(resp.GetPrivileges()), nil
}

// registerTargetSecrets registers a target's credentials with the log
// redaction registry until the returned function runs.
func registerTargetSecrets(reg interface{ Register([]byte) func() }, t *runnerpb.Target) func() {
	var unreg []func()
	add := func(b []byte) {
		if len(b) > 0 {
			unreg = append(unreg, reg.Register(b))
		}
	}
	add(t.GetPassword())
	add(t.GetClientCertificate().GetKeyPem())
	if st := t.GetSshTunnel(); st != nil {
		add(st.GetPassword())
		add(st.GetPrivateKeyPem())
		add(st.GetPassphrase())
	}
	return func() {
		for _, u := range unreg {
			u()
		}
	}
}

// compare reads the scratch database's statistics and compares them with
// the backup's.
func (w *TestRestoreWorker) compare(ctx context.Context, log *slog.Logger, tr sqlc.TestRestore, c sqlc.ListTestRestoreCandidatesRow,
	eng engine.Kind, restoreID uuid.UUID, scratchID, dbName string,
) (sqlc.TestRestoreStatus, string, *Comparison) {
	st, err := w.scratchStats(ctx, tr, eng, restoreID, scratchID)
	if err != nil {
		if ctx.Err() != nil {
			return TRStatusCanceled, CodeCanceled, nil
		}
		log.WarnContext(ctx, "reading the scratch statistics failed", slog.String("code", ErrorCode(err)))
		return TRStatusError, CodeStatsUnavailable, nil
	}
	canon, err := jobs.CanonicalStats(eng, dbName, st)
	if err != nil {
		return TRStatusError, CodeStatsInvalid, nil
	}
	var source json.RawMessage
	if len(c.Stats) > 0 && string(c.Stats) != "null" {
		source = c.Stats
	}
	cmp := CompareStats(eng, source, canon)
	return verdictStatus(cmp.Verdict), "", &cmp
}

// scratchStats asks the runner for the scratch database's statistics at
// the level of the backup's evidence: exact counts on PostgreSQL,
// estimates on MySQL/MariaDB (the runner enforces D-153 itself).
func (w *TestRestoreWorker) scratchStats(ctx context.Context, tr sqlc.TestRestore, eng engine.Kind, restoreID uuid.UUID, scratchID string) (*runnerpb.DumpStats, error) {
	rw := w.o.Restores
	level := runnerpb.StatsLevel_STATS_LEVEL_STANDARD
	if eng == engine.KindPostgres {
		level = runnerpb.StatsLevel_STATS_LEVEL_DEEP
	}
	t, release, err := rw.o.Targets.RestoreTarget(ctx, tr.TargetConnectionID)
	if err != nil {
		return nil, err
	}
	unreg := registerTargetSecrets(rw.o.Registry, t)
	resp, err := rw.o.Runner.ScratchStats(ctx, &runnerpb.ScratchStatsRequest{
		Ctx: &runnerpb.RunContext{RunId: restoreID.String()}, Target: t, TempId: scratchID,
		Stats: &runnerpb.StatsOptions{
			Enabled: true, Level: level, PerTableTimeoutMs: millis(w.o.PerTableTimeout), BudgetMs: millis(w.o.Budget),
		},
	})
	unreg()
	release()
	if err != nil {
		w.securityEvent(ctx, tr.TargetConnectionID, err)
		return nil, err
	}
	if resp.GetStats() == nil {
		return nil, errors.New("restore: the runner returned no statistics")
	}
	return resp.GetStats(), nil
}

func millis(d time.Duration) uint32 {
	ms := d.Milliseconds()
	switch {
	case ms <= 0:
		return 0
	case ms > int64(^uint32(0)):
		return ^uint32(0)
	default:
		return uint32(ms)
	}
}

// dropScratch drops the scratch database on a fresh bounded context (it
// runs after a cancel or timeout too) and reports whether it is gone. A
// failure is audited and alerted; the schedule worker retries it.
func (w *TestRestoreWorker) dropScratch(ctx context.Context, log *slog.Logger, tr sqlc.TestRestore, scratchID string, restoreID uuid.UUID) bool {
	err := dropScratchDatabase(ctx, w.o.Restores, tr.TargetConnectionID, scratchID, restoreID)
	if err == nil {
		return true
	}
	w.securityEvent(ctx, tr.TargetConnectionID, err)
	code := ErrorCode(err)
	log.ErrorContext(context.WithoutCancel(ctx), "dropping the scratch database failed; it will be retried", slog.String("code", code))
	svc := w.svc()
	actx, cancel := finalCtx(ctx)
	defer cancel()
	if _, aerr := svc.o.Auditor.Record(actx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionTestRestoreScratchKept, TargetType: TestRestoreAuditTarget, TargetID: tr.ID.String(),
		Details: audit.Details{"job_id": tr.JobID.String(), "target_connection_id": tr.TargetConnectionID.String(),
			"scratch_database": engine.ScratchPrefix + scratchID, "restore_id": restoreID.String(), "error_code": code},
	}); aerr != nil {
		log.ErrorContext(actx, "recording test_restore.scratch_kept failed", slog.Any("error", aerr))
	}
	svc.o.Alert(actx, Alert{Kind: AlertScratchKept, RestoreID: restoreID, JobID: tr.JobID, Details: map[string]any{
		"test_restore_id": tr.ID.String(), "scratch_database": engine.ScratchPrefix + scratchID, "error_code": code,
	}})
	return false
}

// dropScratchDatabase calls DropScratchDatabase on its own bounded context.
func dropScratchDatabase(ctx context.Context, rw *Worker, conn uuid.UUID, scratchID string, restoreID uuid.UUID) error {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	t, release, err := rw.o.Targets.RestoreTarget(dctx, conn)
	if err != nil {
		return err
	}
	_, err = rw.o.Runner.DropScratchDatabase(dctx, &runnerpb.DropScratchDatabaseRequest{
		Ctx: &runnerpb.RunContext{RunId: restoreID.String()}, Target: t, TempId: scratchID,
	})
	release()
	return err
}

// securityEvent audits and alerts pin / host-key mismatches of the target.
func (w *TestRestoreWorker) securityEvent(ctx context.Context, conn uuid.UUID, err error) {
	r := runnerpb.ErrorReason(err)
	if r != runnerpb.ReasonPinMismatch && r != runnerpb.ReasonSSHHostKey {
		return
	}
	if rerr := w.o.Restores.o.Targets.RecordRunFailure(context.WithoutCancel(ctx), conn, runOpTestRestore, err); rerr != nil {
		w.o.Logger.ErrorContext(context.WithoutCancel(ctx), "recording a connection security event failed", slog.Any("error", rerr))
	}
}

// recordUnstarted stores an item whose restore could not be created.
func (w *TestRestoreWorker) recordUnstarted(ctx context.Context, log *slog.Logger, trID, runItem uuid.UUID, dbName, scratchID, code string) {
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	err := db.Tx(fctx, w.svc().o.DB, func(q *sqlc.Queries) error {
		it, err := q.InsertTestRestoreItem(fctx, sqlc.InsertTestRestoreItemParams{
			TestRestoreID: trID, RunItemID: runItem, DbName: dbName, ScratchID: scratchID,
		})
		if err != nil {
			return err
		}
		_, err = q.FinishTestRestoreItem(fctx, sqlc.FinishTestRestoreItemParams{
			ID: it.ID, Status: TRStatusError, ErrorCode: &code, Diff: []byte("{}"), ScratchDropped: true,
		})
		return err
	})
	if err != nil {
		log.ErrorContext(fctx, "recording a test-restore database error failed", slog.Any("error", err))
	}
}

// finish records the verdict, audits, emits and alerts. It returns the
// River result (a cancel ends the job as canceled).
func (w *TestRestoreWorker) finish(ctx context.Context, log *slog.Logger, tr sqlc.TestRestore, out testOutcome) error {
	svc := w.svc()
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	var passed, warned, failed int32
	dbs := make([]map[string]any, 0, len(out.items))
	for _, it := range out.items {
		switch it.status {
		case TRStatusPassed:
			passed++
		case TRStatusWarnings:
			warned++
		case TRStatusFailed:
			failed++
		default:
		}
		d := map[string]any{"database": it.db, "status": string(it.status)}
		if it.code != "" {
			d["error_code"] = it.code
		}
		dbs = append(dbs, d)
	}
	p := sqlc.FinishTestRestoreParams{
		ID: tr.ID, Status: out.status, Databases: int32(len(out.items)), //nolint:gosec // at most maxTestDatabases
		Passed: passed, Warned: warned, Failed: failed,
	}
	if out.code != "" {
		code, msg := out.code, TestErrorMessage(out.code)
		p.ErrorCode, p.ErrorMessage = &code, &msg
	}
	if _, err := sqlc.New(svc.o.DB).FinishTestRestore(fctx, p); err != nil {
		return fmt.Errorf("restore: record test restore %s: %w", tr.ID, err)
	}
	details := audit.Details{
		"job_id": tr.JobID.String(), "target_connection_id": tr.TargetConnectionID.String(), "trigger": string(tr.Trigger),
		"status": string(out.status), "error_code": out.code, "databases": dbs,
	}
	if out.runID != nil {
		details["run_id"] = out.runID.String()
	}
	if out.privileges != nil {
		details["target_privileges"] = string(out.privileges.Status)
		details["target_privilege_reasons"] = slices.Clone(out.privileges.Reasons)
		details["privileged_target_acknowledged"] = out.acked
	}
	if _, err := svc.o.Auditor.Record(fctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionTestRestoreFinished, TargetType: TestRestoreAuditTarget, TargetID: tr.ID.String(), Details: details,
	}); err != nil {
		log.ErrorContext(fctx, "recording test_restore.finished failed", slog.Any("error", err))
	}
	ev := NotifyTestFailed
	switch out.status {
	case TRStatusPassed:
		ev = NotifyTestPassed
	case TRStatusWarnings:
		ev = NotifyTestWarnings
	default:
	}
	w.o.Notifier.EmitTestRestore(fctx, TestNotification{Event: ev, TestRestoreID: tr.ID, JobID: tr.JobID, RunID: out.runID, Status: out.status, ErrorCode: out.code})
	if out.status == TRStatusFailed || out.status == TRStatusError {
		svc.o.Alert(fctx, Alert{Kind: AlertTestRestoreFailed, JobID: tr.JobID, Details: map[string]any{
			"test_restore_id": tr.ID.String(), "status": string(out.status), "error_code": out.code, "failed_databases": int(failed),
		}})
	}
	log.InfoContext(fctx, "test restore finished", slog.String("status", string(out.status)), slog.String("code", out.code))
	if out.status == TRStatusCanceled {
		return river.JobCancel(errRestoreCanceled)
	}
	return nil
}
