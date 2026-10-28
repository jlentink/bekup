package runner

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

const scratchID = "0123456789ab"

func deepStats() *runnerpb.StatsOptions {
	return &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}
}

func TestScratchStats(t *testing.T) {
	t.Run("postgres deep counts in the derived database", func(t *testing.T) {
		w := pgWorld()
		env := statsEnv(t, w, engine.KindPostgres)
		env.conn.dbs = []string{"app", "postgres", "bekup_test_" + scratchID}
		resp, err := env.ops.scratchStats(t.Context(), &runnerpb.ScratchStatsRequest{
			Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: scratchID, Stats: deepStats(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetDatabase() != "bekup_test_"+scratchID || resp.GetStats().GetSameSnapshot() {
			t.Fatalf("resp %v", resp)
		}
		st := resp.GetStats()
		if st.GetLevel() != runnerpb.StatsLevel_STATS_LEVEL_DEEP || st.GetObjects().GetTables() != 3 {
			t.Fatalf("stats %v", st)
		}
		var exact int
		for _, tb := range st.GetTables() {
			if tb.GetCount() == runnerpb.RowCount_ROW_COUNT_EXACT {
				exact++
			}
		}
		if exact != 2 {
			t.Fatalf("exact counts %d: %v", exact, st.GetTables())
		}
		if w.ended.Load() == 0 {
			t.Fatal("the statistics transaction was not ended")
		}
		env.mu.Lock()
		defer env.mu.Unlock()
		for _, p := range env.opened {
			if p.Database != "" && p.Database != "bekup_test_"+scratchID {
				t.Fatalf("session to %q", p.Database)
			}
		}
		if len(env.exec.Calls()) != 0 {
			t.Fatal("a container ran")
		}
	})
	t.Run("mysql estimates only", func(t *testing.T) {
		w := pgWorld()
		w.catalog.Tables = w.catalog.Tables[:2]
		for i := range w.catalog.Tables {
			w.catalog.Tables[i].Schema = ""
		}
		env := statsEnv(t, w, engine.KindMySQL)
		env.conn.dbs = []string{"shop", "bekup_test_" + scratchID}
		resp, err := env.ops.scratchStats(t.Context(), &runnerpb.ScratchStatsRequest{
			Ctx: rc(), Target: myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4"), TempId: scratchID, Stats: deepStats(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if w.counted.Load() != 0 {
			t.Fatal("a MySQL user table was counted")
		}
		for _, tb := range resp.GetStats().GetTables() {
			if tb.GetCount() == runnerpb.RowCount_ROW_COUNT_EXACT || !tb.GetApproximate() {
				t.Fatalf("table %v", tb)
			}
		}
	})
	t.Run("missing scratch database", func(t *testing.T) {
		env := statsEnv(t, pgWorld(), engine.KindPostgres)
		_, err := env.ops.scratchStats(t.Context(), &runnerpb.ScratchStatsRequest{
			Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: scratchID, Stats: deepStats(),
		})
		wantReason(t, err, codes.NotFound, runnerpb.ReasonDatabaseNotFound)
	})
	t.Run("rejects", func(t *testing.T) {
		env := statsEnv(t, pgWorld(), engine.KindPostgres)
		for i, r := range []*runnerpb.ScratchStatsRequest{
			{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: "app", Stats: deepStats()},
			{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: "0123456789AB", Stats: deepStats()},
			{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: scratchID},
			{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: scratchID + "x", Stats: deepStats()},
		} {
			if _, err := env.ops.scratchStats(t.Context(), r); status.Code(err) != codes.InvalidArgument {
				t.Errorf("case %d: %v", i, err)
			}
		}
	})
}

func TestDropScratchDatabase(t *testing.T) {
	t.Run("derived name only", func(t *testing.T) {
		env := newOpsEnv(t)
		env.conn.dbs = []string{"app", "bekup_test_" + scratchID}
		resp, err := env.ops.dropScratch(t.Context(), &runnerpb.DropScratchDatabaseRequest{
			Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: scratchID,
		})
		if err != nil || !resp.GetExisted() {
			t.Fatalf("%v %v", resp, err)
		}
		calls := env.exec.Calls()
		if len(calls) != 1 {
			t.Fatalf("calls = %d", len(calls))
		}
		if a := strings.Join(calls[0].cmd.Argv, " "); !strings.Contains(a, `DROP DATABASE IF EXISTS "bekup_test_`+scratchID+`"`) {
			t.Fatalf("argv = %v", calls[0].cmd.Argv)
		}
		// Already gone: still a success, existed false.
		env.conn.dbs = []string{"app"}
		resp, err = env.ops.dropScratch(t.Context(), &runnerpb.DropScratchDatabaseRequest{
			Ctx: rc(), Target: myTarget(runnerpb.Engine_ENGINE_MARIADB, "11.4"), TempId: scratchID,
		})
		if err != nil || resp.GetExisted() {
			t.Fatalf("%v %v", resp, err)
		}
	})
	t.Run("a hostile name cannot be passed", func(t *testing.T) {
		env := newOpsEnv(t)
		for i, id := range []string{"", "app", "../app", "0123456789a", "0123456789ab;DROP", "app__bekup_restore_r1", "0123456789AB"} {
			_, err := env.ops.dropScratch(t.Context(), &runnerpb.DropScratchDatabaseRequest{
				Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), TempId: id,
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("case %d (%q): %v", i, id, err)
			}
		}
		if len(env.exec.Calls()) != 0 {
			t.Fatal("container ran")
		}
	})
}
