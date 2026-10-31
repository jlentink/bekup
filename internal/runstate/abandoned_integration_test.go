//go:build integration

package runstate

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// riverJob inserts a River job row in state (finalized states get
// finalized_at) and returns its id.
func (e *stEnv) riverJob(state string) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.t.Context(), `INSERT INTO river_job (kind, max_attempts, state, finalized_at, args)
		VALUES ('backup_run', 2, $1::river_job_state,
		        CASE WHEN $1 IN ('completed', 'discarded', 'cancelled') THEN now() END, '{}')
		RETURNING id`, state).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// staleRun inserts a run in status (queued/running) last changed `age` ago.
func (e *stEnv) staleRun(status string, river *int64, age time.Duration) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	at := time.Now().Add(-age).UTC().Truncate(time.Second)
	if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO runs (id, job_id, trigger, status, river_job_id, attempt, created_at, updated_at, queued_at)
		VALUES ($1, $2, 'schedule', $3::run_status, $4, 2, $5, $5, $5)`, id, e.job, status, river, at); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestFailAbandonedRuns(t *testing.T) {
	e := newStEnv(t)
	discarded := e.riverJob("discarded")
	completed := e.riverJob("completed")
	retryable := e.riverJob("retryable")
	available := e.riverJob("available")
	gone := int64(999999)

	abandoned := []uuid.UUID{
		e.staleRun("running", &discarded, time.Hour),
		e.staleRun("running", &completed, time.Hour),
		e.staleRun("queued", &gone, time.Hour),
	}
	kept := []uuid.UUID{
		e.staleRun("running", &retryable, time.Hour),   // the retry finishes it (Begin)
		e.staleRun("queued", &available, time.Hour),    // waiting for a slot
		e.staleRun("running", &discarded, time.Minute), // inside the grace period
	}
	obs := &recordingObserver{}
	n, err := e.store.WithObserver(obs).FailAbandonedRuns(t.Context())
	if err != nil || n != len(abandoned) {
		t.Fatalf("failed %d, %v", n, err)
	}
	for _, id := range abandoned {
		r, err := e.q.GetRun(t.Context(), id)
		if err != nil || r.Status != StatusFailed || r.ErrorCode == nil || *r.ErrorCode != CodeInterrupted || r.FinishedAt == nil {
			t.Errorf("abandoned run %s: %+v %v", id, r, err)
		}
	}
	for _, id := range kept {
		r, err := e.q.GetRun(t.Context(), id)
		if err != nil || (r.Status != StatusRunning && r.Status != StatusQueued) {
			t.Errorf("kept run %s: %s %v", id, r.Status, err)
		}
	}
	if got := obs.all(); len(got) != len(abandoned) {
		t.Errorf("observed %d runs", len(got))
	}
	// Each finished run notified (backup.failed) like any other failure.
	var events int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_events WHERE type = 'backup.failed' AND run_id = ANY($1)`,
		abandoned).Scan(&events); err != nil || events != len(abandoned) {
		t.Errorf("backup.failed events = %d, %v", events, err)
	}
	// Idempotent.
	if n, err := e.store.FailAbandonedRuns(t.Context()); err != nil || n != 0 {
		t.Fatalf("second call: %d %v", n, err)
	}
}

// An abandoned run whose cancellation was requested ends canceled, not
// failed.
func TestFailAbandonedRunsCancelRequested(t *testing.T) {
	e := newStEnv(t)
	riverDone := e.riverJob("discarded")
	id := uuid.New()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	// Inserted directly: an UPDATE would refresh updated_at (trigger).
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO runs (id, job_id, trigger, status, river_job_id, attempt, created_at, updated_at, queued_at, cancel_requested_at)
		VALUES ($1, $2, 'schedule', 'running', $3, 1, $4, $4, $4, $4)`, id, e.job, riverDone, at); err != nil {
		t.Fatal(err)
	}
	if n, err := e.store.FailAbandonedRuns(t.Context()); err != nil || n != 1 {
		t.Fatalf("finished %d, %v", n, err)
	}
	r, err := e.q.GetRun(t.Context(), id)
	if err != nil || r.Status != StatusCanceled || r.ErrorCode == nil || *r.ErrorCode != CodeCanceled || r.FinishedAt == nil {
		t.Fatalf("run %+v %v", r, err)
	}
}
