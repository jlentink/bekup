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
var alertDescriptions = map[string]string{
	KindLoginBurstAddress: fmt.Sprintf("At least %d failed sign-ins from one client address within %d minutes.", BurstPerAddress, int(BurstWindow.Minutes())),
	KindLoginBurstAccount: fmt.Sprintf("At least %d failed sign-ins on one account within %d minutes.", BurstPerAccount, int(BurstWindow.Minutes())),
	KindLoginBurstGlobal:  fmt.Sprintf("At least %d failed sign-ins in total within %d minutes.", BurstGlobal, int(BurstWindow.Minutes())),
	KindCertExpiring:      "An internal certificate expires soon.",
	KindCertExpired:       "An internal certificate expired.",
	KindPinExpiring:       "The pinned certificate of a database connection expires soon.",
	KindPinExpired:        "The pinned certificate of a database connection expired.",
}

// alertRepeat is the repeat rule of the deduplicated kinds (every other
// kind is sent every time).
var alertRepeat = map[string]string{
	KindInsecureTransport:          "once a day",
	KindInsecureDBTLS:              "once a day",
	KindInsecureDBRoles:            "once a day",
	KindUnpinnedImage:              "once a day",
	KindAuditHeadExportFailed:      "once a day",
	KindTLSPinMismatch:             "once per connection and day",
	KindSSHHostKeyMismatch:         "once per connection and day",
	KindDestinationHostKeyMismatch: "once per destination and day",
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
