//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

const wsTestOrigin = "http://127.0.0.1:8080"

// startApp runs a serve instance on d until the test ends.
func startApp(t *testing.T, d dbtest.DB) string {
	t.Helper()
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.New(ctx, options(t, d.AppURL, logs))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop")
		}
	})
	// Session events from other instances arrive once the LISTEN
	// connection is up and the hub is subscribed to the bus.
	waitUntil(t, 10*time.Second, func() bool { return app.SubscriptionsReady(a) })
	return a.Addr().String()
}

// waitUntil polls cond until it holds or within passes.
func waitUntil(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// gqlPost runs a GraphQL request with the session cookie and returns the
// response and any session cookie it sets.
func gqlPost(t *testing.T, addr, cookie, query string) (map[string]any, string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/graphql", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", wsTestOrigin)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	set := ""
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookiePolicyFor(wsTestOrigin).Name() && c.Value != "" {
			set = c.Name + "=" + c.Value
		}
	}
	return out, set
}

func createUser(t *testing.T, d dbtest.DB, email, password string) {
	t.Helper()
	h, err := auth.NewHasher(auth.HasherOptions{Params: auth.MinArgon2Params, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := h.Hash(t.Context(), password)
	if err != nil {
		t.Fatal(err)
	}
	pool := d.AppPool(t)
	if _, err := sqlc.New(pool).InsertUser(t.Context(), sqlc.InsertUserParams{
		Email: email, DisplayName: "WS User", PasswordHash: &hash, Role: sqlc.UserRoleUser,
	}); err != nil {
		t.Fatal(err)
	}
}

func login(t *testing.T, addr, email, password string) string {
	t.Helper()
	out, cookie := gqlPost(t, addr, "", `mutation { login(email: "`+email+`", password: "`+password+`") { user { id } } }`)
	if cookie == "" {
		t.Fatalf("login failed: %v", out)
	}
	return cookie
}

// dialWS upgrades with the cookie and returns the HTTP status.
func dialWS(t *testing.T, addr, cookie string) (*websocket.Conn, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": {wsTestOrigin}, "Cookie": {cookie}}
	c, resp, err := websocket.Dial(ctx, "ws://"+addr+"/graphql", &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"graphql-transport-ws"}})
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
		t.Cleanup(func() {
			if err := c.CloseNow(); err != nil {
				return
			}
		})
	}
	return c, status, err
}

// subscribeSessionEvents opens a subscription socket with the cookie.
func subscribeSessionEvents(t *testing.T, addr, cookie string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, status, err := dialWS(t, addr, cookie)
	if err != nil {
		t.Fatalf("dial: %v (%d)", err, status)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"connection_init"}`)); err != nil {
		t.Fatal(err)
	}
	_, b, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(b), "connection_ack") {
		t.Fatalf("init: %s %v", b, err)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"id":"1","type":"subscribe","payload":{"query":"subscription { sessionEvents { kind current } }"}}`)); err != nil {
		t.Fatal(err)
	}
	return c
}

func closeCode(t *testing.T, c *websocket.Conn, within time.Duration) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

// TestLogoutClosesSocketOnOtherInstance runs two serve instances on one
// database: a logout on instance A closes the subscription socket on
// instance B through LISTEN/NOTIFY, far below the periodic re-check (30s).
func TestLogoutClosesSocketOnOtherInstance(t *testing.T) {
	d := dbtest.Migrated(t)
	createUser(t, d, "ws@example.com", "blue-mountain-river-42")
	a := startApp(t, d)
	b := startApp(t, d)
	cookie := login(t, a, "ws@example.com", "blue-mountain-river-42")
	c := subscribeSessionEvents(t, b, cookie)

	start := time.Now()
	if out, _ := gqlPost(t, a, cookie, `mutation { logout }`); out["errors"] != nil {
		t.Fatalf("logout: %v", out)
	}
	if code := closeCode(t, c, 5*time.Second); code != 4401 {
		t.Fatalf("close code %d, want 4401", code)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("socket closed after %s", d)
	}
	// The ended session cannot open a new socket.
	if _, status, err := dialWS(t, b, cookie); err == nil || status != http.StatusUnauthorized {
		t.Fatalf("upgrade with a logged-out cookie: %v (%d)", err, status)
	}
}

// TestRevokeSessionClosesSocket: revoking a session from another session
// of the same user closes the revoked session's socket (same instance).
func TestRevokeSessionClosesSocket(t *testing.T) {
	d := dbtest.Migrated(t)
	createUser(t, d, "ws2@example.com", "blue-mountain-river-42")
	a := startApp(t, d)
	victim := login(t, a, "ws2@example.com", "blue-mountain-river-42")
	other := login(t, a, "ws2@example.com", "blue-mountain-river-42")
	c := subscribeSessionEvents(t, a, victim)

	out, _ := gqlPost(t, a, other, `{ mySessions { id current } }`)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data struct {
			MySessions []struct {
				ID      string `json:"id"`
				Current bool   `json:"current"`
			} `json:"mySessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	var victimID string
	for _, s := range list.Data.MySessions {
		if !s.Current {
			victimID = s.ID
		}
	}
	if victimID == "" {
		t.Fatalf("sessions: %v", out)
	}
	if out, _ := gqlPost(t, a, other, `mutation { revokeSession(id: "`+victimID+`") }`); out["errors"] != nil {
		t.Fatalf("revokeSession: %v", out)
	}
	if code := closeCode(t, c, 5*time.Second); code != 4401 {
		t.Fatalf("close code %d, want 4401", code)
	}
}
