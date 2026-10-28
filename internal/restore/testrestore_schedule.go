package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/scheduler"
)

// The test-restore schedule (task 4.6): River kind test_restore_schedule
// (maintenance queue, every TestScheduleInterval, unique) enqueues the due
// test restores of jobs with test restores enabled (one queued or running
// test restore per job; a due occurrence while one is active is skipped,
// missed occurrences collapse into one), fails test restores no worker will
// finish (crashed worker, discarded job) and retries dropping scratch
// databases whose drop failed.

// Schedule settings.
const (
	// TestScheduleInterval is the period of the schedule worker.
	TestScheduleInterval = time.Minute
	// maxScheduledPerTick bounds the jobs handled per tick.
	maxScheduledPerTick = 100
	// maxScratchRetries bounds the leftover drops per tick.
	maxScratchRetries = 20
)

// TestTrigger says why a test restore was queued.
type TestTrigger = sqlc.TestRestoreTrigger

// Triggers.
const (
	TestTriggerSchedule = sqlc.TestRestoreTriggerSchedule
	TestTriggerManual   = sqlc.TestRestoreTriggerManual
)

// enqueueTestRestoreTx records a queued test restore with its River job
// (the id only) and audits the request, in tx. ErrTestRestoreActive when
// the job already has a queued or running one.
func (s *Service) enqueueTestRestoreTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, ins Inserter, job, target uuid.UUID,
	trigger TestTrigger, by *uuid.UUID, scheduledFor *time.Time, ip *audit.Event,
) (sqlc.TestRestore, error) {
	if ins == nil {
		return sqlc.TestRestore{}, errors.New("restore: no queue configured")
	}
	row, err := q.InsertTestRestore(ctx, sqlc.InsertTestRestoreParams{
		ID: uuid.New(), JobID: job, TargetConnectionID: target, Trigger: trigger, RequestedBy: by, ScheduledFor: scheduledFor,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.TestRestore{}, ErrTestRestoreActive
	}
	if err != nil {
		return sqlc.TestRestore{}, fmt.Errorf("restore: insert test restore: %w", err)
	}
	res, err := ins.InsertTx(ctx, tx, queue.TestRestoreArgs{TestRestoreID: row.ID}, queue.InsertOptions{})
	if err != nil {
		return sqlc.TestRestore{}, fmt.Errorf("restore: enqueue test restore: %w", err)
	}
	if res.Duplicate {
		return sqlc.TestRestore{}, ErrTestRestoreActive
	}
	if n, err := q.SetTestRestoreRiverJob(ctx, sqlc.SetTestRestoreRiverJobParams{ID: row.ID, RiverJobID: &res.JobID}); err != nil || n != 1 {
		return sqlc.TestRestore{}, fmt.Errorf("restore: set the test restore's River job: %w", errors.Join(err, errors.New("not updated")))
	}
	row.RiverJobID = &res.JobID
	ev := audit.Event{Actor: audit.SystemActor()}
	if ip != nil {
		ev = *ip
	}
	ev.Action, ev.TargetType, ev.TargetID = audit.ActionTestRestoreRequested, TestRestoreAuditTarget, row.ID.String()
	ev.Details = audit.Details{"job_id": job.String(), "target_connection_id": target.String(), "trigger": string(trigger), "queue_job_id": res.JobID}
	if scheduledFor != nil {
		ev.Details["scheduled_for"] = scheduledFor.UTC().Format(time.RFC3339)
	}
	if _, err := s.o.Auditor.RecordTx(ctx, q, ev); err != nil {
		return sqlc.TestRestore{}, err
	}
	return row, nil
}

// ScheduleTestRestores enqueues the due test restores (one transaction per
// job) and advances each job's next test restore to the first occurrence
// after now. A job whose enqueue fails still advances (the occurrence is
// skipped and logged), so it cannot block the others.
func (s *Service) ScheduleTestRestores(ctx context.Context, ins Inserter) (int, error) {
	queued := 0
	var errs []error
	for range maxScheduledPerTick {
		now := s.o.Now().UTC()
		var (
			handled bool
			failed  *sqlc.LockDueTestRestoresRow
		)
		err := db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
			rows, err := q.LockDueTestRestores(ctx, sqlc.LockDueTestRestoresParams{Now: now, MaxRows: 1})
			if err != nil || len(rows) == 0 {
				return err
			}
			r := rows[0]
			handled = true
			if r.TestRestoreConnectionID != nil {
				_, err := s.enqueueTestRestoreTx(ctx, tx, q, ins, r.ID, *r.TestRestoreConnectionID, TestTriggerSchedule, nil, r.TestRestoreNextAt, nil)
				switch {
				case errors.Is(err, ErrTestRestoreActive):
					s.o.Logger.InfoContext(ctx, "test restore skipped: the previous one is still queued or running", slog.String("job_id", r.ID.String()))
				case err != nil:
					failed = &r
					return err
				default:
					queued++
				}
			}
			return q.AdvanceTestRestore(ctx, sqlc.AdvanceTestRestoreParams{ID: r.ID, NextAt: nextTestOccurrence(r.TestRestoreSchedule, now)})
		})
		if failed != nil {
			errs = append(errs, err)
			s.o.Logger.ErrorContext(ctx, "enqueueing a scheduled test restore failed; the occurrence is skipped",
				slog.String("job_id", failed.ID.String()), slog.Any("error", err))
			if aerr := sqlc.New(s.o.DB).AdvanceTestRestore(ctx, sqlc.AdvanceTestRestoreParams{
				ID: failed.ID, NextAt: nextTestOccurrence(failed.TestRestoreSchedule, now),
			}); aerr != nil {
				return queued, errors.Join(append(errs, aerr)...)
			}
			continue
		}
		if err != nil {
			return queued, errors.Join(append(errs, err)...)
		}
		if !handled {
			break
		}
	}
	return queued, errors.Join(errs...)
}

// nextTestOccurrence is the first occurrence after now (nil: manual or an
// unusable schedule, which stops scheduling until the settings change).
func nextTestOccurrence(raw []byte, now time.Time) *time.Time {
	sched, err := scheduler.Parse(raw, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval})
	if err != nil || sched.Kind == scheduler.KindManual {
		return nil
	}
	t, err := scheduler.Next(sched, now)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

// SweepStuckTestRestores fails the test restores no worker will finish
// (code abandoned; their running items too) and releases their run slot.
// Their scratch databases are dropped by the leftover retry.
func (s *Service) SweepStuckTestRestores(ctx context.Context, timeout, grace time.Duration) (int, error) {
	if timeout <= 0 {
		return 0, errors.New("restore: the test-restore sweep needs the restore timeout")
	}
	if grace <= 0 {
		grace = StuckGrace
	}
	now := s.o.Now()
	started, idle := now.Add(-timeout-grace), now.Add(-grace)
	ids, err := sqlc.New(s.o.DB).ListStuckTestRestores(ctx, sqlc.ListStuckTestRestoresParams{StartedBefore: started, IdleBefore: idle, MaxRows: maxStuckPerSweep})
	if err != nil {
		return 0, fmt.Errorf("restore: list stuck test restores: %w", err)
	}
	n := 0
	var errs []error
	for _, id := range ids {
		err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
			tr, err := q.LockTestRestore(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			stuck, err := q.IsTestRestoreStuck(ctx, sqlc.IsTestRestoreStuckParams{ID: id, StartedBefore: started, IdleBefore: idle})
			if err != nil || !stuck {
				return err
			}
			code, msg := CodeAbandoned, TestErrorMessage(CodeAbandoned)
			if _, err := q.FinishTestRestore(ctx, sqlc.FinishTestRestoreParams{ID: id, Status: TRStatusError, ErrorCode: &code, ErrorMessage: &msg,
				Databases: tr.Databases, Passed: tr.Passed, Warned: tr.Warned, Failed: tr.Failed}); err != nil {
				return err
			}
			if err := q.FailRunningTestRestoreItems(ctx, sqlc.FailRunningTestRestoreItemsParams{TestRestoreID: id, ErrorCode: &code}); err != nil {
				return err
			}
			if err := q.DeleteRunSlot(ctx, id); err != nil {
				return err
			}
			if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
				Actor: audit.SystemActor(), Action: audit.ActionTestRestoreAbandoned, TargetType: TestRestoreAuditTarget, TargetID: id.String(),
				Details: audit.Details{"job_id": tr.JobID.String(), "previous_status": string(tr.Status), "error_code": CodeAbandoned},
			}); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("restore: abandon test restore %s: %w", id, err))
		}
	}
	return n, errors.Join(errs...)
}

// RetryScratchDrops drops the scratch databases of finished test restores
// whose drop failed (or whose worker crashed), through rw's runner and
// targets. It reports how many are now gone.
func (s *Service) RetryScratchDrops(ctx context.Context, rw *Worker) (int, error) {
	q := sqlc.New(s.o.DB)
	rows, err := q.ListUndroppedScratch(ctx, maxScratchRetries)
	if err != nil {
		return 0, fmt.Errorf("restore: list leftover scratch databases: %w", err)
	}
	n := 0
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		if !engine.IsBekupScratchDatabase(engine.ScratchPrefix + r.ScratchID) {
			continue
		}
		if derr := dropScratchDatabase(ctx, rw, r.TargetConnectionID, r.ScratchID, r.TestRestoreID); derr != nil {
			// Logged and retried at the next tick; one unreachable target
			// must not fail the schedule worker.
			s.o.Logger.WarnContext(ctx, "dropping a leftover scratch database failed; retrying later",
				slog.String("test_restore_id", r.TestRestoreID.String()), slog.String("code", ErrorCode(derr)))
			continue
		}
		if err := q.SetTestRestoreItemDropped(ctx, r.ID); err != nil {
			return n, fmt.Errorf("restore: record a dropped scratch database: %w", err)
		}
		n++
	}
	return n, nil //nolint:nilerr // per-item drop failures are logged and retried at the next tick
}

// TestScheduleWorker runs the schedule, the stuck sweep and the leftover
// drops (River kind test_restore_schedule).
type TestScheduleWorker struct {
	river.WorkerDefaults[queue.TestRestoreScheduleArgs]
	Service *Service
	// Restores drops leftover scratch databases (runner and targets).
	Restores *Worker
	// Queue inserts the test_restore jobs (resolved late).
	Queue func() Inserter
	// RestoreTimeout is workers.restore_timeout; Grace overrides
	// StuckGrace (tests).
	RestoreTimeout time.Duration
	Grace          time.Duration
}

// Work implements river.Worker.
func (w *TestScheduleWorker) Work(ctx context.Context, _ *river.Job[queue.TestRestoreScheduleArgs]) error {
	var ins Inserter
	if w.Queue != nil {
		ins = w.Queue()
	}
	log := w.Service.o.Logger
	n, err := w.Service.ScheduleTestRestores(ctx, ins)
	if n > 0 {
		log.InfoContext(ctx, "scheduled test restores queued", slog.Int("count", n))
	}
	a, serr := w.Service.SweepStuckTestRestores(ctx, w.RestoreTimeout, w.Grace)
	if a > 0 {
		log.WarnContext(ctx, "test restores abandoned: no worker finished them", slog.Int("count", a))
	}
	d, derr := w.Service.RetryScratchDrops(ctx, w.Restores)
	if d > 0 {
		log.InfoContext(ctx, "leftover scratch databases dropped", slog.Int("count", d))
	}
	return errors.Join(err, serr, derr)
}

// RegisterTestSchedule registers the schedule worker and its periodic job.
func RegisterTestSchedule(ws *queue.Workers, w *TestScheduleWorker) error {
	if w == nil || w.Service == nil || w.Restores == nil || w.RestoreTimeout <= 0 {
		return errors.New("restore: the test-restore schedule needs the service, the restore worker and the restore timeout")
	}
	if err := queue.Register(ws, w); err != nil {
		return err
	}
	return ws.AddPeriodic(TestScheduleInterval, queue.TestRestoreScheduleArgs{})
}
