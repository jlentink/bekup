// Package retention applies each job's retention policy (keep the last N
// complete runs and/or complete runs younger than N days) per destination
// (task 3.8, D-148).
//
// A retention_sweep River job (retention queue, unique per job) runs after
// every successful or partial backup run (enqueued by the runstate finish
// hook in the run's transaction, PostRunHook) and daily: the periodic
// fan-out (no job id) enqueues a sweep for every job with present
// artifacts, soft-deleted jobs included, and removes leftover temporary
// upload files on disk and SFTP destinations.
//
// Plan is the pure policy. Per destination, a run is complete when every
// item has a present, verified artifact there plus its manifest. Kept are:
// the newest complete run (always), the newest N complete runs, complete
// runs younger than N days, runs with an artifact under an Object Lock and
// incomplete runs newer than the newest complete one. A destination
// without a complete run deletes nothing.
//
// Safety: the plan is checked against its invariants; before the first
// delete the newest complete run's objects must still exist with their
// recorded sizes (otherwise the destination is skipped, audited and
// alerted); no-delete destinations are skipped (lifecycle rules handle
// them); every delete is preceded by a Stat, so an object that vanished is
// marked missing, audited and alerted (unexpected deletion) instead of
// silently "deleted"; Object Lock refusals keep the artifact; at most
// retention.max_deletions_per_sweep objects are deleted per job sweep;
// retention.dry_run (or the args' DryRun) only logs the plan. Artifact
// rows are never deleted: runstate moves them to deleted or missing. Each
// delete runs under runstate.LockForDeletionTx: an artifact a download (or
// a restore) holds is skipped until a later sweep, and no hold can be taken
// while it is being deleted (D-155).
//
// Visibility (D-209): every sweep records per destination the incomplete
// runs kept only by the newer-incomplete or no-complete rule (Plan's
// Hold) in retention_holds and emits retention.blocked (once per job,
// destination and day). A changed retention policy enqueues a sweep of the
// job (reason policy_changed). The daily fan-out first fails runs no worker
// will finish (RunReaper), then, per destination, removes leftover
// temporary files and runs the unrecorded-file scan with empty-directory
// pruning (UnrecordedScanner). EstimateKept is the editor's preview of
// how many backups a policy keeps for a schedule, with the same keep
// rules.
package retention
