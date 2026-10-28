package app

import (
	"fmt"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/runnerclient"
)

// newConnections builds the runner client from bekup.toml [runner] (mTLS,
// TLS 1.3, private CA, the runner's DNS SAN; D-127) and the connection
// service (task 2.11, D-135). The client connects lazily: a runner that is
// down only fails the runner operations, but unreadable or invalid client
// certificates refuse startup.
func (a *App) newConnections() (*connections.Service, error) {
	rc, err := runnerclient.New(a.opts.Config.Runner)
	if err != nil {
		return nil, fmt.Errorf("refusing to start: %w", err)
	}
	a.runner = rc
	return connections.New(connections.Options{
		DB:      a.pool,
		Keyring: a.keyring,
		Auditor: a.auditor,
		Runner:  rc.RPC(),
		Logger:  a.opts.Logger,
		// Per-connection run limits (task 3.13, D-156).
		MaxConcurrentRuns:        a.opts.Config.Workers.MaxConcurrentRuns,
		DefaultRunsPerConnection: a.opts.Config.Workers.MaxRunsPerConnection,
	})
}
