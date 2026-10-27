package runner

import (
	"context"
	"math"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// service implements runner.v1.RunnerService: Health here, every other RPC
// through operations (ops*.go).
type service struct {
	runnerpb.UnimplementedRunnerServiceServer

	version string
	docker  DockerStatusFunc
	limits  *limiter
	certEnd time.Time
	// ops serves every RPC except Health (ops*.go).
	ops *operations
}

func newService(version string, docker DockerStatusFunc, limits *limiter, certEnd time.Time) *service {
	return &service{version: version, docker: docker, limits: limits, certEnd: certEnd}
}

// Health reports the runner's version and state and echoes the caller's
// verified identity.
func (s *service) Health(ctx context.Context, _ *runnerpb.HealthRequest) (*runnerpb.HealthResponse, error) {
	docker := runnerpb.DockerStatus_DOCKER_STATUS_UNKNOWN
	if s.docker != nil {
		docker = s.docker(ctx)
	}
	return &runnerpb.HealthResponse{
		Version:             s.version,
		ApiVersion:          runnerpb.APIVersion,
		CallerIdentity:      CallerFromContext(ctx),
		DockerStatus:        docker,
		ActiveRuns:          clampUint32(s.limits.activeRuns()),
		MaxConcurrentRuns:   clampUint32(s.limits.maxRuns()),
		CertificateNotAfter: timestamppb.New(s.certEnd),
	}, nil
}

func clampUint32(n int) uint32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxUint32:
		return math.MaxUint32
	default:
		return uint32(n)
	}
}
