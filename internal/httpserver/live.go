package httpserver

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/config"
)

// LiveSettings are the [server] values a runtime config reload may change
// without a restart (D-200): the trusted proxies, the body limit and the
// per-IP and per-user rate limits (rate and burst; the tracked-client
// bound stays).
type LiveSettings struct {
	TrustedProxies []netip.Prefix
	MaxBodyBytes   int64
	RatePerSecond  float64
	RateBurst      int
	UserPerSecond  float64
	UserBurst      int
}

// LiveFromConfig maps the live keys of [server].
func LiveFromConfig(c *config.Server) (LiveSettings, error) {
	proxies, err := parseProxies(c.TrustedProxies)
	if err != nil {
		return LiveSettings{}, err
	}
	return LiveSettings{
		TrustedProxies: proxies,
		MaxBodyBytes:   c.MaxBodyBytes,
		RatePerSecond:  c.RateLimitPerSecond,
		RateBurst:      c.RateLimitBurst,
		UserPerSecond:  c.UserRateLimitPerSecond,
		UserBurst:      c.UserRateLimitBurst,
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
	proxies   atomic.Pointer[[]netip.Prefix]
	bodyLimit atomic.Int64
	ip        *RateLimiter[netip.Prefix]
	users     *RateLimiter[uuid.UUID]
}

func newLive(proxies []netip.Prefix, body int64, ip *RateLimiter[netip.Prefix], users *RateLimiter[uuid.UUID]) *Live {
	l := &Live{ip: ip, users: users}
	p := slices.Clone(proxies)
	l.proxies.Store(&p)
	l.bodyLimit.Store(body)
	return l
}

func (l *Live) trustedProxies() []netip.Prefix { return *l.proxies.Load() }

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
	return func() {
		l.proxies.Store(&proxies)
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
