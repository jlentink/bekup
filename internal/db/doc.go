// Package db owns access to the bekup application database: the pgx pool
// (NewPool), the embedded goose migrations including the vendored River schema
// (Migrator), the sqlc-generated queries (internal/db/sqlc) and the Tx
// transaction helper. No hand-built SQL lives outside this package; all
// queries are in internal/db/queries/*.sql.
//
// Roles (D-054): migrations run as bekup_owner, the runtime pool connects as
// bekup_app. The audit_log hash chain is computed by a database trigger; the
// auditchain subpackage recomputes it in Go. See docs/security/db-roles.md,
// docs/security/audit.md and docs/development.md.
package db
