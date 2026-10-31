package dbprobe

import (
	"context"
	"errors"
	"time"
)

// Source statistics (task 3.10, D-141): the catalog reads and row counts
// the runner takes before a dump. Every query is read-only and runs inside
// one REPEATABLE READ READ ONLY transaction (PostgreSQL: the exported
// snapshot pg_dump --snapshot reuses; MySQL/MariaDB: a consistent
// snapshot started right before the dump). Exact row counts exist for
// PostgreSQL only; MySQL/MariaDB stats come from information_schema
// alone and never read a user table or view (D-153). Only names and
// counts leave this package, never data values.

// Statistics limits.
const (
	// MaxStatsTables bounds the tables listed (and counted) per database;
	// TablesTotal still reports the full number.
	MaxStatsTables = 10000
	// MaxWarningObjects bounds the object names listed per warning.
	MaxWarningObjects = 50
	// minCountTimeout is the smallest server-side limit of one count.
	minCountTimeout = 100 * time.Millisecond
)

// ErrCountTimeout reports a row count stopped by its time limit.
var ErrCountTimeout = errors.New("dbprobe: row count timed out")

// ErrCountUnsafe reports a connection that refuses row counts because the
// server cannot block views and foreign tables in them (PostgreSQL without
// restrict_nonsystem_relation_kind, D-152).
var ErrCountUnsafe = errors.New("dbprobe: row counts are disabled on this server")

// ErrNoStats reports a connection without statistics support.
var ErrNoStats = errors.New("dbprobe: statistics are not supported")

// ObjectCounts are the objects of a database outside the system schemas
// (extension members excluded), or the cluster globals.
type ObjectCounts struct {
	Schemas, Tables, Views, MaterializedViews, Sequences int64
	Functions, Procedures, Triggers, Extensions, Events  int64
	Roles, Tablespaces                                   int64
}

// Table is one table of the catalog.
type Table struct {
	// Schema is empty for MySQL/MariaDB.
	Schema string
	Name   string
	// Estimate is the catalog row estimate, -1 when there is none.
	Estimate int64
	// Readable: the role may SELECT it (PostgreSQL from the catalog;
	// MySQL/MariaDB from the information_schema grants, D-153).
	Readable bool
	// Transactional is false for MyISAM/Aria/MEMORY/CSV/... tables.
	Transactional bool
}

// Qualified is the table's display name ("schema.table" or "table").
func (t Table) Qualified() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// Names is a bounded list of affected objects.
type Names struct {
	Count   int
	Objects []string
}

// Add records one object (listing at most MaxWarningObjects).
func (n *Names) Add(name string) {
	n.Count++
	if len(n.Objects) < MaxWarningObjects {
		n.Objects = append(n.Objects, name)
	}
}

// Catalog is what the catalog reads found out about one database.
type Catalog struct {
	Objects ObjectCounts
	// Tables holds at most MaxStatsTables base tables with data, sorted by
	// schema and name (byte order).
	Tables      []Table
	TablesTotal int
	// NoPrivilege lists objects the role cannot read (tables and sequences
	// without SELECT, schemas without USAGE).
	NoPrivilege Names
	// NonTransactional lists tables of engines without MVCC snapshots
	// (MySQL/MariaDB).
	NonTransactional Names
}

// StatsSource is implemented by the PostgreSQL and MySQL/MariaDB
// connections. A connection with an open statistics transaction is used by
// one goroutine.
type StatsSource interface {
	// BeginStats opens the READ ONLY repeatable-read transaction. With
	// export (PostgreSQL) it returns the pg_export_snapshot() id; the
	// snapshot stays importable until EndStats.
	BeginStats(ctx context.Context, export bool) (snapshot string, err error)
	// JoinStats opens the transaction of a helper connection: PostgreSQL
	// imports snapshot (SET TRANSACTION SNAPSHOT), MySQL/MariaDB start
	// their own consistent snapshot.
	JoinStats(ctx context.Context, snapshot string) error
	// Catalog reads the object counts, the bounded table list and the
	// completeness checks of db.
	Catalog(ctx context.Context, db string) (*Catalog, error)
	// CountRows counts t exactly inside the transaction; timeout is
	// enforced by the server (ErrCountTimeout). A missing privilege is an
	// *Error with ReasonPermission. The transaction stays usable after
	// either.
	CountRows(ctx context.Context, db string, t Table, timeout time.Duration) (int64, error)
	// GlobalCounts counts the cluster's roles (without the predefined pg_*
	// roles) and tablespaces (without pg_default and pg_global).
	GlobalCounts(ctx context.Context) (roles, tablespaces int64, err error)
	// SafeRowCounts reports whether CountRows may run: a row count names a
	// relation, which must not be able to run code of the database owner
	// (PostgreSQL: restrict_nonsystem_relation_kind in effect).
	SafeRowCounts() bool
	// SchemaVersion reads the schema version of db inside the
	// transaction (D-210, see version.go). It never fails: problems are
	// reported as the result's status. The transaction stays usable.
	SchemaVersion(ctx context.Context, db string, q VersionQuery) VersionResult
	// EndStats ends the transaction (rollback).
	EndStats(ctx context.Context) error
}

// countTimeoutMillis is the server-side limit of one count.
func countTimeoutMillis(d time.Duration) int64 {
	return max(d, minCountTimeout).Milliseconds()
}
