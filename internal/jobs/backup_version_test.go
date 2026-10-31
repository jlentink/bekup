package jobs

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/schemaversion"
)

func presetQuery(name string) *runnerpb.VersionQuery {
	return &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: name}, Schema: "public"}
}

func withVersion(st *runnerpb.DumpStats, s runnerpb.VersionStatus, v string) *runnerpb.DumpStats {
	st.VersionStatus, st.SchemaVersion = s, v
	return st
}

func TestEncodeStatsSchemaVersion(t *testing.T) {
	in := StatsInput{Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app", Version: presetQuery("goose")}
	ev, err := encodeStats(in, withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "42 (dirty)"), nil)
	if err != nil || ev.versionStatus != schemaversion.StatusOK || ev.version != "42 (dirty)" {
		t.Fatalf("ok: %+v %v", ev, err)
	}
	m := decodeJSON(t, ev.canonical)
	assertIntsAndStrings(t, m, "$")
	if m["schema_version"] != "42 (dirty)" || m["schema_version_status"] != "ok" {
		t.Fatalf("canonical %s", ev.canonical)
	}
	ev, err = encodeStats(in, withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_NONE, ""), nil)
	if err != nil || ev.versionStatus != schemaversion.StatusNone {
		t.Fatalf("none: %+v %v", ev, err)
	}
	if m := decodeJSON(t, ev.canonical); m["schema_version"] != nil || m["schema_version_status"] != "none" {
		t.Fatalf("canonical %s", ev.canonical)
	}
	// A requested version without a status (an older runner) is unavailable.
	ev, err = encodeStats(in, pgDeepStats(), nil)
	if err != nil || ev.versionStatus != schemaversion.StatusUnavailable {
		t.Fatalf("unspecified: %+v %v", ev, err)
	}
	for name, st := range map[string]*runnerpb.DumpStats{
		"long":            withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, strings.Repeat("9", 65)),
		"control":         withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "4\n2"),
		"empty ok":        withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, ""),
		"untrimmed":       withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, " 42"),
		"value with none": withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_NONE, "42"),
		"value invalid":   withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_INVALID, "42"),
		"value unavail":   withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE, "42"),
	} {
		if _, err := encodeStats(in, st, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A version nobody asked for is a protocol violation.
	in.Version = nil
	if _, err := encodeStats(in, withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "1"), nil); err == nil {
		t.Error("unrequested version accepted")
	}
}

func TestVersionOutcome(t *testing.T) {
	for st, code := range map[schemaversion.Status]string{
		schemaversion.StatusOK: "", schemaversion.StatusNone: "", schemaversion.StatusUnavailable: CodeVersionUnavailable,
		schemaversion.StatusInvalid: CodeVersionInvalid,
	} {
		r := versionOutcome("app", st, "7")
		if r.SchemaVersionStatus != st {
			t.Errorf("%s: status %s", st, r.SchemaVersionStatus)
		}
		if (r.SchemaVersion == "7") != (st == schemaversion.StatusOK) {
			t.Errorf("%s: version %q", st, r.SchemaVersion)
		}
		switch {
		case code == "" && len(r.Warnings) != 0:
			t.Errorf("%s: warnings %+v", st, r.Warnings)
		case code != "" && (len(r.Warnings) != 1 || r.Warnings[0].Code != code || r.Warnings[0].Database != "app"):
			t.Errorf("%s: warnings %+v", st, r.Warnings)
		}
	}
}

func TestVersionQueryRequest(t *testing.T) {
	vq := func(q runstate.SchemaVersionQuery) *runnerpb.VersionQuery {
		t.Helper()
		r, refused := versionQuery(q)
		if refused {
			t.Fatalf("%+v refused", q)
		}
		return r
	}
	if vq(runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModeNone}) != nil {
		t.Error("none")
	}
	q := vq(runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModePreset, Preset: "flyway", Schema: "app"})
	if q.GetPreset() != "flyway" || q.GetSchema() != "app" {
		t.Errorf("preset %v", q)
	}
	q = vq(runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModeCustom, CustomSQL: "SELECT 1", Schema: "public"})
	if q.GetCustomSql() != "SELECT 1" || q.GetSchema() != "" {
		t.Errorf("custom %v", q)
	}
	if vq(runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModePreset}) != nil ||
		vq(runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModeCustom}) != nil {
		t.Error("empty query sent")
	}
	// A stored setting the (stricter) rules refuse is not sent.
	for _, sq := range []runstate.SchemaVersionQuery{
		{Mode: sqlc.JobVersionQueryModeCustom, CustomSQL: "SELECT max(v) FROM public.t; -- saved under older rules"},
		{Mode: sqlc.JobVersionQueryModeCustom, CustomSQL: "SELECT 1 UNION SELECT 2"},
		{Mode: sqlc.JobVersionQueryModePreset, Preset: "removed-preset", Schema: "public"},
		{Mode: sqlc.JobVersionQueryModePreset, Preset: "goose", Schema: `a"b`},
	} {
		if r, refused := versionQuery(sq); r != nil || !refused {
			t.Errorf("%+v: sent %v refused %v", sq, r, refused)
		}
	}
}

// A refused stored query: nothing is sent, the item reports the version
// as unavailable with the refusal text, and the dump is unaffected.
func TestRunnerStatsSessionVersionRefused(t *testing.T) {
	in := StatsInput{RunID: uuid.New(), JobID: uuid.New(), Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app", VersionRefused: true}
	s, err := RunnerStats{}.Begin(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	req := &runnerpb.DumpRequest{}
	s.PrepareDump(req, nil)
	if req.GetVersionQuery() != nil || !req.GetStats().GetEnabled() {
		t.Fatalf("request %v", req)
	}
	for name, o := range map[string]DumpOutcome{
		"with stats": {OK: true, Stats: pgDeepStats()},
		"no stats":   {OK: true},
	} {
		r, err := s.End(t.Context(), o)
		if err != nil {
			t.Fatal(err)
		}
		last := r.Warnings[len(r.Warnings)-1]
		if r.SchemaVersionStatus != schemaversion.StatusUnavailable || last.Code != CodeVersionUnavailable || last.Message != versionRefusedText {
			t.Fatalf("%s: %+v", name, r)
		}
		if name == "with stats" && decodeJSON(t, r.Stats)["schema_version_status"] != "unavailable" {
			t.Fatalf("canonical %s", r.Stats)
		}
	}
	if r, err := s.End(t.Context(), DumpOutcome{OK: false}); err != nil || r.SchemaVersionStatus != "" {
		t.Fatalf("failed dump: %+v %v", r, err)
	}
}

func TestRunnerStatsSessionVersion(t *testing.T) {
	rs := RunnerStats{}
	in := StatsInput{RunID: uuid.New(), JobID: uuid.New(), Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app", Version: presetQuery("rails")}
	s, err := rs.Begin(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	req := &runnerpb.DumpRequest{}
	s.PrepareDump(req, nil)
	if req.GetVersionQuery().GetPreset() != "rails" || !req.GetStats().GetEnabled() {
		t.Fatalf("request %v", req)
	}
	end := func(o DumpOutcome) StatsResult {
		t.Helper()
		r, err := s.End(t.Context(), o)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// No stats message: the version is unavailable (complete dump only).
	r := end(DumpOutcome{OK: true})
	if r.SchemaVersionStatus != schemaversion.StatusUnavailable || len(r.Warnings) != 1 || r.Warnings[0].Code != CodeVersionUnavailable {
		t.Fatalf("no stats: %+v", r)
	}
	if r := end(DumpOutcome{OK: false}); r.SchemaVersionStatus != "" || len(r.Warnings) != 0 {
		t.Fatalf("failed dump without stats: %+v", r)
	}
	r = end(DumpOutcome{OK: true, Stats: withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_INVALID, "")})
	if r.SchemaVersionStatus != schemaversion.StatusInvalid || r.Warnings[len(r.Warnings)-1].Code != CodeVersionInvalid || len(r.Stats) == 0 {
		t.Fatalf("invalid: %+v", r)
	}
	// A failed dump records no version.
	r = end(DumpOutcome{OK: false, Stats: withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "9")})
	if r.SchemaVersionStatus != "" || r.SchemaVersion != "" {
		t.Fatalf("failed dump: %+v", r)
	}
	// Malformed stats: the version is unavailable.
	bad := withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "x\x00")
	r = end(DumpOutcome{OK: true, Stats: bad})
	if r.Stats != nil || r.SchemaVersionStatus != schemaversion.StatusUnavailable {
		t.Fatalf("malformed: %+v", r)
	}
}

// TestBackupRunSchemaVersion: the job's query reaches every database dump
// (not the globals), versions land on the items and in the manifest, and a
// failed capture is a warning that never changes the run status.
func TestBackupRunSchemaVersion(t *testing.T) {
	h := newHarness(t)
	h.opts.Stats = RunnerStats{Store: &fakeStatsStore{}}
	h.store.job.SchemaVersion = runstate.SchemaVersionQuery{Mode: sqlc.JobVersionQueryModePreset, Preset: "golang-migrate", Schema: "public"}
	sc := h.runner.dumps["app"]
	sc.stats = withVersion(pgDeepStats(), runnerpb.VersionStatus_VERSION_STATUS_OK, "42 (dirty)")
	h.runner.dumps["app"] = sc
	sh := h.runner.dumps["shop"]
	sh.stats = withVersion(&runnerpb.DumpStats{Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD, Objects: &runnerpb.ObjectCounts{}},
		runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE, "")
	h.runner.dumps["shop"] = sh
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusSuccess || len(res.Warnings) == 0 {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	for _, r := range h.runner.requests {
		if r.GetVersionQuery().GetPreset() != "golang-migrate" || r.GetVersionQuery().GetSchema() != "public" {
			t.Fatalf("request %v", r)
		}
	}
	app := h.store.item("app", sqlc.RunItemKindDatabase)
	if app.res.SchemaVersionStatus != "ok" || app.res.SchemaVersion != "42 (dirty)" {
		t.Fatalf("app item %+v", app.res)
	}
	shop := h.store.item("shop", sqlc.RunItemKindDatabase)
	found := false
	for _, w := range shop.res.Warnings {
		found = found || w.Code == CodeVersionUnavailable
	}
	if shop.res.SchemaVersionStatus != "unavailable" || shop.res.SchemaVersion != "" || !found {
		t.Fatalf("shop item %+v", shop.res)
	}
	if g := h.store.item("", sqlc.RunItemKindGlobals); g != nil && g.res.SchemaVersionStatus != "" {
		t.Fatalf("globals item %+v", g.res)
	}
	file, _ := h.opener.dests[h.destA].object(h.store.manifests[0].Key)
	m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
	if err != nil {
		t.Fatal(err)
	}
	dbs := asMap(t, decodeJSON(t, m.Stats)["databases"])
	if a := asMap(t, dbs["app"]); a["schema_version"] != "42 (dirty)" || a["schema_version_status"] != "ok" {
		t.Fatalf("manifest app stats %v", a)
	}
}
