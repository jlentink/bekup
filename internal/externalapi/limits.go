package externalapi

import (
	"errors"
	"net/netip"
	"time"

	"github.com/jlentink/bekup/internal/httpserver"
)

// FailedAuthOptions configure the per-IP throttle of failed
// authentications (D-220 item 8: 10 per 5 minutes by default).
type FailedAuthOptions struct {
	// Limit failures per Window and client IP (IPv6 per /64).
	Limit  int
	Window time.Duration
	// MaxClients bounds the tracked IPs (LRU).
	MaxClients int
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// failedAuth throttles failed authentications per client IP. It is a token
// bucket of Limit tokens refilled at Limit per Window: a request takes a
// token before its token is checked (refused with 429 when none is left)
// and gives it back when the check succeeds, so only failures count and
// concurrent guesses cannot overtake the limit.
type failedAuth struct {
	l *httpserver.RateLimiter[netip.Prefix]
}

func newFailedAuth(o FailedAuthOptions) (*failedAuth, error) {
	if o.Limit < 1 || o.Window <= 0 {
		return nil, errors.New("externalapi: the failed authentication throttle needs Limit >= 1 and Window > 0")
	}
	l, err := httpserver.NewRateLimiter[netip.Prefix](httpserver.RateLimitOptions{
		PerSecond: float64(o.Limit) / o.Window.Seconds(), Burst: o.Limit, MaxClients: o.MaxClients, Now: o.Now,
	})
	if err != nil {
		return nil, err
	}
	return &failedAuth{l: l}, nil
}

// reserve takes a token for ip; false with the wait when ip is throttled.
func (f *failedAuth) reserve(ip netip.Addr) (bool, time.Duration) {
	return f.l.Allow(httpserver.IPKey(ip))
}

// release gives the token of an attempt that did not fail back.
func (f *failedAuth) release(ip netip.Addr) {
	f.l.Refund(httpserver.IPKey(ip), 1)
}
