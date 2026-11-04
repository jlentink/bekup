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
	// ActorAPI is an external API token (D-220): the action was requested
	// with the token, not by a signed-in user.
	ActorAPI ActorKind = "api"
)

// ActorKinds returns every actor kind (the audit_log actor_kind CHECK).
func ActorKinds() []ActorKind { return []ActorKind{ActorUser, ActorSystem, ActorCLI, ActorAPI} }

// Actor identifies who performed an action. UserID is set for ActorUser and
// optional for ActorCLI (a CLI action on behalf of a known account); API is
// set for ActorAPI only.
type Actor struct {
	Kind   ActorKind
	UserID *uuid.UUID
	API    *APIRequester
}

// APIRequester names the API token behind an ActorAPI entry and the run
// request it acted on (none for api_token.ip_denied). It is stored in the
// entry's details (DetailAPITokenID, DetailAPITokenName, DetailRequestID);
// there is no actor column for it and never any part of the token itself.
type APIRequester struct {
	TokenID   uuid.UUID
	TokenName string
	RequestID uuid.UUID
}

// Detail keys added for ActorAPI entries.
const (
	DetailAPITokenID   = "api_token_id"
	DetailAPITokenName = "api_token_name"
	DetailRequestID    = "request_id"
)

// maxAPITokenName is the longest token name (api_tokens_name_length).
const maxAPITokenName = 100

// UserActor is an authenticated user.
func UserActor(id uuid.UUID) Actor { return Actor{Kind: ActorUser, UserID: &id} }

// SystemActor is bekup itself (scheduler, workers, startup checks).
func SystemActor() Actor { return Actor{Kind: ActorSystem} }

// CLIActor is a host operator running a bekup subcommand (D-069).
func CLIActor() Actor { return Actor{Kind: ActorCLI} }

// APIActor is the external API token tokenID (named tokenName) acting on
// its run request requestID (D-220). requestID is uuid.Nil only for the
// actions that concern the token itself (api_token.ip_denied).
func APIActor(tokenID uuid.UUID, tokenName string, requestID uuid.UUID) Actor {
	return Actor{Kind: ActorAPI, API: &APIRequester{TokenID: tokenID, TokenName: tokenName, RequestID: requestID}}
}

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
