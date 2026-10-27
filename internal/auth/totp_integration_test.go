//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

type recFactorNotifier struct {
	mu      sync.Mutex
	used    []SecondFactorEvent
	removed []SecondFactorEvent
	locked  []SecondFactorEvent
}

func (r *recFactorNotifier) RecoveryCodeUsed(_ context.Context, ev SecondFactorEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.used = append(r.used, ev)
}

func (r *recFactorNotifier) SecondFactorRemoved(_ context.Context, ev SecondFactorEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, ev)
}

func (r *recFactorNotifier) SecondFactorLocked(_ context.Context, ev SecondFactorEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locked = append(r.locked, ev)
}

type guardFunc func(ctx context.Context, userID uuid.UUID, removing Factor, remaining []Factor) error

func (g guardFunc) CheckFactorRemoval(ctx context.Context, userID uuid.UUID, removing Factor, remaining []Factor) error {
	return g(ctx, userID, removing, remaining)
}

// fixedFactor reports a fixed answer for one factor kind.
type fixedFactor struct {
	kind Factor
	has  bool
}

func (f fixedFactor) Kind() Factor { return f.kind }
func (f fixedFactor) HasFactor(context.Context, uuid.UUID) (bool, error) {
	return f.has, nil
}

type totpEnv struct {
	*env
	kr       *crypto.Keyring
	notifier *recFactorNotifier
	factors  *SecondFactors
	guard    FactorRemovalGuard
	svc      *TOTP
}

func newTOTPEnv(t *testing.T, extra ...FactorChecker) *totpEnv {
	t.Helper()
	e := &totpEnv{env: newEnv(t), notifier: &recFactorNotifier{}}
	var err error
	e.kr, err = crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.kr.Destroy)
	e.factors = NewSecondFactors(extra...)
	e.svc, err = NewTOTP(TOTPOptions{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Factors: e.factors,
		RemovalGuard: guardFunc(func(ctx context.Context, u uuid.UUID, f Factor, rem []Factor) error {
			if e.guard == nil {
				return nil
			}
			return e.guard.CheckFactorRemoval(ctx, u, f, rem)
		}),
		Lockout:  LockoutPolicy{Threshold: 3, Base: time.Minute, Max: 10 * time.Minute},
		Notifier: e.notifier, Now: e.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.factors.Register(e.svc)
	return e
}

// principal is a signed-in user with a fresh step-up.
func (e *totpEnv) principal(u sqlc.User) Principal {
	return Principal{UserID: u.ID, Role: Role(u.Role), SessionID: uuid.New(), AAL: AAL1, StepUpAt: e.clock.Now()}
}

func (e *totpEnv) ctx() context.Context { return WithRequestMeta(e.t.Context(), testMeta) }

// enroll runs a complete enrollment and returns the raw secret and the
// recovery codes.
func (e *totpEnv) enroll(u sqlc.User) ([]byte, []string) {
	e.t.Helper()
	p := e.principal(u)
	en, err := e.svc.BeginTOTPEnrollment(e.ctx(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	secret := decodeManualKey(e.t, en.ManualKey)
	codes, err := e.svc.ConfirmTOTPEnrollment(e.ctx(), p, codeAt(e.t, secret, e.clock.Now()))
	if err != nil {
		e.t.Fatal(err)
	}
	return secret, codes
}

func decodeManualKey(t *testing.T, k string) []byte {
	t.Helper()
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(k)
	if err != nil || len(b) != TOTPSecretBytes {
		t.Fatalf("manual key %v (%d bytes)", err, len(b))
	}
	return b
}

func (e *totpEnv) status(id uuid.UUID) TwoFactorStatus {
	e.t.Helper()
	s, err := e.svc.Status(e.ctx(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *totpEnv) hasFactor(id uuid.UUID) bool {
	e.t.Helper()
	has, err := e.factors.HasSecondFactor(e.ctx(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return has
}

func (e *totpEnv) state(id uuid.UUID) sqlc.GetUserTOTPStateRow {
	e.t.Helper()
	st, err := sqlc.New(e.pool).GetUserTOTPState(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// nextStep advances the clock by one TOTP period, so the next code is a
// new step.
func (e *totpEnv) nextStep() { e.clock.Advance(TOTPPeriod) }

func TestTOTPEnrollment(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("alice@example.com", sqlc.UserRoleUser)
	p := e.principal(u)
	ctx := e.ctx()

	en, err := e.svc.BeginTOTPEnrollment(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	secret := decodeManualKey(t, en.ManualKey)
	if !strings.HasPrefix(en.URI, "otpauth://totp/bekup:alice@example.com?") ||
		!strings.Contains(en.URI, "secret="+en.ManualKey) || !strings.Contains(en.URI, "algorithm=SHA1") ||
		!strings.Contains(en.URI, "digits=6") || !strings.Contains(en.URI, "period=30") {
		t.Fatalf("URI %s", en.URI)
	}
	if !strings.HasPrefix(en.QRCodeSVG, "<svg ") || strings.Contains(en.QRCodeSVG, "data:") {
		t.Fatal("QR code is not an inline SVG without data: URIs")
	}

	// Pending: sealed under its own AAD; no plaintext in the database.
	st := e.state(u.ID)
	if st.TotpSecretEnc != nil || st.TotpPendingEnc == nil || st.TotpPendingExpiresAt == nil {
		t.Fatal("pending secret not stored as pending")
	}
	assertNoPlaintext(t, st.TotpPendingEnc, secret, en.ManualKey)
	if _, err := e.kr.Open(st.TotpPendingEnc, totpAAD(u.ID, aadFieldTOTPSecret)); err == nil {
		t.Fatal("pending ciphertext opens as an active secret")
	}
	if pt, err := e.kr.Open(st.TotpPendingEnc, totpAAD(u.ID, aadFieldTOTPPending)); err != nil || !bytes.Equal(pt, secret) {
		t.Fatal("pending ciphertext does not open under its AAD")
	}
	if s := e.status(u.ID); s.TOTPEnabled || !s.TOTPEnrollmentPending {
		t.Fatalf("status %+v", s)
	}
	if has := e.hasFactor(u.ID); has {
		t.Fatal("a pending enrollment counts as a factor")
	}

	// A wrong code does not confirm.
	if _, err := e.svc.ConfirmTOTPEnrollment(ctx, p, "000000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: %v", err)
	}
	code := codeAt(t, secret, e.clock.Now())
	codes, err := e.svc.ConfirmTOTPEnrollment(ctx, p, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d recovery codes", len(codes))
	}

	st = e.state(u.ID)
	if st.TotpSecretEnc == nil || st.TotpPendingEnc != nil || st.TotpLastStep == nil || st.TotpEnabledAt == nil {
		t.Fatalf("not enabled: %+v", st)
	}
	assertNoPlaintext(t, st.TotpSecretEnc, secret, en.ManualKey)
	if pt, err := e.kr.Open(st.TotpSecretEnc, totpAAD(u.ID, aadFieldTOTPSecret)); err != nil || !bytes.Equal(pt, secret) {
		t.Fatal("active secret does not open under users:<id>:totp_secret")
	}
	for _, wrong := range []crypto.AAD{
		totpAAD(u.ID, aadFieldTOTPPending), totpAAD(uuid.New(), aadFieldTOTPSecret),
		{Table: "sessions", RowID: u.ID.String(), Field: aadFieldTOTPSecret},
	} {
		if _, err := e.kr.Open(st.TotpSecretEnc, wrong); err == nil {
			t.Fatalf("active secret opens under %s", wrong)
		}
	}
	// The confirmation code's step is used: it cannot log in.
	if err := e.svc.VerifyTOTP(ctx, u.ID, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("confirmation code replayed: %v", err)
	}
	if _, err := e.svc.BeginTOTPEnrollment(ctx, p); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("re-enroll while enabled: %v", err)
	}
	s, err := e.svc.Status(ctx, u.ID)
	if err != nil || !s.TOTPEnabled || s.TOTPEnrollmentPending || s.RecoveryCodesRemaining != RecoveryCodeCount {
		t.Fatalf("status %+v %v", s, err)
	}
	if has := e.hasFactor(u.ID); !has {
		t.Fatal("TOTP does not count as a factor")
	}

	if n := len(e.entries(audit.ActionAuthMFAEnrollmentStarted)); n != 1 {
		t.Fatalf("%d enrollment_started entries", n)
	}
	enrolled := e.entries(audit.ActionAuthMFAEnrolled)
	if len(enrolled) != 1 || !detail(t, enrolled[0].Details, "factor", "totp") {
		t.Fatalf("enrolled entries %+v", enrolled)
	}
	failed := e.entries(audit.ActionAuthMFAFailed)
	if len(failed) != 2 { // the wrong confirmation code and the replay
		t.Fatalf("%d failure entries", len(failed))
	}
	for _, en := range append(failed, enrolled...) {
		if strings.Contains(string(en.Details), code) {
			t.Fatal("a code was written to the audit log")
		}
	}
}

// detail reports whether the audit details hold key = want.
func detail(t *testing.T, raw json.RawMessage, key string, want any) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m[key] == want
}

func assertNoPlaintext(t *testing.T, enc, secret []byte, b32 string) {
	t.Helper()
	if bytes.Contains(enc, secret) || bytes.Contains(enc, []byte(b32)) || bytes.Contains(bytes.ToUpper(enc), []byte(b32)) {
		t.Fatal("plaintext secret in the database")
	}
}

func TestTOTPEnrollmentExpires(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("bob@example.com", sqlc.UserRoleUser)
	en, err := e.svc.BeginTOTPEnrollment(e.ctx(), e.principal(u))
	if err != nil {
		t.Fatal(err)
	}
	secret := decodeManualKey(t, en.ManualKey)
	e.clock.Advance(TOTPEnrollmentTTL + time.Second)
	if _, err := e.svc.ConfirmTOTPEnrollment(e.ctx(), e.principal(u), codeAt(t, secret, e.clock.Now())); !errors.Is(err, ErrNoEnrollmentPending) {
		t.Fatalf("expired enrollment: %v", err)
	}
	if _, err := e.svc.ConfirmTOTPEnrollment(e.ctx(), e.principal(e.user("c@example.com", sqlc.UserRoleUser)), "123456"); !errors.Is(err, ErrNoEnrollmentPending) {
		t.Fatalf("no enrollment: %v", err)
	}
}

func TestTOTPStepUpRequired(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("carol@example.com", sqlc.UserRoleUser)
	e.enroll(u)
	stale := e.principal(u)
	stale.StepUpAt = e.clock.Now().Add(-6 * time.Minute)
	none := e.principal(u)
	none.StepUpAt = time.Time{}
	for _, p := range []Principal{stale, none} {
		if _, err := e.svc.BeginTOTPEnrollment(e.ctx(), p); !errors.Is(err, ErrStepUpRequired) {
			t.Errorf("begin: %v", err)
		}
		if _, err := e.svc.ConfirmTOTPEnrollment(e.ctx(), p, "123456"); !errors.Is(err, ErrStepUpRequired) {
			t.Errorf("confirm: %v", err)
		}
		if err := e.svc.DisableTOTP(e.ctx(), p); !errors.Is(err, ErrStepUpRequired) {
			t.Errorf("disable: %v", err)
		}
		if _, err := e.svc.RegenerateRecoveryCodes(e.ctx(), p); !errors.Is(err, ErrStepUpRequired) {
			t.Errorf("regenerate: %v", err)
		}
	}
	if s := e.status(u.ID); !s.TOTPEnabled || s.RecoveryCodesRemaining != RecoveryCodeCount {
		t.Fatal("a refused operation changed state")
	}
}

func TestVerifyTOTPReplayAndSkew(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("dave@example.com", sqlc.UserRoleUser)
	secret, _ := e.enroll(u)
	ctx := e.ctx()
	e.nextStep()
	e.nextStep()
	now := e.clock.Now()

	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, now.Add(-90*time.Second))); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code outside the skew window: %v", err)
	}
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, now.Add(60*time.Second))); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code two steps ahead: %v", err)
	}
	code := codeAt(t, secret, now)
	if err := e.svc.VerifyTOTP(ctx, u.ID, code); err != nil {
		t.Fatalf("valid code: %v", err)
	}
	if st := e.state(u.ID); st.MfaFailedCount != 0 {
		t.Fatal("success did not reset the failure counter")
	}
	if err := e.svc.VerifyTOTP(ctx, u.ID, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("replayed code: %v", err)
	}
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, now.Add(-30*time.Second))); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("older step (within the window): %v", err)
	}
	// The next step (clock skew ahead) is still fine.
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, now.Add(30*time.Second))); err != nil {
		t.Fatalf("next step: %v", err)
	}
	if n := len(e.entries(audit.ActionAuthMFAVerified)); n != 2 {
		t.Fatalf("%d verified entries", n)
	}
	failedEntries := e.entries(audit.ActionAuthMFAFailed)
	reasons := make([]string, 0, len(failedEntries))
	for _, en := range failedEntries {
		reasons = append(reasons, string(en.Details))
	}
	if len(reasons) != 4 || !strings.Contains(strings.Join(reasons, ""), `"reason": "bad_code"`) {
		t.Fatalf("failure entries %v", reasons)
	}

	// Unknown users and users without TOTP get the same answer.
	if err := e.svc.VerifyTOTP(ctx, uuid.New(), code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("unknown user: %v", err)
	}
	other := e.user("erin@example.com", sqlc.UserRoleUser)
	if err := e.svc.VerifyTOTP(ctx, other.ID, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("user without TOTP: %v", err)
	}
}

func TestVerifyTOTPConcurrentSameCode(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("frank@example.com", sqlc.UserRoleUser)
	secret, _ := e.enroll(u)
	e.nextStep()
	code := codeAt(t, secret, e.clock.Now())
	const n = 12
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = e.svc.VerifyTOTP(e.ctx(), u.ID, code)
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInvalidCode):
		default:
			var le *SecondFactorLockedError
			if !errors.As(err, &le) {
				t.Fatalf("unexpected error %v", err)
			}
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent verifications of one code succeeded, want exactly 1", ok)
	}
}

func TestSecondFactorLockout(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("grace@example.com", sqlc.UserRoleUser)
	secret, codes := e.enroll(u)
	ctx := e.ctx()
	for range 3 {
		if err := e.svc.VerifyTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrInvalidCode) {
			t.Fatal(err)
		}
	}
	st := e.state(u.ID)
	if st.MfaFailedCount != 3 || st.MfaLockedUntil == nil {
		t.Fatalf("not locked: %+v", st)
	}
	e.nextStep()
	var le *SecondFactorLockedError
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, e.clock.Now())); !errors.As(err, &le) || le.RetryAfter <= 0 {
		t.Fatalf("locked TOTP check: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, codes[0]); !errors.As(err, &le) {
		t.Fatalf("locked recovery code: %v", err)
	}
	if st := e.state(u.ID); st.MfaFailedCount != 3 {
		t.Fatal("attempts while locked were counted")
	}
	if len(e.notifier.locked) != 1 || len(e.entries(audit.ActionAuthMFALockout)) != 1 {
		t.Fatal("lockout not notified/audited once")
	}
	// The password lockout is untouched.
	if du := e.dbUser(u.ID); du.FailedLoginCount != 0 || du.LockedUntil != nil {
		t.Fatal("second-factor failures changed the password lockout")
	}
	e.clock.Advance(2 * time.Minute)
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, e.clock.Now())); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
	if st := e.state(u.ID); st.MfaFailedCount != 0 || st.MfaLockedUntil != nil {
		t.Fatal("success did not reset the lock")
	}
}

func TestRecoveryCodes(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("heidi@example.com", sqlc.UserRoleUser)
	_, codes := e.enroll(u)
	ctx := e.ctx()

	// Only keyed hashes are stored.
	var rows []sqlc.ListUnusedRecoveryCodesRow
	rows, err := sqlc.New(e.pool).ListUnusedRecoveryCodes(ctx, u.ID)
	if err != nil || len(rows) != RecoveryCodeCount {
		t.Fatalf("%d rows %v", len(rows), err)
	}
	for _, r := range rows {
		for _, c := range codes {
			norm, _ := normalizeRecoveryCode(c)
			if bytes.Contains(r.CodeMac, []byte(norm)) || bytes.Contains(r.CodeMac, []byte(c)) {
				t.Fatal("plaintext recovery code stored")
			}
		}
		if r.KeyID != e.kr.PrimaryID().String() {
			t.Fatal("key id not recorded")
		}
	}

	// Case, dashes and look-alikes do not matter.
	if err := e.svc.UseRecoveryCode(ctx, u.ID, strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))); err != nil {
		t.Fatalf("use: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, codes[0]); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("reuse: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, "0000-0000-0000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: %v", err)
	}
	// Another user's code does not work.
	v := e.user("ivan@example.com", sqlc.UserRoleUser)
	if err := e.svc.UseRecoveryCode(ctx, v.ID, codes[1]); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("foreign code: %v", err)
	}
	if s := e.status(u.ID); s.RecoveryCodesRemaining != RecoveryCodeCount-1 {
		t.Fatalf("remaining %d", s.RecoveryCodesRemaining)
	}
	used := e.entries(audit.ActionAuthRecoveryCodeUsed)
	if len(used) != 1 || !detail(t, used[0].Details, "recovery_codes_remaining", float64(9)) {
		t.Fatalf("used entries %+v", used)
	}
	if len(e.notifier.used) != 1 || e.notifier.used[0].Remaining != 9 {
		t.Fatalf("notifier %+v", e.notifier.used)
	}

	// Concurrent use of one code: exactly one succeeds.
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = e.svc.UseRecoveryCode(e.ctx(), u.ID, codes[2])
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent uses of one recovery code succeeded, want exactly 1", ok)
	}
	// Reset the failure counter the losers produced.
	if err := sqlc.New(e.pool).ResetMFAFailures(ctx, u.ID); err != nil {
		t.Fatal(err)
	}

	// Regenerating invalidates every old code.
	fresh, err := e.svc.RegenerateRecoveryCodes(ctx, e.principal(u))
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerate: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, codes[3]); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("old code after regenerate: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, fresh[0]); err != nil {
		t.Fatalf("new code: %v", err)
	}
	if n := len(e.entries(audit.ActionAuthRecoveryCodesRegenerated)); n != 1 {
		t.Fatalf("%d regenerated entries", n)
	}

	// A key rotation keeps old codes valid.
	if _, err := e.kr.AddPrimary(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, fresh[1]); err != nil {
		t.Fatalf("code after key rotation: %v", err)
	}
}

func TestDisableTOTP(t *testing.T) {
	e := newTOTPEnv(t)
	u := e.user("judy@example.com", sqlc.UserRoleUser)
	secret, codes := e.enroll(u)
	ctx := e.ctx()

	e.guard = guardFunc(func(_ context.Context, _ uuid.UUID, f Factor, rem []Factor) error {
		if f != FactorTOTP || len(rem) != 0 {
			t.Errorf("guard got %s %v", f, rem)
		}
		return ErrLastFactor
	})
	if err := e.svc.DisableTOTP(ctx, e.principal(u)); !errors.Is(err, ErrLastFactor) {
		t.Fatalf("guard: %v", err)
	}
	if s := e.status(u.ID); !s.TOTPEnabled {
		t.Fatal("refused disable changed state")
	}
	e.guard = nil
	if err := e.svc.DisableTOTP(ctx, e.principal(u)); err != nil {
		t.Fatal(err)
	}
	st := e.state(u.ID)
	if st.TotpSecretEnc != nil || st.TotpLastStep != nil || st.TotpEnabledAt != nil {
		t.Fatal("secret not removed")
	}
	if s := e.status(u.ID); s.TOTPEnabled || s.RecoveryCodesRemaining != 0 {
		t.Fatalf("status %+v", s)
	}
	e.nextStep()
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, e.clock.Now())); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("verify after disable: %v", err)
	}
	if err := e.svc.UseRecoveryCode(ctx, u.ID, codes[0]); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("recovery code after the last factor was removed: %v", err)
	}
	if _, err := e.svc.RegenerateRecoveryCodes(ctx, e.principal(u)); !errors.Is(err, ErrNoSecondFactor) {
		t.Fatalf("regenerate without factor: %v", err)
	}
	if err := e.svc.DisableTOTP(ctx, e.principal(u)); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Fatalf("disable twice: %v", err)
	}
	removed := e.entries(audit.ActionAuthMFARemoved)
	if len(removed) != 1 || !detail(t, removed[0].Details, "recovery_codes_deleted", float64(10)) {
		t.Fatalf("removed entries %+v", removed)
	}
	if len(e.notifier.removed) != 1 || e.notifier.removed[0].Factor != FactorTOTP {
		t.Fatal("removal not notified")
	}
}

func TestDisableTOTPKeepsCodesWithOtherFactor(t *testing.T) {
	e := newTOTPEnv(t, fixedFactor{kind: FactorPasskey, has: true})
	u := e.user("ken@example.com", sqlc.UserRoleUser)
	_, codes := e.enroll(u)
	var got []Factor
	e.guard = guardFunc(func(_ context.Context, _ uuid.UUID, _ Factor, rem []Factor) error {
		got = rem
		return nil
	})
	if err := e.svc.DisableTOTP(e.ctx(), e.principal(u)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != FactorPasskey {
		t.Fatalf("remaining %v", got)
	}
	if err := e.svc.UseRecoveryCode(e.ctx(), u.ID, codes[0]); err != nil {
		t.Fatalf("recovery code with a passkey left: %v", err)
	}
}
