//go:build integration

package calendar

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type seed struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
}

func (s seed) exec(sql string, args ...any) {
	s.t.Helper()
	if _, err := s.pool.Exec(s.ctx, sql, args...); err != nil {
		s.t.Fatal(err)
	}
}

func (s seed) dest(name string) uuid.UUID {
	id := uuid.New()
	s.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, $2, 'disk', '{"path":"x"}')`, id, name)
	return id
}

// run inserts a run of job with n database items.
func (s seed) run(job uuid.UUID, status string, n int, at time.Time) (uuid.UUID, []uuid.UUID) {
	id := uuid.New()
	var fin *time.Time
	if status != "queued" && status != "running" {
		fin = &at
	}
	s.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, storage_timestamp)
		VALUES ($1, $2, 'schedule', $3, $4, $4, $5, date_trunc('second', $4::timestamptz))`, id, job, status, at, fin)
	items := make([]uuid.UUID, n)
	for i := range items {
		items[i] = uuid.New()
		s.exec(`INSERT INTO run_items (id, run_id, kind, db_name, position, status, finished_at) VALUES ($1, $2, 'database', $3, $4, 'success', now())`,
			items[i], id, "db"+string(rune('a'+i)), i)
	}
	return id, items
}

// artifact stores item (uuid.Nil: the manifest) of run on dest.
func (s seed) artifact(job, run, item, dest uuid.UUID, verified bool, lock *time.Time) {
	sum := make([]byte, 32)
	if item == uuid.Nil {
		s.exec(`INSERT INTO artifacts (run_id, job_id, destination_id, kind, object_key, size, sha256, verified, lock_until)
			VALUES ($1, $2, $3, 'manifest', $4, 1, $5, true, $6)`, run, job, dest, uuid.NewString(), sum, lock)
		return
	}
	s.exec(`INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256, encryption_mode, verified, lock_until)
		VALUES ($1, $2, $3, $4, 'database', $5, 1, $6, 'none', $7, $8)`, run, item, job, dest, uuid.NewString(), sum, verified, lock)
}

// TestCopiesCompleteness checks the per-copy summary has the meaning of
// retention.Plan: complete needs every item verified here plus the
// manifest; unfinished runs and non-present artifacts are ignored.
func TestCopiesCompleteness(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	s := seed{t: t, ctx: t.Context(), pool: pool}
	destA, destB := s.dest("a"), s.dest("b")
	job, err := dbtest.InsertJob(s.ctx, pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{destA, destB}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lock := now.Add(30 * 24 * time.Hour).Truncate(time.Second)

	full, fi := s.run(job, "success", 2, now.Add(-3*time.Hour))
	for _, it := range fi {
		s.artifact(job, full, it, destA, true, nil)
		s.artifact(job, full, it, destB, it != fi[1], &lock) // destB: one unverified
	}
	s.artifact(job, full, uuid.Nil, destA, true, nil)
	s.artifact(job, full, uuid.Nil, destB, true, nil)

	partial, pi := s.run(job, "partial", 2, now.Add(-2*time.Hour))
	s.artifact(job, partial, pi[0], destA, true, nil) // second item missing
	s.artifact(job, partial, uuid.Nil, destA, true, nil)

	noManifest, ni := s.run(job, "success", 1, now.Add(-time.Hour))
	s.artifact(job, noManifest, ni[0], destA, true, nil)

	active, ai := s.run(job, "running", 1, now)
	s.artifact(job, active, ai[0], destA, true, nil)
	s.artifact(job, active, uuid.Nil, destA, true, nil)

	deleted, di := s.run(job, "success", 1, now.Add(-4*time.Hour))
	s.artifact(job, deleted, di[0], destA, true, nil)
	s.exec(`UPDATE artifacts SET state = 'deleted', deleted_at = now() WHERE run_id = $1`, deleted)

	rows, err := sqlc.New(pool).ListCalendarCopies(s.ctx, sqlc.ListCalendarCopiesParams{JobIds: []uuid.UUID{job}, MaxRows: 100})
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ run, dest uuid.UUID }
	got := map[key]sqlc.ListCalendarCopiesRow{}
	for _, r := range rows {
		got[key{r.RunID, r.DestinationID}] = r
	}
	for k, want := range map[key]bool{
		{full, destA}: true, {full, destB}: false, {partial, destA}: false, {noManifest, destA}: false,
	} {
		r, ok := got[k]
		if !ok || r.Complete != want {
			t.Fatalf("copy %v: %+v (present %v), want complete=%v", k, r, ok, want)
		}
	}
	if len(got) != 4 {
		t.Fatalf("got %d copies (active or deleted ones included?)", len(got))
	}
	if r := got[key{full, destB}]; !r.LockUntil.Equal(lock) {
		t.Fatalf("lock_until %v", r.LockUntil)
	}
	if r := got[key{full, destA}]; r.LockUntil.After(time.Unix(0, 0)) {
		t.Fatalf("no lock expected: %v", r.LockUntil)
	}
}

// TestBuildScope checks the job scope: owners see their jobs only, a
// deleted owner sees nothing, deleted jobs never appear.
func TestBuildScope(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	s := seed{t: t, ctx: t.Context(), pool: pool}
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Minute)
	sched := `{"kind":"cron","cron":"0 * * * *","timezone":"UTC"}`
	mine, err := dbtest.InsertJob(s.ctx, pool, dbtest.JobRow{Enabled: true, Schedule: sched, NextRunAt: &next})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := dbtest.InsertJob(s.ctx, pool, dbtest.JobRow{Enabled: true, Schedule: sched, NextRunAt: &next})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := dbtest.InsertJob(s.ctx, pool, dbtest.JobRow{Enabled: true, Schedule: sched, NextRunAt: &next, Deleted: true})
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	s.exec(`INSERT INTO users (id, email, role) VALUES ($1, 'o@example.com', 'user')`, user)
	s.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2), ($3, $2)`, mine, user, gone)

	svc := New(pool)
	req := Request{From: time.Now(), To: time.Now().Add(48 * time.Hour), Timezone: "UTC"}
	jobsOf := func(c Calendar) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, e := range c.Events {
			out[e.JobID] = true
		}
		return out
	}
	c, err := svc.Build(s.ctx, Scope{Owner: user}, req)
	if err != nil {
		t.Fatal(err)
	}
	if j := jobsOf(c); !j[mine] || j[theirs] || j[gone] {
		t.Fatalf("owner sees %v", j)
	}
	c, err = svc.Build(s.ctx, Scope{All: true}, req)
	if err != nil {
		t.Fatal(err)
	}
	if j := jobsOf(c); !j[mine] || !j[theirs] || j[gone] {
		t.Fatalf("staff sees %v", j)
	}
	s.exec(`UPDATE users SET deleted_at = now(), disabled = true WHERE id = $1`, user)
	c, err = svc.Build(s.ctx, Scope{Owner: user}, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Events) != 0 {
		t.Fatalf("deleted owner sees %d events", len(c.Events))
	}
	if _, err := svc.Build(s.ctx, Scope{}, req); !errors.Is(err, ErrBadScope) {
		t.Fatalf("empty scope: %v", err)
	}
}

// TestRunLookbackByStart checks the run scan looks back by the start time:
// a run that waited in the queue for longer than the look-back but started
// recently and overlaps the range is shown; one that finished before the
// range is not; a started-early run marks its original slot in the range
// even when it ran long before.
func TestRunLookbackByStart(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	s := seed{t: t, ctx: t.Context(), pool: pool}
	job, err := dbtest.InsertJob(s.ctx, pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	from, to := now.Add(-24*time.Hour), now.Add(24*time.Hour)
	longQueued, done, early := uuid.New(), uuid.New(), uuid.New()
	s.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at)
		VALUES ($1, $2, 'manual', 'success', $3, $4, $5)`, longQueued, job, from.Add(-20*24*time.Hour), from.Add(-time.Hour), from.Add(time.Hour))
	s.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at)
		VALUES ($1, $2, 'manual', 'success', $3, $3, $4)`, done, job, from.Add(-3*time.Hour), from.Add(-2*time.Hour))
	slot := now.Add(2 * time.Hour)
	s.exec(`INSERT INTO runs (id, job_id, trigger, status, scheduled_for, queued_at, started_at, finished_at)
		VALUES ($1, $2, 'early', 'success', $3, $4, $4, $5)`, early, job, slot, from.Add(-30*24*time.Hour), from.Add(-30*24*time.Hour+time.Hour))

	c, err := New(pool).Build(s.ctx, Scope{All: true}, Request{From: from, To: to, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	runs, marked := map[uuid.UUID]bool{}, false
	for _, e := range c.Events {
		if e.Kind == KindRun {
			runs[*e.RunID] = true
		}
		if e.Kind == KindPlanned && e.StartedEarly && *e.RunID == early && e.Start.Equal(slot) {
			marked = true
		}
	}
	if !runs[longQueued] || runs[done] || runs[early] || !marked {
		t.Fatalf("runs %v, early slot marked %v: %+v", runs, marked, c.Events)
	}
}

// TestCalendarReadLimits checks the calendar transaction is read only and
// bounded by the statement timeout (D-160).
func TestCalendarReadLimits(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if err := sqlc.New(tx).SetCalendarReadLimits(ctx, "5000ms"); err != nil {
		t.Fatal(err)
	}
	var timeout, ro string
	if err := tx.QueryRow(ctx, "SELECT current_setting('statement_timeout'), current_setting('transaction_read_only')").Scan(&timeout, &ro); err != nil {
		t.Fatal(err)
	}
	if timeout != "5s" || ro != "on" {
		t.Fatalf("statement_timeout %q, read only %q", timeout, ro)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM schedule_skips"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write in the calendar transaction: %v", err)
	}
	// The limits are transaction-local: the pool's next session is unchanged.
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT current_setting('statement_timeout')").Scan(&timeout); err != nil || timeout == "5s" {
		t.Fatalf("statement_timeout leaked: %q %v", timeout, err)
	}
}
