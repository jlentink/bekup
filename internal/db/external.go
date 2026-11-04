package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// The external API role (D-220). bekup_external is optional: it exists only
// when the external API is set up (bundled init script
// deploy/postgres/init/20-bekup-external.sh, or RolePlan.External for an
// external database). Its complete privileges are CONNECT on the database,
// USAGE on schema api and EXECUTE on the functions below; migration 00037
// and every Migrator.Up apply them (public.bekup_sync_external_grants())
// and the privilege audit (ExternalRoleFindings) refuses anything else.
// Single-account mode does not support it.

// externalFunctions are the api functions bekup_external may execute, as
// "name(argument types)" in pg_catalog.oidvectortypes form.
func externalFunctions() []string {
	return []string{
		"authenticate(uuid, bytea, inet)",
		"request_run(uuid, bytea, uuid, text, inet)",
		"request_status(uuid, bytea, uuid, inet)",
		"schema_version()",
		"token_jobs(uuid, bytea, inet)",
	}
}

// maxFindings bounds the privilege findings named in an error.
const maxFindings = 20

// CheckExternalRole verifies the connection of `bekup external-api`
// (D-220): the session logged in as bekup_external itself (session_user and,
// on PostgreSQL 16+, SYSTEM_USER with SCRAM), the role has no dangerous
// attribute and no membership of bekup_owner, bekup_app or a dangerous
// role, and it holds no privilege beyond the allowlist. The external API
// calls it at startup and refuses to run otherwise.
func CheckExternalRole(ctx context.Context, db sqlc.DBTX) error {
	q := sqlc.New(db)
	if err := checkSessionRole(ctx, q, ExternalRole, "", "check the external API's database user and password file"); err != nil {
		return err
	}
	return checkExternalRole(ctx, q)
}

// CheckExternalLogin is CheckExternalRole without the privilege audit, for
// a database that is not migrated yet (`db bootstrap`): before migration
// 00001 PUBLIC still holds its default privileges, and Migrator.Up audits
// the role after the grant sync.
func CheckExternalLogin(ctx context.Context, db sqlc.DBTX) error {
	q := sqlc.New(db)
	if err := checkSessionRole(ctx, q, ExternalRole, "", "check the external API's database user and password file"); err != nil {
		return err
	}
	rc, err := roleCheck(ctx, q, ExternalRole, []string{OwnerRole, AppRole})
	if err != nil {
		return err
	}
	return validateRole(rc)
}

// checkExternalRole runs the attribute, membership and privilege checks
// of an existing bekup_external.
func checkExternalRole(ctx context.Context, q *sqlc.Queries) error {
	rc, err := roleCheck(ctx, q, ExternalRole, []string{OwnerRole, AppRole})
	if err != nil {
		return err
	}
	errs := []error{validateRole(rc)}
	findings, err := q.ExternalRoleFindings(ctx, sqlc.ExternalRoleFindingsParams{RoleName: ExternalRole, AllowedFunctions: externalFunctions()})
	if err != nil {
		return fmt.Errorf("audit the privileges of %s: %w", ExternalRole, err)
	}
	if len(findings) > 0 {
		errs = append(errs, fmt.Errorf("role %s holds more than the external API may use: %s (REVOKE it as an administrator; bekup migrate up re-applies the allowed privileges, D-220)",
			ExternalRole, joinBounded(findings, maxFindings)))
	}
	return errors.Join(errs...)
}

// joinBounded joins at most limit items and counts the rest.
func joinBounded(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:limit], "; ") + fmt.Sprintf("; and %d more", len(items)-limit)
}

// checkExternalForMigrate is the bekup_external part of the migrator's role
// check: nothing without the role; with it, single-account mode is refused
// and the role must have no dangerous attribute or membership. The
// privilege audit follows the grant sync in Migrator.Up, which first takes
// back whatever bekup_owner granted beyond the allowlist.
func checkExternalForMigrate(ctx context.Context, q *sqlc.Queries, login string) error {
	exists, err := q.ExternalRoleExists(ctx, ExternalRole)
	if err != nil {
		return fmt.Errorf("check role %s: %w", ExternalRole, err)
	}
	if !exists {
		return nil
	}
	if login != "" {
		return fmt.Errorf("%w: the role exists on this server; drop it (DROP ROLE %s) or switch to two roles", ErrExternalSingleAccount, ExternalRole)
	}
	rc, err := roleCheck(ctx, q, ExternalRole, []string{OwnerRole, AppRole})
	if err != nil {
		return err
	}
	return validateRole(rc)
}

// syncExternal re-applies bekup_external's privileges after the migrations
// (D-220) and audits the result; a no-op without the role.
func syncExternal(ctx context.Context, q *sqlc.Queries, login string) error {
	if login != "" {
		// Single-account mode never grants the role (checkOwnerRole
		// refused an existing one; this catches one created since).
		return checkExternalForMigrate(ctx, q, login)
	}
	synced, err := q.SyncExternalGrants(ctx)
	if err != nil {
		return fmt.Errorf("apply the privileges of %s: %w", ExternalRole, err)
	}
	if !synced {
		return nil
	}
	return checkExternalRole(ctx, q)
}
