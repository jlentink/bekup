//go:build integration

package runner

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Schema-version capture against real servers (D-210): every preset on
// its framework's table layout, missing tables, unexpected layouts, the
// custom-query bounds (timeout, read-only, long, huge and binary results,
// multiple statements), the table lock and snapshot consistency.
//
// Tests of the execution bounds call SchemaVersion with statements the
// lexical guard (schemaversion.ValidateCustomSQL) refuses, such as
// pg_sleep: the bounds must hold on their own (rawCustom).

// pgVersionSeed creates every framework's table (PostgreSQL syntax).
const pgVersionSeed = `
CREATE TABLE public.schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
INSERT INTO public.schema_migrations VALUES (42, true);
CREATE TABLE public.goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now());
INSERT INTO public.goose_db_version (version_id, is_applied) VALUES (0, true), (20240101, true), (20240202, true), (20240202, false);
CREATE TABLE public.flyway_schema_history (installed_rank int PRIMARY KEY, version varchar(50), description varchar(200) NOT NULL,
  type varchar(20) NOT NULL, script varchar(1000) NOT NULL, checksum int, installed_by varchar(100) NOT NULL,
  installed_on timestamp NOT NULL DEFAULT now(), execution_time int NOT NULL, success boolean NOT NULL);
INSERT INTO public.flyway_schema_history VALUES (1, '1', 'init', 'SQL', 'V1__init.sql', 1, 'x', now(), 1, true),
  (2, '2.1', 'more', 'SQL', 'V2_1__more.sql', 1, 'x', now(), 1, true), (3, '3', 'broken', 'SQL', 'V3__broken.sql', 1, 'x', now(), 1, false),
  (4, '4', 'four', 'SQL', 'V4__four.sql', 1, 'x', now(), 1, true), (5, '4', 'four', 'UNDO_SQL', 'U4__four.sql', 1, 'x', now(), 1, true);
CREATE TABLE public.databasechangelog (id varchar(255) NOT NULL, author varchar(255) NOT NULL, filename varchar(255) NOT NULL,
  dateexecuted timestamp NOT NULL, orderexecuted int NOT NULL, exectype varchar(10) NOT NULL, md5sum varchar(35), description varchar(255),
  comments varchar(255), tag varchar(255), liquibase varchar(20), contexts varchar(255), labels varchar(255), deployment_id varchar(10));
INSERT INTO public.databasechangelog (id, author, filename, dateexecuted, orderexecuted, exectype) VALUES
  ('create-users', 'a', 'f.xml', '2024-01-01', 1, 'EXECUTED'), ('add-index', 'a', 'f.xml', '2024-02-01', 2, 'EXECUTED'),
  ('failed-one', 'a', 'f.xml', '2024-03-01', 3, 'FAILED'), ('refresh-views', 'a', 'f.xml', '2024-04-01', 4, 'RERAN');
CREATE TABLE public.django_migrations (id bigserial PRIMARY KEY, app varchar(255) NOT NULL, name varchar(255) NOT NULL, applied timestamptz NOT NULL);
INSERT INTO public.django_migrations (app, name, applied) VALUES ('auth', '0001_initial', '2024-01-01'), ('orders', '0042_add_total', '2024-02-01');
CREATE TABLE public.migrations (id serial PRIMARY KEY, migration varchar(255) NOT NULL, batch int NOT NULL);
INSERT INTO public.migrations (migration, batch) VALUES ('2014_10_12_000000_create_users_table', 1), ('2024_01_01_000000_add_orders', 2);
CREATE TABLE public.alembic_version (version_num varchar(32) NOT NULL PRIMARY KEY);
INSERT INTO public.alembic_version VALUES ('ffe1'), ('abc2');
CREATE TABLE public._prisma_migrations (id varchar(36) PRIMARY KEY, checksum varchar(64) NOT NULL, finished_at timestamptz,
  migration_name varchar(255) NOT NULL, logs text, rolled_back_at timestamptz, started_at timestamptz NOT NULL DEFAULT now(), applied_steps_count int NOT NULL DEFAULT 0);
INSERT INTO public._prisma_migrations (id, checksum, finished_at, migration_name, rolled_back_at) VALUES
  ('1', 'c', '2024-01-01', '20240101000000_init', NULL), ('2', 'c', '2024-02-01', '20240201000000_orders', NULL),
  ('3', 'c', '2024-03-01', '20240301000000_rolled', '2024-03-02');
CREATE TABLE public.knex_migrations (id serial PRIMARY KEY, name varchar(255), batch int, migration_time timestamptz);
INSERT INTO public.knex_migrations (name, batch, migration_time) VALUES ('20200101_users.js', 1, now()), ('20240101_orders.js', 2, now());
CREATE TABLE public."SequelizeMeta" (name varchar(255) NOT NULL PRIMARY KEY);
INSERT INTO public."SequelizeMeta" VALUES ('20200101000000-create-user.js'), ('20240101000000-create-order.js');
CREATE SCHEMA rails; CREATE TABLE rails.schema_migrations (version varchar NOT NULL PRIMARY KEY);
INSERT INTO rails.schema_migrations VALUES ('9'), ('20240101120000'), ('20231231000000');
CREATE SCHEMA empty;
CREATE SCHEMA viewy; CREATE VIEW viewy.knex_migrations AS SELECT 1 AS id, 'x'::varchar AS name;
CREATE SCHEMA dom; CREATE DOMAIN dom.mname AS text; CREATE TABLE dom.knex_migrations (id int, name dom.mname); INSERT INTO dom.knex_migrations VALUES (1, 'x');
CREATE TABLE public.writes (n int);
CREATE FUNCTION public.do_write() RETURNS text LANGUAGE plpgsql AS $$ BEGIN INSERT INTO public.writes VALUES (1); RETURN 'wrote'; END $$;
CREATE TABLE public.vtab (v text); INSERT INTO public.vtab VALUES ('v1');
`

// mySQLVersionSeed is the same for MySQL/MariaDB (database verdb; Rails
// in verrails, its table name collides with golang-migrate's).
const mySQLVersionSeed = "CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL); " +
	"INSERT INTO schema_migrations VALUES (42, false); " +
	"CREATE TABLE goose_db_version (id serial, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (id)); " +
	"INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true), (20240101, true), (20240202, true), (20240202, false); " +
	"CREATE TABLE flyway_schema_history (installed_rank int NOT NULL PRIMARY KEY, version varchar(50), description varchar(200) NOT NULL, " +
	"type varchar(20) NOT NULL, script varchar(1000) NOT NULL, checksum int, installed_by varchar(100) NOT NULL, " +
	"installed_on timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, execution_time int NOT NULL, success tinyint(1) NOT NULL); " +
	"INSERT INTO flyway_schema_history (installed_rank, version, description, type, script, installed_by, execution_time, success) VALUES " +
	"(1, '1', 'init', 'SQL', 'V1', 'x', 1, 1), (2, '2.1', 'more', 'SQL', 'V2', 'x', 1, 1), (3, '3', 'broken', 'SQL', 'V3', 'x', 1, 0), " +
	"(4, '4', 'four', 'SQL', 'V4', 'x', 1, 1), (5, '4', 'four', 'UNDO_SQL', 'U4', 'x', 1, 1); " +
	"CREATE TABLE DATABASECHANGELOG (ID varchar(255) NOT NULL, AUTHOR varchar(255) NOT NULL, FILENAME varchar(255) NOT NULL, " +
	"DATEEXECUTED datetime NOT NULL, ORDEREXECUTED int NOT NULL, EXECTYPE varchar(10) NOT NULL, MD5SUM varchar(35)); " +
	"INSERT INTO DATABASECHANGELOG (ID, AUTHOR, FILENAME, DATEEXECUTED, ORDEREXECUTED, EXECTYPE) VALUES " +
	"('create-users', 'a', 'f', '2024-01-01', 1, 'EXECUTED'), ('add-index', 'a', 'f', '2024-02-01', 2, 'MARK_RAN'), ('failed', 'a', 'f', '2024-03-01', 3, 'FAILED'), ('refresh', 'a', 'f', '2024-04-01', 4, 'RERAN'); " +
	"CREATE TABLE django_migrations (id bigint AUTO_INCREMENT PRIMARY KEY, app varchar(255) NOT NULL, name varchar(255) NOT NULL, applied datetime(6) NOT NULL); " +
	"INSERT INTO django_migrations (app, name, applied) VALUES ('auth', '0001_initial', '2024-01-01'), ('orders', '0042_add_total', '2024-02-01'); " +
	"CREATE TABLE migrations (id int unsigned AUTO_INCREMENT PRIMARY KEY, migration varchar(255) NOT NULL, batch int NOT NULL); " +
	"INSERT INTO migrations (migration, batch) VALUES ('2014_10_12_000000_create_users_table', 1), ('2024_01_01_000000_add_orders', 2); " +
	"CREATE TABLE alembic_version (version_num varchar(32) NOT NULL PRIMARY KEY); INSERT INTO alembic_version VALUES ('ffe1'), ('abc2'); " +
	"CREATE TABLE _prisma_migrations (id varchar(36) PRIMARY KEY, checksum varchar(64) NOT NULL, finished_at datetime(3) NULL, " +
	"migration_name varchar(255) NOT NULL, logs text, rolled_back_at datetime(3) NULL, started_at datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), applied_steps_count int unsigned NOT NULL DEFAULT 0); " +
	"INSERT INTO _prisma_migrations (id, checksum, finished_at, migration_name, rolled_back_at) VALUES " +
	"('1', 'c', '2024-01-01', '20240101000000_init', NULL), ('2', 'c', '2024-02-01', '20240201000000_orders', NULL), ('3', 'c', '2024-03-01', '20240301000000_rolled', '2024-03-02'); " +
	"CREATE TABLE knex_migrations (id int unsigned AUTO_INCREMENT PRIMARY KEY, name varchar(255), batch int, migration_time timestamp NULL); " +
	"INSERT INTO knex_migrations (name, batch) VALUES ('20200101_users.js', 1), ('20240101_orders.js', 2); " +
	"CREATE TABLE SequelizeMeta (name varchar(255) NOT NULL PRIMARY KEY); " +
	"INSERT INTO SequelizeMeta VALUES ('20200101000000-create-user.js'), ('20240101000000-create-order.js'); " +
	"CREATE TABLE writes (n int)"

// versionWant is the expected capture of each preset on the seeds.
var versionWant = map[string]map[engine.Kind]string{
	"golang-migrate": {engine.KindPostgres: "42 (dirty)", engine.KindMySQL: "42"},
	"goose":          {engine.KindPostgres: "20240101", engine.KindMySQL: "20240101"},
	"flyway":         {engine.KindPostgres: "2.1", engine.KindMySQL: "2.1"},
	"liquibase":      {engine.KindPostgres: "add-index", engine.KindMySQL: "add-index"},
	"rails":          {engine.KindPostgres: "20240101120000", engine.KindMySQL: "20240101120000"},
	"django":         {engine.KindPostgres: "orders.0042_add_total", engine.KindMySQL: "orders.0042_add_total"},
	"laravel":        {engine.KindPostgres: "2024_01_01_000000_add_orders", engine.KindMySQL: "2024_01_01_000000_add_orders"},
	"alembic":        {engine.KindPostgres: "abc2,ffe1", engine.KindMySQL: "abc2,ffe1"},
	"prisma":         {engine.KindPostgres: "20240201000000_orders", engine.KindMySQL: "20240201000000_orders"},
	"knex":           {engine.KindPostgres: "20240101_orders.js", engine.KindMySQL: "20240101_orders.js"},
	"sequelize":      {engine.KindPostgres: "20240101000000-create-order.js", engine.KindMySQL: "20240101000000-create-order.js"},
}

// versionSession opens a stats session with an open statistics
// transaction on db.
func (in *itInner) versionSession(t *testing.T, name string) dbprobe.StatsSource {
	const db = "verdb"
	t.Helper()
	tg := in.h.Targets[name]
	user := "root"
	if tg.Kind == engine.KindPostgres {
		user = itPGUser
	}
	p := dbprobe.Params{
		Kind: tg.Kind, Host: tg.Host, Port: tg.Port, User: user, Password: []byte(in.h.Password), Database: db,
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", net.JoinHostPort(tg.Host, strconv.Itoa(tg.Port)))
		},
	}
	if tg.TLS != "disabled" {
		p.TLS = dbprobe.UnverifiedTLS(tg.Host)
	}
	c, err := dbprobe.Open(in.ctx(t, time.Minute), p)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { itIgnore(c.Close()) })
	src, ok := c.(dbprobe.StatsSource)
	if !ok {
		t.Fatal("no stats source")
	}
	if _, err := src.BeginStats(in.ctx(t, time.Minute), tg.Kind == engine.KindPostgres); err != nil {
		t.Fatalf("begin %s: %v", name, err)
	}
	return src
}

func preset(t *testing.T, name string) *schemaversion.Preset {
	t.Helper()
	p, ok := schemaversion.Lookup(name)
	if !ok {
		t.Fatalf("no preset %s", name)
	}
	return p
}

func (in *itInner) testSchemaVersion(t *testing.T) {
	in.sql(t, "pg16", "postgres", "CREATE DATABASE verdb")
	in.sql(t, "pg16", "verdb", pgVersionSeed)
	for _, name := range []string{"mysql84", "maria118"} {
		in.sql(t, name, "shop", "CREATE DATABASE verdb; CREATE DATABASE verrails")
		in.sql(t, name, "verdb", mySQLVersionSeed)
		in.sql(t, name, "verrails", "CREATE TABLE schema_migrations (version varchar(255) NOT NULL PRIMARY KEY); "+
			"INSERT INTO schema_migrations VALUES ('9'), ('20240101120000'), ('20231231000000'); "+
			"CREATE VIEW alembic_version AS SELECT 'x' AS version_num")
	}
	t.Run("pg_presets", in.versionPGPresets)
	t.Run("pg_custom", in.versionPGCustom)
	t.Run("pg_snapshot", in.versionPGSnapshot)
	t.Run("pg_lock", in.versionPGLock)
	for _, name := range []string{"mysql84", "maria118"} {
		t.Run(name, func(t *testing.T) { in.versionMySQL(t, name) })
	}
	t.Run("dump_rpc", in.versionDumpRPC)
}

func (in *itInner) versionPGPresets(t *testing.T) {
	src := in.versionSession(t, "pg16")
	ctx := in.ctx(t, 2*time.Minute)
	for _, p := range schemaversion.Presets() {
		schema := "public"
		if p.Name == "rails" {
			schema = "rails"
		}
		r := src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Preset: p, Schema: schema})
		if want := versionWant[p.Name][engine.KindPostgres]; r.Status != schemaversion.StatusOK || r.Value != want {
			t.Errorf("%s: got %+v, want %q", p.Name, r, want)
		}
	}
	for _, tc := range []struct {
		preset, schema string
		want           schemaversion.Status
	}{
		{"liquibase", "empty", schemaversion.StatusNone},             // no table
		{"goose", "nope", schemaversion.StatusNone},                  // no schema
		{"knex", "viewy", schemaversion.StatusUnavailable},           // a view, not a table
		{"knex", "dom", schemaversion.StatusUnavailable},             // a user type
		{"golang-migrate", "rails", schemaversion.StatusUnavailable}, // the Rails layout (no dirty column)
	} {
		r := src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Preset: preset(t, tc.preset), Schema: tc.schema})
		if r.Status != tc.want || r.Value != "" {
			t.Errorf("%s in %s: got %+v, want %s", tc.preset, tc.schema, r, tc.want)
		}
	}
	// The session (and its snapshot transaction) stays usable.
	if _, err := src.Catalog(ctx, "verdb"); err != nil {
		t.Fatalf("catalog after the captures: %v", err)
	}
}

func (in *itInner) versionPGCustom(t *testing.T) {
	src := in.versionSession(t, "pg16")
	ctx := in.ctx(t, 2*time.Minute)
	custom := func(q string) dbprobe.VersionResult {
		t.Helper()
		body, err := schemaversion.StatementBody(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		return src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Custom: body})
	}
	rawCustom := func(q string) dbprobe.VersionResult {
		t.Helper()
		if schemaversion.ValidateCustomSQL(q) == nil {
			t.Fatalf("%q: the guard accepts it; use custom", q)
		}
		return src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Custom: q})
	}
	if r := custom("SELECT max(version)::pg_catalog.text FROM public.schema_migrations"); r.Status != schemaversion.StatusOK || r.Value != "42" {
		t.Errorf("max: %+v", r)
	}
	if r := custom("SELECT version FROM schema_migrations"); r.Status != schemaversion.StatusNone {
		t.Errorf("unqualified table (search_path pg_catalog, pg_temp): %+v", r)
	}
	if r := custom("SELECT x FROM public.nope"); r.Status != schemaversion.StatusNone {
		t.Errorf("missing table: %+v", r)
	}
	if r := custom("SELECT v FROM public.vtab WHERE false"); r.Status != schemaversion.StatusNone {
		t.Errorf("no row: %+v", r)
	}
	start := time.Now()
	if r := rawCustom("SELECT pg_catalog.pg_sleep(30)"); r.Status != schemaversion.StatusUnavailable || r.Detail != "sqlstate=57014" || r.SessionLost {
		t.Errorf("sleep: %+v", r)
	}
	if r := custom("SELECT pg_catalog.count(*)::pg_catalog.text FROM pg_catalog.generate_series(1, 2000000000) AS g"); r.Status != schemaversion.StatusUnavailable ||
		r.Detail != "sqlstate=57014" {
		t.Errorf("long-running: %+v", r)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("the statement timeout did not stop the query (%s)", d)
	}
	if r := custom("SELECT public.do_write()"); r.Status != schemaversion.StatusUnavailable || r.Detail != "sqlstate=25006" {
		t.Errorf("write: %+v", r)
	}
	if got := in.sql(t, "pg16", "verdb", "SELECT count(*) FROM public.writes"); got != "0" {
		t.Errorf("a write happened: %s", got)
	}
	if r := custom("SELECT pg_catalog.repeat('x', 65)"); r.Status != schemaversion.StatusInvalid {
		t.Errorf("long: %+v", r)
	}
	if r := custom("SELECT pg_catalog.chr(1)"); r.Status != schemaversion.StatusInvalid {
		t.Errorf("control character: %+v", r)
	}
	if r := custom("SELECT pg_catalog.decode('414243', 'hex')"); r.Status != schemaversion.StatusInvalid || r.Detail != "binary" {
		t.Errorf("bytea: %+v", r)
	}
	if r := custom("SELECT v FROM (VALUES ('v1'), ('v2')) AS x (v)"); r.Status != schemaversion.StatusOK || r.Value != "v1" {
		t.Errorf("first row only: %+v", r)
	}
	// Only the first column counts; a large second one is received but
	// never kept.
	if r := custom("SELECT 'v1', pg_catalog.repeat('x', 500000)"); r.Status != schemaversion.StatusOK || r.Value != "v1" {
		t.Errorf("first column only: %+v", r)
	}
	if _, err := src.Catalog(ctx, "verdb"); err != nil {
		t.Fatalf("catalog after the captures: %v", err)
	}
	// A row above the protocol cap breaks the session instead of being
	// buffered; the caller then stops sharing the snapshot.
	if r := custom("SELECT pg_catalog.repeat('x', 2000000)"); r.Status != schemaversion.StatusUnavailable || !r.SessionLost {
		t.Errorf("huge row: %+v", r)
	}
}

// versionPGLock: a preset waits at most lock_timeout for its table lock
// (no definition change can slip in between the checks and the read).
func (in *itInner) versionPGLock(t *testing.T) {
	src := in.versionSession(t, "pg16")
	ctx := in.ctx(t, 2*time.Minute)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// psql holds an ACCESS EXCLUSIVE lock for 8 seconds.
		in.sql(t, "pg16", "verdb", "BEGIN; LOCK TABLE public.knex_migrations IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(8); COMMIT;")
	}()
	deadline := time.Now().Add(20 * time.Second)
	for in.sql(t, "pg16", "verdb", "SELECT count(*) FROM pg_locks l JOIN pg_class c ON c.oid = l.relation "+
		"WHERE c.relname = 'knex_migrations' AND l.mode = 'AccessExclusiveLock' AND l.granted") != "1" {
		if time.Now().After(deadline) {
			t.Fatal("the lock was never taken")
		}
		time.Sleep(100 * time.Millisecond)
	}
	start := time.Now()
	r := src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Preset: preset(t, "knex"), Schema: "public"})
	elapsed := time.Since(start)
	<-done
	if r.Status != schemaversion.StatusUnavailable || r.Detail != "sqlstate=55P03" || r.SessionLost {
		t.Errorf("locked table: %+v", r)
	}
	if elapsed > 6*time.Second {
		t.Errorf("the lock wait was not bounded (%s)", elapsed)
	}
}

// versionPGSnapshot: a version committed while the capture runs (after the
// stats transaction took its snapshot) is not seen.
func (in *itInner) versionPGSnapshot(t *testing.T) {
	src := in.versionSession(t, "pg16")
	ctx := in.ctx(t, 2*time.Minute)
	// pg_sleep is refused by the guard; the execution is tested directly.
	body := "WITH s AS MATERIALIZED (SELECT pg_catalog.pg_sleep(3)) SELECT v FROM s, public.vtab"
	done := make(chan dbprobe.VersionResult, 1)
	go func() { done <- src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Custom: body}) }()
	// Wait until the capture sleeps, then change the version.
	deadline := time.Now().Add(20 * time.Second)
	for {
		n := in.sql(t, "pg16", "verdb", "SELECT count(*) FROM pg_stat_activity WHERE application_name = 'bekup-probe' AND query LIKE '%pg_sleep(3)%' AND state = 'active'")
		if n == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the capture never started")
		}
		time.Sleep(100 * time.Millisecond)
	}
	in.sql(t, "pg16", "verdb", "UPDATE public.vtab SET v = 'v2'")
	r := <-done
	if r.Status != schemaversion.StatusOK || r.Value != "v1" {
		t.Fatalf("the capture saw a change after its snapshot: %+v", r)
	}
	in.sql(t, "pg16", "verdb", "UPDATE public.vtab SET v = 'v1'")
}

func (in *itInner) versionMySQL(t *testing.T, name string) {
	kind := in.h.Targets[name].Kind
	ctx := in.ctx(t, 2*time.Minute)
	src := in.versionSession(t, name)
	for _, p := range schemaversion.Presets() {
		db := "verdb"
		if p.Name == "rails" {
			db = "verrails"
		}
		r := src.SchemaVersion(ctx, db, dbprobe.VersionQuery{Preset: p})
		if want := versionWant[p.Name][engine.KindMySQL]; r.Status != schemaversion.StatusOK || r.Value != want {
			t.Errorf("%s: got %+v, want %q", p.Name, r, want)
		}
	}
	if r := src.SchemaVersion(ctx, "shop", dbprobe.VersionQuery{Preset: preset(t, "liquibase")}); r.Status != schemaversion.StatusNone {
		t.Errorf("missing table: %+v", r)
	}
	// A view named like the framework table is not read.
	if r := src.SchemaVersion(ctx, "verrails", dbprobe.VersionQuery{Preset: preset(t, "alembic")}); r.Status != schemaversion.StatusUnavailable {
		t.Errorf("view: %+v", r)
	}
	custom := func(q string) dbprobe.VersionResult {
		t.Helper()
		body, err := schemaversion.StatementBody(q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		return src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Custom: body})
	}
	rawCustom := func(q string) dbprobe.VersionResult {
		t.Helper()
		if schemaversion.ValidateCustomSQL(q) == nil {
			t.Fatalf("%q: the guard accepts it; use custom", q)
		}
		return src.SchemaVersion(ctx, "verdb", dbprobe.VersionQuery{Custom: q})
	}
	if r := custom("SELECT MAX(version) FROM schema_migrations"); r.Status != schemaversion.StatusOK || r.Value != "42" {
		t.Errorf("custom: %+v", r)
	}
	if r := custom("SELECT x FROM nope"); r.Status != schemaversion.StatusNone {
		t.Errorf("missing table: %+v", r)
	}
	start := time.Now()
	r := rawCustom("SELECT CONCAT('v', SLEEP(30)) FROM schema_migrations")
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("%s: the statement limit did not stop the query (%s, %+v)", kind, d, r)
	}
	if r := custom("SELECT REPEAT('x', 65)"); r.Status != schemaversion.StatusInvalid {
		t.Errorf("long: %+v", r)
	}
	if r := custom("SELECT CHAR(1)"); r.Status != schemaversion.StatusInvalid {
		t.Errorf("control character: %+v", r)
	}
	if r := custom("SELECT UNHEX('00FF')"); r.Status != schemaversion.StatusInvalid {
		t.Errorf("binary: %+v", r)
	}
	if r := custom("SELECT migration FROM migrations ORDER BY id"); r.Status != schemaversion.StatusOK || r.Value != "2014_10_12_000000_create_users_table" {
		t.Errorf("first row only: %+v", r)
	}
	if r := custom("SELECT 'v1' AS a, REPEAT('x', 500000) AS b"); r.Status != schemaversion.StatusOK || r.Value != "v1" {
		t.Errorf("first column only: %+v", r)
	}
	// The READ ONLY transaction refuses writes even when the lexical
	// checks are bypassed (the runner never sends such a statement).
	if r := rawCustom("INSERT INTO writes VALUES (1)"); r.Status != schemaversion.StatusUnavailable {
		t.Errorf("%s: write: %+v", kind, r)
	}
	if got := itLastLine(in.sql(t, name, "verdb", "SELECT COUNT(*) FROM writes")); got != "0" {
		t.Errorf("%s: a write happened: %s", kind, got)
	}
	// A result above the read budget breaks the session (it ends after
	// the capture anyway) instead of being buffered: last.
	if r := custom("SELECT REPEAT('x', 2000000)"); r.Status != schemaversion.StatusUnavailable {
		t.Errorf("%s: huge result: %+v", kind, r)
	}
}

// versionDumpRPC: the version travels in the Dump's stats message; a
// multi-statement custom query is refused before anything runs.
func (in *itInner) versionDumpRPC(t *testing.T) {
	run := func(rt *runnerpb.Target, q *runnerpb.VersionQuery) (*runnerpb.DumpStats, *runnerpb.DumpResult, error) {
		t.Helper()
		s, err := in.rpc.Dump(in.ctx(t, 5*time.Minute), &runnerpb.DumpRequest{Ctx: in.rc(), Target: rt, Database: "verdb",
			Stats: statsOpts(runnerpb.StatsLevel_STATS_LEVEL_STANDARD, 0), VersionQuery: q})
		if err != nil {
			return nil, nil, err
		}
		var (
			st  *runnerpb.DumpStats
			res *runnerpb.DumpResult
		)
		for {
			m, err := s.Recv()
			if errors.Is(err, io.EOF) {
				return st, res, nil
			}
			if err != nil {
				return st, res, err
			}
			if m.GetStats() != nil {
				st = m.GetStats()
			}
			if m.GetResult() != nil {
				res = m.GetResult()
			}
		}
	}
	st, res, err := run(in.target("pg16"), &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "golang-migrate"}})
	if err != nil || res.GetExitCode() != 0 || st.GetVersionStatus() != runnerpb.VersionStatus_VERSION_STATUS_OK || st.GetSchemaVersion() != "42 (dirty)" || !st.GetSameSnapshot() {
		t.Fatalf("pg preset: %v %v %v", st, res, err)
	}
	st, res, err = run(in.target("mysql84"), &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT MAX(version_id) FROM goose_db_version"}})
	if err != nil || res.GetExitCode() != 0 || st.GetVersionStatus() != runnerpb.VersionStatus_VERSION_STATUS_OK || st.GetSchemaVersion() != "20240202" {
		t.Fatalf("mysql custom: %v %v %v", st, res, err)
	}
	// A failing query never fails the dump.
	st, res, err = run(in.target("pg16"), &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{
		CustomSql: "SELECT pg_catalog.count(*)::pg_catalog.text FROM pg_catalog.generate_series(1, 2000000000) AS g"}})
	if err != nil || res.GetExitCode() != 0 || st.GetVersionStatus() != runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE {
		t.Fatalf("pg timeout: %v %v %v", st, res, err)
	}
	// A huge result: the version is unavailable, the dump still succeeds
	// with its own snapshot.
	st, res, err = run(in.target("pg16"), &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT pg_catalog.repeat('x', 2000000)"}})
	if err != nil || res.GetExitCode() != 0 || st.GetVersionStatus() != runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE || st.GetSameSnapshot() {
		t.Fatalf("pg huge result: %v %v %v", st, res, err)
	}
	for _, q := range []string{"SELECT 1; SELECT 2", "SELECT 1;", "SELECT 1 UNION SELECT 2", "SELECT pg_catalog.pg_sleep(30)", "SELECT 1 -- c"} {
		_, _, err = run(in.target("pg16"), &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: q}})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%q: %v", q, err)
		}
		if strings.Contains(err.Error(), q) {
			t.Fatalf("the error quotes the query: %v", err)
		}
	}
}
