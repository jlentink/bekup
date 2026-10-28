# Retention

Every job has a retention policy: **keep the last N backups**, **keep
backups younger than X days**, or both (a backup is kept when either rule
keeps it). bekup applies it **per destination**, after every successful or
partial run and once a day. Design: [D-148](../DECISIONS.md).

## When retention runs

- **After a run.** A run that ends `success` or `partial` queues a
  retention sweep of its job in the same database transaction that records
  the result.
- **Daily.** Once a day (and when the worker leader starts) bekup queues a
  sweep for every job that still has backups, **including deleted jobs**,
  and removes leftover temporary upload files (`.<random>.partial`) older
  than `retention.partial_cleanup_age` (default 24 h) on disk and SFTP
  destinations.

Sweeps run in the `retention` queue (`workers.retention_queue_workers`,
`workers.retention_timeout`). There is at most one queued or running sweep
per job, so two sweeps of a job never overlap.

## What "a backup" means on a destination

A job's run writes one object per database (plus the PostgreSQL globals)
and a signed `manifest.json` to each destination. On a given destination a
run is **complete** when every one of its databases has a verified object
there and its manifest is there too.

Completeness is judged per destination: if one destination failed during a
run (the run is `partial`), that run is complete on the other destinations
but not on the failed one. A run in which a database failed is complete
nowhere.

## The policy, per destination

For each destination of the job, bekup keeps:

1. the **newest complete run**, always, regardless of age or policy;
2. the newest **N** complete runs (keep last N);
3. complete runs **younger than X days** (keep days);
4. every run with an object under an **Object Lock** whose recorded
   retain-until date is in the future;
5. incomplete runs **newer** than the newest complete run (they may hold
   the only copy of a database).

Everything else is deleted, including the leftovers of incomplete runs
older than the newest complete one. A destination that holds **no**
complete run is left alone entirely: retention never deletes there.

Examples (keep last 2, nightly backups):

| Destination | Runs (newest first) | Deleted |
|---|---|---|
| Disk | Mon ✔, Sun ✔, Sat ✔, Fri ✔ | Sat, Fri |
| S3 (missed Mon) | Mon (partial: no manifest), Sun ✔, Sat ✔, Fri ✔ | Fri (Mon is kept: newer than the newest complete run) |

## Safety rules

- **Newest complete backup check.** Before deleting anything on a
  destination, bekup confirms that every object of the newest complete run
  still exists there with its recorded size. If one is missing it is
  marked `missing`, nothing is deleted on that destination, and the event
  is audited (`retention.artifact_missing`, `retention.aborted`) and
  alerted. If the check cannot be completed (network error), the
  destination is skipped for this sweep.
- **Deletion cap.** One sweep of a job deletes at most
  `retention.max_deletions_per_sweep` objects (default 500). The rest waits
  for the next sweep. This limits the damage of a wrong policy or a bug.
- **Unexpected deletions.** bekup checks each object before deleting it.
  An object that is already gone was removed outside bekup: its artifact is
  marked `missing` (not `deleted`), audited and alerted.
- **Backups in use.** A backup that is being downloaded (and, later,
  restored) is held: retention skips it, counts it as `held` in the sweep
  summary (`retention.swept`) and deletes it on a later sweep once the hold
  is released. A hold cannot start while bekup is deleting the object.
- **Order.** Per run, the database objects go first; the manifest goes
  only after all of them are gone from that destination.
- **Rows stay.** Artifact rows are never removed from the database: a
  deleted backup keeps its history with state `deleted` and the deletion
  time.

## Object Lock and no-delete mode

- **Object Lock.** Runs whose recorded lock has not expired are kept by
  the policy. Objects locked without bekup knowing it (for example by a
  bucket default retention) are still protected: the S3 service refuses
  the delete, the artifact stays `present`, and the sweep summary records
  how many objects were locked and until when. They are deleted by a later
  sweep once the lock has expired. bekup never sends the
  governance-bypass header. See [Immutable backups](../security/immutable-backups.md).
- **No-delete mode.** Retention skips a no-delete destination completely
  (nothing is contacted, artifacts stay `present`); bucket lifecycle rules
  expire the objects. Pick a lifecycle expiry comfortably above your
  backup interval, because lifecycle rules do not know the
  "always keep the newest complete backup" rule.

## Deleted jobs

Deleting a job does not delete its backups. They remain restorable and
age out by the job's policy; the newest complete backup of a deleted job is
kept until it is removed explicitly.

## Dry run

Set `retention.dry_run = true` to make every sweep only compute and log its
plan (kept runs with the reason, runs that would be deleted, object counts)
without deleting anything or contacting the destinations. The daily partial
cleanup is skipped too. bekup logs a warning at startup while dry run is
on. Use it to check a new policy before enabling it.

## Audit and logs

Per destination with something to delete, the audit log records
`retention.deletion_planned` before the first delete (if that entry cannot
be written, nothing is deleted) and `retention.swept` afterwards with the
counts (deleted, missing, locked, failed, capped) and up to 50 deleted run
ids. Sweeps with nothing to do record nothing. Per-object outcomes are on
the artifact rows (state and time). See [Audit log](../security/audit.md).

## Configuration

| Key | Default | Meaning |
|---|---|---|
| `retention.dry_run` | `false` | Only plan and log, never delete. |
| `retention.max_deletions_per_sweep` | `500` | Hard cap per job sweep (1-100000). |
| `retention.partial_cleanup_age` | `24h` | Age of leftover temporary files removed daily (1h-30d). |
| `workers.retention_timeout` | `1h` | Time limit of one sweep. |
| `workers.retention_queue_workers` | `2` | Sweeps worked at the same time per process. |
