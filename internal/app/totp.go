package app

import (
	"github.com/jlentink/bekup/internal/auth"
)

// newTOTP builds the TOTP / recovery-code service (task 1.7, D-111) and
// registers it as the TOTP checker in factors. Second-factor failures use
// the password lockout settings with their own counter. The 2FA policy
// (task 1.9) guards removing the last factor; the alert hooks stay no-ops
// (the audit observer alerts from the audit entries, task 5.5).
func (a *App) newTOTP(factors *auth.SecondFactors) (*auth.TOTP, error) {
	sec := a.opts.Config.Security
	t, err := auth.NewTOTP(auth.TOTPOptions{
		DB:      a.pool,
		Keyring: a.keyring,
		Auditor: a.auditor,
		Factors: factors,
		// The last factor cannot be removed while 2FA is required (task 1.9).
		RemovalGuard: a.twoFactorPolicy,
		// The lockout policy is the auth service's shared, reloadable one
		// (D-200).
		Limits:       a.auth.Limits(),
		StepUpWindow: sec.StepUpMaxAge.D(),
		Logger:       a.opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	factors.Register(t)
	return t, nil
}
