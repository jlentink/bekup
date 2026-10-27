package runner

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// hostKeyTimeout bounds one FetchHostKey call (dial + key exchange).
const hostKeyTimeout = 30 * time.Second

// fetchHostKey implements FetchHostKey (task 2.11): the bastion passes the
// target allowlist (validator, then the guard's dial-time check), the SSH
// key exchange runs only until the server presented its host key, and no
// authentication is attempted. The key is returned for an admin's
// out-of-band confirmation (TOFU, D-122); the runner never stores it.
// Failures other than an unsupported key type or a denied address are all
// ReasonUnreachable (no port scanning through the runner).
func (o *operations) fetchHostKey(ctx context.Context, req *runnerpb.FetchHostKeyRequest) (resp *runnerpb.FetchHostKeyResponse, err error) {
	a := newAudit("FetchHostKey", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	ep, err := o.val.FetchHostKey(ctx, req)
	if err != nil {
		return nil, err
	}
	a.extra = append(a.extra, slog.String("bastion", ep.Host), slog.Int("bastion_port", ep.Port))
	ctx, cancel := context.WithTimeout(ctx, hostKeyTimeout)
	defer cancel()
	info, err := o.hostKey(ctx, ep.Host, ep.Port, o.val.Guard())
	if err != nil {
		var te *tunnel.Error
		if errors.As(err, &te) {
			o.log.LogAttrs(ctx, slog.LevelInfo, "runner_target_error",
				slog.String("rpc", "FetchHostKey"), slog.String("detail", te.Kind.Error()))
		}
		return nil, hostKeyStatus(err)
	}
	a.extra = append(a.extra, slog.String("host_key_type", info.Type), slog.String("host_key_fingerprint", info.FingerprintSHA256))
	return &runnerpb.FetchHostKeyResponse{
		Type: info.Type, FingerprintSha256: info.FingerprintSHA256, AuthorizedKey: info.AuthorizedKey,
	}, nil
}

// hostKeyStatus maps a tunnel.FetchHostKey error to a normalized status.
func hostKeyStatus(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "call canceled")
	case errors.Is(err, targets.ErrDenied):
		return status.Error(codes.PermissionDenied, targets.ErrDenied.Error())
	case errors.Is(err, tunnel.ErrUnsupportedHostKey):
		return runnerpb.ReasonSSHHostKeyUnsupported.Status()
	default:
		// Refused, filtered, timeouts, handshake failures and non-SSH
		// services look the same.
		return runnerpb.ReasonUnreachable.Status()
	}
}
