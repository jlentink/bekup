package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
)

// fakeResolver maps tokens to principals and counts lookups.
type fakeResolver struct {
	mu      sync.Mutex
	calls   int
	byToken map[string]auth.Principal
	err     error
}

func (f *fakeResolver) Resolve(_ context.Context, token string, _ auth.RequestMeta) (auth.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return auth.Principal{}, f.err
	}
	p, ok := f.byToken[token]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return p, nil
}

type seen struct {
	principal auth.Principal
	hasP      bool
	setter    bool
	meta      auth.RequestMeta
}

func sessionServer(t *testing.T, baseURL string, res SessionResolver, perSecond float64, onRequest func(http.ResponseWriter, *http.Request)) (http.Handler, *seen) {
	t.Helper()
	s := &seen{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.principal, s.hasP = auth.FromContext(r.Context())
		_, s.setter = auth.CookieSetterFrom(r.Context())
		s.meta = auth.RequestMetaFrom(r.Context())
		if onRequest != nil {
			onRequest(w, r)
		}
		w.WriteHeader(http.StatusOK)
	})
	srv, err := New(Options{
		BaseURL: baseURL, MaxBodyBytes: 1 << 20, Logger: slog.New(slog.DiscardHandler),
		Ready:     func(context.Context) error { return nil },
		RateLimit: RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   h, API: h, UI: testUI,
		Sessions: &SessionOptions{
			Resolver: res, Cookies: auth.CookiePolicyFor(baseURL),
			UserLimit: RateLimitOptions{PerSecond: perSecond, Burst: 2, MaxClients: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler(), s
}

func get(h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("User-Agent", "agent\x01/1")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// ck is a request cookie (attributes are irrelevant on requests; set for
// the linter).
func ck(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}

func validPrincipal() auth.Principal {
	return auth.Principal{UserID: uuid.New(), SessionID: uuid.New(), Role: auth.RoleAdmin, AAL: 1}
}

func TestSessionMiddlewareResolves(t *testing.T) {
	p := validPrincipal()
	res := &fakeResolver{byToken: map[string]auth.Principal{"good": p}}
	h, s := sessionServer(t, "https://bekup.example.com", res, 0, nil)

	// No cookie: anonymous, a cookie setter and meta, no lookup.
	w := get(h, "/graphql")
	if w.Code != http.StatusOK || s.hasP || !s.setter || res.calls != 0 {
		t.Fatalf("no cookie: code %d principal %v setter %v calls %d", w.Code, s.hasP, s.setter, res.calls)
	}
	if s.meta.IP.String() != "192.0.2.1" || s.meta.UserAgent != "agent/1" {
		t.Fatalf("request meta %+v", s.meta)
	}

	// Valid cookie: principal attached.
	w = get(h, "/api/x", ck(auth.SessionCookieName, "good"))
	if w.Code != http.StatusOK || !s.hasP || s.principal != p || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("valid cookie: %d %+v %q", w.Code, s.principal, w.Header().Get("Set-Cookie"))
	}

	// Unknown/expired: anonymous and the cookie is cleared.
	w = get(h, "/graphql", ck(auth.SessionCookieName, "expired"))
	sc := w.Header().Get("Set-Cookie")
	if w.Code != http.StatusOK || s.hasP || !strings.HasPrefix(sc, auth.SessionCookieName+"=;") || !strings.Contains(sc, "Max-Age=0") {
		t.Fatalf("expired cookie: %d %v %q", w.Code, s.hasP, sc)
	}

	// In https mode the http cookie name is ignored (no lookup).
	calls := res.calls
	get(h, "/graphql", ck(auth.InsecureSessionCookieName, "good"))
	if s.hasP || res.calls != calls {
		t.Fatal("bekup_session honored in https mode")
	}

	// Two session cookies: ambiguous, anonymous, cleared, no lookup.
	w = get(h, "/graphql", ck(auth.SessionCookieName, "good"), ck(auth.SessionCookieName, "evil"))
	if s.hasP || res.calls != calls || !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("duplicate session cookies not refused")
	}

	// Static files, health and SPA routes never look sessions up.
	for _, path := range []string{"/", "/healthz", "/assets/index-AbCdEf12.js", "/some/page"} {
		get(h, path, ck(auth.SessionCookieName, "good"))
	}
	if res.calls != calls {
		t.Fatal("session lookup outside /graphql and /api")
	}
}

func TestSessionMiddlewareHTTPMode(t *testing.T) {
	p := validPrincipal()
	res := &fakeResolver{byToken: map[string]auth.Principal{"good": p}}
	h, s := sessionServer(t, "http://localhost:8080", res, 0, func(_ http.ResponseWriter, r *http.Request) {
		if c, ok := auth.CookieSetterFrom(r.Context()); ok && r.URL.Path == "/api/login" {
			c.SetSessionCookie("new-token")
		}
	})
	get(h, "/graphql", ck(auth.InsecureSessionCookieName, "good"))
	if !s.hasP {
		t.Fatal("bekup_session not honored in http mode")
	}
	get(h, "/graphql", ck(auth.SessionCookieName, "good"))
	if s.hasP {
		t.Fatal("__Host- cookie honored in http mode")
	}
	w := get(h, "/api/login")
	sc := w.Header().Get("Set-Cookie")
	if !strings.HasPrefix(sc, "bekup_session=new-token") || strings.Contains(sc, "Secure") || !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Strict") {
		t.Fatalf("http-mode cookie %q", sc)
	}
}

func TestSessionMiddlewareFailsClosed(t *testing.T) {
	res := &fakeResolver{err: errors.New("db down")}
	h, s := sessionServer(t, "https://bekup.example.com", res, 0, nil)
	w := get(h, "/graphql", ck(auth.SessionCookieName, "good"))
	if w.Code != http.StatusServiceUnavailable || s.hasP {
		t.Fatalf("lookup error: %d", w.Code)
	}
}

func TestSessionMiddlewareUserRateLimit(t *testing.T) {
	p := validPrincipal()
	res := &fakeResolver{byToken: map[string]auth.Principal{"good": p}}
	h, _ := sessionServer(t, "https://bekup.example.com", res, 0.001, nil)
	c := ck(auth.SessionCookieName, "good")
	for i := range 2 {
		if w := get(h, "/graphql", c); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := get(h, "/graphql", c)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the per-user limit: %d", w.Code)
	}
	// Anonymous requests are not limited per user.
	if w := get(h, "/graphql"); w.Code != http.StatusOK {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

// ChargeUser takes further tokens from the caller's per-user bucket (the
// GraphQL layer charges root fields beyond the first, D-117).
func TestChargeUser(t *testing.T) {
	p := validPrincipal()
	res := &fakeResolver{byToken: map[string]auth.Principal{"good": p}}
	var charged []bool
	h, _ := sessionServer(t, "https://bekup.example.com", res, 0.001, func(_ http.ResponseWriter, r *http.Request) {
		ok, _ := ChargeUser(r.Context(), 1)
		charged = append(charged, ok)
	})
	c := ck(auth.SessionCookieName, "good")
	// Burst 2: the request takes one token, the charge the second.
	if w := get(h, "/graphql", c); w.Code != http.StatusOK || len(charged) != 1 || !charged[0] {
		t.Fatalf("first request: %d %v", w.Code, charged)
	}
	if w := get(h, "/graphql", c); w.Code != http.StatusTooManyRequests {
		t.Fatalf("bucket not charged: %d", w.Code)
	}
	// Anonymous callers and n <= 0 are never charged.
	if ok, _ := ChargeUser(context.Background(), 5); !ok {
		t.Fatal("anonymous charge refused")
	}
}

func TestRateLimiterAllowN(t *testing.T) {
	now := time.Unix(0, 0)
	l, err := NewRateLimiter[string](RateLimitOptions{PerSecond: 1, Burst: 3, MaxClients: 10, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.AllowN("k", 4); ok {
		t.Fatal("more than the burst allowed")
	}
	if ok, _ := l.AllowN("k", 3); !ok {
		t.Fatal("the burst refused")
	}
	ok, wait := l.AllowN("k", 2)
	if ok || wait != 2*time.Second {
		t.Fatalf("empty bucket: %v %s", ok, wait)
	}
	now = now.Add(2 * time.Second)
	if ok, _ := l.AllowN("k", 2); !ok {
		t.Fatal("refilled tokens refused")
	}
}

func TestCookieSinkKeepsLastDecision(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Add("Set-Cookie", "other=1")
	s := &cookieSink{w: w, policy: auth.CookiePolicy{Secure: true}}
	s.ClearSessionCookie()
	s.SetSessionCookie("tok")
	vals := w.Header().Values("Set-Cookie")
	if len(vals) != 2 || vals[0] != "other=1" || !strings.HasPrefix(vals[1], auth.SessionCookieName+"=tok") {
		t.Fatalf("Set-Cookie = %q", vals)
	}
}

func TestSessionOptionsRequireResolver(t *testing.T) {
	_, err := New(Options{
		BaseURL: "https://x.example", MaxBodyBytes: 1, Logger: slog.New(slog.DiscardHandler),
		Ready:     func(context.Context) error { return nil },
		RateLimit: RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 1},
		Sessions:  &SessionOptions{},
	})
	if err == nil {
		t.Fatal("Sessions without a resolver accepted")
	}
}
