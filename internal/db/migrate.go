package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/migrations"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

// Database roles (D-054; fixed names, D-207).
const (
	// OwnerRole is the role migrations must run as.
	OwnerRole = config.DBOwnerRole
	// AppRole is the runtime role of serve and worker.
	AppRole = config.DBAppRole
)

// MigrationStatus is the state of one migration.
type MigrationStatus struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// MigrationResult describes one applied or rolled-back migration.
type MigrationResult struct {
	Version  int64
	Name     string
	Duration time.Duration
}

// Migrator applies the embedded goose migrations as bekup_owner.
//
// Concurrency: every operation first takes a session advisory lock on a
// dedicated, unpooled connection (lockConn) that lives only as long as the
// Migrator; the migrations themselves run on a separate single connection.
// If the process dies the connection closes and Postgres releases the lock,
// so a crashed migrator never blocks the others. Other instances wait.
type Migrator struct {
	lockConn *pgx.Conn
	sqlDB    *sql.DB
	provider *goose.Provider
	tls      TLSOptions
	login    string // single-account login role ("" with two roles)
}

// NewMigrator connects with the owner credentials (config.Bekup.
// OwnerDBCredentials; the caller zeroes them) through the one connection
// builder, registers the URL and password for log redaction and runs the
// full role check (checkOwnerRole) on every run. Every connection uses the
// TLS policy tlsOpts (D-100). In single-account mode (creds.SetRole) every
// session switches to bekup_owner first (D-207). Close the Migrator when
// done.
func NewMigrator(ctx context.Context, creds config.DBCredentials, tlsOpts TLSOptions, reg *logging.Registry) (*Migrator, error) {
	if creds.SetRole != "" && creds.SetRole != OwnerRole {
		return nil, fmt.Errorf("migrations must run as %s", OwnerRole)
	}
	cc, err := connConfig(creds, tlsOpts, reg)
	if err != nil {
		return nil, err
	}
	cc.RuntimeParams["application_name"] = "bekup-migrate"

	lockConn, err := pgx.ConnectConfig(ctx, cc.Copy())
	if err != nil {
		return nil, fmt.Errorf("connect to database as %s: %w", OwnerRole, err)
	}
	m := &Migrator{lockConn: lockConn, tls: tlsOpts, login: singleAccountLogin(creds)}
	if err := checkOwnerRole(ctx, sqlc.New(lockConn), m.login); err != nil {
		return nil, errors.Join(err, m.Close(ctx))
	}

	m.sqlDB = stdlib.OpenDB(*cc.Copy())
	m.sqlDB.SetMaxOpenConns(1)
	m.provider, err = goose.NewProvider(goose.DialectPostgres, m.sqlDB, migrations.FS,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("load migrations: %w", err), m.Close(ctx))
	}
	return m, nil
}

// singleAccountLogin is the login role of single-account credentials, or ""
// with two roles (URL mode never uses single-account).
func singleAccountLogin(creds config.DBCredentials) string {
	if creds.SetRole == "" {
		return ""
	}
	return creds.User
}

// checkSessionRole verifies who the session is (D-099, D-207). With two
// roles (login == "") the session must have logged in as role itself, so a
// role setting (ALTER ROLE x SET role = ...) cannot make another login,
// possibly a superuser, pass as bekup's role. In single-account mode the
// session logged in as login and acts as role after SET ROLE; a superuser
// login is refused. On PostgreSQL 16+ the authenticated identity
// (SYSTEM_USER) must also be the expected login authenticated with
// SCRAM-SHA-256: session_user can be changed after authentication (SET
// SESSION AUTHORIZATION, a session_authorization setting), the
// authenticated identity cannot.
func checkSessionRole(ctx context.Context, q *sqlc.Queries, role, login, hint string) error {
	sr, err := q.SessionRoles(ctx)
	if err != nil {
		return fmt.Errorf("check database role: %w", err)
	}
	if sr.ActiveRole != role {
		what := "the app must connect"
		if role == OwnerRole {
			what = "migrations must run"
		}
		return fmt.Errorf("%s as %s, connected as %q (%s)", what, role, sr.ActiveRole, hint)
	}
	expected := login
	if login == "" {
		expected = role
		if sr.SessionRole != role {
			return fmt.Errorf("the session logged in as %q but acts as %s (a role setting or SET ROLE): log in as %s itself (%s)", sr.SessionRole, role, role, hint)
		}
	} else {
		if sr.SessionRole != login {
			return fmt.Errorf("single-account mode: the session logged in as %q, not as database.user %q", sr.SessionRole, login)
		}
		if sr.SessionSuperuser {
			return fmt.Errorf("single-account mode refuses a SUPERUSER login role (%q): use an account with CREATEROLE and CREATEDB, or better the two least-privilege roles", login)
		}
	}
	if sr.ServerVersionNum < 160000 {
		return nil
	}
	id, err := q.SessionSystemUser(ctx)
	if err != nil {
		return fmt.Errorf("check database role: %w", err)
	}
	return checkSystemUser(id, expected)
}

// checkSystemUser compares the session's SYSTEM_USER with the identity
// bekup authenticated as (PostgreSQL 16+): "scram-sha-256:<login>". An
// "md5" pg_hba entry also yields a SCRAM exchange for a role with a SCRAM
// verifier and is reported as "md5:<login>"; require_auth has already
// refused every exchange but SCRAM on the client side, so both are
// accepted. A foreign identity is never echoed.
func checkSystemUser(id, login string) error {
	method, identity, ok := strings.Cut(id, ":")
	switch {
	case ok && identity == login && (method == requireAuth || method == "md5"):
		return nil
	case !ok || (method != requireAuth && method != "md5"):
		return fmt.Errorf("the session was not authenticated with a password exchange (SYSTEM_USER method %q); bekup only accepts SCRAM-SHA-256 logins of %q", safeMethod(method), login)
	default:
		return fmt.Errorf("the session authenticated as another role than %q (SYSTEM_USER differs: SET SESSION AUTHORIZATION or a session_authorization setting)", login)
	}
}

// safeMethod bounds a server-reported authentication method for messages.
func safeMethod(m string) string {
	if m == "" {
		return "none"
	}
	if len(m) > 32 || strings.ContainsFunc(m, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' }) {
		return "unknown"
	}
	return m
}

// checkOwnerRole runs the full role check on every migrate run (not only when
// migration 00001 is first applied, D-092/D-098): the session must be a plain
// bekup_owner (or, in single-account mode, the configured login acting as
// bekup_owner), bekup_app must exist, and neither role may hold or inherit
// dangerous privileges (see checkRole).
func checkOwnerRole(ctx context.Context, q *sqlc.Queries, login string) error {
	if err := checkSessionRole(ctx, q, OwnerRole, login, "check database.owner_url_file / database.owner_password_file"); err != nil {
		return err
	}
	owner, err := roleCheck(ctx, q, OwnerRole, nil)
	if err != nil {
		return err
	}
	app, err := roleCheck(ctx, q, AppRole, []string{OwnerRole})
	if err != nil {
		return err
	}
	return errors.Join(validateRole(owner), validateRole(app))
}

// CheckAppRole verifies that db is connected as bekup_app and that the role
// is not SUPERUSER/BYPASSRLS/CREATEROLE/REPLICATION and is not a member of
// bekup_owner, of a role with those attributes or of a predefined pg_* role.
// login is the single-account login role (D-207; "" with two roles, when
// the session must have logged in as bekup_app itself). `bekup serve`
// calls it at startup and refuses to run otherwise.
func CheckAppRole(ctx context.Context, db sqlc.DBTX, login string) error {
	q := sqlc.New(db)
	if err := checkSessionRole(ctx, q, AppRole, login, "check database.url_file / database.user"); err != nil {
		return err
	}
	app, err := roleCheck(ctx, q, AppRole, []string{OwnerRole})
	if err != nil {
		return err
	}
	return validateRole(app)
}

func roleCheck(ctx context.Context, q *sqlc.Queries, role string, forbidden []string) (sqlc.RoleSecurityCheckRow, error) {
	if forbidden == nil {
		forbidden = []string{}
	}
	rc, err := q.RoleSecurityCheck(ctx, sqlc.RoleSecurityCheckParams{RoleName: role, ForbiddenRoles: forbidden})
	if errors.Is(err, pgx.ErrNoRows) {
		return rc, fmt.Errorf("role %s does not exist; create it with bekup db bootstrap or the deploy init script (docs/security/db-roles.md)", role)
	}
	if err != nil {
		return rc, fmt.Errorf("check role %s: %w", role, err)
	}
	return rc, nil
}

// validateRole rejects a role with dangerous attributes or memberships.
//
// Memberships are checked because SET ROLE (or inheritance) gives a role
// every privilege of the roles it belongs to. Every predefined pg_* role is
// refused (except pg_database_owner, which the owner holds implicitly):
// pg_read_server_files, pg_write_server_files and pg_execute_server_program
// are superuser-equivalent; pg_write_all_data bypasses the audit-log grants;
// pg_read_all_data exposes other databases of the cluster; pg_signal_backend,
// pg_checkpoint and pg_maintain allow disrupting other sessions or locking
// tables; pg_create_subscription makes the server connect out; and the
// monitoring roles disclose server settings and other sessions' queries.
// bekup needs none of them.
func validateRole(rc sqlc.RoleSecurityCheckRow) error {
	var bad []string
	for name, set := range map[string]bool{
		"SUPERUSER": rc.IsSuperuser, "BYPASSRLS": rc.BypassRls,
		"CREATEROLE": rc.CreateRole, "REPLICATION": rc.Replication,
	} {
		if set {
			bad = append(bad, name)
		}
	}
	slices.Sort(bad)
	var errs []error
	if len(bad) > 0 {
		errs = append(errs, fmt.Errorf("role %s must not have %s", rc.RoleName, strings.Join(bad, ", ")))
	}
	if len(rc.DangerousMemberships) > 0 {
		errs = append(errs, fmt.Errorf("role %s must not be a member of %s (REVOKE the membership)",
			rc.RoleName, strings.Join(rc.DangerousMemberships, ", ")))
	}
	return errors.Join(errs...)
}

// Close releases both connections.
func (m *Migrator) Close(ctx context.Context) error {
	var errs []error
	if m.sqlDB != nil {
		errs = append(errs, m.sqlDB.Close())
	}
	if m.lockConn != nil {
		errs = append(errs, m.lockConn.Close(ctx))
	}
	return errors.Join(errs...)
}

// withLock runs fn while holding the migration advisory lock.
func (m *Migrator) withLock(ctx context.Context, fn func() error) (err error) {
	q := sqlc.New(m.lockConn)
	if err := q.AcquireMigrationLock(ctx); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		ok, uerr := q.ReleaseMigrationLock(context.WithoutCancel(ctx))
		if uerr == nil && !ok {
			uerr = errors.New("lock was not held")
		}
		if uerr != nil {
			err = errors.Join(err, fmt.Errorf("release migration lock: %w", uerr))
		}
	}()
	return fn()
}

// Up applies all pending migrations.
func (m *Migrator) Up(ctx context.Context) ([]MigrationResult, error) {
	var res []*goose.MigrationResult
	err := m.withLock(ctx, func() error {
		var err error
		res, err = m.provider.Up(ctx)
		return err
	})
	if err != nil {
		return results(res), fmt.Errorf("apply migrations: %w", err)
	}
	return results(res), nil
}

// DownOne rolls back the most recent migration. Dev and tests only: in
// production migrations are forward-only (the CLI enforces dev mode).
func (m *Migrator) DownOne(ctx context.Context) ([]MigrationResult, error) {
	var res *goose.MigrationResult
	err := m.withLock(ctx, func() error {
		var err error
		res, err = m.provider.Down(ctx)
		return err
	})
	var out []MigrationResult
	if res != nil {
		out = results([]*goose.MigrationResult{res})
	}
	if err != nil {
		return out, fmt.Errorf("roll back migration: %w", err)
	}
	return out, nil
}

// DownTo rolls back to version (0: everything). Tests only.
func (m *Migrator) DownTo(ctx context.Context, version int64) ([]MigrationResult, error) {
	var res []*goose.MigrationResult
	err := m.withLock(ctx, func() error {
		var err error
		res, err = m.provider.DownTo(ctx, version)
		return err
	})
	if err != nil {
		return results(res), fmt.Errorf("roll back migrations: %w", err)
	}
	return results(res), nil
}

// Status reports every known migration.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	var st []*goose.MigrationStatus
	err := m.withLock(ctx, func() error {
		var err error
		st, err = m.provider.Status(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("migration status: %w", err)
	}
	out := make([]MigrationStatus, 0, len(st))
	for _, s := range st {
		if s == nil || s.Source == nil {
			continue
		}
		out = append(out, MigrationStatus{
			Version:   s.Source.Version,
			Name:      s.Source.Path,
			Applied:   s.State == goose.StateApplied,
			AppliedAt: s.AppliedAt,
		})
	}
	return out, nil
}

func results(rs []*goose.MigrationResult) []MigrationResult {
	out := make([]MigrationResult, 0, len(rs))
	for _, r := range rs {
		if r == nil || r.Source == nil {
			continue
		}
		out = append(out, MigrationResult{Version: r.Source.Version, Name: r.Source.Path, Duration: r.Duration})
	}
	return out
}
