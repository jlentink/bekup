package retention

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// partialAt places only item 0 (of 2) and the manifest: incomplete.
func partialAt(dest uuid.UUID) placement {
	return placement{dest: dest, items: []int{0}, manifest: true}
}

func TestPlanHold(t *testing.T) {
	t.Run("one failed backup inside the policy is no hold", func(t *testing.T) {
		f := newFixture()
		f.run("c1", 2*day, 2, full(destA, 2))
		f.run("p1", day, 2, partialAt(destA))
		if h := f.plan(Policy{KeepLast: 7})[destA].Hold; h != nil {
			t.Fatalf("hold = %+v", h)
		}
	})
	t.Run("newer incomplete runs beyond keep-last", func(t *testing.T) {
		f := newFixture()
		c := f.run("c1", 10*day, 2, full(destA, 2))
		var ps []uuid.UUID
		for i := 9; i >= 1; i-- {
			ps = append(ps, f.run("p", time.Duration(i)*day, 2, partialAt(destA)))
		}
		p := f.plan(Policy{KeepLast: 3})[destA]
		h := p.Hold
		if h == nil || h.Reason != KeepNewerIncomplete {
			t.Fatalf("hold = %+v", h)
		}
		// Ranks 0..2 (the newest three) are inside keep-last; the six
		// older incomplete runs are held only by the rule.
		if !slices.Equal(h.Runs, ps[:6]) || !h.Since.Equal(now.Add(-9*day)) || !h.NewestComplete.Equal(now.Add(-10*day)) {
			t.Fatalf("hold = %+v", h)
		}
		if p.NewestComplete != c || len(p.Delete) != 0 {
			t.Fatalf("plan changed: %+v", p)
		}
	})
	t.Run("keep-days covers recent incomplete runs", func(t *testing.T) {
		f := newFixture()
		f.run("c1", 10*day, 2, full(destA, 2))
		old := f.run("p-old", 8*day, 2, partialAt(destA))
		f.run("p-new", 2*day, 2, partialAt(destA))
		h := f.plan(Policy{KeepDays: 5})[destA].Hold
		if h == nil || !slices.Equal(h.Runs, []uuid.UUID{old}) {
			t.Fatalf("hold = %+v", h)
		}
	})
	t.Run("no complete run", func(t *testing.T) {
		f := newFixture()
		a := f.run("p1", 3*day, 2, partialAt(destA))
		b := f.run("p2", 2*day, 2, partialAt(destA))
		f.run("c-elsewhere", day, 2, full(destB, 2))
		plans := f.plan(Policy{KeepLast: 1})
		h := plans[destA].Hold
		if h == nil || h.Reason != KeepNoComplete || !h.NewestComplete.IsZero() || !slices.Equal(h.Runs, []uuid.UUID{a}) {
			t.Fatalf("hold = %+v (b=%v)", h, b)
		}
		if plans[destB].Hold != nil {
			t.Fatalf("destination B holds: %+v", plans[destB].Hold)
		}
	})
	t.Run("locked runs are not holds", func(t *testing.T) {
		f := newFixture()
		f.run("c1", 5*day, 2, full(destA, 2))
		lp := partialAt(destA)
		lp.lock = now.Add(day)
		f.run("p-locked", 4*day, 2, lp)
		p1 := f.run("p1", 3*day, 2, partialAt(destA))
		f.run("p2", 2*day, 2, partialAt(destA)) // rank 0: inside keep-last
		h := f.plan(Policy{KeepLast: 1})[destA].Hold
		if h == nil || !slices.Equal(h.Runs, []uuid.UUID{p1}) {
			t.Fatalf("hold = %+v", h)
		}
	})
	t.Run("inactive policy holds nothing", func(t *testing.T) {
		f := newFixture()
		f.run("p1", 3*day, 2, partialAt(destA))
		if h := f.plan(Policy{})[destA].Hold; h != nil {
			t.Fatalf("hold = %+v", h)
		}
	})
}

func TestHoldReasonAndDedupKey(t *testing.T) {
	if holdReason(KeepNoComplete) != HoldNoCompleteRun || holdReason(KeepNewerIncomplete) != HoldNewerThanNewestComplete {
		t.Fatal("hold reasons")
	}
	job, dest := uuid.New(), uuid.New()
	k1 := HoldDedupKey(job, dest, time.Date(2026, 10, 1, 23, 59, 0, 0, time.UTC))
	k2 := HoldDedupKey(job, dest, time.Date(2026, 10, 1, 0, 1, 0, 0, time.UTC))
	k3 := HoldDedupKey(job, dest, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if k1 != k2 || k1 == k3 || !strings.HasPrefix(k1, "retention.blocked:") || !strings.HasSuffix(k1, ":2026-10-01") {
		t.Fatalf("dedup keys %q %q %q", k1, k2, k3)
	}
	if HoldDedupKey(job, uuid.New(), now) == HoldDedupKey(job, dest, now) {
		t.Fatal("dedup key must differ per destination")
	}
}

// every returns a NextFunc with a fixed interval.
func every(d time.Duration) NextFunc {
	return func(t time.Time) (time.Time, error) { return t.Add(d), nil }
}

func manual(time.Time) (time.Time, error) { return time.Time{}, ErrNoOccurrence }

func TestEstimateKept(t *testing.T) {
	for _, c := range []struct {
		name   string
		p      Policy
		next   NextFunc
		want   Estimate
		wantOK bool
	}{
		{"daily keep last 7", Policy{KeepLast: 7}, every(day), Estimate{Kept: 7, ByKeepLast: 7, Known: true}, true},
		{"daily keep 30 days", Policy{KeepDays: 30}, every(day), Estimate{Kept: 30, ByKeepDays: 30, Known: true}, true},
		{"daily 7 + 30 days add up", Policy{KeepLast: 7, KeepDays: 30}, every(day),
			Estimate{Kept: 30, ByKeepLast: 7, ByKeepDays: 30, Known: true}, true},
		{"weekly keep last 4 + 7 days", Policy{KeepLast: 4, KeepDays: 7}, every(7 * day),
			Estimate{Kept: 4, ByKeepLast: 4, ByKeepDays: 1, Known: true}, true},
		{"hourly 2 days", Policy{KeepDays: 2}, every(time.Hour), Estimate{Kept: 48, ByKeepDays: 48, Known: true}, true},
		{"every 5 minutes for 3650 days is a lower bound", Policy{KeepDays: 3650}, every(5 * time.Minute),
			Estimate{Kept: MaxEstimateRuns, ByKeepDays: MaxEstimateRuns, Known: true, AtLeast: true}, true},
		{"monthly 1 day keeps the newest", Policy{KeepDays: 1}, every(30 * day), Estimate{Kept: 1, ByKeepDays: 1, Known: true}, true},
		{"manual keep last", Policy{KeepLast: 5}, manual, Estimate{Kept: 5, ByKeepLast: 5, Known: true}, true},
		{"manual with days is unknown", Policy{KeepLast: 5, KeepDays: 10}, manual, Estimate{ByKeepLast: 5}, true},
		{"inactive policy", Policy{}, every(day), Estimate{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := EstimateKept(c.p, now, c.next)
			if (err == nil) != c.wantOK {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Fatalf("estimate = %+v, want %+v", got, c.want)
			}
		})
	}
	boom := errors.New("boom")
	if _, err := EstimateKept(Policy{KeepLast: 3}, now, func(time.Time) (time.Time, error) { return time.Time{}, boom }); !errors.Is(err, boom) {
		t.Fatalf("schedule error: %v", err)
	}
}

// TestEstimateMatchesPlan ties the estimate to Plan: with complete runs at
// the projected times, Plan keeps exactly Estimate.Kept runs.
func TestEstimateMatchesPlan(t *testing.T) {
	for _, c := range []struct {
		p    Policy
		step time.Duration
	}{
		{Policy{KeepLast: 7}, day}, {Policy{KeepDays: 30}, day}, {Policy{KeepLast: 7, KeepDays: 30}, day},
		{Policy{KeepLast: 40, KeepDays: 3}, 6 * time.Hour}, {Policy{KeepLast: 2, KeepDays: 14}, 7 * day},
		{Policy{KeepDays: 1}, 90 * time.Minute},
	} {
		e, err := EstimateKept(c.p, now, every(c.step))
		if err != nil || !e.Known {
			t.Fatalf("%+v: %+v %v", c.p, e, err)
		}
		// Enough complete runs ending at "now" (the newest projected).
		f := newFixture()
		n := e.Kept + c.p.KeepLast + 10
		for i := n - 1; i >= 0; i-- {
			f.run("r", time.Duration(i)*c.step, 1, full(destA, 1))
		}
		p := f.plan(c.p)[destA]
		if len(p.Keep) != e.Kept {
			t.Errorf("%+v every %s: plan keeps %d, estimate %d", c.p, c.step, len(p.Keep), e.Kept)
		}
	}
}
