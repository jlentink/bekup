package app

import (
	"log/slog"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
)

// secondFactors returns the aggregator of enrolled second factors that
// internal/authz (REQUIRES_2FA_ENROLLMENT) and the 2FA policy consult.
// Each factor service registers itself with it.
func (a *App) secondFactors() *auth.SecondFactors {
	if a.factors == nil {
		a.factors = auth.NewSecondFactors()
	}
	return a.factors
}

// newPasskeys builds the passkey service (task 1.8, D-109). The relying
// party comes from server.base_url; over plain http on a host other than
// localhost passkeys are unavailable (D-095) and a startup line says so.
func (a *App) newPasskeys() (*passkey.Service, error) {
	cfg := a.opts.Config
	svc, err := passkey.New(passkey.Options{
		DB:           a.pool,
		Auditor:      a.auditor,
		BaseURL:      cfg.Server.BaseURL,
		Dev:          a.opts.Dev,
		StepUpWindow: cfg.Security.StepUpMaxAge.D(),
		Factors:      a.secondFactors(),
		RemovalGuard: a.twoFactorPolicy,
		Logger:       a.opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	a.secondFactors().Register(svc)
	if st := svc.Status(); !st.Available {
		a.opts.Logger.Warn("passkeys unavailable", slog.String("reason", st.Reason))
	}
	return svc, nil
}
