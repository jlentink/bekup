package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDNS answers from a map and counts the lookups.
type fakeDNS struct {
	mu    sync.Mutex
	addrs map[string][]netip.Addr
	calls int
}

func (f *fakeDNS) set(name string, addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, netip.MustParseAddr(a))
	}
	if len(out) == 0 {
		delete(f.addrs, name)
		return
	}
	f.addrs[name] = out
}

func (f *fakeDNS) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if a, ok := f.addrs[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func (f *fakeDNS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeIfaces are serve's interfaces in the tests: loopback and its
// address on the proxy network (172.20.0.2/16, fd00::2/64).
func fakeIfaces() ([]netip.Prefix, error) {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("172.20.0.2/16"), netip.MustParsePrefix("fd00::2/64"),
		netip.MustParsePrefix("172.30.0.2/16"), // e.g. the database network
	}, nil
}

func newHostEnv(t *testing.T, names ...string) (*testEnv, *fakeDNS) {
	t.Helper()
	return newHostEnvWith(t, nil, names...)
}

func newHostEnvWith(t *testing.T, mutate func(*Options), names ...string) (*testEnv, *fakeDNS) {
	t.Helper()
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	f.set(ServeAlias, "172.20.0.2", "fd00::2")
	env := newEnv(t, func(o *Options) {
		o.TrustedProxies = nil
		o.TrustedProxyHosts = names
		o.ProxyHostResolver = f.resolve
		o.InterfaceAddrs = fakeIfaces
		if mutate != nil {
			mutate(o)
		}
	})
	return env, f
}

// clientOf sends a request with X-Forwarded-For from peer and returns the
// client address bekup used.
func (e *testEnv) clientOf(peer string) string {
	r := req(http.MethodGet, "/api/x", "X-Forwarded-For", "198.51.100.9")
	r.RemoteAddr = peer
	e.do(r)
	e.rec.mu.Lock()
	defer e.rec.mu.Unlock()
	return e.rec.ip.String()
}

func TestProxyHostsTrustResolvedAddresses(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("before the first resolution the proxy must not be trusted, got %s", got)
	}
	env.srv.live.hosts.refresh(context.Background())
	if got := env.clientOf("172.20.0.5:1"); got != "198.51.100.9" {
		t.Fatalf("resolved proxy: client %s, want the forwarded address", got)
	}
	if got := env.clientOf("172.20.0.6:1"); got != "172.20.0.6" {
		t.Fatalf("another container on the network must not be trusted, got %s", got)
	}
	if !strings.Contains(env.logText(), "trusted proxy host resolved") {
		t.Fatalf("the change is logged: %s", env.logText())
	}
}

func TestProxyHostsUnresolvableTrustsNothing(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	h := env.srv.live.hosts
	h.refresh(context.Background())
	f.set("npm") // the container is gone
	h.refresh(context.Background())
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("an unresolvable name must trust nothing, got %s", got)
	}
	if !strings.Contains(env.logText(), "does not resolve") {
		t.Fatalf("missing warning: %s", env.logText())
	}
}

func TestProxyHostsOnlyPrivateAddresses(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "203.0.113.5", "127.0.0.1", "169.254.1.1", "::1", "fd00::5", "172.20.1.3")
	env.srv.live.hosts.refresh(context.Background())
	for peer, want := range map[string]string{
		"203.0.113.5:1": "203.0.113.5", "127.0.0.1:1": "127.0.0.1", "169.254.1.1:1": "169.254.1.1",
		"[fd00::5]:1": "198.51.100.9", "172.20.1.3:1": "198.51.100.9",
	} {
		if got := env.clientOf(peer); got != want {
			t.Errorf("peer %s: client %s, want %s", peer, got, want)
		}
	}
}

func TestProxyHostsReResolveOnUntrustedXFF(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	h := env.srv.live.hosts
	h.interval = time.Hour
	h.minGap = 0
	f.set("npm", "172.20.0.5")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- env.srv.ProxyHosts().Run(ctx) }()
	waitFor(t, func() bool { return env.clientOf("172.20.0.5:1") == "198.51.100.9" })
	// The proxy is recreated with a new address: its first request is
	// untrusted and triggers a resolution.
	f.set("npm", "172.20.0.9")
	before := f.count()
	if got := env.clientOf("172.20.0.9:1"); got != "172.20.0.9" {
		t.Fatalf("not yet re-resolved, got %s", got)
	}
	waitFor(t, func() bool { return f.count() > before })
	waitFor(t, func() bool { return env.clientOf("172.20.0.9:1") == "198.51.100.9" })
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("the old address must lose its trust, got %s", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProxyHostsKickRateLimited(t *testing.T) {
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	now := time.Unix(1000, 0)
	h := newProxyHosts([]string{"npm"}, proxyHostsOptions{resolve: f.resolve, ifaces: fakeIfaces}, func([]netip.Prefix, time.Time) {})
	h.now = func() time.Time { return now }
	h.interval = time.Hour
	h.minGap = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	// One refresh: the name and the alias bekup-serve.
	waitFor(t, func() bool { return f.count() == 2 })
	for range 50 {
		h.kick()
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.count(); n != 2 {
		t.Fatalf("kicks within the minimum gap must wait, got %d lookups", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProxyHostsLiveReload(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	f.set("caddy", "172.20.0.7")
	h := env.srv.live.hosts
	h.refresh(context.Background())
	s := liveSettings()
	s.TrustedProxies = nil
	s.TrustedProxyHosts = []string{"caddy"}
	if err := env.srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("a removed name loses its trust at once, got %s", got)
	}
	if got := env.clientOf("172.20.0.7:1"); got != "172.20.0.7" {
		t.Fatalf("an added name is trusted only after resolution, got %s", got)
	}
	h.refresh(context.Background())
	if got := env.clientOf("172.20.0.7:1"); got != "198.51.100.9" {
		t.Fatalf("added name after resolution: %s", got)
	}
	s.TrustedProxyHosts = []string{"bad name"}
	if err := env.srv.Live().Apply(s); err == nil {
		t.Fatal("an invalid name must be refused")
	}
	if got := env.clientOf("172.20.0.7:1"); got != "198.51.100.9" {
		t.Fatalf("a refused reload keeps the running names: %s", got)
	}
}

func TestProxyHostsRefusedAtStart(t *testing.T) {
	_, err := New(Options{
		BaseURL: "https://backup.example.com", MaxBodyBytes: 1, Logger: slog.New(slog.DiscardHandler), Ready: func(context.Context) error { return nil },
		RateLimit:         RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 100},
		TrustedProxyHosts: []string{"10.0.0.1"},
	})
	if err == nil {
		t.Fatal("an IP address is not a trusted proxy host")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A removed proxy container's address can be handed to another container
// on the network: requests from an address trusted through a name keep
// re-resolving it, so the trust ends soon after the name stopped resolving.
func TestProxyHostsAddressReuse(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	h := env.srv.live.hosts
	h.interval = time.Hour
	h.minGap = 0
	f.set("npm", "172.20.0.5")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- env.srv.ProxyHosts().Run(ctx) }()
	waitFor(t, func() bool { return env.clientOf("172.20.0.5:1") == "198.51.100.9" })
	f.set("npm") // the proxy container is gone; another one has its address
	waitFor(t, func() bool { return env.clientOf("172.20.0.5:1") == "172.20.0.5" })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
