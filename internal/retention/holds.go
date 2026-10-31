package retention

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

// Retention holds (D-209). Every sweep records, per destination, the
// incomplete runs the plan keeps although no keep rule would keep them
// (Plan's Hold) in retention_holds, which Job.retentionHolds shows on the
// job page and the dashboard, and emits retention.blocked for each held
// destination, deduplicated per job, destination and UTC day through the
// outbox. Holds of destinations that no longer hold anything for the job,
// and every hold of a deleted job, are removed.

// Hold reasons as stored (retention_holds.reason).
const (
	HoldNoCompleteRun           = "no_complete_run"
	HoldNewerThanNewestComplete = "newer_than_newest_complete"
)

// holdReason maps a plan's keep reason to the stored reason.
func holdReason(r KeepReason) string {
	if r == KeepNoComplete {
		return HoldNoCompleteRun
	}
	return HoldNewerThanNewestComplete
}

// HoldDedupKey is the dedup key of a retention.blocked event: one per job,
// destination and UTC day.
func HoldDedupKey(jobID, destID uuid.UUID, now time.Time) string {
	return "retention.blocked:" + jobID.String() + ":" + destID.String() + ":" + now.UTC().Format(time.DateOnly)
}

// recordHolds writes the holds of plans for job in one transaction and
// emits the day's retention.blocked events.
func (w *Worker) recordHolds(ctx context.Context, job *sqlc.GetJobRetentionRow, plans []DestinationPlan) error {
	now := w.o.Now().UTC()
	return db.Tx(ctx, w.o.DB, func(q *sqlc.Queries) error {
		keep := []uuid.UUID{}
		if job.DeletedAt == nil {
			for i := range plans {
				h := plans[i].Hold
				if h == nil {
					continue
				}
				dest := plans[i].DestinationID
				keep = append(keep, dest)
				if err := upsertHold(ctx, q, job.ID, dest, h, now); err != nil {
					return err
				}
			}
		}
		if _, err := q.DeleteRetentionHoldsExcept(ctx, sqlc.DeleteRetentionHoldsExceptParams{JobID: job.ID, Keep: keep}); err != nil {
			return fmt.Errorf("retention: delete ended holds: %w", err)
		}
		return nil
	})
}

func upsertHold(ctx context.Context, q *sqlc.Queries, jobID, dest uuid.UUID, h *Hold, now time.Time) error {
	reason := holdReason(h.Reason)
	arg := sqlc.UpsertRetentionHoldParams{
		JobID: jobID, DestinationID: dest, Reason: reason,
		HeldRuns:  int32(min(len(h.Runs), 1<<30)),
		HeldSince: h.Since.UTC(), CheckedAt: now,
	}
	detail := notify.Detail{
		"reason": reason, "held_runs": len(h.Runs), "since": h.Since.UTC().Format(time.DateOnly),
	}
	if !h.NewestComplete.IsZero() {
		t := h.NewestComplete.UTC()
		arg.NewestCompleteAt = &t
		detail["newest_complete"] = t.Format(time.DateOnly)
	}
	if _, err := q.UpsertRetentionHold(ctx, arg); err != nil {
		return fmt.Errorf("retention: record hold: %w", err)
	}
	if _, err := notify.Emit(ctx, q, notify.Event{
		Type: notify.RetentionBlocked, Subject: notify.Subject{JobID: jobID, DestinationID: dest},
		Detail: detail, DedupKey: HoldDedupKey(jobID, dest, now),
	}); err != nil {
		return fmt.Errorf("retention: emit retention.blocked: %w", err)
	}
	return nil
}

// HoldView is one recorded hold with its destination (Job.retentionHolds).
type HoldView struct {
	JobID              uuid.UUID
	DestinationID      uuid.UUID
	DestinationName    string
	DestinationType    sqlc.DestinationType
	DestinationDeleted bool
	// Reason is HoldNoCompleteRun or HoldNewerThanNewestComplete.
	Reason           string
	HeldRuns         int
	Since            time.Time
	NewestCompleteAt *time.Time
	FirstSeenAt      time.Time
	CheckedAt        time.Time
}

// MaxHoldBatch bounds the jobs of one HoldsForJobs call.
const MaxHoldBatch = 500

// HoldsForJobs returns the recorded holds of jobs by job id (jobs without a
// hold are absent). The caller has authorized the jobs already.
func HoldsForJobs(ctx context.Context, q sqlc.DBTX, jobIDs []uuid.UUID) (map[uuid.UUID][]HoldView, error) {
	out := make(map[uuid.UUID][]HoldView, len(jobIDs))
	for _, id := range jobIDs {
		out[id] = nil
	}
	if len(jobIDs) == 0 {
		return out, nil
	}
	if len(jobIDs) > MaxHoldBatch {
		return nil, fmt.Errorf("retention: at most %d jobs per hold batch", MaxHoldBatch)
	}
	rows, err := sqlc.New(q).ListRetentionHoldsForJobs(ctx, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("retention: list holds: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		out[r.JobID] = append(out[r.JobID], HoldView{
			JobID: r.JobID, DestinationID: r.DestinationID, DestinationName: r.DestinationName,
			DestinationType: r.DestinationType, DestinationDeleted: r.DestinationDeleted, Reason: r.Reason,
			HeldRuns: int(r.HeldRuns), Since: r.HeldSince, NewestCompleteAt: r.NewestCompleteAt,
			FirstSeenAt: r.FirstSeenAt, CheckedAt: r.CheckedAt,
		})
	}
	return out, nil
}

// HoldReader reads the recorded holds for the API (Job.retentionHolds).
type HoldReader struct{ DB sqlc.DBTX }

// HoldsForJobs implements the API's batch load (see HoldsForJobs).
func (h HoldReader) HoldsForJobs(ctx context.Context, jobIDs []uuid.UUID) (map[uuid.UUID][]HoldView, error) {
	return HoldsForJobs(ctx, h.DB, jobIDs)
}
