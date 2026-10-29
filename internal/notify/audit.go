package notify

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// ObserveAudit is the audit.Observer of serve and the workers: it turns
// the audited actions that are notification events (job and connection
// changes, own-account security events, system security events) into
// events, in the transaction of the audit entry. Keeping the mapping here
// means producers that already audit need no extra emit call (D-161).
//
// Only fixed facts are copied (identifiers, a kind, the client IP for
// own-account events); audit details are never forwarded.
func ObserveAudit(ctx context.Context, q *sqlc.Queries, ev audit.Event) error {
	now := time.Now().UTC()
	events := eventsFromAudit(ev, now)
	for i := range events {
		if _, err := Emit(ctx, q, events[i]); err != nil {
			return err
		}
	}
	if ev.Action == audit.ActionAuthLoginFailed {
		return loginBurst(ctx, q, ev, now)
	}
	return nil
}

// Own-account (security.account) kinds.
const (
	KindSignIn                   = "sign_in"
	KindPasswordChanged          = "password_changed"
	KindPasswordReset            = "password_reset"
	KindSecondFactorAdded        = "second_factor_added"
	KindSecondFactorRemoved      = "second_factor_removed"
	KindRecoveryCodeUsed         = "recovery_code_used"
	KindRecoveryCodesRegenerated = "recovery_codes_regenerated"
	KindSecondFactorsReset       = "second_factors_reset"
	KindAccountLocked            = "account_locked"
	KindSecondFactorLocked       = "second_factor_locked"
	KindRoleChanged              = "role_changed"
)

// System (security.system) kinds.
const (
	KindOptOut                     = "opt_out"
	KindAdminCreated               = "admin_created"
	KindAdminGranted               = "admin_granted"
	KindKeysRotated                = "keys_rotated"
	KindInstanceKeyCreated         = "instance_key_created"
	KindCertsRotated               = "certs_rotated"
	KindAuditVerifyFailed          = "audit_verify_failed"
	KindTLSPinMismatch             = "tls_pin_mismatch"
	KindSSHHostKeyMismatch         = "ssh_host_key_mismatch"
	KindDestinationHostKeyMismatch = "destination_host_key_mismatch"
	KindRestoreVerificationFailed  = "restore_verification_failed"
)

var accountKinds = map[audit.Action]string{
	audit.ActionAuthPasswordChanged:          KindPasswordChanged,
	audit.ActionUserPasswordReset:            KindPasswordReset,
	audit.ActionAuthMFAEnrolled:              KindSecondFactorAdded,
	audit.ActionAuthMFARemoved:               KindSecondFactorRemoved,
	audit.ActionAuthRecoveryCodeUsed:         KindRecoveryCodeUsed,
	audit.ActionAuthRecoveryCodesRegenerated: KindRecoveryCodesRegenerated,
	audit.ActionUserSecondFactorsReset:       KindSecondFactorsReset,
	audit.ActionAuthLockout:                  KindAccountLocked,
	audit.ActionAuthMFALockout:               KindSecondFactorLocked,
	audit.ActionUserRoleChanged:              KindRoleChanged,
}

var systemKinds = map[audit.Action]string{
	audit.ActionSecurityOptOut:             KindOptOut,
	audit.ActionKeysRotated:                KindKeysRotated,
	audit.ActionInstanceKeyCreated:         KindInstanceKeyCreated,
	audit.ActionCertsRotated:               KindCertsRotated,
	audit.ActionAuditVerifyFailed:          KindAuditVerifyFailed,
	audit.ActionConnectionPinMismatch:      KindTLSPinMismatch,
	audit.ActionConnectionHostKeyMismatch:  KindSSHHostKeyMismatch,
	audit.ActionDestinationHostKeyMismatch: KindDestinationHostKeyMismatch,
	audit.ActionRestoreVerificationFailed:  KindRestoreVerificationFailed,
}

var jobActions = map[audit.Action]EventType{
	audit.ActionJobCreated:                 JobCreated,
	audit.ActionJobUpdated:                 JobUpdated,
	audit.ActionJobDisabled:                JobDisabled,
	audit.ActionJobEnabled:                 JobEnabled,
	audit.ActionJobDeleted:                 JobDeleted,
	audit.ActionJobEncryptionRecipientSet:  JobEncryptionChanged,
	audit.ActionJobEncryptionPassphraseSet: JobEncryptionChanged,
	audit.ActionJobEncryptionOptedOut:      JobEncryptionChanged,
	audit.ActionJobEncryptionOptedIn:       JobEncryptionChanged,
}

var connectionActions = map[audit.Action]EventType{
	audit.ActionConnectionPinConfirmed:     ConnectionPinChanged,
	audit.ActionConnectionBackupPinStaged:  ConnectionPinChanged,
	audit.ActionConnectionHostKeyConfirmed: ConnectionHostKeyChanged,
}

// targetID parses the audit target when it has the given type.
func targetID(ev audit.Event, typ string) uuid.UUID {
	if ev.TargetType != typ {
		return uuid.Nil
	}
	id, err := uuid.Parse(ev.TargetID)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// subjectUser is the account an own-account action concerns: the target
// user, else the acting user.
func subjectUser(ev audit.Event) uuid.UUID {
	if id := targetID(ev, "user"); id != uuid.Nil {
		return id
	}
	if ev.Actor.Kind == audit.ActorUser && ev.Actor.UserID != nil {
		return *ev.Actor.UserID
	}
	return uuid.Nil
}

func detailString(d audit.Details, k string) string {
	s, ok := d[k].(string)
	if !ok || !cleanText(s, 64) {
		return ""
	}
	return s
}

// fromAudit maps one audit event to a notification event.
func fromAudit(ev audit.Event, now time.Time) (Event, bool) {
	if t, ok := jobActions[ev.Action]; ok {
		id := targetID(ev, "job")
		if id == uuid.Nil {
			return Event{}, false
		}
		return Event{Type: t, Subject: Subject{JobID: id}, Detail: Detail{"action": string(ev.Action)}}, true
	}
	if t, ok := connectionActions[ev.Action]; ok {
		id := targetID(ev, "connection")
		if id == uuid.Nil {
			return Event{}, false
		}
		return Event{Type: t, Subject: Subject{ConnectionID: id}}, true
	}
	switch ev.Action {
	case audit.ActionAuthLoginSucceeded:
		// Only a complete sign-in: with a second factor pending, the
		// completion (auth.login.second_factor) reports it.
		if pending, ok := ev.Details["second_factor_pending"].(bool); ok && pending {
			return Event{}, false
		}
		return signIn(ev, now)
	case audit.ActionAuthLoginSecondFactor:
		return signIn(ev, now)
	case audit.ActionUserCreated:
		if detailString(ev.Details, "role") != "admin" {
			return Event{}, false
		}
		return system(ev, KindAdminCreated), true
	default:
	}
	if kind, ok := accountKinds[ev.Action]; ok {
		u := subjectUser(ev)
		if u == uuid.Nil {
			return Event{}, false
		}
		d := Detail{"kind": kind}
		if ev.IP.IsValid() {
			d["ip"] = clientIP(ev.IP)
		}
		if ev.Action == audit.ActionUserRoleChanged {
			d["role"] = detailString(ev.Details, "role")
		}
		return Event{Type: SecurityAccount, Subject: Subject{UserID: u}, Detail: d}, true
	}
	if kind, ok := systemKinds[ev.Action]; ok {
		return system(ev, kind), true
	}
	return Event{}, false
}

// system builds a security.system event with the identifiers of the audit
// target.
func system(ev audit.Event, kind string) Event {
	out := Event{Type: SecuritySystem, Detail: Detail{"kind": kind}}
	if s := detailString(ev.Details, "setting"); s != "" {
		out.Detail["setting"] = s
	}
	out.Subject.ConnectionID = targetID(ev, "connection")
	out.Subject.DestinationID = targetID(ev, "destination")
	out.Subject.JobID = targetID(ev, "job")
	out.Subject.RestoreID = targetID(ev, "restore")
	out.Subject.UserID = targetID(ev, "user")
	out.Subject.ArtifactID = targetID(ev, "artifact")
	if out.Subject.JobID == uuid.Nil {
		// Restore, retention and artifact entries name their job in the
		// details.
		if id, err := uuid.Parse(detailString(ev.Details, "job_id")); err == nil {
			out.Subject.JobID = id
		}
	}
	return out
}

// clientIP is the canonical text of a client address for details and
// dedup keys: no IPv6 zone (a link-local "fe80::1%eth0" is "fe80::1", so
// the zone can neither split nor inject into a key), IPv4-mapped
// addresses unmapped, lowercase.
func clientIP(a netip.Addr) string { return strings.ToLower(a.WithZone("").Unmap().String()) }

// signIn is the "new sign-in" own-account event, at most one per account,
// client address and day.
func signIn(ev audit.Event, now time.Time) (Event, bool) {
	u := subjectUser(ev)
	if u == uuid.Nil {
		return Event{}, false
	}
	d := Detail{"kind": KindSignIn}
	ip := "unknown"
	if ev.IP.IsValid() {
		ip = clientIP(ev.IP)
		d["ip"] = ip
	}
	return Event{
		Type: SecurityAccount, Subject: Subject{UserID: u}, Detail: d,
		DedupKey: "security.account:sign_in:" + u.String() + ":" + ip + ":" + now.Format("2006-01-02"),
	}, true
}

// extraFromAudit returns the additional events of an audit entry beyond
// fromAudit: a role change to admin is both an own-account event and a
// system event; a connection TLS downgrade (security.opt_out on a
// connection) is also the staff event connection.tls_downgraded.
func extraFromAudit(ev audit.Event) []Event {
	switch ev.Action {
	case audit.ActionUserRoleChanged:
		if detailString(ev.Details, "role") == "admin" {
			return []Event{system(ev, KindAdminGranted)}
		}
	case audit.ActionSecurityOptOut:
		if id := targetID(ev, "connection"); id != uuid.Nil {
			return []Event{{Type: ConnectionTLSDowngraded, Subject: Subject{ConnectionID: id}}}
		}
	default:
	}
	return nil
}
