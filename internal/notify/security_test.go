package notify

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
)

// TestSecurityAlertsFromAudit: every security hook of task 5.5 (each one
// is an audited action) yields the right mandatory event, audience,
// severity and dedup behavior. The events are what ObserveAudit emits
// (a fake emitter: the pure mapping).
func TestSecurityAlertsFromAudit(t *testing.T) {
	user, admin, job, conn, dest, art, rst := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ip := netip.MustParseAddr("198.51.100.4")
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	aa, ua, sys := audit.UserActor(admin), audit.UserActor(user), audit.SystemActor()
	cli := audit.CLIEvent
	type want struct {
		typ     EventType
		kind    string
		sev     Severity
		subject Subject
		dedup   bool
	}
	cases := []struct {
		name string
		ev   audit.Event
		want []want
	}{
		{"tls downgrade (opt-out)", audit.Event{Actor: aa, Action: audit.ActionSecurityOptOut, TargetType: "connection", TargetID: conn.String(), Details: audit.Details{"setting": "connection.tls_mode"}},
			[]want{{SecuritySystem, KindOptOut, SeverityError, Subject{ConnectionID: conn}, false}, {ConnectionTLSDowngraded, "", "", Subject{ConnectionID: conn}, false}}},
		{"encryption opt-out", audit.Event{Actor: aa, Action: audit.ActionSecurityOptOut, TargetType: "job", TargetID: job.String()},
			[]want{{SecuritySystem, KindOptOut, SeverityError, Subject{JobID: job}, false}}},
		{"pin mismatch", audit.Event{Actor: sys, Action: audit.ActionConnectionPinMismatch, TargetType: "connection", TargetID: conn.String()},
			[]want{{SecuritySystem, KindTLSPinMismatch, SeverityCritical, Subject{ConnectionID: conn}, true}}},
		{"ssh host key mismatch", audit.Event{Actor: aa, Action: audit.ActionConnectionHostKeyMismatch, TargetType: "connection", TargetID: conn.String()},
			[]want{{SecuritySystem, KindSSHHostKeyMismatch, SeverityCritical, Subject{ConnectionID: conn}, true}}},
		{"sftp host key mismatch", audit.Event{Actor: aa, Action: audit.ActionDestinationHostKeyMismatch, TargetType: "destination", TargetID: dest.String()},
			[]want{{SecuritySystem, KindDestinationHostKeyMismatch, SeverityCritical, Subject{DestinationID: dest}, true}}},
		{"owner added", audit.Event{Actor: aa, Action: audit.ActionJobOwnerAdded, TargetType: "job", TargetID: job.String()},
			[]want{{SecuritySystem, KindJobOwnerAdded, SeverityWarning, Subject{JobID: job}, false}}},
		{"owner removed", audit.Event{Actor: aa, Action: audit.ActionJobOwnerRemoved, TargetType: "job", TargetID: job.String()},
			[]want{{SecuritySystem, KindJobOwnerRemoved, SeverityInfo, Subject{JobID: job}, false}}},
		{"passphrase export", audit.Event{Actor: aa, Action: audit.ActionJobPassphrasesExported, TargetType: "job", TargetID: job.String()},
			[]want{{SecuritySystem, KindPassphrasesExported, SeverityWarning, Subject{JobID: job}, false}}},
		{"artifact deleted outside bekup", audit.Event{Actor: sys, Action: audit.ActionRetentionArtifactMissing, TargetType: "artifact", TargetID: art.String(), Details: audit.Details{"job_id": job.String()}},
			[]want{{SecuritySystem, KindArtifactDeletedExternally, SeverityError, Subject{JobID: job, ArtifactID: art}, true}}},
		{"retention aborted", audit.Event{Actor: sys, Action: audit.ActionRetentionAborted, TargetType: "job", TargetID: job.String(), Details: audit.Details{"destination_id": dest.String()}},
			[]want{{SecuritySystem, KindRetentionAborted, SeverityError, Subject{JobID: job}, true}}},
		{"restore verification failed", audit.Event{Actor: sys, Action: audit.ActionRestoreVerificationFailed, TargetType: "restore", TargetID: rst.String(), Details: audit.Details{"job_id": job.String()}},
			[]want{{SecuritySystem, KindRestoreVerificationFailed, SeverityCritical, Subject{JobID: job, RestoreID: rst}, false}}},
		{"request-stage verification failed", audit.Event{Actor: aa, Action: audit.ActionRestoreVerificationFailed, TargetType: "artifact", TargetID: art.String(), Details: audit.Details{"job_id": job.String(), "stage": "request"}},
			[]want{{SecuritySystem, KindRestoreVerificationFailed, SeverityCritical, Subject{JobID: job, ArtifactID: art}, false}}},
		{"privileged restore target", audit.Event{Actor: aa, Action: audit.ActionRestoreRequested, TargetType: "restore", TargetID: rst.String(), Details: audit.Details{"job_id": job.String(), "privileged_target_acknowledged": true}},
			[]want{{SecuritySystem, KindRestorePrivilegedTarget, SeverityWarning, Subject{JobID: job, RestoreID: rst}, false}}},
		{"privileged restore refused", audit.Event{Actor: sys, Action: audit.ActionRestorePrivilegedTarget, TargetType: "restore", TargetID: rst.String(), Details: audit.Details{"job_id": job.String()}},
			[]want{{SecuritySystem, KindRestorePrivilegedRefused, SeverityWarning, Subject{JobID: job, RestoreID: rst}, false}}},
		{"privileged test-restore target", audit.Event{Actor: aa, Action: audit.ActionJobTestRestoreUpdated, TargetType: "job", TargetID: job.String(), Details: audit.Details{"privileged_target_acknowledged": true}},
			[]want{{SecuritySystem, KindTestRestorePrivilegedTarget, SeverityWarning, Subject{JobID: job}, false}}},
		{"custom version query set", audit.Event{Actor: aa, Action: audit.ActionJobVersionQueryChanged, TargetType: "job", TargetID: job.String(), Details: audit.Details{"mode": "custom", "custom_sql_changed": true}},
			[]want{{SecuritySystem, KindJobCustomVersionQuery, SeverityWarning, Subject{JobID: job}, false}}},
		{"custom version query unchanged", audit.Event{Actor: aa, Action: audit.ActionJobVersionQueryChanged, TargetType: "job", TargetID: job.String(), Details: audit.Details{"mode": "custom", "custom_sql_changed": false}},
			nil},
		{"custom version query retargeted", audit.Event{Actor: aa, Action: audit.ActionJobUpdated, TargetType: "job", TargetID: job.String(), Details: audit.Details{"fields": []string{"connection_id"}, "custom_version_query_retargeted": true}},
			[]want{{JobUpdated, "", "", Subject{JobID: job}, false}, {SecuritySystem, KindJobCustomVersionQuery, SeverityWarning, Subject{JobID: job}, false}}},
		{"job updated", audit.Event{Actor: aa, Action: audit.ActionJobUpdated, TargetType: "job", TargetID: job.String(), Details: audit.Details{"fields": []string{"connection_id"}}},
			[]want{{JobUpdated, "", "", Subject{JobID: job}, false}}},
		{"version preset set", audit.Event{Actor: aa, Action: audit.ActionJobVersionQueryChanged, TargetType: "job", TargetID: job.String(), Details: audit.Details{"mode": "preset", "custom_sql_changed": true}},
			nil},
		{"secdef neutralized", audit.Event{Actor: sys, Action: audit.ActionRestoreSecdefNeutralized, TargetType: "restore", TargetID: rst.String(), Details: audit.Details{"job_id": job.String()}},
			[]want{{SecuritySystem, KindSecdefNeutralized, SeverityWarning, Subject{JobID: job, RestoreID: rst}, false}}},
		{"2fa policy weakened", audit.Event{Actor: aa, Action: audit.ActionMFAPolicyUpdated, TargetType: "setting", TargetID: "security.two_factor_policy", Details: audit.Details{"weakened": true}},
			[]want{{SecuritySystem, KindTwoFactorPolicyWeakened, SeverityError, Subject{}, false}}},
		{"ssrf allowlist widened", audit.Event{Actor: aa, Action: audit.ActionSettingChanged, TargetType: "setting", TargetID: "netguard.allowlist", Details: audit.Details{"setting": "netguard.allowlist", "widened": true}},
			[]want{{SecuritySystem, KindSSRFAllowlistWidened, SeverityError, Subject{}, false}}},
		{"ssrf allowlist narrowed", audit.Event{Actor: aa, Action: audit.ActionSettingChanged, TargetType: "setting", TargetID: "netguard.allowlist", Details: audit.Details{"setting": "netguard.allowlist", "widened": false}},
			nil},
		{"other setting widened flag", audit.Event{Actor: aa, Action: audit.ActionSettingChanged, TargetType: "setting", TargetID: "smtp", Details: audit.Details{"setting": "smtp", "widened": true}},
			nil},
		{"config reload weakened", audit.Event{Actor: sys, Action: audit.ActionConfigReloaded, TargetType: "config", Details: audit.Details{"request_id": rst.String(), "trigger": "ui", "weakened": []string{"server.trusted_proxies", "security.lockout_threshold"}}},
			[]want{{SecuritySystem, KindConfigReloadWeakened, SeverityError, Subject{}, true}}},
		{"config reload not weakened", audit.Event{Actor: sys, Action: audit.ActionConfigReloaded, TargetType: "config", Details: audit.Details{"request_id": rst.String(), "weakened": []string{}, "applied": []string{"log.level"}}},
			nil},
		{"config reload failed", audit.Event{Actor: sys, Action: audit.ActionConfigReloadFailed, TargetType: "config", Details: audit.Details{"error_kind": "invalid"}},
			nil},
		{"passkey counter anomaly", audit.Event{Actor: ua, Action: audit.ActionAuthPasskeySignCountAnomaly, IP: ip},
			[]want{{SecurityAccount, KindPasskeyCloneSuspected, SeverityError, Subject{UserID: user}, false}, {SecuritySystem, KindPasskeyCloneSuspected, SeverityError, Subject{UserID: user}, false}}},
		{"rejected download ticket", audit.Event{Actor: sys, Action: audit.ActionDownloadTicketRejected, TargetType: "artifact", TargetID: art.String(), Details: audit.Details{"ticket_id": uuid.NewString()}},
			[]want{{SecuritySystem, KindDownloadRejected, SeverityWarning, Subject{ArtifactID: art}, true}}},
		{"plain http startup", audit.Event{Actor: sys, Action: audit.ActionSystemInsecureTransportStarted, TargetType: "server"},
			[]want{{SecuritySystem, KindInsecureTransport, SeverityError, Subject{}, true}}},
		{"weak db tls startup", audit.Event{Actor: sys, Action: audit.ActionSecurityInsecureDBTLS, TargetType: "server"},
			[]want{{SecuritySystem, KindInsecureDBTLS, SeverityError, Subject{}, true}}},
		{"single-account db roles startup", audit.Event{Actor: sys, Action: audit.ActionSecurityInsecureDBRoles, TargetType: "server"},
			[]want{{SecuritySystem, KindInsecureDBRoles, SeverityError, Subject{}, true}}},
		{"unpinned image startup", audit.Event{Actor: sys, Action: audit.ActionSecurityUnpinnedImage, TargetType: "server"},
			[]want{{SecuritySystem, KindUnpinnedImage, SeverityWarning, Subject{}, true}}},
		{"serve alias conflict", audit.Event{Actor: sys, Action: audit.ActionSecurityServeAliasConflict, TargetType: "server", Details: audit.Details{"alias": "bekup-serve", "addresses": []string{"172.20.0.66"}}},
			[]want{{SecuritySystem, KindServeAliasConflict, SeverityCritical, Subject{}, true}}},
		{"db bootstrap via cli", audit.CLIEvent(audit.ActionCLIDBBootstrap, "database", "bekup", nil),
			[]want{{SecuritySystem, KindCLIAction, SeverityWarning, Subject{}, false}}},
		{"head export failed", audit.Event{Actor: sys, Action: audit.ActionAuditHeadExportFailed},
			[]want{{SecuritySystem, KindAuditHeadExportFailed, SeverityError, Subject{}, true}}},
		{"audit verify failed", audit.Event{Actor: sys, Action: audit.ActionAuditVerifyFailed, TargetType: "audit_log", TargetID: "12", Details: audit.Details{"finding": "hash_mismatch:12"}},
			[]want{{SecuritySystem, KindAuditVerifyFailed, SeverityCritical, Subject{}, true}}},
		{"keys rotated", audit.Event{Actor: sys, Action: audit.ActionKeysRotated, TargetType: "keyring"},
			[]want{{SecuritySystem, KindKeysRotated, SeverityWarning, Subject{}, false}}},
		{"recovery code used", audit.Event{Actor: ua, Action: audit.ActionAuthRecoveryCodeUsed, IP: ip},
			[]want{{SecurityAccount, KindRecoveryCodeUsed, "", Subject{UserID: user}, false}}},
		{"lockout", audit.Event{Actor: sys, Action: audit.ActionAuthLockout, TargetType: "user", TargetID: user.String()},
			[]want{{SecurityAccount, KindAccountLocked, "", Subject{UserID: user}, false}}},
		{"factor reset by admin", audit.Event{Actor: aa, Action: audit.ActionUserSecondFactorsReset, TargetType: "user", TargetID: user.String()},
			[]want{{SecurityAccount, KindSecondFactorsReset, "", Subject{UserID: user}, false}}},
		{"new admin", audit.Event{Actor: aa, Action: audit.ActionUserRoleChanged, TargetType: "user", TargetID: user.String(), Details: audit.Details{"role": "admin"}},
			[]want{{SecurityAccount, KindRoleChanged, "", Subject{UserID: user}, false}, {SecuritySystem, KindAdminGranted, SeverityWarning, Subject{UserID: user}, false}}},
		// Break-glass CLI (D-069).
		{"cli reset-password", cli(audit.ActionCLIAdminPasswordReset, "user", user.String(), nil),
			[]want{{SecurityAccount, KindPasswordReset, SeverityError, Subject{UserID: user}, false}, {SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli reset-2fa", cli(audit.ActionCLIAdminMFAReset, "user", user.String(), nil),
			[]want{{SecurityAccount, KindSecondFactorsReset, SeverityError, Subject{UserID: user}, false}, {SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli unlock", cli(audit.ActionCLIAdminUnlocked, "user", user.String(), nil),
			[]want{{SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli disable", cli(audit.ActionCLIAdminDisabled, "user", user.String(), nil),
			[]want{{SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli create admin", cli(audit.ActionCLIAdminCreated, "user", user.String(), audit.Details{"role": "admin"}),
			[]want{{SecuritySystem, KindAdminCreated, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli create user", cli(audit.ActionCLIAdminCreated, "user", user.String(), audit.Details{"role": "user"}),
			[]want{{SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
		{"cli certs rotate", cli(audit.ActionCertsRotated, "certs", "", nil),
			[]want{{SecuritySystem, KindCertsRotated, SeverityWarning, Subject{}, false}}},
		{"cli reindex import", cli(audit.ActionCLIRunImported, "run", uuid.NewString(), nil),
			[]want{{SecuritySystem, KindCLIAction, SeverityWarning, Subject{}, false}}},
		{"cli role change", cli(audit.ActionUserRoleChanged, "user", user.String(), audit.Details{"role": "maintainer"}),
			[]want{{SecurityAccount, KindRoleChanged, "", Subject{UserID: user}, false}, {SecuritySystem, KindCLIAction, SeverityWarning, Subject{UserID: user}, false}}},
	}
	for _, c := range cases {
		got := eventsFromAudit(c.ev, now)
		if len(got) != len(c.want) {
			t.Errorf("%s: %d events, want %d: %+v", c.name, len(got), len(c.want), got)
			continue
		}
		for i, w := range c.want {
			g := got[i]
			if g.Type != w.typ || g.Subject != w.subject {
				t.Errorf("%s[%d]: got %s %+v, want %s %+v", c.name, i, g.Type, g.Subject, w.typ, w.subject)
			}
			if w.kind != "" && g.Detail["kind"] != w.kind {
				t.Errorf("%s[%d]: kind %v, want %s", c.name, i, g.Detail["kind"], w.kind)
			}
			if w.sev != "" && g.Severity != w.sev {
				t.Errorf("%s[%d]: severity %s, want %s", c.name, i, g.Severity, w.sev)
			}
			if w.dedup != (g.DedupKey != "") {
				t.Errorf("%s[%d]: dedup key %q", c.name, i, g.DedupKey)
			}
			if _, err := g.validate(); err != nil {
				t.Errorf("%s[%d]: %v", c.name, i, err)
			}
			if _, err := ValidateDetail(g.Detail); err != nil {
				t.Errorf("%s[%d]: %v", c.name, i, err)
			}
			if c.ev.Actor.Kind == audit.ActorCLI && g.Type == SecuritySystem &&
				(g.Detail["via"] != "cli" || g.Detail["action"] != string(c.ev.Action)) {
				t.Errorf("%s[%d]: CLI facts missing: %+v", c.name, i, g.Detail)
			}
			if m, _ := Lookup(g.Type); g.Type == SecuritySystem || g.Type == SecurityAccount {
				if !m.Mandatory {
					t.Errorf("%s: %s is not mandatory", c.name, g.Type)
				}
			}
		}
	}

	// Not alerted: an unweakened policy change, an unacknowledged restore
	// or test-restore target.
	for _, ev := range []audit.Event{
		{Actor: aa, Action: audit.ActionMFAPolicyUpdated, Details: audit.Details{"weakened": false}},
		{Actor: aa, Action: audit.ActionRestoreRequested, TargetType: "restore", TargetID: rst.String(), Details: audit.Details{"privileged_target_acknowledged": false}},
		{Actor: aa, Action: audit.ActionJobTestRestoreUpdated, TargetType: "job", TargetID: job.String()},
		{Actor: sys, Action: audit.ActionAuthLoginFailed, TargetType: "user", IP: ip},
	} {
		for _, g := range eventsFromAudit(ev, now) {
			if g.Type == SecuritySystem {
				t.Errorf("%s alerted: %+v", ev.Action, g)
			}
		}
	}

	// Posture alerts repeat at most daily and share the startup key.
	a := eventsFromAudit(audit.Event{Actor: sys, Action: audit.ActionSystemInsecureTransportStarted}, now)
	b := eventsFromAudit(audit.Event{Actor: sys, Action: audit.ActionSystemInsecureTransportStarted}, now.Add(time.Hour))
	if a[0].DedupKey != b[0].DedupKey || a[0].DedupKey != PostureDedupKey(KindInsecureTransport, now) {
		t.Fatalf("posture dedup keys %q / %q", a[0].DedupKey, b[0].DedupKey)
	}
	// No secret-ish detail is ever copied: the CLI's details pass only the
	// OS user and host name.
	ev := cli(audit.ActionCLIAdminPasswordReset, "user", user.String(), audit.Details{"temporary_until": "x", "email": "a@b.c"})
	for _, g := range eventsFromAudit(ev, now) {
		if _, ok := g.Detail["email"]; ok {
			t.Fatal("e-mail address copied into an alert")
		}
	}
}

func TestDedupPart(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "none",
		"2001:DB8::1":            "2001-db8--1",
		"Hash_Mismatch":          "hash_mismatch",
		strings.Repeat("a", 100): strings.Repeat("a", 64),
	} {
		if got := dedupPart(in); got != want {
			t.Errorf("dedupPart(%q) = %q, want %q", in, got, want)
		}
		if !dedupRe.MatchString("x" + dedupPart(in)) {
			t.Errorf("dedupPart(%q) is not a valid key part", in)
		}
	}
}

func TestSystemBody(t *testing.T) {
	cases := map[string]Detail{
		"203.0.113.9":                     {"kind": KindLoginBurstAddress, "ip": "203.0.113.9", "failures": float64(25), "window_minutes": float64(15)},
		"runner_client":                   {"kind": KindCertExpiring, "component": "runner_client", "not_after": "2026-11-04", "threshold_days": float64(7)},
		"2026-11-04":                      {"kind": KindPinExpired, "not_after": "2026-11-04"},
		"cli.admin":                       {"kind": KindCLIAction, "action": "cli.admin.unlocked"},
		"plain HTTP":                      {"kind": KindInsecureTransport},
		"cloned":                          {"kind": KindPasskeyCloneSuspected},
		"chain verifica":                  {"kind": KindAuditVerifyFailed},
		"failed sign-ins":                 {"kind": KindLoginBurstAccount, "failures": float64(10), "window_minutes": float64(15)},
		"Personal webhooks of every user": {"kind": KindSSRFAllowlistWidened, "personal_webhooks": true},
		"Changed: server.trusted_proxies": {"kind": KindConfigReloadWeakened, "settings": "server.trusted_proxies"},
	}
	for want, d := range cases {
		if got := systemBody(d); !strings.Contains(got, want) {
			t.Errorf("systemBody(%v) = %q, want it to contain %q", d, got, want)
		}
	}
	if strings.Contains(systemBody(Detail{"kind": KindSSRFAllowlistWidened}), "Personal") {
		t.Error("personal sentence without the flag")
	}
	// The audit flag reaches the alert detail (D-198).
	ev := audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged,
		Details: audit.Details{"setting": ssrfAllowlistSetting, "widened": true, "personal_webhooks_widened": true}}
	var found bool
	for _, g := range eventsFromAudit(ev, time.Now()) {
		if g.Detail["kind"] == KindSSRFAllowlistWidened && g.Detail["personal_webhooks"] == true {
			found = true
		}
	}
	if !found {
		t.Error("personal webhook widening not flagged in the alert")
	}
	// Every system kind has a sentence.
	for kind := range systemSeverity {
		if systemBody(Detail{"kind": kind}) == "" {
			t.Errorf("kind %s has no message", kind)
		}
	}
}

// TestSecurityAlertTypes: every security.system kind with a severity is
// listed with a description and a repeat rule, and every kind with a
// fixed message has a severity (the reference cannot drift).
func TestSecurityAlertTypes(t *testing.T) {
	types := SecurityAlertTypes()
	seen := map[string]bool{}
	for i, a := range types {
		if a.Description == "" || a.Repeat == "" || !a.Severity.Valid() {
			t.Errorf("%s: incomplete %+v", a.Kind, a)
		}
		if seen[a.Kind] {
			t.Errorf("%s listed twice", a.Kind)
		}
		seen[a.Kind] = true
		if i > 0 && severityRank[types[i-1].Severity] > severityRank[a.Severity] {
			t.Errorf("not sorted by severity at %s", a.Kind)
		}
	}
	for kind := range systemMessages {
		if !seen[kind] {
			t.Errorf("%s has a message but no severity", kind)
		}
	}
	for kind := range alertRepeat {
		if !seen[kind] {
			t.Errorf("repeat rule for unknown kind %s", kind)
		}
	}
	if !seen[KindSSRFAllowlistWidened] || !seen[KindLoginBurstGlobal] {
		t.Fatal("missing kinds")
	}
}

// TestServeAliasConflictAlert: the alert names the foreign addresses
// (addresses only) and is deduplicated per address set and day (D-215).
func TestServeAliasConflictAlert(t *testing.T) {
	now := time.Now()
	mk := func(addrs ...any) Event {
		ev := audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSecurityServeAliasConflict, TargetType: "server",
			Details: audit.Details{"alias": "bekup-serve", "addresses": addrs}}
		got := eventsFromAudit(ev, now)
		if len(got) != 1 || got[0].Type != SecuritySystem {
			t.Fatalf("events %+v", got)
		}
		return got[0]
	}
	a := mk("172.20.0.66", "fd00::66")
	b := mk("fd00::66", "172.20.0.66")
	c := mk("172.20.0.67")
	if a.DedupKey == "" || a.DedupKey != b.DedupKey || a.DedupKey == c.DedupKey {
		t.Fatalf("dedup keys %q %q %q", a.DedupKey, b.DedupKey, c.DedupKey)
	}
	if a.Detail["addresses"] != "172.20.0.66, fd00::66" || a.Severity != SeverityCritical {
		t.Fatalf("event %+v", a)
	}
	if d := mk("172.20.0.66", "not an address", 42); d.Detail["addresses"] != "172.20.0.66" {
		t.Fatalf("detail %+v", d.Detail)
	}
	if _, err := ValidateDetail(a.Detail); err != nil {
		t.Fatal(err)
	}
}

// TestConfigReloadWeakenedAlert: replicas answering one request with the
// same weakened keys share a dedup key; other keys or requests do not;
// values never reach the alert, only key names (D-200).
func TestConfigReloadWeakenedAlert(t *testing.T) {
	req := uuid.NewString()
	mk := func(request string, keys ...any) Event {
		ev := audit.Event{Actor: audit.SystemActor(), Action: audit.ActionConfigReloaded, TargetType: "config",
			Details: audit.Details{"request_id": request, "trigger": "ui", "weakened": keys}}
		got := eventsFromAudit(ev, time.Now())
		if len(got) != 1 {
			t.Fatalf("events %+v", got)
		}
		return got[0]
	}
	a := mk(req, "server.trusted_proxies", "netguard.allow_cidrs")
	b := mk(req, "netguard.allow_cidrs", "server.trusted_proxies")
	c := mk(req, "server.trusted_proxies")
	d := mk(uuid.NewString(), "server.trusted_proxies", "netguard.allow_cidrs")
	if a.DedupKey == "" || a.DedupKey != b.DedupKey || a.DedupKey == c.DedupKey || a.DedupKey == d.DedupKey {
		t.Fatalf("dedup keys %q %q %q %q", a.DedupKey, b.DedupKey, c.DedupKey, d.DedupKey)
	}
	if a.Detail["count"] != 2 || a.Detail["settings"] != "netguard.allow_cidrs, server.trusted_proxies" {
		t.Fatalf("detail %+v", a.Detail)
	}
	if _, err := ValidateDetail(a.Detail); err != nil {
		t.Fatal(err)
	}
	// Anything that is not a key name is dropped.
	e := mk(req, "server.trusted_proxies", "10.0.0.0/8", 42)
	if e.Detail["count"] != 1 {
		t.Fatalf("non-key entries kept: %+v", e.Detail)
	}
	// Many keys stay within the detail bounds.
	many := make([]any, 0, 40)
	for i := range 40 {
		many = append(many, "security.key_"+strings.Repeat("x", i%10)+string(rune('a'+i%26)))
	}
	if _, err := ValidateDetail(mk(req, many...).Detail); err != nil {
		t.Fatal(err)
	}
}
