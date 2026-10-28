// Package queue sets up River, bekup's Postgres-backed job queue (task 3.4,
// D-043, D-145).
//
//   - Queues: backup, restore, retention, notify and maintenance, each with a
//     per-process concurrency from [workers] and a per-queue attempt
//     timeout and default MaxAttempts (backup 2, restore 1, notify 10).
//   - Kinds: every job kind is declared in the static catalog (kinds.go)
//     with its queue, attempts and uniqueness, so insert-only processes
//     (serve --no-worker) apply exactly the same insert options as workers.
//     Unique kinds allow one job per unique args key in the states of
//     UniqueStates (available, pending, scheduled, running, retryable; not
//     completed), so runs of one backup job never overlap.
//   - Args carry IDs only (uuid, integers, time, bool and named enum
//     strings); CheckArgs rejects plain strings, byte slices, maps,
//     interfaces and secret-looking field names, and New refuses a catalog
//     that fails it.
//   - Workers register through Register, which binds River's per-attempt
//     Timeout to the queue's configured timeout. RescueStuckJobsAfter is
//     the longest queue timeout plus a margin, so a job is never rescued
//     while it may still run.
//   - Client.Run starts the workers and shuts down gracefully: stop
//     fetching, let running jobs finish for workers.shutdown_timeout, then
//     cancel them and wait a bounded time.
//
// The client runs on the app pool as bekup_app. River's schema is part of
// the goose migrations (D-042); its reindexer is disabled because bekup_app
// does not own the indexes.
package queue
