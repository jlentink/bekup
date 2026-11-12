package notify

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/notify/webhook"
)

// TestCatalogMetadata pins the D-161 catalog: groups, audiences, problem
// flags, priorities and the mandatory security events.
func TestCatalogMetadata(t *testing.T) {
	want := map[EventType]struct {
		group     Group
		audience  Audience
		problem   bool
		low       bool
		mandatory bool
		op        authz.Operation
	}{
		BackupStarted:               {GroupBackup, AudienceJob, false, true, false, authz.OpRunView},
		BackupSucceeded:             {GroupBackup, AudienceJob, false, false, false, authz.OpRunView},
		BackupSucceededWithWarnings: {GroupBackup, AudienceJob, true, false, false, authz.OpRunView},
		BackupPartial:               {GroupBackup, AudienceJob, true, false, false, authz.OpRunView},
		BackupFailed:                {GroupBackup, AudienceJob, true, false, false, authz.OpRunView},
		BackupCanceled:              {GroupBackup, AudienceJob, false, false, false, authz.OpRunView},
		BackupMissed:                {GroupBackup, AudienceJob, true, false, false, authz.OpRunView},
		BackupStartedEarly:          {GroupBackup, AudienceJob, false, false, false, authz.OpRunView},
		RestoreStarted:              {GroupRestore, AudienceJob, false, true, false, authz.OpRestoreView},
		RestoreCompleted:            {GroupRestore, AudienceJob, false, false, false, authz.OpRestoreView},
		RestoreFailed:               {GroupRestore, AudienceJob, true, false, false, authz.OpRestoreView},
		TestRestorePassed:           {GroupRestore, AudienceJob, false, false, false, authz.OpTestRestoreView},
		TestRestoreWarnings:         {GroupRestore, AudienceJob, true, false, false, authz.OpTestRestoreView},
		TestRestoreFailed:           {GroupRestore, AudienceJob, true, false, false, authz.OpTestRestoreView},
		RetentionSummary:            {GroupRetention, AudienceJob, false, false, false, authz.OpArtifactView},
		RetentionBlocked:            {GroupRetention, AudienceJob, true, false, false, authz.OpArtifactView},
		ArtifactMissing:             {GroupRetention, AudienceJob, true, false, false, authz.OpArtifactView},
		DestinationTestFailed:       {GroupRetention, AudienceStaff, true, false, false, authz.OpDestinationView},
		DestinationLowSpace:         {GroupRetention, AudienceStaff, true, false, false, authz.OpDestinationView},
		JobCreated:                  {GroupConfig, AudienceJob, false, false, false, authz.OpJobView},
		JobUpdated:                  {GroupConfig, AudienceJob, false, false, false, authz.OpJobView},
		JobDisabled:                 {GroupConfig, AudienceJob, false, false, false, authz.OpJobView},
		JobEnabled:                  {GroupConfig, AudienceJob, false, false, false, authz.OpJobView},
		JobDeleted:                  {GroupConfig, AudienceJob, false, false, false, authz.OpJobView},
		JobEncryptionChanged:        {GroupConfig, AudienceJob, true, false, false, authz.OpJobView},
		ConnectionPinChanged:        {GroupConfig, AudienceStaff, true, false, false, authz.OpConnectionView},
		ConnectionTLSDowngraded:     {GroupConfig, AudienceStaff, true, false, false, authz.OpConnectionView},
		ConnectionHostKeyChanged:    {GroupConfig, AudienceStaff, true, false, false, authz.OpConnectionView},
		SecurityAccount:             {GroupSecurity, AudienceOwnAccount, true, false, true, authz.OpAccountView},
		SecuritySystem:              {GroupSecurity, AudienceAdmins, true, false, true, authz.OpAlertList},
		SummaryPeriodic:             {GroupSummary, AudienceOwnAccount, false, false, false, authz.OpSummaryView},
	}
	if len(Catalog()) != len(want) {
		t.Fatalf("catalog has %d events, want %d", len(Catalog()), len(want))
	}
	for typ, w := range want {
		m, ok := Lookup(typ)
		if !ok {
			t.Fatalf("%s missing", typ)
		}
		if m.Group != w.group || m.Audience != w.audience || m.Problem != w.problem ||
			(m.Priority == PriorityLow) != w.low || m.Mandatory != w.mandatory || m.Op != w.op {
			t.Errorf("%s: got %+v", typ, m)
		}
		if !m.Op.Known() || !m.Severity.Valid() || m.Label == "" {
			t.Errorf("%s: incomplete metadata %+v", typ, m)
		}
		if strings.HasSuffix(string(typ), ".started") != (m.Priority == PriorityLow) {
			t.Errorf("%s: *.started events (and only they) are low priority", typ)
		}
		if err := webhookTypeOK(string(typ)); err != nil {
			t.Errorf("%s: not a valid webhook event type: %v", typ, err)
		}
	}
	// Catalog order: groups in display order.
	last := -1
	for _, m := range Catalog() {
		idx := map[Group]int{GroupBackup: 0, GroupRestore: 1, GroupRetention: 2, GroupConfig: 3, GroupSummary: 4, GroupSecurity: 5}[m.Group]
		if idx < last {
			t.Fatalf("catalog not grouped: %s", m.Type)
		}
		last = idx
	}
	if got := MandatoryTypes(); len(got) != 2 {
		t.Fatalf("mandatory types = %v", got)
	}
}

func webhookTypeOK(typ string) error {
	_, err := webhook.BuildPayload("x", typ, time.Now(), nil)
	return err
}

func TestDefaults(t *testing.T) {
	failed, _ := Lookup(BackupFailed)
	ok, _ := Lookup(BackupSucceeded)
	for _, c := range []struct {
		m    Meta
		ch   Channel
		want bool
	}{
		{failed, ChannelInbox, true}, {failed, ChannelEmail, true}, {failed, ChannelWebhook, false},
		{ok, ChannelInbox, true}, {ok, ChannelEmail, false}, {ok, ChannelWebhook, false},
	} {
		if got := Enabled(c.m, c.ch, nil, nil); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.m.Type, c.ch, got, c.want)
		}
	}
}

// TestSummaryChannels: the periodic summary (D-226) goes to the inbox and
// by e-mail by default although it is no problem event, and never to a
// webhook, whatever the preferences say.
func TestSummaryChannels(t *testing.T) {
	m, ok := Lookup(SummaryPeriodic)
	if !ok || m.Subject != SubjectUser || m.Mandatory || m.Problem {
		t.Fatalf("summary.periodic = %+v", m)
	}
	if !Enabled(m, ChannelInbox, nil, nil) || !Enabled(m, ChannelEmail, nil, nil) || Enabled(m, ChannelWebhook, nil, nil) {
		t.Fatal("defaults must be inbox + e-mail, no webhook")
	}
	if Enabled(m, ChannelWebhook, nil, []Pref{{Channel: ChannelWebhook, Enabled: true}}) {
		t.Fatal("a stored webhook preference must not enable the webhook")
	}
	if Enabled(m, ChannelEmail, nil, []Pref{{Channel: ChannelEmail, Enabled: false}}) {
		t.Fatal("the e-mail can be turned off")
	}
	if got := OfferedChannels(m); len(got) != 2 || got[0] != ChannelInbox || got[1] != ChannelEmail {
		t.Fatalf("offered = %v", got)
	}
	if got := DefaultChannels(m); len(got) != 2 {
		t.Fatalf("default channels = %v", got)
	}
	if c := cells(m, nil); len(c) != 2 {
		t.Fatalf("matrix cells = %+v", c)
	}
	if _, _, err := checkFilter([]Group{GroupSummary}, nil); err == nil {
		t.Fatal("shared channels must not take the summary group")
	}
	for _, other := range Catalog() {
		if other.Type != SummaryPeriodic && len(OfferedChannels(other)) != len(MatrixChannels()) {
			t.Errorf("%s offers %v", other.Type, OfferedChannels(other))
		}
	}
}

func TestPreferencePrecedence(t *testing.T) {
	m, _ := Lookup(BackupSucceeded)
	job, other := uuid.New(), uuid.New()
	perEventOn := Pref{Channel: ChannelEmail, Enabled: true}
	perJobOff := Pref{JobID: &job, Channel: ChannelEmail, Enabled: false}
	otherJobOn := Pref{JobID: &other, Channel: ChannelWebhook, Enabled: true}
	cases := []struct {
		name  string
		prefs []Pref
		job   *uuid.UUID
		ch    Channel
		want  bool
	}{
		{"default off", nil, &job, ChannelEmail, false},
		{"per event on", []Pref{perEventOn}, &job, ChannelEmail, true},
		{"per job beats per event", []Pref{perEventOn, perJobOff}, &job, ChannelEmail, false},
		{"per job beats per event (order)", []Pref{perJobOff, perEventOn}, &job, ChannelEmail, false},
		{"other job's override ignored", []Pref{otherJobOn}, &job, ChannelWebhook, false},
		{"override of the job applies", []Pref{otherJobOn}, &other, ChannelWebhook, true},
		{"job override without a job", []Pref{perJobOff, perEventOn}, nil, ChannelEmail, true},
		{"inbox can be turned off", []Pref{{Channel: ChannelInbox, Enabled: false}}, &job, ChannelInbox, false},
	}
	for _, c := range cases {
		if got := Enabled(m, c.ch, c.job, c.prefs); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMandatoryCannotBeDisabled: security events keep the inbox and
// e-mail whatever the preferences say; webhooks can be added.
func TestMandatoryCannotBeDisabled(t *testing.T) {
	for _, typ := range []EventType{SecurityAccount, SecuritySystem} {
		m, _ := Lookup(typ)
		off := []Pref{{Channel: ChannelInbox}, {Channel: ChannelEmail}, {Channel: ChannelWebhook, Enabled: true}}
		if !Enabled(m, ChannelInbox, nil, off) || !Enabled(m, ChannelEmail, nil, off) {
			t.Fatalf("%s: mandatory channels were disabled", typ)
		}
		if !Enabled(m, ChannelWebhook, nil, off) {
			t.Fatalf("%s: an added webhook must apply", typ)
		}
		if Enabled(m, ChannelWebhook, nil, nil) {
			t.Fatalf("%s: webhooks are opt-in", typ)
		}
	}
}

func TestSharedFilter(t *testing.T) {
	failed, _ := Lookup(BackupFailed)
	sec, _ := Lookup(SecuritySystem)
	cases := []struct {
		m              Meta
		groups, events []string
		want           bool
	}{
		{failed, nil, nil, true},
		{failed, []string{"backup"}, nil, true},
		{failed, []string{"restore"}, nil, false},
		{failed, nil, []string{"backup.failed"}, true},
		{failed, []string{"restore"}, []string{"backup.succeeded"}, false},
		{sec, nil, nil, false},
		{sec, []string{"security"}, []string{"security.system"}, false},
	}
	for _, c := range cases {
		if got := sharedFilterMatches(c.m, c.groups, c.events); got != c.want {
			t.Errorf("%s %v %v: got %v", c.m.Type, c.groups, c.events, got)
		}
	}
}

func TestEventValidation(t *testing.T) {
	job := uuid.New()
	cases := []struct {
		name string
		ev   Event
		want error
	}{
		{"unknown", Event{Type: "backup.exploded", Subject: Subject{JobID: job}}, ErrUnknownEvent},
		{"no job", Event{Type: BackupFailed}, ErrMissingSubject},
		{"no user", Event{Type: SecurityAccount}, ErrMissingSubject},
		{"no connection", Event{Type: ConnectionPinChanged, Subject: Subject{JobID: job}}, ErrMissingSubject},
		{"no destination", Event{Type: DestinationTestFailed}, ErrMissingSubject},
		{"bad severity", Event{Type: BackupFailed, Subject: Subject{JobID: job}, Severity: "loud"}, ErrInvalidDetail},
		{"bad dedup", Event{Type: BackupFailed, Subject: Subject{JobID: job}, DedupKey: "Has Space"}, ErrInvalidDetail},
		{"ok", Event{Type: BackupFailed, Subject: Subject{JobID: job}}, nil},
		{"system without subject ok", Event{Type: SecuritySystem}, nil},
	}
	for _, c := range cases {
		_, err := c.ev.validate()
		if !errors.Is(err, c.want) && (c.want != nil || err != nil) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestValidateDetail(t *testing.T) {
	ok := []Detail{nil, {}, {"status": "failed", "attempt": int64(2), "ok": true, "n": 3}}
	for _, d := range ok {
		if _, err := ValidateDetail(d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
	bad := []Detail{
		{"Status": "x"},
		{"password": "x"},
		{"api_token": "x"},
		{"hmac_key": "x"},
		{"private_note": "x"},
		{"msg": "line\nbreak"},
		{"msg": "sep\u2028x"},
		{"msg": strings.Repeat("a", MaxDetailString+1)},
		{"f": 1.5},
		{"m": map[string]any{}},
		{"b": []byte("x")},
	}
	for _, d := range bad {
		if _, err := ValidateDetail(d); !errors.Is(err, ErrInvalidDetail) {
			t.Errorf("%v: err = %v", d, err)
		}
	}
	many := Detail{}
	for i := range MaxDetailKeys + 1 {
		many[string(rune('a'+i))+"k"] = "v"
	}
	if _, err := ValidateDetail(many); err == nil {
		t.Error("too many keys accepted")
	}
}

func TestFromAudit(t *testing.T) {
	user, admin, job, conn := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ip := netip.MustParseAddr("192.0.2.7")
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	ua := audit.UserActor(user)
	aa := audit.UserActor(admin)
	cases := []struct {
		name    string
		ev      audit.Event
		want    EventType
		subject Subject
		kind    string
	}{
		{"job created", audit.Event{Actor: aa, Action: audit.ActionJobCreated, TargetType: "job", TargetID: job.String()}, JobCreated, Subject{JobID: job}, ""},
		{"job encryption", audit.Event{Actor: aa, Action: audit.ActionJobEncryptionOptedOut, TargetType: "job", TargetID: job.String()}, JobEncryptionChanged, Subject{JobID: job}, ""},
		{"pin", audit.Event{Actor: aa, Action: audit.ActionConnectionPinConfirmed, TargetType: "connection", TargetID: conn.String()}, ConnectionPinChanged, Subject{ConnectionID: conn}, ""},
		{"host key", audit.Event{Actor: aa, Action: audit.ActionConnectionHostKeyConfirmed, TargetType: "connection", TargetID: conn.String()}, ConnectionHostKeyChanged, Subject{ConnectionID: conn}, ""},
		{"password changed", audit.Event{Actor: ua, Action: audit.ActionAuthPasswordChanged, TargetType: "user", TargetID: user.String(), IP: ip}, SecurityAccount, Subject{UserID: user}, KindPasswordChanged},
		{"reset by admin", audit.Event{Actor: aa, Action: audit.ActionUserPasswordReset, TargetType: "user", TargetID: user.String()}, SecurityAccount, Subject{UserID: user}, KindPasswordReset},
		{"lockout", audit.Event{Actor: audit.SystemActor(), Action: audit.ActionAuthLockout, TargetType: "user", TargetID: user.String()}, SecurityAccount, Subject{UserID: user}, KindAccountLocked},
		{"mfa enrolled", audit.Event{Actor: ua, Action: audit.ActionAuthMFAEnrolled}, SecurityAccount, Subject{UserID: user}, KindSecondFactorAdded},
		{"sign in", audit.Event{Actor: ua, Action: audit.ActionAuthLoginSucceeded, TargetType: "user", TargetID: user.String(), IP: ip, Details: audit.Details{"second_factor_pending": false}}, SecurityAccount, Subject{UserID: user}, KindSignIn},
		{"sign in completed", audit.Event{Actor: ua, Action: audit.ActionAuthLoginSecondFactor, IP: ip}, SecurityAccount, Subject{UserID: user}, KindSignIn},
		{"opt out", audit.Event{Actor: aa, Action: audit.ActionSecurityOptOut, TargetType: "job", TargetID: job.String(), Details: audit.Details{"setting": "job.encryption"}}, SecuritySystem, Subject{JobID: job}, KindOptOut},
		{"admin created", audit.Event{Actor: aa, Action: audit.ActionUserCreated, TargetType: "user", TargetID: user.String(), Details: audit.Details{"role": "admin"}}, SecuritySystem, Subject{UserID: user}, KindAdminCreated},
		{"keys rotated", audit.Event{Actor: audit.SystemActor(), Action: audit.ActionKeysRotated, TargetType: "keyring"}, SecuritySystem, Subject{}, KindKeysRotated},
	}
	for _, c := range cases {
		got, ok := fromAudit(c.ev, now)
		if !ok {
			t.Fatalf("%s: not mapped", c.name)
		}
		if got.Type != c.want || got.Subject != c.subject {
			t.Errorf("%s: got %s %+v", c.name, got.Type, got.Subject)
		}
		if c.kind != "" && got.Detail["kind"] != c.kind {
			t.Errorf("%s: kind %v", c.name, got.Detail["kind"])
		}
		if _, err := got.validate(); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if _, err := ValidateDetail(got.Detail); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// Not mapped: a sign-in still waiting for its second factor, a
	// non-admin user created, unrelated actions, a job action without a
	// job target.
	for _, ev := range []audit.Event{
		{Actor: ua, Action: audit.ActionAuthLoginSucceeded, Details: audit.Details{"second_factor_pending": true}},
		{Actor: aa, Action: audit.ActionUserCreated, TargetType: "user", TargetID: user.String(), Details: audit.Details{"role": "user"}},
		{Actor: ua, Action: audit.ActionAuthLogout},
		{Actor: aa, Action: audit.ActionJobCreated, TargetType: "run", TargetID: job.String()},
	} {
		if got, ok := fromAudit(ev, now); ok {
			t.Errorf("%s mapped to %s", ev.Action, got.Type)
		}
	}
	// Sign-in alerts are deduplicated per account, address and day.
	a, _ := fromAudit(audit.Event{Actor: ua, Action: audit.ActionAuthLoginSecondFactor, IP: ip}, now)
	b, _ := fromAudit(audit.Event{Actor: ua, Action: audit.ActionAuthLoginSecondFactor, IP: ip}, now.Add(time.Hour))
	if a.DedupKey == "" || a.DedupKey != b.DedupKey {
		t.Fatalf("dedup keys %q / %q", a.DedupKey, b.DedupKey)
	}
	// M5 review L1: a zoned link-local address is stored without its zone
	// (details and dedup key), so zones neither split nor inject keys.
	zoned := netip.MustParseAddr("fe80::1%eth0")
	z1, _ := fromAudit(audit.Event{Actor: ua, Action: audit.ActionAuthLoginSecondFactor, IP: zoned}, now)
	z2, _ := fromAudit(audit.Event{Actor: ua, Action: audit.ActionAuthLoginSecondFactor, IP: netip.MustParseAddr("fe80::1")}, now)
	if z1.Detail["ip"] != "fe80::1" || z1.DedupKey != z2.DedupKey || strings.Contains(z1.DedupKey, "%") {
		t.Fatalf("zoned sign-in: detail %v, keys %q / %q", z1.Detail["ip"], z1.DedupKey, z2.DedupKey)
	}
	pc, _ := fromAudit(audit.Event{Actor: ua, Action: audit.ActionAuthPasswordChanged, TargetType: "user", TargetID: user.String(), IP: zoned}, now)
	if pc.Detail["ip"] != "fe80::1" {
		t.Fatalf("zoned account event ip = %v", pc.Detail["ip"])
	}
	if ae := accountEvent(audit.Event{IP: netip.MustParseAddr("::ffff:192.0.2.9")}, user, KindPasskeyCloneSuspected); ae.Detail["ip"] != "192.0.2.9" {
		t.Fatalf("mapped account event ip = %v", ae.Detail["ip"])
	}
	// Extra events: admin granted, TLS downgrade.
	ex := extraFromAudit(audit.Event{Actor: aa, Action: audit.ActionUserRoleChanged, TargetType: "user", TargetID: user.String(), Details: audit.Details{"role": "admin"}})
	if len(ex) != 1 || ex[0].Type != SecuritySystem || ex[0].Detail["kind"] != KindAdminGranted {
		t.Fatalf("role change extras = %+v", ex)
	}
	ex = extraFromAudit(audit.Event{Actor: aa, Action: audit.ActionSecurityOptOut, TargetType: "connection", TargetID: conn.String()})
	if len(ex) != 1 || ex[0].Type != ConnectionTLSDowngraded || ex[0].Subject.ConnectionID != conn {
		t.Fatalf("downgrade extras = %+v", ex)
	}
}

func testService(t *testing.T) *Service {
	t.Helper()
	return &Service{o: Options{MaxPerUserPerHour: 5, Now: time.Now}, base: mustURL(t, "https://bekup.example.com/app")}
}

func TestRenderMinimalAndLinked(t *testing.T) {
	s := testService(t)
	job, run := uuid.New(), uuid.New()
	ev := &sqlc.NotificationEvent{
		ID: uuid.New(), Type: string(BackupFailed), Severity: "error", JobID: &job, RunID: &run,
		Detail: json.RawMessage(`{"status":"failed","error_code":"dump_failed"}`), CreatedAt: time.Now(),
	}
	m, _ := Lookup(BackupFailed)
	c := s.render(ev, m, names{job: displayName("nightly\x07 <db>")}, i18n.EN)
	if c.Title != "Backup failed: nightly  <db>" {
		t.Fatalf("title %q", c.Title)
	}
	if c.Link != "https://bekup.example.com/app/runs/"+run.String() {
		t.Fatalf("link %q", c.Link)
	}
	if !strings.Contains(c.Body, "dump_failed") {
		t.Fatalf("body %q", c.Body)
	}
	b, err := json.Marshal(c.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"event":"backup.failed"`, `"run_id"`, `"job"`, `"link"`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("payload lacks %s: %s", k, b)
		}
	}
	if _, err := emailFor(ev, c, names{job: "nightly"}, i18n.EN); err != nil {
		t.Fatal(err)
	}
	// Every catalog event renders a title, a body and a valid e-mail.
	for _, m := range Catalog() {
		user := uuid.New()
		e := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(m.Type), Severity: string(m.Severity), JobID: &job, UserID: &user,
			ConnectionID: &job, DestinationID: &job, Detail: json.RawMessage(`{"kind":"sign_in"}`), CreatedAt: time.Now()}
		c := s.render(e, m, names{job: "j", connection: "c", destination: "d"}, i18n.EN)
		if c.Title == "" || c.Body == "" || c.Link == "" {
			t.Errorf("%s: %+v", m.Type, c)
		}
		if _, err := emailFor(e, c, names{job: "j"}, i18n.EN); err != nil {
			t.Errorf("%s: %v", m.Type, err)
		}
	}
	if _, err := s.floodEmail(5, i18n.EN); err != nil {
		t.Fatal(err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestDisplayName(t *testing.T) {
	if got := displayName("a\nb c"); got != "a b c" {
		t.Fatalf("%q", got)
	}
	if got := displayName(strings.Repeat("x", 300)); len([]rune(got)) != 100 {
		t.Fatalf("len %d", len([]rune(got)))
	}
	if displayName("  ") != "(unnamed)" {
		t.Fatal("empty name")
	}
	// Format characters (bidi overrides and isolates, zero-width ones) are
	// shown as spaces like control characters; ordinary text is kept.
	for in, want := range map[string]string{
		"invoice\u202Efdp.exe":          "invoice fdp.exe",
		"\u202Abilling\u202C":           "billing",
		"\u202Dx\u202B":                 "x",
		"a\u2066b\u2067c\u2068d\u2069e": "a b c d e",
		"Bil\u200Bling":                 "Bil ling",
		"\uFEFFdocs":                    "docs",
		"a\u00ADb":                      "a b",
		"\u2028x\u2029":                 "x",
		"Prod-DB café 😀 日本":             "Prod-DB café 😀 日本",
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := DisplayName("\u202E\u2066"); got != "(unnamed)" {
		t.Errorf("only format characters = %q", got)
	}
}

// TestRenderRetentionBlocked: retention.blocked names the destination, the
// held count, the start and the cause (D-209).
func TestRenderRetentionBlocked(t *testing.T) {
	s := testService(t)
	job, dest := uuid.New(), uuid.New()
	m, _ := Lookup(RetentionBlocked)
	for _, c := range []struct {
		detail string
		want   []string
	}{
		{`{"reason":"newer_than_newest_complete","held_runs":3,"since":"2026-10-01","newest_complete":"2026-09-30"}`,
			[]string{"3 incomplete backup(s)", `"offsite"`, "since 2026-10-01", "after 2026-09-30 are incomplete"}},
		{`{"reason":"no_complete_run","held_runs":1,"since":"2026-10-02"}`,
			[]string{"1 incomplete backup(s)", "holds no complete backup"}},
	} {
		ev := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(RetentionBlocked), Severity: "warning", JobID: &job,
			DestinationID: &dest, Detail: json.RawMessage(c.detail), CreatedAt: time.Now()}
		got := s.render(ev, m, names{job: "nightly", destination: "offsite"}, i18n.EN)
		for _, w := range c.want {
			if !strings.Contains(got.Body, w) {
				t.Errorf("body %q lacks %q", got.Body, w)
			}
		}
		if got.Link != "https://bekup.example.com/app/jobs/"+job.String() {
			t.Errorf("link %q", got.Link)
		}
	}
}

// TestRenderDestinationLowSpace: destination.low_space names the
// destination, the free space, the part of the D-225 rule that holds and,
// for a reminder, the start of the episode (D-227).
func TestRenderDestinationLowSpace(t *testing.T) {
	s := testService(t)
	dest := uuid.New()
	m, _ := Lookup(DestinationLowSpace)
	for _, c := range []struct {
		detail     string
		want, deny []string
	}{
		{`{"cause":"fraction","free_bytes":1073741824,"total_bytes":21474836480,"newest_set_bytes":0,"low_since":"2026-11-05T10:00:00Z","reminder":0}`,
			[]string{`"offsite"`, "1.0 GiB of 20.0 GiB free", "less than 10%"}, []string{"since"}},
		{`{"cause":"below_newest_set","free_bytes":1073741824,"newest_set_bytes":2147483648,"low_since":"2026-11-05T10:00:00Z","reminder":2}`,
			[]string{"1.0 GiB free", "newest set of backups there needs (2.0 GiB)", "low since 5 Nov 2026 10:00 UTC"}, []string{"free of"}},
	} {
		ev := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(DestinationLowSpace), Severity: "warning",
			DestinationID: &dest, Detail: json.RawMessage(c.detail), CreatedAt: time.Now()}
		got := s.render(ev, m, names{destination: "offsite"}, i18n.EN)
		for _, w := range c.want {
			if !strings.Contains(got.Body, w) {
				t.Errorf("body %q lacks %q", got.Body, w)
			}
		}
		for _, w := range c.deny {
			if strings.Contains(got.Body, w) {
				t.Errorf("body %q contains %q", got.Body, w)
			}
		}
		if got.Link != "https://bekup.example.com/app/destinations/"+dest.String() {
			t.Errorf("link %q", got.Link)
		}
	}
}
