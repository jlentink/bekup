package app

import (
	"github.com/jlentink/bekup/internal/monitor"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
)

// Security alerts (task 5.5, D-190). Every security event that bekup
// audits reaches the admins (security.system) or the affected account
// (security.account) through the audit observer (notify.ObserveAudit on
// a.auditor), in the audit entry's transaction: TLS downgrades and other
// opt-outs, pin and host-key mismatches, new admins, owner changes,
// passphrase exports, key and certificate rotation, restore verification
// failures and privileged restore targets, unexpected artifact deletions,
// aborted retention, rejected download tickets, the startup posture and
// the account events of internal/auth (lockouts, recovery codes, factor
// changes, a weakened 2FA policy, passkey counter anomalies), plus the
// failed-login burst detection. The post-commit alert hooks of the
// services (connections/jobs/jobkeys/restore/retention AlertFunc, the
// auth and passkey notifiers) therefore keep their defaults (a log line,
// or nothing): emitting from them too would duplicate every alert. The
// conditions no action reports run in the periodic security check below.

// registerSecurityChecks registers the hourly security check (certificate
// and pin expiry, incremental audit chain verification, posture
// reminders) with the workers.
func (a *App) registerSecurityChecks(ws *queue.Workers) error {
	cfg := a.opts.Config
	c, err := monitor.NewSecurityChecker(monitor.SecurityOptions{
		DB: a.pool, Auditor: a.auditor, Runner: a.runnerHealth(),
		Certs: []monitor.CertFile{
			{Component: monitor.CertRunnerClient, Path: cfg.Runner.CertFile},
			{Component: monitor.CertRunnerCA, Path: cfg.Runner.CAFile},
			{Component: monitor.CertPostgresCA, Path: cfg.Database.TLSCAFile},
			{Component: monitor.CertPostgresClient, Path: cfg.Database.TLSClientCertFile},
		},
		Posture: a.weakenedPosture,
		Logger:  a.opts.Logger,
	})
	if err != nil {
		return err
	}
	return monitor.RegisterSecurityChecks(ws, c)
}

// runnerHealth is the runner client as a monitor.RunnerHealth (nil when
// this process has none; an untyped nil keeps the interface nil).
func (a *App) runnerHealth() monitor.RunnerHealth {
	if a.runner == nil {
		return nil
	}
	return a.runner
}

// weakenedPosture lists the weakened settings of this configuration that
// the startup audits too (D-095, D-100, D-207): never in dev mode.
func (a *App) weakenedPosture() []string {
	if a.opts.Dev {
		return nil
	}
	var out []string
	if a.opts.Config.InsecureTransport() {
		out = append(out, notify.KindInsecureTransport)
	}
	if a.opts.Config.Database.TLSWeakened() {
		out = append(out, notify.KindInsecureDBTLS)
	}
	if a.opts.Config.Database.SingleAccount {
		out = append(out, notify.KindInsecureDBRoles)
	}
	return out
}
