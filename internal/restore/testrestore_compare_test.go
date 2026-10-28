package restore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
)

const pgSource = `{"level":"deep","consistency":"snapshot",
 "objects":{"schemas":1,"tables":2,"views":1,"materialized_views":0,"sequences":1,"functions":2,"procedures":0,"triggers":1,"extensions":1},
 "tables":{"public.a":{"rows":100,"count":"exact"},"public.b":{"rows":7,"count":"exact"}},"tables_total":2}`

const myObjects = `"objects":{"schemas":0,"tables":2,"views":1,"sequences":0,"functions":0,"procedures":1,"triggers":1,"events":0}`

func findingCodes(c Comparison) string {
	out := make([]string, 0, len(c.Findings))
	for _, f := range c.Findings {
		out = append(out, f.Severity+":"+f.Code+":"+f.Object)
	}
	return strings.Join(out, ",")
}

func TestCompareStats(t *testing.T) {
	tests := []struct {
		name     string
		eng      engine.Kind
		src, dst string
		verdict  Verdict
		want     string
	}{
		{name: "exact match passes", eng: engine.KindPostgres, src: pgSource, dst: pgSource, verdict: VerdictPass},
		{
			name: "postgres row mismatch fails", eng: engine.KindPostgres, src: pgSource,
			dst:     strings.Replace(pgSource, `"rows":7,`, `"rows":6,`, 1),
			verdict: VerdictFail, want: "fail:row_count_mismatch:public.b",
		},
		{
			name: "missing table fails", eng: engine.KindPostgres, src: pgSource,
			dst: strings.Replace(strings.Replace(strings.Replace(pgSource, `,"public.b":{"rows":7,"count":"exact"}`, ``, 1),
				`"tables":2,`, `"tables":1,`, 1), `"tables_total":2`, `"tables_total":1`, 1),
			verdict: VerdictFail, want: "fail:object_count_mismatch:tables,fail:tables_total_mismatch:,fail:table_missing:public.b",
		},
		{
			name: "unexpected table fails", eng: engine.KindPostgres, src: pgSource,
			dst:     strings.Replace(pgSource, `"public.b":{"rows":7,"count":"exact"}`, `"public.b":{"rows":7,"count":"exact"},"public.z":{"rows":1,"count":"exact"}`, 1),
			verdict: VerdictFail, want: "fail:table_unexpected:public.z",
		},
		{
			name: "missing view fails", eng: engine.KindPostgres, src: pgSource,
			dst:     strings.Replace(pgSource, `"views":1`, `"views":0`, 1),
			verdict: VerdictFail, want: "fail:object_count_mismatch:views",
		},
		{
			name: "estimates only warn", eng: engine.KindPostgres,
			src:     strings.Replace(strings.ReplaceAll(pgSource, `"count":"exact"`, `"count":"estimate"`), `"level":"deep"`, `"level":"standard"`, 1),
			dst:     strings.Replace(pgSource, `"rows":100,`, `"rows":104,`, 1),
			verdict: VerdictWarn, want: "warn:row_count_delta:public.a",
		},
		{
			name: "no same snapshot only warns", eng: engine.KindPostgres,
			src:     strings.Replace(pgSource, `"snapshot"`, `"approximate"`, 1),
			dst:     strings.Replace(pgSource, `"rows":100,`, `"rows":99,`, 1),
			verdict: VerdictWarn, want: "warn:row_count_delta:public.a",
		},
		{
			name: "unknown counts warn", eng: engine.KindPostgres,
			src:     strings.Replace(pgSource, `"public.a":{"rows":100,"count":"exact"}`, `"public.a":{"count":"unknown"}`, 1),
			dst:     pgSource,
			verdict: VerdictWarn, want: "warn:row_count_unknown:public.a",
		},
		{
			name: "mysql deltas warn", eng: engine.KindMySQL,
			src:     `{"level":"standard","consistency":"approximate",` + myObjects + `,"tables":{"t":{"rows":900,"count":"estimate","approximate":1},"b":{"rows":950,"count":"estimate","approximate":1}},"tables_total":2}`,
			dst:     `{"level":"standard","consistency":"approximate",` + myObjects + `,"tables":{"t":{"rows":871,"count":"estimate","approximate":1},"b":{"rows":950,"count":"estimate","approximate":1}},"tables_total":2}`,
			verdict: VerdictWarn, want: "warn:row_count_delta:t",
		},
		{
			name: "mysql exact-looking counts never fail", eng: engine.KindMariaDB,
			src:     `{"level":"standard","consistency":"approximate",` + myObjects + `,"tables":{"t":{"rows":900,"count":"exact"},"b":{"rows":950,"count":"exact"}},"tables_total":2}`,
			dst:     `{"level":"standard","consistency":"approximate",` + myObjects + `,"tables":{"t":{"rows":1,"count":"exact"},"b":{"rows":950,"count":"exact"}},"tables_total":2}`,
			verdict: VerdictWarn, want: "warn:row_count_delta:t",
		},
		{
			name: "mysql missing procedure fails", eng: engine.KindMySQL,
			src:     `{"level":"standard","consistency":"approximate",` + myObjects + `,"tables":{},"tables_total":0}`,
			dst:     `{"level":"standard","consistency":"approximate",` + strings.Replace(myObjects, `"procedures":1`, `"procedures":0`, 1) + `,"tables":{},"tables_total":0}`,
			verdict: VerdictFail, want: "fail:object_count_mismatch:procedures",
		},
		{
			name: "source warnings warn", eng: engine.KindPostgres,
			src:     strings.Replace(pgSource, `"tables_total":2`, `"tables_total":2,"warnings":[{"code":"insufficient_privileges","count":1},{"code":"non_transactional_tables","count":2}]`, 1),
			dst:     pgSource,
			verdict: VerdictWarn, want: "warn:source_warnings:insufficient_privileges,non_transactional_tables",
		},
		{
			name: "truncated lists compare counts only", eng: engine.KindPostgres,
			src:     strings.Replace(pgSource, `"tables_total":2`, `"tables_total":2,"tables_truncated":1`, 1),
			dst:     strings.Replace(pgSource, `,"public.b":{"rows":7,"count":"exact"}`, ``, 1),
			verdict: VerdictWarn, want: "warn:tables_truncated:",
		},
		{name: "missing source stats warn", eng: engine.KindPostgres, src: ``, dst: pgSource, verdict: VerdictWarn, want: "warn:source_stats_missing:"},
		{name: "invalid source stats warn", eng: engine.KindPostgres, src: `{"tables":[]}`, dst: pgSource, verdict: VerdictWarn, want: "warn:source_stats_invalid:"},
		{name: "invalid scratch stats fail", eng: engine.KindPostgres, src: pgSource, dst: `{"objects":{},"tables":{"x":{"count":"exact"}}}`, verdict: VerdictFail, want: "fail:scratch_stats_invalid:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var src json.RawMessage
			if tc.src != "" {
				src = json.RawMessage(tc.src)
			}
			c := CompareStats(tc.eng, src, json.RawMessage(tc.dst))
			if c.Verdict != tc.verdict || findingCodes(c) != tc.want {
				t.Fatalf("verdict %s findings %q, want %s %q", c.Verdict, findingCodes(c), tc.verdict, tc.want)
			}
		})
	}
}

func TestCompareStatsDelta(t *testing.T) {
	c := CompareStats(engine.KindPostgres, json.RawMessage(pgSource), json.RawMessage(strings.Replace(pgSource, `"rows":100,`, `"rows":90,`, 1)))
	if len(c.Findings) != 1 {
		t.Fatalf("findings %v", c.Findings)
	}
	f := c.Findings[0]
	if *f.Source != 100 || *f.Restored != 90 || *f.Delta != -10 || c.TablesCompared != 2 || c.ExactTables != 2 {
		t.Fatalf("finding %+v / %+v", f, c)
	}
}

func TestCompareStatsBounded(t *testing.T) {
	var src, dst strings.Builder
	src.WriteString(`{"level":"deep","consistency":"snapshot","objects":{"tables":1000},"tables":{`)
	dst.WriteString(`{"level":"deep","consistency":"snapshot","objects":{"tables":1000},"tables":{`)
	for i := range 1000 {
		if i > 0 {
			src.WriteByte(',')
			dst.WriteByte(',')
		}
		fmt.Fprintf(&src, `"public.t%04d":{"rows":%d,"count":"exact"}`, i, i)
		fmt.Fprintf(&dst, `"public.t%04d":{"rows":%d,"count":"exact"}`, i, i+1)
	}
	src.WriteString(`},"tables_total":1000}`)
	dst.WriteString(`},"tables_total":1000}`)
	c := CompareStats(engine.KindPostgres, json.RawMessage(src.String()), json.RawMessage(dst.String()))
	if c.Verdict != VerdictFail || len(c.Findings) != MaxFindings || c.FindingsTotal != 1000 {
		t.Fatalf("verdict %s findings %d total %d", c.Verdict, len(c.Findings), c.FindingsTotal)
	}
	b := encodeDiff(Diff{Comparison: &c})
	if len(b) > maxDiffBytes {
		t.Fatalf("diff %d bytes", len(b))
	}
	d, err := ParseDiff(b)
	if err != nil || d.Comparison == nil || d.Comparison.FindingsTotal != 1000 {
		t.Fatalf("round trip: %v %+v", err, d)
	}
	// Overlong names are cut.
	long := strings.Repeat("x", 400)
	c = CompareStats(engine.KindPostgres, json.RawMessage(`{"objects":{},"tables":{"`+long+`":{"rows":1,"count":"exact"}}}`),
		json.RawMessage(`{"objects":{},"tables":{}}`))
	if len(c.Findings) != 1 || len(c.Findings[0].Object) != maxFindingNameBytes {
		t.Fatalf("findings %v", c.Findings)
	}
}

func FuzzCompareStats(f *testing.F) {
	f.Add([]byte(pgSource), []byte(pgSource))
	f.Add([]byte(`{}`), []byte(`{"objects":{}}`))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		c := CompareStats(engine.KindPostgres, a, b)
		if len(c.Findings) > MaxFindings || c.FindingsTotal < len(c.Findings) {
			t.Fatalf("unbounded: %d/%d", len(c.Findings), c.FindingsTotal)
		}
		if len(encodeDiff(Diff{Comparison: &c})) > maxDiffBytes {
			t.Fatal("diff too large")
		}
	})
}

// A PostgreSQL source without the public schema restores into a database
// created from template0, which has an empty public schema (D-170).
func TestCompareStatsImplicitPublicSchema(t *testing.T) {
	src := `{"level":"deep","consistency":"snapshot",
 "objects":{"schemas":1,"tables":1,"views":0,"materialized_views":0,"sequences":0,"functions":0,"procedures":0,"triggers":0,"extensions":1},
 "tables":{"app.t":{"rows":3,"count":"exact"}},"tables_total":1}`
	withPublic := strings.Replace(src, `"schemas":1`, `"schemas":2`, 1)
	tests := []struct {
		name     string
		eng      engine.Kind
		src, dst string
		verdict  Verdict
		want     string
	}{
		{name: "source without public passes", eng: engine.KindPostgres, src: src, dst: withPublic, verdict: VerdictPass},
		{
			name: "two extra schemas still fail", eng: engine.KindPostgres, src: src,
			dst: strings.Replace(src, `"schemas":1`, `"schemas":3`, 1), verdict: VerdictFail, want: "fail:object_count_mismatch:schemas",
		},
		{
			name: "a missing schema still fails", eng: engine.KindPostgres, src: withPublic, dst: src,
			verdict: VerdictFail, want: "fail:object_count_mismatch:schemas",
		},
		{
			name: "extra public with a table fails", eng: engine.KindPostgres, src: src,
			dst: strings.Replace(strings.Replace(strings.Replace(withPublic, `"app.t":{"rows":3,"count":"exact"}`,
				`"app.t":{"rows":3,"count":"exact"},"public.x":{"rows":0,"count":"exact"}`, 1), `"tables":1,`, `"tables":2,`, 1),
				`"tables_total":1`, `"tables_total":2`, 1),
			verdict: VerdictFail, want: "fail:object_count_mismatch:schemas,fail:object_count_mismatch:tables,fail:tables_total_mismatch:,fail:table_unexpected:public.x",
		},
		{
			name: "other differences are not masked", eng: engine.KindPostgres, src: src,
			dst: strings.Replace(withPublic, `"views":0`, `"views":1`, 1), verdict: VerdictFail, want: "fail:object_count_mismatch:views",
		},
		{
			name: "truncated table lists keep the schema check", eng: engine.KindPostgres,
			src: strings.Replace(src, `"tables_total":1`, `"tables_total":1,"tables_truncated":1`, 1), dst: withPublic,
			verdict: VerdictFail, want: "fail:object_count_mismatch:schemas,warn:tables_truncated:",
		},
		{
			name: "mysql is not normalized", eng: engine.KindMySQL,
			src:     strings.Replace(src, `"consistency":"snapshot"`, `"consistency":"approximate"`, 1),
			dst:     strings.Replace(withPublic, `"consistency":"snapshot"`, `"consistency":"approximate"`, 1),
			verdict: VerdictFail, want: "fail:object_count_mismatch:schemas",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CompareStats(tt.eng, json.RawMessage(tt.src), json.RawMessage(tt.dst))
			if c.Verdict != tt.verdict || findingCodes(c) != tt.want {
				t.Fatalf("verdict %s findings %q, want %s %q", c.Verdict, findingCodes(c), tt.verdict, tt.want)
			}
		})
	}
}
