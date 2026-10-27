package executor

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// Errors of the executor. Callers compare with errors.Is; Status maps them
// to gRPC statuses with static messages. Wrapped details (Docker errors,
// addresses) are for the runner's own log only.
var (
	// ErrInvalidSpec: the RunSpec is incomplete or inconsistent (a bug in
	// the calling RPC handler, never request data).
	ErrInvalidSpec = errors.New("executor: invalid run spec")
	// ErrNotReady: Docker is unreachable or the runner's own container has
	// not been detected yet.
	ErrNotReady = errors.New("executor: docker is not ready")
	// ErrBusy: limits.max_concurrent_runs runs are already active.
	ErrBusy = errors.New("executor: too many concurrent runs")
	// ErrDuplicateRun: a run with the same run id is active.
	ErrDuplicateRun = errors.New("executor: a run with this id is already active")
	// ErrPoolExhausted: no free /28 in docker.run_subnet_pool.
	ErrPoolExhausted = errors.New("executor: no free per-run subnet in docker.run_subnet_pool")
	// ErrTargetBusy: another active run maps the same IP-literal target
	// (or a direct target inside its per-run subnet).
	ErrTargetBusy = errors.New("executor: another active run uses the same target address range")
	// ErrUnmappableTarget: the target cannot be represented inside a
	// per-run network with host-name verification intact (D-132).
	ErrUnmappableTarget = errors.New("executor: verified TLS to an IP-literal or localhost target needs an SSH tunnel and an address that can be mapped into the per-run network")
	// ErrImage: the allowlisted image is missing and could not be pulled.
	ErrImage = errors.New("executor: image not available")
	// ErrTimeout: the run exceeded its timeout and the container was killed.
	ErrTimeout = errors.New("executor: run timed out")
	// ErrDocker: a Docker API call failed.
	ErrDocker = errors.New("executor: docker operation failed")
	// ErrUpstream: the per-run proxy or SSH tunnel could not be set up.
	ErrUpstream = errors.New("executor: target connection could not be set up")
	// ErrStdin: reading the restore payload failed; the container was
	// killed so that it never sees a clean end of input.
	ErrStdin = errors.New("executor: restore input failed")
	// ErrOutput: the stdout consumer failed (stream loss); the container
	// was killed.
	ErrOutput = errors.New("executor: output consumer failed")
)

// Status converts an error of Run or PrePull into a gRPC status error with
// a static message (never addresses, names or Docker messages).
func Status(err error) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
		return err
	}
	var te *tunnel.Error
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "run canceled")
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, ErrTimeout.Error())
	case errors.Is(err, ErrBusy), errors.Is(err, ErrPoolExhausted), errors.Is(err, ErrTargetBusy):
		return status.Error(codes.ResourceExhausted, errMsg(err, ErrBusy, ErrPoolExhausted, ErrTargetBusy))
	case errors.Is(err, ErrNotReady):
		return status.Error(codes.Unavailable, ErrNotReady.Error())
	case errors.Is(err, ErrUnmappableTarget):
		return status.Error(codes.FailedPrecondition, ErrUnmappableTarget.Error())
	case errors.Is(err, targets.ErrDenied):
		return status.Error(codes.PermissionDenied, targets.ErrDenied.Error())
	case errors.As(err, &te):
		// Normalized like the target errors (runnerpb reasons, D-134): a
		// host-key mismatch is a security event serve recognizes.
		switch {
		case errors.Is(err, tunnel.ErrHostKeyMismatch):
			return runnerpb.ReasonSSHHostKey.Status()
		case errors.Is(err, tunnel.ErrUnsupportedHostKey):
			return runnerpb.ReasonSSHHostKeyUnsupported.Status()
		case errors.Is(err, tunnel.ErrAuthFailed):
			return runnerpb.ReasonSSHAuthFailed.Status()
		default:
			return status.Error(codes.Unavailable, ErrUpstream.Error())
		}
	case errors.Is(err, ErrUpstream), errors.Is(err, targets.ErrResolve):
		return status.Error(codes.Unavailable, ErrUpstream.Error())
	case errors.Is(err, ErrImage):
		return status.Error(codes.Unavailable, ErrImage.Error())
	case errors.Is(err, ErrStdin):
		return status.Error(codes.Aborted, ErrStdin.Error())
	case errors.Is(err, ErrOutput):
		return status.Error(codes.Aborted, ErrOutput.Error())
	case errors.Is(err, ErrDuplicateRun):
		return status.Error(codes.AlreadyExists, ErrDuplicateRun.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

// errMsg returns the message of the first sentinel err matches.
func errMsg(err error, sentinels ...error) string {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s.Error()
		}
	}
	return "internal error"
}
