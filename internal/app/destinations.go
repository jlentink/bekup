package app

import (
	"fmt"

	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/netguard"
)

// newDestinations builds the destination service (task 2.12, D-131) with
// the SSRF guard of [netguard] (loopback allowlistable only in dev mode)
// and storage.disk_root. http:// S3 endpoints are allowed in dev mode only.
func (a *App) newDestinations() (*destinations.Service, error) {
	cfg := a.opts.Config
	guard, err := netguard.FromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("netguard: %w", err)
	}
	return destinations.New(destinations.Options{
		DB:        a.pool,
		Keyring:   a.keyring,
		Auditor:   a.auditor,
		Guard:     guard,
		DiskRoot:  cfg.Storage.DiskRoot,
		AllowHTTP: a.opts.Dev,
		Logger:    a.opts.Logger,
	})
}
