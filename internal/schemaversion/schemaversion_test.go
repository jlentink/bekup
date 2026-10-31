package schemaversion

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
)

func strp(s string) *string { return &s }

// Every preset has a PostgreSQL and a MySQL/MariaDB statement that reads
// only declared columns of its own table, at most one row.
func TestPresetsComplete(t *testing.T) {
	want := []string{"golang-migrate", "goose", "flyway", "liquibase", "rails", "django", "laravel", "alembic", "prisma", "knex", "sequelize"}
	ps := Presets()
	if len(ps) != len(want) {
		t.Fatalf("got %d presets, want %d", len(ps), len(want))
	}
	nameRe := regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	seen := map[string]bool{}
	for i, p := range ps {
		if p.Name != want[i] || !nameRe.MatchString(p.Name) || seen[p.Name] {
			t.Errorf("preset %d: name %q", i, p.Name)
		}
		seen[p.Name] = true
		if p.Label == "" || p.Description == "" || len(p.Engines()) != 3 {
			t.Errorf("%s: label/description/engines missing", p.Name)
		}
		if got, ok := Lookup(p.Name); !ok || got != p {
			t.Errorf("%s: lookup", p.Name)
		}
		for _, k := range []engine.Kind{engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB} {
			q := p.QueryFor(k)
			if q.Table == "" || len(q.Columns) == 0 || !strings.HasPrefix(q.SQL, "SELECT ") || !strings.Contains(q.SQL, TablePlaceholder) {
				t.Errorf("%s/%s: incomplete query %+v", p.Name, k, q)
			}
			if strings.Contains(q.SQL, ";") {
				t.Errorf("%s/%s: semicolon", p.Name, k)
			}
			if !strings.Contains(q.SQL, "LIMIT 1") && !strings.Contains(q.SQL, "string_agg") && !strings.Contains(q.SQL, "GROUP_CONCAT") {
				t.Errorf("%s/%s: may read more than one row", p.Name, k)
			}
			quote := "`"
			if k == engine.KindPostgres {
				quote = `"`
			}
			// Every quoted identifier in the statement is a declared column.
			for _, m := range regexp.MustCompile(regexp.QuoteMeta(quote)+`([^`+quote+`]+)`+regexp.QuoteMeta(quote)).FindAllStringSubmatch(q.SQL, -1) {
				if m[1] == "C" && k == engine.KindPostgres {
					continue // the "C" collation
				}
				found := false
				for _, c := range q.Columns {
					if c == m[1] {
						found = true
					}
				}
				if !found {
					t.Errorf("%s/%s: column %q not declared", p.Name, k, m[1])
				}
			}
			if st := q.Statement(`"s"."t"`); strings.Contains(st, TablePlaceholder) {
				t.Errorf("%s: placeholder left", p.Name)
			}
		}
	}
	if _, ok := Lookup("nope"); ok || ValidatePreset("nope") == nil || ValidatePreset("goose") != nil {
		t.Error("unknown preset accepted")
	}
}

func TestPresetFormat(t *testing.T) {
	gm, _ := Lookup("golang-migrate")
	dj, _ := Lookup("django")
	gs, _ := Lookup("goose")
	for _, tc := range []struct {
		p    *Preset
		cols []*string
		want string
		ok   bool
	}{
		{gm, []*string{strp("42"), strp("f")}, "42", true},
		{gm, []*string{strp("42"), strp("t")}, "42 (dirty)", true},
		{gm, []*string{strp("42"), strp("1")}, "42 (dirty)", true},
		{gm, []*string{strp("42"), strp("0")}, "42", true},
		{gm, []*string{strp("42"), nil}, "42", true},
		{gm, []*string{nil, strp("t")}, "", false},
		{dj, []*string{strp("orders"), strp("0042_add_total")}, "orders.0042_add_total", true},
		{dj, []*string{strp("orders"), nil}, "", false},
		{gs, []*string{strp("20240101120000")}, "20240101120000", true},
		{gs, []*string{nil}, "", false},
		{gs, nil, "", false},
	} {
		got, ok := tc.p.Format(tc.cols)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s %v: got %q %v, want %q %v", tc.p.Name, tc.cols, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCleanValue(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		st   Status
	}{
		{"42", "42", StatusOK},
		{"  42 (dirty)\n", "42 (dirty)", StatusOK},
		{"", "", StatusNone},
		{"   ", "", StatusNone},
		{strings.Repeat("x", 64), strings.Repeat("x", 64), StatusOK},
		{strings.Repeat("x", 65), "", StatusInvalid},
		{"a\x01b", "", StatusInvalid},
		{"caf\xc3\xa9", "", StatusInvalid},
		{"a\tb", "", StatusInvalid},
	} {
		got, st := CleanValue(tc.in)
		if got != tc.want || st != tc.st {
			t.Errorf("%q: got %q %s, want %q %s", tc.in, got, st, tc.want, tc.st)
		}
	}
	if !ValidValue("42") || ValidValue(" 42") || ValidValue("") {
		t.Error("ValidValue")
	}
}

func TestValidateSchema(t *testing.T) {
	for s, ok := range map[string]bool{
		"public": true, "_app": true, "App_2": true, strings.Repeat("a", 63): true,
		"": false, "2a": false, "a-b": false, `a"b`: false, "a b": false, strings.Repeat("a", 64): false, "a$": false,
	} {
		if (ValidateSchema(s) == nil) != ok {
			t.Errorf("%q: want ok=%v", s, ok)
		}
	}
}

func TestValidateCustomSQL(t *testing.T) {
	ok := []string{
		"SELECT version FROM public.schema_migrations",
		"select max(version) from app.schema_migrations",
		"  \n\tSELECT 1\n",
		"WITH v AS (SELECT 1 AS n) SELECT n FROM v",
		"SELECT 'it''s fine' AS x",
		`SELECT "odd name" FROM "My Table"`,
		"SELECT `v` FROM `db`.`t` ORDER BY `v` DESC LIMIT 1",
		"SELECT updated_at, created_by, started_at, execution_time FROM x",
		"SELECT version FROM flyway_schema_history WHERE success ORDER BY installed_rank DESC LIMIT 1",
		"SELECT a - -1 FROM t",
		"SELECT a / 2 * 3 FROM t",
		"SELECT max(version)::text FROM public.schema_migrations",
		"SELECT x1 FROM t2 WHERE col1 = 2",
		"SELECT comments, settings, offset_x FROM t",
	}
	for _, q := range ok {
		if err := ValidateCustomSQL(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	// Each refused query with a fragment the message must contain (the
	// offending word or character, never the query).
	bad := []struct{ q, msg string }{
		{"", "empty"},
		{"   ", "empty"},
		{"SELECT " + strings.Repeat("1", MaxCustomSQLBytes), "longer"},
		{"UPDATE t SET x = 1", "start with SELECT"},
		{"(SELECT 1)", "start with SELECT"},
		{"1 SELECT", "start with SELECT"},
		{`"select" 1`, "start with SELECT"},
		{"selected FROM t", "start with SELECT"},
		{"SELECT 1;", "semicolon"},
		{"SELECT 1 ;  ", "semicolon"},
		{"SELECT 1; SELECT 2", "semicolon"},
		{"SELECT ';' AS x", "semicolon"},
		{"; SELECT 1", "semicolon"},
		{"SELECT 1 -- note", "comment (--)"},
		{"SELECT '--' AS x", "comment (--)"},
		{"SELECT 1 /* note */", "comment (/*)"},
		{"SELECT 1 */", "comment (*/)"},
		{"SELECT 1 # note", "comment (#)"},
		{"SELECT 1 /*! INTO OUTFILE '/tmp/x' */", "comment"},
		{"SELECT /*+ MAX_EXECUTION_TIME(1) */ 1", "comment"},
		{"-- only a comment", "comment"},
		// reviewer bypasses
		{"SELECT 1 -- \r, pg_catalog.pg_read_file('/etc/hostname')\n", "carriage return"},
		{"SELECT 1 \r, pg_catalog.pg_read_file('/etc/hostname')\n", "carriage return"},
		{"SELECT word FROM pg_catalog.ts_stat('SELECT pg_catalog.to_tsvector(pg_catalog.pg_read_file(''/etc/hostname''))')", "ts_stat"},
		{"SELECT word FROM pg_catalog.ts_rewrite('a', 'b')", "ts_rewrite"},
		{"SELECT 'pg_read_file' AS x", "pg_read_file"},
		{"SELECT version FROM a UNION SELECT pg_read_file('/etc/passwd')", "UNION"},
		{"SELECT version FROM a UNION ALL SELECT 1", "UNION"},
		{"SELECT 1union SELECT 2", "UNION"},
		{"SELECT 1 FROM t WHERE 0x1union SELECT 2", "UNION"},
		{"SELECT 1e1union SELECT 2", "UNION"},
		{"SELECT v FROM a INTERSECT SELECT v FROM b", "INTERSECT"},
		{"SELECT v FROM a EXCEPT SELECT v FROM b", "EXCEPT"},
		{"SELECT 1 INTO OUTFILE '/tmp/x'", "INTO"},
		{"SELECT version FROM t INTO DUMPFILE '/tmp/x'", "INTO"},
		{"SELECT * INTO new_t FROM t", "INTO"},
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", "DELETE"},
		{"SELECT * FROM t FOR UPDATE", "UPDATE"},
		{"SELECT * FROM t FOR SHARE", "SHARE"},
		{"SELECT * FROM t LOCK IN SHARE MODE", "LOCK"},
		{"SELECT type FROM flyway_schema_history WHERE type <> 'DELETE'", "DELETE"},
		{"SELECT 'set' AS x", "SET"},
		{"SELECT REPLACE(v, 'a', 'b') FROM t", "REPLACE"},
		{"SELECT SLEEP(10)", "SLEEP"},
		{"SELECT BENCHMARK(1000000, MD5('x'))", "BENCHMARK"},
		{"SELECT 1 FROM t WHERE x IN (SELECT 1 FROM u FOR SHARE)", "SHARE"},
		{"SELECT pg_sleep(10)", "pg_sleep*"},
		{"SELECT pg_catalog.pg_sleep_for('1 minute')", "pg_sleep*"},
		{"SELECT pg_read_file('/etc/passwd')", "pg_read_file"},
		{`SELECT "pg_read_file"('/etc/passwd')`, "pg_read_file"},
		{`SELECT "PG_TERMINATE_BACKEND"(1)`, "pg_terminate_backend"},
		{"SELECT pg_catalog.pg_ls_dir('.')", "pg_ls_*"},
		{"SELECT lo_export(1, '/tmp/x')", "lo_*"},
		{"SELECT query_to_xml('select 1', true, true, '')", "query_to_xml*"},
		{"SELECT query_to_xmlschema('select 1', true, true, '')", "query_to_xml*"},
		{"SELECT pg_catalog.database_to_xmlschema(true, true, '')", "database_to_xml*"},
		{"SELECT foo_to_xmlschema(1)", "*_to_xmlschema"},
		{"SELECT dblink('x', 'y')", "dblink*"},
		{"SELECT load_file('/etc/passwd')", "load_file"},
		{"SELECT set_config('a', 'b', false)", "set_config"},
		{"SELECT get_lock('x', 1)", "get_lock"},
		{"SELECT pg_create_logical_replication_slot('s', 'p')", "pg_create_*"},
		{"SELECT pg_create_physical_replication_slot('s')", "pg_create_*"},
		{"SELECT pg_drop_replication_slot('s')", "pg_drop_replication_slot"},
		{"SELECT pg_stat_reset()", "pg_stat_reset*"},
		{"SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset*"},
		{"SELECT pg_import_system_collations('pg_catalog')", "pg_import_system_collations"},
		{"SELECT xp_cmdshell('id')", "xp_*"},
		{"SELECT sys_exec('id')", "sys_*"},
		{"SELECT a\\b", "backslash"},
		{"SELECT 'a\\'b'", "backslash"},
		{"SELECT $$x$$", "$"},
		{"SELECT $1", "$"},
		{`SELECT U&"d!0061t" UESCAPE '!'`, "U&"},
		{"SELECT u&'x'", "U&"},
		{"SELECT 1 FROM t WHERE x = 'unterminated", "unterminated"},
		{`SELECT "unterminated`, "unterminated"},
		{"SELECT `unterminated", "unterminated"},
		{"SELECT (1", "parentheses"},
		{"SELECT 1) AS q, (SELECT 2", "parentheses"},
		{"SELECT 1\x00", "NUL"},
		{"SELECT 1\x0b", "0x0B"},
		{"SELECT 1\x7f", "0x7F"},
		{"SELECT 'caf\xc3\xa9'", "0xC3"},
	}
	for _, tc := range bad {
		err := ValidateCustomSQL(tc.q)
		var se *SQLError
		if err == nil || !errors.As(err, &se) || se.Message == "" {
			t.Errorf("%q: accepted (%v)", tc.q, err)
			continue
		}
		if !strings.Contains(se.Message, tc.msg) {
			t.Errorf("%q: message %q does not name %q", tc.q, se.Message, tc.msg)
		}
		if len(tc.q) > 24 && strings.Contains(se.Message, tc.q) {
			t.Errorf("%q: the message echoes the query", tc.q)
		}
	}
}

func TestStatementBody(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT 1":            "SELECT 1",
		"  SELECT 1 \n":       "SELECT 1",
		"\tSELECT 'x' AS x\n": "SELECT 'x' AS x",
	} {
		got, err := StatementBody(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := StatementBody("DROP TABLE x"); err == nil {
		t.Error("invalid query accepted")
	}
	if _, err := StatementBody("SELECT 1;"); err == nil {
		t.Error("trailing semicolon accepted")
	}
}

func TestModesAndStatuses(t *testing.T) {
	for _, m := range []Mode{ModeNone, ModePreset, ModeCustom} {
		if !m.Valid() {
			t.Errorf("%s invalid", m)
		}
	}
	if Mode("x").Valid() || Status("x").Valid() {
		t.Error("unknown accepted")
	}
	for _, s := range []Status{StatusOK, StatusNone, StatusUnavailable, StatusInvalid} {
		if !s.Valid() {
			t.Errorf("%s invalid", s)
		}
	}
}

// FuzzValidateCustomSQL: the validator never panics, and an accepted
// query satisfies every rule when re-checked independently: bounded,
// allowed characters, no denied sequence, starts with SELECT/WITH, and no
// token (after any digit) is a denied word or function.
func FuzzValidateCustomSQL(f *testing.F) {
	for _, s := range []string{
		"SELECT 1", "SELECT 1;", "WITH a AS (SELECT 1) SELECT * FROM a", "SELECT ';'", "SELECT 1 -- x\n",
		"SELECT 1 /* x */", "SELECT \"a\"\"b\"", "SELECT `a``b`", "SELECT 'a''b'", "SELECT 1; SELECT 2",
		"SELECT 1 -- \r, pg_catalog.pg_read_file('/etc/hostname')\n", "SELECT 1union SELECT 2",
		"SELECT word FROM pg_catalog.ts_stat('SELECT 1')", "SELECT 1 INTO OUTFILE '/tmp/x'",
	} {
		f.Add(s)
	}
	tokenRe := regexp.MustCompile(`[A-Za-z0-9_]+`)
	f.Fuzz(func(t *testing.T, q string) {
		if ValidateCustomSQL(q) != nil {
			return
		}
		if len(q) > MaxCustomSQLBytes || strings.TrimSpace(q) == "" {
			t.Fatalf("bounds: %q", q)
		}
		for i := 0; i < len(q); i++ {
			if c := q[i]; (c < 0x20 && c != '\n' && c != '\t') || c > 0x7e {
				t.Fatalf("forbidden byte accepted: %q", q)
			}
		}
		for _, s := range []string{";", "--", "/*", "*/", "#", "\\", "$"} {
			if strings.Contains(q, s) {
				t.Fatalf("%q accepted in %q", s, q)
			}
		}
		lower := strings.ToLower(strings.TrimLeft(q, " \t\n"))
		if !regexp.MustCompile(`^(select|with)([^a-z0-9_]|$)`).MatchString(lower) {
			t.Fatalf("no SELECT/WITH: %q", q)
		}
		for _, tok := range tokenRe.FindAllString(lower, -1) {
			for k := 0; k < len(tok); k++ {
				if k > 0 && (tok[k-1] < '0' || tok[k-1] > '9') {
					continue
				}
				w := strings.TrimLeft(tok[k:], "0123456789")
				if deniedWords[w] || deniedFunction(w) != "" {
					t.Fatalf("denied word %q accepted in %q", w, q)
				}
			}
		}
		body, err := StatementBody(q)
		if err != nil || ValidateCustomSQL(body) != nil {
			t.Fatalf("the body of an accepted query is refused: %q", body)
		}
	})
}

// Every preset truncates its output in SQL (bounded result, V-2), Flyway
// skips undo rows and undone versions, Liquibase skips re-runs.
func TestPresetsBoundedAndSemantics(t *testing.T) {
	for _, p := range Presets() {
		if pg := p.QueryFor(engine.KindPostgres).SQL; !strings.HasPrefix(pg, "SELECT pg_catalog.left(") || !strings.Contains(pg, "::pg_catalog.text, 257)") {
			t.Errorf("%s: PostgreSQL output not truncated: %s", p.Name, pg)
		}
		if my := p.QueryFor(engine.KindMySQL).SQL; !strings.HasPrefix(my, "SELECT LEFT(CAST(") || !strings.Contains(my, " AS CHAR), 257)") {
			t.Errorf("%s: MySQL output not truncated: %s", p.Name, my)
		}
	}
	fw, _ := Lookup("flyway")
	lb, _ := Lookup("liquibase")
	for _, k := range []engine.Kind{engine.KindPostgres, engine.KindMySQL} {
		if q := fw.QueryFor(k).SQL; strings.Count(q, "'UNDO_'") != 2 || !strings.Contains(q, "NOT EXISTS") {
			t.Errorf("flyway/%s: undo rows not handled: %s", k, q)
		}
		if q := lb.QueryFor(k).SQL; strings.Contains(q, "RERAN") || !strings.Contains(q, "EXECUTED") || !strings.Contains(q, "MARK_RAN") {
			t.Errorf("liquibase/%s: exectypes: %s", k, q)
		}
	}
	if MaxRawValueBytes <= MaxValueBytes {
		t.Error("the raw bound must exceed the value bound")
	}
}
