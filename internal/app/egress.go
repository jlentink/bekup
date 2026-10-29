package app

import (
	"context"
	"fmt"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/egress"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pubsub"
)

// WorkerListenerApplicationName prefixes the application_name of the LISTEN
// connection of `bekup worker` (settings events only; "<prefix>:<instance
// id>", configreload.ListenerApplicationName).
const WorkerListenerApplicationName = configreload.WorkerListenerPrefix

// newGuard builds the SSRF guards of this process (D-120, D-196, D-198):
// one per scope (destinations, SMTP, shared webhooks, personal webhooks).
// The bootstrap list of [netguard] (loopback allowlistable only in dev
// mode) applies to every scope except personal webhooks; the admin list,
// applied by the egress service to all guards together, applies per
// entry to the scopes it names.
func (a *App) newGuard() error {
	g, err := netguard.SetFromConfig(a.opts.Config)
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	a.guards = g
	return nil
}

// newEgress builds the admin allowlist service, applies the stored list
// before any outbound connection is made (fail closed: a failed read
// stops startup) and registers its sync component. bus delivers the
// settings events (serve: the shared bus fed by its listener; worker: a
// bus of its own with a listener for that channel only). It needs the
// policy for updates (serve).
func (a *App) newEgress(ctx context.Context, bus *pubsub.Bus) error {
	cfg := a.opts.Config
	svc, err := egress.New(egress.Options{
		DB: a.pool, Auditor: a.auditor, Policy: a.policy, Guards: a.guards, Publisher: a.publisher(),
		ConfigCIDRs: cfg.Netguard.AllowCIDRs, ConfigHosts: cfg.Netguard.AllowHosts, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	if err := svc.Load(cctx); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	a.egress = svc
	a.components = append(a.components, egress.NewSync(svc, bus, 0))
	return nil
}

// newWorkerSettingsListener gives `bekup worker` its own bus and a LISTEN
// connection for the settings events (the worker has no subscriptions).
func (a *App) newWorkerSettingsListener() (*pubsub.Bus, error) {
	cfg := a.opts.Config
	poolOpts, err := db.PoolOptionsFromConfig(cfg, configreload.ListenerApplicationName(configreload.KindWorker, a.instanceID))
	if err != nil {
		return nil, err
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return nil, fmt.Errorf("read database URL: %w", err)
	}
	connect, err := db.NewConnector(url, poolOpts)
	config.Zero(url)
	if err != nil {
		return nil, err
	}
	bus := pubsub.NewBus()
	l, err := pubsub.NewListener(pubsub.ListenerOptions{
		Connect: connect, Channels: []pubsub.Channel{pubsub.SettingsEvents}, Bus: bus, Logger: a.opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	a.components = append(a.components, l)
	return bus, nil
}
