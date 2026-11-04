package externalapi

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"
)

// States of a run request (GET /api/v1/requests/{id}, D-220 item 6).
const (
	StatePending  = "pending"  // filed, not decided by the workers yet
	StateRejected = "rejected" // decided without a run (reason says why)
	StateQueued   = "queued"   // the run waits for a worker
	StateRunning  = "running"  // the run is in progress
	StateRetrying = "retrying" // an attempt failed and River holds a retry
	StateSuccess  = "success"
	StatePartial  = "partial"
	StateFailed   = "failed"
	StateCanceled = "canceled"
	StateSkipped  = "skipped"
)

// Request outcomes (run_requests.outcome).
const (
	outcomePending  = "pending"
	outcomeAccepted = "accepted"
	outcomeRejected = "rejected"
)

// ReasonRunMissing is the reason of an accepted request whose run and
// River job are both gone (never expected; reported as failed).
const ReasonRunMissing = "run_missing"

// ReasonRunInterrupted is the reason of an accepted request whose run is
// still queued or running although River will not execute it again (the
// worker stopped; runstate.FailAbandonedRuns finishes the run as failed
// with code interrupted).
const ReasonRunInterrupted = "run_interrupted"

// riverActive are the River states in which the job will still execute
// (again): an attempt is waiting, scheduled or in progress.
var riverActive = map[string]bool{
	string(rivertype.JobStateAvailable): true, string(rivertype.JobStatePending): true,
	string(rivertype.JobStateScheduled): true, string(rivertype.JobStateRetryable): true,
	string(rivertype.JobStateRunning): true,
}

// Status is the JSON answer of GET /api/v1/requests/{id}.
type Status struct {
	RequestID uuid.UUID `json:"requestId"`
	JobID     uuid.UUID `json:"jobId"`
	State     string    `json:"state"`
	// Done is true once nothing will change any more (false while River
	// still holds a retry).
	Done bool `json:"done"`
	// Success is true only for the state success.
	Success bool `json:"success"`
	// Status is the exact status of the latest run (SUCCESS, PARTIAL,
	// FAILED, CANCELED, SKIPPED, QUEUED, RUNNING); nil without a run.
	Status     *string    `json:"status"`
	Attempt    *int32     `json:"attempt"`
	RunID      *uuid.UUID `json:"runId"`
	Reason     *string    `json:"reason"`
	CreatedAt  time.Time  `json:"createdAt"`
	QueuedAt   *time.Time `json:"queuedAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// Aggregate derives the request state from the raw facts. River retries
// insert a new run per attempt on the same River job (the facts carry the
// latest one), so a failed attempt is "retrying" while River still holds
// the job and "failed" only after River gave up. Only a successful run
// counts as success.
func Aggregate(f RequestFacts) Status {
	s := Status{
		RequestID: f.RequestID, JobID: f.JobID, CreatedAt: utc(f.CreatedAt),
		RunID: f.RunID, Attempt: f.RunAttempt,
		QueuedAt: utcPtr(f.RunQueuedAt), StartedAt: utcPtr(f.RunStartedAt),
	}
	if f.RunStatus != nil {
		up := strings.ToUpper(*f.RunStatus)
		s.Status = &up
	}
	switch f.Outcome {
	case outcomeRejected:
		s.State, s.Done = StateRejected, true
		s.Reason = f.Reason
		s.FinishedAt = utcPtr(f.DecidedAt)
		return s
	case outcomeAccepted:
	default:
		s.State = StatePending
		return s
	}
	river := ""
	if f.RiverState != nil {
		river = *f.RiverState
	}
	run := ""
	if f.RunStatus != nil {
		run = *f.RunStatus
	}
	switch run {
	case "":
		s.State, s.Done = withoutRun(river)
		if s.Done && s.State == StateFailed {
			reason := ReasonRunMissing
			s.Reason = &reason
		}
	case "queued", "running":
		s.State, s.Done = unfinished(run, f.RiverState, f.RunAttempt)
		if s.Done && s.State == StateFailed {
			reason := ReasonRunInterrupted
			s.Reason = &reason
		}
	case "failed":
		if riverActive[river] {
			s.State = StateRetrying
		} else {
			s.State, s.Done = StateFailed, true
		}
	case "success", "partial", "canceled", "skipped":
		s.State, s.Done = run, true
	default:
		// A status this binary does not know: never success.
		s.State, s.Done = StateFailed, true
	}
	s.Success = s.State == StateSuccess
	if s.Done {
		s.FinishedAt = utcPtr(f.RunFinishedAt)
	}
	return s
}

// unfinished is the state of a request whose latest run is still queued
// or running: River's job state decides whether that run will still move.
// A job River finished (completed, discarded), canceled or deleted
// without the run's terminal write will never run it again: such a run is
// finished by runstate.FailAbandonedRuns as failed (code interrupted) or,
// after a cancellation, as canceled, so the request reports that end at
// once. A job waiting for a retry (retryable, or available/scheduled/
// pending again after a crashed attempt was rescued) is retrying.
func unfinished(run string, riverState *string, attempt *int32) (string, bool) {
	if riverState == nil {
		// River deleted the job: no attempt follows.
		return StateFailed, true
	}
	switch rivertype.JobState(*riverState) {
	case rivertype.JobStateCancelled:
		return StateCanceled, true
	case rivertype.JobStateCompleted, rivertype.JobStateDiscarded:
		return StateFailed, true
	case rivertype.JobStateRetryable:
		return StateRetrying, false
	case rivertype.JobStateRunning:
		// The attempt is executing (a queued run is about to begin).
		if run == "running" {
			return StateRunning, false
		}
	case rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled:
		if run == "running" {
			// The running attempt died and River queued the next one.
			return StateRetrying, false
		}
	default:
		// A state this binary does not know: report what the run says.
		if run == "running" {
			return StateRunning, false
		}
	}
	if attempt != nil && *attempt > 1 {
		return StateRetrying, false
	}
	return StateQueued, false
}

// withoutRun is the state of an accepted request without a run row: only
// River's state is left.
func withoutRun(river string) (string, bool) {
	switch rivertype.JobState(river) {
	case rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled:
		return StateQueued, false
	case rivertype.JobStateRunning:
		return StateRunning, false
	case rivertype.JobStateRetryable:
		return StateRetrying, false
	case rivertype.JobStateCancelled:
		return StateCanceled, true
	case rivertype.JobStateCompleted, rivertype.JobStateDiscarded:
		return StateFailed, true
	default:
		return StateFailed, true
	}
}

func utc(t time.Time) time.Time { return t.UTC() }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
