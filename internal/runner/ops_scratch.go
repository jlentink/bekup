package runner

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Test-restore scratch databases (task 4.6). serve restores a backup into a
// new database bekup_test_<temp_id> (CreateDatabase + Restore), then asks
// the runner for the scratch database's statistics (ScratchStats: the
// same catalog reads and counts as a Dump's stats, through the same pinned,
// hostile-safe Go-driver session, D-151/D-152/D-153) and always drops it
// (DropScratchDatabase). Both RPCs take only the temp_id: the runner
// derives the name, so neither can inspect or drop any other database.

// scratchStatsTimeout bounds a ScratchStats call beyond the stats budget
// (session setup, catalog reads).
const scratchStatsTimeout = 5 * time.Minute

// scratchStats implements ScratchStats.
func (o *operations) scratchStats(ctx context.Context, req *runnerpb.ScratchStatsRequest) (resp *runnerpb.ScratchStatsResponse, err error) {
	a := newAudit("ScratchStats", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	d, err := o.val.ScratchStats(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(d.Target)
	a.database = d.Database
	ctx, cancel := context.WithTimeout(ctx, d.Stats.Budget+scratchStatsTimeout)
	defer cancel()
	if _, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{needDB: []string{d.Database}}); err != nil {
		o.logCause(ctx, "ScratchStats", err)
		return nil, targetStatus(err)
	}
	sc := o.captureStats(ctx, d, req.GetTarget())
	if sc == nil {
		return nil, runnerpb.ReasonFailed.Status()
	}
	// A scratch database has no concurrent dump: the statistics
	// transaction ends at once.
	sc.close(ctx)
	// The PostgreSQL snapshot export is meaningless here.
	sc.msg.SameSnapshot = false
	a.extra = append(a.extra, statsAudit(sc.msg)...)
	return &runnerpb.ScratchStatsResponse{Stats: sc.msg, Database: d.Database}, nil
}

// dropScratch implements DropScratchDatabase: DROP DATABASE IF EXISTS of
// the derived scratch name (WITH (FORCE) on PostgreSQL 13+); the engine
// builder refuses any name that is not a bekup-derived one.
func (o *operations) dropScratch(ctx context.Context, req *runnerpb.DropScratchDatabaseRequest) (resp *runnerpb.DropScratchDatabaseResponse, err error) {
	a := newAudit("DropScratchDatabase", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	d, err := o.val.DropScratchDatabase(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(d.Target)
	a.database = d.Database
	if o.exec == nil {
		return nil, errNoExecutor
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	chk, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{})
	if err != nil {
		o.logCause(ctx, "DropScratchDatabase", err)
		return nil, targetStatus(err)
	}
	existed := slices.Contains(chk.databases, d.Database)
	a.extra = append(a.extra, slog.Bool("existed", existed))
	code, err := o.statement(ctx, req.GetCtx().GetRunId(), "dropscratch", d.Target, req.GetTarget(), chk.version,
		func(et engine.Target) (engine.Command, error) {
			return d.Engine.DropDatabase(d.Entry, et, d.Database)
		})
	a.exitCode = &code
	if err != nil {
		return nil, err
	}
	return &runnerpb.DropScratchDatabaseResponse{Existed: existed}, nil
}
