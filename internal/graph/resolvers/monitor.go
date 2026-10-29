package resolvers

import (
	"context"
	"errors"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/monitor"
)

// SystemHealthSource backs Query.systemHealth (*monitor.Health).
type SystemHealthSource interface {
	Snapshot(ctx context.Context) monitor.Snapshot
}

var errSystemHealthNotConfigured = errors.New("resolvers: system health not configured")

func (r *Resolver) systemHealth(ctx context.Context) (*model.SystemHealth, error) {
	if r.SystemHealth == nil {
		return nil, errSystemHealthNotConfigured
	}
	if err := r.Authz.Authorize(ctx, authz.OpSystemHealthView); err != nil {
		return nil, err
	}
	return systemHealthModel(r.SystemHealth.Snapshot(ctx)), nil
}

func systemHealthModel(s monitor.Snapshot) *model.SystemHealth {
	out := &model.SystemHealth{
		CheckedAt:           s.CheckedAt,
		Runner:              &model.RunnerHealth{Up: s.Runner.Up, DockerOk: s.Runner.Up && s.Runner.DockerOK},
		Certificates:        make([]model.CertificateExpiry, 0, len(s.Certificates)),
		EarliestPinNotAfter: s.EarliestPinNotAfter,
		OldestLastSuccess:   s.OldestLastSuccess,
		SchedulerEnabled:    s.SchedulerEnabled,
		LastSchedulerTick:   s.LastSchedulerTick,
	}
	if s.Runner.Up {
		v, active, maxRuns := s.Runner.Version, s.Runner.ActiveRuns, s.Runner.MaxConcurrentRuns
		out.Runner.Version, out.Runner.ActiveRuns, out.Runner.MaxConcurrentRuns = &v, &active, &maxRuns
	}
	for _, c := range s.Certificates {
		out.Certificates = append(out.Certificates, model.CertificateExpiry{Component: c.Component, NotAfter: c.NotAfter})
	}
	if s.QueueOK {
		out.Queue = make([]model.QueueDepth, 0, len(s.Queue))
		for _, q := range s.Queue {
			out.Queue = append(out.Queue, model.QueueDepth{Queue: q.Queue, State: q.State, Jobs: int(min(q.Jobs, 1<<31-1))})
		}
	}
	return out
}
