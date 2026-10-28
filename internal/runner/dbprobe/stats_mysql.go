package dbprobe

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// MySQL/MariaDB statistics queries. information_schema shows only the
// objects the account has some privilege on: objects it cannot see at all
// are invisible here and to the dump tool alike (documented limit).
const (
	myObjectCountsQuery = `SELECT ` +
		`(SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'), ` +
		`(SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE IN ('VIEW', 'SYSTEM VIEW')), ` +
		`(SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'SEQUENCE'), ` +
		`(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? AND ROUTINE_TYPE = 'FUNCTION'), ` +
		`(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? AND ROUTINE_TYPE = 'PROCEDURE'), ` +
		`(SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ?), ` +
		`(SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ?)`

	// myTablesQuery lists the base tables with engine and TABLE_ROWS
	// (InnoDB: an estimate; MyISAM/Aria: exact at the time of reading).
	myTablesQuery = `SELECT TABLE_NAME, COALESCE(ENGINE, ''), COALESCE(TABLE_ROWS, -1) FROM information_schema.TABLES ` +
		`WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`

	// myBeginStats starts the statistics transaction: a consistent
	// snapshot taken right before the dump tool takes its own.
	myIsolation  = `SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ`
	myBeginStats = `START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY`
	// myStatsExpiry makes MySQL 8 read fresh TABLE_ROWS instead of its
	// cached statistics (best effort; MariaDB has no such variable).
	myStatsExpiry = `SET SESSION information_schema_stats_expiry = 0`
)

// transactionalEngines keep MVCC snapshots: --single-transaction dumps
// them consistently. Every other engine (MyISAM, Aria, MEMORY, CSV,
// ARCHIVE, MRG_MYISAM, ...) is dumped as it changes.
var transactionalEngines = []string{"innodb", "rocksdb", "tokudb", "sequence"}

func (c *myConn) BeginStats(ctx context.Context, _ bool) (string, error) {
	if c.eng.Kind() == engine.KindMySQL {
		discardResult(c.c.ExecContext(ctx, myStatsExpiry))
	}
	return "", c.JoinStats(ctx, "")
}

func (c *myConn) JoinStats(ctx context.Context, _ string) error {
	for _, q := range []string{myIsolation, myBeginStats} {
		if _, err := c.c.ExecContext(ctx, q); err != nil {
			return queryFailure(ctx, err, myReason)
		}
	}
	return nil
}

func (c *myConn) Catalog(ctx context.Context, db string) (*Catalog, error) {
	if err := engine.ValidateDBName(db); err != nil {
		return nil, newError(runnerpb.ReasonFailed, "database name", nil)
	}
	out := &Catalog{}
	args := []any{db, db, db, db, db, db, db}
	var counts [7]int64
	if err := c.c.QueryRowContext(ctx, myObjectCountsQuery, args...).Scan(
		&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5], &counts[6]); err != nil {
		return nil, queryFailure(ctx, err, myReason)
	}
	out.Objects = ObjectCounts{
		Schemas: 1, Tables: counts[0], Views: counts[1], Sequences: counts[2],
		Functions: counts[3], Procedures: counts[4], Triggers: counts[5], Events: counts[6],
	}
	rows, err := c.c.QueryContext(ctx, myTablesQuery, db)
	if err != nil {
		return nil, queryFailure(ctx, err, myReason)
	}
	defer func() { discardErr(rows.Close()) }()
	var all []Table
	for rows.Next() {
		var (
			name, eng string
			est       int64
		)
		if err := rows.Scan(&name, &eng, &est); err != nil {
			return nil, queryFailure(ctx, err, myReason)
		}
		out.TablesTotal++
		t := Table{Name: name, Estimate: max(est, -1), Readable: true,
			Transactional: slices.Contains(transactionalEngines, strings.ToLower(eng))}
		if !t.Transactional {
			out.NonTransactional.Add(name)
		}
		// Keep the list bounded while reading (the total keeps counting).
		if len(all) < 4*MaxStatsTables {
			all = append(all, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, queryFailure(ctx, err, myReason)
	}
	slices.SortFunc(all, func(a, b Table) int { return strings.Compare(a.Name, b.Name) })
	if len(all) > MaxStatsTables {
		all = all[:MaxStatsTables]
	}
	out.Tables = all
	slices.Sort(out.NonTransactional.Objects)
	if err := c.markUnreadable(ctx, db, out); err != nil {
		return nil, err
	}
	return out, nil
}

// MySQL/MariaDB privilege checks read information_schema only (user
// decision, D-153: no statement ever reads a user table or view).
const (
	myWhoAmI = `SELECT CURRENT_USER(), CURRENT_ROLE()`
	// myBroadSelect counts global and database-level SELECT grants of
	// the account (database grants may be patterns: db LIKE pattern).
	myBroadSelect = `SELECT (SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'SELECT') + ` +
		`(SELECT COUNT(*) FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'SELECT' AND ? LIKE TABLE_SCHEMA)`
	// myTableSelect lists the tables of db with a table-level SELECT grant.
	myTableSelect = `SELECT TABLE_NAME FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE = ? AND TABLE_SCHEMA = ? AND PRIVILEGE_TYPE = 'SELECT'`
)

// markUnreadable flags the listed tables the account has no SELECT grant
// on (global, database or table level). Privileges that reach the account
// through roles are not visible in information_schema: with an active
// role the check is skipped (every table assumed readable; the dump tool
// still reports a missing privilege). Column-level grants do not count.
func (c *myConn) markUnreadable(ctx context.Context, db string, out *Catalog) error {
	var user, role sql.NullString
	if err := c.c.QueryRowContext(ctx, myWhoAmI).Scan(&user, &role); err != nil {
		return queryFailure(ctx, err, myReason)
	}
	if role.Valid && role.String != "" && !strings.EqualFold(role.String, "NONE") {
		return nil
	}
	at := strings.LastIndexByte(user.String, '@')
	if !user.Valid || at < 0 {
		return newError(runnerpb.ReasonFailed, "current user", nil)
	}
	grantee := "'" + user.String[:at] + "'@'" + user.String[at+1:] + "'"
	var broad int64
	if err := c.c.QueryRowContext(ctx, myBroadSelect, grantee, grantee, db).Scan(&broad); err != nil {
		return queryFailure(ctx, err, myReason)
	}
	if broad > 0 {
		return nil
	}
	granted, err := c.column(ctx, 4*MaxStatsTables, myTableSelect, grantee, db)
	if err != nil {
		return err
	}
	for i := range out.Tables {
		if !slices.Contains(granted, out.Tables[i].Name) {
			out.Tables[i].Readable = false
			out.NoPrivilege.Add(out.Tables[i].Name)
		}
	}
	return nil
}

// SafeRowCounts is false for MySQL/MariaDB (user decision, D-153): row
// stats are information_schema estimates only and no statement reads a
// user table or view, so a database owner's view swaps, SQL SECURITY
// INVOKER views or stored functions are never evaluated by the runner.
func (c *myConn) SafeRowCounts() bool { return false }

// CountRows always refuses (see SafeRowCounts).
func (c *myConn) CountRows(context.Context, string, Table, time.Duration) (int64, error) {
	return 0, ErrCountUnsafe
}

func (c *myConn) GlobalCounts(context.Context) (int64, int64, error) { return 0, 0, ErrNoStats }

func (c *myConn) EndStats(ctx context.Context) error {
	if _, err := c.c.ExecContext(ctx, "ROLLBACK"); err != nil {
		return queryFailure(ctx, err, myReason)
	}
	return nil
}
