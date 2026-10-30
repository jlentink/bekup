//go:build integration

package graph_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/runstate"
)

// startBusListener bridges LISTEN/NOTIFY into bus on the stream channels,
// as serve does (internal/app newSubscriptions). Publisher.PublishTx
// publishes on the local bus before the transaction commits; only the
// NOTIFY delivered after the commit guarantees that a stream re-loads the
// committed state. A stream test without the listener races the commit
// (a delivery may read the old row and nothing announces the new one).
func startBusListener(t *testing.T, d dbtest.DB, bus *pubsub.Bus) {
	t.Helper()
	connect, err := db.NewConnector([]byte(d.AppURL), db.PoolOptions{
		ApplicationName: "bekup-graph-test-listener", TLS: dbtest.TLS(), Registry: logging.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := pubsub.NewListener(pubsub.ListenerOptions{
		Connect: connect,
		Channels: []pubsub.Channel{
			pubsub.SessionEvents, pubsub.RunEvents, pubsub.RestoreEvents, pubsub.NotificationEvents, pubsub.SettingsEvents,
		},
		Bus:        bus,
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
	deadline := time.Now().Add(10 * time.Second)
	for !l.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("pubsub listener did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// slowCommitDB delays every commit: the local bus hint of a run change is
// then always delivered (and re-loaded) before the change is visible.
type slowCommitDB struct {
	sqlc.DBTX
	begin interface {
		Begin(context.Context) (pgx.Tx, error)
	}
	delay time.Duration
}

func (s *slowCommitDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.begin.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &slowCommitTx{Tx: tx, delay: s.delay}, nil
}

type slowCommitTx struct {
	pgx.Tx
	delay time.Duration
}

func (t *slowCommitTx) Commit(ctx context.Context) error {
	time.Sleep(t.delay)
	return t.Tx.Commit(ctx)
}

// TestRunStreamsSeeCommittedState: a stream that loaded a run on the
// early local hint (before the commit) still delivers the committed final
// state, because the NOTIFY after the commit announces the run again
// (the CI flake of TestRunEventStreams: its harness had no listener).
func TestRunStreamsSeeCommittedState(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	adminC := e.mustLogin("admin@example.com")
	ref := e.newRun(e.jobA, nil)

	updates := e.openStream(adminC, runUpdatedSub, map[string]any{"run": ref.RunID})
	logs := e.openStream(adminC, runLogSub, map[string]any{"run": ref.RunID})
	waitSubscribers(t, e, 2)

	e.store = runstate.NewStore(&slowCommitDB{DBTX: e.pool, begin: e.pool, delay: 300 * time.Millisecond},
		&pubsub.Publisher{Bus: e.events}, nil)
	e.logs(ref, "starting")
	e.completeRun(ref)

	var got runEvGQL
	for got.Status != "SUCCESS" {
		nextData(t, updates, "runUpdated", &got)
	}
	var batch struct{ Done bool }
	for !batch.Done {
		nextData(t, logs, "runLogAppended", &batch)
	}
}
