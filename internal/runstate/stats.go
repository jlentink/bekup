package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pipeline"
)

// Completeness evidence read side (RunItem.stats, D-187): the canonical
// stats objects the backup worker stored in backup_stats (internal/jobs
// encodeStats, D-141/D-151) parsed into a typed, bounded view. Names and
// counts only, never data values. The bounds repeat the writer's caps, so
// a row that exceeds them (it cannot be written by bekup) is cut, never
// passed through unbounded.

// Bounds of a parsed stats object.
const (
	// MaxStatsTables caps the listed tables of an item.
	MaxStatsTables = 10000
	// MaxStatsFindings caps the warnings plus notices of an item.
	MaxStatsFindings = 32
	// MaxStatsFindingObjects caps the object names of a finding.
	MaxStatsFindingObjects = 50
	// maxStatsNameBytes caps a displayed name (the writer accepts 256).
	maxStatsNameBytes = 256
	// maxStatsObjectKinds caps the object kinds.
	maxStatsObjectKinds = 32
	// maxStatsBytes caps a parsed object (backup_stats allows 4 MiB).
	maxStatsBytes = 4 << 20
)

// statsNoticeCodes are the stats finding codes that are informational
// (notices, D-155) rather than warnings.
var statsNoticeCodes = map[string]bool{
	"databases_excluded": true,
}

// RowCount says how a table's row count was obtained.
type RowCount string

// Row count kinds.
const (
	RowCountExact    RowCount = "exact"
	RowCountEstimate RowCount = "estimate"
	RowCountUnknown  RowCount = "unknown"
)

// TableStats is the row count of one table.
type TableStats struct {
	// Name is "schema.table" (PostgreSQL) or the table name (MySQL/MariaDB).
	Name string
	// Rows is nil when Count is RowCountUnknown.
	Rows  *int64
	Count RowCount
	// Approximate: taken outside the dump's snapshot.
	Approximate bool
}

// ObjectCount is the number of objects of one kind.
type ObjectCount struct {
	Kind  string
	Count int64
}

// StatsFinding is a completeness warning or notice.
type StatsFinding struct {
	Code      string
	Count     int64
	Objects   []string
	Truncated bool
}

// ItemStats is the completeness evidence of one run item.
type ItemStats struct {
	RunItemID uuid.UUID
	RunID     uuid.UUID
	// Level is standard or deep (the job's verification level).
	Level string
	// Consistency is snapshot or approximate.
	Consistency     string
	Objects         []ObjectCount
	Tables          []TableStats
	TablesTotal     int64
	TablesTruncated bool
	Warnings        []StatsFinding
	Notices         []StatsFinding
	RecordedAt      time.Time
}

// Stats returns the completeness evidence of runs by run id, then by run
// item id (every requested run has an entry; items without a recorded or
// readable stats object have none).
func (r *Reader) Stats(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]*ItemStats, error) {
	if len(runIDs) > MaxBatch {
		return nil, ErrBadPage
	}
	out := make(map[uuid.UUID]map[uuid.UUID]*ItemStats, len(runIDs))
	for _, id := range runIDs {
		out[id] = map[uuid.UUID]*ItemStats{}
	}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(r.db).ListBackupStatsForRuns(ctx, runIDs)
	if err != nil {
		return nil, fmt.Errorf("runstate: list stats: %w", err)
	}
	for i := range rows {
		row := &rows[i]
		st, err := ParseItemStats(row.Stats)
		if err != nil {
			// Unreadable evidence is shown as absent; the signed manifest
			// still carries the original.
			continue
		}
		st.RunItemID, st.RunID, st.RecordedAt = row.RunItemID, row.RunID, row.CreatedAt
		st.Level = string(row.Level)
		if row.Consistency == "snapshot" || row.Consistency == "approximate" {
			st.Consistency = row.Consistency
		}
		if m := out[row.RunID]; m != nil {
			m[row.RunItemID] = st
		}
	}
	return out, nil
}

type canonFinding struct {
	Code      string   `json:"code"`
	Count     int64    `json:"count"`
	Objects   []string `json:"objects"`
	Truncated int64    `json:"truncated"`
}

type canonTable struct {
	Rows        *int64 `json:"rows"`
	Count       string `json:"count"`
	Approximate int64  `json:"approximate"`
}

type canonItemStats struct {
	Level           string                     `json:"level"`
	Consistency     string                     `json:"consistency"`
	Objects         map[string]int64           `json:"objects"`
	Tables          map[string]json.RawMessage `json:"tables"`
	TablesTotal     int64                      `json:"tables_total"`
	TablesTruncated int64                      `json:"tables_truncated"`
	Warnings        []canonFinding             `json:"warnings"`
}

// ParseItemStats parses a canonical stats object (D-151) into a bounded
// view. Malformed objects are an error; values beyond the bounds are cut
// (TablesTruncated / Truncated set).
func ParseItemStats(raw json.RawMessage) (*ItemStats, error) {
	if len(raw) == 0 || len(raw) > maxStatsBytes {
		return nil, fmt.Errorf("runstate: stats size %d", len(raw))
	}
	var c canonItemStats
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("runstate: parse stats: %w", err)
	}
	if c.Level != "standard" && c.Level != "deep" {
		return nil, fmt.Errorf("runstate: stats level %q", c.Level)
	}
	if c.Consistency != "snapshot" && c.Consistency != "approximate" {
		return nil, fmt.Errorf("runstate: stats consistency %q", c.Consistency)
	}
	if len(c.Objects) > maxStatsObjectKinds {
		return nil, fmt.Errorf("runstate: %d object kinds", len(c.Objects))
	}
	st := &ItemStats{
		Level: c.Level, Consistency: c.Consistency,
		TablesTotal: max(c.TablesTotal, 0), TablesTruncated: c.TablesTruncated == 1,
		Objects: make([]ObjectCount, 0, len(c.Objects)), Tables: []TableStats{},
		Warnings: []StatsFinding{}, Notices: []StatsFinding{},
	}
	for k, n := range c.Objects {
		if !validStatsCode(k) {
			return nil, fmt.Errorf("runstate: object kind %q", k)
		}
		st.Objects = append(st.Objects, ObjectCount{Kind: k, Count: max(n, 0)})
	}
	sort.Slice(st.Objects, func(i, j int) bool { return st.Objects[i].Kind < st.Objects[j].Kind })

	keys := make([]string, 0, len(c.Tables))
	for k := range c.Tables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(st.Tables) >= MaxStatsTables {
			st.TablesTruncated = true
			break
		}
		var t canonTable
		if err := json.Unmarshal(c.Tables[k], &t); err != nil {
			return nil, fmt.Errorf("runstate: parse table: %w", err)
		}
		name, err := tableName(k)
		if err != nil {
			return nil, err
		}
		ts := TableStats{Name: name, Approximate: t.Approximate == 1}
		switch RowCount(t.Count) {
		case RowCountExact, RowCountEstimate:
			if t.Rows == nil || *t.Rows < 0 {
				return nil, fmt.Errorf("runstate: table %q without rows", name)
			}
			rows := *t.Rows
			ts.Count, ts.Rows = RowCount(t.Count), &rows
		case RowCountUnknown:
			ts.Count = RowCountUnknown
		default:
			return nil, fmt.Errorf("runstate: row count kind %q", t.Count)
		}
		st.Tables = append(st.Tables, ts)
	}
	sort.SliceStable(st.Tables, func(i, j int) bool { return st.Tables[i].Name < st.Tables[j].Name })

	for i, w := range c.Warnings {
		if i >= MaxStatsFindings {
			break
		}
		if !validStatsCode(w.Code) {
			return nil, fmt.Errorf("runstate: finding code %q", w.Code)
		}
		f := StatsFinding{Code: w.Code, Count: max(w.Count, 0), Truncated: w.Truncated == 1, Objects: []string{}}
		for j, o := range w.Objects {
			if j >= MaxStatsFindingObjects {
				f.Truncated = true
				break
			}
			if o == "" || len(o) > maxStatsNameBytes || strings.ContainsRune(o, 0) {
				return nil, errors.New("runstate: invalid object name")
			}
			f.Objects = append(f.Objects, o)
		}
		if statsNoticeCodes[f.Code] {
			st.Notices = append(st.Notices, f)
		} else {
			st.Warnings = append(st.Warnings, f)
		}
	}
	return st, nil
}

// tableName decodes a stats table key: EncodeName(schema)+"."+EncodeName(name)
// or EncodeName(name) (encoded names never contain a dot).
func tableName(key string) (string, error) {
	parts := strings.Split(key, ".")
	if len(parts) > 2 {
		return "", errors.New("runstate: invalid table key")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := pipeline.DecodeName(p)
		if err != nil || len(n) > maxStatsNameBytes || strings.ContainsRune(n, 0) {
			return "", errors.New("runstate: invalid table key")
		}
		out = append(out, n)
	}
	return strings.Join(out, "."), nil
}

// validStatsCode accepts a lowercase code (a-z, 0-9, _), ≤ 64 bytes.
func validStatsCode(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}
