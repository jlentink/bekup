package graph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
)

// fakeSessions is the fake session store of the stream tests: the
// session middleware resolves cookie values through it and the hub
// re-checks sessions against it.
type fakeSessions struct {
	mu     sync.Mutex
	tokens map[string]auth.Principal
	state  map[uuid.UUID]auth.Principal
}

func (s *fakeSessions) Resolve(_ context.Context, token string, _ auth.RequestMeta) (auth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.tokens[token]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	if _, ok := s.state[p.SessionID]; !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return p, nil
}

func (s *fakeSessions) Check(_ context.Context, id uuid.UUID) (auth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[id]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return st, nil
}

// addWith adds a session whose principal mutate adjusts (nil: none).
func (s *fakeSessions) addWith(role auth.Role, mutate func(*auth.Principal)) (string, auth.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := authztest.Principal(role)
	if mutate != nil {
		mutate(&p)
	}
	token := uuid.NewString()
	s.tokens[token] = p
	s.state[p.SessionID] = p
	return token, p
}

func (s *fakeSessions) add(role auth.Role) (string, auth.Principal) { return s.addWith(role, nil) }

// addToken adds a second cookie for the session of p.
func (s *fakeSessions) addToken(p auth.Principal) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := uuid.NewString()
	s.tokens[token] = p
	return token
}

func (s *fakeSessions) update(id uuid.UUID, mutate func(*auth.Principal)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state[id]
	mutate(&st)
	s.state[id] = st
}

func (s *fakeSessions) revoke(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, id)
}

type streamEnv struct {
	url      string
	sessions *fakeSessions
	hub      *streams.Hub
	cookie   string
	stopHub  func()
}

type streamEnvOpts struct {
	hub       func(*streams.Options)
	keepalive time.Duration
	// timeouts sets short server read/write timeouts (deadline lifting).
	timeouts time.Duration
}

func newStreamEnv(t *testing.T, eo streamEnvOpts) *streamEnv {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	sessions := &fakeSessions{tokens: map[string]auth.Principal{}, state: map[uuid.UUID]auth.Principal{}}
	ho := streams.Options{Validator: sessions, RevalidateInterval: time.Hour, CheckTTL: time.Hour, Logger: logger}
	if eo.hub != nil {
		eo.hub(&ho)
	}
	hub, err := streams.NewHub(ho)
	if err != nil {
		t.Fatal(err)
	}
	policy := authz.New(authz.Options{})
	opts := defaultOptions(logger)
	opts.Access = NewPolicyAccess(policy)
	opts.Policy = policy
	opts.Streams = &StreamOptions{Hub: hub, AllowedOrigins: []string{testOrigin}, KeepaliveInterval: eo.keepalive}
	gql, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	cookies := auth.CookiePolicyFor(testOrigin)
	srv, err := httpserver.New(httpserver.Options{
		BaseURL:      testOrigin,
		MaxBodyBytes: 1 << 20,
		RateLimit:    httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		Logger:       logger,
		Ready:        func(context.Context) error { return nil },
		GraphQL:      gql,
		Sessions:     &httpserver.SessionOptions{Resolver: sessions, Cookies: cookies},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv.Handler())
	if eo.timeouts > 0 {
		ts.Config.ReadTimeout = eo.timeouts
		ts.Config.WriteTimeout = eo.timeouts
	}
	ts.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := hub.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	// The hub must stop first: it ends the open streams that ts.Close
	// would otherwise wait for.
	t.Cleanup(func() { stop(); ts.Close() })
	return &streamEnv{url: ts.URL + "/graphql", sessions: sessions, hub: hub, cookie: cookies.Name(), stopHub: stop}
}

type reqOpts struct {
	token  string
	origin string // "" testOrigin, "-" none
	accept string // "" event stream, "-" none
	query  string // "" sessionEventsQuery
}

const sessionEventsQuery = `subscription { accountEvents { session { kind sessionId current } } }`

// streamResp is a response whose body the test cleanup closes.
type streamResp struct {
	StatusCode int
	Header     http.Header
	Body       io.Reader
}

func (e *streamEnv) post(t *testing.T, o reqOpts) *streamResp {
	t.Helper()
	if o.query == "" {
		o.query = sessionEventsQuery
	}
	body, err := json.Marshal(map[string]any{"query": o.query})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	switch o.accept {
	case "":
		req.Header.Set("Accept", EventStreamMediaType)
	case "-":
	default:
		req.Header.Set("Accept", o.accept)
	}
	switch o.origin {
	case "":
		req.Header.Set("Origin", testOrigin)
	case "-":
	default:
		req.Header.Set("Origin", o.origin)
	}
	if o.token != "" {
		req.Header.Set("Cookie", e.cookie+"="+o.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeBody(t, resp) })
	return &streamResp{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}
}

// sseEvent is one parsed event; comment is set for comment lines.
type sseEvent struct {
	event   string
	data    string
	comment bool
}

// sseReader parses a graphql-sse response in the background.
type sseReader struct {
	ch chan sseEvent
}

func newSSEReader(resp *streamResp) *sseReader {
	r := &sseReader{ch: make(chan sseEvent, 64)}
	go func() {
		defer close(r.ch)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.event != "" || ev.data != "" {
					r.ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ":"):
				r.ch <- sseEvent{comment: true, data: strings.TrimPrefix(line, ":")}
			case strings.HasPrefix(line, "event:"):
				ev.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}()
	return r
}

// next returns the next non-comment event; ok is false when the stream
// closed or within passed.
func (r *sseReader) next(t *testing.T, within time.Duration) (sseEvent, bool) {
	t.Helper()
	timer := time.NewTimer(within)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-r.ch:
			if !ok {
				return sseEvent{}, false
			}
			if ev.comment {
				continue
			}
			return ev, true
		case <-timer.C:
			return sseEvent{}, false
		}
	}
}

// open opens a stream with token and checks the response headers.
func (e *streamEnv) open(t *testing.T, token string) *sseReader {
	t.Helper()
	resp := e.post(t, reqOpts{token: token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	h := resp.Header
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers %v", h)
	}
	if h.Get("Content-Security-Policy") != httpserver.ContentSecurityPolicy {
		t.Fatal("security headers missing")
	}
	return newSSEReader(resp)
}

// endCode waits for the final error event and the complete event and
// returns the error code.
func endCode(t *testing.T, r *sseReader, within time.Duration) string {
	t.Helper()
	code := ""
	for {
		ev, ok := r.next(t, within)
		if !ok {
			t.Fatalf("stream closed without complete (code %q)", code)
		}
		switch ev.event {
		case "complete":
			if _, more := r.next(t, time.Second); more {
				t.Fatal("event after complete")
			}
			return code
		case "next":
			var resp gqlResponse
			if err := json.Unmarshal([]byte(ev.data), &resp); err != nil {
				t.Fatalf("bad next data %q", ev.data)
			}
			if c := errorCode(resp); c != "" {
				code = c
			}
		}
	}
}

func TestStreamDeliversEvents(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, p := e.sessions.add(auth.RoleUser)
	r := e.open(t, token)
	other := uuid.New()
	got := make(chan sseEvent, 1)
	go func() {
		ev, _ := r.next(t, 5*time.Second)
		got <- ev
	}()
	// Delivered only once the resolver is listening: dispatch until then.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	var ev sseEvent
wait:
	for {
		select {
		case ev = <-got:
			break wait
		case <-tick.C:
			e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: other})
		}
	}
	if ev.event != "next" {
		t.Fatalf("got %+v", ev)
	}
	var resp struct {
		Data struct {
			AccountEvents struct {
				Session struct {
					Kind      string  `json:"kind"`
					SessionID *string `json:"sessionId"`
					Current   bool    `json:"current"`
				} `json:"session"`
				Notification *struct{} `json:"notification"`
			} `json:"accountEvents"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(ev.data), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.AccountEvents.Notification != nil {
		t.Fatalf("session event with a notification: %s", ev.data)
	}
	se := resp.Data.AccountEvents.Session
	if se.Kind != "REVOKED" || se.SessionID == nil || *se.SessionID != other.String() || se.Current {
		t.Fatalf("event %+v", se)
	}
}

func TestStreamRejections(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, _ := e.sessions.add(auth.RoleUser)
	pending, _ := e.sessions.addWith(auth.RoleAdmin, func(p *auth.Principal) { p.AAL = auth.AAL1; p.MFAPending = true })
	enroll, _ := e.sessions.addWith(auth.RoleAdmin, func(p *auth.Principal) { p.EnrollmentRequired = true })
	cases := []struct {
		name   string
		o      reqOpts
		status int
	}{
		{"no cookie", reqOpts{}, http.StatusUnauthorized},
		{"login waiting for its second factor", reqOpts{token: pending}, http.StatusUnauthorized},
		{"2FA enrollment required", reqOpts{token: enroll}, http.StatusForbidden},
		{"unknown cookie", reqOpts{token: "bogus"}, http.StatusUnauthorized},
		{"cross-origin", reqOpts{token: token, origin: "https://evil.example"}, http.StatusForbidden},
		{"no origin", reqOpts{token: token, origin: "-"}, http.StatusForbidden},
		{"query as a stream", reqOpts{token: token, query: `{ serverInfo { version } }`}, http.StatusForbidden},
		{"mutation as a stream", reqOpts{token: token, query: `mutation { logout }`}, http.StatusForbidden},
		{"invalid subscription", reqOpts{token: token, query: `subscription { nope }`}, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := e.post(t, tc.o)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Fatal("a refused request opened a stream")
			}
		})
	}
	if total, _ := e.hub.Count(uuid.Nil); total != 0 {
		t.Fatalf("%d streams left after refusals", total)
	}
}

func TestQueryOverStreamIsForbidden(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, _ := e.sessions.add(auth.RoleUser)
	resp := e.post(t, reqOpts{token: token, query: `{ serverInfo { version } }`})
	var r gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if errorCode(r) != "FORBIDDEN" || r.Data != nil {
		t.Fatalf("query as a stream: %+v", r)
	}
}

func TestSubscriptionOverPOSTRejected(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, _ := e.sessions.add(auth.RoleUser)
	for _, accept := range []string{"-", "application/json"} {
		resp := e.post(t, reqOpts{token: token, accept: accept})
		var r gqlResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			t.Fatal(err)
		}
		if errorCode(r) != "FORBIDDEN" {
			t.Fatalf("subscription over plain POST (Accept %q): %+v", accept, r)
		}
	}
}

func TestStreamCaps(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{hub: func(o *streams.Options) { o.MaxPerSession = 1; o.MaxPerUser = 2; o.MaxTotal = 3 }})
	a, pa := e.sessions.add(auth.RoleUser)
	e.open(t, a)
	if resp := e.post(t, reqOpts{token: a}); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("second stream of a session: %d", resp.StatusCode)
	}
	// A second session of the same user (another browser): allowed once.
	a2, pa2 := e.sessions.addWith(auth.RoleUser, func(p *auth.Principal) { p.UserID = pa.UserID })
	e.open(t, a2)
	a3, _ := e.sessions.addWith(auth.RoleUser, func(p *auth.Principal) { p.UserID = pa.UserID })
	if resp := e.post(t, reqOpts{token: a3}); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third stream of a user: %d", resp.StatusCode)
	}
	b, _ := e.sessions.add(auth.RoleUser)
	e.open(t, b)
	x, _ := e.sessions.add(auth.RoleAdmin)
	if resp := e.post(t, reqOpts{token: x}); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stream over the global cap: %d", resp.StatusCode)
	}
	// Ending a stream frees its slot.
	e.sessions.revoke(pa2.SessionID)
	e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: pa2.UserID, SessionID: pa2.SessionID})
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 2 })
	e.open(t, x)
}

func TestStreamClientDisconnectFreesSlot(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, _ := e.sessions.add(auth.RoleUser)
	resp := e.post(t, reqOpts{token: token})
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
	c, ok := resp.Body.(io.Closer)
	if !ok {
		t.Fatal("body is not closable")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 0 })
}

func TestStreamRevokedSessionEndsImmediately(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, p := e.sessions.add(auth.RoleUser)
	r := e.open(t, token)
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
	start := time.Now()
	e.sessions.revoke(p.SessionID)
	e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: p.SessionID})
	if code := endCode(t, r, 2*time.Second); code != "UNAUTHENTICATED" {
		t.Fatalf("end code %q, want UNAUTHENTICATED", code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("ended after %s", d)
	}
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 0 })
	// The ended session cannot open a new stream.
	if resp := e.post(t, reqOpts{token: token}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stream with an ended session: %d", resp.StatusCode)
	}
}

func TestStreamRotationEndsForReconnect(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, p := e.sessions.add(auth.RoleUser)
	r := e.open(t, token)
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
	e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRotated, UserID: p.UserID, SessionID: p.SessionID})
	if code := endCode(t, r, 2*time.Second); code != "SESSION_ROTATED" {
		t.Fatalf("end code %q, want SESSION_ROTATED", code)
	}
	// The client reconnects (here with a second cookie of the session).
	e.open(t, e.sessions.addToken(p))
}

func TestStreamPeriodicCheck(t *testing.T) {
	cases := []struct {
		name   string
		change func(s *fakeSessions, p auth.Principal)
		code   string
	}{
		{"revoked", func(s *fakeSessions, p auth.Principal) { s.revoke(p.SessionID) }, "UNAUTHENTICATED"},
		{"role changed", func(s *fakeSessions, p auth.Principal) {
			s.update(p.SessionID, func(np *auth.Principal) { np.Role = auth.RoleUser })
		}, "FORBIDDEN"},
		// M1 review L2: the principal is rebuilt, restrictions included.
		{"enrollment now required", func(s *fakeSessions, p auth.Principal) {
			s.update(p.SessionID, func(np *auth.Principal) { np.EnrollmentRequired = true })
		}, "FORBIDDEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No events at all: only the periodic check notices.
			e := newStreamEnv(t, streamEnvOpts{hub: func(o *streams.Options) { o.RevalidateInterval = time.Second }})
			token, p := e.sessions.add(auth.RoleAdmin)
			r := e.open(t, token)
			waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
			tc.change(e.sessions, p)
			if code := endCode(t, r, 3*time.Second); code != tc.code {
				t.Fatalf("end code %q, want %s", code, tc.code)
			}
		})
	}
}

func TestStreamKeepalive(t *testing.T) {
	// Server timeouts far below the stream's life: the deadlines are
	// lifted per stream (an expired read deadline would also cancel the
	// request context).
	e := newStreamEnv(t, streamEnvOpts{keepalive: 100 * time.Millisecond, timeouts: 300 * time.Millisecond})
	token, _ := e.sessions.add(auth.RoleUser)
	r := e.open(t, token)
	deadline := time.After(3 * time.Second)
	keepalives := 0
	start := time.Now()
	for time.Since(start) < 1200*time.Millisecond {
		select {
		case ev, ok := <-r.ch:
			if !ok {
				t.Fatalf("stream closed after %s", time.Since(start))
			}
			if ev.comment && strings.TrimSpace(ev.data) == "keepalive" {
				keepalives++
			}
		case <-deadline:
			t.Fatal("timeout")
		}
	}
	if keepalives < 5 {
		t.Fatalf("%d keepalives in 1.2s at 100ms", keepalives)
	}
}

// Shutdown ends the open streams with UNAVAILABLE, and a stream opened
// after the hub stopped is refused with 503 instead of staying open.
func TestStreamShutdown(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	token, _ := e.sessions.add(auth.RoleUser)
	r := e.open(t, token)
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
	e.stopHub()
	if code := endCode(t, r, 2*time.Second); code != "UNAVAILABLE" {
		t.Fatalf("end code %q, want UNAVAILABLE", code)
	}
	resp := e.post(t, reqOpts{token: token})
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("stream after shutdown: %d, want 503", resp.StatusCode)
	}
}

func TestPlainGETAndUpgradeHeaderOnPOST(t *testing.T) {
	e := newStreamEnv(t, streamEnvOpts{})
	get, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	get.Header.Set("Accept", EventStreamMediaType)
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.url, strings.NewReader(`{"query":"{ serverInfo { version } }"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Upgrade", "websocket")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST with Upgrade: %d", resp.StatusCode)
	}
}

func TestStreamsDisabled(t *testing.T) {
	env := newEnv(t, nil)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql", strings.NewReader(`{"query":"subscription { accountEvents { session { kind } } }"}`))
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", EventStreamMediaType)
	w := httptest.NewRecorder()
	env.h.ServeHTTP(w, r)
	if w.Code != http.StatusNotAcceptable {
		t.Fatalf("status %d, want 406", w.Code)
	}
}

func TestStreamOptionsValidate(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	opts := defaultOptions(logger)
	opts.Streams = &StreamOptions{}
	if _, err := New(opts); err == nil {
		t.Fatal("Streams without hub accepted")
	}
}

func TestAcceptsEventStream(t *testing.T) {
	cases := map[string]bool{
		"":                                    false,
		"application/json":                    false,
		"text/event-stream":                   true,
		"application/json, text/event-stream": true,
		"text/event-stream;q=0.9":             true,
		"text/event-streams":                  false,
		"*/*":                                 false,
	}
	for accept, want := range cases {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql", http.NoBody)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		if got := acceptsEventStream(r); got != want {
			t.Errorf("Accept %q: %v, want %v", accept, got, want)
		}
	}
}

// waitFor polls cond until it holds (2s at most).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
}
