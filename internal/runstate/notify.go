package runstate

import (
	"context"
	"fmt"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

// emitRunEvent adds the D-161 backup event of a run that started or
// reached a terminal status to the notification outbox, in the
// transaction of the status change (task 5.3). Skipped runs emit nothing;
// a run pulled forward by startNextRunEarly reports backup.started_early
// instead of backup.started.
func emitRunEvent(ctx context.Context, q *sqlc.Queries, run *sqlc.Run) error {
	var t notify.EventType
	switch run.Status {
	case StatusRunning:
		t = notify.BackupStarted
		if run.Trigger == sqlc.RunTriggerEarly {
			t = notify.BackupStartedEarly
		}
	case StatusSuccess:
		t = notify.BackupSucceeded
		if run.HasWarnings {
			t = notify.BackupSucceededWithWarnings
		}
	case StatusPartial:
		t = notify.BackupPartial
	case StatusFailed:
		t = notify.BackupFailed
	case StatusCanceled:
		t = notify.BackupCanceled
	default:
		return nil
	}
	d := notify.Detail{"status": string(run.Status), "trigger": string(run.Trigger), "attempt": int64(run.Attempt)}
	if run.ErrorCode != nil && *run.ErrorCode != "" {
		d["error_code"] = clamp(*run.ErrorCode, 64)
	}
	if _, err := notify.Emit(ctx, q, notify.Event{
		Type: t, Subject: notify.Subject{JobID: run.JobID, RunID: run.ID}, Detail: d,
	}); err != nil {
		return fmt.Errorf("runstate: emit %s: %w", t, err)
	}
	return nil
}
