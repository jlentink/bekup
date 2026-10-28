package connections

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Privileged restore roles (task 4.8, D-068). SQL inside a restored dump
// runs with the connection role's rights; the runner's Probe classifies
// the role. An older runner (no verdict) and an incomplete detection are
// both "unknown", which callers treat as privileged (fail closed).

// PrivilegeStatus is the verdict on a connection's role.
type PrivilegeStatus string

// Verdicts.
const (
	PrivilegeNotPrivileged PrivilegeStatus = "not_privileged"
	PrivilegePrivileged    PrivilegeStatus = "privileged"
	PrivilegeUnknown       PrivilegeStatus = "unknown"
)

// Reason codes added by serve.
const (
	// ReasonNoVerdict: the runner sent no verdict.
	ReasonNoVerdict = "no_verdict"
	// ReasonInvalidReason replaces a reason outside the expected syntax.
	ReasonInvalidReason = "invalid_reason"
)

// Bounds of a verdict taken from the runner.
const (
	maxPrivilegeReasons = 32
	maxPrivilegeReason  = 64
)

// Privileges is the verdict on a connection's role.
type Privileges struct {
	Status PrivilegeStatus
	// Reasons are stable codes (e.g. "superuser", "global:FILE", "global_data:SELECT",
	// "member_of:pg_execute_server_program", "roles_granted").
	Reasons []string
}

// Gated reports whether restores into the connection need an admin and an
// acknowledgement (privileged or unknown).
func (p Privileges) Gated() bool { return p.Status != PrivilegeNotPrivileged }

// PrivilegesFromRunner converts and bounds the runner's verdict. Reasons
// are kept only if they consist of printable ASCII letters, digits and
// " _:-" (they are shown in the UI and written to the audit log).
func PrivilegesFromRunner(v *runnerpb.TargetPrivileges) Privileges {
	out := Privileges{Status: PrivilegeUnknown, Reasons: []string{}}
	switch v.GetStatus() {
	case runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED:
		out.Status = PrivilegeNotPrivileged
	case runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED:
		out.Status = PrivilegePrivileged
	case runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN:
	default:
		out.Reasons = append(out.Reasons, ReasonNoVerdict)
		return out
	}
	for _, r := range v.GetReasons() {
		if len(out.Reasons) >= maxPrivilegeReasons {
			break
		}
		if !validReason(r) {
			r = ReasonInvalidReason
		}
		if !slices.Contains(out.Reasons, r) {
			out.Reasons = append(out.Reasons, r)
		}
	}
	if out.Status == PrivilegeNotPrivileged && len(out.Reasons) > 0 {
		// Inconsistent answer: fail closed.
		out.Status = PrivilegeUnknown
	}
	if out.Status != PrivilegeNotPrivileged && len(out.Reasons) == 0 {
		out.Reasons = append(out.Reasons, ReasonNoVerdict)
	}
	return out
}

func validReason(r string) bool {
	if r == "" || len(r) > maxPrivilegeReason {
		return false
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == ':' || c == ' ' || c == '-':
		default:
			return false
		}
	}
	return true
}

// TargetProbe is what a restore needs to know about a target: its
// databases, the runner's warnings and the role's privilege verdict.
type TargetProbe struct {
	Databases  []string
	Warnings   []string
	Privileges Privileges
}

// ProbeTarget runs the runner's Probe for a restore target (temporary
// connections when actor.AllowTemporary): one rate-limited runner call.
func (s *Service) ProbeTarget(ctx context.Context, actor Actor, id uuid.UUID) (TargetProbe, error) {
	ctx, row, done, err := s.begin(ctx, actor, id)
	if err != nil {
		return TargetProbe{}, err
	}
	defer done()
	resp, err := s.probe(ctx, actor, &row, "probe_restore_target")
	if err != nil {
		return TargetProbe{}, err
	}
	return TargetProbe{
		Databases: resp.GetDatabases(), Warnings: resp.GetWarnings(), Privileges: PrivilegesFromRunner(resp.GetPrivileges()),
	}, nil
}
