//go:build integration

package scheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/scheduler"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type clock struct{ ns atomic.Int64 }

func newClock(t time.Time) *clock { c := &clock{}; c.set(t); return c }
func (c *clock) set(t time.Time)  { c.ns.Store(t.UnixNano()) }
func (c *clock) now() time.Time   { return time.Unix(0, c.ns.Load()).UTC() }

type countingMetrics struct {
	mu       sync.Mutex
	enqueued map[queue.Trigger]int
	skipped  map[scheduler.SkipReason]int
	ticks    int
}

func (m *countingMetrics) TickCompleted(time.Duration, error) { m.mu.Lock(); m.ticks++; m.mu.Unlock() }
func (m *countingMetrics) Enqueued(tr queue.Trigger) {
	m.mu.Lock()
	m.enqueued[tr]++
	m.mu.Unlock()
}
func (m *countingMetrics) Skipped(r scheduler.SkipReason) {
	m.mu.Lock()
	m.skipped[r]++
	m.mu.Unlock()
}

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	queue   *queue.Client
	clock   *clock
	metrics *countingMetrics
}

func newEnv(t *testing.T, start time.Time) *env {
	t.Helper()
	pool := dbtest.Migrated(t).AppPool(t)
	q, err := queue.New(pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, pool: pool, queue: q, clock: newClock(start),
		metrics: &countingMetrics{enqueued: map[queue.Trigger]int{}, skipped: map[scheduler.SkipReason]int{}}}
}

func (e *env) loop(batch int) *scheduler.Loop {
	e.t.Helper()
	l, err := scheduler.NewLoop(scheduler.LoopOptions{DB: e.pool, Queue: e.queue, Logger: slog.New(slog.DiscardHandler),
		Interval: 20 * time.Millisecond, BatchSize: batch, Now: e.clock.now, Metrics: e.metrics})
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *env) tick() scheduler.TickResult {
	e.t.Helper()
	res, err := e.loop(100).Tick(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

type jobRow struct {
	enabled   bool
	schedule  string
	nextRunAt *time.Time
	deleted   bool
}

func (e *env) addJob(j jobRow) uuid.UUID {
	e.t.Helper()
	id, err := dbtest.InsertJob(e.t.Context(), e.pool, dbtest.JobRow{
		Enabled: j.enabled, Schedule: j.schedule, NextRunAt: j.nextRunAt, Deleted: j.deleted,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) nextRunAt(id uuid.UUID) (next, last *time.Time) {
	e.t.Helper()
	if err := e.pool.QueryRow(e.t.Context(), "SELECT next_run_at, last_scheduled_at FROM jobs WHERE id = $1", id).Scan(&next, &last); err != nil {
		e.t.Fatal(err)
	}
	return next, last
}

type riverRun struct {
	args        queue.BackupRunArgs
	queue       string
	maxAttempts int
	state       string
}

func (e *env) runs(id uuid.UUID) []riverRun {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), "SELECT args, queue, max_attempts, state::text FROM river_job WHERE kind = 'backup_run' AND args->>'job_id' = $1::text ORDER BY id", id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []riverRun
	for rows.Next() {
		var raw []byte
		var r riverRun
		if err := rows.Scan(&raw, &r.queue, &r.maxAttempts, &r.state); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.args); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) skips(id uuid.UUID) []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), "SELECT reason FROM schedule_skips WHERE job_id = $1 ORDER BY id", id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func ptr(t time.Time) *time.Time { return &t }

const hourly = `{"kind":"interval","every":1,"unit":"hours","anchor":"2026-01-01T00:00","timezone":"UTC"}`

var t0 = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

func TestDueJobEnqueuedOnce(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	id := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	if r := e.tick(); r.Enqueued != 1 || r.CatchUp != 0 || r.Skipped != 0 {
		t.Fatalf("tick %+v", r)
	}
	runs := e.runs(id)
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	r := runs[0]
	if r.args.JobID != id || !r.args.ScheduledFor.Equal(t0) || r.args.Trigger != queue.TriggerSchedule ||
		r.queue != string(queue.QueueBackup) || r.maxAttempts != queue.MaxAttemptsBackup || r.state != "available" {
		t.Fatalf("run %+v", r)
	}
	next, last := e.nextRunAt(id)
	if next == nil || !next.Equal(t0.Add(time.Hour)) || last == nil || !last.Equal(e.clock.now()) {
		t.Fatalf("next %v last %v", next, last)
	}
	// A second tick at the same time does nothing.
	if r := e.tick(); r.Due != 0 {
		t.Fatalf("second tick %+v", r)
	}
	if len(e.runs(id)) != 1 {
		t.Fatal("enqueued twice")
	}
	if e.metrics.enqueued[queue.TriggerSchedule] != 1 {
		t.Fatalf("metrics %+v", e.metrics.enqueued)
	}
}

// failingQueue fails the inserts of one job (after writing something in the
// transaction, so the savepoint rollback is exercised).
type failingQueue struct {
	inner scheduler.Inserter
	bad   uuid.UUID
	calls atomic.Int32
}

func (f *failingQueue) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error) {
	if a, ok := args.(queue.BackupRunArgs); ok && a.JobID == f.bad {
		f.calls.Add(1)
		if _, err := f.inner.InsertTx(ctx, tx, args, o); err != nil {
			return queue.InsertResult{}, err
		}
		return queue.InsertResult{}, errors.New("queue unavailable for this job")
	}
	return f.inner.InsertTx(ctx, tx, args, o)
}

// TestFailingJobDoesNotBlockBatch: one job whose handling fails is rolled
// back alone (no River job, no run row, next_run_at unchanged), the other
// due jobs of the batch are enqueued, the failed job is tried once per tick
// and enqueued by a later tick once it works.
func TestFailingJobDoesNotBlockBatch(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	good1 := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0.Add(-2 * time.Second))})
	bad := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0.Add(-time.Second))})
	good2 := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	fq := &failingQueue{inner: e.queue, bad: bad}
	// Batch size 1: the failed job must not be locked again by the next
	// batch of the same tick.
	l, err := scheduler.NewLoop(scheduler.LoopOptions{DB: e.pool, Queue: fq, Logger: slog.New(slog.DiscardHandler),
		BatchSize: 1, Now: e.clock.now, Metrics: e.metrics})
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Tick(t.Context())
	if err != nil || res.Enqueued != 2 || res.Failed != 1 {
		t.Fatalf("tick %+v %v", res, err)
	}
	if fq.calls.Load() != 1 {
		t.Fatalf("the failing job was tried %d times in one tick", fq.calls.Load())
	}
	for _, id := range []uuid.UUID{good1, good2} {
		if len(e.runs(id)) != 1 {
			t.Fatalf("job %s not enqueued", id)
		}
	}
	if len(e.runs(bad)) != 0 {
		t.Fatal("the failed job's River insert was not rolled back")
	}
	var runs int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM runs WHERE job_id = $1", bad).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs of the failed job: %d %v", runs, err)
	}
	if next, _ := e.nextRunAt(bad); next == nil || !next.Equal(t0.Add(-time.Second)) {
		t.Fatalf("next_run_at of the failed job moved: %v", next)
	}
	// The next tick retries it; once it works it is enqueued.
	fq.bad = uuid.New()
	if res, err := l.Tick(t.Context()); err != nil || res.Enqueued != 1 || res.Failed != 0 {
		t.Fatalf("retry tick %+v %v", res, err)
	}
	if len(e.runs(bad)) != 1 {
		t.Fatal("the job was not enqueued after the failure cleared")
	}
}

func TestCatchUpAfterDowntime(t *testing.T) {
	e := newEnv(t, t0.Add(5*time.Hour+time.Minute))
	id := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	if r := e.tick(); r.Enqueued != 1 || r.CatchUp != 1 {
		t.Fatalf("tick %+v", r)
	}
	runs := e.runs(id)
	if len(runs) != 1 || runs[0].args.Trigger != queue.TriggerCatchUp || !runs[0].args.ScheduledFor.Equal(t0) {
		t.Fatalf("runs %+v", runs)
	}
	if next, _ := e.nextRunAt(id); next == nil || !next.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("next %v", next)
	}
}

func TestStillRunningSkipsOccurrence(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	id := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	e.tick()
	// The run is still queued (nobody works it) at the next occurrence.
	e.clock.set(t0.Add(time.Hour + time.Minute))
	if r := e.tick(); r.Enqueued != 0 || r.Skipped != 1 {
		t.Fatalf("tick %+v", r)
	}
	if runs := e.runs(id); len(runs) != 1 {
		t.Fatalf("overlapping runs %+v", runs)
	}
	if s := e.skips(id); len(s) != 1 || s[0] != string(scheduler.SkipStillRunning) {
		t.Fatalf("skips %v", s)
	}
	if next, _ := e.nextRunAt(id); next == nil || !next.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("next %v", next)
	}
	// Running blocks as well; once the run completed, the next occurrence
	// is enqueued.
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'running', attempted_at = now() WHERE args->>'job_id' = $1::text", id); err != nil {
		t.Fatal(err)
	}
	e.clock.set(t0.Add(2*time.Hour + time.Minute))
	if r := e.tick(); r.Skipped != 1 {
		t.Fatalf("tick while running %+v", r)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE args->>'job_id' = $1::text", id); err != nil {
		t.Fatal(err)
	}
	e.clock.set(t0.Add(3*time.Hour + time.Minute))
	if r := e.tick(); r.Enqueued != 1 {
		t.Fatalf("tick after completion %+v", r)
	}
	if runs := e.runs(id); len(runs) != 2 || !runs[1].args.ScheduledFor.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("runs %+v", runs)
	}
	if e.metrics.skipped[scheduler.SkipStillRunning] != 2 {
		t.Fatalf("metrics %+v", e.metrics.skipped)
	}
}

func TestIgnoredJobs(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	ids := map[string]uuid.UUID{
		"manual":   e.addJob(jobRow{enabled: true, schedule: `{"kind":"manual"}`, nextRunAt: ptr(t0)}),
		"disabled": e.addJob(jobRow{enabled: false, schedule: hourly, nextRunAt: ptr(t0)}),
		"deleted":  e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0), deleted: true}),
		"future":   e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0.Add(time.Hour))}),
		"unset":    e.addJob(jobRow{enabled: true, schedule: hourly}),
	}
	if r := e.tick(); r.Due != 0 {
		t.Fatalf("tick %+v", r)
	}
	for name, id := range ids {
		if runs := e.runs(id); len(runs) != 0 {
			t.Errorf("%s: enqueued %+v", name, runs)
		}
		if _, last := e.nextRunAt(id); last != nil {
			t.Errorf("%s: touched", name)
		}
	}
}

func TestUnschedulableJob(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	bad := e.addJob(jobRow{enabled: true, schedule: `{"kind":"interval","every":0,"unit":"hours","anchor":"2026-01-01T00:00","timezone":"UTC"}`, nextRunAt: ptr(t0)})
	good := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	if r := e.tick(); r.Enqueued != 1 || r.Skipped != 1 {
		t.Fatalf("tick %+v", r)
	}
	if next, last := e.nextRunAt(bad); next != nil || last == nil {
		t.Fatalf("bad job next %v last %v", next, last)
	}
	if s := e.skips(bad); len(s) != 1 || s[0] != string(scheduler.SkipUnschedulable) {
		t.Fatalf("skips %v", s)
	}
	if len(e.runs(bad)) != 0 || len(e.runs(good)) != 1 {
		t.Fatal("wrong runs")
	}
	e.clock.set(t0.Add(30 * time.Hour))
	e.tick()
	if len(e.skips(bad)) != 1 {
		t.Fatal("unschedulable job handled again")
	}
}

// TestDSTAdvance: a daily 02:30 job in Europe/Amsterdam across the spring
// gap (02:30 does not exist on 2026-03-29: shifted to 03:30 CEST) and the
// autumn overlap (02:30 on 2026-10-25 happens twice: the first, CEST, one).
func TestDSTAdvance(t *testing.T) {
	const daily = `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T02:30","timezone":"Europe/Amsterdam"}`
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		stored    time.Time
		wantNext  time.Time
		wantRunAt time.Time
	}{
		{"spring", time.Date(2026, 3, 28, 2, 30, 0, 0, ams), time.Date(2026, 3, 29, 3, 30, 0, 0, ams), time.Date(2026, 3, 28, 1, 30, 0, 0, time.UTC)},
		{"autumn", time.Date(2026, 10, 24, 2, 30, 0, 0, ams), time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), time.Date(2026, 10, 24, 0, 30, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, c.stored.Add(time.Minute))
			id := e.addJob(jobRow{enabled: true, schedule: daily, nextRunAt: ptr(c.stored)})
			e.tick()
			next, _ := e.nextRunAt(id)
			if next == nil || !next.Equal(c.wantNext) {
				t.Fatalf("next %v, want %v", next, c.wantNext.UTC())
			}
			// Same math as internal/scheduler (D-143).
			s, err := scheduler.Parse([]byte(daily), scheduler.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if n, err := scheduler.Next(s, e.clock.now()); err != nil || !n.Equal(*next) {
				t.Fatalf("scheduler.Next %v %v", n, err)
			}
			if runs := e.runs(id); len(runs) != 1 || !runs[0].args.ScheduledFor.Equal(c.wantRunAt) {
				t.Fatalf("runs %+v", runs)
			}
		})
	}
}

// TestConcurrentLoopsNeverDoubleInsert runs four loops (small batches,
// many ticks) against the same database and fake clock.
func TestConcurrentLoopsNeverDoubleInsert(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	const jobs = 150
	ids := make([]uuid.UUID, jobs)
	for i := range ids {
		ids[i] = e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0.Add(-time.Duration(i%3) * time.Hour))})
	}
	var wg sync.WaitGroup
	var enqueued atomic.Int64
	errs := make(chan error, 64)
	for i := range 4 {
		l := e.loop(3 + 2*i)
		wg.Go(func() {
			for range 8 {
				r, err := l.Tick(t.Context())
				if err != nil {
					errs <- err
					return
				}
				enqueued.Add(int64(r.Enqueued))
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if enqueued.Load() != jobs {
		t.Fatalf("enqueued %d, want %d", enqueued.Load(), jobs)
	}
	var total, distinct int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*), count(DISTINCT args->>'job_id') FROM river_job WHERE kind = 'backup_run'").Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total != jobs || distinct != jobs {
		t.Fatalf("river jobs %d (distinct %d), want %d", total, distinct, jobs)
	}
	var skips int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM schedule_skips").Scan(&skips); err != nil {
		t.Fatal(err)
	}
	if skips != 0 {
		t.Fatalf("%d skips", skips)
	}
	for _, id := range ids[:5] {
		if next, _ := e.nextRunAt(id); next == nil || !next.Equal(t0.Add(time.Hour)) {
			t.Fatalf("next %v", next)
		}
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := newEnv(t, t0.Add(time.Minute))
	id := e.addJob(jobRow{enabled: true, schedule: hourly, nextRunAt: ptr(t0)})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.loop(10).Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(e.runs(id)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("loop never enqueued")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestSkipCleanupWorker(t *testing.T) {
	e := newEnv(t, t0)
	id := e.addJob(jobRow{enabled: true, schedule: hourly})
	now := time.Now()
	for i, age := range []time.Duration{scheduler.SkipRetention + time.Hour, scheduler.SkipRetention + 48*time.Hour, time.Hour} {
		if _, err := e.pool.Exec(t.Context(), "INSERT INTO schedule_skips (job_id, reason, created_at) VALUES ($1, 'still_running', $2)", id, now.Add(-age)); err != nil {
			t.Fatal(fmt.Errorf("row %d: %w", i, err))
		}
	}
	w := &scheduler.SkipCleanupWorker{DB: e.pool, Now: func() time.Time { return now }}
	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if s := e.skips(id); len(s) != 1 {
		t.Fatalf("kept %d rows, want 1", len(s))
	}
	// bekup_app cannot hard-delete jobs (soft delete only).
	if _, err := e.pool.Exec(t.Context(), "DELETE FROM jobs WHERE id = $1", id); err == nil {
		t.Fatal("bekup_app deleted a job")
	}
}
