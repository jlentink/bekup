package targets

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestPolicySnapshot(t *testing.T) {
	t.Parallel()
	g := newGuard(t, Options{
		AllowCIDRs:  []string{"10.20.0.7/16", "::ffff:198.51.100.0/120"},
		AllowHosts:  []string{"DB.Example.com.", "a.example.com"},
		DenyCIDRs:   []string{"10.20.99.0/24"},
		DenyHosts:   []string{"evil.example.com"},
		OwnNetworks: []netip.Prefix{netip.MustParsePrefix("10.214.0.0/16")},
		BastionTargets: []string{
			"Bastion.example.com=db.internal", "bastion.example.com=10.30.0.0/24",
			"203.0.113.7=127.0.0.1/32",
		},
	}, nil)
	p := g.Policy()
	if want := prefixes("10.20.0.0/16", "198.51.100.0/24"); !slices.Equal(p.AllowCIDRs, want) {
		t.Errorf("AllowCIDRs = %v, want %v (masked, unmapped)", p.AllowCIDRs, want)
	}
	if !slices.Equal(p.AllowHosts, []string{"a.example.com", "db.example.com"}) {
		t.Errorf("AllowHosts = %v (sorted, normalized)", p.AllowHosts)
	}
	if !slices.Equal(p.DenyCIDRs, prefixes("10.20.99.0/24")) || !slices.Equal(p.DenyHosts, []string{"evil.example.com"}) {
		t.Errorf("deny lists = %v %v", p.DenyCIDRs, p.DenyHosts)
	}
	if len(p.Bastions) != 2 || p.Bastions[0].Bastion != "203.0.113.7" || p.Bastions[1].Bastion != "bastion.example.com" {
		t.Fatalf("Bastions = %+v", p.Bastions)
	}
	b := p.Bastions[1]
	if !slices.Equal(b.CIDRs, prefixes("10.30.0.0/24")) || !slices.Equal(b.Hosts, []string{"db.internal"}) {
		t.Errorf("bastion = %+v", b)
	}
	if !slices.Equal(p.OwnNetworks, prefixes("10.214.0.0/16")) || p.Warnings != nil {
		t.Errorf("own %v warnings %v", p.OwnNetworks, p.Warnings)
	}
	// The snapshot is a copy.
	p.AllowCIDRs[0] = netip.Prefix{}
	p.Bastions[1].CIDRs[0] = netip.Prefix{}
	if q := g.Policy(); !q.AllowCIDRs[0].IsValid() || !q.Bastions[1].CIDRs[0].IsValid() {
		t.Error("Policy shares the guard's slices")
	}
}

func TestWarningCodes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		opts Options
		want []WarningCode
	}{
		{Options{}, []WarningCode{WarningEmptyAllowlist}},
		{Options{BastionTargets: []string{"b.example.com=10.1.0.0/16"}}, []WarningCode{WarningEmptyAllowlist}},
		{Options{AllowHosts: []string{"db.example.com"}}, []WarningCode{WarningBastionsUnrestricted}},
		{Options{AllowCIDRs: []string{"10.0.0.0/8"}, BastionTargets: []string{"b.example.com=10.1.0.0/16"}}, nil},
	} {
		g := newGuard(t, tt.opts, nil)
		if got := g.WarningCodes(); !slices.Equal(got, tt.want) {
			t.Errorf("%+v: WarningCodes = %v, want %v", tt.opts, got, tt.want)
		}
		if got := g.Policy().Warnings; !slices.Equal(got, tt.want) {
			t.Errorf("%+v: Policy().Warnings = %v", tt.opts, got)
		}
		// Every code has its startup text.
		for i, w := range g.Warnings() {
			if !strings.HasPrefix(w, "targets: ") || w != warningText[tt.want[i]] {
				t.Errorf("warning %d = %q", i, w)
			}
		}
	}
}

// TestAlwaysDeniedMatchesCheck: every range AlwaysDenied lists is denied by
// Check with an "always denied" reason (step 1, before any list).
func TestAlwaysDeniedMatchesCheck(t *testing.T) {
	t.Parallel()
	g := newGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}}, nil)
	alwaysDeniedErr := func(a netip.Addr) bool {
		var de *DeniedError
		return errors.As(g.Check(a, ""), &de) && strings.HasPrefix(de.Reason, "always denied")
	}
	seen := map[DeniedCategory]bool{}
	for _, r := range AlwaysDenied() {
		if seen[r.Category] {
			t.Errorf("category %q listed twice", r.Category)
		}
		seen[r.Category] = true
		if r.Category == DeniedEmbeddedIPv4 || r.Category == DeniedNonGlobalIPv6 {
			continue // conditional or rangeless: checked by example below
		}
		if len(r.Prefixes) == 0 {
			t.Errorf("%q: no ranges", r.Category)
		}
		for _, p := range r.Prefixes {
			first, last := p.Masked().Addr(), lastAddr(p)
			if !alwaysDeniedErr(first) || !alwaysDeniedErr(last) {
				t.Errorf("%q: %s is not always denied (%v .. %v)", r.Category, p, g.Check(first, ""), g.Check(last, ""))
			}
		}
	}
	for _, a := range []string{"64:ff9b::7f00:1", "2002:a9fe:a9fe::1", "4000::1", "::2:0:0:1"} {
		if !alwaysDeniedErr(netip.MustParseAddr(a)) {
			t.Errorf("%s: %v", a, g.Check(netip.MustParseAddr(a), ""))
		}
	}
	// The conditional ranges are not denied as a whole.
	if alwaysDeniedErr(netip.MustParseAddr("64:ff9b::808:808")) {
		t.Error("NAT64 of a public address is always denied")
	}
	if len(seen) != 10 {
		t.Errorf("categories = %d, want 10", len(seen))
	}
	// The result is a copy.
	AlwaysDenied()[0].Prefixes[0] = netip.Prefix{}
	if !AlwaysDenied()[0].Prefixes[0].IsValid() {
		t.Error("AlwaysDenied shares its table")
	}
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}
