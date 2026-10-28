package retention

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
)

// successes returns nthSuccess over fixed future times.
func successes(ts ...time.Time) func(int) (time.Time, bool) {
	return func(n int) (time.Time, bool) {
		if n < 1 || n > len(ts) {
			return time.Time{}, false
		}
		return ts[n-1], true
	}
}

// daily returns n daily successes starting one day after now.
func daily(n int) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = now.Add(time.Duration(i+1) * day)
	}
	return out
}

func TestExpiries(t *testing.T) {
	r1, r2, r3, r4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	lock := now.Add(100 * day)
	// Newest first: r4 (incomplete, newest), r3, r2, r1 (complete).
	copies := []Copy{
		{RunID: r1, DestinationID: destA, Time: now.Add(-10 * day), Complete: true},
		{RunID: r2, DestinationID: destA, Time: now.Add(-5 * day), Complete: true},
		{RunID: r3, DestinationID: destA, Time: now.Add(-1 * day), Complete: true},
		{RunID: r4, DestinationID: destA, Time: now.Add(-1 * time.Hour)},
	}
	type want struct {
		at        time.Time
		projected bool
		cause     ExpiryCause
	}
	for name, tc := range map[string]struct {
		pol     Policy
		copies  []Copy
		future  []time.Time
		want    map[uuid.UUID]want
		missing []uuid.UUID
	}{
		"keep_days only: exact, newest projected": {
			pol: Policy{KeepDays: 7}, copies: copies, future: daily(3),
			want: map[uuid.UUID]want{
				r1: {now, false, ExpiryEligible},
				r2: {now.Add(2 * day), false, ExpiryKeepDays},
				r3: {now.Add(6 * day), true, ExpiryKeepDays}, // newest complete: waits for 1 run (day 1), keep_days later
				r4: {now.Add(1 * day), true, ExpiryNewerComplete},
			},
		},
		"keep_last only: projected": {
			pol: Policy{KeepLast: 2}, copies: copies, future: daily(3),
			want: map[uuid.UUID]want{
				r1: {now, false, ExpiryEligible},
				r2: {now.Add(1 * day), true, ExpiryKeepLast}, // rank 1: one newer run
				r3: {now.Add(2 * day), true, ExpiryKeepLast}, // rank 0: two newer runs
				r4: {now.Add(1 * day), true, ExpiryNewerComplete},
			},
		},
		"both: the later date": {
			pol: Policy{KeepLast: 2, KeepDays: 2}, copies: copies, future: daily(3),
			want: map[uuid.UUID]want{
				r1: {now, false, ExpiryEligible},
				r2: {now.Add(1 * day), true, ExpiryKeepLast},
				r3: {now.Add(2 * day), true, ExpiryKeepLast},
			},
		},
		"keep_days beats keep_last": {
			pol: Policy{KeepLast: 1, KeepDays: 30}, copies: copies[:3], future: daily(3),
			want: map[uuid.UUID]want{
				r1: {now.Add(20 * day), false, ExpiryKeepDays},
				r2: {now.Add(25 * day), false, ExpiryKeepDays},
				r3: {now.Add(29 * day), true, ExpiryKeepDays},
			},
		},
		"lock_until is exact and wins": {
			pol: Policy{KeepLast: 1},
			copies: []Copy{
				{RunID: r1, DestinationID: destA, Time: now.Add(-10 * day), Complete: true, LockUntil: lock},
				{RunID: r2, DestinationID: destA, Time: now.Add(-5 * day), Complete: true},
			},
			future: daily(1),
			want: map[uuid.UUID]want{
				r1: {lock, false, ExpiryLocked},
				r2: {now.Add(1 * day), true, ExpiryKeepLast},
			},
		},
		"unknown future: newest omitted": {
			pol: Policy{KeepLast: 2}, copies: copies[:3], future: daily(1),
			want: map[uuid.UUID]want{
				r1: {now, false, ExpiryEligible},
				r2: {now.Add(1 * day), true, ExpiryKeepLast},
			},
			missing: []uuid.UUID{r3},
		},
		"no complete copy: all wait for one": {
			pol: Policy{KeepDays: 1},
			copies: []Copy{
				{RunID: r1, DestinationID: destA, Time: now.Add(-10 * day)},
				{RunID: r2, DestinationID: destA, Time: now.Add(-5 * day)},
			},
			future: daily(1),
			want: map[uuid.UUID]want{
				r1: {now.Add(day), true, ExpiryNewerComplete},
				r2: {now.Add(day), true, ExpiryNewerComplete},
			},
		},
		"no policy: nothing": {
			pol: Policy{}, copies: copies, future: daily(3), missing: []uuid.UUID{r1, r2, r3, r4},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := map[uuid.UUID]Expiry{}
			for _, e := range Expiries(tc.pol, now, tc.copies, successes(tc.future...)) {
				got[e.RunID] = e
			}
			for id, w := range tc.want {
				g, ok := got[id]
				if !ok {
					t.Fatalf("no expiry for %s", id)
				}
				if !g.At.Equal(w.at) || g.Projected != w.projected || g.Cause != w.cause {
					t.Errorf("%s: got %v projected=%v cause=%s, want %v %v %s", id, g.At, g.Projected, g.Cause, w.at, w.projected, w.cause)
				}
			}
			for _, id := range tc.missing {
				if _, ok := got[id]; ok {
					t.Errorf("unexpected expiry for %s", id)
				}
			}
		})
	}
}

// TestExpiriesMatchPlan checks the projection against Plan itself: with
// the assumed future runs present, every copy is kept just before its
// expiry and eligible at it.
func TestExpiriesMatchPlan(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for iter := range 300 {
		pol := Policy{KeepLast: rng.IntN(4), KeepDays: rng.IntN(5)}
		if !pol.active() {
			pol.KeepLast = 1
		}
		f := newFixture()
		copies := make([]Copy, 0, 6)
		for i := range 1 + rng.IntN(6) {
			age := time.Duration(rng.IntN(10*24)) * time.Hour
			completeHere := rng.IntN(3) > 0
			p := full(destA, 1)
			if !completeHere {
				p.manifest = false
			}
			var lockUntil time.Time
			if rng.IntN(5) == 0 {
				lockUntil = now.Add(time.Duration(rng.IntN(20*24)) * time.Hour)
				p.lock = lockUntil
			}
			id := f.run("r"+string(rune('a'+i)), age, 1, p)
			copies = append(copies, Copy{RunID: id, DestinationID: destA, Time: now.Add(-age), Complete: completeHere, LockUntil: lockUntil})
		}
		future := make([]time.Time, 0, 8)
		for i := range 8 {
			future = append(future, now.Add(time.Duration(i*13+1+rng.IntN(12))*time.Hour))
		}
		planAt := func(at time.Time) DestinationPlan {
			runs, arts := append([]Run(nil), f.runs...), append([]Artifact(nil), f.arts...)
			for _, ft := range future {
				if ft.After(at) {
					continue
				}
				item := uuid.New()
				r := Run{ID: uuid.New(), Time: ft, Items: []uuid.UUID{item}}
				runs = append(runs, r)
				arts = append(arts,
					Artifact{ID: uuid.New(), RunID: r.ID, DestinationID: destA, ItemID: &item, Key: r.ID.String(), Verified: true},
					Artifact{ID: uuid.New(), RunID: r.ID, DestinationID: destA, Key: r.ID.String() + "/m", Verified: true})
			}
			for _, p := range Plan(Input{Policy: pol, Now: at, Runs: runs, Artifacts: arts}) {
				if p.DestinationID == destA {
					return p
				}
			}
			t.Fatal("no plan")
			return DestinationPlan{}
		}
		deleted := func(p DestinationPlan, id uuid.UUID) bool {
			for _, d := range p.Delete {
				if d.RunID == id {
					return true
				}
			}
			return false
		}
		for _, e := range Expiries(pol, now, copies, successes(future...)) {
			if e.At.After(now) && deleted(planAt(e.At.Add(-time.Second)), e.RunID) {
				t.Fatalf("iter %d: %+v deleted before its expiry (policy %+v)", iter, e, pol)
			}
			if !deleted(planAt(e.At), e.RunID) {
				t.Fatalf("iter %d: %+v kept at its expiry (policy %+v)", iter, e, pol)
			}
		}
	}
}
