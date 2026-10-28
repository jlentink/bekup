package app

import (
	"context"

	"github.com/jlentink/bekup/internal/downloads"
)

// newDownloads builds the download ticket service (task 3.9, D-150) on the
// destination and job key services and the central policy.
func (a *App) newDownloads() error {
	svc, err := downloads.New(downloads.Options{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Authorizer: a.policy,
		Destinations: a.destinations, Keys: a.jobKeys, Logger: a.opts.Logger,
		MaxConcurrent:        a.opts.Config.Workers.MaxConcurrentDownloads,
		MaxConcurrentPerUser: a.opts.Config.Workers.MaxDownloadsPerUser,
	})
	if err != nil {
		return err
	}
	a.downloads = svc
	return nil
}

// downloadCleanupComponent deletes expired download tickets (and with them
// any sealed identity of an unused one) every 30 seconds.
type downloadCleanupComponent struct{ svc *downloads.Service }

func (c downloadCleanupComponent) Name() string { return "download ticket cleanup" }

func (c downloadCleanupComponent) Run(ctx context.Context) error { return c.svc.RunCleanup(ctx) }
