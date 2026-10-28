# Operations

> **Status:** partial. The health endpoints and the startup checks of
> `bekup serve` (task 1.1) are described below. Metrics, alerts and
> troubleshooting are filled in by tasks 5.4–5.6 and completed in task 7.5.

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
actor kind `cli`, the OS user and the hostname (`cli.admin.*`). From task 5.5
every such action also raises a security alert to all admins.

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
  leftovers on disk/SFTP. Watch for `retention alert` WARN lines
  (`retention_artifact_missing`, `retention_sweep_aborted`) until alert
  delivery (task 5.5) exists. See [Retention](guides/retention.md).
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

## Shutdown

On SIGINT or SIGTERM `serve` stops accepting connections and lets in-flight
requests finish for up to `server.shutdown_timeout` (default 30s), then
closes the remaining connections. In parallel the workers (in `serve` and in
`bekup worker`) stop fetching jobs and let running jobs finish for up to
`workers.shutdown_timeout` (default 2m); after that the jobs are canceled
(a canceled backup is retried once later; restores are not) and the process
waits at most 30s more. Keep the container's stop grace period longer than
the larger of the two plus 30s (compose gives `serve` `stop_grace_period: 180s`).
