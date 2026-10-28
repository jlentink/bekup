package resolvers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func TestRestoreErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code apierror.Code
	}{
		{serviceutil.FieldErr("targetDatabase", "bad"), apierror.Validation},
		{fmt.Errorf("%w: %w", restore.ErrEngineMismatch, serviceutil.FieldErr("targetConnectionId", "x")), apierror.Validation},
		{&restore.VerificationError{Code: restore.VerifyManifestSignature}, apierror.Conflict},
		{restore.ErrNotFound, apierror.NotFound},
		{restore.ErrUnavailable, apierror.Conflict},
		{restore.ErrFinished, apierror.Conflict},
		{restore.ErrTargetBusy, apierror.Conflict},
		{restore.ErrTargetChanged, apierror.Conflict},
		{apierror.New(apierror.Forbidden, "not allowed"), apierror.Forbidden},
		{apierror.New(apierror.StepUpRequired, "x"), apierror.StepUpRequired},
	}
	for _, c := range cases {
		if got := restoreError(c.err); !apierror.IsCode(got, c.code) {
			t.Errorf("%v: got %v, want %s", c.err, got, c.code)
		}
	}
	if restoreError(nil) != nil {
		t.Fatal("nil")
	}
	// Anything else stays an internal (masked) error.
	if got := restoreError(errors.New("boom")); apierror.IsCode(got, apierror.Validation) {
		t.Fatalf("internal error mapped to %v", got)
	}
}

// TestRestoreEventPump: one event at the start, coalesced events of the
// restore only, at most one per gap, and the pump stops when done closes.
func TestRestoreEventPump(t *testing.T) {
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.RestoreEvents.Name, 1024)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := make(chan *restoreEvent)
	done := make(chan struct{})
	id := uuid.New()
	const gap = 30 * time.Millisecond
	go pumpRestoreEvents(ctx, sub, src, done, id, gap)

	recv := func(d time.Duration) bool {
		select {
		case _, ok := <-src:
			return ok
		case <-time.After(d):
			return false
		}
	}
	if !recv(time.Second) {
		t.Fatal("no initial event")
	}
	for range 100 {
		bus.Publish(pubsub.RestoreEvents.Name, restore.Payload(restore.EventLog, id))
		bus.Publish(pubsub.RestoreEvents.Name, restore.Payload(restore.EventRestore, uuid.New())) // another restore
	}
	time.Sleep(3 * gap)
	if !recv(time.Second) {
		t.Fatal("no event after changes")
	}
	if recv(5 * gap) {
		t.Fatal("events were not coalesced")
	}
	bus.Publish(pubsub.RestoreEvents.Name, "garbage")
	if recv(5 * gap) {
		t.Fatal("an undecodable payload produced an event")
	}
	close(done)
	select {
	case _, ok := <-src:
		if ok {
			t.Fatal("event after done")
		}
	case <-time.After(time.Second):
		t.Fatal("the pump did not stop")
	}
}

func TestRestoreUpdatedNeedsStream(t *testing.T) {
	r := &Resolver{}
	if _, err := r.restoreUpdated(context.Background(), uuid.New()); !errors.Is(err, errNotStream) {
		t.Fatalf("outside a stream: %v", err)
	}
	if _, err := r.restoreTargets(context.Background(), uuid.New()); !errors.Is(err, errRestoresNotConfigured) {
		t.Fatalf("without the API: %v", err)
	}
}
