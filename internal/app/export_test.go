package app

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/monitor"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pubsub"
)

// SubscriptionsReady reports whether a's LISTEN connection is up and its
// event stream hub receives session events (tests poll it instead of
// sleeping).
func SubscriptionsReady(a *App) bool {
	return a.subs.listener.Connected() && a.subs.bus.Subscribers(pubsub.SessionEvents.Name) > 0
}

// Metrics returns a's metric hooks (tests feed fake events).
func Metrics(a *App) *monitor.Metrics { return a.metrics }

// ReloadAPI returns a's config reload API (serve only).
func ReloadAPI(a *App) *configreload.API { return a.reloadAPI }

// CatchUp runs a's config reload catch-up once (as after a Resync).
func CatchUp(ctx context.Context, a *App) {
	configreload.NewSync(a.reloader, a.pool, nil, nil).CatchUp(ctx)
}

// LiveValues are the values a's components run with for one key of every
// reload applier group (zero/false where a does not run the component).
type LiveValues struct {
	LogLevel          slog.Level
	HTTP              bool
	MaxBodyBytes      int64
	MaxDepth          int
	PasswordMinLength int
	// AllowsHost: the destinations guard allows a private address for host.
	AllowsHost        func(host string) bool
	CollapseWindow    time.Duration
	Retention         bool
	PartialCleanupAge time.Duration
}

// Live returns the values a's components run with now.
func Live(a *App) LiveValues {
	v := LiveValues{
		LogLevel:       a.opts.LogLevel.Level(),
		CollapseWindow: a.notifier.CurrentLimits().CollapseWindow,
		AllowsHost: func(host string) bool {
			return a.guards.Guard(netguard.ScopeDestinations).Check(netip.MustParseAddr("10.200.0.1"), host) == nil
		},
	}
	if a.httpServer != nil {
		v.HTTP, v.MaxBodyBytes = true, a.httpServer.Live().MaxBodyBytes()
	}
	if a.gql != nil {
		v.MaxDepth = a.gql.CurrentLimits().MaxDepth
	}
	if a.auth != nil {
		v.PasswordMinLength = a.auth.Limits().Policy().MinLength
	}
	if a.retention != nil {
		v.Retention, v.PartialCleanupAge = true, a.retention.CurrentLimits().PartialCleanupAge
	}
	return v
}
