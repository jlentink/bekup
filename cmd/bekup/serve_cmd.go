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
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the web UI, GraphQL API and scheduler",
		Long: "Run the HTTP server (embedded web UI, GraphQL API, health endpoints; workers and the\n" +
			"scheduler follow in later releases) as bekup_app. Refuses to start when the database role is\n" +
			"privileged or the schema version differs from this binary (run `bekup migrate` first).\n" +
			"SIGINT/SIGTERM drain in-flight requests within server.shutdown_timeout.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, err := config.LoadBekup(g.options(c))
			if err != nil {
				return fmt.Errorf("invalid configuration:\n%w", err)
			}
			logger, err := newLogger(c.ErrOrStderr(), res)
			if err != nil {
				return err
			}
			// Includes the plain-HTTP (D-095), weakened DB TLS (D-100) and
			// wildcard listen (D-098) warnings.
			logLoaded(c, logger, res.Path, res.Warnings)
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return app.Run(ctx, app.Options{
				Config:  res.Config,
				Dev:     res.Dev,
				Logger:  logger,
				UI:      web.DistFS(),
				Version: version,
			})
		},
	}
}
