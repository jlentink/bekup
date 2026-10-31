package dbprobe

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/schemaversion"
)

const (
	// myVersionTables finds a preset's table in the dumped database
	// (exact name first; case-insensitively for lower_case_table_names).
	myVersionTables = `SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND LOWER(TABLE_NAME) = LOWER(?)`
	// myVersionColumns lists the table's columns and their base types.
	myVersionColumns = `SELECT LOWER(COLUMN_NAME), LOWER(DATA_TYPE) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`
)

// myVersionReadBytes bounds what a capture reads from the server (the
// driver buffers whole rows; a larger result breaks the session, which
// ends right after the capture anyway).
const myVersionReadBytes = 1 << 20

// myVersionLimits are the session limits of a capture: the statement
// limit (MySQL max_execution_time in ms; MariaDB max_statement_time in
// seconds) and the metadata-lock wait (whole seconds, at least 1).
func myVersionLimits(k engine.Kind) []string {
	lock := "SET SESSION lock_wait_timeout = " + strconv.FormatInt(max(int64(schemaversion.LockTimeout.Seconds()), 1), 10)
	if k == engine.KindMySQL {
		return []string{"SET SESSION max_execution_time = " + strconv.FormatInt(schemaversion.StatementTimeout.Milliseconds(), 10), lock}
	}
	return []string{"SET SESSION max_statement_time = " + strconv.FormatInt(int64(schemaversion.StatementTimeout.Seconds()), 10), lock}
}

// myVersionStatus maps a statement error: an unknown table or database is
// "no version", everything else is unavailable.
func myVersionStatus(err error) VersionResult {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		d := "mysql=" + strconv.Itoa(int(me.Number))
		if me.Number == 1146 || me.Number == 1049 {
			return versionFailure(schemaversion.StatusNone, d)
		}
		return versionFailure(schemaversion.StatusUnavailable, d)
	}
	return versionFailure(schemaversion.StatusUnavailable, "query")
}

// SchemaVersion implements StatsSource (see version.go). The stats
// transaction is READ ONLY; the session ends right after the stats, so the
// session limits set here need no reset.
func (c *myConn) SchemaVersion(ctx context.Context, db string, q VersionQuery) VersionResult {
	if engine.ValidateDBName(db) != nil {
		return versionFailure(schemaversion.StatusUnavailable, "database name")
	}
	ctx, cancel := context.WithTimeout(ctx, schemaversion.CaptureTimeout)
	defer cancel()
	c.budget.arm(myVersionReadBytes)
	defer c.budget.disarm()
	for _, s := range myVersionLimits(c.eng.Kind()) {
		if _, err := c.c.ExecContext(ctx, s); err != nil {
			return versionFailure(schemaversion.StatusUnavailable, "limits")
		}
	}
	qdb, err := engine.QuoteMySQLObject(db)
	if err != nil {
		return versionFailure(schemaversion.StatusUnavailable, "database name")
	}
	switch {
	case q.Preset != nil:
		pq := q.Preset.QueryFor(c.eng.Kind())
		table, r, ok := c.presetTable(ctx, db, pq)
		if !ok {
			return r
		}
		qt, err := engine.QuoteMySQLObject(table)
		if err != nil {
			return versionFailure(schemaversion.StatusUnavailable, "table name")
		}
		res := c.firstRow(ctx, q, pq.Statement(qdb+"."+qt))
		// information_schema shows the current definition: a table
		// replaced (or a column retyped) between the check and the read
		// discards the value (the window itself is a residual risk,
		// D-210).
		if again, _, ok := c.presetTable(ctx, db, pq); !ok || again != table {
			return versionFailure(schemaversion.StatusUnavailable, "layout: changed")
		}
		return res
	case q.Custom != "":
		// Unqualified tables of a custom query are those of the dumped
		// database.
		if _, err := c.c.ExecContext(ctx, "USE "+qdb); err != nil {
			return myVersionStatus(err)
		}
		return c.firstRow(ctx, q, myCustomStatement(q.Custom))
	default:
		return versionFailure(schemaversion.StatusUnavailable, "no query")
	}
}

// myCustomStatement wraps a validated custom query: one row at most, and a
// derived table keeps the statement a single SELECT.
func myCustomStatement(custom string) string {
	return "SELECT * FROM (\n" + custom + "\n) AS bekup_version LIMIT 1"
}

// firstRow runs stmt and reads the first row only.
func (c *myConn) firstRow(ctx context.Context, q VersionQuery, stmt string) VersionResult {
	rows, err := c.c.QueryContext(ctx, stmt)
	if err != nil {
		return myVersionStatus(err)
	}
	defer func() { discardErr(rows.Close()) }()
	names, err := rows.Columns()
	if err != nil || len(names) == 0 {
		return versionFailure(schemaversion.StatusUnavailable, "columns")
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return myVersionStatus(err)
		}
		return versionFailure(schemaversion.StatusNone, "no row")
	}
	vals := make([]sql.RawBytes, len(names))
	dest := make([]any, len(names))
	for i := range vals {
		dest[i] = &vals[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return myVersionStatus(err)
	}
	if q.Preset == nil {
		vals = vals[:1] // a custom query: the first column only
	}
	cols := make([]*string, len(vals))
	for i, v := range vals {
		if v != nil {
			s := string(v)
			cols[i] = &s
		}
	}
	return versionValue(q, cols)
}

// presetTable finds the preset's table (exact name first, else the one
// case-insensitive match) and checks it is a base table whose read
// columns have built-in types.
func (c *myConn) presetTable(ctx context.Context, db string, pq schemaversion.Query) (string, VersionResult, bool) {
	found, err := c.versionTables(ctx, db, pq.Table)
	if err != nil {
		return "", myVersionStatus(err), false
	}
	var pick *myTable
	for i := range found {
		if found[i].name == pq.Table {
			pick = &found[i]
		}
	}
	switch {
	case pick != nil:
	case len(found) == 0:
		return "", versionFailure(schemaversion.StatusNone, "no table"), false
	case len(found) == 1:
		pick = &found[0]
	default:
		return "", versionFailure(schemaversion.StatusUnavailable, "layout: ambiguous table"), false
	}
	if pick.kind != "BASE TABLE" {
		return "", versionFailure(schemaversion.StatusUnavailable, "layout: not a table"), false
	}
	types, err := c.versionColumns(ctx, db, pick.name)
	if err != nil {
		return "", myVersionStatus(err), false
	}
	for _, col := range pq.Columns {
		t, ok := types[strings.ToLower(col)]
		if !ok || !slices.Contains(schemaversion.TypeAllowedMySQL, t) {
			return "", versionFailure(schemaversion.StatusUnavailable, "layout: columns"), false
		}
	}
	return pick.name, VersionResult{}, true
}

type myTable struct{ name, kind string }

// versionTables lists the tables named like table (at most 8).
func (c *myConn) versionTables(ctx context.Context, db, table string) ([]myTable, error) {
	rows, err := c.c.QueryContext(ctx, myVersionTables, db, table)
	if err != nil {
		return nil, err
	}
	defer func() { discardErr(rows.Close()) }()
	var found []myTable
	for rows.Next() {
		var t myTable
		if err := rows.Scan(&t.name, &t.kind); err != nil {
			return nil, err
		}
		if len(found) < 8 {
			found = append(found, t)
		}
	}
	return found, rows.Err()
}

// versionColumns maps the table's lower-case column names to their types.
func (c *myConn) versionColumns(ctx context.Context, db, table string) (map[string]string, error) {
	rows, err := c.c.QueryContext(ctx, myVersionColumns, db, table)
	if err != nil {
		return nil, err
	}
	defer func() { discardErr(rows.Close()) }()
	types := map[string]string{}
	for rows.Next() {
		var n, t string
		if err := rows.Scan(&n, &t); err != nil {
			return nil, err
		}
		if len(types) < 4096 {
			types[n] = t
		}
	}
	return types, rows.Err()
}
