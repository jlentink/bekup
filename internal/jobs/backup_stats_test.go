package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

func pgDeepStats() *runnerpb.DumpStats {
	return &runnerpb.DumpStats{
		Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, SameSnapshot: true,
		Objects: &runnerpb.ObjectCounts{Schemas: 2, Tables: 3, Views: 1, MaterializedViews: 1, Sequences: 2, Functions: 4, Procedures: 1, Triggers: 1, Extensions: 1},
		Tables: []*runnerpb.TableStats{
			{Schema: "public", Name: "users", Rows: 123, Count: runnerpb.RowCount_ROW_COUNT_EXACT},
			{Schema: "public", Name: "big.table", Count: runnerpb.RowCount_ROW_COUNT_UNKNOWN},
			{Schema: "private", Name: "keys", Count: runnerpb.RowCount_ROW_COUNT_UNKNOWN},
		},
		TablesTotal: 3,
		Warnings: []*runnerpb.StatsWarning{
			{Code: "insufficient_privileges", Count: 1, Objects: []string{"private.keys"}},
			{Code: "row_count_timeout", Count: 1, Objects: []string{"public.big.table"}},
		},
	}
}

// assertIntsAndStrings walks a decoded JSON value: only objects, arrays,
// strings and integers are allowed (D-144 canonical JSON).
func assertIntsAndStrings(t *testing.T, v any, path string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			assertIntsAndStrings(t, e, path+"."+k)
		}
	case []any:
		for _, e := range x {
			assertIntsAndStrings(t, e, path+"[]")
		}
	case string:
	case json.Number:
		if _, err := x.Int64(); err != nil {
			t.Fatalf("%s: non-integer number %s", path, x)
		}
	default:
		t.Fatalf("%s: forbidden JSON type %T", path, v)
	}
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEncodeStatsCanonical(t *testing.T) {
	in := StatsInput{Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app", Deep: true}
	ev, err := encodeStats(in, pgDeepStats(), &runnerpb.DumpResult{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"consistency":"snapshot","level":"deep","objects":{"extensions":1,"functions":4,"materialized_views":1,"procedures":1,` +
		`"schemas":2,"sequences":2,"tables":3,"triggers":1,"views":1},"tables":{"private.keys":{"count":"unknown"},` +
		`"public.big%2Etable":{"count":"unknown"},"public.users":{"count":"exact","rows":123}},"tables_total":3,` +
		`"warnings":[{"code":"insufficient_privileges","count":1,"objects":["private.keys"]},` +
		`{"code":"row_count_timeout","count":1,"objects":["public.big.table"]}]}`
	if string(ev.canonical) != want {
		t.Fatalf("stats\n got  %s\n want %s", ev.canonical, want)
	}
	again, err := pipeline.Canonicalize(ev.canonical)
	if err != nil || !bytes.Equal(again, ev.canonical) {
		t.Fatalf("not canonical: %v", err)
	}
	assertIntsAndStrings(t, decodeJSON(t, ev.canonical), "$")
	if ev.consistency != "snapshot" || len(ev.warnings) != 2 || ev.warnings[0].Code != "insufficient_privileges" ||
		!strings.Contains(ev.warnings[0].Message, "private.keys") || ev.warnings[0].Database != "app" {
		t.Fatalf("evidence %+v", ev)
	}
}

func TestEncodeStatsMySQLAndGlobals(t *testing.T) {
	st := &runnerpb.DumpStats{
		Level:   runnerpb.StatsLevel_STATS_LEVEL_STANDARD,
		Objects: &runnerpb.ObjectCounts{Schemas: 1, Tables: 2, Events: 1},
		Tables: []*runnerpb.TableStats{
			{Name: "m", Rows: 2, Count: runnerpb.RowCount_ROW_COUNT_ESTIMATE, Approximate: true},
			{Name: "t", Rows: 900, Count: runnerpb.RowCount_ROW_COUNT_ESTIMATE, Approximate: true},
		},
		TablesTotal: 5, TablesTruncated: true,
		Warnings: []*runnerpb.StatsWarning{{Code: "non_transactional_tables", Count: 1, Objects: []string{"m"}}},
	}
	// SameSnapshot is never trusted for MySQL.
	st.SameSnapshot = true
	ev, err := encodeStats(StatsInput{Engine: engine.KindMariaDB, Kind: pipeline.KindDatabase, Database: "shop"}, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, ev.canonical)
	assertIntsAndStrings(t, m, "$")
	tm := asMap(t, asMap(t, m["tables"])["m"])
	if ev.consistency != "approximate" || tm["approximate"] != json.Number("1") || tm["count"] != "estimate" ||
		m["tables_truncated"] != json.Number("1") || m["level"] != "standard" {
		t.Fatalf("stats %s", ev.canonical)
	}
	if obj := asMap(t, m["objects"]); obj["events"] != json.Number("1") || obj["extensions"] != nil {
		t.Fatalf("objects %v", m["objects"])
	}

	g := &runnerpb.DumpStats{Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, Objects: &runnerpb.ObjectCounts{Roles: 7, Tablespaces: 1}}
	ev, err = encodeStats(StatsInput{Engine: engine.KindPostgres, Kind: pipeline.KindGlobals}, g, &runnerpb.DumpResult{RolesWithoutPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"consistency":"approximate","level":"deep","objects":{"roles":7,"tablespaces":1},"warnings":[{"code":"roles_without_passwords","count":7}]}`
	if string(ev.canonical) != want {
		t.Fatalf("globals\n got  %s\n want %s", ev.canonical, want)
	}
}

func TestEncodeStatsRejects(t *testing.T) {
	in := StatsInput{Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app"}
	for name, mut := range map[string]func(*runnerpb.DumpStats){
		"level":       func(s *runnerpb.DumpStats) { s.Level = 0 },
		"count kind":  func(s *runnerpb.DumpStats) { s.Tables[0].Count = 0 },
		"negative":    func(s *runnerpb.DumpStats) { s.Tables[0].Rows = -1 },
		"nul name":    func(s *runnerpb.DumpStats) { s.Tables[0].Name = "a\x00b" },
		"long name":   func(s *runnerpb.DumpStats) { s.Tables[0].Name = strings.Repeat("x", 300) },
		"duplicate":   func(s *runnerpb.DumpStats) { s.Tables[1] = s.Tables[0] },
		"bad code":    func(s *runnerpb.DumpStats) { s.Warnings[0].Code = "Bad Code" },
		"bad object":  func(s *runnerpb.DumpStats) { s.Warnings[0].Objects = []string{""} },
		"many tables": func(s *runnerpb.DumpStats) { s.Tables = make([]*runnerpb.TableStats, maxStatsTables+1) },
		"many objects": func(s *runnerpb.DumpStats) {
			s.Warnings[0].Objects = make([]string, maxStatsWarningObjects+1)
		},
	} {
		st := pgDeepStats()
		mut(st)
		if _, err := encodeStats(in, st, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStatsWarningMessage(t *testing.T) {
	objs := make([]string, 0, 22)
	objs = append(objs, "a\nb", "c\x1bd")
	for i := range 20 {
		objs = append(objs, "t"+string(rune('a'+i)))
	}
	w := statsWarning("app", "non_transactional_tables", 40, objs)
	if strings.ContainsAny(w.Message, "\n\x1b") || !strings.Contains(w.Message, "a?b") || !strings.Contains(w.Message, "and 32 more") {
		t.Fatalf("message %q", w.Message)
	}
	if w := statsWarning("", "something_new", 3, nil); w.Code != "something_new" || !strings.Contains(w.Message, "3 object(s)") {
		t.Fatalf("unknown code %+v", w)
	}
	if got := displayName(strings.Repeat("é", 100)); len([]rune(got)) != 65 {
		t.Fatalf("display name %q", got)
	}
}

type fakeStatsStore struct {
	mu   sync.Mutex
	rows []sqlc.InsertBackupStatsParams
	err  error
}

func (s *fakeStatsStore) InsertBackupStats(_ context.Context, p sqlc.InsertBackupStatsParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.rows = append(s.rows, p)
	return nil
}

func TestRunnerStatsSession(t *testing.T) {
	store := &fakeStatsStore{}
	rs := RunnerStats{Store: store, PerTableTimeout: 1500 * time.Millisecond, Budget: time.Minute}
	in := StatsInput{RunID: uuid.New(), JobID: uuid.New(), ItemID: uuid.New(), Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "app", Deep: true}
	s, err := rs.Begin(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	req := &runnerpb.DumpRequest{}
	s.PrepareDump(req, nil)
	if o := req.GetStats(); !o.GetEnabled() || o.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_DEEP || o.GetPerTableTimeoutMs() != 1500 || o.GetBudgetMs() != 60000 {
		t.Fatalf("options %v", o)
	}
	g := &runnerpb.DumpGlobalsRequest{}
	s.PrepareDump(nil, g)
	if !g.GetStats().GetEnabled() {
		t.Fatal("globals options")
	}
	// An incomplete dump yields nothing.
	if r, err := s.End(t.Context(), DumpOutcome{OK: false, Stats: pgDeepStats()}); err != nil || r.Stats != nil || len(store.rows) != 0 {
		t.Fatalf("incomplete: %+v %v", r, err)
	}
	r, err := s.End(t.Context(), DumpOutcome{OK: true, Stats: pgDeepStats(), Result: &runnerpb.DumpResult{}})
	if err != nil || len(r.Stats) == 0 || len(r.Warnings) != 2 {
		t.Fatalf("end: %+v %v", r, err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("rows %d", len(store.rows))
	}
	row := store.rows[0]
	if row.RunItemID != in.ItemID || row.Level != sqlc.JobVerificationDeep || row.Consistency != "snapshot" || row.TablesTotal != 3 ||
		row.WarningCount != 2 || !bytes.Equal(row.Stats, r.Stats) {
		t.Fatalf("row %+v", row)
	}
	store.err = errors.New("db down")
	r, err = s.End(t.Context(), DumpOutcome{OK: true, Stats: pgDeepStats()})
	if err != nil || len(r.Stats) == 0 || r.Warnings[len(r.Warnings)-1].Code != "stats_not_recorded" {
		t.Fatalf("store failure: %+v %v", r, err)
	}
	bad := pgDeepStats()
	bad.Level = 0
	r, err = s.End(t.Context(), DumpOutcome{OK: true, Stats: bad})
	if err != nil || r.Stats != nil || len(r.Warnings) != 1 || r.Warnings[0].Code != "stats_invalid" {
		t.Fatalf("malformed: %+v %v", r, err)
	}
}

func TestManifestStatsOmitTables(t *testing.T) {
	big := map[string]any{"level": "deep", "tables_total": 1, "tables": map[string]any{strings.Repeat("t", 100): map[string]any{"count": "unknown"}}}
	b, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pipeline.Canonicalize(b)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]*itemState, 0, 100)
	for i := range 100 {
		items = append(items, &itemState{kind: pipeline.KindDatabase, db: "db" + strings.Repeat("x", i), stats: c})
	}
	if got := manifestStats(items, nil); !strings.Contains(string(got), `"tables":`) {
		t.Fatalf("small stats lost their tables")
	}
	stripped := withoutTables(c)
	if strings.Contains(string(stripped), `"tables":`) || !strings.Contains(string(stripped), `"tables_omitted":1`) {
		t.Fatalf("stripped %s", stripped)
	}
	// Over the budget the tables go, the rest stays.
	huge := map[string]any{"level": "deep", "tables": map[string]any{}}
	tm := asMap(t, huge["tables"])
	for i := range 40000 {
		tm[strings.Repeat("n", 150)+uuid.NewString()+string(rune('a'+i%26))] = map[string]any{"count": "exact", "rows": 1}
	}
	if b, err = json.Marshal(huge); err != nil {
		t.Fatal(err)
	}
	hc, err := pipeline.Canonicalize(b)
	if err != nil {
		t.Fatal(err)
	}
	items = []*itemState{{kind: pipeline.KindDatabase, db: "a", stats: hc}, {kind: pipeline.KindDatabase, db: "b", stats: hc}}
	got := manifestStats(items, nil)
	if len(got) == 0 || len(got) > maxManifestStatsBytes || strings.Contains(string(got), `"rows"`) || !strings.Contains(string(got), `"level":"deep"`) {
		t.Fatalf("huge stats: %d bytes", len(got))
	}
}

func TestExcludedNotice(t *testing.T) {
	job := runstate.Job{AllDatabases: true, Exclude: []string{"skip", "gone"}}
	w, ex, ok := excludedNotice(job, []string{"app", "skip"})
	if !ok || w.Code != "databases_excluded" || !strings.Contains(w.Message, "skip") || strings.Contains(w.Message, "gone") ||
		len(ex) != 1 || ex[0] != "skip" {
		t.Fatalf("notice %+v %v", w, ex)
	}
	if _, _, ok := excludedNotice(runstate.Job{Databases: []string{"app"}}, []string{"app", "x"}); ok {
		t.Fatal("selected mode notices")
	}
	// The manifest's stats list the excluded databases (encoded strings).
	st := manifestStats(nil, []string{"skip", "a b"})
	if string(st) != `{"databases_excluded":["skip","a%20b"]}` {
		t.Fatalf("manifest stats %s", st)
	}
}

func TestBackupRunStats(t *testing.T) {
	h := newHarness(t)
	store := &fakeStatsStore{}
	h.opts.Stats = RunnerStats{Store: store}
	h.store.job.Verification = sqlc.JobVerificationDeep
	sc := h.runner.dumps["app"]
	sc.stats = pgDeepStats()
	h.runner.dumps["app"] = sc
	g := h.runner.dumps[""]
	g.stats = &runnerpb.DumpStats{Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, Objects: &runnerpb.ObjectCounts{Roles: 3}}
	h.runner.dumps[""] = g
	sh := h.runner.dumps["shop"]
	sh.warnings = []string{"stats_unavailable"}
	h.runner.dumps["shop"] = sh
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := h.result()
	if res.Status != runstate.StatusSuccess {
		t.Fatalf("status %s %+v", res.Status, res.Failure)
	}
	for _, r := range h.runner.requests {
		if o := r.GetStats(); !o.GetEnabled() || o.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_DEEP {
			t.Fatalf("request stats %v", o)
		}
	}
	if len(h.runner.globalsReqs) != 1 || !h.runner.globalsReqs[0].GetStats().GetEnabled() {
		t.Fatal("globals request without stats")
	}
	codes := map[string]bool{}
	for _, w := range res.Warnings {
		codes[w.Code] = true
	}
	for _, c := range []string{"insufficient_privileges", "row_count_timeout", "stats_unavailable"} {
		if !codes[c] {
			t.Fatalf("missing warning %s in %+v", c, res.Warnings)
		}
	}
	// The excluded databases are a notice, not a warning (task 3.11).
	if codes["databases_excluded"] || len(res.Notices) != 1 || res.Notices[0].Code != "databases_excluded" {
		t.Fatalf("notices %+v warnings %+v", res.Notices, res.Warnings)
	}
	app := h.store.item("app", sqlc.RunItemKindDatabase)
	found := false
	for _, w := range app.res.Warnings {
		found = found || w.Code == "insufficient_privileges"
	}
	if !found {
		t.Fatalf("item warnings %+v", app.res.Warnings)
	}
	if len(store.rows) != 2 { // app + globals; shop had none
		t.Fatalf("backup_stats rows %d", len(store.rows))
	}
	file, _ := h.opener.dests[h.destA].object(h.store.manifests[0].Key)
	m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
	if err != nil {
		t.Fatal(err)
	}
	ms := decodeJSON(t, m.Stats)
	assertIntsAndStrings(t, ms, "$")
	dbs := asMap(t, ms["databases"])
	appStats := asMap(t, dbs["app"])
	if appStats["consistency"] != "snapshot" || ms["globals"] == nil || dbs["shop"] != nil {
		t.Fatalf("manifest stats %s", m.Stats)
	}
	if ex, ok := ms["databases_excluded"].([]any); !ok || len(ex) != 1 || ex[0] != "skipme" {
		t.Fatalf("manifest stats %s", m.Stats)
	}
	for _, w := range m.Warnings {
		if strings.HasPrefix(w, "databases_excluded") {
			t.Fatalf("manifest warning %q", w)
		}
	}
}

// TestBackupRunFailedDumpKeepsStatsWarnings: a dump that fails still
// records the runner's validated completeness warnings on its item (they
// often explain the failure), but no backup_stats row and no manifest stats.
func TestBackupRunFailedDumpKeepsStatsWarnings(t *testing.T) {
	h := newHarness(t)
	store := &fakeStatsStore{}
	h.opts.Stats = RunnerStats{Store: store}
	sc := h.runner.dumps["app"]
	sc.stats, sc.exit = pgDeepStats(), 1
	h.runner.dumps["app"] = sc
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := h.store.item("app", sqlc.RunItemKindDatabase)
	if app.res.Status != runstate.ItemFailed || app.res.Failure.Code != CodeDumpFailed {
		t.Fatalf("item %+v", app.res)
	}
	found := false
	for _, w := range app.res.Warnings {
		found = found || w.Code == "insufficient_privileges"
	}
	if !found {
		t.Fatalf("the failed item lost its completeness warnings: %+v", app.res.Warnings)
	}
	for _, r := range store.rows {
		if r.RunItemID == app.ID {
			t.Fatal("a failed dump got a backup_stats row")
		}
	}
	file, _ := h.opener.dests[h.destA].object(h.store.manifests[0].Key)
	m, err := pipeline.VerifySignature(file, pipeline.PublicKeys(h.signer.pub))
	if err != nil {
		t.Fatal(err)
	}
	if dbs, ok := decodeJSON(t, m.Stats)["databases"].(map[string]any); ok && dbs["app"] != nil {
		t.Fatalf("manifest stats of a failed dump: %s", m.Stats)
	}
	// Malformed stats of a failed dump still become stats_invalid.
	s := &runnerStatsSession{in: StatsInput{Engine: engine.KindPostgres, Kind: pipeline.KindDatabase, Database: "x"}}
	res, err := s.End(t.Context(), DumpOutcome{OK: false, Stats: &runnerpb.DumpStats{}})
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Code != "stats_invalid" || res.Stats != nil {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestBackupRunStatsAfterChunkIsProtocolError(t *testing.T) {
	h := newHarness(t)
	h.opts.Stats = RunnerStats{}
	sc := h.runner.dumps["app"]
	sc.stats, sc.statsLate = pgDeepStats(), true
	h.runner.dumps["app"] = sc
	if err := h.work(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := h.store.item("app", sqlc.RunItemKindDatabase)
	if app.res.Status != runstate.ItemFailed || app.res.Failure == nil || app.res.Failure.Code != CodeDumpIncomplete {
		t.Fatalf("item %+v", app.res)
	}
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %T", v)
	}
	return m
}
