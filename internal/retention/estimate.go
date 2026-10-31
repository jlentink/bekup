package retention

import (
	"errors"
	"time"
)

// Estimate of the backups a policy keeps per destination (D-209): the job
// editor's live preview. It assumes that every planned occurrence of the
// schedule produces a complete run and counts what the keep rules of Plan
// keep once the schedule has run long enough (the steady state): the
// newest complete run, the newest KeepLast and those younger than KeepDays.
// The rules add up (a run is kept when either keeps it), so Kept is the
// size of the union, normally the larger of the two counts.

// MaxEstimateRuns bounds the occurrences one estimate projects; a policy
// that needs more (KeepDays over a very frequent schedule) is reported as
// a lower bound (AtLeast).
const MaxEstimateRuns = 20000

// Estimate is the steady-state number of complete runs kept per
// destination.
type Estimate struct {
	// Kept is the number kept (valid only when Known).
	Kept int
	// ByKeepLast is the number KeepLast keeps on its own.
	ByKeepLast int
	// ByKeepDays is the number KeepDays keeps on its own (valid only when
	// Known).
	ByKeepDays int
	// Known is false when the count depends on when runs happen: a
	// schedule without occurrences (manual) and a KeepDays limit.
	Known bool
	// AtLeast reports that the projection stopped at MaxEstimateRuns: at
	// least Kept runs are kept.
	AtLeast bool
}

// ErrNoOccurrence is what a NextFunc returns when the schedule has no
// further occurrence (a manual schedule).
var ErrNoOccurrence = errors.New("retention: the schedule has no further occurrence")

// NextFunc returns the first occurrence of a schedule strictly after t
// (ErrNoOccurrence: none).
type NextFunc func(t time.Time) (time.Time, error)

// EstimateKept projects the steady state of p for a schedule whose
// occurrences next yields, starting after from.
func EstimateKept(p Policy, from time.Time, next NextFunc) (Estimate, error) {
	var e Estimate
	if !p.active() {
		return e, errors.New("retention: the policy keeps everything")
	}
	first, err := next(from)
	if errors.Is(err, ErrNoOccurrence) {
		// Manual runs: KeepLast is exact, KeepDays depends on how often
		// someone starts a backup.
		e.ByKeepLast = p.KeepLast
		if p.KeepDays == 0 {
			e.Kept, e.Known = max(p.KeepLast, 1), true
		}
		return e, nil
	}
	if err != nil {
		return e, err
	}
	window := time.Duration(p.KeepDays) * 24 * time.Hour
	times := []time.Time{first}
	needMore := func() bool {
		n := len(times)
		return n <= p.KeepLast || (p.KeepDays > 0 && times[n-1].Sub(times[0]) < window)
	}
	for needMore() {
		if len(times) >= MaxEstimateRuns {
			e.AtLeast = true
			break
		}
		t, err := next(times[len(times)-1])
		if errors.Is(err, ErrNoOccurrence) {
			break // a finite schedule: count what it produces
		}
		if err != nil {
			return e, err
		}
		times = append(times, t)
	}
	// The newest projected run is "now"; ranks count back from it.
	now := times[len(times)-1]
	for i := len(times) - 1; i >= 0; i-- {
		rank := len(times) - 1 - i
		byLast := p.keepLastCovers(rank)
		byDays := false
		if until, ok := p.keepDaysUntil(times[i]); ok && now.Before(until) {
			byDays = true
		}
		if byLast {
			e.ByKeepLast++
		}
		if byDays {
			e.ByKeepDays++
		}
		if rank == 0 || byLast || byDays {
			e.Kept++
		}
	}
	e.Known = true
	return e, nil
}
