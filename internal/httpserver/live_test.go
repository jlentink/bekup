package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
)

func liveSettings() LiveSettings {
	return LiveSettings{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
		MaxBodyBytes:   64, RatePerSecond: 1, RateBurst: 1000, UserPerSecond: 1, UserBurst: 10,
	}
}

func TestLiveApplyTrustedProxies(t *testing.T) {
	env := newEnv(t, nil)
	proxied := func() netip.Addr {
		r := req(http.MethodGet, "/api/x", "X-Forwarded-For", "198.51.100.9")
		r.RemoteAddr = "192.0.2.5:1"
		env.do(r)
		env.rec.mu.Lock()
		defer env.rec.mu.Unlock()
		return env.rec.ip
	}
	if got := proxied(); got.String() != "192.0.2.5" {
		t.Fatalf("untrusted peer: %v", got)
	}
	s := liveSettings()
	s.TrustedProxies = append(s.TrustedProxies, netip.MustParsePrefix("192.0.2.0/24"))
	if err := env.srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	if got := proxied(); got.String() != "198.51.100.9" {
		t.Fatalf("after widening: %v", got)
	}
	if err := env.srv.Live().Apply(liveSettings()); err != nil {
		t.Fatal(err)
	}
	if got := proxied(); got.String() != "192.0.2.5" {
		t.Fatalf("after narrowing: %v", got)
	}
}

func TestLiveApplyBodyAndRate(t *testing.T) {
	env := newEnv(t, nil)
	post := func(n int) int {
		r := req(http.MethodPost, "/api/body", "Origin", sameOrigin)
		r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", n)))
		r.ContentLength = int64(n)
		return env.do(r).Code
	}
	if c := post(100); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("100 bytes before: %d", c)
	}
	s := liveSettings()
	s.MaxBodyBytes = 200
	if err := env.srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	if c := post(100); c != http.StatusOK {
		t.Fatalf("100 bytes after raising: %d", c)
	}
	// A lower burst applies to existing buckets at once.
	s.RateBurst = 1
	if err := env.srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	get := func() int { return env.do(req(http.MethodGet, "/healthz")).Code }
	if c := get(); c != http.StatusOK {
		t.Fatalf("first: %d", c)
	}
	if c := get(); c != http.StatusTooManyRequests {
		t.Fatalf("second with burst 1: %d", c)
	}
}

func TestLivePrepareRejectsAndKeeps(t *testing.T) {
	env := newEnv(t, nil)
	live := env.srv.Live()
	before := live.bodyLimit.Load()
	for _, bad := range []func(*LiveSettings){
		func(s *LiveSettings) { s.MaxBodyBytes = 0 },
		func(s *LiveSettings) { s.RatePerSecond = 0 },
		func(s *LiveSettings) { s.RateBurst = 0 },
		func(s *LiveSettings) { s.TrustedProxies = []netip.Prefix{{}} },
	} {
		s := liveSettings()
		s.MaxBodyBytes = 999
		bad(&s)
		if _, err := live.Prepare(s); err == nil {
			t.Errorf("Prepare accepted %+v", s)
		}
	}
	if live.bodyLimit.Load() != before {
		t.Fatal("a failed Prepare changed the body limit")
	}
	// Prepare alone changes nothing until the commit runs.
	s := liveSettings()
	s.MaxBodyBytes = 999
	commit, err := live.Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	if live.bodyLimit.Load() != before {
		t.Fatal("Prepare applied before the commit")
	}
	commit()
	if live.bodyLimit.Load() != 999 {
		t.Fatal("commit did not apply")
	}
}

func TestLiveUserLimit(t *testing.T) {
	res := &fakeResolver{byToken: map[string]auth.Principal{"good": validPrincipal()}}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	policy := auth.CookiePolicyFor("https://backup.example.com")
	srv, err := New(Options{
		BaseURL: "https://backup.example.com", MaxBodyBytes: 1 << 20, Logger: slog.New(slog.DiscardHandler),
		Ready:     func(context.Context) error { return nil },
		RateLimit: RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   ok, UI: testUI,
		Sessions: &SessionOptions{Resolver: res, Cookies: policy,
			UserLimit: RateLimitOptions{PerSecond: 0.001, Burst: 5, MaxClients: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := liveSettings()
	s.MaxBodyBytes = 1 << 20
	s.UserPerSecond, s.UserBurst = 0.001, 1
	if err := srv.Live().Apply(s); err != nil {
		t.Fatal(err)
	}
	cookie := ck(policy.Name(), "good")
	if w := get(srv.Handler(), "/graphql", cookie); w.Code != http.StatusOK {
		t.Fatalf("first request: %d", w.Code)
	}
	if w := get(srv.Handler(), "/graphql", cookie); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request with user burst 1: %d", w.Code)
	}
}

// TestLiveConcurrent swaps the settings while requests run (-race).
func TestLiveConcurrent(t *testing.T) {
	env := newEnv(t, nil)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				r := req(http.MethodGet, "/api/x", "X-Forwarded-For", "198.51.100.9")
				r.RemoteAddr = "10.0.0.5:1"
				env.do(r)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 200 {
				s := liveSettings()
				s.MaxBodyBytes = int64(64 + i + j)
				s.RateBurst = 500 + j
				if err := env.srv.Live().Apply(s); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestLiveFromConfig(t *testing.T) {
	c := config.DefaultBekup().Server
	c.TrustedProxies = []string{" 10.1.2.3/16 "}
	s, err := LiveFromConfig(&c)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.TrustedProxies) != 1 || s.TrustedProxies[0].String() != "10.1.0.0/16" || s.MaxBodyBytes != c.MaxBodyBytes ||
		s.RateBurst != c.RateLimitBurst || s.UserBurst != c.UserRateLimitBurst {
		t.Fatalf("LiveFromConfig = %+v", s)
	}
	c.TrustedProxies = []string{"nope"}
	if _, err := LiveFromConfig(&c); err == nil {
		t.Fatal("invalid CIDR accepted")
	}
}

func TestRateLimiterSetLimits(t *testing.T) {
	l, err := NewRateLimiter[string](RateLimitOptions{PerSecond: 1, Burst: 5, MaxClients: 10})
	if err != nil {
		t.Fatal(err)
	}
	l.Allow("a")
	for _, bad := range [][2]float64{{0, 1}, {1, 0}, {-1, 1}} {
		if err := l.SetLimits(bad[0], int(bad[1])); err == nil {
			t.Errorf("SetLimits(%v) accepted", bad)
		}
	}
	if r, b := l.Limits(); r != 1 || b != 5 {
		t.Fatalf("a refused SetLimits changed the limits: %v %d", r, b)
	}
	if err := l.SetLimits(0.001, 2); err != nil {
		t.Fatal(err)
	}
	// "a" had 4 tokens left; capped at the new burst of 2.
	for i := range 2 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("token %d refused", i)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("tokens above the new burst were kept")
	}
}
