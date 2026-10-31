package dbprobe

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/schemaversion"
)

const (
	// pgVersionEnd discards everything the capture did.
	pgVersionEnd = `ROLLBACK TO SAVEPOINT bekup_version; RELEASE SAVEPOINT bekup_version`

	// pgVersionLayoutQuery checks a preset's table: its kind, the SELECT
	// privilege and how many of the read columns ($3) exist with a
	// built-in pg_catalog type of the allowlist ($4). No row: no such
	// table.
	pgVersionLayoutQuery = `SELECT c.relkind, pg_catalog.has_table_privilege(c.oid, 'SELECT'), ` +
		`(SELECT ` + pgCount + ` FROM pg_catalog.pg_attribute a ` +
		`JOIN pg_catalog.pg_type t ON t.oid` + pgEq + `a.atttypid ` +
		`JOIN pg_catalog.pg_namespace tn ON tn.oid` + pgEq + `t.typnamespace ` +
		`WHERE a.attrelid` + pgEq + `c.oid AND a.attnum` + pgGtOp + `0 AND NOT a.attisdropped ` +
		`AND a.attname` + pgEq + `ANY ($3::pg_catalog.name[]) AND tn.nspname` + pgEq + `'pg_catalog' ` +
		`AND t.typname` + pgEq + `ANY ($4::pg_catalog.name[])) ` +
		`FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid` + pgEq + `c.relnamespace ` +
		`WHERE n.nspname` + pgEq + `$1::pg_catalog.name AND c.relname` + pgEq + `$2::pg_catalog.name`

	pgGtOp = ` OPERATOR(pg_catalog.>) `

	// pgByteaOID is the bytea type: a binary result is invalid.
	pgByteaOID = 17
)

// pgVersionBegin opens the savepoint of a capture with its limits (integer
// milliseconds). SET LOCAL ends with the savepoint's rollback, so the stats
// transaction (whose idle timeout is lifted for pg_dump) keeps its own
// settings.
func pgVersionBegin() string {
	ms := func(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }
	return "SAVEPOINT bekup_version; SET LOCAL statement_timeout TO " + ms(schemaversion.StatementTimeout) +
		"; SET LOCAL lock_timeout TO " + ms(schemaversion.LockTimeout) +
		"; SET LOCAL idle_in_transaction_session_timeout TO " + ms(schemaversion.CaptureTimeout)
}

// pgArray encodes simple names as a text array literal.
func pgArray(names []string) []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(n)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// pgVersionStatus maps a statement error: a missing table or schema is
// "no version", everything else is unavailable.
func pgVersionStatus(err error) VersionResult {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.Code == "42P01" || pe.Code == "3F000" {
			return versionFailure(schemaversion.StatusNone, "sqlstate="+pe.Code)
		}
		return versionFailure(schemaversion.StatusUnavailable, "sqlstate="+pe.Code)
	}
	return versionFailure(schemaversion.StatusUnavailable, "query")
}

// pgVersionEndTimeout bounds the rollback of the capture's savepoint (a
// fresh context: the capture's own may have expired).
const pgVersionEndTimeout = 5 * time.Second

// pgBuiltinTypeOIDs are the fixed OIDs of schemaversion.TypeAllowedPG (a
// test ties both): the types a preset's raw columns may have in the
// current definition (RowDescription reports a domain's base type).
var pgBuiltinTypeOIDs = map[uint32]string{
	21: "int2", 23: "int4", 20: "int8", 1700: "numeric", 25: "text", 1043: "varchar", 1042: "bpchar",
	16: "bool", 1114: "timestamp", 1184: "timestamptz", 1082: "date",
}

// SchemaVersion implements StatsSource (see version.go). The savepoint is
// always rolled back with a fresh, short context; when that fails (or the
// connection broke) the result says SessionLost, so the caller stops
// sharing the snapshot with the dump.
func (c *pgConn) SchemaVersion(ctx context.Context, _ string, q VersionQuery) VersionResult {
	if !c.restricted {
		// Without restrict_nonsystem_relation_kind a read could run a
		// view's (the database owner's) code: refused before anything is
		// sent (D-152).
		return versionFailure(schemaversion.StatusUnavailable, "unsafe server")
	}
	cctx, cancel := context.WithTimeout(ctx, schemaversion.CaptureTimeout)
	defer cancel()
	var res VersionResult
	if err := c.exec(cctx, pgVersionBegin()); err != nil {
		// The SAVEPOINT may have succeeded before a SET LOCAL failed: the
		// rollback below recovers the transaction either way.
		res = versionFailure(schemaversion.StatusUnavailable, "savepoint")
	} else {
		res = c.schemaVersion(cctx, q)
	}
	ectx, ecancel := context.WithTimeout(context.WithoutCancel(ctx), pgVersionEndTimeout)
	defer ecancel()
	if err := c.exec(ectx, pgVersionEnd); err != nil || c.c.IsClosed() {
		return VersionResult{Status: schemaversion.StatusUnavailable, Detail: "rollback", SessionLost: true}
	}
	return res
}

// execRaw runs a simple-protocol statement and returns the driver's error
// unwrapped (pgVersionStatus classifies it).
func (c *pgConn) execRaw(ctx context.Context, sql string) error {
	_, err := c.c.Exec(ctx, sql).ReadAll()
	return err
}

func (c *pgConn) schemaVersion(ctx context.Context, q VersionQuery) VersionResult {
	var sql string
	switch {
	case q.Preset != nil:
		pq := q.Preset.QueryFor(engine.KindPostgres)
		s, err := engine.QuotePGObject(q.Schema)
		if err != nil {
			return versionFailure(schemaversion.StatusUnavailable, "schema name")
		}
		t, err := engine.QuotePGObject(pq.Table)
		if err != nil {
			return versionFailure(schemaversion.StatusUnavailable, "table name")
		}
		table := s + "." + t
		// The lock (bounded by lock_timeout, released with the
		// savepoint) keeps the definition from changing between the
		// checks and the read. A missing table or schema is "no version".
		if err := c.execRaw(ctx, "LOCK TABLE "+table+" IN ACCESS SHARE MODE"); err != nil {
			return pgVersionStatus(err)
		}
		if r, ok := c.checkPresetLayout(ctx, q.Schema, pq); !ok {
			return r
		}
		if r, ok := c.checkPresetTypes(ctx, table, pq); !ok {
			return r
		}
		sql = pq.Statement(table)
	case q.Custom != "":
		sql = "SELECT * FROM (\n" + q.Custom + "\n) bekup_version LIMIT 1"
	default:
		return versionFailure(schemaversion.StatusUnavailable, "no query")
	}
	rr := c.c.ExecParams(ctx, sql, nil, nil, nil, nil)
	var (
		cols   []*string
		gotRow bool
		binary bool
	)
	if rr.NextRow() {
		gotRow = true
		if fds := rr.FieldDescriptions(); len(fds) > 0 && fds[0].DataTypeOID == pgByteaOID {
			binary = true
		}
		vals := rr.Values()
		if q.Preset == nil && len(vals) > 1 {
			vals = vals[:1] // a custom query: the first column only
		}
		for _, v := range vals {
			if v == nil {
				cols = append(cols, nil)
				continue
			}
			s := string(v)
			cols = append(cols, &s)
		}
	}
	if _, err := rr.Close(); err != nil {
		return pgVersionStatus(err)
	}
	switch {
	case !gotRow:
		return versionFailure(schemaversion.StatusNone, "no row")
	case binary && q.Preset == nil:
		return versionFailure(schemaversion.StatusInvalid, "binary")
	}
	return versionValue(q, cols)
}

// checkPresetTypes describes (prepares, never runs) a statement reading
// the preset's raw columns and checks the result types of the current,
// locked definition: the catalog check reads the snapshot, which does not
// see a column type changed after it was taken.
func (c *pgConn) checkPresetTypes(ctx context.Context, table string, pq schemaversion.Query) (VersionResult, bool) {
	cols := make([]string, len(pq.Columns))
	for i, col := range pq.Columns {
		qc, err := engine.QuotePGObject(col)
		if err != nil {
			return versionFailure(schemaversion.StatusUnavailable, "column name"), false
		}
		cols[i] = qc
	}
	sd, err := c.c.Prepare(ctx, "", "SELECT "+strings.Join(cols, ", ")+" FROM "+table+" LIMIT 0", nil)
	if err != nil {
		return pgVersionStatus(err), false
	}
	if len(sd.Fields) != len(pq.Columns) {
		return versionFailure(schemaversion.StatusUnavailable, "layout: columns"), false
	}
	for _, f := range sd.Fields {
		if _, ok := pgBuiltinTypeOIDs[f.DataTypeOID]; !ok {
			return versionFailure(schemaversion.StatusUnavailable, "layout: column type"), false
		}
	}
	return VersionResult{}, true
}

// checkPresetLayout verifies the preset table before it is read.
func (c *pgConn) checkPresetLayout(ctx context.Context, schema string, pq schemaversion.Query) (VersionResult, bool) {
	rr := c.c.ExecParams(ctx, pgVersionLayoutQuery,
		[][]byte{[]byte(schema), []byte(pq.Table), pgArray(pq.Columns), pgArray(schemaversion.TypeAllowedPG)},
		nil, nil, nil)
	var rows [][]string
	for rr.NextRow() {
		if len(rows) > 0 {
			break
		}
		v := rr.Values()
		row := make([]string, len(v))
		for i, b := range v {
			row[i] = string(b)
		}
		rows = append(rows, row)
	}
	if _, err := rr.Close(); err != nil {
		return pgVersionStatus(err), false
	}
	if len(rows) == 0 {
		return versionFailure(schemaversion.StatusNone, "no table"), false
	}
	r := rows[0]
	if len(r) != 3 {
		return versionFailure(schemaversion.StatusUnavailable, "layout"), false
	}
	if r[0] != "r" && r[0] != "p" {
		return versionFailure(schemaversion.StatusUnavailable, "layout: not a table"), false
	}
	if r[1] != "t" {
		return versionFailure(schemaversion.StatusUnavailable, "sqlstate=42501"), false
	}
	if n, err := parseCount(r[2]); err != nil || n != int64(len(pq.Columns)) {
		return versionFailure(schemaversion.StatusUnavailable, "layout: columns"), false
	}
	return VersionResult{}, true
}
