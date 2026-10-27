package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// TOTP parameters (D-111): the authenticator-app standard of RFC 6238 as
// every common app implements it.
const (
	// TOTPSecretBytes is the size of a TOTP secret (160 bits, RFC 4226's
	// recommendation for HMAC-SHA1).
	TOTPSecretBytes = 20
	// TOTPPeriod is the time step.
	TOTPPeriod = 30 * time.Second
	// TOTPDigits is the code length.
	TOTPDigits = 6
	// TOTPSkew is the accepted clock skew in steps (±1: 90 s window).
	TOTPSkew = 1
	// TOTPEnrollmentTTL is how long a begun enrollment can be confirmed.
	TOTPEnrollmentTTL = 10 * time.Minute
	// DefaultTOTPIssuer names bekup in authenticator apps.
	DefaultTOTPIssuer = "bekup"
)

// maxStepUpWindow mirrors authz.MaxStepUpWindow (5 min, D-047); authz
// imports auth, so the value is repeated here.
const maxStepUpWindow = 5 * time.Minute

// AAD components of the TOTP secrets (D-085, D-086).
const (
	aadTableUsers      = "users"
	aadFieldTOTPSecret = "totp_secret"
	// aadFieldTOTPPending: a pending secret has its own AAD, so a
	// ciphertext from totp_pending_enc can never be used as an active
	// secret without the explicit re-seal in ConfirmTOTPEnrollment.
	aadFieldTOTPPending = "totp_pending"
)

// Errors of the second-factor services. Resolvers map them to API errors.
var (
	// ErrInvalidCode is the single answer to a TOTP or recovery code that
	// is wrong, malformed, already used (replay) or checked against an
	// account without that factor.
	ErrInvalidCode = errors.New("auth: invalid code")
	// ErrStepUpRequired means the caller's session has no fresh step-up.
	ErrStepUpRequired = errors.New("auth: a fresh step-up is required")
	// ErrTOTPAlreadyEnabled means TOTP must be disabled before a new
	// enrollment.
	ErrTOTPAlreadyEnabled = errors.New("auth: TOTP is already enabled")
	// ErrTOTPNotEnabled means there is no TOTP to disable.
	ErrTOTPNotEnabled = errors.New("auth: TOTP is not enabled")
	// ErrNoEnrollmentPending means there is no unexpired TOTP enrollment
	// to confirm.
	ErrNoEnrollmentPending = errors.New("auth: no TOTP enrollment pending")
	// ErrNoSecondFactor means recovery codes need an enrolled second
	// factor.
	ErrNoSecondFactor = errors.New("auth: no second factor enrolled")
)

// SecondFactorLockedError means too many consecutive second-factor
// failures; checks are refused until RetryAfter has passed.
type SecondFactorLockedError struct {
	RetryAfter time.Duration
}

func (e *SecondFactorLockedError) Error() string {
	return "auth: too many failed second-factor attempts"
}

// SecondFactorEvent describes a security-relevant second-factor event for
// the alert hooks (task 5.5).
type SecondFactorEvent struct {
	UserID uuid.UUID
	Factor Factor
	// Remaining is the number of unused recovery codes (RecoveryCodeUsed)
	// or of remaining factor kinds (SecondFactorRemoved).
	Remaining int
	// Until and Failures describe a lock (SecondFactorLocked).
	Until    time.Time
	Failures int
}

// SecondFactorNotifier is told about committed second-factor events (task
// 5.5 raises security alerts from them). Implementations must not block
// for long; errors are theirs to log.
type SecondFactorNotifier interface {
	RecoveryCodeUsed(ctx context.Context, ev SecondFactorEvent)
	SecondFactorRemoved(ctx context.Context, ev SecondFactorEvent)
	SecondFactorLocked(ctx context.Context, ev SecondFactorEvent)
}

// NopSecondFactorNotifier ignores every event (until task 5.5).
type NopSecondFactorNotifier struct{}

// RecoveryCodeUsed does nothing.
func (NopSecondFactorNotifier) RecoveryCodeUsed(context.Context, SecondFactorEvent) {}

// SecondFactorRemoved does nothing.
func (NopSecondFactorNotifier) SecondFactorRemoved(context.Context, SecondFactorEvent) {}

// SecondFactorLocked does nothing.
func (NopSecondFactorNotifier) SecondFactorLocked(context.Context, SecondFactorEvent) {}

// TOTPOptions configure NewTOTP.
type TOTPOptions struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	// Factors is the registry of every factor kind (with this service
	// registered as the TOTP checker): it decides whether a user still has
	// a factor when TOTP is disabled and whether recovery codes may be
	// generated. nil: TOTP is the only factor.
	Factors *SecondFactors
	// RemovalGuard may refuse disabling TOTP (task 1.9: last factor while
	// 2FA is required). nil: AllowFactorRemoval.
	RemovalGuard FactorRemovalGuard
	// Lockout is the backoff after consecutive failed second-factor checks
	// (security.lockout_*; its own counter, users.mfa_failed_count).
	Lockout LockoutPolicy
	// StepUpWindow is security.stepup_max_age (at most 5 min).
	StepUpWindow time.Duration
	// Notifier receives the alert hooks (nil: none).
	Notifier SecondFactorNotifier
	// Issuer names bekup in authenticator apps (default "bekup").
	Issuer string
	Logger *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// TOTP implements TOTP enrollment and verification and recovery codes
// (task 1.7, D-111). It is safe for concurrent use; every state change is
// a conditional UPDATE, so concurrent checks of one code cannot both pass.
type TOTP struct {
	o        TOTPOptions
	refusals *lockedRefusals
}

// NewTOTP returns the service.
func NewTOTP(o TOTPOptions) (*TOTP, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil {
		return nil, errors.New("auth: DB, Keyring and Auditor are required")
	}
	if o.RemovalGuard == nil {
		o.RemovalGuard = AllowFactorRemoval{}
	}
	o.Lockout = o.Lockout.normalized()
	if o.StepUpWindow <= 0 || o.StepUpWindow > maxStepUpWindow {
		o.StepUpWindow = maxStepUpWindow
	}
	if o.Notifier == nil {
		o.Notifier = NopSecondFactorNotifier{}
	}
	if o.Issuer == "" {
		o.Issuer = DefaultTOTPIssuer
	}
	if strings.ContainsAny(o.Issuer, ":/?#&=%") {
		return nil, errors.New("auth: the TOTP issuer must not contain URI delimiters")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &TOTP{o: o, refusals: newLockedRefusals()}, nil
}

// Kind reports FactorTOTP (FactorChecker).
func (t *TOTP) Kind() Factor { return FactorTOTP }

// HasFactor reports whether userID has a confirmed TOTP secret
// (FactorChecker).
func (t *TOTP) HasFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := sqlc.New(t.o.DB).HasTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: check TOTP: %w", err)
	}
	return ok, nil
}

// TOTPEnrollment is a begun enrollment. URI, QRCodeSVG and ManualKey all
// carry the secret: they are returned once, to the enrolling user, and
// never again (not even after confirmation).
type TOTPEnrollment struct {
	// URI is the otpauth:// key URI.
	URI string
	// QRCodeSVG is URI as an inline SVG QR code (no data: URI, D-082).
	QRCodeSVG string
	// ManualKey is the secret in unpadded base32 for manual entry.
	ManualKey string
	// ExpiresAt is the confirmation deadline.
	ExpiresAt time.Time
}

// requireStepUp is the service-side step-up check (the GraphQL @stepUp
// directive and the authz policy check it first; defense in depth).
func (t *TOTP) requireStepUp(p Principal) error {
	if !p.Valid() || !fresh(p.StepUpAt, t.o.StepUpWindow, t.o.Now()) {
		return ErrStepUpRequired
	}
	return nil
}

func (t *TOTP) state(ctx context.Context, userID uuid.UUID) (sqlc.GetUserTOTPStateRow, error) {
	st, err := sqlc.New(t.o.DB).GetUserTOTPState(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, errNoTOTPUser
	}
	if err != nil {
		return st, fmt.Errorf("auth: load TOTP state: %w", err)
	}
	return st, nil
}

func totpAAD(userID uuid.UUID, field string) crypto.AAD {
	return crypto.AAD{Table: aadTableUsers, RowID: userID.String(), Field: field}
}

// BeginTOTPEnrollment starts (or restarts) the caller's TOTP enrollment:
// a new 160-bit secret from crypto/rand is stored sealed as pending for
// TOTPEnrollmentTTL and returned as URI, QR code and manual key. Needs a
// fresh step-up; refused with ErrTOTPAlreadyEnabled while TOTP is on.
func (t *TOTP) BeginTOTPEnrollment(ctx context.Context, p Principal) (TOTPEnrollment, error) {
	if err := t.requireStepUp(p); err != nil {
		return TOTPEnrollment{}, err
	}
	st, err := t.state(ctx, p.UserID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if st.TotpSecretEnc != nil {
		return TOTPEnrollment{}, ErrTOTPAlreadyEnabled
	}
	secret := make([]byte, TOTPSecretBytes)
	defer crypto.Zero(secret)
	_, _ = rand.Read(secret) // crypto/rand.Read never returns an error
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: t.o.Issuer, AccountName: st.Email, Period: uint(TOTPPeriod / time.Second),
		Secret: secret, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("auth: build TOTP key: %w", err)
	}
	svg, err := QRCodeSVG(key.URL())
	if err != nil {
		return TOTPEnrollment{}, err
	}
	sealed, err := t.o.Keyring.Seal(secret, totpAAD(p.UserID, aadFieldTOTPPending))
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("auth: seal TOTP secret: %w", err)
	}
	expires := t.o.Now().Add(TOTPEnrollmentTTL)
	meta := RequestMetaFrom(ctx)
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		n, err := q.SetTOTPPending(ctx, sqlc.SetTOTPPendingParams{ID: p.UserID, PendingEnc: sealed, ExpiresAt: &expires})
		if err != nil {
			return fmt.Errorf("auth: store pending TOTP secret: %w", err)
		}
		if n == 0 {
			return ErrTOTPAlreadyEnabled
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFAEnrollmentStarted, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{"factor": string(FactorTOTP), "expires_at": expires.UTC().Format(time.RFC3339)},
		})
		return err
	})
	if err != nil {
		return TOTPEnrollment{}, err
	}
	return TOTPEnrollment{URI: key.URL(), QRCodeSVG: svg, ManualKey: key.Secret(), ExpiresAt: expires}, nil
}

// ConfirmTOTPEnrollment completes the caller's pending enrollment with a
// valid code: the secret is re-sealed as the active one, the code's step
// becomes the last used step, and a new set of recovery codes replaces any
// old one. The codes are returned once. Needs a fresh step-up. A wrong
// code answers ErrInvalidCode (audited; not counted towards the
// second-factor lockout: the caller is already signed in and chose the
// secret).
func (t *TOTP) ConfirmTOTPEnrollment(ctx context.Context, p Principal, code string) ([]string, error) {
	if err := t.requireStepUp(p); err != nil {
		return nil, err
	}
	st, err := t.state(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if st.TotpSecretEnc != nil {
		return nil, ErrTOTPAlreadyEnabled
	}
	if st.TotpPendingEnc == nil || st.TotpPendingExpiresAt == nil || !st.TotpPendingExpiresAt.After(t.o.Now()) {
		return nil, ErrNoEnrollmentPending
	}
	meta := RequestMetaFrom(ctx)
	secret, err := t.o.Keyring.Open(st.TotpPendingEnc, totpAAD(p.UserID, aadFieldTOTPPending))
	if err != nil {
		return nil, fmt.Errorf("auth: open pending TOTP secret: %w", err)
	}
	defer crypto.Zero(secret)
	step, ok := matchTOTP(secret, code, t.o.Now(), -1)
	if !ok {
		if _, err := t.o.Auditor.Record(ctx, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFAFailed, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{"factor": string(FactorTOTP), "reason": "bad_code", "stage": "enrollment"},
		}); err != nil {
			return nil, err
		}
		return nil, ErrInvalidCode
	}
	sealed, err := t.o.Keyring.Seal(secret, totpAAD(p.UserID, aadFieldTOTPSecret))
	if err != nil {
		return nil, fmt.Errorf("auth: seal TOTP secret: %w", err)
	}
	codes, err := t.newRecoveryCodes(p.UserID)
	if err != nil {
		return nil, err
	}
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		n, err := q.ConfirmTOTP(ctx, sqlc.ConfirmTOTPParams{
			ID: p.UserID, SecretEnc: sealed, Step: &step, PendingEnc: st.TotpPendingEnc,
		})
		if err != nil {
			return fmt.Errorf("auth: confirm TOTP: %w", err)
		}
		if n == 0 {
			return ErrNoEnrollmentPending
		}
		if err := q.ResetMFAFailures(ctx, p.UserID); err != nil {
			return fmt.Errorf("auth: reset second-factor failures: %w", err)
		}
		replaced, err := codes.storeTx(ctx, q)
		if err != nil {
			return err
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFAEnrolled, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{
				"factor": string(FactorTOTP), "recovery_codes_issued": len(codes.plain),
				"recovery_codes_replaced": replaced,
			},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return codes.plain, nil
}

// DisableTOTP removes the caller's TOTP secret (and a pending one). Needs
// a fresh step-up; the RemovalGuard may refuse (ErrLastFactor). When no
// other factor remains, the recovery codes are deleted too (they only
// stand in for a factor). The SecondFactorRemoved hook fires after commit.
func (t *TOTP) DisableTOTP(ctx context.Context, p Principal) error {
	if err := t.requireStepUp(p); err != nil {
		return err
	}
	st, err := t.state(ctx, p.UserID)
	if err != nil {
		return err
	}
	if st.TotpSecretEnc == nil {
		return ErrTOTPNotEnabled
	}
	remaining, err := t.otherFactors(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := t.o.RemovalGuard.CheckFactorRemoval(ctx, p.UserID, FactorTOTP, remaining); err != nil {
		return err
	}
	meta := RequestMetaFrom(ctx)
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		n, err := q.DisableTOTP(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("auth: disable TOTP: %w", err)
		}
		if n == 0 {
			return ErrTOTPNotEnabled
		}
		var deleted int64
		if len(remaining) == 0 {
			if deleted, err = q.DeleteRecoveryCodes(ctx, p.UserID); err != nil {
				return fmt.Errorf("auth: delete recovery codes: %w", err)
			}
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthMFARemoved, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{
				"factor": string(FactorTOTP), "remaining_factors": factorNames(remaining),
				"recovery_codes_deleted": deleted,
			},
		})
		return err
	})
	if err != nil {
		return err
	}
	t.o.Logger.InfoContext(ctx, "second factor removed", slog.String("user_id", p.UserID.String()), slog.String("factor", string(FactorTOTP)))
	t.o.Notifier.SecondFactorRemoved(ctx, SecondFactorEvent{UserID: p.UserID, Factor: FactorTOTP, Remaining: len(remaining)})
	return nil
}

// otherFactors returns the factor kinds userID has besides TOTP.
func (t *TOTP) otherFactors(ctx context.Context, userID uuid.UUID) ([]Factor, error) {
	if t.o.Factors == nil {
		return nil, nil
	}
	all, err := t.o.Factors.Enrolled(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Factor, 0, len(all))
	for _, f := range all {
		if f != FactorTOTP {
			out = append(out, f)
		}
	}
	return out, nil
}

// hasAnyFactor reports whether userID has any second factor.
func (t *TOTP) hasAnyFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	if t.o.Factors == nil {
		return t.HasFactor(ctx, userID)
	}
	return t.o.Factors.HasSecondFactor(ctx, userID)
}

func factorNames(fs []Factor) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f))
	}
	return out
}

// VerifyTOTP checks a TOTP code of userID (the second step of a login or a
// step-up, task 1.9). The code must match a step within ±TOTPSkew of now
// that is after users.totp_last_step; the step is then recorded with a
// conditional UPDATE, so a code (or an older one) is accepted at most once
// even under concurrency. Every check is reserved and counted towards the
// second-factor lockout before the code is looked at (D-117: a
// conditional UPDATE, so concurrent guesses cannot exceed the threshold);
// every failure is ErrInvalidCode and audited; while locked, checks are
// refused with *SecondFactorLockedError without looking at the code. A
// success is audited and resets the counter. The caller elevates the session.
func (t *TOTP) VerifyTOTP(ctx context.Context, userID uuid.UUID, code string) error {
	st, err := t.state(ctx, userID)
	if errors.Is(err, errNoTOTPUser) {
		return ErrInvalidCode
	}
	if err != nil {
		return err
	}
	att, err := t.reserveMFA(ctx, userID, FactorTOTP)
	if err != nil {
		return err
	}
	if st.Disabled || st.TotpSecretEnc == nil {
		return t.failMFA(ctx, att, FactorTOTP, "not_enrolled")
	}
	secret, err := t.o.Keyring.Open(st.TotpSecretEnc, totpAAD(userID, aadFieldTOTPSecret))
	if err != nil {
		return fmt.Errorf("auth: open TOTP secret: %w", err)
	}
	last := int64(-1)
	if st.TotpLastStep != nil {
		last = *st.TotpLastStep
	}
	step, ok := matchTOTP(secret, code, t.o.Now(), last)
	crypto.Zero(secret)
	if !ok {
		return t.failMFA(ctx, att, FactorTOTP, "bad_code")
	}
	meta := RequestMetaFrom(ctx)
	err = db.Tx(ctx, t.o.DB, func(q *sqlc.Queries) error {
		n, err := q.AcceptTOTPStep(ctx, sqlc.AcceptTOTPStepParams{ID: userID, Step: &step, SecretEnc: st.TotpSecretEnc})
		if err != nil {
			return fmt.Errorf("auth: record TOTP step: %w", err)
		}
		if n == 0 {
			return errReplay
		}
		_, err = t.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(userID), Action: audit.ActionAuthMFAVerified, IP: meta.IP,
			TargetType: "user", TargetID: userID.String(),
			Details: audit.Details{"factor": string(FactorTOTP)},
		})
		return err
	})
	if errors.Is(err, errReplay) {
		return t.failMFA(ctx, att, FactorTOTP, "replayed")
	}
	return err
}

// errNoTOTPUser: the user of a second-factor operation does not exist.
var errNoTOTPUser = errors.New("auth: user not found")

// errReplay: the step was accepted concurrently (or the secret changed).
var errReplay = errors.New("auth: TOTP step already used")

// matchTOTP returns the newest time step within ±TOTPSkew of now whose
// code equals code and that is after last (-1: none). Every candidate is
// computed and compared in constant time.
func matchTOTP(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	code = normalizeTOTPCode(code)
	valid := len(code) == TOTPDigits
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	cur := now.Unix() / int64(TOTPPeriod/time.Second)
	var match int64 = -1
	for s := cur - TOTPSkew; s <= cur+TOTPSkew; s++ {
		if s < 0 {
			continue
		}
		want, err := hotp.GenerateCodeCustom(b32, uint64(s), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return 0, false
		}
		eq := subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1
		if eq && s > last {
			match = s
		}
	}
	if !valid || match < 0 {
		return 0, false
	}
	return match, true
}

// normalizeTOTPCode strips spaces and dashes; anything but exactly
// TOTPDigits ASCII digits yields "".
func normalizeTOTPCode(code string) string {
	if len(code) > 32 {
		return ""
	}
	var b strings.Builder
	for _, r := range code {
		switch {
		case r == ' ' || r == '-':
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != TOTPDigits {
		return ""
	}
	return b.String()
}

// TwoFactorStatus is a user's second-factor state as shown to themselves.
type TwoFactorStatus struct {
	TOTPEnabled   bool
	TOTPEnabledAt *time.Time
	// TOTPEnrollmentPending: a begun enrollment can still be confirmed.
	TOTPEnrollmentPending  bool
	RecoveryCodesRemaining int
}

// Status returns userID's second-factor state.
func (t *TOTP) Status(ctx context.Context, userID uuid.UUID) (TwoFactorStatus, error) {
	st, err := t.state(ctx, userID)
	if err != nil {
		return TwoFactorStatus{}, err
	}
	n, err := sqlc.New(t.o.DB).CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return TwoFactorStatus{}, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	return TwoFactorStatus{
		TOTPEnabled:   st.TotpSecretEnc != nil,
		TOTPEnabledAt: st.TotpEnabledAt,
		TOTPEnrollmentPending: st.TotpSecretEnc == nil && st.TotpPendingEnc != nil &&
			st.TotpPendingExpiresAt != nil && st.TotpPendingExpiresAt.After(t.o.Now()),
		RecoveryCodesRemaining: int(n),
	}, nil
}
