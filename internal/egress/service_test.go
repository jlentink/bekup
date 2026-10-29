package egress

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
)

func newTestService(t *testing.T, dev bool) *Service {
	t.Helper()
	g, err := netguard.NewSet(netguard.Options{LoopbackAllowable: dev})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{DB: nopDB{}, Guards: g})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidate(t *testing.T) {
	many := make([]string, MaxEntries+1)
	for i := range many {
		many[i] = "10.0.0.0/8"
	}
	tests := []struct {
		name      string
		dev       bool
		in        Update
		wantField string
		wantMsg   string
		wantCIDRs []string
		wantHosts []string
	}{
		{name: "empty", wantCIDRs: []string{}, wantHosts: []string{}},
		{
			name:      "canonical",
			in:        Update{CIDRs: Values([]string{"10.1.0.0/8", "10.0.0.0/8"}), Hosts: Values([]string{"MinIO.Lan."})},
			wantCIDRs: []string{"10.0.0.0/8"}, wantHosts: []string{"minio.lan"},
		},
		{name: "too many cidrs", in: Update{CIDRs: Values(many)}, wantField: "cidrs", wantMsg: "at most 256"},
		{name: "too many hosts", in: Update{Hosts: Values(many)}, wantField: "hosts", wantMsg: "at most 256"},
		{name: "long entry", in: Update{Hosts: Values([]string{"a.example", strings.Repeat("a", 300)})}, wantField: "hosts.1", wantMsg: "too long"},
		{name: "slash zero", in: Update{CIDRs: Values([]string{"10.0.0.0/8", "0.0.0.0/0"})}, wantField: "cidrs.1", wantMsg: "every address"},
		{name: "metadata", in: Update{CIDRs: Values([]string{"169.254.169.254/32"})}, wantField: "cidrs.0", wantMsg: "always-denied"},
		{name: "ip as host", in: Update{Hosts: Values([]string{"10.0.0.1"})}, wantField: "hosts.0", wantMsg: "use a CIDR"},
		{name: "public suffix wildcard", in: Update{Hosts: Values([]string{"*.co.uk"})}, wantField: "hosts.0", wantMsg: "public suffix"},
		{name: "wildcard dns service", in: Update{Hosts: Values([]string{"*.nip.io"})}, wantField: "hosts.0", wantMsg: "wildcard-DNS"},
		{name: "no scopes", in: Update{CIDRs: []netguard.Entry{{Value: "10.0.0.0/8", Scopes: []netguard.Scope{}}}}, wantField: "cidrs.0.scopes", wantMsg: "at least one"},
		{name: "unknown scope", in: Update{Hosts: []netguard.Entry{{Value: "a.lan", Scopes: []netguard.Scope{"everything"}}}}, wantField: "hosts.0.scopes", wantMsg: "unknown scope"},
		{name: "loopback outside dev", in: Update{CIDRs: Values([]string{"127.0.0.0/8"})}, wantField: "cidrs.0", wantMsg: "loopback"},
		{name: "loopback in dev", dev: true, in: Update{CIDRs: Values([]string{"127.0.0.0/8"})}, wantCIDRs: []string{"127.0.0.0/8"}, wantHosts: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, h, err := newTestService(t, tt.dev).Validate(tt.in)
			if tt.wantField != "" {
				var fe *FieldError
				if !errors.As(err, &fe) || fe.Field != tt.wantField || !strings.Contains(fe.Message, tt.wantMsg) {
					t.Fatalf("err = %v, want field %s containing %q", err, tt.wantField, tt.wantMsg)
				}
				return
			}
			if err != nil || !slices.Equal(vals(c), tt.wantCIDRs) || !slices.Equal(vals(h), tt.wantHosts) {
				t.Fatalf("got %v %v %v, want %v %v", c, h, err, tt.wantCIDRs, tt.wantHosts)
			}
		})
	}
}

func TestDiffAndSameTime(t *testing.T) {
	if got := diff([]string{"a", "b", "c"}, []string{"b"}); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("diff = %v", got)
	}
	if got := diff(nil, []string{"b"}); got == nil || len(got) != 0 {
		t.Fatalf("diff of nothing = %#v (want an empty, non-nil list for the audit details)", got)
	}
	now := time.Now()
	later := now.Add(time.Second)
	for _, tt := range []struct {
		a, b *time.Time
		want bool
	}{
		{nil, nil, true}, {&now, nil, false}, {nil, &now, false}, {&now, &later, false},
		{&now, ptr(now.Truncate(time.Microsecond)), true},
	} {
		if got := sameTime(tt.a, tt.b); got != tt.want {
			t.Errorf("sameTime(%v, %v) = %v", tt.a, tt.b, got)
		}
	}
}

func TestNewRequires(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without DB and Guard succeeded")
	}
}

func ptr(t time.Time) *time.Time { return &t }

func vals(e []netguard.Entry) []string {
	out := make([]string, 0, len(e))
	for _, x := range e {
		out = append(out, x.Value)
	}
	return out
}

// Stored lists: version 1 gets the default scopes (never personal
// webhooks); version 2 keeps the stored scopes; entries without scopes
// or unknown versions make the whole value untrusted.
func TestDecodeList(t *testing.T) {
	l, ok := decodeList([]byte(`{"version":1,"cidrs":["10.0.0.0/8"],"hosts":["a.lan"]}`))
	if !ok || len(l.CIDRs) != 1 || !slices.Equal(l.CIDRs[0].Scopes, []string{"destinations", "smtp", "shared_webhooks"}) || l.Hosts[0].Value != "a.lan" {
		t.Fatalf("v1 = %+v %v", l, ok)
	}
	l, ok = decodeList([]byte(`{"version":2,"cidrs":[{"value":"10.0.0.0/8","scopes":["personal_webhooks"]}]}`))
	if !ok || !slices.Equal(l.CIDRs[0].Scopes, []string{"personal_webhooks"}) || l.Hosts == nil {
		t.Fatalf("v2 = %+v %v", l, ok)
	}
	for _, bad := range []string{`{"version":2,"cidrs":[{"value":"10.0.0.0/8"}]}`, `{"version":3}`, `[]`, `nonsense`} {
		if l, ok := decodeList([]byte(bad)); ok || len(l.CIDRs) != 0 {
			t.Errorf("%s decoded: %+v", bad, l)
		}
	}
}

func TestDiffEntries(t *testing.T) {
	def := netguard.DefaultScopes()
	old := []netguard.Entry{{Value: "10.0.0.0/8", Scopes: def}, {Value: "10.1.0.0/16", Scopes: []netguard.Scope{netguard.ScopeSMTP}}}
	next := []netguard.Entry{
		{Value: "10.0.0.0/8", Scopes: []netguard.Scope{netguard.ScopeDestinations}},                          // narrowed
		{Value: "10.1.0.0/16", Scopes: []netguard.Scope{netguard.ScopeSMTP, netguard.ScopePersonalWebhooks}}, // gained personal
		{Value: "10.2.0.0/16", Scopes: def}, // new
	}
	d := diffEntries(next, old)
	if !slices.Equal(d.added, []string{"10.2.0.0/16"}) || len(d.scopes) != 1 || !slices.Equal(d.scopes["10.1.0.0/16"], []string{"personal_webhooks"}) {
		t.Fatalf("diff %+v", d)
	}
	if !slices.Contains(d.personal, true) {
		t.Fatal("personal widening not flagged")
	}
	if d := diffEntries(old[:1], old); len(d.added) != 0 || len(d.scopes) != 0 || slices.Contains(d.personal, true) {
		t.Fatalf("narrowing flagged %+v", d)
	}
}
