//go:build integration

package pubsub_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pubsub"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const listenerApp = "bekup-listen-test"

var errRollback = errors.New("roll back")

func startListener(t *testing.T, d dbtest.DB, bus *pubsub.Bus) *pubsub.Listener {
	t.Helper()
	connect, err := db.NewConnector(config.DBCredentials{URL: []byte(d.AppURL)}, db.PoolOptions{
		ApplicationName: listenerApp, TLS: dbtest.TLS(), Registry: logging.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := pubsub.NewListener(pubsub.ListenerOptions{
		Connect: connect, Channels: []pubsub.Channel{pubsub.SessionEvents}, Bus: bus,
		MinBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := l.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, 10*time.Second, l.Connected)
	return l
}

func waitFor(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func expect(t *testing.T, s *pubsub.Subscription, payload string) {
	t.Helper()
	select {
	case m := <-s.C():
		if m.Channel != pubsub.SessionEvents.Name || m.Payload != payload {
			t.Fatalf("got %+v, want payload %q", m, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("notification %q not received", payload)
	}
}

func expectNone(t *testing.T, s *pubsub.Subscription, within time.Duration) {
	t.Helper()
	select {
	case m := <-s.C():
		t.Fatalf("unexpected notification %+v", m)
	case <-time.After(within):
	}
}

func drainResync(s *pubsub.Subscription) {
	select {
	case <-s.Resync():
	default:
	}
}

func TestListenerDeliversOnCommitOnlyAndReconnects(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.SessionEvents.Name, 16)
	defer sub.Close()
	l := startListener(t, d, bus)
	drainResync(sub)
	// NOTIFY only (no local bus): what other instances see.
	remote := &pubsub.Publisher{}

	ev := auth.SessionEvent{Kind: auth.SessionRevoked, UserID: uuid.New(), SessionID: uuid.New()}
	if err := db.Tx(t.Context(), pool, func(q *sqlc.Queries) error {
		if err := remote.PublishTx(t.Context(), q, pubsub.SessionEvents.Name, ev.Encode()); err != nil {
			return err
		}
		// Not before the commit.
		expectNone(t, sub, 200*time.Millisecond)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, ev.Encode())
	got, err := auth.DecodeSessionEvent(ev.Encode())
	if err != nil || got != ev {
		t.Fatalf("payload is not just the identifiers: %+v %v", got, err)
	}

	// A rolled-back transaction sends nothing.
	rolledBack := auth.SessionEvent{Kind: auth.SessionsRevokedAll, UserID: uuid.New()}
	if err := db.Tx(t.Context(), pool, func(q *sqlc.Queries) error {
		if err := remote.PublishTx(t.Context(), q, pubsub.SessionEvents.Name, rolledBack.Encode()); err != nil {
			return err
		}
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatalf("transaction: %v", err)
	}
	expectNone(t, sub, 300*time.Millisecond)

	// Payloads other than identifiers are refused before reaching Postgres.
	if err := db.Tx(t.Context(), pool, func(q *sqlc.Queries) error {
		return remote.PublishTx(t.Context(), q, pubsub.SessionEvents.Name, "user ada@example.com")
	}); err == nil {
		t.Fatal("non-identifier payload accepted")
	}

	// Kill the LISTEN connection: the listener reconnects, signals a
	// resync (notifications in between are lost) and delivers again.
	if err := d.SuperExec(t.Context(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = '"+listenerApp+"'"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return l.Connects() >= 2 && l.Connected() })
	select {
	case <-sub.Resync():
	case <-time.After(5 * time.Second):
		t.Fatal("no resync signal after reconnect")
	}
	again := auth.SessionEvent{Kind: auth.SessionRotated, UserID: uuid.New(), SessionID: uuid.New()}
	if err := db.Tx(t.Context(), pool, func(q *sqlc.Queries) error {
		return remote.PublishTx(t.Context(), q, pubsub.SessionEvents.Name, again.Encode())
	}); err != nil {
		t.Fatal(err)
	}
	expect(t, sub, again.Encode())
}

func TestPublisherLocalBusIsImmediate(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	bus := pubsub.NewBus()
	sub := bus.Subscribe(pubsub.SessionEvents.Name, 16)
	defer sub.Close()
	local := &pubsub.Publisher{Bus: bus}
	ev := auth.SessionEvent{Kind: auth.SessionsRevokedAll, UserID: uuid.New()}
	if err := db.Tx(t.Context(), pool, func(q *sqlc.Queries) error {
		if err := local.PublishTx(t.Context(), q, pubsub.SessionEvents.Name, ev.Encode()); err != nil {
			return err
		}
		expect(t, sub, ev.Encode()) // in-process: before the commit
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
