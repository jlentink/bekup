package retention

import (
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
