package app

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/scheduler"
)

// poolHeadroom is the number of connections River's notifier, producers and
// serve's own traffic need beside the queue workers.
const poolHeadroom = 4

// newQueue builds the River client (task 3.4, D-145). With workers it is a
// component that executes jobs; without (serve --no-worker) it only
// inserts, so the scheduler and later runNow keep working.
func (a *App) newQueue(withWorkers bool) error {
	settings := queue.SettingsFromConfig(a.opts.Config.Workers)
	opts := queue.Options{Settings: settings, Logger: a.opts.Logger, FetchPollInterval: a.opts.QueueFetchPollInterval}
	if withWorkers {
		ws, err := queue.NewWorkers(settings)
		if err != nil {
			return err
		}
		if err := a.registerWorkers(ws); err != nil {
			return fmt.Errorf("register workers: %w", err)
		}
		opts.Workers = ws
	}
	if withWorkers {
		total := 0
		for _, q := range settings.Queues {
			total += q.MaxWorkers
		}
		if need := total + poolHeadroom; a.opts.Config.Database.MaxConns < need {
			a.opts.Logger.Warn("database.max_conns is smaller than the queue workers plus headroom; jobs will wait for connections",
				slog.Int("max_conns", a.opts.Config.Database.MaxConns), slog.Int("suggested", need))
		}
	}
	c, err := queue.New(a.pool, opts)
	if err != nil {
		return err
	}
	a.queue = c
	if withWorkers {
		a.components = append(a.components, c)
	}
	return nil
}

// registerWorkers registers every River worker. Each later task adds its
// worker here (with its kind in internal/queue/kinds.go): backup runs
// (3.6), retention (3.8), restores (4.x), notifications (5.x).
func (a *App) registerWorkers(ws *queue.Workers) error {
	if err := scheduler.RegisterWorkers(ws, a.pool); err != nil {
		return err
	}
	bw, err := a.newBackupWorker()
	if err != nil {
		return err
	}
	if err := queue.Register(ws, bw); err != nil {
		return err
	}
	rw, err := a.newRetentionWorker()
	if err != nil {
		return err
	}
	return retention.Register(ws, rw)
}

// queueInserter resolves the queue client late: workers are built before
// the client that runs them.
func (a *App) queueInserter() retention.Inserter {
	if a.queue == nil {
		return nil
	}
	return a.queue
}

// newRetentionWorker builds the retention worker (task 3.8, D-148).
func (a *App) newRetentionWorker() (*retention.Worker, error) {
	if a.destinations == nil {
		return nil, errors.New("app: the retention worker needs the destination service")
	}
	cfg := a.opts.Config.Retention
	if cfg.DryRun {
		a.opts.Logger.Warn("retention.dry_run is set: retention plans are only logged, nothing is deleted")
	}
	return retention.New(retention.Options{
		DB: a.pool, Auditor: a.auditor, Destinations: a.destinations, Queue: a.queueInserter,
		Publisher: a.publisher(), DryRun: cfg.DryRun, MaxDeletions: cfg.MaxDeletionsPerSweep,
		PartialCleanupAge: cfg.PartialCleanupAge.D(), Logger: a.opts.Logger,
	})
}

// newScheduler adds the scheduler loop when scheduler.enabled. It is safe on
// every serve instance (FOR UPDATE SKIP LOCKED + River unique inserts), so
// it does not depend on whether this instance runs workers.
func (a *App) newScheduler() error {
	cfg := a.opts.Config.Scheduler
	if !cfg.Enabled {
		a.opts.Logger.Info("scheduler disabled on this instance (scheduler.enabled = false)")
		return nil
	}
	l, err := scheduler.NewLoop(scheduler.LoopOptions{
		DB:        a.pool,
		Queue:     a.queue,
		Logger:    a.opts.Logger,
		Interval:  cfg.TickInterval.D(),
		BatchSize: cfg.BatchSize,
		Publisher: a.publisher(),
	})
	if err != nil {
		return err
	}
	a.components = append(a.components, l)
	return nil
}
