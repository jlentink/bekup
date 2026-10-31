//go:build integration

package retention

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

type holdRow struct {
	reason   string
	held     int
	since    time.Time
	newest   *time.Time
	firstAt  time.Time
	checkeAt time.Time
}

func (e *env) holds(job uuid.UUID) map[uuid.UUID]holdRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT destination_id, reason, held_runs, held_since, newest_complete_at, first_seen_at, checked_at
		FROM retention_holds WHERE job_id = $1`, job)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]holdRow{}
	for rows.Next() {
		var d uuid.UUID
		var h holdRow
		if err := rows.Scan(&d, &h.reason, &h.held, &h.since, &h.newest, &h.firstAt, &h.checkeAt); err != nil {
			e.t.Fatal(err)
		}
		out[d] = h
	}
	return out
}

type blockedEvent struct {
	dest   uuid.UUID
	dedup  string
	detail map[string]any
}

func (e *env) blockedEvents(job uuid.UUID) []blockedEvent {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT destination_id, dedup_key, detail FROM notification_events
		WHERE type = 'retention.blocked' AND job_id = $1 ORDER BY created_at, id`, job)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []blockedEvent
	for rows.Next() {
		var ev blockedEvent
		var raw []byte
		if err := rows.Scan(&ev.dest, &ev.dedup, &raw); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &ev.detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

// TestIntegrationRetentionHolds: a sweep records the incomplete runs kept
// only by the newer-than-newest-complete rule, emits retention.blocked once
// per job, destination and day, and clears the hold once a newer complete
// run exists (D-209).
func TestIntegrationRetentionHolds(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("h", false)
	other := e.diskDest("o", false)
	j := e.job(1, 0, false, disk, other)
	complete := e.run(j, 5*day, 2, on{dest: disk}, on{dest: other})
	held := make([]runInfo, 0, 3)
	for _, age := range []time.Duration{4 * day, 3 * day, 2 * day} {
		// Item 1 never reached disk: incomplete there, complete on other.
		held = append(held, e.run(j, age, 2, on{dest: disk, items: []int{0}}, on{dest: other}))
	}
	now := time.Now()
	w := e.worker(func(o *Options) { o.Now = func() time.Time { return now } })
	if _, err := w.Sweep(t.Context(), j.id, queue.RetentionPostRun, false); err != nil {
		t.Fatal(err)
	}
	hs := e.holds(j.id)
	h, ok := hs[disk]
	if len(hs) != 1 || !ok || h.reason != HoldNewerThanNewestComplete || h.held != 2 || h.newest == nil {
		t.Fatalf("holds = %+v", hs)
	}
	// The oldest held run is the one at 4 days (the newest is inside keep-last).
	if d := now.Add(-4 * day).Sub(h.since); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("held since %v", h.since)
	}
	// On disk the complete run is the newest complete one (kept); on
	// other the newer runs are complete, so it went there.
	for _, o := range complete.objects {
		if st, _ := e.state(o.id); (o.dest == disk) != (st == "present") {
			t.Errorf("complete run artifact %s on %s: %s", o.key, o.dest, st)
		}
	}
	for _, r := range held {
		for _, o := range r.objects {
			if o.dest == disk {
				if st, _ := e.state(o.id); st != "present" {
					t.Errorf("held run artifact %s: %s", o.key, st)
				}
			}
		}
	}
	evs := e.blockedEvents(j.id)
	if len(evs) != 1 || evs[0].dest != disk || evs[0].dedup != HoldDedupKey(j.id, disk, now) ||
		evs[0].detail["held_runs"] != float64(2) || evs[0].detail["reason"] != HoldNewerThanNewestComplete {
		t.Fatalf("events = %+v", evs)
	}
	// A second sweep the same day confirms the hold without a new event.
	later := now.Add(time.Hour)
	w2 := e.worker(func(o *Options) { o.Now = func() time.Time { return later } })
	if _, err := w2.Sweep(t.Context(), j.id, queue.RetentionDaily, false); err != nil {
		t.Fatal(err)
	}
	if evs := e.blockedEvents(j.id); len(evs) != 1 {
		t.Fatalf("deduplicated events = %d", len(evs))
	}
	if h2 := e.holds(j.id)[disk]; !h2.firstAt.Equal(h.firstAt) || !h2.checkeAt.After(h.checkeAt) {
		t.Fatalf("hold not confirmed: %+v -> %+v", h, h2)
	}
	// The next day: another event.
	tomorrow := now.Add(25 * time.Hour)
	w3 := e.worker(func(o *Options) { o.Now = func() time.Time { return tomorrow } })
	if _, err := w3.Sweep(t.Context(), j.id, queue.RetentionDaily, false); err != nil {
		t.Fatal(err)
	}
	if evs := e.blockedEvents(j.id); len(evs) != 2 {
		t.Fatalf("next-day events = %d", len(evs))
	}
	// A new complete run ends the hold (and the old runs become eligible).
	e.run(j, 0, 2, on{dest: disk}, on{dest: other})
	if _, err := w.Sweep(t.Context(), j.id, queue.RetentionPostRun, false); err != nil {
		t.Fatal(err)
	}
	if hs := e.holds(j.id); len(hs) != 0 {
		t.Fatalf("hold kept after a complete run: %+v", hs)
	}
	for _, o := range complete.objects {
		if st, _ := e.state(o.id); st != "deleted" {
			t.Errorf("old complete run artifact %s: %s", o.key, st)
		}
	}

	// No complete run at all; a deleted job's holds are dropped.
	nc := e.job(1, 0, false, disk)
	e.run(nc, 3*day, 2, on{dest: disk, items: []int{0}})
	e.run(nc, 2*day, 2, on{dest: disk, items: []int{0}})
	if _, err := w.Sweep(t.Context(), nc.id, queue.RetentionPostRun, false); err != nil {
		t.Fatal(err)
	}
	if h := e.holds(nc.id)[disk]; h.reason != HoldNoCompleteRun || h.held != 1 || h.newest != nil {
		t.Fatalf("no-complete hold = %+v", h)
	}
	if ev := e.blockedEvents(nc.id); len(ev) != 1 || ev[0].detail["newest_complete"] != nil {
		t.Fatalf("no-complete event = %+v", ev)
	}
	views, err := HoldsForJobs(t.Context(), e.pool, []uuid.UUID{nc.id, j.id})
	if err != nil || len(views[nc.id]) != 1 || len(views[j.id]) != 0 || views[nc.id][0].DestinationID != disk {
		t.Fatalf("hold views = %+v, %v", views, err)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE jobs SET deleted_at = now(), enabled = false WHERE id = $1`, nc.id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Sweep(t.Context(), nc.id, queue.RetentionDaily, false); err != nil {
		t.Fatal(err)
	}
	if hs := e.holds(nc.id); len(hs) != 0 {
		t.Fatalf("deleted job keeps holds: %+v", hs)
	}
}

type fakeScanner struct {
	mu    sync.Mutex
	calls []uuid.UUID
}

func (f *fakeScanner) ScanUnrecorded(_ context.Context, id uuid.UUID, d storage.Destination) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d == nil {
		panic("no destination")
	}
	f.calls = append(f.calls, id)
	return nil
}

// TestIntegrationDailyFailsAbandonedRunsAndScans: the daily fan-out fails
// abandoned runs first (their files then count for retention) and hands
// every non-deleted destination to the unrecorded-file scanner (D-209).
func TestIntegrationDailyFailsAbandonedRunsAndScans(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("s", false)
	gone := e.diskDest("g", false)
	if _, err := e.pool.Exec(t.Context(), `UPDATE destinations SET deleted_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	j := e.job(1, 0, false, disk)
	// A run left running long ago without a River job.
	stuck := uuid.New()
	ts := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO runs (id, job_id, trigger, status, started_at, storage_timestamp, river_job_id, created_at, updated_at)
		VALUES ($1, $2, 'schedule', 'running', $3, $3, 987654321, $3, $3)`, stuck, j.id, ts); err != nil {
		t.Fatal(err)
	}
	// A fresh running run stays.
	fresh := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO runs (id, job_id, trigger, status, started_at, storage_timestamp)
		VALUES ($1, $2, 'schedule', 'running', now(), date_trunc('second', now()))`, fresh, j.id); err != nil {
		t.Fatal(err)
	}
	// A hold left over for a job/destination without present artifacts
	// (no sweep would end it) is dropped.
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO retention_holds (job_id, destination_id, reason, held_runs, held_since, first_seen_at, checked_at)
		VALUES ($1, $2, 'no_complete_run', 1, now(), now(), now())`, j.id, disk); err != nil {
		t.Fatal(err)
	}
	sc := &fakeScanner{}
	store := runstate.NewStore(e.pool, nil, nil)
	w := e.worker(func(o *Options) { o.Runs = store; o.Unrecorded = sc })
	if err := w.Work(t.Context(), &river.Job[queue.RetentionSweepArgs]{Args: queue.RetentionSweepArgs{Reason: queue.RetentionDaily}}); err != nil {
		t.Fatal(err)
	}
	var st, code string
	if err := e.pool.QueryRow(t.Context(), `SELECT status::text, coalesce(error_code, '') FROM runs WHERE id = $1`, stuck).Scan(&st, &code); err != nil {
		t.Fatal(err)
	}
	if st != "failed" || code != runstate.CodeInterrupted {
		t.Fatalf("abandoned run: %s %s", st, code)
	}
	if err := e.pool.QueryRow(t.Context(), `SELECT status::text FROM runs WHERE id = $1`, fresh).Scan(&st); err != nil || st != "running" {
		t.Fatalf("fresh run: %s %v", st, err)
	}
	if len(sc.calls) != 1 || sc.calls[0] != disk {
		t.Fatalf("scanned %v, want only %s", sc.calls, disk)
	}
	if hs := e.holds(j.id); len(hs) != 0 {
		t.Fatalf("stale hold kept: %+v", hs)
	}
	// A dry-run daily neither fails runs nor scans.
	if err := w.Daily(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if len(sc.calls) != 1 {
		t.Fatalf("dry run scanned: %v", sc.calls)
	}
}
