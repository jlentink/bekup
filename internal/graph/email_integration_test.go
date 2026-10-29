//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/resolvers"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify/email"
)

// fakeMailer records messages instead of sending them; fail makes Send
// return that error.
type fakeMailer struct {
	mu   sync.Mutex
	sent []*email.Message
	fail error
}

func (f *fakeMailer) Send(_ context.Context, m *email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if err := m.Validate(); err != nil {
		return err
	}
	f.sent = append(f.sent, m)
	return nil
}

// SendDelivery makes fakeMailer a DeliveryMailer reporting TLS 1.3.
func (f *fakeMailer) SendDelivery(ctx context.Context, m *email.Message) (email.Delivery, error) {
	if err := f.Send(ctx, m); err != nil {
		return email.Delivery{}, err
	}
	return email.Delivery{Encrypted: true, TLSVersion: "TLS 1.3"}, nil
}

func (f *fakeMailer) last(t *testing.T) *email.Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no message sent")
	}
	return f.sent[len(f.sent)-1]
}

type emailEnv struct {
	*destEnv
	mailer *fakeMailer
}

func newEmailEnv(t *testing.T) *emailEnv {
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
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := email.NewSettings(email.SettingsOptions{DB: e.pool, Keyring: kr, Auditor: e.auditor, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	test, err := email.NewTestMailer(email.TestMailerOptions{Sender: mailer, Auditor: e.auditor})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := email.NewVerifier(email.VerifierOptions{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Mailer: mailer, BaseURL: testBaseURL,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
		Email: resolvers.EmailServices{Settings: settings, Test: test, Verifier: verifier},
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
	return &emailEnv{destEnv: &destEnv{authEnv: e}, mailer: mailer}
}

const (
	smtpFields    = `configured enabled host port mode username hasPassword fromAddress fromName caBundlePem connectTimeoutSeconds sendTimeoutSeconds mayBeUnencrypted updatedAt`
	smtpQuery     = `{ smtpSettings { ` + smtpFields + ` } }`
	updateSMTPMut = `mutation($in: UpdateSmtpSettingsInput!) { updateSmtpSettings(input: $in) { ` + smtpFields + ` } }`
	testEmailMut  = `mutation($to: String!) { sendTestEmail(to: $to) { ok code message detail encrypted tlsVersion } }`
	requestVerify = `mutation { requestEmailVerification { sentTo expiresAt } }`
	verifyMut     = `mutation($c: String!) { verifyEmail(code: $c) { id emailVerified emailVerifiedAt } }`
)

const smtpTestPassword = "graph-relay-password-91c"

func TestSMTPSettingsGraphQL(t *testing.T) {
	e := newEmailEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	ac := e.mustLogin("admin@example.com")
	mc := e.mustLogin("maint@example.com")

	// Defaults before anything is stored.
	resp := e.mustGQL(ac, smtpQuery, nil)
	if d := string(resp.Data["smtpSettings"]); !strings.Contains(d, `"configured":false`) ||
		!strings.Contains(d, `"mode":"AUTO"`) || !strings.Contains(d, `"mayBeUnencrypted":true`) {
		t.Fatalf("defaults %s", resp.Data["smtpSettings"])
	}
	// Admin only.
	if r := e.gql(mc, smtpQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer: %+v", r.Errors)
	}
	in := map[string]any{
		"enabled": true, "host": "127.0.0.1", "port": 2525, "mode": "STARTTLS",
		"username": "bekup", "password": smtpTestPassword, "fromAddress": "bekup@example.com", "fromName": "bekup",
	}
	if r := e.gql(mc, updateSMTPMut, map[string]any{"in": in}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer update: %+v", r.Errors)
	}
	// Step-up required.
	e.staleStepUp(admin.ID)
	if r := e.gql(ac, updateSMTPMut, map[string]any{"in": in}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	resp = e.mustGQL(ac, updateSMTPMut, map[string]any{"in": in})
	var got struct {
		Configured       bool    `json:"configured"`
		HasPassword      bool    `json:"hasPassword"`
		MayBeUnencrypted bool    `json:"mayBeUnencrypted"`
		Mode             string  `json:"mode"`
		Username         *string `json:"username"`
	}
	if err := json.Unmarshal(resp.Data["updateSmtpSettings"], &got); err != nil {
		t.Fatal(err)
	}
	if !got.Configured || !got.HasPassword || got.MayBeUnencrypted || got.Mode != "STARTTLS" || got.Username == nil {
		t.Fatalf("settings %+v", got)
	}
	// The removed PLAINTEXT mode is unknown.
	plain := map[string]any{"enabled": true, "host": "127.0.0.1", "port": 1025, "mode": "PLAINTEXT", "fromAddress": "bekup@example.com", "clearPassword": true}
	if r := e.gql(ac, updateSMTPMut, map[string]any{"in": plain}); len(r.Errors) == 0 {
		t.Fatal("PLAINTEXT accepted")
	}
	// Header injection in the sender is refused.
	bad := map[string]any{"enabled": true, "host": "127.0.0.1", "port": 2525, "mode": "STARTTLS", "username": "bekup",
		"fromAddress": "bekup@example.com\r\nBcc: x@example.com"}
	if r := e.gql(ac, updateSMTPMut, map[string]any{"in": bad}); r.code() != "VALIDATION" {
		t.Fatalf("injection: %+v", r.Errors)
	}
	// AUTO (D-180) is flagged for the UI badge. Weakening STARTTLS to AUTO
	// needs the password again (M5 review L3, D-198).
	auto := map[string]any{"enabled": true, "host": "127.0.0.1", "port": 2525, "mode": "AUTO", "username": "bekup", "fromAddress": "bekup@example.com"}
	if r := e.gql(ac, updateSMTPMut, map[string]any{"in": auto}); r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "password" {
		t.Fatalf("weakened without the password: %+v", r.Errors)
	}
	// A changed CA bundle needs it too.
	withCA := map[string]any{"enabled": true, "host": "127.0.0.1", "port": 2525, "mode": "STARTTLS", "username": "bekup",
		"fromAddress": "bekup@example.com", "caBundlePem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crNewCA(t).cert.Raw}))}
	if r := e.gql(ac, updateSMTPMut, map[string]any{"in": withCA}); r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "password" {
		t.Fatalf("CA bundle changed without the password: %+v", r.Errors)
	}
	auto["password"] = smtpTestPassword
	resp = e.mustGQL(ac, updateSMTPMut, map[string]any{"in": auto})
	if d := string(resp.Data["updateSmtpSettings"]); !strings.Contains(d, `"mode":"AUTO"`) || !strings.Contains(d, `"mayBeUnencrypted":true`) {
		t.Fatalf("auto: %s", d)
	}
	e.mustGQL(ac, smtpQuery, nil)
	if n := countAudit(t, e.auditor, audit.ActionSettingChanged); n != 2 {
		t.Fatalf("setting.changed %d", n)
	}
	if n := countAudit(t, e.auditor, audit.ActionSecretChanged); n != 2 {
		t.Fatalf("secret.changed %d", n)
	}

	// sendTestEmail: admin only, single root, results with fixed codes.
	if r := e.gql(mc, testEmailMut, map[string]any{"to": "ops@example.com"}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer test: %+v", r.Errors)
	}
	if r := e.gql(ac, `mutation { a: sendTestEmail(to: "a@example.com") { ok } b: sendTestEmail(to: "b@example.com") { ok } }`, nil); r.code() != "VALIDATION" {
		t.Fatalf("two root fields: %+v", r.Errors)
	}
	resp = e.mustGQL(ac, testEmailMut, map[string]any{"to": "ops@example.com"})
	if s := string(resp.Data["sendTestEmail"]); !strings.Contains(s, `"ok":true`) ||
		!strings.Contains(s, `"encrypted":true`) || !strings.Contains(s, `"tlsVersion":"TLS 1.3"`) {
		t.Fatalf("test: %s", resp.Data["sendTestEmail"])
	}
	if m := e.mailer.last(t); m.To[0] != "ops@example.com" || m.Subject != "bekup test e-mail" {
		t.Fatalf("message %+v", m)
	}
	if r := e.gql(ac, testEmailMut, map[string]any{"to": "ops@example.com\nBcc: x@example.com"}); r.code() != "VALIDATION" {
		t.Fatalf("injection in to: %+v", r.Errors)
	}
	e.mailer.fail = &email.SendError{Code: email.CodeTLS}
	resp = e.mustGQL(ac, testEmailMut, map[string]any{"to": "ops@example.com"})
	if s := string(resp.Data["sendTestEmail"]); !strings.Contains(s, `"ok":false`) || !strings.Contains(s, `"code":"tls_failed"`) ||
		!strings.Contains(s, `"encrypted":false`) || !strings.Contains(s, `"tlsVersion":null`) {
		t.Fatalf("failed test: %s", s)
	}
	e.mailer.fail = nil
	if n := countAudit(t, e.auditor, audit.ActionSMTPTestSent); n != 2 {
		t.Fatalf("smtp.test_sent %d", n)
	}
	e.assertNoSecrets(smtpTestPassword)
}

func TestEmailVerificationGraphQL(t *testing.T) {
	e := newEmailEnv(t)
	pat := e.user("pat@example.com", sqlc.UserRoleUser)
	e.user("eve@example.com", sqlc.UserRoleUser)
	pc := e.mustLogin("pat@example.com")
	ec := e.mustLogin("eve@example.com")

	if r := e.gql("", requestVerify, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous: %+v", r.Errors)
	}
	resp := e.mustGQL(pc, requestVerify, nil)
	if !strings.Contains(string(resp.Data["requestEmailVerification"]), `"sentTo":"pat@example.com"`) {
		t.Fatalf("request: %s", resp.Data["requestEmailVerification"])
	}
	if r := e.gql(pc, requestVerify, nil); r.code() != "RATE_LIMITED" {
		t.Fatalf("cooldown: %+v", r.Errors)
	}
	m := e.mailer.last(t)
	prefix := testBaseURL + email.VerificationPath + "#code="
	i := strings.Index(m.Text, prefix)
	if i < 0 || m.To[0] != "pat@example.com" {
		t.Fatalf("message %+v", m)
	}
	code := m.Text[i+len(prefix) : i+len(prefix)+43]

	if r := e.gql(pc, `mutation($c: String!) { a: verifyEmail(code: $c) { id } b: verifyEmail(code: $c) { id } }`, map[string]any{"c": code}); r.code() != "VALIDATION" {
		t.Fatalf("two root fields: %+v", r.Errors)
	}
	if r := e.gql(ec, verifyMut, map[string]any{"c": code}); r.code() != "VALIDATION" {
		t.Fatalf("other account: %+v", r.Errors)
	}
	if r := e.gql("", verifyMut, map[string]any{"c": code}); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous verify: %+v", r.Errors)
	}
	resp = e.mustGQL(pc, verifyMut, map[string]any{"c": code})
	if !strings.Contains(string(resp.Data["verifyEmail"]), `"emailVerified":true`) || !strings.Contains(string(resp.Data["verifyEmail"]), pat.ID.String()) {
		t.Fatalf("verify: %s", resp.Data["verifyEmail"])
	}
	if r := e.gql(pc, verifyMut, map[string]any{"c": code}); r.code() != "VALIDATION" {
		t.Fatalf("reuse: %+v", r.Errors)
	}
	if r := e.gql(pc, requestVerify, nil); r.code() != "CONFLICT" {
		t.Fatalf("already verified: %+v", r.Errors)
	}
	resp = e.mustGQL(pc, `{ me { emailVerified } }`, nil)
	if !strings.Contains(string(resp.Data["me"]), `"emailVerified":true`) {
		t.Fatalf("me: %s", resp.Data["me"])
	}
	// Delivery failures reveal no SMTP details to users.
	e.mailer.fail = &email.SendError{Code: email.CodeAuthFailed, Detail: "SMTP reply 535"}
	r := e.gql(ec, requestVerify, nil)
	if r.code() != "CONNECTION_FAILED" || strings.Contains(r.Errors[0].Message, "535") {
		t.Fatalf("send failure: %+v", r.Errors)
	}
}
