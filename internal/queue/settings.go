package queue

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/config"
)

// Queue is a River queue name.
type Queue string

// The queues. Every kind in the catalog belongs to one of them.
const (
	QueueBackup      Queue = "backup"
	QueueRestore     Queue = "restore"
	QueueRetention   Queue = "retention"
	QueueNotify      Queue = "notify"
	QueueMaintenance Queue = "maintenance"
)

// Queues lists every queue.
func Queues() []Queue {
	return []Queue{QueueBackup, QueueRestore, QueueRetention, QueueNotify, QueueMaintenance}
}

// Default attempts per queue (D-043): a failed backup is retried once, a
// restore never automatically, a notification up to ten times.
const (
	MaxAttemptsBackup      = 2
	MaxAttemptsRestore     = 1
	MaxAttemptsRetention   = 3
	MaxAttemptsNotify      = 10
	MaxAttemptsMaintenance = 3
)

// Fixed durations (D-145).
const (
	// MaintenanceTimeout bounds one housekeeping job.
	MaintenanceTimeout = 15 * time.Minute
	// RescueMargin is added to the longest queue timeout to get
	// RescueStuckJobsAfter.
	RescueMargin = time.Hour
	// HardStopTimeout bounds the wait for canceled jobs after the soft stop
	// timed out.
	HardStopTimeout = 30 * time.Second
	// CompletedJobRetention, CanceledJobRetention and
	// DiscardedJobRetention: how long River keeps finished rows. River's
	// table is not the run history (the runs table of task 3.6 is), so
	// completed rows go after a day; failures stay a week for diagnosis.
	CompletedJobRetention = 24 * time.Hour
	CanceledJobRetention  = 7 * 24 * time.Hour
	DiscardedJobRetention = 7 * 24 * time.Hour
)

// UniqueStates are the states in which a unique job blocks another with the
// same unique key (D-043): every active state, never completed, canceled
// or discarded. River requires pending in any custom set, so it is listed
// although bekup never creates pending jobs.
func UniqueStates() []rivertype.JobState {
	return []rivertype.JobState{
		rivertype.JobStateAvailable,
		rivertype.JobStatePending,
		rivertype.JobStateRetryable,
		rivertype.JobStateRunning,
		rivertype.JobStateScheduled,
	}
}

// PerQueue configure one queue.
type PerQueue struct {
	// MaxWorkers is the per-process concurrency.
	MaxWorkers int
	// Timeout bounds one attempt of any job in the queue (River's
	// per-worker Timeout). For backups it is the cap of the per-job
	// timeout, which the worker enforces inside the attempt.
	Timeout time.Duration
	// MaxAttempts is the default of the queue's kinds.
	MaxAttempts int
}

// Settings are the River settings derived from [workers].
type Settings struct {
	Queues map[Queue]PerQueue
	// RescueStuckJobsAfter is the longest queue timeout + RescueMargin.
	RescueStuckJobsAfter time.Duration
	// ShutdownTimeout is the soft-stop wait (workers.shutdown_timeout).
	ShutdownTimeout time.Duration
	// HardStopTimeout bounds the wait after canceling running jobs.
	HardStopTimeout time.Duration
}

// SettingsFromConfig derives the settings from a validated config.
func SettingsFromConfig(w config.Workers) Settings {
	s := Settings{
		Queues: map[Queue]PerQueue{
			QueueBackup:      {MaxWorkers: w.BackupQueueWorkers, Timeout: w.BackupTimeout.D(), MaxAttempts: MaxAttemptsBackup},
			QueueRestore:     {MaxWorkers: w.RestoreQueueWorkers, Timeout: w.RestoreTimeout.D(), MaxAttempts: MaxAttemptsRestore},
			QueueRetention:   {MaxWorkers: w.RetentionQueueWorkers, Timeout: w.RetentionTimeout.D(), MaxAttempts: MaxAttemptsRetention},
			QueueNotify:      {MaxWorkers: w.NotifyQueueWorkers, Timeout: w.NotifyTimeout.D(), MaxAttempts: MaxAttemptsNotify},
			QueueMaintenance: {MaxWorkers: w.MaintenanceQueueWorkers, Timeout: MaintenanceTimeout, MaxAttempts: MaxAttemptsMaintenance},
		},
		ShutdownTimeout: w.ShutdownTimeout.D(),
		HardStopTimeout: HardStopTimeout,
	}
	s.RescueStuckJobsAfter = s.LongestTimeout() + RescueMargin
	return s
}

// LongestTimeout is the largest queue timeout.
func (s Settings) LongestTimeout() time.Duration {
	var longest time.Duration
	for _, q := range s.Queues {
		longest = max(longest, q.Timeout)
	}
	return longest
}

// validate checks the invariants River and D-043 rely on.
func (s Settings) validate() error {
	for _, q := range Queues() {
		qs, ok := s.Queues[q]
		if !ok {
			return fmt.Errorf("queue %s is not configured", q)
		}
		if qs.MaxWorkers < 1 || qs.MaxWorkers > 10_000 {
			return fmt.Errorf("queue %s: max workers %d out of range", q, qs.MaxWorkers)
		}
		if qs.Timeout <= 0 {
			return fmt.Errorf("queue %s: timeout must be positive", q)
		}
		if qs.MaxAttempts < 1 {
			return fmt.Errorf("queue %s: max attempts must be positive", q)
		}
	}
	if s.Queues[QueueRestore].MaxAttempts != MaxAttemptsRestore {
		return fmt.Errorf("queue %s: restores are never retried (max attempts must be %d)", QueueRestore, MaxAttemptsRestore)
	}
	if len(s.Queues) != len(Queues()) {
		return errors.New("unknown queue configured")
	}
	if s.RescueStuckJobsAfter <= s.LongestTimeout() {
		return fmt.Errorf("RescueStuckJobsAfter %s must exceed the longest queue timeout %s", s.RescueStuckJobsAfter, s.LongestTimeout())
	}
	if s.ShutdownTimeout <= 0 || s.HardStopTimeout <= 0 {
		return errors.New("shutdown timeouts must be positive")
	}
	return nil
}

func knownQueue(q Queue) bool { return slices.Contains(Queues(), q) }
