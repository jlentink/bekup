package app

import (
	"context"
	"errors"
	"net"

	"github.com/jlentink/bekup/internal/monitor"
)

// newMetrics builds the metrics registry and event counters (task 5.6,
// D-176) right after the database is open: the restore service, the
// workers and the scheduler get it as their metric hook.
func (a *App) newMetrics() {
	a.metrics = monitor.New(monitor.Options{Version: a.opts.Version, DB: a.pool})
}

// newHealth builds the health snapshot behind Query.systemHealth and the
// /metrics gauges (serve only: a worker would duplicate the gauges). It
// needs the runner client.
func (a *App) newHealth() error {
	if a.metrics == nil || a.runner == nil {
		return errors.New("app: the health snapshot needs the metrics and the runner client")
	}
	cfg := a.opts.Config
	h, err := monitor.NewHealth(monitor.HealthOptions{
		DB:     a.pool,
		Runner: a.runner,
		Certs: []monitor.CertFile{
			{Component: monitor.CertRunnerClient, Path: cfg.Runner.CertFile},
			{Component: monitor.CertRunnerCA, Path: cfg.Runner.CAFile},
			{Component: monitor.CertPostgresCA, Path: cfg.Database.TLSCAFile},
			{Component: monitor.CertPostgresClient, Path: cfg.Database.TLSClientCertFile},
		},
		Metrics:          a.metrics,
		SchedulerEnabled: cfg.Scheduler.Enabled,
		PerJobLabels:     cfg.Metrics.PerJobLabels,
		Logger:           a.opts.Logger,
	})
	if err != nil {
		return err
	}
	if err := a.metrics.Registry().Register(h.Collector()); err != nil {
		return err
	}
	a.health = h
	return nil
}

// newMetricsServer adds the internal /metrics server when metrics.enabled
// (bound now, so a taken port or a bad bearer file refuses startup).
func (a *App) newMetricsServer(ctx context.Context) error {
	cfg := a.opts.Config.Metrics
	if !cfg.Enabled {
		a.opts.Logger.InfoContext(ctx, "metrics endpoint disabled (metrics.enabled = false)")
		return nil
	}
	s, err := monitor.NewServer(ctx, monitor.ServerOptions{
		Listen: cfg.Listen, Listener: a.opts.MetricsListener, Gatherer: a.metrics.Registry(),
		BearerFile: cfg.AuthBearerFile, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.metricsAddr = s.Addr()
	a.components = append(a.components, metricsComponent{s})
	return nil
}

// MetricsAddr is the bound metrics address (nil when disabled).
func (a *App) MetricsAddr() net.Addr { return a.metricsAddr }

// metricsComponent adapts *monitor.Server to Component (close is
// unexported per package).
type metricsComponent struct{ s *monitor.Server }

func (c metricsComponent) Name() string                  { return c.s.Name() }
func (c metricsComponent) Run(ctx context.Context) error { return c.s.Run(ctx) }
func (c metricsComponent) close() error                  { return c.s.Close() }
