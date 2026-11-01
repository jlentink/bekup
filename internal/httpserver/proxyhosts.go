package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// Trusted proxies by container name (server.trusted_proxy_hosts, D-215)
// and serve's own address on the proxy network.
//
// The own reverse proxy shares a Docker network with serve, where Docker
// assigns its address. serve resolves the names through Docker's embedded
// DNS and trusts exactly the returned private addresses (/32, /128) that
// lie in the subnet of serve's own interface on that network, at most one
// per address family (two answers of one family mean another container
// carries the same name or alias: the name then trusts nothing). serve
// finds its interface on the proxy network by resolving its own network
// alias ServeAlias and matching the answers with its interface addresses;
// while that fails, names trust nothing (fail closed).
//
// Everything is resolved in parallel every ProxyHostInterval and, at most
// every proxyHostMinGap, when X-Forwarded-For arrives from an untrusted
// peer (the proxy may have been recreated with a new address), while a
// peer trusted through a name sends requests (a removed proxy container's
// address may be handed to another container: the name then stops
// resolving) and when a connection is refused while serve's own address is
// unknown. Addresses trusted through a name count only while their
// resolution is at most ProxyHostMaxAge old.
//
// The same lookup guards the own-proxy compose shape (Options.OwnProxy):
// every answer for ServeAlias must be an address of this container, else
// another container took the alias (requests the proxy sends to
// http://bekup-serve:8080 could reach it): serve warns, reports it through
// Options.SelfConflict (an audited security alert) and /readyz fails. In
// the join shape (Options.JoinMode) the listener accepts connections only
// on serve's own address on the proxy network (and loopback for the
// healthcheck), so bekup's internal networks and a database network cannot
// reach the wildcard listener.

// ProxyHostResolver returns the addresses of a host name.
type ProxyHostResolver func(ctx context.Context, host string) ([]netip.Addr, error)

// InterfaceAddrs returns the addresses of this container's interfaces with
// their subnet prefix length (net.InterfaceAddrs; tests replace it).
type InterfaceAddrs func() ([]netip.Prefix, error)

// ServeAlias is serve's network alias on the proxy network of the own-proxy
// compose files (D-215): the operator's proxy forwards to
// http://bekup-serve:8080.
const ServeAlias = "bekup-serve"

// Bounds and intervals of the resolution.
const (
	// ProxyHostInterval is the regular re-resolution interval (also of
	// the ServeAlias check).
	ProxyHostInterval = 20 * time.Second
	// ProxyHostMaxAge is how long a resolution keeps its trust: a peer
	// trusted through a name is untrusted while its resolution is older.
	ProxyHostMaxAge = 30 * time.Second
	// proxyHostMinGap is the minimum time between two resolutions.
	proxyHostMinGap = 5 * time.Second
	// proxyHostTimeout bounds the resolution of one name.
	proxyHostTimeout = 5 * time.Second
	// maxProxyHostAddrs bounds the answers looked at per name.
	maxProxyHostAddrs = 16
	// refusedWarnInterval rate-limits the refused-connection warning.
	refusedWarnInterval = time.Minute
)

// Errors of the readiness check.
var (
	errAliasConflict = errors.New("httpserver: the network alias " + ServeAlias + " also resolves to another container's address (D-215)")
	errSelfUnknown   = errors.New("httpserver: serve's address on the proxy network is unknown (the alias " + ServeAlias + " does not resolve to this container, D-215)")
)

// defaultProxyHostResolver asks the process resolver (in a container:
// Docker's embedded DNS). The trailing dot makes the name absolute, so the
// host's search domains are never appended (a container name must not turn
// into a public DNS name).
func defaultProxyHostResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host+".")
}

// defaultInterfaceAddrs lists the container's interface addresses.
func defaultInterfaceAddrs() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("httpserver: interface addresses: %w", err)
	}
	out := make([]netip.Prefix, 0, len(addrs))
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ones, bits := n.Mask.Size()
		ip = canonicalIP(ip)
		if ip.Is4() && bits == 128 {
			ones -= 96
		}
		if ones >= 0 && ones <= ip.BitLen() {
			out = append(out, netip.PrefixFrom(ip, ones))
		}
	}
	return out, nil
}

// selfState is the result of the ServeAlias lookup.
type selfState struct {
	// own are serve's addresses among the answers: its addresses on the
	// proxy network.
	own []netip.Addr
	// subnets are the subnets of the interfaces that carry own.
	subnets []netip.Prefix
	// foreign are answers that are no address of this container.
	foreign []netip.Addr
}

// known reports whether serve's address on the proxy network is known.
func (s *selfState) known() bool { return s != nil && len(s.own) > 0 }

// nameResult is the trust of one name after a resolution.
type nameResult struct {
	addrs []netip.Addr
	// reason is why the name trusts nothing ("" when it trusts addrs).
	reason string
}

func (r nameResult) key() string { return r.reason + "|" + joinAddrs(r.addrs) }

// Reasons a name trusts nothing (log messages in logName).
const (
	reasonNoResolve   = "no_resolve"
	reasonNoAddress   = "no_address"
	reasonNonPrivate  = "non_private"
	reasonOutside     = "outside_proxy_network"
	reasonCollision   = "collision"
	reasonSelfUnknown = "self_unknown"
)

// proxyHosts holds the configured names, their resolved addresses and
// serve's own address on the proxy network.
type proxyHosts struct {
	resolve  ProxyHostResolver
	ifaces   InterfaceAddrs
	logger   *slog.Logger
	now      func() time.Time
	interval time.Duration
	minGap   time.Duration
	// checkSelf resolves ServeAlias even without names (own-proxy compose
	// shape); strict additionally makes an unknown own address a failed
	// readiness and makes the listener refuse every non-loopback
	// connection (join shape).
	checkSelf, strict bool
	// publish receives the trusted prefixes of all names and the time
	// their resolution started; it is called with mu held, so publishes
	// never overtake each other (D-215 review L1).
	publish func(prefixes []netip.Prefix, at time.Time)
	// onConflict reports another container's addresses behind ServeAlias
	// (an audited security alert); an error retries at the next refresh.
	onConflict func(ctx context.Context, foreign []netip.Addr) error

	// self is the latest ServeAlias result (nil: not checked).
	self atomic.Pointer[selfState]

	mu          sync.Mutex
	names       []string
	byName      map[string]nameResult
	kickCh      chan struct{}
	lastRun     time.Time
	resolvedAt  time.Time
	started     uint64
	stored      uint64
	conflictKey string
	selfKey     string

	warnMu   sync.Mutex
	lastWarn time.Time
}

// proxyHostsOptions are the optional parts of newProxyHosts.
type proxyHostsOptions struct {
	resolve    ProxyHostResolver
	ifaces     InterfaceAddrs
	logger     *slog.Logger
	checkSelf  bool
	strict     bool
	onConflict func(ctx context.Context, foreign []netip.Addr) error
}

func newProxyHosts(names []string, o proxyHostsOptions, publish func([]netip.Prefix, time.Time)) *proxyHosts {
	if o.resolve == nil {
		o.resolve = defaultProxyHostResolver
	}
	if o.ifaces == nil {
		o.ifaces = defaultInterfaceAddrs
	}
	return &proxyHosts{
		resolve: o.resolve, ifaces: o.ifaces, logger: o.logger, now: time.Now,
		interval: ProxyHostInterval, minGap: proxyHostMinGap,
		checkSelf: o.checkSelf || o.strict, strict: o.strict,
		publish: publish, onConflict: o.onConflict,
		names: normalizeHosts(names), byName: map[string]nameResult{},
		kickCh: make(chan struct{}, 1),
	}
}

// normalizeHosts lower-cases and de-duplicates the names.
func normalizeHosts(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// validateProxyHosts checks names like the config validator does.
func validateProxyHosts(names []string) error {
	if len(names) > config.MaxTrustedProxyHosts {
		return fmt.Errorf("httpserver: at most %d trusted proxy hosts", config.MaxTrustedProxyHosts)
	}
	for i, n := range names {
		if !config.ValidContainerName(strings.TrimSpace(n)) {
			return fmt.Errorf("httpserver: invalid trusted proxy host at %d", i)
		}
	}
	return nil
}

// setNames replaces the names: removed names lose their trust at once,
// new ones are trusted after the next resolution (kicked here).
func (h *proxyHosts) setNames(names []string) {
	names = normalizeHosts(names)
	h.mu.Lock()
	h.names = names
	for n := range h.byName {
		if !slices.Contains(names, n) {
			delete(h.byName, n)
		}
	}
	h.publishLocked()
	h.mu.Unlock()
	h.kick()
}

// activeLocked reports whether anything is resolved.
func (h *proxyHosts) activeLocked() bool { return h.checkSelf || len(h.names) > 0 }

// kick asks for a resolution soon (non-blocking; coalesced).
func (h *proxyHosts) kick() {
	h.mu.Lock()
	active := h.activeLocked()
	h.mu.Unlock()
	if !active {
		return
	}
	select {
	case h.kickCh <- struct{}{}:
	default:
	}
}

// kickIfStale kicks a resolution when the last one is older than minGap.
func (h *proxyHosts) kickIfStale() {
	h.mu.Lock()
	stale := h.now().Sub(h.lastRun) >= h.minGap
	h.mu.Unlock()
	if stale {
		h.kick()
	}
}

// publishLocked hands the trusted prefixes to the server (mu held).
func (h *proxyHosts) publishLocked() {
	var out []netip.Prefix
	for _, n := range h.names {
		r := h.byName[n]
		if r.reason != "" {
			continue // a collision keeps its addresses for the log only
		}
		for _, a := range r.addrs {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	h.publish(out, h.resolvedAt)
}

// usableProxyAddr accepts only addresses a container on a Docker network
// can have: private IPv4 (RFC 1918) or IPv6 ULA, never loopback,
// unspecified, link-local, multicast or public addresses (a name that
// escaped to public DNS must not be trusted).
func usableProxyAddr(a netip.Addr) bool {
	a = canonicalIP(a)
	return a.IsValid() && a.IsPrivate()
}

// canonicalAddrs canonicalizes, de-duplicates, bounds and sorts answers.
func canonicalAddrs(in []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, min(len(in), maxProxyHostAddrs))
	for _, a := range in {
		a = canonicalIP(a)
		if a.IsValid() && !slices.Contains(out, a) && len(out) < maxProxyHostAddrs {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y netip.Addr) int { return x.Compare(y) })
	return out
}

// evalSelf matches the ServeAlias answers with the interface addresses.
func (h *proxyHosts) evalSelf(answers []netip.Addr, err error) *selfState {
	st := &selfState{}
	if err != nil {
		return st
	}
	ifs, ierr := h.ifaces()
	if ierr != nil {
		// Unknown interfaces: nothing is own, nothing is foreign (fail
		// closed without a false alarm).
		return st
	}
	for _, a := range canonicalAddrs(answers) {
		own := false
		for _, p := range ifs {
			if canonicalIP(p.Addr()) == a {
				own = true
				if m := p.Masked(); !slices.Contains(st.subnets, m) {
					st.subnets = append(st.subnets, m)
				}
			}
		}
		if own {
			st.own = append(st.own, a)
		} else {
			st.foreign = append(st.foreign, a)
		}
	}
	return st
}

// evalName decides what one name's answers trust.
func evalName(answers []netip.Addr, err error, self *selfState) nameResult {
	switch {
	case err != nil:
		return nameResult{reason: reasonNoResolve}
	case len(answers) == 0:
		return nameResult{reason: reasonNoAddress}
	case !self.known():
		return nameResult{reason: reasonSelfUnknown}
	}
	var private, in []netip.Addr
	for _, a := range canonicalAddrs(answers) {
		if !usableProxyAddr(a) {
			continue
		}
		private = append(private, a)
		if isTrusted(a, self.subnets) && !slices.Contains(self.own, a) {
			in = append(in, a)
		}
	}
	switch {
	case len(private) == 0:
		return nameResult{reason: reasonNonPrivate}
	case len(in) == 0:
		return nameResult{reason: reasonOutside}
	}
	v4, v6 := 0, 0
	for _, a := range in {
		if a.Is4() {
			v4++
		} else {
			v6++
		}
	}
	if v4 > 1 || v6 > 1 {
		return nameResult{addrs: in, reason: reasonCollision}
	}
	return nameResult{addrs: in}
}

// refresh resolves every name and ServeAlias once, in parallel, and
// publishes the result.
func (h *proxyHosts) refresh(ctx context.Context) {
	h.mu.Lock()
	names := slices.Clone(h.names)
	selfOn := h.checkSelf || len(names) > 0
	start := h.now()
	h.lastRun = start
	h.started++
	gen := h.started
	h.mu.Unlock()

	type answer struct {
		addrs []netip.Addr
		err   error
	}
	answers := make([]answer, len(names))
	var self answer
	lookup := func(name string, out *answer) {
		rctx, cancel := context.WithTimeout(ctx, proxyHostTimeout)
		defer cancel()
		out.addrs, out.err = h.resolve(rctx, name)
	}
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Go(func() { lookup(n, &answers[i]) })
	}
	if selfOn {
		wg.Go(func() { lookup(ServeAlias, &self) })
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	var st *selfState
	if selfOn {
		st = h.evalSelf(self.addrs, self.err)
	}
	results := make([]nameResult, len(names))
	for i := range names {
		results[i] = evalName(answers[i].addrs, answers[i].err, st)
	}

	type change struct {
		name string
		r    nameResult
	}
	var changes []change
	h.mu.Lock()
	if gen < h.stored {
		// A newer resolution was stored meanwhile.
		h.mu.Unlock()
		return
	}
	h.stored = gen
	h.self.Store(st)
	for i, n := range names {
		if !slices.Contains(h.names, n) {
			continue // removed by a reload meanwhile
		}
		if prev, ok := h.byName[n]; !ok || prev.key() != results[i].key() {
			changes = append(changes, change{n, results[i]})
		}
		h.byName[n] = results[i]
	}
	h.resolvedAt = start
	h.publishLocked()
	selfChanged := false
	if k := selfKey(st); k != h.selfKey {
		h.selfKey, selfChanged = k, true
	}
	h.mu.Unlock()

	if selfChanged {
		h.logSelf(st)
	}
	for _, c := range changes {
		h.logName(c.name, c.r)
	}
	h.reportConflict(ctx, st)
}

func selfKey(st *selfState) string {
	if st == nil {
		return ""
	}
	return joinAddrs(st.own) + "|" + joinAddrs(st.foreign)
}

func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ",")
}

func addrStrings(addrs []netip.Addr) []string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return s
}

// logSelf logs a changed ServeAlias result (addresses only).
func (h *proxyHosts) logSelf(st *selfState) {
	if h.logger == nil || st == nil {
		return
	}
	if st.known() {
		h.logger.Info("serve's address on the proxy network", slog.String("alias", ServeAlias), slog.Any("addresses", addrStrings(st.own)))
		return
	}
	if h.strict {
		h.logger.Warn("the network alias bekup-serve does not resolve to an address of this container, so serve's address on the proxy network is unknown: trusted_proxy_hosts trust nothing and every non-loopback connection is refused (is serve on the proxy's network with the alias bekup-serve? D-215)",
			slog.String("alias", ServeAlias))
		return
	}
	h.logger.Warn("the network alias bekup-serve does not resolve to an address of this container, so serve's address on the proxy network is unknown: trusted_proxy_hosts trust nothing (is serve on the proxy's network with the alias bekup-serve? D-215)",
		slog.String("alias", ServeAlias))
}

// logName logs a name whose trust changed (names and addresses are no
// secrets; resolver errors are not logged verbatim).
func (h *proxyHosts) logName(name string, r nameResult) {
	if h.logger == nil {
		return
	}
	host := slog.String("host", name)
	switch r.reason {
	case "":
		h.logger.Info("trusted proxy host resolved", host, slog.Any("addresses", addrStrings(r.addrs)))
	case reasonNoResolve:
		h.logger.Warn("trusted proxy host does not resolve; it is not trusted (is the proxy container running on a network serve shares?)", host)
	case reasonNonPrivate:
		h.logger.Warn("trusted proxy host resolves only to non-private addresses; it is not trusted", host)
	case reasonOutside:
		h.logger.Warn("trusted proxy host resolves only to addresses outside serve's subnet on the proxy network (or to a public or CGNAT range); it is not trusted (docs/installation/reverse-proxy.md#troubleshooting)", host)
	case reasonCollision:
		h.logger.Warn("trusted proxy host resolves to more than one address of one family on the proxy network: another container carries the same name or alias; it is not trusted until only one remains", host, slog.Any("addresses", addrStrings(r.addrs)))
	case reasonSelfUnknown:
		h.logger.Warn("trusted proxy host is not trusted while serve's own address on the proxy network is unknown (the alias bekup-serve)", host)
	default:
		h.logger.Warn("trusted proxy host has no address; it is not trusted", host)
	}
}

// reportConflict warns about and reports another container behind
// ServeAlias once per set of foreign addresses (again after a failed
// report).
func (h *proxyHosts) reportConflict(ctx context.Context, st *selfState) {
	key := ""
	if st != nil && len(st.foreign) > 0 {
		key = joinAddrs(st.foreign)
	}
	h.mu.Lock()
	prev := h.conflictKey
	h.conflictKey = key
	h.mu.Unlock()
	if key == "" || key == prev {
		return
	}
	if h.logger != nil {
		h.logger.WarnContext(ctx, "the network alias bekup-serve also resolves to another container's address: requests your proxy sends to http://bekup-serve:8080 may reach that container (alias takeover, D-215); remove its alias or point your proxy at serve's container name; /readyz fails until then",
			slog.String("alias", ServeAlias), slog.Any("foreign_addresses", addrStrings(st.foreign)), slog.Any("own_addresses", addrStrings(st.own)))
	}
	if h.onConflict == nil {
		return
	}
	if err := h.onConflict(ctx, slices.Clone(st.foreign)); err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "reporting the bekup-serve alias conflict failed; retried at the next resolution", slog.Any("error", err))
		}
		h.mu.Lock()
		if h.conflictKey == key {
			h.conflictKey = ""
		}
		h.mu.Unlock()
	}
}

// readyErr is the readiness verdict of the ServeAlias check.
func (h *proxyHosts) readyErr() error {
	st := h.self.Load()
	if st != nil && len(st.foreign) > 0 {
		return errAliasConflict
	}
	if h.strict && !st.known() {
		return errSelfUnknown
	}
	return nil
}

// run resolves at start, every interval and on kicks (at most every
// minGap) until ctx ends.
func (h *proxyHosts) run(ctx context.Context) error {
	h.refresh(ctx)
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			h.refresh(ctx)
		case <-h.kickCh:
			h.mu.Lock()
			wait := h.minGap - h.now().Sub(h.lastRun)
			h.mu.Unlock()
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
			}
			h.refresh(ctx)
		}
	}
}

// --- listener filter (join shape) ------------------------------------------

// tcpAddrOf returns the canonical IP of a connection address.
func tcpAddrOf(a net.Addr) (netip.Addr, bool) {
	if t, ok := a.(*net.TCPAddr); ok {
		ip := canonicalIP(t.AddrPort().Addr())
		return ip, ip.IsValid()
	}
	if a == nil {
		return netip.Addr{}, false
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return canonicalIP(ap.Addr()), true
}

// acceptConn reports whether a connection may be served in the join
// shape: on loopback (the healthcheck inside the container), or on
// serve's own address on the proxy network from a peer in that subnet.
func (h *proxyHosts) acceptConn(local, remote net.Addr) bool {
	la, ok := tcpAddrOf(local)
	if !ok {
		return false
	}
	if la.IsLoopback() {
		return true
	}
	st := h.self.Load()
	if !st.known() {
		h.kickIfStale()
		return false
	}
	ra, ok := tcpAddrOf(remote)
	return ok && slices.Contains(st.own, la) && isTrusted(ra, st.subnets)
}

// refused logs a refused connection (at most once per
// refusedWarnInterval).
func (h *proxyHosts) refused(c net.Conn) {
	if h.logger == nil {
		return
	}
	now := h.now()
	h.warnMu.Lock()
	if !h.lastWarn.IsZero() && now.Sub(h.lastWarn) < refusedWarnInterval {
		h.warnMu.Unlock()
		return
	}
	h.lastWarn = now
	h.warnMu.Unlock()
	local, peer := slog.String("local", c.LocalAddr().String()), slog.String("peer", c.RemoteAddr().String())
	if !h.self.Load().known() {
		h.logger.Warn("serve closed a connection because its address on the proxy network is unknown (the alias bekup-serve does not resolve to this container); every non-loopback connection is refused until it does (D-215)", local, peer)
		return
	}
	h.logger.Warn("serve closed a connection that did not arrive on its address on the proxy network: in the own-proxy join shape only your proxy's network reaches serve (D-215)", local, peer)
}

// proxyNetListener closes every accepted connection acceptConn refuses,
// before a byte is read (like the runner's executor.FilterListener).
type proxyNetListener struct {
	net.Listener
	h *proxyHosts
}

// Accept returns the next permitted connection.
func (l *proxyNetListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.h.acceptConn(c.LocalAddr(), c.RemoteAddr()) {
			return c, nil
		}
		l.h.refused(c)
		// A failed close leaves nothing to do: the connection is dropped.
		discardCloseErr(c.Close())
	}
}

// discardCloseErr drops the error of closing a refused connection.
func discardCloseErr(error) {}

// ProxyHostsRunner is the component that keeps server.trusted_proxy_hosts
// and serve's own address on the proxy network resolved (internal/app runs
// it next to the HTTP server).
type ProxyHostsRunner struct{ h *proxyHosts }

// Name identifies the component in logs.
func (r ProxyHostsRunner) Name() string { return "trusted proxy hosts" }

// Run resolves until ctx ends.
func (r ProxyHostsRunner) Run(ctx context.Context) error { return r.h.run(ctx) }

// ProxyHosts returns the resolver component of the server.
func (s *Server) ProxyHosts() ProxyHostsRunner { return ProxyHostsRunner{h: s.live.hosts} }

// WrapListener returns ln filtered for the join shape (Options.JoinMode:
// only serve's own address on the proxy network and loopback are served;
// ProxyHosts must run) and ln itself otherwise.
func (s *Server) WrapListener(ln net.Listener) net.Listener {
	if !s.opts.JoinMode {
		return ln
	}
	return &proxyNetListener{Listener: ln, h: s.live.hosts}
}
