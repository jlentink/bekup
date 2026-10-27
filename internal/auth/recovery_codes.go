package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Recovery codes (D-111): RecoveryCodeCount codes of RecoveryCodeChars
// Crockford base32 characters (5 bits each: 60 bits), shown as
// "xxxx-xxxx-xxxx". Only HMAC-SHA256 under a keyring subkey is stored.
const (
	RecoveryCodeCount = 10
	RecoveryCodeChars = 12
	// recoveryCodeGroup is the display group size.
	recoveryCodeGroup = 4
	// recoveryMACPurpose is the crypto.Keyring.MAC purpose.
	recoveryMACPurpose = "recovery-code"
	// recoveryMACContext prefixes the MAC input (versioned).
	recoveryMACContext = "bekup-recovery-code-v1"
)

// crockford is Crockford's base32 alphabet (no i, l, o, u).
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// recoveryCodeSet is a freshly generated set: the codes to show once and
// their MACs to store.
type recoveryCodeSet struct {
	userID uuid.UUID
	plain  []string
	keyID  crypto.KeyID
	macs   [][]byte
}

// newRecoveryCodes generates RecoveryCodeCount codes for userID.
func (t *TOTP) newRecoveryCodes(userID uuid.UUID) (*recoveryCodeSet, error) {
	set := &recoveryCodeSet{userID: userID}
	seen := make(map[string]bool, RecoveryCodeCount)
	for len(set.plain) < RecoveryCodeCount {
		raw := make([]byte, RecoveryCodeChars)
		_, _ = rand.Read(raw) // crypto/rand.Read never returns an error
		var b strings.Builder
		for i, c := range raw {
			if i > 0 && i%recoveryCodeGroup == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(crockford[c&31]) // 256 is a multiple of 32: unbiased
		}
		code := b.String()
		norm, _ := normalizeRecoveryCode(code)
		if seen[norm] {
			continue
		}
		seen[norm] = true
		id, mac, err := t.o.Keyring.MAC(recoveryMACPurpose, recoveryMACInput(userID, norm))
		if err != nil {
			return nil, fmt.Errorf("auth: hash recovery code: %w", err)
		}
		set.keyID = id
		set.plain = append(set.plain, code)
		set.macs = append(set.macs, mac)
	}
	return set, nil
}

// storeTx replaces the user's recovery codes with the set and returns how
// many codes (used or not) were replaced.
func (s *recoveryCodeSet) storeTx(ctx context.Context, q *sqlc.Queries) (int64, error) {
	n, err := q.DeleteRecoveryCodes(ctx, s.userID)
	if err != nil {
		return 0, fmt.Errorf("auth: delete recovery codes: %w", err)
	}
	for _, mac := range s.macs {
		if err := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{
			UserID: s.userID, KeyID: s.keyID.String(), CodeMac: mac,
		}); err != nil {
			return 0, fmt.Errorf("auth: store recovery code: %w", err)
		}
	}
	return n, nil
}

// recoveryMACInput binds a normalized code to its user.
func recoveryMACInput(userID uuid.UUID, norm string) []byte {
	return []byte(recoveryMACContext + "\x00" + userID.String() + "\x00" + norm)
}

// normalizeRecoveryCode lowercases, drops spaces and dashes and maps the
// look-alikes i/l → 1 and o → 0; it reports false unless exactly
// RecoveryCodeChars alphabet characters remain.
func normalizeRecoveryCode(code string) (string, bool) {
	if len(code) > 64 {
		return "", false
	}
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		switch r {
		case ' ', '-':
			continue
		case 'i', 'l':
			r = '1'
		case 'o':
			r = '0'
		}
		if r > 127 || !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != RecoveryCodeChars {
		return "", false
	}
	return b.String(), true
}

// RegenerateRecoveryCodes replaces the caller's recovery codes with a new
// set, returned once. Needs a fresh step-up and an enrolled second factor
// (ErrNoSecondFactor).
func (t *TOTP) RegenerateRecoveryCodes(ctx context.Context, p Principal) ([]string, error) {
	if err := t.requireStepUp(p); err != nil {
		return nil, err
	}
	has, err := t.hasAnyFactor(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNoSecondFactor
	}
	codes, err := t.newRecoveryCodes(p.UserID)
	if err != nil {
		return nil, err
	}
	meta := RequestMetaFrom(ctx)
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		replaced, err := codes.storeTx(ctx, q)
		if err != nil {
			return err
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthRecoveryCodesRegenerated, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{"recovery_codes_issued": len(codes.plain), "recovery_codes_replaced": replaced},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return codes.plain, nil
}

// UseRecoveryCode spends one of userID's recovery codes in place of a
// second factor (task 1.9). The code is compared in constant time against
// every unused code's MAC and marked used with a conditional UPDATE, so it
// works at most once even under concurrency. Every check is reserved and
// counted towards the second-factor lockout before it runs (D-117);
// failures are ErrInvalidCode and audited; while locked,
// *SecondFactorLockedError. A success is audited (with the number of codes
// left), resets the counter and fires the RecoveryCodeUsed hook.
func (t *TOTP) UseRecoveryCode(ctx context.Context, userID uuid.UUID, code string) error {
	st, err := t.state(ctx, userID)
	if errors.Is(err, errNoTOTPUser) {
		return ErrInvalidCode
	}
	if err != nil {
		return err
	}
	att, err := t.reserveMFA(ctx, userID, FactorRecoveryCode)
	if err != nil {
		return err
	}
	norm, ok := normalizeRecoveryCode(code)
	if st.Disabled || !ok {
		return t.failMFA(ctx, att, FactorRecoveryCode, "bad_code")
	}
	rows, err := sqlc.New(t.o.DB).ListUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: list recovery codes: %w", err)
	}
	msg := recoveryMACInput(userID, norm)
	macs := make(map[string][]byte, 1)
	var match uuid.UUID
	for _, r := range rows {
		mac, seen := macs[r.KeyID]
		if !seen {
			id, err := crypto.ParseKeyID(r.KeyID)
			if err == nil {
				mac, err = t.o.Keyring.MACWith(id, recoveryMACPurpose, msg)
			}
			if err != nil {
				// A code under a key no longer in the keyring cannot match.
				t.o.Logger.WarnContext(ctx, "recovery code key unavailable", slog.String("key_id", r.KeyID))
				mac = nil
			}
			macs[r.KeyID] = mac
		}
		if mac != nil && hmac.Equal(mac, r.CodeMac) {
			match = r.ID
		}
	}
	if match == uuid.Nil {
		return t.failMFA(ctx, att, FactorRecoveryCode, "bad_code")
	}
	meta := RequestMetaFrom(ctx)
	var remaining int64
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		n, err := q.UseRecoveryCode(ctx, sqlc.UseRecoveryCodeParams{ID: match, UserID: userID})
		if err != nil {
			return fmt.Errorf("auth: use recovery code: %w", err)
		}
		if n == 0 {
			return errReplay
		}
		if err := q.ResetMFAFailures(ctx, userID); err != nil {
			return fmt.Errorf("auth: reset second-factor failures: %w", err)
		}
		if remaining, err = q.CountUnusedRecoveryCodes(ctx, userID); err != nil {
			return fmt.Errorf("auth: count recovery codes: %w", err)
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(userID), Action: audit.ActionAuthRecoveryCodeUsed, IP: meta.IP,
			TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{"recovery_codes_remaining": remaining},
		})
		return err
	})
	if errors.Is(err, errReplay) {
		return t.failMFA(ctx, att, FactorRecoveryCode, "replayed")
	}
	if err != nil {
		return err
	}
	t.o.Logger.WarnContext(ctx, "recovery code used", slog.String("user_id", userID.String()), slog.Int64("remaining", remaining))
	t.o.Notifier.RecoveryCodeUsed(ctx, SecondFactorEvent{UserID: userID, Factor: FactorRecoveryCode, Remaining: int(remaining)})
	return nil
}

// SecondFactorAttempt is a second-factor check reserved (and counted)
// before it runs (D-117); pass it to RecordSecondFactorFailure when the
// check fails. A success clears the count.
type SecondFactorAttempt struct {
	userID uuid.UUID
	att    attempt
}

// reserveMFA reserves one second-factor check of userID (D-117): a
// conditional UPDATE counts it only while the second factors are not
// locked and locks them in the same statement when the threshold is
// reached, so concurrent checks can never run more than the threshold
// allows. While locked it answers *SecondFactorLockedError without
// counting; the refusal is recorded at most once per lock window.
func (t *TOTP) reserveMFA(ctx context.Context, userID uuid.UUID, f Factor) (SecondFactorAttempt, error) {
	threshold, base, maxSecs := t.o.Lockout.reserveArgs()
	q := sqlc.New(t.o.DB)
	for range 2 {
		now := t.o.Now()
		row, err := q.ReserveMFAAttempt(ctx, sqlc.ReserveMFAAttemptParams{
			ID: userID, Threshold: threshold, AtTime: now, BaseSeconds: base, MaxSeconds: maxSecs,
		})
		if err == nil {
			return SecondFactorAttempt{userID: userID, att: attempt{count: row.MfaFailedCount, lockedUntil: row.MfaLockedUntil}}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return SecondFactorAttempt{}, fmt.Errorf("auth: reserve second-factor attempt: %w", err)
		}
		st, err := t.state(ctx, userID)
		if errors.Is(err, errNoTOTPUser) {
			return SecondFactorAttempt{}, ErrInvalidCode
		}
		if err != nil {
			return SecondFactorAttempt{}, err
		}
		if st.MfaLockedUntil != nil && st.MfaLockedUntil.After(now) {
			return SecondFactorAttempt{}, t.refuseLocked(ctx, st, f, *st.MfaLockedUntil, now)
		}
		// Unlocked in between (a success, an admin reset): try again.
	}
	// Never verify without a reservation.
	return SecondFactorAttempt{}, &SecondFactorLockedError{RetryAfter: time.Second}
}

// refuseLocked records a check refused while the second factors are
// locked (at most once per user and lock window; the further refusals are
// counted and reported with the next entry) and returns the
// *SecondFactorLockedError.
func (t *TOTP) refuseLocked(ctx context.Context, st sqlc.GetUserTOTPStateRow, f Factor, until, now time.Time) error {
	ret := &SecondFactorLockedError{RetryAfter: until.Sub(now)}
	record, previous := t.refusals.note(st.ID, until, now)
	if !record {
		return ret
	}
	details := audit.Details{
		"factor": string(f), "reason": "locked", "failed_count": st.MfaFailedCount,
		"locked_until": until.UTC().Format(time.RFC3339), "aggregated": true,
	}
	if previous > 0 {
		details["refused_while_locked"] = previous
	}
	if _, err := t.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionAuthMFAFailed, IP: RequestMetaFrom(ctx).IP,
		TargetType: "user", TargetID: st.ID.String(), Details: details,
	}); err != nil {
		return err
	}
	return ret
}

// failMFA records a failed second-factor check that was reserved (and
// counted) by reserveMFA and, when reserving it locked the second
// factors, the lock, in one transaction; it then notifies about a new
// lock and returns ErrInvalidCode.
func (t *TOTP) failMFA(ctx context.Context, a SecondFactorAttempt, f Factor, reason string) error {
	meta := RequestMetaFrom(ctx)
	userID := a.userID
	var locked *SecondFactorEvent
	refused := t.refusals.take(userID)
	err := db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		details := audit.Details{"factor": string(f), "reason": reason, "failed_count": a.att.count}
		if refused > 0 {
			details["refused_while_locked"] = refused
		}
		if _, err := t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionAuthMFAFailed, IP: meta.IP,
			TargetType: "user", TargetID: userID.String(), Details: details,
		}); err != nil {
			return err
		}
		if !a.att.locked() {
			return nil
		}
		until := *a.att.lockedUntil
		if _, err := t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionAuthMFALockout, IP: meta.IP,
			TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{
				"failed_count": a.att.count, "locked_until": until.UTC().Format(time.RFC3339),
				"lock_seconds": int64(t.o.Lockout.LockFor(int(a.att.count)) / time.Second),
			},
		}); err != nil {
			return err
		}
		locked = &SecondFactorEvent{UserID: userID, Factor: f, Until: until, Failures: int(a.att.count)}
		return nil
	})
	if err != nil {
		return err
	}
	if locked != nil {
		t.o.Logger.WarnContext(ctx, "second factors locked after failed checks",
			slog.String("user_id", userID.String()), slog.Int("failed_count", locked.Failures),
			slog.Time("locked_until", locked.Until))
		t.o.Notifier.SecondFactorLocked(ctx, *locked)
	}
	return ErrInvalidCode
}

// ReserveSecondFactorAttempt reserves (counts) one check of a second
// factor of kind f before it runs (D-117); while userID's second factors
// are locked it answers *SecondFactorLockedError (recorded at most once
// per lock window). The two-step login and step-up (task 1.9) call it
// before verifying a passkey assertion; VerifyTOTP and UseRecoveryCode
// reserve themselves. An unknown user answers ErrInvalidCode. A failed
// check must be reported with RecordSecondFactorFailure; a success resets
// the count (ResetSecondFactorFailuresTx).
func (t *TOTP) ReserveSecondFactorAttempt(ctx context.Context, userID uuid.UUID, f Factor) (SecondFactorAttempt, error) {
	return t.reserveMFA(ctx, userID, f)
}

// RecordSecondFactorFailure records a rejected second factor of kind f
// whose attempt was reserved with ReserveSecondFactorAttempt (task 1.9: a
// passkey assertion that did not verify), applies the lock that reserving
// it may have reached, audits it with reason and returns ErrInvalidCode
// (or the audit/DB error).
func (t *TOTP) RecordSecondFactorFailure(ctx context.Context, a SecondFactorAttempt, f Factor, reason string) error {
	return t.failMFA(ctx, a, f, reason)
}

// ResetSecondFactorFailuresTx clears the second-factor failure counter and
// lock of userID inside the caller's transaction (a passkey assertion
// succeeded, task 1.9).
func ResetSecondFactorFailuresTx(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) error {
	if err := q.ResetMFAFailures(ctx, userID); err != nil {
		return fmt.Errorf("auth: reset second-factor failures: %w", err)
	}
	return nil
}
