package restore

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jlentink/bekup/internal/engine"
)

// The completeness comparison of a test restore (task 4.6, D-141, D-151,
// D-153). Both sides are canonical stats objects (internal/jobs
// CanonicalStats / backup_stats): the source statistics the runner took at
// dump time and the statistics of the restored scratch database.
//
//   - Object counts must match exactly (a missing view, function, trigger
//     or table fails the check); an object kind on one side only counts as
//     0 on the other. One exception (D-170): a PostgreSQL source without
//     the public schema restores into a database that has an empty one,
//     so exactly one extra schema without tables in public is equal.
//   - Tables: every source table must exist in the scratch database and no
//     other table may appear (fail), unless a side's table list was
//     truncated (then only the counts are compared, with a warning).
//   - Rows: PostgreSQL source counts taken in the dump's own snapshot
//     (consistency "snapshot", count "exact") must equal the scratch
//     database's exact count (fail otherwise). Everything else (catalog
//     estimates, MySQL/MariaDB tables, which are always approximate
//     estimates, D-153) is compared as an estimate: a difference is a
//     warning with the delta, never a failure.
//   - Unknown counts on either side, missing source statistics and source
//     completeness warnings are warnings.
//
// The result holds names and counts only (never data values) and is
// bounded (MaxFindings findings, names as recorded: percent-encoded).

// Verdict is the outcome of a comparison.
type Verdict string

// Verdicts.
const (
	VerdictPass Verdict = "pass"
	VerdictWarn Verdict = "warn"
	VerdictFail Verdict = "fail"
)

// Finding severities.
const (
	SeverityWarn = "warn"
	SeverityFail = "fail"
)

// Finding codes.
const (
	FindObjectCount     = "object_count_mismatch"
	FindTableMissing    = "table_missing"
	FindTableUnexpected = "table_unexpected"
	FindRowCount        = "row_count_mismatch"
	FindRowDelta        = "row_count_delta"
	FindRowUnknown      = "row_count_unknown"
	FindTablesTotal     = "tables_total_mismatch"
	FindTablesTruncated = "tables_truncated"
	FindSourceMissing   = "source_stats_missing"
	FindSourceInvalid   = "source_stats_invalid"
	FindSourceWarnings  = "source_warnings"
	FindScratchInvalid  = "scratch_stats_invalid"
)

// MaxFindings bounds the findings kept per database (FindingsTotal still
// counts all).
const MaxFindings = 200

// maxFindingNameBytes bounds a name in a finding.
const maxFindingNameBytes = 256

// Finding is one difference.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	// Object is an object kind ("views") or a table key (schema.table,
	// percent-encoded as recorded), or a list of warning codes.
	Object   string `json:"object,omitempty"`
	Source   *int64 `json:"source,omitempty"`
	Restored *int64 `json:"restored,omitempty"`
	Delta    *int64 `json:"delta,omitempty"`
}

// Comparison is the result for one database.
type Comparison struct {
	Verdict  Verdict   `json:"verdict"`
	Findings []Finding `json:"findings"`
	// FindingsTotal counts every finding (Findings keeps MaxFindings).
	FindingsTotal int `json:"findings_total"`
	// TablesCompared / ExactTables: tables present on both sides, and of
	// those the ones compared exactly.
	TablesCompared int    `json:"tables_compared"`
	ExactTables    int    `json:"exact_tables"`
	SourceLevel    string `json:"source_level,omitempty"`
	Consistency    string `json:"consistency,omitempty"`
}

// canonStats is the canonical stats object (D-151 item 6).
type canonStats struct {
	Level           string                `json:"level"`
	Consistency     string                `json:"consistency"`
	Objects         map[string]int64      `json:"objects"`
	Tables          map[string]canonTable `json:"tables"`
	TablesTotal     *int64                `json:"tables_total"`
	TablesTruncated int64                 `json:"tables_truncated"`
	Warnings        []struct {
		Code  string `json:"code"`
		Count int64  `json:"count"`
	} `json:"warnings"`
}

type canonTable struct {
	Rows        *int64 `json:"rows"`
	Count       string `json:"count"`
	Approximate int64  `json:"approximate"`
}

// maxCanonBytes bounds a parsed stats object (backup_stats allows 4 MiB).
const maxCanonBytes = 4 << 20

func parseCanon(raw json.RawMessage) (*canonStats, error) {
	if len(raw) == 0 {
		return nil, errors.New("restore: no stats")
	}
	if len(raw) > maxCanonBytes {
		return nil, errors.New("restore: stats too large")
	}
	var c canonStats
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("restore: parse stats: %w", err)
	}
	if c.Objects == nil {
		return nil, errors.New("restore: stats without object counts")
	}
	for k, t := range c.Tables {
		if k == "" || len(k) > 2*maxFindingNameBytes+1 {
			return nil, errors.New("restore: invalid table key")
		}
		switch t.Count {
		case "exact", "estimate":
			if t.Rows == nil || *t.Rows < 0 {
				return nil, errors.New("restore: invalid row count")
			}
		case "unknown":
		default:
			return nil, errors.New("restore: invalid count kind")
		}
	}
	return &c, nil
}

// comparer accumulates findings.
type comparer struct {
	c Comparison
}

func ptr(n int64) *int64 { return &n }

func (m *comparer) add(f Finding) {
	m.c.FindingsTotal++
	switch {
	case f.Severity == SeverityFail:
		m.c.Verdict = VerdictFail
	case m.c.Verdict == VerdictPass:
		m.c.Verdict = VerdictWarn
	}
	if len(f.Object) > maxFindingNameBytes {
		f.Object = f.Object[:maxFindingNameBytes]
	}
	if len(m.c.Findings) < MaxFindings {
		m.c.Findings = append(m.c.Findings, f)
	}
}

// CompareStats compares the source statistics of a backup with those of
// its restored scratch database (both canonical stats objects). source nil
// (no backup_stats recorded) only warns; an unreadable scratch side fails.
func CompareStats(eng engine.Kind, source, restored json.RawMessage) Comparison {
	m := &comparer{c: Comparison{Verdict: VerdictPass, Findings: []Finding{}}}
	dst, err := parseCanon(restored)
	if err != nil {
		m.add(Finding{Severity: SeverityFail, Code: FindScratchInvalid})
		return m.c
	}
	if len(source) == 0 {
		m.add(Finding{Severity: SeverityWarn, Code: FindSourceMissing})
		return m.c
	}
	src, err := parseCanon(source)
	if err != nil {
		m.add(Finding{Severity: SeverityWarn, Code: FindSourceInvalid})
		return m.c
	}
	m.c.SourceLevel, m.c.Consistency = src.Level, src.Consistency
	m.objects(eng, src, dst)
	m.tables(eng, src, dst)
	if len(src.Warnings) > 0 {
		codes := make([]string, 0, len(src.Warnings))
		for _, w := range src.Warnings {
			if codeShape.MatchString(w.Code) {
				codes = append(codes, w.Code)
			}
		}
		sort.Strings(codes)
		m.add(Finding{Severity: SeverityWarn, Code: FindSourceWarnings, Object: strings.Join(codes, ","), Source: ptr(int64(len(src.Warnings)))})
	}
	return m.c
}

// implicitPublicSchema reports the one schema difference a PostgreSQL
// restore always produces for a source database without the public
// schema: the scratch database is created from template0, which has
// public, and pg_dump never drops it. It applies only when the restored
// database has exactly one schema more, neither side lists a table in
// public (so the extra schema holds no table) and neither table list was
// truncated (so that is known). Every other object count must still match
// exactly, so any other difference is still a failure.
func implicitPublicSchema(eng engine.Kind, src, dst *canonStats) bool {
	if eng != engine.KindPostgres || src.TablesTruncated != 0 || dst.TablesTruncated != 0 {
		return false
	}
	if dst.Objects["schemas"] != src.Objects["schemas"]+1 {
		return false
	}
	return !hasSchemaTables(src, "public") && !hasSchemaTables(dst, "public")
}

// hasSchemaTables reports a table key in schema (keys are
// schema.table with both parts percent-encoded, so the dot is the
// separator).
func hasSchemaTables(c *canonStats, schema string) bool {
	prefix := schema + "."
	for k := range c.Tables {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func (m *comparer) objects(eng engine.Kind, src, dst *canonStats) {
	publicOnly := implicitPublicSchema(eng, src, dst)
	kinds := make([]string, 0, len(src.Objects)+len(dst.Objects))
	seen := map[string]bool{}
	for _, o := range []map[string]int64{src.Objects, dst.Objects} {
		for k := range o {
			if !seen[k] {
				seen[k] = true
				kinds = append(kinds, k)
			}
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		s, d := src.Objects[k], dst.Objects[k]
		if k == "schemas" && publicOnly {
			continue
		}
		if s != d {
			m.add(Finding{Severity: SeverityFail, Code: FindObjectCount, Object: k, Source: ptr(s), Restored: ptr(d), Delta: ptr(d - s)})
		}
	}
}

func (m *comparer) tables(eng engine.Kind, src, dst *canonStats) {
	truncated := src.TablesTruncated != 0 || dst.TablesTruncated != 0
	if src.TablesTotal != nil && dst.TablesTotal != nil && *src.TablesTotal != *dst.TablesTotal {
		m.add(Finding{Severity: SeverityFail, Code: FindTablesTotal, Source: ptr(*src.TablesTotal), Restored: ptr(*dst.TablesTotal),
			Delta: ptr(*dst.TablesTotal - *src.TablesTotal)})
	}
	if truncated {
		m.add(Finding{Severity: SeverityWarn, Code: FindTablesTruncated})
	}
	keys := make([]string, 0, len(src.Tables))
	for k := range src.Tables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Snapshot-exact source counts exist for PostgreSQL only; MySQL/MariaDB
	// counts are estimates (D-153) and never fail.
	exactSource := eng == engine.KindPostgres && src.Consistency == "snapshot"
	for _, k := range keys {
		s := src.Tables[k]
		d, ok := dst.Tables[k]
		if !ok {
			if !truncated {
				m.add(Finding{Severity: SeverityFail, Code: FindTableMissing, Object: k})
			}
			continue
		}
		m.c.TablesCompared++
		switch {
		case s.Count == "unknown" || d.Count == "unknown":
			f := Finding{Severity: SeverityWarn, Code: FindRowUnknown, Object: k}
			if s.Rows != nil {
				f.Source = ptr(*s.Rows)
			}
			if d.Rows != nil {
				f.Restored = ptr(*d.Rows)
			}
			m.add(f)
		case exactSource && s.Count == "exact" && s.Approximate == 0 && d.Count == "exact":
			m.c.ExactTables++
			if *s.Rows != *d.Rows {
				m.add(Finding{Severity: SeverityFail, Code: FindRowCount, Object: k, Source: ptr(*s.Rows), Restored: ptr(*d.Rows), Delta: ptr(*d.Rows - *s.Rows)})
			}
		default:
			if *s.Rows != *d.Rows {
				m.add(Finding{Severity: SeverityWarn, Code: FindRowDelta, Object: k, Source: ptr(*s.Rows), Restored: ptr(*d.Rows), Delta: ptr(*d.Rows - *s.Rows)})
			}
		}
	}
	if truncated {
		return
	}
	extra := make([]string, 0)
	for k := range dst.Tables {
		if _, ok := src.Tables[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		m.add(Finding{Severity: SeverityFail, Code: FindTableUnexpected, Object: k})
	}
}

// Diff is the stored comparison of one test-restore item
// (test_restore_items.diff): the comparison plus the restore's outcome.
type Diff struct {
	Comparison *Comparison `json:"comparison,omitempty"`
	// RestoreStatus / RestoreErrorCode: the internal restore's outcome
	// (set when it did not succeed).
	RestoreStatus    string `json:"restore_status,omitempty"`
	RestoreErrorCode string `json:"restore_error_code,omitempty"`
	// ScratchKept: the scratch database could not be dropped (the sweep
	// retries).
	ScratchKept bool `json:"scratch_kept,omitempty"`
}

// maxDiffBytes stays below the test_restore_items.diff CHECK (256 KiB).
const maxDiffBytes = 200 << 10

// encodeDiff marshals d, dropping findings until it fits.
func encodeDiff(d Diff) []byte {
	for {
		b, err := json.Marshal(d)
		if err == nil && len(b) <= maxDiffBytes {
			return b
		}
		if d.Comparison == nil || len(d.Comparison.Findings) == 0 {
			return []byte("{}")
		}
		c := *d.Comparison
		c.Findings = c.Findings[:len(c.Findings)/2]
		d.Comparison = &c
	}
}

// ParseDiff reads a stored diff (unknown fields are ignored).
func ParseDiff(raw []byte) (Diff, error) {
	var d Diff
	if len(raw) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return Diff{}, fmt.Errorf("restore: parse diff: %w", err)
	}
	return d, nil
}
