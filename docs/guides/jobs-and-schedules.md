# Jobs and schedules

> **Status:** implemented: schedules, the job model and GraphQL API,
> backup runs, run history and live run events, completeness evidence and
> the calendar (M3), missed-backup alerts (M5), and the jobs editor, the
> job and run pages and the calendar page of the web UI (M6).

How to create backup jobs: selecting databases (all, one or several, plus Postgres globals), schedules (interval + anchor in a timezone, cron, or manual-only), expected windows for missed-backup alerts, destinations, owners and timeouts.

## The job model

A job (D-146) describes **what** is backed up, **when**, **where to** and
**who** may see it. Admins and maintainers manage every job; backup owners
see and run only their own jobs (see [Who may do what](#who-may-do-what)).

| Field | Meaning |
|---|---|
| `name` | Unique (case-insensitive) among jobs that are not deleted; 1–100 characters. |
| `slug` | Names the job's directory in every destination (`{prefix}/{slug}/{timestamp}/...`). Derived from the name (`Production DB` → `production-db`, `-2`, `-3` on collisions) or chosen at creation: lower-case letters, digits and inner hyphens, ≤ 63 characters. It **never changes** (also enforced in the database) and is never reused, also after the job is deleted. |
| `connection` | The database server. Temporary restore connections cannot be used. While a job that is not deleted uses a connection, the connection cannot be deleted. |
| `dbMode` | `ALL`: every database on the server, one file each, minus `exclude`. `SELECTED`: exactly `databases` (1–256 names). Names use the strict engine character set (no quotes, spaces, `=`, `/`, `\`, no leading `-`). |
| `includeGlobals` | PostgreSQL only: also dump the cluster globals (roles, tablespaces) as a separate file. Default on for PostgreSQL; MySQL/MariaDB jobs refuse it. |
| `schedule` | Manual, interval or cron (below). The next run (`nextRunAt`) is computed on save; a changed schedule is re-planned from now. |
| `enabled` | Default on. A disabled job has no scheduled **and no manual** runs. Enabling plans the next run from now (missed periods are not caught up). |
| `retention` | `keepLast` (1–10000 backups) and/or `keepDays` (1–36500 days); at least one. Applied per destination after every successful/partial run and daily; the newest complete backup on each destination is never deleted (see [Retention](retention.md)). |
| `timeoutSeconds` | Optional run timeout, 60 seconds up to `workers.backup_timeout` (the queue cap, default 6h); empty means the cap. |
| `expectedWindowSeconds` | Optional missed-backup alert window, 60 seconds to 366 days: how long after each planned time a backup must have succeeded before `backup.missed` is sent (see [Missed-backup alerts](#missed-backup-alerts)). Empty means derived: twice the run timeout plus one hour (13 h with the defaults). |
| `verification` | `STANDARD` (object counts and row estimates) or `DEEP` (exact per-table row counts, PostgreSQL only; MySQL/MariaDB always use estimates); completeness evidence of task 3.10. |
| `destinations` | 1–16 destinations in fan-out order; the dump runs once and is written to all. A destination used by a job that is not deleted cannot be deleted. |
| `encryption` | Always on at creation (see [Encryption](encryption.md)): a generated passphrase (shown once in `createJob`), a chosen passphrase, a given age recipient or a generated age keypair (identity shown once). Only an admin can opt a job out afterwards. |
| `owners` | Backup owners (users). Visible to admins and maintainers only. |

**Deleting** a job is a soft delete: its schedule stops at once, a run that
is already in progress finishes, and the job's artifacts, keys, owners and
audit history stay. Its name can be reused; its slug cannot.

### In the web UI

- **Backups → Jobs** lists the jobs you may see with their schedule, last
  run, next run and encryption state, with a search and filters (enabled,
  connection). **New job** (admins and maintainers) opens the editor:
  connection, databases (**Select only**, the default: just the checked
  databases; or **Exclude**: every database except the checked ones,
  including databases created later; cluster globals for PostgreSQL), the
  schedule builder (interval, cron or manual, with a preview of the next
  occurrences), retention and limits, destinations, the verification level
  and encryption. At creation the job gets a
  generated passphrase by default, shown **once** after saving; admins
  can choose a passphrase or an age recipient instead.
- A job's page shows its configuration, the run history (live) and the
  latest test-restore verdict. **Run now (extra run)** and
  **Start next planned run early** are explained [below](#run-now-vs-start-the-next-run-early);
  **Edit job** opens the editor with the **Encryption** section (key
  actions, admins only, behind a step-up; see
  [encryption](encryption.md)), the **Test restores** section and the
  **Owners** panel (owners can be added once the job exists).
- **Backups → Runs** lists every run you may see, updated live. A run's
  page shows its databases, the files per destination with their
  verification and signature state, warnings and errors, the
  [completeness evidence](#completeness-evidence-verification-levels-and-warnings),
  the live log while it runs (**Follow** keeps the tail in view, **Copy
  log**), **Cancel run** (staff), and per file **Download**
  ([downloads](downloads.md)) and **Restore** ([restore](restore.md)).
- **Overview → Calendar** shows planned and past runs and expiries
  ([below](#calendar)).

### Who may do what

| Action | Admin | Maintainer | Owner (own jobs) | Other users |
|---|---|---|---|---|
| List / view jobs | all | all | own only | – |
| Create, edit, enable/disable, delete | yes | yes | – | – |
| Preview a schedule | yes | yes | – | – |
| Run now, start the next run early | yes | yes | yes | – |
| Assign / remove owners (step-up) | yes | yes, not themselves | – | – |
| Change keys, opt back in, choose a key at creation (step-up) | yes | – | – | – |
| Opt out of encryption, export passphrases (step-up) | yes | – | – | – |

Owner rights apply to accounts with the **user** role only: a maintainer or
admin listed as an owner gains nothing from it (D-067). Every owner change
is audited and raises a security alert. A deleted account owns nothing.

To pick owners, admins and maintainers search the **user directory**
(`userDirectory(search, first, after)`): only the id, display name,
e-mail address, role and disabled flag of accounts that are not deleted
(D-184). `jobs(filter: {ownerId: "…"})` lists the jobs a user owns; the
owner filter is for admins and maintainers (backup owners get
`FORBIDDEN`; they see only their own jobs anyway).

**Run now** enqueues a manual run of an enabled job. A job never has two
runs at once: while one is queued, running or waiting for a retry, "Run
now" answers `CONFLICT` ("a run of this job is already active").

### Run now vs. start the next run early

There are two ways to start a backup by hand (D-156, D-157):

| | **Run now** (`runJobNow`) | **Start the next run early** (`startNextRunEarly`) |
|---|---|---|
| What runs | an **extra** run (trigger `MANUAL`) | the **next planned** run, now (trigger `EARLY`) |
| The planned run | still runs at its time | is skipped at its time |
| Next run afterwards | unchanged | the occurrence after the one pulled forward |
| Manual schedule | allowed | refused (`VALIDATION`): there is no planned run |
| Disabled job | refused (`CONFLICT`) | refused (`VALIDATION`) |
| Next run already started early | allowed | refused (`VALIDATION`) until its original time has passed |
| A run is active | `CONFLICT`, nothing changes | `CONFLICT`, nothing changes (the next run stays planned) |

Note the different codes for a disabled job: `runJobNow` answers
`CONFLICT`, `startNextRunEarly` answers `VALIDATION` (field `id`, the
message names the reason). Clients (the web UI does) should treat both as
"enable the job first".

An early run records the planned time it stands for in `scheduledFor`; the
calendar shows that occurrence as started early. "The occurrence after"
is exactly one step of the schedule: a daily 03:00 job pulled forward from
Saturday next runs on Sunday at 03:00 local time, even across a daylight
saving change, and a monthly day-31 job pulled forward from 31 January next
runs on 28 (or 29) February. If the planned run was already due (the
scheduler had not picked it up yet) and the following occurrence has passed
as well, the schedule continues with the first occurrence after now, as
after downtime.

Only the run that is genuinely next can be pulled forward (D-160): once it
was started early, `startNextRunEarly` refuses the following one
(`VALIDATION`, "the next planned run was already started early") until the
original time of the pulled-forward run has passed. The planned schedule is
therefore never more than one occurrence ahead, and repeated early starts
cannot postpone the planned backups. Use `runJobNow` for further extra runs.
Both actions are audited (`job.run_requested`,
`job.run_started_early` with the original and the new next run).

### GraphQL

```graphql
mutation {
  createJob(input: {
    name: "Production DB"
    connectionId: "…"
    dbMode: ALL
    exclude: ["scratch"]
    schedule: {kind: INTERVAL, every: 1, unit: DAYS, anchor: "2026-11-01T03:00", timezone: "Europe/Amsterdam"}
    retention: {keepLast: 14, keepDays: 30}
    destinationIds: ["…", "…"]
  }) {
    job { id slug nextRunAt scheduleDescription encryption { mode passphrase { version } } }
    generatedPassphrase   # shown once: store it offline
  }
}

query { previewSchedule(input: {kind: CRON, cron: "0 3 * * 1-5", timezone: "UTC"}, count: 5) { description next } }
mutation { runJobNow(id: "…") { queuedAt } }
mutation { startNextRunEarly(id: "…") { runId queuedAt job { nextRunAt } } }
```

Other fields: `jobs(filter, first, after)`, `job(id)`, `updateJob`,
`deleteJob`, `enableJob`, `disableJob`, `assignJobOwners`, `removeJobOwner`
and the key operations `setJobPassphrase`, `generateJobPassphrase`,
`setJobRecipient`, `generateAgeKeypair`, `enableJobEncryption`,
`disableJobEncryption` and `exportJobPassphrases` (see
[Encryption](encryption.md) and `docs/reference/schema.graphql`).

### Test restores

A job can regularly prove that its backups restore completely (task 4.6,
D-169). The settings are part of the job:

| Setting | Default | Notes |
|---|---|---|
| `enabled` | off | scheduled test restores |
| `schedule` | weekly, Sunday 04:00 UTC | any [schedule](#schedules); `MANUAL` = only `runTestRestoreNow` |
| `targetConnectionId` | none | the **scratch connection**, required while enabled |
| `acknowledgePrivilegedTarget` | false | required (admins only) when the scratch connection's role is privileged or unknown |

The scratch connection must be a permanent connection with the job's
engine (MySQL and MariaDB are different engines). It may be the job's own
connection: bekup only ever creates and drops databases named
`bekup_test_<id>` there, and never touches another database. Its user needs
the right to create and drop databases (`CREATEDB` on PostgreSQL,
`CREATE`/`DROP` on MySQL/MariaDB). A connection used as a scratch target
cannot be deleted while a job designates it.

Changing the settings (`setJobTestRestore`) is for admins and maintainers
and needs a passkey or authenticator check within the last 5 minutes, like
a restore: the scratch connection receives a full copy of the job's data.
The change is audited (`job.test_restore_updated`). The scratch
connection's role is probed when it is designated and again before every
test restore: a privileged or unknown role needs an admin and
`acknowledgePrivilegedTarget` (D-068, D-170; see
[privileged scratch connections](restore.md#privileged-scratch-connections)). Removing or changing
the scratch connection also stops test restores that were already queued
for the old one. Backup owners see the settings and the results of their
jobs. What a test restore checks and how results are reported is described
in [Restore](restore.md#scheduled-test-restores).

In the web UI the settings are the **Test restores** section of the job
editor (admins and maintainers). It offers the permanent connections of
the job's engine and shows the role verdict known for the chosen one: the
verdict stored when it was designated or, for another connection, the one
from its last connection test. Admins tick the acknowledgement for a
privileged, unknown or not yet tested role; the server probes the role on
save and decides. Saving asks for a passkey or authenticator code. The job
page shows the latest verdict with a summary of the differences, **Run
test restore now** (staff) and **All test restores**: the history of the
job's test restores and, for each, the restored databases with every
finding (backup vs. restored count and the difference).

## Schedules

Every job has exactly one schedule (D-009, D-044, D-143):

| Kind | Example | Meaning |
|---|---|---|
| Manual | `{"kind":"manual"}` | Never runs on its own; only "Run now". |
| Interval | every 2 days at 03:00 in Europe/Amsterdam | N minutes, hours, days, weeks or months from an **anchor**. |
| Cron | `0 3 * * 1-5` in America/New_York | A standard 5-field cron expression. |

Every interval and cron schedule has an **IANA timezone** (`Europe/Amsterdam`,
`America/New_York`, `UTC`, ...). `Local` and abbreviations like `CET` are not
accepted: the zone must mean the same thing on every host. bekup ships its
own copy of the zone database (Go's `time/tzdata`), so minimal containers
work too.

### Interval schedules

An interval schedule has `every` (N), a `unit` and an `anchor`: a local date
and time (`YYYY-MM-DDTHH:MM`) in the schedule's zone. The anchor is the
**first run**; later runs follow from it.

```json
{"kind":"interval","every":2,"unit":"days","anchor":"2026-11-01T03:00","timezone":"Europe/Amsterdam"}
{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-11-02T03:00","weekday":"monday","timezone":"UTC"}
{"kind":"interval","every":1,"unit":"months","anchor":"2026-11-01T02:00","day_of_month":1,"timezone":"Europe/Amsterdam"}
{"kind":"interval","every":6,"unit":"hours","anchor":"2026-11-01T00:15","timezone":"UTC"}
```

| Unit | Runs | Limits |
|---|---|---|
| `minutes`, `hours` | Every N minutes/hours of **elapsed time** from the anchor. | N ≤ 1440 minutes / 168 hours, and at least `scheduler.min_interval` (default 5 minutes). |
| `days` | Every N calendar days at the anchor's time of day, starting on the anchor date. | N ≤ 366 |
| `weeks` | Every N weeks on `weekday` (default: the anchor's weekday) at the anchor's time, starting on the first such day on or after the anchor date. | N ≤ 52 |
| `months` | Every N months on `day_of_month` (default: the anchor's day) at the anchor's time, starting in the anchor's month (never before the anchor). | N ≤ 24 |

**Sub-day intervals are absolute.** "Every 6 hours" is always six real hours
apart, also across a DST change: from 00:00 on the night clocks go forward the
runs are at 00:00, 07:00 and 13:00 local time (still 6 hours apart).

**Day, week and month intervals follow the wall clock.** "Every day at 03:00"
runs at 03:00 local time every day, whether the zone is on summer or winter
time; the day of a DST change is 23 or 25 hours long, so the gap between two
runs changes accordingly.

**Month ends are clamped, without drift.** A monthly schedule always aims at
its `day_of_month` and uses the last day of months that are too short:
day 31 runs on Jan 31, Feb 28 (Feb 29 in leap years), Mar 31, Apr 30, ...
It never "gets stuck" on day 28 after February.

### Cron schedules

```json
{"kind":"cron","cron":"0 3 * * 1-5","timezone":"Europe/Amsterdam"}
```

- Exactly five fields: minute, hour, day of month, month, day of week
  (0-6, Sunday = 0; names such as `mon` and `jan-mar` work; `*`, `?`,
  ranges, steps and lists are supported).
- If both day of month and day of week are restricted, either one matching
  is enough (classic cron behaviour): `0 0 13 * 5` runs on every 13th and
  every Friday.
- Descriptors: `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`,
  `@midnight`, `@hourly`.
- Not accepted: a seconds field, `@every ...`, `@reboot`, `TZ=`/`CRON_TZ=`
  prefixes (use the `timezone` field), expressions that never match a date
  (`0 0 30 2 *`), and expressions whose closest two runs are less than
  `scheduler.min_interval` apart (`*/2 * * * *` with the default 5 minutes).
- Expressions are at most 128 characters.

Cron times are wall-clock times in the schedule's zone and follow the same
DST rules as interval schedules (below). This differs from the common Go cron
library, which skips times in the spring-forward gap and runs twice in the
repeated fall-back hour.

### Daylight saving time

When clocks go **forward** (e.g. Europe/Amsterdam on the last Sunday of March,
02:00 → 03:00), local times in the gap do not exist. A run planned in the gap
is **shifted forward by the size of the gap**: 02:30 runs at 03:30 that day,
and at 02:30 again on the following days. In zones with a 30-minute change
(Australia/Lord_Howe) 02:15 becomes 02:45. If the shifted time coincides with
another run of the same schedule, it runs once.

When clocks go **back** (e.g. the last Sunday of October, 03:00 → 02:00),
local times between 02:00 and 03:00 happen twice. A run planned then runs
**once, at the first occurrence** (02:30 summer time). An hourly cron such as
`30 * * * *` therefore runs at 02:30 only in the first pass of that hour.

Sub-day intervals ignore all of this because they count elapsed time.

| Schedule (Europe/Amsterdam) | Spring-forward day (Mar 29, 2026) | Fall-back day (Oct 25, 2026) |
|---|---|---|
| every day at 02:30 | 03:30 CEST | 02:30 CEST (once) |
| every day at 03:00 | 03:00 CEST | 03:00 CET |
| cron `30 2 * * *` | 03:30 CEST | 02:30 CEST (once) |
| every 1 hour from 00:00 | 00:00, 01:00, 03:00, 04:00 ... | 00:00, 01:00, 02:00 CEST, 02:00 CET, 03:00 ... |

### Missed runs

If bekup was not running when one or more runs were due (maintenance,
outage), it does **not** replay them all. At the next scheduler tick it starts
**one catch-up run** for the whole missed period and continues with the next
regular time in the future. Example: a daily 03:00 job, bekup down from
Monday 01:00 to Thursday 12:00, runs once on Thursday at about 12:00 (marked
as catch-up) and again on Friday at 03:00.

### Runs never overlap

A job has at most one run queued, running or waiting for its retry. If the
previous run is still active when the next time comes, that time is
**skipped** (recorded as "skipped: still running") and the job continues
with the following regular time. A job whose stored schedule can no longer
be evaluated stops being scheduled until it is saved again (recorded as
"unschedulable" and logged at WARN). Skip records are kept for 90 days.

### Missed-backup alerts

A worker checks every 5 minutes whether each enabled job with a schedule
(not manual) got a backup in time (D-188). A planned time **T** is
**missed** when no run of the job ended `success` or `partial` between T
and T + expected window. Counted are the job's own run for T (also a run
started early for T), a later or manual run, and the run that was still
running when T was skipped. So:

| Situation | Missed? |
|---|---|
| The run for T succeeded (or partially succeeded) within the window | no |
| T was skipped because the previous run was still running, and that run succeeded within T's window | no |
| The run for T failed or was canceled, and nothing succeeded within the window | yes (`run_failed`, `run_canceled`) |
| The run for T is still queued or running when the window ends | yes (`run_queued`, `run_running`) |
| The run for T succeeded, but only after the window | yes (`finished_late`) |
| The scheduler did not start T at all (bekup or the scheduler not running) | yes (`not_enqueued`) |
| The schedule can no longer be evaluated | yes (`unschedulable`) |

Planned times are taken from what the scheduler recorded (runs, skip
records) and the stored next run time; times before a job was enabled are
never judged. Disabled, manual-only and deleted jobs are not checked.

**One alert per outage:** consecutive missed times count as one outage,
which sends one `backup.missed` and then a reminder once a day while no
backup succeeds. The next successful backup ends the outage
(`backup.succeeded` is the "all clear"); a later miss starts a new one.
Times whose deadline lies more than 7 days back are not looked at, except
a planned time the scheduler never started. The alerts need a worker
process to run; for independent monitoring use the `/metrics` endpoint
(oldest last success per job, see [Operations](../operations.md)).

### Limits

| Limit | Value |
|---|---|
| Minimum interval for minute/hour schedules and between cron runs | `scheduler.min_interval` (default `5m`, allowed `1m`–`24h`); checked when a job is created or its schedule changed |
| Anchor years | 2000–2199 |
| Schedule document | ≤ 1 KiB JSON, unknown fields rejected |
| Schedule preview | up to 50 upcoming runs |

Raising `scheduler.min_interval` later does not stop existing jobs; it only
applies when a schedule is saved again.

## Runs

Every scheduled, catch-up or manual run becomes a **run** the moment it is
queued (`runJobNow` returns its `runId`). A run backs up each selected
database into its own file (one file per database; with "Exclude",
every database on the server except the excluded ones), plus the PostgreSQL
globals when the job includes them, then writes a signed `manifest.json`
next to the files on every destination that holds all of them.

### Run statuses

| Status | Meaning |
|---|---|
| `QUEUED` | Waiting for a worker or for a free concurrency slot (`workers.max_concurrent_runs`, `workers.max_runs_per_connection`). |
| `RUNNING` | Dumping, storing and verifying. |
| `SUCCESS` | Every database (and the globals) was stored **and verified** on every destination of the job, and the signed manifest is on all of them. |
| `PARTIAL` | At least one complete, verified backup file exists, but something is missing: a destination failed or fell behind and was dropped, a database failed (dump error, verification failure, not on the server), a manifest could not be written, or the run timed out after some databases. The run's warnings say what. |
| `FAILED` | Nothing usable was stored (the server was unreachable, every destination failed, ...). |
| `CANCELED` | An admin or maintainer canceled it (`cancelRun`). |
| `SKIPPED` | The job was deleted or disabled before the run started. |

Each database is verified with the engine's own tool while it is stored:
PostgreSQL archives with `pg_restore` (in an offline container on the
runner), MySQL/MariaDB dumps and the PostgreSQL globals by their completion
trailer. A dump that fails verification is kept but flagged unverified and
is **not** listed in the manifest, so it is never offered for restores.
An incomplete or canceled dump never leaves a file behind.

A run is limited by the job's timeout (default `workers.backup_timeout`, at
most that value). On timeout the database in progress is abandoned, the
databases already finished keep their files and manifest, and the run ends
`PARTIAL` (or `FAILED` if nothing finished). If the runner is unreachable
and nothing was stored, or the worker shuts down mid-run, the run is retried
once as a new run.

### Completeness evidence (verification levels and warnings)

Before each dump the runner records **source statistics** of the database
(D-141): object counts (schemas, tables, views, materialized views,
sequences, functions, procedures, triggers, extensions or events) and row
counts per table. They are signed into the run's manifest and kept per
database, so a test restore can prove that the backup is complete, not just
intact. The job's `verification` level chooses the row counts:

| Level | Row counts | Cost |
|---|---|---|
| `STANDARD` (default) | the catalog's estimates (`reltuples`, `TABLE_ROWS`), marked `estimate` | a few catalog queries; fine for huge databases |
| `DEEP` | PostgreSQL: an exact `count(*)` per table; MySQL/MariaDB: same as `STANDARD` | one full count per table; each count is limited to 5 minutes and the whole capture to 30 minutes (tables beyond that are `unknown` with a warning) |

**PostgreSQL** counts are exact for the backup itself: the runner takes
them inside a read-only snapshot and runs `pg_dump --snapshot` in that same
snapshot, keeping the snapshot's transaction open until the dump finished
(it holds back vacuum for that time, like `pg_dump` does). The backup user
needs nothing extra: `pg_dump` imports the snapshot as the same user.
**MySQL/MariaDB** row stats are always the `information_schema`
estimates (`TABLE_ROWS`; exact for MyISAM/Aria, approximate for InnoDB),
at both levels: `DEEP` adds exactness only for PostgreSQL, and the
statistics are recorded at level `STANDARD`. bekup never runs a statement
against a MySQL/MariaDB user table or view (D-153): a database owner could
otherwise have the backup account evaluate a swapped-in view. Object
counts are exact; every table is marked `approximate`.

For both engines the runner treats the target database as untrusted: its
PostgreSQL sessions pin `search_path` and `row_security` and block views
and foreign tables, so functions, operators or policies created by the
database's owner never run as the backup user (D-152). On a PostgreSQL
server without the August 2024 security releases (no
`restrict_nonsystem_relation_kind`) `DEEP` counts are skipped
(`row_count_failed`); a table protected by row-level security that applies
to the backup user is `unknown` with `insufficient_privileges` (grant
`BYPASSRLS`, which `pg_dump` needs for it anyway).

Completeness warnings are recorded on the database and the run (the status
stays `SUCCESS` or `PARTIAL`; the run shows **has warnings**). A database
whose dump failed keeps its warnings (they often explain the failure):

| Code | Meaning |
|---|---|
| `insufficient_privileges` | the backup user cannot read some objects (PostgreSQL: tables or sequences without SELECT, schemas without USAGE; MySQL/MariaDB: tables it can see without a SELECT grant at global, database or table level, read from `information_schema`; skipped when the account uses roles, whose grants `information_schema` does not show). Objects a MySQL/MariaDB user cannot see at all are invisible to bekup and the dump tool alike: grant the backup user SELECT, SHOW VIEW, TRIGGER, EVENT and routine access on the whole database. |
| `non_transactional_tables` | MyISAM, Aria, MEMORY, CSV, ... tables are dumped without a consistent snapshot; writes during the backup make the dump inconsistent |
| `row_count_timeout`, `stats_budget_exceeded`, `row_count_failed` | some exact counts are missing (`unknown`) |
| `stats_partial` | more than 10 000 tables: the rest is counted but not itemized |
| `roles_without_passwords` | the PostgreSQL globals were dumped without role passwords (the backup user cannot read `pg_authid`) |
| `stats_unavailable` | the statistics could not be captured; the backup ran without evidence |

With "Exclude", the databases on the server that the job's exclude
list leaves out are reported as an informational **notice**
(`databases_excluded` in `Run.notices`), not a warning: excluding them is
intended, so it does not set **has warnings**. The signed manifest lists
them in `stats.databases_excluded`.

The evidence of each database is readable over GraphQL as
`RunItem.stats` (D-187), with the same access as the run (backup owners
see their own jobs' runs only): `level` (`STANDARD`/`DEEP`),
`consistency` (`SNAPSHOT`: PostgreSQL counts taken in the dump's snapshot;
`APPROXIMATE`: taken right before the dump), object counts by kind, the
per-table row counts with `exact` / `estimate` / `unknown` /
`approximate` flags (`rows` is null when unknown), `tablesTotal`,
`tablesTruncated`, and the completeness `warnings` and `notices` with
their codes and up to 50 object names each. It is null for databases
without recorded evidence (failed or unverified dumps,
`stats_unavailable`). A page of runs loads the evidence of all its items
in one query. Names and counts only, never data.

In the web UI the run page shows this evidence per database once the run
has finished: level and consistency, object counts, warnings and notices
with their object names, a note when the table list was truncated, and the
per-table row counts with an `exact`, `estimate` or `unknown` badge (plus
`approximate` for MySQL/MariaDB). Long table lists show the first 50
tables with a filter and "show all".

### Canceling a run

`cancelRun(id)` (admins and maintainers; backup owners can start runs of
their jobs but not cancel them) stops a queued run at once and a running
run within seconds: the dump container is killed and every upload is
aborted. Files of databases finished before the cancel stay. A run that
already failed while its job waits for the automatic retry gets that retry
canceled (`retryCanceled: true`; the run keeps its status). The request is
audited (`run.cancel_requested`).

### Run logs

Each run keeps the runner's tool output and the worker's events. Known
secret values (the connection's password and keys, the job passphrase) and
credentials in connection strings are redacted, lines longer than
`workers.run_log_max_line_bytes` are truncated and at most
`workers.run_log_max_lines` lines are stored; a final line counts what was
left out.

### Run history and live updates

The run history, run details, logs and stored files are GraphQL queries
(task 3.7): `runs(filter, first, after)` (newest first; filter by job,
status, trigger and a `since`/`until` time range), `run(id)` (databases,
files per destination with their verification and signature state,
warnings, errors, sizes, durations, the signed manifest), `runLogs(runId,
afterId, first)` and `artifacts(filter, first, after)` (the backup
browser). `Job.lastRun` and `Job.recentRuns` show the newest runs of a job.
Backup owners see the runs and files of their own jobs only, and never who
started a run (that is shown to admins and maintainers).

```graphql
query { runs(filter: { jobId: "…", status: FAILED }, first: 20) {
  edges { node { id status queuedAt durationSeconds errorMessage } }
  pageInfo { hasNextPage endCursor } } }
```

Live updates are subscriptions over Server-Sent Events:
`runUpdated(jobId, runId)` sends the current state of a run whenever it
changes (both filters optional), and `runLogAppended(runId, afterId)` sends
new log lines in batches and ends with a `done` batch when the run is
over. Every event is checked against your current rights when it is sent:
an owner removed from a job stops receiving its runs at once. A session may
hold `graphql.stream_max_per_session` streams (default 4), so the UI opens
at most one page stream per tab (D-197): `runActivity` (run changes and
log lines) on a run page, one `runUpdated` filtered on the client on list
pages, `activityUpdated` on the dashboard.

## Calendar

The Calendar page has month, week and day views, filters by job,
connection and destination, and a time-zone selector (default: the
browser's zone). Planned runs are outlined, paused ones hatched, started-
early ones marked, overdue ones highlighted, skipped ones struck through,
runs filled by status and expiries labelled exact or projected; a click
opens the run or job. Arrow keys move between days, Enter opens a day, and
a notice appears when the range was truncated.

The job calendar (D-156, D-159; the **Calendar** page of the web UI,
D-185) shows, for a range of at most 93 days:

| Kind | What | Markers |
|---|---|---|
| `PLANNED` | the planned runs from now (or from the start of the range, if later) | `paused`: the job is disabled, the run will not happen; `startedEarly`: the run was pulled forward with `startNextRunEarly` (`runId` is the early run); `overdue`: the planned time of an enabled job has passed and the scheduler has not picked it up yet, shown at now (missed runs collapse into this one catch-up run, which the projection also counts at now) |
| `RUN` | runs that overlap the range, with `status` and `trigger` (`SCHEDULE`, `CATCH_UP`, `MANUAL`, `EARLY`) | `end` once finished; an error code in `detail` |
| `SKIPPED` | planned runs the scheduler skipped (`detail`: the previous run was still running, or the schedule had no valid next run) | |
| `EXPIRY` | the earliest time [retention](retention.md) may remove a run's copy on one destination (the next retention sweep at or after it removes it) | `projected` |

An expiry is **exact** when it follows from the run alone: the
keep-days window (run time + days) or an Object Lock retain-until. It is
**projected** when it depends on runs that have not happened yet: under
keep-last a run is removed only after enough newer complete runs exist,
and the newest complete run on a destination is never removed until a
newer complete run exists there. The projection assumes every planned run
succeeds; a run is kept as long as either rule keeps it, so the expiry is
the later date (and never before an Object Lock retain-until). Disabled
and manual-only jobs get no projected expiries (no runs are planned).
Example: with keep-last 7 and a daily job, today's backup is projected to
expire with the 7th planned run after it. An expiry is the earliest time
retention *may* remove a copy: a copy that is being downloaded or restored
is held (artifact holds, D-155, at most 1 hour) and is removed by the first
sweep after the hold ends, which can be shortly after the time shown.

```graphql
query { calendar(from: "2026-11-01T00:00:00Z", to: "2026-12-01T00:00:00Z",
                 timezone: "Europe/Amsterdam", jobIds: ["…"]) {
  truncated
  events { kind jobId jobName start end status trigger runId destinationId
           projected paused startedEarly overdue detail } } }
```

Times are UTC; `timezone` (an IANA name such as `Europe/Amsterdam`) is
validated and echoed for the client to group events by local day.
`jobIds` narrows the jobs (omit it for all jobs). Everyone can open the
calendar; backup owners see their own jobs only (other ids are ignored).
The result is bounded (at most 5,000 events, 500 planned runs per job and
500 jobs); when a bound is hit `truncated` is true and you should narrow
the range or the jobs. When the event cap is hit, the earliest 5,000 events
are kept, so a truncated calendar is complete up to its last event. The
reads run in one read-only transaction with a 5-second statement timeout
(D-160); a request that hits it fails with an internal error.
