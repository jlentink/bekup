package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// The privilege re-check of the restore worker (M4 review, D-173). The
// API probes the target role when a restore is requested (D-168); the
// restore may wait in the queue for a long time, and the role may gain
// privileges meanwhile. Right before a restore is staged the worker probes
// the target again (the runner's Probe, through the slot holder's target
// load) and refuses it, audited, when the verdict is gated (privileged or
// unknown) unless
//
//   - the request recorded an admin's acknowledgement
//     (restores.privileged_target_acknowledged),
//   - every current reason is one the admin acknowledged
//     (restores.target_privilege_reasons), and
//   - the requester is still an active admin.
//
// Internal (test) restores skip this: the test-restore worker checks its
// scratch target against the job's stored acknowledgement itself.

// recheckPrivileges re-probes the target role of r before it starts.
func (w *Worker) recheckPrivileges(ctx context.Context, log *slog.Logger, r sqlc.Restore, conn connections.Connection) error {
	now, err := w.probePrivileges(ctx, r, conn)
	if err != nil {
		return err
	}
	if !now.Gated() {
		return nil
	}
	ok, why, err := w.acknowledgedFor(ctx, r, now)
	if err != nil {
		return err
	}
	if ok {
		log.InfoContext(ctx, "restore target role is privileged; covered by the acknowledgement of the request",
			slog.String("target_privileges", string(now.Status)))
		return nil
	}
	requested := ""
	if r.TargetPrivileges != nil {
		requested = *r.TargetPrivileges
	}
	if aerr := w.audit(ctx, audit.ActionRestorePrivilegedTarget, r, audit.Details{
		"requested_privileges": requested, "requested_privilege_reasons": slices.Clone(r.TargetPrivilegeReasons),
		"privileged_target_acknowledged": r.PrivilegedTargetAcknowledged,
		"target_privileges":              string(now.Status), "target_privilege_reasons": slices.Clone(now.Reasons),
		"refusal": why,
	}); aerr != nil {
		log.ErrorContext(ctx, "recording restore.privileged_target_refused failed", slog.Any("error", aerr))
	}
	log.WarnContext(ctx, "restore refused: the target role became privileged after the request", slog.String("refusal", why))
	w.o.Service.o.Alert(ctx, Alert{Kind: AlertPrivilegedTarget, RestoreID: r.ID, JobID: r.JobID, ArtifactID: r.ArtifactID,
		Details: map[string]any{
			"target_connection_id": r.TargetConnectionID.String(), "target_privileges": string(now.Status),
			"reasons": slices.Clone(now.Reasons), "refused": true, "refusal": why,
		}})
	return ErrPrivilegedTarget
}

// probePrivileges reads the target role's current verdict: unknown
// (target_not_ready) for a target that is not ready, else one runner
// Probe. Pin and host-key mismatches are recorded as security events.
func (w *Worker) probePrivileges(ctx context.Context, r sqlc.Restore, conn connections.Connection) (connections.Privileges, error) {
	if !conn.Ready {
		return connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonTargetNotReady}}, nil
	}
	t, release, err := w.o.Targets.RunningRestoreTarget(ctx, r.TargetConnectionID, r.ID)
	if err != nil {
		var nr *connections.NotReadyError
		switch {
		case errors.As(err, &nr):
			return connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{ReasonTargetNotReady}}, nil
		case errors.Is(err, connections.ErrNotFound):
			return connections.Privileges{}, ErrTargetUnavailable
		default:
			return connections.Privileges{}, err
		}
	}
	unreg := registerTargetSecrets(w.o.Registry, t)
	resp, err := w.o.Runner.Probe(ctx, &runnerpb.ProbeRequest{Ctx: &runnerpb.RunContext{RunId: r.ID.String()}, Target: t})
	release()
	unreg()
	if err != nil {
		if rr := runnerpb.ErrorReason(err); rr == runnerpb.ReasonPinMismatch || rr == runnerpb.ReasonSSHHostKey {
			if rerr := w.o.Targets.RecordRunFailure(context.WithoutCancel(ctx), r.TargetConnectionID, runOpRestore, err); rerr != nil {
				return connections.Privileges{}, fmt.Errorf("%w: %w", ErrAuditFailed, rerr)
			}
		}
		return connections.Privileges{}, err
	}
	return connections.PrivilegesFromRunner(resp.GetPrivileges()), nil
}

// acknowledgedFor reports whether r carries an acknowledgement covering
// the gated verdict now, and why not.
func (w *Worker) acknowledgedFor(ctx context.Context, r sqlc.Restore, now connections.Privileges) (bool, string, error) {
	if !r.PrivilegedTargetAcknowledged || r.RequestedBy == nil {
		return false, "not_acknowledged", nil
	}
	for _, reason := range now.Reasons {
		if !slices.Contains(r.TargetPrivilegeReasons, reason) {
			return false, "new_reasons", nil
		}
	}
	u, err := sqlc.New(w.o.Service.o.DB).GetUserByID(ctx, *r.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "requester_not_admin", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("restore: load the requester: %w", err)
	}
	if u.Role != sqlc.UserRole(auth.RoleAdmin) || u.Disabled || u.DeletedAt != nil {
		return false, "requester_not_admin", nil
	}
	return true, "", nil
}
