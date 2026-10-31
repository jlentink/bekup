//go:build integration

package db_test

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/externaltest"
	"github.com/jlentink/bekup/internal/logging"
)

// External database backend (D-207): plain PostgreSQL 15-17 containers with
// a test CA, a non-superuser admin with CREATEROLE and CREATEDB, bootstrap,
// the migrate preflight, migrations and the serve role checks over
// fields-mode credentials.

const (
	extAdmin    = "ext_admin"
	extAdminPW  = "admin-password-0123456789"
	extOwnerPW  = `owner-p@ss'w"o\rd;--0123`
	extAppPW    = `app-p@ss:/?#%&= x-0123456`
	extDatabase = "bekup"
)

type extEnv struct {
	s   *externaltest.Server
	tls db.TLSOptions
}

func newExtEnv(t *testing.T, image string, rsa bool) *extEnv {
	t.Helper()
	s := externaltest.Start(t, image, externaltest.Options{RSA: rsa})
	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = s.CAFile
	o, err := db.LoadTLSOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &extEnv{s: s, tls: o}
}

func (e *extEnv) creds(user, pw, dbname string) config.DBCredentials {
	return config.DBCredentials{Host: e.s.Host, Port: e.s.Port, Database: dbname, User: user, Password: []byte(pw)}
}

func (e *extEnv) bootstrap(ctx context.Context, t *testing.T, mutate func(*db.BootstrapOptions)) (*db.BootstrapResult, error) {
	t.Helper()
	o := db.BootstrapOptions{
		Admin: e.creds(extAdmin, extAdminPW, "postgres"), TLS: e.tls, Database: extDatabase,
		OwnerPassword: []byte(extOwnerPW), AppPassword: []byte(extAppPW), Registry: logging.NewRegistry(),
	}
	if mutate != nil {
		mutate(&o)
	}
	return db.Bootstrap(ctx, o)
}

func (e *extEnv) migrator(ctx context.Context, t *testing.T, creds config.DBCredentials, o db.TLSOptions) (*db.Migrator, error) {
	t.Helper()
	m, err := db.NewMigrator(ctx, creds, o, logging.NewRegistry())
	if err == nil {
		t.Cleanup(func() {
			if err := m.Close(context.WithoutCancel(ctx)); err != nil {
				t.Error(err)
			}
		})
	}
	return m, err
}

func (e *extEnv) pool(ctx context.Context, t *testing.T, creds config.DBCredentials, o db.TLSOptions) (*pgxpool.Pool, error) {
	t.Helper()
	p, err := db.NewPool(ctx, creds, db.PoolOptions{MaxConns: 4, TLS: o, Registry: logging.NewRegistry()})
	if err == nil {
		t.Cleanup(p.Close)
	}
	return p, err
}

// adopt accepts the existing roles (--adopt-existing-roles).
func adopt(o *db.BootstrapOptions) { o.AdoptExistingRoles = true }

func hasAction(r *db.BootstrapResult, prefix string) bool {
	return r != nil && slices.ContainsFunc(r.Actions, func(a string) bool { return strings.HasPrefix(a, prefix) })
}

// migrateAndCheck runs the preflight, the migrations and the serve checks.
func (e *extEnv) migrateAndCheck(t *testing.T, dbname string) {
	t.Helper()
	ctx := t.Context()
	m, err := e.migrator(ctx, t, e.creds(db.OwnerRole, extOwnerPW, dbname), e.tls)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := m.Check(t.Context())
	if err != nil {
		t.Fatalf("preflight: %v (%+v)", err, rep)
	}
	if !rep.TLS.Encrypted || !rep.TLS.HostVerified || rep.SingleAccount {
		t.Fatalf("report %+v", rep)
	}
	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rep, err = m.Check(t.Context()); err != nil || rep.Citext != db.CitextInstalled {
		t.Fatalf("preflight after migrations: %v %+v", err, rep)
	}
	app, err := e.pool(ctx, t, e.creds(db.AppRole, extAppPW, dbname), e.tls)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CheckAppRole(t.Context(), app, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchemaVersion(t.Context(), app); err != nil {
		t.Fatal(err)
	}
}

func TestExternalBootstrapTwoRoles(t *testing.T) {
	for _, tc := range []struct {
		name, image    string
		citextNegative bool
	}{
		{"postgres15", externaltest.PG15, true},
		{"postgres17", externaltest.PG17, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			e := newExtEnv(t, tc.image, false)
			if err := e.s.CreateAdmin(ctx, extAdmin, extAdminPW); err != nil {
				t.Fatal(err)
			}

			res, err := e.bootstrap(ctx, t, func(o *db.BootstrapOptions) { o.DryRun = true })
			if err != nil || res.Changed || !hasAction(res, "create role bekup_owner") {
				t.Fatalf("dry run: %v %+v", err, res)
			}
			res, err = e.bootstrap(ctx, t, nil)
			if err != nil {
				t.Fatalf("bootstrap: %v (%+v)", err, res)
			}
			for _, want := range []string{"create role bekup_owner", "create role bekup_app", "create database bekup", "revoke bekup_owner"} {
				if !hasAction(res, want) {
					t.Errorf("missing action %q in %v", want, res.Actions)
				}
			}
			if !res.Changed || res.Citext != db.CitextCreatable || !res.TLS.HostVerified || res.AdminRole != extAdmin {
				t.Fatalf("result %+v", res)
			}
			// Existing roles are only adopted explicitly.
			if _, err = e.bootstrap(ctx, t, nil); err == nil || !strings.Contains(err.Error(), "--adopt-existing-roles") {
				t.Fatalf("second run without adopting: %v", err)
			}
			// Idempotent: the second run creates nothing.
			res, err = e.bootstrap(ctx, t, adopt)
			if err != nil || res.Changed || hasAction(res, "create ") {
				t.Fatalf("second run: %v %+v", err, res)
			}
			// The admin does not keep bekup_owner's privileges.
			var usage bool
			if err := superQueryRow(t, e, "SELECT pg_has_role('ext_admin', 'bekup_owner', 'USAGE')", &usage); err != nil || usage {
				t.Fatalf("admin still holds bekup_owner: %v %v", usage, err)
			}

			e.migrateAndCheck(t, extDatabase)

			m, err := e.migrator(ctx, t, e.creds(db.OwnerRole, extOwnerPW, extDatabase), e.tls)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := m.Check(ctx)
			if err != nil || !slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, "may connect to other databases: postgres") }) {
				t.Fatalf("warnings: %v %v", err, rep.Warnings)
			}

			t.Run("tls server name", func(t *testing.T) {
				byIP := e.creds(db.AppRole, extAppPW, extDatabase)
				byIP.Host = "127.0.0.1"
				if _, err := e.pool(ctx, t, byIP, e.tls); err != nil {
					t.Fatalf("IP SAN: %v", err)
				}
				o := e.tls
				o.ServerName = externaltest.ServerName
				if _, err := e.pool(ctx, t, byIP, o); err != nil {
					t.Fatalf("tls_server_name: %v", err)
				}
				o.ServerName = "wrong.bekup.test"
				_, err := e.pool(ctx, t, byIP, o)
				if err == nil || !strings.Contains(err.Error(), "certificate") {
					t.Fatalf("SAN mismatch accepted: %v", err)
				}
				if _, err := e.migrator(ctx, t, e.creds(db.OwnerRole, extOwnerPW, extDatabase), o); err == nil {
					t.Fatal("migrator accepted a SAN mismatch")
				}
			})

			t.Run("md5 password", func(t *testing.T) {
				if err := e.s.SuperExec(ctx, "postgres", "SET password_encryption = 'md5'",
					"ALTER ROLE bekup_app PASSWORD '"+strings.ReplaceAll(extAppPW, "'", "''")+"'"); err != nil {
					t.Fatal(err)
				}
				_, err := e.pool(ctx, t, e.creds(db.AppRole, extAppPW, extDatabase), e.tls)
				if err == nil {
					t.Fatal("login with an MD5-stored password succeeded")
				}
				if strings.Contains(err.Error(), "p@ss") {
					t.Fatalf("error leaks the password: %v", err)
				}
				res, err := e.bootstrap(ctx, t, func(o *db.BootstrapOptions) { o.ResetPasswords, o.AdoptExistingRoles = true, true })
				if err != nil || !res.Changed || !hasAction(res, "set the password of role bekup_app") {
					t.Fatalf("reset: %v %+v", err, res)
				}
				if _, err := e.pool(ctx, t, e.creds(db.AppRole, extAppPW, extDatabase), e.tls); err != nil {
					t.Fatalf("after reset: %v", err)
				}
			})

			t.Run("managed admin membership refused", func(t *testing.T) {
				if err := e.s.SuperExec(ctx, "postgres", "CREATE ROLE cloudsqlsuperuser NOLOGIN CREATEROLE CREATEDB",
					"GRANT cloudsqlsuperuser TO bekup_app"); err != nil {
					t.Fatal(err)
				}
				_, err := e.bootstrap(ctx, t, adopt)
				if err == nil || !strings.Contains(err.Error(), "member of cloudsqlsuperuser") {
					t.Fatalf("bootstrap: %v", err)
				}
				app, err := e.pool(ctx, t, e.creds(db.AppRole, extAppPW, extDatabase), e.tls)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.CheckAppRole(ctx, app, ""); err == nil || !strings.Contains(err.Error(), "cloudsqlsuperuser") {
					t.Fatalf("CheckAppRole: %v", err)
				}
				if err := e.s.SuperExec(ctx, "postgres", "REVOKE cloudsqlsuperuser FROM bekup_app"); err != nil {
					t.Fatal(err)
				}
			})

			t.Run("role setting cannot impersonate bekup_app", func(t *testing.T) {
				if err := e.s.SuperExec(ctx, "postgres", "CREATE ROLE sneaky LOGIN PASSWORD 'sneaky-password-0123'",
					"GRANT bekup_app TO sneaky", "GRANT CONNECT ON DATABASE bekup TO sneaky",
					"ALTER ROLE sneaky SET role = 'bekup_app'"); err != nil {
					t.Fatal(err)
				}
				p, err := e.pool(ctx, t, e.creds("sneaky", "sneaky-password-0123", extDatabase), e.tls)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.CheckAppRole(ctx, p, ""); err == nil || !strings.Contains(err.Error(), `logged in as "sneaky"`) {
					t.Fatalf("CheckAppRole: %v", err)
				}
				// The membership itself: bootstrap refuses it, migrate check
				// warns about it.
				if _, err := e.bootstrap(ctx, t, adopt); err == nil || !strings.Contains(err.Error(), "sneaky is a member of bekup_app") {
					t.Fatalf("bootstrap with a foreign member: %v", err)
				}
				m, err := e.migrator(ctx, t, e.creds(db.OwnerRole, extOwnerPW, extDatabase), e.tls)
				if err != nil {
					t.Fatal(err)
				}
				rep, err := m.Check(ctx)
				if err != nil || !slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, "sneaky is a member of bekup_app") }) {
					t.Fatalf("migrate check: %v %v", err, rep.Warnings)
				}
				if err := e.s.SuperExec(ctx, "postgres", "REVOKE bekup_app FROM sneaky"); err != nil {
					t.Fatal(err)
				}
			})

			t.Run("foreign ADMIN OPTION refused", func(t *testing.T) {
				if err := e.s.SuperExec(ctx, "postgres", "CREATE ROLE mallory NOLOGIN", "GRANT bekup_owner TO mallory WITH ADMIN OPTION"); err != nil {
					t.Fatal(err)
				}
				_, err := e.bootstrap(ctx, t, adopt)
				if err == nil || !strings.Contains(err.Error(), "mallory is a member of bekup_owner WITH ADMIN OPTION") ||
					!strings.Contains(err.Error(), `REVOKE bekup_owner FROM "mallory";`) {
					t.Fatalf("bootstrap: %v", err)
				}
				if err := e.s.SuperExec(ctx, "postgres", "REVOKE bekup_owner FROM mallory"); err != nil {
					t.Fatal(err)
				}
				if _, err := e.bootstrap(ctx, t, adopt); err != nil {
					t.Fatalf("bootstrap after the revoke: %v", err)
				}
			})

			t.Run("session authorization cannot impersonate bekup_app", func(t *testing.T) {
				// A superuser switches its session user to bekup_app:
				// session_user and current_user both say bekup_app, only the
				// authenticated identity (SYSTEM_USER, PostgreSQL 16+) differs.
				conn, err := pgx.Connect(ctx, "postgres://postgres:"+externaltest.SuperPassword+"@"+hostPort(e)+"/"+extDatabase+"?sslmode=disable")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := conn.Close(context.WithoutCancel(ctx)); err != nil {
						t.Error(err)
					}
				})
				if _, err := conn.Exec(ctx, "SET SESSION AUTHORIZATION bekup_app"); err != nil {
					t.Fatal(err)
				}
				err = db.CheckAppRole(ctx, conn, "")
				if tc.image == externaltest.PG15 {
					// No SYSTEM_USER before 16: documented limitation (D-207).
					if err != nil {
						t.Fatalf("PostgreSQL 15: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "authenticated as another role") || strings.Contains(err.Error(), "postgres") {
					t.Fatalf("CheckAppRole: %v", err)
				}
			})

			if tc.citextNegative {
				t.Run("citext not creatable", func(t *testing.T) { citextNegative(t, e) })
			}
		})
	}
}

// citextNegative makes citext an untrusted extension: bootstrap (a
// non-superuser admin) cannot create it and warns, the preflight fails with
// the fix, and after an administrator created it everything passes.
func citextNegative(t *testing.T, e *extEnv) {
	ctx := t.Context()
	const control = "/usr/local/share/postgresql/extension/citext.control"
	if _, err := e.s.Exec(ctx, "root", "sed", "-i", "s/^trusted = true/trusted = false/", control); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := e.s.Exec(context.WithoutCancel(ctx), "root", "sed", "-i", "s/^trusted = false/trusted = true/", control); err != nil {
			t.Error(err)
		}
	})
	res, err := e.bootstrap(ctx, t, func(o *db.BootstrapOptions) { o.Database, o.AdoptExistingRoles = "bekup_nocitext", true })
	if err != nil || !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "could not create citext") }) {
		t.Fatalf("bootstrap: %v %+v", err, res)
	}
	m, err := e.migrator(ctx, t, e.creds(db.OwnerRole, extOwnerPW, "bekup_nocitext"), e.tls)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Check(ctx); err == nil || !strings.Contains(err.Error(), "citext extension is not trusted") {
		t.Fatalf("preflight: %v", err)
	}
	if err := e.s.SuperExec(ctx, "bekup_nocitext", "CREATE EXTENSION citext"); err != nil {
		t.Fatal(err)
	}
	if rep, err := m.Check(ctx); err != nil || rep.Citext != db.CitextInstalled {
		t.Fatalf("preflight after CREATE EXTENSION: %v %+v", err, rep)
	}
}

func superQueryRow(t *testing.T, e *extEnv, q string, dst any) error {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), "postgres://postgres:"+externaltest.SuperPassword+"@"+hostPort(e)+"/postgres?sslmode=disable")
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	return conn.QueryRow(t.Context(), q).Scan(dst)
}

func hostPort(e *extEnv) string {
	return net.JoinHostPort(e.s.Host, strconv.Itoa(int(e.s.Port)))
}

// TestExternalRolesSQLPSQL runs the script of `bekup db roles-sql` with psql
// as a non-superuser admin (PostgreSQL 16, RSA certificates), then migrates;
// a bootstrap afterwards finds nothing to do.
func TestExternalRolesSQLPSQL(t *testing.T) {
	ctx := t.Context()
	e := newExtEnv(t, externaltest.PG16, true)
	if err := e.s.CreateAdmin(ctx, extAdmin, extAdminPW); err != nil {
		t.Fatal(err)
	}
	ownerV, err := db.NewSCRAMVerifier([]byte(extOwnerPW))
	if err != nil {
		t.Fatal(err)
	}
	appV, err := db.NewSCRAMVerifier([]byte(extAppPW))
	if err != nil {
		t.Fatal(err)
	}
	script, err := db.RolesSQLScript(db.NewRolesSQLPlan(extDatabase, ownerV, appV, false, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.CopyFile(ctx, "/tmp/bekup-roles.sql", []byte(script)); err != nil {
		t.Fatal(err)
	}
	if out, err := e.s.Exec(ctx, "", "env", "PGPASSWORD="+extAdminPW, "psql", "-v", "ON_ERROR_STOP=1", "--no-psqlrc",
		"-f", "/tmp/bekup-roles.sql", "host=127.0.0.1 dbname=postgres user="+extAdmin+" sslmode=require"); err != nil {
		t.Fatalf("psql: %v\n%s", err, out)
	}
	e.migrateAndCheck(t, extDatabase)
	res, err := e.bootstrap(ctx, t, adopt)
	if err != nil || res.Changed {
		t.Fatalf("bootstrap after roles-sql: %v %+v", err, res)
	}
	// PostgreSQL 16+ gave the creating admin ADMIN OPTION on both roles:
	// another admin is refused (a foreign ADMIN OPTION holder), and
	// migrate check names the holder.
	if err := e.s.CreateAdmin(ctx, "other_admin", extAdminPW); err != nil {
		t.Fatal(err)
	}
	_, err = e.bootstrap(ctx, t, func(o *db.BootstrapOptions) {
		o.Admin, o.AdoptExistingRoles = e.creds("other_admin", extAdminPW, "postgres"), true
	})
	if err == nil || !strings.Contains(err.Error(), "ext_admin is a member of bekup_owner WITH ADMIN OPTION") {
		t.Fatalf("bootstrap as another admin: %v", err)
	}
	var usage bool
	if err := superQueryRow(t, e, "SELECT pg_has_role('ext_admin', 'bekup_owner', 'USAGE')", &usage); err != nil || usage {
		t.Fatalf("admin still holds bekup_owner: %v %v", usage, err)
	}
}

// TestExternalSingleAccount: one CREATEROLE+CREATEDB account bootstraps
// NOLOGIN roles, migrates and runs the app through SET ROLE (D-207); a
// re-run after the account dropped CREATEROLE grants nothing again (15 and
// 17).
func TestExternalSingleAccount(t *testing.T) {
	for _, image := range []string{externaltest.PG15, externaltest.PG17} {
		t.Run(image, func(t *testing.T) { singleAccount(t, image) })
	}
}

func singleAccount(t *testing.T, image string) {
	ctx := t.Context()
	e := newExtEnv(t, image, false)
	const acct, acctPW = "acct_single", "acct-password-0123456789"
	if err := e.s.CreateAdmin(ctx, acct, acctPW); err != nil {
		t.Fatal(err)
	}
	res, err := db.Bootstrap(ctx, db.BootstrapOptions{
		Admin: e.creds(acct, acctPW, "postgres"), TLS: e.tls, Database: extDatabase, SingleAccount: true, Registry: logging.NewRegistry(),
	})
	if err != nil || !hasAction(res, "grant bekup_app to the executing account") || hasAction(res, "revoke bekup_owner") {
		t.Fatalf("bootstrap: %v %+v", err, res)
	}
	var login bool
	if err := superQueryRow(t, e, "SELECT rolcanlogin FROM pg_roles WHERE rolname = 'bekup_owner'", &login); err != nil || login {
		t.Fatalf("bekup_owner can log in: %v %v", login, err)
	}

	owner := e.creds(acct, acctPW, extDatabase)
	owner.SetRole = db.OwnerRole
	m, err := e.migrator(ctx, t, owner, e.tls)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := m.Check(ctx)
	if err != nil || !rep.SingleAccount || !slices.Contains(rep.LoginFindings, "CREATEROLE") {
		t.Fatalf("preflight: %v %+v", err, rep)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	app := e.creds(acct, acctPW, extDatabase)
	app.SetRole = db.AppRole
	p, err := e.pool(ctx, t, app, e.tls)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CheckAppRole(ctx, p, acct); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchemaVersion(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckAppRole(ctx, p, ""); err == nil {
		t.Fatal("two-role check accepted a single-account session")
	}
	// The sessions still act with bekup_app's privileges only.
	if _, err := p.Exec(ctx, "UPDATE audit_log SET action = action"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("audit_log UPDATE: %v", err)
	}

	// A superuser is never accepted as the single account.
	super := e.creds("postgres", externaltest.SuperPassword, extDatabase)
	super.SetRole = db.OwnerRole
	if _, err := e.migrator(ctx, t, super, e.tls); err == nil || !strings.Contains(err.Error(), "refuses a SUPERUSER") {
		t.Fatalf("superuser: %v", err)
	}
	if _, err := db.Bootstrap(ctx, db.BootstrapOptions{
		Admin: e.creds("postgres", externaltest.SuperPassword, "postgres"), TLS: e.tls, Database: "bekup2", SingleAccount: true, Registry: logging.NewRegistry(),
	}); err == nil || !strings.Contains(err.Error(), "refuses a SUPERUSER") {
		t.Fatalf("superuser bootstrap: %v", err)
	}

	// The account no longer needs CREATEROLE once the roles exist: a
	// re-run holds both roles already and grants nothing.
	if err := e.s.SuperExec(ctx, "postgres", "ALTER ROLE "+acct+" NOCREATEROLE"); err != nil {
		t.Fatal(err)
	}
	res, err = db.Bootstrap(ctx, db.BootstrapOptions{
		Admin: e.creds(acct, acctPW, "postgres"), TLS: e.tls, Database: extDatabase, SingleAccount: true, AdoptExistingRoles: true, Registry: logging.NewRegistry(),
	})
	if err != nil || res.Changed || hasAction(res, "grant bekup_owner") || hasAction(res, "grant bekup_app to") {
		t.Fatalf("re-run without CREATEROLE: %v %+v", err, res)
	}
}

// TestExternalBootstrapNeedsPrivileges: an account without CREATEROLE is
// refused before anything changes.
func TestExternalBootstrapNeedsPrivileges(t *testing.T) {
	ctx := t.Context()
	e := newExtEnv(t, externaltest.PG15, false)
	if err := e.s.SuperExec(ctx, "postgres", "CREATE ROLE weak LOGIN CREATEDB PASSWORD 'weak-password-0123456'"); err != nil {
		t.Fatal(err)
	}
	_, err := e.bootstrap(ctx, t, func(o *db.BootstrapOptions) { o.Admin = e.creds("weak", "weak-password-0123456", "postgres") })
	if err == nil || !strings.Contains(err.Error(), "lacks CREATEROLE") {
		t.Fatalf("got %v", err)
	}
	var n int
	if err := superQueryRow(t, e, "SELECT count(*) FROM pg_roles WHERE rolname IN ('bekup_owner', 'bekup_app')", &n); err != nil || n != 0 {
		t.Fatalf("roles created: %d %v", n, err)
	}
	// Wrong admin password: a generic error without the password.
	_, err = e.bootstrap(ctx, t, func(o *db.BootstrapOptions) { o.Admin = e.creds(extAdmin, "wrong-password-xyz-0123", "postgres") })
	if err == nil || strings.Contains(err.Error(), "wrong-password") {
		t.Fatalf("got %v", err)
	}
}

// TestPreflightBundled: the preflight passes on a database prepared by the
// bundled init script (NOINHERIT bekup_owner owning public), before and
// after the migrations (other test databases may show up as connectable
// warnings).
func TestPreflightBundled(t *testing.T) {
	d := newTestDB(t)
	m := d.migrator(t)
	for _, phase := range []string{"before", "after"} {
		rep, err := m.Check(t.Context())
		if err != nil {
			t.Fatalf("%s migrations: %v", phase, err)
		}
		if rep.SingleAccount || !rep.TLS.HostVerified || rep.Database != d.name {
			t.Fatalf("%s migrations: %+v", phase, rep)
		}
		if phase == "before" {
			if _, err := m.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
}
