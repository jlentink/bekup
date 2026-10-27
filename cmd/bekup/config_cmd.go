package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
)

func newConfigCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate or print the effective configuration",
	}
	var runner bool
	cmd.PersistentFlags().BoolVar(&runner, "runner", false, "use runner.toml instead of bekup.toml")

	validate := &cobra.Command{
		Use:   "validate",
		Short: "Load and validate the configuration; report every error at once",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return withConfig(c, g, runner, func(path string, dev bool, warnings []string, _ func(io.Writer) error) error {
				if path == "" {
					path = "none (dev mode, built-in defaults)"
				}
				var b strings.Builder
				fmt.Fprintf(&b, "configuration OK (file: %s, dev mode: %t)\n", path, dev)
				for _, w := range warnings {
					fmt.Fprintf(&b, "warning: %s\n", w)
				}
				_, err := io.WriteString(c.OutOrStdout(), b.String())
				return err
			})
		},
	}

	var redacted bool
	printCmd := &cobra.Command{
		Use:   "print",
		Short: "Print the effective configuration with the source of every key (secrets redacted)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !redacted {
				return errors.New("config print only supports --redacted output; secrets are never printed")
			}
			return withConfig(c, g, runner, func(_ string, _ bool, _ []string, write func(io.Writer) error) error {
				return write(c.OutOrStdout())
			})
		},
	}
	printCmd.Flags().BoolVar(&redacted, "redacted", true, "redact secrets (always on; --redacted=false is refused)")

	cmd.AddCommand(validate, printCmd)
	return cmd
}

// withConfig loads bekup.toml or runner.toml, logs the loaded path and any
// warnings to stderr, and passes the details to fn.
func withConfig(c *cobra.Command, g *globalFlags, runner bool,
	fn func(path string, dev bool, warnings []string, write func(io.Writer) error) error,
) error {
	if runner {
		res, _, err := loadRunnerConfig(c, g)
		if err != nil {
			return err
		}
		return fn(res.Path, res.Dev, res.Warnings, res.WriteRedacted)
	}
	opts := g.options(c)
	stderr := c.ErrOrStderr()
	res, err := config.LoadBekup(opts)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger, err := newLogger(stderr, res)
	if err != nil {
		return err
	}
	logLoaded(c, logger, res.Path, res.Warnings)
	return fn(res.Path, res.Dev, res.Warnings, res.WriteRedacted)
}

func logLoaded(c *cobra.Command, logger *slog.Logger, path string, warnings []string) {
	ctx := c.Context()
	logger.InfoContext(ctx, "configuration loaded", slog.String("config_file", path))
	for _, msg := range warnings {
		logger.WarnContext(ctx, "configuration warning", slog.String("warning", msg))
	}
}
