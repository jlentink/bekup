package dbprobe

import (
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Every PostgreSQL preset statement and the layout check are
// schema-qualified like every other statement of a session (D-152).
func TestPGVersionQueriesQualified(t *testing.T) {
	s, err := engine.QuotePGObject("app schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range schemaversion.Presets() {
		q := p.QueryFor(engine.KindPostgres)
		tb, err := engine.QuotePGObject(q.Table)
		if err != nil {
			t.Fatal(err)
		}
		if why := pgUnqualified(q.Statement(s + "." + tb)); why != "" {
			t.Errorf("%s: %s", p.Name, why)
		}
	}
	for name, q := range map[string]string{
		"layout": pgVersionLayoutQuery, "begin": pgVersionBegin(), "end": pgVersionEnd,
		"describe": `SELECT "a", "b" FROM "s"."t" LIMIT 0`, // the LOCK statement has no operator (TestPostgresVersionPresetFlow)
	} {
		if why := pgUnqualified(q); why != "" {
			t.Errorf("%s: %s", name, why)
		}
	}
	if b := pgVersionBegin(); !strings.Contains(b, "statement_timeout TO 5000") || !strings.Contains(b, "lock_timeout TO 2000") ||
		!strings.Contains(b, "idle_in_transaction_session_timeout TO 30000") || !strings.HasPrefix(b, "SAVEPOINT bekup_version") {
		t.Errorf("begin %q", b)
	}
	if got := string(pgArray([]string{"version", "dirty"})); got != `{"version","dirty"}` {
		t.Errorf("array %q", got)
	}
}

func TestMySQLVersionLimits(t *testing.T) {
	my := myVersionLimits(engine.KindMySQL)
	maria := myVersionLimits(engine.KindMariaDB)
	if len(my) != 2 || my[0] != "SET SESSION max_execution_time = 5000" || my[1] != "SET SESSION lock_wait_timeout = 2" {
		t.Errorf("mysql %q", my)
	}
	if len(maria) != 2 || maria[0] != "SET SESSION max_statement_time = 5" || maria[1] != "SET SESSION lock_wait_timeout = 2" {
		t.Errorf("mariadb %q", maria)
	}
}

func strp(s string) *string { return &s }

func TestVersionValue(t *testing.T) {
	gm, _ := schemaversion.Lookup("golang-migrate")
	for _, tc := range []struct {
		q    VersionQuery
		cols []*string
		want VersionResult
	}{
		{VersionQuery{Preset: gm}, []*string{strp("7"), strp("t")}, VersionResult{Value: "7 (dirty)", Status: schemaversion.StatusOK}},
		{VersionQuery{Preset: gm}, []*string{nil, strp("f")}, VersionResult{Status: schemaversion.StatusNone, Detail: "null"}},
		{VersionQuery{Custom: "SELECT 1"}, []*string{strp(" v1.2 "), strp("ignored")}, VersionResult{Value: "v1.2", Status: schemaversion.StatusOK}},
		{VersionQuery{Custom: "SELECT 1"}, []*string{strp(strings.Repeat("9", 65))}, VersionResult{Status: schemaversion.StatusInvalid, Detail: "value"}},
		{VersionQuery{Custom: "SELECT 1"}, []*string{strp("\x00\x01")}, VersionResult{Status: schemaversion.StatusInvalid, Detail: "value"}},
		{VersionQuery{Custom: "SELECT 1"}, []*string{strp("")}, VersionResult{Status: schemaversion.StatusNone, Detail: "value"}},
	} {
		if got := versionValue(tc.q, tc.cols); got != tc.want {
			t.Errorf("%v: got %+v, want %+v", tc.cols, got, tc.want)
		}
	}
}

// Without restrict_nonsystem_relation_kind nothing is sent for a version
// query (a read could run a view's code).
func TestPostgresVersionRefusedWithoutRestriction(t *testing.T) {
	q := make(chan string, 16)
	addr := fakePGWith(t, fakePGOpts{want: testPassword, noRestrict: true, queries: q})
	c, err := Open(t.Context(), params(engine.KindPostgres, dialTo(addr)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	for len(q) > 0 {
		<-q
	}
	src, ok := c.(StatsSource)
	if !ok {
		t.Fatal("no stats source")
	}
	gm, _ := schemaversion.Lookup("golang-migrate")
	r := src.SchemaVersion(t.Context(), "app", VersionQuery{Preset: gm, Schema: "public"})
	if r.Status != schemaversion.StatusUnavailable || r.Detail != "unsafe server" {
		t.Fatalf("result %+v", r)
	}
	if len(q) != 0 {
		t.Fatalf("statement sent: %q", <-q)
	}
}

func TestPGBuiltinTypeOIDs(t *testing.T) {
	if len(pgBuiltinTypeOIDs) != len(schemaversion.TypeAllowedPG) {
		t.Fatalf("%d OIDs for %d types", len(pgBuiltinTypeOIDs), len(schemaversion.TypeAllowedPG))
	}
	for _, name := range pgBuiltinTypeOIDs {
		if !slices.Contains(schemaversion.TypeAllowedPG, name) {
			t.Errorf("%s not allowed", name)
		}
	}
}

func TestMyCustomStatement(t *testing.T) {
	if got := myCustomStatement("SELECT v FROM t"); got != "SELECT * FROM (\nSELECT v FROM t\n) AS bekup_version LIMIT 1" {
		t.Errorf("got %q", got)
	}
}

func TestVersionValueRawBound(t *testing.T) {
	gm, _ := schemaversion.Lookup("golang-migrate")
	long := strings.Repeat(" ", schemaversion.MaxRawValueBytes) + "1"
	if r := versionValue(VersionQuery{Preset: gm}, []*string{&long, nil}); r.Status != schemaversion.StatusInvalid {
		t.Errorf("raw bound: %+v", r)
	}
	if r := versionValue(VersionQuery{Custom: "SELECT 1"}, []*string{&long}); r.Status != schemaversion.StatusInvalid {
		t.Errorf("raw bound custom: %+v", r)
	}
}

// The read budget ends reads past it and is lifted again.
func TestBudgetConn(t *testing.T) {
	a, b := net.Pipe()
	defer func() { discardErr(a.Close()) }()
	defer func() { discardErr(b.Close()) }()
	bc := newBudgetConn(a)
	go func() {
		for range 2 {
			if _, err := b.Write(make([]byte, 64)); err != nil {
				return
			}
		}
	}()
	buf := make([]byte, 100)
	bc.arm(10)
	if n, err := bc.Read(buf); err != nil || n != 10 {
		t.Fatalf("first read %d %v", n, err)
	}
	if _, err := bc.Read(buf); !errors.Is(err, errReadBudget) {
		t.Fatalf("over budget: %v", err)
	}
	bc.disarm()
	if n, err := bc.Read(buf); err != nil || n == 0 {
		t.Fatalf("after disarm %d %v", n, err)
	}
	var nilBudget *budgetConn
	nilBudget.arm(1) // no-op without a budget
	nilBudget.disarm()
}

// versionSession opens a fake PostgreSQL session for a capture and drains
// the hardening statements.
func versionSession(t *testing.T, o fakePGOpts) (StatsSource, chan string) {
	t.Helper()
	q := make(chan string, 64)
	o.want, o.queries = testPassword, q
	addr := fakePGWith(t, o)
	c, err := Open(t.Context(), params(engine.KindPostgres, dialTo(addr)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardErr(c.Close()) })
	for len(q) > 0 {
		<-q
	}
	src, ok := c.(StatsSource)
	if !ok {
		t.Fatal("no stats source")
	}
	return src, q
}

func drain(q chan string) []string {
	var out []string
	for len(q) > 0 {
		out = append(out, <-q)
	}
	return out
}

// A preset capture locks its table, checks the snapshot's catalog and the
// current result types, reads, and always rolls the savepoint back.
func TestPostgresVersionPresetFlow(t *testing.T) {
	gm, _ := schemaversion.Lookup("golang-migrate")
	for _, tc := range []struct {
		name   string
		oids   []uint32
		status schemaversion.Status
		value  string
		detail string
	}{
		{"builtin types", []uint32{20, 16}, schemaversion.StatusOK, "42 (dirty)", ""},
		{"user type", []uint32{20, 16384}, schemaversion.StatusUnavailable, "", "layout: column type"},
		{"column count", []uint32{20}, schemaversion.StatusUnavailable, "", "layout: columns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, q := versionSession(t, fakePGOpts{answer: func(sql string) ([][]string, []uint32, bool) {
				switch {
				case strings.Contains(sql, "pg_catalog.has_table_privilege"):
					return [][]string{{"r", "t", "2"}}, nil, true
				case strings.HasSuffix(sql, " LIMIT 0"):
					return nil, tc.oids, true
				case strings.Contains(sql, "pg_catalog.left("):
					return [][]string{{"42", "t"}}, []uint32{25, 16}, true
				}
				return nil, nil, false
			}})
			r := src.SchemaVersion(t.Context(), "app", VersionQuery{Preset: gm, Schema: "public"})
			if r.Status != tc.status || r.Value != tc.value || (tc.detail != "" && r.Detail != tc.detail) || r.SessionLost {
				t.Fatalf("result %+v", r)
			}
			got := drain(q)
			if len(got) < 4 || !strings.HasPrefix(got[0], "SAVEPOINT bekup_version") ||
				got[1] != `LOCK TABLE "public"."schema_migrations" IN ACCESS SHARE MODE` ||
				!strings.Contains(got[2], "pg_catalog.has_table_privilege") ||
				got[3] != `SELECT "version", "dirty" FROM "public"."schema_migrations" LIMIT 0` || got[len(got)-1] != pgVersionEnd {
				t.Fatalf("statements %q", got)
			}
			if read := slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "pg_catalog.left(") }); read != (tc.status == schemaversion.StatusOK) {
				t.Fatalf("read statement sent: %v (%q)", read, got)
			}
		})
	}
}

// A failed savepoint rollback marks the session lost; a failed begin is
// rolled back and leaves the session usable.
func TestPostgresVersionSessionLost(t *testing.T) {
	src, _ := versionSession(t, fakePGOpts{failSimple: func(s string) bool { return strings.HasPrefix(s, "ROLLBACK TO SAVEPOINT") }})
	r := src.SchemaVersion(t.Context(), "app", VersionQuery{Custom: "SELECT 1"})
	if !r.SessionLost || r.Status != schemaversion.StatusUnavailable {
		t.Fatalf("rollback failure: %+v", r)
	}
	src, q := versionSession(t, fakePGOpts{failSimple: func(s string) bool { return strings.HasPrefix(s, "SAVEPOINT") }})
	r = src.SchemaVersion(t.Context(), "app", VersionQuery{Custom: "SELECT 1"})
	if r.SessionLost || r.Status != schemaversion.StatusUnavailable || r.Detail != "savepoint" {
		t.Fatalf("begin failure: %+v", r)
	}
	if got := drain(q); len(got) != 2 || got[1] != pgVersionEnd {
		t.Fatalf("statements %q", got)
	}
}

// A custom query is wrapped, only its first column is read, and a row
// larger than the protocol cap breaks the session instead of being
// buffered.
func TestPostgresVersionCustom(t *testing.T) {
	src, q := versionSession(t, fakePGOpts{answer: func(sql string) ([][]string, []uint32, bool) {
		if strings.Contains(sql, "bekup_version LIMIT 1") {
			return [][]string{{"v7", strings.Repeat("x", 1000)}}, nil, true
		}
		return nil, nil, false
	}})
	r := src.SchemaVersion(t.Context(), "app", VersionQuery{Custom: "SELECT v, w FROM public.t"})
	if r.Status != schemaversion.StatusOK || r.Value != "v7" {
		t.Fatalf("result %+v", r)
	}
	if got := drain(q); len(got) != 3 || got[1] != "SELECT * FROM (\nSELECT v, w FROM public.t\n) bekup_version LIMIT 1" {
		t.Fatalf("statements %q", got)
	}
	big, _ := versionSession(t, fakePGOpts{answer: func(sql string) ([][]string, []uint32, bool) {
		if strings.Contains(sql, "bekup_version LIMIT 1") {
			return [][]string{{strings.Repeat("x", pgMaxMessageBytes+1)}}, nil, true
		}
		return nil, nil, false
	}})
	r = big.SchemaVersion(t.Context(), "app", VersionQuery{Custom: "SELECT v FROM public.t"})
	if r.Status != schemaversion.StatusUnavailable || !r.SessionLost {
		t.Fatalf("oversized row: %+v", r)
	}
}
