package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
)

// DigestWorker works the daily notify_retention_digest job.
type DigestWorker struct {
	river.WorkerDefaults[queue.NotifyRetentionDigestArgs]
	s *Service
}

// Work implements river.Worker.
func (w *DigestWorker) Work(ctx context.Context, _ *river.Job[queue.NotifyRetentionDigestArgs]) error {
	_, err := w.s.RetentionDigest(ctx)
	return err
}

// RetentionDigest emits one retention.summary per job and UTC day for the
// last DigestDays complete days in which retention deleted backups of the
// job: retention removals are never notified per file (D-161). The dedup
// key (job, day) makes re-runs and restarts harmless. It returns the
// number of new events.
func (s *Service) RetentionDigest(ctx context.Context) (int, error) {
	q := sqlc.New(s.o.DB)
	today := s.o.Now().UTC().Truncate(24 * time.Hour)
	emitted := 0
	for i := DigestDays; i >= 1; i-- {
		from := today.AddDate(0, 0, -i)
		day := from.Format("2006-01-02")
		rows, err := q.RetentionDeletionsByJob(ctx, sqlc.RetentionDeletionsByJobParams{FromAt: from, UntilAt: from.AddDate(0, 0, 1)})
		if err != nil {
			return emitted, fmt.Errorf("notify: retention deletions: %w", err)
		}
		for _, r := range rows {
			id, err := Emit(ctx, q, Event{
				Type: RetentionSummary, Subject: Subject{JobID: r.JobID},
				Detail:   Detail{"date": day, "runs": r.Runs, "files": r.Files, "bytes": r.Bytes},
				DedupKey: "retention.summary:" + r.JobID.String() + ":" + day,
			})
			if err != nil {
				return emitted, err
			}
			if id != uuid.Nil {
				emitted++
			}
		}
	}
	if emitted > 0 {
		s.o.Logger.InfoContext(ctx, "retention digests emitted", slog.Int("events", emitted))
	}
	return emitted, nil
}

// CleanupWorker works the daily notify_cleanup job.
type CleanupWorker struct {
	river.WorkerDefaults[queue.NotifyCleanupArgs]
	s *Service
}

// Work implements river.Worker.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[queue.NotifyCleanupArgs]) error {
	return w.s.Cleanup(ctx)
}

// Cleanup deletes events (with their deliveries) older than EventRetention
// and inbox rows older than InboxRetention.
func (s *Service) Cleanup(ctx context.Context) error {
	q := sqlc.New(s.o.DB)
	now := s.o.Now()
	ev, err := q.DeleteOldNotificationEvents(ctx, now.Add(-EventRetention))
	if err != nil {
		return fmt.Errorf("notify: delete old events: %w", err)
	}
	in, err := q.DeleteOldNotifications(ctx, now.Add(-InboxRetention))
	if err != nil {
		return fmt.Errorf("notify: delete old inbox rows: %w", err)
	}
	if ev > 0 || in > 0 {
		s.o.Logger.InfoContext(ctx, "old notifications deleted", slog.Int64("events", ev), slog.Int64("inbox", in))
	}
	return nil
}
