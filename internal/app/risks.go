package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/riskaccept"
)

// Accepted security risks (D-228). The service is built right after the
// auditor (serve and worker): the startup audits ask it whether this
// process's posture is accepted, the hourly security check sweeps and
// skips accepted reminders, and serve's GraphQL layer lists, accepts and
// revokes.

// newRisks builds the risk acceptance service. The policy is created
// later, so the service authorizes through lateAuthorizer.
func (a *App) newRisks() error {
	svc, err := riskaccept.New(riskaccept.Options{
		DB: a.pool, Auditor: a.auditor, Authorizer: lateAuthorizer{a},
		Posture: a.postureConditions,
		SMTP:    a.smtpFacts,
		Logger:  a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.risks = svc
	return nil
}

// lateAuthorizer forwards to the central policy once it exists (refusing
// before).
type lateAuthorizer struct{ a *App }

var errNoPolicy = errors.New("app: the authorization policy is not ready")

func (l lateAuthorizer) Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error {
	if l.a.policy == nil {
		return errNoPolicy
	}
	return l.a.policy.Authorize(ctx, op, res...)
}

func (l lateAuthorizer) JobScope(ctx context.Context) (authz.Scope, uuid.UUID, error) {
	if l.a.policy == nil {
		return authz.ScopeAll, uuid.Nil, errNoPolicy
	}
	return l.a.policy.JobScope(ctx)
}

// postureConditions are the weakened instance conditions of this
// process's configuration (the conditions of weakenedPosture); never in
// dev mode.
func (a *App) postureConditions() []riskaccept.Condition {
	if a.opts.Dev {
		return nil
	}
	cfg := a.opts.Config
	var out []riskaccept.Condition
	if cfg.InsecureTransport() {
		out = append(out, riskaccept.InsecureTransport(cfg.Server.BaseURL, cfg.Server.AllowInsecureHTTP))
	}
	if cfg.Database.TLSWeakened() {
		out = append(out, riskaccept.InsecureDBTLS(cfg.Database.TLSMode, cfg.Database.TLSCAFile != "", cfg.Database.Host))
	}
	if cfg.Database.SingleAccount {
		out = append(out, riskaccept.InsecureDBRoles())
	}
	if a.imageFollowing() {
		out = append(out, riskaccept.UnpinnedImage())
	}
	return out
}

// smtpFacts reads the stored SMTP settings for the SMTP risk (serve; a
// process without the e-mail services has none).
func (a *App) smtpFacts(ctx context.Context) (riskaccept.SMTPFacts, error) {
	if a.email.Settings == nil {
		return riskaccept.SMTPFacts{}, riskaccept.ErrSMTPUnavailable
	}
	v, err := a.email.Settings.Get(ctx)
	if err != nil {
		return riskaccept.SMTPFacts{}, err
	}
	return riskaccept.SMTPFacts{
		Active: v.Configured && v.Enabled, Mode: string(v.Mode), Host: v.Host, Port: v.Port, Username: v.Username,
	}, nil
}

// acceptedPosture adds the D-228 details to a startup posture entry when
// an active acceptance covers this process's condition of kind: the entry
// is still recorded (fail closed), but the audit observer raises no alert.
func (a *App) acceptedPosture(ctx context.Context, kind string, d audit.Details) {
	if a.risks == nil {
		return
	}
	cov, ok := a.risks.PostureCoverage(ctx, kind)
	if !ok {
		return
	}
	d["risk_accepted"] = true
	d["risk_acceptance_id"] = cov.AcceptanceID.String()
	d["accepted_until"] = ""
	if cov.ExpiresAt != nil {
		d["accepted_until"] = cov.ExpiresAt.UTC().Format(time.RFC3339)
	}
}
