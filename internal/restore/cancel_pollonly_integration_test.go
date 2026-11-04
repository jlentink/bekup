//go:build integration

package restore_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
)

// TestRunningRestoreCancelPollOnly (D-220): the River clients are
// poll-only, so a running restore is canceled through the database:
// JobCancelTx from another client (serve's restore.API.Cancel uses
// queue.Client.CancelTx exactly like this for a running restore) reaches
// the worker client's job, while forged river_control notifications of
// bekup_external (cancel, pause) change nothing.
func TestRunningRestoreCancelPollOnly(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.Received["app"] = []byte("old")
	w.runner.Block = true
	j, s := w.pgArtifact(randomDump(600 << 10))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))

	logger := slog.New(slog.DiscardHandler)
	settings := queue.SettingsFromConfig(config.DefaultBekup().Workers)
	ws, err := queue.NewWorkers(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := restore.Register(ws, w.worker); err != nil {
		t.Fatal(err)
	}
	// The worker process and, separately, the serve process.
	worker, err := queue.New(w.pool, queue.Options{Settings: settings, Logger: logger, Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	serve, err := queue.New(w.pool, queue.Options{Settings: settings, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TxWith(t.Context(), w.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		return w.svc.EnqueueTx(t.Context(), tx, q, serve, id)
	}); err != nil {
		t.Fatal(err)
	}
	r, err := w.svc.Get(t.Context(), id)
	if err != nil || r.RiverJobID == nil {
		t.Fatalf("restore %+v %v", r, err)
	}
	riverID := *r.RiverJobID

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- worker.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-w.runner.Started:
	case <-time.After(30 * time.Second):
		t.Fatal("restore did not start")
	}

	// Forged control notifications as bekup_external, on River's topic
	// with and without its schema prefix.
	ext := w.d.ExternalPool(t)
	for _, ch := range []string{"public.river_control", "river_control"} {
		for _, p := range []string{
			fmt.Sprintf(`{"action":"cancel","job_id":%d,"queue":"restore"}`, riverID),
			`{"action":"pause","queue":"restore"}`, `{"action":"pause","queue":"*"}`,
		} {
			if _, err := ext.Exec(t.Context(), "SELECT pg_notify($1, $2)", ch, p); err != nil {
				t.Fatalf("NOTIFY as bekup_external: %v", err)
			}
		}
	}
	// River polls cancel requests every 2 s.
	time.Sleep(5 * time.Second)
	if st, _ := w.status(id); st != "restoring" {
		t.Fatalf("restore after forged notifications: %s", st)
	}

	// The real cancel from the other client.
	if err := db.TxWith(t.Context(), w.pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
		st, err := serve.CancelTx(t.Context(), tx, riverID)
		if err == nil && st != rivertype.JobStateRunning {
			err = fmt.Errorf("River state %s", st)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, code := w.status(id)
		if st == "canceled" && code == restore.CodeCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restore not canceled: %s %s", st, code)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(w.runner.Data("app")) != "old" {
		t.Fatal("the overwritten target changed")
	}
}
