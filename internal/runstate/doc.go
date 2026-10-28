// Package runstate is the only writer of the backup run tables (task 3.6,
// D-147): runs, run_items, artifacts, run_logs and run_slots.
//
// Every status change goes through Store (or EnqueueTx / RequestCancelTx in
// the caller's transaction) and publishes a run event on the
// bekup_run_events channel in the same transaction (pubsub.RunEvents,
// identifiers only: "run:<run id>:<job id>" or "log:<run id>:<job id>"), so
// the live views of task 3.7 only subscribe and re-load what changed with
// the caller's rights.
//
//   - EnqueueTx inserts the backup_run River job and its queued run row
//     atomically (scheduler and runJobNow).
//   - Store.Begin picks up the queued run of a River job (or creates one
//     for a retry), Start / Finish move it through running to exactly one
//     terminal status; items, artifacts and the manifest signing key are
//     recorded as the run proceeds.
//   - Slots is the global and per-connection run semaphore: one row per
//     running backup or restore under a transaction-scoped advisory lock
//     (never a session lock on a pooled connection), kept alive by
//     heartbeats and ignored once stale.
//   - LogWriter batches run-log lines (every FlushInterval or BatchSize
//     lines), redacts them (Scrub), sanitizes control characters and
//     enforces the per-run line count and line length caps.
//
// Reader (task 3.7, D-149) is the read side for the GraphQL API: scoped
// run and artifact pages, batch loads per page, run logs. It never writes
// and never authorizes; callers pass authz.JobScope and authorize single
// runs with the job stored on the row.
//
// Error and warning texts stored here are fixed, sanitized messages chosen
// by the worker; never put server output, connection strings or secrets in
// them.
package runstate
