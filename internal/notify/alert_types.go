package notify

import (
	"fmt"
	"sort"
)

// SecurityAlertType describes one security.system alert kind for the
// admin reference (Query.securityAlertTypes): its fixed severity, what
// raises it and how often it repeats. The server decides; the list only
// documents it so clients need not hard-code it.
type SecurityAlertType struct {
	Kind        string
	Severity    Severity
	Description string
	// Repeat is how often the alert is sent ("every time", "once a day",
	// ...), from the deduplication rules of systemDedupKey, loginBurst and
	// internal/monitor/security.go.
	Repeat string
}

// alertDescriptions are the reference texts of the kinds whose inbox
// sentence carries values (systemBody); the others use systemMessages.
//
//nolint:gosec // G101: alert descriptions that mention API tokens, not credentials
var alertDescriptions = map[string]string{
	KindLoginBurstAddress:        fmt.Sprintf("At least %d failed sign-ins from one client address within %d minutes.", BurstPerAddress, int(BurstWindow.Minutes())),
	KindLoginBurstAccount:        fmt.Sprintf("At least %d failed sign-ins on one account within %d minutes.", BurstPerAccount, int(BurstWindow.Minutes())),
	KindLoginBurstGlobal:         fmt.Sprintf("At least %d failed sign-ins in total within %d minutes.", BurstGlobal, int(BurstWindow.Minutes())),
	KindCertExpiring:             "An internal certificate expires soon.",
	KindCertExpired:              "An internal certificate expired.",
	KindPinExpiring:              "The pinned certificate of a database connection expires soon.",
	KindPinExpired:               "The pinned certificate of a database connection expired.",
	KindAPIAddressDenied:         "A valid external API token was presented from an address outside its address allowlist and refused (it may be stolen); its creator is alerted too.",
	KindAPITokenCreated:          "An external API token was created (a machine credential that can start backups of its jobs).",
	KindAPITokenAnyAddress:       "An external API token may be used from any address (no client address allowlist), chosen when it was created or by an address change; a change names who made it and alerts the token's creator too.",
	KindAPITokenNeverExpires:     "An external API token was created without an expiry (valid until it is revoked).",
	KindRiskAccepted:             "A weakened setting was accepted as a risk for one month or one year; it is no longer flagged as an open issue (D-228).",
	KindRiskAcceptedPermanently:  "A weakened setting was accepted as a risk without an end date (D-228).",
	KindRiskAcceptanceExpiring:   "A risk acceptance ends within 7 days; the acceptor is alerted too.",
	KindRiskAcceptanceEnded:      "A risk acceptance ended (it expired, the condition changed or the acceptor lost the right) and the risk is flagged again; the acceptor is alerted too.",
	KindAPITokenAddressesWidened: "An address change lets an external API token be used from networks its old address list did not cover; it names who made the change, and the token's creator is alerted too.",
}

// alertRepeat is the repeat rule of the deduplicated kinds (every other
// kind is sent every time).
var alertRepeat = map[string]string{
	KindInsecureTransport:          "once a day",
	KindInsecureDBTLS:              "once a day",
	KindInsecureDBRoles:            "once a day",
	KindUnpinnedImage:              "once a day",
	KindServeAliasConflict:         "once per set of foreign addresses and day",
	KindExternalAPIAliasConflict:   "once per set of addresses and day",
	KindAuditHeadExportFailed:      "once a day",
	KindTLSPinMismatch:             "once per connection and day",
	KindSSHHostKeyMismatch:         "once per connection and day",
	KindDestinationHostKeyMismatch: "once per destination and day",
	KindDestinationTLSPinMismatch:  "once per destination and day",
	KindRetentionAborted:           "once per job, destination and day",
	KindDownloadRejected:           "once per download link",
	KindArtifactDeletedExternally:  "once per backup file",
	KindAuditVerifyFailed:          "once per finding",
	KindLoginBurstAddress:          fmt.Sprintf("once per client address and %d minutes", int(BurstWindow.Minutes())),
	KindLoginBurstAccount:          fmt.Sprintf("once per account and %d minutes", int(BurstWindow.Minutes())),
	KindLoginBurstGlobal:           fmt.Sprintf("once per %d minutes", int(BurstWindow.Minutes())),
	KindCertExpiring:               "once per certificate and warning threshold",
	KindCertExpired:                "once per certificate",
	KindPinExpiring:                "once per pin and warning threshold",
	KindPinExpired:                 "once per pin",
	KindConfigReloadWeakened:       "once per reload request and set of keys",
	KindAPIAddressDenied:           "once per API token and 10 minutes",
	KindRiskAcceptanceExpiring:     "once per acceptance",
}

var severityRank = map[Severity]int{SeverityCritical: 0, SeverityError: 1, SeverityWarning: 2, SeverityInfo: 3}

// SecurityAlertTypes returns every security.system alert kind, most
// severe first, then by kind.
func SecurityAlertTypes() []SecurityAlertType {
	out := make([]SecurityAlertType, 0, len(systemSeverity))
	for kind, sev := range systemSeverity {
		d, ok := alertDescriptions[kind]
		if !ok {
			d = systemMessages[kind]
		}
		r, ok := alertRepeat[kind]
		if !ok {
			r = "every time"
		}
		out = append(out, SecurityAlertType{Kind: kind, Severity: sev, Description: d, Repeat: r})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := severityRank[out[i].Severity], severityRank[out[j].Severity]; a != b {
			return a < b
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
