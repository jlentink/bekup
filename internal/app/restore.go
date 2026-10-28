package app

import (
	"context"
	"errors"
	"time"

	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runstate"
)

// newRestore builds the restore staging service (task 4.1, D-162) on the
// destination and job key services and storage.scratch_dir. The restore
// worker (newRestoreWorker) and restoreArtifact (task 4.5) build on it.
func (a *App) newRestore() error {
	cfg := a.opts.Config
	svc, err := restore.New(restore.Options{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Destinations: a.destinations, Keys: a.jobKeys,
		ScratchDir: cfg.Storage.ScratchDir, ScratchReserve: cfg.Storage.ScratchReserveBytes,
		StaleAfter: cfg.Storage.ScratchStaleAfter.D(), Publisher: a.publisher(), Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.restore = svc
	return nil
}

// newRestoreWorker builds the restore worker (task 4.2, D-166): it shares
// the run slots with backups and talks to the runner for the target.
func (a *App) newRestoreWorker() (*restore.Worker, error) {
	if a.restore == nil || a.connections == nil || a.runner == nil {
		return nil, errors.New("app: the restore worker needs the restore and connection services and the runner")
	}
	w := a.opts.Config.Workers
	return restore.NewWorker(restore.WorkerOptions{
		Service: a.restore, Slots: runstate.NewSlots(a.pool), Targets: a.connections, Runner: a.runner.RPC(),
		Limits:      runstate.SlotLimits{Global: w.MaxConcurrentRuns, PerConnection: w.MaxRunsPerConnection},
		LogMaxLines: w.RunLogMaxLines, LogMaxLineBytes: w.RunLogMaxLineBytes,
		Logger: a.opts.Logger,
	})
}

// restoreHousekeepingComponent sweeps leftover staging directories (at
// startup and every 10 minutes) and deletes expired identity handoffs.
type restoreHousekeepingComponent struct{ svc *restore.Service }

func (c restoreHousekeepingComponent) Name() string { return "restore housekeeping" }

func (c restoreHousekeepingComponent) Run(ctx context.Context) error {
	return c.svc.RunHousekeeping(ctx)
}

// newRestoreAPI builds the restore API behind GraphQL (task 4.5, D-167):
// after the policy, the connection service and the queue client.
func (a *App) newRestoreAPI() error {
	if a.restore == nil || a.connections == nil || a.policy == nil {
		return errors.New("app: the restore API needs the restore and connection services and the policy")
	}
	api, err := restore.NewAPI(restore.APIOptions{
		Service: a.restore, Policy: a.policy, Connections: a.connections, Owners: jobs.Ownership{DB: a.pool},
		Queue: func() restore.Inserter {
			if a.queue == nil {
				return nil
			}
			return a.queue
		},
		Canceler: func() restore.Canceler {
			if a.queue == nil {
				return nil
			}
			return a.queue
		},
		RestoreTimeout: a.opts.Config.Workers.RestoreTimeout.D(),
		Logger:         a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.restoreAPI = api
	return nil
}

// newTestRestoreAPI builds the test-restore API behind GraphQL (task 4.6).
func (a *App) newTestRestoreAPI() error {
	if a.restore == nil || a.policy == nil {
		return errors.New("app: the test-restore API needs the restore service and the policy")
	}
	api, err := restore.NewTestRestoreAPI(restore.TestRestoreAPIOptions{
		Service: a.restore, Policy: a.policy,
		Queue: func() restore.Inserter {
			if a.queue == nil {
				return nil
			}
			return a.queue
		},
		Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.testRestoreAPI = api
	return nil
}

// registerTestRestores registers the test-restore worker and the schedule
// worker (task 4.6) on top of the restore worker.
func (a *App) registerTestRestores(ws *queue.Workers, rsw *restore.Worker) error {
	perTable, budget := statsLimits()
	trw, err := restore.NewTestRestoreWorker(restore.TestRestoreWorkerOptions{
		Restores: rsw, Notifier: a.testRestoreNotifier(), PerTableTimeout: perTable, Budget: budget,
		Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	if err := restore.RegisterTestRestore(ws, trw); err != nil {
		return err
	}
	return restore.RegisterTestSchedule(ws, &restore.TestScheduleWorker{
		Service: a.restore, Restores: rsw, RestoreTimeout: a.opts.Config.Workers.RestoreTimeout.D(),
		Queue: func() restore.Inserter {
			if a.queue == nil {
				return nil
			}
			return a.queue
		},
	})
}

// testRestoreNotifier is the D-161 emit hook of test restores: a no-op
// until task 5.3 delivers notifications (then it is the serve notifier).
func (a *App) testRestoreNotifier() restore.TestNotifier { return restore.NoTestNotify{} }

// statsLimits are the per-table timeout and the budget of completeness
// statistics (D-151): the runner's defaults (0) for the backup statistics
// and the test-restore scratch statistics alike, so both sides of a
// comparison are read under the same limits. There is no configuration
// key yet; one would be read here for both.
func statsLimits() (perTable, budget time.Duration) { return 0, 0 }
