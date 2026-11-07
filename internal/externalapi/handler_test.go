package externalapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/httpserver"
)

// fakeToken is a token of the fake store.
type fakeToken struct {
	hash [32]byte
	name string
	// expires nil: never expires.
	expires *time.Time
	// allowed nil: any address.
	allowed []netip.Prefix
	jobs    []Job
}

// fakeStore mimics the api.* functions: every call authenticates the
// token (hash and client address) itself.
type fakeStore struct {
	mu       sync.Mutex
	tokens   map[uuid.UUID]*fakeToken
	requests map[uuid.UUID]RequestFacts
	owner    map[uuid.UUID]uuid.UUID // request -> token
	// refusal forces api.request_run's answer; retryAt goes with it.
	refusal string
	retryAt *time.Time
	// fail makes every call fail like a lost database.
	fail error
	// statusFail makes only RequestStatus fail.
	statusFail error
	calls      int
	ips        []netip.Addr
	keys       []string
	// byKey replays idempotency keys.
	byKey map[string]uuid.UUID
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tokens: map[uuid.UUID]*fakeToken{}, requests: map[uuid.UUID]RequestFacts{},
		owner: map[uuid.UUID]uuid.UUID{}, byKey: map[string]uuid.UUID{},
	}
}

func (s *fakeStore) admit(c Credential, ip netip.Addr) (*fakeToken, bool) {
	s.calls++
	s.ips = append(s.ips, ip)
	t, ok := s.tokens[c.TokenID]
	if !ok || t.hash != c.SecretHash {
		return nil, false
	}
	if t.allowed == nil {
		return t, true
	}
	for _, p := range t.allowed {
		if ip.IsValid() && p.Contains(ip) {
			return t, true
		}
	}
	return nil, false
}

func (s *fakeStore) Authenticate(_ context.Context, c Credential, ip netip.Addr) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return Token{}, s.fail
	}
	t, ok := s.admit(c, ip)
	if !ok {
		return Token{}, ErrUnauthenticated
	}
	return Token{Name: t.name, ExpiresAt: t.expires}, nil
}

func (s *fakeStore) TokenJobs(_ context.Context, c Credential, ip netip.Addr) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	t, ok := s.admit(c, ip)
	if !ok {
		return nil, nil
	}
	return t.jobs, nil
}

func (s *fakeStore) RequestRun(_ context.Context, c Credential, jobID uuid.UUID, key string, ip netip.Addr) (RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return RunResult{}, s.fail
	}
	s.keys = append(s.keys, key)
	t, ok := s.admit(c, ip)
	if !ok {
		return RunResult{Refusal: RefusalUnauthenticated}, nil
	}
	if s.refusal != "" {
		return RunResult{Refusal: s.refusal, RetryAt: s.retryAt}, nil
	}
	inScope := false
	for _, j := range t.jobs {
		inScope = inScope || j.ID == jobID
	}
	if !inScope {
		return RunResult{Refusal: RefusalNotFound}, nil
	}
	if id, ok := s.byKey[key]; ok && key != "" {
		return RunResult{RequestID: id, CreatedAt: s.requests[id].CreatedAt, Replayed: true}, nil
	}
	id := uuid.New()
	f := RequestFacts{RequestID: id, JobID: jobID, Outcome: "pending", CreatedAt: time.Now()}
	s.requests[id], s.owner[id] = f, c.TokenID
	if key != "" {
		s.byKey[key] = id
	}
	return RunResult{RequestID: id, CreatedAt: f.CreatedAt}, nil
}

func (s *fakeStore) RequestStatus(_ context.Context, c Credential, id uuid.UUID, ip netip.Addr) (RequestFacts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return RequestFacts{}, s.fail
	}
	if s.statusFail != nil {
		return RequestFacts{}, s.statusFail
	}
	if _, ok := s.admit(c, ip); !ok {
		return RequestFacts{}, ErrNotFound
	}
	f, ok := s.requests[id]
	if !ok || s.owner[id] != c.TokenID {
		return RequestFacts{}, ErrNotFound
	}
	return f, nil
}

func (s *fakeStore) Ping(context.Context) error { return s.fail }

func (s *fakeStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// addToken creates a token with the given jobs and returns its plaintext.
func (s *fakeStore) addToken(t *testing.T, allowed []netip.Prefix, jobs ...Job) string {
	t.Helper()
	g, err := apikey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[g.ID] = &fakeToken{hash: g.Hash, name: "ci " + g.ID.String()[:6], expires: new(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)), allowed: allowed, jobs: jobs}
	return g.Plaintext
}

type env struct {
	t      *testing.T
	store  *fakeStore
	h      http.Handler
	logs   *bytes.Buffer
	logsMu sync.Mutex
	now    time.Time
}

func (e *env) Write(p []byte) (int, error) {
	e.logsMu.Lock()
	defer e.logsMu.Unlock()
	return e.logs.Write(p)
}

func (e *env) logText() string {
	e.logsMu.Lock()
	defer e.logsMu.Unlock()
	return e.logs.String()
}

const (
	proxyPeer = "10.0.0.5:4444"
	ciAddr    = "203.0.113.7"
)

func newTestEnv(t *testing.T, mutate func(*Options)) *env {
	t.Helper()
	e := &env{t: t, store: newFakeStore(), logs: &bytes.Buffer{}, now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewJSONHandler(e, nil))
	o := Options{
		Store: e.store, Logger: log,
		TokenLimit: httpserver.RateLimitOptions{PerSecond: 1, Burst: 50, MaxClients: 100},
		FailedAuth: FailedAuthOptions{Limit: 10, Window: 5 * time.Minute, MaxClients: 100},
		Now:        func() time.Time { return e.now },
	}
	if mutate != nil {
		mutate(&o)
	}
	api, err := NewHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.NewAPIServer(httpserver.APIOptions{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32")},
		MaxBodyBytes:   16 << 10,
		RateLimit:      httpserver.RateLimitOptions{PerSecond: 100, Burst: 1000, MaxClients: 100},
		Logger:         log,
		Ready:          func(context.Context) error { return nil },
		API:            api,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return e
}

// req describes one request; from is the client address the trusted
// proxy forwards (empty: a direct peer 192.0.2.50).
type req struct {
	method, path, token, from, body, ctype string
	hdr                                    []string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader = http.NoBody
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	hr := httptest.NewRequestWithContext(context.Background(), r.method, r.path, body)
	if r.from != "" {
		hr.RemoteAddr = proxyPeer
		hr.Header.Set("X-Forwarded-For", r.from)
	} else {
		hr.RemoteAddr = "192.0.2.50:5555"
	}
	if r.token != "" {
		hr.Header.Set("Authorization", "Bearer "+r.token)
	}
	if r.ctype != "" {
		hr.Header.Set("Content-Type", r.ctype)
	}
	for i := 0; i+1 < len(r.hdr); i += 2 {
		hr.Header.Add(r.hdr[i], r.hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, hr)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type %q", w.Header().Get("Content-Type"))
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return v
}

func wantError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if e := decode[httpserver.APIError](t, w); e.Error != code {
		t.Fatalf("code %q, want %q", e.Error, code)
	}
}

func runsPath(job uuid.UUID) string { return "/api/v1/jobs/" + job.String() + "/runs" }

func TestRequestRunAccepted(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New(), Name: "app db"}
	tok := e.store.addToken(t, nil, job)
	w := e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, from: ciAddr})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	got := decode[RunAccepted](t, w)
	if got.JobID != job.ID || got.State != StatePending || got.RequestID == uuid.Nil ||
		got.StatusURL != "/api/v1/requests/"+got.RequestID.String() || w.Header().Get("Location") != got.StatusURL ||
		got.CreatedAt.IsZero() || w.Header().Get(ReplayedHeader) != "" {
		t.Fatalf("answer %+v %v", got, w.Header())
	}
	// The forwarded address of the trusted proxy reached the database.
	if ips := e.store.ips; len(ips) != 1 || ips[0].String() != ciAddr {
		t.Fatalf("client addresses %v", ips)
	}
	// An empty JSON object is an empty request too.
	w = e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, body: "{}", ctype: "application/json; charset=utf-8"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("{} body: %d %s", w.Code, w.Body.String())
	}
}

func TestRequestRunIdempotency(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New(), Name: "app"}
	tok := e.store.addToken(t, nil, job)
	first := e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, hdr: []string{IdempotencyHeader, "build-42_a"}})
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Body.String())
	}
	orig := decode[RunAccepted](t, first)
	e.store.mu.Lock()
	f := e.store.requests[orig.RequestID]
	f.Outcome, f.RiverState, f.RunStatus, f.RunAttempt = "accepted", ptr("running"), ptr("running"), ptr(int32(1))
	e.store.requests[orig.RequestID] = f
	e.store.mu.Unlock()
	again := e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, hdr: []string{IdempotencyHeader, "build-42_a"}})
	if again.Code != http.StatusAccepted || again.Header().Get(ReplayedHeader) != "true" {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}
	if got := decode[RunAccepted](t, again); got.RequestID != orig.RequestID || got.State != StateRunning {
		t.Fatalf("replay answer %+v", got)
	}
	if keys := e.store.keys; keys[0] != "build-42_a" {
		t.Fatalf("keys %v", keys)
	}
	// A replay whose state cannot be read is 503, never a false pending
	// (finding 6).
	e.store.mu.Lock()
	e.store.statusFail = errors.New("connection lost")
	e.store.mu.Unlock()
	lost := e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, hdr: []string{IdempotencyHeader, "build-42_a"}})
	wantError(t, lost, http.StatusServiceUnavailable, "unavailable")
	if lost.Header().Get(ReplayedHeader) != "" || lost.Header().Get("Retry-After") != "5" {
		t.Fatalf("replay without state: %v", lost.Header())
	}
	e.store.mu.Lock()
	e.store.statusFail = nil
	e.store.mu.Unlock()
	for _, bad := range [][]string{
		{IdempotencyHeader, ""}, {IdempotencyHeader, "has space"}, {IdempotencyHeader, strings.Repeat("a", 65)},
		{IdempotencyHeader, "ümlaut"}, {IdempotencyHeader, "a", IdempotencyHeader, "b"},
	} {
		wantError(t, e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, hdr: bad}), http.StatusBadRequest, "invalid_request")
	}
}

func TestRequestRunRefusals(t *testing.T) {
	retry := time.Date(2026, 11, 3, 12, 7, 30, 0, time.UTC)
	for _, tc := range []struct {
		refusal    string
		status     int
		code       string
		retryAfter string
	}{
		{RefusalNotFound, http.StatusNotFound, "not_found", ""},
		{RefusalInvalidRequest, http.StatusBadRequest, "invalid_request", ""},
		{RefusalIdempotencyMismatch, http.StatusConflict, "idempotency_mismatch", ""},
		{RefusalJobDisabled, http.StatusConflict, "job_disabled", ""},
		{RefusalRunActive, http.StatusConflict, "run_active", ""},
		{RefusalTooManyPending, http.StatusTooManyRequests, "too_many_pending", "5"},
		{RefusalRateLimited, http.StatusTooManyRequests, "rate_limited", "450"},
		{"something_new", http.StatusInternalServerError, "internal_error", ""},
	} {
		t.Run(tc.refusal, func(t *testing.T) {
			e := newTestEnv(t, nil)
			job := Job{ID: uuid.New()}
			tok := e.store.addToken(t, nil, job)
			e.store.refusal, e.store.retryAt = tc.refusal, &retry
			w := e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok})
			wantError(t, w, tc.status, tc.code)
			if got := w.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Fatalf("Retry-After %q, want %q", got, tc.retryAfter)
			}
		})
	}
}

func TestUnauthenticatedIsGeneric(t *testing.T) {
	// Many failures from two addresses: the throttle is TestFailedAuthThrottle's.
	e := newTestEnv(t, func(o *Options) { o.FailedAuth.Limit = 1000 })
	job := Job{ID: uuid.New()}
	allowed := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	tok := e.store.addToken(t, allowed, job)
	other := e.store.addToken(t, nil, job)
	k, err := apikey.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	wrongSecret := apikey.Format(k.ID, make([]byte, apikey.SecretBytes))
	cases := map[string]req{
		"no header":         {},
		"malformed":         {token: "not-a-token"},
		"wrong secret":      {token: wrongSecret},
		"unknown token":     {token: e.store.addTokenUnstored(t)},
		"refused address":   {token: tok, from: ciAddr},
		"refused, direct":   {token: tok},
		"basic scheme":      {hdr: []string{"Authorization", "Basic " + tok}},
		"two headers":       {hdr: []string{"Authorization", "Bearer " + tok, "Authorization", "Bearer " + other}},
		"bearer, no space":  {hdr: []string{"Authorization", "Bearer\t" + tok}},
		"token with spaces": {hdr: []string{"Authorization", "Bearer  " + tok}},
	}
	type answer struct {
		status int
		body   string
		auth   string
	}
	for _, ep := range []struct{ method, path string }{
		{http.MethodPost, runsPath(job.ID)},
		{http.MethodGet, "/api/v1/requests/" + uuid.NewString()},
		{http.MethodGet, "/api/v1/self"},
		{http.MethodPost, "/api/v1/jobs/not-a-uuid/runs"},
	} {
		var first *answer
		for name, r := range cases {
			r.method, r.path = ep.method, ep.path
			w := e.do(r)
			got := answer{w.Code, w.Body.String(), w.Header().Get("WWW-Authenticate")}
			if got.status != http.StatusUnauthorized || got.auth != `Bearer realm="bekup"` {
				t.Fatalf("%s %s %s: %+v", ep.method, ep.path, name, got)
			}
			if first == nil {
				first = &got
			} else if got != *first {
				t.Fatalf("%s %s %s: %+v differs from %+v", ep.method, ep.path, name, got, *first)
			}
		}
	}
	// The allowed address passes.
	if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok, from: "198.51.100.9"}); w.Code != http.StatusOK {
		t.Fatalf("allowed address: %d", w.Code)
	}
}

// addTokenUnstored returns a well-formed token the store does not know.
func (s *fakeStore) addTokenUnstored(t *testing.T) string {
	t.Helper()
	g, err := apikey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return g.Plaintext
}

func TestNotFoundIsEnumerationSafe(t *testing.T) {
	e := newTestEnv(t, nil)
	mine := Job{ID: uuid.New(), Name: "mine"}
	theirs := Job{ID: uuid.New(), Name: "theirs"}
	tok := e.store.addToken(t, nil, mine)
	otherTok := e.store.addToken(t, nil, theirs)
	// Unknown, out-of-scope and malformed job ids answer alike.
	bodies := map[string]bool{}
	for _, p := range []string{runsPath(uuid.New()), runsPath(theirs.ID), "/api/v1/jobs/xyz/runs"} {
		w := e.do(req{method: http.MethodPost, path: p, token: tok})
		wantError(t, w, http.StatusNotFound, "not_found")
		bodies[w.Body.String()] = true
	}
	if len(bodies) != 1 {
		t.Fatalf("job 404 bodies differ: %v", bodies)
	}
	// Another token's request, an unknown request and a malformed id.
	w := e.do(req{method: http.MethodPost, path: runsPath(theirs.ID), token: otherTok})
	theirReq := decode[RunAccepted](t, w).RequestID
	bodies = map[string]bool{}
	for _, p := range []string{"/api/v1/requests/" + theirReq.String(), "/api/v1/requests/" + uuid.NewString(), "/api/v1/requests/zzz"} {
		w := e.do(req{method: http.MethodGet, path: p, token: tok})
		wantError(t, w, http.StatusNotFound, "not_found")
		bodies[w.Body.String()] = true
	}
	if len(bodies) != 1 {
		t.Fatalf("request 404 bodies differ: %v", bodies)
	}
	// Never 403 anywhere.
	if strings.Contains(e.logText(), `"status":403`) {
		t.Fatal("a 403 was served")
	}
}

func TestRequestStatus(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	id := decode[RunAccepted](t, e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok})).RequestID
	w := e.do(req{method: http.MethodGet, path: "/api/v1/requests/" + id.String(), token: tok})
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if s := decode[Status](t, w); s.State != StatePending || s.Done || s.Success || s.RequestID != id || s.JobID != job.ID {
		t.Fatalf("pending status %+v", s)
	}
	e.store.mu.Lock()
	f := e.store.requests[id]
	f.Outcome, f.RiverState, f.RunID, f.RunStatus, f.RunAttempt = "accepted", ptr("completed"), ptr(uuid.New()), ptr("success"), ptr(int32(2))
	e.store.requests[id] = f
	e.store.mu.Unlock()
	w = e.do(req{method: http.MethodGet, path: "/api/v1/requests/" + id.String(), token: tok})
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"requestId", "jobId", "state", "done", "success", "status", "attempt", "runId", "reason", "createdAt", "queuedAt", "startedAt", "finishedAt"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("status lacks %s", k)
		}
	}
	if raw["state"] != "success" || raw["done"] != true || raw["success"] != true || raw["status"] != "SUCCESS" || raw["attempt"] != float64(2) {
		t.Fatalf("success status %v", raw)
	}
}

func TestSelf(t *testing.T) {
	e := newTestEnv(t, nil)
	jobs := []Job{{ID: uuid.New(), Name: "a"}, {ID: uuid.New(), Name: "b"}}
	tok := e.store.addToken(t, nil, jobs...)
	w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok, from: ciAddr})
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	s := decode[Self](t, w)
	if !strings.HasPrefix(s.Name, "ci ") || s.ExpiresAt == nil || !s.ExpiresAt.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) ||
		s.NeverExpires || len(s.Jobs) != 2 ||
		s.Jobs[0].ID != jobs[0].ID || s.Jobs[1].Name != "b" || s.ClientIP == nil || *s.ClientIP != ciAddr {
		t.Fatalf("self %+v", s)
	}
	// A spoofed X-Forwarded-For from an untrusted peer: the peer is the
	// client address.
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/self", http.NoBody)
	hr.RemoteAddr = "192.0.2.50:1"
	hr.Header.Set("X-Forwarded-For", ciAddr)
	hr.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, hr)
	if s := decode[Self](t, rec); s.ClientIP == nil || *s.ClientIP != "192.0.2.50" {
		t.Fatalf("spoofed XFF used: %+v", s.ClientIP)
	}
}

// TestSelfNeverExpires: a token without expiry (D-229) authenticates and
// reports expiresAt null with neverExpires.
func TestSelfNeverExpires(t *testing.T) {
	e := newTestEnv(t, nil)
	tok := e.store.addToken(t, nil, Job{ID: uuid.New(), Name: "a"})
	e.store.mu.Lock()
	for _, ft := range e.store.tokens {
		ft.expires = nil
	}
	e.store.mu.Unlock()
	w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok, from: ciAddr})
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"expiresAt":null`) || !strings.Contains(w.Body.String(), `"neverExpires":true`) {
		t.Fatalf("self %s", w.Body.String())
	}
}

func TestTokenInQueryRefused(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	for _, p := range []string{
		"/api/v1/self?token=" + tok, "/api/v1/self?access_token=" + tok, runsPath(job.ID) + "?x=1", "/api/v1/self?",
	} {
		w := e.do(req{method: http.MethodGet, path: p, token: tok})
		if strings.Contains(p, "/runs") {
			w = e.do(req{method: http.MethodPost, path: p, token: tok})
		}
		wantError(t, w, http.StatusBadRequest, "invalid_request")
	}
	if n := e.store.callCount(); n != 0 {
		t.Fatalf("the store was called %d times", n)
	}
	if strings.Contains(e.logText(), tok) {
		t.Fatal("token logged")
	}
}

func TestBodyRules(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	for _, tc := range []struct {
		body, ctype string
		status      int
		code        string
	}{
		{`{}`, "text/plain", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{`{}`, "", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{`{"jobId":"x"}`, "application/json", http.StatusBadRequest, "invalid_request"},
		{`[]`, "application/json", http.StatusBadRequest, "invalid_request"},
		{`null`, "application/json", http.StatusBadRequest, "invalid_request"},
		{` null `, "application/json", http.StatusBadRequest, "invalid_request"},
		{`{} {}`, "application/json", http.StatusBadRequest, "invalid_request"},
		{`{`, "application/json", http.StatusBadRequest, "invalid_request"},
		{strings.Repeat(" ", 17<<10), "application/json", http.StatusRequestEntityTooLarge, "body_too_large"},
	} {
		wantError(t, e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok, body: tc.body, ctype: tc.ctype}), tc.status, tc.code)
	}
	wantError(t, e.do(req{method: http.MethodPut, path: runsPath(job.ID), token: tok}), http.StatusMethodNotAllowed, "method_not_allowed")
	wantError(t, e.do(req{method: http.MethodGet, path: "/api/v1/nothing", token: tok}), http.StatusNotFound, "not_found")
}

func TestFailedAuthThrottle(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	// Successful requests never count.
	for range 20 {
		if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok, from: ciAddr}); w.Code != http.StatusOK {
			t.Fatalf("valid request: %d", w.Code)
		}
	}
	// Requests without a credential do not count either.
	for range 20 {
		if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", from: ciAddr}); w.Code != http.StatusUnauthorized {
			t.Fatalf("no credential: %d", w.Code)
		}
	}
	bad := e.store.addTokenUnstored(t)
	for i := range 10 {
		if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: bad, from: ciAddr}); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, w.Code)
		}
	}
	calls := e.store.callCount()
	// The 11th attempt is throttled before any database call, even with a
	// valid token, and says when to retry.
	for _, token := range []string{bad, tok} {
		w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: token, from: ciAddr})
		wantError(t, w, http.StatusTooManyRequests, "rate_limited")
		if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
			t.Fatalf("Retry-After %q", ra)
		}
	}
	if e.store.callCount() != calls {
		t.Fatal("a throttled attempt reached the database")
	}
	// Other addresses are not affected.
	if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok, from: "198.51.100.3"}); w.Code != http.StatusOK {
		t.Fatalf("other address: %d", w.Code)
	}
}

func TestPerTokenLimit(t *testing.T) {
	e := newTestEnv(t, func(o *Options) {
		o.TokenLimit = httpserver.RateLimitOptions{PerSecond: 0.001, Burst: 2, MaxClients: 10}
	})
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	other := e.store.addToken(t, nil, job)
	k, err := apikey.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	wrong := apikey.Format(k.ID, make([]byte, apikey.SecretBytes))
	// Wrong secrets for the same id never drain the real token's bucket.
	for range 3 {
		e.do(req{method: http.MethodGet, path: "/api/v1/self", token: wrong, from: "198.51.100.77"})
	}
	for range 2 {
		if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok}); w.Code != http.StatusOK {
			t.Fatalf("within the burst: %d", w.Code)
		}
	}
	w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok})
	wantError(t, w, http.StatusTooManyRequests, "rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: other}); w.Code != http.StatusOK {
		t.Fatalf("another token: %d", w.Code)
	}
}

func TestDatabaseFailure(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	e.store.fail = errors.New("connection refused")
	for _, r := range []req{
		{method: http.MethodPost, path: runsPath(job.ID), token: tok},
		{method: http.MethodGet, path: "/api/v1/requests/" + uuid.NewString(), token: tok},
		{method: http.MethodGet, path: "/api/v1/self", token: tok},
	} {
		w := e.do(r)
		wantError(t, w, http.StatusServiceUnavailable, "unavailable")
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
	}
	// Database failures do not count as failed authentications.
	for range 12 {
		e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok})
	}
	e.store.fail = nil
	if w := e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok}); w.Code != http.StatusOK {
		t.Fatalf("after recovery: %d", w.Code)
	}
	if strings.Contains(e.logText(), tok) || !strings.Contains(e.logText(), "external API database call failed") {
		t.Fatalf("log:\n%s", e.logText())
	}
}

func TestLogsNeverShowTokens(t *testing.T) {
	e := newTestEnv(t, nil)
	job := Job{ID: uuid.New()}
	tok := e.store.addToken(t, nil, job)
	e.do(req{method: http.MethodPost, path: runsPath(job.ID), token: tok})
	e.do(req{method: http.MethodGet, path: "/api/v1/self", token: tok})
	e.do(req{method: http.MethodGet, path: "/api/v1/self?token=" + tok})
	e.do(req{method: http.MethodGet, path: "/api/v1/requests/" + tok, token: tok})
	logs := e.logText()
	k, err := apikey.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	secretPart := tok[len(apikey.Prefix)+apikey.IDChars+1:]
	for _, leak := range []string{tok, secretPart, "Bearer", "Authorization"} {
		if strings.Contains(logs, leak) {
			t.Fatalf("log shows %q:\n%s", leak, logs)
		}
	}
	if strings.Contains(logs, k.ID.String()) {
		t.Fatalf("log shows the token id:\n%s", logs)
	}
	if strings.Count(logs, `"msg":"http request"`) != 4 {
		t.Fatalf("access log:\n%s", logs)
	}
}

func TestNewHandlerValidates(t *testing.T) {
	if _, err := NewHandler(Options{}); err == nil {
		t.Fatal("no store accepted")
	}
	if _, err := NewHandler(Options{Store: newFakeStore(), TokenLimit: httpserver.RateLimitOptions{PerSecond: 1, Burst: 1, MaxClients: 1}}); err == nil {
		t.Fatal("no failed-auth limit accepted")
	}
}
