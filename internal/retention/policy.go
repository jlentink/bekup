package retention

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Policy is a job's retention policy. At least one of KeepLast and KeepDays
// is positive (DB CHECK); a policy with neither keeps everything (fail
// safe).
type Policy struct {
	// KeepLast keeps the newest N complete runs per destination (0 = off).
	KeepLast int
	// KeepDays keeps complete runs younger than N days (0 = off).
	KeepDays int
}

func (p Policy) active() bool { return p.KeepLast > 0 || p.KeepDays > 0 }

// keepLastCovers reports whether the complete run at rank (0 = newest
// complete run on a destination) is among the newest KeepLast.
func (p Policy) keepLastCovers(rank int) bool { return p.KeepLast > 0 && rank < p.KeepLast }

// keepDaysUntil returns when a complete run of time t leaves the KeepDays
// window: it is kept while now is before that instant (ok = false: KeepDays
// is off).
func (p Policy) keepDaysUntil(t time.Time) (time.Time, bool) {
	if p.KeepDays <= 0 {
		return time.Time{}, false
	}
	return t.Add(time.Duration(p.KeepDays) * 24 * time.Hour), true
}

// Artifact is one present object of a finished run on a destination.
type Artifact struct {
	ID            uuid.UUID
	RunID         uuid.UUID
	DestinationID uuid.UUID
	// ItemID is the run item (nil for the manifest).
	ItemID   *uuid.UUID
	Key      string
	Size     int64
	Verified bool
	// LockUntil is the recorded Object Lock retain-until (zero: none).
	LockUntil time.Time
}

// Manifest reports whether a is the run's manifest.json.
func (a Artifact) Manifest() bool { return a.ItemID == nil }

// Run is a finished run with present artifacts.
type Run struct {
	ID uuid.UUID
	// Time orders runs (the storage timestamp of the run's directory).
	Time time.Time
	// Items are the run's items (databases and globals).
	Items []uuid.UUID
}

// Input is everything the policy needs for one job.
type Input struct {
	Policy Policy
	Now    time.Time
	Runs   []Run
	// Artifacts are the present artifacts of Runs (any destination).
	Artifacts []Artifact
}

// KeepReason says why a run is kept on a destination.
type KeepReason string

// Keep reasons, in priority order (a run kept for several reasons reports
// the first).
const (
	// KeepNewestComplete: the newest complete run, never deleted.
	KeepNewestComplete KeepReason = "newest_complete"
	// KeepLocked: an artifact of the run is under an Object Lock.
	KeepLocked KeepReason = "locked"
	// KeepLast: among the newest N complete runs.
	KeepLast KeepReason = "keep_last"
	// KeepDays: a complete run younger than the configured days.
	KeepDays KeepReason = "keep_days"
	// KeepNewerIncomplete: an incomplete run newer than the newest complete
	// one (it may hold the only copy of some database).
	KeepNewerIncomplete KeepReason = "newer_than_newest_complete"
	// KeepNoComplete: the destination holds no complete run, so nothing
	// is deleted there.
	KeepNoComplete KeepReason = "no_complete_run"
	// KeepNoPolicy: the job has no active policy.
	KeepNoPolicy KeepReason = "no_policy"
)

// DestinationPlan is the plan for one destination of a job.
type DestinationPlan struct {
	DestinationID uuid.UUID
	// NewestComplete is the newest run that is complete on this
	// destination (uuid.Nil: none, nothing is deleted).
	NewestComplete uuid.UUID
	// NewestArtifacts are the present artifacts of NewestComplete here
	// (the invariant check confirms they exist before deleting anything).
	NewestArtifacts []Artifact
	// Complete lists the complete runs, newest first.
	Complete []uuid.UUID
	// Keep maps every kept run to its reason.
	Keep map[uuid.UUID]KeepReason
	// Delete lists the eligible runs, oldest first, with their artifacts
	// (dumps first, the manifest last).
	Delete []RunDeletion
}

// RunDeletion is one eligible run on one destination.
type RunDeletion struct {
	RunID    uuid.UUID
	Time     time.Time
	Complete bool
	// Dumps are the database/globals artifacts; Manifest is nil when the
	// run has no manifest here.
	Dumps    []Artifact
	Manifest *Artifact
}

// Objects is the number of objects of d.
func (d RunDeletion) Objects() int {
	n := len(d.Dumps)
	if d.Manifest != nil {
		n++
	}
	return n
}

// DeleteCount is the number of objects the plan deletes.
func (p DestinationPlan) DeleteCount() int {
	n := 0
	for _, d := range p.Delete {
		n += d.Objects()
	}
	return n
}

// Plan computes the per-destination retention plan of one job. It is pure:
// the same input gives the same plan. Destinations are ordered by id.
//
// Per destination: a run is complete when it has at least one item, every
// item has a present, verified artifact there and its manifest is present
// there. Kept are the newest complete run (always), the newest KeepLast
// complete runs, complete runs younger than KeepDays, every run with an
// artifact locked beyond Now, and incomplete runs newer than the newest
// complete one. Everything else is eligible. A destination without a
// complete run deletes nothing.
func Plan(in Input) []DestinationPlan {
	runs := make(map[uuid.UUID]Run, len(in.Runs))
	for _, r := range in.Runs {
		runs[r.ID] = r
	}
	byDest := map[uuid.UUID]map[uuid.UUID][]Artifact{}
	for _, a := range in.Artifacts {
		if _, ok := runs[a.RunID]; !ok {
			continue // artifact of an unknown (unfinished) run: never touched
		}
		m := byDest[a.DestinationID]
		if m == nil {
			m = map[uuid.UUID][]Artifact{}
			byDest[a.DestinationID] = m
		}
		m[a.RunID] = append(m[a.RunID], a)
	}
	dests := make([]uuid.UUID, 0, len(byDest))
	for d := range byDest {
		dests = append(dests, d)
	}
	slices.SortFunc(dests, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })

	out := make([]DestinationPlan, 0, len(dests))
	for _, d := range dests {
		out = append(out, planDestination(in, runs, d, byDest[d]))
	}
	return out
}

// newerFirst orders runs newest first (ties: larger id first, stable).
func newerFirst(a, b Run) int {
	if c := b.Time.Compare(a.Time); c != 0 {
		return c
	}
	return cmp.Compare(b.ID.String(), a.ID.String())
}

func complete(r Run, arts []Artifact) bool {
	if len(r.Items) == 0 {
		return false
	}
	manifest := false
	items := map[uuid.UUID]bool{}
	for _, a := range arts {
		if a.Manifest() {
			manifest = true
			continue
		}
		if a.Verified {
			items[*a.ItemID] = true
		}
	}
	if !manifest {
		return false
	}
	for _, it := range r.Items {
		if !items[it] {
			return false
		}
	}
	return true
}

func planDestination(in Input, runs map[uuid.UUID]Run, dest uuid.UUID, arts map[uuid.UUID][]Artifact) DestinationPlan {
	p := DestinationPlan{DestinationID: dest, Keep: map[uuid.UUID]KeepReason{}}
	here := make([]Run, 0, len(arts))
	for id := range arts {
		here = append(here, runs[id])
	}
	slices.SortFunc(here, newerFirst)

	var completeRuns []Run
	for _, r := range here {
		if complete(r, arts[r.ID]) {
			completeRuns = append(completeRuns, r)
			p.Complete = append(p.Complete, r.ID)
		}
	}
	keep := func(id uuid.UUID, why KeepReason) {
		if _, ok := p.Keep[id]; !ok {
			p.Keep[id] = why
		}
	}
	if len(completeRuns) == 0 || !in.Policy.active() {
		why := KeepNoComplete
		if len(completeRuns) > 0 {
			why = KeepNoPolicy
			p.NewestComplete = completeRuns[0].ID
			p.NewestArtifacts = slices.Clone(arts[completeRuns[0].ID])
		}
		for _, r := range here {
			keep(r.ID, why)
		}
		return p
	}
	newest := completeRuns[0]
	p.NewestComplete = newest.ID
	p.NewestArtifacts = slices.Clone(arts[newest.ID])
	keep(newest.ID, KeepNewestComplete)

	for _, r := range here {
		for _, a := range arts[r.ID] {
			if a.LockUntil.After(in.Now) {
				keep(r.ID, KeepLocked)
				break
			}
		}
	}
	for i, r := range completeRuns {
		if in.Policy.keepLastCovers(i) {
			keep(r.ID, KeepLast)
		}
		if until, ok := in.Policy.keepDaysUntil(r.Time); ok && in.Now.Before(until) {
			keep(r.ID, KeepDays)
		}
	}
	completeSet := map[uuid.UUID]bool{}
	for _, r := range completeRuns {
		completeSet[r.ID] = true
	}
	for _, r := range here {
		if !completeSet[r.ID] && newerFirst(r, newest) <= 0 {
			// Newer than (or ordered before) the newest complete run.
			keep(r.ID, KeepNewerIncomplete)
		}
	}

	for i := len(here) - 1; i >= 0; i-- { // oldest first
		r := here[i]
		if _, kept := p.Keep[r.ID]; kept {
			continue
		}
		del := RunDeletion{RunID: r.ID, Time: r.Time, Complete: completeSet[r.ID]}
		for _, a := range arts[r.ID] {
			if a.Manifest() {
				m := a
				del.Manifest = &m
				continue
			}
			del.Dumps = append(del.Dumps, a)
		}
		slices.SortFunc(del.Dumps, func(a, b Artifact) int { return cmp.Compare(a.Key, b.Key) })
		p.Delete = append(p.Delete, del)
	}
	return p
}

// checkPlan verifies the plan's own invariants: the newest complete run is
// kept and none of its artifacts is scheduled for deletion, and no kept run
// appears in Delete. A violation means a bug; the destination is skipped.
func checkPlan(p DestinationPlan) bool {
	if p.NewestComplete == uuid.Nil {
		return len(p.Delete) == 0
	}
	if _, ok := p.Keep[p.NewestComplete]; !ok {
		return false
	}
	for _, d := range p.Delete {
		if d.RunID == p.NewestComplete {
			return false
		}
		if _, kept := p.Keep[d.RunID]; kept {
			return false
		}
		for _, a := range d.Dumps {
			if a.RunID != d.RunID || a.DestinationID != p.DestinationID || a.Manifest() {
				return false
			}
		}
		if m := d.Manifest; m != nil && (m.RunID != d.RunID || m.DestinationID != p.DestinationID || !m.Manifest()) {
			return false
		}
	}
	return true
}
