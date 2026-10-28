package dbprobe

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// PostgreSQL statistics queries. User objects live outside pg_catalog,
// information_schema, pg_toast and the temporary schemas; members of
// extensions are left out (pg_dump recreates them with CREATE EXTENSION).
// Every query is static; names travel only as results, and the one
// statement built from catalog names (the count) quotes them with
// engine.QuotePGObject.
//
// Every function, aggregate, operator, type and collation is
// schema-qualified (pg_catalog.count(*), OPERATOR(pg_catalog.=),
// pg_catalog.name[], pg_catalog."C"), so nothing resolves through the
// search_path even if the session pin failed (D-152); TestPGQueriesQualified
// enforces it. "x IN (...)" is written "x OPERATOR(pg_catalog.=) ANY
// (array)" because IN looks its operator up by name.
const (
	pgEq   = ` OPERATOR(pg_catalog.=) `
	pgNe   = ` OPERATOR(pg_catalog.<>) `
	pgNRe  = ` OPERATOR(pg_catalog.!~) `
	pgCat  = ` OPERATOR(pg_catalog.||) `
	pgColl = ` COLLATE pg_catalog."C"`

	pgUserNS     = `n.nspname` + pgNe + `ALL ('{pg_catalog,information_schema,pg_toast}'::pg_catalog.name[]) AND n.nspname` + pgNRe + `'^pg_(toast_)?temp_'`
	pgNotExtRel  = `NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.classid` + pgEq + `'pg_catalog.pg_class'::pg_catalog.regclass AND d.objid` + pgEq + `c.oid AND d.deptype` + pgEq + `'e')`
	pgNotExtNS   = `NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.classid` + pgEq + `'pg_catalog.pg_namespace'::pg_catalog.regclass AND d.objid` + pgEq + `n.oid AND d.deptype` + pgEq + `'e')`
	pgNotExtProc = `NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.classid` + pgEq + `'pg_catalog.pg_proc'::pg_catalog.regclass AND d.objid` + pgEq + `p.oid AND d.deptype` + pgEq + `'e')`
	pgRel        = `FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid` + pgEq + `c.relnamespace WHERE ` + pgUserNS + ` AND ` + pgNotExtRel
	pgProc       = `FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid` + pgEq + `p.pronamespace WHERE ` + pgUserNS + ` AND ` + pgNotExtProc
	pgCount      = `pg_catalog.count(*)`

	pgObjectCountsQuery = `SELECT ` +
		`(SELECT ` + pgCount + ` FROM pg_catalog.pg_namespace n WHERE ` + pgUserNS + ` AND ` + pgNotExtNS + `), ` +
		`(SELECT ` + pgCount + ` ` + pgRel + ` AND c.relkind` + pgEq + `ANY ('{r,p}'::pg_catalog."char"[])), ` +
		`(SELECT ` + pgCount + ` ` + pgRel + ` AND c.relkind` + pgEq + `'v'), ` +
		`(SELECT ` + pgCount + ` ` + pgRel + ` AND c.relkind` + pgEq + `'m'), ` +
		`(SELECT ` + pgCount + ` ` + pgRel + ` AND c.relkind` + pgEq + `'S'), ` +
		`(SELECT ` + pgCount + ` ` + pgProc + ` AND p.prokind` + pgNe + `'p'), ` +
		`(SELECT ` + pgCount + ` ` + pgProc + ` AND p.prokind` + pgEq + `'p'), ` +
		`(SELECT ` + pgCount + ` FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid` + pgEq + `t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid` + pgEq + `c.relnamespace WHERE NOT t.tgisinternal AND ` + pgUserNS + ` AND ` + pgNotExtRel + `), ` +
		`(SELECT ` + pgCount + ` FROM pg_catalog.pg_extension)`

	// pgTablesQuery lists the tables holding data (relkind r: plain tables
	// and partitions; partitioned parents hold none) with the estimate
	// (reltuples; -1 = never analyzed on PostgreSQL 14+) and the SELECT
	// privilege; the window count is the total before the LIMIT.
	pgTablesQuery = `SELECT n.nspname, c.relname, c.reltuples::pg_catalog.int8, pg_catalog.has_table_privilege(c.oid, 'SELECT'), ` + pgCount + ` OVER () ` +
		pgRel + ` AND c.relkind` + pgEq + `'r' ORDER BY n.nspname` + pgColl + `, c.relname` + pgColl + ` LIMIT 10000`

	// pgNoPrivilegeQuery lists what pg_dump cannot read with this role:
	// tables and sequences without SELECT (has_table_privilege covers
	// sequences; has_sequence_privilege would fail on other relations, as
	// the planner may evaluate it before the relkind filter), schemas
	// without USAGE.
	// Functions are read from the catalog (no EXECUTE needed) and are not
	// listed.
	pgNoPrivilegeQuery = `SELECT name, ` + pgCount + ` OVER () FROM (` +
		`SELECT n.nspname::pg_catalog.text` + pgCat + `'.'` + pgCat + `c.relname::pg_catalog.text AS name ` + pgRel +
		` AND c.relkind` + pgEq + `ANY ('{r,p,S}'::pg_catalog."char"[]) AND NOT pg_catalog.has_table_privilege(c.oid, 'SELECT') ` +
		`UNION ALL SELECT n.nspname::pg_catalog.text FROM pg_catalog.pg_namespace n WHERE ` + pgUserNS + ` AND ` + pgNotExtNS + ` AND NOT pg_catalog.has_schema_privilege(n.oid, 'USAGE')` +
		`) x ORDER BY name` + pgColl + ` LIMIT 50`

	pgGlobalCountsQuery = `SELECT (SELECT ` + pgCount + ` FROM pg_catalog.pg_roles r WHERE r.rolname` + pgNRe + `'^pg_'), ` +
		`(SELECT ` + pgCount + ` FROM pg_catalog.pg_tablespace s WHERE s.spcname` + pgNe + `ALL ('{pg_default,pg_global}'::pg_catalog.name[]))`

	// pgBeginStats starts the statistics transaction. The idle and
	// transaction timeouts are lifted for it (where the server has them):
	// the exporting transaction must survive until pg_dump has imported
	// its snapshot.
	pgBeginStats   = `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY`
	pgLiftTimeouts = `SELECT pg_catalog.set_config(s.name, '0', true) FROM pg_catalog.pg_settings s ` +
		`WHERE s.name` + pgEq + `ANY ('{idle_in_transaction_session_timeout,transaction_timeout}'::pg_catalog.text[])`
	pgExportSnapshot = `SELECT pg_catalog.pg_export_snapshot()`
)

// rows runs one statement and returns every row as text values (bounded).
func (c *pgConn) rows(ctx context.Context, sql string, limit int) ([][]string, error) {
	rr := c.c.ExecParams(ctx, sql, nil, nil, nil, nil)
	var out [][]string
	for rr.NextRow() {
		if len(out) >= limit {
			discardResult(rr.Close())
			return nil, newError(runnerpb.ReasonFailed, "too many rows", nil)
		}
		v := rr.Values()
		row := make([]string, len(v))
		for i, b := range v {
			row[i] = string(b)
		}
		out = append(out, row)
	}
	if _, err := rr.Close(); err != nil {
		return nil, queryFailure(ctx, err, pgReason)
	}
	return out, nil
}

// exec runs a simple-protocol statement (no parameters).
func (c *pgConn) exec(ctx context.Context, sql string) error {
	if _, err := c.c.Exec(ctx, sql).ReadAll(); err != nil {
		return queryFailure(ctx, err, pgReason)
	}
	return nil
}

func (c *pgConn) BeginStats(ctx context.Context, export bool) (string, error) {
	if err := c.exec(ctx, pgBeginStats); err != nil {
		return "", err
	}
	if _, err := c.rows(ctx, pgLiftTimeouts, 4); err != nil {
		return "", err
	}
	if !export {
		return "", nil
	}
	r, err := c.rows(ctx, pgExportSnapshot, 1)
	if err != nil {
		return "", err
	}
	if len(r) != 1 || len(r[0]) != 1 || engine.ValidateSnapshotID(r[0][0]) != nil {
		return "", newError(runnerpb.ReasonFailed, "snapshot id", nil)
	}
	return r[0][0], nil
}

func (c *pgConn) JoinStats(ctx context.Context, snapshot string) error {
	if err := engine.ValidateSnapshotID(snapshot); err != nil {
		return newError(runnerpb.ReasonFailed, "snapshot id", nil)
	}
	// The id is validated (hex digits and dashes only), so the literal
	// needs no escaping; SET TRANSACTION SNAPSHOT takes no parameters.
	return c.exec(ctx, pgBeginStats+"; SET TRANSACTION SNAPSHOT '"+snapshot+"'")
}

func (c *pgConn) Catalog(ctx context.Context, _ string) (*Catalog, error) {
	out := &Catalog{}
	r, err := c.rows(ctx, pgObjectCountsQuery, 1)
	if err != nil {
		return nil, err
	}
	if len(r) != 1 || len(r[0]) != 9 {
		return nil, newError(runnerpb.ReasonFailed, "object counts", nil)
	}
	o := &out.Objects
	for i, dst := range []*int64{&o.Schemas, &o.Tables, &o.Views, &o.MaterializedViews, &o.Sequences, &o.Functions, &o.Procedures, &o.Triggers, &o.Extensions} {
		if *dst, err = parseCount(r[0][i]); err != nil {
			return nil, err
		}
	}
	tables, err := c.rows(ctx, pgTablesQuery, MaxStatsTables)
	if err != nil {
		return nil, err
	}
	for _, row := range tables {
		if len(row) != 5 {
			return nil, newError(runnerpb.ReasonFailed, "table row", nil)
		}
		est, err := strconv.ParseInt(row[2], 10, 64)
		if err != nil {
			return nil, newError(runnerpb.ReasonFailed, "estimate", nil)
		}
		total, err := parseCount(row[4])
		if err != nil {
			return nil, err
		}
		out.TablesTotal = int(total)
		out.Tables = append(out.Tables, Table{
			Schema: row[0], Name: row[1], Estimate: max(est, -1), Readable: row[3] == "t", Transactional: true,
		})
	}
	priv, err := c.rows(ctx, pgNoPrivilegeQuery, MaxWarningObjects)
	if err != nil {
		return nil, err
	}
	for _, row := range priv {
		if len(row) != 2 {
			return nil, newError(runnerpb.ReasonFailed, "privilege row", nil)
		}
		n, err := parseCount(row[1])
		if err != nil {
			return nil, err
		}
		out.NoPrivilege.Objects = append(out.NoPrivilege.Objects, row[0])
		out.NoPrivilege.Count = int(n)
	}
	return out, nil
}

// SafeRowCounts reports whether restrict_nonsystem_relation_kind is in
// effect (see pgConn.harden): only then may a count name a relation.
func (c *pgConn) SafeRowCounts() bool { return c.restricted }

func (c *pgConn) CountRows(ctx context.Context, _ string, t Table, timeout time.Duration) (int64, error) {
	if !c.restricted {
		return 0, ErrCountUnsafe
	}
	s, err := engine.QuotePGObject(t.Schema)
	if err != nil {
		return 0, newError(runnerpb.ReasonFailed, "object name", nil)
	}
	n, err := engine.QuotePGObject(t.Name)
	if err != nil {
		return 0, newError(runnerpb.ReasonFailed, "object name", nil)
	}
	// A failed statement aborts the transaction: each count runs in a
	// savepoint so a timeout or a missing privilege leaves the snapshot
	// usable. The timeout is an integer; ONLY skips inheritance children
	// (they are listed themselves).
	if err := c.exec(ctx, "SAVEPOINT bekup_count; SET LOCAL statement_timeout = "+strconv.FormatInt(countTimeoutMillis(timeout), 10)); err != nil {
		return 0, err
	}
	// With row_security off, a table whose policies apply to this role
	// fails (42501, reported as missing privilege) instead of running the
	// policy expressions.
	r, qerr := c.rows(ctx, "SELECT "+pgCount+" FROM ONLY "+s+"."+n, 1)
	if qerr != nil {
		if err := c.exec(ctx, "ROLLBACK TO SAVEPOINT bekup_count"); err != nil {
			return 0, err
		}
		var pe *pgconn.PgError
		if errors.As(qerr, &pe) && pe.Code == "57014" {
			return 0, ErrCountTimeout
		}
		return 0, qerr
	}
	if err := c.exec(ctx, "RELEASE SAVEPOINT bekup_count"); err != nil {
		return 0, err
	}
	if len(r) != 1 || len(r[0]) != 1 {
		return 0, newError(runnerpb.ReasonFailed, "count rows", nil)
	}
	return parseCount(r[0][0])
}

func (c *pgConn) GlobalCounts(ctx context.Context) (int64, int64, error) {
	r, err := c.rows(ctx, pgGlobalCountsQuery, 1)
	if err != nil {
		return 0, 0, err
	}
	if len(r) != 1 || len(r[0]) != 2 {
		return 0, 0, newError(runnerpb.ReasonFailed, "global counts", nil)
	}
	roles, err := parseCount(r[0][0])
	if err != nil {
		return 0, 0, err
	}
	ts, err := parseCount(r[0][1])
	return roles, ts, err
}

func (c *pgConn) EndStats(ctx context.Context) error { return c.exec(ctx, "ROLLBACK") }

// parseCount parses a non-negative integer result.
func parseCount(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0, newError(runnerpb.ReasonFailed, "count", nil)
	}
	return n, nil
}
