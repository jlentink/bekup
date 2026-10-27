//go:build integration

package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
)

const (
	testBaseURL  = "https://bekup.test"
	testPassword = "blue-mountain-river-42"
)

type gqlResp struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func (r gqlResp) code() string {
	if len(r.Errors) == 0 {
		return ""
	}
	if c, ok := r.Errors[0].Extensions["code"].(string); ok {
		return c
	}
	return ""
}

type authEnv struct {
	t       *testing.T
	h       http.Handler
	pool    *pgxpool.Pool
	d       dbtest.DB
	auditor *audit.Auditor
	limited atomic.Bool
	hasher  *auth.Hasher
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &authEnv{t: t, d: d, pool: d.AppPool(t)}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	svc, err := auth.NewService(auth.ServiceOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors(),
		Lockout: auth.LockoutPolicy{Threshold: 3, Base: time.Minute, Max: time.Hour},
		LoginLimiter: func(netip.Addr) (bool, time.Duration) {
			return !e.limited.Load(), 10 * time.Second
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return e
}

func (e *authEnv) user(email string, role sqlc.UserRole) sqlc.User {
	e.t.Helper()
	h, err := e.hasher.Hash(e.t.Context(), testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := sqlc.New(e.pool).InsertUser(e.t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: "Pat", PasswordHash: &h, Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// do posts a GraphQL request from the base-URL origin (or origin, if set)
// with the session cookie value (if any) and returns the decoded response
// and the raw recorder.
func (e *authEnv) do(cookie, origin, query string, vars map[string]any) (gqlResp, *httptest.ResponseRecorder) {
	e.t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(e.t.Context(), http.MethodPost, "/graphql", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.50:4000"
	req.Header.Set("Content-Type", "application/json")
	if origin == "" {
		origin = testBaseURL
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("User-Agent", "integration-test")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var resp gqlResp
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			e.t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
	}
	return resp, w
}

// sessionCookie returns the session cookie set by w ("" if none; "-" if
// it was cleared).
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range (&http.Response{Header: w.Header()}).Cookies() {
		if c.Name != auth.SessionCookieName {
			continue
		}
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
			t.Fatalf("session cookie attributes: %+v", c)
		}
		if c.MaxAge < 0 {
			return "-"
		}
		return c.Value
	}
	return ""
}

const (
	loginMutation  = `mutation($e: String!, $p: String!) { login(email: $e, password: $p) { user { id email role } secondFactorRequired availableFactors } }`
	meQuery        = `{ me { id email displayName role } }`
	sessionsQuery  = `{ mySessions { id current aal userAgent ip } }`
	logoutMutation = `mutation { logout }`
)

func (e *authEnv) login(email, password string) (gqlResp, string) {
	e.t.Helper()
	resp, w := e.do("", "", loginMutation, map[string]any{"e": email, "p": password})
	return resp, sessionCookie(e.t, w)
}

func (e *authEnv) mustLogin(email string) string {
	e.t.Helper()
	resp, c := e.login(email, testPassword)
	if len(resp.Errors) > 0 || c == "" || c == "-" {
		e.t.Fatalf("login %s: %+v cookie %q", email, resp.Errors, c)
	}
	return c
}

func TestLoginMeLogout(t *testing.T) {
	e := newAuthEnv(t)
	u := e.user("pat@example.com", sqlc.UserRoleMaintainer)

	// Without the cookie every @authenticated field is refused.
	if r, _ := e.do("", "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("me without session: %+v", r)
	}

	resp, cookie := e.login("pat@example.com", testPassword)
	if len(resp.Errors) > 0 || cookie == "" {
		t.Fatalf("login: %+v", resp)
	}
	var lr struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
		SecondFactorRequired bool     `json:"secondFactorRequired"`
		AvailableFactors     []string `json:"availableFactors"`
	}
	if err := json.Unmarshal(resp.Data["login"], &lr); err != nil {
		t.Fatal(err)
	}
	if lr.User.ID != u.ID.String() || lr.User.Role != "MAINTAINER" || lr.SecondFactorRequired || len(lr.AvailableFactors) != 0 {
		t.Fatalf("login result %+v", lr)
	}

	r, _ := e.do(cookie, "", meQuery, nil)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["me"]), `"email":"pat@example.com"`) {
		t.Fatalf("me: %+v %s", r.Errors, r.Data["me"])
	}
	r, _ = e.do(cookie, "", sessionsQuery, nil)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["mySessions"]), `"current":true`) || !strings.Contains(string(r.Data["mySessions"]), `"userAgent":"integration-test"`) {
		t.Fatalf("mySessions: %+v %s", r.Errors, r.Data["mySessions"])
	}
	if strings.Contains(string(r.Data["mySessions"]), cookie) {
		t.Fatal("mySessions exposes the token")
	}

	// Introspection: refused anonymously, allowed with a session.
	if r, _ := e.do("", "", `{ __schema { queryType { name } } }`, nil); r.code() != "INTROSPECTION_DISABLED" {
		t.Fatalf("anonymous introspection: %+v", r)
	}
	if r, _ := e.do(cookie, "", `{ __schema { queryType { name } } }`, nil); len(r.Errors) > 0 {
		t.Fatalf("authenticated introspection: %+v", r.Errors)
	}

	r, w := e.do(cookie, "", logoutMutation, nil)
	if len(r.Errors) > 0 || sessionCookie(t, w) != "-" {
		t.Fatalf("logout: %+v cookie %q", r.Errors, sessionCookie(t, w))
	}
	r, w = e.do(cookie, "", meQuery, nil)
	if r.code() != "UNAUTHENTICATED" || sessionCookie(t, w) != "-" {
		t.Fatalf("me after logout: %+v (stale cookie must be cleared)", r)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthLogout); n != 1 {
		t.Fatalf("%d logout entries", n)
	}
}

func countAudit(t *testing.T, a *audit.Auditor, action audit.Action) int {
	t.Helper()
	page, err := a.List(t.Context(), audit.Filter{Action: action}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Entries)
}

func TestLoginFailureGenericAndAudited(t *testing.T) {
	e := newAuthEnv(t)
	e.user("pat@example.com", sqlc.UserRoleUser)
	for _, c := range []struct{ email, pw string }{
		{"pat@example.com", "wrong-password-123"},
		{"nobody@example.com", testPassword},
	} {
		resp, cookie := e.login(c.email, c.pw)
		if resp.code() != "UNAUTHENTICATED" || resp.Errors[0].Message != "invalid email or password" || cookie != "" {
			t.Fatalf("login(%s): %+v cookie %q", c.email, resp.Errors, cookie)
		}
	}
	page, err := e.auditor.List(t.Context(), audit.Filter{Action: audit.ActionAuthLoginFailed}, 0, 10)
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("failure entries: %d %v", len(page.Entries), err)
	}
	for _, en := range page.Entries {
		if strings.Contains(string(en.Details), "wrong-password") || strings.Contains(string(en.Details), testPassword) || !strings.Contains(string(en.Details), "reason") {
			t.Fatalf("details %s", en.Details)
		}
	}

	e.limited.Store(true)
	if resp, _ := e.login("pat@example.com", testPassword); resp.code() != "RATE_LIMITED" {
		t.Fatalf("rate limited login: %+v", resp)
	}
}

func TestLockedAccount(t *testing.T) {
	e := newAuthEnv(t)
	e.user("locked@example.com", sqlc.UserRoleUser)
	existing := e.mustLogin("locked@example.com")
	for range 3 {
		e.login("locked@example.com", "wrong-password-123")
	}
	resp, cookie := e.login("locked@example.com", testPassword)
	if resp.code() != "UNAUTHENTICATED" || resp.Errors[0].Message != "invalid email or password" || cookie != "" {
		t.Fatalf("locked login: %+v", resp)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthLockout); n != 1 {
		t.Fatalf("%d lockout entries", n)
	}
	// The session from before the guessing stays valid (no targeted DoS).
	if r, _ := e.do(existing, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("lockout ended an existing session: %+v", r.Errors)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	e := newAuthEnv(t)
	e.user("pat@example.com", sqlc.UserRoleUser)
	a := e.mustLogin("pat@example.com")
	b := e.mustLogin("pat@example.com")

	const change = `mutation($c: String!, $n: String!) { changePassword(currentPassword: $c, newPassword: $n) }`
	r, _ := e.do(a, "", change, map[string]any{"c": "wrong-password-123", "n": "new-granite-harbor-77"})
	if r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "currentPassword" {
		t.Fatalf("wrong current: %+v", r.Errors)
	}
	r, _ = e.do(a, "", change, map[string]any{"c": testPassword, "n": "short"})
	if r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "newPassword" {
		t.Fatalf("weak new: %+v", r.Errors)
	}
	r, w := e.do(a, "", change, map[string]any{"c": testPassword, "n": "new-granite-harbor-77"})
	if len(r.Errors) > 0 {
		t.Fatalf("change: %+v", r.Errors)
	}
	fresh := sessionCookie(t, w)
	if fresh == "" || fresh == "-" || fresh == a {
		t.Fatalf("no rotated cookie: %q", fresh)
	}
	for name, c := range map[string]string{"old current": a, "other": b} {
		if r, _ := e.do(c, "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
			t.Errorf("%s session survived: %+v", name, r)
		}
	}
	if r, _ := e.do(fresh, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("new session: %+v", r.Errors)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthPasswordChanged); n != 1 {
		t.Fatalf("%d password change entries", n)
	}
}

func TestRevokeSessionGraphQL(t *testing.T) {
	e := newAuthEnv(t)
	e.user("pat@example.com", sqlc.UserRoleUser)
	e.user("sam@example.com", sqlc.UserRoleUser)
	a := e.mustLogin("pat@example.com")
	b := e.mustLogin("pat@example.com")
	s := e.mustLogin("sam@example.com")

	sessionID := func(cookie string) string {
		r, _ := e.do(cookie, "", sessionsQuery, nil)
		var list []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		}
		if err := json.Unmarshal(r.Data["mySessions"], &list); err != nil {
			t.Fatal(err)
		}
		for _, x := range list {
			if x.Current {
				return x.ID
			}
		}
		t.Fatal("no current session")
		return ""
	}
	const revoke = `mutation($id: UUID!) { revokeSession(id: $id) }`
	if r, _ := e.do(a, "", revoke, map[string]any{"id": sessionID(s)}); r.code() != "NOT_FOUND" {
		t.Fatalf("revoking another user's session: %+v", r)
	}
	if r, _ := e.do(a, "", revoke, map[string]any{"id": sessionID(b)}); len(r.Errors) > 0 {
		t.Fatalf("revoke own other session: %+v", r.Errors)
	}
	if r, _ := e.do(b, "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatal("revoked session still works")
	}
	r, w := e.do(a, "", revoke, map[string]any{"id": sessionID(a)})
	if len(r.Errors) > 0 || sessionCookie(t, w) != "-" {
		t.Fatalf("revoke current: %+v cookie %q", r.Errors, sessionCookie(t, w))
	}
	if r, _ := e.do(s, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatal("another user's session was affected")
	}
}

// TestCrossOriginLoginRejected checks that the 1.1 Origin middleware still
// rejects cross-site POSTs to /graphql (CSRF) before any resolver runs.
func TestCrossOriginLoginRejected(t *testing.T) {
	e := newAuthEnv(t)
	e.user("pat@example.com", sqlc.UserRoleUser)
	cookie := e.mustLogin("pat@example.com")
	_, w := e.do("", "https://evil.example", loginMutation, map[string]any{"e": "pat@example.com", "p": testPassword})
	if w.Code != http.StatusForbidden || sessionCookie(t, w) != "" {
		t.Fatalf("cross-origin login: %d", w.Code)
	}
	_, w = e.do(cookie, "https://evil.example", logoutMutation, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin logout: %d", w.Code)
	}
	if r, _ := e.do(cookie, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatal("the cross-origin logout had an effect")
	}
}
