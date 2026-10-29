package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/config"
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
			"is privileged, the schema version differs from this binary (run `bekup migrate` first) or the\n" +
			"master keyring does not match. SIGINT/SIGTERM drain in-flight requests within\n" +
			"server.shutdown_timeout and running jobs within workers.shutdown_timeout. SIGHUP re-reads\n" +
			"bekup.toml and applies the keys that are safe to change at runtime (D-200).",
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
				UI:            web.DistFS(),
				Version:       version,
				NoWorker:      noWorker,
				LoadOptions:   g.options(c),
				ConfigPath:    res.Path,
				Warnings:      res.Warnings,
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
			"master keyring). SIGINT/SIGTERM stop fetching jobs, let running jobs finish within\n" +
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
			logLoaded(c, logger, res.Path, res.Warnings)
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			hup, stopHUP := reloadSignals()
			defer stopHUP()
			return app.Run(ctx, app.Options{
				Config:        res.Config,
				Dev:           res.Dev,
				Logger:        logger,
				Version:       version,
				Mode:          app.ModeWorker,
				LoadOptions:   g.options(c),
				ConfigPath:    res.Path,
				Warnings:      res.Warnings,
				LogLevel:      level,
				ReloadSignals: hup,
			})
		},
	}
}
