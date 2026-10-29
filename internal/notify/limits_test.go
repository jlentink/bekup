package notify

import (
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/notify/webhook"
)

func TestPrepareLimits(t *testing.T) {
	s := &Service{}
	initial := Limits{CollapseWindow: time.Minute, MaxPerUserPerHour: 30, FailurePolicy: webhook.DefaultFailurePolicy()}
	s.limits.Store(&initial)
	bad := []Limits{
		{CollapseWindow: 0, MaxPerUserPerHour: 1, FailurePolicy: webhook.DefaultFailurePolicy()},
		{CollapseWindow: time.Second, MaxPerUserPerHour: 0, FailurePolicy: webhook.DefaultFailurePolicy()},
		{CollapseWindow: time.Second, MaxPerUserPerHour: 1, FailurePolicy: webhook.FailurePolicy{}},
	}
	for _, l := range bad {
		if _, err := s.PrepareLimits(l); err == nil {
			t.Errorf("PrepareLimits(%+v) accepted", l)
		}
	}
	next := initial
	next.MaxPerUserPerHour = 5
	next.FailurePolicy.MaxPermanent = 2
	commit, err := s.PrepareLimits(next)
	if err != nil {
		t.Fatal(err)
	}
	if s.CurrentLimits() != initial {
		t.Fatal("Prepare applied before the commit")
	}
	commit()
	if s.CurrentLimits() != next {
		t.Fatalf("CurrentLimits = %+v", s.CurrentLimits())
	}
}

func TestSetLimitsConcurrent(t *testing.T) {
	s := &Service{}
	s.limits.Store(&Limits{CollapseWindow: time.Minute, MaxPerUserPerHour: 30, FailurePolicy: webhook.DefaultFailurePolicy()})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			_ = s.CurrentLimits().MaxPerUserPerHour
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 1000 {
			if err := s.SetLimits(Limits{CollapseWindow: time.Second, MaxPerUserPerHour: 1 + i, FailurePolicy: webhook.DefaultFailurePolicy()}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}
