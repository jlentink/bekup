package audit

import (
	"net/netip"
	"os"
	"os/user"

	"github.com/google/uuid"
)

// ActorKind is who performed an action (the audit_log actor_kind CHECK).
type ActorKind string

// Actor kinds.
const (
	ActorUser   ActorKind = "user"
	ActorSystem ActorKind = "system"
	ActorCLI    ActorKind = "cli"
)

// Actor identifies who performed an action. UserID is set for ActorUser and
// optional for ActorCLI (a CLI action on behalf of a known account).
type Actor struct {
	Kind   ActorKind
	UserID *uuid.UUID
}

// UserActor is an authenticated user.
func UserActor(id uuid.UUID) Actor { return Actor{Kind: ActorUser, UserID: &id} }

// SystemActor is bekup itself (scheduler, workers, startup checks).
func SystemActor() Actor { return Actor{Kind: ActorSystem} }

// CLIActor is a host operator running a bekup subcommand (D-069).
func CLIActor() Actor { return Actor{Kind: ActorCLI} }

// Details are free-form, non-secret context for an entry. They are redacted
// (Redact) and stored as jsonb (at most 64 KiB).
type Details map[string]any

// Event is one audit entry to record.
type Event struct {
	Actor  Actor
	Action Action
	// TargetType and TargetID name the object acted on (optional; at most
	// 128 and 256 characters).
	TargetType string
	TargetID   string
	// IP is the client address (optional; zones and IPv4-in-IPv6 mapping
	// are stripped).
	IP      netip.Addr
	Details Details
}

// Detail keys added by CLIEvent.
const (
	DetailOSUser   = "os_user"
	DetailOSUID    = "os_uid"
	DetailHostname = "hostname"
)

// CLIEvent builds an event for a break-glass CLI action (D-069, `bekup
// admin`, task 1.6): actor kind cli, plus the OS user and hostname when
// available. Use Auditor.Record or RecordTx to store it with an auditor
// whose observer is notify.ObserveAudit (cmd/bekup newCLIAuditor): that
// raises the security.system alert in the same transaction (task 5.5).
func CLIEvent(action Action, targetType, targetID string, details Details) Event {
	d := make(Details, len(details)+3)
	for k, v := range details {
		d[k] = v
	}
	if u, err := user.Current(); err == nil {
		d[DetailOSUser] = u.Username
		d[DetailOSUID] = u.Uid
	}
	if h, err := os.Hostname(); err == nil {
		d[DetailHostname] = h
	}
	return Event{Actor: CLIActor(), Action: action, TargetType: targetType, TargetID: targetID, Details: d}
}
