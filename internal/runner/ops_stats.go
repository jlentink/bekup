package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Source statistics of a dump (task 3.10, D-141, D-151). For PostgreSQL the
// runner opens a Go-driver session to the dumped database (same target
// allowlist, pin pre-flight and TLS rules as every other check), starts a
// REPEATABLE READ READ ONLY transaction, exports its snapshot, reads the
// catalog and counts rows inside it, sends the stats and then runs
// pg_dump --snapshot=<id> while the transaction stays open; it is closed
// only after the dump container has finished, so pg_dump has long imported
// the snapshot by then. For MySQL/MariaDB the stats are information_schema
// estimates and grants only, at both levels (user decision, D-153): no
// statement reads a user table or view; every table is approximate.

const (
	// statsHelpers is the number of extra connections for deep counts
	// (each imports the snapshot), used from statsHelperMinTables tables.
	statsHelpers         = 2
	statsHelperMinTables = 16
	// maxStatsTableBytes bounds the table entries of one stats message
	// (the gRPC message cap is ~1 MiB).
	maxStatsTableBytes = 768 << 10
	// maxStatsWarnings bounds the warnings of one stats message.
	maxStatsWarnings = 32
	// statsEndTimeout bounds closing the statistics transaction.
	statsEndTimeout = 10 * time.Second
)

// Stats warning codes (stable strings; StatsWarning.code).
const (
	statsWarnPrivileges  = "insufficient_privileges"
	statsWarnNonTx       = "non_transactional_tables"
	statsWarnTimeout     = "row_count_timeout"
	statsWarnBudget      = "stats_budget_exceeded"
	statsWarnCountFailed = "row_count_failed"
	statsWarnPartial     = "stats_partial"
	// warnStatsUnavailable is a DumpResult warning: statistics were
	// requested but could not be captured (the dump still ran).
	warnStatsUnavailable = "stats_unavailable"
)

// statsCapture is an open statistics session.
type statsCapture struct {
	// snapshot is the exported PostgreSQL snapshot ("" otherwise).
	snapshot string
	msg      *runnerpb.DumpStats
	src      dbprobe.StatsSource
	done     func()
	once     sync.Once
}

// close ends the statistics transaction and the session (idempotent).
// It runs even when ctx is canceled (bounded by statsEndTimeout).
func (s *statsCapture) close(ctx context.Context) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.src != nil {
			ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statsEndTimeout)
			discardErr(s.src.EndStats(ectx))
			cancel()
		}
		if s.done != nil {
			s.done()
		}
	})
}

// statsSession opens a Go-driver session to db with statistics support.
func (o *operations) statsSession(ctx context.Context, t *validate.Target, rt *runnerpb.Target, db string) (dbprobe.StatsSource, func(), error) {
	conn, _, done, err := o.session(ctx, t, rt, db)
	if err != nil {
		done()
		return nil, func() {}, err
	}
	src, ok := conn.(dbprobe.StatsSource)
	if !ok {
		done()
		return nil, func() {}, dbprobe.ErrNoStats
	}
	return src, done, nil
}

// captureStats takes the statistics of a database dump. It returns nil
// when they cannot be captured (logged; the dump runs without them). The
// caller must close the capture after the dump.
func (o *operations) captureStats(ctx context.Context, d *validate.DatabaseOp, rt *runnerpb.Target) *statsCapture {
	src, done, err := o.statsSession(ctx, d.Target, rt, d.Database)
	if err != nil {
		o.logStatsFailure(ctx, "session", err)
		return nil
	}
	sc := &statsCapture{src: src, done: done}
	pg := d.Kind == engine.KindPostgres
	snap, err := src.BeginStats(ctx, pg)
	if err != nil {
		sc.src = nil // nothing to end
		sc.close(ctx)
		o.logStatsFailure(ctx, "begin", err)
		return nil
	}
	sc.snapshot = snap
	bctx, cancel := context.WithTimeout(ctx, d.Stats.Budget)
	defer cancel()
	cat, err := src.Catalog(bctx, d.Database)
	if err != nil {
		sc.close(ctx)
		o.logStatsFailure(ctx, "catalog", err)
		return nil
	}
	// MySQL/MariaDB: estimates only, never a statement on a user table
	// (user decision, D-153); deep behaves like standard there.
	deep := d.Stats.Deep && pg
	var counts []countResult
	switch {
	case deep && !src.SafeRowCounts():
		// The server cannot keep a count from running database-owner
		// code (PostgreSQL without restrict_nonsystem_relation_kind):
		// no relation is named, every table stays unknown (D-152).
		o.log.LogAttrs(ctx, slog.LevelWarn, "runner_stats_counts_disabled", slog.String("reason", "server lacks restrict_nonsystem_relation_kind"))
		counts = make([]countResult, len(cat.Tables))
		for i, t := range cat.Tables {
			counts[i] = countResult{done: true, state: countFailed}
			if !t.Readable {
				counts[i].state = countDenied
			}
		}
	case deep:
		counts = o.countTables(ctx, bctx, sc, d, rt, cat.Tables)
	}
	sc.msg = buildStats(d.Kind, deep, cat, counts)
	sc.msg.SameSnapshot = pg && snap != ""
	if d.Version != nil {
		// Same transaction (PostgreSQL: the dump's snapshot), D-210.
		if r := o.captureVersion(ctx, sc.src, d, sc.msg); r.SessionLost && pg {
			// The statistics transaction (and its exported snapshot) is
			// gone: the dump takes its own snapshot instead of failing on
			// the import, and the stats no longer claim the same one.
			sc.msg.SameSnapshot = false
			sc.close(ctx)
			sc.snapshot = ""
		}
	}
	if !pg {
		// The dump tool takes its own snapshot: nothing to hold.
		sc.close(ctx)
		sc.snapshot = ""
	}
	return sc
}

// captureVersion reads the schema version into the stats message (D-210).
// The log line carries the query kind, the status and a fixed
// classification only, never the query, a server message or the value.
func (o *operations) captureVersion(ctx context.Context, src dbprobe.StatsSource, d *validate.DatabaseOp, msg *runnerpb.DumpStats) dbprobe.VersionResult {
	r := src.SchemaVersion(ctx, d.Database, dbprobe.VersionQuery{Preset: d.Version.Preset, Custom: d.Version.Custom, Schema: d.Version.Schema})
	msg.VersionStatus = versionStatus(r.Status)
	if msg.VersionStatus == runnerpb.VersionStatus_VERSION_STATUS_OK {
		msg.SchemaVersion = r.Value
	}
	query := "custom"
	if d.Version.Preset != nil {
		query = "preset:" + d.Version.Preset.Name
	}
	level := slog.LevelInfo
	if r.Status != schemaversion.StatusOK && r.Status != schemaversion.StatusNone {
		level = slog.LevelWarn
	}
	o.log.LogAttrs(ctx, level, "runner_schema_version", slog.String("query", query),
		slog.String("status", string(r.Status)), slog.String("detail", r.Detail), slog.Bool("session_lost", r.SessionLost))
	return r
}

// versionStatus maps a capture status to the API enum (unknown:
// unavailable).
func versionStatus(s schemaversion.Status) runnerpb.VersionStatus {
	switch s {
	case schemaversion.StatusOK:
		return runnerpb.VersionStatus_VERSION_STATUS_OK
	case schemaversion.StatusNone:
		return runnerpb.VersionStatus_VERSION_STATUS_NONE
	case schemaversion.StatusInvalid:
		return runnerpb.VersionStatus_VERSION_STATUS_INVALID
	default:
		return runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE
	}
}

// captureGlobalStats counts the roles and tablespaces before a globals
// dump (nil when that fails).
func (o *operations) captureGlobalStats(ctx context.Context, t *validate.Target, rt *runnerpb.Target, st *validate.Stats) *runnerpb.DumpStats {
	src, done, err := o.statsSession(ctx, t, rt, "")
	if err != nil {
		o.logStatsFailure(ctx, "session", err)
		return nil
	}
	defer done()
	bctx, cancel := context.WithTimeout(ctx, st.Budget)
	defer cancel()
	roles, ts, err := src.GlobalCounts(bctx)
	if err != nil {
		o.logStatsFailure(ctx, "globals", err)
		return nil
	}
	level := runnerpb.StatsLevel_STATS_LEVEL_STANDARD
	if st.Deep {
		level = runnerpb.StatsLevel_STATS_LEVEL_DEEP
	}
	return &runnerpb.DumpStats{Level: level, Objects: &runnerpb.ObjectCounts{Roles: clampU32(roles), Tablespaces: clampU32(ts)}}
}

// logStatsFailure logs why statistics are missing (classification only).
func (o *operations) logStatsFailure(ctx context.Context, step string, err error) {
	attrs := []slog.Attr{slog.String("step", step)}
	var pe *dbprobe.Error
	if errors.As(err, &pe) {
		attrs = append(attrs, slog.String("reason", string(pe.Reason)), slog.String("detail", pe.Detail))
	}
	o.log.LogAttrs(ctx, slog.LevelWarn, "runner_stats_unavailable", attrs...)
}

// countResult is the outcome of one exact count.
type countResult struct {
	done  bool
	rows  int64
	state countState
}

type countState int

const (
	countPending countState = iota
	countOK
	countTimeout
	countDenied
	countFailed
	countBudget
)

// countTables counts every readable table exactly, on the statistics
// connection plus up to statsHelpers connections that import the same
// snapshot (PostgreSQL) or take their own consistent snapshot
// (MySQL/MariaDB). Each count is limited to min(per-table timeout,
// remaining budget); tables not reached within the budget stay unknown.
func (o *operations) countTables(ctx, bctx context.Context, sc *statsCapture, d *validate.DatabaseOp, rt *runnerpb.Target, tables []dbprobe.Table) []countResult {
	res := make([]countResult, len(tables))
	work := make(chan int, len(tables))
	for i, t := range tables {
		if t.Readable {
			work <- i
		} else {
			res[i] = countResult{done: true, state: countDenied}
		}
	}
	close(work)
	deadline, _ := bctx.Deadline()

	worker := func(src dbprobe.StatsSource) {
		for i := range work {
			left := time.Until(deadline)
			if bctx.Err() != nil || left <= 0 {
				res[i] = countResult{done: true, state: countBudget}
				continue
			}
			n, err := src.CountRows(bctx, d.Database, tables[i], min(d.Stats.PerTable, left))
			switch {
			case err == nil:
				res[i] = countResult{done: true, rows: n, state: countOK}
			case errors.Is(err, dbprobe.ErrCountTimeout):
				res[i] = countResult{done: true, state: countTimeout}
			case dbprobe.ReasonOf(err) == runnerpb.ReasonPermission:
				res[i] = countResult{done: true, state: countDenied}
			case errors.Is(err, dbprobe.ErrCountUnsafe):
				res[i] = countResult{done: true, state: countFailed}
			case bctx.Err() != nil:
				res[i] = countResult{done: true, state: countBudget}
			default:
				// The connection is unusable: this worker stops; the
				// others (or the budget) take the rest.
				res[i] = countResult{done: true, state: countFailed}
				return
			}
		}
	}

	var wg sync.WaitGroup
	if d.Stats.Deep && len(work) >= statsHelperMinTables {
		for range statsHelpers {
			hsrc, hdone, err := o.statsSession(bctx, d.Target, rt, d.Database)
			if err != nil {
				o.logStatsFailure(ctx, "helper", err)
				break
			}
			if err := hsrc.JoinStats(bctx, sc.snapshot); err != nil {
				hdone()
				o.logStatsFailure(ctx, "helper", err)
				break
			}
			wg.Go(func() {
				defer hdone()
				defer func() {
					ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statsEndTimeout)
					discardErr(hsrc.EndStats(ectx))
					cancel()
				}()
				worker(hsrc)
			})
		}
	}
	worker(sc.src)
	wg.Wait()
	// Tables left in the queue by stopped workers.
	for i := range res {
		if !res[i].done {
			res[i] = countResult{done: true, state: countFailed}
		}
	}
	return res
}

// buildStats assembles the stats message: object counts, the bounded table
// list (exact counts for deep, estimates for standard) and the warnings.
func buildStats(kind engine.Kind, deep bool, cat *dbprobe.Catalog, counts []countResult) *runnerpb.DumpStats {
	level := runnerpb.StatsLevel_STATS_LEVEL_STANDARD
	if deep {
		level = runnerpb.StatsLevel_STATS_LEVEL_DEEP
	}
	ob := cat.Objects
	msg := &runnerpb.DumpStats{
		Level: level,
		Objects: &runnerpb.ObjectCounts{
			Schemas: clampU32(ob.Schemas), Tables: clampU32(ob.Tables), Views: clampU32(ob.Views),
			MaterializedViews: clampU32(ob.MaterializedViews), Sequences: clampU32(ob.Sequences),
			Functions: clampU32(ob.Functions), Procedures: clampU32(ob.Procedures), Triggers: clampU32(ob.Triggers),
			Extensions: clampU32(ob.Extensions), Events: clampU32(ob.Events),
		},
		TablesTotal: clampU32(int64(cat.TablesTotal)),
	}
	approximate := kind != engine.KindPostgres
	priv := cat.NoPrivilege
	var timeouts, budget, failed dbprobe.Names
	size := 0
	for i, t := range cat.Tables {
		ts := &runnerpb.TableStats{Schema: t.Schema, Name: t.Name, Approximate: approximate, Count: runnerpb.RowCount_ROW_COUNT_UNKNOWN}
		switch {
		case deep && i < len(counts):
			switch counts[i].state {
			case countOK:
				ts.Rows, ts.Count = counts[i].rows, runnerpb.RowCount_ROW_COUNT_EXACT
			case countTimeout:
				timeouts.Add(t.Qualified())
			case countBudget:
				budget.Add(t.Qualified())
			case countDenied:
				// PostgreSQL lists unreadable tables from the catalog;
				// MySQL/MariaDB learn it from the count.
				if t.Readable {
					priv.Add(t.Qualified())
				}
			default:
				failed.Add(t.Qualified())
			}
		case !deep && t.Readable && t.Estimate >= 0:
			ts.Rows, ts.Count = t.Estimate, runnerpb.RowCount_ROW_COUNT_ESTIMATE
		}
		size += len(t.Schema) + len(t.Name) + 24
		if size > maxStatsTableBytes {
			msg.TablesTruncated = true
			continue
		}
		msg.Tables = append(msg.Tables, ts)
	}
	if len(msg.Tables) < cat.TablesTotal {
		msg.TablesTruncated = true
	}
	add := func(code string, n dbprobe.Names) {
		if n.Count == 0 || len(msg.Warnings) >= maxStatsWarnings {
			return
		}
		msg.Warnings = append(msg.Warnings, &runnerpb.StatsWarning{
			Code: code, Count: clampU32(int64(n.Count)), Objects: n.Objects, ObjectsTruncated: n.Count > len(n.Objects),
		})
	}
	add(statsWarnPrivileges, priv)
	add(statsWarnNonTx, cat.NonTransactional)
	add(statsWarnTimeout, timeouts)
	add(statsWarnBudget, budget)
	add(statsWarnCountFailed, failed)
	if msg.TablesTruncated {
		add(statsWarnPartial, dbprobe.Names{Count: cat.TablesTotal - len(msg.Tables)})
	}
	return msg
}

func clampU32(n int64) uint32 {
	switch {
	case n < 0:
		return 0
	case n > int64(^uint32(0)):
		return ^uint32(0)
	default:
		return uint32(n)
	}
}

// statsAudit are the audit attributes of a capture (counts only).
func statsAudit(m *runnerpb.DumpStats) []slog.Attr {
	return []slog.Attr{
		slog.String("stats_level", m.GetLevel().String()),
		slog.Bool("same_snapshot", m.GetSameSnapshot()),
		slog.Uint64("tables_total", uint64(m.GetTablesTotal())),
		slog.Int("stats_warnings", len(m.GetWarnings())),
		slog.String("version_status", m.GetVersionStatus().String()),
	}
}
