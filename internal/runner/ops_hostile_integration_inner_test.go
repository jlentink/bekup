//go:build integration

package runner

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// A hostile target database (D-152): its non-superuser owner shadows
// count(*), the comparison/concatenation operators and catalog functions
// in its own schema, points the database's search_path at it, protects a
// table with a row-level security policy calling its function and adds a
// view that calls it. Every trap RAISEs a WARNING with a marker into the
// server log (that works inside READ ONLY transactions too). Stats
// captures and dumps through the runner, as a superuser and as a plain
// read-only role, must never fire a trap and must still report correct
// numbers.

const hostileMarker = "bekup-trap-fired"

const hostileSetup = `CREATE SCHEMA evil;
CREATE FUNCTION evil.fire(what text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN RAISE WARNING 'bekup-trap-fired: % as % superuser=%', what, current_user, pg_catalog.current_setting('is_superuser'); END $$;
CREATE FUNCTION evil.cnt_step(s bigint) RETURNS bigint LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('count'); RETURN s + 1; END $$;
CREATE AGGREGATE evil.count(*) (sfunc = evil.cnt_step, stype = bigint, initcond = '424242');
CREATE FUNCTION evil.name_eq(a name, b name) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('name ='); RETURN a OPERATOR(pg_catalog.=) b; END $$;
CREATE OPERATOR evil.= (LEFTARG = name, RIGHTARG = name, FUNCTION = evil.name_eq);
CREATE FUNCTION evil.name_ne(a name, b name) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('name <>'); RETURN a OPERATOR(pg_catalog.<>) b; END $$;
CREATE OPERATOR evil.<> (LEFTARG = name, RIGHTARG = name, FUNCTION = evil.name_ne);
CREATE FUNCTION evil.oid_eq(a oid, b oid) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('oid ='); RETURN a OPERATOR(pg_catalog.=) b; END $$;
CREATE OPERATOR evil.= (LEFTARG = oid, RIGHTARG = oid, FUNCTION = evil.oid_eq);
CREATE FUNCTION evil.char_eq(a "char", b "char") RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('char ='); RETURN a OPERATOR(pg_catalog.=) b; END $$;
CREATE OPERATOR evil.= (LEFTARG = "char", RIGHTARG = "char", FUNCTION = evil.char_eq);
CREATE FUNCTION evil.char_ne(a "char", b "char") RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('char <>'); RETURN a OPERATOR(pg_catalog.<>) b; END $$;
CREATE OPERATOR evil.<> (LEFTARG = "char", RIGHTARG = "char", FUNCTION = evil.char_ne);
CREATE FUNCTION evil.name_nre(a name, b text) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('!~'); RETURN a OPERATOR(pg_catalog.!~) b; END $$;
CREATE OPERATOR evil.!~ (LEFTARG = name, RIGHTARG = text, FUNCTION = evil.name_nre);
CREATE FUNCTION evil.text_cat(a text, b text) RETURNS text LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('||'); RETURN a OPERATOR(pg_catalog.||) b; END $$;
CREATE OPERATOR evil.|| (LEFTARG = text, RIGHTARG = text, FUNCTION = evil.text_cat);
CREATE FUNCTION evil.has_table_privilege(o oid, p text) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('has_table_privilege'); RETURN true; END $$;
CREATE FUNCTION evil.current_setting(n text) RETURNS text LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('current_setting'); RETURN pg_catalog.current_setting(n); END $$;
CREATE FUNCTION evil.view_val() RETURNS int LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('view'); RETURN 1; END $$;
CREATE FUNCTION evil.tenant_ok(t text) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM evil.fire('policy'); RETURN true; END $$;
CREATE TABLE public.items (id int); INSERT INTO public.items SELECT pg_catalog.generate_series(1, 37);
CREATE TABLE public.tenant (id int, tenant text); INSERT INTO public.tenant VALUES (1, 'a'), (2, 'b');
ALTER TABLE public.tenant ENABLE ROW LEVEL SECURITY;
CREATE POLICY p ON public.tenant USING (evil.tenant_ok(tenant));
CREATE VIEW public.trapview AS SELECT evil.view_val() AS x;
DO $$ BEGIN FOR i IN 1..16 LOOP EXECUTE pg_catalog.format('CREATE TABLE public.f%s AS SELECT pg_catalog.generate_series(1, %s) AS id', i, i); END LOOP; END $$;
GRANT USAGE ON SCHEMA evil TO hostro; GRANT SELECT ON ALL TABLES IN SCHEMA public TO hostro;
ALTER DATABASE hostiledb SET search_path = evil, pg_catalog;
ALTER DATABASE hostiledb SET row_security = on`

// hostileFired counts the trap markers in a target's server log.
func (in *itInner) hostileFired(name string) int {
	time.Sleep(time.Second) // let the server flush its log
	return strings.Count(itLogs(in.dc, in.h.Targets[name].Container), hostileMarker)
}

func (in *itInner) testHostile(t *testing.T) {
	for _, name := range []string{"pg13", "pg16", "pg18"} {
		t.Run(name, func(t *testing.T) { in.testHostileOn(t, name) })
	}
}

func (in *itInner) testHostileOn(t *testing.T, name string) {
	tg := in.h.Targets[name]
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	in.sql(t, name, "postgres", "CREATE ROLE evilowner LOGIN; CREATE ROLE hostro LOGIN PASSWORD '"+pw+"'; "+
		"GRANT CONNECT ON DATABASE postgres TO hostro")
	in.sql(t, name, "postgres", "CREATE DATABASE hostiledb OWNER evilowner")
	itExec(t, in.dc, tg.Container, "psql", "-U", "evilowner", "-d", "hostiledb", "-At", "-v", "ON_ERROR_STOP=1", "-c", hostileSetup)

	// Positive control: an ordinary session of the superuser and of the
	// read-only role do run the traps.
	before := in.hostileFired(name)
	got := in.sql(t, name, "hostiledb", "SELECT count(*) FROM pg_catalog.pg_class")
	live := false
	for _, line := range strings.Split(got, "\n") { // stdout and stderr interleave
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && n >= 424242 {
			live = true
		}
	}
	if !live {
		t.Fatalf("control: the count(*) shadow is not live: %q", got)
	}
	itExec(t, in.dc, tg.Container, "psql", "-U", "hostro", "-d", "hostiledb", "-At", "-c", "SELECT 1 FROM public.tenant")
	base := in.hostileFired(name)
	if base <= before {
		t.Fatalf("control: no trap fired (%d -> %d)", before, base)
	}

	t.Run("dbprobe", func(t *testing.T) { in.hostileProbe(t, name) })
	if n := in.hostileFired(name); n != base {
		t.Fatalf("dbprobe: %d trap(s) fired:\n%s", n-base, itLogs(in.dc, tg.Container))
	}

	t.Run("superuser", func(t *testing.T) {
		d, st, err := in.dumpWithStats(t, in.target(name), "hostiledb", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_DEEP, 0), nil)
		if err != nil || st == nil || d.result.GetExitCode() != 0 {
			t.Fatalf("dump: %v %v %v logs %v", d.result, st, err, d.logs)
		}
		if !st.GetSameSnapshot() || st.GetTablesTotal() != 18 || len(st.GetWarnings()) != 0 {
			t.Fatalf("stats %v", st)
		}
		if o := st.GetObjects(); o.GetTables() != 18 || o.GetViews() != 1 || o.GetSchemas() != 2 {
			t.Fatalf("objects %v", o)
		}
		// A superuser bypasses RLS: the policy is not evaluated at all.
		for tbl, rows := range map[string]int64{"items": 37, "tenant": 2, "f1": 1, "f16": 16} {
			if ts := statsTable(st, "public", tbl); ts.GetCount() != runnerpb.RowCount_ROW_COUNT_EXACT || ts.GetRows() != rows {
				t.Fatalf("%s: %v", tbl, ts)
			}
		}
	})
	if n := in.hostileFired(name); n != base {
		t.Fatalf("superuser stats/dump: %d trap(s) fired:\n%s", n-base, itLogs(in.dc, tg.Container))
	}

	t.Run("readonly_role", func(t *testing.T) {
		rt := in.target(name)
		rt.User = "hostro"
		// pg_dump itself refuses the RLS table (row_security off): only
		// the stats matter here.
		_, st, err := in.dumpWithStats(t, rt, "hostiledb", statsOpts(runnerpb.StatsLevel_STATS_LEVEL_DEEP, 0), nil)
		if st == nil {
			t.Fatalf("no stats: %v", err)
		}
		if ts := statsTable(st, "public", "tenant"); ts.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
			t.Fatalf("RLS table counted: %v", ts)
		}
		if w := statsWarn(st, statsWarnPrivileges); w == nil || !slices.Contains(w.GetObjects(), "public.tenant") {
			t.Fatalf("warnings %v", st.GetWarnings())
		}
		if ts := statsTable(st, "public", "items"); ts.GetCount() != runnerpb.RowCount_ROW_COUNT_EXACT || ts.GetRows() != 37 {
			t.Fatalf("items %v", ts)
		}
	})
	if n := in.hostileFired(name); n != base {
		t.Fatalf("read-only role stats/dump: %d trap(s) fired:\n%s", n-base, itLogs(in.dc, tg.Container))
	}
}

// hostileProbe drives dbprobe directly: the per-database settings do not
// override the startup pins, a count that names a view (a table swapped
// for a view after the catalog read) is refused by the server, and an RLS
// table is an error for a role the policy applies to.
func (in *itInner) hostileProbe(t *testing.T, name string) {
	tg := in.h.Targets[name]
	open := func(user string) dbprobe.StatsSource {
		t.Helper()
		c, err := dbprobe.Open(in.ctx(t, time.Minute), dbprobe.Params{
			Kind: engine.KindPostgres, Host: tg.Host, Port: tg.Port, User: user, Password: []byte(in.h.Password),
			Database: "hostiledb", TLS: dbprobe.UnverifiedTLS(tg.Host),
			Dial: func(ctx context.Context) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", net.JoinHostPort(tg.Host, strconv.Itoa(tg.Port)))
			},
		})
		if err != nil {
			t.Fatalf("open as %s: %v", user, err)
		}
		t.Cleanup(func() { itIgnore(c.Close()) })
		src, ok := c.(dbprobe.StatsSource)
		if !ok || !src.SafeRowCounts() {
			t.Fatalf("%s: restrict_nonsystem_relation_kind not in effect", name)
		}
		if _, err := src.BeginStats(in.ctx(t, time.Minute), true); err != nil {
			t.Fatalf("begin as %s: %v", user, err)
		}
		return src
	}
	ctx := in.ctx(t, 2*time.Minute)
	su := open(itPGUser)
	if n, err := su.CountRows(ctx, "", dbprobe.Table{Schema: "public", Name: "items"}, 10*time.Second); err != nil || n != 37 {
		t.Fatalf("items: %d %v", n, err)
	}
	if n, err := su.CountRows(ctx, "", dbprobe.Table{Schema: "public", Name: "trapview"}, 10*time.Second); err == nil {
		t.Fatalf("a count over a view succeeded: %d", n)
	}
	if n, err := su.CountRows(ctx, "", dbprobe.Table{Schema: "public", Name: "tenant"}, 10*time.Second); err != nil || n != 2 {
		t.Fatalf("tenant as superuser: %d %v", n, err)
	}
	cat, err := su.Catalog(ctx, "hostiledb")
	if err != nil || cat.TablesTotal != 18 {
		t.Fatalf("catalog: %v %v", cat, err)
	}
	ro := open("hostro")
	if _, err := ro.CountRows(ctx, "", dbprobe.Table{Schema: "public", Name: "tenant"}, 10*time.Second); dbprobe.ReasonOf(err) != runnerpb.ReasonPermission {
		t.Fatalf("tenant as hostro: %v", err)
	}
	if n, err := ro.CountRows(ctx, "", dbprobe.Table{Schema: "public", Name: "items"}, 10*time.Second); err != nil || n != 37 {
		t.Fatalf("items as hostro after the refused count: %d %v", n, err)
	}
	if _, _, err := ro.GlobalCounts(ctx); err != nil {
		t.Fatalf("globals: %v", err)
	}
}
