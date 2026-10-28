package resolvers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/runstate"
)

type fakeRunIDs struct {
	mu    sync.Mutex
	calls int
	ids   []uuid.UUID
}

func (f *fakeRunIDs) RunIDsUpdatedSince(context.Context, time.Time, *uuid.UUID, *uuid.UUID, int) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.ids, nil
}

// TestRunEventsCoalesced: a burst of events for few runs is delivered once
// per run (pending runs are merged), deliveries are spaced by the gap, and
// an overflow of distinct runs falls back to one resync instead of an
// unbounded queue.
func TestRunEventsCoalesced(t *testing.T) {
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.RunEvents.Name, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := make(chan *runEvent)
	ids := &fakeRunIDs{}
	const gap = 20 * time.Millisecond
	go pumpCoalescedRunEvents(ctx, sub, src, ids, nil, nil, gap)

	job := uuid.New()
	a, b := uuid.New(), uuid.New()
	for range 500 {
		bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventRun, runstate.Ref{RunID: a, JobID: job}))
		bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventRun, runstate.Ref{RunID: b, JobID: job}))
	}
	// Give the pump time to take in the burst before anything is read.
	time.Sleep(100 * time.Millisecond)
	got := map[uuid.UUID]int{}
	var times []time.Time
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case ev := <-src:
			got[ev.runID]++
			times = append(times, time.Now())
		case <-time.After(10 * gap):
			break loop
		case <-timeout:
			break loop
		}
	}
	// The first event of each run may be delivered while the burst still
	// arrives; after that every run is pending at most once.
	if got[a] < 1 || got[a] > 3 || got[b] < 1 || got[b] > 3 || len(got) != 2 {
		t.Fatalf("deliveries %v for 1000 events", got)
	}
	for i := 1; i < len(times); i++ {
		if d := times[i].Sub(times[i-1]); d < gap-5*time.Millisecond {
			t.Fatalf("deliveries %v apart (gap %v)", d, gap)
		}
	}

	// More distinct runs than resyncMax: the overflow becomes a resync.
	extra := uuid.New()
	ids.mu.Lock()
	ids.ids = []uuid.UUID{extra}
	ids.mu.Unlock()
	for range resyncMax + 50 {
		bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventRun, runstate.Ref{RunID: uuid.New(), JobID: job}))
	}
	time.Sleep(100 * time.Millisecond)
	n, sawExtra := 0, false
	deadline := time.After(10 * time.Second)
	for n <= resyncMax && !sawExtra {
		select {
		case ev := <-src:
			n++
			sawExtra = sawExtra || ev.runID == extra
		case <-deadline:
			t.Fatalf("only %d deliveries", n)
		}
	}
	ids.mu.Lock()
	calls := ids.calls
	ids.mu.Unlock()
	if !sawExtra || calls != 1 || n != resyncMax+1 {
		t.Fatalf("delivered %d, resync calls %d, saw resync result %v", n, calls, sawExtra)
	}
}

// TestRunEventsFilter: events of other runs or jobs are dropped before
// they cost anything.
func TestRunEventsFilter(t *testing.T) {
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.RunEvents.Name, 64)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := make(chan *runEvent)
	job, other := uuid.New(), uuid.New()
	go pumpCoalescedRunEvents(ctx, sub, src, &fakeRunIDs{}, &job, nil, time.Millisecond)
	mine := uuid.New()
	bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventRun, runstate.Ref{RunID: uuid.New(), JobID: other}))
	bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventLog, runstate.Ref{RunID: mine, JobID: job}))
	bus.Publish(pubsub.RunEvents.Name, runstate.Payload(runstate.EventRun, runstate.Ref{RunID: mine, JobID: job}))
	select {
	case ev := <-src:
		if ev.runID != mine {
			t.Fatalf("delivered %s", ev.runID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing delivered")
	}
	select {
	case ev := <-src:
		t.Fatalf("extra delivery %s", ev.runID)
	case <-time.After(50 * time.Millisecond):
	}
}
