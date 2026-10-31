package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/email"
)

// Content is what every channel shows of an event: a title, one short
// sentence, a link into the UI (built from server.base_url) and the
// minimal data object of the webhook envelope and the inbox payload. It
// never contains secrets, connection strings or database content.
type Content struct {
	Title string
	Body  string
	Link  string
	Data  map[string]any
}

// names are the display names of an event's subjects.
type names struct {
	job, connection, destination string
	jobDeleted                   bool
}

// displayName makes an admin-entered name safe for every channel: no
// control characters, at most 100 characters.
func displayName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:99]) + "…"
	}
	if s == "" {
		return "(unnamed)"
	}
	return s
}

func (s *Service) loadNames(ctx context.Context, q *sqlc.Queries, ev *sqlc.NotificationEvent) (names, error) {
	var n names
	if ev.JobID != nil {
		j, err := q.GetNotifyJob(ctx, *ev.JobID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			n.job, n.jobDeleted = "(deleted job)", true
		case err != nil:
			return n, fmt.Errorf("notify: load job: %w", err)
		default:
			n.job, n.jobDeleted = displayName(j.Name), j.DeletedAt != nil
		}
	}
	if ev.ConnectionID != nil {
		name, err := q.GetNotifyConnectionName(ctx, *ev.ConnectionID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return n, fmt.Errorf("notify: load connection: %w", err)
		}
		n.connection = displayName(name)
	}
	if ev.DestinationID != nil {
		name, err := q.GetNotifyDestinationName(ctx, *ev.DestinationID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return n, fmt.Errorf("notify: load destination: %w", err)
		}
		n.destination = displayName(name)
	}
	return n, nil
}

func (s *Service) link(path string) string {
	u := *s.base
	u.Path += path
	return u.String()
}

func idPath(prefix string, id *uuid.UUID) string {
	if id == nil {
		return prefix
	}
	return prefix + "/" + id.String()
}

// detailOf decodes the stored detail (written by Emit, so trusted to be
// small and clean).
func detailOf(ev *sqlc.NotificationEvent) Detail {
	d := Detail{}
	if len(ev.Detail) == 0 {
		return d
	}
	if err := json.Unmarshal(ev.Detail, &d); err != nil {
		// A malformed detail renders without it.
		return Detail{}
	}
	return d
}

func str(d Detail, k string) string {
	if s, ok := d[k].(string); ok {
		return s
	}
	return ""
}

func num(d Detail, k string) int64 {
	if f, ok := d[k].(float64); ok {
		return int64(f)
	}
	return 0
}

var accountMessages = map[string]string{
	KindSignIn:                   "Someone signed in to your account. If this was not you, change your password and review your sessions.",
	KindPasswordChanged:          "The password of your account was changed. If this was not you, contact an administrator at once.",
	KindPasswordReset:            "An administrator reset the password of your account.",
	KindSecondFactorAdded:        "A second factor (passkey or authenticator app) was added to your account.",
	KindSecondFactorRemoved:      "A second factor was removed from your account.",
	KindRecoveryCodeUsed:         "A recovery code of your account was used to sign in.",
	KindRecoveryCodesRegenerated: "New recovery codes were generated for your account; the old ones no longer work.",
	KindSecondFactorsReset:       "An administrator reset the second factors of your account.",
	KindAccountLocked:            "Your account was locked after repeated failed sign-in attempts.",
	KindSecondFactorLocked:       "Second-factor checks of your account were locked after repeated failures.",
	KindRoleChanged:              "The role of your account was changed.",
	KindPasskeyCloneSuspected:    "A passkey of your account reported a signature counter that went backwards (possibly a cloned authenticator); the sign-in was refused. Review your passkeys.",
}

var systemMessages = map[string]string{
	KindOptOut:                     "A security setting was weakened (an opt-out). Review it in the audit log.",
	KindAdminCreated:               "A new administrator account was created.",
	KindAdminGranted:               "An account was given the administrator role.",
	KindKeysRotated:                "The master keys of this instance changed.",
	KindInstanceKeyCreated:         "A new instance signing key was created.",
	KindCertsRotated:               "The internal certificates were rotated.",
	KindAuditVerifyFailed:          "The audit log chain verification failed.",
	KindTLSPinMismatch:             "A database server presented a certificate that does not match its pin.",
	KindSSHHostKeyMismatch:         "An SSH tunnel host presented a host key that does not match its pin.",
	KindDestinationHostKeyMismatch: "An SFTP destination presented a host key that does not match its pin.",
	KindRestoreVerificationFailed:  "A backup file failed its verification before a restore (possible tampering).",
	// Task 5.5 (D-190).
	KindJobOwnerAdded:               "An owner was added to a job (the account gains access to its backups).",
	KindJobOwnerRemoved:             "An owner was removed from a job.",
	KindPassphrasesExported:         "The encryption passphrases of a job were exported.",
	KindArtifactDeletedExternally:   "A backup file disappeared from its destination without bekup deleting it.",
	KindRetentionAborted:            "Retention stopped on a destination because a safety check failed (the newest backup is missing or changed).",
	KindRestorePrivilegedTarget:     "A restore into a privileged (or unverified) database role was requested with an acknowledgement.",
	KindRestorePrivilegedRefused:    "A restore was refused because its target role became privileged after the request.",
	KindTestRestorePrivilegedTarget: "A privileged (or unverified) database role was set as a test-restore scratch target with an acknowledgement.",
	KindJobCustomVersionQuery:       "A custom schema-version query was set on a job, or its job moved to another connection; it runs on the backed-up databases with the backup role's rights.",
	KindSecdefNeutralized:           "A restore switched SECURITY DEFINER functions of the restored database to SECURITY INVOKER.",
	KindTwoFactorPolicyWeakened:     "The two-factor authentication policy was weakened.",
	KindPasskeyCloneSuspected:       "A passkey reported a signature counter that went backwards (possibly a cloned authenticator).",
	KindDownloadRejected:            "A download link was presented after use, after expiry, by another session or by an account without access.",
	KindInsecureTransport:           "bekup is running over plain HTTP outside dev mode; sessions and passwords travel unencrypted.",
	KindInsecureDBTLS:               "bekup connects to its database with a weakened TLS mode (the server identity is not fully verified).",
	KindInsecureDBRoles:             "bekup runs migrations and the app with one database account (single-account mode), so a compromised server process could alter the schema and the audit log.",
	KindUnpinnedImage:               "bekup runs an image that follows a tag (such as :latest) instead of a digest, so whoever moves the tag decides which code runs; pin a digest with setup upgrade --image to remove this warning.",
	KindAuditHeadExportFailed:       "The signed audit chain head could not be exported.",
	KindCLIAction:                   "A break-glass command was run on the bekup host.",
	KindSSRFAllowlistWidened:        "The SSRF allowlist was widened: bekup may now connect to more private hosts or networks. Review the change in the audit log.",
	KindConfigReloadWeakened:        "A configuration reload applied a change that weakens security (for example a wider trusted-proxy or SSRF allowlist, higher limits or a shorter lockout). Review bekup.toml and the audit log.",
}

// render builds the content of ev.
func (s *Service) render(ev *sqlc.NotificationEvent, m Meta, n names) Content {
	d := detailOf(ev)
	job := n.job
	var body, path string
	switch m.Group {
	case GroupBackup:
		path = idPath("/runs", ev.RunID)
		switch EventType(ev.Type) {
		case BackupStarted, BackupStartedEarly:
			body = fmt.Sprintf("The backup of job %q started.", job)
		case BackupSucceeded:
			body = fmt.Sprintf("The backup of job %q succeeded.", job)
		case BackupSucceededWithWarnings:
			body = fmt.Sprintf("The backup of job %q succeeded with warnings.", job)
		case BackupPartial:
			body = fmt.Sprintf("The backup of job %q failed for some databases.", job)
		case BackupFailed:
			body = fmt.Sprintf("The backup of job %q failed.", job)
		case BackupCanceled:
			body = fmt.Sprintf("The backup of job %q was canceled.", job)
		case BackupMissed:
			body = fmt.Sprintf("The backup of job %q did not run in its expected window.", job)
			path = idPath("/jobs", ev.JobID)
		default:
		}
	case GroupRestore:
		path = idPath("/restores", ev.RestoreID)
		switch EventType(ev.Type) {
		case RestoreStarted:
			body = fmt.Sprintf("A restore of a backup of job %q started.", job)
		case RestoreCompleted:
			body = fmt.Sprintf("A restore of a backup of job %q completed.", job)
		case RestoreFailed:
			body = fmt.Sprintf("A restore of a backup of job %q failed.", job)
		case TestRestorePassed:
			body, path = fmt.Sprintf("The test restore of job %q passed.", job), idPath("/jobs", ev.JobID)
		case TestRestoreWarnings:
			body, path = fmt.Sprintf("The test restore of job %q passed with warnings.", job), idPath("/jobs", ev.JobID)
		case TestRestoreFailed:
			body, path = fmt.Sprintf("The test restore of job %q failed.", job), idPath("/jobs", ev.JobID)
		default:
		}
	case GroupRetention:
		switch EventType(ev.Type) {
		case RetentionSummary:
			body = fmt.Sprintf("Retention removed %d backup(s) (%d file(s), %d bytes) of job %q on %s.",
				num(d, "runs"), num(d, "files"), num(d, "bytes"), job, str(d, "date"))
			path = idPath("/jobs", ev.JobID)
		case RetentionBlocked:
			cause := "the destination holds no complete backup of the job"
			if c := str(d, "newest_complete"); c != "" {
				cause = "the backups after " + c + " are incomplete"
			}
			body = fmt.Sprintf("Retention is keeping %d incomplete backup(s) of job %q on destination %q since %s: %s.",
				num(d, "held_runs"), job, n.destination, str(d, "since"), cause)
			path = idPath("/jobs", ev.JobID)
		case ArtifactMissing:
			body = fmt.Sprintf("A backup file of job %q is missing from its destination; bekup did not delete it.", job)
			path = idPath("/jobs", ev.JobID)
		case DestinationTestFailed:
			body = fmt.Sprintf("The test of destination %q failed.", n.destination)
			path = idPath("/destinations", ev.DestinationID)
		default:
		}
	case GroupConfig:
		if m.Subject == SubjectConnection {
			path = idPath("/connections", ev.ConnectionID)
			switch EventType(ev.Type) {
			case ConnectionPinChanged:
				body = fmt.Sprintf("The certificate pin of connection %q was changed.", n.connection)
			case ConnectionTLSDowngraded:
				body = fmt.Sprintf("The TLS protection of connection %q was weakened.", n.connection)
			case ConnectionHostKeyChanged:
				body = fmt.Sprintf("The SSH host key pin of connection %q was changed.", n.connection)
			default:
			}
		} else {
			path = idPath("/jobs", ev.JobID)
			verb := map[EventType]string{
				JobCreated: "was created", JobUpdated: "was changed", JobDisabled: "was disabled",
				JobEnabled: "was enabled", JobDeleted: "was deleted", JobEncryptionChanged: "had its encryption changed",
			}[EventType(ev.Type)]
			body = fmt.Sprintf("Job %q %s.", job, verb)
			if n.jobDeleted {
				path = "/jobs"
			}
		}
	case GroupSecurity:
		if EventType(ev.Type) == SecurityAccount {
			path = "/profile/security"
			body = accountMessages[str(d, "kind")]
		} else {
			path = "/admin/audit"
			body = systemBody(d)
		}
		if body == "" {
			body = "A security event occurred. Review it in bekup."
		}
	}
	title := m.Label
	if job != "" && m.Subject == SubjectJob {
		title = m.Label + ": " + job
	}
	if code := str(d, "error_code"); code != "" {
		body += " Reason: " + code + "."
	}
	if ev.CollapsedCount > 0 {
		body += fmt.Sprintf(" (%d similar event(s) collapsed.)", ev.CollapsedCount)
	}
	c := Content{Title: clampRunes(title, 200), Body: clampRunes(body, 1000)}
	if path != "" {
		c.Link = s.link(path)
	}
	c.Data = s.data(ev, m, n, d, c)
	return c
}

func clampRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// data is the minimal webhook / inbox payload.
func (s *Service) data(ev *sqlc.NotificationEvent, m Meta, n names, d Detail, c Content) map[string]any {
	out := map[string]any{
		"event":       ev.Type,
		"group":       string(m.Group),
		"severity":    ev.Severity,
		"title":       c.Title,
		"summary":     c.Body,
		"occurred_at": ev.CreatedAt.UTC().Format(time.RFC3339),
	}
	if c.Link != "" {
		out["link"] = c.Link
	}
	if ev.JobID != nil {
		out["job"] = map[string]any{"id": ev.JobID.String(), "name": n.job}
	}
	if ev.ConnectionID != nil {
		out["connection"] = map[string]any{"id": ev.ConnectionID.String(), "name": n.connection}
	}
	if ev.DestinationID != nil {
		out["destination"] = map[string]any{"id": ev.DestinationID.String(), "name": n.destination}
	}
	for k, id := range map[string]*uuid.UUID{
		"run_id": ev.RunID, "restore_id": ev.RestoreID, "test_restore_id": ev.TestRestoreID, "artifact_id": ev.ArtifactID,
	} {
		if id != nil {
			out[k] = id.String()
		}
	}
	if len(d) > 0 {
		out["detail"] = d
	}
	if ev.CollapsedCount > 0 {
		out["collapsed"] = ev.CollapsedCount
	}
	return out
}

// emailFor renders the e-mail of c (the 5.1 layout).
func emailFor(ev *sqlc.NotificationEvent, c Content, n names) (email.Content, error) {
	details := []email.Detail{{Label: "Event", Value: ev.Type}}
	if n.job != "" {
		details = append(details, email.Detail{Label: "Job", Value: n.job})
	}
	if n.connection != "" {
		details = append(details, email.Detail{Label: "Connection", Value: n.connection})
	}
	if n.destination != "" {
		details = append(details, email.Detail{Label: "Destination", Value: n.destination})
	}
	details = append(details, email.Detail{Label: "Time", Value: ev.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC")})
	notice := email.Notice{
		Subject: "[bekup] " + clampRunes(c.Title, 180), Heading: c.Title, Paragraphs: []string{c.Body}, Details: details,
	}
	if c.Link != "" {
		notice.Action = &email.Action{Label: "Open in bekup", URL: c.Link}
	}
	return email.Render(notice)
}

// floodEmail is the one summary e-mail of the per-user flood rule.
func (s *Service) floodEmail(limit int) (email.Content, error) {
	return email.Render(email.Notice{
		Subject: "[bekup] Notifications paused for an hour",
		Heading: "Many notifications in a short time",
		Paragraphs: []string{
			fmt.Sprintf("You received %d notifications through this channel within the last hour.", limit),
			"Further notifications (except security alerts) are only shown in your bekup inbox until the rate drops.",
		},
		Action: &email.Action{Label: "Open the inbox", URL: s.link("/")},
	})
}

// floodData is the webhook payload of the flood summary.
func (s *Service) floodData(limit int) map[string]any {
	return map[string]any{
		"summary": fmt.Sprintf("%d notifications within the last hour; further ones (except security alerts) are only shown in the bekup inbox until the rate drops.", limit),
		"link":    s.link("/"),
	}
}

// FloodSummaryType is the webhook event type of the flood summary.
const FloodSummaryType = "notification.flood_summary"
