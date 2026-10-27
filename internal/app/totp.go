package app

import (
	"github.com/jlentink/bekup/internal/auth"
)

// newTOTP builds the TOTP / recovery-code service (task 1.7, D-111) and
// registers it as the TOTP checker in factors. Second-factor failures use
// the password lockout settings with their own counter. The 2FA policy
// (task 1.9) guards removing the last factor; the alert hooks are no-ops
// until task 5.5.
func (a *App) newTOTP(factors *auth.SecondFactors) (*auth.TOTP, error) {
	sec := a.opts.Config.Security
	t, err := auth.NewTOTP(auth.TOTPOptions{
		DB:      a.pool,
		Keyring: a.keyring,
		Auditor: a.auditor,
		Factors: factors,
		// The last factor cannot be removed while 2FA is required (task 1.9).
		RemovalGuard: a.twoFactorPolicy,
		Lockout: auth.LockoutPolicy{
			Threshold: sec.LockoutThreshold,
			Base:      sec.LockoutBaseDuration.D(),
			Max:       sec.LockoutMaxDuration.D(),
		},
		StepUpWindow: sec.StepUpMaxAge.D(),
		Logger:       a.opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	factors.Register(t)
	return t, nil
}
