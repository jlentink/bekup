package restore

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Metrics receives finished restores and test restores (the Prometheus
// counters of task 5.6). Calls happen after the terminal status committed;
// implementations must be fast and must not fail.
type Metrics interface {
	// RestoreFinished counts a restore that reached a terminal status.
	RestoreFinished(mode sqlc.RestoreMode, status Status)
	// TestRestoreFinished counts a test restore that reached a terminal
	// status.
	TestRestoreFinished(status sqlc.TestRestoreStatus)
}

// NopMetrics discards everything.
type NopMetrics struct{}

// RestoreFinished implements Metrics.
func (NopMetrics) RestoreFinished(sqlc.RestoreMode, Status) {}

// TestRestoreFinished implements Metrics.
func (NopMetrics) TestRestoreFinished(sqlc.TestRestoreStatus) {}

// observeFinished reports a committed terminal restore whose mode the
// caller does not hold: the mode is read back (best effort; a failed read
// is counted under mode "unknown").
func (s *Service) observeFinished(ctx context.Context, id uuid.UUID, status Status) {
	if _, nop := s.o.Metrics.(NopMetrics); nop {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	mode := sqlc.RestoreMode("unknown")
	if r, err := sqlc.New(s.o.DB).GetRestore(rctx, id); err == nil {
		mode = r.Mode
	}
	s.o.Metrics.RestoreFinished(mode, status)
}
