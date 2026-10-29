package netguard

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/jlentink/bekup/internal/hostname"
)

// allowlist is an immutable, validated set of CIDRs and host patterns.
type allowlist struct {
	prefixes []netip.Prefix // IPv4 prefixes are stored unmapped
	exact    map[string]struct{}
	suffixes []string // ".example.com" for "*.example.com"
}

var emptyAllowlist = &allowlist{exact: map[string]struct{}{}}

func (a *allowlist) containsAddr(addr netip.Addr) bool {
	for _, p := range a.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (a *allowlist) matchesHost(host string) bool {
	if host == "" {
		return false
	}
	if _, ok := a.exact[host]; ok {
		return true
	}
	for _, s := range a.suffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// v6AllowContainers are the IPv6 ranges an allowlist prefix may lie in
// (global unicast and the soft-denied special ranges). Anything else is
// reserved space that is always denied.
var v6AllowContainers = []netip.Prefix{
	globalUnicast,
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

func containsPrefix(outer, inner netip.Prefix) bool {
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

// isCarveOut reports whether hard rule r is a metadata address strictly
// inside prefix p (such as 100.100.100.200 in 100.64.0.0/10). Such a
// prefix is accepted: Guard.Check classifies before consulting the
// allowlist, so the carved-out address stays denied.
func isCarveOut(r rangeRule, p netip.Prefix) bool {
	return r.reason == ReasonMetadata && p.Bits() < r.prefix.Bits() && p.Contains(r.prefix.Addr())
}

// parseAllowPrefix validates one allowlist CIDR. It rejects /0 and any
// prefix that overlaps a hard-denied range, so the allowlist can never
// re-allow metadata, link-local, multicast, reserved or tunnel forms (or
// loopback outside dev mode).
func parseAllowPrefix(s string, loopbackSoft bool) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q", s)
	}
	if p.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("CIDR %q must not have a zone", s)
	}
	p = p.Masked()
	if p.Bits() == 0 {
		return netip.Prefix{}, fmt.Errorf("CIDR %q matches every address", s)
	}
	if p.Addr().Is6() && p.Overlaps(v4Mapped) {
		if p.Bits() <= v4Mapped.Bits() {
			return netip.Prefix{}, fmt.Errorf("CIDR %q covers the whole IPv4-mapped range", s)
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-v4Mapped.Bits()).Masked()
	}
	if p.Addr().Is4() {
		if !loopbackSoft && p.Overlaps(v4Loopback) {
			return netip.Prefix{}, fmt.Errorf("CIDR %q overlaps the loopback range (only allowed in dev mode)", s)
		}
		for _, r := range v4Rules {
			if r.hard && p.Overlaps(r.prefix) && !isCarveOut(r, p) {
				return netip.Prefix{}, fmt.Errorf("CIDR %q overlaps the always-denied range %s (%s)", s, r.prefix, r.reason)
			}
		}
		return p, nil
	}
	if loopbackSoft && p == v6Loopback {
		return p, nil
	}
	inside := false
	for _, c := range v6AllowContainers {
		if containsPrefix(c, p) {
			inside = true
			break
		}
	}
	if !inside {
		return netip.Prefix{}, fmt.Errorf("CIDR %q is outside the allowlistable IPv6 ranges", s)
	}
	for _, r := range v6Rules {
		if r.hard && p.Overlaps(r.prefix) && !isCarveOut(r, p) {
			return netip.Prefix{}, fmt.Errorf("CIDR %q overlaps the always-denied range %s (%s)", s, r.prefix, r.reason)
		}
	}
	if p.Overlaps(sixToFour) {
		return netip.Prefix{}, fmt.Errorf("CIDR %q overlaps the 6to4 range %s; allowlist the IPv4 address instead", s, sixToFour)
	}
	return p, nil
}

// normalizeHost lowercases a host name and strips one trailing dot.
func normalizeHost(h string) string { return hostname.Normalize(h) }

// validHostName applies the shared host-name rule (internal/hostname,
// D-140) to a normalized name, so no component accepts a host the guard
// refuses.
func validHostName(h string) bool { return hostname.Validate(h) == nil }

// wildcardDNSServices are public services that resolve names under them
// to any address encoded in the name (10-0-0-5.nip.io → 10.0.0.5) or to
// loopback. An allowlist entry under one of them would let anyone pick
// the private address a name resolves to, so they are refused (exact and
// wildcard entries).
var wildcardDNSServices = []string{
	"nip.io", "sslip.io", "xip.io", "nip.direct", "localtest.me", "lvh.me",
	"vcap.me", "traefik.me", "localhost.direct", "1u.ms", "lacolhost.com",
}

// underWildcardDNSService reports whether h is one of wildcardDNSServices
// or a name under one.
func underWildcardDNSService(h string) bool {
	for _, d := range wildcardDNSServices {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// isPublicSuffix reports whether h is itself a public suffix (ICANN or
// private section of the Public Suffix List, e.g. co.uk or github.io):
// a wildcard under it would match names anyone can register.
func isPublicSuffix(h string) bool {
	ps, _ := publicsuffix.PublicSuffix(h)
	return ps == h
}

// parseHostPattern validates an allowlist host entry: an exact host name or
// "*.suffix" with at least two labels in the suffix, where the suffix is
// not a public suffix (*.co.uk). IP literals and names under public
// wildcard-DNS services (nip.io and the like) are rejected.
func parseHostPattern(s string) (pattern string, wildcard bool, err error) {
	h := normalizeHost(s)
	if rest, ok := strings.CutPrefix(h, "*."); ok {
		if !validHostName(rest) || !strings.Contains(rest, ".") {
			return "", false, fmt.Errorf("invalid wildcard host %q (use *.example.com)", s)
		}
		if _, err := netip.ParseAddr(rest); err == nil {
			return "", false, fmt.Errorf("invalid wildcard host %q", s)
		}
		if isPublicSuffix(rest) {
			return "", false, fmt.Errorf("wildcard host %q covers a public suffix (anyone can register names under it)", s)
		}
		if underWildcardDNSService(rest) {
			return "", false, fmt.Errorf("wildcard host %q is under a public wildcard-DNS service", s)
		}
		return "." + rest, true, nil
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return "", false, fmt.Errorf("host %q is an IP address; use a CIDR such as %s/32", s, h)
	}
	if !validHostName(h) {
		return "", false, fmt.Errorf("invalid host name %q (no scheme, port or path)", s)
	}
	if underWildcardDNSService(h) {
		return "", false, fmt.Errorf("host %q is under a public wildcard-DNS service", s)
	}
	return h, false, nil
}

func buildAllowlist(cidrs, hosts []string, loopbackSoft bool) (*allowlist, error) {
	a := &allowlist{exact: make(map[string]struct{}, len(hosts))}
	var errs []error
	for _, s := range cidrs {
		p, err := parseAllowPrefix(s, loopbackSoft)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		a.prefixes = append(a.prefixes, p)
	}
	for _, s := range hosts {
		pat, wildcard, err := parseHostPattern(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if wildcard {
			a.suffixes = append(a.suffixes, pat)
		} else {
			a.exact[pat] = struct{}{}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("netguard: invalid allowlist: %w", errors.Join(errs...))
	}
	return a, nil
}

// Allowlist list names of EntryError.
const (
	ListCIDRs = "cidrs"
	ListHosts = "hosts"
)

// EntryError is one rejected allowlist entry: the list it belongs to
// (ListCIDRs or ListHosts), its index there and a presentable reason
// (it quotes the entry, never anything else).
type EntryError struct {
	List  string
	Index int
	// Field is "" for the entry's value and "scopes" for its scopes
	// (Set.NormalizeEntries).
	Field   string
	Message string
}

func (e *EntryError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("netguard: %s[%d].%s: %s", e.List, e.Index, e.Field, e.Message)
	}
	return fmt.Sprintf("netguard: %s[%d]: %s", e.List, e.Index, e.Message)
}

// NormalizeAllowlist validates every entry exactly like SetAllowlist and
// returns the canonical forms (CIDRs masked, IPv4-mapped prefixes
// unmapped; host names lowercased without a trailing dot, wildcards as
// "*.suffix"), in input order without duplicates, plus one EntryError per
// rejected entry. Callers that must not apply a partially valid list
// check errs first.
func (g *Guard) NormalizeAllowlist(cidrs, hosts []string) (outCIDRs, outHosts []string, errs []*EntryError) {
	outCIDRs, outHosts = []string{}, []string{}
	seen := map[string]bool{}
	for i, s := range cidrs {
		p, err := parseAllowPrefix(s, g.loopbackSoft)
		if err != nil {
			errs = append(errs, &EntryError{List: ListCIDRs, Index: i, Message: err.Error()})
			continue
		}
		c := p.String()
		if !seen["c:"+c] {
			seen["c:"+c] = true
			outCIDRs = append(outCIDRs, c)
		}
	}
	for i, s := range hosts {
		pat, wildcard, err := parseHostPattern(s)
		if err != nil {
			errs = append(errs, &EntryError{List: ListHosts, Index: i, Message: err.Error()})
			continue
		}
		h := pat
		if wildcard {
			h = "*" + pat
		}
		if !seen["h:"+h] {
			seen["h:"+h] = true
			outHosts = append(outHosts, h)
		}
	}
	return outCIDRs, outHosts, errs
}

// normalizeValue validates one entry of list (ListCIDRs or ListHosts)
// and returns its canonical form.
func (g *Guard) normalizeValue(list, value string) (string, error) {
	if list == ListCIDRs {
		p, err := parseAllowPrefix(value, g.loopbackSoft)
		if err != nil {
			return "", err
		}
		return p.String(), nil
	}
	pat, wildcard, err := parseHostPattern(value)
	if err != nil {
		return "", err
	}
	if wildcard {
		return "*" + pat, nil
	}
	return pat, nil
}

// LoopbackAllowable reports whether loopback may be allowlisted (dev mode).
func (g *Guard) LoopbackAllowable() bool { return g.loopbackSoft }
