package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newDocsCmd is a hidden development command that generates reference
// documentation (`just docs`).
func newDocsCmd() *cobra.Command {
	docs := &cobra.Command{
		Use:    "docs",
		Short:  "Generate reference documentation (development)",
		Hidden: true,
	}
	var dir string
	cli := &cobra.Command{
		Use:   "cli",
		Short: "Write the Markdown CLI reference (one file per command) to --dir",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return writeCLIDocs(c.Root(), dir)
		},
	}
	cli.Flags().StringVar(&dir, "dir", "", "output directory, e.g. docs/reference/cli (required)")
	if err := cli.MarkFlagRequired("dir"); err != nil {
		panic(err) // programming error: the flag is defined above
	}
	docs.AddCommand(cli)
	return docs
}

// writeCLIDocs regenerates dir deterministically: stale bekup*.md files are
// removed first and the "Auto generated ... on <date>" footer is disabled.
// Hidden commands (such as docs itself) are skipped by cobra/doc.
func writeCLIDocs(root *cobra.Command, dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	stale, err := filepath.Glob(filepath.Join(dir, "bekup*.md"))
	if err != nil {
		return err
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("remove stale %s: %w", f, err)
		}
	}
	disableAutoGenTag(root)
	if err := doc.GenMarkdownTree(root, dir); err != nil {
		return fmt.Errorf("generate CLI reference: %w", err)
	}
	return nil
}

func disableAutoGenTag(c *cobra.Command) {
	c.DisableAutoGenTag = true
	for _, sub := range c.Commands() {
		disableAutoGenTag(sub)
	}
}
