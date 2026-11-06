package summary

import (
	"errors"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func settings(f Frequency, hour int, zone string) Settings {
	return Settings{Frequency: f, Hour: hour, Weekday: 1, MonthDay: 1, Timezone: zone}
}

// TestNextDue pins the due times of every frequency, including the DST
// transitions of Europe/Amsterdam and America/New_York (the scheduler's
// rules: a nonexistent local time moves forward by the gap, an ambiguous
// one takes the earlier instant).
func TestNextDue(t *testing.T) {
	t.Parallel()
	ams, ny := "Europe/Amsterdam", "America/New_York"
	weeklyTue := settings(Weekly, 8, ams)
	weeklyTue.Weekday = 2
	monthly15 := settings(Monthly, 6, "UTC")
	monthly15.MonthDay = 15
	cases := []struct {
		name  string
		s     Settings
		last  string
		after string
		want  string
	}{
		{"daily", settings(Daily, 8, ams), "", "2026-11-05T10:00:00Z", "2026-11-06T07:00:00Z"},
		{"daily before the hour", settings(Daily, 8, ams), "", "2026-11-05T06:00:00Z", "2026-11-05T07:00:00Z"},
		{"daily exactly at due is after", settings(Daily, 8, ams), "", "2026-11-05T07:00:00Z", "2026-11-06T07:00:00Z"},
		{"weekly default Monday", settings(Weekly, 8, ams), "", "2026-11-05T10:00:00Z", "2026-11-09T07:00:00Z"},
		{"weekly Tuesday", weeklyTue, "", "2026-11-05T10:00:00Z", "2026-11-10T07:00:00Z"},
		{"monthly the 1st", settings(Monthly, 8, ams), "", "2026-11-05T10:00:00Z", "2026-12-01T07:00:00Z"},
		{"monthly the 15th", monthly15, "", "2026-11-05T10:00:00Z", "2026-11-15T06:00:00Z"},
		{"monthly later this month", monthly15, "", "2026-11-15T06:00:00Z", "2026-12-15T06:00:00Z"},
		// Every other day: from the first due time (the next daily time),
		// then every second day from the last summary.
		{"every other day, first", settings(EveryOtherDay, 8, "UTC"), "", "2026-11-05T10:00:00Z", "2026-11-06T08:00:00Z"},
		{"every other day, after one", settings(EveryOtherDay, 8, "UTC"), "2026-11-06T08:00:00Z", "2026-11-06T08:00:00Z", "2026-11-08T08:00:00Z"},
		{"every other day, counts from the last", settings(EveryOtherDay, 8, "UTC"), "2026-11-06T08:00:00Z", "2026-11-07T12:00:00Z", "2026-11-08T08:00:00Z"},
		{"every other day, new hour keeps the series", settings(EveryOtherDay, 20, "UTC"), "2026-11-06T08:00:00Z", "2026-11-06T09:00:00Z", "2026-11-06T20:00:00Z"},
		// Amsterdam spring forward (2026-03-29 02:00 -> 03:00).
		{"ams spring gap", settings(Daily, 2, ams), "", "2026-03-28T12:00:00Z", "2026-03-29T01:00:00Z"},
		{"ams after spring", settings(Daily, 2, ams), "", "2026-03-29T01:00:00Z", "2026-03-30T00:00:00Z"},
		{"ams weekly across spring", settings(Weekly, 8, ams), "", "2026-03-27T12:00:00Z", "2026-03-30T06:00:00Z"},
		// Amsterdam fall back (2026-10-25 03:00 -> 02:00): 02:30 twice.
		{"ams fall ambiguous", settings(Daily, 2, ams), "", "2026-10-24T12:00:00Z", "2026-10-25T00:00:00Z"},
		{"ams after fall", settings(Daily, 2, ams), "", "2026-10-25T00:00:00Z", "2026-10-26T01:00:00Z"},
		// New York spring forward (2026-03-08 02:00 -> 03:00).
		{"ny spring gap", settings(Daily, 2, ny), "", "2026-03-07T12:00:00Z", "2026-03-08T07:00:00Z"},
		{"ny weekly before spring", settings(Weekly, 8, ny), "", "2026-02-27T12:00:00Z", "2026-03-02T13:00:00Z"},
		{"ny weekly after spring", settings(Weekly, 8, ny), "", "2026-03-02T13:00:00Z", "2026-03-09T12:00:00Z"},
		// New York fall back (2026-11-01 02:00 -> 01:00): 01:30 twice.
		{"ny fall ambiguous", settings(Daily, 1, ny), "", "2026-10-31T12:00:00Z", "2026-11-01T05:00:00Z"},
		{"ny after fall", settings(Daily, 1, ny), "", "2026-11-01T05:00:00Z", "2026-11-02T06:00:00Z"},
		{"ny monthly across fall", settings(Monthly, 8, ny), "", "2026-10-15T12:00:00Z", "2026-11-01T13:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var last time.Time
			if c.last != "" {
				last = at(t, c.last)
			}
			got, err := NextDue(c.s, last, at(t, c.after))
			if err != nil {
				t.Fatal(err)
			}
			if want := at(t, c.want); !got.Equal(want) {
				t.Fatalf("NextDue = %s, want %s", got.UTC().Format(time.RFC3339), c.want)
			}
		})
	}
}

// TestNextDueMonthDays: every day 1-28 exists in every month, February
// of a common year included.
func TestNextDueMonthDays(t *testing.T) {
	t.Parallel()
	for md := 1; md <= MaxMonthDay; md++ {
		s := settings(Monthly, 8, "Europe/Amsterdam")
		s.MonthDay = md
		got, err := NextDue(s, time.Time{}, at(t, "2027-01-31T23:00:00Z"))
		if err != nil {
			t.Fatal(err)
		}
		local := got.In(mustZone(t, "Europe/Amsterdam"))
		if local.Year() != 2027 || local.Month() != time.February || local.Day() != md || local.Hour() != 8 {
			t.Fatalf("day %d: %s", md, local)
		}
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNextDueNever(t *testing.T) {
	t.Parallel()
	got, err := NextDue(settings(Never, 8, "UTC"), time.Time{}, time.Now())
	if err != nil || !got.IsZero() {
		t.Fatalf("never = %v, %v", got, err)
	}
}

// TestDecide covers the period of a due summary: at least one calendar
// period (a settings change may overlap the previous summary), from the
// last period's end when that is earlier, and an outage collapses into
// one summary covering the whole gap.
func TestDecide(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *time.Time {
		v := at(t, s)
		return &v
	}
	cases := []struct {
		name             string
		s                Settings
		due, now         string
		last             *time.Time
		start, end, next string
		catchUp          bool
	}{
		{name: "weekly on time", s: settings(Weekly, 8, "UTC"), due: "2026-11-09T08:00:00Z", now: "2026-11-09T08:03:00Z",
			last: ptr("2026-11-02T08:00:00Z"), start: "2026-11-02T08:00:00Z", end: "2026-11-09T08:00:00Z", next: "2026-11-16T08:00:00Z"},
		{name: "weekly first summary", s: settings(Weekly, 8, "UTC"), due: "2026-11-09T08:00:00Z", now: "2026-11-09T08:03:00Z",
			start: "2026-11-02T08:00:00Z", end: "2026-11-09T08:00:00Z", next: "2026-11-16T08:00:00Z"},
		{name: "weekly after three weeks down", s: settings(Weekly, 8, "UTC"), due: "2026-11-02T08:00:00Z", now: "2026-11-20T12:00:00Z",
			last: ptr("2026-10-26T08:00:00Z"), start: "2026-10-26T08:00:00Z", end: "2026-11-16T08:00:00Z", next: "2026-11-23T08:00:00Z", catchUp: true},
		{name: "daily after downtime", s: settings(Daily, 8, "Europe/Amsterdam"), due: "2026-11-05T07:00:00Z", now: "2026-11-08T06:59:00Z",
			last: ptr("2026-11-04T07:00:00Z"), start: "2026-11-04T07:00:00Z", end: "2026-11-07T07:00:00Z", next: "2026-11-08T07:00:00Z", catchUp: true},
		{name: "every other day first", s: settings(EveryOtherDay, 8, "UTC"), due: "2026-11-06T08:00:00Z", now: "2026-11-06T08:01:00Z",
			start: "2026-11-04T08:00:00Z", end: "2026-11-06T08:00:00Z", next: "2026-11-08T08:00:00Z"},
		{name: "every other day later", s: settings(EveryOtherDay, 8, "UTC"), due: "2026-11-08T08:00:00Z", now: "2026-11-08T08:01:00Z",
			last: ptr("2026-11-06T08:00:00Z"), start: "2026-11-06T08:00:00Z", end: "2026-11-08T08:00:00Z", next: "2026-11-10T08:00:00Z"},
		{name: "every other day after downtime", s: settings(EveryOtherDay, 8, "UTC"), due: "2026-11-08T08:00:00Z", now: "2026-11-13T09:00:00Z",
			last: ptr("2026-11-06T08:00:00Z"), start: "2026-11-06T08:00:00Z", end: "2026-11-12T08:00:00Z", next: "2026-11-14T08:00:00Z", catchUp: true},
		{name: "monthly first summary", s: settings(Monthly, 8, "UTC"), due: "2026-12-01T08:00:00Z", now: "2026-12-01T08:02:00Z",
			start: "2026-11-01T08:00:00Z", end: "2026-12-01T08:00:00Z", next: "2027-01-01T08:00:00Z"},
		{name: "ams daily across spring forward", s: settings(Daily, 2, "Europe/Amsterdam"), due: "2026-03-29T01:00:00Z", now: "2026-03-29T01:02:00Z",
			last: ptr("2026-03-28T01:00:00Z"), start: "2026-03-28T01:00:00Z", end: "2026-03-29T01:00:00Z", next: "2026-03-30T00:00:00Z"},
		{name: "ny weekly first summary across spring", s: settings(Weekly, 8, "America/New_York"), due: "2026-03-09T12:00:00Z", now: "2026-03-09T12:01:00Z",
			start: "2026-03-02T13:00:00Z", end: "2026-03-09T12:00:00Z", next: "2026-03-16T12:00:00Z"},
		// D-226 amendment 1: at least one full calendar period.
		{name: "weekly 08:00 to daily 12:00 gives a day", s: settings(Daily, 12, "UTC"), due: "2026-11-09T12:00:00Z", now: "2026-11-09T12:03:00Z",
			last: ptr("2026-11-09T08:00:00Z"), start: "2026-11-08T12:00:00Z", end: "2026-11-09T12:00:00Z", next: "2026-11-10T12:00:00Z"},
		{name: "hour change gives a day", s: settings(Daily, 10, "Europe/Amsterdam"), due: "2026-11-09T09:00:00Z", now: "2026-11-09T09:01:00Z",
			last: ptr("2026-11-09T07:00:00Z"), start: "2026-11-08T09:00:00Z", end: "2026-11-09T09:00:00Z", next: "2026-11-10T09:00:00Z"},
		{name: "daily after three days down", s: settings(Daily, 8, "UTC"), due: "2026-11-05T08:00:00Z", now: "2026-11-07T09:00:00Z",
			last: ptr("2026-11-04T08:00:00Z"), start: "2026-11-04T08:00:00Z", end: "2026-11-07T08:00:00Z", next: "2026-11-08T08:00:00Z", catchUp: true},
		{name: "ams daily on the spring-forward day is 23 h", s: settings(Daily, 8, "Europe/Amsterdam"), due: "2026-03-29T06:00:00Z", now: "2026-03-29T06:01:00Z",
			last: ptr("2026-03-28T07:00:00Z"), start: "2026-03-28T07:00:00Z", end: "2026-03-29T06:00:00Z", next: "2026-03-30T06:00:00Z"},
		{name: "ams daily first summary on the fall-back day is 25 h", s: settings(Daily, 8, "Europe/Amsterdam"), due: "2026-10-25T07:00:00Z", now: "2026-10-25T07:01:00Z",
			start: "2026-10-24T06:00:00Z", end: "2026-10-25T07:00:00Z", next: "2026-10-26T07:00:00Z"},
		{name: "monthly is a calendar month (February)", s: settings(Monthly, 8, "UTC"), due: "2027-03-01T08:00:00Z", now: "2027-03-01T08:02:00Z",
			last: ptr("2027-02-20T08:00:00Z"), start: "2027-02-01T08:00:00Z", end: "2027-03-01T08:00:00Z", next: "2027-04-01T08:00:00Z"},
		{name: "period capped at a year", s: settings(Weekly, 8, "UTC"), due: "2026-11-09T08:00:00Z", now: "2026-11-09T08:03:00Z",
			last: ptr("2024-01-01T08:00:00Z"), start: "2025-11-08T08:00:00Z", end: "2026-11-09T08:00:00Z", next: "2026-11-16T08:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := decide(c.s, at(t, c.due), c.last, at(t, c.now))
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range []struct {
				name string
				got  time.Time
				want string
			}{{"start", d.PeriodStart, c.start}, {"end", d.PeriodEnd, c.end}, {"next", d.NextDue, c.next}} {
				if !x.got.Equal(at(t, x.want)) {
					t.Errorf("%s = %s, want %s", x.name, x.got.UTC().Format(time.RFC3339), x.want)
				}
			}
			if d.CatchUp != c.catchUp {
				t.Errorf("catch-up = %v", d.CatchUp)
			}
			if !d.NextDue.After(at(t, c.now)) {
				t.Errorf("next due %s is not after now", d.NextDue)
			}
		})
	}
	if _, err := decide(settings(Never, 8, "UTC"), time.Now(), nil, time.Now()); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("never = %v", err)
	}
}

// TestDecideLongOutage: an outage longer than the walk bound still yields
// one summary and a due time after now.
func TestDecideLongOutage(t *testing.T) {
	t.Parallel()
	now := at(t, "2045-01-01T12:00:00Z")
	d, err := decide(settings(Daily, 8, "UTC"), at(t, "2026-01-01T08:00:00Z"), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !d.NextDue.After(now) || !d.CatchUp || d.PeriodEnd.Sub(d.PeriodStart) > maxPeriod {
		t.Fatalf("decision = %+v", d)
	}
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()
	ok := settings(Weekly, 8, "Europe/Amsterdam")
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Settings{
		{Frequency: "hourly", Hour: 8, Weekday: 1, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Weekly, Hour: 24, Weekday: 1, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Weekly, Hour: -1, Weekday: 1, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Weekly, Hour: 8, Weekday: 0, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Weekly, Hour: 8, Weekday: 8, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Monthly, Hour: 8, Weekday: 1, MonthDay: 29, Timezone: "UTC"},
		{Frequency: Monthly, Hour: 8, Weekday: 1, MonthDay: 0, Timezone: "UTC"},
		{Frequency: Weekly, Hour: 8, Weekday: 1, MonthDay: 1, Timezone: "Local"},
		{Frequency: Weekly, Hour: 8, Weekday: 1, MonthDay: 1, Timezone: "Mars/Olympus"},
		{Frequency: Weekly, Hour: 8, Weekday: 1, MonthDay: 1, Timezone: ""},
		{Frequency: Weekly, Hour: 8, Weekday: 1, MonthDay: 1, Timezone: "../etc/passwd"},
	}
	for _, s := range bad {
		if err := s.Validate(); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("%+v: %v", s, err)
		}
		if err := checkSettings(s); err == nil {
			t.Errorf("checkSettings(%+v) passed", s)
		}
	}
}

func TestBasisAndEffective(t *testing.T) {
	t.Parallel()
	d := DefaultDefaults()
	if d.Frequency != Weekly || d.Hour != 8 || d.Timezone != "UTC" {
		t.Fatalf("defaults = %+v", d)
	}
	eff, custom := effective(nil, d)
	if custom || eff != d.Settings() || eff.Weekday != 1 || eff.MonthDay != 1 {
		t.Fatalf("no row = %+v %v", eff, custom)
	}
	// A state-only row (written by the tick) follows the defaults.
	eff, custom = effective(&sqlc.UserSummarySetting{}, d)
	if custom || eff != d.Settings() {
		t.Fatalf("state row = %+v %v", eff, custom)
	}
	f := sqlc.SummaryFrequencyDaily
	h, wd, md, tz := int16(7), int16(3), int16(9), "Europe/Amsterdam"
	eff, custom = effective(&sqlc.UserSummarySetting{Frequency: &f, Hour: &h, Weekday: &wd, MonthDay: &md, Timezone: &tz}, d)
	if !custom || eff != (Settings{Frequency: Daily, Hour: 7, Weekday: 3, MonthDay: 9, Timezone: tz}) {
		t.Fatalf("own row = %+v %v", eff, custom)
	}
	seen := map[string]bool{}
	for _, s := range []Settings{
		settings(Weekly, 8, "UTC"), settings(Weekly, 9, "UTC"), settings(Daily, 8, "UTC"),
		settings(Weekly, 8, "Europe/Amsterdam"), {Frequency: Weekly, Hour: 8, Weekday: 2, MonthDay: 1, Timezone: "UTC"},
		{Frequency: Weekly, Hour: 8, Weekday: 1, MonthDay: 2, Timezone: "UTC"},
	} {
		b := s.basis()
		if seen[b] || len(b) > 200 {
			t.Fatalf("basis %q not unique or too long", b)
		}
		seen[b] = true
	}
}

// TestPeriodBefore: one calendar period in the user's zone.
func TestPeriodBefore(t *testing.T) {
	t.Parallel()
	ams := mustZone(t, "Europe/Amsterdam")
	for _, c := range []struct {
		f         Frequency
		zone      *time.Location
		end, want string
	}{
		{Daily, time.UTC, "2026-11-09T12:00:00Z", "2026-11-08T12:00:00Z"},
		{EveryOtherDay, time.UTC, "2026-11-09T12:00:00Z", "2026-11-07T12:00:00Z"},
		{Weekly, time.UTC, "2026-11-09T12:00:00Z", "2026-11-02T12:00:00Z"},
		{Monthly, time.UTC, "2026-03-01T08:00:00Z", "2026-02-01T08:00:00Z"},
		// A day the previous month lacks clamps to its last day.
		{Monthly, time.UTC, "2026-03-31T08:00:00Z", "2026-02-28T08:00:00Z"},
		{Monthly, time.UTC, "2026-03-29T08:00:00Z", "2026-02-28T08:00:00Z"},
		{Monthly, time.UTC, "2028-03-31T08:00:00Z", "2028-02-29T08:00:00Z"},
		{Monthly, time.UTC, "2028-03-30T08:00:00Z", "2028-02-29T08:00:00Z"},
		{Monthly, time.UTC, "2028-03-29T08:00:00Z", "2028-02-29T08:00:00Z"},
		{Monthly, time.UTC, "2026-05-31T08:00:00Z", "2026-04-30T08:00:00Z"},
		{Monthly, time.UTC, "2026-01-31T08:00:00Z", "2025-12-31T08:00:00Z"},
		// Same local time across the spring-forward change (CEST → CET).
		{Monthly, ams, "2026-03-31T10:00:00Z", "2026-02-28T11:00:00Z"},
		{Daily, ams, "2026-03-29T06:00:00Z", "2026-03-28T07:00:00Z"},
		{Daily, ams, "2026-10-25T07:00:00Z", "2026-10-24T06:00:00Z"},
		{Weekly, ams, "2026-03-30T06:00:00Z", "2026-03-23T07:00:00Z"},
		{Weekly, mustZone(t, "America/New_York"), "2026-03-09T12:00:00Z", "2026-03-02T13:00:00Z"},
	} {
		if got := periodBefore(at(t, c.end), c.f, c.zone); !got.Equal(at(t, c.want)) {
			t.Errorf("%s %s before %s = %s, want %s", c.f, c.zone, c.end, got.UTC().Format(time.RFC3339), c.want)
		}
	}
}

// TestPreviewStart: the preview covers one full period up to now, never
// "since the last due time".
func TestPreviewStart(t *testing.T) {
	t.Parallel()
	now := at(t, "2026-11-09T11:00:00Z")
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{settings(Daily, 8, "Europe/Amsterdam"), "2026-11-08T11:00:00Z"},
		{settings(EveryOtherDay, 8, "UTC"), "2026-11-07T11:00:00Z"},
		{settings(Weekly, 8, "UTC"), "2026-11-02T11:00:00Z"},
		{settings(Monthly, 8, "UTC"), "2026-10-09T11:00:00Z"},
		{settings(Never, 8, "UTC"), "2026-11-02T11:00:00Z"},
		{settings(Daily, 8, "Mars/Base"), "2026-11-08T11:00:00Z"},
	} {
		if got := previewStart(c.s, now); !got.Equal(at(t, c.want)) {
			t.Errorf("%+v: %s, want %s", c.s, got.UTC().Format(time.RFC3339), c.want)
		}
	}
	// A month back from the 29th-31st clamps to the previous month's last
	// day (a leap year included), so the preview covers a whole month.
	for _, c := range []struct{ now, want string }{
		{"2026-03-31T11:00:00Z", "2026-02-28T11:00:00Z"},
		{"2028-03-31T11:00:00Z", "2028-02-29T11:00:00Z"},
		{"2026-12-31T11:00:00Z", "2026-11-30T11:00:00Z"},
	} {
		got := previewStart(settings(Monthly, 8, "UTC"), at(t, c.now))
		if !got.Equal(at(t, c.want)) {
			t.Errorf("monthly preview at %s: %s, want %s", c.now, got.UTC().Format(time.RFC3339), c.want)
		}
		p := summaryPeriod{from: got, until: at(t, c.now), loc: time.UTC, frequency: Monthly, preview: true}
		if w := p.window(); w != "the last month" {
			t.Errorf("monthly preview at %s: %q", c.now, w)
		}
	}
	// Across the fall-back night in Amsterdam a day is 25 hours.
	got := previewStart(settings(Daily, 8, "Europe/Amsterdam"), at(t, "2026-10-25T12:00:00Z"))
	if want := at(t, "2026-10-24T11:00:00Z"); !got.Equal(want) {
		t.Errorf("fall back: %s", got.UTC())
	}
}
