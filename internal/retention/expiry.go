package retention

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Expiry projection for the job calendar (task 3.14, D-156). It answers
// "when may retention first remove this copy?" with the same keep rules as
// Plan (the newest complete run, KeepLast, KeepDays, Object Lock and the
// newer-incomplete rule), given the times at which future complete runs
// are assumed to arrive.

// Copy is one finished run's present copy on one destination, summarized:
// Complete has the meaning of Plan (at least one item, every item with a
// present, verified artifact here, and the manifest present here) and
// LockUntil is the latest recorded Object Lock retain-until of its
// artifacts (zero: none).
type Copy struct {
	RunID         uuid.UUID
	DestinationID uuid.UUID
	// Time orders runs, as Run.Time.
	Time      time.Time
	Complete  bool
	LockUntil time.Time
}

// ExpiryCause says what determines an expiry time.
type ExpiryCause string

// Expiry causes.
const (
	// ExpiryEligible: no rule keeps the copy any more; the next sweep may
	// remove it.
	ExpiryEligible ExpiryCause = "eligible"
	// ExpiryKeepDays: the copy leaves the KeepDays window.
	ExpiryKeepDays ExpiryCause = "keep_days"
	// ExpiryKeepLast: enough newer complete runs push it out of KeepLast.
	ExpiryKeepLast ExpiryCause = "keep_last"
	// ExpiryNewerComplete: the copy is the newest complete run (or an
	// incomplete one newer than it) and stays until a newer complete run
	// exists.
	ExpiryNewerComplete ExpiryCause = "newer_complete"
	// ExpiryLocked: an Object Lock retain-until outlasts every other rule.
	ExpiryLocked ExpiryCause = "locked"
)

// Expiry is the earliest time retention may remove one copy. Projected is
// set when the removal depends on future complete runs (the newest
// complete rule or KeepLast); At then assumes that every planned
// occurrence succeeds. Retention removes the copy at its first sweep at or
// after At.
type Expiry struct {
	RunID         uuid.UUID
	DestinationID uuid.UUID
	At            time.Time
	Projected     bool
	Cause         ExpiryCause
	// NewerNeeded is the number of newer complete runs the removal waits
	// for (0: none).
	NewerNeeded int
}

// newerNeeded returns how many newer complete runs must exist before the
// complete run at rank (0 = newest) is kept neither by KeepLast nor as the
// newest complete run.
func (p Policy) newerNeeded(rank int) int {
	n := 0
	if p.keepLastCovers(rank) {
		n = p.KeepLast - rank
	}
	if rank == 0 && n < 1 {
		n = 1
	}
	return n
}

// Expiries projects the expiry of every copy of one job. nthSuccess(n)
// returns the time of the n-th future complete run (n >= 1; ok = false
// when unknown, e.g. a manual or paused schedule or beyond the horizon);
// copies whose removal waits for an unknown run are omitted, as are all
// copies of a job without an active policy (everything is kept). The
// result is ordered by destination id, then newest run first.
func Expiries(p Policy, now time.Time, copies []Copy, nthSuccess func(n int) (time.Time, bool)) []Expiry {
	if !p.active() || len(copies) == 0 {
		return nil
	}
	byDest := map[uuid.UUID][]Copy{}
	for _, c := range copies {
		byDest[c.DestinationID] = append(byDest[c.DestinationID], c)
	}
	dests := make([]uuid.UUID, 0, len(byDest))
	for d := range byDest {
		dests = append(dests, d)
	}
	slices.SortFunc(dests, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })

	var out []Expiry
	for _, d := range dests {
		list := byDest[d]
		slices.SortFunc(list, func(a, b Copy) int {
			return newerFirst(Run{ID: a.RunID, Time: a.Time}, Run{ID: b.RunID, Time: b.Time})
		})
		rank, seenComplete := 0, false
		for _, c := range list {
			e := Expiry{RunID: c.RunID, DestinationID: d, At: now, Cause: ExpiryEligible}
			switch {
			case c.Complete:
				e.NewerNeeded = p.newerNeeded(rank)
				if until, ok := p.keepDaysUntil(c.Time); ok && until.After(e.At) {
					e.At, e.Cause = until, ExpiryKeepDays
				}
				rank++
				seenComplete = true
			case !seenComplete:
				// Newer than the newest complete run here (or there is
				// none): kept until a newer complete run exists.
				e.NewerNeeded = 1
			}
			if e.NewerNeeded > 0 {
				t, ok := nthSuccess(e.NewerNeeded)
				if !ok {
					continue
				}
				e.Projected = true
				if t.After(e.At) {
					e.At = t
					e.Cause = ExpiryKeepLast
					if !c.Complete || !p.keepLastCovers(rank-1) {
						e.Cause = ExpiryNewerComplete
					}
				}
			}
			if c.LockUntil.After(e.At) {
				e.At, e.Cause = c.LockUntil, ExpiryLocked
			}
			out = append(out, e)
		}
	}
	return out
}
