package targets

import (
	"maps"
	"net/netip"
	"slices"
)

// Policy is a read-only snapshot of the allowlist as the guard applies it
// (normalized: masked CIDRs, lower-case host names without a trailing
// dot). The runner reports it through GetTargetPolicy so admins can see
// it in the UI (D-223); it holds no secrets.
type Policy struct {
	// AllowCIDRs and DenyCIDRs keep the configured order.
	AllowCIDRs []netip.Prefix
	DenyCIDRs  []netip.Prefix
	// AllowHosts and DenyHosts are sorted.
	AllowHosts []string
	DenyHosts  []string
	// Bastions are sorted by bastion.
	Bastions []BastionPolicy
	// OwnNetworks are the runner's own networks at the time of the call
	// (SetOwnNetworks): control network, own addresses, the run subnet
	// pool and every per-run network.
	OwnNetworks []netip.Prefix
	// Warnings are the startup warnings (WarningCodes).
	Warnings []WarningCode
}

// BastionPolicy is the bastion_targets list of one bastion.
type BastionPolicy struct {
	// Bastion is the normalized host name or IP address.
	Bastion string
	// CIDRs in the configured order, then Hosts sorted.
	CIDRs []netip.Prefix
	Hosts []string
}

// Policy returns a snapshot of the allowlist.
func (g *Guard) Policy() Policy {
	p := Policy{
		AllowCIDRs:  slices.Clone(g.allowCIDRs),
		DenyCIDRs:   slices.Clone(g.denyCIDRs),
		AllowHosts:  slices.Sorted(maps.Keys(g.allowHosts)),
		DenyHosts:   slices.Sorted(maps.Keys(g.denyHosts)),
		OwnNetworks: g.OwnNetworks(),
		Warnings:    g.WarningCodes(),
	}
	for _, b := range slices.Sorted(maps.Keys(g.bastions)) {
		r := g.bastions[b]
		p.Bastions = append(p.Bastions, BastionPolicy{
			Bastion: b,
			CIDRs:   slices.Clone(r.cidrs),
			Hosts:   slices.Sorted(maps.Keys(r.hosts)),
		})
	}
	return p
}

// DeniedCategory names a class of addresses that no runner.toml entry can
// allow (step 1 of the decision order). The values are stable.
type DeniedCategory string

// Always-denied categories.
const (
	DeniedLoopback      DeniedCategory = "loopback"
	DeniedUnspecified   DeniedCategory = "unspecified"
	DeniedLinkLocal     DeniedCategory = "link_local"
	DeniedCloudMetadata DeniedCategory = "cloud_metadata"
	// DeniedMulticast covers multicast and the IPv4 broadcast address.
	DeniedMulticast DeniedCategory = "multicast_broadcast"
	DeniedReserved  DeniedCategory = "reserved"
	// DeniedIPv4InIPv6: IPv4-compatible and IPv4-translated IPv6 forms
	// (IPv4-mapped addresses are unmapped and checked as IPv4).
	DeniedIPv4InIPv6 DeniedCategory = "ipv4_in_ipv6"
	DeniedTeredo     DeniedCategory = "teredo"
	// DeniedEmbeddedIPv4: NAT64 and 6to4 addresses only when the embedded
	// IPv4 address is itself denied by default; the ranges are listed
	// for orientation, not denied as a whole.
	DeniedEmbeddedIPv4 DeniedCategory = "embedded_denied_ipv4"
	// DeniedNonGlobalIPv6: IPv6 outside global unicast (2000::/3) that no
	// other category names, except the private ranges (unique-local,
	// site-local, discard, local-use NAT64), which are denied only by
	// default. No fixed list of ranges.
	DeniedNonGlobalIPv6 DeniedCategory = "non_global_ipv6"
)

// DeniedRange is one always-denied category with its ranges.
type DeniedRange struct {
	Category DeniedCategory
	Prefixes []netip.Prefix
}

// alwaysDenied is the fixed list behind AlwaysDenied: the hard-denied set
// of internal/netguard's Classify, grouped for people. A test checks that
// Check denies every listed range (except the conditional and the
// rangeless categories, which it checks by example).
var alwaysDenied = []DeniedRange{
	{DeniedLoopback, prefixes("127.0.0.0/8", "::1/128")},
	{DeniedUnspecified, prefixes("0.0.0.0/8", "::/128")},
	{DeniedLinkLocal, prefixes("169.254.0.0/16", "fe80::/10")},
	{DeniedCloudMetadata, prefixes("169.254.169.254/32", "100.100.100.200/32", "fd00:ec2::254/128")},
	{DeniedMulticast, prefixes("224.0.0.0/4", "255.255.255.255/32", "ff00::/8")},
	{DeniedReserved, prefixes("240.0.0.0/4")},
	{DeniedIPv4InIPv6, prefixes("::/96", "::ffff:0:0:0/96")},
	{DeniedTeredo, prefixes("2001::/32")},
	{DeniedEmbeddedIPv4, prefixes("64:ff9b::/96", "2002::/16")},
	{DeniedNonGlobalIPv6, nil},
}

// AlwaysDenied returns the always-denied categories in a fixed order.
func AlwaysDenied() []DeniedRange {
	out := make([]DeniedRange, len(alwaysDenied))
	for i, r := range alwaysDenied {
		out[i] = DeniedRange{Category: r.Category, Prefixes: slices.Clone(r.Prefixes)}
	}
	return out
}

func prefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, v := range s {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}
