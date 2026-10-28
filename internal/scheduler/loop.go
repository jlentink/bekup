package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
)

// SkipReason is the reason of a schedule_skips row.
type SkipReason string

// Skip reasons (schedule_skips.reason).
const (
	// SkipStillRunning: the previous run of the job is still queued,
	// running or awaiting a retry; the occurrence is not enqueued.
	SkipStillRunning SkipReason = "still_running"
	// SkipUnschedulable: the stored schedule does not compile or has no
	// further occurrence; next_run_at is reset to NULL.
	SkipUnschedulable SkipReason = "unschedulable"
)

// Loop defaults and bounds.
const (
	DefaultTickInterval = 15 * time.Second
	DefaultBatchSize    = 100
	// maxBatchesPerTick bounds one tick; the rest waits for the next.
	maxBatchesPerTick = 20
	// tickTimeout bounds one tick's transactions.
	tickTimeout = 30 * time.Second
)

// Inserter inserts River jobs inside a transaction (*queue.Client).
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error)
}

// Metrics receives scheduler events (Prometheus in task 5.6).
type Metrics interface {
	// TickCompleted is called after every tick with its duration and error.
	TickCompleted(d time.Duration, err error)
	// Enqueued counts an enqueued run.
	Enqueued(trigger queue.Trigger)
	// Skipped counts a skipped occurrence.
	Skipped(reason SkipReason)
}

// NopMetrics discards everything.
type NopMetrics struct{}

// TickCompleted implements Metrics.
func (NopMetrics) TickCompleted(time.Duration, error) {}

// Enqueued implements Metrics.
func (NopMetrics) Enqueued(queue.Trigger) {}

// Skipped implements Metrics.
func (NopMetrics) Skipped(SkipReason) {}

// LoopOptions configure NewLoop.
type LoopOptions struct {
	// DB is the app pool (bekup_app).
	DB db.TxBeginner
	// Queue inserts the backup_run jobs.
	Queue Inserter
	// Logger is the redacting process logger.
	Logger *slog.Logger
	// Interval between ticks (scheduler.tick_interval).
	Interval time.Duration
	// BatchSize is the number of due jobs locked per transaction.
	BatchSize int
	// Now is the clock (tests); nil = time.Now.
	Now func() time.Time
	// Metrics; nil = NopMetrics.
	Metrics Metrics
	// Publisher sends the run events of the queued runs (nil: NOTIFY
	// only).
	Publisher *pubsub.Publisher
}

// Loop is the multi-instance-safe scheduler loop (D-044, D-145). Every tick
// locks due jobs with FOR UPDATE SKIP LOCKED, enqueues at most one
// backup_run per job (missed occurrences collapse into one catch-up run),
// records skipped occurrences and advances next_run_at, all in one
// transaction per batch. Running it on several instances is safe.
type Loop struct {
	db       db.TxBeginner
	queue    Inserter
	logger   *slog.Logger
	interval time.Duration
	batch    int
	now      func() time.Time
	metrics  Metrics
	pub      *pubsub.Publisher
}

// NewLoop validates opts.
func NewLoop(opts LoopOptions) (*Loop, error) {
	if opts.DB == nil || opts.Queue == nil || opts.Logger == nil {
		return nil, errors.New("scheduler: DB, Queue and Logger are required")
	}
	l := &Loop{db: opts.DB, queue: opts.Queue, logger: opts.Logger, interval: opts.Interval,
		batch: opts.BatchSize, now: opts.Now, metrics: opts.Metrics, pub: opts.Publisher}
	if l.interval <= 0 {
		l.interval = DefaultTickInterval
	}
	if l.batch <= 0 {
		l.batch = DefaultBatchSize
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.metrics == nil {
		l.metrics = NopMetrics{}
	}
	return l, nil
}

// Name implements the app component interface.
func (l *Loop) Name() string { return "scheduler" }

// Run ticks immediately and then every Interval until ctx is canceled. A
// tick interrupted by shutdown rolls back and is redone by the next tick
// of any instance. Tick errors are logged; the loop keeps going.
func (l *Loop) Run(ctx context.Context) error {
	l.logger.InfoContext(ctx, "scheduler started", slog.Duration("interval", l.interval), slog.Int("batch_size", l.batch))
	t := time.NewTicker(l.interval)
	defer t.Stop()
	for {
		l.runTick(ctx)
		select {
		case <-ctx.Done():
			l.logger.InfoContext(context.WithoutCancel(ctx), "scheduler stopped")
			return nil
		case <-t.C:
		}
	}
}

func (l *Loop) runTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()
	res, err := l.Tick(tctx)
	if err != nil && ctx.Err() == nil {
		l.logger.WarnContext(ctx, "scheduler tick failed", slog.Any("error", err))
	}
	if res.Enqueued+res.Skipped+res.Failed > 0 {
		l.logger.InfoContext(ctx, "scheduler tick", slog.Int("enqueued", res.Enqueued),
			slog.Int("skipped", res.Skipped), slog.Int("failed", res.Failed), slog.Int("due", res.Due))
	}
}

// TickResult summarizes a tick.
type TickResult struct {
	// Due is the number of due jobs handled.
	Due int
	// Enqueued runs (CatchUp of them were catch-up runs).
	Enqueued int
	CatchUp  int
	// Skipped occurrences (still running or unschedulable).
	Skipped int
	// Failed jobs: their handling failed and was rolled back (retried next
	// tick).
	Failed int
}

// Tick handles the due jobs once, in batches of BatchSize, each batch in
// one transaction and each job under its own savepoint: a job whose
// handling fails is rolled back alone, logged, left due and skipped for
// the rest of the tick (the next tick retries it), so one bad job never
// blocks the others.
func (l *Loop) Tick(ctx context.Context) (TickResult, error) {
	start := time.Now()
	var total TickResult
	var err error
	failed := map[uuid.UUID]bool{}
	for range maxBatchesPerTick {
		var res TickResult
		var n int
		n, res, err = l.batchOnce(ctx, l.now(), failed)
		if err != nil {
			break
		}
		total.Due += res.Due
		total.Enqueued += res.Enqueued
		total.CatchUp += res.CatchUp
		total.Skipped += res.Skipped
		total.Failed += res.Failed
		if n < l.batch {
			break
		}
	}
	l.metrics.TickCompleted(time.Since(start), err)
	return total, err
}

// event is a metrics/log event of a committed batch.
type event struct {
	job     string
	trigger queue.Trigger
	skip    SkipReason
	err     string
	// failErr: the job's handling failed and was rolled back.
	failErr error
}

// handleSavepoint runs handle under a savepoint of tx: on failure only this
// job's writes (River job, run row, skip record, next_run_at) are rolled
// back.
func (l *Loop) handleSavepoint(ctx context.Context, tx pgx.Tx, r sqlc.LockDueJobsRow, now time.Time) (event, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return event{}, fmt.Errorf("%w: %w", errTxBroken, err)
	}
	ev, err := l.handle(ctx, sp, sqlc.New(sp), r, now)
	if err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return event{}, fmt.Errorf("%w: job %s: %w (rollback to savepoint: %w)", errTxBroken, r.ID, err, rbErr)
		}
		return event{}, fmt.Errorf("job %s: %w", r.ID, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return event{}, fmt.Errorf("%w: release savepoint: %w", errTxBroken, err)
	}
	return ev, nil
}

// errTxBroken: the batch transaction itself failed (not just one job).
var errTxBroken = errors.New("scheduler: batch transaction failed")

// batchOnce handles one batch. failed holds the jobs whose handling failed
// earlier in this tick (excluded from the batch); new failures are added
// once the batch committed.
func (l *Loop) batchOnce(ctx context.Context, now time.Time, failed map[uuid.UUID]bool) (int, TickResult, error) {
	var res TickResult
	var events []event
	var n int
	var newFailed []uuid.UUID
	exclude := make([]uuid.UUID, 0, len(failed))
	for id := range failed {
		exclude = append(exclude, id)
	}
	err := db.TxWith(ctx, l.db, func(tx pgx.Tx, q *sqlc.Queries) error {
		res, events, n, newFailed = TickResult{}, events[:0], 0, newFailed[:0]
		rows, err := q.LockDueJobs(ctx, sqlc.LockDueJobsParams{Now: now, Exclude: exclude, BatchSize: int32(l.batch)}) //nolint:gosec // batch is bounded by config validation (<= 1000)
		if err != nil {
			return fmt.Errorf("lock due jobs: %w", err)
		}
		n = len(rows)
		for _, r := range rows {
			ev, err := l.handleSavepoint(ctx, tx, r, now)
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, errTxBroken) {
					return err
				}
				res.Failed++
				newFailed = append(newFailed, r.ID)
				events = append(events, event{job: r.ID.String(), failErr: err})
				continue
			}
			res.Due++
			switch {
			case ev.skip != "":
				res.Skipped++
			case ev.trigger != "":
				res.Enqueued++
				if ev.trigger == queue.TriggerCatchUp {
					res.CatchUp++
				}
			}
			events = append(events, ev)
		}
		return nil
	})
	if err != nil {
		return 0, TickResult{}, err
	}
	for _, id := range newFailed {
		failed[id] = true
	}
	// Only committed effects are reported.
	for _, ev := range events {
		switch {
		case ev.failErr != nil:
			l.logger.ErrorContext(ctx, "scheduling a due job failed; it stays due and is retried next tick",
				slog.String("job_id", ev.job), slog.Any("error", ev.failErr))
		case ev.skip == SkipUnschedulable:
			l.metrics.Skipped(ev.skip)
			l.logger.WarnContext(ctx, "job schedule is unusable; scheduling stopped until the job is saved again",
				slog.String("job_id", ev.job), slog.String("reason", ev.err))
		case ev.skip != "":
			l.metrics.Skipped(ev.skip)
			l.logger.InfoContext(ctx, "scheduled run skipped: the previous run is still active",
				slog.String("job_id", ev.job))
		case ev.trigger != "":
			l.metrics.Enqueued(ev.trigger)
		}
	}
	return n, res, nil
}

// handle decides one locked job.
func (l *Loop) handle(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, r sqlc.LockDueJobsRow, now time.Time) (event, error) {
	ev := event{job: r.ID.String()}
	nextRunAt := time.Time{}
	if r.NextRunAt != nil {
		nextRunAt = *r.NextRunAt
	}
	d, err := decide(r.Schedule, nextRunAt, now)
	if err != nil {
		ev.skip, ev.err = SkipUnschedulable, unschedulableReason(err)
		if err := q.InsertScheduleSkip(ctx, sqlc.InsertScheduleSkipParams{JobID: r.ID, ScheduledFor: r.NextRunAt, Reason: string(SkipUnschedulable)}); err != nil {
			return ev, fmt.Errorf("record skip: %w", err)
		}
		return ev, advance(ctx, q, r, time.Time{}, now)
	}
	if !d.Run {
		// Not due after all (manual or a future next_run_at): just store
		// the decision's next_run_at.
		return ev, advance(ctx, q, r, d.NextRunAt, now)
	}
	ev.trigger = queue.TriggerSchedule
	if d.CatchUp {
		ev.trigger = queue.TriggerCatchUp
	}
	// The River job and its queued run row are inserted together
	// (internal/runstate, task 3.6).
	ins, err := runstate.EnqueueTx(ctx, tx, l.queue, l.pub, queue.BackupRunArgs{JobID: r.ID, ScheduledFor: d.ScheduledAt.UTC(), Trigger: ev.trigger})
	if err != nil {
		return ev, err
	}
	if ins.Duplicate {
		ev.trigger, ev.skip = "", SkipStillRunning
		sf := d.ScheduledAt
		if err := q.InsertScheduleSkip(ctx, sqlc.InsertScheduleSkipParams{JobID: r.ID, ScheduledFor: &sf, Reason: string(SkipStillRunning)}); err != nil {
			return ev, fmt.Errorf("record skip: %w", err)
		}
	}
	return ev, advance(ctx, q, r, d.NextRunAt, now)
}

func advance(ctx context.Context, q *sqlc.Queries, r sqlc.LockDueJobsRow, next, now time.Time) error {
	var p *time.Time
	if !next.IsZero() {
		n := next.UTC()
		p = &n
	}
	if err := q.AdvanceJobSchedule(ctx, sqlc.AdvanceJobScheduleParams{ID: r.ID, NextRunAt: p, ScheduledAt: now}); err != nil {
		return fmt.Errorf("advance next_run_at: %w", err)
	}
	return nil
}

// decide decodes a stored schedule and evaluates it. The configurable
// policy (min_interval) is not re-checked: tightening it must not stop
// existing jobs (D-143).
func decide(raw []byte, nextRunAt, now time.Time) (Decision, error) {
	s, err := decodeStored(raw)
	if err != nil {
		return Decision{}, err
	}
	p, err := Compile(s)
	if err != nil {
		return Decision{}, err
	}
	return p.Due(nextRunAt, now)
}

func decodeStored(raw []byte) (Schedule, error) {
	if len(raw) > MaxJSONBytes {
		return Schedule{}, fmt.Errorf("%w: stored schedule too large", ErrInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Schedule
	if err := dec.Decode(&s); err != nil {
		return Schedule{}, fmt.Errorf("%w: malformed stored schedule", ErrInvalid)
	}
	return s, nil
}

// unschedulableReason is a log-safe classification of a decision error.
func unschedulableReason(err error) string {
	switch {
	case errors.Is(err, ErrNoOccurrence):
		return "no further occurrence"
	case errors.Is(err, ErrOutOfRange):
		return "time out of range"
	case errors.Is(err, ErrInvalid):
		return "invalid schedule"
	default:
		return "schedule evaluation failed"
	}
}
