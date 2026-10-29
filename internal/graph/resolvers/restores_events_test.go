package resolvers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
)

func restorePayloadID(p string) (uuid.UUID, bool) {
	kind, id, err := restore.ParsePayload(p)
	return id, err == nil && kind == restore.EventRestore
}

// TestRestoreEventsCoalesced covers the restoresUpdated pump (D-196): log
// events and malformed payloads are ignored, events of one restore are
// merged, and a lost-message signal re-sends what the resync returns.
func TestRestoreEventsCoalesced(t *testing.T) {
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.RestoreEvents.Name, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := make(chan uuid.UUID)
	var mu sync.Mutex
	resyncCalls := 0
	resynced := uuid.New()
	resync := func(context.Context, time.Time, int) ([]uuid.UUID, error) {
		mu.Lock()
		defer mu.Unlock()
		resyncCalls++
		return []uuid.UUID{resynced}, nil
	}
	const gap = 10 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		pumpCoalescedIDs(ctx, sub, out, func(id uuid.UUID) uuid.UUID { return id }, restorePayloadID, resync, gap)
	}()

	a := uuid.New()
	bus.Publish(pubsub.RestoreEvents.Name, restore.Payload(restore.EventLog, uuid.New()))
	bus.Publish(pubsub.RestoreEvents.Name, "restore:not-a-uuid")
	for range 200 {
		bus.Publish(pubsub.RestoreEvents.Name, restore.Payload(restore.EventRestore, a))
	}
	got := map[uuid.UUID]int{}
	collect := func() {
		for {
			select {
			case id := <-out:
				got[id]++
			case <-time.After(20 * gap):
				return
			}
		}
	}
	collect()
	if len(got) != 1 || got[a] < 1 || got[a] > 3 {
		t.Fatalf("deliveries %v for 200 events of one restore", got)
	}

	// Possibly lost messages: the resync's restores are sent.
	bus.Resync(pubsub.RestoreEvents.Name)
	collect()
	mu.Lock()
	calls := resyncCalls
	mu.Unlock()
	if calls != 1 || got[resynced] != 1 {
		t.Fatalf("after resync: calls %d, deliveries %v", calls, got)
	}
	cancel()
	<-done
	if _, ok := <-out; ok {
		t.Fatal("the pump did not close its channel")
	}
}
