package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Supported PostgreSQL major versions of the app DB (D-207).
const (
	minServerVersionNum    = 150000
	testedServerVersionMax = 179999
)

// Citext states reported by the preflight.
const (
	CitextInstalled    = "installed"
	CitextCreatable    = "creatable by bekup_owner (trusted extension)"
	CitextNotCreatable = "not creatable by bekup_owner"
	CitextUnavailable  = "not available on the server"
	CitextNotAllowed   = "not allow-listed (azure.extensions)"
)

// PreflightReport is the result of `bekup migrate check` (D-207): facts
// about the server and the database plus warnings. It never contains a
// host name or credentials.
type PreflightReport struct {
	ServerVersion    string
	ServerVersionNum int
	Database         string
	Citext           string
	TLS              TLSFacts
	// SingleAccount: the session logged in as one account and switched
	// to bekup_owner (D-207).
	SingleAccount bool
	// LoginFindings are dangerous attributes or memberships of the
	// single-account login role (empty with two roles).
	LoginFindings []string
	Warnings      []string
}

// Check runs the preflight on the migrator's owner connection; NewMigrator
// already ran the full role check. `bekup migrate up` runs it before any
// migration. The report is filled as far as possible; the error joins every
// failed check.
func (m *Migrator) Check(ctx context.Context) (*PreflightReport, error) {
	return preflight(ctx, m.lockConn, m.tls, m.login)
}

// preflight checks what the released migrations need from the server and
// the database: PostgreSQL 15+, a UTF8 database owned by bekup_owner, a
// public schema bekup_owner controls and citext (migration 00004). It
// warns about members and ADMIN OPTION holders of bekup's roles other than
// the single-account login (the migrator cannot know which administrator
// ran bootstrap; bootstrap itself refuses foreign ones).
func preflight(ctx context.Context, conn *pgx.Conn, o TLSOptions, login string) (*PreflightReport, error) {
	q := sqlc.New(conn)
	r := &PreflightReport{TLS: ConnTLSFacts(conn.PgConn(), o), SingleAccount: login != ""}
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	v, err := q.PreflightServerVersion(ctx)
	if err != nil {
		return r, fmt.Errorf("read server version: %w", err)
	}
	r.ServerVersion, r.ServerVersionNum = v.Version, int(v.VersionNum)
	switch {
	case v.VersionNum < minServerVersionNum:
		fail("bekup needs PostgreSQL 15 or newer (server: %s)", v.Version)
	case v.VersionNum > testedServerVersionMax:
		r.Warnings = append(r.Warnings, fmt.Sprintf("PostgreSQL %s is newer than the tested versions 15 to 17", v.Version))
	}

	d, err := q.PreflightDatabase(ctx)
	if err != nil {
		return r, errors.Join(append(errs, fmt.Errorf("read database facts: %w", err))...)
	}
	r.Database = d.Name
	if d.Owner != OwnerRole {
		fail("database %q is owned by %q, not %s (ALTER DATABASE %s OWNER TO %s as an administrator, or run bekup db bootstrap)",
			d.Name, d.Owner, OwnerRole, pgx.Identifier{d.Name}.Sanitize(), OwnerRole)
	}
	if d.Encoding != "UTF8" {
		fail("database %q uses encoding %s; bekup needs UTF8 (create it with ENCODING 'UTF8' TEMPLATE template0)", d.Name, d.Encoding)
	}

	owner, err := q.PreflightPublicSchemaOwner(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fail("schema public does not exist in database %q (CREATE SCHEMA public AUTHORIZATION %s)", d.Name, OwnerRole)
	case err != nil:
		return r, errors.Join(append(errs, fmt.Errorf("read schema owner: %w", err))...)
	case owner != OwnerRole && owner != "pg_database_owner":
		fail("schema public is owned by %q; bekup_owner must control it (ALTER SCHEMA public OWNER TO %s as an administrator)", owner, OwnerRole)
	default:
		can, err := q.PreflightPublicSchemaCreate(ctx)
		if err != nil {
			return r, errors.Join(append(errs, fmt.Errorf("read schema privileges: %w", err))...)
		}
		if !can {
			fail("%s cannot create objects in schema public (owned by %s): on PostgreSQL 15 a NOINHERIT database owner gets no privileges from pg_database_owner; run ALTER ROLE %s INHERIT, or ALTER SCHEMA public OWNER TO %s as a superuser",
				OwnerRole, owner, OwnerRole, OwnerRole)
		}
	}

	c, err := q.PreflightCitext(ctx)
	if err != nil {
		return r, errors.Join(append(errs, fmt.Errorf("read citext status: %w", err))...)
	}
	r.Citext = citextState(c)
	switch r.Citext {
	case CitextUnavailable:
		fail("the citext extension is not available on the server (it ships with PostgreSQL's contrib modules; ask your provider)")
	case CitextNotAllowed:
		fail("the citext extension is not allow-listed: add CITEXT to the azure.extensions server parameter")
	case CitextNotCreatable:
		fail("the citext extension is not trusted on this server, so bekup_owner cannot create it: create it once as an administrator " +
			"(bekup db bootstrap does it when the admin may; or run CREATE EXTENSION citext; in the bekup database; bekup db roles-sql --create-extensions prints it)")
	}

	dbs, err := q.PreflightConnectableDatabases(ctx, AppRole)
	if err != nil {
		return r, errors.Join(append(errs, fmt.Errorf("read database privileges: %w", err))...)
	}
	if len(dbs) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s may connect to other databases: %s (usually through the PUBLIC default; on a cluster dedicated to bekup, REVOKE CONNECT ON DATABASE ... FROM PUBLIC)",
			AppRole, strings.Join(dbs, ", ")))
	}

	roles := []string{OwnerRole, AppRole}
	if login != "" {
		roles = append(roles, login)
	}
	settings, err := q.PreflightRoleSettings(ctx, roles)
	if err != nil {
		return r, errors.Join(append(errs, fmt.Errorf("read role settings: %w", err))...)
	}
	if len(settings) > 0 {
		var parts []string
		for _, s := range settings {
			parts = append(parts, fmt.Sprintf("role %s in database %s", s.RoleName, s.DatabaseName))
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf("ALTER ROLE/DATABASE ... SET entries apply to bekup's sessions: %s (bekup sets search_path, statement_timeout and the isolation level itself; review the entries with psql \\drds)",
			strings.Join(parts, "; ")))
	}

	ms, err := roleMemberships(ctx, q)
	if err != nil {
		return r, errors.Join(append(errs, err)...)
	}
	if w := membershipWarning(ms, login); w != "" {
		r.Warnings = append(r.Warnings, w)
	}

	if login != "" {
		f, err := LoginFindings(ctx, q, login)
		if err != nil {
			return r, errors.Join(append(errs, err)...)
		}
		r.LoginFindings = f
	}
	if !r.TLS.Encrypted {
		r.Warnings = append(r.Warnings, "the connection is not encrypted (database.tls_mode = \"disable\")")
	}
	return r, errors.Join(errs...)
}

func citextState(c sqlc.PreflightCitextRow) string {
	switch {
	case c.Installed:
		return CitextInstalled
	case !c.Available:
		return CitextUnavailable
	case c.AzureAllowlist && !azureAllows(c.AzureExtensions, "citext"):
		return CitextNotAllowed
	case c.Trusted:
		return CitextCreatable
	default:
		return CitextNotCreatable
	}
}

// azureAllows reports whether the azure.extensions list names ext.
func azureAllows(list, ext string) bool {
	return slices.ContainsFunc(strings.Split(list, ","), func(s string) bool {
		return strings.EqualFold(strings.TrimSpace(s), ext)
	})
}

// LoginFindings lists the dangerous attributes and memberships of the
// single-account login role (D-207): reported in the startup warning, the
// audit entry and `bekup migrate check`; SUPERUSER is refused earlier
// (checkSessionRole).
func LoginFindings(ctx context.Context, q *sqlc.Queries, login string) ([]string, error) {
	rc, err := roleCheck(ctx, q, login, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range []struct {
		set  bool
		name string
	}{{rc.IsSuperuser, "SUPERUSER"}, {rc.BypassRls, "BYPASSRLS"}, {rc.CreateRole, "CREATEROLE"}, {rc.Replication, "REPLICATION"}} {
		if a.set {
			out = append(out, a.name)
		}
	}
	for _, m := range rc.DangerousMemberships {
		out = append(out, "member of "+m)
	}
	return out, nil
}
