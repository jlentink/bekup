package notify

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func TestInboxPayloadRoundTrip(t *testing.T) {
	n, u := uuid.New(), uuid.New()
	p := InboxPayload(n, u)
	if err := pubsub.Validate(pubsub.NotificationEvents.Name, p); err != nil {
		t.Fatalf("payload refused by pubsub: %v", err)
	}
	gn, gu, err := ParseInboxPayload(p)
	if err != nil || gn != n || gu != u {
		t.Fatalf("round trip: %v %v %v", gn, gu, err)
	}
	for _, bad := range []string{"", "inbox:", "inbox:x:y", "run:" + n.String() + ":" + u.String(), "inbox:" + n.String()} {
		if _, _, err := ParseInboxPayload(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzParseInboxPayload(f *testing.F) {
	f.Add(InboxPayload(uuid.New(), uuid.New()))
	f.Add("inbox::")
	f.Fuzz(func(t *testing.T, s string) {
		n, u, err := ParseInboxPayload(s)
		if err == nil && InboxPayload(n, u) != s {
			t.Fatalf("accepted non-canonical %q", s)
		}
	})
}

// TestCatalogForRole: users see job and own-account events, maintainers
// also staff events, admins everything (the security.system alerts).
func TestCatalogForRole(t *testing.T) {
	types := func(r auth.Role) []EventType {
		var out []EventType
		for _, g := range CatalogFor(r) {
			if len(g.Events) == 0 {
				t.Fatalf("%s: empty group %s", r, g.Group)
			}
			for _, m := range g.Events {
				out = append(out, m.Type)
			}
		}
		return out
	}
	admin, maint, user := types(auth.RoleAdmin), types(auth.RoleMaintainer), types(auth.RoleUser)
	if len(admin) != len(Catalog()) {
		t.Fatalf("admin sees %d of %d events", len(admin), len(Catalog()))
	}
	for _, c := range []struct {
		list []EventType
		ev   EventType
		want bool
	}{
		{maint, SecuritySystem, false}, {maint, ConnectionPinChanged, true}, {maint, DestinationTestFailed, true},
		{maint, SecurityAccount, true}, {user, ConnectionPinChanged, false}, {user, DestinationTestFailed, false},
		{user, SecuritySystem, false}, {user, BackupFailed, true}, {user, SecurityAccount, true},
		{user, JobDeleted, true}, {admin, SecuritySystem, true},
	} {
		if got := slices.Contains(c.list, c.ev); got != c.want {
			t.Errorf("%s visible = %v, want %v", c.ev, got, c.want)
		}
	}
	if CatalogFor("nobody") != nil {
		t.Fatal("unknown role sees events")
	}
}

func TestMatrixCells(t *testing.T) {
	failed, _ := Lookup(BackupFailed)
	ok, _ := Lookup(BackupSucceeded)
	sec, _ := Lookup(SecurityAccount)
	job := uuid.New()

	// Defaults: inbox on, e-mail for problems, webhooks off.
	got := cells(ok, nil)
	if !got[0].Enabled || got[1].Enabled || got[2].Enabled || got[0].Source != SourceDefault {
		t.Fatalf("defaults %+v", got)
	}
	if got := cells(failed, nil); !got[1].Enabled {
		t.Fatalf("problem e-mail default %+v", got)
	}
	// A per-event row overrides; a per-job row does not change the cell.
	got = cells(ok, []Pref{{Channel: ChannelWebhook, Enabled: true}, {JobID: &job, Channel: ChannelInbox, Enabled: false}})
	if !got[2].Enabled || got[2].Source != SourceOverride || !got[0].Enabled || got[0].Source != SourceDefault {
		t.Fatalf("override %+v", got)
	}
	// Mandatory: inbox and e-mail locked on, whatever is stored.
	got = cells(sec, []Pref{{Channel: ChannelInbox, Enabled: false}, {Channel: ChannelEmail, Enabled: false}})
	for _, c := range got[:2] {
		if !c.Enabled || !c.Locked || c.Source != SourceMandatory {
			t.Fatalf("mandatory %+v", got)
		}
	}
	if got[2].Locked {
		t.Fatal("webhook of a security event locked")
	}
	if d := DefaultChannels(sec); !slices.Equal(d, []Channel{ChannelInbox, ChannelEmail}) {
		t.Fatalf("security defaults %v", d)
	}
	if d := DefaultChannels(ok); !slices.Equal(d, []Channel{ChannelInbox}) {
		t.Fatalf("success defaults %v", d)
	}
}

func fieldOf(err error) string {
	var fe *serviceutil.FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func TestCheckFilter(t *testing.T) {
	gs, es, err := checkFilter([]Group{GroupBackup, GroupBackup}, []EventType{RestoreFailed})
	if err != nil || !slices.Equal(gs, []string{"backup"}) || !slices.Equal(es, []string{"restore.failed"}) {
		t.Fatalf("%v %v %v", gs, es, err)
	}
	if gs, es, err := checkFilter(nil, nil); err != nil || len(gs) != 0 || len(es) != 0 || gs == nil || es == nil {
		t.Fatalf("empty filter %v %v %v", gs, es, err)
	}
	for name, c := range map[string]struct {
		g []Group
		e []EventType
	}{
		"security group":   {g: []Group{GroupSecurity}},
		"unknown group":    {g: []Group{"nope"}},
		"security event":   {e: []EventType{SecuritySystem}},
		"unknown event":    {e: []EventType{"backup.exploded"}},
		"not a job event":  {e: []EventType{ConnectionPinChanged}},
		"too many events":  {e: slices.Repeat([]EventType{BackupFailed}, 65)},
		"too many groups":  {g: slices.Repeat([]Group{GroupBackup}, 17)},
		"destination test": {e: []EventType{DestinationTestFailed}},
	} {
		if _, _, err := checkFilter(c.g, c.e); fieldOf(err) == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckAddressesAndName(t *testing.T) {
	got, err := checkAddresses([]string{" ops@example.com", "OPS@example.com", "dba@example.com"})
	if err != nil || !slices.Equal(got, []string{"ops@example.com", "dba@example.com"}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range [][]string{nil, {"not an address"}, {"a@example.com\r\nBcc: x@example.com"}, slices.Repeat([]string{"a@example.com"}, MaxListAddresses+1)} {
		if _, err := checkAddresses(bad); fieldOf(err) != "addresses" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if n, err := checkName("name", "  On-call  "); err != nil || n != "On-call" {
		t.Fatalf("%q %v", n, err)
	}
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("x", 101)} {
		if _, err := checkName("label", bad); fieldOf(err) != "label" {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestStateFrom(t *testing.T) {
	manual, auto, code := DisabledManual, "permanent_failures", "http_status"
	if s := stateFrom(false, nil, &manual, 0, 0, nil, nil); s.DisabledReason != "manual" || s.DisabledMessage == "" {
		t.Fatalf("manual %+v", s)
	}
	s := stateFrom(false, nil, &auto, 3, 5, nil, &code)
	if s.DisabledReason != auto || !strings.Contains(s.DisabledMessage, "rejected") || s.PermanentFailures != 5 || s.LastFailureCode != code {
		t.Fatalf("auto %+v", s)
	}
	// An enabled channel shows no stale reason.
	if s := stateFrom(true, nil, &auto, 0, 0, nil, nil); s.DisabledReason != "" || s.DisabledMessage != "" {
		t.Fatalf("enabled %+v", s)
	}
}

// M5 review: personal webhook tests are bucketed (no status code, no
// distinction between refused, TLS, status and redirect failures).
func TestPersonalTestResult(t *testing.T) {
	cases := []struct {
		in       TestResult
		wantOK   bool
		wantCode string
	}{
		{TestResult{OK: true, Code: "ok", StatusCode: 204}, true, "ok"},
		{TestResult{Code: string(webhook.CodeHTTPStatus), StatusCode: 404, Message: "x"}, false, CodePersonalTestFailed},
		{TestResult{Code: string(webhook.CodeHTTPStatus), StatusCode: 500}, false, CodePersonalTestFailed},
		{TestResult{Code: string(webhook.CodeRedirect), StatusCode: 302}, false, CodePersonalTestFailed},
		{TestResult{Code: string(webhook.CodeTLS)}, false, CodePersonalTestFailed},
		{TestResult{Code: string(webhook.CodeUnreachable)}, false, CodePersonalTestFailed},
		{TestResult{Code: string(webhook.CodeNotAllowed)}, false, string(webhook.CodeNotAllowed)},
	}
	failedMsg := ""
	for _, c := range cases {
		got := personalTestResult(c.in)
		if got.OK != c.wantOK || got.Code != c.wantCode || got.StatusCode != 0 {
			t.Errorf("%+v -> %+v", c.in, got)
		}
		if got.Code == CodePersonalTestFailed {
			if failedMsg == "" {
				failedMsg = got.Message
			} else if got.Message != failedMsg {
				t.Errorf("failure messages differ: %q / %q", failedMsg, got.Message)
			}
		}
	}
}
