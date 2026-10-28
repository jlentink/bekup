package dbprobe

import (
	"context"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Neutralizing a restored PostgreSQL database (M4 review, D-171). A
// restore with --no-owner makes the connection's role the owner of every
// restored object, so a SECURITY DEFINER function from the dump would run
// with the restore role's rights for anyone who may call it (for example
// ALTER ROLE CURRENT_USER PASSWORD ...). Before a restored database is
// swapped in or reported as restored, the runner switches every SECURITY
// DEFINER function and procedure to SECURITY INVOKER and revokes CONNECT
// and TEMPORARY on the database from PUBLIC, in one transaction of its
// pinned session (D-152), and checks the result. Built-in functions (owned
// by the bootstrap superuser in pg_catalog / information_schema) are left
// alone.
//
// The ALTER statements name a function by its regprocedure text, which
// the server renders with quote_ident and qualifies every name that is not
// visible through the pinned search_path (pg_catalog, pg_temp); each runs
// through the extended protocol, which accepts exactly one statement.

// Limits of a neutralization.
const (
	// MaxNeutralizeFunctions bounds the functions of one database.
	MaxNeutralizeFunctions = 100000
	// maxSignatureBytes bounds one regprocedure text.
	maxSignatureBytes = 64 << 10
)

const (
	// pgSystemProc: built-in functions (bootstrap superuser, system schemas).
	pgSystemProc = `(p.proowner` + pgEq + `'10'::pg_catalog.oid AND n.nspname` + pgEq + `ANY ('{pg_catalog,information_schema}'::pg_catalog.name[]))`
	pgSecdefFrom = `FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid` + pgEq + `p.pronamespace WHERE p.prosecdef AND NOT ` + pgSystemProc

	// pgNeutralizeOwnerQuery: the session's database and whether the
	// session's role owns it.
	pgNeutralizeOwnerQuery = `SELECT pg_catalog.current_database()::pg_catalog.text, ` +
		`((SELECT r.oid FROM pg_catalog.pg_roles r WHERE r.rolname` + pgEq + `CURRENT_USER)` + pgEq + `d.datdba)::pg_catalog.text ` +
		`FROM pg_catalog.pg_database d WHERE d.datname` + pgEq + `pg_catalog.current_database()`
	// pgSecdefQuery lists the SECURITY DEFINER routines (signature, kind).
	pgSecdefQuery = `SELECT p.oid::pg_catalog.regprocedure::pg_catalog.text, p.prokind::pg_catalog.text ` + pgSecdefFrom +
		` ORDER BY p.oid LIMIT 100001`
	// pgSecdefCountQuery re-checks that none is left.
	pgSecdefCountQuery = `SELECT ` + pgCount + ` ` + pgSecdefFrom
	// pgPublicAccessQuery: does PUBLIC still have CONNECT or TEMPORARY?
	pgPublicAccessQuery = `SELECT (pg_catalog.has_database_privilege('public', d.oid, 'CONNECT') OR ` +
		`pg_catalog.has_database_privilege('public', d.oid, 'TEMPORARY'))::pg_catalog.text ` +
		`FROM pg_catalog.pg_database d WHERE d.datname` + pgEq + `pg_catalog.current_database()`
	// pgForeignSessionsQuery counts the client sessions on the database of
	// non-superuser roles other than the session's: PUBLIC lost CONNECT
	// right after CREATE DATABASE, so such a session raced in during that
	// moment and may have called a SECURITY DEFINER function before it was
	// neutralized.
	pgForeignSessionsQuery = `SELECT ` + pgCount + ` FROM pg_catalog.pg_stat_activity a WHERE a.datname` + pgEq + `pg_catalog.current_database() ` +
		`AND a.pid` + pgNe + `pg_catalog.pg_backend_pid() AND a.backend_type` + pgEq + `'client backend' ` +
		`AND (SELECT r.oid FROM pg_catalog.pg_roles r WHERE r.rolname` + pgEq + `CURRENT_USER)` + pgNe + `a.usesysid ` +
		`AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles s WHERE s.oid` + pgEq + `a.usesysid AND s.rolsuper)`
	pgBegin    = `BEGIN`
	pgCommit   = `COMMIT`
	pgRollback = `ROLLBACK`
)

// Neutralized is the outcome of a neutralization.
type Neutralized struct {
	// Count is the number of routines switched to SECURITY INVOKER.
	Count int
	// Functions are the first maxList signatures (each ≤ 256 bytes).
	Functions []string
}

// Neutralizer is implemented by PostgreSQL connections.
type Neutralizer interface {
	// NeutralizeRestored hardens the session's database, which must be db
	// and owned by the session's role (ReasonPermission otherwise).
	NeutralizeRestored(ctx context.Context, db string, maxList int) (Neutralized, error)
}

// maxListedSignatureBytes bounds a reported signature.
const maxListedSignatureBytes = 256

// NeutralizeRestored implements Neutralizer.
func (c *pgConn) NeutralizeRestored(ctx context.Context, db string, maxList int) (out Neutralized, err error) {
	q, err := engine.QuotePGIdent(db)
	if err != nil {
		return Neutralized{}, newError(runnerpb.ReasonFailed, "database name", nil)
	}
	r, err := c.rows(ctx, pgNeutralizeOwnerQuery, 1)
	if err != nil {
		return Neutralized{}, err
	}
	if len(r) != 1 || len(r[0]) != 2 || r[0][0] != db {
		return Neutralized{}, newError(runnerpb.ReasonFailed, "session database", nil)
	}
	if r[0][1] != "true" {
		return Neutralized{}, newError(runnerpb.ReasonPermission, "not the database owner", nil)
	}
	if err := c.exec(ctx, pgBegin); err != nil {
		return Neutralized{}, err
	}
	defer func() {
		if err != nil {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
			discardErr(c.exec(rctx, pgRollback))
			cancel()
		}
	}()
	fns, err := c.rows(ctx, pgSecdefQuery, MaxNeutralizeFunctions)
	if err != nil {
		return Neutralized{}, err
	}
	for _, f := range fns {
		if len(f) != 2 || f[0] == "" || len(f[0]) > maxSignatureBytes || !utf8.ValidString(f[0]) {
			return Neutralized{}, newError(runnerpb.ReasonFailed, "function signature", nil)
		}
		kw := "FUNCTION"
		if f[1] == "p" {
			kw = "PROCEDURE"
		}
		if _, err := c.rows(ctx, "ALTER "+kw+" "+f[0]+" SECURITY INVOKER", 0); err != nil {
			return Neutralized{}, err
		}
		out.Count++
		if len(out.Functions) < maxList {
			out.Functions = append(out.Functions, truncateUTF8(f[0], maxListedSignatureBytes))
		}
	}
	if _, err := c.rows(ctx, "REVOKE CONNECT, TEMPORARY ON DATABASE "+q+" FROM PUBLIC", 0); err != nil {
		return Neutralized{}, err
	}
	left, err := c.rows(ctx, pgSecdefCountQuery, 1)
	if err != nil {
		return Neutralized{}, err
	}
	if len(left) != 1 || len(left[0]) != 1 || left[0][0] != "0" {
		return Neutralized{}, newError(runnerpb.ReasonFailed, "security definer left", nil)
	}
	pub, err := c.rows(ctx, pgPublicAccessQuery, 1)
	if err != nil {
		return Neutralized{}, err
	}
	if len(pub) != 1 || len(pub[0]) != 1 || pub[0][0] != "false" {
		return Neutralized{}, newError(runnerpb.ReasonFailed, "public access left", nil)
	}
	if err := c.exec(ctx, pgCommit); err != nil {
		return Neutralized{}, err
	}
	// After the commit: the neutralization stays even when this fails.
	foreign, err := c.rows(ctx, pgForeignSessionsQuery, 1)
	if err != nil {
		return out, err
	}
	if len(foreign) != 1 || len(foreign[0]) != 1 || foreign[0][0] != "0" {
		return out, newError(runnerpb.ReasonFailed, "foreign sessions on the restored database", nil)
	}
	return out, nil
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for s != "" && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
