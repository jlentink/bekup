package validate

import (
	"context"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Test-restore scratch databases (task 4.6). Requests carry only the
// scratch id; the validator derives the name bekup_test_<id>
// (engine.ScratchDatabaseName), so no request can point the scratch RPCs at
// another database.

// scratchName derives and checks the scratch database name of a temp_id.
func scratchName(t *Target, id string) (string, error) {
	name, err := engine.ScratchDatabaseName(id)
	if err != nil || !engine.IsBekupScratchDatabase(name) || t.Engine.IsSystemDatabase(name) || engine.ValidateDBName(name) != nil {
		return "", fieldError("temp_id", "must be [a-z0-9]{12}")
	}
	return name, nil
}

// ScratchStats validates a ScratchStatsRequest: the derived scratch
// database and enabled statistics options.
func (v *Validator) ScratchStats(ctx context.Context, req *runnerpb.ScratchStatsRequest) (*DatabaseOp, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	name, err := scratchName(t, req.GetTempId())
	if err != nil {
		return nil, err
	}
	st, err := StatsOptions(req.GetStats())
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fieldError("stats", "statistics must be enabled")
	}
	return &DatabaseOp{Target: t, Database: name, Stats: st}, nil
}

// DropScratchDatabase validates a DropScratchDatabaseRequest and derives
// the name to drop.
func (v *Validator) DropScratchDatabase(ctx context.Context, req *runnerpb.DropScratchDatabaseRequest) (*DatabaseOp, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	name, err := scratchName(t, req.GetTempId())
	if err != nil {
		return nil, err
	}
	return &DatabaseOp{Target: t, Database: name}, nil
}
