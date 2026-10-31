package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runner"
	"github.com/jlentink/bekup/internal/runner/executor"
)

func newRunnerCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "runner",
		Short: "Run the Docker runner (mTLS gRPC, reads runner.toml)",
		Long: "Run the runner sidecar: the only component with Docker access. It serves the typed\n" +
			"runner.v1 gRPC API over mTLS (TLS 1.3, private CA, allowed client SANs from runner.toml)\n" +
			"and the standard gRPC health service on listen.health_socket for `bekup healthcheck runner`.\n" +
			"It runs dump/restore containers on per-run internal networks through the Docker socket\n" +
			"(docker.host) and removes leftovers of earlier runs at startup and every 5 minutes.\n" +
			"It refuses to start, before it touches Docker or listens, on another version than\n" +
			"BEKUP_SETUP_VERSION (and, outside dev mode, on BEKUP_IMAGE_MODE=following without it; D-212).\n" +
			"SIGINT/SIGTERM drain in-flight calls for up to 45s.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, logger, err := loadRunnerConfig(c, g)
			if err != nil {
				return err
			}
			// D-212: the setup version guard before Docker or the gRPC
			// listener is touched: a runner of another version than setup
			// prepared (a moved tag, a pulled image) never starts.
			if err := checkDeployment(c, logger, res.Dev, "runner"); err != nil {
				return err
			}
			// Docker over the unix socket of docker.host only. The client does
			// not connect yet; the executor initializes in the background
			// (retrying while the daemon is unavailable) and Health reports
			// the Docker state.
			docker, closer, err := executor.NewMobyDocker(res.Config.Docker.Host)
			if err != nil {
				return err
			}
			defer func() { discardCloseErr(closer.Close()) }()
			srv, err := runner.New(runner.Options{
				Config:       res.Config,
				Dev:          res.Dev,
				Logger:       logger,
				Version:      version,
				DockerClient: docker,
			})
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

// loadRunnerConfig loads runner.toml and builds the redacting logger.
// runner.toml has no [log] section: --log-level sets the level, the format
// is json (text in dev mode). Load warnings are logged.
func loadRunnerConfig(c *cobra.Command, g *globalFlags) (*config.Result[config.Runner], *slog.Logger, error) {
	opts := g.options(c)
	opts.Flags = nil
	res, err := config.LoadRunner(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid runner configuration:\n%w", err)
	}
	format := "json"
	if res.Dev {
		format = "text"
	}
	logger, err := logging.New(c.ErrOrStderr(), logging.Options{Level: g.logLevel, Format: format})
	if err != nil {
		return nil, nil, err
	}
	logLoaded(c, logger, res.Path, res.Warnings)
	return res, logger, nil
}

// discardCloseErr ignores the error of closing the Docker client on exit.
func discardCloseErr(error) {}
