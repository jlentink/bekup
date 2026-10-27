//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/auth/passkey/passkeytest"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
)

// newPasskeyEnv is newAuthEnv plus the passkey service on base.
func newPasskeyEnv(t *testing.T, base string) *authEnv {
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
	factors := auth.NewSecondFactors()
	svc, err := auth.NewService(auth.ServiceOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: factors,
		LoginLimiter: func(netip.Addr) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	pk, err := passkey.New(passkey.Options{DB: e.pool, Auditor: e.auditor, BaseURL: base, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(pk)
	pol := authz.New(authz.Options{SecondFactors: factors})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, Passkeys: pk,
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

const (
	webauthnStatusQuery = `{ webauthnStatus { webauthnAvailable reason rpId } }`
	myPasskeysQuery     = `{ myPasskeys { id name transports backupEligible backedUp userVerified lastUsedAt signCountAnomalyAt } }`
	beginRegMutation    = `mutation { beginPasskeyRegistration }`
	finishRegMutation   = `mutation($n: String!, $r: JSON!) { finishPasskeyRegistration(name: $n, response: $r) { id name userVerified } }`
	renameMutation      = `mutation($id: UUID!, $n: String!) { renamePasskey(id: $id, name: $n) { id name } }`
	deleteMutation      = `mutation($id: UUID!) { deletePasskey(id: $id) }`
)

func TestPasskeyGraphQL(t *testing.T) {
	e := newPasskeyEnv(t, testBaseURL)
	e.user("pat@example.com", sqlc.UserRoleUser)

	if r, _ := e.do("", "", webauthnStatusQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous webauthnStatus: %+v", r)
	}
	if r, _ := e.do("", "", beginRegMutation, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous begin: %+v", r)
	}
	cookie := e.mustLogin("pat@example.com")

	r, _ := e.do(cookie, "", webauthnStatusQuery, nil)
	if len(r.Errors) > 0 || string(r.Data["webauthnStatus"]) != `{"webauthnAvailable":true,"reason":null,"rpId":"bekup.test"}` {
		t.Fatalf("webauthnStatus: %+v %s", r.Errors, r.Data["webauthnStatus"])
	}

	// The login counts as step-up: registration works right away.
	r, _ = e.do(cookie, "", beginRegMutation, nil)
	if len(r.Errors) > 0 {
		t.Fatalf("begin: %+v", r.Errors)
	}
	a, err := passkeytest.New(testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Register(r.Data["beginPasskeyRegistration"])
	if err != nil {
		t.Fatal(err)
	}
	var respObj map[string]any
	if err := json.Unmarshal(resp, &respObj); err != nil {
		t.Fatal(err)
	}
	r, _ = e.do(cookie, "", finishRegMutation, map[string]any{"n": "YubiKey", "r": respObj})
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["finishPasskeyRegistration"]), `"name":"YubiKey"`) {
		t.Fatalf("finish: %+v %s", r.Errors, r.Data["finishPasskeyRegistration"])
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Data["finishPasskeyRegistration"], &created); err != nil {
		t.Fatal(err)
	}
	// Replay: no pending registration any more.
	if r, _ := e.do(cookie, "", finishRegMutation, map[string]any{"n": "again", "r": respObj}); r.code() != "VALIDATION" {
		t.Fatalf("replayed finish: %+v", r)
	}
	// Non-object JSON input is refused by the scalar.
	if r, _ := e.do(cookie, "", finishRegMutation, map[string]any{"n": "x", "r": "a string"}); r.code() != "VALIDATION" {
		t.Fatalf("string JSON input: %+v", r)
	}

	r, _ = e.do(cookie, "", myPasskeysQuery, nil)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["myPasskeys"]), `"transports":["usb"]`) {
		t.Fatalf("myPasskeys: %+v %s", r.Errors, r.Data["myPasskeys"])
	}
	if strings.Contains(string(r.Data["myPasskeys"]), "publicKey") {
		t.Fatal("myPasskeys exposes key material")
	}
	r, _ = e.do(cookie, "", renameMutation, map[string]any{"id": created.ID, "n": ""})
	if r.code() != "VALIDATION" {
		t.Fatalf("empty rename: %+v", r)
	}

	// With a stale step-up every change is refused; the list still works.
	if err := e.d.SuperExec(t.Context(), "UPDATE sessions SET stepup_at = now() - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		q    string
		vars map[string]any
	}{
		{beginRegMutation, nil},
		{finishRegMutation, map[string]any{"n": "x", "r": respObj}},
		{renameMutation, map[string]any{"id": created.ID, "n": "y"}},
		{deleteMutation, map[string]any{"id": created.ID}},
	} {
		if r, _ := e.do(cookie, "", tc.q, tc.vars); r.code() != "STEP_UP_REQUIRED" {
			t.Fatalf("%s without step-up: %+v", tc.q, r)
		}
	}
	if r, _ := e.do(cookie, "", myPasskeysQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("myPasskeys without step-up: %+v", r.Errors)
	}
	if err := e.d.SuperExec(t.Context(), "UPDATE sessions SET stepup_at = now()"); err != nil {
		t.Fatal(err)
	}
	r, _ = e.do(cookie, "", renameMutation, map[string]any{"id": created.ID, "n": "Laptop"})
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["renamePasskey"]), `"name":"Laptop"`) {
		t.Fatalf("rename: %+v", r)
	}
	r, _ = e.do(cookie, "", deleteMutation, map[string]any{"id": created.ID})
	if len(r.Errors) > 0 || string(r.Data["deletePasskey"]) != "true" {
		t.Fatalf("delete: %+v", r)
	}
	if r, _ := e.do(cookie, "", deleteMutation, map[string]any{"id": created.ID}); r.code() != "NOT_FOUND" {
		t.Fatalf("delete twice: %+v", r)
	}
	for _, action := range []audit.Action{
		audit.ActionAuthPasskeyRegistrationBegun, audit.ActionAuthMFAEnrolled,
		audit.ActionAuthPasskeyRenamed, audit.ActionAuthMFARemoved,
	} {
		if n := countAudit(t, e.auditor, action); n != 1 {
			t.Errorf("%s: %d entries", action, n)
		}
	}
}

func TestPasskeyGraphQLUnavailable(t *testing.T) {
	e := newPasskeyEnv(t, "http://bekup.example.com")
	e.user("pat@example.com", sqlc.UserRoleAdmin)
	cookie := e.mustLogin("pat@example.com")
	r, _ := e.do(cookie, "", webauthnStatusQuery, nil)
	var st struct {
		Available bool    `json:"webauthnAvailable"`
		Reason    *string `json:"reason"`
		RPID      *string `json:"rpId"`
	}
	if err := json.Unmarshal(r.Data["webauthnStatus"], &st); err != nil || st.Available || st.Reason == nil || st.RPID != nil {
		t.Fatalf("status: %+v %v", st, err)
	}
	if r, _ := e.do(cookie, "", beginRegMutation, nil); r.code() != "FORBIDDEN" || !strings.Contains(r.Errors[0].Message, "secure context") {
		t.Fatalf("begin over http: %+v", r)
	}
}
