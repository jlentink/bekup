package externalapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"
)

func ptr[T any](v T) *T { return &v }

// riverCanceledState is River's canceled job state.
var riverCanceledState = string(rivertype.JobStateCancelled)

func TestAggregate(t *testing.T) {
	created := time.Date(2026, 11, 3, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	decided := created.Add(2 * time.Second)
	finished := created.Add(time.Minute)
	reason := "run_active"
	for _, tc := range []struct {
		name       string
		outcome    string
		river      *string
		run        string
		attempt    int32
		want       string
		done, ok   bool
		wantStatus string
		finished   bool
		reason     string
	}{
		{name: "undecided", outcome: "pending", want: StatePending},
		{name: "rejected", outcome: "rejected", want: StateRejected, done: true, finished: true, reason: reason},
		{name: "queued", outcome: "accepted", river: ptr("available"), run: "queued", attempt: 1, want: StateQueued, wantStatus: "QUEUED"},
		{name: "scheduled first attempt", outcome: "accepted", river: ptr("scheduled"), run: "queued", attempt: 1, want: StateQueued, wantStatus: "QUEUED"},
		{name: "running", outcome: "accepted", river: ptr("running"), run: "running", attempt: 1, want: StateRunning, wantStatus: "RUNNING"},
		{name: "success", outcome: "accepted", river: ptr("completed"), run: "success", attempt: 1, want: StateSuccess, done: true, ok: true, wantStatus: "SUCCESS", finished: true},
		{name: "success after River deleted the job", outcome: "accepted", run: "success", attempt: 2, want: StateSuccess, done: true, ok: true, wantStatus: "SUCCESS", finished: true},
		{name: "success before River completed", outcome: "accepted", river: ptr("running"), run: "success", attempt: 1, want: StateSuccess, done: true, ok: true, wantStatus: "SUCCESS", finished: true},
		{name: "partial is not success", outcome: "accepted", river: ptr("completed"), run: "partial", attempt: 1, want: StatePartial, done: true, wantStatus: "PARTIAL", finished: true},
		{name: "skipped", outcome: "accepted", river: ptr("completed"), run: "skipped", attempt: 1, want: StateSkipped, done: true, wantStatus: "SKIPPED", finished: true},
		{name: "canceled", outcome: "accepted", river: ptr(riverCanceledState), run: "canceled", attempt: 1, want: StateCanceled, done: true, wantStatus: "CANCELED", finished: true},
		{name: "failed, retry pending", outcome: "accepted", river: ptr("retryable"), run: "failed", attempt: 1, want: StateRetrying, wantStatus: "FAILED"},
		{name: "failed, retry due", outcome: "accepted", river: ptr("available"), run: "failed", attempt: 1, want: StateRetrying, wantStatus: "FAILED"},
		{name: "failed, retry picked up", outcome: "accepted", river: ptr("running"), run: "failed", attempt: 1, want: StateRetrying, wantStatus: "FAILED"},
		{name: "retry queued", outcome: "accepted", river: ptr("running"), run: "queued", attempt: 2, want: StateRetrying, wantStatus: "QUEUED"},
		{name: "retry running", outcome: "accepted", river: ptr("running"), run: "running", attempt: 2, want: StateRunning, wantStatus: "RUNNING"},
		{name: "failed, retries exhausted", outcome: "accepted", river: ptr("discarded"), run: "failed", attempt: 3, want: StateFailed, done: true, wantStatus: "FAILED", finished: true},
		{name: "failed, River job deleted", outcome: "accepted", run: "failed", attempt: 3, want: StateFailed, done: true, wantStatus: "FAILED", finished: true},
		{name: "queued run, job canceled", outcome: "accepted", river: ptr(riverCanceledState), run: "queued", attempt: 1, want: StateCanceled, done: true, wantStatus: "QUEUED", finished: true},
		{name: "queued run, job discarded", outcome: "accepted", river: ptr("discarded"), run: "queued", attempt: 1, want: StateFailed, done: true, wantStatus: "QUEUED", finished: true, reason: ReasonRunInterrupted},
		// A run River will not execute again (finding 5): the end
		// FailAbandonedRuns will write, at once.
		{name: "running run, job discarded", outcome: "accepted", river: ptr("discarded"), run: "running", attempt: 3, want: StateFailed, done: true, wantStatus: "RUNNING", finished: true, reason: ReasonRunInterrupted},
		{name: "running run, job completed", outcome: "accepted", river: ptr("completed"), run: "running", attempt: 1, want: StateFailed, done: true, wantStatus: "RUNNING", finished: true, reason: ReasonRunInterrupted},
		{name: "running run, job canceled", outcome: "accepted", river: ptr(riverCanceledState), run: "running", attempt: 1, want: StateCanceled, done: true, wantStatus: "RUNNING", finished: true},
		{name: "running run, River job deleted", outcome: "accepted", run: "running", attempt: 1, want: StateFailed, done: true, wantStatus: "RUNNING", finished: true, reason: ReasonRunInterrupted},
		{name: "queued run, River job deleted", outcome: "accepted", run: "queued", attempt: 1, want: StateFailed, done: true, wantStatus: "QUEUED", finished: true, reason: ReasonRunInterrupted},
		{name: "running run, job retryable", outcome: "accepted", river: ptr("retryable"), run: "running", attempt: 1, want: StateRetrying, wantStatus: "RUNNING"},
		{name: "queued run, job retryable", outcome: "accepted", river: ptr("retryable"), run: "queued", attempt: 1, want: StateRetrying, wantStatus: "QUEUED"},
		{name: "running run, rescued job available", outcome: "accepted", river: ptr("available"), run: "running", attempt: 1, want: StateRetrying, wantStatus: "RUNNING"},
		{name: "running run, rescued job scheduled", outcome: "accepted", river: ptr("scheduled"), run: "running", attempt: 2, want: StateRetrying, wantStatus: "RUNNING"},
		{name: "queued run, attempt starting", outcome: "accepted", river: ptr("running"), run: "queued", attempt: 1, want: StateQueued, wantStatus: "QUEUED"},
		{name: "running run, unknown River state", outcome: "accepted", river: ptr("exploded"), run: "running", attempt: 1, want: StateRunning, wantStatus: "RUNNING"},
		{name: "unknown run status", outcome: "accepted", river: ptr("completed"), run: "exploded", attempt: 1, want: StateFailed, done: true, wantStatus: "EXPLODED", finished: true},
		{name: "no run, River available", outcome: "accepted", river: ptr("available"), want: StateQueued},
		{name: "no run, River running", outcome: "accepted", river: ptr("running"), want: StateRunning},
		{name: "no run, River retryable", outcome: "accepted", river: ptr("retryable"), want: StateRetrying},
		{name: "no run, River canceled the job", outcome: "accepted", river: ptr(riverCanceledState), want: StateCanceled, done: true},
		{name: "no run, nothing left", outcome: "accepted", want: StateFailed, done: true, reason: ReasonRunMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := RequestFacts{RequestID: uuid.New(), JobID: uuid.New(), Outcome: tc.outcome, CreatedAt: created, RiverState: tc.river}
			if tc.outcome == "rejected" {
				f.Reason, f.DecidedAt = &reason, &decided
			}
			if tc.run != "" {
				f.RunID, f.RunStatus, f.RunAttempt = ptr(uuid.New()), ptr(tc.run), ptr(tc.attempt)
				f.RunQueuedAt, f.RunStartedAt, f.RunFinishedAt = &created, &created, &finished
			}
			s := Aggregate(f)
			if s.State != tc.want || s.Done != tc.done || s.Success != tc.ok {
				t.Fatalf("state %s done %v success %v, want %s %v %v", s.State, s.Done, s.Success, tc.want, tc.done, tc.ok)
			}
			if s.Success != (s.State == StateSuccess) {
				t.Fatal("success must mean exactly the state success")
			}
			gotStatus := ""
			if s.Status != nil {
				gotStatus = *s.Status
			}
			if gotStatus != tc.wantStatus {
				t.Fatalf("status %q, want %q", gotStatus, tc.wantStatus)
			}
			if (s.FinishedAt != nil) != tc.finished {
				t.Fatalf("finishedAt %v, want set %v", s.FinishedAt, tc.finished)
			}
			gotReason := ""
			if s.Reason != nil {
				gotReason = *s.Reason
			}
			if gotReason != tc.reason {
				t.Fatalf("reason %q, want %q", gotReason, tc.reason)
			}
			if s.CreatedAt.Location() != time.UTC || s.RequestID != f.RequestID || s.JobID != f.JobID {
				t.Fatalf("identity/time: %+v", s)
			}
		})
	}
}
