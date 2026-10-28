package main

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

// globalFlags are the persistent flags shared by every subcommand.
type globalFlags struct {
	configPath string
	dev        bool
	logLevel   string
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "bekup",
		Short:         "bekup: secure, self-hosted database backup manager",
		Long:          "bekup schedules and runs PostgreSQL, MySQL and MariaDB backups with native dump tools,\nencrypts them and writes them to disk, S3 and SFTP.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&g.configPath, "config", "", "config file path (or BEKUP_CONFIG); a missing explicit file is an error")
	pf.BoolVar(&g.dev, "dev", false, "dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production")
	pf.StringVar(&g.logLevel, "log-level", "", "log level: debug, info, warn or error (overrides log.level)")

	root.AddCommand(newVersionCmd(), newConfigCmd(g), newCertsCmd(g), newKeysCmd(g), newMigrateCmd(g), newAuditCmd(g), newAdminCmd(g), newServeCmd(g), newWorkerCmd(g), newRunnerCmd(g), newHealthcheckCmd(g), newDocsCmd(),
		newVerifyCmd(), newDecryptCmd(), newReindexCmd(g))
	for _, s := range stubs {
		root.AddCommand(newStubCmd(s))
	}
	return root
}

// options converts the global flags into config load options.
func (g *globalFlags) options(cmd *cobra.Command) config.Options {
	opts := config.Options{ConfigPath: g.configPath, Dev: g.dev}
	if cmd.Flags().Changed("log-level") {
		opts.Flags = map[string]string{"log.level": g.logLevel}
	}
	return opts
}

// newLogger builds the redacting logger for a loaded bekup config. In dev
// mode the default format is text.
func newLogger(w io.Writer, res *config.Result[config.Bekup]) (*slog.Logger, error) {
	format := res.Config.Log.Format
	if res.Dev && res.Sources["log.format"] == config.SourceDefault {
		format = "text"
	}
	return logging.New(w, logging.Options{Level: res.Config.Log.Level, Format: format})
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the bekup version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "bekup %s\n", version)
			return err
		},
	}
}
