//go:build integration

package runner

import (
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Completeness evidence against real servers (task 3.10): the PostgreSQL
// stats share the dump's snapshot, privilege, timeout and
// non-transactional warnings fire, MySQL/MariaDB counts are approximate.

func statsOpts(level runnerpb.StatsLevel, perTableMs uint32) *runnerpb.StatsOptions {
	return &runnerpb.StatsOptions{Enabled: true, Level: level, PerTableTimeoutMs: perTableMs}
}

// dumpWithStats runs a Dump with stats; onStats runs when the stats
// message arrives (before any data).
func (in *itInner) dumpWithStats(t *testing.T, rt *runnerpb.Target, db string, o *runnerpb.StatsOptions, onStats func(*runnerpb.DumpStats)) (dumpOut, *runnerpb.DumpStats, error) {
	t.Helper()
	var out dumpOut
	var st *runnerpb.DumpStats
	s, err := in.rpc.Dump(in.ctx(t, 5*time.Minute), &runnerpb.DumpRequest{Ctx: in.rc(), Target: rt, Database: db, Stats: o})
	if err != nil {
		return out, nil, err
	}
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return out, st, nil
		}
		if err != nil {
			return out, st, err
		}
		switch {
		case m.GetStats() != nil:
			if st != nil || len(out.data) > 0 {
				t.Fatal("stats after data or twice")
			}
			st = m.GetStats()
			if onStats != nil {
				onStats(st)
			}
		case m.GetChunk() != nil:
			out.data = append(out.data, m.GetChunk().GetData()...)
		case m.GetLog() != nil:
			out.logs = append(out.logs, m.GetLog().GetText())
		case m.GetResult() != nil:
			out.result = m.GetResult()
		}
	}
}

func statsTable(st *runnerpb.DumpStats, schema, name string) *runnerpb.TableStats {
	for _, ts := range st.GetTables() {
		if ts.GetSchema() == schema && ts.GetName() == name {
			return ts
		}
	}
	return nil
}

func statsWarn(st *runnerpb.DumpStats, code string) *runnerpb.StatsWarning {
	for _, w := range st.GetWarnings() {
		if w.GetCode() == code {
			return w
		}
	}
	return nil
}

func (in *itInner) testStats(t *testing.T) {
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	in.sql(t, "pg16", "postgres", "CREATE DATABASE statsdb")
	in.sql(t, "pg16", "statsdb", `CREATE SCHEMA s1; CREATE TABLE s1.a (id int PRIMARY KEY);
INSERT INTO s1.a SELECT g FROM generate_series(1, 1000) g;
CREATE TABLE public."Odd ""name"" t" (x int); INSERT INTO public."Odd ""name"" t" VALUES (1), (2), (3);
CREATE TABLE public.hidden (x int); INSERT INTO public.hidden SELECT g FROM generate_series(1, 5) g;
CREATE VIEW public.v AS SELECT count(*) FROM s1.a; CREATE SEQUENCE public.seq1;
CREATE FUNCTION public.f() RETURNS int LANGUAGE sql AS 'SELECT 1'`)
	in.sql(t, "pg16", "statsdb", "CREATE ROLE statsro LOGIN PASSWORD '"+pw+"'; GRANT CONNECT ON DATABASE postgres TO statsro; "+
		`GRANT USAGE ON SCHEMA s1 TO statsro; GRANT SELECT ON s1.a, public."Odd ""name"" t", public.seq1 TO statsro`)

	t.Run("pg_same_snapshot", func(t *testing.T) {
		d, st, err := in.dumpWithStats(t, in.target("pg16"), "statsdb", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_DEEP, 0), func(*runnerpb.DumpStats) {
			// Written after the snapshot was exported: neither the stats
			// nor the dump may see these rows.
			in.sql(t, "pg16", "statsdb", "INSERT INTO s1.a SELECT g FROM generate_series(1001, 1250) g")
		})
		if err != nil || d.result.GetExitCode() != 0 || st == nil {
			t.Fatalf("dump: %v %v %v logs %v", d.result, st, err, d.logs)
		}
		if !st.GetSameSnapshot() || st.GetTablesTotal() != 3 || len(st.GetWarnings()) != 0 {
			t.Fatalf("stats %v", st)
		}
		o := st.GetObjects()
		if o.GetSchemas() != 2 || o.GetTables() != 3 || o.GetViews() != 1 || o.GetSequences() != 1 || o.GetFunctions() != 1 {
			t.Fatalf("objects %v", o)
		}
		a := statsTable(st, "s1", "a")
		odd := statsTable(st, "public", `Odd "name" t`)
		if a.GetCount() != runnerpb.RowCount_ROW_COUNT_EXACT || a.GetRows() != 1000 || odd.GetRows() != 3 || a.GetApproximate() {
			t.Fatalf("tables %v", st.GetTables())
		}
		if err := in.create(t, in.target("pg16"), "statsrestored"); err != nil {
			t.Fatal(err)
		}
		r, logs, err := in.restore(t, in.target("pg16"), "statsrestored", "16", runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data)
		if err != nil || r.GetExitCode() != 0 {
			t.Fatalf("restore: %v %v %v", r, err, logs)
		}
		restored := in.sql(t, "pg16", "statsrestored", "SELECT count(*) FROM s1.a")
		live := in.sql(t, "pg16", "statsdb", "SELECT count(*) FROM s1.a")
		if restored != strconv.FormatInt(a.GetRows(), 10) || live != "1250" {
			t.Fatalf("stats %d, restored %s, live %s", a.GetRows(), restored, live)
		}
		tables := in.sql(t, "pg16", "statsrestored", "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')")
		if tables != strconv.Itoa(int(o.GetTables())) {
			t.Fatalf("restored tables %s, stats %d", tables, o.GetTables())
		}
	})

	t.Run("pg_restricted_role", func(t *testing.T) {
		rt := in.target("pg16")
		rt.User = "statsro"
		_, st, err := in.dumpWithStats(t, rt, "statsdb", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_STANDARD, 0), nil)
		if st == nil {
			t.Fatalf("no stats: %v", err)
		}
		w := statsWarn(st, statsWarnPrivileges)
		if w == nil || !slices.Contains(w.GetObjects(), "public.hidden") || slices.Contains(w.GetObjects(), "s1.a") {
			t.Fatalf("warnings %v", st.GetWarnings())
		}
		if h := statsTable(st, "public", "hidden"); h.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
			t.Fatalf("hidden %v", h)
		}
	})

	t.Run("pg_timeout_unknown", func(t *testing.T) {
		tg := in.h.Targets["pg16"]
		held := make(chan struct{})
		go func() {
			defer close(held)
			itExecCode(t, in.dc, tg.Container, "psql", "-U", itPGUser, "-d", "statsdb", "-c",
				"BEGIN; LOCK TABLE s1.a IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(6); COMMIT")
		}()
		deadline := time.Now().Add(30 * time.Second)
		for in.sql(t, "pg16", "statsdb", "SELECT count(*) FROM pg_locks WHERE relation = 's1.a'::regclass AND mode = 'AccessExclusiveLock' AND granted") != "1" {
			if time.Now().After(deadline) {
				t.Fatal("the lock was not taken")
			}
			time.Sleep(100 * time.Millisecond)
		}
		d, st, err := in.dumpWithStats(t, in.target("pg16"), "statsdb", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_DEEP, 1000), nil)
		<-held
		if err != nil || st == nil || d.result.GetExitCode() != 0 {
			t.Fatalf("dump: %v %v %v %v", d.result, st, err, d.logs)
		}
		if a := statsTable(st, "s1", "a"); a.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
			t.Fatalf("locked table %v", a)
		}
		if w := statsWarn(st, statsWarnTimeout); w == nil || !slices.Contains(w.GetObjects(), "s1.a") {
			t.Fatalf("warnings %v", st.GetWarnings())
		}
		if h := statsTable(st, "public", "hidden"); h.GetCount() != runnerpb.RowCount_ROW_COUNT_EXACT || h.GetRows() != 5 {
			t.Fatalf("other tables still counted: %v", h)
		}
	})

	for _, name := range []string{"mysql84", "maria118"} {
		t.Run(name, func(t *testing.T) {
			// The general log records every statement: the runner must
			// never address a user table (D-153: estimates only).
			in.sql(t, name, "shop", "SET GLOBAL log_output = 'TABLE'; TRUNCATE TABLE mysql.general_log; SET GLOBAL general_log = 'ON'")
			for _, level := range []runnerpb.StatsLevel{runnerpb.StatsLevel_STATS_LEVEL_DEEP, runnerpb.StatsLevel_STATS_LEVEL_STANDARD} {
				d, st, err := in.dumpWithStats(t, in.target(name), "shop", statsOpts(level, 0), nil)
				if err != nil || st == nil || d.result.GetExitCode() != 0 {
					t.Fatalf("dump: %v %v %v", d.result, st, err)
				}
				tt, m := statsTable(st, "", "t"), statsTable(st, "", "m")
				if st.GetSameSnapshot() || st.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_STANDARD ||
					tt.GetCount() != runnerpb.RowCount_ROW_COUNT_ESTIMATE || !tt.GetApproximate() ||
					m.GetCount() != runnerpb.RowCount_ROW_COUNT_ESTIMATE || m.GetRows() != 2 || !m.GetApproximate() {
					t.Fatalf("%v: stats %v", level, st)
				}
				if w := statsWarn(st, statsWarnNonTx); w == nil || !slices.Contains(w.GetObjects(), "m") || slices.Contains(w.GetObjects(), "t") {
					t.Fatalf("warnings %v", st.GetWarnings())
				}
				if st.GetObjects().GetTables() != 2 || st.GetObjects().GetSchemas() != 1 || len(st.GetWarnings()) != 1 {
					t.Fatalf("objects %v warnings %v", st.GetObjects(), st.GetWarnings())
				}
			}
			in.sql(t, name, "shop", "SET GLOBAL general_log = 'OFF'")
			const userTableStmt = "SELECT COUNT(*) FROM mysql.general_log WHERE CONVERT(argument USING utf8mb4) LIKE '%`shop`.%' " +
				"OR CONVERT(argument USING utf8mb4) LIKE '%MAX_EXECUTION_TIME%' OR CONVERT(argument USING utf8mb4) LIKE 'SET STATEMENT%'"
			if n := itLastLine(in.sql(t, name, "shop", userTableStmt)); n != "0" {
				t.Fatalf("%s statement(s) addressed a user table:\n%s", n,
					in.sql(t, name, "shop", "SELECT CONVERT(argument USING utf8mb4) FROM mysql.general_log WHERE CONVERT(argument USING utf8mb4) LIKE '%`shop`.%'"))
			}
			// Positive control: such a statement is logged.
			in.sql(t, name, "shop", "TRUNCATE TABLE mysql.general_log; SET GLOBAL general_log = 'ON'; SELECT COUNT(*) FROM `shop`.`t`; SET GLOBAL general_log = 'OFF'")
			if n := itLastLine(in.sql(t, name, "shop", userTableStmt)); n == "0" {
				t.Fatal("control: the general log did not record the user-table statement")
			}

			// A user that sees m (INSERT) but may not read it: flagged from
			// the grants, not from a probe.
			in.sql(t, name, "shop", "CREATE USER 'statsro'@'%' IDENTIFIED BY '"+pw+"'; GRANT SELECT ON shop.t TO 'statsro'@'%'; "+
				"GRANT INSERT ON shop.m TO 'statsro'@'%'")
			rt := in.target(name)
			rt.User = "statsro"
			// The dump itself fails (no SELECT on m); only the stats matter.
			_, st, err := in.dumpWithStats(t, rt, "shop", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_STANDARD, 0), nil)
			if st == nil {
				t.Fatalf("no stats for the restricted user: %v", err)
			}
			if w := statsWarn(st, statsWarnPrivileges); w == nil || !slices.Contains(w.GetObjects(), "m") || slices.Contains(w.GetObjects(), "t") {
				t.Fatalf("restricted warnings %v", st.GetWarnings())
			}
			if m := statsTable(st, "", "m"); m.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
				t.Fatalf("unreadable m %v", m)
			}
		})
	}
}

// itLastLine drops the client's warning lines before a single result.
func itLastLine(s string) string { return s[strings.LastIndex(s, "\n")+1:] }
