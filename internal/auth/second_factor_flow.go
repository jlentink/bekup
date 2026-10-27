package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// StepUpMethod is how a step-up is performed (task 1.9, D-112).
type StepUpMethod string

// Step-up methods: the password only for accounts without a second
// factor; otherwise one of the factor kinds.
const (
	StepUpPassword     StepUpMethod = "password"
	StepUpTOTP         StepUpMethod = StepUpMethod(FactorTOTP)
	StepUpPasskey      StepUpMethod = StepUpMethod(FactorPasskey)
	StepUpRecoveryCode StepUpMethod = StepUpMethod(FactorRecoveryCode)
)

// Errors of the second-factor flow. Resolvers map them to API errors.
var (
	// ErrAssertionRejected marks a passkey assertion that did not verify
	// (no ceremony, malformed, wrong signature, counter regression, ...):
	// it counts towards the second-factor lockout. PasskeyAsserter
	// implementations wrap such errors with it.
	ErrAssertionRejected = errors.New("auth: passkey assertion rejected")
	// ErrPasskeysUnavailable means passkeys cannot be used with this server
	// (D-095) or the user has none.
	ErrPasskeysUnavailable = errors.New("auth: passkeys are unavailable")
	// ErrSecondFactorPending means the session still waits for its second
	// factor (a step-up needs a completed login).
	ErrSecondFactorPending = errors.New("auth: the login still waits for its second factor")
	// ErrFactorRequired means the account has a second factor, so a
	// step-up with the password alone is refused.
	ErrFactorRequired = errors.New("auth: a second factor is required for this account")
	// ErrMethodUnavailable means the chosen method is not enrolled.
	ErrMethodUnavailable = errors.New("auth: this method is not available for the account")
)

// PasskeyAsserter is the assertion half of the passkey service
// (internal/auth/passkey implements it).
type PasskeyAsserter interface {
	// Available reports whether passkeys can be used with this server.
	Available() bool
	// BeginAssertion starts an assertion for userID bound to sessionID and
	// returns the request options JSON.
	BeginAssertion(ctx context.Context, sessionID, userID uuid.UUID) (json.RawMessage, error)
	// VerifyAssertion checks the browser's response against the pending
	// assertion of sessionID; failures of the response itself wrap
	// ErrAssertionRejected.
	VerifyAssertion(ctx context.Context, sessionID, userID uuid.UUID, response []byte) (AssertionResult, error)
}

// AssertionResult is a verified passkey assertion.
type AssertionResult struct {
	// UserVerified is the UV flag of the assertion: the authenticator
	// verified the user (PIN, biometrics). Only such an assertion sets the
	// session's second-factor time (aal2_at) that restores require; a
	// user-presence-only assertion still completes a login or step-up
	// (D-116).
	UserVerified bool
}

// SecondFactorFlowOptions configure NewSecondFactorFlow.
type SecondFactorFlowOptions struct {
	DB       DB
	Sessions *Sessions
	Auditor  *audit.Auditor
	// Service checks passwords for password step-ups (required).
	Service *Service
	// TOTP verifies TOTP and recovery codes and owns the second-factor
	// lockout (required).
	TOTP *TOTP
	// Passkeys verifies assertions (nil: passkeys unavailable).
	Passkeys PasskeyAsserter
	// Factors lists enrolled factors (required).
	Factors FactorLister
	Logger  *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// SecondFactorFlow implements the second step of a login and the step-up
// (task 1.9, D-112). It is safe for concurrent use.
type SecondFactorFlow struct {
	o SecondFactorFlowOptions
}

// NewSecondFactorFlow returns the flow.
func NewSecondFactorFlow(o SecondFactorFlowOptions) (*SecondFactorFlow, error) {
	if o.DB == nil || o.Sessions == nil || o.Auditor == nil || o.Service == nil || o.TOTP == nil || o.Factors == nil {
		return nil, errors.New("auth: DB, Sessions, Auditor, Service, TOTP and Factors are required")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &SecondFactorFlow{o: o}, nil
}

func (f *SecondFactorFlow) passkeysAvailable() bool {
	return f.o.Passkeys != nil && f.o.Passkeys.Available()
}

// BeginPasskeyLogin starts the passkey assertion of a login waiting for
// its second factor and returns the request options JSON.
func (f *SecondFactorFlow) BeginPasskeyLogin(ctx context.Context, p Principal) (json.RawMessage, error) {
	if !p.Valid() || !p.MFAPending {
		return nil, ErrNotPending
	}
	return f.beginPasskey(ctx, p)
}

func (f *SecondFactorFlow) beginPasskey(ctx context.Context, p Principal) (json.RawMessage, error) {
	if !f.passkeysAvailable() {
		return nil, ErrPasskeysUnavailable
	}
	enrolled, err := f.o.Factors.Enrolled(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(enrolled, FactorPasskey) {
		return nil, ErrPasskeysUnavailable
	}
	return f.o.Passkeys.BeginAssertion(ctx, p.SessionID, p.UserID)
}

// verify checks one second factor of p's user and reports whether it sets
// the session's second-factor time (aal2_at): TOTP and a user-verified
// passkey assertion do, a recovery code and a user-presence-only passkey
// assertion do not (D-112, D-116). Every rejection counts towards the
// second-factor lockout (ErrInvalidCode); while locked,
// *SecondFactorLockedError.
func (f *SecondFactorFlow) verify(ctx context.Context, p Principal, method Factor, code string, assertion []byte) (bool, error) {
	switch method {
	case FactorTOTP:
		return true, f.o.TOTP.VerifyTOTP(ctx, p.UserID, code)
	case FactorRecoveryCode:
		return false, f.o.TOTP.UseRecoveryCode(ctx, p.UserID, code)
	case FactorPasskey:
		if !f.passkeysAvailable() {
			return false, ErrPasskeysUnavailable
		}
		att, err := f.o.TOTP.ReserveSecondFactorAttempt(ctx, p.UserID, FactorPasskey)
		if err != nil {
			return false, err
		}
		res, err := f.o.Passkeys.VerifyAssertion(ctx, p.SessionID, p.UserID, assertion)
		if errors.Is(err, ErrAssertionRejected) {
			return false, f.o.TOTP.RecordSecondFactorFailure(ctx, att, FactorPasskey, "assertion_rejected")
		}
		return res.UserVerified, err
	default:
		return false, ErrMethodUnavailable
	}
}

// CompleteSecondFactor finishes the login of the mfa_pending session of p
// with a TOTP code, a recovery code or a passkey assertion. On success the
// session is rotated into an AAL2 session with a fresh step-up (and a
// fresh second-factor time for TOTP and a user-verified passkey, not for a
// recovery code or a user-presence-only passkey; D-116), the method is
// audited and the new session is returned (its token goes into the
// cookie). Failures count towards the second-factor lockout.
func (f *SecondFactorFlow) CompleteSecondFactor(ctx context.Context, p Principal, method Factor, code string, assertion []byte) (Issued, error) {
	if !p.Valid() || !p.MFAPending {
		return Issued{}, ErrNotPending
	}
	aal2Time, err := f.verify(ctx, p, method, code, assertion)
	if err != nil {
		return Issued{}, err
	}
	meta := RequestMetaFrom(ctx)
	var out Issued
	err = db.Tx(ctx, f.o.DB, func(q *sqlc.Queries) error {
		var err error
		out, err = f.o.Sessions.CompleteSecondFactorTx(ctx, q, p.SessionID, aal2Time, meta)
		if err != nil {
			return err
		}
		if method == FactorPasskey {
			if err := ResetSecondFactorFailuresTx(ctx, q, p.UserID); err != nil {
				return err
			}
		}
		_, err = f.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthLoginSecondFactor, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(),
			Details: audit.Details{
				"method": string(method), "aal": AAL2, "session_id": out.Session.ID.String(),
				"previous_session_id": p.SessionID.String(), "second_factor_time": aal2Time,
			},
		})
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return out, nil
}

// StepUpChallenge lists how the caller can step up.
type StepUpChallenge struct {
	// Methods are the usable methods: StepUpPassword alone for an account
	// without a second factor; otherwise the enrolled factor kinds (and
	// recovery codes while unused ones are left).
	Methods []StepUpMethod
	// PasskeyOptions are the assertion request options when a passkey can
	// be used (the challenge is bound to the session).
	PasskeyOptions json.RawMessage
}

// BeginStepUp returns the step-up methods of the caller and, when a
// passkey can be used, starts its assertion.
func (f *SecondFactorFlow) BeginStepUp(ctx context.Context, p Principal) (StepUpChallenge, error) {
	if !p.Valid() {
		return StepUpChallenge{}, ErrNotPending
	}
	if p.MFAPending {
		return StepUpChallenge{}, ErrSecondFactorPending
	}
	enrolled, err := f.o.Factors.Enrolled(ctx, p.UserID)
	if err != nil {
		return StepUpChallenge{}, err
	}
	if len(enrolled) == 0 {
		return StepUpChallenge{Methods: []StepUpMethod{StepUpPassword}}, nil
	}
	var ch StepUpChallenge
	for _, k := range enrolled {
		switch k {
		case FactorTOTP:
			ch.Methods = append(ch.Methods, StepUpTOTP)
		case FactorPasskey:
			if !f.passkeysAvailable() {
				continue
			}
			opts, err := f.o.Passkeys.BeginAssertion(ctx, p.SessionID, p.UserID)
			if err != nil {
				return StepUpChallenge{}, err
			}
			ch.Methods = append(ch.Methods, StepUpPasskey)
			ch.PasskeyOptions = opts
		case FactorRecoveryCode:
			// Not a factor of its own; offered below while codes are left.
		}
	}
	n, err := sqlc.New(f.o.DB).CountUnusedRecoveryCodes(ctx, p.UserID)
	if err != nil {
		return StepUpChallenge{}, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	if n > 0 {
		ch.Methods = append(ch.Methods, StepUpRecoveryCode)
	}
	return ch, nil
}

// StepUpResult is a successful step-up.
type StepUpResult struct {
	// Issued is set when the session was rotated (a passkey/TOTP step-up
	// raised it from AAL1 to AAL2): its token goes into the cookie.
	Issued *Issued
	// StepUpAt is the new step-up time; AAL2At the new second-factor time
	// (nil for a password or recovery-code step-up).
	StepUpAt time.Time
	AAL2At   *time.Time
	// AAL is the session's level afterwards.
	AAL int
}

// CompleteStepUp performs a step-up of p's session (D-112):
//
//   - an account without a second factor steps up with its password
//     (StepUpPassword; a wrong one counts towards the login lockout);
//   - an account with one must use a factor: TOTP or a user-verified
//     passkey set the step-up and second-factor times (an AAL1 session is
//     rotated to AAL2); a recovery code or a user-presence-only passkey
//     (D-116) sets only the step-up time (it does not satisfy the restore
//     check); a recovery code also raises the recovery-code alert;
//     the password alone is refused (ErrFactorRequired).
//
// Successes are audited (auth.stepup.succeeded with the method, never the
// code); rejected codes are audited by the factor checks.
func (f *SecondFactorFlow) CompleteStepUp(ctx context.Context, p Principal, method StepUpMethod, value string, assertion []byte) (StepUpResult, error) {
	if !p.Valid() {
		return StepUpResult{}, ErrNotPending
	}
	if p.MFAPending {
		return StepUpResult{}, ErrSecondFactorPending
	}
	enrolled, err := f.o.Factors.Enrolled(ctx, p.UserID)
	if err != nil {
		return StepUpResult{}, err
	}
	meta := RequestMetaFrom(ctx)
	withFactor := false
	switch method {
	case StepUpPassword:
		if len(enrolled) > 0 {
			return StepUpResult{}, ErrFactorRequired
		}
		if err := f.o.Service.VerifyPasswordStepUp(ctx, p.UserID, value, meta); err != nil {
			return StepUpResult{}, err
		}
	case StepUpTOTP, StepUpPasskey, StepUpRecoveryCode:
		if len(enrolled) == 0 {
			return StepUpResult{}, ErrNoSecondFactor
		}
		if method != StepUpRecoveryCode && !slices.Contains(enrolled, Factor(method)) {
			return StepUpResult{}, ErrMethodUnavailable
		}
		// TOTP and a user-verified passkey set the second-factor time
		// (and raise an AAL1 session); a recovery code and a
		// user-presence-only passkey set the step-up time only (D-116).
		if withFactor, err = f.verify(ctx, p, Factor(method), value, assertion); err != nil {
			return StepUpResult{}, err
		}
	default:
		return StepUpResult{}, ErrMethodUnavailable
	}
	now := f.o.Now()
	res := StepUpResult{StepUpAt: now, AAL: p.AAL}
	err = db.Tx(ctx, f.o.DB, func(q *sqlc.Queries) error {
		switch {
		case withFactor && p.AAL < AAL2:
			issued, err := f.o.Sessions.ElevateAALTx(ctx, q, p.SessionID, AAL2, meta)
			if err != nil {
				return err
			}
			res.Issued, res.AAL, res.AAL2At = &issued, AAL2, &now
		case withFactor:
			if err := f.o.Sessions.MarkSecondFactorTx(ctx, q, p.SessionID); err != nil {
				return err
			}
			res.AAL2At = &now
		default:
			if err := f.o.Sessions.MarkStepUpTx(ctx, q, p.SessionID); err != nil {
				return err
			}
		}
		if method == StepUpPasskey {
			if err := ResetSecondFactorFailuresTx(ctx, q, p.UserID); err != nil {
				return err
			}
		}
		details := audit.Details{"method": string(method), "session_id": p.SessionID.String(), "aal": res.AAL, "second_factor_time": withFactor}
		if res.Issued != nil {
			details["new_session_id"] = res.Issued.Session.ID.String()
		}
		_, err := f.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionAuthStepUpSucceeded, IP: meta.IP,
			TargetType: "user", TargetID: p.UserID.String(), Details: details,
		})
		return err
	})
	if err != nil {
		return StepUpResult{}, err
	}
	if method == StepUpRecoveryCode {
		f.o.Logger.WarnContext(ctx, "step-up with a recovery code", slog.String("user_id", p.UserID.String()))
	}
	return res, nil
}
