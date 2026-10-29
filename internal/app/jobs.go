package app

import (
	"errors"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
)

// newPolicy builds the central authz policy (task 1.4) with the
// DB-backed job ownership (task 3.5) and the enrolled second factors
// (tasks 1.7/1.8). The GraphQL layer and the services that re-check their
// caller share it.
func (a *App) newPolicy() *authz.Policy {
	return authz.New(authz.Options{
		StepUpWindow:  a.opts.Config.Security.StepUpMaxAge.D(),
		SecondFactors: a.secondFactors(),
		Ownership:     jobs.Ownership{DB: a.pool},
	})
}

// newJobKeys builds the job encryption key service (task 3.2, D-142). It
// comes before the queue: the backup worker reads the job keys.
func (a *App) newJobKeys() error {
	keys, err := jobkeys.New(jobkeys.Options{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Authorizer: a.policy, Logger: a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.jobKeys = keys
	return nil
}

// newJobs builds the job service (task 3.5, D-146). It needs the queue
// (runJobNow, cancelRun) and enforces scheduler.min_interval and
// workers.backup_timeout.
func (a *App) newJobs() error {
	cfg := a.opts.Config
	svc, err := jobs.New(jobs.Options{
		DB: a.pool, Auditor: a.auditor, Authorizer: a.policy, Keys: a.jobKeys, Queue: a.queue,
		Canceler: a.queue, Publisher: a.publisher(),
		Limits:     scheduler.Limits{MinInterval: cfg.Scheduler.MinInterval.D()},
		MaxTimeout: cfg.Workers.BackupTimeout.D(),
		// D-170: designating a scratch target probes its role.
		TestTargets: a.connections,
		Logger:      a.opts.Logger,
	})
	if err != nil {
		return err
	}
	a.jobs = svc
	return nil
}

// publisher sends run events: pg_notify plus, in serve, the local bus.
func (a *App) publisher() *pubsub.Publisher { return &pubsub.Publisher{Bus: a.subs.bus} }

// newBackupWorker builds the backup run worker (task 3.6, D-147) on the
// connection, destination and job key services.
func (a *App) newBackupWorker() (*jobs.BackupWorker, error) {
	w := a.opts.Config.Workers
	if a.connections == nil || a.destinations == nil || a.jobKeys == nil || a.runner == nil {
		return nil, errors.New("app: the backup worker needs the connection, destination and job key services")
	}
	return jobs.NewBackupWorker(jobs.BackupWorkerOptions{
		// A finished run (success/partial) enqueues its job's retention
		// sweep in the same transaction (task 3.8).
		Store: runstate.NewStore(a.pool, a.publisher(), nil).
			WithFinishHook(retention.PostRunHook(a.queueInserter), a.opts.Logger).
			WithObserver(a.metrics),
		Slots:        runstate.NewSlots(a.pool),
		Targets:      a.connections,
		Runner:       a.runner.RPC(),
		Destinations: a.destinations,
		Keys:         a.jobKeys,
		Signer:       jobs.InstanceKeySigner{DB: a.pool, Keyring: a.keyring, Auditor: a.auditor},
		Stats:        a.runnerStats(),
		Limits:       runstate.SlotLimits{Global: w.MaxConcurrentRuns, PerConnection: w.MaxRunsPerConnection},
		QueueTimeout: w.BackupTimeout.D(),
		LogMaxLines:  w.RunLogMaxLines, LogMaxLineBytes: w.RunLogMaxLineBytes,
		BekupVersion: a.opts.Version,
		Logger:       a.opts.Logger,
	})
}

// runnerStats is the backup worker's statistics collector (D-151) with the
// limits shared with the test-restore scratch statistics (statsLimits).
func (a *App) runnerStats() jobs.RunnerStats {
	perTable, budget := statsLimits()
	return jobs.RunnerStats{Store: sqlc.New(a.pool), PerTableTimeout: perTable, Budget: budget, Logger: a.opts.Logger}
}
