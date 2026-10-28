package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
)

// Skip-record housekeeping (D-145).
const (
	// SkipRetention is how long schedule_skips rows are kept.
	SkipRetention = 90 * 24 * time.Hour
	// SkipCleanupInterval is the period of the cleanup job.
	SkipCleanupInterval = 6 * time.Hour
	skipCleanupBatch    = 1000
	skipCleanupMaxLoops = 100
)

// SkipCleanupWorker deletes schedule_skips rows older than SkipRetention
// (River kind schedule_skip_cleanup, maintenance queue).
type SkipCleanupWorker struct {
	river.WorkerDefaults[queue.ScheduleSkipCleanupArgs]
	// DB is the app pool.
	DB sqlc.DBTX
	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

// Work implements river.Worker.
func (w *SkipCleanupWorker) Work(ctx context.Context, _ *river.Job[queue.ScheduleSkipCleanupArgs]) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	q := sqlc.New(w.DB)
	before := now().Add(-SkipRetention)
	for range skipCleanupMaxLoops {
		n, err := q.DeleteScheduleSkipsBefore(ctx, sqlc.DeleteScheduleSkipsBeforeParams{Before: before, MaxRows: skipCleanupBatch})
		if err != nil {
			return fmt.Errorf("delete old schedule skips: %w", err)
		}
		if n < skipCleanupBatch {
			return nil
		}
	}
	return nil
}

// RegisterWorkers registers the scheduler's workers and periodic jobs.
func RegisterWorkers(ws *queue.Workers, dbtx sqlc.DBTX) error {
	if err := queue.Register(ws, &SkipCleanupWorker{DB: dbtx}); err != nil {
		return err
	}
	return ws.AddPeriodic(SkipCleanupInterval, queue.ScheduleSkipCleanupArgs{})
}
