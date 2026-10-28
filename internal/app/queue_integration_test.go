//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/db/dbtest"
)

// addDueJob inserts an enabled hourly job whose next run is overdue.
func addDueJob(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	due := time.Now().Add(-time.Minute)
	id, err := dbtest.InsertJob(t.Context(), pool, dbtest.JobRow{
		Enabled: true, NextRunAt: &due,
		Schedule: `{"kind":"interval","every":1,"unit":"hours","anchor":"2026-01-01T00:00","timezone":"UTC"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func runApp(t *testing.T, o app.Options) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	a, err := app.New(ctx, o)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			return errors.New("Run did not return")
		}
	}
}

func riverRuns(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM river_job WHERE kind = 'backup_run' AND args->>'job_id' = $1::text", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServeRunsSchedulerAndWorkers(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	id := addDueJob(t, pool)
	logs := &syncBuffer{}
	o := options(t, d.AppURL, logs)
	o.QueueFetchPollInterval = 100 * time.Millisecond
	stop := runApp(t, o)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	waitFor(t, "the scheduled run", func() bool { return riverRuns(t, pool, id) == 1 })
	waitFor(t, "the workers", func() bool { return strings.Contains(logs.String(), "river workers started") })
	// The periodic housekeeping job was enqueued by the leader and worked.
	waitFor(t, "the skip cleanup job", func() bool {
		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM river_job WHERE kind = 'schedule_skip_cleanup' AND state = 'completed'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n >= 1
	})
	// The backup worker (task 3.6) works the run: the test job has no
	// encryption configuration, so the run fails closed and is recorded.
	var status, code string
	waitFor(t, "the backup run", func() bool {
		err := pool.QueryRow(t.Context(), "SELECT status::text, coalesce(error_code, '') FROM runs WHERE job_id = $1", id).Scan(&status, &code)
		return err == nil && status != "queued" && status != "running"
	})
	if status != "failed" || code != "encryption_unavailable" {
		t.Fatalf("backup run %s (%s)", status, code)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"scheduler started", "river workers stopped", "bekup serve stopped"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing log %q", want)
		}
	}
}

func TestServeNoWorkerStillSchedules(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	id := addDueJob(t, pool)
	logs := &syncBuffer{}
	o := options(t, d.AppURL, logs)
	o.NoWorker = true
	stop := runApp(t, o)
	waitFor(t, "the scheduled run", func() bool { return riverRuns(t, pool, id) == 1 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "river workers started") {
		t.Fatal("--no-worker started workers")
	}
}

func TestServeSchedulerDisabled(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	id := addDueJob(t, pool)
	logs := &syncBuffer{}
	o := options(t, d.AppURL, logs)
	o.Config.Scheduler.Enabled = false
	stop := runApp(t, o)
	waitFor(t, "the workers", func() bool { return strings.Contains(logs.String(), "river workers started") })
	time.Sleep(300 * time.Millisecond)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if riverRuns(t, pool, id) != 0 || strings.Contains(logs.String(), "scheduler started") {
		t.Fatal("disabled scheduler enqueued")
	}
}

func TestWorkerMode(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	id := addDueJob(t, pool)
	logs := &syncBuffer{}
	o := options(t, d.AppURL, logs)
	o.Mode = app.ModeWorker
	o.UI = nil
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a, err := app.New(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if a.Addr() != nil {
		t.Fatal("worker mode listens for HTTP")
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	waitFor(t, "the workers", func() bool { return strings.Contains(logs.String(), "river workers started") })
	var appName string
	if err := pool.QueryRow(t.Context(), "SELECT count(*)::text FROM pg_stat_activity WHERE application_name = $1", app.WorkerApplicationName).Scan(&appName); err != nil || appName == "0" {
		t.Fatalf("no %s connection: %q %v", app.WorkerApplicationName, appName, err)
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// No scheduler in a worker process.
	if riverRuns(t, pool, id) != 0 || strings.Contains(logs.String(), "scheduler started") {
		t.Fatal("worker ran the scheduler")
	}
	if !strings.Contains(logs.String(), "bekup worker stopped") {
		t.Fatal("no stop log")
	}

	// Same startup checks as serve: role, keyring.
	owner := options(t, d.OwnerURL, &syncBuffer{})
	owner.Mode = app.ModeWorker
	if _, err := app.New(t.Context(), owner); err == nil || !strings.Contains(err.Error(), "role check failed") {
		t.Fatalf("owner role: %v", err)
	}
	wrong := options(t, d.AppURL, &syncBuffer{})
	wrong.Mode = app.ModeWorker
	wrong.Config.Security.MasterKey = newKeyring(t)
	if _, err := app.New(t.Context(), wrong); !errors.Is(err, app.ErrKeyringMismatch) {
		t.Fatalf("wrong keyring: %v", err)
	}
	both := options(t, d.AppURL, &syncBuffer{})
	both.Mode, both.NoWorker = app.ModeWorker, true
	if _, err := app.New(t.Context(), both); err == nil {
		t.Fatal("worker with --no-worker accepted")
	}
}
