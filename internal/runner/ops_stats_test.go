package runner

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// statsWorld is the fake database behind every statistics connection.
type statsWorld struct {
	mu        sync.Mutex
	catalog   dbprobe.Catalog
	rows      map[string]int64
	timeouts  map[string]bool
	denied    map[string]bool
	slow      time.Duration
	beginErr  error
	snapshot  string
	joined    []string
	ended     atomic.Int32
	counted   atomic.Int32
	globalErr error
	// unsafeCounts: the server cannot restrict relation kinds.
	unsafeCounts bool
	// version is the schema-version result; versionQueries records the
	// queries asked (D-210).
	version        dbprobe.VersionResult
	versionQueries []dbprobe.VersionQuery
}

type fakeStatsConn struct {
	*fakeConn
	w    *statsWorld
	open bool
}

func (c *fakeStatsConn) BeginStats(_ context.Context, export bool) (string, error) {
	if c.w.beginErr != nil {
		return "", c.w.beginErr
	}
	c.open = true
	if export {
		return c.w.snapshot, nil
	}
	return "", nil
}

func (c *fakeStatsConn) JoinStats(_ context.Context, snapshot string) error {
	c.w.mu.Lock()
	c.w.joined = append(c.w.joined, snapshot)
	c.w.mu.Unlock()
	c.open = true
	return nil
}

func (c *fakeStatsConn) Catalog(context.Context, string) (*dbprobe.Catalog, error) {
	cat := c.w.catalog
	cat.Tables = slices.Clone(cat.Tables)
	return &cat, nil
}

func (c *fakeStatsConn) CountRows(ctx context.Context, _ string, t dbprobe.Table, timeout time.Duration) (int64, error) {
	if !c.open {
		return 0, errors.New("no transaction")
	}
	c.w.counted.Add(1)
	if c.w.slow > 0 {
		select {
		case <-time.After(min(c.w.slow, timeout)):
			if timeout < c.w.slow {
				return 0, dbprobe.ErrCountTimeout
			}
		case <-ctx.Done():
			return 0, &dbprobe.Error{Reason: runnerpb.ReasonTimeout}
		}
	}
	switch {
	case c.w.timeouts[t.Name]:
		return 0, dbprobe.ErrCountTimeout
	case c.w.denied[t.Name]:
		return 0, &dbprobe.Error{Reason: runnerpb.ReasonPermission}
	}
	return c.w.rows[t.Name], nil
}

func (c *fakeStatsConn) SafeRowCounts() bool { return !c.w.unsafeCounts }

func (c *fakeStatsConn) GlobalCounts(context.Context) (int64, int64, error) {
	return 7, 1, c.w.globalErr
}

func (c *fakeStatsConn) SchemaVersion(_ context.Context, _ string, q dbprobe.VersionQuery) dbprobe.VersionResult {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	c.w.versionQueries = append(c.w.versionQueries, q)
	if !c.open {
		return dbprobe.VersionResult{Status: "unavailable", Detail: "no transaction"}
	}
	return c.w.version
}

func (c *fakeStatsConn) EndStats(context.Context) error {
	c.open = false
	c.w.ended.Add(1)
	return nil
}

// statsEnv routes the stats sessions (database set) to the fake world and
// the pre-checks (maintenance database) to the plain fake connection.
func statsEnv(t *testing.T, w *statsWorld, kind engine.Kind) *opsEnv {
	t.Helper()
	env := newOpsEnv(t)
	if kind != engine.KindPostgres {
		env.conn.raw, env.conn.version, env.conn.dbs = "8.4.3", engine.Version{Major: 8, Minor: 4, Patch: 3}, []string{"shop"}
	}
	env.openFn = func(_ context.Context, p dbprobe.Params) (dbprobe.Conn, error) {
		if p.Database == "" {
			return env.conn, nil
		}
		return &fakeStatsConn{fakeConn: env.conn, w: w}, nil
	}
	return env
}

func pgWorld() *statsWorld {
	return &statsWorld{
		snapshot: "00000003-0000001B-1",
		catalog: dbprobe.Catalog{
			Objects: dbprobe.ObjectCounts{Schemas: 2, Tables: 3, Views: 1, Sequences: 2, Functions: 4, Triggers: 1, Extensions: 1},
			Tables: []dbprobe.Table{
				{Schema: "public", Name: "a", Estimate: 90, Readable: true, Transactional: true},
				{Schema: "public", Name: "b", Estimate: -1, Readable: true, Transactional: true},
				{Schema: "private", Name: "c", Estimate: 5, Readable: false, Transactional: true},
			},
			TablesTotal: 3,
			NoPrivilege: dbprobe.Names{Count: 1, Objects: []string{"private.c"}},
		},
		rows:     map[string]int64{"a": 100, "b": 7},
		timeouts: map[string]bool{},
		denied:   map[string]bool{},
	}
}

func collectDump(t *testing.T, env *opsEnv, req *runnerpb.DumpRequest) ([]*runnerpb.DumpResponse, error) {
	t.Helper()
	var msgs []*runnerpb.DumpResponse
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	err := env.ops.dump(ctx, req, func(m *runnerpb.DumpResponse) error { msgs = append(msgs, m); return nil })
	return msgs, err
}

func warningByCode(ws []*runnerpb.StatsWarning, code string) *runnerpb.StatsWarning {
	for _, w := range ws {
		if w.GetCode() == code {
			return w
		}
	}
	return nil
}

func TestDumpStatsPostgresSameSnapshot(t *testing.T) {
	w := pgWorld()
	w.timeouts["b"] = true
	env := statsEnv(t, w, engine.KindPostgres)
	heldOpen := false
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			heldOpen = w.ended.Load() == 0
			return executor.Result{}, c.spec.Stdout(bytes.Repeat([]byte("D"), 100))
		},
	}
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil {
		t.Fatal(err)
	}
	// The stats come before the first chunk.
	statsAt, chunkAt := -1, -1
	for i, m := range msgs {
		if m.GetStats() != nil && statsAt < 0 {
			statsAt = i
		}
		if m.GetChunk() != nil && chunkAt < 0 {
			chunkAt = i
		}
	}
	if statsAt < 0 || chunkAt < 0 || statsAt > chunkAt {
		t.Fatalf("stats at %d, first chunk at %d", statsAt, chunkAt)
	}
	st := msgs[statsAt].GetStats()
	if !st.GetSameSnapshot() || st.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_DEEP || st.GetTablesTotal() != 3 || st.GetTablesTruncated() {
		t.Fatalf("stats = %v", st)
	}
	if st.GetObjects().GetTables() != 3 || st.GetObjects().GetFunctions() != 4 {
		t.Fatalf("objects = %v", st.GetObjects())
	}
	want := map[string]struct {
		rows  int64
		count runnerpb.RowCount
	}{"a": {100, runnerpb.RowCount_ROW_COUNT_EXACT}, "b": {0, runnerpb.RowCount_ROW_COUNT_UNKNOWN}, "c": {0, runnerpb.RowCount_ROW_COUNT_UNKNOWN}}
	for _, ts := range st.GetTables() {
		wt := want[ts.GetName()]
		if ts.GetRows() != wt.rows || ts.GetCount() != wt.count || ts.GetApproximate() {
			t.Fatalf("table %v", ts)
		}
	}
	if pw := warningByCode(st.GetWarnings(), statsWarnPrivileges); pw == nil || pw.GetCount() != 1 || pw.GetObjects()[0] != "private.c" {
		t.Fatalf("privilege warning %v", st.GetWarnings())
	}
	if tw := warningByCode(st.GetWarnings(), statsWarnTimeout); tw == nil || tw.GetObjects()[0] != "public.b" {
		t.Fatalf("timeout warning %v", st.GetWarnings())
	}
	argv := strings.Join(env.exec.Calls()[0].cmd.Argv, " ")
	if !strings.Contains(argv, "--snapshot=00000003-0000001B-1") {
		t.Fatalf("argv = %s", argv)
	}
	if !heldOpen || w.ended.Load() == 0 {
		t.Fatalf("snapshot transaction held during the dump: %v, ended %d", heldOpen, w.ended.Load())
	}
	if res := msgs[len(msgs)-1].GetResult(); res == nil || slices.Contains(res.GetWarnings(), warnStatsUnavailable) {
		t.Fatalf("result = %v", res)
	}
	if !strings.Contains(env.logs.String(), "stats_level=STATS_LEVEL_DEEP") {
		t.Fatalf("audit line without stats: %s", env.logs.String())
	}
}

// A server that cannot restrict relation kinds gets no row counts: no
// relation is named, the readable tables are reported as failed counts.
func TestDumpStatsUnsafeServerSkipsCounts(t *testing.T) {
	w := pgWorld()
	w.unsafeCounts = true
	env := statsEnv(t, w, engine.KindPostgres)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			return executor.Result{}, c.spec.Stdout([]byte("D"))
		},
	}
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil {
		t.Fatal(err)
	}
	var st *runnerpb.DumpStats
	for _, m := range msgs {
		if m.GetStats() != nil {
			st = m.GetStats()
		}
	}
	if st == nil || w.counted.Load() != 0 {
		t.Fatalf("stats %v, counted %d", st, w.counted.Load())
	}
	for _, ts := range st.GetTables() {
		if ts.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
			t.Fatalf("table %v", ts)
		}
	}
	fw := warningByCode(st.GetWarnings(), statsWarnCountFailed)
	if fw == nil || fw.GetCount() != 2 || slices.Contains(fw.GetObjects(), "private.c") {
		t.Fatalf("warnings %v", st.GetWarnings())
	}
	if pw := warningByCode(st.GetWarnings(), statsWarnPrivileges); pw == nil || pw.GetCount() != 1 {
		t.Fatalf("privilege warning %v", st.GetWarnings())
	}
	if !strings.Contains(env.logs.String(), "runner_stats_counts_disabled") {
		t.Fatalf("no log line: %s", env.logs.String())
	}
}

func TestDumpStatsStandardUsesEstimates(t *testing.T) {
	w := pgWorld()
	env := statsEnv(t, w, engine.KindPostgres)
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD}})
	if err != nil {
		t.Fatal(err)
	}
	st := msgs[0].GetStats()
	if st == nil || w.counted.Load() != 0 {
		t.Fatalf("stats %v, counted %d", st, w.counted.Load())
	}
	got := map[string]*runnerpb.TableStats{}
	for _, ts := range st.GetTables() {
		got[ts.GetName()] = ts
	}
	if got["a"].GetCount() != runnerpb.RowCount_ROW_COUNT_ESTIMATE || got["a"].GetRows() != 90 ||
		got["b"].GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN || got["c"].GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
		t.Fatalf("tables = %v", st.GetTables())
	}
}

// MySQL/MariaDB stats are estimates only, even when deep is requested
// (D-153): no row is counted, no user table is read.
func TestDumpStatsMySQLApproximate(t *testing.T) {
	w := &statsWorld{
		catalog: dbprobe.Catalog{
			Objects: dbprobe.ObjectCounts{Schemas: 1, Tables: 2, Events: 1},
			Tables: []dbprobe.Table{
				{Name: "m", Estimate: 2, Readable: true},
				{Name: "t", Estimate: 880, Readable: false, Transactional: true},
			},
			TablesTotal:      2,
			NonTransactional: dbprobe.Names{Count: 1, Objects: []string{"m"}},
			NoPrivilege:      dbprobe.Names{Count: 1, Objects: []string{"t"}},
		},
		rows:         map[string]int64{"m": 2, "t": 900},
		unsafeCounts: true,
	}
	env := statsEnv(t, w, engine.KindMySQL)
	closedBefore := false
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			closedBefore = w.ended.Load() == 1
			return executor.Result{}, c.spec.Stdout([]byte("-- Dump completed on x\n"))
		},
	}
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4"), Database: "shop",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil {
		t.Fatal(err)
	}
	st := msgs[0].GetStats()
	if st == nil || st.GetSameSnapshot() || st.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_STANDARD || w.counted.Load() != 0 {
		t.Fatalf("stats = %v, counted %d", st, w.counted.Load())
	}
	for _, ts := range st.GetTables() {
		if !ts.GetApproximate() {
			t.Fatalf("table not approximate: %v", ts)
		}
	}
	if st.GetTables()[0].GetRows() != 2 || st.GetTables()[0].GetCount() != runnerpb.RowCount_ROW_COUNT_ESTIMATE {
		t.Fatalf("m = %v", st.GetTables()[0])
	}
	if st.GetTables()[1].GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN || warningByCode(st.GetWarnings(), statsWarnCountFailed) != nil {
		t.Fatalf("t = %v, warnings %v", st.GetTables()[1], st.GetWarnings())
	}
	if nt := warningByCode(st.GetWarnings(), statsWarnNonTx); nt == nil || nt.GetObjects()[0] != "m" {
		t.Fatalf("non-transactional warning %v", st.GetWarnings())
	}
	if pw := warningByCode(st.GetWarnings(), statsWarnPrivileges); pw == nil || pw.GetObjects()[0] != "t" {
		t.Fatalf("privilege warning (from the grants) %v", st.GetWarnings())
	}
	if !closedBefore {
		t.Fatal("the MySQL stats transaction must end before the dump")
	}
	if strings.Contains(strings.Join(env.exec.Calls()[0].cmd.Argv, " "), "--snapshot") {
		t.Fatal("no snapshot for MySQL")
	}
}

func TestDumpStatsUnavailable(t *testing.T) {
	w := pgWorld()
	w.beginErr = &dbprobe.Error{Reason: runnerpb.ReasonFailed, Detail: "sqlstate=25006"}
	env := statsEnv(t, w, engine.KindPostgres)
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.GetStats() != nil {
			t.Fatal("stats despite the failure")
		}
	}
	if res := msgs[len(msgs)-1].GetResult(); !slices.Contains(res.GetWarnings(), warnStatsUnavailable) {
		t.Fatalf("result = %v", res)
	}
	if strings.Contains(strings.Join(env.exec.Calls()[0].cmd.Argv, " "), "--snapshot") {
		t.Fatal("snapshot without stats")
	}
	if !strings.Contains(env.logs.String(), "runner_stats_unavailable") {
		t.Fatal("failure not logged")
	}
}

func TestDumpStatsValidation(t *testing.T) {
	env := statsEnv(t, pgWorld(), engine.KindPostgres)
	for _, o := range []*runnerpb.StatsOptions{
		{Enabled: true},
		{Enabled: true, Level: runnerpb.StatsLevel(9)},
		{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, PerTableTimeoutMs: uint32(2 * time.Hour / time.Millisecond)},
		{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, BudgetMs: uint32(7 * time.Hour / time.Millisecond)},
	} {
		_, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app", Stats: o})
		wantReason(t, err, codes.InvalidArgument, "")
	}
	s, err := validate.StatsOptions(&runnerpb.StatsOptions{Enabled: false, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP})
	if err != nil || s != nil {
		t.Fatalf("disabled: %v %v", s, err)
	}
	s, err = validate.StatsOptions(&runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD, PerTableTimeoutMs: 1500})
	if err != nil || s.Deep || s.PerTable != 1500*time.Millisecond || s.Budget != validate.DefaultStatsBudget {
		t.Fatalf("standard: %+v %v", s, err)
	}
}

func TestCountTablesBudgetAndHelpers(t *testing.T) {
	w := pgWorld()
	w.catalog.Tables = nil
	for i := range 40 {
		name := "t" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		w.catalog.Tables = append(w.catalog.Tables, dbprobe.Table{Schema: "public", Name: name, Readable: true, Transactional: true})
	}
	w.catalog.TablesTotal = 40
	w.catalog.NoPrivilege = dbprobe.Names{}
	w.slow = 30 * time.Millisecond
	env := statsEnv(t, w, engine.KindPostgres)
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, PerTableTimeoutMs: 10, BudgetMs: 400}})
	if err != nil {
		t.Fatal(err)
	}
	st := msgs[0].GetStats()
	if st == nil {
		t.Fatal("no stats")
	}
	// Every count exceeds its 10 ms limit: unknown + timeout warning.
	tw := warningByCode(st.GetWarnings(), statsWarnTimeout)
	if tw == nil || len(tw.GetObjects()) == 0 {
		t.Fatalf("warnings = %v", st.GetWarnings())
	}
	for _, ts := range st.GetTables() {
		if ts.GetCount() != runnerpb.RowCount_ROW_COUNT_UNKNOWN {
			t.Fatalf("table %v", ts)
		}
	}
	w.mu.Lock()
	joined := slices.Clone(w.joined)
	w.mu.Unlock()
	if len(joined) != statsHelpers || joined[0] != w.snapshot {
		t.Fatalf("helpers joined %v", joined)
	}

	// A budget smaller than the counts: the rest is unknown (budget).
	w.slow = 50 * time.Millisecond
	msgs, err = collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP, PerTableTimeoutMs: 1000, BudgetMs: 120}})
	if err != nil {
		t.Fatal(err)
	}
	if bw := warningByCode(msgs[0].GetStats().GetWarnings(), statsWarnBudget); bw == nil || bw.GetCount() == 0 {
		t.Fatalf("budget warning %v", msgs[0].GetStats().GetWarnings())
	}
}

func TestBuildStatsTruncation(t *testing.T) {
	cat := &dbprobe.Catalog{TablesTotal: 25000}
	long := strings.Repeat("x", 60)
	for i := range dbprobe.MaxStatsTables {
		cat.Tables = append(cat.Tables, dbprobe.Table{Schema: long, Name: long + string(rune('a'+i%26)), Estimate: 1, Readable: true})
	}
	m := buildStats(engine.KindPostgres, false, cat, nil)
	if !m.GetTablesTruncated() || len(m.GetTables()) == 0 || len(m.GetTables()) >= dbprobe.MaxStatsTables || m.GetTablesTotal() != 25000 {
		t.Fatalf("tables %d truncated %v", len(m.GetTables()), m.GetTablesTruncated())
	}
	pw := warningByCode(m.GetWarnings(), statsWarnPartial)
	if pw == nil || int(pw.GetCount()) != 25000-len(m.GetTables()) {
		t.Fatalf("partial warning %v", m.GetWarnings())
	}
	if n := len(mustMarshal(t, m)); n > runnerpb.MaxMessageBytes {
		t.Fatalf("stats message %d bytes exceeds the gRPC cap", n)
	}
	// Warning object lists are bounded.
	var names dbprobe.Names
	for range 500 {
		names.Add("x")
	}
	if names.Count != 500 || len(names.Objects) != dbprobe.MaxWarningObjects {
		t.Fatalf("names %d/%d", names.Count, len(names.Objects))
	}
}

func TestDumpGlobalsStats(t *testing.T) {
	w := pgWorld()
	env := statsEnv(t, w, engine.KindPostgres)
	env.openFn = func(_ context.Context, _ dbprobe.Params) (dbprobe.Conn, error) {
		return &fakeStatsConn{fakeConn: env.conn, w: w}, nil
	}
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			return executor.Result{}, c.spec.Stdout([]byte("-- PostgreSQL database cluster dump complete\n"))
		},
	}
	var msgs []*runnerpb.DumpGlobalsResponse
	err := env.ops.dumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED),
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD}},
		func(m *runnerpb.DumpGlobalsResponse) error { msgs = append(msgs, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if st := msgs[0].GetStats(); st == nil || st.GetObjects().GetRoles() != 7 || st.GetObjects().GetTablespaces() != 1 {
		t.Fatalf("globals stats %v", msgs[0])
	}

	w.globalErr = errors.New("boom")
	msgs = nil
	err = env.ops.dumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED),
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD}},
		func(m *runnerpb.DumpGlobalsResponse) error { msgs = append(msgs, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if res := msgs[len(msgs)-1].GetResult(); !slices.Contains(res.GetWarnings(), warnStatsUnavailable) {
		t.Fatalf("result %v", res)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The schema version (D-210) travels in the stats message, is read inside
// the stats transaction and never reaches the runner log.
func TestDumpSchemaVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		q      *runnerpb.VersionQuery
		result dbprobe.VersionResult
		status runnerpb.VersionStatus
		value  string
	}{
		{"preset ok", &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "golang-migrate"}, Schema: "app"},
			dbprobe.VersionResult{Value: "42 (dirty)", Status: "ok"}, runnerpb.VersionStatus_VERSION_STATUS_OK, "42 (dirty)"},
		{"custom none", &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_CustomSql{CustomSql: "SELECT secret_marker FROM public.t"}},
			dbprobe.VersionResult{Status: "none", Detail: "no row"}, runnerpb.VersionStatus_VERSION_STATUS_NONE, ""},
		{"unavailable", &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}},
			dbprobe.VersionResult{Value: "ignored", Status: "unavailable", Detail: "sqlstate=42501"}, runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE, ""},
		{"invalid", &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}},
			dbprobe.VersionResult{Status: "invalid", Detail: "value"}, runnerpb.VersionStatus_VERSION_STATUS_INVALID, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pgWorld()
			w.version = tc.result
			env := statsEnv(t, w, engine.KindPostgres)
			env.exec.script = []func(context.Context, *call) (executor.Result, error){
				func(_ context.Context, c *call) (executor.Result, error) {
					return executor.Result{}, c.spec.Stdout([]byte("D"))
				},
			}
			msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
				Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD}, VersionQuery: tc.q})
			if err != nil {
				t.Fatal(err)
			}
			st := msgs[0].GetStats()
			if st == nil || st.GetVersionStatus() != tc.status || st.GetSchemaVersion() != tc.value {
				t.Fatalf("stats %v", st)
			}
			if len(w.versionQueries) != 1 {
				t.Fatalf("queries %v", w.versionQueries)
			}
			got := w.versionQueries[0]
			if p := tc.q.GetPreset(); p != "" && (got.Preset == nil || got.Preset.Name != p || got.Custom != "") {
				t.Fatalf("preset query %+v", got)
			}
			if tc.q.GetCustomSql() != "" && (got.Custom != "SELECT secret_marker FROM public.t" || got.Preset != nil) {
				t.Fatalf("custom query %+v", got)
			}
			logs := env.logs.String()
			if !strings.Contains(logs, "runner_schema_version") || strings.Contains(logs, "secret_marker") ||
				strings.Contains(logs, "42 (dirty)") || strings.Contains(logs, "ignored") {
				t.Fatalf("log: %s", logs)
			}
		})
	}
}

// A capture that leaves the statistics transaction unusable (failed
// savepoint rollback, broken connection) must not hand its snapshot to
// pg_dump: the dump runs with its own snapshot and the stats say so.
func TestDumpSchemaVersionSessionLost(t *testing.T) {
	w := pgWorld()
	w.version = dbprobe.VersionResult{Status: "unavailable", Detail: "rollback", SessionLost: true}
	env := statsEnv(t, w, engine.KindPostgres)
	endedBeforeDump := false
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			endedBeforeDump = w.ended.Load() > 0
			return executor.Result{}, c.spec.Stdout([]byte("D"))
		},
	}
	msgs, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		Stats:        &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_STANDARD},
		VersionQuery: &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}}})
	if err != nil {
		t.Fatal(err)
	}
	st := msgs[0].GetStats()
	if st == nil || st.GetSameSnapshot() || st.GetVersionStatus() != runnerpb.VersionStatus_VERSION_STATUS_UNAVAILABLE {
		t.Fatalf("stats %v", st)
	}
	if argv := strings.Join(env.exec.Calls()[0].cmd.Argv, " "); strings.Contains(argv, "--snapshot") {
		t.Fatalf("snapshot passed after a lost session: %s", argv)
	}
	if !endedBeforeDump {
		t.Fatal("the broken statistics session was kept open during the dump")
	}
}

func TestDumpSchemaVersionNeedsStats(t *testing.T) {
	env := statsEnv(t, pgWorld(), engine.KindPostgres)
	_, err := collectDump(t, env, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app",
		VersionQuery: &runnerpb.VersionQuery{Query: &runnerpb.VersionQuery_Preset{Preset: "goose"}}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err %v", err)
	}
}
