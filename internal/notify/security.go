package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Security alerts (task 5.5, D-190). Every security-relevant audited
// action becomes a mandatory security.system (admins) or security.account
// (the affected user) event through ObserveAudit, in the audit entry's
// transaction; the periodic security checks (internal/monitor/security.go)
// and the failed-login burst detection add the conditions no single
// action reports. This file holds the mapping beyond the D-183 base set, the
// severities and the deduplication keys.

// System (security.system) kinds added by task 5.5.
const (
	KindJobOwnerAdded               = "job_owner_added"
	KindJobOwnerRemoved             = "job_owner_removed"
	KindPassphrasesExported         = "passphrases_exported"
	KindArtifactDeletedExternally   = "artifact_deleted_externally"
	KindRetentionAborted            = "retention_aborted"
	KindRestorePrivilegedTarget     = "restore_privileged_target"
	KindRestorePrivilegedRefused    = "restore_privileged_refused"
	KindTestRestorePrivilegedTarget = "test_restore_privileged_target"
	// KindJobCustomVersionQuery: a job's custom schema-version query was
	// set or changed, or the job moved to another connection (D-210:
	// user-defined SQL that runs on the target with the backup role's
	// rights).
	KindJobCustomVersionQuery   = "job_custom_version_query"
	KindSecdefNeutralized       = "secdef_neutralized"
	KindTwoFactorPolicyWeakened = "two_factor_policy_weakened"
	KindPasskeyCloneSuspected   = "passkey_clone_suspected" //nolint:gosec // G101: an alert kind, not a credential
	KindDownloadRejected        = "download_rejected"
	KindInsecureTransport       = "insecure_transport"
	KindInsecureDBTLS           = "insecure_db_tls"
	KindInsecureDBRoles         = "insecure_db_roles"
	// KindUnpinnedImage: serve or worker runs a bekup image that follows
	// a tag instead of a digest (BEKUP_IMAGE_MODE=following, D-212).
	KindUnpinnedImage = "unpinned_image"
	// KindServeAliasConflict: serve's network alias bekup-serve also
	// resolves to another container on the own proxy's network (D-215).
	KindServeAliasConflict = "serve_alias_conflict"
	// KindExternalAPIAliasConflict: the external API's network alias
	// bekup-external-api resolves to more than one container (or to
	// serve) on the own proxy's network (L2 of the D-220 review).
	KindExternalAPIAliasConflict = "external_api_alias_conflict"
	KindAuditHeadExportFailed    = "audit_head_export_failed"
	KindCLIAction                = "cli_action"
	KindLoginBurstAddress        = "login_burst_address"
	KindLoginBurstAccount        = "login_burst_account"
	KindLoginBurstGlobal         = "login_burst_global"
	KindCertExpiring             = "cert_expiring"
	KindCertExpired              = "cert_expired"
	KindPinExpiring              = "pin_expiring"
	KindPinExpired               = "pin_expired"
	// KindSSRFAllowlistWidened: an admin added entries to the runtime
	// SSRF allowlist (D-196).
	KindSSRFAllowlistWidened = "ssrf_allowlist_widened"
	// KindConfigReloadWeakened: a runtime config reload applied a change
	// that weakens security (D-200; config.reloaded with weakened keys).
	KindConfigReloadWeakened = "config_reload_weakened"
	// KindAPIAddressDenied: a valid external API token was presented from
	// a client address outside its allowlist, possibly stolen (D-220
	// amendment 1; api_token.ip_denied). Also a security.account alert to
	// the token's creator.
	KindAPIAddressDenied = "api_token_ip_denied"
	// KindAPITokenCreated: an external API token was created (D-220;
	// api_token.created): a new machine credential that can start
	// backups.
	KindAPITokenCreated = "api_token_created"
	// KindAPITokenAnyAddress: an external API token may be used from any
	// address, chosen at creation or by an address change (D-220
	// amendment 1: a weakened setting, permanently flagged in the UI).
	KindAPITokenAnyAddress = "api_token_any_address" //nolint:gosec // G101: an alert kind, not a credential
	// Accepted security risks (D-228): an admin or backup owner accepted a
	// weakened condition (risk.accepted), permanently (duration forever),
	// an acceptance expires within 7 days (risk.acceptance_expiring), or
	// it ended without a revoke so the risk is flagged again
	// (risk.acceptance_ended: expired, condition_changed,
	// acceptor_lost_rights). Expiring and ended also go to the acceptor.
	KindRiskAccepted            = "risk_accepted"
	KindRiskAcceptedPermanently = "risk_accepted_permanently"
	KindRiskAcceptanceExpiring  = "risk_acceptance_expiring"
	KindRiskAcceptanceEnded     = "risk_acceptance_ended"
	// KindAPITokenNeverExpires: an external API token was created without
	// an expiry (D-229: an explicit choice, permanently flagged in the
	// UI).
	KindAPITokenNeverExpires = "api_token_never_expires"
	// KindAPITokenAddressesWidened: an address change lets an external
	// API token be used from addresses it could not be used from before
	// (a network not covered by the old list, or "any address"; M1 of the
	// D-220 review). A security.system alert unless "any address" was
	// newly chosen (that is api_token_any_address), and always a
	// security.account alert to the token's creator; both name who
	// changed it.
	KindAPITokenAddressesWidened = "api_token_addresses_widened" //nolint:gosec // G101: an alert kind, not a credential
	// KindDestinationTLSPinCleared: an admin removed the certificate pin
	// of a WebDAV destination; it is verified against the system roots or
	// its CA bundle again (D-222; destination.tls_pin_cleared).
	KindDestinationTLSPinCleared = "destination_tls_pin_cleared"
)

// ssrfAllowlistSetting is the settings key of the runtime SSRF allowlist
// (internal/egress.SettingKey; not imported to keep notify free of the
// egress package).
const ssrfAllowlistSetting = "netguard.allowlist"

// Failed-login burst thresholds: within BurstWindow, BurstPerAddress
// failures from one client address (across accounts), BurstPerAccount on
// one existing account (from any address) or BurstGlobal in total raise one
// security.system alert per address, account or instance and window.
const (
	BurstWindow     = 15 * time.Minute
	BurstPerAddress = 20
	BurstPerAccount = 10
	BurstGlobal     = 100
)

// securitySystemKinds are the plain action → security.system mappings of
// task 5.5 (systemKinds holds the D-183 base set).
var securitySystemKinds = map[audit.Action]string{
	audit.ActionJobOwnerAdded:                    KindJobOwnerAdded,
	audit.ActionJobOwnerRemoved:                  KindJobOwnerRemoved,
	audit.ActionJobPassphrasesExported:           KindPassphrasesExported,
	audit.ActionRetentionArtifactMissing:         KindArtifactDeletedExternally,
	audit.ActionRetentionAborted:                 KindRetentionAborted,
	audit.ActionRestorePrivilegedTarget:          KindRestorePrivilegedRefused,
	audit.ActionRestoreSecdefNeutralized:         KindSecdefNeutralized,
	audit.ActionAuthPasskeySignCountAnomaly:      KindPasskeyCloneSuspected,
	audit.ActionDownloadTicketRejected:           KindDownloadRejected,
	audit.ActionSystemInsecureTransportStarted:   KindInsecureTransport,
	audit.ActionSecurityInsecureHTTP:             KindInsecureTransport,
	audit.ActionSecurityInsecureDBTLS:            KindInsecureDBTLS,
	audit.ActionSecurityInsecureDBRoles:          KindInsecureDBRoles,
	audit.ActionSecurityUnpinnedImage:            KindUnpinnedImage,
	audit.ActionSecurityServeAliasConflict:       KindServeAliasConflict,
	audit.ActionSecurityExternalAPIAliasConflict: KindExternalAPIAliasConflict,
	audit.ActionAuditHeadExportFailed:            KindAuditHeadExportFailed,
	audit.ActionAPIAddressDenied:                 KindAPIAddressDenied,
	audit.ActionAPITokenCreated:                  KindAPITokenCreated,
	audit.ActionDestinationTLSPinCleared:         KindDestinationTLSPinCleared,
}

// cliAccountKinds are break-glass CLI actions that also concern the
// target account (security.account).
var cliAccountKinds = map[audit.Action]string{
	audit.ActionCLIAdminPasswordReset: KindPasswordReset,
	audit.ActionCLIAdminMFAReset:      KindSecondFactorsReset,
}

// systemSeverity is the severity of each security.system kind (the
// catalog default, critical, applies to kinds not listed).
var systemSeverity = map[string]Severity{
	KindOptOut:                      SeverityError,
	KindAdminCreated:                SeverityWarning,
	KindAdminGranted:                SeverityWarning,
	KindKeysRotated:                 SeverityWarning,
	KindInstanceKeyCreated:          SeverityInfo,
	KindCertsRotated:                SeverityWarning,
	KindAuditVerifyFailed:           SeverityCritical,
	KindTLSPinMismatch:              SeverityCritical,
	KindSSHHostKeyMismatch:          SeverityCritical,
	KindDestinationHostKeyMismatch:  SeverityCritical,
	KindDestinationTLSPinMismatch:   SeverityCritical,
	KindDestinationTLSPinCleared:    SeverityWarning,
	KindRestoreVerificationFailed:   SeverityCritical,
	KindJobOwnerAdded:               SeverityWarning,
	KindJobOwnerRemoved:             SeverityInfo,
	KindPassphrasesExported:         SeverityWarning,
	KindArtifactDeletedExternally:   SeverityError,
	KindRetentionAborted:            SeverityError,
	KindRestorePrivilegedTarget:     SeverityWarning,
	KindRestorePrivilegedRefused:    SeverityWarning,
	KindTestRestorePrivilegedTarget: SeverityWarning,
	KindJobCustomVersionQuery:       SeverityWarning,
	KindSecdefNeutralized:           SeverityWarning,
	KindTwoFactorPolicyWeakened:     SeverityError,
	KindPasskeyCloneSuspected:       SeverityError,
	KindDownloadRejected:            SeverityWarning,
	KindInsecureTransport:           SeverityError,
	KindInsecureDBTLS:               SeverityError,
	KindInsecureDBRoles:             SeverityError,
	KindUnpinnedImage:               SeverityWarning,
	KindServeAliasConflict:          SeverityCritical,
	KindExternalAPIAliasConflict:    SeverityCritical,
	KindAuditHeadExportFailed:       SeverityError,
	KindCLIAction:                   SeverityWarning,
	KindLoginBurstAddress:           SeverityError,
	KindLoginBurstAccount:           SeverityError,
	KindLoginBurstGlobal:            SeverityError,
	KindCertExpiring:                SeverityWarning,
	KindCertExpired:                 SeverityCritical,
	KindPinExpiring:                 SeverityWarning,
	KindPinExpired:                  SeverityError,
	KindSSRFAllowlistWidened:        SeverityError,
	KindConfigReloadWeakened:        SeverityError,
	KindAPIAddressDenied:            SeverityError,
	KindAPITokenCreated:             SeverityWarning,
	KindAPITokenAnyAddress:          SeverityError,
	KindAPITokenNeverExpires:        SeverityError,
	KindRiskAccepted:                SeverityWarning,
	KindRiskAcceptedPermanently:     SeverityError,
	KindRiskAcceptanceExpiring:      SeverityWarning,
	KindRiskAcceptanceEnded:         SeverityWarning,
	KindAPITokenAddressesWidened:    SeverityWarning,
}

// SystemSeverity returns the severity of a security.system kind.
func SystemSeverity(kind string) Severity {
	if s, ok := systemSeverity[kind]; ok {
		return s
	}
	return SeverityCritical
}

// eventsFromAudit returns every notification event of one audit entry
// (pure; ObserveAudit emits them and adds the burst check).
func eventsFromAudit(ev audit.Event, now time.Time) []Event {
	var events []Event
	if out, ok := fromAudit(ev, now); ok {
		events = append(events, out)
	}
	events = append(events, extraFromAudit(ev)...)
	events = append(events, securityFromAudit(ev)...)
	if ev.Actor.Kind == audit.ActorCLI && !hasType(events, SecuritySystem) {
		// D-069: every break-glass CLI action is a security.system alert.
		events = append(events, system(ev, KindCLIAction))
	}
	for i := range events {
		if events[i].Type != SecuritySystem {
			continue
		}
		kind := str(events[i].Detail, "kind")
		if events[i].Severity == "" {
			events[i].Severity = SystemSeverity(kind)
		}
		if events[i].DedupKey == "" {
			events[i].DedupKey = systemDedupKey(ev, kind, now)
		}
		if ev.Actor.Kind == audit.ActorCLI {
			events[i].Detail["via"] = "cli"
			events[i].Detail["action"] = string(ev.Action)
			for _, k := range []string{audit.DetailOSUser, audit.DetailHostname} {
				if s := detailString(ev.Details, k); s != "" {
					events[i].Detail[k] = s
				}
			}
		}
	}
	return events
}

func hasType(events []Event, t EventType) bool {
	for i := range events {
		if events[i].Type == t {
			return true
		}
	}
	return false
}

func detailTrue(d audit.Details, k string) bool {
	b, ok := d[k].(bool)
	return ok && b
}

// securityFromAudit maps the task 5.5 actions.
func securityFromAudit(ev audit.Event) []Event {
	var out []Event
	kind := securitySystemKinds[ev.Action]
	switch ev.Action {
	case audit.ActionRestoreRequested:
		if detailTrue(ev.Details, "privileged_target_acknowledged") {
			kind = KindRestorePrivilegedTarget
		}
	case audit.ActionJobTestRestoreUpdated:
		if detailTrue(ev.Details, "privileged_target_acknowledged") {
			kind = KindTestRestorePrivilegedTarget
		}
	case audit.ActionJobVersionQueryChanged:
		if detailString(ev.Details, "mode") == "custom" && detailTrue(ev.Details, "custom_sql_changed") {
			kind = KindJobCustomVersionQuery
		}
	case audit.ActionJobUpdated:
		// A job with a custom query moved to another connection.
		if detailTrue(ev.Details, "custom_version_query_retargeted") {
			kind = KindJobCustomVersionQuery
		}
	case audit.ActionMFAPolicyUpdated:
		if detailTrue(ev.Details, "weakened") {
			kind = KindTwoFactorPolicyWeakened
		}
	case audit.ActionSettingChanged:
		if detailString(ev.Details, "setting") == ssrfAllowlistSetting && detailTrue(ev.Details, "widened") {
			kind = KindSSRFAllowlistWidened
		}
	case audit.ActionConfigReloaded:
		if len(detailStrings(ev.Details, "weakened")) > 0 {
			kind = KindConfigReloadWeakened
		}
	case audit.ActionCLIAdminCreated:
		if detailString(ev.Details, "role") == "admin" {
			kind = KindAdminCreated
		}
	case audit.ActionAuthPasskeySignCountAnomaly:
		// The account holder is told too (a cloned authenticator).
		if u := subjectUser(ev); u != uuid.Nil {
			out = append(out, accountEvent(ev, u, KindPasskeyCloneSuspected))
		}
	case audit.ActionAPITokenCreated, audit.ActionAPITokenAddressesChanged:
		// "Any address" newly chosen: a separate, more severe alert.
		anyAddr := detailTrue(ev.Details, "allow_any_address") && !detailTrue(ev.Details, "previous_allow_any_address")
		if anyAddr {
			a := system(ev, KindAPITokenAnyAddress)
			apiTokenDetail(ev, a.Detail)
			out = append(out, a)
		}
		// No expiry (D-229): only at creation (it never changes).
		if ev.Action == audit.ActionAPITokenCreated && detailTrue(ev.Details, "never_expires") {
			a := system(ev, KindAPITokenNeverExpires)
			apiTokenDetail(ev, a.Detail)
			out = append(out, a)
		}
		if ev.Action == audit.ActionAPITokenAddressesChanged && detailTrue(ev.Details, "widened") {
			if !anyAddr {
				a := system(ev, KindAPITokenAddressesWidened)
				apiTokenDetail(ev, a.Detail)
				out = append(out, a)
			}
			// The token's creator is told too, whoever changed it.
			if u, err := uuid.Parse(detailString(ev.Details, "creator_id")); err == nil && u != uuid.Nil {
				a := accountEvent(ev, u, KindAPITokenAddressesWidened)
				apiTokenDetail(ev, a.Detail)
				out = append(out, a)
			}
		}
	case audit.ActionSystemInsecureTransportStarted, audit.ActionSecurityInsecureHTTP, audit.ActionSecurityInsecureDBTLS,
		audit.ActionSecurityInsecureDBRoles, audit.ActionSecurityUnpinnedImage:
		// An accepted posture risk (D-228): the startup entry is still
		// recorded, but it is no open issue: no alert.
		if detailTrue(ev.Details, "risk_accepted") {
			kind = ""
		}
	case audit.ActionRiskAccepted:
		kind = KindRiskAccepted
		if detailString(ev.Details, "duration") == "forever" {
			kind = KindRiskAcceptedPermanently
		}
	case audit.ActionRiskAcceptanceExpiring:
		kind = KindRiskAcceptanceExpiring
		if u, err := uuid.Parse(detailString(ev.Details, "creator_id")); err == nil && u != uuid.Nil {
			a := accountEvent(ev, u, KindRiskAcceptanceExpiring)
			riskDetail(ev, a.Detail)
			out = append(out, a)
		}
	case audit.ActionRiskAcceptanceEnded:
		switch detailString(ev.Details, "end_reason") {
		case "expired", "condition_changed", "acceptor_lost_rights":
			kind = KindRiskAcceptanceEnded
			if u, err := uuid.Parse(detailString(ev.Details, "creator_id")); err == nil && u != uuid.Nil {
				a := accountEvent(ev, u, KindRiskAcceptanceEnded)
				riskDetail(ev, a.Detail)
				out = append(out, a)
			}
		default:
		}
	case audit.ActionAPIAddressDenied:
		// The token's creator is told too (their token may be stolen).
		if u, err := uuid.Parse(detailString(ev.Details, "creator_id")); err == nil && u != uuid.Nil {
			a := accountEvent(ev, u, KindAPIAddressDenied)
			apiDenialDetail(ev, a.Detail)
			a.DedupKey = apiDenialDedupKey(SecurityAccount, ev)
			out = append(out, a)
		}
	default:
	}
	if k, ok := cliAccountKinds[ev.Action]; ok {
		if u := targetID(ev, "user"); u != uuid.Nil {
			out = append(out, accountEvent(ev, u, k))
		}
	}
	if kind != "" {
		s := system(ev, kind)
		switch kind {
		case KindPasskeyCloneSuspected:
			s.Subject.UserID = subjectUser(ev)
		case KindTwoFactorPolicyWeakened:
			s.Detail["setting"] = "security.two_factor_policy"
		case KindConfigReloadWeakened:
			// Key names only (bounded); never values (D-200).
			keys := detailStrings(ev.Details, "weakened")
			s.Detail["count"] = len(keys)
			// "settings", not "keys": key-like names are redacted as secrets.
			s.Detail["settings"] = joinBounded(keys, MaxDetailString)
			if t := detailString(ev.Details, "trigger"); t != "" {
				s.Detail["trigger"] = t
			}
		case KindAPIAddressDenied:
			apiDenialDetail(ev, s.Detail)
		case KindAPITokenCreated:
			apiTokenDetail(ev, s.Detail)
		case KindRiskAccepted, KindRiskAcceptedPermanently, KindRiskAcceptanceExpiring, KindRiskAcceptanceEnded:
			riskDetail(ev, s.Detail)
			if id, err := uuid.Parse(detailString(ev.Details, "connection_id")); err == nil {
				s.Subject.ConnectionID = id
			}
		case KindServeAliasConflict, KindExternalAPIAliasConflict:
			// Container addresses only (bounded).
			s.Detail["addresses"] = joinBounded(detailAddrs(ev.Details, "addresses"), MaxDetailString)
		case KindSSRFAllowlistWidened:
			s.Detail["setting"] = ssrfAllowlistSetting
			if detailTrue(ev.Details, "personal_webhooks_widened") {
				// D-198: users' personal webhooks gained private targets.
				s.Detail["personal_webhooks"] = true
			}
		default:
		}
		out = append(out, s)
	}
	return out
}

// riskDetail adds the facts of a risk acceptance entry: the risk kind
// (a fixed set), the acceptance id, the duration, the expiry date and the
// end reason. Never the reason text or the condition (server names).
func riskDetail(ev audit.Event, d Detail) {
	d["risk_kind"] = detailString(ev.Details, "kind")
	if id, err := uuid.Parse(detailString(ev.Details, "risk_acceptance_id")); err == nil {
		d["acceptance_id"] = id.String()
	}
	if v := detailString(ev.Details, "duration"); v != "" {
		d["duration"] = v
	}
	if t, err := time.Parse(time.RFC3339, detailString(ev.Details, "expires_at")); err == nil {
		d["expires"] = t.UTC().Format("2006-01-02")
	}
	if v := detailString(ev.Details, "end_reason"); v != "" {
		d["end_reason"] = v
	}
}

// riskLabels name the accepted risk kinds in alert sentences.
var riskLabels = map[string]string{
	"insecure_transport": "bekup served over plain HTTP",
	"insecure_db_tls":    "the weakened TLS mode of the app database",
	"insecure_db_roles":  "single-account database mode",
	"unpinned_image":     "an image that follows a tag",
	"connection_tls":     "the relaxed TLS of a database connection",
	"privileged_role":    "a privileged database role of a connection",
	"smtp_unencrypted":   "e-mail that may be sent unencrypted",
	"encryption_opt_out": "unencrypted backups of a job",
}

// riskBody is the sentence of the risk acceptance alerts.
func riskBody(kind string, d Detail) string {
	what := riskLabels[str(d, "risk_kind")]
	if what == "" {
		what = "a security risk"
	}
	switch kind {
	case KindRiskAccepted:
		until := ""
		if e := str(d, "expires"); e != "" {
			until = " until " + e
		}
		return "The risk of " + what + " was accepted" + until + "; it is no longer flagged as an open issue. Review it under Accepted risks."
	case KindRiskAcceptedPermanently:
		return "The risk of " + what + " was accepted without an end date; it will not be flagged again until the acceptance is revoked or the condition changes. Review it under Accepted risks."
	case KindRiskAcceptanceExpiring:
		return "The acceptance of " + what + " ends on " + str(d, "expires") + "; the risk will then be flagged again. Accept it again or fix the condition."
	case KindRiskAcceptanceEnded:
		why := "it expired"
		switch str(d, "end_reason") {
		case "condition_changed":
			why = "the condition changed"
		case "acceptor_lost_rights":
			why = "the account that accepted it may no longer decide about it"
		}
		return "The acceptance of " + what + " ended (" + why + "); the risk is flagged again."
	}
	return ""
}

// apiDenialDetail adds the facts of an api_token.ip_denied entry to an
// alert's detail: the token's id and display name (from the API actor)
// and the refused client address ("unknown" when the external API passed
// none). The detail key names avoid "token" (ValidateDetail refuses
// secret-looking keys).
func apiDenialDetail(ev audit.Event, d Detail) {
	d["ip"] = "unknown"
	if ev.IP.IsValid() {
		d["ip"] = clientIP(ev.IP)
	}
	if r := ev.Actor.API; r != nil {
		d["api_id"] = r.TokenID.String()
		d["api_name"] = displayName(r.TokenName)
	}
}

// apiTokenDetail adds the facts of an api_token.created or
// api_token.addresses_changed entry to an alert's detail: the token's id
// and display name, the number of jobs and networks, whether any address
// is allowed and the expiry date (creation only). Never the token.
func apiTokenDetail(ev audit.Event, d Detail) {
	if id, err := uuid.Parse(detailString(ev.Details, "api_token_id")); err == nil {
		d["api_id"] = id.String()
	}
	// Names may be longer than detailString allows; displayName bounds
	// and cleans them.
	name := ""
	if n, ok := ev.Details["api_token_name"].(string); ok {
		name = n
	}
	d["api_name"] = displayName(name)
	d["jobs"] = detailLen(ev.Details, "job_ids")
	d["networks"] = detailLen(ev.Details, "allowed_cidrs")
	d["any_address"] = detailTrue(ev.Details, "allow_any_address")
	if ev.Action == audit.ActionAPITokenAddressesChanged {
		// Who changed it (display name only, never the e-mail address)
		// and whether that was the token's creator.
		actor := ""
		if n, ok := ev.Details["actor_name"].(string); ok {
			actor = n
		}
		d["changed_by"] = displayName(actor)
		d["by_creator"] = detailTrue(ev.Details, "changed_by_creator")
	}
	if t, err := time.Parse(time.RFC3339, detailString(ev.Details, "expires_at")); err == nil {
		d["expires"] = t.UTC().Format("2006-01-02")
	}
	if ev.Action == audit.ActionAPITokenCreated {
		d["never_expires"] = detailTrue(ev.Details, "never_expires")
	}
}

// detailLen is the length of a list detail (a []string as recorded, or
// []any after a JSON round trip); 0 for anything else.
func detailLen(d audit.Details, k string) int {
	switch v := d[k].(type) {
	case []string:
		return len(v)
	case []any:
		return len(v)
	default:
		return 0
	}
}

// apiDenialDedupKey: one alert per recorded denial (the database records
// at most one per token and 10 minutes), so a retried consumer
// transaction cannot alert twice.
func apiDenialDedupKey(t EventType, ev audit.Event) string {
	if id, err := uuid.Parse(detailString(ev.Details, "denial_id")); err == nil {
		return string(t) + ":" + KindAPIAddressDenied + ":" + id.String()
	}
	return ""
}

// accountEvent is a security.account event for user u.
func accountEvent(ev audit.Event, u uuid.UUID, kind string) Event {
	d := Detail{"kind": kind}
	if ev.IP.IsValid() {
		d["ip"] = clientIP(ev.IP)
	}
	return Event{Type: SecurityAccount, Subject: Subject{UserID: u}, Detail: d, Severity: SeverityError}
}

// dedupPart makes s usable inside a dedup key (lowercase, [a-z0-9_.-],
// at most 64 characters).
func dedupPart(s string) string {
	s = strings.ToLower(s)
	b := make([]byte, 0, min(len(s), 64))
	for i := 0; i < len(s) && len(b) < 64; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
			b = append(b, c)
		default:
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "none"
	}
	return string(b)
}

func day(now time.Time) string { return now.UTC().Format("2006-01-02") }

// systemDedupKey deduplicates the system alerts that can repeat: posture
// alerts once per day, mismatches once per object and day, retention
// aborts once per job, destination and day, a rejected download ticket
// once, an audit verification finding once per finding. Others are never
// deduplicated ("").
func systemDedupKey(ev audit.Event, kind string, now time.Time) string {
	p := "security.system:" + kind + ":"
	switch kind {
	case KindInsecureTransport, KindInsecureDBTLS, KindInsecureDBRoles, KindUnpinnedImage, KindAuditHeadExportFailed:
		return p + day(now)
	case KindTLSPinMismatch, KindSSHHostKeyMismatch, KindDestinationHostKeyMismatch, KindDestinationTLSPinMismatch:
		return p + dedupPart(ev.TargetID) + ":" + day(now)
	case KindRetentionAborted:
		return p + dedupPart(ev.TargetID) + ":" + dedupPart(detailString(ev.Details, "destination_id")) + ":" + day(now)
	case KindDownloadRejected:
		if t := detailString(ev.Details, "ticket_id"); t != "" {
			return p + dedupPart(t)
		}
	case KindArtifactDeletedExternally:
		return p + dedupPart(ev.TargetID)
	case KindServeAliasConflict, KindExternalAPIAliasConflict:
		// serve reports each new set of foreign addresses once; a set
		// that persists or comes back alerts again the next day.
		return p + keysHash(detailAddrs(ev.Details, "addresses")) + ":" + day(now)
	case KindConfigReloadWeakened:
		// Replicas that apply the same change for one request raise one
		// alert; a different set of keys raises another.
		if r := detailString(ev.Details, "request_id"); r != "" {
			return p + dedupPart(r) + ":" + keysHash(detailStrings(ev.Details, "weakened"))
		}
	case KindAuditVerifyFailed:
		if f := detailString(ev.Details, "finding"); f != "" {
			return p + dedupPart(f)
		}
	case KindAPIAddressDenied:
		return apiDenialDedupKey(SecuritySystem, ev)
	default:
	}
	return ""
}

// PostureDedupKey is the once-per-day key of a posture alert
// (KindInsecureTransport, KindInsecureDBTLS, KindInsecureDBRoles,
// KindUnpinnedImage): the
// startup audit entry and the periodic reminder share it.
func PostureDedupKey(kind string, now time.Time) string {
	return "security.system:" + kind + ":" + day(now)
}

// loginBurst raises the failed-login burst alerts after an
// auth.login.failed entry was written with q: at most one per client
// address, account and window, and one for the whole instance. The alert
// names the address or the existing account only (never an e-mail
// address: unknown accounts have no target id).
func loginBurst(ctx context.Context, q *sqlc.Queries, ev audit.Event, now time.Time) error {
	var ip *netip.Addr
	if ev.IP.IsValid() {
		a := ev.IP.WithZone("").Unmap()
		ip = &a
	}
	user := targetID(ev, "user")
	var uid *string
	if user != uuid.Nil {
		s := user.String()
		uid = &s
	}
	c, err := q.CountRecentLoginFailures(ctx, sqlc.CountRecentLoginFailuresParams{
		Ip: ip, UserID: uid, Since: now.Add(-BurstWindow),
	})
	if err != nil {
		return fmt.Errorf("notify: count failed sign-ins: %w", err)
	}
	bucket := strconv.FormatInt(now.UTC().Truncate(BurstWindow).Unix(), 10)
	minutes := int(BurstWindow / time.Minute)
	var events []Event
	if ip != nil && c.ByAddress >= BurstPerAddress {
		events = append(events, Event{
			Type: SecuritySystem, Severity: SystemSeverity(KindLoginBurstAddress),
			Detail:   Detail{"kind": KindLoginBurstAddress, "ip": ip.String(), "failures": c.ByAddress, "window_minutes": minutes},
			DedupKey: "security.system:" + KindLoginBurstAddress + ":" + dedupPart(ip.String()) + ":" + bucket,
		})
	}
	if uid != nil && c.ByAccount >= BurstPerAccount {
		events = append(events, Event{
			Type: SecuritySystem, Severity: SystemSeverity(KindLoginBurstAccount), Subject: Subject{UserID: user},
			Detail:   Detail{"kind": KindLoginBurstAccount, "failures": c.ByAccount, "window_minutes": minutes},
			DedupKey: "security.system:" + KindLoginBurstAccount + ":" + user.String() + ":" + bucket,
		})
	}
	if c.Total >= BurstGlobal {
		events = append(events, Event{
			Type: SecuritySystem, Severity: SystemSeverity(KindLoginBurstGlobal),
			Detail:   Detail{"kind": KindLoginBurstGlobal, "failures": c.Total, "window_minutes": minutes},
			DedupKey: "security.system:" + KindLoginBurstGlobal + ":" + bucket,
		})
	}
	for i := range events {
		if _, err := Emit(ctx, q, events[i]); err != nil {
			return err
		}
	}
	return nil
}

// systemBody is the sentence of a security.system event: the fixed text
// of its kind, with the counts and dates of the kinds that carry them.
func systemBody(d Detail) string {
	kind := str(d, "kind")
	switch kind {
	case KindLoginBurstAddress:
		return fmt.Sprintf("%d failed sign-ins from %s within %d minutes.", num(d, "failures"), str(d, "ip"), num(d, "window_minutes"))
	case KindLoginBurstAccount:
		return fmt.Sprintf("%d failed sign-ins on one account within %d minutes.", num(d, "failures"), num(d, "window_minutes"))
	case KindLoginBurstGlobal:
		return fmt.Sprintf("%d failed sign-ins in total within %d minutes.", num(d, "failures"), num(d, "window_minutes"))
	case KindCertExpiring:
		return fmt.Sprintf("The %s certificate expires on %s (in %d days or less). Renew it with bekup setup renew (or bekup certs rotate).",
			str(d, "component"), str(d, "not_after"), num(d, "threshold_days"))
	case KindCertExpired:
		return fmt.Sprintf("The %s certificate expired on %s. Renew it with bekup setup renew (or bekup certs rotate).", str(d, "component"), str(d, "not_after"))
	case KindPinExpiring:
		return fmt.Sprintf("The pinned certificate of a database connection expires on %s (in %d days or less); re-pin it after the server's certificate is renewed.",
			str(d, "not_after"), num(d, "threshold_days"))
	case KindPinExpired:
		return fmt.Sprintf("The pinned certificate of a database connection expired on %s.", str(d, "not_after"))
	case KindConfigReloadWeakened:
		if k := str(d, "settings"); k != "" {
			return systemMessages[kind] + " Changed: " + k + "."
		}
	case KindSSRFAllowlistWidened:
		if b, ok := d["personal_webhooks"].(bool); ok && b {
			return systemMessages[kind] + " Personal webhooks of every user may now reach private targets as well."
		}
	case KindCLIAction:
		if a := str(d, "action"); a != "" {
			return "A break-glass command was run on the bekup host (" + a + ")."
		}
	case KindAPIAddressDenied:
		return apiDenialBody(d)
	case KindAPITokenCreated:
		return apiTokenCreatedBody(d)
	case KindAPITokenAnyAddress:
		return fmt.Sprintf("The external API token %q may now be used from any address%s: no client address allowlist protects it. "+
			"Restrict it to your CI runners' networks if you can.", apiName(d), apiChangedBy(d))
	case KindRiskAccepted, KindRiskAcceptedPermanently, KindRiskAcceptanceExpiring, KindRiskAcceptanceEnded:
		return riskBody(kind, d)
	case KindAPITokenNeverExpires:
		return fmt.Sprintf("The external API token %q never expires: it stays valid until it is revoked. "+
			"Revoke it when the pipeline that uses it is retired.", apiName(d))
	case KindAPITokenAddressesWidened:
		return apiWidenedBody(d)
	default:
	}
	if m, ok := systemMessages[kind]; ok {
		return m
	}
	return ""
}

// apiName is the display name of the token of an API token alert.
func apiName(d Detail) string {
	if n := str(d, "api_name"); n != "" {
		return n
	}
	return "(unnamed)"
}

// apiChangedBy names who changed a token's addresses (" (changed by
// ...)"), stressing a change by someone other than the token's creator;
// "" for alerts of a token's creation.
func apiChangedBy(d Detail) string {
	who := str(d, "changed_by")
	if who == "" {
		return ""
	}
	if b, ok := d["by_creator"].(bool); ok && b {
		return fmt.Sprintf(" (changed by %s, its creator)", who)
	}
	return fmt.Sprintf(" (changed by %s, not its creator)", who)
}

// apiWidenedBody is the sentence of the api_token_addresses_widened
// alerts (admins and the token's creator).
func apiWidenedBody(d Detail) string {
	where := fmt.Sprintf("from more addresses (now %d allowed network(s))", num(d, "networks"))
	if b, ok := d["any_address"].(bool); ok && b {
		where = "from any address"
	}
	return fmt.Sprintf("The client addresses of the external API token %q were widened%s: it may now be used %s. "+
		"If you did not expect this change, review the token's addresses or revoke it.", apiName(d), apiChangedBy(d), where)
}

// apiTokenCreatedBody is the sentence of the api_token_created alert.
func apiTokenCreatedBody(d Detail) string {
	where := fmt.Sprintf("from %d allowed network(s)", num(d, "networks"))
	if b, ok := d["any_address"].(bool); ok && b {
		where = "from any address"
	}
	until := ""
	if e := str(d, "expires"); e != "" {
		until = ", valid until " + e
	} else if b, ok := d["never_expires"].(bool); ok && b {
		until = ", never expiring"
	}
	return fmt.Sprintf("An external API token %q was created; it can start backups of %d job(s) %s%s.",
		apiName(d), num(d, "jobs"), where, until)
}

// apiDenialBody is the sentence of the api_token_ip_denied alerts (admins
// and the token's creator).
func apiDenialBody(d Detail) string {
	name := str(d, "api_name")
	if name == "" {
		name = "(unnamed)"
	}
	ip := str(d, "ip")
	if ip == "" {
		ip = "unknown"
	}
	return fmt.Sprintf("The external API token %q was presented from the address %s, which its address allowlist does not include; "+
		"the request was refused. If you do not recognize the address, the token may be stolen: revoke it.", name, ip)
}

// keyNameRE is the shape of a config key name in a reload entry.
var keyNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}$`)

// detailStrings returns the config key names in a list detail (a []string
// as recorded, or []any after a JSON round trip); anything else is
// dropped.
func detailStrings(d audit.Details, k string) []string {
	var raw []any
	switch v := d[k].(type) {
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	case []any:
		raw = v
	default:
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok && keyNameRE.MatchString(s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// detailAddrs returns the IP addresses in a list detail (canonical,
// sorted, at most 16); anything that is not an address is dropped.
func detailAddrs(d audit.Details, k string) []string {
	var raw []any
	switch v := d[k].(type) {
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	case []any:
		raw = v
	default:
		return nil
	}
	out := make([]string, 0, min(len(raw), 16))
	for _, x := range raw {
		s, ok := x.(string)
		if !ok {
			continue
		}
		if a, err := netip.ParseAddr(s); err == nil && len(out) < 16 {
			out = append(out, a.WithZone("").Unmap().String())
		}
	}
	sort.Strings(out)
	return out
}

// joinBounded joins names with ", " up to n bytes, ending with "..." when
// some did not fit.
func joinBounded(names []string, n int) string {
	var b strings.Builder
	for i, s := range names {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		if b.Len()+len(sep)+len(s) > n-3 {
			b.WriteString("...")
			break
		}
		b.WriteString(sep)
		b.WriteString(s)
	}
	return b.String()
}

// keysHash is a short, stable hash of a sorted key list (dedup keys).
func keysHash(keys []string) string {
	h := sha256.Sum256([]byte(strings.Join(keys, ",")))
	return hex.EncodeToString(h[:8])
}
