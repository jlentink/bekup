//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/auth/passkey/passkeytest"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
)

// mfaClock is the clock of the TOTP service: every generated code moves
// it one step forward, so each code is for a new time step (replay
// protection accepts each step once).
type mfaClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *mfaClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *mfaClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

// mfaEnv is the full task 1.9 stack: auth, TOTP, passkeys, the 2FA policy
// (enrollment check in the session middleware, last-factor guard) and the
// second-factor flow.
type mfaEnv struct {
	*authEnv
	clock  *mfaClock
	policy *auth.TwoFactorPolicies
	users  *auth.UserAdmin
}

func newMFAEnv(t *testing.T) *mfaEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &mfaEnv{authEnv: &authEnv{t: t, d: d, pool: d.AppPool(t)}, clock: &mfaClock{}}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	lockout := auth.LockoutPolicy{Threshold: 3, Base: time.Minute, Max: time.Hour}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	factors := auth.NewSecondFactors()
	svc, err := auth.NewService(auth.ServiceOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: factors, Lockout: lockout,
		LoginLimiter: func(netip.Addr) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	e.policy, err = auth.NewTwoFactorPolicies(auth.TwoFactorPoliciesOptions{DB: e.pool, Auditor: e.auditor, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	tsvc, err := auth.NewTOTP(auth.TOTPOptions{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Factors: factors, RemovalGuard: e.policy,
		Lockout: lockout, Now: e.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(tsvc)
	pk, err := passkey.New(passkey.Options{DB: e.pool, Auditor: e.auditor, BaseURL: testBaseURL, Factors: factors, RemovalGuard: e.policy})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(pk)
	flow, err := auth.NewSecondFactorFlow(auth.SecondFactorFlowOptions{
		DB: e.pool, Sessions: sessions, Auditor: e.auditor, Service: svc, TOTP: tsvc, Passkeys: pk, Factors: factors,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.users, err = auth.NewUserAdmin(auth.UserAdminOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Policy: auth.NewPasswordPolicy(12, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{SecondFactors: factors})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, TOTP: tsvc, Passkeys: pk,
		SecondFactor: flow, TwoFactorPolicy: e.policy, Users: e.users,
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
			Resolver: httpserver.SessionsResolver{Sessions: sessions, Enrollment: e.policy},
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
	mfaLoginMutation = `mutation($e: String!, $p: String!) { login(email: $e, password: $p) {
		user { email } secondFactorRequired availableFactors passwordChangeRequired twoFactorEnrollmentRequired } }`
	completeMutation       = `mutation($m: SecondFactorKind!, $c: String, $a: JSON) { completeSecondFactor(method: $m, code: $c, assertion: $a) { user { email } secondFactorRequired passwordChangeRequired } }`
	beginPasskeyLoginMut   = `mutation { beginPasskeyLogin }`
	beginStepUpMutation    = `mutation { beginStepUp { methods passkeyOptions } }`
	completeStepUpMutation = `mutation($m: StepUpMethod!, $c: String, $p: String, $a: JSON) { completeStepUp(method: $m, code: $c, password: $p, assertion: $a) { stepUpAt aal2At aal } }`
	policyQuery            = `{ twoFactorPolicy { requireForAll requiredRoles gracePeriodDays updatedAt passkeysAvailable } }`
	updatePolicyMutation   = `mutation($all: Boolean!, $roles: [Role!]!, $g: Int!) { updateTwoFactorPolicy(input: {requireForAll: $all, requiredRoles: $roles, gracePeriodDays: $g}) { requireForAll requiredRoles gracePeriodDays } }`
	meMFAQuery             = `{ me { email twoFactorRequired twoFactorEnrollmentDeadline } }`
)

type loginOut struct {
	User *struct {
		Email string `json:"email"`
	} `json:"user"`
	SecondFactorRequired        bool     `json:"secondFactorRequired"`
	AvailableFactors            []string `json:"availableFactors"`
	PasswordChangeRequired      bool     `json:"passwordChangeRequired"`
	TwoFactorEnrollmentRequired bool     `json:"twoFactorEnrollmentRequired"`
}

// mfaLogin logs in with the password and returns the result and cookie.
func (e *mfaEnv) mfaLogin(email string) (loginOut, string) {
	e.t.Helper()
	r, w := e.do("", "", mfaLoginMutation, map[string]any{"e": email, "p": testPassword})
	c := sessionCookie(e.t, w)
	if len(r.Errors) > 0 || c == "" || c == "-" {
		e.t.Fatalf("login %s: %+v cookie %q", email, r.Errors, c)
	}
	var out loginOut
	if err := json.Unmarshal(r.Data["login"], &out); err != nil {
		e.t.Fatal(err)
	}
	// M1 review L1: a login waiting for its second factor returns no
	// account data; a completed one returns the user.
	if out.SecondFactorRequired != (out.User == nil) {
		e.t.Fatalf("login %s: secondFactorRequired %v with user %+v", email, out.SecondFactorRequired, out.User)
	}
	return out, c
}

// enrollTOTP enrolls TOTP for the session (it needs a fresh step-up) and
// returns the key and the recovery codes.
func (e *mfaEnv) enrollTOTP(cookie string) (string, []string) {
	e.t.Helper()
	r, _ := e.do(cookie, "", beginMutation, nil)
	if len(r.Errors) > 0 {
		e.t.Fatalf("begin TOTP: %+v", r.Errors)
	}
	var en struct {
		ManualEntryKey string `json:"manualEntryKey"`
	}
	if err := json.Unmarshal(r.Data["beginTotpEnrollment"], &en); err != nil {
		e.t.Fatal(err)
	}
	r, _ = e.do(cookie, "", confirmMutation, map[string]any{"c": e.code(en.ManualEntryKey)})
	if len(r.Errors) > 0 {
		e.t.Fatalf("confirm TOTP: %+v", r.Errors)
	}
	var rc struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(r.Data["confirmTotpEnrollment"], &rc); err != nil {
		e.t.Fatal(err)
	}
	return en.ManualEntryKey, rc.Codes
}

// code returns a TOTP code for a new time step.
func (e *mfaEnv) code(key string) string {
	e.t.Helper()
	e.clock.advance(31 * time.Second)
	c, err := totp.GenerateCodeCustom(key, e.clock.Now(), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// complete runs completeSecondFactor and returns the response and the new
// cookie ("" if none was set).
func (e *mfaEnv) complete(cookie string, vars map[string]any) (gqlResp, string) {
	e.t.Helper()
	r, w := e.do(cookie, "", completeMutation, vars)
	return r, sessionCookie(e.t, w)
}

// session returns the session row of the cookie's session (via the
// sessions of the user, most recent first).
func (e *mfaEnv) latestSession(userID uuid.UUID) sqlc.Session {
	e.t.Helper()
	rows, err := sqlc.New(e.pool).ListActiveUserSessions(e.t.Context(), userID)
	if err != nil || len(rows) == 0 {
		e.t.Fatalf("sessions: %v (%d)", err, len(rows))
	}
	return rows[0]
}

func (e *mfaEnv) setStepUp(userID uuid.UUID, ago time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET stepup_at = now() - make_interval(secs => $2) WHERE user_id = $1 AND NOT mfa_pending",
		userID, ago.Seconds()); err != nil {
		e.t.Fatal(err)
	}
}

// auditDetails returns the details of the newest entries of action.
func auditDetails(t *testing.T, a *audit.Auditor, action audit.Action) []map[string]any {
	t.Helper()
	page, err := a.List(t.Context(), audit.Filter{Action: action}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(page.Entries))
	for i := range page.Entries {
		var d map[string]any
		if err := json.Unmarshal(page.Entries[i].Details, &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// pendingDenied checks that a pending session can use nothing protected.
func (e *mfaEnv) pendingDenied(cookie string) {
	e.t.Helper()
	for _, q := range []string{
		meQuery, sessionsQuery, statusQuery, myPasskeysQuery, webauthnStatusQuery, beginMutation, regenMutation,
		beginStepUpMutation, policyQuery, `{ auditLog { pageInfo { hasNextPage } } }`,
		`mutation { changePassword(currentPassword: "a", newPassword: "b") }`,
	} {
		if r, _ := e.do(cookie, "", q, nil); r.code() != "SECOND_FACTOR_REQUIRED" {
			e.t.Errorf("pending session %s: %+v", q, r)
		}
	}
}

func TestTwoStepLoginTOTP(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("tina@example.com", sqlc.UserRoleUser)
	out, cookie := e.mfaLogin(u.Email)
	if out.SecondFactorRequired {
		t.Fatal("login without a factor needs no second step")
	}
	if r, _ := e.do(cookie, "", completeMutation, map[string]any{"m": "TOTP", "c": "123456"}); r.code() != "CONFLICT" {
		t.Fatalf("complete on a full session: %+v", r)
	}
	key, _ := e.enrollTOTP(cookie)

	out, pending := e.mfaLogin(u.Email)
	if !out.SecondFactorRequired || !slices.Equal(out.AvailableFactors, []string{"TOTP", "RECOVERY_CODE"}) {
		t.Fatalf("login result %+v", out)
	}
	sess := e.latestSession(u.ID)
	if !sess.MfaPending || sess.Aal != 1 || sess.StepupAt != nil || sess.Aal2At != nil ||
		sess.AbsoluteExpiresAt.Sub(sess.CreatedAt) > auth.PendingSecondFactorTTL+time.Second {
		t.Fatalf("pending session %+v", sess)
	}
	e.pendingDenied(pending)

	// A missing code is a validation error that is not counted.
	if r, _ := e.complete(pending, map[string]any{"m": "TOTP"}); r.code() != "VALIDATION" {
		t.Fatalf("missing code: %+v", r)
	}
	if r, _ := e.complete(pending, map[string]any{"m": "TOTP", "c": "000000"}); r.code() != "VALIDATION" {
		t.Fatalf("wrong code: %+v", r)
	}
	r, full := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)})
	if len(r.Errors) > 0 || full == "" || full == pending {
		t.Fatalf("complete: %+v cookie %q", r.Errors, full)
	}
	if r, _ := e.do(pending, "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("pending cookie after completion: %+v", r)
	}
	if r, _ := e.do(full, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("me after completion: %+v", r.Errors)
	}
	sess = e.latestSession(u.ID)
	if sess.MfaPending || sess.Aal != 2 || sess.StepupAt == nil || sess.Aal2At == nil ||
		sess.AbsoluteExpiresAt.Sub(sess.CreatedAt) < time.Hour {
		t.Fatalf("completed session %+v", sess)
	}
	// The completed login is a fresh step-up: @stepUp fields work.
	if r, _ := e.do(full, "", regenMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("regenerate after login: %+v", r.Errors)
	}
	if r, _ := e.complete(full, map[string]any{"m": "TOTP", "c": e.code(key)}); r.code() != "CONFLICT" {
		t.Fatalf("second completion: %+v", r)
	}
	details := auditDetails(t, e.auditor, audit.ActionAuthLoginSecondFactor)
	if len(details) != 1 || details[0]["method"] != "totp" {
		t.Fatalf("second-factor audit %+v", details)
	}
	for _, d := range details {
		for k := range d {
			if strings.Contains(k, "code") {
				t.Fatalf("audit details carry %q", k)
			}
		}
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthMFAFailed); n != 1 {
		t.Fatalf("%d failure audit rows", n)
	}
	// Logout works on a pending session.
	_, pending = e.mfaLogin(u.Email)
	if r, _ := e.do(pending, "", logoutMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("logout of a pending session: %+v", r.Errors)
	}
}

func TestTwoStepLoginRecoveryCode(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("rita@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	_, codes := e.enrollTOTP(cookie)
	_, pending := e.mfaLogin(u.Email)
	r, full := e.complete(pending, map[string]any{"m": "RECOVERY_CODE", "c": codes[0]})
	if len(r.Errors) > 0 || full == "" {
		t.Fatalf("complete with recovery code: %+v", r.Errors)
	}
	// AAL2 and a fresh step-up, but no second-factor time: a recovery code
	// does not satisfy the restore check.
	sess := e.latestSession(u.ID)
	if sess.Aal != 2 || sess.StepupAt == nil || sess.Aal2At != nil {
		t.Fatalf("session after recovery code %+v", sess)
	}
	_, pending = e.mfaLogin(u.Email)
	if r, _ := e.complete(pending, map[string]any{"m": "RECOVERY_CODE", "c": codes[0]}); r.code() != "VALIDATION" {
		t.Fatalf("reused recovery code: %+v", r)
	}
	if d := auditDetails(t, e.auditor, audit.ActionAuthLoginSecondFactor); len(d) != 1 || d[0]["method"] != "recovery_code" {
		t.Fatalf("audit %+v", d)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthRecoveryCodeUsed); n != 1 {
		t.Fatalf("%d recovery-code audit rows", n)
	}
}

// registerPasskey registers a software passkey for the session.
func (e *mfaEnv) registerPasskey(cookie string) *passkeytest.Authenticator {
	e.t.Helper()
	r, _ := e.do(cookie, "", beginRegMutation, nil)
	if len(r.Errors) > 0 {
		e.t.Fatalf("begin registration: %+v", r.Errors)
	}
	a, err := passkeytest.New(testBaseURL)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := a.Register(r.Data["beginPasskeyRegistration"])
	if err != nil {
		e.t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(resp, &obj); err != nil {
		e.t.Fatal(err)
	}
	if r, _ := e.do(cookie, "", finishRegMutation, map[string]any{"n": "Key", "r": obj}); len(r.Errors) > 0 {
		e.t.Fatalf("finish registration: %+v", r.Errors)
	}
	return a
}

func assertionOf(t *testing.T, a *passkeytest.Authenticator, options json.RawMessage) map[string]any {
	t.Helper()
	resp, err := a.Assert(options)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(resp, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestTwoStepLoginPasskey(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("paula@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	a := e.registerPasskey(cookie)

	out, pending := e.mfaLogin(u.Email)
	if !out.SecondFactorRequired || !slices.Equal(out.AvailableFactors, []string{"PASSKEY"}) {
		t.Fatalf("login result %+v", out)
	}
	r, _ := e.do(pending, "", beginPasskeyLoginMut, nil)
	if len(r.Errors) > 0 {
		t.Fatalf("begin passkey login: %+v", r.Errors)
	}
	assertion := assertionOf(t, a, r.Data["beginPasskeyLogin"])
	if r, _ := e.complete(pending, map[string]any{"m": "PASSKEY"}); r.code() != "VALIDATION" {
		t.Fatalf("missing assertion: %+v", r)
	}
	r, full := e.complete(pending, map[string]any{"m": "PASSKEY", "a": assertion})
	if len(r.Errors) > 0 || full == "" {
		t.Fatalf("complete with passkey: %+v", r.Errors)
	}
	if sess := e.latestSession(u.ID); sess.Aal != 2 || sess.Aal2At == nil {
		t.Fatalf("session after passkey %+v", sess)
	}

	// A replayed assertion (its ceremony is gone) is rejected and counted.
	_, pending = e.mfaLogin(u.Email)
	if r, _ := e.complete(pending, map[string]any{"m": "PASSKEY", "a": assertion}); r.code() != "VALIDATION" {
		t.Fatalf("replayed assertion: %+v", r)
	}
	var failures int32
	if err := e.pool.QueryRow(t.Context(), "SELECT mfa_failed_count FROM users WHERE id = $1", u.ID).Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("mfa_failed_count %d %v", failures, err)
	}
	if r, _ := e.complete(pending, map[string]any{"m": "TOTP", "c": "123456"}); r.code() != "VALIDATION" {
		t.Fatalf("TOTP without TOTP: %+v", r)
	}
}

func TestPendingSessionExpires(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("eve@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	key, _ := e.enrollTOTP(cookie)
	_, pending := e.mfaLogin(u.Email)
	if _, err := e.pool.Exec(t.Context(), `UPDATE sessions SET idle_expires_at = now() - interval '1 second',
		absolute_expires_at = now() - interval '1 second' WHERE mfa_pending`); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)}); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("complete after the pending TTL: %+v", r)
	}
}

func TestSecondStepLockout(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("lars@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	key, _ := e.enrollTOTP(cookie)
	_, pending := e.mfaLogin(u.Email)
	for range 3 {
		if r, _ := e.complete(pending, map[string]any{"m": "TOTP", "c": "000000"}); r.code() != "VALIDATION" {
			t.Fatalf("wrong code: %+v", r)
		}
	}
	if r, _ := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)}); r.code() != "RATE_LIMITED" {
		t.Fatalf("right code while locked: %+v", r)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthMFALockout); n != 1 {
		t.Fatalf("%d lockout audit rows", n)
	}
}

func TestStepUpWithPassword(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("nora@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	r, _ := e.do(cookie, "", beginStepUpMutation, nil)
	if len(r.Errors) > 0 || string(r.Data["beginStepUp"]) != `{"methods":["PASSWORD"],"passkeyOptions":null}` {
		t.Fatalf("beginStepUp: %+v %s", r.Errors, r.Data["beginStepUp"])
	}
	// @stepUp windows: 4 minutes old is fresh, 6 minutes old is not.
	e.setStepUp(u.ID, 4*time.Minute)
	if r, _ := e.do(cookie, "", beginMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("4m old step-up: %+v", r.Errors)
	}
	e.setStepUp(u.ID, 6*time.Minute)
	if r, _ := e.do(cookie, "", beginMutation, nil); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("6m old step-up: %+v", r)
	}
	if r, _ := e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "PASSWORD", "p": "wrong-password-123"}); r.code() != "VALIDATION" {
		t.Fatalf("wrong password: %+v", r)
	}
	if r, _ := e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "TOTP", "c": "123456"}); r.code() != "VALIDATION" {
		t.Fatalf("TOTP step-up without a factor: %+v", r)
	}
	r, _ = e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "PASSWORD", "p": testPassword})
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["completeStepUp"]), `"aal2At":null,"aal":1`) {
		t.Fatalf("password step-up: %+v %s", r.Errors, r.Data["completeStepUp"])
	}
	if r, _ := e.do(cookie, "", beginMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("@stepUp after password step-up: %+v", r.Errors)
	}
	if sess := e.latestSession(u.ID); sess.Aal2At != nil {
		t.Fatal("a password step-up set the second-factor time")
	}
	if d := auditDetails(t, e.auditor, audit.ActionAuthStepUpSucceeded); len(d) != 1 || d[0]["method"] != "password" {
		t.Fatalf("step-up audit %+v", d)
	}
	if n := countAudit(t, e.auditor, audit.ActionAuthStepUpFailed); n != 1 {
		t.Fatalf("%d step-up failure rows", n)
	}
}

func TestStepUpWithFactor(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("finn@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	key, codes := e.enrollTOTP(cookie)
	_, pending := e.mfaLogin(u.Email)
	_, cookie = e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)})

	r, _ := e.do(cookie, "", beginStepUpMutation, nil)
	if len(r.Errors) > 0 || string(r.Data["beginStepUp"]) != `{"methods":["TOTP","RECOVERY_CODE"],"passkeyOptions":null}` {
		t.Fatalf("beginStepUp: %+v %s", r.Errors, r.Data["beginStepUp"])
	}
	// The password alone is refused for an account with a factor.
	if r, _ := e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "PASSWORD", "p": testPassword}); r.code() != "VALIDATION" {
		t.Fatalf("password step-up with a factor: %+v", r)
	}
	e.setStepUp(u.ID, 10*time.Minute)
	if _, err := e.pool.Exec(t.Context(), "UPDATE sessions SET aal2_at = NULL WHERE user_id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "TOTP", "c": e.code(key)})
	if len(r.Errors) > 0 || strings.Contains(string(r.Data["completeStepUp"]), `"aal2At":null`) {
		t.Fatalf("TOTP step-up: %+v %s", r.Errors, r.Data["completeStepUp"])
	}
	if sess := e.latestSession(u.ID); sess.Aal2At == nil || time.Since(*sess.Aal2At) > time.Minute {
		t.Fatalf("second-factor time not set: %+v", sess)
	}
	// A recovery code steps up but sets no second-factor time.
	e.setStepUp(u.ID, 10*time.Minute)
	if _, err := e.pool.Exec(t.Context(), "UPDATE sessions SET aal2_at = NULL WHERE user_id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "RECOVERY_CODE", "c": codes[1]})
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["completeStepUp"]), `"aal2At":null`) {
		t.Fatalf("recovery-code step-up: %+v %s", r.Errors, r.Data["completeStepUp"])
	}
	if sess := e.latestSession(u.ID); sess.Aal2At != nil || sess.StepupAt == nil || time.Since(*sess.StepupAt) > time.Minute {
		t.Fatalf("session after recovery-code step-up: %+v", sess)
	}
	methods := map[string]int{}
	for _, d := range auditDetails(t, e.auditor, audit.ActionAuthStepUpSucceeded) {
		if m, ok := d["method"].(string); ok {
			methods[m]++
		}
	}
	if methods["totp"] != 1 || methods["recovery_code"] != 1 {
		t.Fatalf("step-up audit methods %v", methods)
	}
}

// A passkey step-up of an AAL1 session (the factor was enrolled during
// the session) raises it to AAL2 and rotates it.
func TestStepUpPasskeyRotatesAAL1(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("ari@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	a := e.registerPasskey(cookie)
	r, _ := e.do(cookie, "", beginStepUpMutation, nil)
	var ch struct {
		Methods        []string        `json:"methods"`
		PasskeyOptions json.RawMessage `json:"passkeyOptions"`
	}
	if err := json.Unmarshal(r.Data["beginStepUp"], &ch); err != nil || !slices.Equal(ch.Methods, []string{"PASSKEY"}) {
		t.Fatalf("beginStepUp %+v %v %+v", ch, err, r.Errors)
	}
	r, w := e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "PASSKEY", "a": assertionOf(t, a, ch.PasskeyOptions)})
	rotated := sessionCookie(t, w)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["completeStepUp"]), `"aal":2`) || rotated == "" || rotated == cookie {
		t.Fatalf("passkey step-up: %+v %s cookie %q", r.Errors, r.Data["completeStepUp"], rotated)
	}
	if r, _ := e.do(cookie, "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("old cookie after rotation: %+v", r)
	}
	if r, _ := e.do(rotated, "", meQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("rotated cookie: %+v", r.Errors)
	}
}

func TestTwoFactorPolicyAdmin(t *testing.T) {
	e := newMFAEnv(t)
	admin := e.user("ada@example.com", sqlc.UserRoleAdmin)
	maint := e.user("max@example.com", sqlc.UserRoleMaintainer)
	_, ac := e.mfaLogin(admin.Email)
	_, mc := e.mfaLogin(maint.Email)

	r, _ := e.do(ac, "", policyQuery, nil)
	if len(r.Errors) > 0 || string(r.Data["twoFactorPolicy"]) !=
		`{"requireForAll":false,"requiredRoles":[],"gracePeriodDays":7,"updatedAt":null,"passkeysAvailable":true}` {
		t.Fatalf("default policy: %+v %s", r.Errors, r.Data["twoFactorPolicy"])
	}
	if r, _ := e.do(mc, "", policyQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer reads the policy: %+v", r)
	}
	vars := map[string]any{"all": false, "roles": []string{"ADMIN", "MAINTAINER"}, "g": 7}
	if r, _ := e.do(mc, "", updatePolicyMutation, vars); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer updates the policy: %+v", r)
	}
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{}, "g": 91}); r.code() != "VALIDATION" {
		t.Fatalf("grace 91: %+v", r)
	}
	e.setStepUp(admin.ID, 10*time.Minute)
	if r, _ := e.do(ac, "", updatePolicyMutation, vars); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("update without step-up: %+v", r)
	}
	e.setStepUp(admin.ID, 0)
	if r, _ := e.do(ac, "", updatePolicyMutation, vars); len(r.Errors) > 0 {
		t.Fatalf("update: %+v", r.Errors)
	}
	d := auditDetails(t, e.auditor, audit.ActionMFAPolicyUpdated)
	if len(d) != 1 || d[0]["weakened"] != false {
		t.Fatalf("policy audit %+v", d)
	}
	// Weakening is audited as such.
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"ADMIN"}, "g": 7}); len(r.Errors) > 0 {
		t.Fatalf("weaken: %+v", r.Errors)
	}
	if d := auditDetails(t, e.auditor, audit.ActionMFAPolicyUpdated); len(d) != 2 || d[0]["weakened"] != true {
		t.Fatalf("weakening audit %+v", d)
	}
}

// The enrollment requirement per role: within the grace period full access
// and a deadline on me; afterwards only enrollment, status and logout.
func TestForcedEnrollment(t *testing.T) {
	e := newMFAEnv(t)
	admin := e.user("ada@example.com", sqlc.UserRoleAdmin)
	maint := e.user("max@example.com", sqlc.UserRoleMaintainer)
	user := e.user("uma@example.com", sqlc.UserRoleUser)
	_, ac := e.mfaLogin(admin.Email)
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"ADMIN", "MAINTAINER"}, "g": 7}); len(r.Errors) > 0 {
		t.Fatalf("policy: %+v", r.Errors)
	}

	// Within the grace period.
	out, mc := e.mfaLogin(maint.Email)
	if out.TwoFactorEnrollmentRequired {
		t.Fatal("restricted within the grace period")
	}
	r, _ := e.do(mc, "", meMFAQuery, nil)
	var me struct {
		Required bool       `json:"twoFactorRequired"`
		Deadline *time.Time `json:"twoFactorEnrollmentDeadline"`
	}
	if err := json.Unmarshal(r.Data["me"], &me); err != nil || !me.Required || me.Deadline == nil ||
		me.Deadline.Sub(time.Now().Add(7*24*time.Hour)).Abs() > time.Minute {
		t.Fatalf("me within grace: %+v %v %+v", me, err, r.Errors)
	}
	if r, _ := e.do(mc, "", sessionsQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("sessions within grace: %+v", r.Errors)
	}
	// The user role is not required.
	_, uc := e.mfaLogin(user.Email)
	if r, _ := e.do(uc, "", meMFAQuery, nil); !strings.Contains(string(r.Data["me"]), `"twoFactorRequired":false,"twoFactorEnrollmentDeadline":null`) {
		t.Fatalf("me of a user: %s", r.Data["me"])
	}

	// The grace period ends (0 days): the maintainer is restricted at once.
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"ADMIN", "MAINTAINER"}, "g": 0}); len(r.Errors) > 0 {
		t.Fatalf("policy: %+v", r.Errors)
	}
	for _, q := range []string{sessionsQuery, `{ users { pageInfo { hasNextPage } } }`, `mutation { changePassword(currentPassword: "a", newPassword: "b") }`} {
		if r, _ := e.do(mc, "", q, nil); r.code() != "REQUIRES_2FA_ENROLLMENT" {
			t.Errorf("restricted %s: %+v", q, r)
		}
	}
	for _, q := range []string{meMFAQuery, statusQuery, myPasskeysQuery, webauthnStatusQuery, beginStepUpMutation} {
		if r, _ := e.do(mc, "", q, nil); len(r.Errors) > 0 {
			t.Errorf("restricted may %s: %+v", q, r.Errors)
		}
	}
	out, mc = e.mfaLogin(maint.Email)
	if !out.TwoFactorEnrollmentRequired || out.SecondFactorRequired {
		t.Fatalf("login of a restricted account %+v", out)
	}
	// The admins' own requirement: the admin has no factor either now.
	if r, _ := e.do(ac, "", policyQuery, nil); r.code() != "REQUIRES_2FA_ENROLLMENT" {
		t.Fatalf("restricted admin reads the policy: %+v", r)
	}
	// Enrolling lifts the restriction.
	e.enrollTOTP(mc)
	if r, _ := e.do(mc, "", sessionsQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("sessions after enrollment: %+v", r.Errors)
	}
	if r, _ := e.do(uc, "", sessionsQuery, nil); len(r.Errors) > 0 {
		t.Fatalf("user not required: %+v", r.Errors)
	}
}

func TestLastFactorGuard(t *testing.T) {
	e := newMFAEnv(t)
	admin := e.user("ada@example.com", sqlc.UserRoleAdmin)
	user := e.user("uma@example.com", sqlc.UserRoleUser)

	// Nothing is required by default (D-112); with 2FA required for admins
	// the last factor stays.
	_, ac := e.mfaLogin(admin.Email)
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"ADMIN"}, "g": 7}); len(r.Errors) > 0 {
		t.Fatalf("policy: %+v", r.Errors)
	}
	e.enrollTOTP(ac)
	r, _ := e.do(ac, "", disableMutation, nil)
	if r.code() != "CONFLICT" || !strings.Contains(r.Errors[0].Message, "last second factor") {
		t.Fatalf("disable the last factor of an admin: %+v", r)
	}
	if d := auditDetails(t, e.auditor, audit.ActionAuthMFARemovalRefused); len(d) != 1 || d[0]["factor"] != "totp" {
		t.Fatalf("refusal audit %+v", d)
	}
	// With a passkey as well, TOTP can go, but then the passkey cannot.
	e.registerPasskey(ac)
	if r, _ := e.do(ac, "", disableMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("disable TOTP with a passkey left: %+v", r.Errors)
	}
	r, _ = e.do(ac, "", myPasskeysQuery, nil)
	var pks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Data["myPasskeys"], &pks); err != nil || len(pks) != 1 {
		t.Fatalf("passkeys %v %v", pks, err)
	}
	if r, _ := e.do(ac, "", deleteMutation, map[string]any{"id": pks[0].ID}); r.code() != "CONFLICT" {
		t.Fatalf("delete the last passkey of an admin: %+v", r)
	}

	// Users are not required: removing the last factor is allowed (step-up
	// and audit as before).
	_, uc := e.mfaLogin(user.Email)
	e.enrollTOTP(uc)
	if r, _ := e.do(uc, "", disableMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("user disables the last factor: %+v", r.Errors)
	}
}

// A temporary password with a second factor: second step first, then only
// the password change.
func TestPasswordChangeAfterSecondFactor(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("tom@example.com", sqlc.UserRoleUser)
	_, cookie := e.mfaLogin(u.Email)
	key, _ := e.enrollTOTP(cookie)
	if _, err := e.pool.Exec(t.Context(), "UPDATE users SET password_temporary_until = now() + interval '1 hour' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	out, pending := e.mfaLogin(u.Email)
	if !out.SecondFactorRequired {
		t.Fatalf("login %+v", out)
	}
	e.pendingDenied(pending)
	r, full := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)})
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["completeSecondFactor"]), `"passwordChangeRequired":true`) {
		t.Fatalf("complete: %+v %s", r.Errors, r.Data["completeSecondFactor"])
	}
	if r, _ := e.do(full, "", regenMutation, nil); r.code() != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("recovery codes before the password change: %+v", r)
	}
	if r, _ := e.do(full, "", beginStepUpMutation, nil); len(r.Errors) > 0 {
		t.Fatalf("step-up before the password change: %+v", r.Errors)
	}
	if r, _ := e.do(full, "", `mutation($c: String!, $n: String!) { changePassword(currentPassword: $c, newPassword: $n) }`,
		map[string]any{"c": testPassword, "n": "an-entirely-new-passphrase-77"}); len(r.Errors) > 0 {
		t.Fatalf("change password: %+v", r.Errors)
	}
}

const resetFactorsMutation = `mutation($id: UUID!) { resetUserSecondFactors(userId: $id) {
	user { id twoFactorRequired twoFactorEnrollmentDeadline } removedTotp removedPasskeys removedRecoveryCodes revokedSessions } }`

// Lost-factor recovery (task 1.9): an admin resets every second factor of
// a user; the old factors and sessions stop working and a new enrollment
// grace period starts at the reset.
func TestResetUserSecondFactors(t *testing.T) {
	e := newMFAEnv(t)
	ctx := t.Context()
	admin := e.user("ada@example.com", sqlc.UserRoleAdmin)
	u := e.user("lou@example.com", sqlc.UserRoleUser)
	_, ac := e.mfaLogin(admin.Email)
	// 2FA required for users with a 2-day grace period, started long ago
	// (and the account is older too), so only the reset can give new time.
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"USER"}, "g": 2}); len(r.Errors) > 0 {
		t.Fatalf("policy: %+v", r.Errors)
	}
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err := e.d.SuperExec(ctx, "UPDATE settings SET value = jsonb_set(value, '{since,user}', to_jsonb('"+old+"'::text)) WHERE key = '"+
		auth.TwoFactorPolicySetting+"'"); err != nil {
		t.Fatal(err)
	}
	if err := e.d.SuperExec(ctx, "UPDATE users SET created_at = now() - interval '30 days' WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.policy.Get(ctx); err != nil { // refresh the cached policy
		t.Fatal(err)
	}

	// The user is restricted, enrolls TOTP (recovery codes) and a passkey.
	out, uc := e.mfaLogin(u.Email)
	if !out.TwoFactorEnrollmentRequired {
		t.Fatalf("login before enrollment %+v", out)
	}
	key, codes := e.enrollTOTP(uc)
	_, pending := e.mfaLogin(u.Email)
	r, uc := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)})
	if len(r.Errors) > 0 || uc == "" {
		t.Fatalf("second factor: %+v", r.Errors)
	}
	authn := e.registerPasskey(uc)

	idVars := map[string]any{"id": u.ID.String()}
	// Not your own account; unknown accounts; a stale step-up.
	if r, _ := e.do(ac, "", resetFactorsMutation, map[string]any{"id": admin.ID.String()}); r.code() != "FORBIDDEN" {
		t.Fatalf("self reset: %+v", r)
	}
	if r, _ := e.do(ac, "", resetFactorsMutation, map[string]any{"id": uuid.NewString()}); r.code() != "NOT_FOUND" {
		t.Fatalf("unknown user: %+v", r)
	}
	if r, _ := e.do(uc, "", resetFactorsMutation, idVars); r.code() != "FORBIDDEN" {
		t.Fatalf("user resets: %+v", r)
	}
	e.setStepUp(admin.ID, 10*time.Minute)
	if r, _ := e.do(ac, "", resetFactorsMutation, idVars); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r)
	}
	e.setStepUp(admin.ID, 0)

	r, _ = e.do(ac, "", resetFactorsMutation, idVars)
	var res struct {
		User struct {
			Required bool       `json:"twoFactorRequired"`
			Deadline *time.Time `json:"twoFactorEnrollmentDeadline"`
		} `json:"user"`
		RemovedTotp          bool `json:"removedTotp"`
		RemovedPasskeys      int  `json:"removedPasskeys"`
		RemovedRecoveryCodes int  `json:"removedRecoveryCodes"`
		RevokedSessions      int  `json:"revokedSessions"`
	}
	if len(r.Errors) > 0 {
		t.Fatalf("reset: %+v", r.Errors)
	}
	r.field(t, "resetUserSecondFactors", &res)
	if !res.RemovedTotp || res.RemovedPasskeys != 1 || res.RemovedRecoveryCodes != len(codes) || res.RevokedSessions < 1 ||
		!res.User.Required || res.User.Deadline == nil || res.User.Deadline.Sub(time.Now().Add(48*time.Hour)).Abs() > time.Minute {
		t.Fatalf("reset result %+v", res)
	}
	// The user's sessions are gone.
	if r, _ := e.do(uc, "", meQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("old session: %+v", r)
	}
	// The login is no longer two-step and, thanks to the new grace period,
	// not restricted.
	out, uc = e.mfaLogin(u.Email)
	if out.SecondFactorRequired || out.TwoFactorEnrollmentRequired {
		t.Fatalf("login after reset %+v", out)
	}
	// The old TOTP key, recovery codes and passkey no longer count: only
	// the password is left for step-up, and the old factors are refused.
	r, _ = e.do(uc, "", beginStepUpMutation, nil)
	if len(r.Errors) > 0 || !strings.Contains(string(r.Data["beginStepUp"]), `"methods":["PASSWORD"]`) ||
		!strings.Contains(string(r.Data["beginStepUp"]), `"passkeyOptions":null`) {
		t.Fatalf("step-up methods after reset: %+v %s", r.Errors, r.Data["beginStepUp"])
	}
	for _, vars := range []map[string]any{
		{"m": "TOTP", "c": e.code(key)},
		{"m": "RECOVERY_CODE", "c": codes[1]},
		{"m": "PASSKEY", "a": assertionOf(t, authn, json.RawMessage(`{"publicKey":{"challenge":"AAAAAAAAAAAAAAAAAAAAAA","rpId":"localhost","allowCredentials":[]}}`))},
	} {
		if r, _ := e.do(uc, "", completeStepUpMutation, vars); len(r.Errors) == 0 {
			t.Errorf("old factor %v accepted for step-up", vars["m"])
		}
		if r, _ := e.do(uc, "", completeMutation, vars); len(r.Errors) == 0 {
			t.Errorf("old factor %v completed a login", vars["m"])
		}
	}
	var left int
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM recovery_codes WHERE user_id = $1)
		+ (SELECT count(*) FROM webauthn_credentials WHERE user_id = $1)
		+ (SELECT count(*) FROM users WHERE id = $1 AND (totp_secret_enc IS NOT NULL OR mfa_reset_at IS NULL))`, u.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("factor rows left %d %v", left, err)
	}
	if d := auditDetails(t, e.auditor, audit.ActionUserSecondFactorsReset); len(d) != 1 || d[0]["authenticator_app_removed"] != true {
		t.Fatalf("audit %+v", d)
	}
	if ents, err := e.auditor.List(ctx, audit.Filter{Action: audit.ActionUserSecondFactorsReset}, 0, 1); err != nil ||
		ents.Entries[0].ActorUserID == nil || *ents.Entries[0].ActorUserID != admin.ID {
		t.Fatalf("audit actor %+v %v", ents, err)
	}

	// A grace period of 0: the reset account is restricted at once.
	if r, _ := e.do(ac, "", updatePolicyMutation, map[string]any{"all": false, "roles": []string{"USER"}, "g": 0}); len(r.Errors) > 0 {
		t.Fatalf("policy: %+v", r.Errors)
	}
	if r, _ := e.do(uc, "", sessionsQuery, nil); r.code() != "REQUIRES_2FA_ENROLLMENT" {
		t.Fatalf("zero grace after reset: %+v", r)
	}
}

// D-116: only a user-verified passkey assertion (UV flag) sets the
// session's second-factor time that restores require; a user-presence-only
// assertion still completes the login and a step-up.
func TestPasskeyAAL2RequiresUserVerification(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("uv@example.com", sqlc.UserRoleAdmin)
	_, cookie := e.mfaLogin(u.Email)
	a := e.registerPasskey(cookie)
	policy := authz.New(authz.Options{SecondFactors: authztest.SecondFactors{Enrolled: map[uuid.UUID]bool{u.ID: true}}})
	restore := func(sess sqlc.Session) error {
		p := auth.Principal{UserID: u.ID, SessionID: sess.ID, Role: auth.RoleAdmin, AAL: int(sess.Aal)}
		if sess.StepupAt != nil {
			p.StepUpAt = *sess.StepupAt
		}
		if sess.Aal2At != nil {
			p.AAL2At = *sess.Aal2At
		}
		return policy.Authorize(auth.WithPrincipal(t.Context(), p), authz.OpRestoreGlobals)
	}
	login := func(uv bool) (sqlc.Session, string) {
		t.Helper()
		a.UserVerified = uv
		_, pending := e.mfaLogin(u.Email)
		r, _ := e.do(pending, "", beginPasskeyLoginMut, nil)
		if len(r.Errors) > 0 {
			t.Fatalf("begin passkey login: %+v", r.Errors)
		}
		r, full := e.complete(pending, map[string]any{"m": "PASSKEY", "a": assertionOf(t, a, r.Data["beginPasskeyLogin"])})
		if len(r.Errors) > 0 || full == "" {
			t.Fatalf("complete with passkey (uv=%v): %+v", uv, r.Errors)
		}
		return e.latestSession(u.ID), full
	}
	stepUp := func(cookie string, uv bool) string {
		t.Helper()
		a.UserVerified = uv
		r, _ := e.do(cookie, "", beginStepUpMutation, nil)
		var ch struct {
			PasskeyOptions json.RawMessage `json:"passkeyOptions"`
		}
		if err := json.Unmarshal(r.Data["beginStepUp"], &ch); err != nil || len(r.Errors) > 0 {
			t.Fatalf("beginStepUp: %v %+v", err, r.Errors)
		}
		r, _ = e.do(cookie, "", completeStepUpMutation, map[string]any{"m": "PASSKEY", "a": assertionOf(t, a, ch.PasskeyOptions)})
		if len(r.Errors) > 0 {
			t.Fatalf("passkey step-up (uv=%v): %+v", uv, r.Errors)
		}
		return string(r.Data["completeStepUp"])
	}

	sess, up := login(false)
	if sess.Aal != 2 || sess.Aal2At != nil || sess.StepupAt == nil {
		t.Fatalf("UP-only passkey login: %+v", sess)
	}
	if err := restore(sess); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Fatalf("restore after a UP-only passkey login: %v", err)
	}
	// A UP-only step-up sets the step-up time only; a UV one the
	// second-factor time as well.
	if out := stepUp(up, false); !strings.Contains(out, `"aal2At":null`) {
		t.Fatalf("UP-only step-up result %s", out)
	}
	if sess := e.latestSession(u.ID); sess.Aal2At != nil || restore(sess) == nil {
		t.Fatalf("UP-only step-up set the second-factor time: %+v", sess)
	}
	if out := stepUp(up, true); strings.Contains(out, `"aal2At":null`) {
		t.Fatalf("UV step-up result %s", out)
	}
	if sess := e.latestSession(u.ID); sess.Aal2At == nil || restore(sess) != nil {
		t.Fatalf("UV step-up: %+v %v", sess, restore(sess))
	}

	sess, _ = login(true)
	if sess.Aal != 2 || sess.Aal2At == nil {
		t.Fatalf("UV passkey login: %+v", sess)
	}
	if err := restore(sess); err != nil {
		t.Fatalf("restore after a UV passkey login: %v", err)
	}
}
