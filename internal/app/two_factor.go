package app

import (
	"errors"

	"github.com/jlentink/bekup/internal/auth"
)

// newTwoFactorPolicy builds the 2FA policy service (task 1.9, D-112): the
// policy in settings, the per-request enrollment check and the last-factor
// guard of the TOTP and passkey services. A weakening is alerted from its
// audit entry (mfa_policy.updated, weakened) by the audit observer
// (task 5.5); the notifier hook stays a no-op.
func (a *App) newTwoFactorPolicy() (*auth.TwoFactorPolicies, error) {
	return auth.NewTwoFactorPolicies(auth.TwoFactorPoliciesOptions{
		DB:           a.pool,
		Auditor:      a.auditor,
		Factors:      a.secondFactors(),
		StepUpWindow: a.opts.Config.Security.StepUpMaxAge.D(),
		Logger:       a.opts.Logger,
	})
}

// newSecondFactorFlow builds the two-step login and step-up (task 1.9)
// on the auth, TOTP and passkey services.
func (a *App) newSecondFactorFlow() (*auth.SecondFactorFlow, error) {
	if a.auth == nil || a.totp == nil || a.passkeys == nil {
		return nil, errors.New("app: the auth, TOTP and passkey services must be built first")
	}
	return auth.NewSecondFactorFlow(auth.SecondFactorFlowOptions{
		DB:       a.pool,
		Sessions: a.auth.Sessions(),
		Auditor:  a.auditor,
		Service:  a.auth,
		TOTP:     a.totp,
		Passkeys: a.passkeys,
		Factors:  a.secondFactors(),
		Logger:   a.opts.Logger,
	})
}
