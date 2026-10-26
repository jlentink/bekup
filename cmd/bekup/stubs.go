package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type stub struct {
	name  string
	short string
	task  string
}

// stubs are subcommands that later tasks implement.
var stubs = []stub{
	{"serve", "Run the web UI, GraphQL API and scheduler", "1.1"},
	{"worker", "Run background workers only", "3.4"},
	{"runner", "Run the Docker runner (mTLS gRPC, reads runner.toml)", "2.4"},
	{"admin", "Create admins and reset passwords", "1.6"},
	{"audit", "Verify the audit log hash chain", "1.3"},
	{"verify", "Verify a backup artifact offline", "4.7"},
	{"decrypt", "Decrypt a backup artifact offline", "4.7"},
}

func newStubCmd(s stub) *cobra.Command {
	return &cobra.Command{
		Use:                s.name,
		Short:              s.short + " (not implemented yet)",
		DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("%s: not implemented yet (task %s)", s.name, s.task)
		},
	}
}
