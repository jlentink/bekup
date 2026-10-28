package audit

import (
	"regexp"
	"slices"
)

// Action is an audit action name: dot-separated lowercase words, the first
// word naming the area (auth, user, session, ...). Only actions in the
// catalog below can be recorded; add new ones here (extensible, typo-safe).
type Action string

// Action catalog. Keep it sorted by area; docs/security/audit.md lists it.
const (
	// Authentication (tasks 1.5-1.8).
	ActionAuthLoginSucceeded  Action = "auth.login.succeeded"
	ActionAuthLoginFailed     Action = "auth.login.failed"
	ActionAuthLogout          Action = "auth.logout"
	ActionAuthLockout         Action = "auth.lockout"
	ActionAuthStepUpSucceeded Action = "auth.stepup.succeeded"
	ActionAuthStepUpFailed    Action = "auth.stepup.failed"
	ActionAuthPasswordChanged Action = "auth.password.changed"
	// ActionAuthPasswordChangeFailed: a password change with a wrong
	// current password (counts towards the lockout).
	ActionAuthPasswordChangeFailed Action = "auth.password.change_failed"
	ActionAuthMFAEnrolled          Action = "auth.mfa.enrolled"
	ActionAuthMFARemoved           Action = "auth.mfa.removed"
	ActionAuthRecoveryCodeUsed     Action = "auth.recovery_code.used"

	// TOTP and recovery codes (task 1.7). Enrollment and removal use
	// auth.mfa.enrolled / auth.mfa.removed with details.factor = "totp".
	ActionAuthMFAEnrollmentStarted Action = "auth.mfa.enrollment_started"
	// ActionAuthMFAVerified: a TOTP code was accepted.
	ActionAuthMFAVerified Action = "auth.mfa.verified"
	// ActionAuthMFAFailed: a TOTP or recovery code was rejected (wrong,
	// replayed, locked); details.factor and details.reason say which.
	ActionAuthMFAFailed Action = "auth.mfa.failed"
	// ActionAuthMFALockout: second-factor checks locked after repeated
	// failures (separate from the password lockout).
	ActionAuthMFALockout               Action = "auth.mfa.lockout"
	ActionAuthRecoveryCodesRegenerated Action = "auth.recovery_code.regenerated"

	// Two-step login and the 2FA policy (task 1.9).
	// ActionAuthLoginSecondFactor: a login's second step succeeded (details:
	// method, never the code); the session became AAL2.
	ActionAuthLoginSecondFactor Action = "auth.login.second_factor"
	// ActionAuthMFARemovalRefused: removing the last second factor was
	// refused because the 2FA policy requires one.
	ActionAuthMFARemovalRefused Action = "auth.mfa.removal_refused"
	// ActionMFAPolicyUpdated: an admin changed the 2FA policy.
	ActionMFAPolicyUpdated Action = "mfa_policy.updated"

	// Users and sessions.
	ActionUserCreated     Action = "user.created"
	ActionUserUpdated     Action = "user.updated"
	ActionUserDisabled    Action = "user.disabled"
	ActionUserEnabled     Action = "user.enabled"
	ActionUserRoleChanged Action = "user.role_changed"
	ActionUserDeleted     Action = "user.deleted"
	// ActionUserPasswordReset: an admin set a temporary password (task 1.6).
	ActionUserPasswordReset Action = "user.password_reset"
	ActionUserUnlocked      Action = "user.unlocked"
	// ActionUserSecondFactorsReset: an admin removed every second factor
	// of an account (lost-factor recovery, task 1.9).
	ActionUserSecondFactorsReset Action = "user.second_factors_reset"
	ActionSessionRevoked         Action = "session.revoked"
	ActionSessionsRevoked        Action = "session.revoked_all"

	// Settings and stored credentials (the value is never recorded).
	// Reserved: recorded from tasks 2.11/2.12 (connection and destination
	// secrets) and 5.1 (SMTP).
	ActionSecretChanged Action = "secret.changed"
	// Reserved: recorded from tasks 2.3 (SSRF allowlist) and 5.1/6.9
	// (admin settings).
	ActionSettingChanged Action = "setting.changed"

	// Security posture changes (D-001: weakened settings are audited).
	// Recorded from task 2.11 (TLS downgrade of a connection, step-up +
	// badge) and later per-destination/job opt-outs, alerted in task 5.5.
	ActionSecurityOptOut Action = "security.opt_out"
	// Reserved: a runtime switch to plain HTTP (task 5.5 alerting); the
	// startup posture is system.insecure_transport_started (D-095).
	ActionSecurityInsecureHTTP Action = "security.insecure_http"
	// ActionSecurityInsecureDBTLS: serve started outside dev mode with a
	// weakened database.tls_mode (require or disable; D-100).
	ActionSecurityInsecureDBTLS Action = "security.insecure_db_tls"

	// bekup itself (startup posture, D-095).
	ActionSystemInsecureTransportStarted Action = "system.insecure_transport_started"

	// Break-glass CLI (D-069; task 1.6).
	ActionCLIAdminCreated       Action = "cli.admin.created"
	ActionCLIAdminPasswordReset Action = "cli.admin.password_reset" //nolint:gosec // G101: an action name, not a credential
	ActionCLIAdminMFAReset      Action = "cli.admin.mfa_reset"
	ActionCLIAdminUnlocked      Action = "cli.admin.unlocked"
	ActionCLIAdminDisabled      Action = "cli.admin.disabled"

	// Keys, certificates and the audit chain itself.
	// ActionKeysRotated: serve found master keys without a stored
	// key-check value (after `bekup keys rotate`) and recorded them
	// (D-119).
	ActionKeysRotated Action = "keys.rotated"
	// ActionKeysCheckValueStored: the first start stored the key-check
	// values of the master keyring (D-086, D-119).
	ActionKeysCheckValueStored Action = "keys.check_value_stored"
	ActionInstanceKeyCreated   Action = "keys.instance_key.created"
	// ActionCertsRotated: `bekup certs rotate` (or `certs init --force`)
	// replaced certificate material (CLI event).
	ActionCertsRotated Action = "certs.rotated"
	// Reserved: recorded by the periodic chain verification (task 5.6),
	// alerted in task 5.5.
	ActionAuditVerifyFailed     Action = "audit.verify.failed"
	ActionAuditHeadExportFailed Action = "audit.head_export.failed"
	// Downloads (task 3.9, D-150; target type artifact). Details carry
	// the ticket id, mode and artifact/job/run ids, never the ticket value
	// or an age identity. ticket_rejected: a ticket was presented by
	// another session, after use, after expiry or by a caller who lost
	// access (the ticket is burned).
	ActionDownloadTicketCreated  Action = "download.ticket_created"
	ActionDownloadTicketRejected Action = "download.ticket_rejected"
	ActionDownloadStarted        Action = "download.started"
	ActionDownloadCompleted      Action = "download.completed"
	ActionDownloadFailed         Action = "download.failed"

	// Connections (task 2.11, D-135). Details name the changed fields;
	// credential fields only ever appear by name (and additionally as a
	// secret.changed entry), never with a value.
	ActionConnectionCreated          Action = "connection.created"
	ActionConnectionUpdated          Action = "connection.updated"
	ActionConnectionDeleted          Action = "connection.deleted"
	ActionConnectionTLSMode          Action = "connection.tls_mode_changed"
	ActionConnectionPinConfirmed     Action = "connection.pin_confirmed"
	ActionConnectionBackupPinStaged  Action = "connection.backup_pin_staged"
	ActionConnectionHostKeyConfirmed Action = "connection.host_key_confirmed"
	ActionConnectionSSHKeyGenerated  Action = "connection.ssh_key_generated"
	// ActionConnectionPinMismatch: the runner refused a connection because
	// the target's certificate does not match the pin (security event,
	// alerted in task 5.5).
	ActionConnectionPinMismatch Action = "connection.tls_pin_mismatch"
	// ActionConnectionHostKeyMismatch: the SSH bastion presented another
	// host key than the pinned one (security event, alerted in task 5.5).
	ActionConnectionHostKeyMismatch Action = "connection.ssh_host_key_mismatch"

	// Destinations (task 2.12, D-131). Details name the changed fields;
	// credential fields only ever appear as "changed" (and additionally as
	// a secret.changed entry), never with a value.
	ActionDestinationCreated          Action = "destination.created"
	ActionDestinationUpdated          Action = "destination.updated"
	ActionDestinationDeleted          Action = "destination.deleted"
	ActionDestinationHostKeyConfirmed Action = "destination.host_key_confirmed"
	ActionDestinationSSHKeyGenerated  Action = "destination.ssh_key_generated"
	// ActionDestinationHostKeyMismatch: an SFTP server presented another
	// host key than the pinned one during a test (security event, D-140;
	// alerted in task 5.5).
	ActionDestinationHostKeyMismatch Action = "destination.ssh_host_key_mismatch"

	// Job encryption (task 3.2, D-142). Details carry versions, modes and
	// public recipients only, never a passphrase or an identity. An opt-out
	// is recorded as job.encryption_opted_out plus security.opt_out.
	ActionJobEncryptionRecipientSet Action = "job.encryption_recipient_set"
	ActionJobEncryptionOptedOut     Action = "job.encryption_opted_out"
	ActionJobEncryptionOptedIn      Action = "job.encryption_opted_in"
)

// Job passphrase actions (task 3.2, D-142): a new passphrase version (set
// or generated; also recorded as secret.changed) and the offline-recovery
// export of every version (admin, step-up; alerted).
const (
	ActionJobEncryptionPassphraseSet Action = "job.encryption_passphrase_set"
	ActionJobPassphrasesExported     Action = "job.passphrases_exported"
)

// Job actions (task 3.5, D-146). Details carry field names and short
// non-secret values (names, slugs, ids, schedule JSON), never key material.
// Owner changes grant data access to role user and raise a security alert
// (D-067); a manual run request records the requesting user.
const (
	ActionJobCreated      Action = "job.created"
	ActionJobUpdated      Action = "job.updated"
	ActionJobDeleted      Action = "job.deleted"
	ActionJobEnabled      Action = "job.enabled"
	ActionJobDisabled     Action = "job.disabled"
	ActionJobOwnerAdded   Action = "job.owner_added"
	ActionJobOwnerRemoved Action = "job.owner_removed"
	ActionJobRunRequested Action = "job.run_requested"
	// ActionJobRunStartedEarly: startNextRunEarly (task 3.12, D-156) with
	// the pulled-forward and the new next_run_at.
	ActionJobRunStartedEarly Action = "job.run_started_early"
)

// Run actions (task 3.6, D-147; target type run, target id the run id).
// A cancel request records the job, the run status at the request and the
// River job state; runs themselves are operational history in the runs
// table, not audit events.
const (
	ActionRunCancelRequested Action = "run.cancel_requested"
)

// Retention actions (task 3.8, D-148; system actor, target type job with
// the job id, except artifact_missing: target type artifact). Per-object
// outcomes live in the artifacts table (state, state_changed_at); the audit
// log gets one entry before and one after the deletions on a destination.
const (
	// ActionRetentionDeletionPlanned: recorded before the first delete on
	// a destination (eligible runs and object count); a failure aborts.
	ActionRetentionDeletionPlanned Action = "retention.deletion_planned"
	// ActionRetentionSwept: the outcome on a destination (deleted,
	// missing, locked, failed, capped).
	ActionRetentionSwept Action = "retention.swept"
	// ActionRetentionAborted: a safety check stopped retention on a
	// destination (newest complete backup missing or changed, plan
	// invariant violated); also alerted.
	ActionRetentionAborted Action = "retention.aborted"
	// ActionRetentionArtifactMissing: an object vanished without bekup
	// deleting it (unexpected deletion; also alerted).
	ActionRetentionArtifactMissing Action = "retention.artifact_missing"
)

// Passkey actions (task 1.8). Enrollment and removal use auth.mfa.enrolled
// / auth.mfa.removed with details.factor = "passkey".
//
//nolint:gosec // G101: action names that contain "passkey", not credentials
const (
	ActionAuthPasskeyRegistrationBegun  Action = "auth.passkey.registration_begun"
	ActionAuthPasskeyRegistrationFailed Action = "auth.passkey.registration_failed"
	ActionAuthPasskeyRenamed            Action = "auth.passkey.renamed"
	ActionAuthPasskeyAssertionSucceeded Action = "auth.passkey.assertion_succeeded"
	ActionAuthPasskeyAssertionFailed    Action = "auth.passkey.assertion_failed"
	// ActionAuthPasskeySignCountAnomaly: an assertion with a regressed
	// signature counter was refused (possible cloned authenticator).
	ActionAuthPasskeySignCountAnomaly Action = "auth.passkey.sign_count_anomaly"
)

var catalog = []Action{
	ActionAuthLoginSucceeded, ActionAuthLoginFailed, ActionAuthLogout, ActionAuthLockout,
	ActionAuthStepUpSucceeded, ActionAuthStepUpFailed, ActionAuthPasswordChanged, ActionAuthPasswordChangeFailed,
	ActionAuthMFAEnrolled, ActionAuthMFARemoved, ActionAuthRecoveryCodeUsed,
	ActionAuthPasskeyRegistrationBegun, ActionAuthPasskeyRegistrationFailed, ActionAuthPasskeyRenamed,
	ActionAuthPasskeyAssertionSucceeded, ActionAuthPasskeyAssertionFailed, ActionAuthPasskeySignCountAnomaly,
	ActionAuthMFAEnrollmentStarted, ActionAuthMFAVerified, ActionAuthMFAFailed, ActionAuthMFALockout,
	ActionAuthRecoveryCodesRegenerated, ActionAuthLoginSecondFactor, ActionAuthMFARemovalRefused,
	ActionMFAPolicyUpdated,
	ActionUserCreated, ActionUserUpdated, ActionUserDisabled, ActionUserEnabled,
	ActionUserRoleChanged, ActionUserDeleted, ActionUserPasswordReset, ActionUserUnlocked,
	ActionUserSecondFactorsReset,
	ActionSessionRevoked, ActionSessionsRevoked,
	ActionSecretChanged, ActionSettingChanged,
	ActionSecurityOptOut, ActionSecurityInsecureHTTP, ActionSecurityInsecureDBTLS,
	ActionSystemInsecureTransportStarted,
	ActionCLIAdminCreated, ActionCLIAdminPasswordReset, ActionCLIAdminMFAReset, ActionCLIAdminUnlocked,
	ActionCLIAdminDisabled,
	ActionKeysRotated, ActionKeysCheckValueStored, ActionInstanceKeyCreated, ActionCertsRotated,
	ActionAuditVerifyFailed, ActionAuditHeadExportFailed,
	ActionDownloadTicketCreated, ActionDownloadTicketRejected, ActionDownloadStarted, ActionDownloadCompleted,
	ActionDownloadFailed,
	ActionConnectionCreated, ActionConnectionUpdated, ActionConnectionDeleted, ActionConnectionTLSMode,
	ActionConnectionPinConfirmed, ActionConnectionBackupPinStaged, ActionConnectionHostKeyConfirmed,
	ActionConnectionSSHKeyGenerated, ActionConnectionPinMismatch, ActionConnectionHostKeyMismatch,
	ActionDestinationCreated, ActionDestinationUpdated, ActionDestinationDeleted,
	ActionDestinationHostKeyConfirmed, ActionDestinationSSHKeyGenerated, ActionDestinationHostKeyMismatch,
	ActionJobEncryptionPassphraseSet, ActionJobEncryptionRecipientSet, ActionJobEncryptionOptedOut,
	ActionJobEncryptionOptedIn, ActionJobPassphrasesExported,
	ActionJobCreated, ActionJobUpdated, ActionJobDeleted, ActionJobEnabled, ActionJobDisabled,
	ActionJobOwnerAdded, ActionJobOwnerRemoved, ActionJobRunRequested, ActionJobRunStartedEarly,
	ActionRunCancelRequested,
	ActionRetentionDeletionPlanned, ActionRetentionSwept, ActionRetentionAborted, ActionRetentionArtifactMissing,
}

// actionRe is the syntax of an action: at least two dot-separated words,
// at most 128 characters (the audit_log CHECK).
var actionRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Valid reports whether a is in the catalog.
func (a Action) Valid() bool { return slices.Contains(catalog, a) }

// Actions returns the catalog.
func Actions() []Action { return slices.Clone(catalog) }
