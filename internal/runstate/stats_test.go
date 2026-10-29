package runstate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseItemStats(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		raw   string
		ok    bool
		check func(t *testing.T, st *ItemStats)
	}{
		"deep exact": {
			raw: `{"consistency":"snapshot","level":"deep","objects":{"tables":2,"schemas":1},"tables":{"public.b":{"count":"exact","rows":5},"public.a%2Fx":{"count":"exact","rows":0}},"tables_total":2}`,
			ok:  true,
			check: func(t *testing.T, st *ItemStats) {
				t.Helper()
				if st.Level != "deep" || st.Consistency != "snapshot" || st.TablesTotal != 2 || st.TablesTruncated {
					t.Fatalf("%+v", st)
				}
				if len(st.Tables) != 2 || st.Tables[0].Name != "public.a/x" || st.Tables[0].Count != RowCountExact || *st.Tables[0].Rows != 0 || st.Tables[0].Approximate {
					t.Fatalf("tables: %+v", st.Tables)
				}
				if len(st.Objects) != 2 || st.Objects[0].Kind != "schemas" {
					t.Fatalf("objects: %+v", st.Objects)
				}
			},
		},
		"estimate and unknown": {
			raw: `{"consistency":"approximate","level":"standard","objects":{},"tables":{"t1":{"approximate":1,"count":"estimate","rows":9},"t2":{"approximate":1,"count":"unknown"}},"tables_total":5,"tables_truncated":1,"warnings":[{"code":"row_count_timeout","count":1,"objects":["t2"],"truncated":1},{"code":"databases_excluded","count":3}]}`,
			ok:  true,
			check: func(t *testing.T, st *ItemStats) {
				t.Helper()
				if !st.TablesTruncated || st.Tables[0].Count != RowCountEstimate || !st.Tables[0].Approximate || st.Tables[1].Count != RowCountUnknown || st.Tables[1].Rows != nil {
					t.Fatalf("%+v", st.Tables)
				}
				if len(st.Warnings) != 1 || !st.Warnings[0].Truncated || len(st.Notices) != 1 || st.Notices[0].Count != 3 {
					t.Fatalf("findings: %+v %+v", st.Warnings, st.Notices)
				}
			},
		},
		"globals": {
			raw: `{"consistency":"approximate","level":"standard","objects":{"roles":3,"tablespaces":0},"warnings":[{"code":"roles_without_passwords","count":3}]}`,
			ok:  true,
			check: func(t *testing.T, st *ItemStats) {
				t.Helper()
				if len(st.Tables) != 0 || st.Tables == nil || len(st.Warnings) != 1 {
					t.Fatalf("%+v", st)
				}
			},
		},
		"empty":               {raw: ``},
		"not an object":       {raw: `[1]`},
		"bad level":           {raw: `{"level":"x","consistency":"snapshot"}`},
		"bad consistency":     {raw: `{"level":"deep","consistency":"x"}`},
		"bad count kind":      {raw: `{"level":"deep","consistency":"snapshot","tables":{"t":{"count":"maybe","rows":1}}}`},
		"exact without rows":  {raw: `{"level":"deep","consistency":"snapshot","tables":{"t":{"count":"exact"}}}`},
		"negative rows":       {raw: `{"level":"deep","consistency":"snapshot","tables":{"t":{"count":"exact","rows":-1}}}`},
		"non-canonical name":  {raw: `{"level":"deep","consistency":"snapshot","tables":{"a%61":{"count":"unknown"}}}`},
		"three part name":     {raw: `{"level":"deep","consistency":"snapshot","tables":{"a.b.c":{"count":"unknown"}}}`},
		"bad finding code":    {raw: `{"level":"deep","consistency":"snapshot","warnings":[{"code":"Bad Code","count":1}]}`},
		"bad object kind":     {raw: `{"level":"deep","consistency":"snapshot","objects":{"Tables":1}}`},
		"nul in object name":  {raw: `{"level":"deep","consistency":"snapshot","warnings":[{"code":"x","count":1,"objects":["a\u0000b"]}]}`},
		"float count":         {raw: `{"level":"deep","consistency":"snapshot","tables":{"t":{"count":"exact","rows":1.5}}}`},
		"string where number": {raw: `{"level":"deep","consistency":"snapshot","tables_total":"2"}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st, err := ParseItemStats(json.RawMessage(tc.raw))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
			if tc.ok && tc.check != nil {
				tc.check(t, st)
			}
		})
	}
}

func TestParseItemStatsBounds(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(`{"level":"deep","consistency":"snapshot","tables":{`)
	for i := range MaxStatsTables + 5 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"t%05d":{"count":"exact","rows":%d}`, i, i)
	}
	b.WriteString(`},"warnings":[`)
	for i := range MaxStatsFindings + 3 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"code":"x","count":60,"objects":[`)
		for j := range MaxStatsFindingObjects + 2 {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `"o%d"`, j)
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)
	st, err := ParseItemStats(json.RawMessage(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tables) != MaxStatsTables || !st.TablesTruncated || st.Tables[0].Name != "t00000" {
		t.Fatalf("tables: %d truncated %v", len(st.Tables), st.TablesTruncated)
	}
	if len(st.Warnings) != MaxStatsFindings || len(st.Warnings[0].Objects) != MaxStatsFindingObjects || !st.Warnings[0].Truncated {
		t.Fatalf("findings: %d", len(st.Warnings))
	}
	if _, err := ParseItemStats(make(json.RawMessage, maxStatsBytes+1)); err == nil {
		t.Fatal("oversized stats accepted")
	}
}

func FuzzParseItemStats(f *testing.F) {
	f.Add(`{"consistency":"snapshot","level":"deep","objects":{"tables":1},"tables":{"public.t":{"count":"exact","rows":1}},"tables_total":1}`)
	f.Add(`{"consistency":"approximate","level":"standard","warnings":[{"code":"x","count":1,"objects":["a"]}]}`)
	f.Fuzz(func(t *testing.T, raw string) {
		st, err := ParseItemStats(json.RawMessage(raw))
		if err != nil {
			return
		}
		if len(st.Tables) > MaxStatsTables || len(st.Warnings)+len(st.Notices) > MaxStatsFindings {
			t.Fatalf("unbounded: %d tables", len(st.Tables))
		}
		for _, tb := range st.Tables {
			if (tb.Rows == nil) != (tb.Count == RowCountUnknown) || (tb.Rows != nil && *tb.Rows < 0) {
				t.Fatalf("table: %+v", tb)
			}
		}
	})
}
