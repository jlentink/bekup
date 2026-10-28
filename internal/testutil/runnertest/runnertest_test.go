package runnertest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/runnerpb"
)

type fakeHealth struct {
	calls  int
	okFrom int
}

func (f *fakeHealth) Health(context.Context) (*runnerpb.HealthResponse, error) {
	f.calls++
	if f.calls < f.okFrom {
		if f.calls == 1 {
			return nil, errors.New("unavailable")
		}
		return &runnerpb.HealthResponse{DockerStatus: runnerpb.DockerStatus_DOCKER_STATUS_UNKNOWN}, nil
	}
	return &runnerpb.HealthResponse{DockerStatus: runnerpb.DockerStatus_DOCKER_STATUS_OK}, nil
}

func TestWaitDockerOK(t *testing.T) {
	f := &fakeHealth{okFrom: 3}
	WaitDockerOK(t, f, 10*time.Second, nil)
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3", f.calls)
	}
}
