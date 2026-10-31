package validate

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/jlentink/bekup/internal/runnerpb"
)

func versionReq(t *testing.T, q *runnerpb.VersionQuery, stats bool) *runnerpb.DumpRequest {
	t.Helper()
	r := &runnerpb.DumpRequest{Ctx: runCtx(), Target: validTarget(t), Database: "app_prod", VersionQuery: q}
	if stats {
		r.Stats = &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD}
	}
	return r
}

func TestDumpVersionQueryValid(t *testing.T) {
	v := newTestValidator(t)
	got, err := v.Dump(t.Context(), versionReq(t, &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "golang-migrate"}}, true))
	if err != nil || got.Version == nil || got.Version.Preset == nil || got.Version.Preset.Name != "golang-migrate" || got.Version.Schema != "public" {
		t.Fatalf("preset: %+v %v", got, err)
	}
	got, err = v.Dump(t.Context(), versionReq(t, &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "flyway"}, Schema: "app_1"}, true))
	if err != nil || got.Version.Schema != "app_1" {
		t.Fatalf("schema: %+v %v", got, err)
	}
	got, err = v.Dump(t.Context(), versionReq(t, &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "  SELECT max(v) FROM public.t\n"}}, true))
	if err != nil || got.Version.Custom != "SELECT max(v) FROM public.t" || got.Version.Preset != nil {
		t.Fatalf("custom: %+v %v", got, err)
	}
	got, err = v.Dump(t.Context(), versionReq(t, nil, true))
	if err != nil || got.Version != nil {
		t.Fatalf("none: %+v %v", got, err)
	}
}

func TestDumpVersionQueryInvalid(t *testing.T) {
	v := newTestValidator(t)
	for name, tc := range map[string]struct {
		q     *runnerpb.VersionQuery
		stats bool
	}{
		"without stats":      {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}}, false},
		"no query":           {&runnerpb.VersionQuery{}, true},
		"unknown preset":     {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "nope"}}, true},
		"empty preset":       {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: ""}}, true},
		"bad schema":         {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}, Schema: `a"b`}, true},
		"long schema":        {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}, Schema: strings.Repeat("a", 64)}, true},
		"custom with schema": {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1"}, Schema: "public"}, true},
		"custom empty":       {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: ""}}, true},
		"custom two":         {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1; SELECT 2"}}, true},
		"custom write":       {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "DELETE FROM t"}}, true},
		"custom trailing ;":  {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1;"}}, true},
		"custom comment":     {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1 -- x"}}, true},
		"custom union":       {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1 UNION SELECT 2"}}, true},
		"custom CR":          {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1\r"}}, true},
		"custom NUL":         {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT 1\x00"}}, true},
		"custom long":        {&runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT " + strings.Repeat("1", 2100)}}, true},
	} {
		_, err := v.Dump(t.Context(), versionReq(t, tc.q, tc.stats))
		if code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "SELECT 2") || strings.Contains(err.Error(), "DELETE")) {
			t.Errorf("%s: the error quotes the query: %v", name, err)
		}
	}
}
