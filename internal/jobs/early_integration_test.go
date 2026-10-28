//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func (e *env) nextRunAt(id uuid.UUID) *time.Time {
	e.t.Helper()
	var next *time.Time
	if err := e.pool.QueryRow(e.t.Context(), "SELECT next_run_at FROM jobs WHERE id = $1", id).Scan(&next); err != nil {
		e.t.Fatal(err)
	}
	return next
}

func (e *env) runCount(id uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), "SELECT count(*) FROM runs WHERE job_id = $1", id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) finishQueueJob(id int64) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = $1", id); err != nil {
		e.t.Fatal(err)
	}
}

func fieldError(t *testing.T, err error, want string) {
	t.Helper()
	var fe *serviceutil.FieldError
	if !errors.As(err, &fe) || !strings.Contains(fe.Message, want) {
		t.Fatalf("want a validation error containing %q, got %v", want, err)
	}
}

func TestStartNextRunEarly(t *testing.T) {
	e := newEnv(t)
	// Daily at 03:00 Amsterdam; created on 27 March the next occurrence is
	// 28 March 03:00 CET and the one after it 29 March 03:00 CEST (the
	// clocks spring forward in between: 23 hours later).
	e.setNow(time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC))
	j := e.create("Early")
	original := time.Date(2026, 3, 28, 2, 0, 0, 0, time.UTC)
	following := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)
	if got := e.nextRunAt(j.ID); got == nil || !got.Equal(original) {
		t.Fatalf("initial next_run_at: %v", got)
	}
	if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}

	// Owners of other jobs (and other users) are denied, nothing changes.
	if _, err := e.svc.StartNextRunEarly(e.other.ctx, e.other.actor, j.ID); apiCode(err) != apierror.Forbidden {
		t.Fatalf("non-owner: %v", err)
	}
	// The actor must be the caller.
	if _, err := e.svc.StartNextRunEarly(e.owner.ctx, e.admin.actor, j.ID); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("actor mismatch: %v", err)
	}

	res, err := e.svc.StartNextRunEarly(e.owner.ctx, e.owner.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ScheduledFor.Equal(original) || !res.NextRunAt.Equal(following) || res.QueueJobID == 0 || res.RunID == uuid.Nil {
		t.Fatalf("result: %+v", res)
	}
	if got := e.nextRunAt(j.ID); got == nil || !got.Equal(following) {
		t.Fatalf("next_run_at after early start: %v", got)
	}
	var raw []byte
	if err := e.pool.QueryRow(t.Context(), "SELECT args FROM river_job WHERE id = $1", res.QueueJobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var args queue.BackupRunArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	if args.JobID != j.ID || args.Trigger != queue.TriggerEarly || !args.ScheduledFor.Equal(original) ||
		args.TriggeredBy == nil || *args.TriggeredBy != e.owner.actor.UserID {
		t.Fatalf("args: %+v", args)
	}
	run, err := e.q.GetRun(t.Context(), res.RunID)
	if err != nil || run.Status != sqlc.RunStatusQueued || run.Trigger != sqlc.RunTriggerEarly ||
		run.ScheduledFor == nil || !run.ScheduledFor.Equal(original) || run.RiverJobID == nil || *run.RiverJobID != res.QueueJobID ||
		run.TriggeredBy == nil || *run.TriggeredBy != e.owner.actor.UserID {
		t.Fatalf("queued run: %+v %v", run, err)
	}
	entries := e.audits(audit.ActionJobRunStartedEarly)
	if len(entries) != 1 || entries[0]["scheduled_for"] != original.Format(time.RFC3339) ||
		entries[0]["next_run_at"] != following.Format(time.RFC3339) || entries[0]["run_id"] != res.RunID.String() {
		t.Fatalf("audit: %+v", entries)
	}

	// A second request before the original slot: the following occurrence
	// is not the natural next one, so it is refused (D-160).
	_, err = e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, j.ID)
	fieldError(t, err, "already started early")
	// After the original slot, while the early run is still queued:
	// CONFLICT, nothing changed.
	e.setNow(original.Add(time.Minute))
	if _, err := e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, j.ID); !errors.Is(err, ErrRunActive) {
		t.Fatalf("overlap: %v", err)
	}
	if got := e.nextRunAt(j.ID); got == nil || !got.Equal(following) {
		t.Fatalf("next_run_at after conflict: %v", got)
	}
	if n := len(e.audits(audit.ActionJobRunStartedEarly)); n != 1 {
		t.Fatalf("audits after conflict: %d", n)
	}
	if n := e.runCount(j.ID); n != 1 {
		t.Fatalf("runs after conflict: %d", n)
	}
	// runJobNow stays an extra run and is refused while one is active.
	if _, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j.ID); !errors.Is(err, ErrRunActive) {
		t.Fatalf("run now overlap: %v", err)
	}

	// The original slot does not run: the scheduler loop, with its clock
	// past the original time (and the early run finished), finds nothing due.
	e.finishQueueJob(res.QueueJobID)
	qc, err := queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	loopNow := original.Add(time.Hour)
	loop, err := scheduler.NewLoop(scheduler.LoopOptions{DB: e.pool, Queue: qc, Logger: slog.New(slog.DiscardHandler),
		Interval: 5 * time.Millisecond, Now: func() time.Time { return loopNow }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.runCount(j.ID); n != 1 {
		t.Fatalf("runs after the original slot: %d", n)
	}
	if got := e.nextRunAt(j.ID); got == nil || !got.Equal(following) {
		t.Fatalf("next_run_at after the original slot: %v", got)
	}
	// At the following occurrence the scheduler runs as usual.
	loopNow = following.Add(time.Minute)
	if r, err := loop.Tick(t.Context()); err != nil || r.Enqueued != 1 || r.CatchUp != 0 {
		t.Fatalf("tick at the following occurrence: %+v %v", r, err)
	}
	if n := e.runCount(j.ID); n != 2 {
		t.Fatalf("runs after the following occurrence: %d", n)
	}
}

func TestStartNextRunEarlyMonthEnd(t *testing.T) {
	e := newEnv(t)
	e.setNow(time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC))
	in := e.input("Month end")
	in.Schedule = scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitMonths, Anchor: "2026-01-31T02:00", DayOfMonth: 31, Timezone: "UTC"}
	c, err := e.svc.Create(e.maint.ctx, e.maint.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	c.Destroy()
	want := []time.Time{
		time.Date(2026, 2, 28, 2, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 2, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 30, 2, 0, 0, 0, time.UTC),
	}
	prev := time.Date(2026, 1, 31, 2, 0, 0, 0, time.UTC)
	var last time.Time
	for i, w := range want {
		if i > 0 {
			// Only the natural next occurrence can be started early (D-160):
			// let the slot before prev (pulled forward in the previous step)
			// pass first.
			e.setNow(last.Add(time.Hour))
		}
		last = prev
		res, err := e.svc.StartNextRunEarly(e.maint.ctx, e.maint.actor, c.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !res.ScheduledFor.Equal(prev) || !res.NextRunAt.Equal(w) {
			t.Fatalf("step %d: %+v", i, res)
		}
		if got := e.nextRunAt(c.Job.ID); got == nil || !got.Equal(w) {
			t.Fatalf("step %d next_run_at: %v", i, got)
		}
		e.finishQueueJob(res.QueueJobID)
		prev = w
	}
}

// Back-to-back early starts cannot push the schedule further ahead (D-160):
// once the next occurrence was pulled forward, the following one is refused
// until the natural slot of the first has passed.
func TestStartNextRunEarlyOnlyOnceAhead(t *testing.T) {
	e := newEnv(t)
	e.setNow(time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC))
	j := e.create("Once ahead")
	if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}
	first, err := e.svc.StartNextRunEarly(e.owner.ctx, e.owner.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.finishQueueJob(first.QueueJobID)

	// The early run finished; another early start would postpone the planned
	// backups by one more occurrence: refused for owners and admins alike,
	// and nothing changes.
	for _, u := range []struct {
		ctx   context.Context
		actor Actor
	}{{e.owner.ctx, e.owner.actor}, {e.admin.ctx, e.admin.actor}} {
		_, err := e.svc.StartNextRunEarly(u.ctx, u.actor, j.ID)
		fieldError(t, err, "the next planned run was already started early")
	}
	if got := e.nextRunAt(j.ID); got == nil || !got.Equal(first.NextRunAt) {
		t.Fatalf("next_run_at after the refusal: %v", got)
	}
	if n := e.runCount(j.ID); n != 1 {
		t.Fatalf("runs after the refusal: %d", n)
	}
	if n := len(e.audits(audit.ActionJobRunStartedEarly)); n != 1 {
		t.Fatalf("audits after the refusal: %d", n)
	}

	// Once the natural slot of the pulled-forward occurrence has passed, the
	// stored next_run_at is the natural next occurrence again.
	e.setNow(first.ScheduledFor.Add(time.Minute))
	second, err := e.svc.StartNextRunEarly(e.owner.ctx, e.owner.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ScheduledFor.Equal(first.NextRunAt) || !second.NextRunAt.After(second.ScheduledFor) {
		t.Fatalf("second: %+v (first %+v)", second, first)
	}
}

func TestStartNextRunEarlyOverdue(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	e.setNow(now)
	j := e.create("Overdue")
	// Missed for days (e.g. the scheduler was down): the run stands for the
	// stored occurrence and the schedule continues after now.
	stale := time.Date(2026, 6, 7, 1, 0, 0, 0, time.UTC)
	if _, err := e.pool.Exec(t.Context(), "UPDATE jobs SET next_run_at = $2 WHERE id = $1", j.ID, stale); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC); !res.ScheduledFor.Equal(stale) || !res.NextRunAt.Equal(want) {
		t.Fatalf("overdue: %+v", res)
	}
}

func TestStartNextRunEarlyRefused(t *testing.T) {
	e := newEnv(t)
	// Manual schedule.
	in := e.input("Manual")
	in.Schedule = scheduler.Schedule{Kind: scheduler.KindManual}
	c, err := e.svc.Create(e.admin.ctx, e.admin.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	c.Destroy()
	_, err = e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, c.Job.ID)
	fieldError(t, err, "manual schedule")

	// Disabled.
	off := e.create("Off")
	if _, err := e.svc.SetEnabled(e.admin.ctx, e.admin.actor, off.ID, false); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, off.ID)
	fieldError(t, err, "disabled")

	// Enabled without a planned run (the scheduler reset an unusable
	// schedule).
	none := e.create("No plan")
	if _, err := e.pool.Exec(t.Context(), "UPDATE jobs SET next_run_at = NULL WHERE id = $1", none.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, none.ID)
	fieldError(t, err, "no planned run")

	// Deleted and unknown jobs are not found.
	if err := e.svc.Delete(e.admin.ctx, e.admin.actor, off.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{off.ID, uuid.New()} {
		if _, err := e.svc.StartNextRunEarly(e.admin.ctx, e.admin.actor, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted/unknown %s: %v", id, err)
		}
	}
	for _, id := range []uuid.UUID{c.Job.ID, off.ID, none.ID} {
		if n := e.runCount(id); n != 0 {
			t.Fatalf("runs of refused job %s: %d", id, n)
		}
	}
	if n := len(e.audits(audit.ActionJobRunStartedEarly)); n != 0 {
		t.Fatalf("audits of refusals: %d", n)
	}
}
