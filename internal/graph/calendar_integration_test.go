//go:build integration

package graph_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

const calendarQuery = `query($from: Time!, $to: Time!, $tz: String!, $ids: [UUID!]) {
	calendar(from: $from, to: $to, timezone: $tz, jobIds: $ids) {
		truncated from to timezone
		events { jobId jobName kind start end status trigger runId destinationId projected paused startedEarly detail } } }`

type calendarGQL struct {
	Truncated bool
	Timezone  string
	Events    []struct {
		JobID         string
		Kind          string
		Start         time.Time
		Status        *string
		Trigger       *string
		RunID         *string
		DestinationID *string
		Projected     bool
		Paused        bool
		Detail        *string
	}
}

const dailySchedule = `{"kind":"cron","cron":"0 3 * * *","timezone":"UTC"}`

func (e *runEnv) calendar(cookie string, vars map[string]any) calendarGQL {
	e.t.Helper()
	var c calendarGQL
	e.mustGQL(cookie, calendarQuery, vars).field(e.t, "calendar", &c)
	return c
}

// scheduleDaily gives job a daily 03:00 UTC schedule and its next_run_at.
func (e *runEnv) scheduleDaily(job uuid.UUID, enabled bool) time.Time {
	e.t.Helper()
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	if _, err := e.pool.Exec(e.t.Context(), `UPDATE jobs SET schedule = $2::jsonb, next_run_at = $3, enabled = $4 WHERE id = $1`,
		job, dailySchedule, next, enabled); err != nil {
		e.t.Fatal(err)
	}
	return next
}

func TestCalendarGraphQL(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC, ownerC, otherC := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")
	ctx := t.Context()

	nextA := e.scheduleDaily(e.jobA, true) // keep_last 7
	e.scheduleDaily(e.jobB, true)
	if _, err := e.pool.Exec(ctx, `UPDATE jobs SET retention_keep_last = NULL, retention_keep_days = 3 WHERE id = $1`, e.jobB); err != nil {
		t.Fatal(err)
	}
	paused, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{DestinationIDs: []uuid.UUID{e.dest}})
	if err != nil {
		t.Fatal(err)
	}
	e.scheduleDaily(paused, false)

	runA := e.newRun(e.jobA, nil)
	e.completeRun(runA)
	oldB, newB := e.newRun(e.jobB, nil), e.newRun(e.jobB, nil)
	e.completeRun(oldB)
	e.completeRun(newB)
	// Storage timestamps have second precision: order the two explicitly.
	if _, err := e.pool.Exec(ctx, `UPDATE runs SET storage_timestamp = date_trunc('second', now() - interval '1 hour') WHERE id = $1`, oldB.RunID); err != nil {
		t.Fatal(err)
	}
	queuedB := e.newRun(e.jobB, nil)
	skipAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	if _, err := e.pool.Exec(ctx, `INSERT INTO schedule_skips (job_id, scheduled_for, reason) VALUES ($1, $2, 'still_running')`, e.jobB, skipAt); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	vars := map[string]any{"from": now.Add(-24 * time.Hour).Format(time.RFC3339), "to": now.Add(30 * 24 * time.Hour).Format(time.RFC3339), "tz": "Europe/Amsterdam"}

	c := e.calendar(adminC, vars)
	if c.Truncated || c.Timezone != "Europe/Amsterdam" {
		t.Fatalf("calendar: truncated=%v tz=%q", c.Truncated, c.Timezone)
	}
	count := map[string]map[string]int{}
	for _, ev := range c.Events {
		if count[ev.JobID] == nil {
			count[ev.JobID] = map[string]int{}
		}
		count[ev.JobID][ev.Kind]++
		switch {
		case ev.Kind == "PLANNED" && ev.JobID == paused.String() && !ev.Paused:
			t.Fatalf("disabled job not paused: %+v", ev)
		case ev.Kind == "PLANNED" && ev.JobID != paused.String() && ev.Paused:
			t.Fatalf("enabled job paused: %+v", ev)
		case ev.Kind == "RUN" && (ev.Status == nil || ev.Trigger == nil || ev.RunID == nil):
			t.Fatalf("run event: %+v", ev)
		case ev.Kind == "EXPIRY" && (ev.DestinationID == nil || *ev.DestinationID != e.dest.String() || ev.Detail == nil):
			t.Fatalf("expiry event: %+v", ev)
		case ev.Kind == "SKIPPED" && (!ev.Start.Equal(skipAt) || ev.Detail == nil):
			t.Fatalf("skip event: %+v", ev)
		}
	}
	a, b := count[e.jobA.String()], count[e.jobB.String()]
	if a["PLANNED"] < 29 || a["RUN"] != 1 || a["EXPIRY"] != 1 || b["RUN"] != 3 || b["SKIPPED"] != 1 || b["EXPIRY"] != 2 || count[paused.String()]["PLANNED"] < 29 {
		t.Fatalf("counts: %v", count)
	}
	for _, ev := range c.Events {
		switch {
		case ev.Kind == "EXPIRY" && ev.JobID == e.jobA.String():
			// keep_last 7: removable after 7 more daily runs (projected).
			if want := nextA.AddDate(0, 0, 6); !ev.Projected || !ev.Start.Equal(want) || *ev.RunID != runA.RunID.String() {
				t.Fatalf("keep_last expiry %+v, want %v", ev, want)
			}
		case ev.Kind == "EXPIRY" && *ev.RunID == oldB.RunID.String():
			if ev.Projected || ev.Start.Before(now.Add(70*time.Hour)) || ev.Start.After(now.Add(72*time.Hour)) {
				t.Fatalf("keep_days expiry %+v", ev)
			}
		case ev.Kind == "EXPIRY" && *ev.RunID == newB.RunID.String():
			if !ev.Projected {
				t.Fatalf("newest complete run must be projected: %+v", ev)
			}
		case ev.Kind == "RUN" && *ev.RunID == queuedB.RunID.String():
			if *ev.Status != "QUEUED" || *ev.Trigger != "MANUAL" {
				t.Fatalf("queued run %+v", ev)
			}
		}
	}

	// Owners see their own jobs only; other ids are dropped silently.
	only := func(c calendarGQL, job string) {
		t.Helper()
		if len(c.Events) == 0 && job != "" {
			t.Fatal("no events")
		}
		for _, ev := range c.Events {
			if ev.JobID != job {
				t.Fatalf("foreign event %+v", ev)
			}
		}
	}
	only(e.calendar(ownerC, vars), e.jobA.String())
	withIDs := func(ids ...uuid.UUID) map[string]any {
		v := map[string]any{"ids": append([]uuid.UUID{}, ids...)} // [] (no jobs), never null
		for k, x := range vars {
			v[k] = x
		}
		return v
	}
	only(e.calendar(ownerC, withIDs(e.jobA, e.jobB)), e.jobA.String())
	only(e.calendar(ownerC, withIDs(e.jobB)), "")
	only(e.calendar(otherC, vars), "")
	only(e.calendar(adminC, withIDs(e.jobB)), e.jobB.String())
	only(e.calendar(adminC, withIDs()), "")

	// Validation.
	for name, v := range map[string]map[string]any{
		"range > 93 days": {"from": now.Format(time.RFC3339), "to": now.Add(94 * 24 * time.Hour).Format(time.RFC3339), "tz": "UTC"},
		"to before from":  {"from": now.Format(time.RFC3339), "to": now.Add(-time.Hour).Format(time.RFC3339), "tz": "UTC"},
		"bad timezone":    {"from": now.Format(time.RFC3339), "to": now.Add(time.Hour).Format(time.RFC3339), "tz": "Mars/Base"},
		"local timezone":  {"from": now.Format(time.RFC3339), "to": now.Add(time.Hour).Format(time.RFC3339), "tz": "Local"},
	} {
		if r := e.gql(adminC, calendarQuery, v); r.code() != "VALIDATION" {
			t.Fatalf("%s: %+v", name, r.Errors)
		}
	}
	if r := e.gql("", calendarQuery, vars); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous: %+v", r.Errors)
	}
}

// TestCalendarQueryCount checks the calendar loads ~50 jobs with a fixed
// number of queries (no per-job queries), all inside its transaction.
func TestCalendarQueryCount(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	adminC := e.mustLogin("admin@example.com")
	jobs := make([]uuid.UUID, 0, 50)
	jobs = append(jobs, e.jobA, e.jobB)
	for range 48 {
		id, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{e.dest}})
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, id)
	}
	for i, j := range jobs {
		e.scheduleDaily(j, true)
		ref := e.newRun(j, nil)
		if i%5 == 0 {
			e.completeRun(ref)
		}
	}
	now := time.Now().UTC()
	vars := map[string]any{"from": now.Add(-24 * time.Hour).Format(time.RFC3339), "to": now.Add(14 * 24 * time.Hour).Format(time.RFC3339), "tz": "UTC"}
	e.counter.n.Store(0)
	c := e.calendar(adminC, vars)
	// Four batched reads plus the read-only/statement-timeout setup (D-160).
	if n := e.counter.n.Load(); n > 5 {
		t.Fatalf("calendar used %d queries for %d jobs", n, len(jobs))
	}
	seen := map[string]bool{}
	for _, ev := range c.Events {
		seen[ev.JobID] = true
	}
	if len(seen) != len(jobs) || c.Truncated {
		t.Fatalf("saw %d of %d jobs (truncated=%v)", len(seen), len(jobs), c.Truncated)
	}
}
