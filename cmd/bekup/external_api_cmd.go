package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/externalapi"
	"github.com/jlentink/bekup/internal/logging"
)

func newExternalAPICmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "external-api",
		Short: "Run the token-authenticated REST API for CI pipelines (reads external-api.toml)",
		Long: "Run the external API (D-220): POST /api/v1/jobs/{jobId}/runs starts a backup job's run,\n" +
			"GET /api/v1/requests/{requestId} reports its state and GET /api/v1/self the token's scope and\n" +
			"the client address bekup sees. Clients authenticate with a per-job API token in the\n" +
			"Authorization: Bearer header only. It serves plain HTTP behind the TLS reverse proxy\n" +
			"(listen.trusted_proxies / trusted_proxy_hosts) and connects to the app database only as\n" +
			"bekup_external; it never reads bekup.toml, the master key or runner certificates.\n" +
			"It refuses to start on another version than BEKUP_SETUP_VERSION (D-212), when the\n" +
			"database role holds anything beyond its allowlist, or when the schema version differs from\n" +
			"this binary (run `bekup migrate up` first). SIGINT/SIGTERM drain in-flight requests within\n" +
			"listen.shutdown_timeout.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, logger, err := loadExternalAPIConfig(c, g)
			if err != nil {
				return err
			}
			if err := checkDeployment(c, logger, res.Dev, "external-api"); err != nil {
				return err
			}
			dep, err := deployguard.FromEnv(lookupEnv)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return externalapi.Run(ctx, externalapi.RunOptions{Config: res.Config, Logger: logger, Deployment: dep})
		},
	}
}

// loadExternalAPIConfig loads external-api.toml and builds the redacting
// logger (--log-level overrides logging.level; text is the default format
// in dev mode). Load warnings are logged.
func loadExternalAPIConfig(c *cobra.Command, g *globalFlags) (*config.Result[config.ExternalAPI], *slog.Logger, error) {
	opts := config.Options{ConfigPath: g.configPath, Dev: g.dev}
	if c.Flags().Changed("log-level") {
		opts.Flags = map[string]string{"logging.level": g.logLevel}
	}
	res, err := config.LoadExternalAPI(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid external API configuration:\n%w", err)
	}
	format := res.Config.Logging.Format
	if res.Dev && res.Sources["logging.format"] == config.SourceDefault {
		format = "text"
	}
	logger, err := logging.New(c.ErrOrStderr(), logging.Options{Level: res.Config.Logging.Level, Format: format})
	if err != nil {
		return nil, nil, err
	}
	logLoaded(c, logger, res.Path, dropJoinWildcard(logger, res.Config, res.Warnings, res.Dev))
	return res, logger, nil
}

// dropJoinWildcard removes the allow_wildcard startup warning when the
// wildcard is the own-proxy join shape's own (D-220 amendment 4: the
// compose service sets BEKUP_LISTEN_SHAPE and listen.address is
// config.ExternalAPIJoinListen; the listener then serves only the external
// API's address on the proxy network and loopback) and logs an INFO line
// instead. A malformed deployment fact keeps the warning (checkDeployment
// refuses it next).
func dropJoinWildcard(logger *slog.Logger, cfg *config.ExternalAPI, warnings []string, dev bool) []string {
	w := cfg.WildcardListenWarning(dev)
	if w == "" {
		return warnings
	}
	dep, err := deployguard.FromEnv(lookupEnv)
	if err != nil || !externalapi.JoinMode(cfg, dep) {
		return warnings
	}
	kept := make([]string, 0, len(warnings))
	for _, x := range warnings {
		if x != w {
			kept = append(kept, x)
		}
	}
	logger.Info("listen.address binds every interface of the external-api container, as the own-proxy join shape requires (D-220); it accepts connections only on its address on your proxy's network (alias bekup-external-api) and on loopback")
	return kept
}
