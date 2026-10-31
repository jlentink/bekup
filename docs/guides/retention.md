# Retention

Every job has a retention policy: **keep the last N backups**, **keep
backups younger than X days**, or both (a backup is kept when either rule
keeps it). bekup applies it **per destination**, after every successful or
partial run, after a change of the policy and once a day. Design:
[D-148](../DECISIONS.md), visibility and cleanup [D-209](../DECISIONS.md).

## The two rules add up

"Keep last N" and "keep X days" are not limits that both have to be met:
a backup stays while **either** rule keeps it. With nightly backups, *keep
last 7* and *keep 30 days* together keep about **30** backups per
destination, not 7. Use one rule if you want a simple bound.

The job editor shows a live estimate next to the retention fields: "with
these settings about N backups per destination are kept", computed by the
server from the schedule (it projects the planned runs, assumes they all
succeed and applies the same keep rules as retention), with the count of
each rule so you can see how they combine. For a manual schedule with a day
limit the count depends on how often you start a backup and is shown as
unknown. A very frequent schedule with a long day limit is shown as "at
least N" (the projection stops at 20,000 runs).

New jobs default to **keep the last 7** with no day limit. Existing jobs
keep their settings.

## When retention runs

- **After a run.** A run that ends `success` or `partial` queues a
  retention sweep of its job in the same database transaction that records
  the result.
- **After a policy change.** Saving a changed retention policy queues a
  sweep of the job in the same transaction, so the new values apply right
  away. If a sweep of the job is already queued, that one applies them; if
  one is running at that moment, the next sweep (after the next run, or the
  daily one) does.
- **Daily.** Once a day (and when the worker leader starts) bekup
  1. marks runs that no worker will finish as failed (`interrupted`): a run
     left `queued` or `running` for more than 15 minutes after its last
     attempt ended (the worker process died and no retry follows); a run
     someone asked to cancel becomes `canceled` instead. Their files then
     count for retention like those of any failed run. Retention holds of
     destinations where a job has no backups left are dropped;
  2. queues a sweep for every job that still has backups, **including
     deleted jobs**;
  3. per destination: removes leftover temporary upload files
     (`.<random>.partial`) older than `retention.partial_cleanup_age`
     (default 24 h) on disk and SFTP destinations, removes empty backup
     directories and looks for unrecorded files (see
     [Unrecorded files and empty folders](#unrecorded-files-and-empty-folders)).
     Each destination gets an equal share of the remaining time of the
     daily job (`workers.retention_timeout`), so a slow or unreachable
     destination cannot keep the others from being maintained.

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

## When incomplete backups block retention

Rules 1 and 5 protect the only copies of your data, but they also mean
that a destination where backups keep failing grows: nothing older than the
last complete backup goes away, and the incomplete backups after it are
all kept. bekup shows this instead of silently keeping files:

- After every sweep it records, per job and destination, the incomplete
  backups that are kept **only** because of rule 5 (or because the
  destination has no complete backup at all) and that the keep rules would
  not keep on their own. A single failed night inside your policy is not a
  hold; ten failed nights with *keep last 3* are (seven of them are held).
- The **job page** and the **dashboard** ("Needs attention") show a warning
  per destination: *Retention is holding N incomplete backups on
  &lt;destination&gt; since &lt;date&gt;: no complete backup there (since
  &lt;date&gt;)*. Everyone who can see the job sees it.
- The notification event **`retention.blocked`** (retention group, a
  problem event: inbox and e-mail by default) is sent at most once per job,
  destination and day while the hold lasts. See
  [Notifications](notifications.md).

The hold ends by itself with the next complete backup on that destination;
retention then removes the old backups as usual. Common causes: a database
that fails every night (fix the database or exclude it), a destination that
is unreachable or full, or a manifest that cannot be written.

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

## Unrecorded files and empty folders

bekup stores backups as `{job slug}/{timestamp}/{file}` below the
destination's location. Files can end up there without a record in bekup's
database, for example when a backup was interrupted after a file was
written but before it was recorded, or when someone copied files into the
directory. Retention only ever deletes recorded files, so such files would
stay forever. The daily maintenance therefore:

- **Removes empty folders** (disk and SFTP): run folders
  (`{job slug}/{timestamp}`) and job folders (`{job slug}`, only for slugs
  of jobs bekup knows, deleted ones included) that are empty and older than
  24 hours. It only looks at those two levels of bekup's layout, checks
  every folder with `lstat` (symbolic links are never followed or removed),
  checks emptiness and age again right before removing it, never removes a
  folder that is not empty and never the destination's own directory. Destinations in no-delete mode are left alone. A folder that
  only holds a hidden file (for example `.DS_Store`) is not empty and
  stays.
- **Records unrecorded files** (disk, SFTP and S3, by listing the folders
  of the job slugs bekup knows, deleted jobs included; anything else at the
  location is not bekup's to judge): files in bekup's layout that are
  older than 24 hours, have no backup record on any destination and do not
  belong to a queued or running backup. At most
  200,000 objects are listed and 1,000 files recorded per destination and
  day; the page says when the list is incomplete. Only the key, size and
  dates are stored.
- **Never deletes them automatically.** Admins see them on the destination
  page (**Unrecorded files**, plus a warning at the top of the page) and can
  delete them one by one. A delete needs a fresh confirmation (step-up), is
  audited (`destination.unrecorded_file_deleted`, recorded before the file
  is deleted; no delete without it) and is checked again at that moment:
  the file must still exist and must not have changed in the last 24
  hours, and the key must still have no backup record on any destination
  and must not belong to a running backup. Destinations in no-delete mode
  refuse it.

Hidden files (names starting with a dot), files outside bekup's layout and
folders of unknown job slugs are not listed.

## Deleted jobs

Deleting a job does not delete its backups. They remain restorable and
age out by the job's policy; the newest complete backup of a deleted job is
kept until it is removed explicitly.

## Dry run

Set `retention.dry_run = true` to make every sweep only compute and log its
plan (kept runs with the reason, runs that would be deleted, object counts)
without deleting anything or contacting the destinations. The daily partial
cleanup, empty-folder removal, unrecorded-file scan and the marking of
interrupted runs are skipped too (retention holds are still recorded). bekup logs a warning at startup while dry run is
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
