package runner

import (
	"context"
	"errors"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// discardErr marks errors of best-effort cleanup as intentionally ignored.
func discardErr(error) {}

// probe implements Probe: the pin pre-flight (PINNED), then the Go driver
// inside the runner detects the version and lists the databases. Errors
// are normalized (runnerpb.Reason).
func (o *operations) probe(ctx context.Context, req *runnerpb.ProbeRequest) (resp *runnerpb.ProbeResponse, err error) {
	a := newAudit("Probe", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	t, err := o.val.Probe(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(t)
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	conn, warns, done, err := o.session(ctx, t, req.GetTarget(), "")
	defer done()
	if err != nil {
		o.logCause(ctx, "Probe", err)
		return nil, targetStatus(err)
	}
	raw, version, err := conn.ServerVersion(ctx)
	if err != nil {
		o.logCause(ctx, "Probe", err)
		return nil, targetStatus(err)
	}
	dbs, skipped, err := conn.Databases(ctx)
	if err != nil {
		o.logCause(ctx, "Probe", err)
		return nil, targetStatus(err)
	}
	resp = &runnerpb.ProbeResponse{ServerVersion: raw, Databases: dbs}
	if skipped > 0 {
		warns = append(warns, warnSkippedNames)
	}

	cat := o.val.Catalog()
	switch detected, _, err := cat.SelectDumpClient(t.Kind, version); {
	case errors.Is(err, engine.ErrServerTooOld):
		warns = append(warns, warnServerTooOld)
	case err != nil:
		warns = append(warns, warnNoClient)
	default:
		resp.DetectedVersionKey = detected.Key()
	}
	if cw, err := compatWarnings(t.Entry, version); err == nil {
		resp.VersionKeyCompatible = true
		warns = append(warns, cw...)
	}
	resp.Warnings = dedup(warns)
	return resp, nil
}

// dedup removes repeated warning codes, keeping the first occurrence.
func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
