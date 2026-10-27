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

	"github.com/jlentink/bekup/internal/db/migrations"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

// Database roles (D-054).
const (
	// OwnerRole is the role migrations must run as.
	OwnerRole = "bekup_owner"
	// AppRole is the runtime role of serve and worker.
	AppRole = "bekup_app"
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
}

// NewMigrator connects with the owner URL (from config.Bekup.DatabaseOwnerURL;
// the caller zeroes it), registers the URL and password for log redaction and
// runs the full role check (checkOwnerRole) on every run. Every connection
// uses the TLS policy tlsOpts (D-100). Close the Migrator when done.
func NewMigrator(ctx context.Context, ownerURL []byte, tlsOpts TLSOptions, reg *logging.Registry) (*Migrator, error) {
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	reg.Register(ownerURL)
	cc, err := pgx.ParseConfig(string(ownerURL))
	if err != nil {
		return nil, ErrInvalidURL
	}
	registerPassword(reg, cc)
	if err := applyTLS(&cc.Config, ownerURL, tlsOpts); err != nil {
		return nil, err
	}
	cc.RuntimeParams["application_name"] = "bekup-migrate"

	lockConn, err := pgx.ConnectConfig(ctx, cc.Copy())
	if err != nil {
		return nil, fmt.Errorf("connect to database as %s: %w", OwnerRole, err)
	}
	m := &Migrator{lockConn: lockConn}
	if err := checkOwnerRole(ctx, sqlc.New(lockConn)); err != nil {
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

// checkOwnerRole runs the full role check on every migrate run (not only when
// migration 00001 is first applied, D-092/D-098): the session must be a plain
// bekup_owner, bekup_app must exist, and neither role may hold or inherit
// dangerous privileges (see checkRole).
func checkOwnerRole(ctx context.Context, q *sqlc.Queries) error {
	current, err := q.CurrentRoleName(ctx)
	if err != nil {
		return fmt.Errorf("check database role: %w", err)
	}
	if current != OwnerRole {
		return fmt.Errorf("migrations must run as %s, connected as %q (set database.owner_url_file)", OwnerRole, current)
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
// `bekup serve` calls it at startup and refuses to run otherwise.
func CheckAppRole(ctx context.Context, db sqlc.DBTX) error {
	q := sqlc.New(db)
	current, err := q.CurrentRoleName(ctx)
	if err != nil {
		return fmt.Errorf("check database role: %w", err)
	}
	if current != AppRole {
		return fmt.Errorf("the app must connect as %s, connected as %q (check database.url_file)", AppRole, current)
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
		return rc, fmt.Errorf("role %s does not exist; create it with the deploy init script (docs/security/db-roles.md)", role)
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
