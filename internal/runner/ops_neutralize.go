package runner

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Neutralizing a restored PostgreSQL database (M4 review, D-171): see
// dbprobe.Neutralizer. serve calls NeutralizeRestoredDatabase after
// pg_restore succeeded and before the swap (overwrite) or the success of a
// new-database or test restore; a failure aborts the restore. The runner
// derives the name (staging and scratch) or checks it (a new database: not
// a bekup name, owned by the connection's role), so the RPC cannot alter
// databases the restore role did not create.

const (
	// neutralizeTimeout bounds a NeutralizeRestoredDatabase call.
	neutralizeTimeout = 10 * time.Minute
	// maxNeutralizedListed bounds the signatures in a response.
	maxNeutralizedListed = 50
)

// neutralize implements NeutralizeRestoredDatabase.
func (o *operations) neutralize(ctx context.Context, req *runnerpb.NeutralizeRestoredDatabaseRequest) (resp *runnerpb.NeutralizeRestoredDatabaseResponse, err error) {
	a := newAudit("NeutralizeRestoredDatabase", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	if e := req.GetTarget().GetEngine(); e == runnerpb.Engine_ENGINE_MYSQL || e == runnerpb.Engine_ENGINE_MARIADB {
		return nil, status.Error(codes.FailedPrecondition, "neutralizing a restored database is PostgreSQL only")
	}
	d, err := o.val.NeutralizeRestoredDatabase(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(d.Target)
	a.database = d.Database
	ctx, cancel := context.WithTimeout(ctx, neutralizeTimeout)
	defer cancel()
	if _, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{needDB: []string{d.Database}}); err != nil {
		o.logCause(ctx, "NeutralizeRestoredDatabase", err)
		return nil, targetStatus(err)
	}
	conn, _, done, err := o.session(ctx, d.Target, req.GetTarget(), d.Database)
	defer done()
	if err != nil {
		o.logCause(ctx, "NeutralizeRestoredDatabase", err)
		return nil, targetStatus(err)
	}
	n, ok := conn.(dbprobe.Neutralizer)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "neutralizing a restored database is PostgreSQL only")
	}
	res, err := n.NeutralizeRestored(ctx, d.Database, maxNeutralizedListed)
	if err != nil {
		o.logCause(ctx, "NeutralizeRestoredDatabase", err)
		return nil, targetStatus(err)
	}
	a.extra = append(a.extra, slog.Int("neutralized", res.Count))
	return &runnerpb.NeutralizeRestoredDatabaseResponse{
		Database: d.Database, Neutralized: uint32(min(res.Count, dbprobe.MaxNeutralizeFunctions)), //nolint:gosec // bounded by MaxNeutralizeFunctions
		Functions: res.Functions,
	}, nil
}
