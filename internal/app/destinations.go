package app

import (
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/netguard"
)

// newDestinations builds the destination service (task 2.12, D-131) with
// the process's destinations SSRF guard ([netguard] plus the admin entries
// scoped to destinations) and
// storage.disk_root. http:// S3 endpoints are allowed in dev mode only.
func (a *App) newDestinations() (*destinations.Service, error) {
	cfg := a.opts.Config
	return destinations.New(destinations.Options{
		DB:        a.pool,
		Keyring:   a.keyring,
		Auditor:   a.auditor,
		Guard:     a.guards.Guard(netguard.ScopeDestinations),
		DiskRoot:  cfg.Storage.DiskRoot,
		AllowHTTP: a.opts.Dev,
		Logger:    a.opts.Logger,
		// destination.test_failed (task 5.3).
		TestFailed: a.destinationTestFailed,
	})
}
