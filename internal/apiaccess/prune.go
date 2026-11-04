package apiaccess

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
)

// Run request housekeeping (D-220).
const (
	// RequestRetention is how long decided run requests and processed
	// access denials are kept (the token's "last used", its last-seen
	// address and the run history's token name come from them).
	RequestRetention = 30 * 24 * time.Hour
	// PruneInterval is the period of the prune job.
	PruneInterval = 6 * time.Hour
	pruneBatch    = 1000
	pruneMaxLoops = 100
)

// PruneWorker deletes decided run requests and processed access denials
// older than RequestRetention (River kind run_request_prune, maintenance
// queue). Pending rows are left to the consumer.
type PruneWorker struct {
	river.WorkerDefaults[queue.RunRequestPruneArgs]
	// DB is the app pool.
	DB sqlc.DBTX
	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

// Work implements river.Worker.
func (w *PruneWorker) Work(ctx context.Context, _ *river.Job[queue.RunRequestPruneArgs]) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	q := sqlc.New(w.DB)
	before := now().Add(-RequestRetention)
	for range pruneMaxLoops {
		n, err := q.PruneRunRequests(ctx, sqlc.PruneRunRequestsParams{Before: before, MaxRows: pruneBatch})
		if err != nil {
			return fmt.Errorf("apiaccess: prune run requests: %w", err)
		}
		if n < pruneBatch {
			break
		}
	}
	for range pruneMaxLoops {
		n, err := q.PruneAPIAccessDenials(ctx, sqlc.PruneAPIAccessDenialsParams{Before: before, MaxRows: pruneBatch})
		if err != nil {
			return fmt.Errorf("apiaccess: prune access denials: %w", err)
		}
		if n < pruneBatch {
			return nil
		}
	}
	return nil
}

// RegisterPrune registers the prune worker and its periodic job.
func RegisterPrune(ws *queue.Workers, dbtx sqlc.DBTX) error {
	if err := queue.Register(ws, &PruneWorker{DB: dbtx}); err != nil {
		return err
	}
	return ws.AddPeriodic(PruneInterval, queue.RunRequestPruneArgs{})
}
