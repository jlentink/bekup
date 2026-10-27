package targets

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/netguard"
)

// DefaultDialTimeout bounds one DialContext call (DNS plus all attempts).
const DefaultDialTimeout = 10 * time.Second

// maxAddrs caps the resolved addresses one host may have; more is refused
// rather than silently truncated, so validation and dial see the same set.
const maxAddrs = 16

// maxEntries bounds each configured list.
const maxEntries = 1024

// Errors. Callers compare with errors.Is. Messages are safe to return to
// serve (no addresses, no request data).
var (
	// ErrDenied: the address or host is not allowed (*DeniedError).
	ErrDenied = errors.New("target not allowed by the runner's target allowlist")
	// ErrResolve: the host name could not be resolved.
	ErrResolve = errors.New("target host could not be resolved")
	// ErrInvalidHost: the host is not an IP literal or a valid host name.
	ErrInvalidHost = errors.New("invalid target host")
	// ErrUnsupportedNetwork: only tcp, tcp4 and tcp6 can be dialed.
	ErrUnsupportedNetwork = errors.New("unsupported network")
)

// Reasons a target is denied (for logs and admins; stable strings).
const (
	ReasonNotAllowed = "not in the target allowlist"
	ReasonOwnNetwork = "runner network"
	ReasonDenyCIDR   = "deny_cidrs"
	ReasonDenyHost   = "deny_hosts"
	// ReasonDockerNetwork: a Docker network's subnet or gateway that no
	// allow_cidrs entry inside it (or, for a gateway, its exact /32 or
	// /128) allows; allow_hosts never opens one.
	ReasonDockerNetwork = "docker network"
	// ReasonBastionTarget: not in the bastion's bastion_targets list.
	ReasonBastionTarget = "not in the bastion's target list"
)

// DeniedError says why an address was denied. Its Error() contains the
// address and is meant for the runner's own logs; the gRPC layer returns
// only ErrDenied's message.
type DeniedError struct {
	IP     netip.Addr
	Reason string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("target address %s denied: %s", e.IP, e.Reason)
}

// Unwrap makes errors.Is(err, ErrDenied) true.
func (e *DeniedError) Unwrap() error { return ErrDenied }

// LookupFunc resolves host; network is "ip", "ip4" or "ip6".
type LookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

// Options configures a Guard. The lists use the runner.toml syntax
// ([targets]); config.Load has already validated them, New re-validates.
type Options struct {
	AllowCIDRs []string
	AllowHosts []string
	DenyCIDRs  []string
	DenyHosts  []string
	// BastionTargets are "<bastion>=<CIDR or host name>" entries: a
	// bastion listed here may only forward to the listed targets.
	BastionTargets []string
	// OwnNetworks are the runner's own networks (always denied); they can
	// be replaced later with SetOwnNetworks.
	OwnNetworks []netip.Prefix
	// DialTimeout bounds one DialContext call; 0 = DefaultDialTimeout.
	DialTimeout time.Duration
	// Lookup replaces the system resolver (tests); nil = the system's.
	Lookup LookupFunc
}

// Guard enforces the target allowlist. It is safe for concurrent use.
type Guard struct {
	allowCIDRs  []netip.Prefix
	denyCIDRs   []netip.Prefix
	allowHosts  map[string]struct{}
	denyHosts   map[string]struct{}
	own         atomic.Pointer[[]netip.Prefix]
	docker      atomic.Pointer[dockerNets]
	bastions    map[string]*bastionRule
	dialTimeout time.Duration
	lookup      LookupFunc
}

// New validates the options and builds a Guard.
func New(opts Options) (*Guard, error) {
	var errs []error
	g := &Guard{
		allowHosts:  map[string]struct{}{},
		denyHosts:   map[string]struct{}{},
		dialTimeout: opts.DialTimeout,
		lookup:      opts.Lookup,
	}
	for _, l := range [][]string{opts.AllowCIDRs, opts.AllowHosts, opts.DenyCIDRs, opts.DenyHosts} {
		if len(l) > maxEntries {
			return nil, fmt.Errorf("targets: more than %d entries in a list", maxEntries)
		}
	}
	for _, s := range opts.AllowCIDRs {
		p, err := parsePrefix(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("allow_cidrs: %w", err))
			continue
		}
		if err := checkAllowPrefix(p); err != nil {
			errs = append(errs, fmt.Errorf("allow_cidrs: %w", err))
			continue
		}
		g.allowCIDRs = append(g.allowCIDRs, p)
	}
	for _, s := range opts.DenyCIDRs {
		p, err := parsePrefix(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("deny_cidrs: %w", err))
			continue
		}
		g.denyCIDRs = append(g.denyCIDRs, p)
	}
	for _, s := range opts.AllowHosts {
		h, err := parseHost(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("allow_hosts: %w", err))
			continue
		}
		g.allowHosts[h] = struct{}{}
	}
	for _, s := range opts.DenyHosts {
		h, err := parseHost(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("deny_hosts: %w", err))
			continue
		}
		g.denyHosts[h] = struct{}{}
	}
	if len(opts.BastionTargets) > maxEntries {
		return nil, fmt.Errorf("targets: more than %d entries in a list", maxEntries)
	}
	g.bastions = map[string]*bastionRule{}
	for _, s := range opts.BastionTargets {
		if err := g.addBastionTarget(s); err != nil {
			errs = append(errs, fmt.Errorf("bastion_targets: %w", err))
		}
	}
	g.docker.Store(&dockerNets{})
	if err := g.SetOwnNetworks(opts.OwnNetworks); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("targets: invalid allowlist: %w", errors.Join(errs...))
	}
	if g.dialTimeout <= 0 {
		g.dialTimeout = DefaultDialTimeout
	}
	if g.lookup == nil {
		g.lookup = net.DefaultResolver.LookupNetIP
	}
	return g, nil
}

// FromConfig builds a Guard from runner.toml [targets].
func FromConfig(c *config.Runner) (*Guard, error) {
	return New(Options{
		AllowCIDRs: c.Targets.AllowCIDRs,
		AllowHosts: c.Targets.AllowHosts,
		DenyCIDRs:  c.Targets.DenyCIDRs,
		DenyHosts:  c.Targets.DenyHosts,

		BastionTargets: c.Targets.BastionTargets,
	})
}

// Empty reports whether nothing is allowed (both allow lists empty).
func (g *Guard) Empty() bool { return len(g.allowCIDRs) == 0 && len(g.allowHosts) == 0 }

// Warnings returns the startup warnings of the allowlist.
func (g *Guard) Warnings() []string {
	if g.Empty() {
		return []string{"targets: allow_cidrs and allow_hosts are empty, so every target database and SSH bastion is denied; configure [targets] in runner.toml"}
	}
	if len(g.bastions) == 0 {
		return []string{WarningNoBastionTargets}
	}
	return nil
}

// WarningNoBastionTargets is the startup warning of a runner without
// bastion_targets (D-138).
const WarningNoBastionTargets = "targets: bastion_targets is empty, so an allowed SSH bastion may forward to any host it reaches (only deny_cidrs and deny_hosts apply behind it); list each bastion's databases in bastion_targets"

// SetOwnNetworks replaces the runner's own networks (always denied). The
// executor (task 2.6) calls it with the control network and adds every
// per-run network's subnet before the run starts.
func (g *Guard) SetOwnNetworks(prefixes []netip.Prefix) error {
	own := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if !p.IsValid() {
			return errors.New("targets: invalid own network prefix")
		}
		own = append(own, unmapPrefix(p.Masked()))
	}
	g.own.Store(&own)
	return nil
}

// OwnNetworks returns a copy of the runner's own networks.
func (g *Guard) OwnNetworks() []netip.Prefix { return slices.Clone(*g.own.Load()) }

// Check returns nil when a connection to addr is allowed. host is the name
// the caller asked for ("" for an IP literal); it only matters for the host
// lists. A denial is a *DeniedError.
func (g *Guard) Check(addr netip.Addr, host string) error {
	if !addr.IsValid() {
		return &DeniedError{IP: addr, Reason: string(netguard.ReasonInvalid)}
	}
	ip := addr.WithZone("").Unmap()
	if c := netguard.Classify(ip); c.Blocked && c.Hard {
		return &DeniedError{IP: ip, Reason: "always denied: " + string(c.Reason)}
	}
	for _, p := range *g.own.Load() {
		if p.Contains(ip) {
			return &DeniedError{IP: ip, Reason: ReasonOwnNetwork}
		}
	}
	if g.dockerDenied(ip) {
		return &DeniedError{IP: ip, Reason: ReasonDockerNetwork}
	}
	h := normalizeHost(host)
	if h != "" {
		if _, ok := g.denyHosts[h]; ok {
			return &DeniedError{IP: ip, Reason: ReasonDenyHost}
		}
	}
	for _, p := range g.denyCIDRs {
		if p.Contains(ip) {
			return &DeniedError{IP: ip, Reason: ReasonDenyCIDR}
		}
	}
	for _, p := range g.allowCIDRs {
		if p.Contains(ip) {
			return nil
		}
	}
	if h != "" {
		if _, ok := g.allowHosts[h]; ok {
			return nil
		}
	}
	return &DeniedError{IP: ip, Reason: ReasonNotAllowed}
}

// CheckDenyLists applies only deny_hosts (host != "") and deny_cidrs (a
// valid addr). It is for hosts the runner never dials itself, such as a
// database behind an SSH bastion (the bastion is checked with Resolve).
func (g *Guard) CheckDenyLists(addr netip.Addr, host string) error {
	if h := normalizeHost(host); h != "" {
		if _, ok := g.denyHosts[h]; ok {
			return &DeniedError{IP: addr, Reason: ReasonDenyHost}
		}
	}
	if addr.IsValid() {
		ip := addr.WithZone("").Unmap()
		for _, p := range g.denyCIDRs {
			if p.Contains(ip) {
				return &DeniedError{IP: ip, Reason: ReasonDenyCIDR}
			}
		}
	}
	return nil
}

// Resolve resolves host (an IP literal or a host name) and returns its
// addresses when every one of them is allowed. Use it when a request is
// validated; DialContext checks again at dial time.
func (g *Guard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, g.dialTimeout)
	defer cancel()
	addrs, name, err := g.resolve(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if err := g.Check(a, name); err != nil {
			return nil, err
		}
	}
	return addrs, nil
}

// DialContext resolves address ("host:port") and connects to the first
// allowed address. Every socket is checked again in the dialer's Control
// hook on the exact address it connects to. Only tcp, tcp4 and tcp6 are
// supported. The signature fits tunnel.Dialer and net.Dialer users.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ipNet, ok := lookupNetwork(network)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedNetwork, network)
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed address", ErrInvalidHost)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("%w: invalid port", ErrInvalidHost)
	}
	ctx, cancel := context.WithTimeout(ctx, g.dialTimeout)
	defer cancel()
	addrs, name, err := g.resolve(ctx, ipNet, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Control: g.control(name)}
	var firstErr error
	for _, a := range addrs {
		if err := g.Check(a, name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		conn, err := d.DialContext(ctx, network, netip.AddrPortFrom(a.WithZone("").Unmap(), uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		if firstErr == nil || (errors.Is(firstErr, ErrDenied) && !errors.Is(err, ErrDenied)) {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// control returns the dialer's Control hook: the authoritative check on
// the exact socket address.
func (g *Guard) control(name string) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return &DeniedError{Reason: "unparsable socket address"}
		}
		return g.Check(ap.Addr(), name)
	}
}

// resolve returns the addresses of host and its normalized name ("" for
// an IP literal).
func (g *Guard) resolve(ctx context.Context, ipNet, host string) ([]netip.Addr, string, error) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return nil, "", fmt.Errorf("%w: zones are not allowed", ErrInvalidHost)
		}
		if (ipNet == "ip4" && !a.Unmap().Is4()) || (ipNet == "ip6" && a.Is4()) {
			return nil, "", ErrResolve
		}
		return []netip.Addr{a}, "", nil
	}
	name := normalizeHost(host)
	if !validHostName(name) {
		return nil, "", ErrInvalidHost
	}
	// A denied name is refused before any DNS query is made.
	if _, ok := g.denyHosts[name]; ok {
		return nil, "", &DeniedError{Reason: ReasonDenyHost}
	}
	addrs, err := g.lookup(ctx, ipNet, name)
	if err != nil || len(addrs) == 0 {
		return nil, "", ErrResolve
	}
	if len(addrs) > maxAddrs {
		return nil, "", fmt.Errorf("%w: more than %d addresses", ErrResolve, maxAddrs)
	}
	return addrs, name, nil
}

func lookupNetwork(network string) (string, bool) {
	switch network {
	case "tcp":
		return "ip", true
	case "tcp4":
		return "ip4", true
	case "tcp6":
		return "ip6", true
	default:
		return "", false
	}
}

// ipv4Mapped is ::ffff:0:0/96.
var ipv4Mapped = netip.MustParsePrefix("::ffff:0:0/96")

// parsePrefix parses a CIDR and unmaps IPv4-mapped IPv6 prefixes.
func parsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q", s)
	}
	if p.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q (zone)", s)
	}
	p = p.Masked()
	if p.Addr().Is6() && p.Bits() < ipv4Mapped.Bits() && p.Overlaps(ipv4Mapped) {
		return netip.Prefix{}, fmt.Errorf("CIDR %q covers the whole IPv4-mapped range", s)
	}
	return unmapPrefix(p), nil
}

// unmapPrefix turns ::ffff:a.b.c.d/n (n >= 96) into a.b.c.d/(n-96).
func unmapPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= ipv4Mapped.Bits() {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-ipv4Mapped.Bits()).Masked()
	}
	return p
}

// alwaysDeniedPrefixes are the ranges an allow prefix must not overlap:
// the same set the config check uses (D-098), so every loadable runner.toml
// builds. The full always-denied set is enforced per address by
// netguard.Classify in Check.
var alwaysDeniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

func checkAllowPrefix(p netip.Prefix) error {
	if p.Bits() == 0 {
		return fmt.Errorf("%s allows everything", p)
	}
	for _, d := range alwaysDeniedPrefixes {
		if p.Overlaps(d) {
			return fmt.Errorf("%s overlaps the always-denied range %s", p, d)
		}
	}
	return nil
}

// normalizeHost lowercases a host name and strips one trailing dot.
func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func validHostName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for l := range strings.SplitSeq(h, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := range len(l) {
			c := l[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return false
			}
		}
	}
	// All-numeric names are ambiguous with IPv4 literals.
	return strings.Trim(h, "0123456789.") != ""
}

func parseHost(s string) (string, error) {
	h := normalizeHost(s)
	if _, err := netip.ParseAddr(h); err == nil {
		return "", fmt.Errorf("%q is an IP address; use a CIDR", s)
	}
	if !validHostName(h) {
		return "", fmt.Errorf("invalid host name %q", s)
	}
	return h, nil
}

// dockerNets are the subnets and gateways of every Docker network on the
// daemon (the executor refreshes them).
type dockerNets struct {
	subnets  []netip.Prefix
	gateways []netip.Addr
}

// SetDockerNetworks replaces the Docker networks' subnets and gateways
// (D-138). An address inside one is denied unless an allow_cidrs entry
// that lies inside that subnet contains it; a gateway (the host's side of
// the bridge) needs an allow_cidrs entry of exactly its address (/32,
// /128). allow_hosts never allows such an address, and broad entries like
// 172.16.0.0/12 do not open Docker networks.
func (g *Guard) SetDockerNetworks(subnets []netip.Prefix, gateways []netip.Addr) {
	d := &dockerNets{}
	for _, p := range subnets {
		if p.IsValid() {
			d.subnets = append(d.subnets, unmapPrefix(p.Masked()))
		}
	}
	for _, a := range gateways {
		if a.IsValid() {
			d.gateways = append(d.gateways, a.WithZone("").Unmap())
		}
	}
	g.docker.Store(d)
}

// dockerDenied reports whether ip is a Docker subnet address or gateway
// without an exact allow_cidrs entry.
func (g *Guard) dockerDenied(ip netip.Addr) bool {
	d := g.docker.Load()
	for _, gw := range d.gateways {
		if gw != ip {
			continue
		}
		if !slices.Contains(g.allowCIDRs, netip.PrefixFrom(ip, ip.BitLen())) {
			return true
		}
	}
	for _, s := range d.subnets {
		if !s.Contains(ip) {
			continue
		}
		ok := false
		for _, p := range g.allowCIDRs {
			if p.Contains(ip) && p.Bits() >= s.Bits() && s.Contains(p.Addr()) {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
	}
	return false
}

// bastionRule is the target list of one bastion (bastion_targets).
type bastionRule struct {
	cidrs []netip.Prefix
	hosts map[string]struct{}
}

// addBastionTarget parses "<bastion>=<CIDR or host name>". The bastion
// is a host name or an IP literal; the target is a CIDR (loopback allowed:
// a database on the bastion's own loopback) or a host name the bastion
// resolves.
func (g *Guard) addBastionTarget(entry string) error {
	b, t, ok := strings.Cut(strings.TrimSpace(entry), "=")
	if !ok {
		return fmt.Errorf("%q: want <bastion>=<CIDR or host name>", entry)
	}
	key, err := bastionKey(b)
	if err != nil {
		return fmt.Errorf("%q: bastion: %w", entry, err)
	}
	r := g.bastions[key]
	if r == nil {
		r = &bastionRule{hosts: map[string]struct{}{}}
		g.bastions[key] = r
	}
	t = strings.TrimSpace(t)
	if strings.Contains(t, "/") {
		p, err := parsePrefix(t)
		if err != nil {
			return fmt.Errorf("%q: %w", entry, err)
		}
		if p.Bits() == 0 {
			return fmt.Errorf("%q: %s allows everything", entry, p)
		}
		r.cidrs = append(r.cidrs, p)
		return nil
	}
	h, err := parseHost(t)
	if err != nil {
		return fmt.Errorf("%q: %w", entry, err)
	}
	r.hosts[h] = struct{}{}
	return nil
}

// bastionKey normalizes a bastion host name or IP literal.
func bastionKey(s string) (string, error) {
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "["), "]")
	if a, err := netip.ParseAddr(s); err == nil {
		if a.Zone() != "" {
			return "", errors.New("zones are not allowed")
		}
		return a.Unmap().String(), nil
	}
	h := normalizeHost(s)
	if !validHostName(h) {
		return "", fmt.Errorf("invalid host name %q", s)
	}
	return h, nil
}

// HasBastionTargets reports whether bastion (its name or one of its
// resolved addresses) has a bastion_targets list.
func (g *Guard) HasBastionTargets(bastion string, addrs []netip.Addr) bool {
	return len(g.bastionRules(bastion, addrs)) > 0
}

func (g *Guard) bastionRules(bastion string, addrs []netip.Addr) []*bastionRule {
	var out []*bastionRule
	if key, err := bastionKey(bastion); err == nil {
		if r := g.bastions[key]; r != nil {
			out = append(out, r)
		}
	}
	for _, a := range addrs {
		if r := g.bastions[a.WithZone("").Unmap().String()]; r != nil && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// CheckBehindBastion applies the bastion's bastion_targets list to the
// target host the bastion will resolve (D-138): an IP literal must be in
// one of its CIDRs, a host name must be listed exactly. A bastion is
// matched by its name and by each of its resolved addresses; when several
// entries match, every one of them must allow the target. A bastion
// without a list is not restricted here (deny lists still apply).
func (g *Guard) CheckBehindBastion(bastion string, addrs []netip.Addr, target string) error {
	rules := g.bastionRules(bastion, addrs)
	if len(rules) == 0 {
		return nil
	}
	t := strings.TrimSuffix(strings.TrimPrefix(target, "["), "]")
	ip, err := netip.ParseAddr(t)
	isIP := err == nil
	if isIP {
		ip = ip.WithZone("").Unmap()
	}
	name := normalizeHost(t)
	for _, r := range rules {
		ok := false
		if isIP {
			for _, p := range r.cidrs {
				if p.Contains(ip) {
					ok = true
					break
				}
			}
		} else if _, found := r.hosts[name]; found {
			ok = true
		}
		if !ok {
			return &DeniedError{IP: ip, Reason: ReasonBastionTarget}
		}
	}
	return nil
}
