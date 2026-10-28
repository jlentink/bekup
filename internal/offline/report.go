package offline

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/pipeline"
)

// Stats summary of the manifest's source statistics (D-141, D-151). The
// stats object is signed but extensible, so it is decoded leniently.

type statsDoc struct {
	Databases         map[string]dbStats `json:"databases"`
	Globals           *dbStats           `json:"globals"`
	DatabasesExcluded []string           `json:"databases_excluded"`
}

type dbStats struct {
	Level           string                `json:"level"`
	Consistency     string                `json:"consistency"`
	Objects         map[string]int64      `json:"objects"`
	Tables          map[string]tableStats `json:"tables"`
	TablesTotal     int64                 `json:"tables_total"`
	TablesTruncated int64                 `json:"tables_truncated"`
	TablesOmitted   int64                 `json:"tables_omitted"`
	Warnings        []statsWarning        `json:"warnings"`
}

type tableStats struct {
	Count string `json:"count"`
	Rows  int64  `json:"rows"`
}

type statsWarning struct {
	Code      string   `json:"code"`
	Count     int64    `json:"count"`
	Objects   []string `json:"objects"`
	Truncated int64    `json:"truncated"`
}

// DBStatsSummary summarizes the statistics of one database (or globals).
type DBStatsSummary struct {
	Name        string           `json:"name"`
	Level       string           `json:"level,omitempty"`
	Consistency string           `json:"consistency,omitempty"`
	Objects     map[string]int64 `json:"objects,omitempty"`
	TablesTotal int64            `json:"tables_total"`
	// ExactRows / EstimatedRows sum the per-table counts; UnknownTables
	// counts tables without a count.
	ExactRows     int64             `json:"exact_rows"`
	EstimatedRows int64             `json:"estimated_rows"`
	UnknownTables int64             `json:"unknown_tables"`
	ListTruncated bool              `json:"list_truncated,omitempty"`
	Warnings      []StatsWarningSum `json:"warnings,omitempty"`
}

// StatsWarningSum is one completeness warning.
type StatsWarningSum struct {
	Code    string   `json:"code"`
	Count   int64    `json:"count"`
	Objects []string `json:"objects,omitempty"`
}

// StatsSummary is the summary of a manifest's stats.
type StatsSummary struct {
	Databases         []DBStatsSummary `json:"databases"`
	Globals           *DBStatsSummary  `json:"globals,omitempty"`
	DatabasesExcluded []string         `json:"databases_excluded,omitempty"`
	WarningCount      int64            `json:"warning_count"`
}

// SummarizeStats summarizes m.Stats (nil when the manifest has none).
func SummarizeStats(m *pipeline.Manifest) (*StatsSummary, error) {
	if len(m.Stats) == 0 {
		return nil, nil
	}
	var doc statsDoc
	if err := json.Unmarshal(m.Stats, &doc); err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	s := &StatsSummary{}
	names := make([]string, 0, len(doc.Databases))
	for n := range doc.Databases {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		d := summarizeDB(decodedName(n), doc.Databases[n])
		s.WarningCount += warningTotal(d.Warnings)
		s.Databases = append(s.Databases, d)
	}
	if doc.Globals != nil {
		g := summarizeDB("(globals)", *doc.Globals)
		s.WarningCount += warningTotal(g.Warnings)
		s.Globals = &g
	}
	for _, n := range doc.DatabasesExcluded {
		s.DatabasesExcluded = append(s.DatabasesExcluded, decodedName(n))
	}
	return s, nil
}

func decodedName(enc string) string {
	if n, err := pipeline.DecodeName(enc); err == nil {
		return SafeText(n)
	}
	return SafeText(enc)
}

func warningTotal(ws []StatsWarningSum) int64 {
	var n int64
	for _, w := range ws {
		n += max(w.Count, 1)
	}
	return n
}

func summarizeDB(name string, d dbStats) DBStatsSummary {
	out := DBStatsSummary{
		Name: name, Level: SafeText(d.Level), Consistency: SafeText(d.Consistency), Objects: map[string]int64{},
		TablesTotal: d.TablesTotal, ListTruncated: d.TablesTruncated == 1 || d.TablesOmitted == 1,
	}
	for k, v := range d.Objects {
		out.Objects[SafeText(k)] = v
	}
	for _, t := range d.Tables {
		switch t.Count {
		case "exact":
			out.ExactRows += t.Rows
		case "estimate":
			out.EstimatedRows += t.Rows
		default:
			out.UnknownTables++
		}
	}
	for _, w := range d.Warnings {
		ws := StatsWarningSum{Code: SafeText(w.Code), Count: w.Count}
		for i, o := range w.Objects {
			if i == 10 {
				ws.Objects = append(ws.Objects, "...")
				break
			}
			ws.Objects = append(ws.Objects, SafeText(o))
		}
		out.Warnings = append(out.Warnings, ws)
	}
	return out
}

// HumanBytes formats a byte count (binary units).
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB (%d bytes)", float64(n)/float64(div), "KMGTPE"[exp], n)
}

// EncryptionText describes an entry's encryption.
func EncryptionText(e pipeline.EntryEncryption) string {
	switch e.Mode {
	case pipeline.EncryptPassphrase:
		return fmt.Sprintf("age passphrase (job passphrase version %d)", e.PassphraseVersion)
	case pipeline.EncryptRecipients:
		return fmt.Sprintf("age recipients (job recipient key version %d, %d recipient(s))", e.RecipientVersion, len(e.RecipientFingerprints))
	case pipeline.EncryptNone:
		return "NOT ENCRYPTED (encryption opt-out)"
	default:
		return SafeText(string(e.Mode))
	}
}

// WriteRunHeader writes the identity block of a verified manifest.
func WriteRunHeader(w io.Writer, rep *Report) {
	m := rep.Manifest
	fmt.Fprintf(w, "manifest signature: VALID (instance key %s", rep.KeyID)
	if rep.KeyFingerprint != "" {
		fmt.Fprintf(w, ", fingerprint %s", rep.KeyFingerprint)
	}
	fmt.Fprintf(w, ")\n")
	fmt.Fprintf(w, "  manifest sha256: %s (%d bytes)\n", rep.ManifestSHA256, rep.ManifestSize)
	fmt.Fprintf(w, "  job:             %s (id %s)\n", m.JobSlug, m.JobID)
	fmt.Fprintf(w, "  run:             %s (timestamp %s, signed %s)\n", m.RunID,
		m.RunTimestamp.Format(time.RFC3339), m.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  engine:          %s, dump client %s, server %s\n", m.Engine.Kind, m.Engine.VersionKey, SafeText(m.Engine.ServerVersion))
	fmt.Fprintf(w, "  written by:      bekup %s\n", m.BekupVersion)
	fmt.Fprintf(w, "  Check that the job and run are the ones you expect: a signature proves only that this\n")
	fmt.Fprintf(w, "  bekup instance wrote the manifest, not that it is the newest run of the job.\n")
}

// WriteReport writes the human-readable verification report.
func WriteReport(w io.Writer, rep *Report) {
	WriteRunHeader(w, rep)
	m := rep.Manifest
	var raw, stored int64
	fmt.Fprintf(w, "artifacts (%d):\n", len(rep.Entries))
	for i := range rep.Entries {
		r := &rep.Entries[i]
		raw += r.Entry.RawSize
		stored += r.Entry.StoredSize
		status := strings.ToUpper(string(r.Status))
		fmt.Fprintf(w, "  [%s] %s %s\n", status, r.Entry.Kind, r.Name)
		fmt.Fprintf(w, "      file:       %s\n", r.Entry.Path)
		fmt.Fprintf(w, "      size:       %s stored, %s raw dump\n", HumanBytes(r.Entry.StoredSize), HumanBytes(r.Entry.RawSize))
		fmt.Fprintf(w, "      encryption: %s\n", EncryptionText(r.Entry.Encryption))
		for _, fp := range r.Entry.Encryption.RecipientFingerprints {
			fmt.Fprintf(w, "        recipient sha256 %s\n", fp)
		}
		fmt.Fprintf(w, "      sha256:     %s (raw %s)\n", r.Entry.SHA256, r.Entry.RawSHA256)
		if r.Detail != "" {
			fmt.Fprintf(w, "      note:       %s\n", r.Detail)
		}
	}
	fmt.Fprintf(w, "total: %s stored, %s raw\n", HumanBytes(stored), HumanBytes(raw))
	if s, err := SummarizeStats(m); err != nil {
		fmt.Fprintf(w, "stats: unreadable (%s)\n", SafeText(err.Error()))
	} else if s != nil {
		writeStats(w, s)
	} else {
		fmt.Fprintf(w, "stats: none recorded\n")
	}
	if len(m.Warnings) > 0 {
		fmt.Fprintf(w, "run warnings (%d):\n", len(m.Warnings))
		for _, x := range m.Warnings {
			fmt.Fprintf(w, "  - %s\n", SafeText(x))
		}
	}
	switch {
	case rep.OK && rep.Partial:
		fmt.Fprintf(w, "result: OK for the checked artifacts (others skipped)\n")
	case rep.OK:
		fmt.Fprintf(w, "result: OK: signature valid, every artifact matches the manifest\n")
	default:
		fmt.Fprintf(w, "result: FAILED: at least one artifact is missing or differs from the signed manifest\n")
	}
}

func writeStats(w io.Writer, s *StatsSummary) {
	fmt.Fprintf(w, "stats (completeness evidence, %d warning(s)):\n", s.WarningCount)
	all := slices.Clone(s.Databases)
	if s.Globals != nil {
		all = append(all, *s.Globals)
	}
	for _, d := range all {
		fmt.Fprintf(w, "  %s: level %s, consistency %s, %d table(s)", d.Name, d.Level, d.Consistency, d.TablesTotal)
		if d.ExactRows > 0 || d.EstimatedRows > 0 {
			fmt.Fprintf(w, ", rows %d exact + %d estimated", d.ExactRows, d.EstimatedRows)
		}
		if d.UnknownTables > 0 {
			fmt.Fprintf(w, ", %d table(s) without a count", d.UnknownTables)
		}
		if d.ListTruncated {
			fmt.Fprintf(w, " (table list truncated)")
		}
		fmt.Fprintln(w)
		if len(d.Objects) > 0 {
			keys := make([]string, 0, len(d.Objects))
			for k := range d.Objects {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, d.Objects[k]))
			}
			fmt.Fprintf(w, "    objects: %s\n", strings.Join(parts, " "))
		}
		for _, x := range d.Warnings {
			fmt.Fprintf(w, "    WARNING %s (%d)", x.Code, x.Count)
			if len(x.Objects) > 0 {
				fmt.Fprintf(w, ": %s", strings.Join(x.Objects, ", "))
			}
			fmt.Fprintln(w)
		}
	}
	if len(s.DatabasesExcluded) > 0 {
		fmt.Fprintf(w, "  excluded on purpose: %s\n", strings.Join(s.DatabasesExcluded, ", "))
	}
}
