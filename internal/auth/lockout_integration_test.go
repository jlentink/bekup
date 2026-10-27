//go:build integration

package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// countReason counts the entries of action whose details have reason.
func (e *env) countReason(action audit.Action, reason string) int {
	e.t.Helper()
	n := 0
	entries := e.entries(action)
	for i := range entries {
		var d map[string]any
		if err := json.Unmarshal(entries[i].Details, &d); err != nil {
			e.t.Fatal(err)
		}
		if d["reason"] == reason {
			n++
		}
	}
	return n
}

// refusedWhileLocked returns the largest refused_while_locked count in the
// entries of action.
func (e *env) refusedWhileLocked(action audit.Action) float64 {
	e.t.Helper()
	var maxN float64
	entries := e.entries(action)
	for i := range entries {
		var d map[string]any
		if err := json.Unmarshal(entries[i].Details, &d); err != nil {
			e.t.Fatal(err)
		}
		if n, ok := d["refused_while_locked"].(float64); ok && n > maxN {
			maxN = n
		}
	}
	return maxN
}

// M1 review M1 (security): concurrent wrong TOTP codes are reserved
// atomically, so no more than the threshold are ever verified; a correct
// code during the lock is refused; refusals while locked are recorded once
// per lock window and their number reported later (M2).
func TestSecondFactorLockoutConcurrent(t *testing.T) {
	e := newTOTPEnv(t) // threshold 3
	u := e.user("race@example.com", sqlc.UserRoleUser)
	secret, _ := e.enroll(u)
	e.nextStep()
	ctx := e.ctx()
	const attempts = 50
	var invalid, locked, other atomic.Int64
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			var le *SecondFactorLockedError
			switch err := e.svc.VerifyTOTP(ctx, u.ID, "000000"); {
			case errors.Is(err, ErrInvalidCode):
				invalid.Add(1)
			case errors.As(err, &le):
				locked.Add(1)
			default:
				other.Add(1)
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if invalid.Load() != 3 || locked.Load() != attempts-3 || other.Load() != 0 {
		t.Fatalf("verified %d, refused %d, other %d; want 3 verified", invalid.Load(), locked.Load(), other.Load())
	}
	st := e.state(u.ID)
	if st.MfaFailedCount != 3 || st.MfaLockedUntil == nil {
		t.Fatalf("state %+v", st)
	}
	// The lock is the policy's backoff for the third failure.
	want := e.clock.Now().Add(e.svc.o.Lockout.LockFor(3))
	if d := st.MfaLockedUntil.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("locked until %s, want ~%s", st.MfaLockedUntil, want)
	}
	if e.countReason(audit.ActionAuthMFAFailed, "bad_code") != 3 || len(e.entries(audit.ActionAuthMFALockout)) != 1 {
		t.Fatal("failures/lockout not audited exactly once each")
	}
	if n := e.countReason(audit.ActionAuthMFAFailed, "locked"); n != 1 {
		t.Fatalf("%d refusal entries while locked, want 1 (aggregated)", n)
	}
	var le *SecondFactorLockedError
	if err := e.svc.VerifyTOTP(ctx, u.ID, codeAt(t, secret, e.clock.Now())); !errors.As(err, &le) {
		t.Fatalf("correct code during the lock: %v", err)
	}
	// After the lock the next failure reports the unrecorded refusals
	// (46 of the burst + the correct code) and doubles the lock.
	e.clock.Advance(2 * time.Minute)
	if err := e.svc.VerifyTOTP(ctx, u.ID, "000000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatal(err)
	}
	if n := e.refusedWhileLocked(audit.ActionAuthMFAFailed); n != attempts-3-1+1 {
		t.Fatalf("refused_while_locked %v, want %d", n, attempts-3)
	}
	st = e.state(u.ID)
	if d := st.MfaLockedUntil.Sub(e.clock.Now()); d < 110*time.Second || d > 130*time.Second {
		t.Fatalf("second lock %s, want ~2m", d)
	}
}

// The same reservation guards password checks: concurrent wrong passwords
// verify at most the threshold; the right one during the lock fails
// generically (with the dummy verification).
func TestLoginLockoutConcurrent(t *testing.T) {
	e := newEnv(t) // threshold 3, 4 hashing slots
	u := e.user("prace@example.com", sqlc.UserRoleUser)
	const attempts = 15
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			if _, err := e.login(u.Email, "wrong-password-123"); !errors.Is(err, ErrInvalidCredentials) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got := e.dbUser(u.ID)
	if got.FailedLoginCount != 3 || got.LockedUntil == nil {
		t.Fatalf("state %+v", got)
	}
	if n := e.countReason(audit.ActionAuthLoginFailed, reasonBadPassword); n != 3 {
		t.Fatalf("%d verified failures, want 3", n)
	}
	if n := e.countReason(audit.ActionAuthLoginFailed, reasonLocked); n != 1 {
		t.Fatalf("%d refusal entries while locked, want 1", n)
	}
	if len(e.entries(audit.ActionAuthLockout)) != 1 {
		t.Fatal("lockout not audited once")
	}
	if _, err := e.login(u.Email, goodPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("right password during the lock: %v", err)
	}
}

// M1 review H1 (c): an authenticated step-up or password change of a
// locked account is refused before argon2id runs (no dummy verification:
// the caller owns the account); a verified password resets the count.
func TestPasswordStepUpWhileLocked(t *testing.T) {
	e := newEnv(t)
	u := e.user("stepup@example.com", sqlc.UserRoleUser)
	p, err := e.resolve(e.mustLogin(u.Email).Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := e.t.Context()
	if err := e.svc.VerifyPasswordStepUp(ctx, u.ID, "wrong-password-123", testMeta); !errors.Is(err, ErrWrongPassword) {
		t.Fatal(err)
	}
	if err := e.svc.VerifyPasswordStepUp(ctx, u.ID, goodPassword, testMeta); err != nil {
		t.Fatal(err)
	}
	if n := e.dbUser(u.ID).FailedLoginCount; n != 0 {
		t.Fatalf("a verified step-up left failed_login_count = %d", n)
	}
	for range 3 {
		if _, err := e.login(u.Email, "wrong-password-123"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	d0 := e.hasher.DummyVerifications()
	var rl *RateLimitedError
	if err := e.svc.VerifyPasswordStepUp(ctx, u.ID, goodPassword, testMeta); !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("step-up while locked: %v", err)
	}
	if _, err := e.svc.ChangePassword(ctx, p, goodPassword, "new-granite-harbor-77", testMeta); !errors.As(err, &rl) {
		t.Fatalf("password change while locked: %v", err)
	}
	if e.hasher.DummyVerifications() != d0 {
		t.Fatal("a locked authenticated check ran argon2id")
	}
	if n := e.dbUser(u.ID).FailedLoginCount; n != 3 {
		t.Fatalf("refusals while locked were counted: %d", n)
	}
}

// M1 review H1 (b): the per-account limiter is taken before every
// password verification, for unknown addresses exactly like known ones.
func TestAccountLimiter(t *testing.T) {
	e := newEnv(t)
	u := e.user("limit@example.com", sqlc.UserRoleUser)
	var mu sync.Mutex
	taken := map[string]int{}
	svc, err := NewService(ServiceOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: e.sessions, Auditor: e.auditor, Factors: NewSecondFactors(),
		Lockout: LockoutPolicy{Threshold: 20, Base: time.Minute, Max: time.Hour}, Now: e.clock.Now,
		AccountLimiter: func(key string) (bool, time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			taken[key]++
			return taken[key] <= 2, 10 * time.Second
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{u.Email, "Nobody@Example.com"} {
		for i := range 3 {
			_, err := svc.Login(e.t.Context(), email, "wrong-password-123", testMeta, [16]byte{})
			var rl *RateLimitedError
			switch {
			case i < 2 && !errors.Is(err, ErrInvalidCredentials):
				t.Fatalf("%s attempt %d: %v", email, i, err)
			case i == 2 && (!errors.As(err, &rl) || rl.RetryAfter != 10*time.Second):
				t.Fatalf("%s over the limit: %v", email, err)
			}
		}
	}
	if taken[AccountKey(u.ID)] != 3 || taken["email:nobody@example.com"] != 3 {
		t.Fatalf("limiter keys: %v", taken)
	}
	// The step-up shares the account's bucket.
	if err := svc.VerifyPasswordStepUp(e.t.Context(), u.ID, goodPassword, testMeta); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("step-up over the account limit: %v", err)
	}
	if n := e.dbUser(u.ID).FailedLoginCount; n != 2 {
		t.Fatalf("limited attempts were counted: %d", n)
	}
}
