//go:build integration

package graph_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
)

// newTOTPGraphEnv is newAuthEnv with the TOTP service wired in.
func newTOTPGraphEnv(t *testing.T) *authEnv {
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
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	tsvc, err := auth.NewTOTP(auth.TOTPOptions{DB: e.pool, Keyring: kr, Auditor: e.auditor, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(tsvc)
	pol := authz.New(authz.Options{SecondFactors: factors})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, TOTP: tsvc,
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
	statusQuery     = `{ myTwoFactorStatus { totpEnabled totpEnabledAt totpEnrollmentPending recoveryCodesRemaining } }`
	beginMutation   = `mutation { beginTotpEnrollment { otpauthUri qrCodeSvg manualEntryKey expiresAt } }`
	confirmMutation = `mutation($c: String!) { confirmTotpEnrollment(code: $c) { codes } }`
	disableMutation = `mutation { disableTotp }`
	regenMutation   = `mutation { regenerateRecoveryCodes { codes } }`
)

func TestTOTPGraphQL(t *testing.T) {
	e := newTOTPGraphEnv(t)
	e.user("tina@example.com", sqlc.UserRoleUser)

	for _, q := range []string{statusQuery, beginMutation, disableMutation, regenMutation} {
		if r, _ := e.do("", "", q, nil); r.code() != "UNAUTHENTICATED" {
			t.Fatalf("anonymous %s: %+v", q, r)
		}
	}
	cookie := e.mustLogin("tina@example.com") // a login is a fresh step-up

	r, _ := e.do(cookie, "", statusQuery, nil)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["myTwoFactorStatus"]), `"totpEnabled":false`) {
		t.Fatalf("status: %+v %s", r.Errors, r.Data["myTwoFactorStatus"])
	}
	if r, _ := e.do(cookie, "", regenMutation, nil); r.code() != "CONFLICT" {
		t.Fatalf("regenerate without factor: %+v", r)
	}

	r, _ = e.do(cookie, "", beginMutation, nil)
	if len(r.Errors) > 0 {
		t.Fatalf("begin: %+v", r.Errors)
	}
	var en struct {
		OtpauthURI     string `json:"otpauthUri"`
		QRCodeSVG      string `json:"qrCodeSvg"`
		ManualEntryKey string `json:"manualEntryKey"`
	}
	if err := json.Unmarshal(r.Data["beginTotpEnrollment"], &en); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(en.QRCodeSVG, "<svg ") || strings.Contains(en.QRCodeSVG, "data:") || !strings.HasPrefix(en.OtpauthURI, "otpauth://totp/") {
		t.Fatalf("enrollment %+v", en)
	}
	if r, _ := e.do(cookie, "", confirmMutation, map[string]any{"c": "000000"}); r.code() != "VALIDATION" {
		t.Fatalf("wrong code: %+v", r)
	}
	code, err := totp.GenerateCodeCustom(en.ManualEntryKey, time.Now(), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	r, _ = e.do(cookie, "", confirmMutation, map[string]any{"c": code})
	if len(r.Errors) > 0 {
		t.Fatalf("confirm: %+v", r.Errors)
	}
	var rc struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(r.Data["confirmTotpEnrollment"], &rc); err != nil || len(rc.Codes) != auth.RecoveryCodeCount {
		t.Fatalf("codes %v %v", rc.Codes, err)
	}
	r, _ = e.do(cookie, "", statusQuery, nil)
	st := string(r.Data["myTwoFactorStatus"])
	if !strings.Contains(st, `"totpEnabled":true`) || !strings.Contains(st, `"recoveryCodesRemaining":10`) {
		t.Fatalf("status after enrollment: %s", st)
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.ManualEntryKey)
	if err != nil || strings.Contains(st, en.ManualEntryKey) || len(key) != auth.TOTPSecretBytes {
		t.Fatal("status exposes the key")
	}
	if r, _ := e.do(cookie, "", beginMutation, nil); r.code() != "CONFLICT" {
		t.Fatalf("begin while enabled: %+v", r)
	}

	// Without a fresh step-up every change is refused.
	if _, err := e.pool.Exec(t.Context(), "UPDATE sessions SET stepup_at = now() - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{beginMutation, disableMutation, regenMutation} {
		if r, _ := e.do(cookie, "", q, nil); r.code() != "STEP_UP_REQUIRED" {
			t.Fatalf("stale step-up %s: %+v", q, r)
		}
	}
	if r, _ := e.do(cookie, "", confirmMutation, map[string]any{"c": code}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up confirm: %+v", r)
	}
	if r, _ := e.do(cookie, "", statusQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("status needs no step-up: %+v", r.Errors)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE sessions SET stepup_at = now()"); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.do(cookie, "", regenMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("regenerate: %+v", r.Errors)
	}
	if r, _ := e.do(cookie, "", disableMutation, nil); len(r.Errors) > 0 || string(r.Data["disableTotp"]) != "true" {
		t.Fatalf("disable: %+v", r)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthMFARemoved); n != 1 {
		t.Fatalf("%d removal audit rows", n)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthRecoveryCodesRegenerated); n != 1 {
		t.Fatalf("%d regenerate audit rows", n)
	}
}
