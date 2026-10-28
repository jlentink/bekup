package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/scheduler"
)

func TestEarlyNextRun(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	dailyAms := scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitDays, Anchor: "2026-01-01T03:00", Timezone: "Europe/Amsterdam"}
	monthly31 := scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitMonths, Anchor: "2026-01-31T02:00", DayOfMonth: 31, Timezone: "UTC"}
	weekdays := scheduler.Schedule{Kind: scheduler.KindCron, Cron: "0 3 * * 1-5", Timezone: "Europe/Amsterdam"}
	hourly := scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitHours, Anchor: "2026-01-01T00:00", Timezone: "UTC"}
	at := func(y int, m time.Month, d, h int, loc *time.Location) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, loc)
	}
	tests := []struct {
		name     string
		sched    scheduler.Schedule
		original time.Time
		now      time.Time
		want     time.Time
	}{
		{
			name: "daily: exactly one occurrence later", sched: dailyAms,
			original: at(2026, 6, 10, 3, ams), now: at(2026, 6, 9, 12, ams), want: at(2026, 6, 11, 3, ams),
		},
		{
			// 23 hours of elapsed time: the clocks spring forward on 29 March.
			name: "daily across the spring DST change", sched: dailyAms,
			original: at(2026, 3, 28, 3, ams), now: at(2026, 3, 27, 20, ams), want: at(2026, 3, 29, 3, ams),
		},
		{
			// 25 hours: the clocks fall back on 25 October.
			name: "daily across the autumn DST change", sched: dailyAms,
			original: at(2026, 10, 24, 3, ams), now: at(2026, 10, 24, 1, ams), want: at(2026, 10, 25, 3, ams),
		},
		{
			name: "monthly day 31 into February", sched: monthly31,
			original: at(2026, 1, 31, 2, time.UTC), now: at(2026, 1, 20, 0, time.UTC), want: at(2026, 2, 28, 2, time.UTC),
		},
		{
			name: "monthly from the clamped February back to day 31", sched: monthly31,
			original: at(2026, 2, 28, 2, time.UTC), now: at(2026, 2, 27, 0, time.UTC), want: at(2026, 3, 31, 2, time.UTC),
		},
		{
			name: "cron Friday to Monday", sched: weekdays,
			original: at(2026, 6, 12, 3, ams), now: at(2026, 6, 11, 12, ams), want: at(2026, 6, 15, 3, ams),
		},
		{
			// The occurrence was already due (the scheduler had not ticked
			// yet) but the following one is still ahead.
			name: "overdue, following occurrence ahead", sched: hourly,
			original: at(2026, 6, 1, 10, time.UTC), now: at(2026, 6, 1, 10, time.UTC).Add(10 * time.Second), want: at(2026, 6, 1, 11, time.UTC),
		},
		{
			// Long overdue: missed occurrences collapse like in the scheduler.
			name: "long overdue collapses to the first after now", sched: hourly,
			original: at(2026, 6, 1, 10, time.UTC), now: at(2026, 6, 1, 15, time.UTC).Add(time.Minute), want: at(2026, 6, 1, 16, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.sched.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			got, err := earlyNextRun(s, tc.original.UTC(), tc.now.UTC())
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("got %s, want %s", got, tc.want.UTC())
			}
		})
	}
}

func TestEarlyNextRunAlreadyEarly(t *testing.T) {
	hourly, err := scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitHours, Anchor: "2026-01-01T00:00", Timezone: "UTC"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 10, 30, 0, 0, time.UTC)
	// next_run_at 12:00 while the natural next occurrence is 11:00: the 11:00
	// occurrence was already started early; refuse instead of postponing.
	for _, original := range []time.Time{now.Add(90 * time.Minute), now.Add(30 * time.Hour)} {
		if _, err := earlyNextRun(hourly, original, now); !errors.Is(err, errAlreadyEarly) {
			t.Fatalf("original %s: %v", original, err)
		}
	}
	// Exactly the natural next occurrence is allowed.
	if got, err := earlyNextRun(hourly, now.Add(30*time.Minute), now); err != nil || !got.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("natural: %s %v", got, err)
	}
}

func TestEarlyNextRunManual(t *testing.T) {
	if _, err := earlyNextRun(scheduler.Schedule{Kind: scheduler.KindManual}, time.Now(), time.Now()); !errors.Is(err, scheduler.ErrManual) {
		t.Fatalf("manual: %v", err)
	}
}
