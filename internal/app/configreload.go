package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/retention"
)

// Runtime config reload wiring (task 6.13, D-200). Every live key of
// bekup.toml is covered by exactly one applier (configreload.New refuses
// otherwise); a key a process does not use is declared with a nil Prepare.
// Appliers read the components from a when they prepare, so the reloader
// can be built before the HTTP handler that serves reloadConfig.

var errNotReady = errors.New("component not ready")

// Live key groups (each one applier).
var (
	reloadKeysLog  = []string{"log.level"}
	reloadKeysHTTP = []string{
		"server.trusted_proxies", "server.max_body_bytes", "server.rate_limit_per_second",
		"server.rate_limit_burst", "server.user_rate_limit_per_second", "server.user_rate_limit_burst",
	}
	reloadKeysGraphQL  = []string{"graphql.max_complexity", "graphql.max_depth", "graphql.max_body_bytes"}
	reloadKeysNetguard = []string{"netguard.allow_cidrs", "netguard.allow_hosts"}
	reloadKeysAuth     = []string{
		"security.password_min_length", "security.password_min_score",
		"security.login_rate_limit_per_minute", "security.login_rate_limit_burst",
		"security.lockout_threshold", "security.lockout_base_duration", "security.lockout_max_duration",
	}
	reloadKeysNotify = []string{
		"notifications.max_per_user_per_hour", "notifications.collapse_window", "notifications.webhook_failure_threshold",
	}
	reloadKeysRetention = []string{"retention.dry_run", "retention.max_deletions_per_sweep", "retention.partial_cleanup_age"}
)

// reloadAppliers returns the appliers of this process.
func (a *App) reloadAppliers() []configreload.Applier {
	serve := a.opts.Mode == ModeServe
	out := []configreload.Applier{
		{Name: "logging", Keys: reloadKeysLog, Prepare: a.prepareLogLevel},
		{Name: "ssrf guard", Keys: reloadKeysNetguard, Prepare: a.prepareNetguard},
		{Name: "notifications", Keys: reloadKeysNotify, Prepare: a.prepareNotify},
	}
	if a.opts.LogLevel == nil {
		out[0].Prepare = nil
	}
	if serve {
		out = append(out,
			configreload.Applier{Name: "http server", Keys: reloadKeysHTTP, Prepare: a.prepareHTTP},
			configreload.Applier{Name: "graphql", Keys: reloadKeysGraphQL, Prepare: a.prepareGraphQL},
			configreload.Applier{Name: "auth", Keys: reloadKeysAuth, Prepare: a.prepareAuth})
	} else {
		// `bekup worker` serves no HTTP and checks no passwords.
		out = append(out,
			configreload.Applier{Name: "http server (not in a worker)", Keys: reloadKeysHTTP},
			configreload.Applier{Name: "graphql (not in a worker)", Keys: reloadKeysGraphQL},
			configreload.Applier{Name: "auth (not in a worker)", Keys: reloadKeysAuth})
	}
	retentionApplier := configreload.Applier{Name: "retention", Keys: reloadKeysRetention, Prepare: a.prepareRetention}
	if serve && a.opts.NoWorker {
		// serve --no-worker runs no retention worker.
		retentionApplier.Prepare = nil
	}
	return append(out, retentionApplier)
}

func (a *App) prepareLogLevel(next *config.Bekup) (func(), error) {
	level, err := logging.ParseLevel(next.Log.Level)
	if err != nil {
		return nil, err
	}
	lv := a.opts.LogLevel
	return func() { lv.Set(level) }, nil
}

func (a *App) prepareHTTP(next *config.Bekup) (func(), error) {
	if a.httpServer == nil {
		return nil, fmt.Errorf("http server: %w", errNotReady)
	}
	s, err := httpserver.LiveFromConfig(&next.Server)
	if err != nil {
		return nil, err
	}
	return a.httpServer.Live().Prepare(s)
}

func (a *App) prepareGraphQL(next *config.Bekup) (func(), error) {
	if a.gql == nil {
		return nil, fmt.Errorf("graphql: %w", errNotReady)
	}
	return a.gql.PrepareLimits(graph.Limits{
		MaxComplexity: next.GraphQL.MaxComplexity, MaxDepth: next.GraphQL.MaxDepth, MaxBodyBytes: next.GraphQL.MaxBodyBytes,
	})
}

// prepareNetguard swaps the bootstrap allowlist of every guard (validated
// with the startup dev mode; personal webhooks never get it, D-198) and
// the list the network page shows.
func (a *App) prepareNetguard(next *config.Bekup) (func(), error) {
	if a.guards == nil {
		return nil, fmt.Errorf("ssrf guard: %w", errNotReady)
	}
	commit, err := a.guards.PrepareBootstrap(next.Netguard.AllowCIDRs, next.Netguard.AllowHosts)
	if err != nil {
		return nil, err
	}
	cidrs, hosts := next.Netguard.AllowCIDRs, next.Netguard.AllowHosts
	eg := a.egress
	return func() {
		commit()
		if eg != nil {
			eg.SetConfigList(cidrs, hosts)
		}
	}, nil
}

// prepareAuth swaps the password and lockout policies (shared by login,
// TOTP and user administration) and the login/account limiters.
func (a *App) prepareAuth(next *config.Bekup) (func(), error) {
	if a.auth == nil || a.loginLimiter == nil || a.accountLimiter == nil {
		return nil, fmt.Errorf("auth: %w", errNotReady)
	}
	sec := next.Security
	commitPolicies, err := a.auth.Limits().Prepare(
		auth.PasswordPolicy{MinLength: sec.PasswordMinLength, MinScore: sec.PasswordMinScore},
		auth.LockoutPolicy{Threshold: sec.LockoutThreshold, Base: sec.LockoutBaseDuration.D(), Max: sec.LockoutMaxDuration.D()},
	)
	if err != nil {
		return nil, err
	}
	rate, burst := float64(sec.LoginRateLimitPerMinute)/60, sec.LoginRateLimitBurst
	commitLogin, err := a.loginLimiter.PrepareLimits(rate, burst)
	if err != nil {
		return nil, err
	}
	commitAccounts, err := a.accountLimiter.PrepareLimits(rate, burst)
	if err != nil {
		return nil, err
	}
	return func() {
		commitPolicies()
		commitLogin()
		commitAccounts()
	}, nil
}

func (a *App) prepareNotify(next *config.Bekup) (func(), error) {
	if a.notifier == nil {
		return nil, fmt.Errorf("notifications: %w", errNotReady)
	}
	cur := a.notifier.CurrentLimits()
	fp := cur.FailurePolicy
	fp.MaxPermanent = next.Notifications.WebhookFailureThreshold
	return a.notifier.PrepareLimits(notify.Limits{
		CollapseWindow:    next.Notifications.CollapseWindow.D(),
		MaxPerUserPerHour: next.Notifications.MaxPerUserPerHour,
		FailurePolicy:     fp,
	})
}

func (a *App) prepareRetention(next *config.Bekup) (func(), error) {
	if a.retention == nil {
		return nil, fmt.Errorf("retention: %w", errNotReady)
	}
	r := next.Retention
	commit, err := a.retention.PrepareLimits(retention.Limits{
		DryRun: r.DryRun, MaxDeletions: r.MaxDeletionsPerSweep, PartialCleanupAge: r.PartialCleanupAge.D(),
	})
	if err != nil {
		return nil, err
	}
	logger := a.opts.Logger
	dry := r.DryRun
	return func() {
		commit()
		if dry {
			logger.Warn("retention.dry_run is set: retention plans are only logged, nothing is deleted")
		}
	}, nil
}

// newReloader builds the reloader of this process (and, in serve, the
// API behind the GraphQL fields).
func (a *App) newReloader() error {
	kind := configreload.KindServe
	if a.opts.Mode == ModeWorker {
		kind = configreload.KindWorker
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	r, err := configreload.New(configreload.Options{
		DB: a.pool, Auditor: a.auditor, Load: a.opts.LoadOptions, ConfigPath: a.opts.ConfigPath, Dev: a.opts.Dev,
		Running: a.opts.Config, Warnings: a.opts.Warnings, Appliers: a.reloadAppliers(),
		InstanceID: a.instanceID, Kind: kind, Hostname: host, Version: a.opts.Version, StartedAt: a.startedAt,
		Metrics: a.metrics, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.reloader = r
	if a.opts.Mode == ModeWorker {
		return nil
	}
	a.reloadAPI, err = configreload.NewAPI(configreload.APIOptions{
		DB: a.pool, Auditor: a.auditor, Policy: a.policy, Reloader: r, Logger: a.opts.Logger,
	})
	return err
}

// startReload records this process's startup row (the process registry)
// and registers the reload components: the broadcast sync on bus, the
// SIGHUP loop and, in serve, the hourly pruning.
func (a *App) startReload(ctx context.Context, bus *pubsub.Bus) error {
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(a.opts.Config))
	defer cancel()
	if err := a.reloader.RecordStartup(cctx); err != nil {
		return err
	}
	a.components = append(a.components, configreload.NewSync(a.reloader, a.pool, bus, a.opts.Logger))
	if a.opts.ReloadSignals != nil {
		a.components = append(a.components, configreload.NewSignalLoop(a.reloader, a.opts.ReloadSignals, 0))
	}
	if a.opts.Mode == ModeServe {
		a.components = append(a.components, configreload.NewPruner(a.pool, a.opts.Logger, time.Hour))
	}
	return nil
}

// Reloader returns the process's config reloader (tests).
func (a *App) Reloader() *configreload.Reloader { return a.reloader }
