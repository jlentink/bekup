//go:build integration

package restore_test

// Test restores (task 4.6) against the app database with a scripted
// runner: scheduling, the restore into a runner-derived scratch database,
// the comparison with backup_stats, the verdicts, the scratch drop on
// success, failure and cancel, a failed drop and its retry, the target
// re-check, the stuck sweep and the API's authorization. The PostgreSQL
// end-to-end path with the real runner is in
// testrestore_pg_integration_test.go.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"google.golang.org/grpc"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

// scratchRunner adds the scratch RPCs and the privilege Probe to the
// fake runner.
type scratchRunner struct {
	*restore.FakeRunner
	mu       sync.Mutex
	stats    *runnerpb.DumpStats
	statsErr error
	dropErr  error
	asked    []string
	dropped  []string
	// privileges is the Probe verdict (nil: not privileged); probeErr
	// fails the Probe.
	privileges *runnerpb.TargetPrivileges
	probeErr   error
	probes     int
	// neutralized records the scratch databases hardened (D-170), in order
	// with the stats calls ("neutralize:<db>" / "stats:<db>").
	order       []string
	neutralErr  error
	neutralized []string
}

func (s *scratchRunner) Probe(_ context.Context, _ *runnerpb.ProbeRequest, _ ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes++
	if s.probeErr != nil {
		return nil, s.probeErr
	}
	p := s.privileges
	if p == nil {
		p = &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED}
	}
	return &runnerpb.ProbeResponse{Privileges: p}, nil
}

// NeutralizeRestoredDatabase hardens a scratch database (D-170): the
// PostgreSQL restorer calls it before the restore reports success.
func (s *scratchRunner) NeutralizeRestoredDatabase(_ context.Context, in *runnerpb.NeutralizeRestoredDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.NeutralizeRestoredDatabaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.neutralErr != nil {
		return nil, s.neutralErr
	}
	if in.GetKind() != runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH || in.GetDatabase() != "" {
		return nil, runnerpb.ReasonFailed.Status()
	}
	name := engine.ScratchPrefix + in.GetTempId()
	if !s.Has(name) {
		return nil, runnerpb.ReasonDatabaseNotFound.Status()
	}
	s.neutralized = append(s.neutralized, name)
	s.order = append(s.order, "neutralize:"+name)
	return &runnerpb.NeutralizeRestoredDatabaseResponse{Database: name}, nil
}

func (s *scratchRunner) ScratchStats(_ context.Context, in *runnerpb.ScratchStatsRequest, _ ...grpc.CallOption) (*runnerpb.ScratchStatsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, in.GetTempId()+":"+in.GetStats().GetLevel().String())
	s.order = append(s.order, "stats:"+engine.ScratchPrefix+in.GetTempId())
	if s.statsErr != nil {
		return nil, s.statsErr
	}
	if !s.Has(engine.ScratchPrefix + in.GetTempId()) {
		return nil, runnerpb.ReasonDatabaseNotFound.Status()
	}
	return &runnerpb.ScratchStatsResponse{Stats: s.stats, Database: engine.ScratchPrefix + in.GetTempId()}, nil
}

func (s *scratchRunner) DropScratchDatabase(_ context.Context, in *runnerpb.DropScratchDatabaseRequest, _ ...grpc.CallOption) (*runnerpb.DropScratchDatabaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dropErr != nil {
		return nil, s.dropErr
	}
	name := engine.ScratchPrefix + in.GetTempId()
	s.dropped = append(s.dropped, name)
	return &runnerpb.DropScratchDatabaseResponse{Existed: s.Has(name)}, nil
}

// leftovers are the scratch databases created and never dropped.
func (s *scratchRunner) leftovers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.CallList() {
		name, ok := strings.CutPrefix(c, "create:")
		if ok && strings.HasPrefix(name, engine.ScratchPrefix) && !slices.Contains(s.dropped, name) {
			out = append(out, name)
		}
	}
	return out
}

// fakeInserter records River inserts (no river_job rows).
type fakeInserter struct {
	n    atomic.Int64
	args []river.JobArgs
	mu   sync.Mutex
}

func (f *fakeInserter) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ queue.InsertOptions) (queue.InsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, args)
	return queue.InsertResult{JobID: 9000 + f.n.Add(1)}, nil
}

type testNotes struct {
	mu sync.Mutex
	n  []restore.TestNotification
}

func (t *testNotes) EmitTestRestore(_ context.Context, n restore.TestNotification) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n = append(t.n, n)
}

func (t *testNotes) last() restore.TestNotification {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.n) == 0 {
		return restore.TestNotification{}
	}
	return t.n[len(t.n)-1]
}

// trEnv is a test-restore environment.
type trEnv struct {
	*env
	runner  *scratchRunner
	worker  *restore.TestRestoreWorker
	restore *restore.Worker
	ins     *fakeInserter
	notes   *testNotes
	target  uuid.UUID
	job     jobInfo
	seqRun  int
}

// pgTestStats is the runner's stats message of the seeded "app" database.
func pgTestStats(rows int64) *runnerpb.DumpStats {
	return &runnerpb.DumpStats{
		Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, SameSnapshot: true,
		Objects:     &runnerpb.ObjectCounts{Schemas: 1, Tables: 1, Extensions: 1},
		Tables:      []*runnerpb.TableStats{{Schema: "public", Name: "items", Rows: rows, Count: runnerpb.RowCount_ROW_COUNT_EXACT}},
		TablesTotal: 1,
	}
}

func newTREnv(t *testing.T) *trEnv {
	t.Helper()
	e := newEnv(t)
	te := &trEnv{env: e, runner: &scratchRunner{FakeRunner: restore.NewFakeRunner(), stats: pgTestStats(1000)}, ins: &fakeInserter{}, notes: &testNotes{}}
	var err error
	te.restore, err = restore.NewWorker(restore.WorkerOptions{
		Service: e.svc, Slots: runstate.NewSlots(e.pool), Targets: &fakeTargets{engine: engine.KindPostgres}, Runner: te.runner,
		Limits:      runstate.SlotLimits{Global: 4, PerConnection: 2},
		LogMaxLines: 1000, LogMaxLineBytes: 4096, LogFlushInterval: 20 * time.Millisecond,
		Registry: logging.NewRegistry(), SlotHeartbeat: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if te.worker, err = restore.NewTestRestoreWorker(restore.TestRestoreWorkerOptions{Restores: te.restore, Notifier: te.notes}); err != nil {
		t.Fatal(err)
	}
	te.job = e.job(jobkeys.ModePassphrase, e.diskID)
	te.target = uuid.New()
	e.exec(`INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username)
		VALUES ($1, 'scratch', 'postgres', '17', '17', 'scratch.example.test', 5432, 'backup')`, te.target)
	e.exec(`UPDATE jobs SET test_restore_enabled = true, test_restore_connection_id = $2, test_restore_next_at = now() - interval '1 minute'
		WHERE id = $1`, te.job.id, te.target)
	return te
}

// seedRun seeds a successful run of the job with backup_stats for rows.
func (te *trEnv) seedRun(rows int64) {
	te.t.Helper()
	te.seqRun++
	s := te.seedAs(te.job, te.diskID, te.disk, []byte("PGDMP fake custom archive"), time.Now().Add(time.Duration(te.seqRun)*time.Minute),
		seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "16"})
	canon, err := jobs.CanonicalStats(engine.KindPostgres, "app", pgTestStats(rows))
	if err != nil {
		te.t.Fatal(err)
	}
	te.exec(`INSERT INTO backup_stats (run_item_id, run_id, job_id, level, consistency, tables_total, stats)
		SELECT id, run_id, $2, 'deep', 'snapshot', 1, $3 FROM run_items WHERE run_id = $1`, s.run, te.job.id, canon)
}

// schedule runs the scheduler and returns the queued test restore.
func (te *trEnv) schedule() uuid.UUID {
	te.t.Helper()
	n, err := te.svc.ScheduleTestRestores(te.t.Context(), te.ins)
	if err != nil || n != 1 {
		te.t.Fatalf("scheduled %d: %v", n, err)
	}
	var id uuid.UUID
	if err := te.pool.QueryRow(te.t.Context(), `SELECT id FROM test_restores WHERE job_id = $1 AND status = 'queued'`, te.job.id).Scan(&id); err != nil {
		te.t.Fatal(err)
	}
	return id
}

// queue inserts a queued test restore directly (manual trigger).
func (te *trEnv) queueOne() uuid.UUID {
	te.t.Helper()
	id := uuid.New()
	te.exec(`INSERT INTO test_restores (id, job_id, target_connection_id, trigger, river_job_id) VALUES ($1, $2, $3, 'manual', 1)`,
		id, te.job.id, te.target)
	return id
}

func (te *trEnv) work(ctx context.Context, id uuid.UUID) error {
	return te.worker.Work(ctx, &river.Job[queue.TestRestoreArgs]{JobRow: &rivertype.JobRow{ID: 4242, Attempt: 1}, Args: queue.TestRestoreArgs{TestRestoreID: id}})
}

func (te *trEnv) result(id uuid.UUID) (sqlc.TestRestore, []sqlc.TestRestoreItem) {
	te.t.Helper()
	q := sqlc.New(te.pool)
	tr, err := q.GetTestRestore(te.t.Context(), id)
	if err != nil {
		te.t.Fatal(err)
	}
	items, err := q.ListTestRestoreItems(te.t.Context(), id)
	if err != nil {
		te.t.Fatal(err)
	}
	return tr, items
}

func (te *trEnv) auditCount(action audit.Action, target string) int {
	return te.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, string(action), target)
}

func TestTestRestoreFlow(t *testing.T) {
	te := newTREnv(t)
	te.seedRun(1000)

	t.Run("scheduled test restore passes", func(t *testing.T) {
		id := te.schedule()
		if args, ok := te.ins.args[0].(queue.TestRestoreArgs); len(te.ins.args) != 1 || !ok || args.TestRestoreID != id {
			t.Fatalf("inserted %v", te.ins.args)
		}
		var next *time.Time
		if err := te.pool.QueryRow(t.Context(), `SELECT test_restore_next_at FROM jobs WHERE id = $1`, te.job.id).Scan(&next); err != nil || next == nil || !next.After(time.Now()) {
			t.Fatalf("next %v %v", next, err)
		}
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusPassed || tr.RunID == nil || tr.Databases != 1 || tr.Passed != 1 || len(items) != 1 {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		it := items[0]
		if it.Status != restore.TRStatusPassed || !it.ScratchDropped || it.RestoreID == nil || it.DbName != "app" {
			t.Fatalf("item %+v", it)
		}
		scratch := engine.ScratchPrefix + it.ScratchID
		if it.ScratchID != restore.TempID(*it.RestoreID) {
			t.Fatalf("scratch id %s not derived from restore %s", it.ScratchID, it.RestoreID)
		}
		calls := strings.Join(te.runner.CallList(), ",")
		if !strings.Contains(calls, "create:"+scratch) || !strings.Contains(calls, "restore:"+scratch) {
			t.Fatalf("calls %s", calls)
		}
		if len(te.runner.asked) != 1 || te.runner.asked[0] != it.ScratchID+":STATS_LEVEL_DEEP" {
			t.Fatalf("stats asked %v", te.runner.asked)
		}
		if l := te.runner.leftovers(); len(l) != 0 {
			t.Fatalf("leftover scratch databases %v", l)
		}
		st, _ := te.status(*it.RestoreID)
		if st != "succeeded" {
			t.Fatalf("internal restore %s", st)
		}
		var by *uuid.UUID
		var db *string
		if err := te.pool.QueryRow(t.Context(), `SELECT requested_by, target_db_name FROM restores WHERE id = $1`, *it.RestoreID).Scan(&by, &db); err != nil ||
			by != nil || db == nil || *db != scratch {
			t.Fatalf("restore row: %v %v %v", by, db, err)
		}
		for _, a := range []audit.Action{audit.ActionTestRestoreRequested, audit.ActionTestRestoreFinished} {
			if te.auditCount(a, id.String()) != 1 {
				t.Errorf("audit %s missing", a)
			}
		}
		if n := te.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.started' AND target_id = $1 AND details->>'test_restore_id' = $2`,
			it.RestoreID.String(), id.String()); n != 1 {
			t.Errorf("restore.started with test_restore_id: %d", n)
		}
		if n := te.notes.last(); n.Event != restore.NotifyTestPassed || n.TestRestoreID != id {
			t.Fatalf("notification %+v", n)
		}
		// One active test restore per job: a second due occurrence while one
		// is queued is skipped.
		te.exec(`UPDATE jobs SET test_restore_next_at = now() - interval '1 minute' WHERE id = $1`, te.job.id)
		te.queueOne()
		if n, err := te.svc.ScheduleTestRestores(t.Context(), te.ins); err != nil || n != 0 {
			t.Fatalf("scheduled while active: %d %v", n, err)
		}
		te.exec(`UPDATE test_restores SET status = 'canceled', finished_at = now() WHERE job_id = $1 AND status = 'queued'`, te.job.id)
	})

	t.Run("tampered stats fail with a diff", func(t *testing.T) {
		te.seedRun(1001) // the backup claims one row more than restored
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusFailed || tr.Failed != 1 || len(items) != 1 || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		d, err := restore.ParseDiff(items[0].Diff)
		if err != nil || d.Comparison == nil || len(d.Comparison.Findings) != 1 {
			t.Fatalf("diff %s: %v", items[0].Diff, err)
		}
		f := d.Comparison.Findings[0]
		if f.Code != restore.FindRowCount || f.Object != "public.items" || *f.Source != 1001 || *f.Restored != 1000 || *f.Delta != -1 {
			t.Fatalf("finding %+v", f)
		}
		if n := te.notes.last(); n.Event != restore.NotifyTestFailed {
			t.Fatalf("notification %+v", n)
		}
		if te.alertKinds()[restore.AlertTestRestoreFailed] == 0 {
			t.Fatal("no run.test_restore_failed alert")
		}
		if l := te.runner.leftovers(); len(l) != 0 {
			t.Fatalf("leftover scratch databases %v", l)
		}
	})

	t.Run("a failed restore is an error and the scratch database is dropped", func(t *testing.T) {
		te.seedRun(1000)
		te.runner.ExitCode = 1
		defer func() { te.runner.ExitCode = 0 }()
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || tr.ErrorCode == nil || *tr.ErrorCode != restore.CodeRestoreFailed || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		d, err := restore.ParseDiff(items[0].Diff)
		if err != nil || d.RestoreStatus != "failed" || d.RestoreErrorCode != restore.CodeRestoreFailed {
			t.Fatalf("diff %+v", d)
		}
		if l := te.runner.leftovers(); len(l) != 0 {
			t.Fatalf("leftover scratch databases %v", l)
		}
	})

	t.Run("cancel drops the scratch database", func(t *testing.T) {
		te.runner.Block = true
		te.runner.Started = make(chan struct{})
		defer func() { te.runner.Block = false }()
		id := te.queueOne()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		go func() {
			<-te.runner.Started
			cancel(rivertype.ErrJobCancelledRemotely)
		}()
		err := te.work(ctx, id)
		var jc *rivertype.JobCancelError
		if !errors.As(err, &jc) {
			t.Fatalf("err = %v", err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusCanceled || len(items) != 1 || items[0].Status != restore.TRStatusCanceled || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		if l := te.runner.leftovers(); len(l) != 0 {
			t.Fatalf("leftover scratch databases %v", l)
		}
	})

	t.Run("a failed drop is kept, audited and retried", func(t *testing.T) {
		te.runner.dropErr = runnerpb.ReasonUnreachable.Status()
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		_, items := te.result(id)
		if items[0].ScratchDropped {
			t.Fatal("scratch reported dropped")
		}
		if d, err := restore.ParseDiff(items[0].Diff); err != nil || !d.ScratchKept {
			t.Fatalf("diff %+v", d)
		}
		if te.auditCount(audit.ActionTestRestoreScratchKept, id.String()) == 0 || te.alertKinds()[restore.AlertScratchKept] == 0 {
			t.Fatal("scratch_kept not audited or alerted")
		}
		if n, err := te.svc.RetryScratchDrops(t.Context(), te.restore); err != nil || n != 0 {
			t.Fatalf("retried while failing: %d", n)
		}
		te.runner.dropErr = nil
		if n, err := te.svc.RetryScratchDrops(t.Context(), te.restore); err != nil || n != 1 {
			t.Fatalf("retry: %d %v", n, err)
		}
		if _, items = te.result(id); !items[0].ScratchDropped || len(te.runner.leftovers()) != 0 {
			t.Fatalf("after retry %+v %v", items, te.runner.leftovers())
		}
	})

	t.Run("a changed target stops a queued test restore", func(t *testing.T) {
		id := te.queueOne()
		other := uuid.New()
		te.exec(`INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username)
			VALUES ($1, 'scratch-2', 'postgres', '17', '17', 'scratch2.example.test', 5432, 'backup')`, other)
		te.exec(`UPDATE jobs SET test_restore_connection_id = $2 WHERE id = $1`, te.job.id, other)
		defer te.exec(`UPDATE jobs SET test_restore_connection_id = $2 WHERE id = $1`, te.job.id, te.target)
		before := len(te.runner.CallList())
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || *tr.ErrorCode != restore.CodeTestTargetChanged || len(items) != 0 || len(te.runner.CallList()) != before {
			t.Fatalf("test restore %+v items %d", tr, len(items))
		}
	})

	t.Run("stats unavailable is an error, unknown counts warn", func(t *testing.T) {
		te.runner.statsErr = runnerpb.ReasonFailed.Status()
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		te.runner.statsErr = nil
		if tr, items := te.result(id); tr.Status != restore.TRStatusError || *items[0].ErrorCode != restore.CodeStatsUnavailable || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		te.runner.stats = pgTestStats(1000)
		te.runner.stats.Tables[0].Count, te.runner.stats.Tables[0].Rows = runnerpb.RowCount_ROW_COUNT_UNKNOWN, 0
		defer func() { te.runner.stats = pgTestStats(1000) }()
		id = te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		if tr, _ := te.result(id); tr.Status != restore.TRStatusWarnings || tr.Warned != 1 {
			t.Fatalf("test restore %+v", tr)
		}
		if n := te.notes.last(); n.Event != restore.NotifyTestWarnings {
			t.Fatalf("notification %+v", n)
		}
	})
}

func (te *trEnv) alertKinds() map[restore.AlertKind]int {
	te.mu.Lock()
	defer te.mu.Unlock()
	out := map[restore.AlertKind]int{}
	for _, a := range te.alerts {
		out[a.Kind]++
	}
	return out
}

func TestTestRestoreRecipientJob(t *testing.T) {
	te := newTREnv(t)
	te.job = te.env.job(jobkeys.ModeRecipient, te.diskID)
	te.exec(`UPDATE jobs SET test_restore_enabled = true, test_restore_connection_id = $2 WHERE id = $1`, te.job.id, te.target)
	te.seedRun(1000)
	id := te.queueOne()
	if err := te.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	tr, items := te.result(id)
	if tr.Status != restore.TRStatusError || len(items) != 1 || *items[0].ErrorCode != restore.CodeIdentityRequired || items[0].RestoreID != nil {
		t.Fatalf("test restore %+v items %+v", tr, items)
	}
	if len(te.runner.CallList()) != 0 {
		t.Fatalf("runner calls %v", te.runner.CallList())
	}
}

func TestTestRestoreNoBackup(t *testing.T) {
	te := newTREnv(t)
	id := te.queueOne()
	if err := te.work(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if tr, _ := te.result(id); tr.Status != restore.TRStatusError || *tr.ErrorCode != restore.CodeNoBackup {
		t.Fatalf("test restore %+v", tr)
	}
}

func TestTestRestoreStuckSweep(t *testing.T) {
	te := newTREnv(t)
	// Running for a long time with its River job gone (crashed worker).
	id := uuid.New()
	te.exec(`INSERT INTO test_restores (id, job_id, target_connection_id, trigger, status, started_at)
		VALUES ($1, $2, $3, 'schedule', 'running', now() - interval '3 hours')`, id, te.job.id, te.target)
	n, err := te.svc.SweepStuckTestRestores(t.Context(), time.Hour, time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("swept %d: %v", n, err)
	}
	if tr, _ := te.result(id); tr.Status != restore.TRStatusError || *tr.ErrorCode != restore.CodeAbandoned {
		t.Fatalf("test restore %+v", tr)
	}
	if te.auditCount(audit.ActionTestRestoreAbandoned, id.String()) != 1 {
		t.Fatal("no test_restore.abandoned")
	}
}

func TestTestRestoreAPI(t *testing.T) {
	te := newTREnv(t)
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: te.pool}})
	api, err := restore.NewTestRestoreAPI(restore.TestRestoreAPIOptions{Service: te.svc, Policy: pol, Queue: func() restore.Inserter { return te.ins }})
	if err != nil {
		t.Fatal(err)
	}
	user := func(role auth.Role) context.Context {
		var id uuid.UUID
		if err := te.pool.QueryRow(t.Context(), `INSERT INTO users (email, display_name, role) VALUES ($1, 'U', $2) RETURNING id`,
			uuid.NewString()+"@example.com", string(role)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return auth.WithPrincipal(t.Context(), auth.Principal{UserID: id, Role: role, SessionID: uuid.New(), AAL: 2, StepUpAt: time.Now(), AAL2At: time.Now()})
	}
	maint, owner, other := user(auth.RoleMaintainer), user(auth.RoleUser), user(auth.RoleUser)
	ownerID, _ := auth.FromContext(owner)
	te.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, te.job.id, ownerID.UserID)

	v, err := api.RunNow(maint, te.job.id)
	if err != nil || v.Status != restore.TRStatusQueued || v.Trigger != restore.TestTriggerManual || v.RequestedBy == nil || v.RiverJobID == nil {
		t.Fatalf("run now: %+v %v", v, err)
	}
	if te.auditCount(audit.ActionTestRestoreRequested, v.ID.String()) != 1 {
		t.Fatal("test_restore.requested missing")
	}
	if _, err := api.RunNow(maint, te.job.id); !errors.Is(err, restore.ErrTestRestoreActive) {
		t.Fatalf("second run now: %v", err)
	}
	if _, err := api.RunNow(owner, te.job.id); err == nil {
		t.Fatal("an owner queued a test restore")
	}
	list, _, err := api.List(owner, te.job.id, nil, 10)
	if err != nil || len(list) != 1 || list[0].ID != v.ID {
		t.Fatalf("owner list: %v %v", list, err)
	}
	if _, _, err := api.List(other, te.job.id, nil, 10); err == nil {
		t.Fatal("a non-owner listed test restores")
	}
	if _, err := api.Get(other, v.ID); err == nil {
		t.Fatal("a non-owner read a test restore")
	}
	if _, err := api.Get(other, uuid.New()); errors.Is(err, restore.ErrNotFound) {
		t.Fatal("an unknown id is NOT_FOUND for a non-owner")
	}
	if _, err := api.Get(maint, uuid.New()); !errors.Is(err, restore.ErrNotFound) {
		t.Fatalf("unknown id for staff: %v", err)
	}
	got, err := api.Get(owner, v.ID)
	if err != nil || got.ID != v.ID || got.Items == nil {
		t.Fatalf("owner get: %+v %v", got, err)
	}
	te.exec(`UPDATE jobs SET test_restore_enabled = false, test_restore_connection_id = NULL WHERE id = $1`, te.job.id)
	te.exec(`UPDATE test_restores SET status = 'canceled', finished_at = now() WHERE id = $1`, v.ID)
	if _, err := api.RunNow(maint, te.job.id); !errors.Is(err, restore.ErrNoTestTarget) {
		t.Fatalf("no target: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), fakeTargetPassword) {
		t.Fatal("credential in a view")
	}
}

// D-068 / D-170: the worker re-probes the scratch target before every test
// restore and refuses a gated role without a covering admin
// acknowledgement; every restored scratch database is hardened before its
// statistics are read.
func TestTestRestorePrivilegedTargetAndNeutralize(t *testing.T) {
	te := newTREnv(t)
	te.seedRun(1000)
	superuser := &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"superuser"}}
	setProbe := func(p *runnerpb.TargetPrivileges, err error) {
		te.runner.mu.Lock()
		defer te.runner.mu.Unlock()
		te.runner.privileges, te.runner.probeErr = p, err
	}
	refused := func(t *testing.T, why string) {
		t.Helper()
		before := len(te.runner.CallList())
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || tr.ErrorCode == nil || *tr.ErrorCode != restore.CodeTestPrivilegedTarget ||
			len(items) != 0 || len(te.runner.CallList()) != before {
			t.Fatalf("%s: test restore %+v items %d calls %v", why, tr, len(items), te.runner.CallList()[before:])
		}
		if n := te.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND details->>'error_code' = 'privileged_target'
			AND details ? 'target_privileges'`, string(audit.ActionTestRestoreFinished), id.String()); n != 1 {
			t.Fatalf("%s: test_restore.finished without the verdict", why)
		}
	}
	passes := func(t *testing.T) uuid.UUID {
		t.Helper()
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		if tr, items := te.result(id); tr.Status != restore.TRStatusPassed {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		return id
	}

	t.Run("a role that became privileged without an acknowledgement is refused", func(t *testing.T) {
		setProbe(superuser, nil)
		refused(t, "privileged")
	})
	t.Run("unknown is refused", func(t *testing.T) {
		setProbe(&runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN, Reasons: []string{"roles_granted"}}, nil)
		refused(t, "unknown")
	})
	t.Run("a failing probe is refused", func(t *testing.T) {
		setProbe(nil, runnerpb.ReasonUnreachable.Status())
		refused(t, "probe failed")
	})
	t.Run("an older runner without a verdict is refused", func(t *testing.T) {
		setProbe(&runnerpb.TargetPrivileges{}, nil)
		refused(t, "no verdict")
	})
	t.Run("a stored admin acknowledgement covers the verdict", func(t *testing.T) {
		te.exec(`UPDATE jobs SET test_restore_target_privileges = 'privileged', test_restore_privilege_reasons = '{superuser}',
			test_restore_privileged_ack_at = now() WHERE id = $1`, te.job.id)
		setProbe(superuser, nil)
		id := passes(t)
		if n := te.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2
			AND details->>'privileged_target_acknowledged' = 'true' AND details->>'target_privileges' = 'privileged'`,
			string(audit.ActionTestRestoreFinished), id.String()); n != 1 {
			t.Fatal("test_restore.finished without the acknowledgement")
		}
	})
	t.Run("a new reason needs a new acknowledgement", func(t *testing.T) {
		setProbe(&runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"superuser", "createrole"}}, nil)
		refused(t, "new reason")
	})
	t.Run("the scratch database is hardened before its statistics are read", func(t *testing.T) {
		setProbe(nil, nil)
		te.runner.mu.Lock()
		te.runner.order = nil
		te.runner.mu.Unlock()
		id := passes(t)
		_, items := te.result(id)
		scratch := engine.ScratchPrefix + items[0].ScratchID
		te.runner.mu.Lock()
		order := slices.Clone(te.runner.order)
		te.runner.mu.Unlock()
		if want := []string{"neutralize:" + scratch, "stats:" + scratch}; !slices.Equal(order, want) {
			t.Fatalf("order %v, want %v", order, want)
		}
	})
	t.Run("a failed hardening is an error, not inspected, and dropped", func(t *testing.T) {
		te.runner.mu.Lock()
		te.runner.neutralErr = runnerpb.ReasonFailed.Status()
		te.runner.order = nil
		te.runner.mu.Unlock()
		defer func() { te.runner.mu.Lock(); te.runner.neutralErr = nil; te.runner.mu.Unlock() }()
		id := te.queueOne()
		if err := te.work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || len(items) != 1 || items[0].ErrorCode == nil ||
			*items[0].ErrorCode != restore.CodeNeutralizeFailed || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		te.runner.mu.Lock()
		order := slices.Clone(te.runner.order)
		te.runner.mu.Unlock()
		if len(order) != 0 {
			t.Fatalf("inspected an unhardened scratch database: %v", order)
		}
		if l := te.runner.leftovers(); len(l) != 0 {
			t.Fatalf("leftover scratch databases %v", l)
		}
	})
}
