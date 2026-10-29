//go:build integration

package missed

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/runstate"
)

func (e *env) stateRows() map[uuid.UUID]time.Time {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT job_id, missed_since FROM job_missed_state`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]time.Time{}
	for rows.Next() {
		var id uuid.UUID
		var since time.Time
		if err := rows.Scan(&id, &since); err != nil {
			e.t.Fatal(err)
		}
		out[id] = since.UTC()
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// TestMissedState covers the detector's state behind Job.missed (D-196).
func TestMissedState(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	future := now.Add(72 * time.Hour)
	rd := runstate.NewReader(e.pool)

	pending := e.job(dbtest.JobRow{Enabled: true, NextRunAt: ptr(now.Add(-3 * time.Hour))})
	failed := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	e.run(failed, "schedule", "failed", now.Add(-3*time.Hour), ptr(now.Add(-170*time.Minute)))
	ok := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	e.run(ok, "schedule", "success", now.Add(-3*time.Hour), ptr(now.Add(-150*time.Minute)))

	e.check(now)
	st := e.stateRows()
	if len(st) != 2 || !st[pending].Equal(now.Add(-3*time.Hour)) || !st[failed].Equal(now.Add(-3*time.Hour)) {
		t.Fatalf("state after the first check: %v", st)
	}
	m, err := rd.MissedForJobs(ctx, []uuid.UUID{pending, failed, ok})
	if err != nil {
		t.Fatal(err)
	}
	if m[pending] == nil || m[failed] == nil || m[ok] != nil || m[failed].Reason != "run_failed" || m[pending].Reason != "not_enqueued" {
		t.Fatalf("MissedForJobs: %+v", m)
	}

	// A run that succeeds after the check ends the outage for readers at
	// once, and for the state at the next check.
	e.run(failed, "manual", "success", time.Time{}, ptr(time.Now().UTC().Add(time.Second)))
	if m, err = rd.MissedForJobs(ctx, []uuid.UUID{failed}); err != nil || m[failed] != nil {
		t.Fatalf("after a success: %+v %v", m, err)
	}
	e.check(now.Add(time.Minute))
	if st := e.stateRows(); len(st) != 1 || st[pending].IsZero() {
		t.Fatalf("state after recovery: %v", st)
	}

	// A disabled job is no longer watched: its state is pruned and it
	// never reads as missed.
	e.exec(`UPDATE jobs SET enabled = false WHERE id = $1`, pending)
	if m, err = rd.MissedForJobs(ctx, []uuid.UUID{pending}); err != nil || m[pending] != nil {
		t.Fatalf("disabled job: %+v %v", m, err)
	}
	e.check(now.Add(2 * time.Minute))
	if st := e.stateRows(); len(st) != 0 {
		t.Fatalf("state after disabling: %v", st)
	}
}
