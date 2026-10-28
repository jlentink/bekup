//go:build integration

package restore_test

// Test restores end to end (task 4.6) with the real runner and PostgreSQL
// (the harness of pg_integration_test.go): the source statistics come from
// a real Dump with deep stats on PostgreSQL 16, the backup is restored into
// a runner-derived bekup_test_<id> database on PostgreSQL 17, its exact
// counts are read through ScratchStats and compared; the scratch database
// is gone after success, a failed comparison, a failed restore and a
// cancel; the scratch RPCs refuse any name the runner did not derive.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// dumpWithStats takes a real dump of a source database with deep stats.
func (e *pgiEnv) dumpWithStats(db string) ([]byte, *runnerpb.DumpStats) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 5*time.Minute)
	defer cancel()
	target, release, err := e.conns.Target(ctx, e.srcConn)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := e.rc.RPC().Dump(ctx, &runnerpb.DumpRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target, Database: db,
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	release()
	if err != nil {
		e.t.Fatal(err)
	}
	var out bytes.Buffer
	var st *runnerpb.DumpStats
	var res *runnerpb.DumpResult
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			e.t.Fatal(err)
		}
		out.Write(m.GetChunk().GetData())
		if m.GetStats() != nil {
			st = m.GetStats()
		}
		if m.GetResult() != nil {
			res = m.GetResult()
		}
	}
	if res.GetExitCode() != 0 || st == nil || !st.GetSameSnapshot() {
		e.t.Fatalf("dump result %v stats %v", res, st)
	}
	return out.Bytes(), st
}

// acknowledgeTestTarget stores the target role's current verdict with an
// admin acknowledgement on the job, as an admin's setJobTestRestore does
// (D-170), and returns the verdict.
func (e *pgiEnv) acknowledgeTestTarget(jobID, conn uuid.UUID) connections.Privileges {
	e.t.Helper()
	ctx := e.t.Context()
	target, release, err := e.conns.Target(ctx, conn)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.rc.RPC().Probe(ctx, &runnerpb.ProbeRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target})
	release()
	if err != nil {
		e.t.Fatal(err)
	}
	p := connections.PrivilegesFromRunner(resp.GetPrivileges())
	e.exec(`UPDATE jobs SET test_restore_target_privileges = $2, test_restore_privilege_reasons = $3,
		test_restore_privileged_ack_at = CASE WHEN $4 THEN now() END WHERE id = $1`, jobID, string(p.Status), p.Reasons, p.Gated())
	return p
}

// scratchDBs lists bekup_test_ databases on the target.
func (e *pgiEnv) scratchDBs() string {
	return e.dk.psql(e.dst, pgiUser, `SELECT coalesce(string_agg(datname, ','), '') FROM pg_database WHERE datname LIKE 'bekup\_test\_%'`)
}

func TestIntegrationPGTestRestore(t *testing.T) {
	e := startPGEnv(t)
	rw := e.worker(e.rc.RPC())
	notes := &testNotes{}
	trw, err := restore.NewTestRestoreWorker(restore.TestRestoreWorkerOptions{Restores: rw, Notifier: notes})
	if err != nil {
		t.Fatal(err)
	}
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	e.exec(`UPDATE jobs SET connection_id = $2, test_restore_enabled = true, test_restore_connection_id = $3,
		test_restore_next_at = now() - interval '1 minute' WHERE id = $1`, j.id, e.srcConn, e.dstConn)
	pgCustom := seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "16"}
	appDump, appStats := e.dumpWithStats("app")
	// The harness connects as the bootstrap superuser: a gated target
	// that only runs with the stored admin acknowledgement (D-170).
	verdict := e.acknowledgeTestTarget(j.id, e.dstConn)

	seed := func(dump []byte, st *runnerpb.DumpStats) {
		t.Helper()
		e.seq++
		s := e.seedAs(j, e.diskID, e.disk, dump, time.Now().Add(time.Duration(e.seq)*time.Minute), pgCustom)
		if st == nil {
			return
		}
		canon, err := jobs.CanonicalStats(engine.KindPostgres, "app", st)
		if err != nil {
			t.Fatal(err)
		}
		e.exec(`INSERT INTO backup_stats (run_item_id, run_id, job_id, level, consistency, tables_total, stats)
			SELECT id, run_id, $2, 'deep', 'snapshot', $4, $3 FROM run_items WHERE run_id = $1`, s.run, j.id, canon, int32(st.GetTablesTotal())) //nolint:gosec // test data
	}
	te := &trEnv{env: e.env, job: j}
	work := func(ctx context.Context, id uuid.UUID) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		te.worker = trw
		return te.work(ctx, id)
	}
	queued := func() uuid.UUID {
		t.Helper()
		id := uuid.New()
		e.exec(`INSERT INTO test_restores (id, job_id, target_connection_id, trigger, river_job_id) VALUES ($1, $2, $3, 'manual', 1)`, id, j.id, e.dstConn)
		return id
	}

	t.Run("scheduled test restore passes", func(t *testing.T) {
		seed(appDump, appStats)
		n, err := e.svc.ScheduleTestRestores(t.Context(), &fakeInserter{})
		if err != nil || n != 1 {
			t.Fatalf("scheduled %d: %v", n, err)
		}
		var id uuid.UUID
		if err := e.pool.QueryRow(t.Context(), `SELECT id FROM test_restores WHERE job_id = $1 AND status = 'queued'`, j.id).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusPassed || len(items) != 1 || !items[0].ScratchDropped {
			d, perr := restore.ParseDiff(items[0].Diff)
			t.Fatalf("test restore %+v items %+v diff %+v (%v)\n%s", tr, items, d, perr, (&wenv{env: e.env}).logs(*items[0].RestoreID))
		}
		d, err := restore.ParseDiff(items[0].Diff)
		if err != nil || d.Comparison == nil || d.Comparison.ExactTables != 1 || d.Comparison.TablesCompared != 1 {
			t.Fatalf("comparison %+v", d.Comparison)
		}
		if left := e.scratchDBs(); left != "" {
			t.Fatalf("leftover scratch databases: %s", left)
		}
		if notes.last().Event != restore.NotifyTestPassed {
			t.Fatalf("notification %+v", notes.last())
		}
	})

	t.Run("a source without the public schema passes", func(t *testing.T) {
		e.dk.psql(e.src, "postgres", "CREATE DATABASE nopublic")
		e.dk.psql(e.src, "nopublic", "DROP SCHEMA public; CREATE SCHEMA app; CREATE TABLE app.t (id int); INSERT INTO app.t SELECT generate_series(1, 5)")
		dump, st := e.dumpWithStats("nopublic")
		seed(dump, st)
		id := queued()
		if err := work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusPassed || len(items) != 1 || !items[0].ScratchDropped {
			d, perr := restore.ParseDiff(items[0].Diff)
			t.Fatalf("test restore %+v items %+v diff %+v (%v)", tr, items, d.Comparison, perr)
		}
	})

	t.Run("an unacknowledged privileged target is refused", func(t *testing.T) {
		if !verdict.Gated() {
			t.Skip("the harness role is not privileged")
		}
		e.exec(`UPDATE jobs SET test_restore_privileged_ack_at = NULL WHERE id = $1`, j.id)
		defer e.exec(`UPDATE jobs SET test_restore_privileged_ack_at = now() WHERE id = $1`, j.id)
		seed(appDump, appStats)
		id := queued()
		if err := work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || tr.ErrorCode == nil || *tr.ErrorCode != restore.CodeTestPrivilegedTarget || len(items) != 0 {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
	})

	t.Run("tampered stats fail with a diff", func(t *testing.T) {
		tampered := pgTampered(appStats)
		seed(appDump, tampered)
		id := queued()
		if err := work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		d, err := restore.ParseDiff(items[0].Diff)
		if err != nil || tr.Status != restore.TRStatusFailed || d.Comparison == nil || len(d.Comparison.Findings) != 1 ||
			d.Comparison.Findings[0].Code != restore.FindRowCount || d.Comparison.Findings[0].Object != "public.items" {
			t.Fatalf("test restore %+v diff %+v", tr, d.Comparison)
		}
		if left := e.scratchDBs(); left != "" {
			t.Fatalf("leftover scratch databases: %s", left)
		}
	})

	t.Run("a failed restore drops the partial scratch database", func(t *testing.T) {
		seed(appDump[:len(appDump)/2], appStats)
		id := queued()
		if err := work(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		tr, items := te.result(id)
		if tr.Status != restore.TRStatusError || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		if left := e.scratchDBs(); left != "" {
			t.Fatalf("leftover scratch databases: %s", left)
		}
	})

	t.Run("cancel drops the scratch database", func(t *testing.T) {
		seed(e.dump("big"), nil)
		id := queued()
		// Streaming runner calls need a deadline (River sets the queue
		// timeout).
		dctx, dcancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer dcancel()
		ctx, cancel := context.WithCancelCause(dctx)
		defer cancel(nil)
		cw, err := restore.NewTestRestoreWorker(restore.TestRestoreWorkerOptions{
			Restores: e.worker(&cancelingRunner{RunnerServiceClient: e.rc.RPC(), after: 3, cancel: cancel}),
		})
		if err != nil {
			t.Fatal(err)
		}
		te.worker = cw
		err = te.work(ctx, id)
		var jc *rivertype.JobCancelError
		tr, items := te.result(id)
		if !errors.As(err, &jc) {
			t.Fatalf("err = %v: %+v %+v\n%s", err, tr, items, (&wenv{env: e.env}).logs(*items[0].RestoreID))
		}
		if tr.Status != restore.TRStatusCanceled || len(items) != 1 || !items[0].ScratchDropped {
			t.Fatalf("test restore %+v items %+v", tr, items)
		}
		if left := e.scratchDBs(); left != "" {
			t.Fatalf("leftover scratch databases: %s", left)
		}
	})

	t.Run("the scratch name is derived by the runner", func(t *testing.T) {
		ctx := t.Context()
		for _, id := range []string{"app", "../app", "0123456789AB", "bekup_test_0123456789ab", "0123456789ab;"} {
			target, release, err := e.conns.Target(ctx, e.dstConn)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.rc.RPC().DropScratchDatabase(ctx, &runnerpb.DropScratchDatabaseRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target, TempId: id})
			release()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("drop %q: %v", id, err)
			}
			target, release, err = e.conns.Target(ctx, e.dstConn)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.rc.RPC().ScratchStats(ctx, &runnerpb.ScratchStatsRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target, TempId: id,
				Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
			release()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("stats %q: %v", id, err)
			}
		}
		target, release, err := e.conns.Target(ctx, e.dstConn)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := e.rc.RPC().DropScratchDatabase(ctx, &runnerpb.DropScratchDatabaseRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target, TempId: "0123456789ab"})
		release()
		if err != nil || resp.GetExisted() {
			t.Fatalf("drop of a missing scratch database: %v %v", resp, err)
		}
		if n := e.dk.psql(e.dst, pgiUser, `SELECT count(*) FROM pg_database WHERE datname IN ('app', 'big')`); n != "2" {
			t.Fatalf("target databases: %s", n)
		}
	})
}

// pgTampered is st with one row more in every exact count.
func pgTampered(st *runnerpb.DumpStats) *runnerpb.DumpStats {
	out := &runnerpb.DumpStats{Level: st.GetLevel(), SameSnapshot: st.GetSameSnapshot(), Objects: st.GetObjects(), TablesTotal: st.GetTablesTotal()}
	for _, tb := range st.GetTables() {
		c := &runnerpb.TableStats{Schema: tb.GetSchema(), Name: tb.GetName(), Rows: tb.GetRows(), Count: tb.GetCount(), Approximate: tb.GetApproximate()}
		if c.Count == runnerpb.RowCount_ROW_COUNT_EXACT {
			c.Rows++
		}
		out.Tables = append(out.Tables, c)
	}
	return out
}
