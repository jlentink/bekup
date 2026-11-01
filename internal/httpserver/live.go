package httpserver

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/config"
)

// LiveSettings are the [server] values a runtime config reload may change
// without a restart (D-200): the trusted proxies (CIDRs and container
// names, D-215), the body limit and the per-IP and per-user rate limits
// (rate and burst; the tracked-client bound stays).
type LiveSettings struct {
	TrustedProxies []netip.Prefix
	// TrustedProxyHosts are container names resolved through Docker's
	// DNS (server.trusted_proxy_hosts).
	TrustedProxyHosts []string
	MaxBodyBytes      int64
	RatePerSecond     float64
	RateBurst         int
	UserPerSecond     float64
	UserBurst         int
}

// LiveFromConfig maps the live keys of [server].
func LiveFromConfig(c *config.Server) (LiveSettings, error) {
	proxies, err := parseProxies(c.TrustedProxies)
	if err != nil {
		return LiveSettings{}, err
	}
	return LiveSettings{
		TrustedProxies:    proxies,
		TrustedProxyHosts: slices.Clone(c.TrustedProxyHosts),
		MaxBodyBytes:      c.MaxBodyBytes,
		RatePerSecond:     c.RateLimitPerSecond,
		RateBurst:         c.RateLimitBurst,
		UserPerSecond:     c.UserRateLimitPerSecond,
		UserBurst:         c.UserRateLimitBurst,
	}, nil
}

func parseProxies(in []string) ([]netip.Prefix, error) {
	proxies := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("server.trusted_proxies: invalid CIDR %q", s)
		}
		proxies = append(proxies, p.Masked())
	}
	return proxies, nil
}

// Live holds the swappable settings of a Server. Requests read them once
// each; Prepare validates new values and returns a commit that only does
// atomic stores.
type Live struct {
	// trust is what requests read: the configured CIDRs plus the
	// addresses of the trusted proxy hosts with the time of their
	// resolution.
	trust     atomic.Pointer[trustSet]
	bodyLimit atomic.Int64
	ip        *RateLimiter[netip.Prefix]
	users     *RateLimiter[uuid.UUID]
	hosts     *proxyHosts
	now       func() time.Time

	mu        sync.Mutex
	static    []netip.Prefix
	hostAddrs []netip.Prefix
	hostAt    time.Time
}

// trustSet is one published trusted-proxy state.
type trustSet struct {
	// all are the CIDRs plus the host addresses; static the CIDRs only.
	all, static []netip.Prefix
	// hosts are the addresses trusted through names, resolved at at.
	hosts []netip.Prefix
	at    time.Time
}

// seenPeer keeps the resolution of a proxy host fresh while its address
// is in use: a peer trusted through a name asks for a re-resolution once
// the last one is older than the minimum gap (D-215: a removed proxy
// container's address can be handed to another container; the name then
// stops resolving and the address loses its trust).
func (l *Live) seenPeer(peer netip.Addr) {
	if t := l.trust.Load(); t != nil && isTrusted(peer, t.hosts) {
		l.hosts.kickIfStale()
	}
}

// liveOptions are the proxy host parts of newLive.
type liveOptions struct {
	hosts []string
	ph    proxyHostsOptions
}

func newLive(proxies []netip.Prefix, o liveOptions, body int64, ip *RateLimiter[netip.Prefix], users *RateLimiter[uuid.UUID]) *Live {
	l := &Live{ip: ip, users: users, static: slices.Clone(proxies), now: time.Now}
	l.hosts = newProxyHosts(o.hosts, o.ph, l.setHostAddrs)
	l.mu.Lock()
	l.publishLocked()
	l.mu.Unlock()
	l.bodyLimit.Store(body)
	return l
}

// trustedProxies are the peers whose forwarding headers count now: the
// addresses trusted through names only while their resolution is at most
// ProxyHostMaxAge old (D-215).
func (l *Live) trustedProxies() []netip.Prefix {
	t := l.trust.Load()
	if len(t.hosts) > 0 && l.now().Sub(t.at) > ProxyHostMaxAge {
		return t.static
	}
	return t.all
}

// setHostAddrs publishes the resolved addresses of the proxy hosts and the
// time their resolution started (called with the proxy hosts' lock held,
// so publishes keep their order).
func (l *Live) setHostAddrs(p []netip.Prefix, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hostAddrs = slices.Clone(p)
	l.hostAt = at
	l.publishLocked()
}

func (l *Live) setStatic(p []netip.Prefix) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.static = p
	l.publishLocked()
}

func (l *Live) publishLocked() {
	all := make([]netip.Prefix, 0, len(l.static)+len(l.hostAddrs))
	all = append(append(all, l.static...), l.hostAddrs...)
	l.trust.Store(&trustSet{all: all, static: slices.Clone(l.static), hosts: slices.Clone(l.hostAddrs), at: l.hostAt})
}

// MaxBodyBytes is the request body limit in force.
func (l *Live) MaxBodyBytes() int64 { return l.bodyLimit.Load() }

// Live returns the server's swappable settings.
func (s *Server) Live() *Live { return s.live }

// Prepare validates s and returns the commit that applies it. The commit
// cannot fail; nothing changes until it runs.
func (l *Live) Prepare(s LiveSettings) (func(), error) {
	if s.MaxBodyBytes <= 0 {
		return nil, errors.New("httpserver: MaxBodyBytes must be positive")
	}
	commitIP, err := l.ip.PrepareLimits(s.RatePerSecond, s.RateBurst)
	if err != nil {
		return nil, err
	}
	commitUsers := func() {}
	if l.users != nil {
		if commitUsers, err = l.users.PrepareLimits(s.UserPerSecond, s.UserBurst); err != nil {
			return nil, err
		}
	}
	proxies := slices.Clone(s.TrustedProxies)
	for i, p := range proxies {
		if !p.IsValid() {
			return nil, fmt.Errorf("httpserver: invalid trusted proxy at %d", i)
		}
		proxies[i] = p.Masked()
	}
	if err := validateProxyHosts(s.TrustedProxyHosts); err != nil {
		return nil, err
	}
	hosts := slices.Clone(s.TrustedProxyHosts)
	return func() {
		l.setStatic(proxies)
		l.hosts.setNames(hosts)
		l.bodyLimit.Store(s.MaxBodyBytes)
		commitIP()
		commitUsers()
	}, nil
}

// Apply validates and applies s at once.
func (l *Live) Apply(s LiveSettings) error {
	commit, err := l.Prepare(s)
	if err != nil {
		return err
	}
	commit()
	return nil
}
