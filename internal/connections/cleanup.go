package connections

import (
	"context"
	"errors"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/queue"
)

// CleanupWorker runs CleanupTemporary (River kind
// temporary_connection_cleanup, maintenance queue, every CleanupInterval).
type CleanupWorker struct {
	river.WorkerDefaults[queue.TemporaryConnectionCleanupArgs]
	Service *Service
}

// Work implements river.Worker.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[queue.TemporaryConnectionCleanupArgs]) error {
	res, err := w.Service.CleanupTemporary(ctx)
	if n := res.Expired + res.RestoreFinished; n > 0 {
		w.Service.o.Logger.InfoContext(ctx, "temporary restore connections deleted",
			slog.Int("expired", res.Expired), slog.Int("restore_finished", res.RestoreFinished))
	}
	return err
}

// RegisterCleanup registers the cleanup worker and its periodic job.
func RegisterCleanup(ws *queue.Workers, svc *Service) error {
	if svc == nil {
		return errors.New("connections: the cleanup worker needs the service")
	}
	if err := queue.Register(ws, &CleanupWorker{Service: svc}); err != nil {
		return err
	}
	return ws.AddPeriodic(CleanupInterval, queue.TemporaryConnectionCleanupArgs{})
}
