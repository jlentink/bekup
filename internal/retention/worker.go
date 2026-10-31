package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// Defaults and fixed limits.
const (
	// DailyInterval is the period of the daily sweep (River periodic job).
	DailyInterval = 24 * time.Hour
	// DefaultMaxDeletions is the default per-sweep deletion cap.
	DefaultMaxDeletions = 500
	// DefaultPartialCleanupAge is the default age of leftover temporary
	// files removed by the daily sweep.
	DefaultPartialCleanupAge = 24 * time.Hour
	// maxConsecutiveFailures aborts a destination after this many failed
	// deletes in a row (unreachable or misbehaving backend).
	maxConsecutiveFailures = 3
	// maxAuditRuns bounds the run ids listed in one audit entry.
	maxAuditRuns = 50
	// finalWriteTimeout bounds the state write after a successful delete,
	// which survives the cancellation of the sweep.
	finalWriteTimeout = 30 * time.Second
	// deleteStepTimeout bounds the Stat + Delete of one object (the
	// artifact row stays locked meanwhile).
	deleteStepTimeout = 5 * time.Minute
)

// AuditTargetType is the target type of the retention audit entries (target
// id = job id); missing artifacts use target type "artifact".
const (
	AuditTargetType         = "job"
	AuditArtifactTargetType = "artifact"
)

// DB is what the worker needs (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Opener opens a destination's backend, soft-deleted ones included
// (*destinations.Service).
type Opener interface {
	Open(ctx context.Context, id uuid.UUID) (storage.Destination, error)
}

// Inserter enqueues River jobs (*queue.Client).
type Inserter = runstate.Inserter

// partialCleaner is implemented by the disk and SFTP backends.
type partialCleaner interface {
	CleanupPartials(ctx context.Context, olderThan time.Duration) (int, error)
}

// RunReaper fails the runs no worker will finish (runstate.Store; D-209):
// a run left queued or running after its last River attempt ended. The
// daily sweep calls it first, so retention then sees their files.
type RunReaper interface {
	FailAbandonedRuns(ctx context.Context) (int, error)
}

// UnrecordedScanner prunes empty directories of bekup's layout and records
// the files no artifact row references on one open destination
// (*destinations.Service; D-209).
type UnrecordedScanner interface {
	ScanUnrecorded(ctx context.Context, id uuid.UUID, d storage.Destination) error
}

// Options configure New.
type Options struct {
	DB      DB
	Auditor *audit.Auditor
	// Destinations opens the backends.
	Destinations Opener
	// Queue enqueues the per-job sweeps of the daily fan-out. It may be
	// resolved late (the queue client is built after the workers).
	Queue func() Inserter
	// Publisher sends run events for artifact state changes (nil: NOTIFY
	// only).
	Publisher *pubsub.Publisher
	// Alerts receives unexpected deletions and aborted sweeps (nil: WARN
	// logs; the security alerts come from their audit entries).
	Alerts AlertFunc
	// DryRun forces every sweep to only plan (retention.dry_run).
	DryRun bool
	// MaxDeletions caps the deletes of one job sweep
	// (retention.max_deletions_per_sweep).
	MaxDeletions int
	// PartialCleanupAge is retention.partial_cleanup_age.
	PartialCleanupAge time.Duration
	// Runs fails abandoned runs before the daily fan-out (nil: none).
	Runs RunReaper
	// Unrecorded scans every non-deleted destination daily (nil: none).
	Unrecorded UnrecordedScanner
	// Metrics counts the outcome of every job sweep (nil: none; task 5.6).
	Metrics Metrics
	Logger  *slog.Logger
	Now     func() time.Time
}

// Metrics receives the result of every finished job sweep (also a failed
// one: what was deleted stays deleted). It must be fast and must not fail.
type Metrics interface {
	SweepFinished(r Result)
}

// NopMetrics discards everything.
type NopMetrics struct{}

// SweepFinished implements Metrics.
func (NopMetrics) SweepFinished(Result) {}

// Worker works retention_sweep jobs; see the package documentation.
type Worker struct {
	river.WorkerDefaults[queue.RetentionSweepArgs]
	o Options
	// limits are the retention.* settings; SetLimits swaps them at runtime
	// (config reload, D-200). Each sweep reads them once.
	limits atomic.Pointer[Limits]
}

// Limits are the retention.* settings a config reload may change (D-200).
type Limits struct {
	// DryRun forces every sweep to only plan (retention.dry_run).
	DryRun bool
	// MaxDeletions caps the deletes of one job sweep (> 0).
	MaxDeletions int
	// PartialCleanupAge is retention.partial_cleanup_age (> 0).
	PartialCleanupAge time.Duration
}

// CurrentLimits returns the settings in effect.
func (w *Worker) CurrentLimits() Limits { return *w.limits.Load() }

// PrepareLimits validates l and returns the commit that applies it (the
// commit only does an atomic store; a running sweep keeps the values it
// started with).
func (w *Worker) PrepareLimits(l Limits) (func(), error) {
	if l.MaxDeletions <= 0 || l.PartialCleanupAge <= 0 {
		return nil, errors.New("retention: MaxDeletions and PartialCleanupAge must be positive")
	}
	return func() { w.limits.Store(&l) }, nil
}

// New validates o.
func New(o Options) (*Worker, error) {
	if o.DB == nil || o.Auditor == nil || o.Destinations == nil {
		return nil, errors.New("retention: DB, Auditor and Destinations are required")
	}
	if o.MaxDeletions <= 0 {
		o.MaxDeletions = DefaultMaxDeletions
	}
	if o.PartialCleanupAge <= 0 {
		o.PartialCleanupAge = DefaultPartialCleanupAge
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Alerts == nil {
		o.Alerts = logAlert(o.Logger)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Publisher == nil {
		o.Publisher = &pubsub.Publisher{}
	}
	if o.Metrics == nil {
		o.Metrics = NopMetrics{}
	}
	w := &Worker{o: o}
	w.limits.Store(&Limits{DryRun: o.DryRun, MaxDeletions: o.MaxDeletions, PartialCleanupAge: o.PartialCleanupAge})
	return w, nil
}

// Register registers the worker and its daily periodic fan-out.
func Register(ws *queue.Workers, w *Worker) error {
	if err := queue.Register(ws, w); err != nil {
		return err
	}
	return ws.AddPeriodic(DailyInterval, queue.RetentionSweepArgs{Reason: queue.RetentionDaily})
}

// PostRunHook is the runstate finish hook that enqueues a post-run sweep of
// the run's job in the run's transaction. A sweep of the job that is still
// queued or running makes it a no-op (the daily sweep catches up).
func PostRunHook(ins func() Inserter) runstate.FinishHook {
	return func(ctx context.Context, tx pgx.Tx, r runstate.Ref, _ sqlc.RunStatus) error {
		q := ins()
		if q == nil {
			return errors.New("retention: no queue to enqueue the post-run sweep")
		}
		id := r.JobID
		_, err := q.InsertTx(ctx, tx, queue.RetentionSweepArgs{JobID: &id, Reason: queue.RetentionPostRun}, queue.InsertOptions{})
		return err
	}
}

// Work implements river.Worker.
func (w *Worker) Work(ctx context.Context, job *river.Job[queue.RetentionSweepArgs]) error {
	a := job.Args
	// One snapshot per sweep: a reload in between never mixes the dry-run
	// flag of one config with the deletion cap of another.
	lim := w.CurrentLimits()
	dry := lim.DryRun || a.DryRun
	if a.JobID == nil {
		return w.daily(ctx, dry, lim)
	}
	res, err := w.sweep(ctx, *a.JobID, a.Reason, dry, lim)
	w.o.Metrics.SweepFinished(res)
	return err
}

// Daily enqueues a sweep for every job with present artifacts and removes
// leftover temporary files (older than PartialCleanupAge) on disk and SFTP
// destinations (skipped in a dry run).
func (w *Worker) Daily(ctx context.Context, dryRun bool) error {
	return w.daily(ctx, dryRun, w.CurrentLimits())
}

func (w *Worker) daily(ctx context.Context, dryRun bool, lim Limits) error {
	if w.o.Runs != nil && !dryRun {
		// Runs no worker will finish become failed (interrupted) first, so
		// the per-job sweeps below see their files.
		n, err := w.o.Runs.FailAbandonedRuns(ctx)
		if err != nil {
			w.o.Logger.WarnContext(ctx, "failing abandoned runs failed", slog.Int("failed", n), slog.Any("error", err))
		} else if n > 0 {
			w.o.Logger.InfoContext(ctx, "abandoned runs marked interrupted", slog.Int("runs", n))
		}
	}
	q := sqlc.New(w.o.DB)
	if n, err := q.DeleteStaleRetentionHolds(ctx); err != nil {
		w.o.Logger.WarnContext(ctx, "dropping stale retention holds failed", slog.Any("error", err))
	} else if n > 0 {
		w.o.Logger.InfoContext(ctx, "stale retention holds dropped", slog.Int64("holds", n))
	}
	ids, err := q.ListRetentionJobIDs(ctx)
	if err != nil {
		return fmt.Errorf("retention: list jobs: %w", err)
	}
	if len(ids) > 0 {
		var ins Inserter
		if w.o.Queue != nil {
			ins = w.o.Queue()
		}
		if ins == nil {
			return errors.New("retention: the daily sweep needs the queue")
		}
		err := db.TxWith(ctx, w.o.DB, func(tx pgx.Tx, _ *sqlc.Queries) error {
			for _, id := range ids {
				if _, err := ins.InsertTx(ctx, tx, queue.RetentionSweepArgs{JobID: &id, Reason: queue.RetentionDaily, DryRun: dryRun}, queue.InsertOptions{}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("retention: enqueue daily sweeps: %w", err)
		}
	}
	w.o.Logger.InfoContext(ctx, "daily retention sweeps enqueued", slog.Int("jobs", len(ids)), slog.Bool("dry_run", dryRun))
	if dryRun {
		return nil
	}
	return w.maintainDestinations(ctx, lim.PartialCleanupAge)
}

// maxDestinationBudget bounds the daily maintenance of one destination
// when the attempt has no deadline of its own.
const maxDestinationBudget = 30 * time.Minute

// destinationBudget is the context of one destination's daily maintenance:
// an equal share of the attempt's remaining time among the destinations
// left (time a fast destination leaves over goes to the later ones), so a
// slow or hanging destination never starves the rest (D-209).
func destinationBudget(ctx context.Context, left int) (context.Context, context.CancelFunc) {
	if dl, ok := ctx.Deadline(); ok && left > 0 {
		return context.WithTimeout(ctx, time.Until(dl)/time.Duration(left))
	}
	return context.WithTimeout(ctx, maxDestinationBudget)
}

// maintainDestinations removes leftover temporary files (disk and SFTP,
// deleted destinations with present artifacts included) and runs the
// unrecorded-file scan with empty-directory pruning on every non-deleted
// destination (D-209), each within its share of the time
// (destinationBudget). A destination that fails is logged and skipped.
func (w *Worker) maintainDestinations(ctx context.Context, age time.Duration) error {
	rows, err := sqlc.New(w.o.DB).ListDailyMaintenanceDestinations(ctx)
	if err != nil {
		return fmt.Errorf("retention: list destinations: %w", err)
	}
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		dctx, cancel := destinationBudget(ctx, len(rows)-i)
		w.maintainDestination(dctx, row, age)
		cancel()
	}
	return nil
}

// maintainDestination is the daily maintenance of one destination.
func (w *Worker) maintainDestination(ctx context.Context, row sqlc.ListDailyMaintenanceDestinationsRow, age time.Duration) {
	scan := w.o.Unrecorded != nil && !row.Deleted
	partials := row.Type == sqlc.DestinationTypeDisk || row.Type == sqlc.DestinationTypeSftp
	if !scan && !partials {
		return
	}
	log := w.o.Logger.With(slog.String("destination_id", row.ID.String()))
	d, err := w.o.Destinations.Open(ctx, row.ID)
	if err != nil {
		log.WarnContext(ctx, "daily destination maintenance: destination unavailable", slog.String("code", string(storage.Classify(err))))
		return
	}
	if c, ok := d.(partialCleaner); ok && partials {
		n, err := c.CleanupPartials(ctx, age)
		switch {
		case err != nil:
			log.WarnContext(ctx, "partial cleanup failed", slog.Int("removed", n), slog.String("code", string(storage.Classify(err))))
		case n > 0:
			log.InfoContext(ctx, "leftover temporary files removed", slog.Int("removed", n))
		}
	}
	if scan {
		if err := w.o.Unrecorded.ScanUnrecorded(ctx, row.ID, d); err != nil {
			log.WarnContext(ctx, "unrecorded-file scan failed", slog.String("code", string(storage.Classify(err))))
		}
	}
	closeQuietly(ctx, log, d)
}

func closeQuietly(ctx context.Context, log *slog.Logger, d storage.Destination) {
	if err := d.Close(); err != nil {
		log.DebugContext(ctx, "closing destination failed", slog.String("code", string(storage.Classify(err))))
	}
}

// Result is the outcome of one job sweep.
type Result struct {
	JobID        uuid.UUID
	DryRun       bool
	Destinations []DestinationResult
}

// DestinationResult is the outcome on one destination.
type DestinationResult struct {
	DestinationID uuid.UUID
	Plan          DestinationPlan
	// Planned is the number of eligible objects.
	Planned int
	Deleted int
	// Missing objects vanished without bekup deleting them.
	Missing int
	// Locked deletes were refused by an Object Lock.
	Locked int
	// Held artifacts were kept because a download or restore holds them
	// (retried by the next sweep).
	Held int
	// LockedUntil is the latest retain-until seen on a refused delete.
	LockedUntil time.Time
	Failed      int
	// SkippedNoDelete: the destination is in no-delete mode.
	SkippedNoDelete bool
	// Capped: the per-sweep deletion cap was reached.
	Capped bool
	// MaxDeletions is the per-sweep cap the sweep started with.
	MaxDeletions int
	// Aborted is a fixed reason when the destination was skipped by a
	// safety check or a backend failure ("" otherwise).
	Aborted     string
	DeletedRuns []uuid.UUID
}

// Abort reasons.
const (
	AbortPlanInvariant     = "plan_invariant_violated"
	AbortNewestMissing     = "newest_complete_missing"
	AbortNewestChanged     = "newest_complete_changed"
	AbortNewestUnverified  = "newest_complete_unverifiable"
	AbortUnavailable       = "destination_unavailable"
	AbortTooManyFailures   = "too_many_failures"
	AbortCanceled          = "canceled"
	abortAuditFailedOnPlan = "audit_failed"
)

// Sweep applies the job's retention on every destination holding its
// artifacts. A missing job is a no-op.
func (w *Worker) Sweep(ctx context.Context, jobID uuid.UUID, reason queue.RetentionReason, dryRun bool) (Result, error) {
	return w.sweep(ctx, jobID, reason, dryRun, w.CurrentLimits())
}

// sweep is Sweep with the limits snapshot of the run.
func (w *Worker) sweep(ctx context.Context, jobID uuid.UUID, reason queue.RetentionReason, dryRun bool, lim Limits) (Result, error) {
	res := Result{JobID: jobID, DryRun: dryRun}
	q := sqlc.New(w.o.DB)
	job, err := q.GetJobRetention(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("retention: load job: %w", err)
	}
	in, err := w.input(ctx, q, &job)
	if err != nil {
		return res, err
	}
	log := w.o.Logger.With(slog.String("job_id", jobID.String()), slog.String("reason", string(reason)), slog.Bool("dry_run", dryRun))
	if job.DeletedAt != nil {
		log = log.With(slog.Bool("job_deleted", true))
	}
	maxDeletions := lim.MaxDeletions
	budget := maxDeletions
	plans := Plan(in)
	if err := w.recordHolds(ctx, &job, plans); err != nil {
		// The holds are informational: deletions go on.
		log.WarnContext(ctx, "retention: recording the retention holds failed", slog.Any("error", err))
	}
	for _, p := range plans {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		dr, err := w.sweepDestination(ctx, log.With(slog.String("destination_id", p.DestinationID.String())), jobID, reason, dryRun, p, &budget, maxDeletions)
		res.Destinations = append(res.Destinations, dr)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func (w *Worker) input(ctx context.Context, q *sqlc.Queries, job *sqlc.GetJobRetentionRow) (Input, error) {
	in := Input{Now: w.o.Now()}
	if job.RetentionKeepLast != nil {
		in.Policy.KeepLast = int(*job.RetentionKeepLast)
	}
	if job.RetentionKeepDays != nil {
		in.Policy.KeepDays = int(*job.RetentionKeepDays)
	}
	arts, err := q.ListRetentionArtifacts(ctx, job.ID)
	if err != nil {
		return in, fmt.Errorf("retention: list artifacts: %w", err)
	}
	items, err := q.ListRetentionRunItems(ctx, job.ID)
	if err != nil {
		return in, fmt.Errorf("retention: list run items: %w", err)
	}
	runs := map[uuid.UUID]*Run{}
	for i := range arts {
		a := &arts[i]
		r := runs[a.RunID]
		if r == nil {
			r = &Run{ID: a.RunID, Time: a.RunTime}
			runs[a.RunID] = r
		}
		art := Artifact{
			ID: a.ID, RunID: a.RunID, DestinationID: a.DestinationID, ItemID: a.RunItemID,
			Key: a.ObjectKey, Size: a.Size, Verified: a.Verified,
		}
		if a.Kind == sqlc.ArtifactKindManifest {
			art.ItemID = nil
		} else if art.ItemID == nil {
			continue // inconsistent row (CHECK forbids it): never touched
		}
		if a.LockUntil != nil {
			art.LockUntil = *a.LockUntil
		}
		in.Artifacts = append(in.Artifacts, art)
	}
	for _, it := range items {
		if r := runs[it.RunID]; r != nil {
			r.Items = append(r.Items, it.ID)
		}
	}
	for _, r := range runs {
		in.Runs = append(in.Runs, *r)
	}
	sort.Slice(in.Runs, func(i, j int) bool { return in.Runs[i].ID.String() < in.Runs[j].ID.String() })
	return in, nil
}

func (w *Worker) sweepDestination(ctx context.Context, log *slog.Logger, jobID uuid.UUID, reason queue.RetentionReason,
	dryRun bool, p DestinationPlan, budget *int, maxDeletions int,
) (DestinationResult, error) {
	dr := DestinationResult{DestinationID: p.DestinationID, Plan: p, Planned: p.DeleteCount(), MaxDeletions: maxDeletions}
	if !checkPlan(p) {
		dr.Aborted = AbortPlanInvariant
		log.ErrorContext(ctx, "retention plan violates its invariants; nothing deleted on this destination")
		err := w.recordAbort(ctx, jobID, &dr, reason)
		return dr, err
	}
	if dr.Planned == 0 {
		return dr, nil
	}
	if dryRun {
		w.logPlan(ctx, log, p)
		return dr, nil
	}
	d, err := w.o.Destinations.Open(ctx, p.DestinationID)
	if err != nil {
		dr.Aborted = AbortUnavailable
		log.WarnContext(ctx, "retention: destination unavailable; nothing deleted there", slog.String("code", string(storage.Classify(err))))
		return dr, nil
	}
	defer closeQuietly(ctx, log, d)
	if d.Capabilities().NoDeleteMode {
		dr.SkippedNoDelete = true
		log.InfoContext(ctx, "retention: destination in no-delete mode; deletion left to its lifecycle rules",
			slog.Int("eligible_objects", dr.Planned))
		return dr, nil
	}
	if abort := w.checkNewest(ctx, log, jobID, d, p, &dr); abort != "" {
		dr.Aborted = abort
		if abort == AbortNewestUnverified {
			return dr, nil // transient (logged); the next sweep retries
		}
		err := w.recordAbort(ctx, jobID, &dr, reason)
		return dr, err
	}
	if err := w.recordPlanned(ctx, jobID, reason, &dr); err != nil {
		dr.Aborted = abortAuditFailedOnPlan
		return dr, err
	}
	w.execute(ctx, log, jobID, d, p, &dr, budget)
	if err := w.recordSwept(ctx, jobID, reason, &dr); err != nil {
		return dr, err
	}
	log.InfoContext(ctx, "retention sweep of destination finished",
		slog.Int("planned", dr.Planned), slog.Int("deleted", dr.Deleted), slog.Int("missing", dr.Missing),
		slog.Int("locked", dr.Locked), slog.Int("held", dr.Held), slog.Int("failed", dr.Failed), slog.Bool("capped", dr.Capped), slog.String("aborted", dr.Aborted))
	return dr, nil
}

func (w *Worker) logPlan(ctx context.Context, log *slog.Logger, p DestinationPlan) {
	log.InfoContext(ctx, "retention dry run: plan computed, nothing deleted",
		slog.String("newest_complete_run", p.NewestComplete.String()), slog.Int("complete_runs", len(p.Complete)),
		slog.Int("kept_runs", len(p.Keep)), slog.Int("eligible_runs", len(p.Delete)), slog.Int("eligible_objects", p.DeleteCount()))
	for _, d := range p.Delete {
		log.InfoContext(ctx, "retention dry run: run would be deleted", slog.String("run_id", d.RunID.String()),
			slog.Time("run_time", d.Time), slog.Bool("complete", d.Complete), slog.Int("objects", d.Objects()))
	}
}

// checkNewest confirms that every artifact of the newest complete run still
// exists with its recorded size before anything is deleted. A missing one is
// marked missing (audited, alerted); any doubt aborts the destination.
func (w *Worker) checkNewest(ctx context.Context, log *slog.Logger, jobID uuid.UUID, d storage.Destination, p DestinationPlan, dr *DestinationResult) string {
	if p.NewestComplete == uuid.Nil || len(p.NewestArtifacts) == 0 {
		return AbortPlanInvariant
	}
	abort := ""
	for _, a := range p.NewestArtifacts {
		key, err := storage.ParseKey(a.Key)
		if err != nil {
			log.ErrorContext(ctx, "retention: stored artifact key is invalid", slog.String("artifact_id", a.ID.String()))
			return AbortNewestUnverified
		}
		info, err := d.Stat(ctx, key)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			if err := w.markMissing(ctx, log, jobID, a, true); err != nil {
				return AbortNewestUnverified
			}
			dr.Missing++
			abort = AbortNewestMissing
		case err != nil:
			log.WarnContext(ctx, "retention: cannot confirm the newest complete backup; nothing deleted there",
				slog.String("artifact_id", a.ID.String()), slog.String("code", string(storage.Classify(err))))
			return AbortNewestUnverified
		case info.Size != a.Size:
			log.ErrorContext(ctx, "retention: an object of the newest complete backup changed size; nothing deleted there",
				slog.String("artifact_id", a.ID.String()), slog.Int64("recorded", a.Size), slog.Int64("found", info.Size))
			if abort == "" {
				abort = AbortNewestChanged
			}
		}
	}
	return abort
}

type outcome int

const (
	outDeleted outcome = iota
	outMissing
	outLocked
	outFailed
	// outHeld: an unexpired hold protects the artifact (kept).
	outHeld
	// outGone: recorded as deleted or missing meanwhile (by another sweep).
	outGone
)

func (w *Worker) execute(ctx context.Context, log *slog.Logger, jobID uuid.UUID, d storage.Destination, p DestinationPlan, dr *DestinationResult, budget *int) {
	failures := 0
	for _, rd := range p.Delete {
		gone := 0
		runDeleted := false
		for _, a := range rd.Dumps {
			if stop := w.stopReason(ctx, budget, failures); stop != "" {
				w.stop(dr, stop)
				return
			}
			o := w.deleteOne(ctx, log, jobID, d, a, dr, budget)
			failures = tally(o, failures)
			if o == outDeleted || o == outMissing || o == outGone {
				gone++
				runDeleted = runDeleted || o == outDeleted
			}
		}
		// The manifest goes only once every dump of the run is gone from
		// this destination (it is the run's index there).
		if rd.Manifest != nil && gone == len(rd.Dumps) {
			if stop := w.stopReason(ctx, budget, failures); stop != "" {
				w.stop(dr, stop)
				return
			}
			o := w.deleteOne(ctx, log, jobID, d, *rd.Manifest, dr, budget)
			failures = tally(o, failures)
			runDeleted = runDeleted || o == outDeleted
		}
		if runDeleted {
			dr.DeletedRuns = append(dr.DeletedRuns, rd.RunID)
		}
	}
}

func tally(o outcome, failures int) int {
	if o == outFailed {
		return failures + 1
	}
	return 0
}

func (w *Worker) stopReason(ctx context.Context, budget *int, failures int) string {
	switch {
	case ctx.Err() != nil:
		return AbortCanceled
	case failures >= maxConsecutiveFailures:
		return AbortTooManyFailures
	case *budget <= 0:
		return "capped"
	}
	return ""
}

func (w *Worker) stop(dr *DestinationResult, why string) {
	if why == "capped" {
		dr.Capped = true
		w.o.Logger.Warn("retention: deletion cap reached; the rest waits for the next sweep",
			slog.String("destination_id", dr.DestinationID.String()), slog.Int("max_deletions_per_sweep", dr.MaxDeletions))
		return
	}
	dr.Aborted = why
}

// deleteOne deletes one object: a Stat first tells an unexpected deletion
// (missing) from an ordinary one (Delete itself is idempotent). The whole
// step runs under a lock of the artifact row (runstate.LockForDeletionTx):
// an artifact with an unexpired hold (a download or restore reading it) is
// skipped and retried by the next sweep, and no hold can be taken while the
// object is being deleted.
func (w *Worker) deleteOne(ctx context.Context, log *slog.Logger, jobID uuid.UUID, d storage.Destination, a Artifact, dr *DestinationResult, budget *int) outcome {
	alog := log.With(slog.String("artifact_id", a.ID.String()), slog.String("run_id", a.RunID.String()))
	key, err := storage.ParseKey(a.Key)
	if err != nil {
		alog.ErrorContext(ctx, "retention: stored artifact key is invalid")
		dr.Failed++
		return outFailed
	}
	// The DB side survives the sweep's cancellation (an object that is gone
	// must be recorded); the storage calls end with the sweep.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deleteStepTimeout+finalWriteTimeout)
	defer cancel()
	opCtx, opCancel := context.WithTimeout(ctx, deleteStepTimeout)
	defer opCancel()
	o := outFailed
	err = db.Tx(fctx, w.o.DB, func(q *sqlc.Queries) error {
		if err := runstate.LockForDeletionTx(fctx, q, a.ID); err != nil {
			return err
		}
		o = w.removeObject(opCtx, alog, d, key, dr, budget)
		switch o {
		case outDeleted:
			_, err := runstate.MarkArtifactDeletedTx(fctx, q, w.o.Publisher, a.ID, w.o.Now())
			return err
		case outMissing:
			return w.markMissingTx(fctx, q, jobID, a, false)
		case outLocked, outFailed, outHeld, outGone:
			return nil // counted by removeObject (locked, failed); nothing to record
		}
		return nil
	})
	switch {
	case errors.Is(err, runstate.ErrArtifactHeld):
		dr.Held++
		alog.InfoContext(ctx, "retention: the artifact is in use (download or restore); kept until a later sweep")
		return outHeld
	case errors.Is(err, runstate.ErrArtifactNotPresent):
		// Recorded as deleted or missing meanwhile: nothing left to do.
		return outGone
	case err != nil && o == outDeleted:
		// The object is gone but its state could not be recorded.
		alog.ErrorContext(ctx, "retention: object deleted but its state could not be recorded", slog.Any("error", err))
		dr.Deleted++
		return outDeleted
	case err != nil:
		alog.ErrorContext(ctx, "retention: recording the outcome failed", slog.Any("error", err))
		if o != outFailed && o != outLocked {
			dr.Failed++ // not counted by removeObject yet
		}
		return outFailed
	}
	switch o {
	case outDeleted:
		dr.Deleted++
	case outMissing:
		dr.Missing++
		w.alertMissing(ctx, alog, jobID, a, false)
	case outLocked, outFailed, outHeld, outGone:
	}
	return o
}

// removeObject stats and deletes one object (no database writes). It
// returns outDeleted, outMissing (vanished without bekup deleting it),
// outLocked (Object Lock) or outFailed; locked and failed are counted.
func (w *Worker) removeObject(ctx context.Context, alog *slog.Logger, d storage.Destination, key storage.Key, dr *DestinationResult, budget *int) outcome {
	info, err := d.Stat(ctx, key)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return outMissing
	case err != nil:
		alog.WarnContext(ctx, "retention: stat failed", slog.String("code", string(storage.Classify(err))))
		dr.Failed++
		return outFailed
	}
	*budget--
	err = d.Delete(ctx, key)
	switch {
	case err == nil:
		return outDeleted
	case errors.Is(err, storage.ErrDeleteNotPermitted):
		dr.Locked++
		if info.RetainUntil.After(dr.LockedUntil) {
			dr.LockedUntil = info.RetainUntil
		}
		alog.InfoContext(ctx, "retention: delete refused by an object lock; the object is kept",
			slog.Time("retain_until", info.RetainUntil), slog.Bool("legal_hold", info.LegalHold), slog.String("lock_mode", string(info.LockMode)))
		return outLocked
	case errors.Is(err, storage.ErrNotFound):
		return outMissing
	default:
		alog.WarnContext(ctx, "retention: delete failed", slog.String("code", string(storage.Classify(err))))
		dr.Failed++
		return outFailed
	}
}

// markMissing records an unexpected deletion: state missing, an audit entry
// in the same transaction (fail closed) and an alert.
func (w *Worker) markMissing(ctx context.Context, log *slog.Logger, jobID uuid.UUID, a Artifact, newest bool) error {
	err := db.Tx(ctx, w.o.DB, func(q *sqlc.Queries) error {
		return w.markMissingTx(ctx, q, jobID, a, newest)
	})
	if errors.Is(err, runstate.ErrArtifactNotPresent) {
		return nil
	}
	if err != nil {
		log.ErrorContext(ctx, "retention: recording a missing object failed", slog.Any("error", err))
		return err
	}
	w.alertMissing(ctx, log, jobID, a, newest)
	return nil
}

// markMissingTx records state missing and its audit entry in q's
// transaction (ErrArtifactNotPresent when it is no longer present).
func (w *Worker) markMissingTx(ctx context.Context, q *sqlc.Queries, jobID uuid.UUID, a Artifact, newest bool) error {
	if _, err := runstate.MarkArtifactMissingTx(ctx, q, w.o.Publisher, a.ID, w.o.Now()); err != nil {
		return err
	}
	_, err := w.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionRetentionArtifactMissing,
		TargetType: AuditArtifactTargetType, TargetID: a.ID.String(),
		Details: audit.Details{
			"job_id": jobID.String(), "run_id": a.RunID.String(), "destination_id": a.DestinationID.String(),
			"object": a.Key, "newest_complete": newest,
		},
	})
	return err
}

// alertMissing logs and alerts a recorded unexpected deletion.
func (w *Worker) alertMissing(ctx context.Context, log *slog.Logger, jobID uuid.UUID, a Artifact, newest bool) {
	log.WarnContext(ctx, "retention: object missing from the destination (deleted outside bekup)", slog.Bool("newest_complete", newest))
	w.o.Alerts(ctx, Alert{
		Kind: AlertArtifactMissing, JobID: jobID, DestinationID: a.DestinationID, RunID: a.RunID, ArtifactID: a.ID,
		Details: map[string]any{"newest_complete": newest},
	})
}

func runIDs(ids []uuid.UUID) (out []string, more int) {
	for i, id := range ids {
		if i == maxAuditRuns {
			return out, len(ids) - maxAuditRuns
		}
		out = append(out, id.String())
	}
	return out, 0
}

// recordPlanned audits the planned deletions before the first delete; a
// failure aborts the destination (fail closed).
func (w *Worker) recordPlanned(ctx context.Context, jobID uuid.UUID, reason queue.RetentionReason, dr *DestinationResult) error {
	ids := make([]uuid.UUID, 0, len(dr.Plan.Delete))
	for _, d := range dr.Plan.Delete {
		ids = append(ids, d.RunID)
	}
	runs, more := runIDs(ids)
	_, err := w.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionRetentionDeletionPlanned,
		TargetType: AuditTargetType, TargetID: jobID.String(),
		Details: audit.Details{
			"destination_id": dr.DestinationID.String(), "reason": string(reason),
			"newest_complete_run": dr.Plan.NewestComplete.String(), "kept_runs": len(dr.Plan.Keep),
			"eligible_runs": runs, "eligible_runs_more": more, "eligible_objects": dr.Planned,
			"max_deletions": dr.MaxDeletions,
		},
	})
	if err != nil {
		return fmt.Errorf("retention: audit planned deletions: %w", err)
	}
	return nil
}

// recordSwept audits the outcome of a destination sweep.
func (w *Worker) recordSwept(ctx context.Context, jobID uuid.UUID, reason queue.RetentionReason, dr *DestinationResult) error {
	runs, more := runIDs(dr.DeletedRuns)
	details := audit.Details{
		"destination_id": dr.DestinationID.String(), "reason": string(reason),
		"deleted": dr.Deleted, "deleted_runs": runs, "deleted_runs_more": more,
		"missing": dr.Missing, "locked": dr.Locked, "held": dr.Held, "failed": dr.Failed, "capped": dr.Capped,
	}
	if !dr.LockedUntil.IsZero() {
		details["locked_until_max"] = dr.LockedUntil.UTC().Format(time.RFC3339)
	}
	if dr.Aborted != "" {
		details["aborted"] = dr.Aborted
	}
	// The outcome is recorded even if the sweep's context ended.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalWriteTimeout)
	defer cancel()
	if _, err := w.o.Auditor.Record(fctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionRetentionSwept,
		TargetType: AuditTargetType, TargetID: jobID.String(), Details: details,
	}); err != nil {
		return fmt.Errorf("retention: audit sweep: %w", err)
	}
	return nil
}

// recordAbort audits and alerts a destination skipped by a safety check.
func (w *Worker) recordAbort(ctx context.Context, jobID uuid.UUID, dr *DestinationResult, reason queue.RetentionReason) error {
	w.o.Alerts(ctx, Alert{
		Kind: AlertSweepAborted, JobID: jobID, DestinationID: dr.DestinationID,
		Details: map[string]any{"aborted": dr.Aborted},
	})
	if _, err := w.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionRetentionAborted,
		TargetType: AuditTargetType, TargetID: jobID.String(),
		Details: audit.Details{
			"destination_id": dr.DestinationID.String(), "reason": string(reason), "aborted": dr.Aborted,
			"newest_complete_run": dr.Plan.NewestComplete.String(), "missing": dr.Missing,
		},
	}); err != nil {
		return fmt.Errorf("retention: audit aborted sweep: %w", err)
	}
	return nil
}
