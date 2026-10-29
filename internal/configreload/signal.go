package configreload

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MinSignalInterval is the shortest gap between two signal reloads.
const MinSignalInterval = 2 * time.Second

// SignalLoop reloads this process for every value received on a channel
// fed by SIGHUP (cmd/bekup; buffered with size 1, so bursts collapse), at
// most once per interval. It is a long-running component (Name, Run).
// Signal reloads are local: a "signal" request row, never a broadcast.
type SignalLoop struct {
	r        *Reloader
	c        <-chan struct{}
	interval time.Duration
}

// NewSignalLoop returns the component (interval 0: MinSignalInterval).
func NewSignalLoop(r *Reloader, c <-chan struct{}, interval time.Duration) *SignalLoop {
	if interval <= 0 {
		interval = MinSignalInterval
	}
	return &SignalLoop{r: r, c: c, interval: interval}
}

// Name implements the app component.
func (s *SignalLoop) Name() string { return "config reload signal" }

// Run waits for signals until ctx ends.
func (s *SignalLoop) Run(ctx context.Context) error {
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.c:
		}
		if wait := s.interval - time.Since(last); !last.IsZero() && wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
		last = time.Now()
		s.reload(ctx)
	}
}

// reload runs one signal reload; the Reloader logs, audits and counts the
// outcome (an unrecorded one is logged there too).
func (s *SignalLoop) reload(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, LocalTimeout)
	defer cancel()
	if _, err := s.r.Reload(rctx, Request{ID: uuid.New(), Trigger: TriggerSignal}); err != nil {
		return
	}
}
