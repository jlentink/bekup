package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

// Completeness evidence (task 3.10, D-141, D-151). The backup worker opens a
// StatsSession right before each dump (database or globals) and closes it
// after the dump. RunnerStats asks the runner to capture the source
// statistics as part of the Dump call (PostgreSQL: inside the exported
// snapshot pg_dump --snapshot reuses), receives them in the stream, turns
// them into a canonical stats object (integers and strings only, D-144)
// for the signed manifest and the backup_stats table, and turns the
// runner's completeness warnings into item warnings.

// StatsInput describes the dump a session belongs to.
type StatsInput struct {
	RunID      uuid.UUID
	JobID      uuid.UUID
	ItemID     uuid.UUID
	Engine     engine.Kind
	VersionKey string
	Kind       pipeline.EntryKind
	// Database is empty for the globals.
	Database string
	// Deep: the job's verification level is "deep" (exact row counts).
	Deep bool
}

// StatsResult is the evidence of one dump.
type StatsResult struct {
	// Stats is a canonical JSON object (integers and strings only, D-144)
	// or nil.
	Stats    json.RawMessage
	Warnings []runstate.Warning
}

// DumpOutcome is what the worker saw of a dump.
type DumpOutcome struct {
	// OK: a complete, verified dump.
	OK bool
	// Stats is the runner's stats message (nil when none arrived).
	Stats *runnerpb.DumpStats
	// Result is the runner's dump result (nil when none arrived).
	Result *runnerpb.DumpResult
}

// StatsSession spans one dump.
type StatsSession interface {
	// PrepareDump adjusts the dump request (the stats options). Exactly
	// one of the requests is set.
	PrepareDump(db *runnerpb.DumpRequest, globals *runnerpb.DumpGlobalsRequest)
	// End is called after the dump.
	End(ctx context.Context, d DumpOutcome) (StatsResult, error)
}

// StatsCollector opens a session per dump. A Begin error is recorded as a
// warning; the dump still runs.
type StatsCollector interface {
	Begin(ctx context.Context, in StatsInput) (StatsSession, error)
}

// NoStats records nothing (tests, or a worker without evidence).
type NoStats struct{}

// Begin implements StatsCollector.
func (NoStats) Begin(context.Context, StatsInput) (StatsSession, error) { return noStats{}, nil }

type noStats struct{}

func (noStats) PrepareDump(*runnerpb.DumpRequest, *runnerpb.DumpGlobalsRequest) {}

func (noStats) End(context.Context, DumpOutcome) (StatsResult, error) { return StatsResult{}, nil }

// StatsStore stores the evidence of an item (sqlc.Queries).
type StatsStore interface {
	InsertBackupStats(ctx context.Context, arg sqlc.InsertBackupStatsParams) error
}

// RunnerStats is the StatsCollector of the backup worker.
type RunnerStats struct {
	// Store records backup_stats rows (nil: not stored).
	Store StatsStore
	// PerTableTimeout and Budget override the runner defaults (0).
	PerTableTimeout time.Duration
	Budget          time.Duration
	Logger          *slog.Logger
}

// Begin implements StatsCollector.
func (r RunnerStats) Begin(_ context.Context, in StatsInput) (StatsSession, error) {
	return &runnerStatsSession{r: r, in: in}, nil
}

type runnerStatsSession struct {
	r  RunnerStats
	in StatsInput
}

func (s *runnerStatsSession) options() *runnerpb.StatsOptions {
	level := runnerpb.StatsLevel_STATS_LEVEL_STANDARD
	if s.in.Deep {
		level = runnerpb.StatsLevel_STATS_LEVEL_DEEP
	}
	return &runnerpb.StatsOptions{
		Enabled: true, Level: level,
		PerTableTimeoutMs: durationMillis(s.r.PerTableTimeout),
		BudgetMs:          durationMillis(s.r.Budget),
	}
}

func durationMillis(d time.Duration) uint32 {
	ms := d.Milliseconds()
	switch {
	case ms <= 0:
		return 0
	case ms > int64(^uint32(0)):
		return ^uint32(0)
	default:
		return uint32(ms)
	}
}

func (s *runnerStatsSession) PrepareDump(db *runnerpb.DumpRequest, globals *runnerpb.DumpGlobalsRequest) {
	if db != nil {
		db.Stats = s.options()
	}
	if globals != nil {
		globals.Stats = s.options()
	}
}

func (s *runnerStatsSession) End(ctx context.Context, d DumpOutcome) (StatsResult, error) {
	if d.Stats == nil {
		// A missing stats message is reported by the runner's
		// stats_unavailable result warning.
		return StatsResult{}, nil
	}
	ev, err := encodeStats(s.in, d.Stats, d.Result)
	if err != nil {
		return StatsResult{Warnings: []runstate.Warning{{Code: "stats_invalid", Message: "the runner's statistics were malformed and were discarded", Database: s.in.Database}}}, nil
	}
	if !d.OK {
		// A failed or unverified dump keeps its validated completeness
		// warnings (they often explain the failure, e.g. missing
		// privileges), but gets no evidence: no backup_stats row and no
		// stats in the manifest.
		return StatsResult{Warnings: ev.warnings}, nil
	}
	res := StatsResult{Stats: ev.canonical, Warnings: ev.warnings}
	if s.r.Store != nil && s.in.ItemID != uuid.Nil {
		level := sqlc.JobVerificationStandard
		if s.in.Deep {
			level = sqlc.JobVerificationDeep
		}
		fctx, cancel := finalCtx(ctx)
		defer cancel()
		if err := s.r.Store.InsertBackupStats(fctx, sqlc.InsertBackupStatsParams{
			RunItemID: s.in.ItemID, RunID: s.in.RunID, JobID: s.in.JobID, Level: level,
			Consistency: ev.consistency, TablesTotal: int32(min(d.Stats.GetTablesTotal(), 1<<31-1)),
			WarningCount: int32(len(ev.warnings)), //nolint:gosec // at most maxStatsWarnings
			Stats:        ev.canonical,
		}); err != nil {
			if s.r.Logger != nil {
				s.r.Logger.ErrorContext(ctx, "recording backup statistics failed", slog.Any("error", err))
			}
			res.Warnings = append(res.Warnings, runstate.Warning{Code: "stats_not_recorded", Message: "the statistics are in the manifest but could not be recorded in the database", Database: s.in.Database})
		}
	}
	return res, nil
}

// --- encoding ---------------------------------------------------------------

// Bounds of the stats a worker accepts from the runner (the runner applies
// the same or tighter ones).
const (
	maxStatsTables         = 10000
	maxStatsWarnings       = 32
	maxStatsWarningObjects = 50
	maxStatsNameBytes      = 256
	// maxNamesInMessage bounds the object names quoted in an item warning
	// (the full bounded list is in the stats object).
	maxNamesInMessage = 8
)

// Stats warning codes the worker knows (runner codes plus the globals
// fallback).
var statsWarningText = map[string]string{
	"insufficient_privileges":  "the backup role cannot read %d object(s) (missing SELECT on tables or sequences, or USAGE on schemas); the dump may fail or be incomplete",
	"non_transactional_tables": "%d non-transactional table(s) (MyISAM, Aria, MEMORY, ...) are dumped without a consistent snapshot; the dump may be inconsistent",
	"row_count_timeout":        "the exact row count of %d table(s) exceeded the per-table time limit; their counts are unknown",
	"stats_budget_exceeded":    "the statistics time budget ran out before %d table(s) were counted; their counts are unknown",
	"row_count_failed":         "%d table(s) could not be counted; their counts are unknown",
	"stats_partial":            "%d table(s) exceed the statistics table limit and are not itemized",
	"roles_without_passwords":  "the globals were dumped without role passwords (%d role(s)); restored roles need new passwords",
	// databases_excluded is a run notice (informational), not a warning.
	"databases_excluded": "%d database(s) on the server are excluded by the job and not backed up",
}

type evidence struct {
	canonical   json.RawMessage
	consistency string
	warnings    []runstate.Warning
}

// encodeStats validates the runner's stats and builds the canonical stats
// object of an item:
//
//	{"level":"deep","consistency":"snapshot","objects":{"tables":10,...},
//	 "tables":{"public.users":{"rows":123,"count":"exact"}},
//	 "tables_total":10,"tables_truncated":1,
//	 "warnings":[{"code":"...","count":2,"objects":["public.x"],"truncated":1}]}
//
// Table keys are pipeline.EncodeName(schema)+"."+pipeline.EncodeName(name)
// (MySQL/MariaDB: the encoded name only). "count" is exact, estimate or
// unknown (no "rows"); "approximate":1 marks MySQL/MariaDB tables. Flags
// are the integer 1 and omitted when false: canonical JSON allows integers
// and strings only.
func encodeStats(in StatsInput, st *runnerpb.DumpStats, res *runnerpb.DumpResult) (evidence, error) {
	var ev evidence
	if len(st.GetTables()) > maxStatsTables || len(st.GetWarnings()) > maxStatsWarnings {
		return ev, errors.New("jobs: stats exceed the bounds")
	}
	out := map[string]any{}
	switch st.GetLevel() {
	case runnerpb.StatsLevel_STATS_LEVEL_DEEP:
		out["level"] = "deep"
	case runnerpb.StatsLevel_STATS_LEVEL_STANDARD:
		out["level"] = "standard"
	default:
		return ev, fmt.Errorf("jobs: stats level %v", st.GetLevel())
	}
	ev.consistency = "approximate"
	if st.GetSameSnapshot() && in.Engine == engine.KindPostgres && in.Kind == pipeline.KindDatabase {
		ev.consistency = "snapshot"
	}
	out["consistency"] = ev.consistency
	out["objects"] = objectCounts(in, st.GetObjects())
	if in.Kind == pipeline.KindDatabase {
		tables := make(map[string]any, len(st.GetTables()))
		for _, t := range st.GetTables() {
			if !validStatsName(t.GetName()) || (t.GetSchema() != "" && !validStatsName(t.GetSchema())) {
				return ev, errors.New("jobs: invalid table name")
			}
			key := pipeline.EncodeName(t.GetName())
			if t.GetSchema() != "" {
				key = pipeline.EncodeName(t.GetSchema()) + "." + key
			}
			if _, dup := tables[key]; dup {
				return ev, errors.New("jobs: duplicate table")
			}
			e := map[string]any{}
			switch t.GetCount() {
			case runnerpb.RowCount_ROW_COUNT_EXACT:
				e["count"], e["rows"] = "exact", t.GetRows()
			case runnerpb.RowCount_ROW_COUNT_ESTIMATE:
				e["count"], e["rows"] = "estimate", t.GetRows()
			case runnerpb.RowCount_ROW_COUNT_UNKNOWN:
				e["count"] = "unknown"
			default:
				return ev, fmt.Errorf("jobs: row count kind %v", t.GetCount())
			}
			if t.GetRows() < 0 {
				return ev, errors.New("jobs: negative row count")
			}
			if t.GetApproximate() {
				e["approximate"] = 1
			}
			tables[key] = e
		}
		out["tables"] = tables
		out["tables_total"] = int64(st.GetTablesTotal())
		if st.GetTablesTruncated() {
			out["tables_truncated"] = 1
		}
	}
	var ws []any
	for _, w := range st.GetWarnings() {
		if !codeShape.MatchString(w.GetCode()) || len(w.GetObjects()) > maxStatsWarningObjects {
			return ev, errors.New("jobs: invalid stats warning")
		}
		for _, o := range w.GetObjects() {
			if !validStatsName(o) {
				return ev, errors.New("jobs: invalid object name")
			}
		}
		ws = append(ws, warningObject(w.GetCode(), int64(w.GetCount()), w.GetObjects(), w.GetObjectsTruncated()))
		ev.warnings = append(ev.warnings, statsWarning(in.Database, w.GetCode(), int64(w.GetCount()), w.GetObjects()))
	}
	// The PostgreSQL globals fallback joins the same warnings model (the
	// item warning itself comes from the runner's result code).
	if in.Kind == pipeline.KindGlobals && res.GetRolesWithoutPasswords() {
		roles := int64(st.GetObjects().GetRoles())
		ws = append(ws, warningObject("roles_without_passwords", roles, nil, false))
	}
	if len(ws) > 0 {
		out["warnings"] = ws
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ev, fmt.Errorf("jobs: encode stats: %w", err)
	}
	if ev.canonical, err = pipeline.Canonicalize(b); err != nil {
		return ev, fmt.Errorf("jobs: canonical stats: %w", err)
	}
	return ev, nil
}

func objectCounts(in StatsInput, o *runnerpb.ObjectCounts) map[string]any {
	if in.Kind == pipeline.KindGlobals {
		return map[string]any{"roles": int64(o.GetRoles()), "tablespaces": int64(o.GetTablespaces())}
	}
	m := map[string]any{
		"schemas": int64(o.GetSchemas()), "tables": int64(o.GetTables()), "views": int64(o.GetViews()),
		"sequences": int64(o.GetSequences()), "functions": int64(o.GetFunctions()), "procedures": int64(o.GetProcedures()),
		"triggers": int64(o.GetTriggers()),
	}
	if in.Engine == engine.KindPostgres {
		m["materialized_views"], m["extensions"] = int64(o.GetMaterializedViews()), int64(o.GetExtensions())
	} else {
		m["events"] = int64(o.GetEvents())
	}
	return m
}

func warningObject(code string, count int64, objects []string, truncated bool) map[string]any {
	w := map[string]any{"code": code, "count": count}
	if len(objects) > 0 {
		w["objects"] = objects
	}
	if truncated {
		w["truncated"] = 1
	}
	return w
}

// statsWarning is the item warning of a stats warning: a fixed text per
// code plus a few sanitized object names.
func statsWarning(db, code string, count int64, objects []string) runstate.Warning {
	text, ok := statsWarningText[code]
	if !ok {
		text = "the runner reported a completeness warning for %d object(s)"
	}
	msg := fmt.Sprintf(text, count)
	if len(objects) > 0 {
		shown := objects[:min(len(objects), maxNamesInMessage)]
		names := make([]string, len(shown))
		for i, o := range shown {
			names[i] = displayName(o)
		}
		msg += ": " + strings.Join(names, ", ")
		if rest := count - int64(len(shown)); rest > 0 {
			msg += fmt.Sprintf(" and %d more", rest)
		}
	}
	return runstate.Warning{Code: code, Message: msg, Database: db}
}

// validStatsName accepts a catalog name: valid UTF-8 (proto3 strings
// already are), no NUL, bounded.
func validStatsName(s string) bool {
	return s != "" && len(s) <= maxStatsNameBytes && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// displayName makes a catalog name safe for a one-line message: control
// characters become "?", at most 64 characters.
func displayName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == 64 {
			b.WriteString("…")
			break
		}
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
