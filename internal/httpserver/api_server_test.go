package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// The external API's server (D-220): serve's client address rule and
// middleware with JSON errors and the API CSP.

const testAPIToken = "bkp_aaisem2ekvthpcezvk54zxpo74_aaaqeayeaudaocajbifqydiob4ibceqtcqkrmfyydenbwha5dypq"

type apiEnv struct {
	h      http.Handler
	srv    *Server
	logs   *bytes.Buffer
	logsMu *sync.Mutex
	seen   netip.Addr
	seenMu sync.Mutex
	ready  error
}

func newAPIEnv(t *testing.T, mutate func(*APIOptions)) *apiEnv {
	t.Helper()
	env := &apiEnv{logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}}
	api := http.NewServeMux()
	api.HandleFunc("/seen", func(w http.ResponseWriter, r *http.Request) {
		env.seenMu.Lock()
		env.seen = ClientIP(r.Context())
		env.seenMu.Unlock()
		if _, err := io.ReadAll(r.Body); IsBodyTooLarge(err) {
			WriteAPIError(w, http.StatusRequestEntityTooLarge)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})
	api.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("panic-detail") })
	o := APIOptions{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32")},
		MaxBodyBytes:   64,
		RateLimit:      RateLimitOptions{PerSecond: 1, Burst: 100, MaxClients: 100},
		Logger:         slog.New(slog.NewJSONHandler(lockedBuffer{env.logsMu, env.logs}, nil)),
		Ready:          func(context.Context) error { return env.ready },
		API:            http.StripPrefix(APIBasePath, api),
	}
	if mutate != nil {
		mutate(&o)
	}
	s, err := NewAPIServer(o)
	if err != nil {
		t.Fatal(err)
	}
	env.h, env.srv = s.Handler(), s
	return env
}

func (e *apiEnv) get(path, peer string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	r.RemoteAddr = peer
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *apiEnv) client(peer string, hdr ...string) string {
	e.get("/api/v1/seen", peer, hdr...)
	e.seenMu.Lock()
	defer e.seenMu.Unlock()
	return e.seen.String()
}

func (e *apiEnv) logText() string {
	e.logsMu.Lock()
	defer e.logsMu.Unlock()
	return e.logs.String()
}

func TestAPIServerClientAddress(t *testing.T) {
	env := newAPIEnv(t, nil)
	for _, tc := range []struct {
		name, peer string
		hdr        []string
		want       string
	}{
		{"direct", "192.0.2.10:4000", nil, "192.0.2.10"},
		{"spoofed XFF from an untrusted peer", "192.0.2.10:4000", []string{"X-Forwarded-For", "203.0.113.7"}, "192.0.2.10"},
		{"spoofed X-Real-IP from an untrusted peer", "192.0.2.10:4000", []string{"X-Real-Ip", "203.0.113.7", "Forwarded", "for=203.0.113.7"}, "192.0.2.10"},
		{"trusted proxy", "10.0.0.5:4000", []string{"X-Forwarded-For", "203.0.113.7"}, "203.0.113.7"},
		{"trusted proxy, client-supplied hops", "10.0.0.5:4000", []string{"X-Forwarded-For", "198.51.100.1, 203.0.113.7"}, "203.0.113.7"},
		{"trusted proxy without XFF", "10.0.0.5:4000", nil, "10.0.0.5"},
		{"next to the proxy", "10.0.0.6:4000", []string{"X-Forwarded-For", "203.0.113.7"}, "10.0.0.6"},
		{"mapped peer", "[::ffff:192.0.2.10]:4000", nil, "192.0.2.10"},
	} {
		if got := env.client(tc.peer, tc.hdr...); got != tc.want {
			t.Errorf("%s: client %s, want %s", tc.name, got, tc.want)
		}
	}
	if logs := env.logText(); !strings.Contains(logs, "not in the trusted proxy list") || !strings.Contains(logs, `"setting":"listen.trusted_proxies"`) {
		t.Fatalf("the untrusted X-Forwarded-For is warned about with the key:\n%s", logs)
	}
}

func TestAPIServerTrustedProxyHosts(t *testing.T) {
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	// The external API locates the proxy network through its own alias,
	// not serve's.
	f.set(ExternalAPIAlias, "172.20.0.2")
	f.set(ServeAlias, "172.20.0.9")
	f.set("npm", "172.20.0.5")
	env := newAPIEnv(t, func(o *APIOptions) {
		o.TrustedProxies = nil
		o.TrustedProxyHosts = []string{"npm"}
		o.ProxyHostResolver = f.resolve
		o.InterfaceAddrs = fakeIfaces
	})
	if got := env.client("172.20.0.5:1", "X-Forwarded-For", "203.0.113.7"); got != "172.20.0.5" {
		t.Fatalf("before the resolution: %s", got)
	}
	env.srv.live.hosts.refresh(context.Background())
	if got := env.client("172.20.0.5:1", "X-Forwarded-For", "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("resolved own proxy: %s", got)
	}
	if got := env.client("172.20.0.6:1", "X-Forwarded-For", "203.0.113.7"); got != "172.20.0.6" {
		t.Fatalf("another container: %s", got)
	}
	if !strings.Contains(env.logText(), `"alias":"bekup-external-api"`) {
		t.Fatalf("own alias not used:\n%s", env.logText())
	}
	// Without the own alias nothing is trusted (fail closed).
	f.set(ExternalAPIAlias)
	env.srv.live.hosts.refresh(context.Background())
	if got := env.client("172.20.0.5:1", "X-Forwarded-For", "203.0.113.7"); got != "172.20.0.5" {
		t.Fatalf("without the own alias: %s", got)
	}
}

func TestAPIServerHeadersAndErrors(t *testing.T) {
	env := newAPIEnv(t, nil)
	w := env.get("/api/v1/seen", "192.0.2.1:1")
	h := w.Header()
	if w.Code != http.StatusOK || h.Get("Content-Security-Policy") != APIContentSecurityPolicy ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" ||
		h.Get("Strict-Transport-Security") == "" || h.Get("X-Request-Id") == "" || h.Get("Content-Type") != "application/json" {
		t.Fatalf("headers: %d %v", w.Code, h)
	}
	// POST without Origin or Sec-Fetch-Site: no Origin check on the API.
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/seen", strings.NewReader("{}"))
	r.RemoteAddr = "192.0.2.1:1"
	w = httptest.NewRecorder()
	env.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("POST without Origin: %d", w.Code)
	}
	jsonError := func(w *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		var e APIError
		if w.Code != status || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &e) != nil || e.Error != code {
			t.Fatalf("want %d %s, got %d %q", status, code, w.Code, w.Body.String())
		}
	}
	jsonError(env.get("/nothing", "192.0.2.1:1"), http.StatusNotFound, "not_found")
	jsonError(env.get("/api/v1/panic", "192.0.2.1:1"), http.StatusInternalServerError, "internal_error")
	if strings.Contains(env.get("/api/v1/panic", "192.0.2.1:1").Body.String(), "panic-detail") {
		t.Fatal("panic detail leaked")
	}
	big := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/seen", strings.NewReader(strings.Repeat("x", 65)))
	big.RemoteAddr = "192.0.2.1:1"
	w = httptest.NewRecorder()
	env.h.ServeHTTP(w, big)
	jsonError(w, http.StatusRequestEntityTooLarge, "body_too_large")

	// The per-IP limiter answers JSON with Retry-After.
	lim := newAPIEnv(t, func(o *APIOptions) { o.RateLimit = RateLimitOptions{PerSecond: 0.01, Burst: 1, MaxClients: 10} })
	lim.get("/api/v1/seen", "192.0.2.1:1")
	w = lim.get("/api/v1/seen", "192.0.2.1:1")
	jsonError(w, http.StatusTooManyRequests, "rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestAPIServerHealth(t *testing.T) {
	env := newAPIEnv(t, nil)
	if w := env.get("/healthz", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("healthz %d", w.Code)
	}
	if w := env.get("/readyz", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("readyz %d", w.Code)
	}
	env.ready = errors.New("db down")
	if w := env.get("/readyz", "127.0.0.1:1"); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "db down") {
		t.Fatalf("readyz failing: %d %q", w.Code, w.Body.String())
	}
}

func TestAPIServerAccessLogNeverShowsTokens(t *testing.T) {
	env := newAPIEnv(t, nil)
	env.get("/api/v1/seen", "192.0.2.1:1", "Authorization", "Bearer "+testAPIToken)
	env.get("/api/v1/seen?token="+testAPIToken, "192.0.2.1:1")
	env.get("/api/v1/requests/"+testAPIToken, "192.0.2.1:1")
	env.get("/api/v1/requests/"+strings.ToUpper(testAPIToken), "192.0.2.1:1")
	logs := env.logText()
	if strings.Count(logs, `"msg":"http request"`) != 4 {
		t.Fatalf("access log:\n%s", logs)
	}
	for _, leak := range []string{testAPIToken, strings.ToUpper(testAPIToken), "Bearer", "Authorization", "aaisem2ekvthpcezvk54zxpo74", "token="} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(leak)) {
			t.Fatalf("access log shows %q:\n%s", leak, logs)
		}
	}
	if !strings.Contains(logs, `"path":"[redacted]"`) || !strings.Contains(logs, `"path":"/api/v1/seen"`) {
		t.Fatalf("paths:\n%s", logs)
	}
}

func TestNewAPIServerValidates(t *testing.T) {
	base := APIOptions{
		MaxBodyBytes: 1, RateLimit: RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 1},
		Logger: slog.New(slog.DiscardHandler), Ready: func(context.Context) error { return nil }, API: http.NotFoundHandler(),
	}
	for name, mutate := range map[string]func(*APIOptions){
		"logger": func(o *APIOptions) { o.Logger = nil },
		"ready":  func(o *APIOptions) { o.Ready = nil },
		"api":    func(o *APIOptions) { o.API = nil },
		"body":   func(o *APIOptions) { o.MaxBodyBytes = 0 },
		"rate":   func(o *APIOptions) { o.RateLimit.PerSecond = 0 },
		"host":   func(o *APIOptions) { o.TrustedProxyHosts = []string{"10.0.0.1"} },
		"cidr":   func(o *APIOptions) { o.TrustedProxies = []netip.Prefix{{}} },
	} {
		o := base
		mutate(&o)
		if _, err := NewAPIServer(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	srv, err := NewAPIServer(base)
	if err != nil {
		t.Fatal(err)
	}
	hs := srv.NewHTTPServer("127.0.0.1:0")
	if hs.MaxHeaderBytes != apiMaxHeaderBytes || hs.ReadHeaderTimeout != 0 && hs.ReadHeaderTimeout < time.Nanosecond {
		t.Fatalf("http server: %+v", hs)
	}
}

// TestAPIServerJoinMode (D-220 amendment 4): in the own-proxy join shape
// the external API's wildcard listener serves only its own address on the
// proxy network (found through its alias bekup-external-api) and loopback,
// fails /readyz while that address is unknown or the alias is taken, and
// still trusts the link proxy's fixed address (link mode: no names).
func TestAPIServerJoinMode(t *testing.T) {
	f := &fakeDNS{addrs: map[string][]netip.Addr{}}
	f.set(ExternalAPIAlias, "172.20.0.2")
	f.set(ServeAlias, "172.20.0.9")
	env := newAPIEnv(t, func(o *APIOptions) {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("172.20.0.5/32")}
		o.JoinMode = true
		o.ProxyHostResolver = f.resolve
		o.InterfaceAddrs = fakeIfaces
	})
	if w := env.get("/readyz", "127.0.0.1:1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the first resolution: readyz %d", w.Code)
	}
	h := env.srv.live.hosts
	if h.acceptConn(tcp("172.20.0.2:8081"), tcp("172.20.0.5:1")) || !h.acceptConn(tcp("127.0.0.1:8081"), tcp("127.0.0.1:1")) {
		t.Fatal("while the own address is unknown only loopback is served")
	}
	h.refresh(context.Background())
	if w := env.get("/readyz", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("own address known: readyz %d", w.Code)
	}
	for _, c := range []struct {
		local, remote string
		ok            bool
	}{
		{"172.20.0.2:8081", "172.20.0.5:1", true},       // the proxy on the proxy network
		{"10.213.250.11:8081", "10.213.250.5:1", false}, // another network
		{"172.30.0.2:8081", "172.30.0.9:1", false},      // a database network
		{"[::1]:8081", "[::1]:1", true},                 // the healthcheck
	} {
		if got := h.acceptConn(tcp(c.local), tcp(c.remote)); got != c.ok {
			t.Errorf("%s <- %s: accepted %v", c.local, c.remote, got)
		}
	}
	if got := env.client("172.20.0.5:1", "X-Forwarded-For", "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("link proxy: %s", got)
	}
	if env.srv.WrapListener(&fakeListener{conns: make(chan net.Conn)}) == nil {
		t.Fatal("no listener")
	}
	// Another container behind the alias: /readyz fails.
	f.set(ExternalAPIAlias, "172.20.0.2", "172.20.0.66")
	h.refresh(context.Background())
	if w := env.get("/readyz", "127.0.0.1:1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("alias conflict: readyz %d", w.Code)
	}
	// Outside the join shape the listener is unchanged.
	plain := newAPIEnv(t, nil)
	ln := &fakeListener{conns: make(chan net.Conn)}
	if plain.srv.WrapListener(ln) != net.Listener(ln) {
		t.Fatal("WrapListener must not wrap outside the join shape")
	}
}
