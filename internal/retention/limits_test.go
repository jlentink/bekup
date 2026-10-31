package retention

import (
	"context"
	"testing"
	"time"
)

func TestPrepareLimits(t *testing.T) {
	w := &Worker{}
	initial := Limits{MaxDeletions: 500, PartialCleanupAge: time.Hour}
	w.limits.Store(&initial)
	for _, l := range []Limits{{MaxDeletions: 0, PartialCleanupAge: time.Hour}, {MaxDeletions: 1}} {
		if _, err := w.PrepareLimits(l); err == nil {
			t.Errorf("PrepareLimits(%+v) accepted", l)
		}
	}
	next := Limits{DryRun: true, MaxDeletions: 3, PartialCleanupAge: 2 * time.Hour}
	commit, err := w.PrepareLimits(next)
	if err != nil {
		t.Fatal(err)
	}
	if w.CurrentLimits() != initial {
		t.Fatal("Prepare applied before the commit")
	}
	commit()
	if w.CurrentLimits() != next {
		t.Fatalf("CurrentLimits = %+v", w.CurrentLimits())
	}
}

// Each destination of the daily maintenance gets an equal share of the
// remaining time (D-209): a hanging one cannot starve the rest.
func TestDestinationBudget(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), 40*time.Minute)
	defer cancel()
	ctx, c := destinationBudget(parent, 4)
	defer c()
	dl, ok := ctx.Deadline()
	if left := time.Until(dl); !ok || left > 10*time.Minute+time.Second || left < 9*time.Minute {
		t.Fatalf("share of 40 min among 4: %s", left)
	}
	ctx2, c2 := destinationBudget(t.Context(), 3)
	defer c2()
	if dl, ok := ctx2.Deadline(); !ok || time.Until(dl) > maxDestinationBudget {
		t.Fatalf("no parent deadline: %v %v", dl, ok)
	}
}
