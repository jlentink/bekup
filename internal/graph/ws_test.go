package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/wsconn"
	"github.com/jlentink/bekup/internal/httpserver"
)

// wsSessions is the fake session store of the WebSocket tests: the
// session middleware resolves cookie values through it and the hub
// re-checks sessions against it.
type wsSessions struct {
	mu     sync.Mutex
	tokens map[string]auth.Principal
	state  map[uuid.UUID]auth.Principal
}

func (s *wsSessions) Resolve(_ context.Context, token string, _ auth.RequestMeta) (auth.Principal, error) {
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

func (s *wsSessions) Check(_ context.Context, id uuid.UUID) (auth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[id]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return st, nil
}

func (s *wsSessions) add(role auth.Role) (string, auth.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := authztest.Principal(role)
	token := uuid.NewString()
	s.tokens[token] = p
	s.state[p.SessionID] = p
	return token, p
}

// addPending adds a session waiting for its second factor (task 1.9).
func (s *wsSessions) addPending(role auth.Role) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := auth.Principal{UserID: uuid.New(), SessionID: uuid.New(), Role: role, AAL: auth.AAL1, MFAPending: true}
	token := uuid.NewString()
	s.tokens[token] = p
	s.state[p.SessionID] = p
	return token
}

// addWith adds a session whose principal mutate adjusts.
func (s *wsSessions) addWith(role auth.Role, mutate func(*auth.Principal)) (string, auth.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := authztest.Principal(role)
	mutate(&p)
	token := uuid.NewString()
	s.tokens[token] = p
	s.state[p.SessionID] = p
	return token, p
}

func (s *wsSessions) update(id uuid.UUID, mutate func(*auth.Principal)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state[id]
	mutate(&st)
	s.state[id] = st
}

func (s *wsSessions) revoke(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, id)
}

func (s *wsSessions) setRole(id uuid.UUID, r auth.Role) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state[id]
	st.Role = r
	s.state[id] = st
}

type wsEnv struct {
	url      string
	http     string
	sessions *wsSessions
	hub      *wsconn.Hub
	cookie   string
	stopHub  func()
}

func newWSEnv(t *testing.T, mutate func(*wsconn.Options), mutateWS func(*WebSocketOptions)) *wsEnv {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	sessions := &wsSessions{tokens: map[string]auth.Principal{}, state: map[uuid.UUID]auth.Principal{}}
	ho := wsconn.Options{Validator: sessions, RevalidateInterval: time.Hour, CheckTTL: time.Hour, Logger: logger}
	if mutate != nil {
		mutate(&ho)
	}
	hub, err := wsconn.NewHub(ho)
	if err != nil {
		t.Fatal(err)
	}
	ws := &WebSocketOptions{Hub: hub, AllowedOrigins: []string{testOrigin}, InitTimeout: 2 * time.Second, MaxMessageBytes: 2048}
	if mutateWS != nil {
		mutateWS(ws)
	}
	policy := authz.New(authz.Options{})
	opts := defaultOptions(logger)
	opts.Access = NewPolicyAccess(policy)
	opts.Policy = policy
	opts.WebSocket = ws
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
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := hub.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return &wsEnv{
		url:      "ws" + strings.TrimPrefix(ts.URL, "http") + "/graphql",
		http:     ts.URL + "/graphql",
		sessions: sessions,
		hub:      hub,
		cookie:   cookies.Name(),
		stopHub:  func() { cancel(); <-done },
	}
}

type dialOpts struct {
	token     string
	origin    string
	protocols []string
}

func (e *wsEnv) dial(t *testing.T, o dialOpts) (*websocket.Conn, int, error) {
	t.Helper()
	h := http.Header{}
	if o.origin == "" {
		o.origin = testOrigin
	}
	if o.origin != "-" {
		h.Set("Origin", o.origin)
	}
	if o.token != "" {
		h.Set("Cookie", e.cookie+"="+o.token)
	}
	if o.protocols == nil {
		o.protocols = []string{WebSocketSubprotocol}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, e.url, &websocket.DialOptions{HTTPHeader: h, Subprotocols: o.protocols})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Log(cerr)
			}
		}
	}
	if c != nil {
		t.Cleanup(func() { closeNow(c) })
	}
	return c, status, err
}

type wsMsg struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func send(t *testing.T, c *websocket.Conn, m any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(t.Context(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

// read returns the next message (skipping pings) or the close error.
func read(t *testing.T, c *websocket.Conn, within time.Duration) (wsMsg, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return wsMsg{}, err
		}
		var m wsMsg
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("bad message %q", b)
		}
		if m.Type == "ping" {
			continue
		}
		return m, nil
	}
}

func closeCodeOf(t *testing.T, c *websocket.Conn, within time.Duration) websocket.StatusCode {
	t.Helper()
	for {
		_, err := read(t, c, within)
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

// connect dials with token and completes connection_init.
func (e *wsEnv) connect(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	c, status, err := e.dial(t, dialOpts{token: token})
	if err != nil {
		t.Fatalf("dial: %v (status %v)", err, status)
	}
	if c.Subprotocol() != WebSocketSubprotocol {
		t.Fatalf("subprotocol %q", c.Subprotocol())
	}
	send(t, c, wsMsg{Type: "connection_init"})
	m, err := read(t, c, 2*time.Second)
	if err != nil || m.Type != "connection_ack" {
		t.Fatalf("init: %+v, %v", m, err)
	}
	return c
}

const sessionEventsQuery = `subscription { sessionEvents { kind sessionId current } }`

func subscribe(t *testing.T, c *websocket.Conn, id, query string) {
	t.Helper()
	send(t, c, map[string]any{"id": id, "type": "subscribe", "payload": map[string]any{"query": query}})
}

// closeNow drops the connection; an error means it is gone already.
func closeNow(c *websocket.Conn) {
	if err := c.CloseNow(); err != nil {
		return
	}
}

// waitSubscribed waits until the connection has an active sessionEvents
// subscriber (subscribe is asynchronous).
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

// errCodeOf extracts the first error code of an error or next message.
func errCodeOf(t *testing.T, m wsMsg) string {
	t.Helper()
	var list []struct {
		Extensions map[string]any `json:"extensions"`
	}
	switch m.Type {
	case "error":
		if err := json.Unmarshal(m.Payload, &list); err != nil {
			t.Fatalf("error payload %s", m.Payload)
		}
	case "next":
		var r struct {
			Errors []struct {
				Extensions map[string]any `json:"extensions"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(m.Payload, &r); err != nil {
			t.Fatal(err)
		}
		for _, e := range r.Errors {
			list = append(list, struct {
				Extensions map[string]any `json:"extensions"`
			}{e.Extensions})
		}
	}
	if len(list) == 0 {
		t.Fatalf("no error in %s %s", m.Type, m.Payload)
	}
	code, ok := list[0].Extensions[ExtCode].(string)
	if !ok {
		t.Fatalf("no error code in %s", m.Payload)
	}
	return code
}

func TestWSUpgradeWithCookieDeliversEvents(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, p := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	subscribe(t, c, "1", sessionEventsQuery)
	other := uuid.New()
	type result struct {
		m   wsMsg
		err error
	}
	got := make(chan result, 1)
	go func() {
		m, err := read(t, c, 5*time.Second)
		got <- result{m, err}
	}()
	// Delivered only once the resolver is listening: dispatch until then.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	var res result
wait:
	for {
		select {
		case res = <-got:
			break wait
		case <-tick.C:
			e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: other})
		}
	}
	if res.err != nil || res.m.Type != "next" || res.m.ID != "1" {
		t.Fatalf("got %+v, %v", res.m, res.err)
	}
	var r struct {
		Data struct {
			SessionEvents struct {
				Kind      string  `json:"kind"`
				SessionID *string `json:"sessionId"`
				Current   bool    `json:"current"`
			} `json:"sessionEvents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.m.Payload, &r); err != nil {
		t.Fatal(err)
	}
	ev := r.Data.SessionEvents
	if ev.Kind != "REVOKED" || ev.SessionID == nil || *ev.SessionID != other.String() || ev.Current {
		t.Fatalf("event %+v", ev)
	}
}

func TestWSUpgradeRejections(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	pending := e.sessions.addPending(auth.RoleAdmin)
	enroll, _ := e.sessions.addWith(auth.RoleAdmin, func(p *auth.Principal) { p.EnrollmentRequired = true })
	cases := []struct {
		name   string
		o      dialOpts
		status int
	}{
		{"no cookie", dialOpts{}, http.StatusUnauthorized},
		{"login waiting for its second factor", dialOpts{token: pending}, http.StatusUnauthorized},
		{"2FA enrollment required", dialOpts{token: enroll}, http.StatusForbidden},
		{"unknown cookie", dialOpts{token: "bogus"}, http.StatusUnauthorized},
		{"cross-origin", dialOpts{token: token, origin: "https://evil.example"}, http.StatusForbidden},
		{"no origin", dialOpts{token: token, origin: "-"}, http.StatusForbidden},
		{"legacy subscriptions-transport-ws", dialOpts{token: token, protocols: []string{"graphql-ws"}}, http.StatusBadRequest},
		{"no subprotocol", dialOpts{token: token, protocols: []string{}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, status, err := e.dial(t, tc.o)
			if err == nil {
				t.Fatalf("upgrade accepted (subprotocol %q)", c.Subprotocol())
			}
			if status != tc.status {
				t.Fatalf("status %d, want %d", status, tc.status)
			}
		})
	}
}

func TestWSInitPayloadCredentialsAreRejected(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	// Without a cookie, credentials in the payload cannot even get a socket.
	if _, status, err := e.dial(t, dialOpts{}); err == nil || status != http.StatusUnauthorized {
		t.Fatalf("upgrade without cookie: %v %d", err, status)
	}
	// With a cookie, an init payload is refused rather than interpreted.
	c, _, err := e.dial(t, dialOpts{token: token})
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, map[string]any{"type": "connection_init", "payload": map[string]any{"Authorization": "Bearer " + token}})
	if code := closeCodeOf(t, c, 2*time.Second); code != wsconn.CloseBadInit {
		t.Fatalf("close code %d, want %d", code, wsconn.CloseBadInit)
	}
}

func TestWSConnectionCap(t *testing.T) {
	e := newWSEnv(t, func(o *wsconn.Options) { o.MaxPerUser = 1; o.MaxTotal = 2 }, nil)
	a, _ := e.sessions.add(auth.RoleUser)
	b, _ := e.sessions.add(auth.RoleUser)
	c := e.connect(t, a)
	if _, status, err := e.dial(t, dialOpts{token: a}); err == nil || status != http.StatusTooManyRequests {
		t.Fatalf("second connection of a user: %v %d", err, status)
	}
	e.connect(t, b)
	x, _ := e.sessions.add(auth.RoleAdmin)
	if _, status, err := e.dial(t, dialOpts{token: x}); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("connection over the global cap: %v %d", err, status)
	}
	// Closing frees the slot.
	if err := c.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 1 })
	e.connect(t, a)
}

func TestWSRevokedSessionClosesImmediately(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, p := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	subscribe(t, c, "1", sessionEventsQuery)
	start := time.Now()
	e.sessions.revoke(p.SessionID)
	e.hub.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: p.SessionID})
	if code := closeCodeOf(t, c, 2*time.Second); code != wsconn.CloseSessionEnded {
		t.Fatalf("close code %d, want 4401", code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("closed after %s", d)
	}
}

func TestWSRevokedSessionClosesPeriodically(t *testing.T) {
	e := newWSEnv(t, func(o *wsconn.Options) { o.RevalidateInterval = time.Second }, nil)
	token, p := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	e.sessions.revoke(p.SessionID) // no event: the fallback must notice
	if code := closeCodeOf(t, c, 3*time.Second); code != wsconn.CloseSessionEnded {
		t.Fatalf("close code %d, want 4401", code)
	}
}

func TestWSRoleChangeCloses(t *testing.T) {
	e := newWSEnv(t, func(o *wsconn.Options) { o.RevalidateInterval = time.Second }, nil)
	token, p := e.sessions.add(auth.RoleAdmin)
	c := e.connect(t, token)
	e.sessions.setRole(p.SessionID, auth.RoleUser)
	if code := closeCodeOf(t, c, 3*time.Second); code != wsconn.CloseAccessChanged {
		t.Fatalf("close code %d, want 4403", code)
	}
}

// M1 review L2: the periodic re-check rebuilds the principal with the
// enrollment check; a new restriction closes the socket with 4403.
func TestWSRestrictionChangeCloses(t *testing.T) {
	e := newWSEnv(t, func(o *wsconn.Options) { o.RevalidateInterval = time.Second }, nil)
	token, p := e.sessions.add(auth.RoleAdmin)
	c := e.connect(t, token)
	e.sessions.update(p.SessionID, func(np *auth.Principal) { np.EnrollmentRequired = true })
	if code := closeCodeOf(t, c, 3*time.Second); code != wsconn.CloseAccessChanged {
		t.Fatalf("close code %d, want 4403", code)
	}
}

// M1 code review: an upgrade after the hub stopped (shutdown) is refused
// with 503 instead of opening a socket nobody will close.
func TestWSUpgradeAfterShutdown(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	e.stopHub()
	if _, status, err := e.dial(t, dialOpts{token: token}); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("upgrade after shutdown: %v %d, want 503", err, status)
	}
}

func TestWSInitTimeout(t *testing.T) {
	e := newWSEnv(t, nil, func(o *WebSocketOptions) { o.InitTimeout = 200 * time.Millisecond })
	token, _ := e.sessions.add(auth.RoleUser)
	c, _, err := e.dial(t, dialOpts{token: token})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code := closeCodeOf(t, c, 3*time.Second)
	if code != websocket.StatusProtocolError {
		t.Fatalf("close code %d", code)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("closed after %s", d)
	}
	waitFor(t, func() bool { total, _ := e.hub.Count(uuid.Nil); return total == 0 })
}

func TestWSMessageSizeLimit(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	subscribe(t, c, "1", sessionEventsQuery+strings.Repeat(" ", 4096))
	if code := closeCodeOf(t, c, 2*time.Second); code != websocket.StatusMessageTooBig {
		t.Fatalf("close code %d, want 1009", code)
	}
}

func TestWSOnlySubscriptions(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	subscribe(t, c, "q", `{ serverInfo { version } }`)
	m, err := read(t, c, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if code := errCodeOf(t, m); code != "FORBIDDEN" {
		t.Fatalf("query over WebSocket: %s", code)
	}
}

func TestWSSubscriptionCap(t *testing.T) {
	e := newWSEnv(t, func(o *wsconn.Options) { o.MaxSubscriptions = 1 }, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	c := e.connect(t, token)
	subscribe(t, c, "1", sessionEventsQuery)
	subscribe(t, c, "2", sessionEventsQuery)
	m, err := read(t, c, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Subscribe messages are handled concurrently: either one may lose.
	if (m.ID != "1" && m.ID != "2") || errCodeOf(t, m) != "RATE_LIMITED" {
		t.Fatalf("second subscription: %+v", m)
	}
}

func TestSubscriptionOverPOSTRejected(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	token, _ := e.sessions.add(auth.RoleUser)
	body, err := json.Marshal(map[string]any{"query": sessionEventsQuery})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.http, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Cookie", e.cookie+"="+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, resp)
	var r gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if errorCode(r) != "FORBIDDEN" {
		t.Fatalf("subscription over POST: %+v", r)
	}
}

func TestPlainGETAndUpgradeHeaderOnPOST(t *testing.T) {
	e := newWSEnv(t, nil, nil)
	get, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.http, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET without upgrade: %d", resp.StatusCode)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.http, strings.NewReader(`{"query":"{ serverInfo { version } }"}`))
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

func TestWebSocketOptionsValidate(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	opts := defaultOptions(logger)
	opts.WebSocket = &WebSocketOptions{}
	if _, err := New(opts); err == nil {
		t.Fatal("WebSocket without hub accepted")
	}
}

func TestOriginHosts(t *testing.T) {
	got := originHosts([]string{"https://bekup.example.com", "http://localhost:5173", "bogus", "https://*.evil"})
	want := []string{"bekup.example.com", "localhost:5173"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v", got)
	}
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
}
