package runner

import (
	"context"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// ScratchStats captures the statistics of a test-restore scratch database
// (task 4.6; the name is derived from temp_id).
func (s *service) ScratchStats(ctx context.Context, req *runnerpb.ScratchStatsRequest) (*runnerpb.ScratchStatsResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.scratchStats(ctx, req)
}

// DropScratchDatabase drops a test-restore scratch database (task 4.6; the
// name is derived from temp_id).
func (s *service) DropScratchDatabase(ctx context.Context, req *runnerpb.DropScratchDatabaseRequest) (*runnerpb.DropScratchDatabaseResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.dropScratch(ctx, req)
}

// NeutralizeRestoredDatabase hardens a restored PostgreSQL database
// (SECURITY DEFINER -> INVOKER, no PUBLIC CONNECT/TEMPORARY; D-171).
func (s *service) NeutralizeRestoredDatabase(ctx context.Context, req *runnerpb.NeutralizeRestoredDatabaseRequest) (*runnerpb.NeutralizeRestoredDatabaseResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.neutralize(ctx, req)
}
