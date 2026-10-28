package calendar

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/scheduler"
)

var (
	now   = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	destA = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	day   = 24 * time.Hour
)

func mustPlan(t *testing.T, s scheduler.Schedule) *scheduler.Plan {
	t.Helper()
	p, err := scheduler.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// dailyAt3 is a daily cron at 03:00 Amsterdam time.
func dailyAt3(t *testing.T) *scheduler.Plan {
	return mustPlan(t, scheduler.Schedule{Kind: scheduler.KindCron, Cron: "0 3 * * *", Timezone: "Europe/Amsterdam"})
}

// enabledJob is an enabled job whose next_run_at is the plan's next
// occurrence after now.
func enabledJob(t *testing.T, name string, p *scheduler.Plan, pol retention.Policy) job {
	t.Helper()
	next, err := p.Next(now)
	if err != nil {
		t.Fatal(err)
	}
	return job{ID: uuid.New(), Name: name, Enabled: true, Plan: p, NextRunAt: &next, Policy: pol}
}

func baseInput(from, to time.Time, jobs ...job) input {
	return input{Now: now, From: from, To: to, Timezone: "Europe/Amsterdam", Jobs: jobs, Copies: map[uuid.UUID][]retention.Copy{}}
}

func ofKind(c Calendar, k Kind) []Event {
	var out []Event
	for i := range c.Events {
		if c.Events[i].Kind == k {
			out = append(out, c.Events[i])
		}
	}
	return out
}

func TestPlannedMatchesNextN(t *testing.T) {
	p := dailyAt3(t)
	j := enabledJob(t, "a", p, retention.Policy{KeepLast: 3})
	c := compute(baseInput(now.Add(-5*day), now.Add(10*day), j))
	got := ofKind(c, KindPlanned)
	want, err := p.NextN(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d planned, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Start.Equal(want[i]) || got[i].Paused || got[i].StartedEarly {
			t.Fatalf("occurrence %d: %+v, want %v", i, got[i], want[i])
		}
	}
	if c.Truncated {
		t.Fatal("unexpected truncation")
	}
}

func TestPlannedFutureRangeAndManual(t *testing.T) {
	p := dailyAt3(t)
	j := enabledJob(t, "a", p, retention.Policy{KeepLast: 3})
	manual := job{ID: uuid.New(), Name: "m", Enabled: true, Plan: mustPlan(t, scheduler.Schedule{Kind: scheduler.KindManual})}
	noNext := job{ID: uuid.New(), Name: "n", Enabled: true, Plan: p}
	from := now.Add(30 * day)
	c := compute(baseInput(from, from.Add(7*day), j, manual, noNext))
	got := ofKind(c, KindPlanned)
	if len(got) != 7 {
		t.Fatalf("got %d planned", len(got))
	}
	for _, e := range got {
		if e.JobID != j.ID || e.Start.Before(from) {
			t.Fatalf("unexpected %+v", e)
		}
	}
	// A range entirely in the past plans nothing.
	if n := len(ofKind(compute(baseInput(now.Add(-9*day), now.Add(-day), j)), KindPlanned)); n != 0 {
		t.Fatalf("past range planned %d", n)
	}
}

func TestPlannedAcrossDST(t *testing.T) {
	// Europe/Amsterdam leaves DST on 2026-10-25: 03:00 local is 01:00 UTC
	// before and 02:00 UTC after.
	p := dailyAt3(t)
	j := enabledJob(t, "a", p, retention.Policy{KeepLast: 1})
	c := compute(baseInput(now, now.Add(10*day), j))
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ofKind(c, KindPlanned) {
		if h := e.Start.In(ams).Hour(); h != 3 {
			t.Fatalf("local hour %d at %v", h, e.Start)
		}
		wantUTC := 1
		if e.Start.After(time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)) {
			wantUTC = 2
		}
		if e.Start.Hour() != wantUTC || e.Start.Location() != time.UTC {
			t.Fatalf("UTC %v", e.Start)
		}
	}
}

func TestPausedJob(t *testing.T) {
	p := dailyAt3(t)
	j := job{ID: uuid.New(), Name: "off", Enabled: false, Plan: p, Policy: retention.Policy{KeepLast: 1}}
	got := ofKind(compute(baseInput(now, now.Add(5*day), j)), KindPlanned)
	if len(got) != 5 {
		t.Fatalf("got %d", len(got))
	}
	for _, e := range got {
		if !e.Paused || e.Detail == "" {
			t.Fatalf("not paused: %+v", e)
		}
	}
}

func TestStartedEarlySuppression(t *testing.T) {
	p := dailyAt3(t)
	j := enabledJob(t, "a", p, retention.Policy{KeepLast: 1})
	slot := *j.NextRunAt
	started := now.Add(-time.Hour)
	r := run{ID: uuid.New(), JobID: j.ID, Status: "success", Trigger: triggerEarly, ScheduledFor: &slot,
		QueuedAt: started, StartedAt: &started, FinishedAt: ptr(now.Add(-30 * time.Minute))}
	in := baseInput(now, now.Add(3*day), j)
	in.Runs = []run{r}
	in.Skips = []skip{{JobID: j.ID, ScheduledFor: &slot, Reason: "started_early", CreatedAt: slot}}
	c := compute(in)
	planned := ofKind(c, KindPlanned)
	if len(planned) != 3 {
		t.Fatalf("got %d planned: %+v", len(planned), planned)
	}
	early := 0
	for _, e := range planned {
		if e.Start.Equal(slot) {
			early++
			if !e.StartedEarly || e.RunID == nil || *e.RunID != r.ID {
				t.Fatalf("slot not marked: %+v", e)
			}
		} else if e.StartedEarly {
			t.Fatalf("wrongly marked %+v", e)
		}
	}
	if early != 1 {
		t.Fatalf("slot shown %d times", early)
	}
	if n := len(ofKind(c, KindSkipped)); n != 0 {
		t.Fatalf("skip of the pulled-forward slot shown: %d", n)
	}
	// The early run itself (before the range) is not a run event here.
	if n := len(ofKind(c, KindRun)); n != 0 {
		t.Fatalf("run events: %d", n)
	}
}

func TestSkipped(t *testing.T) {
	j := enabledJob(t, "a", dailyAt3(t), retention.Policy{KeepLast: 1})
	in := baseInput(now.Add(-3*day), now, j)
	in1, out := now.Add(-2*day), now.Add(-5*day)
	in.Skips = []skip{
		{JobID: j.ID, ScheduledFor: &in1, Reason: "still_running", CreatedAt: in1},
		{JobID: j.ID, ScheduledFor: &out, Reason: "still_running", CreatedAt: out},
		{JobID: j.ID, Reason: "unschedulable", CreatedAt: now.Add(-day)},
		{JobID: j.ID, Reason: "Bad Reason!", CreatedAt: now.Add(-day)},
	}
	got := ofKind(compute(in), KindSkipped)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Detail != "Skipped: the previous run was still running" || got[2].Detail != "Skipped" {
		t.Fatalf("details: %+v", got)
	}
}

func TestRuns(t *testing.T) {
	j := job{ID: uuid.New(), Name: "a", Enabled: true}
	from, to := now.Add(-2*day), now.Add(day)
	at := func(d time.Duration) *time.Time { return ptr(now.Add(d)) }
	code := "dump_failed"
	runs := []run{
		{ID: uuid.New(), JobID: j.ID, Status: "success", Trigger: "schedule", QueuedAt: now.Add(-day), StartedAt: at(-day), FinishedAt: at(-day + time.Hour)},
		{ID: uuid.New(), JobID: j.ID, Status: "failed", Trigger: "manual", QueuedAt: now.Add(-3 * day), StartedAt: at(-3 * day), FinishedAt: at(-2*day + time.Minute), ErrorCode: &code},
		{ID: uuid.New(), JobID: j.ID, Status: "success", Trigger: "schedule", QueuedAt: now.Add(-5 * day), StartedAt: at(-5 * day), FinishedAt: at(-5*day + time.Hour)},
		{ID: uuid.New(), JobID: j.ID, Status: "queued", Trigger: "schedule", QueuedAt: now.Add(-time.Minute), ScheduledFor: at(-2 * time.Minute)},
		{ID: uuid.New(), JobID: j.ID, Status: "running", Trigger: "early", QueuedAt: now.Add(-time.Hour), StartedAt: at(-time.Hour), ScheduledFor: at(5 * day)},
	}
	in := baseInput(from, to, j)
	in.Runs = runs
	got := ofKind(compute(in), KindRun)
	if len(got) != 4 {
		t.Fatalf("got %d runs: %+v", len(got), got)
	}
	if got[0].Status != "failed" || got[0].Detail != code || got[0].End == nil {
		t.Fatalf("overlapping failed run: %+v", got[0])
	}
	queued := got[3]
	if queued.Status != "queued" || !queued.Start.Equal(now.Add(-2*time.Minute)) || queued.End != nil {
		t.Fatalf("queued run: %+v", queued)
	}
	if got[2].Trigger != "early" || got[2].End != nil {
		t.Fatalf("running early run: %+v", got[2])
	}
	// A future range does not show runs that are active now.
	in.From, in.To = now.Add(day), now.Add(2*day)
	if n := len(ofKind(compute(in), KindRun)); n != 0 {
		t.Fatalf("future range shows %d runs", n)
	}
}

// expiryOf returns the expiry event of run r.
func expiryOf(c Calendar, r uuid.UUID) (Event, bool) {
	evs := ofKind(c, KindExpiry)
	for i := range evs {
		if evs[i].RunID != nil && *evs[i].RunID == r {
			return evs[i], true
		}
	}
	return Event{}, false
}

func TestExpiries(t *testing.T) {
	p := dailyAt3(t)
	occ, err := p.NextN(now, 5)
	if err != nil {
		t.Fatal(err)
	}
	old, mid, newest := uuid.New(), uuid.New(), uuid.New()
	lock := now.Add(20 * day)
	copies := []retention.Copy{
		{RunID: old, DestinationID: destA, Time: now.Add(-10 * day), Complete: true, LockUntil: lock},
		{RunID: mid, DestinationID: destA, Time: now.Add(-5 * day), Complete: true},
		{RunID: newest, DestinationID: destA, Time: now.Add(-1 * day), Complete: true},
	}
	type want struct {
		at        time.Time
		projected bool
	}
	for name, tc := range map[string]struct {
		pol     retention.Policy
		enabled bool
		want    map[uuid.UUID]want
		none    []uuid.UUID
	}{
		"keep_days only: exact, newest projected": {
			pol: retention.Policy{KeepDays: 7}, enabled: true,
			want: map[uuid.UUID]want{old: {lock, false}, mid: {now.Add(2 * day), false}, newest: {now.Add(6 * day), true}},
		},
		"keep_last only: projected from planned successes": {
			pol: retention.Policy{KeepLast: 2}, enabled: true,
			want: map[uuid.UUID]want{old: {lock, false}, mid: {occ[0], true}, newest: {occ[1], true}},
		},
		"both: the later date": {
			pol: retention.Policy{KeepLast: 2, KeepDays: 6}, enabled: true,
			want: map[uuid.UUID]want{old: {lock, false}, mid: {now.Add(day), true}, newest: {now.Add(5 * day), true}},
		},
		"paused job: only exact expiries": {
			pol: retention.Policy{KeepLast: 2, KeepDays: 1}, enabled: false,
			want: map[uuid.UUID]want{old: {lock, false}},
			none: []uuid.UUID{mid, newest},
		},
	} {
		t.Run(name, func(t *testing.T) {
			j := enabledJob(t, "a", p, tc.pol)
			j.Enabled = tc.enabled
			in := baseInput(now.Add(-day), now.Add(30*day), j)
			in.Copies[j.ID] = copies
			c := compute(in)
			for id, w := range tc.want {
				e, ok := expiryOf(c, id)
				if !ok {
					t.Fatalf("no expiry for %s: %+v", id, ofKind(c, KindExpiry))
				}
				if !e.Start.Equal(w.at) || e.Projected != w.projected || e.DestinationID == nil || *e.DestinationID != destA || e.Detail == "" {
					t.Errorf("%s: %+v, want %v projected=%v", id, e, w.at, w.projected)
				}
			}
			for _, id := range tc.none {
				if e, ok := expiryOf(c, id); ok {
					t.Errorf("unexpected %+v", e)
				}
			}
		})
	}

	// Expiries outside the range are not shown.
	j := enabledJob(t, "a", p, retention.Policy{KeepDays: 7})
	in := baseInput(now.Add(10*day), now.Add(15*day), j)
	in.Copies[j.ID] = copies
	if n := len(ofKind(compute(in), KindExpiry)); n != 0 {
		t.Fatalf("out of range expiries: %d", n)
	}
}

func TestTruncation(t *testing.T) {
	every5 := mustPlan(t, scheduler.Schedule{Kind: scheduler.KindInterval, Every: 5, Unit: scheduler.UnitMinutes,
		Anchor: "2026-01-01T00:00", Timezone: "UTC"})
	j := enabledJob(t, "a", every5, retention.Policy{KeepLast: 1})
	c := compute(baseInput(now, now.Add(10*day), j))
	if !c.Truncated || len(ofKind(c, KindPlanned)) != MaxPlannedPerJob {
		t.Fatalf("per job: truncated=%v n=%d", c.Truncated, len(c.Events))
	}
	jobs := make([]job, 0, 12)
	for range 12 {
		jobs = append(jobs, enabledJob(t, "j", every5, retention.Policy{KeepLast: 1}))
	}
	c = compute(baseInput(now, now.Add(10*day), jobs...))
	if !c.Truncated || len(c.Events) != MaxEvents {
		t.Fatalf("total: truncated=%v n=%d", c.Truncated, len(c.Events))
	}
	for i := 1; i < len(c.Events); i++ {
		if compareEvents(c.Events[i-1], c.Events[i]) > 0 {
			t.Fatal("events not sorted")
		}
	}
	// The projection budget is shared and bounded.
	pr := &projector{plan: every5, next: *j.NextRunAt, now: now, to: now.Add(90 * day), budget: ptr(10), trunc: ptr(false)}
	if _, ok := pr.nth(11); ok || !*pr.trunc {
		t.Fatal("projection budget not enforced")
	}
	if _, ok := pr.nth(10); !ok {
		t.Fatal("projection within budget")
	}
}

func TestValidate(t *testing.T) {
	ok := Request{From: now, To: now.Add(93 * day), Timezone: "Europe/Amsterdam"}
	if _, err := validate(ok); err != nil {
		t.Fatal(err)
	}
	dup := uuid.New()
	r, err := validate(Request{From: now, To: now.Add(day), Timezone: "UTC", JobIDs: []uuid.UUID{dup, dup}})
	if err != nil || len(r.JobIDs) != 1 {
		t.Fatalf("dedupe: %v %v", r.JobIDs, err)
	}
	if r, err := validate(Request{From: now, To: now.Add(day), Timezone: "UTC", JobIDs: []uuid.UUID{}}); err != nil || r.JobIDs == nil {
		t.Fatal("an empty filter must stay empty (no jobs), not nil (all jobs)")
	}
	for name, tc := range map[string]struct {
		req   Request
		field string
	}{
		"to before from": {Request{From: now, To: now.Add(-time.Hour), Timezone: "UTC"}, "to"},
		"empty range":    {Request{From: now, To: now, Timezone: "UTC"}, "to"},
		"too long":       {Request{From: now, To: now.Add(93*day + time.Second), Timezone: "UTC"}, "to"},
		"too early":      {Request{From: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC), To: now, Timezone: "UTC"}, "from"},
		"bad zone":       {Request{From: now, To: now.Add(day), Timezone: "Mars/Olympus"}, "timezone"},
		"local zone":     {Request{From: now, To: now.Add(day), Timezone: "Local"}, "timezone"},
		"empty zone":     {Request{From: now, To: now.Add(day)}, "timezone"},
		"path zone":      {Request{From: now, To: now.Add(day), Timezone: "../etc/passwd"}, "timezone"},
		"too many ids":   {Request{From: now, To: now.Add(day), Timezone: "UTC", JobIDs: make([]uuid.UUID, MaxJobIDs+1)}, "jobIds"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := validate(tc.req)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("got %v, want a %s error", err, tc.field)
			}
		})
	}
}

// hourly is an hourly UTC schedule on the full hour.
func hourly(t *testing.T) *scheduler.Plan {
	return mustPlan(t, scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitHours,
		Anchor: "2026-01-01T00:00", Timezone: "UTC"})
}

// An overdue job's projection is one catch-up run at now, then the
// occurrences after now (scheduler.Plan.Due, D-143), not one projected
// run per missed occurrence.
func TestExpiryProjectionOverdue(t *testing.T) {
	p := hourly(t)
	overdue := now.Add(-5 * time.Hour)
	j := job{ID: uuid.New(), Name: "late", Enabled: true, Plan: p, NextRunAt: &overdue, Policy: retention.Policy{KeepLast: 3}}
	pr := &projector{plan: p, next: overdue, now: now, to: now.Add(day), budget: ptr(100), trunc: ptr(false)}
	for n, want := range []time.Time{now, now.Add(time.Hour), now.Add(2 * time.Hour)} {
		if got, ok := pr.nth(n + 1); !ok || !got.Equal(want) {
			t.Fatalf("nth(%d) = %v %v, want %v", n+1, got, ok, want)
		}
	}

	c1, c2, c3 := uuid.New(), uuid.New(), uuid.New()
	in := baseInput(now.Add(-day), now.Add(day), j)
	in.Copies[j.ID] = []retention.Copy{
		{RunID: c1, DestinationID: destA, Time: now.Add(-8 * time.Hour), Complete: true},
		{RunID: c2, DestinationID: destA, Time: now.Add(-7 * time.Hour), Complete: true},
		{RunID: c3, DestinationID: destA, Time: now.Add(-6 * time.Hour), Complete: true},
	}
	c := compute(in)
	for id, want := range map[uuid.UUID]time.Time{c1: now, c2: now.Add(time.Hour), c3: now.Add(2 * time.Hour)} {
		e, ok := expiryOf(c, id)
		if !ok || !e.Start.Equal(want) || !e.Projected {
			t.Errorf("expiry of %s: %+v (%v), want %v projected", id, e, ok, want)
		}
	}
}

// An enabled overdue job shows its pending catch-up run at now (Overdue),
// then continues after now; a range after now shows no catch-up.
func TestPlannedOverdue(t *testing.T) {
	p := hourly(t)
	overdue := now.Add(-5 * time.Hour)
	j := job{ID: uuid.New(), Name: "late", Enabled: true, Plan: p, NextRunAt: &overdue, Policy: retention.Policy{KeepLast: 1}}
	got := ofKind(compute(baseInput(now.Add(-day), now.Add(150*time.Minute), j)), KindPlanned)
	want := []time.Time{now, now.Add(time.Hour), now.Add(2 * time.Hour)}
	if len(got) != len(want) {
		t.Fatalf("got %d planned: %+v", len(got), got)
	}
	for i, w := range want {
		if !got[i].Start.Equal(w) || got[i].Overdue != (i == 0) || (i == 0 && got[i].Detail == "") {
			t.Fatalf("occurrence %d: %+v, want %v", i, got[i], w)
		}
	}
	// A range starting after now: no catch-up event, the occurrences from
	// the range start (including one exactly at it).
	from := now.Add(3 * time.Hour)
	got = ofKind(compute(baseInput(from, from.Add(2*time.Hour), j)), KindPlanned)
	if len(got) != 2 || !got[0].Start.Equal(from) || got[0].Overdue || got[1].Overdue {
		t.Fatalf("future range: %+v", got)
	}
	// A paused overdue job is not overdue: it will not run.
	j.Enabled = false
	for _, e := range ofKind(compute(baseInput(now.Add(-day), now.Add(day), j)), KindPlanned) {
		if e.Overdue || !e.Paused {
			t.Fatalf("paused job: %+v", e)
		}
	}
}

// A truncated calendar is a time prefix: the cap keeps the earliest
// events, even when later ones (expiries) were computed first.
func TestTruncationTimePrefix(t *testing.T) {
	every5 := mustPlan(t, scheduler.Schedule{Kind: scheduler.KindInterval, Every: 5, Unit: scheduler.UnitMinutes,
		Anchor: "2026-01-01T00:00", Timezone: "UTC"})
	jobs := make([]job, 0, 12)
	for range 12 {
		jobs = append(jobs, enabledJob(t, "j", every5, retention.Policy{KeepLast: 1}))
	}
	// 6,000 planned occurrences in the first ~42 hours and 6,000 expiries
	// about nine days later (keep-days, exact).
	kd := job{ID: uuid.New(), Name: "kd", Enabled: false, Plan: every5, Policy: retention.Policy{KeepDays: 9}}
	jobs = append(jobs, kd)
	in := baseInput(now, now.Add(10*day), jobs...)
	expiryAt := now.Add(9*day - time.Hour)
	for range 6000 {
		in.Copies[kd.ID] = append(in.Copies[kd.ID], retention.Copy{RunID: uuid.New(), DestinationID: destA, Time: now.Add(-time.Hour), Complete: true})
	}
	// The paused job's own planned occurrences are dropped by the cap too.
	c := compute(in)
	if !c.Truncated || len(c.Events) != MaxEvents {
		t.Fatalf("truncated=%v n=%d", c.Truncated, len(c.Events))
	}
	if n := len(ofKind(c, KindExpiry)); n != 0 {
		t.Fatalf("%d later expiries kept over earlier occurrences", n)
	}
	last := c.Events[len(c.Events)-1].Start
	if !last.Before(expiryAt) || !last.Before(now.Add(2*day)) {
		t.Fatalf("last kept event at %v", last)
	}
	for i := 1; i < len(c.Events); i++ {
		if compareEvents(c.Events[i-1], c.Events[i]) > 0 {
			t.Fatal("events not sorted")
		}
	}
}
