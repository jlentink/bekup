package scheduler

import (
	"errors"
	"testing"
	"time"
)

func mustLoc(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func iv(every int, unit Unit, anchor, tz string) Schedule {
	return Schedule{Kind: KindInterval, Every: every, Unit: unit, Anchor: anchor, Timezone: tz}
}

func cronS(expr, tz string) Schedule { return Schedule{Kind: KindCron, Cron: expr, Timezone: tz} }

// at parses an RFC 3339 instant.
func at(t testing.TB, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func TestNextTable(t *testing.T) {
	cases := []struct {
		name  string
		s     Schedule
		after string
		want  []string // successive occurrences
	}{
		// Day intervals follow the wall clock; DST gaps shift forward,
		// overlaps take the first instant.
		{"ams daily spring forward", iv(1, UnitDays, "2026-01-01T02:30", "Europe/Amsterdam"), "2026-03-28T12:00:00+01:00",
			[]string{"2026-03-29T03:30:00+02:00", "2026-03-30T02:30:00+02:00"}},
		{"ams daily fall back", iv(1, UnitDays, "2026-01-01T02:30", "Europe/Amsterdam"), "2026-10-24T12:00:00+02:00",
			[]string{"2026-10-25T02:30:00+02:00", "2026-10-26T02:30:00+01:00"}},
		{"ams daily 03:00 across DST", iv(1, UnitDays, "2026-03-27T03:00", "Europe/Amsterdam"), "2026-03-27T12:00:00+01:00",
			[]string{"2026-03-28T03:00:00+01:00", "2026-03-29T03:00:00+02:00", "2026-03-30T03:00:00+02:00"}},
		{"nyc daily spring forward", iv(1, UnitDays, "2026-01-01T02:30", "America/New_York"), "2026-03-07T12:00:00-05:00",
			[]string{"2026-03-08T03:30:00-04:00", "2026-03-09T02:30:00-04:00"}},
		{"nyc daily fall back", iv(1, UnitDays, "2026-01-01T01:30", "America/New_York"), "2026-10-31T12:00:00-04:00",
			[]string{"2026-11-01T01:30:00-04:00", "2026-11-02T01:30:00-05:00"}},
		{"lord howe 30-min gap", iv(1, UnitDays, "2026-01-01T02:15", "Australia/Lord_Howe"), "2026-10-03T12:00:00+10:30",
			[]string{"2026-10-04T02:45:00+11:00", "2026-10-05T02:15:00+11:00"}},
		{"lord howe 30-min overlap", iv(1, UnitDays, "2026-01-01T01:45", "Australia/Lord_Howe"), "2026-04-04T12:00:00+11:00",
			[]string{"2026-04-05T01:45:00+11:00", "2026-04-06T01:45:00+10:30"}},
		{"tokyo no DST", iv(2, UnitDays, "2026-01-01T03:00", "Asia/Tokyo"), "2026-01-01T03:00:00+09:00",
			[]string{"2026-01-03T03:00:00+09:00", "2026-01-05T03:00:00+09:00"}},
		{"utc every 2 days", iv(2, UnitDays, "2026-01-01T03:00", "UTC"), "2026-03-01T00:00:00Z",
			[]string{"2026-03-02T03:00:00Z", "2026-03-04T03:00:00Z"}},
		{"anchor in the future", iv(1, UnitDays, "2030-05-05T04:00", "UTC"), "2026-01-01T00:00:00Z",
			[]string{"2030-05-05T04:00:00Z", "2030-05-06T04:00:00Z"}},
		{"nonexistent anchor", iv(1, UnitDays, "2026-03-29T02:30", "Europe/Amsterdam"), "2026-01-01T00:00:00Z",
			[]string{"2026-03-29T03:30:00+02:00", "2026-03-30T02:30:00+02:00"}},
		{"midnight transition santiago", iv(1, UnitDays, "2026-01-01T00:00", "America/Santiago"), "2026-09-05T12:00:00-04:00",
			[]string{"2026-09-06T01:00:00-03:00", "2026-09-07T00:00:00-03:00"}},

		// Sub-day intervals are absolute.
		{"every 6h across spring forward", iv(6, UnitHours, "2026-03-29T00:00", "Europe/Amsterdam"), "2026-03-28T23:00:00+01:00",
			[]string{"2026-03-29T00:00:00+01:00", "2026-03-29T07:00:00+02:00", "2026-03-29T13:00:00+02:00"}},
		{"hourly across fall back", iv(1, UnitHours, "2026-10-25T00:00", "Europe/Amsterdam"), "2026-10-25T00:30:00+02:00",
			[]string{"2026-10-25T01:00:00+02:00", "2026-10-25T02:00:00+02:00", "2026-10-25T02:00:00+01:00", "2026-10-25T03:00:00+01:00"}},
		{"every 90 minutes", iv(90, UnitMinutes, "2026-01-01T00:15", "UTC"), "2026-01-01T01:45:00Z",
			[]string{"2026-01-01T03:15:00Z", "2026-01-01T04:45:00Z"}},
		{"strictly after an occurrence", iv(5, UnitMinutes, "2026-01-01T00:00", "UTC"), "2026-01-01T00:10:00Z",
			[]string{"2026-01-01T00:15:00Z"}},

		// Weeks.
		{"weekly on the anchor weekday", iv(1, UnitWeeks, "2026-01-05T03:00", "UTC"), "2026-01-05T03:00:00Z",
			[]string{"2026-01-12T03:00:00Z", "2026-01-19T03:00:00Z"}},
		{"biweekly friday", Schedule{Kind: KindInterval, Every: 2, Unit: UnitWeeks, Anchor: "2026-01-05T03:00", Weekday: "friday", Timezone: "UTC"}, "2026-01-01T00:00:00Z",
			[]string{"2026-01-09T03:00:00Z", "2026-01-23T03:00:00Z", "2026-02-06T03:00:00Z"}},
		{"weekly sunday 02:30 ams spring", Schedule{Kind: KindInterval, Every: 1, Unit: UnitWeeks, Anchor: "2026-03-01T02:30", Weekday: "sunday", Timezone: "Europe/Amsterdam"}, "2026-03-23T00:00:00+01:00",
			[]string{"2026-03-29T03:30:00+02:00", "2026-04-05T02:30:00+02:00"}},

		// Months clamp to the anchor's (or configured) day each month.
		{"monthly day 31 leap year", iv(1, UnitMonths, "2024-01-31T02:00", "UTC"), "2024-01-31T02:00:00Z",
			[]string{"2024-02-29T02:00:00Z", "2024-03-31T02:00:00Z", "2024-04-30T02:00:00Z", "2024-05-31T02:00:00Z"}},
		{"monthly day 31 common year", iv(1, UnitMonths, "2025-01-31T02:00", "UTC"), "2025-01-31T02:00:00Z",
			[]string{"2025-02-28T02:00:00Z", "2025-03-31T02:00:00Z"}},
		{"monthly day 29 february", iv(12, UnitMonths, "2024-02-29T02:00", "UTC"), "2024-02-29T02:00:00Z",
			[]string{"2025-02-28T02:00:00Z", "2026-02-28T02:00:00Z", "2027-02-28T02:00:00Z", "2028-02-29T02:00:00Z"}},
		{"quarterly on day 1", iv(3, UnitMonths, "2026-01-01T02:00", "Europe/Amsterdam"), "2026-01-01T12:00:00+01:00",
			[]string{"2026-04-01T02:00:00+02:00", "2026-07-01T02:00:00+02:00", "2026-10-01T02:00:00+02:00", "2027-01-01T02:00:00+01:00"}},
		{"day_of_month after the anchor day", Schedule{Kind: KindInterval, Every: 1, Unit: UnitMonths, Anchor: "2026-11-10T02:00", DayOfMonth: 31, Timezone: "UTC"}, "2026-01-01T00:00:00Z",
			[]string{"2026-11-30T02:00:00Z", "2026-12-31T02:00:00Z", "2027-01-31T02:00:00Z"}},
		{"day_of_month before the anchor day", Schedule{Kind: KindInterval, Every: 1, Unit: UnitMonths, Anchor: "2026-11-10T02:00", DayOfMonth: 1, Timezone: "UTC"}, "2026-01-01T00:00:00Z",
			[]string{"2026-12-01T02:00:00Z", "2027-01-01T02:00:00Z"}},
		{"monthly on a nonexistent time", iv(1, UnitMonths, "2026-02-29T02:30", "UTC"), "2026-01-01T00:00:00Z", nil},

		// Cron: wall clock with the same DST rules (robfig would skip the
		// spring-forward day and run twice on the fall-back day).
		{"cron spring forward", cronS("30 2 * * *", "Europe/Amsterdam"), "2026-03-28T12:00:00+01:00",
			[]string{"2026-03-29T03:30:00+02:00", "2026-03-30T02:30:00+02:00"}},
		{"cron fall back once", cronS("30 2 * * *", "Europe/Amsterdam"), "2026-10-24T12:00:00+02:00",
			[]string{"2026-10-25T02:30:00+02:00", "2026-10-26T02:30:00+01:00"}},
		{"cron gap shifts not reordered", cronS("*/30 2,3 * * *", "Europe/Amsterdam"), "2026-03-29T00:00:00+01:00",
			[]string{"2026-03-29T03:00:00+02:00", "2026-03-29T03:30:00+02:00", "2026-03-30T02:00:00+02:00"}},
		{"cron weekdays", cronS("0 3 * * 1-5", "UTC"), "2026-10-23T04:00:00Z",
			[]string{"2026-10-26T03:00:00Z", "2026-10-27T03:00:00Z"}},
		{"cron dom or dow", cronS("0 0 13 * 5", "UTC"), "2026-02-01T00:00:00Z",
			[]string{"2026-02-06T00:00:00Z", "2026-02-13T00:00:00Z", "2026-02-20T00:00:00Z"}},
		{"cron feb 29", cronS("0 0 29 2 *", "UTC"), "2025-01-01T00:00:00Z",
			[]string{"2028-02-29T00:00:00Z", "2032-02-29T00:00:00Z"}},
		{"cron @daily lord howe", cronS("@daily", "Australia/Lord_Howe"), "2026-01-01T12:00:00+11:00",
			[]string{"2026-01-02T00:00:00+11:00"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Compile(tc.s)
			if tc.want == nil {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Compile: want ErrInvalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			cur := at(t, tc.after)
			for i, w := range tc.want {
				got, err := p.Next(cur)
				if err != nil {
					t.Fatalf("Next #%d: %v", i, err)
				}
				if !got.Equal(at(t, w)) {
					t.Fatalf("Next #%d after %s = %s, want %s", i, cur.Format(time.RFC3339), got.Format(time.RFC3339), w)
				}
				cur = got
			}
		})
	}
}

func TestResolveRules(t *testing.T) {
	ams := mustLoc(t, "Europe/Amsterdam")
	cases := []struct {
		w    wall
		want string
		ok   bool // exists
	}{
		{wall{2026, 3, 29, 2, 0}, "2026-03-29T03:00:00+02:00", false},
		{wall{2026, 3, 29, 2, 59}, "2026-03-29T03:59:00+02:00", false},
		{wall{2026, 3, 29, 3, 0}, "2026-03-29T03:00:00+02:00", true},
		{wall{2026, 3, 29, 1, 59}, "2026-03-29T01:59:00+01:00", true},
		{wall{2026, 10, 25, 2, 0}, "2026-10-25T02:00:00+02:00", true},
		{wall{2026, 10, 25, 2, 59}, "2026-10-25T02:59:00+02:00", true},
		{wall{2026, 10, 25, 3, 0}, "2026-10-25T03:00:00+01:00", true},
		{wall{2026, 7, 1, 12, 0}, "2026-07-01T12:00:00+02:00", true},
	}
	for _, tc := range cases {
		got := resolve(tc.w, ams)
		if !got.Equal(at(t, tc.want)) {
			t.Errorf("resolve(%v) = %s, want %s", tc.w, got.Format(time.RFC3339), tc.want)
		}
		if exists(tc.w, ams) != tc.ok {
			t.Errorf("exists(%v) = %v, want %v", tc.w, !tc.ok, tc.ok)
		}
	}
	// Samoa skipped 2011-12-30 entirely (24h gap).
	apia := mustLoc(t, "Pacific/Apia")
	if got := resolve(wall{2011, 12, 30, 3, 0}, apia); !got.Equal(at(t, "2011-12-31T03:00:00+14:00")) {
		t.Errorf("Apia skipped day: %s", got.Format(time.RFC3339))
	}
}

func TestNextManualAndRange(t *testing.T) {
	if _, err := Next(Schedule{Kind: KindManual}, time.Now()); !errors.Is(err, ErrManual) {
		t.Fatalf("manual: %v", err)
	}
	s := iv(1, UnitDays, "2026-01-01T03:00", "UTC")
	if _, err := Next(s, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("range: %v", err)
	}
	if _, err := Next(Schedule{Kind: "weekly"}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestNextN(t *testing.T) {
	s := iv(1, UnitDays, "2026-01-01T03:00", "UTC")
	after := at(t, "2026-01-01T00:00:00Z")
	got, err := NextN(s, after, 5)
	if err != nil || len(got) != 5 {
		t.Fatalf("NextN: %v %v", got, err)
	}
	for i, g := range got {
		if want := time.Date(2026, 1, 1+i, 3, 0, 0, 0, time.UTC); !g.Equal(want) {
			t.Errorf("#%d = %s, want %s", i, g, want)
		}
	}
	for _, n := range []int{0, -1, MaxPreview + 1} {
		if _, err := NextN(s, after, n); !errors.Is(err, ErrInvalid) {
			t.Errorf("NextN(n=%d): want ErrInvalid, got %v", n, err)
		}
	}
	if _, err := NextN(s, after, MaxPreview); err != nil {
		t.Errorf("NextN(max): %v", err)
	}
	if _, err := NextN(Schedule{Kind: KindManual}, after, 5); !errors.Is(err, ErrManual) {
		t.Errorf("NextN(manual): %v", err)
	}
}

func TestDue(t *testing.T) {
	daily := iv(1, UnitDays, "2026-01-01T03:00", "UTC")
	cases := []struct {
		name      string
		s         Schedule
		nextRunAt string
		now       string
		want      Decision
	}{
		{"not yet due", daily, "2026-02-02T03:00:00Z", "2026-02-02T02:59:59Z",
			Decision{NextRunAt: at(t, "2026-02-02T03:00:00Z")}},
		{"due on time", daily, "2026-02-02T03:00:00Z", "2026-02-02T03:00:10Z",
			Decision{Run: true, ScheduledAt: at(t, "2026-02-02T03:00:00Z"), NextRunAt: at(t, "2026-02-03T03:00:00Z")}},
		{"exactly at the occurrence", daily, "2026-02-02T03:00:00Z", "2026-02-02T03:00:00Z",
			Decision{Run: true, ScheduledAt: at(t, "2026-02-02T03:00:00Z"), NextRunAt: at(t, "2026-02-03T03:00:00Z")}},
		{"late but single", daily, "2026-02-02T03:00:00Z", "2026-02-02T20:00:00Z",
			Decision{Run: true, ScheduledAt: at(t, "2026-02-02T03:00:00Z"), NextRunAt: at(t, "2026-02-03T03:00:00Z")}},
		{"down for ten days collapses", daily, "2026-02-02T03:00:00Z", "2026-02-12T12:00:00Z",
			Decision{Run: true, ScheduledAt: at(t, "2026-02-02T03:00:00Z"), CatchUp: true, NextRunAt: at(t, "2026-02-13T03:00:00Z")}},
		{"down a year, every 5 minutes", iv(5, UnitMinutes, "2026-01-01T00:00", "UTC"), "2026-01-01T00:05:00Z", "2027-01-01T00:02:00Z",
			Decision{Run: true, ScheduledAt: at(t, "2026-01-01T00:05:00Z"), CatchUp: true, NextRunAt: at(t, "2027-01-01T00:05:00Z")}},
		{"uninitialized", daily, "", "2026-02-02T12:00:00Z",
			Decision{NextRunAt: at(t, "2026-02-03T03:00:00Z")}},
		{"manual", Schedule{Kind: KindManual}, "2026-02-02T03:00:00Z", "2026-02-12T12:00:00Z", Decision{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var nra time.Time
			if tc.nextRunAt != "" {
				nra = at(t, tc.nextRunAt)
			}
			got, err := Due(tc.s, nra, at(t, tc.now))
			if err != nil {
				t.Fatalf("Due: %v", err)
			}
			if got.Run != tc.want.Run || got.CatchUp != tc.want.CatchUp ||
				!got.ScheduledAt.Equal(tc.want.ScheduledAt) || !got.NextRunAt.Equal(tc.want.NextRunAt) {
				t.Fatalf("Due = %+v, want %+v", got, tc.want)
			}
		})
	}
}
