package dbprobe

import (
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Schema-version capture (D-210). The version query runs on the stats
// session, inside the statistics transaction (READ ONLY; PostgreSQL: the
// snapshot the dump uses), after the catalog reads:
//
//   - PostgreSQL: only on a session with restrict_nonsystem_relation_kind
//     in effect (no view or foreign table can be read, so no code of the
//     database owner runs; D-152), inside a savepoint with SET LOCAL
//     statement_timeout, lock_timeout and
//     idle_in_transaction_session_timeout, rolled back afterwards (the
//     stats transaction keeps its own settings and stays usable whatever
//     the query did). A preset first checks in pg_catalog that its table
//     is a plain or partitioned table the role may SELECT and that every
//     column it reads has a built-in pg_catalog type, then runs its
//     constant, schema-qualified SQL on the quoted schema.table. A custom
//     query runs wrapped as SELECT * FROM (<query>) LIMIT 1 with the
//     session's search_path (pg_catalog, pg_temp): its tables must be
//     schema-qualified. A preset first takes an ACCESS SHARE lock on its
//     table (lock_timeout), so its definition cannot change between the
//     checks and the read; because the catalog check reads the snapshot,
//     the read statement's result types are also checked against the
//     current definition (a Describe of the raw columns) before it runs.
//   - MySQL/MariaDB: inside the READ ONLY consistent-snapshot transaction
//     with max_execution_time (MySQL) or max_statement_time (MariaDB) and
//     lock_wait_timeout; the preset table must be a BASE TABLE with
//     built-in column types (information_schema); a custom query runs
//     after USE of the dumped database, wrapped as SELECT * FROM (<query>)
//     AS bekup_version LIMIT 1 (the driver refuses multiple statements).
//     information_schema reads the current definition, not the snapshot:
//     a preset's table is checked again after the read and the value is
//     discarded when it changed; the window between the check and the
//     read (where a table swapped for a view could run its definer's
//     code) is a residual risk documented in D-210.
//
// Memory is bounded: presets truncate their output in SQL
// (schemaversion.MaxRawValueBytes), PostgreSQL probe sessions refuse
// protocol messages over pgMaxMessageBytes and MySQL/MariaDB sessions read
// at most myVersionReadBytes during a capture. Only the first row is read
// (a custom query: its first column); the result is checked by
// schemaversion.CleanValue. A missing table (or schema / database) is
// StatusNone, every other failure StatusUnavailable. Server messages and
// the result never reach a log: VersionResult.Detail is a fixed
// classification.

// VersionQuery is a validated schema-version query (validate.Version).
type VersionQuery struct {
	// Preset is set for a preset query.
	Preset *schemaversion.Preset
	// Custom is the validated custom statement body.
	Custom string
	// Schema is the PostgreSQL schema of the preset table.
	Schema string
}

// VersionResult is the outcome of a capture.
type VersionResult struct {
	// Value is set only with StatusOK.
	Value  string
	Status schemaversion.Status
	// Detail classifies a failure for the runner log ("sqlstate=42501",
	// "layout", "unsafe server", ...): never a server message or value.
	Detail string
	// SessionLost: the statistics transaction is no longer usable after
	// the capture (the savepoint could not be rolled back or the
	// connection broke), so its exported snapshot must not be handed to
	// the dump (PostgreSQL).
	SessionLost bool
}

func versionFailure(st schemaversion.Status, detail string) VersionResult {
	return VersionResult{Status: st, Detail: detail}
}

// versionValue formats and checks the first row's columns. A column of
// more than schemaversion.MaxRawValueBytes bytes is invalid (the presets
// truncate one byte above that in SQL).
func versionValue(q VersionQuery, cols []*string) VersionResult {
	for _, c := range cols {
		if c != nil && len(*c) > schemaversion.MaxRawValueBytes {
			return versionFailure(schemaversion.StatusInvalid, "value")
		}
	}
	var (
		v  string
		ok bool
	)
	if q.Preset != nil {
		v, ok = q.Preset.Format(cols)
	} else if len(cols) > 0 && cols[0] != nil {
		v, ok = *cols[0], true
	}
	if !ok {
		return versionFailure(schemaversion.StatusNone, "null")
	}
	clean, st := schemaversion.CleanValue(v)
	if st != schemaversion.StatusOK {
		return versionFailure(st, "value")
	}
	return VersionResult{Value: clean, Status: schemaversion.StatusOK}
}
