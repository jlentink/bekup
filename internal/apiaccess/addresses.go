package apiaccess

import (
	"net/netip"
	"strings"

	"github.com/jlentink/bekup/internal/serviceutil"
)

// Client address allowlist limits (D-220 amendment 1; the same rules as
// the api_tokens_allowed_cidrs and api_tokens_addresses CHECKs).
const (
	// MaxAddresses is the most networks a token may list.
	MaxAddresses = 32
	// maxAddressChars bounds one entry (the longest IPv6 prefix is 43
	// characters).
	maxAddressChars = 64
	// MinPrefixV4 and MinPrefixV6 are the widest networks a list may hold
	// (M1 of the D-220 review): anything wider is "any address" in all but
	// name and must be chosen as such (alerted and badged). The same rule
	// as public.api_cidrs_valid.
	MinPrefixV4 = 8
	MinPrefixV6 = 32
)

// Field names of the address inputs (VALIDATION errors).
const (
	FieldAllowedCIDRs    = "allowedCidrs"
	FieldAllowAnyAddress = "allowAnyAddress"
)

// ParseAddresses checks a token's client addresses and returns the list in
// canonical form (nil with allowAny). Exactly one of the two is required:
// a list of 1-32 distinct IPv4/IPv6 networks, or allowAny (an explicit
// choice, never implied by an empty list). A bare address is a host
// network (/32, /128). Refused: host bits (the message names the network
// meant), IPv4 networks wider than /8 and IPv6 networks wider than /32
// (that wide is allowAny, which must be chosen as such), IPv4-mapped IPv6 networks (client
// addresses are unmapped before matching, so they could never match; use
// the IPv4 form), zones, duplicates. Errors are *serviceutil.FieldError.
func ParseAddresses(cidrs []string, allowAny bool) ([]netip.Prefix, error) {
	if allowAny {
		if len(cidrs) > 0 {
			return nil, serviceutil.FieldErr(FieldAllowedCIDRs,
				"remove the networks or clear \"allow from any address\": a token has an address list or allows any address, never both")
		}
		return nil, nil
	}
	if len(cidrs) == 0 {
		return nil, serviceutil.FieldErr(FieldAllowedCIDRs,
			"add at least one network the token may be used from, or explicitly allow any address")
	}
	if len(cidrs) > MaxAddresses {
		return nil, serviceutil.FieldErr(FieldAllowedCIDRs, "at most %d networks per token", MaxAddresses)
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	seen := make(map[netip.Prefix]int, len(cidrs))
	for i, raw := range cidrs {
		n := i + 1
		p, err := parseNetwork(n, raw)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[p]; dup {
			return nil, serviceutil.FieldErr(FieldAllowedCIDRs, "entry %d (%s) repeats entry %d", n, p, first)
		}
		seen[p] = n
		out = append(out, p)
	}
	return out, nil
}

// parseNetwork parses entry n of the list.
func parseNetwork(n int, raw string) (netip.Prefix, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs, "entry %d is empty", n)
	}
	if len(s) > maxAddressChars {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs, "entry %d is too long for a network", n)
	}
	if strings.Contains(s, "%") {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs, "entry %d: IPv6 zones (%%...) are not allowed", n)
	}
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, notANetwork(n, s)
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, notANetwork(n, s)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs,
			"entry %d (%s) is an IPv4-mapped IPv6 network: write it in the IPv4 form (for example 192.0.2.0/24)", n, s)
	}
	if p.Bits() == 0 {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs,
			"entry %d (%s) allows every address: choose \"allow from any address\" instead", n, s)
	}
	if minBits := minPrefix(p); p.Bits() < minBits {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs,
			"entry %d (%s) is wider than /%d: a network that wide is as good as any address; choose \"allow from any address\" instead or narrow it",
			n, s, minBits)
	}
	if m := p.Masked(); m != p {
		return netip.Prefix{}, serviceutil.FieldErr(FieldAllowedCIDRs,
			"entry %d (%s) has bits set after the prefix length: did you mean %s?", n, s, m)
	}
	return p, nil
}

// minPrefix is the shortest prefix length a list entry of p's family may
// have.
func minPrefix(p netip.Prefix) int {
	if p.Addr().Is4() {
		return MinPrefixV4
	}
	return MinPrefixV6
}

func notANetwork(n int, s string) error {
	return serviceutil.FieldErr(FieldAllowedCIDRs,
		"entry %d (%q) is not an IPv4 or IPv6 address or network such as 203.0.113.7 or 203.0.113.0/24", n, s)
}

// AddressStrings returns the canonical text of networks.
func AddressStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}
