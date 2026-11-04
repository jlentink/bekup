//go:build integration

package db_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

// External API database layer (D-220): the bekup_external role created by
// deploy/postgres/init/20-bekup-external.sh, the grant sync and privilege
// audit, and the api functions of migration 00037.

const latestMigration = 39

func (d testDB) externalPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return d.pool(t, d.externalURL)
}

// TestExternalRoleAllowlist: bekup_external logs in only over TLS, passes
// its checks and can call the api functions, and nothing else.
func TestExternalRoleAllowlist(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	ext := d.externalPool(t)
	if err := db.CheckExternalRole(ctx, ext); err != nil {
		t.Fatalf("CheckExternalRole: %v", err)
	}
	if v, err := sqlc.New(ext).ExternalSchemaVersion(ctx); err != nil || v != latestMigration {
		t.Fatalf("api.schema_version() = %d, %v", v, err)
	}
	// Other roles' sessions are refused.
	for name, p := range map[string]*pgxpool.Pool{"app": d.appPool(t), "owner": d.ownerPool(t)} {
		if err := db.CheckExternalRole(ctx, p); err == nil || !strings.Contains(err.Error(), "external API must connect as bekup_external") {
			t.Errorf("%s session: %v", name, err)
		}
	}

	for _, stmt := range []string{
		"SELECT 1 FROM public.api_tokens",
		"SELECT 1 FROM public.users",
		"SELECT 1 FROM public.run_requests",
		"INSERT INTO public.run_requests (token_id, job_id) VALUES (gen_random_uuid(), gen_random_uuid())",
		"UPDATE public.run_requests SET outcome = 'rejected'",
		"SELECT 1 FROM public.river_job",
		"SELECT 1 FROM public.goose_db_version",
		"SELECT public.bekup_schema_version()",
		"SELECT public.api_token_job_allowed(gen_random_uuid(), gen_random_uuid())",
		"SELECT public.api_token_valid(gen_random_uuid())",
		"SELECT public.bekup_sync_external_grants()",
		"SELECT 1 FROM public.api_access_denials",
		"INSERT INTO public.api_access_denials (token_id) VALUES (gen_random_uuid())",
		"SELECT public.api_token_admit(gen_random_uuid(), '\\x00', NULL)",
		"SELECT public.api_client_ip('192.0.2.1')",
		"SELECT public.api_cidrs_valid('{}')",
		"SELECT * FROM api.request_run_result",
		"SELECT * FROM api.authenticate_result",
		"CREATE TABLE api.evil (id int)",
		"CREATE TABLE public.evil (id int)",
		"CREATE TEMP TABLE evil (id int)",
		"CREATE FUNCTION api.evil() RETURNS int LANGUAGE sql AS 'SELECT 1'",
		"SET ROLE bekup_app",
		"SET ROLE bekup_owner",
	} {
		_, err := ext.Exec(ctx, stmt)
		if err == nil {
			t.Errorf("bekup_external could run %q", stmt)
			continue
		}
		if code := pgCode(err); code != "42501" {
			t.Errorf("%q: SQLSTATE %s (%v), want 42501", stmt, code, err)
		}
	}

	// Independently of the audit query: the catalog shows no relation
	// privilege and exactly the five api functions.
	var rels, pubFuncs, apiFuncs int
	if err := superQuery(ctx, d.name, `SELECT
		(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname IN ('public', 'api')
		   AND has_table_privilege('bekup_external', c.oid, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')),
		(SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public' AND has_function_privilege('bekup_external', p.oid, 'EXECUTE')
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')),
		(SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'api' AND has_function_privilege('bekup_external', p.oid, 'EXECUTE'))`,
		&rels, &pubFuncs, &apiFuncs); err != nil {
		t.Fatal(err)
	}
	if rels != 0 || pubFuncs != 0 || apiFuncs != 5 {
		t.Fatalf("bekup_external: %d relation grants, %d public functions, %d api functions (want 0, 0, 5)", rels, pubFuncs, apiFuncs)
	}

	// Only over TLS (the init script's pg_hba rule).
	if p, err := connectWith(ctx, d.externalURL, db.TLSOptions{Mode: config.DBTLSDisable}); err == nil {
		p.Close()
		t.Fatal("bekup_external connected without TLS")
	} else if !strings.Contains(err.Error(), "pg_hba.conf rejects connection") || !strings.Contains(err.Error(), "no encryption") {
		t.Fatalf("plain-text connection: %v", err)
	}
}

// superQuery scans one row of a superuser query in dbname.
func superQuery(ctx context.Context, dbname, query string, dst ...any) (err error) {
	conn, err := pgx.Connect(ctx, urlFor(superUser, superPassword, dbname))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close(ctx)) }()
	return conn.QueryRow(ctx, query).Scan(dst...)
}

// externalACLs renders every privilege the sync touches (database ACL, the
// api schema, its functions and views).
const externalACLs = `
SELECT concat_ws(E'\n',
    'datacl=' || coalesce((SELECT datacl::text FROM pg_database WHERE datname = current_database()), 'NULL'),
    'api=' || coalesce((SELECT nspacl::text FROM pg_namespace WHERE nspname = 'api'), 'NULL'),
    'public=' || coalesce((SELECT nspacl::text FROM pg_namespace WHERE nspname = 'public'), 'NULL'),
    'funcs=' || coalesce((SELECT string_agg(p.proname || ':' || coalesce(p.proacl::text, 'NULL'), ';' ORDER BY p.proname)
                          FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'api'), ''),
    'rels=' || coalesce((SELECT string_agg(c.relname || ':' || coalesce(c.relacl::text, 'NULL'), ';' ORDER BY c.relname)
                         FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'api'), ''))`

// TestSyncExternalGrants: the sync is idempotent, takes back what
// bekup_owner granted beyond the allowlist, the audit refuses what it
// cannot take back, and without the role it does nothing.
func TestSyncExternalGrants(t *testing.T) {
	ctx := withTimeout(t, 3*time.Minute)
	d := migratedDB(t)
	snapshot := func() string {
		t.Helper()
		var s string
		if err := superQuery(ctx, d.name, externalACLs, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := snapshot()
	if !strings.Contains(first, "bekup_external=X/bekup_owner") || !strings.Contains(first, "bekup_external=U/bekup_owner") {
		t.Fatalf("grants missing:\n%s", first)
	}
	m := d.migrator(t)
	for range 2 {
		if _, err := m.Up(ctx); err != nil {
			t.Fatal(err)
		}
		if got := snapshot(); got != first {
			t.Fatalf("sync is not idempotent\nfirst:\n%s\nnow:\n%s", first, got)
		}
	}
	owner := sqlc.New(d.ownerPool(t))
	if synced, err := owner.SyncExternalGrants(ctx); err != nil || !synced {
		t.Fatalf("SyncExternalGrants: %v %v", synced, err)
	}
	if _, err := sqlc.New(d.appPool(t)).SyncExternalGrants(ctx); pgCode(err) != "42501" {
		t.Fatalf("bekup_app ran the sync: %v", err)
	}

	ext := d.externalPool(t)
	t.Run("extra grants are taken back", func(t *testing.T) {
		if err := superExec(ctx, d.name, "GRANT SELECT ON public.users TO bekup_external",
			"GRANT USAGE ON SCHEMA public TO bekup_external",
			"GRANT EXECUTE ON FUNCTION public.bekup_schema_version() TO bekup_external",
			"GRANT SELECT ON api.request_status_result TO bekup_external"); err != nil {
			t.Fatal(err)
		}
		err := db.CheckExternalRole(ctx, ext)
		for _, want := range []string{"privileges on public.users", "USAGE or CREATE on schema public",
			"EXECUTE on public.bekup_schema_version()", "privileges on api.request_status_result"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("audit misses %q: %v", want, err)
			}
		}
		if _, err := m.Up(ctx); err != nil {
			t.Fatalf("migrate up did not repair the grants: %v", err)
		}
		if err := db.CheckExternalRole(ctx, ext); err != nil {
			t.Fatalf("after the sync: %v", err)
		}
		if got := snapshot(); got != first {
			t.Fatalf("after the repair\nwant:\n%s\ngot:\n%s", first, got)
		}
	})

	t.Run("membership refused", func(t *testing.T) {
		weaken(ctx, t, []string{"CREATE ROLE bekup_test_ext_group NOLOGIN", "GRANT bekup_test_ext_group TO bekup_external"},
			[]string{"REVOKE bekup_test_ext_group FROM bekup_external", "DROP ROLE bekup_test_ext_group"})
		if _, err := m.Up(ctx); err == nil || !strings.Contains(err.Error(), "member of role bekup_test_ext_group") {
			t.Fatalf("migrate up: %v", err)
		}
		if err := db.CheckExternalRole(ctx, ext); err == nil || !strings.Contains(err.Error(), "member of role bekup_test_ext_group") {
			t.Fatalf("CheckExternalRole: %v", err)
		}
	})

	t.Run("dangerous membership refused before migrating", func(t *testing.T) {
		weaken(ctx, t, []string{"GRANT pg_read_all_data TO bekup_external"}, []string{"REVOKE pg_read_all_data FROM bekup_external"})
		_, err := db.NewMigrator(ctx, config.DBCredentials{URL: []byte(d.ownerURL)}, tlsVerify, logging.NewRegistry())
		if err == nil || !strings.Contains(err.Error(), "bekup_external must not be a member of pg_read_all_data") {
			t.Fatalf("NewMigrator: %v", err)
		}
		if err := db.CheckExternalRole(ctx, ext); err == nil || !strings.Contains(err.Error(), "pg_read_all_data") {
			t.Fatalf("CheckExternalRole: %v", err)
		}
	})

	t.Run("member of bekup_app refused", func(t *testing.T) {
		weaken(ctx, t, []string{"GRANT bekup_app TO bekup_external"}, []string{"REVOKE bekup_app FROM bekup_external"})
		_, err := db.NewMigrator(ctx, config.DBCredentials{URL: []byte(d.ownerURL)}, tlsVerify, logging.NewRegistry())
		if err == nil || !strings.Contains(err.Error(), "bekup_external must not be a member of bekup_app") {
			t.Fatalf("NewMigrator: %v", err)
		}
	})

	t.Run("no-op without the role", func(t *testing.T) {
		// Roles are cluster-wide: rename it away for the subtest (the
		// SCRAM verifier does not depend on the name).
		weaken(ctx, t, []string{"ALTER ROLE bekup_external RENAME TO bekup_external_away"},
			[]string{"ALTER ROLE bekup_external_away RENAME TO bekup_external"})
		if synced, err := owner.SyncExternalGrants(ctx); err != nil || synced {
			t.Fatalf("SyncExternalGrants without the role: %v %v", synced, err)
		}
		if _, err := m.Up(ctx); err != nil {
			t.Fatalf("migrate up without the role: %v", err)
		}
	})
}

// TestExternalInitScript runs deploy/postgres/init/20-bekup-external.sh again
// on the running server, as the documented upgrade command does: it is
// idempotent, restores the role's attributes, adds its pg_hba rule once,
// refuses a password shared with another bekup role and does nothing while
// the external API is not enabled.
func TestExternalInitScript(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	const script = "/docker-entrypoint-initdb.d/20-bekup-external.sh"
	run := func(env ...string) (string, error) {
		return containerExec(ctx, append(append([]string{"env"}, env...), script, "--reload")...)
	}
	weaken(ctx, t, []string{"ALTER ROLE bekup_external CONNECTION LIMIT 5 INHERIT"}, []string{"ALTER ROLE bekup_external CONNECTION LIMIT 20 NOINHERIT"})
	for range 2 {
		if out, err := run(); err != nil || !strings.Contains(out, "role bekup_external ready") {
			t.Fatalf("re-run: %v\n%s", err, out)
		}
	}
	var limit int
	var inherit bool
	if err := superQuery(ctx, "postgres", "SELECT rolconnlimit, rolinherit FROM pg_roles WHERE rolname = 'bekup_external'", &limit, &inherit); err != nil {
		t.Fatal(err)
	}
	if limit != 20 || inherit {
		t.Fatalf("attributes not restored: limit %d inherit %t", limit, inherit)
	}
	out, err := containerExec(ctx, "sh", "-c", `grep -c 'bekup_external only over TLS' "$PGDATA/pg_hba.conf"; head -n 2 "$PGDATA/pg_hba.conf"`)
	if err != nil || !strings.HasPrefix(out, "1\n") || !strings.Contains(out, "hostnossl  all  bekup_external  all  reject") {
		t.Fatalf("pg_hba.conf: %v\n%s", err, out)
	}
	// The password still works and TLS is still required after the reload.
	d := newTestDB(t)
	p, err := connectWith(ctx, d.externalURL, tlsVerify)
	if err != nil {
		t.Fatalf("login after the re-run: %v", err)
	}
	p.Close()

	if out, err := run("POSTGRES_BEKUP_EXTERNAL_PASSWORD_FILE=/run/secrets/bekup_app_password"); err == nil || !strings.Contains(out, "must differ") {
		t.Fatalf("shared password accepted: %v\n%s", err, out)
	}
	if out, err := run("POSTGRES_BEKUP_EXTERNAL_PASSWORD_FILE=/run/secrets/none"); err != nil || !strings.Contains(out, "is not created") {
		t.Fatalf("missing password file: %v\n%s", err, out)
	}
	// Not enabled (BEKUP_EXTERNAL_API unset, D-220 amendment 4): nothing is
	// created or changed, not even with a shared password.
	for _, v := range []string{"POSTGRES_BEKUP_EXTERNAL_API=", "POSTGRES_BEKUP_EXTERNAL_API=yes"} {
		if out, err := run(v, "POSTGRES_BEKUP_EXTERNAL_PASSWORD_FILE=/run/secrets/bekup_app_password"); err != nil || !strings.Contains(out, "is not enabled") {
			t.Fatalf("%s: %v\n%s", v, err, out)
		}
	}
}

// apiFixture is a migrated database with users, jobs and the three pools.
type apiFixture struct {
	t     *testing.T
	ctx   context.Context
	d     testDB
	app   *pgxpool.Pool
	owner *pgxpool.Pool
	appQ  *sqlc.Queries
	extQ  *sqlc.Queries
}

type apiToken struct {
	id   uuid.UUID
	hash []byte
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := withTimeout(t, 3*time.Minute)
	d := migratedDB(t)
	f := &apiFixture{t: t, ctx: ctx, d: d, app: d.appPool(t), owner: d.ownerPool(t)}
	f.appQ, f.extQ = sqlc.New(f.app), sqlc.New(d.externalPool(t))
	return f
}

func (f *apiFixture) user(role sqlc.UserRole) uuid.UUID {
	f.t.Helper()
	u, err := f.appQ.InsertUser(f.ctx, sqlc.InsertUserParams{Email: uuid.NewString()[:8] + "@example.com", Role: role})
	if err != nil {
		f.t.Fatal(err)
	}
	return u.ID
}

func (f *apiFixture) job(enabled, deleted bool, owners ...uuid.UUID) uuid.UUID {
	f.t.Helper()
	id, err := dbtest.InsertJob(f.ctx, f.app, dbtest.JobRow{Enabled: enabled, Deleted: deleted})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, o := range owners {
		f.exec(f.app, "INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)", id, o)
	}
	return id
}

func (f *apiFixture) exec(p *pgxpool.Pool, sql string, args ...any) {
	f.t.Helper()
	if _, err := p.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func randomHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// token creates a token of createdBy (as bekup_app) for jobs, usable from
// any address.
func (f *apiFixture) token(createdBy uuid.UUID, jobs ...uuid.UUID) apiToken {
	f.t.Helper()
	return f.tokenFrom(createdBy, nil, jobs...)
}

// tokenFrom creates a token limited to cidrs (nil: any address).
func (f *apiFixture) tokenFrom(createdBy uuid.UUID, cidrs []netip.Prefix, jobs ...uuid.UUID) apiToken {
	f.t.Helper()
	tok := apiToken{id: uuid.New(), hash: randomHash(f.t)}
	if _, err := f.appQ.InsertAPIToken(f.ctx, sqlc.InsertAPITokenParams{
		ID: tok.id, Name: "ci " + tok.id.String()[:8], SecretHash: tok.hash, CreatedBy: createdBy, ExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		AllowedCidrs: cidrs, AllowAnyAddress: cidrs == nil,
	}); err != nil {
		f.t.Fatal(err)
	}
	if n, err := f.appQ.InsertAPITokenJobs(f.ctx, sqlc.InsertAPITokenJobsParams{TokenID: tok.id, JobIds: jobs}); err != nil || int(n) != len(jobs) {
		f.t.Fatalf("token jobs: %d %v", n, err)
	}
	return tok
}

// testClientIP is the client address of the fixture's calls.
var testClientIP = netip.MustParseAddr("2001:db8::7")

func (f *apiFixture) request(tok apiToken, job uuid.UUID, key string) sqlc.ApiRequestRunResult {
	f.t.Helper()
	ip := testClientIP
	p := sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: job, Ip: &ip}
	if key != "" {
		p.IdempotencyKey = &key
	}
	r, err := f.extQ.ExternalRequestRun(f.ctx, p)
	if err != nil {
		f.t.Fatalf("request_run: %v", err)
	}
	return r
}

func refusal(r sqlc.ApiRequestRunResult) string {
	if r.Refusal == nil {
		return ""
	}
	return *r.Refusal
}

// wantRefusal checks a request's refusal ("" = accepted).
func (f *apiFixture) wantRefusal(tok apiToken, job uuid.UUID, want string) sqlc.ApiRequestRunResult {
	f.t.Helper()
	r := f.request(tok, job, "")
	if got := refusal(r); got != want {
		f.t.Fatalf("refusal %q, want %q", got, want)
	}
	if (want == "") != (r.RequestID != nil) {
		f.t.Fatalf("request id %v with refusal %q", r.RequestID, want)
	}
	return r
}

func (f *apiFixture) authenticated(tok apiToken) bool {
	f.t.Helper()
	ip := testClientIP
	_, err := f.extQ.ExternalAuthenticate(f.ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: &ip})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return true
}

func (f *apiFixture) status(tok apiToken, req uuid.UUID) (sqlc.ApiRequestStatusResult, bool) {
	f.t.Helper()
	ip := testClientIP
	s, err := f.extQ.ExternalRequestStatus(f.ctx, sqlc.ExternalRequestStatusParams{TokenID: tok.id, SecretHash: tok.hash, RequestID: req, ClientIp: &ip})
	if errors.Is(err, pgx.ErrNoRows) {
		return s, false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return s, true
}

// age moves a request's created_at back (as a superuser without triggers:
// the guard keeps created_at immutable).
func (f *apiFixture) age(id uuid.UUID, by string) {
	f.t.Helper()
	if err := superExec(f.ctx, f.d.name, "SET session_replication_role = replica",
		"UPDATE run_requests SET created_at = now() - interval '"+by+"' WHERE id = '"+id.String()+"'"); err != nil {
		f.t.Fatal(err)
	}
}

// rejectPending decides every pending request (frees the pending cap).
func (f *apiFixture) rejectPending() {
	f.t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.WithoutCancel(f.ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			f.t.Error(err)
		}
	}()
	q := sqlc.New(tx)
	rows, err := q.ClaimPendingRunRequests(f.ctx, sqlc.ClaimPendingRunRequestsParams{MaxAgeSeconds: 600, MaxRows: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, r := range rows {
		if n, err := q.RejectRunRequest(f.ctx, sqlc.RejectRunRequestParams{ID: r.ID, Reason: "run_active"}); err != nil || n != 1 {
			f.t.Fatalf("reject: %d %v", n, err)
		}
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func TestAPIRequestRun(t *testing.T) {
	f := newAPIFixture(t)
	ctx := f.ctx
	admin := f.user(sqlc.UserRoleAdmin)
	jobA, jobB := f.job(true, false), f.job(true, false)
	disabled, deleted := f.job(false, false), f.job(true, true)
	tok := f.token(admin, jobA, jobB, disabled, deleted)

	// Authentication.
	if !f.authenticated(tok) {
		t.Fatal("valid token not authenticated")
	}
	a, err := f.extQ.ExternalAuthenticate(ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: &testClientIP})
	if err != nil || *a.TokenID != tok.id || !strings.HasPrefix(*a.Name, "ci ") || a.ExpiresAt == nil {
		t.Fatalf("authenticate: %+v %v", a, err)
	}
	wrong := apiToken{id: tok.id, hash: randomHash(t)}
	if f.authenticated(wrong) || f.authenticated(apiToken{id: uuid.New(), hash: tok.hash}) {
		t.Fatal("wrong secret or id authenticated")
	}
	if got := refusal(f.request(wrong, jobA, "")); got != "unauthenticated" {
		t.Fatalf("wrong secret: %q", got)
	}
	if r, err := f.extQ.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash[:16], JobID: jobA}); err != nil || refusal(r) != "unauthenticated" {
		t.Fatalf("short hash: %+v %v", r, err)
	}
	jobs, err := f.extQ.ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: &testClientIP})
	if err != nil || len(jobs) != 3 {
		t.Fatalf("token jobs (the deleted job is left out): %+v %v", jobs, err)
	}
	if jobs, err := f.extQ.ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{TokenID: tok.id, SecretHash: wrong.hash, ClientIp: &testClientIP}); err != nil || len(jobs) != 0 {
		t.Fatalf("token jobs with a wrong secret: %+v %v", jobs, err)
	}

	// Scope and job state.
	f.wantRefusal(tok, uuid.New(), "not_found")
	f.wantRefusal(tok, deleted, "not_found")
	f.wantRefusal(tok, f.job(true, false), "not_found") // not in the scope
	f.wantRefusal(tok, disabled, "job_disabled")
	bad := "has space"
	if r, err := f.extQ.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: jobA, IdempotencyKey: &bad}); err != nil || refusal(r) != "invalid_request" {
		t.Fatalf("bad idempotency key: %+v %v", r, err)
	}
	net := netip.MustParseAddr("192.0.2.0")
	if _, err := f.extQ.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: jobA, Ip: &net}); err != nil {
		t.Fatalf("host address: %v", err)
	}
	f.rejectPending()

	// A backup_run of the job in one of River's unique states.
	var riverID int64
	if err := f.owner.QueryRow(ctx, `INSERT INTO river_job (args, kind, max_attempts, queue, state)
		VALUES (jsonb_build_object('job_id', $1::uuid), 'backup_run', 3, 'backup', 'running') RETURNING id`, jobB).Scan(&riverID); err != nil {
		t.Fatal(err)
	}
	f.wantRefusal(tok, jobB, "run_active")
	for _, state := range []string{"retryable", "scheduled", "available"} {
		f.exec(f.owner, "UPDATE river_job SET state = $2::river_job_state WHERE id = $1", riverID, state)
		f.wantRefusal(tok, jobB, "run_active")
	}
	f.exec(f.owner, "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = $1", riverID)
	f.wantRefusal(tok, jobB, "")
	f.rejectPending()

	// Accepted: inserted pending and announced.
	listener, err := pgx.Connect(ctx, listenURL(f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(context.WithoutCancel(ctx)); err != nil {
			t.Error(err)
		}
	}()
	if _, err := listener.Exec(ctx, "LISTEN bekup_api_requests"); err != nil {
		t.Fatal(err)
	}
	first := f.request(tok, jobA, "build-42")
	if refusal(first) != "" || first.RequestID == nil || first.CreatedAt == nil || *first.Replayed {
		t.Fatalf("first request: %+v", first)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	n, err := listener.WaitForNotification(wctx)
	cancel()
	if err != nil || n.Payload != first.RequestID.String() {
		t.Fatalf("notification: %+v %v", n, err)
	}

	// Idempotency: replay within 24 h, mismatch for another job, released
	// after 24 h.
	again := f.request(tok, jobA, "build-42")
	if refusal(again) != "" || *again.RequestID != *first.RequestID || !*again.Replayed {
		t.Fatalf("replay: %+v", again)
	}
	if got := refusal(f.request(tok, jobB, "build-42")); got != "idempotency_mismatch" {
		t.Fatalf("key for another job: %q", got)
	}
	f.age(*first.RequestID, "25 hours")
	fresh := f.request(tok, jobA, "build-42")
	if refusal(fresh) != "" || *fresh.RequestID == *first.RequestID || *fresh.Replayed {
		t.Fatalf("key after 24 h: %+v", fresh)
	}

	// Pending cap: 3 undecided requests per token (first and fresh are
	// pending).
	f.wantRefusal(tok, jobB, "")
	f.wantRefusal(tok, jobB, "too_many_pending")
	f.rejectPending()

	// Hourly cap: 30 requests in the last hour.
	var inHour int
	if err := f.owner.QueryRow(ctx, "SELECT count(*) FROM run_requests WHERE token_id = $1 AND created_at > now() - interval '1 hour'", tok.id).Scan(&inHour); err != nil {
		t.Fatal(err)
	}
	f.exec(f.owner, `INSERT INTO run_requests (token_id, job_id, outcome, reason, decided_at, created_at)
		SELECT $1, $2, 'rejected', 'run_active', now(), now() - interval '50 minutes' FROM generate_series(1, $3::int)`, tok.id, jobA, 30-inHour)
	r := f.wantRefusal(tok, jobA, "rate_limited")
	if r.RetryAt == nil || time.Until(*r.RetryAt) < 5*time.Minute || time.Until(*r.RetryAt) > 60*time.Minute {
		t.Fatalf("retry_at %v", r.RetryAt)
	}

	// Read committed only.
	tx, err := f.d.externalPool(t).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlc.New(tx).ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: jobA})
	if pgCode(err) != "55000" {
		t.Fatalf("repeatable read: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

// listenURL is a plain session in the fixture's database for LISTEN (the
// test superuser; LISTEN needs no privilege).
func listenURL(f *apiFixture) string { return urlFor(superUser, superPassword, f.d.name) }

// TestAPIScopeRule: revocation, expiry, demotion, disabling and ownership
// loss take effect at once, in every function.
func TestAPIScopeRule(t *testing.T) {
	f := newAPIFixture(t)
	ctx := f.ctx
	admin, owner, other, maint := f.user(sqlc.UserRoleAdmin), f.user(sqlc.UserRoleUser), f.user(sqlc.UserRoleUser), f.user(sqlc.UserRoleMaintainer)
	jobA, jobB := f.job(true, false, owner), f.job(true, false, owner)

	allowed := func(tok apiToken, job uuid.UUID) bool {
		t.Helper()
		ok, err := f.appQ.APITokenJobAllowed(ctx, sqlc.APITokenJobAllowedParams{TokenID: tok.id, JobID: job})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	// A user's token covers only the jobs the user owns.
	userTok := f.token(owner, jobA)
	notOwned := f.token(other, jobA)
	if !allowed(userTok, jobA) || allowed(notOwned, jobA) {
		t.Fatal("ownership rule")
	}
	f.wantRefusal(notOwned, jobA, "not_found")
	req := f.wantRefusal(userTok, jobA, "")
	if _, ok := f.status(userTok, *req.RequestID); !ok {
		t.Fatal("own request not visible")
	}
	f.exec(f.app, "DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2", jobA, owner)
	if allowed(userTok, jobA) {
		t.Fatal("ownership loss kept the scope")
	}
	f.wantRefusal(userTok, jobA, "not_found")
	if _, ok := f.status(userTok, *req.RequestID); ok {
		t.Fatal("status visible after the ownership loss")
	}

	// A maintainer's token never works (maintainers cannot hold tokens).
	maintTok := f.token(maint, jobB)
	if f.authenticated(maintTok) || allowed(maintTok, jobB) {
		t.Fatal("maintainer token accepted")
	}

	// Demotion of an admin: admin -> user keeps only owned jobs, ->
	// maintainer ends the token; disabling and deleting end it too.
	adminTok := f.token(admin, jobA, jobB)
	if !allowed(adminTok, jobA) || !allowed(adminTok, jobB) {
		t.Fatal("admin scope")
	}
	f.exec(f.app, "INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)", jobB, admin)
	f.exec(f.app, "UPDATE users SET role = 'user' WHERE id = $1", admin)
	if allowed(adminTok, jobA) || !allowed(adminTok, jobB) || !f.authenticated(adminTok) {
		t.Fatal("demotion to user")
	}
	f.exec(f.app, "UPDATE users SET role = 'maintainer' WHERE id = $1", admin)
	if allowed(adminTok, jobB) || f.authenticated(adminTok) {
		t.Fatal("demotion to maintainer")
	}
	f.exec(f.app, "UPDATE users SET role = 'admin', disabled = true WHERE id = $1", admin)
	if f.authenticated(adminTok) {
		t.Fatal("disabled creator")
	}
	f.exec(f.app, "UPDATE users SET disabled = false WHERE id = $1", admin)
	if !f.authenticated(adminTok) {
		t.Fatal("re-enabled creator")
	}

	// Revocation is immediate and one-way.
	if _, err := f.appQ.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: adminTok.id, RevokedBy: &admin}); err != nil {
		t.Fatal(err)
	}
	if f.authenticated(adminTok) || allowed(adminTok, jobB) {
		t.Fatal("revoked token accepted")
	}
	f.wantRefusal(adminTok, jobB, "unauthenticated")
	if _, err := f.appQ.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: adminTok.id, RevokedBy: &admin}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second revoke: %v", err)
	}
	if _, err := f.app.Exec(ctx, "UPDATE api_tokens SET revoked_at = NULL, revoked_by = NULL WHERE id = $1", adminTok.id); pgCode(err) != "42501" {
		t.Fatalf("un-revoke: %v", err)
	}

	// Expiry: past tokens and an expiry beyond 366 days.
	expired := apiToken{id: uuid.New(), hash: randomHash(t)}
	f.exec(f.owner, `INSERT INTO api_tokens (id, name, secret_hash, created_by, created_at, expires_at, allow_any_address)
		VALUES ($1, 'old', $2, $3, now() - interval '2 days', now() - interval '1 second', true)`, expired.id, expired.hash, admin)
	f.exec(f.owner, "INSERT INTO api_token_jobs (token_id, job_id) VALUES ($1, $2)", expired.id, jobB)
	if f.authenticated(expired) || allowed(expired, jobB) {
		t.Fatal("expired token accepted")
	}
	f.wantRefusal(expired, jobB, "unauthenticated")
	_, err := f.appQ.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{ID: uuid.New(), Name: "long", SecretHash: randomHash(t), CreatedBy: admin, ExpiresAt: time.Now().Add(367 * 24 * time.Hour), AllowAnyAddress: true})
	if pgCode(err) != "23514" {
		t.Fatalf("expiry beyond 366 days: %v", err)
	}
	_, err = f.appQ.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{ID: uuid.New(), Name: "dup", SecretHash: userTok.hash, CreatedBy: admin, ExpiresAt: time.Now().Add(time.Hour), AllowAnyAddress: true})
	if pgCode(err) != "23505" {
		t.Fatalf("duplicate secret hash: %v", err)
	}
}

// TestAPIRequestStatus: only the token's own requests, the raw facts of
// the latest attempt and the River state.
func TestAPIRequestStatus(t *testing.T) {
	f := newAPIFixture(t)
	ctx := f.ctx
	admin := f.user(sqlc.UserRoleAdmin)
	job := f.job(true, false)
	tok, otherTok := f.token(admin, job), f.token(admin, job)
	req := *f.wantRefusal(tok, job, "").RequestID

	s, ok := f.status(tok, req)
	if !ok || *s.Outcome != "pending" || *s.JobID != job || s.RiverJobID != nil || s.RunID != nil {
		t.Fatalf("pending status: %+v %v", s, ok)
	}
	if _, ok := f.status(otherTok, req); ok {
		t.Fatal("another token's request visible")
	}
	if _, ok := f.status(apiToken{id: tok.id, hash: randomHash(t)}, req); ok {
		t.Fatal("visible with a wrong secret")
	}

	// The consumer accepts it: River job + queued run, then a retry.
	tx, err := f.app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(tx)
	claimed, err := q.ClaimPendingRunRequests(ctx, sqlc.ClaimPendingRunRequestsParams{MaxAgeSeconds: 600, MaxRows: 20})
	if err != nil || len(claimed) != 1 || claimed[0].ID != req || claimed[0].Expired {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	// A second consumer skips the locked row.
	other, err := f.app.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := sqlc.New(other).ClaimPendingRunRequests(ctx, sqlc.ClaimPendingRunRequestsParams{MaxAgeSeconds: 600, MaxRows: 20}); err != nil || len(rows) != 0 {
		t.Fatalf("second consumer: %+v %v", rows, err)
	}
	if err := other.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var riverID int64
	if err := f.owner.QueryRow(ctx, `INSERT INTO river_job (args, kind, max_attempts, queue, state)
		VALUES (jsonb_build_object('job_id', $1::uuid), 'backup_run', 3, 'backup', 'available') RETURNING id`, job).Scan(&riverID); err != nil {
		t.Fatal(err)
	}
	run, err := q.InsertRun(ctx, sqlc.InsertRunParams{JobID: job, Trigger: sqlc.RunTriggerApi, RiverJobID: &riverID, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := q.AcceptRunRequest(ctx, sqlc.AcceptRunRequestParams{ID: req, RiverJobID: riverID, RunID: run.ID}); err != nil || n != 1 {
		t.Fatalf("accept: %d %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s, ok = f.status(tok, req)
	if !ok || *s.Outcome != "accepted" || *s.RiverJobID != riverID || *s.RunID != run.ID || *s.RunStatus != "queued" ||
		*s.RunAttempt != 1 || *s.RiverState != "available" || s.DecidedAt == nil || s.RunQueuedAt == nil {
		t.Fatalf("accepted status: %+v", s)
	}
	f.exec(f.owner, "UPDATE runs SET status = 'failed', started_at = now(), finished_at = now() WHERE id = $1", run.ID)
	f.exec(f.owner, "UPDATE river_job SET state = 'retryable', attempt = 1 WHERE id = $1", riverID)
	s, _ = f.status(tok, req)
	if *s.RunStatus != "failed" || *s.RiverState != "retryable" {
		t.Fatalf("retrying: %+v", s)
	}
	retry, err := f.appQ.InsertRun(ctx, sqlc.InsertRunParams{JobID: job, Trigger: sqlc.RunTriggerApi, RiverJobID: &riverID, Attempt: 2})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = f.status(tok, req)
	if *s.RunID != retry.ID || *s.RunAttempt != 2 || *s.RunStatus != "queued" {
		t.Fatalf("latest attempt: %+v", s)
	}
	// River deleted the finished job: the state is NULL.
	f.exec(f.owner, "DELETE FROM river_job WHERE id = $1", riverID)
	if s, _ = f.status(tok, req); s.RiverState != nil {
		t.Fatalf("river state of a deleted job: %v", *s.RiverState)
	}

	// Decided once; identity immutable; bekup_app cannot file or delete
	// tokens/scopes.
	if n, err := f.appQ.RejectRunRequest(ctx, sqlc.RejectRunRequestParams{ID: req, Reason: "expired"}); err != nil || n != 0 {
		t.Fatalf("second decision: %d %v", n, err)
	}
	for stmt, code := range map[string]string{
		"UPDATE run_requests SET outcome = 'rejected', reason = 'expired', river_job_id = NULL, run_id = NULL WHERE id = '" + req.String() + "'": "42501",
		"UPDATE run_requests SET job_id = gen_random_uuid()":                                                     "42501",
		"INSERT INTO run_requests (token_id, job_id) VALUES ('" + tok.id.String() + "', '" + job.String() + "')": "42501",
		"DELETE FROM api_tokens":                                      "42501",
		"DELETE FROM api_token_jobs":                                  "42501",
		"UPDATE api_token_jobs SET job_id = gen_random_uuid()":        "42501",
		"UPDATE api_tokens SET expires_at = now() + interval '1 day'": "42501",
		"UPDATE api_tokens SET secret_hash = secret_hash":             "42501",
	} {
		if _, err := f.app.Exec(ctx, stmt); pgCode(err) != code {
			t.Errorf("%s: %v (want %s)", stmt, err, code)
		}
	}

	// Pruning removes old decided requests only.
	f.age(req, "31 days")
	pending := *f.wantRefusal(tok, job, "").RequestID
	f.age(pending, "31 days")
	if n, err := f.appQ.PruneRunRequests(ctx, sqlc.PruneRunRequestsParams{Before: time.Now().Add(-30 * 24 * time.Hour), MaxRows: 100}); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if _, ok := f.status(tok, req); ok {
		t.Fatal("pruned request still visible")
	}
	if _, ok := f.status(tok, pending); !ok {
		t.Fatal("pending request pruned")
	}

	// Last used: the newest request of the token.
	got, err := f.appQ.GetAPIToken(ctx, tok.id)
	if err != nil || got.LastUsedAt == nil {
		t.Fatalf("last used: %+v %v", got, err)
	}
	if got, err := f.appQ.GetAPIToken(ctx, otherTok.id); err != nil || got.LastUsedAt != nil {
		t.Fatalf("unused token: %+v %v", got, err)
	}
	list, err := f.appQ.ListAPITokensForJob(ctx, sqlc.ListAPITokensForJobParams{JobID: job, MaxRows: 10})
	if err != nil || len(list) != 2 {
		t.Fatalf("tokens of the job: %+v %v", list, err)
	}
	scopes, err := f.appQ.ListAPITokenJobs(ctx, []uuid.UUID{tok.id, otherTok.id})
	if err != nil || len(scopes) != 2 || scopes[0].JobDeleted {
		t.Fatalf("scopes: %+v %v", scopes, err)
	}
}

// The client address allowlist of tokens (D-220 amendment 1).

var (
	allowedV4 = netip.MustParsePrefix("192.0.2.0/24")
	allowedV6 = netip.MustParsePrefix("2001:db8:1::/48")
)

func addrPtr(s string) *netip.Addr {
	if s == "" {
		return nil
	}
	a := netip.MustParseAddr(s)
	return &a
}

// refusedEverywhere: the token gets the generic refusal of a wrong secret
// from ip in every api function.
func (f *apiFixture) refusedEverywhere(tok apiToken, job, req uuid.UUID, ip *netip.Addr) {
	f.t.Helper()
	ctx := f.ctx
	if _, err := f.extQ.ExternalAuthenticate(ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: ip}); !errors.Is(err, pgx.ErrNoRows) {
		f.t.Fatalf("authenticate from %v: %v", ip, err)
	}
	if jobs, err := f.extQ.ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: ip}); err != nil || len(jobs) != 0 {
		f.t.Fatalf("token_jobs from %v: %+v %v", ip, jobs, err)
	}
	r, err := f.extQ.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: job, Ip: ip})
	if err != nil || refusal(r) != "unauthenticated" || r.RequestID != nil || r.RetryAt != nil || *r.Replayed {
		f.t.Fatalf("request_run from %v: %+v %v", ip, r, err)
	}
	if _, err := f.extQ.ExternalRequestStatus(ctx, sqlc.ExternalRequestStatusParams{TokenID: tok.id, SecretHash: tok.hash, RequestID: req, ClientIp: ip}); !errors.Is(err, pgx.ErrNoRows) {
		f.t.Fatalf("request_status from %v: %v", ip, err)
	}
}

// admittedEverywhere: every api function works for the token from ip.
func (f *apiFixture) admittedEverywhere(tok apiToken, job uuid.UUID, ip *netip.Addr) uuid.UUID {
	f.t.Helper()
	ctx := f.ctx
	if a, err := f.extQ.ExternalAuthenticate(ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: ip}); err != nil || *a.TokenID != tok.id {
		f.t.Fatalf("authenticate from %v: %+v %v", ip, a, err)
	}
	if jobs, err := f.extQ.ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: ip}); err != nil || len(jobs) != 1 {
		f.t.Fatalf("token_jobs from %v: %+v %v", ip, jobs, err)
	}
	r, err := f.extQ.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: job, Ip: ip})
	if err != nil || refusal(r) != "" || r.RequestID == nil {
		f.t.Fatalf("request_run from %v: %+v %v", ip, r, err)
	}
	if s, err := f.extQ.ExternalRequestStatus(ctx, sqlc.ExternalRequestStatusParams{TokenID: tok.id, SecretHash: tok.hash, RequestID: *r.RequestID, ClientIp: ip}); err != nil || *s.RequestID != *r.RequestID {
		f.t.Fatalf("request_status from %v: %+v %v", ip, s, err)
	}
	f.rejectPending()
	return *r.RequestID
}

type denialRow struct {
	id       uuid.UUID
	ip       *netip.Addr
	deniedAt time.Time
}

func (f *apiFixture) denials(tok apiToken) []denialRow {
	f.t.Helper()
	rows, err := f.app.Query(f.ctx, `SELECT id, client_ip, denied_at FROM api_access_denials WHERE token_id = $1 ORDER BY denied_at, id`, tok.id)
	if err != nil {
		f.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (denialRow, error) {
		var d denialRow
		return d, r.Scan(&d.id, &d.ip, &d.deniedAt)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// ageDenials moves a token's denials back (as a superuser without
// triggers: the guard keeps denied_at immutable).
func (f *apiFixture) ageDenials(tok apiToken, by string) {
	f.t.Helper()
	if err := superExec(f.ctx, f.d.name, "SET session_replication_role = replica",
		"UPDATE api_access_denials SET denied_at = denied_at - interval '"+by+"' WHERE token_id = '"+tok.id.String()+"'"); err != nil {
		f.t.Fatal(err)
	}
}

// TestAPIAddressConstraints: an allowlist of 1-32 distinct networks
// none wider than /8 (IPv4) or /32 (IPv6), without IPv4-mapped entries, or allow_any_address, exactly one.
func TestAPIAddressConstraints(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.user(sqlc.UserRoleAdmin)
	insert := func(cidrs []netip.Prefix, allowAny bool) error {
		_, err := f.appQ.InsertAPIToken(f.ctx, sqlc.InsertAPITokenParams{
			ID: uuid.New(), Name: "c", SecretHash: randomHash(t), CreatedBy: admin, ExpiresAt: time.Now().Add(time.Hour),
			AllowedCidrs: cidrs, AllowAnyAddress: allowAny,
		})
		return err
	}
	many := func(n int) []netip.Prefix {
		out := make([]netip.Prefix, 0, n)
		for i := range n {
			out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i), 0, 0}), 16))
		}
		return out
	}
	p := netip.MustParsePrefix
	for name, c := range map[string]struct {
		cidrs []netip.Prefix
		any   bool
	}{
		"empty list without any":  {nil, false},
		"empty array without any": {[]netip.Prefix{}, false},
		"list and any":            {[]netip.Prefix{allowedV4}, true},
		"IPv4 /0":                 {[]netip.Prefix{allowedV4, p("0.0.0.0/0")}, false},
		"IPv6 /0":                 {[]netip.Prefix{p("::/0")}, false},
		"IPv4 wider than /8":      {[]netip.Prefix{allowedV4, p("10.0.0.0/7")}, false},
		"IPv4 /1":                 {[]netip.Prefix{p("0.0.0.0/1"), p("128.0.0.0/1")}, false},
		"IPv6 wider than /32":     {[]netip.Prefix{p("2001:db8::/31")}, false},
		"33 entries":              {many(33), false},
		"duplicate":               {[]netip.Prefix{allowedV4, allowedV6, allowedV4}, false},
		"IPv4-mapped network":     {[]netip.Prefix{p("::ffff:192.0.2.0/120")}, false},
	} {
		if err := insert(c.cidrs, c.any); pgCode(err) != "23514" {
			t.Errorf("%s: %v, want a CHECK violation", name, err)
		}
	}
	for name, c := range map[string]struct {
		cidrs []netip.Prefix
		any   bool
	}{
		"any":        {nil, true},
		"32 entries": {many(32), false},
		"v4 and v6":  {[]netip.Prefix{allowedV4, allowedV6, p("198.51.100.7/32"), p("2001:db8::1/128")}, false},
		"overlap":    {[]netip.Prefix{p("10.0.0.0/8"), p("10.1.0.0/16")}, false},
		"widest":     {[]netip.Prefix{p("10.0.0.0/8"), p("2001:db8::/32")}, false},
	} {
		if err := insert(c.cidrs, c.any); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Host bits are refused by the cidr type itself.
	if _, err := f.app.Exec(f.ctx, `INSERT INTO api_tokens (id, name, secret_hash, created_by, expires_at, allowed_cidrs, allow_any_address)
		VALUES (gen_random_uuid(), 'h', $1, $2, now() + interval '1 hour', '{192.0.2.1/24}', false)`, randomHash(t), admin); pgCode(err) != "22P02" {
		t.Fatalf("host bits: %v", err)
	}
	// allow_any_address has no default: it must be chosen.
	if _, err := f.app.Exec(f.ctx, `INSERT INTO api_tokens (id, name, secret_hash, created_by, expires_at)
		VALUES (gen_random_uuid(), 'd', $1, $2, now() + interval '1 hour')`, randomHash(t), admin); pgCode(err) != "23502" {
		t.Fatalf("no choice: %v", err)
	}

	// Updating the addresses: same rules, unrevoked tokens only.
	job := f.job(true, false)
	tok := f.tokenFrom(admin, []netip.Prefix{allowedV4}, job)
	upd := func(cidrs []netip.Prefix, allowAny bool) error {
		_, err := f.appQ.UpdateAPITokenAddresses(f.ctx, sqlc.UpdateAPITokenAddressesParams{ID: tok.id, AllowedCidrs: cidrs, AllowAnyAddress: allowAny})
		return err
	}
	if err := upd(nil, false); pgCode(err) != "23514" {
		t.Fatalf("update to nothing: %v", err)
	}
	if err := upd([]netip.Prefix{p("::/0")}, false); pgCode(err) != "23514" {
		t.Fatalf("update to /0: %v", err)
	}
	f.admittedEverywhere(tok, job, addrPtr("192.0.2.9"))
	if err := upd([]netip.Prefix{allowedV6}, false); err != nil {
		t.Fatal(err)
	}
	f.refusedEverywhere(tok, job, uuid.New(), addrPtr("192.0.2.9"))
	f.admittedEverywhere(tok, job, addrPtr("2001:db8:1::9"))
	if err := upd(nil, true); err != nil {
		t.Fatal(err)
	}
	f.admittedEverywhere(tok, job, addrPtr("203.0.113.5"))
	if _, err := f.appQ.RevokeAPIToken(f.ctx, sqlc.RevokeAPITokenParams{ID: tok.id, RevokedBy: &admin}); err != nil {
		t.Fatal(err)
	}
	if err := upd([]netip.Prefix{allowedV4}, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update of a revoked token: %v", err)
	}
	if _, err := f.app.Exec(f.ctx, "UPDATE api_tokens SET allow_any_address = false, allowed_cidrs = '{192.0.2.0/24}' WHERE id = $1", tok.id); pgCode(err) != "42501" {
		t.Fatalf("raw update of a revoked token: %v", err)
	}
}

// TestAPIAddressAllowlist: allowed IPv4/IPv6 addresses (also IPv4-mapped)
// work in every function; any other address, none or a network gets the
// generic refusal of a wrong secret; allow_any_address works from
// anywhere.
func TestAPIAddressAllowlist(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.user(sqlc.UserRoleAdmin)
	job := f.job(true, false)
	tok := f.tokenFrom(admin, []netip.Prefix{allowedV4, allowedV6}, job)

	for _, ip := range []string{"192.0.2.1", "192.0.2.254", "::ffff:192.0.2.77", "2001:db8:1::1", "2001:db8:1:ffff::1"} {
		req := f.admittedEverywhere(tok, job, addrPtr(ip))
		// The stored address is the normalized one (IPv4-mapped unmapped).
		var stored netip.Addr
		if err := f.app.QueryRow(f.ctx, "SELECT ip FROM run_requests WHERE id = $1", req).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if want := netip.MustParseAddr(ip).Unmap(); stored != want {
			t.Errorf("stored %s for %s, want %s", stored, ip, want)
		}
	}
	req := f.admittedEverywhere(tok, job, addrPtr("192.0.2.5"))

	// The reference answer: a wrong secret from an allowed address.
	wrong := apiToken{id: tok.id, hash: randomHash(t)}
	f.refusedEverywhere(wrong, job, req, addrPtr("192.0.2.5"))
	for _, ip := range []string{"192.0.3.1", "198.51.100.1", "2001:db8:2::1", "::ffff:198.51.100.1", "::192.0.2.1", ""} {
		f.refusedEverywhere(tok, job, req, addrPtr(ip))
	}
	// A network instead of a host address is refused too (it would
	// otherwise contain itself).
	ext := f.d.externalPool(t)
	var n int
	if err := ext.QueryRow(f.ctx, "SELECT count(*) FROM api.authenticate($1, $2, '192.0.2.0/24'::inet)", tok.id, tok.hash).Scan(&n); err != nil || n != 0 {
		t.Fatalf("authenticate from a network: %d %v", n, err)
	}

	// Any address: an explicit choice, then everything (also no address).
	anyTok := f.token(admin, job)
	for _, ip := range []string{"203.0.113.9", "2001:db8:ffff::1", "::ffff:203.0.113.9", ""} {
		f.admittedEverywhere(anyTok, job, addrPtr(ip))
	}
	if len(f.denials(anyTok)) != 0 {
		t.Fatal("denials for a token without an allowlist")
	}

	// Read committed only (a refusal may write a denial).
	tx, err := ext.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlc.New(tx).ExternalAuthenticate(f.ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: addrPtr("192.0.2.1")})
	if pgCode(err) != "55000" {
		t.Fatalf("repeatable read: %v", err)
	}
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
}

// TestAPIAccessDenials: a valid token from a refused address records one
// denial per token and 10 minutes, announced on bekup_api_requests; wrong
// secrets and invalid tokens record nothing; concurrent refusals write
// one row; bekup_app reads and marks denials but cannot write them.
func TestAPIAccessDenials(t *testing.T) {
	f := newAPIFixture(t)
	ctx := f.ctx
	admin := f.user(sqlc.UserRoleAdmin)
	job := f.job(true, false)
	tok := f.tokenFrom(admin, []netip.Prefix{allowedV4}, job)

	listener, err := pgx.Connect(ctx, listenURL(f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(context.WithoutCancel(ctx)); err != nil {
			t.Error(err)
		}
	}()
	if _, err := listener.Exec(ctx, "LISTEN bekup_api_requests"); err != nil {
		t.Fatal(err)
	}

	// Wrong secret and an unknown token from a refused address: nothing.
	f.refusedEverywhere(apiToken{id: tok.id, hash: randomHash(t)}, job, uuid.New(), addrPtr("198.51.100.1"))
	if d := f.denials(tok); len(d) != 0 {
		t.Fatalf("denial for a wrong secret: %+v", d)
	}

	// The first refusal records a denial (IPv4-mapped stored unmapped);
	// the next ones within 10 minutes do not.
	f.refusedEverywhere(tok, job, uuid.New(), addrPtr("::ffff:198.51.100.1"))
	f.refusedEverywhere(tok, job, uuid.New(), addrPtr("2001:db8:9::1"))
	d := f.denials(tok)
	if len(d) != 1 || d[0].ip == nil || *d[0].ip != netip.MustParseAddr("198.51.100.1") {
		t.Fatalf("denials %+v", d)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	note, err := listener.WaitForNotification(wctx)
	cancel()
	if err != nil || note.Payload != "denial:"+d[0].id.String() {
		t.Fatalf("notification: %+v %v", note, err)
	}

	// After 10 minutes the next refusal records again (no address: NULL).
	f.ageDenials(tok, "11 minutes")
	f.refusedEverywhere(tok, job, uuid.New(), nil)
	if d = f.denials(tok); len(d) != 2 || d[1].ip != nil {
		t.Fatalf("denials after 10 minutes %+v", d)
	}

	// An invalid (revoked) token records nothing.
	revoked := f.tokenFrom(admin, []netip.Prefix{allowedV4}, job)
	if _, err := f.appQ.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: revoked.id, RevokedBy: &admin}); err != nil {
		t.Fatal(err)
	}
	f.refusedEverywhere(revoked, job, uuid.New(), addrPtr("198.51.100.1"))
	if d := f.denials(revoked); len(d) != 0 {
		t.Fatalf("denial for a revoked token: %+v", d)
	}

	// Concurrent refusals through every function: one row.
	busy := f.tokenFrom(admin, []netip.Prefix{allowedV6}, job)
	ext := f.d.externalPool(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for _, from := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3", "203.0.113.4", "2001:db8:2::5", "::ffff:198.51.100.6", "2001:db8:3::7", "192.0.2.8"} {
		wg.Go(func() {
			q := sqlc.New(ext)
			ip := netip.MustParseAddr(from)
			for range 4 {
				if _, err := q.ExternalAuthenticate(ctx, sqlc.ExternalAuthenticateParams{TokenID: busy.id, SecretHash: busy.hash, ClientIp: &ip}); !errors.Is(err, pgx.ErrNoRows) {
					errs <- err
				}
				if r, err := q.ExternalRequestRun(ctx, sqlc.ExternalRequestRunParams{TokenID: busy.id, SecretHash: busy.hash, JobID: job, Ip: &ip}); err != nil || refusal(r) != "unauthenticated" {
					errs <- errors.Join(err, errors.New("request_run not refused"))
				}
				if _, err := q.ExternalTokenJobs(ctx, sqlc.ExternalTokenJobsParams{TokenID: busy.id, SecretHash: busy.hash, ClientIp: &ip}); err != nil {
					errs <- err
				}
				if _, err := q.ExternalRequestStatus(ctx, sqlc.ExternalRequestStatusParams{TokenID: busy.id, SecretHash: busy.hash, RequestID: uuid.New(), ClientIp: &ip}); !errors.Is(err, pgx.ErrNoRows) {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if d := f.denials(busy); len(d) != 1 {
		t.Fatalf("%d denials under concurrent refusals", len(d))
	}

	// bekup_app: read, mark once, never write or change a denial; it
	// cannot run the admission itself.
	q := sqlc.New(f.app)
	claimed, err := q.ClaimAPIAccessDenials(ctx, sqlc.ClaimAPIAccessDenialsParams{MaxRows: 100})
	if err != nil || len(claimed) != 3 {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if claimed[0].TokenID != tok.id || claimed[0].CreatedBy != admin || !strings.HasPrefix(claimed[0].TokenName, "ci ") {
		t.Fatalf("claimed %+v", claimed[0])
	}
	if n, err := q.MarkAPIAccessDenialProcessed(ctx, d[0].id); err != nil || n != 1 {
		t.Fatalf("mark: %d %v", n, err)
	}
	if n, err := q.MarkAPIAccessDenialProcessed(ctx, d[0].id); err != nil || n != 0 {
		t.Fatalf("second mark: %d %v", n, err)
	}
	for stmt, code := range map[string]string{
		"INSERT INTO api_access_denials (token_id) VALUES ('" + tok.id.String() + "')":                          "42501",
		"UPDATE api_access_denials SET client_ip = '192.0.2.1'":                                                 "42501",
		"UPDATE api_access_denials SET processed_at = NULL WHERE id = '" + d[0].id.String() + "'":               "42501",
		"SELECT public.api_token_admit('" + tok.id.String() + "', '\\x00', '192.0.2.1')":                        "42501",
		"SELECT * FROM api.authenticate('" + tok.id.String() + "', '\\x00', '192.0.2.1')":                       "42501",
		"UPDATE api_tokens SET allowed_cidrs = '{10.0.0.0/8}', name = 'x' WHERE id = '" + tok.id.String() + "'": "42501",
	} {
		if _, err := f.app.Exec(ctx, stmt); pgCode(err) != code {
			t.Errorf("%s: %v (want %s)", stmt, err, code)
		}
	}

	// Last seen: the later of the newest request and the newest denial.
	seenTok := f.tokenFrom(admin, []netip.Prefix{allowedV4}, job)
	f.admittedEverywhere(seenTok, job, addrPtr("192.0.2.33"))
	last := func(id uuid.UUID) []sqlc.ListAPITokenLastSeenRow {
		t.Helper()
		rows, err := q.ListAPITokenLastSeen(ctx, []uuid.UUID{id, uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if rows := last(seenTok.id); len(rows) != 1 || rows[0].TokenID != seenTok.id || rows[0].Denied || *rows[0].ClientIp != netip.MustParseAddr("192.0.2.33") {
		t.Fatalf("last seen after a request: %+v", rows)
	}
	f.refusedEverywhere(seenTok, job, uuid.New(), addrPtr("203.0.113.66"))
	if rows := last(seenTok.id); len(rows) != 1 || !rows[0].Denied || *rows[0].ClientIp != netip.MustParseAddr("203.0.113.66") {
		t.Fatalf("last seen after a denial: %+v", rows)
	}
	if rows := last(f.token(admin, job).id); len(rows) != 0 {
		t.Fatalf("last seen of an unused token: %+v", rows)
	}

	// Pruning: processed denials older than the cut-off only.
	f.ageDenials(tok, "31 days")
	n, err := q.PruneAPIAccessDenials(ctx, sqlc.PruneAPIAccessDenialsParams{Before: time.Now().Add(-30 * 24 * time.Hour), MaxRows: 100})
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if d := f.denials(tok); len(d) != 1 || d[0].id == claimed[0].ID {
		t.Fatalf("after the prune: %+v", d)
	}
}
