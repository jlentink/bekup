# Operations

> **Status:** partial. The health endpoints, the startup checks, the
> Prometheus metrics, the in-app alerts (missed backups, security alerts),
> the background schedule and the scaling options are described below;
> troubleshooting is completed in task 7.5.

Running bekup day to day: Prometheus metrics on the internal port, missed-backup and security alerts, runner health, backing up bekup's own database and keys, and troubleshooting common failures.

## Health endpoints

`bekup serve` answers two unauthenticated endpoints on `server.listen`. Both
return a short `text/plain` body and never any details; the reason for a
failure is in the serve log (`readiness check failed`, with the request
ID).

| Endpoint | Meaning | Checks | Status codes |
|---|---|---|---|
| `GET /healthz` | **Liveness**: the process is up and serves HTTP | nothing (no database access) | `200 ok` |
| `GET /readyz` | **Readiness**: this instance can serve requests | database ping as `bekup_app`, and the schema version equals the newest migration in this binary (3s timeout) | `200 ok`, `503 not ready` |

`HEAD` works on both. They go through the same middleware as every other
request (security headers, per-IP rate limit), so poll them at a modest
interval.

`/readyz` turns `503` when the database is unreachable or when a newer
`bekup migrate` has moved the schema past this binary, for example during an
upgrade while old instances are still running.

### Container healthcheck

The image is distroless (no shell, no curl). The compose healthcheck runs
the binary itself:

```yaml
healthcheck:
  test: ["CMD", "/usr/local/bin/bekup", "healthcheck", "serve"]
```

`bekup healthcheck serve` loads the same `bekup.toml`, derives the address
from `server.listen` (a wildcard host such as `0.0.0.0` or `[::]` is probed
on loopback), sends `GET /healthz` with a 3-second timeout, ignores
`HTTP(S)_PROXY` and does not follow redirects. It prints `ok` and exits 0
when the answer is `200`, and exits 1 otherwise. It probes liveness, not
readiness, so a short database outage does not mark the container unhealthy
(D-102); point external monitoring at `/readyz` through the proxy or the
internal network if you want readiness alerts.

`bekup healthcheck runner` (D-125) reads `runner.toml` and asks the
standard gRPC health service on the runner's container-local unix socket
`listen.health_socket` (default `/tmp/bekup-runner/health.sock`, on the
container's private `/tmp` tmpfs) for `runner.v1.RunnerService`, with a
3-second timeout. No client certificate is needed: the socket is plaintext,
its directory is `0700` and the socket `0600`, both owned by the runner user,
and it serves nothing but the health status. It reports `SERVING` (prints
`ok`, exit 0) only while the mTLS listener is up, the runner's own
certificate is valid and no shutdown is in progress; otherwise it exits 1.
An empty `listen.health_socket` disables the socket (and the healthcheck
then always fails).

## Metrics

`bekup serve` (and every `bekup worker`) exposes Prometheus metrics on a
**separate, internal listener** (`[metrics]`, D-024, D-176). It is not the
UI/API port and must never be published or routed through the reverse proxy.

| Key | Default | Meaning |
|---|---|---|
| `metrics.enabled` | `true` | Serve the endpoint. |
| `metrics.listen` | `127.0.0.1:9464` | Address to bind. Must differ from `server.listen`. Outside dev mode a wildcard (`:9464`, `0.0.0.0`, `[::]`) is refused unless `metrics.allow_wildcard_listen = true` (startup warning). |
| `metrics.auth_bearer_file` | empty | Optional secret file with a bearer credential (≥ 32 printable characters, e.g. `openssl rand -hex 32`); then `/metrics` requires `Authorization: Bearer <credential>`. |
| `metrics.per_job_labels` | `false` | Add per-job series labelled with the job slug (see below). |

The listener answers only `GET`/`HEAD /metrics` and `GET`/`HEAD /healthz`
(`ok`, never authenticated); everything else is `404`/`405`.

### Scraping with compose

`docker-compose.yml` attaches `serve` to an internal network `metrics`
(`10.213.252.0/24`, override with `METRICS_SUBNET`) with the fixed address
`10.213.252.10` (`SERVE_METRICS_IP`). Bind the endpoint there:

```toml
[metrics]
listen = "10.213.252.10:9464"
```

Attach your Prometheus to that network, either in an override file of the
same project:

```yaml
# compose.override.yaml
services:
  prometheus:
    image: prom/prometheus:<pinned tag>@sha256:<digest>
    user: "65534:65534"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus-data:/prometheus
    networks: [metrics]
volumes:
  prometheus-data: {}
```

(`setup check` lints the override: digest-pinned image, a non-root user, a
read-only root, no capabilities, `no-new-privileges`, literal values (no
`${VAR}`), of bekup's networks only `metrics` and without aliases there,
no bind of bekup's own files; see
[the hardening lint](installation/compose.md#the-override-file-and-the-hardening-lint).)

or from another compose project by declaring the network as external
(`networks: { bekup_metrics: { external: true } }`). Scrape configuration:

```yaml
scrape_configs:
  - job_name: bekup
    static_configs:
      - targets: ["10.213.252.10:9464"]
    # With metrics.auth_bearer_file:
    # authorization:
    #   credentials_file: /etc/prometheus/bekup_metrics_bearer
```

The network is `internal` (no internet). Set `metrics.auth_bearer_file`
when other workloads share it. Without compose, keep the default loopback
address and run the scraper (or an agent) on the same host, or bind a
private interface that only the scraper reaches.

### Metric reference

Event counters are kept **per process** (they restart at 0 with the
process; use `increase()`/`rate()`): a `bekup worker` exposes the runs,
restores and retention it executed, `serve` those of its own workers and
its scheduler (a `bekup worker` binds its own `metrics.listen`: give each
process on a host its own address). The gauges marked *serve* come from a health snapshot that
only `serve` exposes (cached 15 s, so frequent scrapes do not add database
queries or runner calls).

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `bekup_backup_runs_total` | counter | `status` (success, partial, failed, canceled, skipped), `trigger` (schedule, catch_up, manual, early), `engine` (postgres, mysql, mariadb, unknown) | Backup runs that reached a terminal status. Runs canceled before a worker picked them up are not counted. |
| `bekup_backup_run_duration_seconds` | histogram | `engine` | Start to terminal status of started runs (buckets 10 s … 12 h). |
| `bekup_backup_bytes_stored_total` | counter | | Bytes written to destinations (compressed and encrypted, every copy). |
| `bekup_backup_bytes_raw_total` | counter | | Uncompressed dump bytes. |
| `bekup_restores_total` | counter | `status` (succeeded, failed, canceled), `kind` (new_db, overwrite, unknown) | Finished restores (including the scratch restores of test restores). |
| `bekup_test_restores_total` | counter | `status` (passed, warnings, failed, error, canceled) | Finished test restores. |
| `bekup_retention_deleted_total` | counter | | Objects deleted by retention (dry runs not counted). |
| `bekup_retention_missing_total` | counter | | Objects retention found missing (deleted outside bekup; also a retention alert). |
| `bekup_scheduler_ticks_total`, `bekup_scheduler_tick_errors_total` | counter | | Scheduler ticks of this process, and the failed ones. |
| `bekup_scheduler_enqueued_total` | counter | `trigger` (schedule, catch_up) | Runs the scheduler enqueued. |
| `bekup_scheduler_skips_total` | counter | `reason` (still_running, unschedulable) | Occurrences not enqueued. |
| `bekup_scheduler_last_tick_timestamp_seconds` | gauge | | Unix time of this process's last tick (0: none / scheduler off). |
| `bekup_config_reloads_total` | counter | `status` (applied, unchanged, restart_required, rejected, failed) | Runtime config reloads of this process (D-200); a `rejected` or `failed` increase means a process kept its old configuration. |
| `bekup_queue_jobs` | gauge (*serve*) | `queue` (backup, restore, retention, notify, maintenance), `state` (River states) | River jobs; finished rows are pruned by River. |
| `bekup_runner_up` | gauge (*serve*) | | 1 when the runner answered its Health RPC over mTLS. |
| `bekup_runner_docker_ok` | gauge (*serve*) | | 1 when the runner reaches the Docker daemon. |
| `bekup_runner_active_runs` | gauge (*serve*) | | Containers the runner is running (only while up). |
| `bekup_cert_expiry_seconds` | gauge (*serve*) | `component` (runner_server, runner_client, runner_ca, postgres_ca, postgres_client) | Seconds until the earliest certificate in that file expires (negative once expired). `runner_server` comes from the runner; the others from the `bekup.toml` paths. |
| `bekup_db_pin_expiry_seconds_min` | gauge (*serve*) | | Seconds until the earliest pinned target-database certificate expires (absent without pins). |
| `bekup_backup_oldest_last_success_timestamp_seconds` | gauge (*serve*) | | Unix time of the stalest "latest successful run" across enabled jobs that have one. |
| `bekup_backup_last_success_timestamp_seconds` | gauge (*serve*) | `job` (slug) | Only with `metrics.per_job_labels`: the latest successful run per enabled job (at most the 1000 stalest jobs). |
| `bekup_monitor_db_ok` | gauge (*serve*) | | 1 when the last snapshot could read the app database. |
| `bekup_build_info` | gauge | `version`, `goversion` | Always 1. |
| `go_*`, `bekup_process_*` | | | Standard Go runtime and process metrics. |

Labels only ever take the values listed (anything else is reported as
`other`), so the number of series is fixed. **Per-job labels** add one
series per enabled job with the job slug as label value: slugs are names
chosen by admins and become visible to whoever can scrape, and the series
count grows with the installation, hence opt-in. Metrics never contain
user names, e-mail addresses, IDs, host names, error messages or secrets.

### Alert examples (PromQL)

```yaml
groups:
  - name: bekup
    rules:
      - alert: BekupBackupFailed
        expr: sum by (instance) (increase(bekup_backup_runs_total{status="failed"}[1h])) > 0
        labels: { severity: critical }
        annotations: { summary: "A bekup backup run failed in the last hour" }
      - alert: BekupBackupPartial
        expr: sum by (instance) (increase(bekup_backup_runs_total{status="partial"}[6h])) > 0
        labels: { severity: warning }
      # Missed backups: no successful run for 26 h (daily jobs). Without
      # per-job labels: the stalest job.
      - alert: BekupBackupMissed
        expr: time() - bekup_backup_oldest_last_success_timestamp_seconds > 26 * 3600
        labels: { severity: critical }
      # With metrics.per_job_labels = true, per job:
      # expr: time() - bekup_backup_last_success_timestamp_seconds > 26 * 3600
      - alert: BekupRunnerDown
        expr: bekup_runner_up == 0 or bekup_runner_docker_ok == 0
        for: 5m
        labels: { severity: critical }
      - alert: BekupMetricsDown
        expr: up{job="bekup"} == 0
        for: 5m
        labels: { severity: critical }
      - alert: BekupSchedulerStalled
        expr: max(bekup_scheduler_last_tick_timestamp_seconds) > 0 and time() - max(bekup_scheduler_last_tick_timestamp_seconds) > 600
        labels: { severity: critical }
      - alert: BekupCertificateExpiring
        expr: bekup_cert_expiry_seconds < 21 * 86400
        labels: { severity: warning }
        annotations: { summary: "bekup certificate {{ $labels.component }} expires within 21 days" }
      - alert: BekupPinnedCertificateExpiring
        expr: bekup_db_pin_expiry_seconds_min < 21 * 86400
        labels: { severity: warning }
      - alert: BekupQueueBacklog
        expr: sum(bekup_queue_jobs{queue="backup",state=~"available|retryable"}) > 20
        for: 30m
        labels: { severity: warning }
      - alert: BekupRetentionMissingObjects
        expr: increase(bekup_retention_missing_total[1d]) > 0
        labels: { severity: critical }
```

The missed-backup (dead-man) alerts (`backup.missed`, D-188) do not depend
on Prometheus; external alerting is still recommended because it also fires
when `serve` itself is down (`up == 0`).

The missed-backup check (every 5 minutes, in a worker) also records each
job's ongoing outage; the API exposes it as `Job.missed` /
`Job.missedSince` and the dashboard flags those jobs (D-196). A successful
run clears the flag at once; disabled, deleted and manual jobs never show
it. The flag needs a worker process to run, like the alerts.

### Dashboard data in the API

Besides `systemHealth`, the dashboard reads three aggregates (D-196):

- `destinationUsage` (admins and maintainers): bytes and number of the
  backup files bekup tracks as present on each destination, one grouped
  query. Files deleted by retention or found missing do not count; the
  numbers are what bekup wrote, not what the bucket reports (versions,
  lifecycle copies and foreign objects are not included).
- `Job.missed` / `missedSince` (see above).
- `restoresUpdated`: one live stream of every restore the caller may view
  (staff: all, owners: their jobs'), so no page polls for restores.

### Audit chain status

The hourly security check verifies the audit chain (new entries every
hour, the whole chain once a day per process) and stores the outcome;
admins see it on the audit page and through `auditChainStatus` (last
verification time, result, full or incremental, the finding), together
with the newest entry and the newest signed chain head (D-196). The stored
status is informational: whoever can write the app database could forge
it. The proof remains `bekup audit verify` against an exported signed head
(docs/security/audit.md). A failed verification also raises the critical
`audit_verify_failed` alert.

### Runner health in the API

Admins read the same snapshot through GraphQL `systemHealth` (operation
`monitor.system_health_view`, admin only): runner up/Docker/version/active
runs, certificate expiries, queue depth, earliest pin expiry, the stalest
last success, whether the scheduler runs on this instance and its last
tick. The dashboard's system-health tile (admins) shows it.

## Startup checks of `bekup runner`

`runner` refuses to start, with a clear error and exit code 1, when the
configuration is invalid, the listen address is a wildcard outside dev mode
without `listen.allow_wildcard` (D-098), the server certificate, key or
client CA cannot be loaded (TLS 1.3, ed25519, private CA only), the
certificate is not valid at this moment, or the health socket directory is
not a private (`0700`, owned by the runner user, not a symlink) directory.
It logs a warning when its certificate expires within 30 days. On SIGINT /
SIGTERM it stops accepting calls and gives in-flight calls 45 seconds
before cancelling them (compose `stop_grace_period` is 60s).

## Startup checks of `bekup serve` and `bekup worker`

`serve` (and `bekup worker`, which runs the same checks without binding a
listen address) refuses to start, with a clear error and exit code 1, when:

- the configuration is invalid (`bekup config validate` shows the same
  errors);
- the database connection fails, including TLS verification (D-100);
- it is connected as anything other than a plain `bekup_app`, or
  `bekup_app` is SUPERUSER/BYPASSRLS/CREATEROLE/REPLICATION or a member of
  `bekup_owner`, of such a role or of a predefined `pg_*` role (D-099);
- the database schema is **behind** this binary: run `bekup migrate up`
  (the compose `migrate` service does this before `serve` starts);
- the database schema is **ahead** of this binary: a newer bekup release
  migrated it. Run that release or a newer one; downgrades are not
  supported (migrations are forward-only);
- the master keyring (`security.master_key_file`) cannot be read or parsed,
  does not match the key-check values stored in the database, or does not
  open the instance signing key stored in the database (wrong keyring);
- `server.listen` cannot be bound.

Both also apply the admin SSRF allowlist stored in the database before
anything connects out (D-196); if it cannot be read, they do not start. A
stored list they cannot decode, or entries they reject (for example
loopback outside dev mode), are left out and logged at WARN (only the
narrower list applies). Every process keeps it current through a settings
event and re-reads it every 5 minutes; `bekup worker` opens its own
LISTEN connection (`application_name` `bekup-worker-listen:<instance id>`)
for that.

At every start `serve` logs each configuration warning at WARN, for example
plain HTTP (`server.base_url` is not https, D-095), weakened app-database
TLS (D-100) or a wildcard listen address (D-098). Read them.

## Break-glass account administration

The first admin of a new installation is created this way (D-113: there is
no web setup page; `serve` logs the command at WARN while no active admin
exists). Later, when nobody can sign in (lost admin password or second
factor, locked or compromised account), a host operator with access to `bekup.toml` and the database
credentials manages accounts with `bekup admin` (D-069, D-110, D-112). The commands
connect as `bekup_app` with the normal configuration (so they also work in
the `serve` container), print what they did and write an audit entry with
actor kind `cli`, the OS user and the hostname (`cli.admin.*`). Every such
action (like `bekup certs rotate`, `bekup audit sign-head` and `bekup reindex
--import`) also raises a security alert to all admins (inbox and e-mail,
with the command, OS user and host); the command writes it to the database
and a running `serve` delivers it within seconds (D-069, D-190).
`reset-password` and `reset-2fa` also alert the account itself.

| Command | Effect |
|---|---|
| `bekup admin create --email E --display-name N [--role R]` | Creates an account (role `admin` by default; `maintainer` or `user`) with the password you enter. The only way to create the first admin. |
| `bekup admin reset-password --email E` | Sets a new password, clears the lockout and revokes all sessions of the account. |
| `bekup admin reset-2fa --email E` | Lost-factor recovery: removes the account's TOTP, passkeys and recovery codes, clears its second-factor lockout and revokes all its sessions (a warning goes to stderr). If the 2FA policy requires a factor for its role, a new enrollment grace period starts now. Verify the person's identity out of band first. Admins do the same in the API with `resetUserSecondFactors` (step-up, not for their own account). |
| `bekup admin unlock --email E` | Clears the login lockout. |
| `bekup admin disable --email E` | Disables the account and revokes all its sessions. The last active admin cannot be disabled: create another admin first. |

Passwords are prompted twice on the terminal (hidden), or read from
`--password-file` (a file readable only by you; surrounding whitespace is
trimmed). There is no password flag, and the commands refuse to read a
password from a non-terminal stdin. The password policy applies; CLI-set
passwords are permanent. With compose the commands run in the one-shot
`admin` service ([details](installation/compose.md#account-administration-admin)):

```
docker compose run --rm admin reset-password --email admin@example.com
```

Review `cli.admin.*` entries in the audit log regularly: these commands
bypass step-up by design. Reference: [CLI](reference/cli/bekup_admin.md).

## Scaling background work

Background jobs (backups, restores, retention, notifications,
housekeeping) run on River queues in the app database (D-043, D-145).

- **Default:** `bekup serve` runs the HTTP server, the scheduler loop and
  the workers in one process.
- **Scale out:** run `bekup serve --no-worker` for the UI/API and one or
  more `bekup worker` processes for the jobs. Workers use the same
  `bekup.toml`, connect as `bekup_app` and run the same startup checks;
  they bind no port. Give worker processes their own runner client
  certificate (`bekup certs`, component `worker`).
- **Scheduler:** safe on every `serve` instance (`FOR UPDATE SKIP LOCKED` +
  unique River inserts), so several `serve` replicas never enqueue a run
  twice. Turn it off per instance with `scheduler.enabled = false`; it never
  runs in `bekup worker`. At least one `serve` must have it on.
- **Concurrency:** `[workers] backup_queue_workers`,
  `restore_queue_workers`, `retention_queue_workers`, `notify_queue_workers`
  and `maintenance_queue_workers` are per process. Size
  `database.max_conns` to at least their sum plus 4 (a WARN at startup says
  so otherwise). The global limits (`max_concurrent_runs`, default 4,
  backups and restores together; `max_runs_per_connection`, default 1)
  apply across all processes: every running backup holds a row in
  `run_slots` (taken under a transaction-scoped advisory lock, refreshed
  every 30 s, reclaimed after 2 min without a heartbeat). A run that finds
  no free slot stays `queued` and its River job is snoozed for about 15 s,
  so it does not occupy a queue worker while it waits. Every process
  applies its own configured limits to the shared table: keep them equal
  on all instances. `backup_queue_workers` above `max_concurrent_runs`
  only adds waiting.
- **Per-connection limit:** an admin can give a connection its own
  `maxConcurrentRuns` (1-64, at most `max_concurrent_runs`; task 3.13,
  D-158); it replaces `max_runs_per_connection` for that connection only
  and is stored in the database, so every process applies the same value.
  `max_concurrent_runs` still caps the total. Lowering either limit never
  stops active runs: new runs wait until the count drops below it. If you
  lower `max_concurrent_runs` below a connection's own limit, the
  connection is simply capped by the global limit (its
  `effectiveMaxConcurrentRuns` shows the clamped value); saving a new,
  higher value is refused. Restores take slots from the same per-connection
  pool as backups: with a limit of 1, a restore waits until a running backup
  of that connection has finished. See
  [Connections](guides/connections.md#concurrent-runs-per-connection).
- **Run logs:** `run_log_max_lines` (default 10000) and
  `run_log_max_line_bytes` (default 4096) cap what one backup run stores in
  `run_logs`; the run records how many lines were dropped. The run tables
  have no DELETE path for `bekup_app` (history); plan database space
  accordingly.
- **Timeouts:** an attempt never runs longer than its queue's timeout
  (`backup_timeout`, `restore_timeout`, `retention_timeout`,
  `notify_timeout`; housekeeping 15 min). `workers.backup_timeout`
  (default 6h) is also the default and the upper bound of every job's own
  `timeoutSeconds` (60 s up to `backup_timeout`): lowering it makes saving
  a job with a longer timeout fail validation, and a stored longer job
  timeout is still cut off at the queue timeout. A job still marked running after
  the longest of them plus one hour (a crashed worker) is rescued: retried
  if attempts remain (backups: 2 attempts, restores: never retried,
  notifications: 10), otherwise discarded.
- **Retention:** sweeps run after every successful/partial run and daily
  (River periodic job on the elected leader; like every periodic job it
  also runs once whenever a process becomes leader, e.g. on every start). `[retention]
  max_deletions_per_sweep` (default 500) caps deletions per job sweep;
  `dry_run = true` only logs plans (a startup WARN reminds you);
  `partial_cleanup_age` (default 24h) controls the daily removal of crash
  leftovers on disk/SFTP. Objects that vanished outside bekup and aborted
  sweeps are logged as `retention alert` WARN lines and alerted
  (`artifact.missing` to the job's audience and a security alert to every
  admin; [notifications](guides/notifications.md#security-alerts)). See
  [Retention](guides/retention.md).
- **Housekeeping:** besides the daily retention fan-out, the leader runs
  `schedule_skip_cleanup` (maintenance queue) every 6 hours and once at
  start; it deletes `schedule_skips` rows (scheduled occurrences that were
  not enqueued because a run was still active or the job could not be
  scheduled) older than 90 days. Every `serve` process deletes
  expired download tickets, including the sealed age identity of an unused
  one, every 30 seconds (a minute after their 60-second lifetime ends), and
  expired sessions periodically. None of them needs configuration.
- **Maintenance:** River's own reindexer is disabled because `bekup_app`
  does not own River's indexes. Run `REINDEX TABLE CONCURRENTLY river_job`
  as `bekup_owner` during maintenance if the table ever bloats.

## Background schedule

What runs on its own, where, and how often. None of it needs
configuration beyond the keys named. "Leader" means the River periodic job
is enqueued by the elected leader among all processes with workers (and
once whenever a process becomes leader, e.g. at start); the job itself
runs on any worker of its queue.

| What | Where | Interval | Does |
|---|---|---|---|
| Scheduler loop | every `serve` with `scheduler.enabled` | `scheduler.tick_interval` (15 s) | enqueues due backup runs (`backup` queue) |
| Notification dispatcher | every process with workers | 2 s | moves new `notification_events` rows of the outbox into River `notify_fanout` jobs |
| `notify_fanout` | `notify` queue, per event | on demand | resolves the recipients (access now, preferences, noise rules), writes inbox rows and `notification_deliveries`, enqueues `notify_deliver` |
| `notify_deliver` | `notify` queue, per delivery | on demand, up to 10 attempts | re-checks access, sends the e-mail or signed webhook, records the status, applies the webhook auto-disable policy |
| `missed_backup_check` | leader, `maintenance` queue | 5 min | emits `backup.missed` once per outage (reminder every 24 h) and records `Job.missed` (D-188, D-196) |
| `security_check` | leader, `maintenance` queue | 1 h | certificate and pin expiry alerts, audit chain verification (new entries hourly, the whole chain once a day per process; stored for `auditChainStatus`), posture reminders (D-190) |
| `notify_retention_digest` | leader, `maintenance` queue | 24 h | one `retention.summary` per job and UTC day with deletions (the last 3 complete days) |
| `notify_cleanup` | leader, `maintenance` queue | 24 h | deletes notification events and deliveries older than 90 days, inbox entries older than 180 days |
| `retention_sweep` (daily) | leader, `retention` queue | 24 h | a sweep for every job that has backups, plus the partial-file cleanup ([Retention](guides/retention.md)) |
| `test_restore_schedule` | leader, `maintenance` queue | 1 min | enqueues due scheduled test restores |
| `restore_stuck_sweep` | leader, `maintenance` queue | 5 min | fails restores stuck without a live worker |
| `temporary_connection_cleanup` | leader, `maintenance` queue | 5 min | removes expired temporary restore connections |
| `schedule_skip_cleanup` | leader, `maintenance` queue | 6 h | deletes `schedule_skips` rows older than 90 days |
| Restore housekeeping | every `serve` | 1 min (scratch sweep every 10 min) | deletes expired age-identity handoffs and stale staging directories |
| Download ticket cleanup | every `serve` | 30 s | deletes expired tickets and their sealed identities |
| Session cleanup | every `serve` | 1 h | deletes expired sessions |
| Audit head signing | every `serve` | `security.audit_head_sign_interval` (1 h) | signs the audit chain head when the chain has grown |
| SSRF allowlist sync | every process | on change, and every 5 min | applies the admin allowlist stored in `settings` (D-196) |
| Event stream re-check | every open stream | `graphql.stream_revalidate_interval` (30 s) | re-validates the session of each subscription |
| Health snapshot | `serve`, on scrape or `systemHealth` | cached 15 s | runner, Docker, queue and certificate gauges ([Metrics](#metrics)) |

The `notify` queue's concurrency is `workers.notify_queue_workers`, the
maintenance queue's `workers.maintenance_queue_workers`. Alerts that come
from periodic jobs (missed backups, certificate expiry, the audit chain)
therefore need at least one process with workers; with
`serve --no-worker`, run a `bekup worker`.

## Reloading the configuration

`serve` and `worker` apply a subset of `bekup.toml` without a restart
(D-200; the live keys are listed in
[configuration.md](configuration.md#reloading)). Runbook:

1. Edit `bekup.toml` on the host. Keep its ownership and mode (owned by the
   service user or root, not group- or world-writable): a file that fails
   the safety checks is refused.
2. Optionally check it first: `bekup --config /etc/bekup/bekup.toml config
   validate`.
3. Trigger the reload:
   - every process: **Admin → Settings → Configuration → Reload
     configuration** (admin, step-up). The page polls until every expected process
     (`expectedProcesses`, below) answered and lists each process with its
     status;
   - one process: `docker compose kill -s HUP serve worker` (or
     `kill -HUP <pid>`). SIGHUP no longer stops `serve` or `worker`; the
     runner ignores it as before.
4. Read the outcome per process:
   - `applied`: the live keys are active;
   - `unchanged`: the file matches the running configuration;
   - `restart_required`: listed keys changed that only apply after a
     restart (live keys in the same file were applied);
   - `rejected`: the file could not be read (`file`), is not TOML
     (`syntax`) or has an unknown key or invalid value (`invalid`, with the
     key names); nothing changed. Details are in that process's log
     (`config reload rejected the file`);
   - `failed`: a component refused the values (`apply`); nothing
     changed. When the outcome cannot be written to the database
     (`record`) nothing changes either; that outcome is only in the
     process log and `bekup_config_reloads_total{status="failed"}`, never
     on the page.
5. Restart the processes that report `restart_required` when convenient.

Every process records each reload in the audit log (`config.reloaded`,
`config.reload_failed`, system actor; the UI request adds
`config.reload_requested` with the admin). A change that weakens security
raises the `config_reload_weakened` security alert (once per request and
set of keys). Each process also writes a `loaded` row when it starts. The
page lists every process whose LISTEN connection is up (any age; its
`application_name` is `bekup-serve-listen:<instance id>` or
`bekup-worker-listen:<instance id>`) plus every process seen in the last 7
days; stopped ones are grouped below as "not listening" and need no
action. The progress waits only for the expected processes: those that
answered plus the listening ones whose startup row was written at most one
minute after the request (database time, the same rule the processes use
to catch up on missed requests at start and after a reconnect; D-200). A
process started later already runs the newer file and does not answer.
Rows older than 30 days are pruned hourly by `serve`, except the newest row
of a listening process. A process whose LISTEN connection happens to be
reconnecting during that prune may lose a newest row older than 30 days;
it reappears with its next reload (display only). A changed live key that
no running process uses (for example `retention.*` with only
`serve --no-worker` running) raises no alert and is applied when a process
that uses it next starts. `bekup_config_reloads_total{status}`
counts the outcomes per process.

Not reloaded: `runner.toml`, the master keyring, certificates and secret
files (restart, or use `bekup keys rotate` / `bekup certs rotate`).

## Shutdown

On SIGINT or SIGTERM `serve` stops accepting connections and lets in-flight
requests finish for up to `server.shutdown_timeout` (default 30s), then
closes the remaining connections. In parallel the workers (in `serve` and in
`bekup worker`) stop fetching jobs and let running jobs finish for up to
`workers.shutdown_timeout` (default 2m); after that the jobs are canceled
(a canceled backup is retried once later; restores are not) and the process
waits at most 30s more. Keep the container's stop grace period longer than
the larger of the two plus 30s (compose gives `serve` `stop_grace_period: 180s`).
