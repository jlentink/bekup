package netguard

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// DefaultDialTimeout bounds one DialContext call (DNS plus all attempts).
const DefaultDialTimeout = 10 * time.Second

// maxAddrs caps how many resolved addresses one dial tries.
const maxAddrs = 8

// Options configures a Guard.
type Options struct {
	// AllowCIDRs and AllowHosts are the bootstrap allowlist from the TOML
	// ([netguard]); they always apply in addition to the runtime list.
	AllowCIDRs []string
	AllowHosts []string
	// LoopbackAllowable makes loopback soft-denied (allowlistable). Only
	// set it in dev mode.
	LoopbackAllowable bool
	// DialTimeout bounds one DialContext call; 0 means DefaultDialTimeout.
	DialTimeout time.Duration
}

// lookupFunc resolves host to addresses; network is "ip", "ip4" or "ip6".
type lookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

// Guard checks and dials outbound connections. It is safe for concurrent
// use; SetAllowlist swaps the runtime allowlist atomically, and a config
// reload swaps the bootstrap list atomically (Set.PrepareBootstrap, D-200).
type Guard struct {
	loopbackSoft bool
	dialTimeout  time.Duration
	bootstrap    atomic.Pointer[allowlist]
	runtime      atomic.Pointer[allowlist]
	lookup       lookupFunc
}

// New validates the bootstrap allowlist and returns a Guard.
func New(opts Options) (*Guard, error) {
	boot, err := buildAllowlist(opts.AllowCIDRs, opts.AllowHosts, opts.LoopbackAllowable)
	if err != nil {
		return nil, err
	}
	g := &Guard{
		loopbackSoft: opts.LoopbackAllowable,
		dialTimeout:  opts.DialTimeout,
		lookup:       net.DefaultResolver.LookupNetIP,
	}
	if g.dialTimeout <= 0 {
		g.dialTimeout = DefaultDialTimeout
	}
	g.bootstrap.Store(boot)
	g.runtime.Store(emptyAllowlist)
	return g, nil
}

// FromConfig builds a Guard from [netguard]; loopback is allowlistable
// only in dev mode.
func FromConfig(c *config.Bekup) (*Guard, error) {
	return New(Options{
		AllowCIDRs:        c.Netguard.AllowCIDRs,
		AllowHosts:        c.Netguard.AllowHosts,
		LoopbackAllowable: c.Dev.Enabled,
	})
}

// ValidateAllowlist reports whether cidrs and hosts would be accepted by
// SetAllowlist, without changing anything.
func (g *Guard) ValidateAllowlist(cidrs, hosts []string) error {
	_, err := buildAllowlist(cidrs, hosts, g.loopbackSoft)
	return err
}

// SetAllowlist validates and atomically replaces the runtime (admin)
// allowlist. On error the current list stays in place.
func (g *Guard) SetAllowlist(cidrs, hosts []string) error {
	a, err := buildAllowlist(cidrs, hosts, g.loopbackSoft)
	if err != nil {
		return err
	}
	g.runtime.Store(a)
	return nil
}

// Check returns nil when a connection to addr is allowed. host is the
// name the caller asked for ("" for an IP literal); it is only used to
// match host allowlist entries for soft-denied addresses. A denial is a
// *BlockedAddressError.
func (g *Guard) Check(addr netip.Addr, host string) error {
	c := classify(addr, g.loopbackSoft)
	if !c.Blocked {
		return nil
	}
	ip := addr.WithZone("").Unmap()
	if !c.Hard {
		h := normalizeHost(host)
		rt, boot := g.runtime.Load(), g.bootstrap.Load()
		if boot.containsAddr(ip) || rt.containsAddr(ip) ||
			boot.matchesHost(h) || rt.matchesHost(h) {
			return nil
		}
	}
	return &BlockedAddressError{IP: ip, Reason: c.Reason}
}
