package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// certTimeout bounds a certificate capture (dial, upgrade, handshake).
const certTimeout = time.Minute

// fetchServerCertificate implements FetchServerCertificate: validate the
// request (target allowlist, tunnel), open the upstream like every other
// RPC, capture the chain without sending credentials (pinning.Checker.Fetch)
// and describe it. Errors are normalized like the other target RPCs
// (runnerpb reasons, targetStatus) and the call is audited.
func (o *operations) fetchServerCertificate(ctx context.Context, req *runnerpb.FetchServerCertificateRequest) (resp *runnerpb.FetchServerCertificateResponse, err error) {
	a := newAudit("FetchServerCertificate", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	v, err := o.val.FetchServerCertificate(ctx, req)
	if err != nil {
		return nil, err
	}
	a.engine = v.Kind
	a.extra = append(a.extra, slog.Bool("ssh_tunnel", v.SSH != nil))
	o.warnUnrestrictedBastion(ctx, v.SSH)
	ctx, cancel := context.WithTimeout(ctx, certTimeout)
	defer cancel()
	dial, done, err := o.connect(ctx, pinning.Upstream{
		Host: v.Host, Port: v.Port, Guard: o.val.Guard(), SSH: v.SSH,
		Tunnel: req.GetSshTunnel(), Logger: o.log,
	})
	defer done()
	if err != nil {
		return nil, targetStatus(err)
	}
	chain, err := o.fetch(ctx, pinning.Target{Kind: v.Kind, Host: v.Host, Dial: dial})
	if err != nil {
		return nil, targetStatus(err)
	}
	resp = pinning.Describe(chain)
	a.extra = append(a.extra, slog.Int("chain_length", len(chain)))
	return resp, nil
}
