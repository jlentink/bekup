package netguard

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func mustSet(t *testing.T, opts Options) *Set {
	t.Helper()
	s, err := NewSet(opts)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	return s
}

func allowed(s *Set, sc Scope, ip, host string) bool {
	return s.Guard(sc).Check(netip.MustParseAddr(ip), host) == nil
}

// The bootstrap list and admin entries without scopes widen every use
// except personal webhooks; an entry reaches personal webhooks only with
// the personal_webhooks scope (M5 review: scoped allowlists).
func TestSetScopes(t *testing.T) {
	s := mustSet(t, Options{AllowCIDRs: []string{"10.1.0.0/16"}, AllowHosts: []string{"boot.internal"}})
	for _, sc := range DefaultScopes() {
		if !allowed(s, sc, "10.1.2.3", "") || !allowed(s, sc, "192.168.9.9", "boot.internal") {
			t.Fatalf("%s: bootstrap entry not applied", sc)
		}
	}
	if allowed(s, ScopePersonalWebhooks, "10.1.2.3", "") || allowed(s, ScopePersonalWebhooks, "192.168.9.9", "boot.internal") {
		t.Fatal("bootstrap entry widened personal webhooks")
	}
	err := s.SetAllowlist(
		[]Entry{{Value: "10.2.0.0/16"}, {Value: "10.3.0.0/16", Scopes: []Scope{ScopePersonalWebhooks}}, {Value: "10.4.0.0/16", Scopes: []Scope{ScopeSMTP}}},
		[]Entry{{Value: "*.corp.example.com", Scopes: AllScopes()}},
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		sc         Scope
		ip, host   string
		wantAllows bool
	}{
		{ScopeDestinations, "10.2.0.1", "", true},
		{ScopeSharedWebhooks, "10.2.0.1", "", true},
		{ScopePersonalWebhooks, "10.2.0.1", "", false},
		{ScopePersonalWebhooks, "10.3.0.1", "", true},
		{ScopeDestinations, "10.3.0.1", "", false},
		{ScopeSMTP, "10.4.0.1", "", true},
		{ScopeDestinations, "10.4.0.1", "", false},
		{ScopePersonalWebhooks, "10.9.0.1", "a.corp.example.com", true},
		{ScopeDestinations, "10.9.0.1", "a.corp.example.com", true},
		// Hard-denied addresses stay denied whatever the scope.
		{ScopePersonalWebhooks, "169.254.169.254", "a.corp.example.com", false},
	}
	for _, c := range cases {
		if got := allowed(s, c.sc, c.ip, c.host); got != c.wantAllows {
			t.Errorf("%s %s %q: allowed %v, want %v", c.sc, c.ip, c.host, got, c.wantAllows)
		}
	}
	// An invalid list changes nothing.
	if err := s.SetAllowlist([]Entry{{Value: "10.5.0.0/16", Scopes: []Scope{"bogus"}}}, nil); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if !allowed(s, ScopePersonalWebhooks, "10.3.0.1", "") {
		t.Fatal("a rejected list replaced the current one")
	}
	// Clearing the list keeps the bootstrap entries.
	if err := s.SetAllowlist(nil, nil); err != nil {
		t.Fatal(err)
	}
	if allowed(s, ScopePersonalWebhooks, "10.3.0.1", "") || !allowed(s, ScopeSMTP, "10.1.0.1", "") {
		t.Fatal("clearing the runtime list")
	}
}

func TestNormalizeEntries(t *testing.T) {
	s := mustSet(t, Options{})
	c, h, errs := s.NormalizeEntries(
		[]Entry{
			{Value: "10.0.0.1/8", Scopes: []Scope{ScopeSMTP}},
			{Value: "10.0.0.0/8", Scopes: []Scope{ScopePersonalWebhooks, ScopeSMTP}},
			{Value: "192.168.0.0/16"},
			{Value: "127.0.0.1/32"},
			{Value: "172.16.0.0/12", Scopes: []Scope{}},
		},
		[]Entry{{Value: "MinIO.Lan.", Scopes: []Scope{ScopeDestinations, ScopeDestinations}}, {Value: "x.internal", Scopes: []Scope{"nope"}}},
	)
	if len(c) != 2 || c[0].Value != "10.0.0.0/8" || !slices.Equal(c[0].Scopes, []Scope{ScopeSMTP, ScopePersonalWebhooks}) {
		t.Fatalf("cidrs %+v", c)
	}
	if !slices.Equal(c[1].Scopes, DefaultScopes()) {
		t.Fatalf("default scopes %+v", c[1])
	}
	if len(h) != 1 || h[0].Value != "minio.lan" || !slices.Equal(h[0].Scopes, []Scope{ScopeDestinations}) {
		t.Fatalf("hosts %+v", h)
	}
	want := []string{"cidrs[3]", "cidrs[4].scopes", "hosts[1].scopes"}
	if len(errs) != len(want) {
		t.Fatalf("errs %v", errs)
	}
	for i, w := range want {
		if !strings.Contains(errs[i].Error(), w) {
			t.Errorf("err %d = %v, want %s", i, errs[i], w)
		}
	}
	var ee *EntryError
	if !errors.As(error(errs[1]), &ee) || ee.Field != "scopes" || ee.Index != 4 {
		t.Fatalf("scope error %+v", errs[1])
	}
}

// M5 review L4: wildcards over public suffixes and names under public
// wildcard-DNS services are refused.
func TestHostPatternPublicSuffixes(t *testing.T) {
	for _, h := range []string{"*.co.uk", "*.github.io", "*.com.au", "*.nip.io", "*.10.0.0.1.nip.io", "*.sslip.io", "*.xip.io", "*.localtest.me", "*.lvh.me", "10-0-0-1.nip.io", "app.localtest.me"} {
		if _, _, err := parseHostPattern(h); err == nil {
			t.Errorf("%s accepted", h)
		}
	}
	for _, h := range []string{"*.corp.example.com", "*.example.co.uk", "*.corp.internal", "minio.example.com", "*.lan.example"} {
		if _, _, err := parseHostPattern(h); err != nil {
			t.Errorf("%s rejected: %v", h, err)
		}
	}
}

func TestCanonicalScopes(t *testing.T) {
	if _, err := CanonicalScopes(nil); err == nil {
		t.Fatal("empty scopes accepted")
	}
	got, err := CanonicalScopes([]Scope{ScopePersonalWebhooks, ScopeDestinations, ScopePersonalWebhooks})
	if err != nil || !slices.Equal(got, []Scope{ScopeDestinations, ScopePersonalWebhooks}) {
		t.Fatalf("%v %v", got, err)
	}
	if slices.Contains(DefaultScopes(), ScopePersonalWebhooks) {
		t.Fatal("personal webhooks in the default scopes")
	}
}
