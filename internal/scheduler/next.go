package scheduler

import (
	"fmt"
	"time"
)

// Next returns the first occurrence of s strictly after `after`. Manual
// schedules return ErrManual.
func Next(s Schedule, after time.Time) (time.Time, error) {
	p, err := compile(s)
	if err != nil {
		return time.Time{}, err
	}
	return p.Next(after)
}

// NextN returns the next n occurrences strictly after `after` (preview),
// 1 <= n <= MaxPreview.
func NextN(s Schedule, after time.Time, n int) ([]time.Time, error) {
	p, err := compile(s)
	if err != nil {
		return nil, err
	}
	return p.NextN(after, n)
}

// Due evaluates a schedule at a scheduler tick; see Plan.Due.
func Due(s Schedule, nextRunAt, now time.Time) (Decision, error) {
	p, err := compile(s)
	if err != nil {
		return Decision{}, err
	}
	return p.Due(nextRunAt, now)
}

func checkRange(t time.Time) error {
	if y := t.Year(); y < 1 || y > 9999 {
		return ErrOutOfRange
	}
	return nil
}

// Next returns the first occurrence strictly after `after`.
func (p *Plan) Next(after time.Time) (time.Time, error) {
	if err := checkRange(after); err != nil {
		return time.Time{}, err
	}
	switch p.kind {
	case KindManual:
		return time.Time{}, ErrManual
	case KindCron:
		return p.cron.next(after, p.loc)
	case KindInterval:
	}
	switch p.unit {
	case UnitMinutes, UnitHours:
		if after.Before(p.anchorAt) {
			return p.anchorAt, nil
		}
		return p.nextAbsolute(after), nil
	case UnitDays:
		return p.nextDays(p.anchorWall, int64(p.every), after)
	case UnitWeeks:
		return p.nextDays(p.baseDate, int64(p.every)*7, after)
	default:
		return p.nextMonths(after)
	}
}

// nextAbsolute: anchorAt + k*step, k >= 0, in elapsed time.
func (p *Plan) nextAbsolute(after time.Time) time.Time {
	step := int64(p.step / time.Second)
	a := p.anchorAt.Unix()
	k := floorDiv(after.Unix()-a, step) + 1
	return time.Unix(a+k*step, 0).In(p.loc)
}

// nextDays: wall date base + k*stepDays at the anchor time of day, k >= 0.
func (p *Plan) nextDays(base wall, stepDays int64, after time.Time) (time.Time, error) {
	local := wallOf(after.In(p.loc))
	k := max(floorDiv(local.dayNumber()-base.dayNumber(), stepDays)-1, 0)
	for range 8 {
		w := base.addDays(int(k * stepDays))
		if at := resolve(w, p.loc); at.After(after) {
			return at, nil
		}
		k++
	}
	return time.Time{}, fmt.Errorf("%w: day interval did not converge", ErrNoOccurrence)
}

// monthWall is occurrence k of a monthly schedule: anchor month + k*every,
// day clamped to the month's length, anchor time of day.
func (p *Plan) monthWall(k int64) wall {
	ym := int64(p.anchorWall.y)*12 + int64(p.anchorWall.m-1) + k*int64(p.every)
	y, m := int(floorDiv(ym, 12)), time.Month(ym-floorDiv(ym, 12)*12+1)
	return wall{y: y, m: m, d: min(p.dom, daysIn(y, m)), hh: p.anchorWall.hh, mm: p.anchorWall.mm}
}

func (p *Plan) nextMonths(after time.Time) (time.Time, error) {
	local := wallOf(after.In(p.loc))
	diff := (int64(local.y)*12 + int64(local.m-1)) - (int64(p.anchorWall.y)*12 + int64(p.anchorWall.m-1))
	k := max(floorDiv(diff, int64(p.every))-1, 0)
	for range 8 {
		at := resolve(p.monthWall(k), p.loc)
		// The first month may fall before the anchor (day_of_month < anchor day).
		if at.After(after) && !at.Before(p.anchorAt) {
			return at, nil
		}
		k++
	}
	return time.Time{}, fmt.Errorf("%w: month interval did not converge", ErrNoOccurrence)
}

// NextN returns the next n occurrences strictly after `after`; it equals n
// repeated calls of Next.
func (p *Plan) NextN(after time.Time, n int) ([]time.Time, error) {
	if n < 1 || n > MaxPreview {
		return nil, fmt.Errorf("%w: preview size must be between 1 and %d", ErrInvalid, MaxPreview)
	}
	out := make([]time.Time, 0, n)
	t := after
	for range n {
		next, err := p.Next(t)
		if err != nil {
			return out, err
		}
		out = append(out, next)
		t = next
	}
	return out, nil
}

// Decision is the result of Due.
type Decision struct {
	// Run reports that one run must be enqueued now.
	Run bool
	// ScheduledAt is the occurrence the run stands for: the stored
	// next_run_at (the earliest missed occurrence). Use it for the River
	// unique key.
	ScheduledAt time.Time
	// CatchUp reports that more than one occurrence passed (e.g. while bekup
	// was down); they are collapsed into this single run.
	CatchUp bool
	// NextRunAt is the first occurrence strictly after now, the new value of
	// next_run_at (zero for manual schedules).
	NextRunAt time.Time
}

// Due evaluates the plan at a scheduler tick. nextRunAt is the stored
// next_run_at (zero if not yet initialized). Whatever number of occurrences
// lie in (nextRunAt, now], at most one run is returned:
//
//   - manual: never runs, NextRunAt zero;
//   - nextRunAt zero: no run, NextRunAt = Next(now) (initialization);
//   - nextRunAt > now: no run, NextRunAt unchanged;
//   - otherwise one run for nextRunAt, CatchUp if Next(nextRunAt) <= now,
//     and NextRunAt = Next(now), so the missed occurrences are skipped.
func (p *Plan) Due(nextRunAt, now time.Time) (Decision, error) {
	if p.kind == KindManual {
		return Decision{}, nil
	}
	if nextRunAt.IsZero() {
		n, err := p.Next(now)
		return Decision{NextRunAt: n}, err
	}
	if nextRunAt.After(now) {
		return Decision{NextRunAt: nextRunAt}, nil
	}
	following, err := p.Next(nextRunAt)
	if err != nil {
		return Decision{}, err
	}
	n, err := p.Next(now)
	if err != nil {
		return Decision{}, err
	}
	return Decision{
		Run:         true,
		ScheduledAt: nextRunAt,
		CatchUp:     !following.After(now),
		NextRunAt:   n,
	}, nil
}
