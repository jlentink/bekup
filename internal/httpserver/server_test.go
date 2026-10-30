package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

const indexHTML = "<!doctype html><title>bekup</title>"

var testUI = fstest.MapFS{
	"index.html":                 {Data: []byte(indexHTML)},
	"favicon.svg":                {Data: []byte("<svg></svg>")},
	"theme-init.js":              {Data: []byte("void 0;")},
	"assets/index-AbCdEf12.js":   {Data: []byte("console.log(1)")},
	"assets/index-Zx_9-QwE.css":  {Data: []byte("body{}")},
	".gitkeep":                   {Data: nil},
	"assets/unhashed-file.woff2": {Data: []byte("x")},
}

// recorded captures what a mounted handler saw.
type recorded struct {
	mu      sync.Mutex
	ip      netip.Addr
	proto   string
	headers http.Header
	bodyErr error
}

type testEnv struct {
	h      http.Handler
	srv    *Server
	logs   *bytes.Buffer
	rec    *recorded
	ready  *error
	now    *time.Time
	logsMu *sync.Mutex
}

type lockedBuffer struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (b lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func newEnv(t *testing.T, mutate func(*Options)) *testEnv {
	t.Helper()
	env := &testEnv{logs: &bytes.Buffer{}, rec: &recorded{}, logsMu: &sync.Mutex{}}
	var readyErr error
	env.ready = &readyErr
	now := time.Unix(1_700_000_000, 0)
	env.now = &now
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.rec.mu.Lock()
		env.rec.ip = ClientIP(r.Context())
		env.rec.proto = ForwardedProto(r.Context())
		env.rec.headers = r.Header.Clone()
		_, env.rec.bodyErr = io.ReadAll(r.Body)
		env.rec.mu.Unlock()
		switch r.URL.Path {
		case "/api/panic":
			panic("secret-panic-detail")
		case "/api/body":
			if IsBodyTooLarge(env.rec.bodyErr) {
				writeError(w, http.StatusRequestEntityTooLarge)
				return
			}
		}
		writeText(w, http.StatusOK, "ok")
	})
	opts := Options{
		BaseURL:        "https://backup.example.com",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00::/64")},
		MaxBodyBytes:   64,
		RateLimit: RateLimitOptions{PerSecond: 1, Burst: 1000, MaxClients: 100,
			Now: func() time.Time { return *env.now }},
		Logger: slog.New(slog.NewJSONHandler(lockedBuffer{env.logsMu, env.logs}, nil)),
		UI:     testUI,
		Ready:  func(context.Context) error { return *env.ready },
		API:    api,
	}
	if mutate != nil {
		mutate(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	env.h = s.Handler()
	env.srv = s
	return env
}

func (e *testEnv) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *testEnv) logText() string {
	e.logsMu.Lock()
	defer e.logsMu.Unlock()
	return e.logs.String()
}

func req(method, target string, hdr ...string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, target, http.NoBody)
	r.RemoteAddr = "203.0.113.7:51000"
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	return r
}

const sameOrigin = "https://backup.example.com"

func TestSecurityHeadersExact(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy":      "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; worker-src 'none'; manifest-src 'self'; require-trusted-types-for 'script'; trusted-types 'none'",
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"X-Frame-Options":              "DENY",
		"Permissions-Policy":           PermissionsPolicy,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Strict-Transport-Security":    "max-age=63072000; includeSubDomains",
	}
	env := newEnv(t, nil)
	for _, r := range []*http.Request{
		req(http.MethodGet, "/healthz"),
		req(http.MethodGet, "/", "Accept", "text/html"),
		req(http.MethodGet, "/assets/index-AbCdEf12.js"),
		req(http.MethodGet, "/api/x"),
		req(http.MethodGet, "/graphql"),
		req(http.MethodGet, "/nope.js"),
		req(http.MethodPost, "/api/x"), // 403
		req(http.MethodGet, "/api/panic"),
	} {
		w := env.do(r)
		for k, v := range want {
			if got := w.Header().Values(k); len(got) != 1 || got[0] != v {
				t.Errorf("%s %s (%d): %s = %q, want %q", r.Method, r.URL.Path, w.Code, k, got, v)
			}
		}
	}
}

func TestNoHSTSOverHTTP(t *testing.T) {
	env := newEnv(t, func(o *Options) { o.BaseURL = "http://localhost:8080"; o.Dev = true })
	for _, p := range []string{"/healthz", "/", "/api/x"} {
		w := env.do(req(http.MethodGet, p, "Accept", "text/html"))
		if v := w.Header().Get("Strict-Transport-Security"); v != "" {
			t.Errorf("%s: HSTS sent over http: %q", p, v)
		}
		if w.Header().Get("Content-Security-Policy") != ContentSecurityPolicy {
			t.Errorf("%s: CSP missing", p)
		}
	}
	// A forwarded "https" from an untrusted peer must not turn HSTS on.
	w := env.do(req(http.MethodGet, "/healthz", "X-Forwarded-Proto", "https"))
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent because of a spoofed X-Forwarded-Proto")
	}
}

func TestCacheControl(t *testing.T) {
	env := newEnv(t, nil)
	for path, want := range map[string]string{
		"/healthz":                    "no-store",
		"/readyz":                     "no-store",
		"/api/x":                      "no-store",
		"/graphql":                    "no-store",
		"/":                           "no-cache",
		"/index.html":                 "no-cache",
		"/jobs/42":                    "no-cache",
		"/theme-init.js":              "no-cache",
		"/assets/index-AbCdEf12.js":   "public, max-age=31536000, immutable",
		"/assets/index-Zx_9-QwE.css":  "public, max-age=31536000, immutable",
		"/assets/unhashed-file.woff2": "no-cache",
		"/assets/missing-AbCdEf12.js": "no-store",
	} {
		w := env.do(req(http.MethodGet, path, "Accept", "text/html"))
		if got := w.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s (%d): Cache-Control %q, want %q", path, w.Code, got, want)
		}
	}
}

func TestSPA(t *testing.T) {
	env := newEnv(t, nil)
	cases := []struct {
		method, path, accept string
		status               int
		index                bool
	}{
		{"GET", "/", "text/html", 200, true},
		{"GET", "/jobs/42", "text/html,application/xhtml+xml;q=0.9", 200, true},
		{"HEAD", "/settings", "text/html", 200, false},
		{"GET", "/jobs/42", "application/json", 404, false},
		{"GET", "/jobs/42", "", 404, false},
		{"GET", "/api", "text/html", 200, false},         // API handler, not index
		{"GET", "/api/unknown", "text/html", 200, false}, // API handler
		{"GET", "/assets/missing.js", "text/html", 404, false},
		{"GET", "/healthzx", "text/html", 200, true},
		{"GET", "/healthz/x", "text/html", 404, false},
		{"GET", "/readyz/x", "text/html", 404, false},
		{"GET", "/favicon.svg", "", 200, false},
		{"GET", "/../index.html", "", 200, true},
		{"GET", "/.gitkeep", "text/html", 200, true},
	}
	for _, c := range cases {
		w := env.do(req(c.method, c.path, "Accept", c.accept))
		if w.Code != c.status {
			t.Errorf("%s %s: status %d, want %d", c.method, c.path, w.Code, c.status)
		}
		if got := w.Body.String() == indexHTML; got != c.index {
			t.Errorf("%s %s: index served = %v, want %v (body %q)", c.method, c.path, got, c.index, w.Body.String())
		}
	}

	// GraphQL and API without handlers: 404 placeholders, never the SPA.
	env = newEnv(t, func(o *Options) { o.API = nil })
	for _, p := range []string{"/graphql", "/graphql/x", "/api", "/api/", "/api/v1/jobs"} {
		w := env.do(req(http.MethodGet, p, "Accept", "text/html"))
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "<title>") {
			t.Errorf("%s: %d %q, want a plain 404", p, w.Code, w.Body.String())
		}
	}
	// POST to a client route with a valid origin: still 404, not index.
	w := env.do(req(http.MethodPost, "/jobs", "Origin", sameOrigin, "Accept", "text/html"))
	if w.Code != http.StatusNotFound {
		t.Errorf("POST /jobs: %d", w.Code)
	}
	// Content types and conditional requests.
	w = env.do(req(http.MethodGet, "/assets/index-AbCdEf12.js"))
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("js content type %q", ct)
	}
	etag := w.Header().Get("ETag")
	if w = env.do(req(http.MethodGet, "/", "If-None-Match", etag)); w.Code != http.StatusOK {
		t.Errorf("different etag: %d", w.Code)
	}
	if w = env.do(req(http.MethodGet, "/assets/index-AbCdEf12.js", "If-None-Match", etag)); w.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", w.Code)
	}

	// No UI embedded: 404 instead of a broken page.
	env = newEnv(t, func(o *Options) { o.UI = nil })
	if w := env.do(req(http.MethodGet, "/", "Accept", "text/html")); w.Code != http.StatusNotFound {
		t.Errorf("no UI: %d", w.Code)
	}
}

func TestHealthAndReady(t *testing.T) {
	env := newEnv(t, nil)
	if w := env.do(req(http.MethodGet, "/healthz")); w.Code != 200 || w.Body.String() != "ok\n" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body.String())
	}
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != 200 {
		t.Fatalf("readyz: %d", w.Code)
	}
	*env.ready = errors.New("schema version 3 behind: password=hunter2")
	w := env.do(req(http.MethodGet, "/readyz"))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "schema") {
		t.Fatalf("readyz failing: %d %q", w.Code, w.Body.String())
	}
	if w := env.do(req(http.MethodGet, "/healthz")); w.Code != 200 {
		t.Fatalf("healthz must not depend on readiness: %d", w.Code)
	}
	if w := env.do(req(http.MethodPost, "/healthz", "Origin", sameOrigin)); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST healthz: %d", w.Code)
	}
	if w := env.do(req(http.MethodHead, "/healthz")); w.Code != 200 {
		t.Fatalf("HEAD healthz: %d", w.Code)
	}
}

func TestReadyTimeout(t *testing.T) {
	env := newEnv(t, func(o *Options) {
		o.ReadyTimeout = 20 * time.Millisecond
		o.Ready = func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
	})
	if w := env.do(req(http.MethodGet, "/readyz")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz: %d", w.Code)
	}
}

func TestClientIPTrustedProxies(t *testing.T) {
	cases := []struct {
		name   string
		peer   string
		hdr    []string
		wantIP string
		proto  string
	}{
		{"untrusted peer, spoofed XFF/Proto", "203.0.113.7:1", []string{"X-Forwarded-For", "1.2.3.4", "X-Forwarded-Proto", "https"}, "203.0.113.7", ""},
		{"untrusted peer, spoofed Forwarded/X-Real-IP", "203.0.113.7:1", []string{"Forwarded", "for=1.2.3.4;proto=https", "X-Real-Ip", "1.2.3.4"}, "203.0.113.7", ""},
		{"trusted proxy", "10.0.0.5:1", []string{"X-Forwarded-For", "198.51.100.9", "X-Forwarded-Proto", "https"}, "198.51.100.9", "https"},
		{"trusted proxy, client-prepended spoof", "10.0.0.5:1", []string{"X-Forwarded-For", "1.2.3.4, 198.51.100.9"}, "198.51.100.9", ""},
		{"trusted chain", "10.0.0.5:1", []string{"X-Forwarded-For", "198.51.100.9, 10.0.0.7"}, "198.51.100.9", ""},
		{"multiple header lines", "10.0.0.5:1", []string{"X-Forwarded-For", "1.2.3.4", "X-Forwarded-For", "198.51.100.9"}, "198.51.100.9", ""},
		{"all trusted", "10.0.0.5:1", []string{"X-Forwarded-For", "10.0.0.9, 10.0.0.7"}, "10.0.0.9", ""},
		{"garbage entry", "10.0.0.5:1", []string{"X-Forwarded-For", "198.51.100.9, nonsense"}, "10.0.0.5", ""},
		{"no XFF", "10.0.0.5:1", nil, "10.0.0.5", ""},
		{"bad proto ignored", "10.0.0.5:1", []string{"X-Forwarded-Proto", "javascript"}, "10.0.0.5", ""},
		{"ipv6 trusted", "[fd00::1]:1", []string{"X-Forwarded-For", "2001:db8::5"}, "2001:db8::5", ""},
		{"mapped peer", "[::ffff:10.0.0.5]:1", []string{"X-Forwarded-For", "198.51.100.9"}, "198.51.100.9", ""},
		// M5 review L1: zones are dropped, so link-local clients get a
		// storable address (and one client stays one address).
		{"link-local peer with zone", "[fe80::1%eth0]:1", nil, "fe80::1", ""},
		{"zoned XFF entry", "10.0.0.5:1", []string{"X-Forwarded-For", "fe80::7%en0"}, "fe80::7", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newEnv(t, nil)
			r := req(http.MethodGet, "/api/x", c.hdr...)
			r.RemoteAddr = c.peer
			r.Header.Set("X-Forwarded-Host", "evil.example")
			if w := env.do(r); w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			if got := env.rec.ip.String(); got != c.wantIP {
				t.Errorf("client IP %s, want %s", got, c.wantIP)
			}
			if env.rec.proto != c.proto {
				t.Errorf("proto %q, want %q", env.rec.proto, c.proto)
			}
			for _, h := range forwardingHeaders {
				if v := env.rec.headers.Get(h); v != "" {
					t.Errorf("%s reached the handler: %q", h, v)
				}
			}
		})
	}
}

// M1 review L4: a request from a trusted proxy without X-Forwarded-For is
// attributed to the proxy; that is logged, at most once per interval.
func TestMissingXFFWarning(t *testing.T) {
	var buf bytes.Buffer
	now := time.Unix(1000, 0)
	x := &xffWarner{logger: slog.New(slog.NewJSONHandler(&buf, nil)), now: func() time.Time { return now }}
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
	mk := func(peer, path string, xff bool) *http.Request {
		r := req(http.MethodGet, path)
		r.RemoteAddr = peer
		if xff {
			r.Header.Set("X-Forwarded-For", "198.51.100.9")
		}
		return r
	}
	count := func() int { return strings.Count(buf.String(), "without X-Forwarded-For") }
	x.check(mk("203.0.113.7:1", "/api/x", false), trusted) // untrusted peer
	x.check(mk("10.0.0.5:1", "/api/x", true), trusted)     // has XFF
	x.check(mk("10.0.0.5:1", "/healthz", false), trusted)  // health probe
	if count() != 0 {
		t.Fatalf("warned for an exempt request: %s", buf.String())
	}
	x.check(mk("10.0.0.5:1", "/api/x", false), trusted)
	x.check(mk("10.0.0.5:1", "/api/x", false), trusted)
	if count() != 1 || !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Fatalf("want one WARN: %s", buf.String())
	}
	now = now.Add(missingXFFWarnInterval)
	x.check(mk("10.0.0.5:1", "/api/x", false), trusted)
	if count() != 2 {
		t.Fatalf("no warning after the interval: %s", buf.String())
	}
}

// D-205: X-Forwarded-For from a peer that is not a trusted proxy (a proxy
// without its fixed address) is logged, at most once per interval, with
// the peer only (never the header value).
func TestUntrustedXFFWarning(t *testing.T) {
	var buf bytes.Buffer
	now := time.Unix(1000, 0)
	x := &xffWarner{logger: slog.New(slog.NewJSONHandler(&buf, nil)), now: func() time.Time { return now }}
	trusted := []netip.Prefix{netip.MustParsePrefix("10.213.253.5/32")}
	mk := func(peer, path string, xff bool) *http.Request {
		r := req(http.MethodGet, path)
		r.RemoteAddr = peer
		if xff {
			r.Header.Set("X-Forwarded-For", "198.51.100.9")
		}
		return r
	}
	count := func() int { return strings.Count(buf.String(), "not in server.trusted_proxies") }
	x.check(mk("10.213.253.5:1", "/api/x", true), trusted)   // trusted proxy
	x.check(mk("10.213.253.7:1", "/api/x", false), trusted)  // no header
	x.check(mk("10.213.253.7:1", "/healthz", true), trusted) // health probe
	if count() != 0 {
		t.Fatalf("warned for an exempt request: %s", buf.String())
	}
	x.check(mk("10.213.253.7:1", "/api/x", true), trusted)
	x.check(mk("[::ffff:10.213.253.7]:1", "/api/x", true), trusted)
	if count() != 1 || !strings.Contains(buf.String(), `"peer":"10.213.253.7"`) || strings.Contains(buf.String(), "198.51.100.9") {
		t.Fatalf("want one WARN with the peer only: %s", buf.String())
	}
	// The two warnings are rate-limited independently.
	x.check(mk("10.213.253.5:1", "/api/x", false), trusted)
	if !strings.Contains(buf.String(), "without X-Forwarded-For") {
		t.Fatalf("missing-XFF warning suppressed: %s", buf.String())
	}
	now = now.Add(missingXFFWarnInterval)
	x.check(mk("10.213.253.7:1", "/api/x", true), trusted)
	if count() != 2 {
		t.Fatalf("no warning after the interval: %s", buf.String())
	}
}

func TestOriginCheck(t *testing.T) {
	ws := []string{"Connection", "keep-alive, Upgrade", "Upgrade", "websocket"}
	cases := []struct {
		name   string
		dev    bool
		method string
		hdr    []string
		ok     bool
	}{
		{"same-origin POST", false, "POST", []string{"Origin", sameOrigin, "Sec-Fetch-Site", "same-origin"}, true},
		{"same-origin POST, origin only", false, "POST", []string{"Origin", "HTTPS://Backup.Example.com"}, true},
		{"same-origin POST, sec-fetch-site only", false, "POST", []string{"Sec-Fetch-Site", "same-origin"}, true},
		{"cross-site POST", false, "POST", []string{"Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"}, false},
		{"cross-site POST, origin only", false, "POST", []string{"Origin", "https://evil.example"}, false},
		{"same-site subdomain", false, "POST", []string{"Origin", "https://x.backup.example.com", "Sec-Fetch-Site", "same-site"}, false},
		{"matching origin but cross-site fetch", false, "POST", []string{"Origin", sameOrigin, "Sec-Fetch-Site", "cross-site"}, false},
		{"null origin", false, "POST", []string{"Origin", "null"}, false},
		{"http variant of base", false, "POST", []string{"Origin", "http://backup.example.com"}, false},
		{"explicit default port", false, "POST", []string{"Origin", "https://backup.example.com:8443"}, false},
		{"no headers", false, "POST", nil, false},
		{"two origins", false, "POST", []string{"Origin", sameOrigin, "Origin", sameOrigin}, false},
		{"DELETE cross-site", false, "DELETE", []string{"Origin", "https://evil.example"}, false},
		{"PUT same-origin", false, "PUT", []string{"Origin", sameOrigin}, true},
		{"GET cross-site is fine", false, "GET", []string{"Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"}, true},
		{"vite POST outside dev", false, "POST", []string{"Origin", "http://localhost:5173", "Sec-Fetch-Site", "same-origin"}, false},
		{"vite POST in dev", true, "POST", []string{"Origin", "http://localhost:5173", "Sec-Fetch-Site", "same-origin"}, true},
		{"vite 127 POST in dev", true, "POST", []string{"Origin", "http://127.0.0.1:5173"}, true},
		{"other port in dev", true, "POST", []string{"Origin", "http://localhost:3000"}, false},
		{"ws upgrade cross-site", false, "GET", append([]string{"Origin", "https://evil.example"}, ws...), false},
		{"ws upgrade no origin", false, "GET", ws, false},
		{"ws upgrade same-origin", false, "GET", append([]string{"Origin", sameOrigin}, ws...), true},
		{"ws upgrade vite outside dev", false, "GET", append([]string{"Origin", "http://localhost:5173"}, ws...), false},
		{"ws upgrade vite in dev", true, "GET", append([]string{"Origin", "http://localhost:5173"}, ws...), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newEnv(t, func(o *Options) { o.Dev = c.dev })
			w := env.do(req(c.method, "/api/x", c.hdr...))
			if got := w.Code != http.StatusForbidden; got != c.ok {
				t.Errorf("allowed = %v (status %d), want %v", got, w.Code, c.ok)
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	env := newEnv(t, func(o *Options) { o.RateLimit.PerSecond = 0.5; o.RateLimit.Burst = 2 })
	get := func(peer string, hdr ...string) *httptest.ResponseRecorder {
		r := req(http.MethodGet, "/healthz", hdr...)
		r.RemoteAddr = peer
		return env.do(r)
	}
	for i := range 2 {
		if w := get("203.0.113.7:1"); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := get("203.0.113.7:2")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("over limit: %d Retry-After=%q", w.Code, w.Header().Get("Retry-After"))
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("429 without security headers")
	}
	// A spoofed XFF from an untrusted peer does not get a fresh bucket.
	if w := get("203.0.113.7:3", "X-Forwarded-For", "198.51.100.1"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF bypassed the limit: %d", w.Code)
	}
	// Another client is unaffected; behind a trusted proxy, buckets are per client.
	if w := get("203.0.113.8:1"); w.Code != 200 {
		t.Fatalf("other client: %d", w.Code)
	}
	if w := get("10.0.0.5:1", "X-Forwarded-For", "198.51.100.1"); w.Code != 200 {
		t.Fatalf("proxied client: %d", w.Code)
	}
	*env.now = env.now.Add(2 * time.Second)
	if w := get("203.0.113.7:4"); w.Code != 200 {
		t.Fatalf("after refill: %d", w.Code)
	}
}

func TestRateLimiterBounded(t *testing.T) {
	now := time.Unix(0, 0)
	l, err := newRateLimiter(RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 3, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		l.Allow(IPKey(netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})))
	}
	if len(l.buckets) != 3 || l.lru.Len() != 3 {
		t.Fatalf("tracked %d/%d clients, want 3", len(l.buckets), l.lru.Len())
	}
	// IPv6 addresses of one /64 share a bucket.
	a, _ := l.Allow(IPKey(netip.MustParseAddr("2001:db8::1")))
	b, _ := l.Allow(IPKey(netip.MustParseAddr("2001:db8::2")))
	if !a || b {
		t.Fatalf("/64 grouping: first %v second %v", a, b)
	}
	for _, o := range []RateLimitOptions{{PerSecond: 0, Burst: 1, MaxClients: 1}, {PerSecond: 1, Burst: 0, MaxClients: 1}, {PerSecond: 1, Burst: 1}} {
		if _, err := newRateLimiter(o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	env := newEnv(t, nil)
	post := func(body io.Reader, contentLength int64) *httptest.ResponseRecorder {
		r := req(http.MethodPost, "/api/body", "Origin", sameOrigin)
		r.Body = io.NopCloser(body)
		r.ContentLength = contentLength
		return env.do(r)
	}
	if w := post(strings.NewReader("small"), 5); w.Code != 200 {
		t.Fatalf("small body: %d", w.Code)
	}
	big := strings.Repeat("x", 65)
	if w := post(strings.NewReader(big), 65); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared big body: %d", w.Code)
	}
	// Chunked (unknown length): capped while reading.
	if w := post(strings.NewReader(big), -1); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed big body: %d", w.Code)
	}
}

func TestPanicRecovery(t *testing.T) {
	env := newEnv(t, nil)
	w := env.do(req(http.MethodGet, "/api/panic"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "secret-panic-detail") || strings.Contains(w.Body.String(), "goroutine") {
		t.Fatalf("panic leaked to the client: %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	logs := env.logText()
	if !strings.Contains(logs, "http handler panic") || !strings.Contains(logs, `"status":500`) {
		t.Fatalf("panic not logged: %s", logs)
	}
}

func TestRequestIDAndAccessLog(t *testing.T) {
	env := newEnv(t, nil)
	w1 := env.do(req(http.MethodGet, "/healthz", "X-Request-ID", "client-chosen"))
	w2 := env.do(req(http.MethodGet, "/healthz"))
	id1, id2 := w1.Header().Get("X-Request-ID"), w2.Header().Get("X-Request-ID")
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !hex32.MatchString(id1) || !hex32.MatchString(id2) || id1 == id2 {
		t.Fatalf("request IDs %q %q", id1, id2)
	}
	env.do(req(http.MethodGet, "/api/downloads/TICKETVALUE123?ticket=QUERYSECRET", "Accept", "text/html"))
	env.do(req(http.MethodGet, "/jobs/1?token=QUERYSECRET2", "Accept", "text/html"))
	logs := env.logText()
	for _, leak := range []string{"TICKETVALUE123", "QUERYSECRET", "client-chosen"} {
		if strings.Contains(logs, leak) {
			t.Errorf("access log contains %q: %s", leak, logs)
		}
	}
	for _, want := range []string{`"path":"/api/downloads/[redacted]"`, `"path":"/jobs/1"`, `"request_id":"` + id1 + `"`, `"client_ip":"203.0.113.7"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("access log lacks %s: %s", want, logs)
		}
	}
	if got := logPath("/"+strings.Repeat("a", 400), nil); len(got) != maxLoggedPath+3 {
		t.Errorf("long path not truncated: %d", len(got))
	}
}

func TestNewValidation(t *testing.T) {
	base := func() Options {
		return Options{
			BaseURL: "https://x.example", MaxBodyBytes: 1, Logger: slog.New(slog.DiscardHandler),
			Ready:     func(context.Context) error { return nil },
			RateLimit: RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 1},
		}
	}
	if _, err := New(base()); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Options){
		"no logger":   func(o *Options) { o.Logger = nil },
		"no ready":    func(o *Options) { o.Ready = nil },
		"no body cap": func(o *Options) { o.MaxBodyBytes = 0 },
		"bad base":    func(o *Options) { o.BaseURL = "ftp://x" },
		"no host":     func(o *Options) { o.BaseURL = "https://" },
		"bad limiter": func(o *Options) { o.RateLimit.Burst = 0 },
	} {
		o := base()
		mut(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOriginOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://Backup.Example.com":      "https://backup.example.com",
		"https://backup.example.com:443/": "https://backup.example.com",
		"http://localhost:80":             "http://localhost",
		"http://localhost:8080/ui":        "http://localhost:8080",
		"https://[2001:DB8::1]:8443":      "https://[2001:db8::1]:8443",
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, in, http.NoBody)
		if got := originOf(r.URL); got != want {
			t.Errorf("originOf(%s) = %s, want %s", in, got, want)
		}
	}
}

type downloadRecorder struct {
	mu      sync.Mutex
	tickets []string
}

func (d *downloadRecorder) ServeDownload(w http.ResponseWriter, _ *http.Request, ticket string) {
	d.mu.Lock()
	d.tickets = append(d.tickets, ticket)
	d.mu.Unlock()
	writeText(w, http.StatusOK, "file")
}

// The download route (task 3.9, D-150): GET only, the ticket from the
// path, the rest of /api untouched, the ticket never in the access log.
func TestDownloadRoute(t *testing.T) {
	dl := &downloadRecorder{}
	env := newEnv(t, func(o *Options) { o.Download = dl })
	const ticket = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde"
	if w := env.do(req(http.MethodGet, "/api/download/"+ticket)); w.Code != http.StatusOK || w.Body.String() != "file" {
		t.Fatalf("download: %d %q", w.Code, w.Body.String())
	}
	// Other methods never reach the download handler (and a HEAD can
	// never redeem a ticket).
	env.do(req(http.MethodPost, "/api/download/"+ticket, "Origin", sameOrigin))
	env.do(req(http.MethodHead, "/api/download/"+ticket))
	if w := env.do(req(http.MethodGet, "/api/download/"+ticket+"/more")); w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("sub-path must fall through to the API handler: %d %q", w.Code, w.Body.String())
	}
	if w := env.do(req(http.MethodGet, "/api/other")); w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("API handler: %d", w.Code)
	}
	dl.mu.Lock()
	got := dl.tickets
	dl.mu.Unlock()
	if len(got) != 1 || got[0] != ticket {
		t.Fatalf("tickets seen: %q", got)
	}
	logs := env.logText()
	if strings.Contains(logs, ticket) || !strings.Contains(logs, `"path":"/api/download/[redacted]"`) {
		t.Errorf("access log: %s", logs)
	}
	// Without a Download handler the route is a 404 from the API mount.
	env2 := newEnv(t, func(o *Options) { o.API = nil })
	if w := env2.do(req(http.MethodGet, "/api/download/"+ticket)); w.Code != http.StatusNotFound {
		t.Errorf("no handler: %d", w.Code)
	}
}
