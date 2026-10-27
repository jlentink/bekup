package netguard

import (
	"net/netip"
)

// Reason says why an address is denied. The values are stable, short and
// safe to show to admins.
type Reason string

// Denial reasons.
const (
	ReasonInvalid             Reason = "invalid address"
	ReasonUnspecified         Reason = "unspecified"
	ReasonThisNetwork         Reason = "this network (0.0.0.0/8)"
	ReasonLoopback            Reason = "loopback"
	ReasonPrivate             Reason = "private network"
	ReasonSharedAddress       Reason = "shared address space (CGNAT)"
	ReasonLinkLocal           Reason = "link-local"
	ReasonMetadata            Reason = "cloud metadata endpoint"
	ReasonMulticast           Reason = "multicast"
	ReasonBroadcast           Reason = "broadcast"
	ReasonReserved            Reason = "reserved"
	ReasonDocumentation       Reason = "documentation range"
	ReasonBenchmarking        Reason = "benchmarking range"
	ReasonProtocolAssignments Reason = "IETF protocol assignments"
	ReasonUniqueLocal         Reason = "unique-local (fc00::/7)"
	ReasonSiteLocal           Reason = "site-local (deprecated)"
	ReasonDiscard             Reason = "discard-only"
	ReasonIPv4Compatible      Reason = "IPv4-compatible IPv6 (deprecated)"
	ReasonIPv4Translated      Reason = "IPv4-translated IPv6"
	ReasonNAT64Local          Reason = "local-use NAT64"
	ReasonNAT64Embedded       Reason = "NAT64 address embedding a denied IPv4 address"
	Reason6to4Embedded        Reason = "6to4 address embedding a denied IPv4 address"
	ReasonTeredo              Reason = "Teredo tunnel"
)

// Classification is the verdict for one address.
type Classification struct {
	// Blocked is true when the address is denied by default.
	Blocked bool
	// Reason is set when Blocked is true.
	Reason Reason
	// Hard is true when no allowlist entry can re-allow the address.
	Hard bool
}

type rangeRule struct {
	prefix netip.Prefix
	reason Reason
	hard   bool
}

func rule(p string, r Reason, hard bool) rangeRule {
	return rangeRule{prefix: netip.MustParsePrefix(p), reason: r, hard: hard}
}

// v4Rules are checked in order against an (unmapped) IPv4 address; more
// specific entries come first. Loopback is handled separately.
var v4Rules = []rangeRule{
	rule("0.0.0.0/8", ReasonThisNetwork, true),
	rule("255.255.255.255/32", ReasonBroadcast, true),
	rule("169.254.169.254/32", ReasonMetadata, true),
	rule("169.254.0.0/16", ReasonLinkLocal, true),
	rule("100.100.100.200/32", ReasonMetadata, true), // Alibaba Cloud
	rule("224.0.0.0/4", ReasonMulticast, true),
	rule("240.0.0.0/4", ReasonReserved, true),
	rule("10.0.0.0/8", ReasonPrivate, false),
	rule("172.16.0.0/12", ReasonPrivate, false),
	rule("192.168.0.0/16", ReasonPrivate, false),
	rule("100.64.0.0/10", ReasonSharedAddress, false),
	rule("192.0.0.0/24", ReasonProtocolAssignments, false),
	rule("192.0.2.0/24", ReasonDocumentation, false),
	rule("198.51.100.0/24", ReasonDocumentation, false),
	rule("203.0.113.0/24", ReasonDocumentation, false),
	rule("198.18.0.0/15", ReasonBenchmarking, false),
	rule("192.88.99.0/24", ReasonReserved, false), // deprecated 6to4 relay anycast
}

var v4Loopback = netip.MustParsePrefix("127.0.0.0/8")

// v6Rules are checked in order against a non-mapped IPv6 address.
// Loopback and the embedded-IPv4 forms are handled separately.
var v6Rules = []rangeRule{
	rule("::/128", ReasonUnspecified, true),
	rule("::ffff:0:0:0/96", ReasonIPv4Translated, true),
	rule("64:ff9b:1::/48", ReasonNAT64Local, false),
	rule("100::/64", ReasonDiscard, false),
	rule("2001::/32", ReasonTeredo, true),
	rule("2001:2::/48", ReasonBenchmarking, false),
	rule("2001:db8::/32", ReasonDocumentation, false),
	rule("2001::/23", ReasonProtocolAssignments, false),
	rule("3fff::/20", ReasonDocumentation, false),
	rule("5f00::/16", ReasonReserved, false),        // SRv6 SIDs (RFC 9602)
	rule("fd00:ec2::254/128", ReasonMetadata, true), // AWS IMDS over IPv6
	rule("fc00::/7", ReasonUniqueLocal, false),
	rule("fe80::/10", ReasonLinkLocal, true),
	rule("fec0::/10", ReasonSiteLocal, false),
	rule("ff00::/8", ReasonMulticast, true),
}

var (
	v6Loopback     = netip.MustParsePrefix("::1/128")
	v4Compatible   = netip.MustParsePrefix("::/96")
	v4Mapped       = netip.MustParsePrefix("::ffff:0:0/96")
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour      = netip.MustParsePrefix("2002::/16")
	globalUnicast  = netip.MustParsePrefix("2000::/3")
)

// Classify returns the default verdict for addr. IPv4-mapped addresses are
// unmapped first and IPv6 zones are ignored. Loopback is reported as hard;
// a Guard built for dev mode treats it as soft (see Options).
func Classify(addr netip.Addr) Classification {
	return classify(addr, false)
}

func blocked(r Reason, hard bool) Classification {
	return Classification{Blocked: true, Reason: r, Hard: hard}
}

func classify(addr netip.Addr, loopbackSoft bool) Classification {
	if !addr.IsValid() {
		return blocked(ReasonInvalid, true)
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return classifyV4(addr, loopbackSoft)
	}
	return classifyV6(addr, loopbackSoft)
}

func classifyV4(addr netip.Addr, loopbackSoft bool) Classification {
	if v4Loopback.Contains(addr) {
		return blocked(ReasonLoopback, !loopbackSoft)
	}
	for _, r := range v4Rules {
		if r.prefix.Contains(addr) {
			return blocked(r.reason, r.hard)
		}
	}
	return Classification{}
}

func classifyV6(addr netip.Addr, loopbackSoft bool) Classification {
	switch {
	case v6Loopback.Contains(addr):
		return blocked(ReasonLoopback, !loopbackSoft)
	case addr.IsUnspecified():
		return blocked(ReasonUnspecified, true)
	case v4Compatible.Contains(addr):
		return blocked(ReasonIPv4Compatible, true)
	case nat64WellKnown.Contains(addr):
		b := addr.As16()
		inner := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		if classifyV4(inner, false).Blocked {
			return blocked(ReasonNAT64Embedded, true)
		}
		return Classification{}
	case sixToFour.Contains(addr):
		b := addr.As16()
		inner := netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		if classifyV4(inner, false).Blocked {
			return blocked(Reason6to4Embedded, true)
		}
		return Classification{}
	}
	for _, r := range v6Rules {
		if r.prefix.Contains(addr) {
			return blocked(r.reason, r.hard)
		}
	}
	if !globalUnicast.Contains(addr) {
		return blocked(ReasonReserved, true)
	}
	return Classification{}
}
