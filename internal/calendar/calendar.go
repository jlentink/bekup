package calendar

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Limits.
const (
	// MaxRange is the longest calendar range (D-156: 93 days).
	MaxRange = 93 * 24 * time.Hour
	// MaxEvents caps the events of one calendar.
	MaxEvents = 5000
	// MaxPlannedPerJob caps the planned occurrences of one job.
	MaxPlannedPerJob = 500
	// MaxJobs caps the jobs of one calendar.
	MaxJobs = 500
	// MaxJobIDs caps the jobIds filter.
	MaxJobIDs = 500
	// MaxRuns and MaxSkips cap the loaded runs and skip records.
	MaxRuns  = 5000
	MaxSkips = 5000
	// MaxCopies caps the loaded (run, destination) copies.
	MaxCopies = 50000
	// MaxProjection caps the schedule computations of the expiry
	// projection over all jobs of one calendar.
	MaxProjection = 100000
	// runLookback bounds the run scan: a finished run that intersects the
	// range was queued at most this long before it (the job timeout is at
	// most 7 days); active runs are always considered.
	runLookback = 8 * 24 * time.Hour
)

// The supported range of calendar times (the scheduler's anchor years).
var (
	minTime = time.Date(scheduler.MinAnchorYear, 1, 1, 0, 0, 0, 0, time.UTC)
	maxTime = time.Date(scheduler.MaxAnchorYear+1, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Kind is the kind of a calendar event.
type Kind string

// Event kinds.
const (
	KindPlanned Kind = "planned"
	KindRun     Kind = "run"
	KindSkipped Kind = "skipped"
	KindExpiry  Kind = "expiry"
)

// Event is one calendar entry. Times are UTC.
type Event struct {
	JobID   uuid.UUID
	JobName string
	Kind    Kind
	Start   time.Time
	// End is the end of a finished run (nil otherwise).
	End *time.Time
	// Status and Trigger are the run's (runs only; lower case as stored).
	Status  string
	Trigger string
	// RunID: the run (runs, expiries, started-early occurrences).
	RunID *uuid.UUID
	// DestinationID: the destination of an expiry.
	DestinationID *uuid.UUID
	// Projected: an expiry that depends on future runs.
	Projected bool
	// Paused: a planned occurrence of a disabled job.
	Paused bool
	// StartedEarly: a planned occurrence that was pulled forward.
	StartedEarly bool
	// Overdue: the pending catch-up occurrence of an enabled job whose
	// next_run_at has passed; shown at now, the scheduler runs it at its
	// next tick (D-143, D-160).
	Overdue bool
	// Detail is a fixed, bekup-chosen text (empty: none).
	Detail string
}

// Calendar is the result of Build.
type Calendar struct {
	Events    []Event
	Truncated bool
	From, To  time.Time
	Timezone  string
}

// Request is a calendar query.
type Request struct {
	From, To time.Time
	// Timezone is an IANA zone name; it is validated and echoed for the
	// client's day bucketing (times are returned in UTC).
	Timezone string
	// JobIDs narrows the jobs (nil: every visible job; empty: none).
	JobIDs []uuid.UUID
}

// Scope limits the jobs: All, or the jobs Owner owns (authz.JobScope).
type Scope struct {
	All   bool
	Owner uuid.UUID
}

func (s Scope) valid() bool { return s.All || s.Owner != uuid.Nil }

// ValidationError reports an invalid request field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return "calendar: " + e.Field + ": " + e.Message }

// ErrBadScope reports an empty scope (a bug in the caller).
var ErrBadScope = errors.New("calendar: invalid scope")

// validate checks req and returns it normalized (UTC times, deduplicated
// job ids).
func validate(req Request) (Request, error) {
	from, to := req.From.UTC(), req.To.UTC()
	switch {
	case from.Before(minTime) || !to.Before(maxTime):
		return Request{}, &ValidationError{"from", fmt.Sprintf("the range must lie between %d and %d", scheduler.MinAnchorYear, scheduler.MaxAnchorYear)}
	case !from.Before(to):
		return Request{}, &ValidationError{"to", "to must be after from"}
	case to.Sub(from) > MaxRange:
		return Request{}, &ValidationError{"to", "the range must not exceed 93 days"}
	}
	if _, err := scheduler.LoadZone(req.Timezone); err != nil {
		return Request{}, &ValidationError{"timezone", "timezone must be an IANA zone name such as Europe/Amsterdam or UTC"}
	}
	if len(req.JobIDs) > MaxJobIDs {
		return Request{}, &ValidationError{"jobIds", fmt.Sprintf("at most %d job ids", MaxJobIDs)}
	}
	var ids []uuid.UUID
	if req.JobIDs != nil {
		ids = make([]uuid.UUID, 0, len(req.JobIDs))
		seen := make(map[uuid.UUID]bool, len(req.JobIDs))
		for _, id := range req.JobIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return Request{From: from, To: to, Timezone: req.Timezone, JobIDs: ids}, nil
}

// --- pure computation ---------------------------------------------------------

// job is a visible job with its compiled schedule (nil: manual or not
// computable).
type job struct {
	ID        uuid.UUID
	Name      string
	Enabled   bool
	Plan      *scheduler.Plan
	NextRunAt *time.Time
	Policy    retention.Policy
}

// run is a run row.
type run struct {
	ID           uuid.UUID
	JobID        uuid.UUID
	Status       string
	Trigger      string
	ScheduledFor *time.Time
	QueuedAt     time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	ErrorCode    *string
}

// skip is a schedule_skips row.
type skip struct {
	JobID        uuid.UUID
	ScheduledFor *time.Time
	Reason       string
	CreatedAt    time.Time
}

// input is everything compute needs.
type input struct {
	Now, From, To time.Time
	Timezone      string
	Jobs          []job
	Runs          []run
	Skips         []skip
	// Copies are the present copies per job.
	Copies    map[uuid.UUID][]retention.Copy
	Truncated bool
}

// Run triggers and statuses used by the rules.
const (
	triggerEarly = "early"
	statusQueued = "queued"
	statusActive = "running"
)

// collector bounds the events so that a cut calendar is a time prefix: the
// MaxEvents earliest events (compareEvents order) are kept, whatever order
// they were added in (D-160). It holds at most 2*MaxEvents events: when
// full it sorts, keeps the first MaxEvents and from then on refuses events
// after the last kept one.
type collector struct {
	events    []Event
	truncated bool
	// cutoff (valid when full) is the last kept event of a compaction.
	cutoff Event
	full   bool
}

// add adds e. It returns false when e lies after the kept prefix; callers
// that add events in time order can stop then.
func (c *collector) add(e Event) bool {
	if c.full && compareEvents(e, c.cutoff) > 0 {
		c.truncated = true
		return false
	}
	c.events = append(c.events, e)
	if len(c.events) >= 2*MaxEvents {
		c.compact()
	}
	return true
}

// compact sorts the events and keeps the first MaxEvents.
func (c *collector) compact() {
	slices.SortStableFunc(c.events, compareEvents)
	if len(c.events) > MaxEvents {
		clear(c.events[MaxEvents:])
		c.events = c.events[:MaxEvents]
		c.cutoff, c.full, c.truncated = c.events[MaxEvents-1], true, true
	}
}

func inRange(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }

func ptr[T any](v T) *T { return &v }

// compute builds the calendar from loaded rows.
func compute(in input) Calendar {
	c := &collector{truncated: in.Truncated}
	names := make(map[uuid.UUID]string, len(in.Jobs))
	for _, j := range in.Jobs {
		names[j.ID] = j.Name
	}

	// Started-early slots: the original occurrence of an early run.
	early := map[uuid.UUID]map[int64]bool{}
	for _, r := range in.Runs {
		if r.Trigger != triggerEarly || r.ScheduledFor == nil || !inRange(*r.ScheduledFor, in.From, in.To) {
			continue
		}
		if early[r.JobID] == nil {
			early[r.JobID] = map[int64]bool{}
		}
		early[r.JobID][r.ScheduledFor.UnixNano()] = true
		c.add(Event{JobID: r.JobID, JobName: names[r.JobID], Kind: KindPlanned, Start: r.ScheduledFor.UTC(),
			RunID: ptr(r.ID), StartedEarly: true, Detail: "Started early"})
	}
	isEarly := func(id uuid.UUID, t time.Time) bool { return early[id][t.UnixNano()] }

	for _, r := range in.Runs {
		runEvent(c, in, names[r.JobID], r)
	}
	for _, s := range in.Skips {
		at := s.CreatedAt
		if s.ScheduledFor != nil {
			at = *s.ScheduledFor
		}
		if !inRange(at, in.From, in.To) || isEarly(s.JobID, at) {
			continue
		}
		c.add(Event{JobID: s.JobID, JobName: names[s.JobID], Kind: KindSkipped, Start: at.UTC(), Detail: skipDetail(s.Reason)})
	}
	budget := MaxProjection
	for _, j := range in.Jobs {
		expiryEvents(c, in, j, &budget)
	}
	for _, j := range in.Jobs {
		plannedEvents(c, in, j, isEarly)
	}

	c.compact()
	return Calendar{Events: c.events, Truncated: c.truncated, From: in.From, To: in.To, Timezone: in.Timezone}
}

var kindOrder = map[Kind]int{KindRun: 0, KindPlanned: 1, KindSkipped: 2, KindExpiry: 3}

func compareEvents(a, b Event) int {
	if x := a.Start.Compare(b.Start); x != 0 {
		return x
	}
	if x := cmp.Compare(a.JobName, b.JobName); x != 0 {
		return x
	}
	if x := cmp.Compare(a.JobID.String(), b.JobID.String()); x != 0 {
		return x
	}
	if x := cmp.Compare(kindOrder[a.Kind], kindOrder[b.Kind]); x != 0 {
		return x
	}
	if x := compareID(a.RunID, b.RunID); x != 0 {
		return x
	}
	return compareID(a.DestinationID, b.DestinationID)
}

// compareID orders optional ids (nil first) for a deterministic order.
func compareID(a, b *uuid.UUID) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return cmp.Compare(a.String(), b.String())
}

// runEvent adds r when it intersects the range: it starts when it started
// (else at its scheduled time, else when it was queued; an early run that
// has not started at when it was queued) and ends when it finished (an
// active run lasts until now).
func runEvent(c *collector, in input, name string, r run) {
	start := r.QueuedAt
	switch {
	case r.StartedAt != nil:
		start = *r.StartedAt
	case r.ScheduledFor != nil && r.Trigger != triggerEarly:
		start = *r.ScheduledFor
	}
	end := start
	switch {
	case r.FinishedAt != nil:
		end = *r.FinishedAt
	case r.Status == statusQueued || r.Status == statusActive:
		if in.Now.After(end) {
			end = in.Now
		}
	}
	if !start.Before(in.To) || end.Before(in.From) {
		return
	}
	e := Event{JobID: r.JobID, JobName: name, Kind: KindRun, Start: start.UTC(), Status: r.Status, Trigger: r.Trigger, RunID: ptr(r.ID)}
	if r.FinishedAt != nil {
		e.End = ptr(r.FinishedAt.UTC())
	}
	if r.ErrorCode != nil && codeRE.MatchString(*r.ErrorCode) {
		e.Detail = *r.ErrorCode
	}
	c.add(e)
}

// codeRE matches the stored code formats (error codes, skip reasons).
var codeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func skipDetail(reason string) string {
	switch reason {
	case "still_running":
		return "Skipped: the previous run was still running"
	case "unschedulable":
		return "Skipped: the schedule has no valid next occurrence"
	}
	if codeRE.MatchString(reason) {
		return "Skipped (" + reason + ")"
	}
	return "Skipped"
}

// plannedEvents adds the occurrences of j in [max(from, now), to): from
// its next_run_at when enabled, from the schedule (paused) when disabled.
// Pulled-forward occurrences are already shown as started early. An
// enabled job whose next_run_at has passed (the scheduler has not handled
// it yet) shows that pending catch-up run at now, flagged Overdue, and
// continues from the first occurrence after now, as scheduler.Plan.Due
// does (D-143).
func plannedEvents(c *collector, in input, j job, isEarly func(uuid.UUID, time.Time) bool) {
	if j.Plan == nil || j.Plan.Schedule().Kind == scheduler.KindManual {
		return
	}
	start := in.From
	if in.Now.After(start) {
		start = in.Now
	}
	if !start.Before(in.To) {
		return
	}
	var (
		t   time.Time
		err error
	)
	switch {
	case !j.Enabled:
		t, err = j.Plan.Next(start.Add(-time.Nanosecond))
	case j.NextRunAt == nil:
		return // unschedulable: nothing is planned
	case !j.NextRunAt.After(in.Now):
		if inRange(in.Now, in.From, in.To) {
			if !c.add(Event{JobID: j.ID, JobName: j.Name, Kind: KindPlanned, Start: in.Now, Overdue: true,
				Detail: "Overdue: runs at the next scheduler tick"}) {
				return
			}
		}
		if start.Equal(in.Now) {
			t, err = j.Plan.Next(in.Now) // strictly after the catch-up run
		} else {
			t, err = j.Plan.Next(start.Add(-time.Nanosecond))
		}
	case j.NextRunAt.Before(start):
		t, err = j.Plan.Next(start.Add(-time.Nanosecond))
	default:
		t = *j.NextRunAt
	}
	for n := 0; err == nil && t.Before(in.To); n++ {
		if n == MaxPlannedPerJob {
			c.truncated = true
			return
		}
		if !isEarly(j.ID, t) {
			e := Event{JobID: j.ID, JobName: j.Name, Kind: KindPlanned, Start: t.UTC(), Paused: !j.Enabled}
			if e.Paused {
				e.Detail = "Paused: the job is disabled"
			}
			if !c.add(e) {
				return
			}
		}
		t, err = j.Plan.Next(t)
	}
}

// projector yields the assumed future complete runs of a job: its planned
// occurrences from next_run_at, up to the end of the range. An overdue
// next_run_at (<= now) is one catch-up run at now followed by the
// occurrences after now, as scheduler.Plan.Due collapses missed ones
// (D-143); the end of the range (a later run only gives a later expiry, outside the
// range) and within the shared computation budget.
type projector struct {
	plan   *scheduler.Plan
	next   time.Time
	now    time.Time
	to     time.Time
	raw    []time.Time
	done   bool
	budget *int
	trunc  *bool
}

func (p *projector) nth(n int) (time.Time, bool) {
	for len(p.raw) < n && !p.done {
		t := p.next
		if len(p.raw) == 0 && !t.After(p.now) {
			t = p.now // the catch-up run; Plan.Next(now) follows
		}
		if len(p.raw) > 0 {
			var err error
			if t, err = p.plan.Next(p.raw[len(p.raw)-1]); err != nil {
				p.done = true
				break
			}
		}
		if !t.Before(p.to) {
			p.done = true
			break
		}
		if *p.budget <= 0 {
			p.done, *p.trunc = true, true
			break
		}
		*p.budget--
		p.raw = append(p.raw, t)
	}
	if n < 1 || n > len(p.raw) {
		return time.Time{}, false
	}
	if t := p.raw[n-1]; t.After(p.now) {
		return t, true
	}
	return p.now, true
}

// expiryEvents adds the expiries of j's present copies in the range. Only
// an enabled job with a schedule and a next occurrence runs again, so only
// such a job has projected expiries.
func expiryEvents(c *collector, in input, j job, budget *int) {
	copies := in.Copies[j.ID]
	if len(copies) == 0 {
		return
	}
	nth := func(int) (time.Time, bool) { return time.Time{}, false }
	if j.Enabled && j.Plan != nil && j.Plan.Schedule().Kind != scheduler.KindManual && j.NextRunAt != nil {
		var trunc bool
		p := &projector{plan: j.Plan, next: *j.NextRunAt, now: in.Now, to: in.To, budget: budget, trunc: &trunc}
		nth = p.nth
		defer func() { c.truncated = c.truncated || trunc }()
	}
	for _, x := range retention.Expiries(j.Policy, in.Now, copies, nth) {
		if !inRange(x.At, in.From, in.To) {
			continue
		}
		// Expiries are not in time order: keep adding after a refusal.
		c.add(Event{JobID: j.ID, JobName: j.Name, Kind: KindExpiry, Start: x.At.UTC(), RunID: ptr(x.RunID),
			DestinationID: ptr(x.DestinationID), Projected: x.Projected, Detail: expiryDetail(x)})
	}
}

func expiryDetail(x retention.Expiry) string {
	var s string
	switch x.Cause {
	case retention.ExpiryEligible:
		s = "Eligible for removal at the next retention sweep"
	case retention.ExpiryKeepDays:
		s = "Leaves the keep-days window"
	case retention.ExpiryKeepLast:
		s = "Leaves the newest complete runs kept by keep-last"
	case retention.ExpiryNewerComplete:
		s = "Removable once a newer complete run exists"
	case retention.ExpiryLocked:
		s = "Object Lock retention ends"
	default:
		s = "Retention removal"
	}
	if x.Projected {
		s = fmt.Sprintf("Projected (assumes the next %d planned run(s) succeed): %s", x.NewerNeeded, s)
	}
	return s
}
