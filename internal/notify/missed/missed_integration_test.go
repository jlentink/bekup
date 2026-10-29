//go:build integration

package missed

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/scheduler"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	return &env{t: t, pool: d.AppPool(t)}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// job inserts a job with a one-hour expected window.
func (e *env) job(j dbtest.JobRow) uuid.UUID {
	e.t.Helper()
	if j.Schedule == "" {
		j.Schedule = `{"kind":"cron","cron":"0 3 * * *","timezone":"UTC"}`
	}
	id, err := dbtest.InsertJob(e.t.Context(), e.pool, j)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE jobs SET expected_window_seconds = 3600 WHERE id = $1`, id)
	return id
}

// run inserts a scheduled run for occurrence `for` (finished nil = running).
func (e *env) run(job uuid.UUID, trigger, status string, scheduledFor time.Time, finished *time.Time) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	started := scheduledFor.Add(time.Minute)
	if trigger == "early" || trigger == "manual" {
		started = finished.Add(-time.Minute)
	}
	var sf *time.Time
	if trigger != "manual" {
		sf = &scheduledFor
	}
	var storage *time.Time
	if status == "success" || status == "partial" {
		s := started.Truncate(time.Second)
		storage = &s
	}
	e.exec(`INSERT INTO runs (id, job_id, trigger, scheduled_for, status, queued_at, started_at, finished_at, storage_timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $8)`, id, job, trigger, sf, status, started, finished, storage)
	return id
}

type evRow struct {
	Job    uuid.UUID
	Key    string
	Detail map[string]any
}

func (e *env) events() []evRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT job_id, dedup_key, detail FROM notification_events WHERE type = 'backup.missed' ORDER BY created_at, dedup_key`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []evRow
	for rows.Next() {
		var r evRow
		var raw []byte
		if err := rows.Scan(&r.Job, &r.Key, &raw); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) detector(now time.Time) *Detector {
	e.t.Helper()
	d, err := New(Options{DB: e.pool, DefaultTimeout: 6 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) check(now time.Time) Result {
	e.t.Helper()
	r, err := e.detector(now).Check(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func ptr(t time.Time) *time.Time { return &t }

func TestMissedBackups(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC().Truncate(time.Second)
	future := now.Add(72 * time.Hour)

	// The scheduler never enqueued an occurrence three hours ago.
	pending := e.job(dbtest.JobRow{Enabled: true, NextRunAt: ptr(now.Add(-3 * time.Hour))})
	// Successful scheduled run inside the window.
	ok := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	e.run(ok, "schedule", "success", now.Add(-3*time.Hour), ptr(now.Add(-150*time.Minute)))
	// Partial counts as not missed.
	partial := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	e.run(partial, "schedule", "partial", now.Add(-3*time.Hour), ptr(now.Add(-150*time.Minute)))
	// Failed run, no success since.
	failed := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	prev := e.run(failed, "schedule", "success", now.Add(-27*time.Hour), ptr(now.Add(-26*time.Hour)))
	e.run(failed, "schedule", "failed", now.Add(-3*time.Hour), ptr(now.Add(-170*time.Minute)))
	// Skipped occurrence (still running) covered by the long run.
	long := e.job(dbtest.JobRow{Enabled: true, NextRunAt: &future})
	e.run(long, "schedule", "success", now.Add(-210*time.Minute), ptr(now.Add(-165*time.Minute)))
	e.exec(`INSERT INTO schedule_skips (job_id, scheduled_for, reason) VALUES ($1, $2, 'still_running')`, long, now.Add(-3*time.Hour))
	// An occurrence whose window has not passed yet.
	young := e.job(dbtest.JobRow{Enabled: true, NextRunAt: ptr(now.Add(-30 * time.Minute))})
	// Ignored: disabled, manual, deleted.
	e.job(dbtest.JobRow{Enabled: false, NextRunAt: ptr(now.Add(-3 * time.Hour))})
	e.job(dbtest.JobRow{Enabled: true, Schedule: `{"kind":"manual"}`, NextRunAt: ptr(now.Add(-3 * time.Hour))})
	e.job(dbtest.JobRow{Enabled: true, Deleted: true, NextRunAt: ptr(now.Add(-3 * time.Hour))})

	r := e.check(now)
	evs := e.events()
	if r.Emitted != 2 || len(evs) != 2 {
		t.Fatalf("result %+v, events %+v", r, evs)
	}
	got := map[uuid.UUID]evRow{}
	for _, ev := range evs {
		got[ev.Job] = ev
	}
	if ev, ok := got[pending]; !ok || ev.Key != DedupKey(pending, uuid.Nil) || ev.Detail["reason"] != "not_enqueued" ||
		ev.Detail["scheduled_for"] != now.Add(-3*time.Hour).Format(time.RFC3339) || ev.Detail["window_seconds"] != float64(3600) {
		t.Fatalf("pending job event %+v", ev)
	}
	if ev, ok := got[failed]; !ok || ev.Key != DedupKey(failed, prev) || ev.Detail["reason"] != "run_failed" || ev.Detail["ongoing"] != true {
		t.Fatalf("failed job event %+v", ev)
	}
	if _, ok := got[young]; ok {
		t.Fatal("an occurrence inside its window was notified")
	}
	// The scheduler picks it up in time.
	e.exec(`UPDATE jobs SET next_run_at = $2 WHERE id = $1`, young, future)

	// Re-running emits nothing new.
	if r := e.check(now); r.Emitted != 0 || r.Outages != 2 {
		t.Fatalf("second check %+v", r)
	}
	if n := len(e.events()); n != 2 {
		t.Fatalf("events after re-run: %d", n)
	}

	// A later occurrence of the failed job fails too: same outage, no new
	// event within the reminder interval.
	e.run(failed, "schedule", "failed", now.Add(-2*time.Hour), ptr(now.Add(-110*time.Minute)))
	if r := e.check(now.Add(time.Hour)); r.Emitted != 0 {
		t.Fatalf("same outage re-notified: %+v", r)
	}

	// A day later the outages still last: one reminder each, once.
	later := now.Add(ReminderInterval + time.Hour)
	if r := e.check(later); r.Emitted != 2 {
		t.Fatalf("reminders %+v", r)
	}
	if r := e.check(later); r.Emitted != 0 {
		t.Fatalf("reminders repeated %+v", r)
	}
	evs = e.events()
	if len(evs) != 4 || evs[2].Detail["reminder"] != float64(1) {
		t.Fatalf("events with reminders %+v", evs)
	}

	// The failed job recovers, then misses again: a new outage.
	e.run(failed, "manual", "success", time.Time{}, ptr(later.Add(-30*time.Minute)))
	rec := e.run(failed, "schedule", "success", later.Add(-20*time.Minute), ptr(later.Add(-10*time.Minute)))
	e.run(failed, "schedule", "failed", later.Add(time.Hour), ptr(later.Add(70*time.Minute)))
	if r := e.check(later.Add(3 * time.Hour)); r.Emitted != 1 {
		// The failed job's new outage; the pending job's next reminder is
		// not due yet and the old outage of the failed job ended.
		t.Fatalf("after recovery %+v", r)
	}
	found := false
	for _, ev := range e.events() {
		if ev.Key == DedupKey(failed, rec) {
			found = true
		}
	}
	if !found {
		t.Fatal("new outage after recovery not notified")
	}
}

func TestMissedDST(t *testing.T) {
	e := newEnv(t)
	sched := `{"kind":"cron","cron":"30 2 * * *","timezone":"Europe/Amsterdam"}`
	p, err := scheduler.CompileStored([]byte(sched))
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 does not exist on 2026-03-29 in Amsterdam: the occurrence moves
	// forward by the gap to 03:30 CEST = 01:30 UTC (D-143).
	occ, err := p.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC); !occ.Equal(want) {
		t.Fatalf("occurrence %s, want %s", occ, want)
	}
	next, err := p.Next(occ)
	if err != nil {
		t.Fatal(err)
	}
	missedJob := e.job(dbtest.JobRow{Enabled: true, Schedule: sched, NextRunAt: &next})
	e.run(missedJob, "schedule", "failed", occ, ptr(occ.Add(5*time.Minute)))
	okJob := e.job(dbtest.JobRow{Enabled: true, Schedule: sched, NextRunAt: &next})
	e.run(okJob, "schedule", "success", occ, ptr(occ.Add(10*time.Minute)))

	now := occ.Add(5 * time.Hour)
	if r := e.check(now); r.Emitted != 1 {
		t.Fatalf("result %+v", r)
	}
	evs := e.events()
	if len(evs) != 1 || evs[0].Job != missedJob || evs[0].Detail["scheduled_for"] != "2026-03-29T01:30:00Z" {
		t.Fatalf("events %+v", evs)
	}
}
