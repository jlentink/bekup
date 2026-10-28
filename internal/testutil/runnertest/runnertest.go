// Package runnertest holds helpers for integration tests that run a real
// runner. The runner logs "runner listening" as soon as its gRPC listener
// is up, but its executor detects its own container (and the Docker
// daemon) asynchronously; RPCs that start dump/restore containers before
// that fail. WaitDockerOK closes that race.
package runnertest

import (
	"context"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Healther is the part of runnerclient.Client the helpers need.
type Healther interface {
	Health(ctx context.Context) (*runnerpb.HealthResponse, error)
}

// DefaultReadyTimeout is how long WaitDockerOK waits by default.
const DefaultReadyTimeout = 90 * time.Second

// WaitDockerOK polls the runner's Health RPC until it reports
// DOCKER_STATUS_OK, and fails the test after timeout (0:
// DefaultReadyTimeout). logs, when set, is appended to the failure.
func WaitDockerOK(t testing.TB, h Healther, timeout time.Duration, logs func() string) {
	t.Helper()
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		hctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		hr, err := h.Health(hctx)
		cancel()
		if err == nil && hr.GetDockerStatus() == runnerpb.DockerStatus_DOCKER_STATUS_OK {
			return
		}
		if time.Now().After(deadline) || t.Context().Err() != nil {
			extra := ""
			if logs != nil {
				extra = logs()
			}
			t.Fatalf("runner executor not ready: status %v, err %v\n%s", hr.GetDockerStatus(), err, extra)
		}
		select {
		case <-t.Context().Done():
		case <-time.After(time.Second):
		}
	}
}
