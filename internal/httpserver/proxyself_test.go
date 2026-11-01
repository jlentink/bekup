package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// D-215 review: trust limited to serve's subnet on the proxy network,
// alias collisions, the bekup-serve self check, staleness, ordered
// publishes, parallel lookups and the join-shape listener filter.

func TestProxyHostsOnlyInsideProxySubnet(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	// 172.30.0.9 is on another network serve is on (e.g. the database
	// network): a container there named npm must not be trusted.
	f.set("npm", "172.30.0.9")
	env.srv.live.hosts.refresh(context.Background())
	if got := env.clientOf("172.30.0.9:1"); got != "172.30.0.9" {
		t.Fatalf("an address outside the proxy subnet must not be trusted, got %s", got)
	}
	if !strings.Contains(env.logText(), "outside serve's subnet") {
		t.Fatalf("missing warning: %s", env.logText())
	}
	// serve's own address is never a proxy.
	f.set("npm", "172.20.0.2")
	env.srv.live.hosts.refresh(context.Background())
	if got := env.clientOf("172.20.0.2:1"); got != "172.20.0.2" {
		t.Fatalf("serve's own address must not be trusted, got %s", got)
	}
}

func TestProxyHostsCollisionTrustsNothing(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5", "172.20.0.6")
	env.srv.live.hosts.refresh(context.Background())
	for _, peer := range []string{"172.20.0.5:1", "172.20.0.6:1"} {
		if got := env.clientOf(peer); got == "198.51.100.9" {
			t.Fatalf("two addresses of one family must trust nothing (%s)", peer)
		}
	}
	if !strings.Contains(env.logText(), "more than one address of one family") {
		t.Fatalf("missing collision warning: %s", env.logText())
	}
	// One per family is a dual-stack proxy.
	f.set("npm", "172.20.0.5", "fd00::5")
	env.srv.live.hosts.refresh(context.Background())
	if got := env.clientOf("[fd00::5]:1"); got != "198.51.100.9" {
		t.Fatalf("dual-stack proxy: %s", got)
	}
	if got := env.clientOf("172.20.0.5:1"); got != "198.51.100.9" {
		t.Fatalf("dual-stack proxy: %s", got)
	}
}

func TestProxyHostsSelfUnknownTrustsNothing(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	f.set(ServeAlias) // serve lacks its alias on the proxy network
	env.srv.live.hosts.refresh(context.Background())
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("without serve's own address names trust nothing, got %s", got)
	}
	if !strings.Contains(env.logText(), "does not resolve to an address of this container") {
		t.Fatalf("missing warning: %s", env.logText())
	}
	// Outside the join shape an unknown own address does not fail
	// readiness (nothing is listened on in a special way).
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusOK {
		t.Fatalf("readyz: %d", w.Code)
	}
}

func TestProxyHostsStaleResolutionUntrusted(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	h := env.srv.live.hosts
	base := time.Now()
	h.now = func() time.Time { return base }
	h.refresh(context.Background())
	var now atomic.Pointer[time.Time]
	now.Store(&base)
	env.srv.live.now = func() time.Time { return *now.Load() }
	if got := env.clientOf("172.20.0.5:1"); got != "198.51.100.9" {
		t.Fatalf("fresh: %s", got)
	}
	later := base.Add(ProxyHostMaxAge + time.Second)
	now.Store(&later)
	h.now = func() time.Time { return later }
	// Drain a pending kick, then expect the stale peer to kick again.
	select {
	case <-h.kickCh:
	default:
	}
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("a resolution older than %s must not be trusted, got %s", ProxyHostMaxAge, got)
	}
	select {
	case <-h.kickCh:
	default:
		t.Fatal("a stale name-trusted peer must kick a re-resolution")
	}
	h.refresh(context.Background())
	if got := env.clientOf("172.20.0.5:1"); got != "198.51.100.9" {
		t.Fatalf("after re-resolution: %s", got)
	}
}

// blockingDNS blocks the first lookup of block until release is closed.
type blockingDNS struct {
	*fakeDNS
	block   string
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *blockingDNS) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if host == b.block {
		first := false
		b.once.Do(func() { first = true })
		if first {
			// Answer with the address valid when the lookup began.
			addrs, err := b.fakeDNS.resolve(ctx, host)
			close(b.entered)
			<-b.release
			return addrs, err
		}
	}
	return b.fakeDNS.resolve(ctx, host)
}

// An older resolution that finishes after a newer one must not overwrite
// it (review L1).
func TestProxyHostsStalePublishDropped(t *testing.T) {
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	f.set(ServeAlias, "172.20.0.2")
	f.set("npm", "172.20.0.5")
	b := &blockingDNS{fakeDNS: f, block: "npm", entered: make(chan struct{}), release: make(chan struct{})}
	env := newEnvForDNS(t, b.resolve, "npm")
	h := env.srv.live.hosts
	done := make(chan struct{})
	go func() { h.refresh(context.Background()); close(done) }()
	<-b.entered
	f.set("npm", "172.20.0.9") // the proxy was recreated
	h.refresh(context.Background())
	close(b.release)
	<-done
	if got := env.clientOf("172.20.0.9:1"); got != "198.51.100.9" {
		t.Fatalf("the newer resolution must stay: %s", got)
	}
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("the older resolution must be dropped: %s", got)
	}
}

func newEnvForDNS(t *testing.T, resolve ProxyHostResolver, names ...string) *testEnv {
	t.Helper()
	return newEnv(t, func(o *Options) {
		o.TrustedProxies = nil
		o.TrustedProxyHosts = names
		o.ProxyHostResolver = resolve
		o.InterfaceAddrs = fakeIfaces
	})
}

// Reloads racing with resolutions keep the published state consistent
// (run with -race).
func TestProxyHostsReloadRace(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	f.set("caddy", "172.20.0.7")
	h := env.srv.live.hosts
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { h.refresh(context.Background()) })
		wg.Go(func() {
			s := liveSettings()
			s.TrustedProxies = nil
			s.TrustedProxyHosts = []string{[]string{"npm", "caddy"}[i%2]}
			if err := env.srv.Live().Apply(s); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	s := liveSettings()
	s.TrustedProxies = nil
	s.TrustedProxyHosts = []string{"caddy"}
	if err := env.srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	h.refresh(context.Background())
	if got := env.clientOf("172.20.0.5:1"); got != "172.20.0.5" {
		t.Fatalf("removed name still trusted: %s", got)
	}
	if got := env.clientOf("172.20.0.7:1"); got != "198.51.100.9" {
		t.Fatalf("current name not trusted: %s", got)
	}
}

// The names and the alias are looked up in parallel (review L2).
func TestProxyHostsParallelLookups(t *testing.T) {
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	f.set(ServeAlias, "172.20.0.2")
	names := []string{"a1", "a2", "a3"}
	const all int32 = 4 // the names and the alias
	for i, n := range names {
		f.set(n, netip.AddrFrom4([4]byte{172, 20, 1, byte(i + 1)}).String())
	}
	var inFlight, peak atomic.Int32
	barrier := make(chan struct{})
	var once sync.Once
	resolve := func(ctx context.Context, host string) ([]netip.Addr, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if n == all {
			once.Do(func() { close(barrier) })
		}
		select {
		case <-barrier:
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		return f.resolve(ctx, host)
	}
	env := newEnvForDNS(t, resolve, names...)
	env.srv.live.hosts.refresh(context.Background())
	if p := peak.Load(); p != all {
		t.Fatalf("lookups were not parallel: peak %d", p)
	}
	if got := env.clientOf("172.20.1.2:1"); got != "198.51.100.9" {
		t.Fatalf("a2: %s", got)
	}
}

func TestServeAliasConflictReported(t *testing.T) {
	var mu sync.Mutex
	var reports [][]netip.Addr
	fail := true
	env, f := newHostEnvWith(t, func(o *Options) {
		o.OwnProxy = true
		o.SelfConflict = func(_ context.Context, foreign []netip.Addr) error {
			mu.Lock()
			defer mu.Unlock()
			reports = append(reports, foreign)
			if fail {
				fail = false
				return errors.New("db down")
			}
			return nil
		}
	})
	h := env.srv.live.hosts
	h.refresh(context.Background())
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusOK {
		t.Fatalf("no conflict: readyz %d", w.Code)
	}
	f.set(ServeAlias, "172.20.0.2", "fd00::2", "172.20.0.66")
	h.refresh(context.Background()) // reported, fails
	h.refresh(context.Background()) // retried, succeeds
	h.refresh(context.Background()) // unchanged: not again
	mu.Lock()
	n := len(reports)
	got := reports[len(reports)-1]
	mu.Unlock()
	if n != 2 || !slices.Equal(got, []netip.Addr{netip.MustParseAddr("172.20.0.66")}) {
		t.Fatalf("reports: %d, last %v", n, got)
	}
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("conflict: readyz %d", w.Code)
	}
	if !strings.Contains(env.logText(), "alias takeover") {
		t.Fatalf("missing warning: %s", env.logText())
	}
	f.set(ServeAlias, "172.20.0.2", "fd00::2")
	h.refresh(context.Background())
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusOK {
		t.Fatalf("conflict gone: readyz %d", w.Code)
	}
	// A new conflict is reported again.
	f.set(ServeAlias, "172.20.0.2", "172.20.0.66")
	h.refresh(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 3 {
		t.Fatalf("a new conflict must be reported: %d", len(reports))
	}
}

func TestJoinModeReadiness(t *testing.T) {
	env, f := newHostEnvWith(t, func(o *Options) { o.JoinMode = true }, "npm")
	f.set("npm", "172.20.0.5")
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the first resolution: readyz %d", w.Code)
	}
	env.srv.live.hosts.refresh(context.Background())
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusOK {
		t.Fatalf("own address known: readyz %d", w.Code)
	}
	f.set(ServeAlias)
	env.srv.live.hosts.refresh(context.Background())
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("own address unknown: readyz %d", w.Code)
	}
}

// fakeConn is a connection with chosen addresses.
type fakeConn struct {
	net.Conn
	local, remote net.Addr
	closed        atomic.Bool
}

func (c *fakeConn) LocalAddr() net.Addr  { return c.local }
func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c *fakeConn) Close() error         { c.closed.Store(true); return nil }

// fakeListener hands out the queued connections, then net.ErrClosed.
type fakeListener struct {
	conns chan net.Conn
}

func (l *fakeListener) Accept() (net.Conn, error) {
	c, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *fakeListener) Close() error   { return nil }
func (l *fakeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4zero, Port: 8080} }

func tcp(s string) *net.TCPAddr { return net.TCPAddrFromAddrPort(netip.MustParseAddrPort(s)) }

func TestJoinModeListenerFilter(t *testing.T) {
	env, f := newHostEnvWith(t, func(o *Options) { o.JoinMode = true }, "npm")
	f.set("npm", "172.20.0.5")
	cases := []struct {
		name          string
		local, remote string
		ok            bool
	}{
		{"loopback healthcheck", "127.0.0.1:8080", "127.0.0.1:40000", true},
		{"proxy network", "172.20.0.2:8080", "172.20.0.5:40000", true},
		{"proxy network v6", "[fd00::2]:8080", "[fd00::5]:40000", true},
		{"other container on the proxy network", "172.20.0.2:8080", "172.20.0.77:40000", true},
		{"database network", "172.30.0.2:8080", "172.30.0.9:40000", false},
		{"backend network", "10.213.250.10:8080", "10.213.250.20:40000", false},
		{"own address from another subnet", "172.20.0.2:8080", "10.0.0.9:40000", false},
	}
	run := func(t *testing.T, wantOK func(i int) bool) {
		t.Helper()
		ln := &fakeListener{conns: make(chan net.Conn, len(cases))}
		conns := make([]*fakeConn, len(cases))
		for i, c := range cases {
			conns[i] = &fakeConn{local: tcp(c.local), remote: tcp(c.remote)}
			ln.conns <- conns[i]
		}
		close(ln.conns)
		w := env.srv.WrapListener(ln)
		var accepted []net.Conn
		for {
			c, err := w.Accept()
			if err != nil {
				break
			}
			accepted = append(accepted, c)
		}
		for i, c := range cases {
			got := slices.Contains(accepted, net.Conn(conns[i]))
			if got != wantOK(i) || got == conns[i].closed.Load() {
				t.Errorf("%s: accepted %v, closed %v", c.name, got, conns[i].closed.Load())
			}
		}
	}
	t.Run("own address unknown: only loopback", func(t *testing.T) {
		run(t, func(i int) bool { return i == 0 })
	})
	env.srv.live.hosts.refresh(context.Background())
	t.Run("own address known", func(t *testing.T) { run(t, func(i int) bool { return cases[i].ok }) })
	if !strings.Contains(env.logText(), "did not arrive on its address on the proxy network") &&
		!strings.Contains(env.logText(), "address on the proxy network is unknown") {
		t.Fatalf("missing refusal warning: %s", env.logText())
	}
}

func TestJoinModeListenerLoopbackWhileUnknown(t *testing.T) {
	env, _ := newHostEnvWith(t, func(o *Options) { o.JoinMode = true })
	h := env.srv.live.hosts
	if !h.acceptConn(tcp("127.0.0.1:8080"), tcp("127.0.0.1:1")) || !h.acceptConn(tcp("[::1]:8080"), tcp("[::1]:1")) {
		t.Fatal("loopback (the healthcheck) is served while the own address is unknown")
	}
	if h.acceptConn(tcp("172.20.0.2:8080"), tcp("172.20.0.5:1")) {
		t.Fatal("fail closed while the own address is unknown")
	}
	// Without the join shape the listener is unchanged.
	plain, _ := newHostEnv(t)
	ln := &fakeListener{conns: make(chan net.Conn)}
	if plain.srv.WrapListener(ln) != net.Listener(ln) {
		t.Fatal("WrapListener must not wrap outside the join shape")
	}
}

// Health checks still drive the re-resolution hooks (review L4).
func TestHealthRequestsKickResolution(t *testing.T) {
	env, f := newHostEnv(t, "npm")
	f.set("npm", "172.20.0.5")
	h := env.srv.live.hosts
	select {
	case <-h.kickCh:
	default:
	}
	r := req(http.MethodGet, "/healthz", "X-Forwarded-For", "198.51.100.9")
	r.RemoteAddr = "172.20.0.5:1"
	env.do(r)
	select {
	case <-h.kickCh:
	default:
		t.Fatal("an untrusted X-Forwarded-For on /healthz must kick a resolution")
	}
}
