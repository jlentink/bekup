//go:build integration

package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

const streamTestOrigin = "http://127.0.0.1:8080"

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
	req.Header.Set("Origin", streamTestOrigin)
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
		if c.Name == auth.CookiePolicyFor(streamTestOrigin).Name() && c.Value != "" {
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
		Email: email, DisplayName: "Stream User", PasswordHash: &hash, Role: sqlc.UserRoleUser,
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

// streamResp is a response whose body the test cleanup closes.
type streamResp struct {
	StatusCode int
	Header     http.Header
	Body       io.Reader
}

// openStream opens an accountEvents event stream with the cookie and
// returns the response (the caller checks the status).
func openStream(t *testing.T, addr, cookie string) *streamResp {
	t.Helper()
	body := `{"query":"subscription { accountEvents { session { kind current } } }"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/graphql", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", streamTestOrigin)
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Log(err)
		}
	})
	return &streamResp{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}
}

// subscribeSessionEvents opens the stream and fails unless it started.
func subscribeSessionEvents(t *testing.T, addr, cookie string) *streamResp {
	t.Helper()
	resp := openStream(t, addr, cookie)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return resp
}

// endCode reads the stream until its complete event and returns the code
// of the final error event ("" if none).
func endCode(t *testing.T, resp *streamResp, within time.Duration) string {
	t.Helper()
	type result struct {
		code     string
		complete bool
	}
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		code, event := "", ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				if event == "complete" {
					ch <- result{code, true}
					return
				}
			case strings.HasPrefix(line, "data:") && event == "next":
				var r struct {
					Errors []struct {
						Extensions map[string]any `json:"extensions"`
					} `json:"errors"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &r) == nil && len(r.Errors) > 0 {
					if c, ok := r.Errors[0].Extensions["code"].(string); ok {
						code = c
					}
				}
			}
		}
		ch <- result{code, false}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	select {
	case r := <-ch:
		if !r.complete {
			t.Fatalf("stream closed without complete (code %q)", r.code)
		}
		return r.code
	case <-ctx.Done():
		t.Fatal("stream not ended in time")
		return ""
	}
}

// TestLogoutEndsStreamOnOtherInstance runs two serve instances on one
// database: a logout on instance A ends the event stream on instance B
// through LISTEN/NOTIFY, far below the periodic re-check (30s).
func TestLogoutEndsStreamOnOtherInstance(t *testing.T) {
	d := dbtest.Migrated(t)
	createUser(t, d, "stream@example.com", "blue-mountain-river-42")
	a := startApp(t, d)
	b := startApp(t, d)
	cookie := login(t, a, "stream@example.com", "blue-mountain-river-42")
	c := subscribeSessionEvents(t, b, cookie)

	start := time.Now()
	if out, _ := gqlPost(t, a, cookie, `mutation { logout }`); out["errors"] != nil {
		t.Fatalf("logout: %v", out)
	}
	if code := endCode(t, c, 5*time.Second); code != "UNAUTHENTICATED" {
		t.Fatalf("end code %q, want UNAUTHENTICATED", code)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stream ended after %s", d)
	}
	// The ended session cannot open a new stream.
	if resp := openStream(t, b, cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stream with a logged-out cookie: %d", resp.StatusCode)
	}
}

// TestRevokeSessionEndsStream: revoking a session from another session
// of the same user ends the revoked session's stream (same instance).
func TestRevokeSessionEndsStream(t *testing.T) {
	d := dbtest.Migrated(t)
	createUser(t, d, "stream2@example.com", "blue-mountain-river-42")
	a := startApp(t, d)
	victim := login(t, a, "stream2@example.com", "blue-mountain-river-42")
	other := login(t, a, "stream2@example.com", "blue-mountain-river-42")
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
	if code := endCode(t, c, 5*time.Second); code != "UNAUTHENTICATED" {
		t.Fatalf("end code %q, want UNAUTHENTICATED", code)
	}
}
