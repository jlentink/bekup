package egress

import (
	"context"
	"log/slog"
	"time"

	"github.com/jlentink/bekup/internal/pubsub"
)

// DefaultReloadInterval is how often Sync re-applies the stored list
// without a notification (a lost NOTIFY delays a change at most this
// long).
const DefaultReloadInterval = 5 * time.Minute

// Sync keeps the guard of one process in line with the stored list: it is
// a long-running component (Name, Run).
type Sync struct {
	svc      *Service
	bus      *pubsub.Bus
	interval time.Duration
	logger   *slog.Logger
}

// NewSync returns the component; bus delivers pubsub.SettingsEvents (nil:
// periodic reloads only), interval 0 means DefaultReloadInterval.
func NewSync(svc *Service, bus *pubsub.Bus, interval time.Duration) *Sync {
	if interval <= 0 {
		interval = DefaultReloadInterval
	}
	return &Sync{svc: svc, bus: bus, interval: interval, logger: svc.o.Logger}
}

// Name implements the app component.
func (s *Sync) Name() string { return "ssrf allowlist sync" }

// Run reloads on every settings event for SettingKey, after possibly
// lost events and every interval, until ctx ends. A failed reload keeps
// the current list and is retried at the next trigger.
func (s *Sync) Run(ctx context.Context) error {
	var c <-chan pubsub.Message
	var resync <-chan struct{}
	if s.bus != nil {
		sub := s.bus.Subscribe(pubsub.SettingsEvents.Name, 16)
		defer sub.Close()
		c, resync = sub.C(), sub.Resync()
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-c:
			if m.Payload != SettingKey {
				continue
			}
		case <-resync:
		case <-t.C:
		}
		if err := s.svc.Load(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "reloading the SSRF allowlist failed; keeping the current one", slog.Any("error", err))
		}
	}
}
