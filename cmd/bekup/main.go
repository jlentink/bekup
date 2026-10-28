// Command bekup is the single binary of the bekup database backup manager.
// Subcommands: serve, worker, runner, migrate, admin, keys, certs, audit,
// verify, decrypt, reindex, config and version.
package main

import (
	"context"
	"fmt"
	"os"
	_ "time/tzdata" // embedded IANA zone database for schedules (D-044)

	"github.com/jlentink/bekup/internal/logging"
)

// version is injected at build time via
// -ldflags "-X main.version=<x.y.z>" (see the justfile, task 0.1b).
var version = "0.0.0-dev"

func main() {
	if err := newRootCmd().ExecuteContext(context.Background()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bekup: %s\n", errorMessage(err))
		os.Exit(1)
	}
}

// errorMessage renders a fatal error for stderr with URL credentials,
// password=... pairs and every registered secret value removed, like the
// redacting log handler does.
func errorMessage(err error) string {
	return logging.DefaultRegistry.Scrub(logging.RedactCredentials(err.Error()))
}
