// Package jobs manages backup jobs (task 3.5, D-146) and runs them (task
// 3.6, D-147: BackupWorker, CancelRun).
//
// A job names the connection it backs up, the database selection (all
// databases except excludes, or a list; plus the PostgreSQL globals), its
// schedule (internal/scheduler, D-143), retention, timeout, verification
// level, missed-backup window, destinations (fan-out order) and backup
// owners. Its encryption keys live in internal/jobkeys: Create calls
// jobkeys.InitTx in the job's transaction, so every job starts encrypted.
//
// The Service is the only writer of jobs, job_destinations and job_owners.
// Every mutation re-checks the caller with the central policy (the GraphQL
// layer checks first) and requires the actor to be that caller; every
// change is audited in its transaction. next_run_at is computed on create,
// on a changed schedule and on enable; disable and delete clear it. Delete
// is soft (a running run finishes; the worker re-checks the job). RunNow
// enqueues a backup_run River job with the job id only; River's unique key
// refuses a second active run (ErrRunActive).
//
// Ownership implements authz.Ownership with real queries on job_owners and
// jobs: owner grants (view, run now, downloads, restores of the job) apply
// to role user only (D-067). Connections and destinations used by a
// non-deleted job cannot be deleted (their services query the same tables).
//
// BackupWorker works the backup_run River kind: it skips deleted or
// disabled jobs, waits for a run slot (runstate.Slots, snoozing the River
// job), then dumps every selected database (and the PostgreSQL globals)
// through the runner, streams each dump through pipeline.Run to the job's
// destinations while the raw stream is verified (a streamed VerifyDump for
// PostgreSQL archives, the dump's trailer check otherwise), records items
// and artifacts, writes the signed manifest to every destination holding
// all complete items and sets the terminal status (success, partial,
// failed, canceled, skipped). All run state goes through internal/runstate;
// errors are fixed texts per code; the connection's credentials and the job
// passphrase are registered for redaction for the run's duration.
// CancelRun marks a run and cancels its River job (staff only).
package jobs
