package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
)

// Errors of CancelRun.
var (
	// ErrRunNotFound: no run with that id.
	ErrRunNotFound = errors.New("jobs: run not found")
	// ErrRunFinished: the run already finished.
	ErrRunFinished = errors.New("jobs: the run already finished")
)

// RunAuditTargetType is the audit target type of run entries.
const RunAuditTargetType = "run"

// CancelRun requests the cancellation of a queued or running backup run
// (run.cancel: admins and maintainers; owners cannot cancel, D-106). In one
// transaction it marks the run (cancel_requested_at), cancels its River job
// and records run.cancel_requested. A run whose River job had not started
// is finished as canceled at once; a running one is stopped by its worker
// (River's cancel signal or the worker's own poll of cancel_requested_at):
// the runner kills the dump container and the pipeline aborts every Put,
// so no partial object is finalized.
func (s *Service) CancelRun(ctx context.Context, actor Actor, runID uuid.UUID) (RunCancellation, error) {
	if err := s.authorize(ctx, actor, authz.OpRunCancel); err != nil {
		return RunCancellation{}, err
	}
	if s.o.Canceler == nil {
		return RunCancellation{}, errors.New("jobs: no queue configured")
	}
	now := s.o.Now().UTC()
	var out RunCancellation
	err := db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		by := actor.UserID
		before, err := q.GetRun(ctx, runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRunNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load run: %w", err)
		}
		run, err := runstate.RequestCancelTx(ctx, q, s.o.Publisher, runID, &by, now)
		switch {
		case errors.Is(err, runstate.ErrRunNotFound):
			return ErrRunNotFound
		case errors.Is(err, runstate.ErrNotActive):
			// A finished attempt whose River job waits for its retry: the
			// retry would start a new run, so cancel it.
			out, err = s.cancelPendingRetry(ctx, tx, q, actor, before, now)
			return err
		case err != nil:
			return err
		}
		state := rivertype.JobState("")
		if run.RiverJobID != nil {
			state, err = s.o.Canceler.CancelTx(ctx, tx, *run.RiverJobID)
			if err != nil && !errors.Is(err, queue.ErrJobNotFound) {
				return err
			}
		}
		out = RunCancellation{RunID: run.ID, JobID: run.JobID, Status: string(run.Status), RequestedAt: now}
		if state != rivertype.JobStateRunning {
			// Nobody works on it (queued, snoozed, retry pending, or the
			// River row is gone): finish it here.
			row, err := runstate.CancelQueuedTx(ctx, q, s.o.Publisher, runstate.Ref{RunID: run.ID, JobID: run.JobID}, now)
			if err != nil {
				return err
			}
			out.Status = string(row.Status)
		}
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(actor.UserID), Action: audit.ActionRunCancelRequested, TargetType: RunAuditTargetType,
			TargetID: run.ID.String(), IP: actor.IP,
			Details: audit.Details{"job_id": run.JobID.String(), "status": string(before.Status), "river_state": string(state)},
		})
		return err
	})
	if err != nil {
		return RunCancellation{}, err
	}
	return out, nil
}

// cancelPendingRetry cancels the River job of a finished run when the run
// is its newest attempt and the job waits for a retry (retryable or
// scheduled); otherwise the run is simply finished (ErrRunFinished). The
// run keeps its final status; run.cancel_requested is audited with
// retry_canceled.
func (s *Service) cancelPendingRetry(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, actor Actor, run sqlc.Run, now time.Time) (RunCancellation, error) {
	if run.RiverJobID == nil {
		return RunCancellation{}, ErrRunFinished
	}
	latest, err := q.LatestRunForRiverJob(ctx, *run.RiverJobID)
	if err != nil || latest.ID != run.ID {
		// A newer attempt exists (cancel that one) or the lookup failed.
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return RunCancellation{}, fmt.Errorf("jobs: load latest attempt: %w", err)
		}
		return RunCancellation{}, ErrRunFinished
	}
	state, canceled, err := s.o.Canceler.CancelPendingTx(ctx, tx, *run.RiverJobID)
	if errors.Is(err, queue.ErrJobNotFound) || (err == nil && !canceled) {
		return RunCancellation{}, ErrRunFinished
	}
	if err != nil {
		return RunCancellation{}, err
	}
	if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: audit.ActionRunCancelRequested, TargetType: RunAuditTargetType,
		TargetID: run.ID.String(), IP: actor.IP,
		Details: audit.Details{"job_id": run.JobID.String(), "status": string(run.Status), "river_state": string(state), "retry_canceled": true},
	}); err != nil {
		return RunCancellation{}, err
	}
	return RunCancellation{RunID: run.ID, JobID: run.JobID, Status: string(run.Status), RequestedAt: now, RetryCanceled: true}, nil
}
