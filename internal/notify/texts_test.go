package notify

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
)

// textKinds lists the kinds with a fixed sentence under prefix
// (accountTextPrefix or systemTextPrefix), sorted.
func textKinds(prefix string) []string {
	var out []string
	for k := range texts[i18n.EN] {
		if kind, ok := strings.CutPrefix(k, prefix); ok {
			out = append(out, kind)
		}
	}
	sort.Strings(out)
	return out
}

// TestTextsParity: every language has every key with the same verbs.
func TestTextsParity(t *testing.T) {
	if p := texts.Problems(); len(p) > 0 {
		t.Fatalf("catalog problems:\n%s", strings.Join(p, "\n"))
	}
}

// TestFixedAlertTextsCovered: every account and system kind that has a
// fixed sentence keeps one in every language (the former
// accountMessages / systemMessages maps), and nothing else hides under
// those prefixes.
func TestFixedAlertTextsCovered(t *testing.T) {
	account := []string{
		KindSignIn, KindPasswordChanged, KindPasswordReset, KindSecondFactorAdded, KindSecondFactorRemoved,
		KindRecoveryCodeUsed, KindRecoveryCodesRegenerated, KindSecondFactorsReset, KindAccountLocked,
		KindSecondFactorLocked, KindRoleChanged, KindPasskeyCloneSuspected, KindAPIAddressDenied,
		KindAPITokenAddressesWidened, KindRiskAcceptanceExpiring, KindRiskAcceptanceEnded,
	}
	system := []string{
		KindOptOut, KindAdminCreated, KindAdminGranted, KindKeysRotated, KindInstanceKeyCreated, KindCertsRotated,
		KindAuditVerifyFailed, KindTLSPinMismatch, KindSSHHostKeyMismatch, KindDestinationHostKeyMismatch,
		KindDestinationTLSPinMismatch, KindDestinationTLSPinCleared, KindRestoreVerificationFailed,
		KindJobOwnerAdded, KindJobOwnerRemoved, KindPassphrasesExported, KindArtifactDeletedExternally,
		KindRetentionAborted, KindRestorePrivilegedTarget, KindRestorePrivilegedRefused,
		KindTestRestorePrivilegedTarget, KindJobCustomVersionQuery, KindSecdefNeutralized,
		KindTwoFactorPolicyWeakened, KindPasskeyCloneSuspected, KindDownloadRejected, KindInsecureTransport,
		KindInsecureDBTLS, KindInsecureDBRoles, KindUnpinnedImage, KindServeAliasConflict,
		KindExternalAPIAliasConflict, KindAuditHeadExportFailed, KindCLIAction, KindSSRFAllowlistWidened,
		KindConfigReloadWeakened,
	}
	for _, c := range []struct {
		prefix string
		kinds  []string
	}{{accountTextPrefix, account}, {systemTextPrefix, system}} {
		sort.Strings(c.kinds)
		if got := textKinds(c.prefix); strings.Join(got, ",") != strings.Join(c.kinds, ",") {
			t.Errorf("%s kinds = %v, want %v", c.prefix, got, c.kinds)
		}
		for _, l := range i18n.Locales {
			for _, k := range c.kinds {
				if texts[l][c.prefix+k] == "" {
					t.Errorf("%s: no text %s%s", l, c.prefix, k)
				}
			}
		}
	}
	for _, kind := range account {
		if accountMessage(textsFor(i18n.NL), kind) == accountMessage(textsFor(i18n.EN), kind) {
			t.Errorf("account %s: Dutch equals English", kind)
		}
	}
	// Every alert kind with a severity has a sentence in every language.
	for kind := range systemSeverity {
		for _, l := range i18n.Locales {
			if systemBody(textsFor(l), l, Detail{"kind": kind}) == "" {
				t.Errorf("%s: kind %s has no message", l, kind)
			}
		}
	}
	for _, k := range []string{"insecure_transport", "insecure_db_tls", "insecure_db_roles", "unpinned_image",
		"connection_tls", "privileged_role", "smtp_unencrypted", "encryption_opt_out"} {
		if riskLabel(textsFor(i18n.EN), k) == riskLabel(textsFor(i18n.EN), "") {
			t.Errorf("risk kind %s has no label", k)
		}
	}
}

// TestTitlesMatchLabels: every catalog event has a title text, and the
// English title is the catalog label (the API keeps showing Label).
func TestTitlesMatchLabels(t *testing.T) {
	for _, m := range Catalog() {
		en, ok := texts[i18n.EN]["title."+string(m.Type)]
		if !ok {
			t.Errorf("%s: no title text", m.Type)
			continue
		}
		if en != m.Label {
			t.Errorf("%s: English title %q, label %q", m.Type, en, m.Label)
		}
	}
}

// englishMarkers are words that betray an untranslated English sentence.
var englishMarkers = []string{" the ", "The ", " was ", " were ", " and ", " your ", " has been ", " failed", " with "}

// keyLike matches a catalog key that leaked into a text (a missing key
// renders as its own name).
var keyLike = regexp.MustCompile(`\b(title|body|account|system|alert|api|risk|email|flood|name)\.[a-z_]+\b`)

// assertWellFormed fails on fmt artifacts (a wrong argument count) and on
// leaked catalog keys, in any language.
func assertWellFormed(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, "%!") || strings.Contains(text, "(MISSING)") {
		t.Errorf("%s: formatting artifact in %q", what, text)
	}
	if k := keyLike.FindString(text); k != "" && !strings.Contains(text, "server.") {
		t.Errorf("%s: catalog key %q leaked into %q", what, k, text)
	}
}

func assertDutch(t *testing.T, what, nl, en string) {
	t.Helper()
	assertWellFormed(t, what+" (en)", en)
	assertWellFormed(t, what+" (nl)", nl)
	if nl == en {
		t.Errorf("%s: Dutch equals English: %q", what, nl)
	}
	for _, w := range englishMarkers {
		if strings.Contains(nl, w) {
			t.Errorf("%s: Dutch text %q contains %q", what, nl, w)
		}
	}
}

// TestRenderDutch: every catalog event and every alert kind renders a
// Dutch title and body, and the e-mail parts are Dutch.
func TestRenderDutch(t *testing.T) {
	s := testService(t)
	job, run := uuid.New(), uuid.New()
	created := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	details := map[EventType]string{
		RetentionSummary:    `{"date":"2026-11-04","runs":3,"files":1,"bytes":42}`,
		RetentionBlocked:    `{"held_runs":1,"since":"2026-10-01","newest_complete":"2026-09-30"}`,
		DestinationLowSpace: `{"cause":"below_newest_set","free_bytes":1073741824,"total_bytes":2147483648,"newest_set_bytes":2147483648,"low_since":"2026-11-05T10:00:00Z","reminder":1}`,
		SecurityAccount:     `{"kind":"sign_in"}`,
		SecuritySystem:      `{"kind":"tls_pin_mismatch"}`,
		BackupFailed:        `{"error_code":"dump_failed"}`,
	}
	for _, m := range Catalog() {
		if m.Type == SummaryPeriodic {
			continue // the fallback is checked below; the summary renders its own content
		}
		detail := details[m.Type]
		if detail == "" {
			detail = `{}`
		}
		e := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(m.Type), Severity: string(m.Severity), JobID: &job,
			RunID: &run, ConnectionID: &job, DestinationID: &job, Detail: json.RawMessage(detail), CreatedAt: created,
			CollapsedCount: 2}
		n := names{job: "nightly", connection: "pg-main", destination: "offsite"}
		en, nl := s.render(e, m, n, i18n.EN), s.render(e, m, n, i18n.NL)
		assertDutch(t, string(m.Type)+" title", nl.Title, en.Title)
		assertDutch(t, string(m.Type)+" body", nl.Body, en.Body)
		if nl.Link != en.Link {
			t.Errorf("%s: link differs: %q vs %q", m.Type, nl.Link, en.Link)
		}
		if !strings.Contains(nl.Body, "2 vergelijkbare gebeurtenissen samengevoegd") {
			t.Errorf("%s: collapsed suffix missing: %q", m.Type, nl.Body)
		}
		mail, err := emailFor(e, nl, n, i18n.NL)
		if err != nil {
			t.Fatalf("%s: %v", m.Type, err)
		}
		for _, w := range []string{"Gebeurtenis", "Taak", "Openen in bekup", `lang="nl"`} {
			if !strings.Contains(mail.HTML, w) {
				t.Errorf("%s: e-mail lacks %q", m.Type, w)
			}
		}
		if !strings.HasPrefix(mail.Subject, "[bekup] ") {
			t.Errorf("%s: subject %q", m.Type, mail.Subject)
		}
	}

	// Specific Dutch renderings: plurals, dates, reason, fallbacks.
	for _, c := range []struct {
		typ    EventType
		detail string
		n      names
		want   []string
	}{
		{RetentionSummary, `{"date":"2026-11-04","runs":1,"files":3,"bytes":42}`, names{job: "j"},
			[]string{`Het bewaarbeleid heeft 1 back-up (3 bestanden, 42 bytes) van taak "j" verwijderd op 2026-11-04.`}},
		{RetentionBlocked, `{"held_runs":2,"since":"2026-10-02"}`, names{job: "j", destination: "d"},
			[]string{"2 onvolledige back-ups", "de bestemming bevat geen volledige back-up"}},
		{DestinationLowSpace, `{"cause":"fraction","free_bytes":1073741824,"total_bytes":21474836480,"low_since":"2026-11-05T10:00:00Z","reminder":1}`,
			names{destination: "offsite"}, []string{"1,0 GiB van 20,0 GiB vrij", "minder dan 10% ervan is vrij", "sinds 5 nov 2026 10:00 UTC"}},
		{BackupFailed, `{"error_code":"dump_failed"}`, names{job: "j"}, []string{"Reden: dump_failed."}},
		{JobDeleted, `{}`, names{job: "(deleted job)", jobDeleted: true, jobGone: true}, []string{`Taak "(verwijderde taak)" is verwijderd.`}},
		{SecuritySystem, `{"kind":"api_token_ip_denied","ip":"unknown"}`, names{},
			[]string{`"(naamloos)"`, "adres onbekend", "trek het in"}},

		{SecuritySystem, `{"kind":"api_token_created","api_name":"ci","jobs":1,"networks":2,"expires":"2027-01-01"}`, names{},
			[]string{"back-ups starten van 1 taak vanaf 2 toegestane netwerken, geldig tot 2027-01-01."}},
		{SecuritySystem, `{"kind":"risk_acceptance_ended","risk_kind":"privileged_role","end_reason":"condition_changed"}`, names{},
			[]string{"een bevoorrechte databaserol van een verbinding", "(de situatie is veranderd)"}},
		{SecuritySystem, `{"kind":"config_reload_weakened","settings":"server.trusted_proxies"}`, names{},
			[]string{"verzwakt", " Gewijzigd: server.trusted_proxies."}},
		{SecurityAccount, `{"kind":"api_token_addresses_widened","api_name":"ci","networks":1,"changed_by":"Ada","by_creator":false}`, names{},
			[]string{"(gewijzigd door Ada, niet de maker ervan)", "vanaf meer adressen (nu 1 toegestaan netwerk)"}},
		{SummaryPeriodic, `{}`, names{}, []string{"Je periodieke samenvatting kon niet worden samengesteld"}},
	} {
		m, _ := Lookup(c.typ)
		e := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(c.typ), Severity: string(m.Severity), JobID: &job,
			DestinationID: &job, Detail: json.RawMessage(c.detail), CreatedAt: created}
		got := s.render(e, m, c.n, i18n.NL)
		for _, w := range c.want {
			if !strings.Contains(got.Body, w) {
				t.Errorf("%s %s: body %q lacks %q", c.typ, c.detail, got.Body, w)
			}
		}
	}

	// Every alert kind in Dutch, with the values its sentence carries.
	full := Detail{"failures": float64(10), "window_minutes": float64(15), "ip": "192.0.2.1", "component": "runner",
		"not_after": "2026-12-01", "threshold_days": float64(30), "settings": "server.listen", "personal_webhooks": true,
		"action": "admin.create", "api_name": "ci", "jobs": float64(2), "networks": float64(2), "risk_kind": "unpinned_image",
		"expires": "2027-01-01", "changed_by": "Ada"}
	for kind := range systemSeverity {
		d := Detail{"kind": kind}
		for k, v := range full {
			d[k] = v
		}
		assertDutch(t, "system "+kind, systemBody(textsFor(i18n.NL), i18n.NL, d), systemBody(textsFor(i18n.EN), i18n.EN, d))
	}
	// Every account kind through render, with the same values.
	am, _ := Lookup(SecurityAccount)
	for _, kind := range textKinds("account.") {
		d := map[string]any{"kind": kind}
		for k, v := range full {
			d[k] = v
		}
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		e := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(SecurityAccount), Severity: string(am.Severity),
			Detail: raw, CreatedAt: created}
		assertDutch(t, "account "+kind, s.render(e, am, names{}, i18n.NL).Body, s.render(e, am, names{}, i18n.EN).Body)
	}

	// The flood rule.
	mail, err := s.floodEmail(50, i18n.NL)
	if err != nil {
		t.Fatal(err)
	}
	if mail.Subject != "[bekup] Meldingen een uur gepauzeerd" || !strings.Contains(mail.Text, "50 meldingen") {
		t.Errorf("flood e-mail %q / %q", mail.Subject, mail.Text)
	}
	if got, ok := s.floodData(50, i18n.NL)["summary"].(string); !ok || !strings.HasPrefix(got, "50 meldingen in het afgelopen uur") {
		t.Errorf("flood webhook %q", got)
	}
}

// TestRenderEnglishUnchanged pins a few English renderings byte for byte
// (the texts moved into the catalog without changing a character).
func TestRenderEnglishUnchanged(t *testing.T) {
	s := testService(t)
	job := uuid.New()
	for _, c := range []struct {
		typ          EventType
		detail       string
		n            names
		title, body  string
		collapsedCnt int32
	}{
		{RetentionSummary, `{"date":"2026-11-04","runs":1,"files":3,"bytes":42}`, names{job: "j"}, "Retention summary: j",
			`Retention removed 1 backup(s) (3 file(s), 42 bytes) of job "j" on 2026-11-04. (2 similar event(s) collapsed.)`, 2},
		{JobEncryptionChanged, `{}`, names{job: "j"}, "Job encryption changed: j", `Job "j" had its encryption changed.`, 0},
		{JobDeleted, `{}`, names{job: "(deleted job)", jobDeleted: true, jobGone: true}, "Job deleted: (deleted job)",
			`Job "(deleted job)" was deleted.`, 0},
		{SecuritySystem, `{"kind":"api_token_created","api_name":"ci","jobs":1,"networks":1,"never_expires":true}`, names{}, "Security alert",
			`An external API token "ci" was created; it can start backups of 1 job(s) from 1 allowed network(s), never expiring.`, 0},
		{SecurityAccount, `{"kind":"risk_acceptance_ended","risk_kind":"x"}`, names{}, "Account security",
			"The acceptance of a security risk ended (it expired); the risk is flagged again.", 0},
		{DestinationLowSpace, `{"cause":"fraction","free_bytes":1073741824,"low_since":"2026-11-05T10:00:00Z","reminder":1}`,
			names{destination: "o"}, "Destination low on space",
			`Destination "o" is low on space: 1.0 GiB free; less than 10% of it is free. It has been low since 5 Nov 2026 10:00 UTC.`, 0},
	} {
		m, _ := Lookup(c.typ)
		e := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(c.typ), Severity: string(m.Severity), JobID: &job,
			DestinationID: &job, Detail: json.RawMessage(c.detail), CreatedAt: time.Now(), CollapsedCount: c.collapsedCnt}
		got := s.render(e, m, c.n, i18n.EN)
		if got.Title != c.title || got.Body != c.body {
			t.Errorf("%s:\n title %q\n  want %q\n body %q\n  want %q", c.typ, got.Title, c.title, got.Body, c.body)
		}
	}
}
