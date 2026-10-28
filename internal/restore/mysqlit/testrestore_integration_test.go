//go:build integration

package mysqlit

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// dumpStats takes a dump with (deep) statistics: on MySQL/MariaDB the
// runner records estimates only (D-153).
func (in *myInner) dumpStats(t *testing.T, tg myTarget, db string) ([]byte, *runnerpb.DumpStats) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	s, err := in.rpc.Dump(ctx, &runnerpb.DumpRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: in.target(tg), Database: db,
		Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	var st *runnerpb.DumpStats
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, m.GetChunk().GetData()...)
		if m.GetStats() != nil {
			st = m.GetStats()
		}
	}
	if st == nil {
		t.Fatal("no stats")
	}
	return data, st
}

// testRestore is the MySQL/MariaDB part of a test restore (task 4.6): the
// dump restored into a runner-derived scratch database, its statistics
// read through ScratchStats (estimates only), compared with the source's
// (only warnings, never a row-count failure) and the scratch database
// dropped; names the runner did not derive are refused.
func (in *myInner) testRestore(t *testing.T, tg myTarget) {
	dump, st := in.dumpStats(t, tg, "shop")
	src, err := jobs.CanonicalStats(tg.Kind, "shop", st)
	if err != nil {
		t.Fatal(err)
	}
	scratchID := restore.TempID(uuid.New())
	scratch := engine.ScratchPrefix + scratchID
	if _, sink, err := in.restore(t, tg, tg.Kind, scratch, restore.ModeNewDB, restore.KindDatabase, dump); err != nil {
		t.Fatalf("restore: %v\n%s", err, sink.text())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	resp, err := in.rpc.ScratchStats(ctx, &runnerpb.ScratchStatsRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: in.target(tg),
		TempId: scratchID, Stats: &runnerpb.StatsOptions{Enabled: true, Level: runnerpb.StatsLevel_STATS_LEVEL_DEEP}})
	if err != nil || resp.GetDatabase() != scratch {
		t.Fatalf("scratch stats: %v %v", resp, err)
	}
	for _, tb := range resp.GetStats().GetTables() {
		if tb.GetCount() == runnerpb.RowCount_ROW_COUNT_EXACT || !tb.GetApproximate() {
			t.Fatalf("a scratch count is not an estimate: %v", tb)
		}
	}
	dst, err := jobs.CanonicalStats(tg.Kind, "shop", resp.GetStats())
	if err != nil {
		t.Fatal(err)
	}
	cmp := restore.CompareStats(tg.Kind, src, dst)
	t.Logf("comparison %s: %+v", cmp.Verdict, cmp.Findings)
	if cmp.Verdict == restore.VerdictFail {
		t.Fatalf("a MySQL/MariaDB test restore failed: %+v", cmp.Findings)
	}
	for _, f := range cmp.Findings {
		if f.Severity != restore.SeverityWarn || f.Code == restore.FindRowCount {
			t.Fatalf("finding %+v", f)
		}
	}
	// The seed has a MyISAM table: the source's completeness warning is
	// carried into the verdict.
	if cmp.Verdict != restore.VerdictWarn {
		t.Fatalf("verdict %s", cmp.Verdict)
	}

	drop := func(id string) (*runnerpb.DropScratchDatabaseResponse, error) {
		return in.rpc.DropScratchDatabase(ctx, &runnerpb.DropScratchDatabaseRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: in.target(tg), TempId: id})
	}
	for _, bad := range []string{"shop", "../shop", strings.ToUpper(scratchID), scratch} {
		if _, err := drop(bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("drop %q: %v", bad, err)
		}
	}
	if r, err := drop(scratchID); err != nil || !r.GetExisted() {
		t.Fatalf("drop: %v %v", r, err)
	}
	if out := in.sql(t, tg, "", "SHOW DATABASES LIKE '"+scratch+"'"); out != "" {
		t.Fatalf("scratch database still exists: %q", out)
	}
	if out := in.sql(t, tg, "", "SHOW DATABASES LIKE 'shop'"); out != "shop" {
		t.Fatalf("shop: %q", out)
	}
}
