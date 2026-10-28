//go:build integration

package restore_test

// The restore worker (task 4.2) against the app database with a scripted
// runner (restore.FakeRunner): status transitions and events, slot snooze,
// cancel, failures that leave an overwritten target untouched, the engine
// re-check, target re-check, audit and log redaction. The real runner and
// PostgreSQL targets are covered by pg_integration_test.go.

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

const fakeTargetPassword = "fake-target-password-123"

// fakeTargets is a TargetSource for one connection.
type fakeTargets struct {
	mu       sync.Mutex
	engine   engine.Kind
	gone     bool
	recorded []string
}

func (f *fakeTargets) LockRestoreTargetTx(_ context.Context, _ *sqlc.Queries, id uuid.UUID) (connections.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		return connections.Connection{}, connections.ErrNotFound
	}
	return connections.Connection{ID: id, Engine: f.engine, Ready: true}, nil
}

func (f *fakeTargets) RestoreTarget(context.Context, uuid.UUID) (*runnerpb.Target, func(), error) {
	t := &runnerpb.Target{Engine: runnerpb.Engine_ENGINE_POSTGRES, Password: []byte(fakeTargetPassword)}
	return t, func() { runnerpb.ZeroSecrets(t) }, nil
}

func (f *fakeTargets) RunningRestoreTarget(ctx context.Context, id, _ uuid.UUID) (*runnerpb.Target, func(), error) {
	f.mu.Lock()
	gone := f.gone
	f.mu.Unlock()
	if gone {
		return nil, nil, connections.ErrNotFound
	}
	return f.RestoreTarget(ctx, id)
}

func (f *fakeTargets) RecordRunFailure(_ context.Context, _ uuid.UUID, op string, err error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, op+":"+string(runnerpb.ErrorReason(err)))
	return nil
}

// notes collects notifications.
type notes struct {
	mu sync.Mutex
	n  []restore.Notification
}

func (n *notes) Emit(_ context.Context, x restore.Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.n = append(n.n, x)
}

func (n *notes) events() []restore.NotifyEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]restore.NotifyEvent, 0, len(n.n))
	for _, x := range n.n {
		out = append(out, x.Event)
	}
	return out
}

// wenv is a worker test environment.
type wenv struct {
	*env
	runner  *restore.FakeRunner
	targets *fakeTargets
	notes   *notes
	bus     *pubsub.Bus
	worker  *restore.Worker
	slots   *runstate.Slots
}

func newWorkerEnv(t *testing.T, dbs ...string) *wenv {
	t.Helper()
	bus := pubsub.NewBus()
	e := newEnv(t, func(o *restore.Options) { o.Publisher = &pubsub.Publisher{Bus: bus} })
	w := &wenv{env: e, runner: restore.NewFakeRunner(dbs...), targets: &fakeTargets{engine: engine.KindPostgres}, notes: &notes{}, bus: bus,
		slots: runstate.NewSlots(e.pool)}
	var err error
	w.worker, err = restore.NewWorker(restore.WorkerOptions{
		Service: e.svc, Slots: w.slots, Targets: w.targets, Runner: w.runner,
		Limits:      runstate.SlotLimits{Global: 4, PerConnection: 1},
		LogMaxLines: 1000, LogMaxLineBytes: 4096, LogFlushInterval: 20 * time.Millisecond,
		Notifier: w.notes, Registry: logging.NewRegistry(), SlotHeartbeat: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// pgArtifact seeds a PostgreSQL custom-format database artifact.
func (w *wenv) pgArtifact(dump []byte) (jobInfo, seeded) {
	w.t.Helper()
	j := w.job(jobkeys.ModePassphrase, w.diskID)
	return j, w.seedAs(j, w.diskID, w.disk, dump, time.Now(), seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "16"})
}

// request inserts a queued restore into the job's connection.
func (w *wenv) request(j jobInfo, artifact uuid.UUID, mode restore.Mode, dbName *string) uuid.UUID {
	w.t.Helper()
	return w.requestTo(j, artifact, j.conn, mode, dbName)
}

// requestTo inserts a queued restore into target.
func (w *wenv) requestTo(_ jobInfo, artifact, target uuid.UUID, mode restore.Mode, dbName *string) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	err := db.Tx(w.t.Context(), w.pool, func(q *sqlc.Queries) error {
		r, err := w.svc.CreateTx(w.t.Context(), q, restore.CreateInput{
			ArtifactID: artifact, TargetConnectionID: target, TargetDBName: dbName, Mode: mode, RequestedBy: w.admin,
		})
		id = r.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *wenv) work(ctx context.Context, id uuid.UUID) error {
	return w.worker.Work(ctx, &river.Job[queue.RestoreArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1}, Args: queue.RestoreArgs{RestoreID: id}})
}

func (w *wenv) logs(id uuid.UUID) string {
	w.t.Helper()
	rows, err := sqlc.New(w.pool).ListRestoreLogs(w.t.Context(), sqlc.ListRestoreLogsParams{RestoreID: id, AfterID: 0, MaxRows: 10000})
	if err != nil {
		w.t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(string(r.Level) + " " + string(r.Stream) + " " + r.Line + "\n")
	}
	return b.String()
}

func (w *wenv) auditActions(id uuid.UUID) []string {
	w.t.Helper()
	rows, err := w.pool.Query(w.t.Context(), `SELECT action || ':' || coalesce(details->>'status', '') || ':' || coalesce(details->>'error_code', '')
		FROM audit_log WHERE target_type = 'restore' AND target_id = $1 ORDER BY id`, id.String())
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func strp(s string) *string { return &s }

const tempName = "app__bekup_restore_"

// TestWorkerOverwrite: queued -> staging -> verifying -> restoring ->
// succeeded with events, the dump swapped in, logs stored and redacted,
// audit and notifications, slot, hold and staging released.
func TestWorkerOverwrite(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.Received["app"] = []byte("old")
	dump := randomDump(700 << 10)
	j, s := w.pgArtifact(dump)
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	sub := w.bus.Subscribe(pubsub.RestoreEvents.Name, 64)
	defer sub.Close()
	var during string
	w.runner.OnRestore = func(*runnerpb.RestoreHeader) { during, _ = w.status(id) }
	w.runner.Logs = []string{"pg_restore: connecting with password " + fakeTargetPassword, "postgres://bekup:hunter2@db/app"}
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "succeeded" || code != "" {
		t.Fatalf("status %s %s", st, code)
	}
	if during != "restoring" {
		t.Fatalf("status during the restore = %q", during)
	}
	tid := restore.TempID(id)
	want := []string{"probe", "create:" + tempName + tid, "restore:" + tempName + tid, "neutralize:" + tempName + tid, "swap:app", "drop:app__bekup_old_" + tid}
	if got := w.runner.CallList(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v", got)
	}
	if !bytes.Equal(w.runner.Data("app"), dump) {
		t.Fatal("restored data differs from the dump")
	}
	h := w.runner.Headers[0]
	if h.GetSourceVersionKey() != "16" || !h.GetNoOwner() {
		t.Fatalf("header %v", h)
	}
	// Events: staging, verifying, restoring, succeeded (+ log batches).
	var restoreEvents int
	timeout := time.After(2 * time.Second)
drain:
	for {
		select {
		case m := <-sub.C():
			kind, rid, err := restore.ParsePayload(m.Payload)
			if err != nil || rid != id {
				t.Fatalf("payload %q", m.Payload)
			}
			if kind == restore.EventRestore {
				restoreEvents++
			}
		case <-timeout:
			break drain
		}
	}
	if restoreEvents != 4 {
		t.Fatalf("restore events = %d, want 4", restoreEvents)
	}
	logs := w.logs(id)
	if strings.Contains(logs, fakeTargetPassword) || strings.Contains(logs, "hunter2") {
		t.Fatalf("secret in logs:\n%s", logs)
	}
	if !strings.Contains(logs, "stderr pg_restore: connecting") || !strings.Contains(logs, "restore succeeded") {
		t.Fatalf("logs:\n%s", logs)
	}
	var lines int32
	if err := w.pool.QueryRow(t.Context(), "SELECT log_lines FROM restores WHERE id = $1", id).Scan(&lines); err != nil || lines < 5 {
		t.Fatalf("log_lines %d %v", lines, err)
	}
	if got := w.auditActions(id); !slices.Equal(got, []string{"restore.started::", "restore.finished:succeeded:"}) {
		t.Fatalf("audit = %v", got)
	}
	if got := w.notes.events(); !slices.Equal(got, []restore.NotifyEvent{restore.NotifyStarted, restore.NotifyCompleted}) {
		t.Fatalf("notifications = %v", got)
	}
	if n := w.count("SELECT count(*) FROM run_slots"); n != 0 {
		t.Fatalf("slots = %d", n)
	}
	if w.holds(s.artifact) != 0 || len(w.scratchEntries()) != 0 {
		t.Fatal("hold or staging left")
	}
	// A second delivery never restores again.
	if err := w.work(t.Context(), id); err != nil || len(w.runner.CallList()) != 6 {
		t.Fatalf("second work: %v %v", err, w.runner.CallList())
	}
}

// TestWorkerNewDB restores into a new database.
func TestWorkerNewDB(t *testing.T) {
	w := newWorkerEnv(t)
	dump := randomDump(10 << 10)
	j, s := w.pgArtifact(dump)
	id := w.request(j, s.artifact, restore.ModeNewDB, strp("copy"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, _ := w.status(id); st != "succeeded" || !bytes.Equal(w.runner.Data("copy"), dump) {
		t.Fatalf("status %s", st)
	}
	if got := w.runner.CallList(); !slices.Equal(got, []string{"probe", "create:copy", "restore:copy", "neutralize:copy"}) {
		t.Fatalf("calls = %v", got)
	}
}

// TestWorkerSlotSnooze: a busy target connection snoozes the job; the
// restore stays queued and nothing is staged or audited.
func TestWorkerSlotSnooze(t *testing.T) {
	w := newWorkerEnv(t, "app")
	j, s := w.pgArtifact(randomDump(1024))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	other := uuid.New()
	if ok, err := w.slots.TryAcquire(t.Context(), other, j.conn, runstate.SlotBackup, runstate.SlotLimits{Global: 4, PerConnection: 1}); err != nil || !ok {
		t.Fatalf("hold slot: %v %v", ok, err)
	}
	err := w.work(t.Context(), id)
	var snooze *rivertype.JobSnoozeError
	if !errors.As(err, &snooze) || snooze.Duration <= 0 {
		t.Fatalf("err = %v", err)
	}
	if st, _ := w.status(id); st != "queued" {
		t.Fatalf("status %s", st)
	}
	if len(w.runner.CallList()) != 0 || len(w.auditActions(id)) != 0 || len(w.scratchEntries()) != 0 {
		t.Fatal("the snoozed restore did work")
	}
	if err := w.slots.Release(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, _ := w.status(id); st != "succeeded" {
		t.Fatalf("status %s", st)
	}
}

// TestWorkerFailureLeavesTarget: a failing pg_restore fails the restore,
// drops the temporary database and never swaps.
func TestWorkerFailureLeavesTarget(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.Received["app"] = []byte("old")
	w.runner.ExitCode = 1
	j, s := w.pgArtifact(randomDump(4096))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "failed" || code != restore.CodeRestoreFailed {
		t.Fatalf("status %s %s", st, code)
	}
	tid := restore.TempID(id)
	if got := w.runner.CallList(); !slices.Equal(got, []string{"probe", "create:" + tempName + tid, "restore:" + tempName + tid, "drop:" + tempName + tid}) {
		t.Fatalf("calls = %v", got)
	}
	if string(w.runner.Data("app")) != "old" || w.runner.Has(tempName+tid) {
		t.Fatal("target changed or temp kept")
	}
	if got := w.auditActions(id); !slices.Equal(got, []string{"restore.started::", "restore.finished:failed:restore_failed"}) {
		t.Fatalf("audit = %v", got)
	}
	if got := w.notes.events(); !slices.Equal(got, []restore.NotifyEvent{restore.NotifyStarted, restore.NotifyFailed}) {
		t.Fatalf("notifications = %v", got)
	}
}

// TestWorkerCancel: a River cancel mid-restore kills the stream, drops
// the temporary database and records canceled.
func TestWorkerCancel(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.Received["app"] = []byte("old")
	w.runner.Block = true
	j, s := w.pgArtifact(randomDump(600 << 10))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	ctx, cancel := context.WithCancelCause(t.Context())
	go func() {
		<-w.runner.Started
		cancel(rivertype.ErrJobCancelledRemotely)
	}()
	err := w.work(ctx, id)
	var jc *rivertype.JobCancelError
	if !errors.As(err, &jc) {
		t.Fatalf("err = %v", err)
	}
	if st, code := w.status(id); st != "canceled" || code != restore.CodeCanceled {
		t.Fatalf("status %s %s", st, code)
	}
	tid := restore.TempID(id)
	if !slices.Contains(w.runner.CallList(), "drop:"+tempName+tid) || w.runner.Has(tempName+tid) || string(w.runner.Data("app")) != "old" {
		t.Fatalf("calls = %v", w.runner.CallList())
	}
	if len(w.scratchEntries()) != 0 || w.holds(s.artifact) != 0 {
		t.Fatal("staging or hold left")
	}
}

// TestWorkerEngineMismatch: the target's engine is re-checked against the
// signed manifest before anything reaches the runner.
func TestWorkerEngineMismatch(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.targets.engine = engine.KindMySQL
	j, s := w.pgArtifact(randomDump(1024))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "failed" || code != restore.CodeEngineMismatch {
		t.Fatalf("status %s %s", st, code)
	}
	if !slices.Equal(w.runner.CallList(), []string{"probe"}) || len(w.scratchEntries()) != 0 {
		t.Fatal("runner called or staging left")
	}
}

// TestWorkerTargetGone: a deleted / expired target fails the queued
// restore before staging.
func TestWorkerTargetGone(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.targets.gone = true
	j, s := w.pgArtifact(randomDump(1024))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "failed" || code != restore.CodeTargetUnavailable {
		t.Fatalf("status %s %s", st, code)
	}
	if w.holds(s.artifact) != 0 || len(w.runner.CallList()) != 0 || len(w.auditActions(id)) != 0 {
		t.Fatal("work done for a gone target")
	}
}

// TestWorkerPinMismatch: a pin mismatch of the target is recorded as a
// connection security event.
func TestWorkerPinMismatch(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.CreateErr = runnerpb.ReasonPinMismatch.Status()
	j, s := w.pgArtifact(randomDump(1024))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "failed" || code != "tls_pin_mismatch" {
		t.Fatalf("status %s %s", st, code)
	}
	if len(w.targets.recorded) != 1 || w.targets.recorded[0] != "restore:TLS_PIN_MISMATCH" {
		t.Fatalf("recorded %v", w.targets.recorded)
	}
}

// TestWorkerGlobals: a globals artifact restores only as globals.
func TestWorkerGlobals(t *testing.T) {
	w := newWorkerEnv(t)
	dump := []byte("CREATE ROLE reader;\n")
	j := w.job(jobkeys.ModePassphrase, w.diskID)
	s := w.seedAs(j, w.diskID, w.disk, dump, time.Now(), seedSpec{kind: pipeline.KindGlobals, format: pipeline.FormatSQL, engine: "postgres", versionKey: "17"})
	id := w.request(j, s.artifact, restore.ModeNewDB, nil)
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "succeeded" {
		t.Fatalf("status %s %s", st, code)
	}
	if got := w.runner.CallList(); !slices.Equal(got, []string{"probe", "restore:(globals)"}) {
		t.Fatalf("calls = %v", got)
	}
	want, n := restore.IdempotentCreateRoles(dump)
	if n != 1 || !bytes.Equal(w.runner.Data(""), want) {
		t.Fatalf("globals data differs: %q", w.runner.Data(""))
	}
}

// TestWorkerPrivilegeRecheck: the worker probes the target role again
// right before it stages; a role that became privileged after the request
// refuses the restore unless an admin acknowledged exactly those reasons.
func TestWorkerPrivilegeRecheck(t *testing.T) {
	priv := func(reasons ...string) *runnerpb.TargetPrivileges {
		return &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: reasons}
	}
	requestWith := func(w *wenv, j jobInfo, artifact uuid.UUID, by uuid.UUID, p connections.Privileges, ack bool) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		err := db.Tx(t.Context(), w.pool, func(q *sqlc.Queries) error {
			r, err := w.svc.CreateTx(t.Context(), q, restore.CreateInput{
				ArtifactID: artifact, TargetConnectionID: j.conn, TargetDBName: strp("copy"), Mode: restore.ModeNewDB,
				RequestedBy: by, Privileges: &p, PrivilegedAck: ack,
			})
			id = r.ID
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	notPriv := connections.Privileges{Status: connections.PrivilegeNotPrivileged}
	acked := connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"member_of:pg_signal_backend"}}
	for _, c := range []struct {
		name    string
		req     connections.Privileges
		ack     bool
		demoted bool
		now     *runnerpb.TargetPrivileges
		refused string
	}{
		{name: "still not privileged", req: notPriv},
		{name: "became privileged", req: notPriv, now: priv("rolsuper"), refused: "not_acknowledged"},
		{name: "became unknown", req: notPriv, now: &runnerpb.TargetPrivileges{
			Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN, Reasons: []string{"roles_granted"}}, refused: "not_acknowledged"},
		{name: "acknowledged and unchanged", req: acked, ack: true, now: priv("member_of:pg_signal_backend")},
		{name: "acknowledged, new reason", req: acked, ack: true, now: priv("member_of:pg_signal_backend", "rolsuper"), refused: "new_reasons"},
		{name: "acknowledged by a non-admin", req: acked, ack: true, demoted: true, now: priv("member_of:pg_signal_backend"), refused: "requester_not_admin"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorkerEnv(t)
			w.runner.Privileges = c.now
			j, s := w.pgArtifact(randomDump(4096))
			by := w.admin
			if c.demoted {
				if err := w.pool.QueryRow(t.Context(), `INSERT INTO users (email, display_name, role) VALUES ('m@example.com', 'M', 'maintainer') RETURNING id`).Scan(&by); err != nil {
					t.Fatal(err)
				}
			}
			id := requestWith(w, j, s.artifact, by, c.req, c.ack)
			if err := w.work(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			st, code := w.status(id)
			if c.refused == "" {
				if st != "succeeded" {
					t.Fatalf("status %s %s", st, code)
				}
				return
			}
			if st != "failed" || code != restore.CodePrivilegedTarget {
				t.Fatalf("status %s %s", st, code)
			}
			if got := w.runner.CallList(); !slices.Equal(got, []string{"probe"}) {
				t.Fatalf("calls = %v", got)
			}
			var refusal string
			if err := w.pool.QueryRow(t.Context(), `SELECT details->>'refusal' FROM audit_log WHERE action = 'restore.privileged_target_refused' AND target_id = $1`,
				id.String()).Scan(&refusal); err != nil || refusal != c.refused {
				t.Fatalf("audit refusal %q %v", refusal, err)
			}
			if w.holds(s.artifact) != 0 || len(w.scratchEntries()) != 0 {
				t.Fatal("staged although refused")
			}
		})
	}
}

// TestWorkerSecdefAudit: a neutralization that switched functions is
// audited before the swap.
func TestWorkerSecdefAudit(t *testing.T) {
	w := newWorkerEnv(t, "app")
	w.runner.Secdef = []string{"public.esc()"}
	j, s := w.pgArtifact(randomDump(4096))
	id := w.request(j, s.artifact, restore.ModeOverwrite, strp("app"))
	if err := w.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if st, code := w.status(id); st != "succeeded" {
		t.Fatalf("status %s %s", st, code)
	}
	var fns string
	if err := w.pool.QueryRow(t.Context(), `SELECT details->>'functions' FROM audit_log WHERE action = 'restore.secdef_neutralized' AND target_id = $1`,
		id.String()).Scan(&fns); err != nil || !strings.Contains(fns, "public.esc()") {
		t.Fatalf("audit %q %v", fns, err)
	}
	var warns []string
	if err := w.pool.QueryRow(t.Context(), "SELECT warnings FROM restores WHERE id = $1", id).Scan(&warns); err != nil || !slices.Contains(warns, restore.WarnSecdefNeutralized) {
		t.Fatalf("warnings %v %v", warns, err)
	}
	if !strings.Contains(w.logs(id), "switched to SECURITY INVOKER: public.esc()") {
		t.Fatalf("logs:\n%s", w.logs(id))
	}
}
