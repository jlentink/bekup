// Package dbsetup prepares the app database: the setup version guard
// (D-212), `db bootstrap` (roles and database on an external PostgreSQL,
// D-207) and `migrate up` with its preflight. The CLI commands and the
// browser setup (D-217) share it, so both paths apply the same TLS
// policy, guard, role checks and audit records. Reports are facts only:
// no host names, credentials or server messages beyond the checks' own.
package dbsetup
