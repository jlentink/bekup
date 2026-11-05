package summary

import (
	"errors"
	"fmt"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/scheduler"
)

// The periodic summary (D-226): every user gets a summary of the backups
// they may see at a frequency and local time of their choice (or the
// configured defaults). Due times follow the wall clock of the user's
// zone with the scheduler's DST rules (scheduler.Plan: a nonexistent
// local time moves forward by the gap, an ambiguous one takes the earlier
// instant); missed due times (downtime) collapse into one summary that
// covers the whole gap.

// Frequency is how often a user gets the summary.
type Frequency string

// Summary frequencies (the summary_frequency DB enum).
const (
	Daily         Frequency = "daily"
	EveryOtherDay Frequency = "every_other_day"
	Weekly        Frequency = "weekly"
	Monthly       Frequency = "monthly"
	Never         Frequency = "never"
)

// Valid reports whether f is a known frequency.
func (f Frequency) Valid() bool {
	switch f {
	case Daily, EveryOtherDay, Weekly, Monthly, Never:
		return true
	}
	return false
}

// nominal is the rough length of one period (the start of a first period
// when no earlier occurrence exists).
func (f Frequency) nominal() time.Duration {
	switch f {
	case Daily:
		return 24 * time.Hour
	case EveryOtherDay:
		return 48 * time.Hour
	case Monthly:
		return 31 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

// Limits of the summary settings.
const (
	// MaxMonthDay: monthly summaries go out on day 1-28, which
	// every month has.
	MaxMonthDay = 28
	// maxPeriod bounds a period (a long outage or a re-enabled
	// account): older history is not summarized.
	maxPeriod = 366 * 24 * time.Hour
	// maxCatchUpSteps bounds the walk to the newest missed due time.
	maxCatchUpSteps = 5000
)

// Settings are the effective summary settings of a user.
type Settings struct {
	Frequency Frequency
	// Hour is the local hour (0-23).
	Hour int
	// Weekday is the ISO weekday of weekly summaries (1 = Monday ... 7).
	Weekday int
	// MonthDay is the day of monthly summaries (1-28).
	MonthDay int
	// Timezone is the IANA zone of the time.
	Timezone string
}

// Defaults are the configured defaults (notifications.summary_default_*)
// of users without their own settings: Monday for weekly and the 1st for
// monthly summaries.
type Defaults struct {
	Frequency Frequency
	Hour      int
	Timezone  string
}

// DefaultDefaults are the built-in defaults (weekly, 08:00 UTC).
func DefaultDefaults() Defaults {
	return Defaults{Frequency: Weekly, Hour: 8, Timezone: "UTC"}
}

// Settings returns the defaults as settings.
func (d Defaults) Settings() Settings {
	return Settings{Frequency: d.Frequency, Hour: d.Hour, Weekday: 1, MonthDay: 1, Timezone: d.Timezone}
}

// ErrInvalidSettings reports invalid settings.
var ErrInvalidSettings = errors.New("notify: invalid summary settings")

// Validate checks s (the zone with the scheduler's zone rule).
func (s Settings) Validate() error {
	switch {
	case !s.Frequency.Valid():
		return fmt.Errorf("%w: frequency", ErrInvalidSettings)
	case s.Hour < 0 || s.Hour > 23:
		return fmt.Errorf("%w: hour", ErrInvalidSettings)
	case s.Weekday < 1 || s.Weekday > 7:
		return fmt.Errorf("%w: weekday", ErrInvalidSettings)
	case s.MonthDay < 1 || s.MonthDay > MaxMonthDay:
		return fmt.Errorf("%w: month day", ErrInvalidSettings)
	}
	if _, err := scheduler.LoadZone(s.Timezone); err != nil {
		return fmt.Errorf("%w: timezone", ErrInvalidSettings)
	}
	return nil
}

// basis is the canonical effective schedule (stored as due_basis): a
// different basis means the stored due time is stale.
func (s Settings) basis() string {
	return fmt.Sprintf("v1|%s|%02d|%d|%d|%s", s.Frequency, s.Hour, s.Weekday, s.MonthDay, s.Timezone)
}

// isoWeekdayNames maps ISO weekdays (1 = Monday) to the scheduler's
// lower-case names.
var isoWeekdayNames = [...]string{"", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// plan compiles the schedule of s. anchor fixes the series of
// every-other-day summaries (its local date); it must not be zero for
// them.
func (s Settings) plan(anchor time.Time) (*scheduler.Plan, error) {
	sch := scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Timezone: s.Timezone}
	at := fmt.Sprintf("T%02d:00", s.Hour)
	switch s.Frequency {
	case Daily:
		sch.Unit, sch.Anchor = scheduler.UnitDays, "2000-01-01"+at
	case EveryOtherDay:
		if anchor.IsZero() {
			return nil, fmt.Errorf("%w: every-other-day summaries need an anchor", ErrInvalidSettings)
		}
		loc, err := scheduler.LoadZone(s.Timezone)
		if err != nil {
			return nil, fmt.Errorf("%w: timezone", ErrInvalidSettings)
		}
		sch.Every, sch.Unit, sch.Anchor = 2, scheduler.UnitDays, anchor.In(loc).Format("2006-01-02")+at
	case Weekly:
		if s.Weekday < 1 || s.Weekday > 7 {
			return nil, fmt.Errorf("%w: weekday", ErrInvalidSettings)
		}
		// 2000-01-03 is a Monday.
		sch.Unit, sch.Anchor, sch.Weekday = scheduler.UnitWeeks, "2000-01-03"+at, isoWeekdayNames[s.Weekday]
	case Monthly:
		sch.Unit, sch.Anchor, sch.DayOfMonth = scheduler.UnitMonths, "2000-01-01"+at, s.MonthDay
	default:
		return nil, fmt.Errorf("%w: frequency %q has no schedule", ErrInvalidSettings, s.Frequency)
	}
	p, err := scheduler.Compile(sch)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}
	return p, nil
}

// NextDue returns the first due time of s strictly after `after`
// (zero for never). lastPeriodEnd (zero: none) anchors every-other-day
// summaries, which count from the last summary or, without one, from the
// first due time (the next daily time).
func NextDue(s Settings, lastPeriodEnd, after time.Time) (time.Time, error) {
	if s.Frequency == Never {
		return time.Time{}, nil
	}
	anchor := lastPeriodEnd
	if s.Frequency == EveryOtherDay && anchor.IsZero() {
		daily := s
		daily.Frequency = Daily
		return NextDue(daily, time.Time{}, after)
	}
	p, err := s.plan(anchor)
	if err != nil {
		return time.Time{}, err
	}
	return p.Next(after)
}

// decision is what a due summary covers.
type decision struct {
	// PeriodStart and PeriodEnd bound the period (start exclusive, end
	// inclusive).
	PeriodStart, PeriodEnd time.Time
	// NextDue is the following due time (strictly after now).
	NextDue time.Time
	// CatchUp: more than one due time passed (downtime); they are covered
	// by this one summary.
	CatchUp bool
}

// decide evaluates a due summary (due <= now, the stored
// next_due_at): the period ends at the newest due time not after now and
// starts at the previous period's end (or one occurrence earlier for a
// first summary), so missed due times give one summary, never a burst.
func decide(s Settings, due time.Time, lastPeriodEnd *time.Time, now time.Time) (decision, error) {
	if s.Frequency == Never {
		return decision{}, fmt.Errorf("%w: never has no due summary", ErrInvalidSettings)
	}
	anchor := due
	if lastPeriodEnd != nil {
		anchor = *lastPeriodEnd
	}
	p, err := s.plan(anchor)
	if err != nil {
		return decision{}, err
	}
	end := due
	for range maxCatchUpSteps {
		n, err := p.Next(end)
		if err != nil {
			return decision{}, err
		}
		if n.After(now) {
			break
		}
		end = n
	}
	next, err := p.Next(end)
	if err != nil {
		return decision{}, err
	}
	if !next.After(now) {
		// The walk was cut short (an outage of more than maxCatchUpSteps
		// periods): continue from now.
		if next, err = p.Next(now); err != nil {
			return decision{}, err
		}
	}
	var start time.Time
	if lastPeriodEnd != nil && lastPeriodEnd.Before(end) {
		start = *lastPeriodEnd
	} else {
		start = previousOccurrence(p, end, s.Frequency.nominal())
	}
	if end.Sub(start) > maxPeriod {
		start = end.Add(-maxPeriod)
	}
	return decision{PeriodStart: start, PeriodEnd: end, NextDue: next, CatchUp: !end.Equal(due)}, nil
}

// previousOccurrence is the newest occurrence of p before end, or end
// minus the nominal period when the series has none (it starts at end).
func previousOccurrence(p *scheduler.Plan, end time.Time, nominal time.Duration) time.Time {
	t := end.Add(-nominal - 72*time.Hour)
	prev := time.Time{}
	for range 64 {
		n, err := p.Next(t)
		if err != nil || !n.Before(end) {
			break
		}
		prev, t = n, n
	}
	if prev.IsZero() {
		return end.Add(-nominal)
	}
	return prev
}

// effective returns the settings of a stored row (nil: none) under
// the defaults, and whether they are the user's own.
func effective(row *sqlc.UserSummarySetting, d Defaults) (Settings, bool) {
	if row == nil || row.Frequency == nil || row.Hour == nil || row.Weekday == nil || row.MonthDay == nil || row.Timezone == nil {
		return d.Settings(), false
	}
	return Settings{
		Frequency: Frequency(*row.Frequency), Hour: int(*row.Hour), Weekday: int(*row.Weekday),
		MonthDay: int(*row.MonthDay), Timezone: *row.Timezone,
	}, true
}
