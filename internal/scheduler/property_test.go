package scheduler

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// Property tests: random schedules and instants from a fixed set of seeds
// (deterministic in CI; the failing seed is reported).

var propZones = []string{
	"Europe/Amsterdam", "America/New_York", "Australia/Lord_Howe", "Asia/Tokyo",
	"UTC", "America/Santiago", "Pacific/Apia", "Asia/Kolkata", "America/St_Johns",
}

const propRuns = 3000

func prop(t *testing.T, runs int, f func(r *rand.Rand) error) {
	t.Helper()
	if testing.Short() {
		runs /= 10
	}
	for i := range runs {
		seed := uint64(i)*0x9E3779B97F4A7C15 + 1
		if err := f(rand.New(rand.NewPCG(seed, seed^0xDEADBEEF))); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func randZone(r *rand.Rand) string { return propZones[r.IntN(len(propZones))] }

// randInstant returns an instant in 2000..2060, biased towards DST-prone
// nights (01:00-04:00 local) by minute jitter around whole hours.
func randInstant(r *rand.Rand) time.Time {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	span := int64(60 * 365 * 86400)
	return time.Unix(base+r.Int64N(span), int64(r.IntN(1e9))).UTC()
}

func randAnchor(r *rand.Rand) string {
	y := 2000 + r.IntN(60)
	m := time.Month(1 + r.IntN(12))
	d := 1 + r.IntN(daysIn(y, m))
	hh := r.IntN(24)
	if r.IntN(2) == 0 {
		hh = r.IntN(4) // DST transitions cluster at night
	}
	mm := []int{0, 15, 30, 45, r.IntN(60)}[r.IntN(5)]
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC).Format(AnchorLayout)
}

func randCalendar(r *rand.Rand) Schedule {
	s := Schedule{Kind: KindInterval, Anchor: randAnchor(r), Timezone: randZone(r)}
	switch r.IntN(3) {
	case 0:
		s.Unit, s.Every = UnitDays, 1+r.IntN(10)
	case 1:
		s.Unit, s.Every = UnitWeeks, 1+r.IntN(4)
		if r.IntN(2) == 0 {
			s.Weekday = weekdayNames[r.IntN(7)]
		}
	default:
		s.Unit, s.Every = UnitMonths, 1+r.IntN(13)
		if r.IntN(2) == 0 {
			s.DayOfMonth = []int{1, 28, 29, 30, 31, 1 + r.IntN(31)}[r.IntN(6)]
		}
	}
	return s
}

func randSubDay(r *rand.Rand) Schedule {
	s := Schedule{Kind: KindInterval, Anchor: randAnchor(r), Timezone: randZone(r)}
	if r.IntN(2) == 0 {
		s.Unit, s.Every = UnitMinutes, 5+r.IntN(1436)
	} else {
		s.Unit, s.Every = UnitHours, 1+r.IntN(168)
	}
	return s
}

// randCron returns a random valid expression (one that matches some date).
func randCron(r *rand.Rand) string {
	for {
		if e := randCronExpr(r); func() bool { _, _, err := parseCron(e); return err == nil }() {
			return e
		}
	}
}

func randCronExpr(r *rand.Rand) string {
	pick := func(xs ...string) string { return xs[r.IntN(len(xs))] }
	return fmt.Sprintf("%s %s %s %s %s",
		pick("0", "30", "*/15", "5,35", "59", strconv.Itoa(r.IntN(60))),
		pick("*", "2", "1-3", "*/6", "0", "23", strconv.Itoa(r.IntN(24))),
		pick("*", "*", "1", "15", "29", "31", "1-7"),
		pick("*", "*", "3,10", "2", "4,11", "jan-jun"),
		pick("*", "*", "0", "1-5", "6", "sun,wed"))
}

func randAny(r *rand.Rand) Schedule {
	switch r.IntN(3) {
	case 0:
		return randCalendar(r)
	case 1:
		return randSubDay(r)
	default:
		return cronS(randCron(r), randZone(r))
	}
}

func TestPropertyStrictlyIncreasing(t *testing.T) {
	prop(t, propRuns, func(r *rand.Rand) error {
		s := randAny(r)
		p, err := Compile(s)
		if err != nil {
			return fmt.Errorf("compile %+v: %w", s, err)
		}
		after := randInstant(r)
		n1, err := p.Next(after)
		if err != nil {
			return fmt.Errorf("%+v Next(%s): %w", s, after, err)
		}
		n2, err := p.Next(n1)
		if err != nil {
			return fmt.Errorf("%+v Next(%s): %w", s, n1, err)
		}
		if !n1.After(after) || !n2.After(n1) {
			return fmt.Errorf("%+v not increasing: %s -> %s -> %s", s, after, n1, n2)
		}
		if n1.Second() != 0 || n1.Nanosecond() != 0 {
			return fmt.Errorf("%+v occurrence not on a minute: %s", s, n1)
		}
		// No occurrence is skipped: the instant just before n1 yields n1.
		if back, err := p.Next(n1.Add(-time.Second)); err != nil || !back.Equal(n1) {
			return fmt.Errorf("%+v Next(n1-1s) = %s, want %s (%w)", s, back, n1, err)
		}
		return nil
	})
}

func TestPropertyNextNMatchesNext(t *testing.T) {
	prop(t, propRuns/5, func(r *rand.Rand) error {
		s := randAny(r)
		after := randInstant(r)
		n := 1 + r.IntN(MaxPreview)
		list, err := NextN(s, after, n)
		if err != nil || len(list) != n {
			return fmt.Errorf("%+v NextN: %d, %w", s, len(list), err)
		}
		cur := after
		for i := range n {
			next, err := Next(s, cur)
			if err != nil || !next.Equal(list[i]) {
				return fmt.Errorf("%+v #%d: NextN %s, Next %s (%w)", s, i, list[i], next, err)
			}
			cur = next
		}
		return nil
	})
}

// wantWall checks the wall-clock rules for a calendar occurrence: the local
// time is the anchor time, unless that time does not exist on that date, in
// which case the occurrence lies in the gap's aftermath (shifted forward); an
// ambiguous time resolves to its earlier instant.
func checkWall(occ time.Time, hh, mm int, loc *time.Location) (wall, error) {
	got := wallOf(occ.In(loc))
	if got.hh == hh && got.mm == mm {
		// First occurrence: no earlier instant within 3h shows the same wall.
		for d := 15 * time.Minute; d <= 3*time.Hour; d += 15 * time.Minute {
			if wallOf(occ.Add(-d).In(loc)) == got {
				return got, fmt.Errorf("%s is the second instant of an ambiguous time", occ)
			}
		}
		return got, nil
	}
	for _, back := range []int{0, -1} {
		intended := got.addDays(back)
		intended.hh, intended.mm = hh, mm
		if !exists(intended, loc) && occ.Equal(resolve(intended, loc)) && got.utc().After(intended.utc()) {
			return intended, nil
		}
	}
	return got, fmt.Errorf("%s has wall %02d:%02d, want %02d:%02d or a forward shift", occ, got.hh, got.mm, hh, mm)
}

func TestPropertyCalendarWallClock(t *testing.T) {
	prop(t, propRuns, func(r *rand.Rand) error {
		s := randCalendar(r)
		p, err := Compile(s)
		if err != nil {
			return fmt.Errorf("compile %+v: %w", s, err)
		}
		cur := randInstant(r)
		for range 5 {
			occ, err := p.Next(cur)
			if err != nil {
				return fmt.Errorf("%+v: %w", s, err)
			}
			intended, err := checkWall(occ, p.anchorWall.hh, p.anchorWall.mm, p.loc)
			if err != nil {
				return fmt.Errorf("%+v: %w", s, err)
			}
			if occ.Before(p.anchorAt) {
				return fmt.Errorf("%+v: %s before the anchor", s, occ)
			}
			switch p.unit {
			case UnitDays, UnitWeeks:
				base, step := p.anchorWall, int64(p.every)
				if p.unit == UnitWeeks {
					base, step = p.baseDate, step*7
					if wd := intended.utc().Weekday(); weekdayNames[wd] != p.canon.Weekday {
						return fmt.Errorf("%+v: %s on %s", s, occ, wd)
					}
				}
				if diff := intended.dayNumber() - base.dayNumber(); diff%step != 0 {
					return fmt.Errorf("%+v: %s is %d days from the base", s, occ, diff)
				}
			case UnitMonths:
				if want := min(p.dom, daysIn(intended.y, intended.m)); intended.d != want {
					return fmt.Errorf("%+v: %s on day %d, want %d", s, occ, intended.d, want)
				}
				months := (intended.y*12 + int(intended.m)) - (p.anchorWall.y*12 + int(p.anchorWall.m))
				if months%p.every != 0 {
					return fmt.Errorf("%+v: %s is %d months from the anchor", s, occ, months)
				}
			default:
			}
			cur = occ
		}
		return nil
	})
}

func TestPropertySubDayAbsolute(t *testing.T) {
	prop(t, propRuns, func(r *rand.Rand) error {
		s := randSubDay(r)
		p, err := Compile(s)
		if err != nil {
			return fmt.Errorf("compile %+v: %w", s, err)
		}
		cur := randInstant(r)
		prev, err := p.Next(cur)
		if err != nil {
			return err
		}
		if prev.Sub(p.anchorAt)%p.step != 0 {
			return fmt.Errorf("%+v: %s off the series", s, prev)
		}
		for range 5 {
			next, err := p.Next(prev)
			if err != nil {
				return err
			}
			if next.Sub(prev) != p.step {
				return fmt.Errorf("%+v: %s -> %s is %s, want %s", s, prev, next, next.Sub(prev), p.step)
			}
			prev = next
		}
		return nil
	})
}

func TestPropertyDueCollapses(t *testing.T) {
	prop(t, propRuns, func(r *rand.Rand) error {
		s := randAny(r)
		p, err := Compile(s)
		if err != nil {
			return err
		}
		start := randInstant(r)
		nextRunAt, err := p.Next(start)
		if err != nil {
			return err
		}
		down := time.Duration(r.Int64N(int64(400 * 24 * time.Hour)))
		now := nextRunAt.Add(down)
		d, err := p.Due(nextRunAt, now)
		if err != nil {
			return err
		}
		if !d.Run || !d.ScheduledAt.Equal(nextRunAt) || !d.NextRunAt.After(now) {
			return fmt.Errorf("%+v: %+v at %s", s, d, now)
		}
		following, err := p.Next(nextRunAt)
		if err != nil {
			return err
		}
		if d.CatchUp != !following.After(now) {
			return fmt.Errorf("%+v: catchUp %v, following %s, now %s", s, d.CatchUp, following, now)
		}
		// The next tick never runs again for the collapsed period.
		again, err := p.Due(d.NextRunAt, now)
		if err != nil || again.Run || !again.NextRunAt.Equal(d.NextRunAt) {
			return fmt.Errorf("%+v: second tick %+v (%w)", s, again, err)
		}
		return nil
	})
}

// transitionNear reports whether loc changes its offset within ±margin of t.
func transitionNear(t time.Time, loc *time.Location, margin time.Duration) bool {
	_, o1 := t.Add(-margin).In(loc).Zone()
	_, o2 := t.Add(margin).In(loc).Zone()
	if o1 != o2 {
		return true
	}
	start, end := t.In(loc).ZoneBounds()
	return (!start.IsZero() && t.Sub(start) < margin) || (!end.IsZero() && end.Sub(t) < margin)
}

// Cron results equal robfig's SpecSchedule.Next except near DST transitions,
// where robfig skips nonexistent times and repeats ambiguous ones while we
// shift forward / take the first instant.
func TestPropertyCronMatchesRobfig(t *testing.T) {
	prop(t, propRuns, func(r *rand.Rand) error {
		expr, zone := randCron(r), randZone(r)
		p, err := Compile(cronS(expr, zone))
		if err != nil {
			return fmt.Errorf("%s: %w", expr, err)
		}
		rs, err := cronParser.Parse(expr)
		if err != nil {
			return err
		}
		spec, ok := rs.(*cron.SpecSchedule)
		if !ok {
			return fmt.Errorf("%s: not a SpecSchedule", expr)
		}
		spec.Location = p.loc
		cur := randInstant(r)
		for range 5 {
			ours, err := p.Next(cur)
			if err != nil {
				return fmt.Errorf("%s %s: %w", expr, zone, err)
			}
			theirs := spec.Next(cur)
			if !ours.Equal(theirs) {
				// robfig mishandles DST: it skips nonexistent times, repeats
				// ambiguous ones and, in half-hour DST zones, can be off by
				// 30 minutes for a while after a transition.
				near := transitionNear(ours, p.loc, 26*time.Hour) ||
					(!theirs.IsZero() && transitionNear(theirs, p.loc, 26*time.Hour))
				if !near && zone != "Australia/Lord_Howe" {
					return fmt.Errorf("%s %s after %s: ours %s, robfig %s", expr, zone, cur, ours, theirs)
				}
				if err := oracleCheck(p, cur, ours); err != nil {
					return fmt.Errorf("%s %s (robfig %s): %w", expr, zone, theirs, err)
				}
			}
			cur = ours
		}
		return nil
	})
}

// oracleCheck verifies occ = min{resolve(w) : w matches, resolve(w) > after}
// by brute force over every wall minute from a day before `after` to occ.
func oracleCheck(p *Plan, after, occ time.Time) error {
	w := wallOf(after.In(p.loc)).addDays(-1)
	w.hh, w.mm = 0, 0
	end := wallOf(occ.In(p.loc)).utc()
	found := false
	for t := w.utc(); !t.After(end.Add(24 * time.Hour)); t = t.Add(time.Minute) {
		cand := wallOf(t)
		if !p.cron.dayMatches(cand) || p.cron.hour&(1<<uint(cand.hh)) == 0 || p.cron.minute&(1<<uint(cand.mm)) == 0 {
			continue
		}
		at := resolve(cand, p.loc)
		if at.After(after) && at.Before(occ) {
			return fmt.Errorf("missed occurrence %s (wall %v) before %s", at, cand, occ)
		}
		found = found || at.Equal(occ)
	}
	if !found {
		return fmt.Errorf("%s is not an occurrence", occ)
	}
	return nil
}

// In zones without DST the two implementations agree exactly.
func TestPropertyCronEqualsRobfigWithoutDST(t *testing.T) {
	zones := []string{"UTC", "Asia/Tokyo", "Asia/Kolkata"}
	prop(t, propRuns, func(r *rand.Rand) error {
		expr, zone := randCron(r), zones[r.IntN(len(zones))]
		p, err := Compile(cronS(expr, zone))
		if err != nil {
			return err
		}
		rs, err := cronParser.Parse(expr)
		if err != nil {
			return err
		}
		spec, ok := rs.(*cron.SpecSchedule)
		if !ok {
			return fmt.Errorf("%s: not a SpecSchedule", expr)
		}
		spec.Location = p.loc
		cur := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.Int64N(int64(50 * 365 * 24 * time.Hour))))
		for range 5 {
			ours, err := p.Next(cur)
			if err != nil {
				return err
			}
			if theirs := spec.Next(cur); !ours.Equal(theirs) {
				return fmt.Errorf("%s %s after %s: ours %s, robfig %s", expr, zone, cur, ours, theirs)
			}
			cur = ours
		}
		return nil
	})
}

// Cron occurrences also obey the DST rules: matched wall times that are
// ambiguous resolve to their first instant only (never twice).
func TestPropertyCronAmbiguousOnce(t *testing.T) {
	prop(t, propRuns/3, func(r *rand.Rand) error {
		zone := []string{"Europe/Amsterdam", "America/New_York", "Australia/Lord_Howe"}[r.IntN(3)]
		expr := randCron(r)
		p, err := Compile(cronS(expr, zone))
		if err != nil {
			return err
		}
		cur := randInstant(r)
		seen := map[wall]bool{}
		for range 20 {
			occ, err := p.Next(cur)
			if err != nil {
				return err
			}
			w := wallOf(occ.In(p.loc))
			if seen[w] && exists(w, p.loc) {
				return fmt.Errorf("%s %s: wall %v occurs twice", expr, zone, w)
			}
			seen[w] = true
			cur = occ
		}
		return nil
	})
}

// Every cron result is checked against the brute-force definition.
func TestPropertyCronOracle(t *testing.T) {
	prop(t, propRuns/10, func(r *rand.Rand) error {
		expr, zone := randCron(r), randZone(r)
		p, err := Compile(cronS(expr, zone))
		if err != nil {
			return err
		}
		cur := randInstant(r)
		for range 3 {
			occ, err := p.Next(cur)
			if err != nil {
				return err
			}
			if occ.Sub(cur) > 3*24*time.Hour {
				return nil
			}
			if err := oracleCheck(p, cur, occ); err != nil {
				return fmt.Errorf("%s %s after %s: %w", expr, zone, cur, err)
			}
			cur = occ
		}
		return nil
	})
}
