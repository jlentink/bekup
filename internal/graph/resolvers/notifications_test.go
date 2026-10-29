package resolvers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
)

// fakeInbox serves rows of several users; a row becomes visible only
// after visible() (a commit that lags behind the announcement).
type fakeInbox struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]sqlc.Notification
	visible map[uuid.UUID]bool
	since   int
}

func (f *fakeInbox) add(user uuid.UUID, visible bool) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.rows[id] = sqlc.Notification{ID: id, UserID: user, Type: "backup.failed", Severity: "error", Title: "t", CreatedAt: time.Now()}
	f.visible[id] = visible
	return id
}

func (f *fakeInbox) show(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.visible[id] = true
}

func (f *fakeInbox) Notification(_ context.Context, user, id uuid.UUID) (sqlc.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.rows[id]
	if !ok || !f.visible[id] || n.UserID != user {
		return sqlc.Notification{}, notify.ErrNotFound
	}
	return n, nil
}

func (f *fakeInbox) NotificationsSince(_ context.Context, user uuid.UUID, _ time.Time, _ int) ([]sqlc.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since++
	var out []sqlc.Notification
	for id := range f.rows {
		if f.rows[id].UserID == user && f.visible[id] {
			out = append(out, f.rows[id])
		}
	}
	return out, nil
}

func next(t *testing.T, ch <-chan *inboxEvent) uuid.UUID {
	t.Helper()
	select {
	case ev := <-ch:
		return ev.row.ID
	case <-time.After(5 * time.Second):
		t.Fatal("no inbox event")
		return uuid.Nil
	}
}

func TestPumpInboxEvents(t *testing.T) {
	bus := pubsub.NewBus()
	inbox := &fakeInbox{rows: map[uuid.UUID]sqlc.Notification{}, visible: map[uuid.UUID]bool{}}
	me, other := uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub := bus.Subscribe(pubsub.NotificationEvents.Name, 16)
	src := make(chan *inboxEvent)
	done := make(chan struct{})
	go func() { pumpInboxEvents(ctx, sub, src, inbox, me, 10*time.Millisecond); close(done) }()

	// Another user's row (announced with their id, or forged with mine)
	// never comes through; my row does, once, even when announced twice.
	theirs := inbox.add(other, true)
	bus.Publish(pubsub.NotificationEvents.Name, notify.InboxPayload(theirs, other))
	bus.Publish(pubsub.NotificationEvents.Name, notify.InboxPayload(theirs, me))
	bus.Publish(pubsub.NotificationEvents.Name, "garbage")
	mine := inbox.add(me, true)
	bus.Publish(pubsub.NotificationEvents.Name, notify.InboxPayload(mine, me))
	bus.Publish(pubsub.NotificationEvents.Name, notify.InboxPayload(mine, me))
	if got := next(t, src); got != mine {
		t.Fatalf("got %s want %s", got, mine)
	}

	// Announced before its commit: retried until visible.
	late := inbox.add(me, false)
	bus.Publish(pubsub.NotificationEvents.Name, notify.InboxPayload(late, me))
	time.Sleep(30 * time.Millisecond)
	inbox.show(late)
	if got := next(t, src); got != late {
		t.Fatalf("late: got %s", got)
	}

	// A resync re-sends rows not seen yet, never duplicates.
	missed := inbox.add(me, true)
	bus.Resync(pubsub.NotificationEvents.Name)
	if got := next(t, src); got != missed {
		t.Fatalf("resync: got %s", got)
	}
	select {
	case ev := <-src:
		t.Fatalf("duplicate %s", ev.row.ID)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestSeenSetBounded(t *testing.T) {
	s := newSeenSet(2)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	s.add(a)
	s.add(b)
	s.add(b)
	s.add(c)
	if s.has(a) || !s.has(b) || !s.has(c) || len(s.order) != 2 {
		t.Fatalf("%v", s.order)
	}
}

func TestNotificationModel(t *testing.T) {
	n := sqlc.Notification{ID: uuid.New(), Type: "backup.failed", Severity: "error", Payload: []byte(`{"event":"backup.failed"}`)}
	m := notificationModel(&n)
	if m.Group == nil || *m.Group != model.NotificationGroupBackup || m.Severity != model.NotificationSeverityError || len(m.Data) == 0 {
		t.Fatalf("%+v", m)
	}
	notice := sqlc.Notification{ID: uuid.New(), Type: notify.NoticeWebhookDisabled, Severity: "warning", Payload: []byte(`{}`)}
	if m := notificationModel(&notice); m.Group != nil || m.Data != nil {
		t.Fatalf("notice %+v", m)
	}
}
