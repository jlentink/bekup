package targets

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/netguard"
)

type fakeDNS map[string][]netip.Addr

func (f fakeDNS) lookup(_ context.Context, network, host string) ([]netip.Addr, error) {
	addrs, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, a := range addrs {
		if network == "ip4" && !a.Unmap().Is4() || network == "ip6" && a.Is4() {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func newGuard(t *testing.T, opts Options, dns fakeDNS) *Guard {
	t.Helper()
	if dns != nil {
		opts.Lookup = dns.lookup
	}
	g, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestCheck(t *testing.T) {
	g := newGuard(t, Options{
		AllowCIDRs:  []string{"10.20.0.0/16", "192.0.2.0/24", "2001:db8:1::/48", "100.64.0.0/10", "::ffff:198.51.100.0/120"},
		AllowHosts:  []string{"DB.Example.com.", "bastion.example.com"},
		DenyCIDRs:   []string{"10.20.99.0/24"},
		DenyHosts:   []string{"evil.example.com"},
		OwnNetworks: []netip.Prefix{netip.MustParsePrefix("10.20.250.0/24"), netip.MustParsePrefix("::ffff:10.20.251.0/120")},
	}, nil)
	tests := []struct {
		name   string
		addr   string
		host   string
		reason string // "" = allowed
	}{
		{"allowed cidr", "10.20.1.5", "", ""},
		{"allowed cidr mapped", "::ffff:10.20.1.5", "", ""},
		{"mapped allow prefix", "198.51.100.7", "", ""},
		{"allowed v6", "2001:db8:1::5", "", ""},
		{"cgnat allowed explicitly", "100.64.1.1", "", ""},
		{"alibaba metadata inside allowed cgnat", "100.100.100.200", "", "always denied"},
		{"zone ignored", "fe80::1%eth0", "", "always denied"},
		{"not listed", "10.30.0.1", "", ReasonNotAllowed},
		{"public not listed", "8.8.8.8", "", ReasonNotAllowed},
		{"deny cidr beats allow", "10.20.99.1", "", ReasonDenyCIDR},
		{"own network", "10.20.250.10", "", ReasonOwnNetwork},
		{"own network mapped prefix", "10.20.251.3", "", ReasonOwnNetwork},
		{"own network mapped addr", "::ffff:10.20.250.10", "", ReasonOwnNetwork},
		{"loopback", "127.0.0.1", "db.example.com", "always denied"},
		{"loopback mapped", "::ffff:127.0.0.1", "db.example.com", "always denied"},
		{"v6 loopback", "::1", "db.example.com", "always denied"},
		{"metadata", "169.254.169.254", "db.example.com", "always denied"},
		{"metadata mapped", "::ffff:169.254.169.254", "", "always denied"},
		{"aws v6 metadata", "fd00:ec2::254", "db.example.com", "always denied"},
		{"link-local", "169.254.1.1", "db.example.com", "always denied"},
		{"unspecified", "0.0.0.0", "db.example.com", "always denied"},
		{"v6 unspecified", "::", "db.example.com", "always denied"},
		{"this network", "0.1.2.3", "db.example.com", "always denied"},
		{"multicast", "224.0.0.1", "db.example.com", "always denied"},
		{"broadcast", "255.255.255.255", "db.example.com", "always denied"},
		{"nat64 loopback", "64:ff9b::7f00:1", "db.example.com", "always denied"},
		{"6to4 metadata", "2002:a9fe:a9fe::1", "db.example.com", "always denied"},
		{"v4-compatible", "::127.0.0.1", "db.example.com", "always denied"},
		{"host allowed", "203.0.113.9", "db.example.com", ""},
		{"host normalized", "203.0.113.9", "DB.EXAMPLE.COM.", ""},
		{"host allowed private", "172.16.0.9", "bastion.example.com", ""},
		{"host allowed but deny cidr", "10.20.99.2", "db.example.com", ReasonDenyCIDR},
		{"host allowed but own network", "10.20.250.2", "db.example.com", ReasonOwnNetwork},
		{"deny host beats allow cidr", "10.20.1.5", "evil.example.com", ReasonDenyHost},
		{"other host", "203.0.113.9", "other.example.com", ReasonNotAllowed},
		{"suffix is not a wildcard", "203.0.113.9", "x.db.example.com", ReasonNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.Check(netip.MustParseAddr(tt.addr), tt.host)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("Check = %v, want allowed", err)
				}
				return
			}
			var de *DeniedError
			if !errors.As(err, &de) || !errors.Is(err, ErrDenied) {
				t.Fatalf("Check = %v, want DeniedError", err)
			}
			if !strings.Contains(de.Reason, tt.reason) {
				t.Errorf("reason = %q, want %q", de.Reason, tt.reason)
			}
		})
	}
	if err := g.Check(netip.Addr{}, ""); !errors.Is(err, ErrDenied) {
		t.Errorf("invalid addr: %v", err)
	}
}

func TestEmptyAllowlistDeniesAll(t *testing.T) {
	g := newGuard(t, Options{}, fakeDNS{"db.example.com": addrs("10.0.0.5")})
	if !g.Empty() || len(g.Warnings()) != 1 {
		t.Fatalf("Empty=%v Warnings=%v", g.Empty(), g.Warnings())
	}
	for _, a := range []string{"10.0.0.5", "8.8.8.8", "2001:4860::8888"} {
		if err := g.Check(netip.MustParseAddr(a), ""); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v", a, err)
		}
	}
	if _, err := g.Resolve(t.Context(), "db.example.com"); !errors.Is(err, ErrDenied) {
		t.Errorf("Resolve: %v", err)
	}
	g2 := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}}, nil)
	if g2.Empty() || !slices.Equal(g2.Warnings(), []string{WarningNoBastionTargets}) {
		t.Errorf("configured guard without bastion_targets: %v", g2.Warnings())
	}
	g3 := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}, BastionTargets: []string{"b.example.com=10.1.0.0/16"}}, nil)
	if g3.Warnings() != nil {
		t.Errorf("fully configured guard must not warn: %v", g3.Warnings())
	}
}

func TestNewInvalid(t *testing.T) {
	tests := map[string]Options{
		"bad cidr":           {AllowCIDRs: []string{"10.0.0.0/33"}},
		"zero prefix":        {AllowCIDRs: []string{"0.0.0.0/0"}},
		"v6 zero":            {AllowCIDRs: []string{"::/0"}},
		"overlaps loopback":  {AllowCIDRs: []string{"0.0.0.0/1"}},
		"mapped loopback":    {AllowCIDRs: []string{"::ffff:127.0.0.1/128"}},
		"whole mapped range": {AllowCIDRs: []string{"::ffff:0:0/95"}},
		"link-local":         {AllowCIDRs: []string{"169.254.0.0/24"}},
		"metadata v6":        {AllowCIDRs: []string{"fd00:ec2::/32"}},
		"bad deny cidr":      {DenyCIDRs: []string{"nope"}},
		"ip as host":         {AllowHosts: []string{"10.0.0.1"}},
		"wildcard host":      {AllowHosts: []string{"*.example.com"}},
		"bad deny host":      {DenyHosts: []string{"a b"}},
		"invalid own":        {OwnNetworks: []netip.Prefix{{}}},
		"too many entries":   {AllowHosts: make([]string, maxEntries+1)},
		"label starts dash":  {AllowHosts: []string{"-a.example.com"}},
		"numeric host":       {AllowHosts: []string{"1.2.3"}},
		"zone in prefix":     {AllowCIDRs: []string{"fe80::%eth0/64"}},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Fatal("New succeeded")
			}
		})
	}
}

func TestResolve(t *testing.T) {
	dns := fakeDNS{
		"db.example.com":     addrs("10.20.0.5", "2001:db8:1::5"),
		"mixed.example.com":  addrs("10.20.0.6", "127.0.0.1"),
		"rebind.example.com": addrs("169.254.169.254"),
		"many.example.com":   make([]netip.Addr, maxAddrs+1),
		"empty.example.com":  nil,
	}
	g := newGuard(t, Options{AllowCIDRs: []string{"10.20.0.0/16", "2001:db8:1::/48"}}, dns)
	got, err := g.Resolve(t.Context(), "DB.example.com.")
	if err != nil || len(got) != 2 {
		t.Fatalf("Resolve = %v, %v", got, err)
	}
	if got, err := g.Resolve(t.Context(), "10.20.3.4"); err != nil || len(got) != 1 {
		t.Fatalf("Resolve literal = %v, %v", got, err)
	}
	if _, err := g.Resolve(t.Context(), "[2001:db8:1::9]"); err != nil {
		t.Fatalf("Resolve bracketed: %v", err)
	}
	cases := map[string]error{
		"mixed.example.com":   ErrDenied, // one bad address fails the whole host
		"rebind.example.com":  ErrDenied,
		"unknown.example.com": ErrResolve,
		"empty.example.com":   ErrResolve,
		"many.example.com":    ErrResolve,
		"bad host!":           ErrInvalidHost,
		"":                    ErrInvalidHost,
		"fe80::1%eth0":        ErrInvalidHost,
		"127.0.0.1":           ErrDenied,
	}
	for host, want := range cases {
		if _, err := g.Resolve(t.Context(), host); !errors.Is(err, want) {
			t.Errorf("Resolve(%q) = %v, want %v", host, err, want)
		}
	}
}

func TestCheckDenyLists(t *testing.T) {
	g := newGuard(t, Options{DenyCIDRs: []string{"10.9.0.0/16"}, DenyHosts: []string{"old.example.com"}}, nil)
	cases := []struct {
		addr string
		host string
		ok   bool
	}{
		{"", "db.internal", true},
		{"", "OLD.example.com.", false},
		{"10.9.1.1", "", false},
		{"::ffff:10.9.1.1", "", false},
		{"127.0.0.1", "", true}, // only the deny lists apply here
		{"10.8.0.1", "", true},
	}
	for _, tc := range cases {
		var a netip.Addr
		if tc.addr != "" {
			a = netip.MustParseAddr(tc.addr)
		}
		err := g.CheckDenyLists(a, tc.host)
		if (err == nil) != tc.ok {
			t.Errorf("CheckDenyLists(%q, %q) = %v", tc.addr, tc.host, err)
		}
		if err != nil && !errors.Is(err, ErrDenied) {
			t.Errorf("not ErrDenied: %v", err)
		}
	}
}

func TestSetOwnNetworks(t *testing.T) {
	g := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}}, nil)
	a := netip.MustParseAddr("10.1.2.3")
	if err := g.Check(a, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.SetOwnNetworks([]netip.Prefix{netip.MustParsePrefix("10.1.2.0/24")}); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(a, ""); !errors.Is(err, ErrDenied) {
		t.Fatalf("own network not denied: %v", err)
	}
	if len(g.OwnNetworks()) != 1 {
		t.Error("OwnNetworks")
	}
	if err := g.SetOwnNetworks(nil); err != nil || g.Check(a, "") != nil {
		t.Error("clearing own networks")
	}
}

func TestFromConfig(t *testing.T) {
	cfg := config.DefaultRunner()
	g, err := FromConfig(&cfg)
	if err != nil || !g.Empty() {
		t.Fatalf("defaults: %v empty=%v", err, g.Empty())
	}
	cfg.Targets.AllowCIDRs = []string{"10.20.0.0/16"}
	cfg.Targets.AllowHosts = []string{"db.internal.example.com"}
	cfg.Targets.DenyCIDRs = []string{"10.20.9.0/24"}
	cfg.Targets.DenyHosts = []string{"old.internal.example.com"}
	g, err = FromConfig(&cfg)
	if err != nil || g.Empty() {
		t.Fatalf("configured: %v", err)
	}
	if err := g.Check(netip.MustParseAddr("10.20.9.1"), ""); !errors.Is(err, ErrDenied) {
		t.Error("deny_cidrs from config not applied")
	}
}

// discardErr ignores errors of best-effort cleanup.
func discardErr(error) {}

// listen starts a TCP listener on loopback and returns its port.
func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardErr(ln.Close()) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			discardErr(c.Close())
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ln, port
}

func TestDialContext(t *testing.T) {
	_, port := listen(t)
	g := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}, AllowHosts: []string{"db.example.com"}},
		fakeDNS{"db.example.com": addrs("127.0.0.1"), "ok.example.com": addrs("10.255.255.1")})

	// A host on the allowlist that resolves to loopback is still denied
	// (the dial never happens).
	if _, err := g.DialContext(t.Context(), "tcp", net.JoinHostPort("db.example.com", port)); !errors.Is(err, ErrDenied) {
		t.Fatalf("dial loopback host = %v, want ErrDenied", err)
	}
	if _, err := g.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", port)); !errors.Is(err, ErrDenied) {
		t.Fatalf("dial loopback literal = %v", err)
	}
	if _, err := g.DialContext(t.Context(), "tcp", net.JoinHostPort("::ffff:127.0.0.1", port)); !errors.Is(err, ErrDenied) {
		t.Fatalf("dial mapped loopback = %v", err)
	}
	for _, tc := range []struct{ network, addr string }{
		{"udp", "10.0.0.1:53"},
		{"unix", "/tmp/x"},
	} {
		if _, err := g.DialContext(t.Context(), tc.network, tc.addr); !errors.Is(err, ErrUnsupportedNetwork) {
			t.Errorf("%s: %v", tc.network, err)
		}
	}
	for _, a := range []string{"nohostport", "10.0.0.1:0", "10.0.0.1:99999", "10.0.0.1:x"} {
		if _, err := g.DialContext(t.Context(), "tcp", a); !errors.Is(err, ErrInvalidHost) {
			t.Errorf("%q: %v", a, err)
		}
	}
	if _, err := g.DialContext(t.Context(), "tcp6", net.JoinHostPort("10.0.0.1", port)); !errors.Is(err, ErrResolve) {
		t.Errorf("tcp6 to v4 literal: %v", err)
	}
}

// TestControlHook proves the Control hook checks the exact socket address
// (with the requested host name) independently of the pre-check.
func TestControlHook(t *testing.T) {
	g := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}, AllowHosts: []string{"db.example.com"}}, nil)
	cases := []struct {
		host, addr string
		ok         bool
	}{
		{"", "10.1.2.3:5432", true},
		{"", "[::ffff:10.1.2.3]:5432", true},
		{"", "127.0.0.1:5432", false},
		{"", "[::ffff:169.254.169.254]:80", false},
		{"", "203.0.113.1:5432", false},
		{"db.example.com", "203.0.113.1:5432", true},
		{"db.example.com", "127.0.0.1:5432", false},
		{"", "garbage", false},
	}
	for _, tc := range cases {
		err := g.control(tc.host)("tcp", tc.addr, nil)
		if (err == nil) != tc.ok {
			t.Errorf("control(%q)(%q) = %v, want ok=%v", tc.host, tc.addr, err, tc.ok)
		}
	}
	// Own networks added after resolution are seen by the hook.
	if err := g.SetOwnNetworks([]netip.Prefix{netip.MustParsePrefix("10.1.2.0/24")}); err != nil {
		t.Fatal(err)
	}
	if err := g.control("")("tcp", "10.1.2.3:5432", nil); !errors.Is(err, ErrDenied) {
		t.Errorf("own network after resolve: %v", err)
	}
}

func TestDenyHostSkipsDNS(t *testing.T) {
	called := false
	g := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}, DenyHosts: []string{"old.example.com"},
		Lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			called = true
			return addrs("10.0.0.1"), nil
		}}, nil)
	if _, err := g.Resolve(t.Context(), "OLD.example.com"); !errors.Is(err, ErrDenied) || called {
		t.Fatalf("err = %v, dns called = %v", err, called)
	}
}

// FuzzCheck: an always-denied address is never allowed whatever the host
// name, and an IPv4-mapped address gets the same verdict as its IPv4 form.
func FuzzCheck(f *testing.F) {
	f.Add([]byte{127, 0, 0, 1}, "db.example.com")
	f.Add([]byte{169, 254, 169, 254}, "")
	f.Add([]byte{10, 20, 0, 1}, "")
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 127, 0, 0, 1}, "x")
	g, err := New(Options{
		AllowCIDRs: []string{"10.0.0.0/8", "2001:db8::/32", "100.64.0.0/10", "64:ff9b::/96"},
		AllowHosts: []string{"db.example.com"},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte, host string) {
		a, ok := netip.AddrFromSlice(raw)
		if !ok {
			return
		}
		err := g.Check(a, host)
		if c := netguard.Classify(a); c.Blocked && c.Hard && err == nil {
			t.Fatalf("always-denied %s (%s) allowed for %q", a, c.Reason, host)
		}
		if a.Is4() {
			mapped := netip.AddrFrom16(a.As16())
			if (g.Check(mapped, host) == nil) != (err == nil) {
				t.Fatalf("mapped %s verdict differs", mapped)
			}
		}
	})
}

// TestDockerNetworks: Docker subnets and gateways are denied unless an
// allow_cidrs entry inside the subnet (a gateway: its exact /32) allows
// them; allow_hosts names resolving there are denied too.
func TestDockerNetworks(t *testing.T) {
	g := newGuard(t, Options{
		AllowCIDRs: []string{"172.16.0.0/12", "172.20.5.0/24", "172.21.0.1/32", "10.0.0.0/8"},
		AllowHosts: []string{"db.docker.internal"},
	}, fakeDNS{"db.docker.internal": addrs("172.22.0.9")})
	g.SetDockerNetworks(
		[]netip.Prefix{netip.MustParsePrefix("172.20.0.0/16"), netip.MustParsePrefix("172.21.0.0/16"), netip.MustParsePrefix("172.22.0.0/16"), netip.MustParsePrefix("::ffff:172.23.0.0/112")},
		[]netip.Addr{netip.MustParseAddr("172.20.0.1"), netip.MustParseAddr("172.21.0.1"), netip.MustParseAddr("172.22.0.1"), netip.MustParseAddr("10.9.9.9")},
	)
	for a, ok := range map[string]bool{
		"172.20.9.9": false, // only the broad /12 covers it
		"172.20.5.7": true,  // an entry inside the Docker subnet
		"172.20.0.1": false, // gateway without its /32
		"172.21.0.1": true,  // gateway with its exact /32
		"172.21.0.7": false, // the /32 does not open the rest of the subnet
		"172.23.1.1": false, // mapped prefix normalized
		"10.9.9.9":   false, // a gateway outside every Docker subnet
		"10.1.1.1":   true,  // not Docker at all
		"172.30.0.1": true,
	} {
		err := g.Check(netip.MustParseAddr(a), "")
		if (err == nil) != ok {
			t.Errorf("%s: %v, want allowed=%v", a, err, ok)
		}
		var de *DeniedError
		if err != nil && (!errors.As(err, &de) || de.Reason != ReasonDockerNetwork) {
			t.Errorf("%s: reason %v", a, err)
		}
	}
	if err := g.Check(netip.MustParseAddr("172.22.0.9"), "db.docker.internal"); !errors.Is(err, ErrDenied) {
		t.Errorf("allow_hosts opened a Docker network: %v", err)
	}
	if _, err := g.Resolve(t.Context(), "db.docker.internal"); !errors.Is(err, ErrDenied) {
		t.Errorf("Resolve: %v", err)
	}
	g.SetDockerNetworks(nil, nil)
	if err := g.Check(netip.MustParseAddr("172.20.9.9"), ""); err != nil {
		t.Errorf("after clearing: %v", err)
	}
}

// TestBastionTargets: a listed bastion forwards only to its targets.
func TestBastionTargets(t *testing.T) {
	g := newGuard(t, Options{
		AllowHosts: []string{"bastion.example.com"},
		BastionTargets: []string{
			"bastion.example.com=10.30.0.0/24", "Bastion.Example.Com.=db.internal", "203.0.113.7=127.0.0.1/32",
			"[2001:db8::7]=db6.internal",
		},
	}, nil)
	bAddrs := addrs("203.0.113.9")
	for target, ok := range map[string]bool{
		"10.30.0.5": true, "10.30.1.5": false, "db.internal": true, "DB.internal.": true, "other.internal": false,
		"127.0.0.1": false, "[10.30.0.6]": true,
	} {
		if err := g.CheckBehindBastion("bastion.example.com", bAddrs, target); (err == nil) != ok {
			t.Errorf("bastion.example.com -> %s: %v", target, err)
		}
	}
	// Matched by address too; with two matching entries both must allow.
	if err := g.CheckBehindBastion("jump.example.com", addrs("203.0.113.7"), "127.0.0.1"); err != nil {
		t.Errorf("by address: %v", err)
	}
	if err := g.CheckBehindBastion("bastion.example.com", addrs("203.0.113.7"), "127.0.0.1"); !errors.Is(err, ErrDenied) {
		t.Errorf("two rules, one denies: %v", err)
	}
	if err := g.CheckBehindBastion("2001:db8::7", nil, "db6.internal"); err != nil {
		t.Errorf("ipv6 bastion: %v", err)
	}
	// An unlisted bastion is not restricted here.
	if err := g.CheckBehindBastion("other.example.com", addrs("198.51.100.1"), "anything.internal"); err != nil || g.HasBastionTargets("other.example.com", nil) {
		t.Errorf("unlisted bastion: %v", err)
	}
	if !g.HasBastionTargets("BASTION.example.com", nil) || !g.HasBastionTargets("x", addrs("203.0.113.7")) {
		t.Error("HasBastionTargets")
	}
	var de *DeniedError
	if err := g.CheckBehindBastion("bastion.example.com", nil, "x.internal"); !errors.As(err, &de) || de.Reason != ReasonBastionTarget {
		t.Errorf("reason: %v", err)
	}
	for _, bad := range []string{"nobastion", "b.example.com=10.0.0.1", "b.example.com=0.0.0.0/0", "bad host=db", "b=x/y", "fe80::1%eth0=db"} {
		if _, err := New(Options{BastionTargets: []string{bad}}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
