package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/web"
)

func newServeCmd(g *globalFlags) *cobra.Command {
	var noWorker bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web UI, GraphQL API, scheduler and background workers",
		Long: "Run the HTTP server (embedded web UI, GraphQL API, health endpoints), the scheduler loop\n" +
			"(scheduler.enabled) and the River background workers as bekup_app. --no-worker runs serve\n" +
			"without workers (scale them out with `bekup worker`). Refuses to start when the database role\n" +
			"is privileged, the schema version differs from this binary (run `bekup migrate` first), the\n" +
			"master keyring does not match or BEKUP_SETUP_VERSION names another version (D-212).\n" +
			"SIGINT/SIGTERM drain in-flight requests within server.shutdown_timeout and running jobs\n" +
			"within workers.shutdown_timeout. SIGHUP re-reads bekup.toml and applies the keys that are\n" +
			"safe to change at runtime (D-200).",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, err := config.LoadBekup(g.options(c))
			if err != nil {
				return fmt.Errorf("invalid configuration:\n%w", err)
			}
			logger, level, err := newLevelLogger(c.ErrOrStderr(), res)
			if err != nil {
				return err
			}
			dep, err := deployguard.FromEnv(lookupEnv)
			if err != nil {
				return fmt.Errorf("refusing to start: %w", err)
			}
			var known []string
			res.Warnings, known = dropExpectedWildcard(logger, res.Config, res.Warnings, dep)
			// Includes the plain-HTTP (D-095), weakened DB TLS (D-100) and
			// wildcard listen (D-098) warnings.
			logLoaded(c, logger, res.Path, res.Warnings)
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			hup, stopHUP := reloadSignals()
			defer stopHUP()
			return app.Run(ctx, app.Options{
				Config:        res.Config,
				Dev:           res.Dev,
				Logger:        logger,
				Deployment:    dep,
				UI:            web.DistFS(),
				Version:       version,
				NoWorker:      noWorker,
				LoadOptions:   g.options(c),
				ConfigPath:    res.Path,
				Warnings:      res.Warnings,
				KnownWarnings: known,
				LogLevel:      level,
				ReloadSignals: hup,
			})
		},
	}
	cmd.Flags().BoolVar(&noWorker, "no-worker", false, "do not run background workers in this process (run `bekup worker` instead)")
	return cmd
}

func newWorkerCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run background workers only",
		Long: "Run only the River background workers (backups, restores, retention, notifications,\n" +
			"housekeeping) as bekup_app, without HTTP and without the scheduler, to scale work out next to\n" +
			"`bekup serve --no-worker`. Same startup checks as serve (database role, schema version,\n" +
			"master keyring, setup version). SIGINT/SIGTERM stop fetching jobs, let running jobs finish within\n" +
			"workers.shutdown_timeout, then cancel them. SIGHUP re-reads bekup.toml and applies the\n" +
			"keys that are safe to change at runtime (D-200).",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, err := config.LoadBekup(g.options(c))
			if err != nil {
				return fmt.Errorf("invalid configuration:\n%w", err)
			}
			logger, level, err := newLevelLogger(c.ErrOrStderr(), res)
			if err != nil {
				return err
			}
			dep, err := deployguard.FromEnv(lookupEnv)
			if err != nil {
				return fmt.Errorf("refusing to start: %w", err)
			}
			// The worker binds no HTTP listener.
			var known []string
			res.Warnings, known = splitListenWarning(res.Config, res.Warnings)
			logLoaded(c, logger, res.Path, res.Warnings)
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			hup, stopHUP := reloadSignals()
			defer stopHUP()
			return app.Run(ctx, app.Options{
				Config:        res.Config,
				Dev:           res.Dev,
				Logger:        logger,
				Deployment:    dep,
				Version:       version,
				Mode:          app.ModeWorker,
				LoadOptions:   g.options(c),
				ConfigPath:    res.Path,
				Warnings:      res.Warnings,
				KnownWarnings: known,
				LogLevel:      level,
				ReloadSignals: hup,
			})
		},
	}
}

// dropExpectedWildcard removes the allow_wildcard_listen startup warning
// when the wildcard is the recorded compose shape's own (D-215: the
// own-proxy join shape joins the operator's proxy network, where Docker
// assigns serve's address; the shipped serve service sets
// BEKUP_LISTEN_SHAPE, setup check pins server.listen to
// config.OwnProxyJoinListen, and the proxy is trusted only by name:
// trusted_proxy_hosts set, trusted_proxies empty). It logs an INFO line
// instead and returns the dropped warning separately (a config reload must
// not log it as new); every other warning stays.
func dropExpectedWildcard(logger *slog.Logger, cfg *config.Bekup, warnings []string, dep deployguard.Deployment) (kept, dropped []string) {
	w := cfg.WildcardListenWarning()
	if w == "" || !dep.ExpectedWildcardListen(cfg.Server.Listen, config.OwnProxyJoinListen) ||
		len(cfg.Server.TrustedProxyHosts) == 0 || len(cfg.Server.TrustedProxies) > 0 {
		return warnings, nil
	}
	kept, dropped = splitWarning(warnings, w)
	logger.Info("server.listen binds every interface of the serve container, as the own-proxy join shape requires (D-215); serve accepts connections only on its address on your proxy's network (alias bekup-serve) and on loopback, and only server.trusted_proxy_hosts may forward client addresses")
	return kept, dropped
}

// withoutListenWarning drops the allow_wildcard_listen startup warning for
// commands that never bind server.listen (migrate, db bootstrap and the
// CLI commands on the app database, e.g. the admin service): it concerns
// serve only (D-215: the own-proxy compose shape writes the wildcard
// address on purpose). `bekup config validate` and serve keep it.
func withoutListenWarning(cfg *config.Bekup, warnings []string) []string {
	kept, _ := splitListenWarning(cfg, warnings)
	return kept
}

// splitListenWarning is withoutListenWarning that also returns the dropped
// warning (`bekup worker`: a config reload must not log it as new).
func splitListenWarning(cfg *config.Bekup, warnings []string) (kept, dropped []string) {
	w := cfg.WildcardListenWarning()
	if w == "" {
		return warnings, nil
	}
	return splitWarning(warnings, w)
}

// splitWarning separates w from warnings.
func splitWarning(warnings []string, w string) (kept, dropped []string) {
	kept = make([]string, 0, len(warnings))
	for _, x := range warnings {
		if x == w {
			dropped = append(dropped, x)
			continue
		}
		kept = append(kept, x)
	}
	return kept, dropped
}
