# Architecture

> **Status:** partial (end of M6). Components, networks, trust boundaries and
> data flows below describe the design and the compose topology that exist
> today. Implemented: the CLI (`config`, `migrate`, `keys`, `certs`,
> `audit`, `admin`, `healthcheck`, `verify`, `decrypt`, `reindex`), the
> foundation (`internal/config`, `logging`, `db` with app-DB TLS
> verify-full and SCRAM channel binding, `crypto`, `certs`); in `serve`:
> the HTTP layer, the GraphQL API with subscriptions as Server-Sent Events,
> authorization, authentication (passwords, sessions, TOTP and recovery
> codes, passkeys, the 2FA policy and step-up), the audit log with signed
> chain heads and the pubsub bridge (M1); the runner, engines, SSH tunnel,
> SSRF guard, storage backends, connections and destinations (M2); the
> backup pipeline, job keys, scheduler and River queue, jobs, backup runs,
> run history, retention, downloads and the calendar (M3); restores,
> temporary restore connections, scheduled test restores and the offline
> recovery tools (M4); e-mail, webhooks, the notification outbox with
> fan-out and delivery, missed-backup and security alerts, Prometheus
> metrics and `systemHealth` (M5, data flow below); and the complete web
> UI (M6, [below](#web-ui-web)). Still to come: hardening, release
> artifacts and the remaining docs in M7. The page is completed in task
> 7.5.

bekup is one Go binary with several subcommands (D-025) and one container
image. The authoritative threat model is
[`SECURITY.md`](../SECURITY.md) (section 5: trust boundaries and data flows;
section 6: STRIDE per component). This page summarizes it. Decisions are in
[DECISIONS.md](DECISIONS.md).

## Components

| Component | Role | Status |
|---|---|---|
| Reverse proxy (`traefik` or `nginx` profile, opt-in) | Terminates TLS for the UI, HSTS, HTTP→HTTPS redirect, size and rate limits, unbuffered event streams (D-017, D-048, D-124). Traefik uses only its file provider; nginx is the unprivileged image plus a certbot companion. Neither mounts the Docker socket (D-093, D-094). | shipped (task 0.7b) |
| `bekup serve` | GraphQL API (POST + JSON), sessions, embedded React UI, scheduler, River workers (backup, restore, retention, notify), backup pipeline and uploads to destinations. Connects to Postgres as `bekup_app`. | HTTP layer, UI shell and health endpoints (task 1.1), GraphQL API and SSE subscriptions (1.2, 1.10, 2.13), authz (1.4), authentication incl. 2FA and step-up (1.5–1.9), audit with signed heads (1.3), scheduler loop and River queue (3.4), pipeline (3.1), backup run worker and `cancelRun` (3.6), retention worker (3.8), restore and test-restore workers (M4), notification dispatcher, fan-out and delivery workers, missed-backup and security checks, metrics listener (M5), the complete web UI (M6) |
| `bekup worker` | Optional scale-out process: only the River workers (no HTTP, no scheduler), same startup checks as `serve`, connects as `bekup_app` (task 3.4, D-145); builds the destination, runner-client, connection and job-key services the backup worker needs (D-147). | implemented (task 3.4); runs every River worker (backup, restore, retention, notify, maintenance), the notification dispatcher and its own metrics listener |
| `bekup migrate` | One-shot: applies the embedded goose migrations as `bekup_owner`, under an advisory lock (D-042, D-078). The only process that holds the owner credentials. | implemented (task 0.4) |
| `bekup runner` | The **only** component with Docker access (D-003). Small typed mTLS gRPC API, image and target allowlists from its own `runner.toml`, builds the hardened dump-container spec itself (D-029–D-033). | implemented (tasks 2.4–2.7a) |
| Dump containers | Short-lived official, digest-pinned DB images (`pg_dump`, `mysqldump`, `mariadb-dump`, restore clients). Alone on a per-run `internal` network, reaching exactly one target through the runner's proxy or SSH tunnel; secrets only through a stdin preamble (D-032). | implemented (tasks 2.5, 2.6) |
| Postgres app DB | bekup's own state: users, jobs, runs, encrypted credentials, the hash-chained audit log, River queue tables. Least-privilege roles `bekup_owner` / `bekup_app` (D-054, D-092). | implemented (task 0.4) |
| `certs-init` (profile `setup`) | One-shot, no network: creates the private CA and the per-component mTLS certificates (D-079, D-089). | implemented (task 0.6) |
| Offline CLI | `bekup verify` / `bekup decrypt` / `bekup reindex` (and `keys export-public`) for recovery without the bekup DB (D-040, D-164); `internal/offline`. | task 4.7 (done) |

## Compose topology and networks

```
                  ┌──────────── edge (published 80/443, ACME) ────────────┐
 browser ──HTTPS──> traefik | nginx (10.213.250.5)          certbot (nginx profile)
                       │
                       └── frontend (internal) ──> serve (10.213.250.10:8080)
                                                    │  ├─ backend (internal) ──> postgres  <── migrate
                                                    │  ├─ egress ──> S3 / SFTP / SMTP / webhooks (SSRF guard)
                                                    │  └─ runner-ctl (internal, mTLS gRPC)
                                                    │        └──> runner (10.213.251.10:7443) ── docker.sock ──> dockerd
                                                    │                │  └─ targets ──> target DBs, SSH bastions
                                                    │                └─ per-run internal network ──> dump container
```

| Network | Internal | Members | Purpose |
|---|---|---|---|
| `edge` | no | proxy, certbot | published ports and outbound ACME (D-093) |
| `frontend` | yes | proxy (fixed `10.213.250.5`), `serve` (fixed `10.213.250.10`) | the only path to the UI; `trusted_proxies = ["10.213.250.5/32"]` |
| `backend` | yes | `serve`, `migrate`, `postgres` | app database |
| `runner-ctl` | yes | `serve`, `runner` | mTLS gRPC only |
| `egress` | no | `serve` | destinations, SMTP, webhooks; every dial checked by the SSRF guard (D-055) |
| `targets` | no | `runner` | target databases and bastions, limited by the runner's target allowlist |

`serve` can never reach a target database or Docker; the runner can never
reach the app database. `serve`'s port is not published in production
(D-077). Details: [installation/compose.md](installation/compose.md).

**Dev stack.** By default `docker-compose.dev.yml` starts only the app
Postgres (TLS-only, published on `127.0.0.1` for dev), mailpit and adminer;
bekup runs natively on `127.0.0.1:8080` over plain HTTP (D-114, D-095:
allowed, with a startup warning and a permanent UI banner). The `full`
profile containerises `serve` and adds the runner, sample targets, an SSH
bastion, MinIO and SFTP (D-081). The proxy profiles stay opt-in, also in
dev.

## Trust boundaries

Summarized from [SECURITY.md §5](../SECURITY.md#5-trust-boundaries-and-data-flows):

| Boundary | Between | Main controls |
|---|---|---|
| B1 | browser ↔ reverse proxy | TLS 1.2/1.3, ECDHE+AEAD only, HSTS, redirect, size/rate limits (D-048, D-093) |
| B2 | proxy ↔ `serve` | `serve` only on internal `frontend`; forwarded headers only from `trusted_proxies`; CSP, Origin checks and headers set by `serve` too (D-050, D-082) |
| B3 | `serve` ↔ app DB | `bekup_app` DML only, migrations as `bekup_owner` in a separate container, envelope-encrypted secrets, append-only hash-chained audit log (D-046, D-054, D-078) |
| B4 | `serve` ↔ destinations, SMTP, webhooks | dial-time SSRF guard (`internal/netguard`: socket-address check after DNS, allowlist that never re-allows hard ranges), no proxy, no redirects, TLS ≥ 1.2, SFTP host-key pins, disk confined via `os.Root` (D-049, D-055, D-120) |
| B5 | `serve` ↔ runner | mTLS (TLS 1.3, private name-constrained CA, SPIFFE URI SAN check), typed RPCs (D-030, D-089) |
| B6 | runner ↔ Docker | image allowlist (digest-pinned), fixed hardened spec built by the runner (D-031) |
| B7 | dump container ↔ everything else | alone on a per-run internal network, no runner gRPC, no internet; secrets via stdin (D-029, D-032) |
| B8 | runner / dump client ↔ target DB | target allowlist, certificate pinning with a leaf-SPKI pre-flight, SSH host-key pins (D-034, D-035) |

The configuration files are a boundary as well: `serve` reads `bekup.toml`,
the runner reads `runner.toml`, so nothing `serve` controls can change the
runner's allowlists (D-019).

## Data flows

Summaries; the step-by-step flows are in
[SECURITY.md §5 "Data flows"](../SECURITY.md#data-flows).

- **Startup (implemented):** `migrate` applies migrations as
  `bekup_owner` and exits; `serve` then starts with the `bekup_app` URL only,
  checks its role (D-099) and refuses to run unless the schema version equals
  the newest embedded migration (D-078, D-102). See "HTTP layer" below.
- **Backup (task 3.6, implemented):** scheduler / `runJobNow` → River job
  (IDs only) + queued run → worker takes a concurrency slot, decrypts the
  connection secret → runner `Dump` over mTLS → per-run network +
  proxy/tunnel → dump stream → zstd → age → sha256 → fan-out to destinations,
  while the raw stream is verified (`VerifyDump`) → signed manifest. See
  "Backup runs" below.
- **Restore (tasks 4.1–4.5):** fresh 2FA check → `restores` row (queued;
  a recipient identity sealed into `restore_identity_handoff`, 15 min,
  never in River args) + River job `restore` (restore id only) → worker
  (task 4.2, D-166) takes a run slot for the target connection, re-checks
  the target (`LockRestoreTargetTx`), audits `restore.started` →
  `restore.Stage` (task 4.1, D-162): hold the
  artifact (D-155), take the handoff once, check scratch free space, stream
  the stored object into `<scratch_dir>/restore-<id>/` wrapped with an
  ephemeral in-memory age key while hashing → verify stored SHA-256 =
  record and the signed run manifest (`artifactverify.VerifyManifest`:
  signature, job/run binding, signed entries, encryption facts; shared with
  downloads) → `Staged.Open` (decrypt → decompress, bounded, digests
  re-checked at the end) → engine re-check against the signed manifest →
  the `EngineRestorer` of the target's engine through the runner (PG:
  `CreateDatabase` of `<db>__bekup_restore_<id>`, `Restore` with
  `pg_restore --exit-on-error --no-owner`, `RenameSwapDatabase`,
  `DropTempDatabase` of the old or, on failure/cancel, the temporary
  database; MySQL/MariaDB in place after a warning) → `Cleanup` on every
  path, plus a scratch sweeper → finish (sanitized code, log counts,
  warnings), `restore.finished`, notification hook. Status changes and log
  batches (`restore_logs`) publish IDs on `bekup_restore_events`.
  Verification failures are audited (`restore.verification_failed`) and
  alerted.
- **Download (task 3.9, D-150):** step-up → `createDownloadTicket`
  (single-use, 60 s, session-bound; sha256 only in `download_tickets`; an
  age identity for plain recipient downloads sealed into the row) →
  same-origin `GET /api/download/{ticket}` (role/ownership re-checked,
  manifest re-verified) → streamed raw or plain file with `no-store`, the
  last 64 KiB held back until the digests match (`internal/downloads`).
- **Notifications (tasks 5.1–5.3, D-161, D-183):** producers call
  `notify.Emit` in the transaction of their state change (runstate for
  backup runs; the `audit.Observer` maps audited job, connection and
  security actions; restores, test restores, retention anomalies and
  destination tests emit after commit) → `notification_events` outbox →
  dispatcher (processes with workers, every 2 s) → River `notify_fanout`
  (recipients = active accounts the authz policy allows to see the subject
  now; preferences per-job > per-event > D-161 defaults; mandatory security
  events; started-event collapse; per-user flood rule) → inbox rows +
  `notification_deliveries` (idempotency key per event, recipient and
  channel) → River `notify_deliver` (access re-checked, SMTP / HMAC
  webhooks through the SSRF guard, retries, webhook auto-disable). Daily
  `notify_retention_digest` and `notify_cleanup` on the maintenance queue;
  `missed_backup_check` every 5 minutes emits `backup.missed` (dead-man,
  `internal/notify/missed`, D-188). Security alerts (task 5.5, D-190):
  audited security actions map to the mandatory `security.system` /
  `security.account` events in the audit entry's transaction (also for
  `bekup admin|certs|…` CLI entries, which only write the outbox); the
  hourly `security_check` (maintenance queue) adds certificate/pin expiry,
  audit chain verification and posture reminders.

```
producer tx ── state change + notification_events row (+ audit entry)
   dispatcher ── one tx: River notify_fanout {event_id} + dispatched_at
   fan-out ──── one tx: inbox rows + deliveries + River notify_deliver {delivery_id} + fanned_out_at
   deliver ──── re-check access ── send (SMTP / webhook) ── record status, counters, auto-disable
```
- **Offline recovery (task 4.7, D-164):** `bekup verify` / `bekup decrypt`
  on an operator machine against escrowed instance public keys, no bekup DB
  or keyring needed; `bekup reindex` lists and verifies a destination
  read-only and, with the DB, imports fully verified runs of an existing
  job (audited). See [recovery.md](recovery.md).

## HTTP layer (`bekup serve`)

Implemented in task 1.1 (D-102); packages `internal/app` (composition and
startup) and `internal/httpserver` (router and middleware).

**Startup.** `bekup serve` loads `bekup.toml`, logs every configuration
warning at WARN (plain HTTP, weakened DB TLS, wildcard listen), then
`app.New`:

1. opens the `bekup_app` pool (TLS per `database.tls_mode`, D-100);
2. `db.CheckAppRole`: the session must be a plain `bekup_app` (D-099);
3. the schema check: `bekup_app` cannot read `goose_db_version`, so it calls
   the `SECURITY DEFINER` function `bekup_schema_version()` (owner
   `bekup_owner`, pinned `search_path`, EXECUTE for `bekup_app` only,
   migration 00006) and compares it with the newest embedded migration.
   Behind or ahead: refuse to start (D-078);
4. loads the master keyring (fail closed) and checks that it opens the
   stored instance signing key, if any;
5. outside dev mode with a plain-http `base_url`, records the audit entry
   `system.insecure_transport_started` (D-095; fail closed);
6. binds `server.listen`.

Only then does `app.Run` start the components. Each implements
`app.Component` (`Name`, `Run(ctx)`) and runs in an errgroup: the first
failure or SIGINT/SIGTERM stops all of them; the HTTP server drains within
`server.shutdown_timeout`. Today's components are the HTTP server and the
audit chain-head signer (D-101, every `security.audit_head_sign_interval`).
GraphQL (`internal/graph`, below) is mounted through
`httpserver.Options.GraphQL`; River workers (3.4) and the scheduler (3.3)
are added as further components.

**Middleware**, outermost first, applied to every route:

| # | Middleware | Behavior |
|---|---|---|
| 1 | request ID | fresh 128-bit `crypto/rand` ID, `X-Request-ID` response header; client IDs ignored |
| 2 | client IP | peer address; `X-Forwarded-For`/`-Proto` only when the peer is in `server.trusted_proxies` (walked right to left, trusted hops skipped); all forwarding headers then removed from the request |
| 3 | access log | one slog line per request; no query strings; ticket/link paths cut to their prefix |
| 4 | recovery | panic → generic 500, stack only in the log |
| 5 | security headers | exact D-082 CSP, `nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Permissions-Policy`, COOP/CORP `same-origin`, HSTS only when `base_url` is https (D-095), `Cache-Control: no-store` by default |
| 6 | rate limit | per-client-IP token bucket (IPv6 per /64), bounded LRU; 429 + `Retry-After` |
| 7 | Origin check | non-GET/HEAD/OPTIONS requests (incl. event streams) and upgrade requests (none is served) need `Sec-Fetch-Site: same-origin` and/or an `Origin` equal to the `base_url` origin (dev mode also accepts the Vite origins `http://localhost:5173`, `http://127.0.0.1:5173`); 403 otherwise |
| 8 | body limit | `server.max_body_bytes`; 413 |

**Routes.** `GET /healthz` (liveness), `GET /readyz` (DB ping + schema
version), `/graphql` (exactly; sub-paths 404), `/api` (404 until its task
mounts a handler), and
the embedded SPA for everything else: content-hashed files under `/assets/`
are cached `immutable` for a year, other files are `no-cache` with an ETag,
and unknown paths fall back to `index.html` only for GET/HEAD requests that
accept `text/html` and are outside `/api`, `/graphql`, `/healthz`, `/readyz`
and `/assets/`.

**Timeouts.** `http.Server` uses `server.read_header_timeout`,
`read_timeout`, `write_timeout`, `idle_timeout` and `max_header_bytes`.
`write_timeout` bounds every response, so long-lived routes (subscription
event streams, task 2.13; artifact downloads, task 3.9) must lift it per
request with `http.ResponseController.SetWriteDeadline`
(`httpserver.LiftDeadlines` lifts the read and write deadlines of an event
stream, which then bounds each write itself).

Health endpoints and the container healthcheck are described in
[operations.md](operations.md#health-endpoints).

## GraphQL API (`internal/graph`, task 1.2)

gqlgen, schema first (D-050, D-105). The schema lives in
`internal/graph/schema/*.graphqls` (one file per feature area; the shared
file defines `scalar Time`, `scalar UUID`, `enum Role`, the Relay
`PageInfo` and the access directives); `just gen` regenerates
`internal/graph/generated`, `internal/graph/model/models_gen.go`, the
resolver stubs in `internal/graph/resolvers` and the web client types. The
reference copy is [reference/schema.graphql](reference/schema.graphql).

**Transport.** `POST /graphql` with `Content-Type: application/json` only:
other methods get 405, other content types (multipart, form-encoded,
`application/graphql`, ...) 415, bodies above `graphql.max_body_bytes` 413,
and anything but one JSON request object (batches, unknown fields such as
persisted-query ids) 400 without echoing the body. The HTTP middleware
(Origin check, rate limit) runs first. A request that accepts
`text/event-stream` is a subscription stream (below); everything else gets
one JSON response. No GET, no WebSocket, no APQ. The transports are
`internal/graph`'s own on gqlgen's executor (gqlgen's transport package
links a WebSocket library; D-124).

**Limits.** `graphql.max_depth` (fields, fragments expanded),
`graphql.max_complexity` (1 per field by default), a parser token limit
(10 000) and an LRU cache of 1000 parsed documents. Violations fail the
whole operation (HTTP 422) with `extensions.code` `QUERY_TOO_DEEP` or
`COMPLEXITY_LIMIT_EXCEEDED`.

**Introspection** is allowed in dev mode, otherwise only when the access
hook `IntrospectionAllowed(ctx)` says so: a request with a valid session
whose principal is not restricted (no pending second factor, temporary
password or required 2FA enrollment; D-118). Anonymous queries selecting `__schema`/`__type` fail with
`INTROSPECTION_DISABLED`; `__typename` always works. Field suggestions
("Did you mean ...") are off outside dev mode.

**Access control** is deny by default. Every root field carries
`@authenticated`, `@hasRole(roles: [...])` or `@stepUp`, or is listed in
`graph.PublicRootFields` (today `Query.serverInfo` and the rate-limited
`Mutation.login`), and every guarded
root field maps to an `authz.Operation` in `graph.RootFieldOperations`;
`serve` refuses to start otherwise. A root-field middleware denies unmapped
fields at runtime and runs the policy precheck of the field's operation
before the directives and the resolver (see Authorization below).

**Credential operations are alone** (D-117). Root fields that verify,
set or reset credentials or run argon2id are listed in
`graph.SingleRootFields`; an operation that selects one must select
exactly one root field (aliases, repeats and fragment spreads counted,
`__typename` ignored), otherwise it fails with `VALIDATION` before any
resolver runs. `serve` refuses to start when a public mutation or a field
of a credential operation (`graph.CredentialOperations`) is missing from
the list. Every root field beyond the first of any operation costs one
more token of the caller's per-user rate limit.

### Subscriptions as Server-Sent Events (tasks 1.10, 2.13; D-052, D-108, D-124)

```
browser ──POST /graphql, Accept: text/event-stream, Origin, cookie──▶ HTTP middleware (Origin, rate limit, body limit, session)
        ◀──200 text/event-stream: next … complete──  graph.serveStream (Origin again, principal, caps, transport gate)
                                          │ streams.Conn: principal of the request + session watcher
auth.Sessions ──RevokeTx/RevokeAllTx/RotateTx──▶ pubsub.Publisher ──▶ local Bus (at once)
                                          └──pg_notify (on commit)──▶ Postgres ──LISTEN──▶ Listener (every instance) ──▶ Bus
Bus ──▶ streams.Hub ──▶ Conn: re-check session ──▶ final error event (UNAUTHENTICATED / FORBIDDEN / SESSION_ROTATED / UNAVAILABLE) + complete
```

- **Transport.** One `POST /graphql` with `Content-Type: application/json`
  and `Accept: text/event-stream` per subscription (graphql-sse,
  "distinct connections" mode): `next` events carry execution results, a
  `complete` event ends the stream, comment lines are keepalives
  (`graphql.stream_keepalive_interval`). It is an ordinary POST, so the
  Origin check, rate limits, body limit and session middleware apply; the
  handler additionally requires an allowed `Origin`, a cookie session (401
  without), no pending second factor (401) or required enrollment (403)
  and a free slot (`graphql.stream_max_per_session` and
  `graphql.stream_max_per_user` → 429, `graphql.stream_max_total` → 503;
  503 after shutdown). Only subscriptions run as streams and subscriptions
  only as streams (FORBIDDEN otherwise). Refusals are plain JSON before any
  stream starts. The read and write deadlines are lifted per stream, each
  write has its own 30s deadline; responses carry `Cache-Control:
  no-store` and `X-Accel-Buffering: no`. Browsers open at most 6 HTTP/1.1
  connections per origin, so the proxies serve HTTP/2 and pages use at most
  2 to 3 subscriptions.
- **Session watcher (`internal/graph/streams`).** Each stream re-checks its
  session by id and rebuilds its principal like the session middleware,
  including the 2FA enrollment check (`httpserver.SessionsResolver.Check` →
  `auth.Sessions.Check`, no touch: a stream does not extend the idle
  timeout; D-118) every `graphql.stream_revalidate_interval`, on each
  session event for its session or user (with short follow-ups, since the
  local event can precede the commit), after lost events and before
  delivering an event once the last check is older than
  `graphql.stream_session_check_ttl`. New step-up and second-factor times
  refresh the principal used for delivery-time checks. A stream the server
  ends gets a final GraphQL error event, then `complete`:
  `UNAUTHENTICATED` session ended, `FORBIDDEN` user/role/AAL or a
  restriction (pending second factor, temporary password, required
  enrollment) changed, `SESSION_ROTATED` (reopen with the new cookie),
  `UNAVAILABLE` check failed (fail closed) or shutdown. The web client
  (`graphql-sse` Apollo link) sends the user to `/login` on
  `UNAUTHENTICATED`/`FORBIDDEN`, reopens at once after `SESSION_ROTATED`
  and with backoff after `UNAVAILABLE` or network errors.
- **Session events and `internal/pubsub`.** The session store publishes
  `revoked`/`rotated`/`user_revoked` inside the revoking transaction:
  in-process at once and via `pg_notify` to every instance on commit. A
  dedicated LISTEN connection per instance (reconnect with backoff and
  jitter, resync after reconnect) feeds the bus. Payloads are IDs only
  (`kind:user_id:session_id`); receivers always re-check the database.
  Run events (`bekup_run_events`, 3.7) and in-app notifications (5.3b)
  reuse the bridge with their own channels.
- **Delivery-time authz.** Subscription resolvers send through
  `streams.Deliver` + `streams.Authorize(policy, op, ...)`: the session check
  and `Policy.Authorize` run for every event, so a caller who loses access
  stops receiving events. `Subscription.accountEvents` (own sessions and
  inbox notifications in one stream, D-197) is the account stream.
- **CSP.** Streams are same-origin `fetch` requests covered by
  `connect-src 'self'`; `pnpm csp-check` verifies in Chrome that a
  same-origin stream opens and a cross-origin one is blocked.

### Authentication (`internal/auth`, task 1.5, D-047, D-095, D-107)

- **Passwords.** argon2id in the PHC string format
  (`$argon2id$v=19$m=…,t=…,p=…$salt$hash`) with the `security.argon2_*`
  parameters (at least m=64 MiB, t=3, p=1; config can only raise them);
  verification is constant time, and a successful login rehashes a hash
  made with other parameters. A semaphore (`security.argon2_max_concurrent`)
  bounds concurrent hashes; callers wait at most 2 s (and at most 4 ×
  the bound may wait) before getting `RATE_LIMITED`. Stored hashes are
  parsed strictly with upper bounds, so a planted hash cannot make a
  verification arbitrarily expensive.
- **Password policy** for new passwords: at least
  `security.password_min_length` characters (≥ 12) and at most 1024, no
  control characters, not on the embedded top-100k common-password list
  (SecLists, case-insensitive), not containing the account's e-mail
  address, its local part or a word of the display name, and a zxcvbn
  score of at least `security.password_min_score` (≥ 3).
- **Sessions.** 256-bit `crypto/rand` tokens in the cookie; only
  sha256 of the token is stored (`sessions.token_hash`). Cookie
  `__Host-bekup_session` (`Secure; HttpOnly; SameSite=Strict; Path=/`, no
  `Domain`, browser-session lifetime) when `server.base_url` is https;
  `bekup_session` without `Secure` and the `__Host-` prefix only when it is
  plain http (D-095); the other name is ignored. Idle expiry
  (`security.session_idle_timeout`, 30 m) slides, written at most every
  `security.session_touch_interval` (1 m) together with last-seen IP and
  User-Agent (truncated to 256 characters), and never beyond the absolute
  expiry (`security.session_absolute_timeout`, 12 h). A login always
  creates a new session (and revokes the one the browser presented); a
  privilege change rotates it (`Sessions.RotateTx`, `ElevateAAL` for an
  AAL increase) keeping the absolute expiry; a password change
  revokes every session of the user and issues a new one to the caller.
  A login of an account **without** a second factor counts as a fresh
  step-up (`stepup_at`); for accounts with one only the completed second
  factor does (two-step login below, D-112).
- **Session middleware** (`internal/httpserver`, on `/graphql` and `/api`
  only): no cookie means anonymous without a database lookup; a cookie
  resolves to the session and its user in one indexed query; unknown,
  expired, revoked or disabled → anonymous and the cookie is cleared (a
  lockout does not affect existing sessions); two session cookies → anonymous and cleared; a database error →
  503. Authenticated requests then pass the per-user limit
  (`server.user_rate_limit_*`). The middleware also puts a cookie setter
  and the client facts into the context for the resolvers. Every resolved
  principal passes the 2FA policy's enrollment check
  (`httpserver.SessionsResolver.Enrollment` →
  `auth.TwoFactorPolicies.ApplyEnrollment`, task 1.9).
- **Login protection.** A per-IP login limiter
  (`security.login_rate_limit_*`, on top of the server-wide limit) answers
  `RATE_LIMITED`. Every failure answers the same `UNAUTHENTICATED`
  "invalid email or password"; unknown, disabled, locked and password-less
  accounts run a dummy argon2id verification so they take as long as a
  wrong password. Before every argon2id verification (login, password
  step-up, password change) a per-account limiter at the same rate is
  taken (`user:<id>`; unknown addresses by e-mail, so the limiter reveals
  nothing). `security.lockout_threshold` consecutive wrong passwords
  lock the account for `lockout_base_duration`, doubling per further
  failure up to `lockout_max_duration`. Each attempt is reserved before
  it is verified by one conditional UPDATE that counts it only while the
  account is unlocked and sets the lock when the threshold is reached
  (`ReserveLoginAttempt`), so concurrent guesses never exceed the
  threshold (D-117); attempts while locked do not check the password and
  do not extend the lock; a success resets the counter. A locked
  account's own step-up or password change answers `RATE_LIMITED` without
  running argon2id. Refusals while locked are audited once per lock window;
  the number of further refusals follows in the next entry
  (`refused_while_locked`).
  Lockouts are audited; the security alert comes from the audit entry
  (D-190).
- **GraphQL.** `login(email, password): LoginResult` (public, returns the
  user (null while `secondFactorRequired`), `secondFactorRequired`, `availableFactors`,
  `passwordChangeRequired` and `twoFactorEnrollmentRequired`),
  `logout`, `me`, `mySessions`, `revokeSession(id)` and
  `changePassword(currentPassword, newPassword)` (`@stepUp`, needs the
  current password; a wrong one counts towards the lockout).
- **Audit.** Login success and failure (reason code only: `unknown_user`,
  `bad_password`, `disabled`, `locked`, `no_password`, `invalid_input`;
  never the password or an unknown e-mail address), lockouts, logout,
  session revocation and password changes (and failed attempts) are
  recorded; where the event changes state, in the same transaction.
  Failed logins have actor kind `system` with the account as target.
- **Housekeeping.** `serve` deletes sessions that expired or were revoked
  more than 7 days ago once an hour.
- **TOTP** (task 1.7, D-111; `auth.TOTP`). RFC 6238 with SHA-1, 6 digits
  and 30 s steps (`github.com/pquerna/otp`), 160-bit secrets from
  `crypto/rand`. Enrollment is two-phase and needs step-up:
  `beginTotpEnrollment` seals the new secret into
  `users.totp_pending_enc` (AAD `users:<id>:totp_pending`, 10 minutes) and
  returns it once as otpauth URI, inline SVG QR code (rendered by
  `auth.QRCodeSVG`: one `rect` + one `path`, no `data:` URI, CSP-safe)
  and base32 key; `confirmTotpEnrollment(code)` verifies a code, re-seals
  the secret as `users.totp_secret_enc` (AAD `users:<id>:totp_secret`)
  and issues recovery codes. `VerifyTOTP` accepts ±1 step, compares in
  constant time and records the step with a conditional update
  (`totp_last_step < step`), so every step is accepted at most once, even
  under concurrency. `disableTotp` (step-up) asks the
  `FactorRemovalGuard` (the 2FA policy) first.
- **Recovery codes.** 10 × 60-bit codes (`xxxx-xxxx-xxxx`, Crockford
  base32), issued on TOTP confirmation and by `regenerateRecoveryCodes`
  (step-up); a new set replaces the old one and is shown once. Only
  HMAC-SHA256 under a keyring subkey (`crypto.Keyring.MAC`, with the key
  ID per row) is stored in `recovery_codes`; `UseRecoveryCode` spends a
  code with a conditional update (single use). They are deleted when the
  last second factor is removed.
- **Second-factor failures** count in `users.mfa_failed_count` (separate
  from the password counter) with the `security.lockout_*` backoff; every
  check is reserved atomically before the code is looked at
  (`ReserveMFAAttempt`, like the password lockout); while
  `mfa_locked_until` is in the future, TOTP, recovery-code and passkey
  checks are refused (`RATE_LIMITED`, audited once per lock window). All enrollment,
  verification, recovery-code and disable events are audited; recovery
  code use, removal and locks are audited and alerted through their audit
  entries (D-190). `auth.SecondFactors` aggregates the factor checkers (TOTP,
  passkeys) for `authz.SecondFactors`, the login and the 2FA policy.
- **Passkeys** (task 1.8, D-109; `internal/auth/passkey`,
  `github.com/go-webauthn/webauthn`). The relying party comes from
  `server.base_url`: RP ID = its host name, origin = its origin (plus
  `http://localhost:5173` in dev mode on localhost). Passkeys need a secure
  context (D-095): with an `http` base URL on any host but localhost, or an
  IP-address host, they are unavailable, `webauthnStatus` says why, every
  ceremony is refused and `serve` logs a warning. Registration
  (`beginPasskeyRegistration` / `finishPasskeyRegistration`, step-up) uses
  attestation `none`, residentKey and userVerification `preferred`, the
  existing passkeys as `excludeCredentials` and a 3-minute timeout. The
  ceremony state (challenge etc.) lives only server-side in
  `webauthn_ceremonies`, bound to the session and user, and is consumed by
  the finish step whatever its outcome (single use, no cross-session use,
  expiry). The library checks origin, RP ID, challenge, signature and
  ownership. Passkeys (`webauthn_credentials`, at most 20 per user, name,
  transports, AAGUID, backup flags, sign count, last use) are listed,
  renamed and deleted (step-up; the `FactorRemovalGuard` decides about the
  last factor; recovery codes go with the last factor). The assertion API
  (`BeginAssertion` / `FinishAssertion` / `VerifyAssertion`, bound to a
  session) serves the two-step login and step-up; a signature counter that does not
  increase (unless always 0) is refused as a possible clone, recorded on
  the passkey, audited and alerted through the audit entry (D-190). Every registration step, assertion outcome, rename and
  removal is audited. GraphQL passes WebAuthn options and responses as the
  `JSON` scalar (objects of at most 32 KiB).
- **Two-step login** (task 1.9, D-112; `auth.SecondFactorFlow`). For an
  account with a second factor, `login` creates a session flagged
  `sessions.mfa_pending` (AAL1, no step-up, 5-minute absolute expiry) and
  answers `secondFactorRequired` with the usable factors. Its principal
  (`MFAPending`) may only call `beginPasskeyLogin`,
  `completeSecondFactor(method, code | assertion)` and `logout`
  (`SECOND_FACTOR_REQUIRED` otherwise; no event streams). Completion verifies
  TOTP, a recovery code or a passkey assertion bound to the pending
  session, then rotates it (`Sessions.CompleteSecondFactorTx`: rotated
  event, new token, AAL2, `stepup_at` = now and, for TOTP or a passkey
  assertion with the User Verified flag (not a recovery code or a
  user-presence-only passkey, D-116), `aal2_at` = now). Every rejected factor (passkey assertions
  included) counts towards the second-factor lockout.
- **Step-up** (`beginStepUp` / `completeStepUp(method, ...)`): accounts
  with a factor must use it (TOTP or a user-verified passkey set
  `stepup_at` and `aal2_at`, rotating an AAL1 session to AAL2; a recovery
  code or a user-presence-only passkey sets only `stepup_at`, a recovery
  code also raises the recovery-code alert); accounts without one
  re-enter the password (failures count towards the login lockout).
  `@stepUp` needs `stepup_at` within `security.stepup_max_age` (≤ 5 min);
  restores need `aal2_at` within it on an AAL2 session.
- **2FA policy** (`auth.TwoFactorPolicies`, `settings` key
  `security.two_factor_policy`): required for all or per role, grace
  period 0-90 days from the latest of: when the requirement started for
  the role, the account's creation and its last second-factor reset
  (`users.mfa_reset_at`). Default (user decision): nothing required, 7-day
  grace once a role is required. Admin-only
  `twoFactorPolicy` / `updateTwoFactorPolicy` (step-up, audited, weakening
  alerted). After the grace period an account of a required role without
  a factor gets the restricted principal `EnrollmentRequired` (only the own
  account, step-up, factor enrollment and logout;
  `REQUIRES_2FA_ENROLLMENT` otherwise); within it, `me` reports
  `twoFactorEnrollmentDeadline`. The policy is also the
  `FactorRemovalGuard`: the last factor of a required role cannot be
  removed (`CONFLICT`). TOTP always works (also on plain http), so the
  policy can always be met.
- **Lost-factor recovery** (`UserAdmin.ResetSecondFactors`):
  `resetUserSecondFactors(userId)` (admin, step-up, not the own account)
  or `bekup admin reset-2fa --email` removes TOTP, passkeys (and open
  ceremonies) and recovery codes, clears the second-factor lockout, sets
  `mfa_reset_at` and revokes every session in one audited transaction; the
  alert comes from the audit entry (D-190).
- **First admin** (D-113): only `bekup admin create` on the host; there is
  no web setup flow. `serve` logs the command at WARN while no active admin
  exists and keeps running.

### Authorization (`internal/authz`, task 1.4, D-106)

- **Principal.** The session middleware (task 1.5) attaches an
  `auth.Principal` (user, role, session, AAL, `stepup_at`, `aal2_at` and
  the restrictions `MFAPending`, `PasswordChangeRequired`,
  `EnrollmentRequired`) to the request context. A restricted principal may
  only run the operations of its restriction (D-110, D-112). Without one, every guarded field answers
  `UNAUTHENTICATED`; introspection needs a principal too.
- **Operation catalog and policy.** Every API operation is an
  `authz.Operation` with one rule: the roles allowed, the backup-owner
  grant (own jobs only, or listings filtered to own jobs), the freshness
  (step-up within `security.stepup_max_age`, or a passkey/TOTP check for
  restores) and whether a restore target is required. Admins may do
  everything; maintainers everything except connection/destination CRUD
  and secrets, pins and host keys, user management, settings, the audit
  log, passphrase export, opt-outs and downloads; backup owners (role
  `user` in `job_owners`) view, run, download and restore their own jobs.
  Owner rights never apply to maintainers (D-067).
- **Checks.** `Policy.Authorize(ctx, op, resources...)` checks, in order:
  authenticated (`UNAUTHENTICATED`), role or ownership (`FORBIDDEN`),
  restore target (temporary connections and privileged restore roles admin
  only; owners only their own databases or a new database on their
  connections), freshness (`STEP_UP_REQUIRED`, `REQUIRES_2FA_ENROLLMENT`).
  Ownership and enrolled factors come from interfaces implemented by later
  tasks; until then they deny.
- **GraphQL.** `graph.PolicyAccess` implements the directives on top of the
  policy; resolvers of owner-scoped operations call `Authorize` with the
  job loaded from the database, list resolvers filter by `JobScope`.
- **Matrix test.** `internal/authz/matrix_test.go` pins the outcome of every
  operation for admin, maintainer, maintainer-owner, owner, non-owner and
  anonymous; it fails when an operation has no rule or no row, and the
  `graph` tests fail when a root field is neither public nor mapped.
- **Audit log.** `Query.auditLog` (admin only) pages through
  `audit.Auditor.List` with opaque cursors and filters on action, actor,
  target and time.

**Errors.** Resolvers return `apierror.Error` values for user-facing
errors, with `extensions.code` one of `UNAUTHENTICATED`, `FORBIDDEN`,
`STEP_UP_REQUIRED`, `REQUIRES_2FA_ENROLLMENT`, `VALIDATION` (plus
`extensions.field`), `NOT_FOUND`, `RATE_LIMITED`, `CONFLICT`. Every other
error and every panic is masked as `"internal error"` with code `INTERNAL`
and `extensions.requestId` (the `X-Request-ID` of the response); the real
error is logged with that request ID. Transport rejections use
`BAD_REQUEST`.

**Public data.** `Query.serverInfo { version insecureTransport devMode }`
is the only anonymous query (`Mutation.login` the only anonymous
mutation); the UI uses `insecureTransport` (and the
page's own protocol) for the permanent "Connection is not encrypted (no
HTTPS)" banner (D-095).

## Storage (`internal/storage`, task 2.8, D-121)

Every destination backend implements `storage.Destination`: `Put`
(streaming, atomic, never overwrites, optional expected SHA-256, object-lock
options that fail closed where unsupported), `Get`, `Stat`, `List` (pages of
up to 1000 in S3 key order, segment-aligned prefixes), `Delete` (idempotent;
refused in no-delete mode or under a lock), `Test(prefix)` (write, read and
delete probe below the job prefix) and `Capabilities`. Errors are wrapped
sentinels; users only see the fixed text of `storage.Classify(err)`.

Object keys are validated values built as
`{prefix}/{job-slug}/{2026-10-26T030000Z}/{enc(db)}.{ext}` plus
`manifest.json`; `enc(db)` is a canonical percent encoding (hashed when
over 180 bytes). Keys never contain `..`, absolute paths, dot-prefixed
segments, control or non-ASCII bytes.

The disk backend is implemented: all access goes through an `os.Root` on
`storage.disk_root` (plus an optional subdirectory), files are 0600 and
directories 0700, symbolic links are never followed, and an object is
written as `.<random>.partial`, fsynced and published with an exclusive
hard link. Crash leftovers are removed by `CleanupPartials`. S3 (task 2.9)
and SFTP (task 2.10) run the same conformance suite
(`internal/storage/storagetest`).

## Backup pipeline (`internal/pipeline`, task 3.1, D-144)

`pipeline.Run(ctx, src, spec)` streams one dump (one database or the
globals) from the runner to every destination of the job:

```
runner stream ─▶ count + SHA-256 (raw) ─▶ zstd ─▶ age ─▶ count + SHA-256 (stored)
                                                           │ immutable chunks (1 MiB)
                                       ┌───────────────────┼───────────────────┐
                                  [8 chunks]          [8 chunks]          [8 chunks]
                                  Put (disk)           Put (S3)           Put (SFTP)
```

- **Compression:** zstd level 1–22 (default 3) with 1–8 encoder goroutines
  (default 2). **Encryption:** age with the job's versioned passphrase
  (scrypt work factor 18, bounded at 20) or its X25519 recipients; `none`
  only with the admin opt-out flag. The configuration comes from
  `jobkeys` through `pipeline.Encryptor` (task 3.2).
- **Fan-out:** every destination has its own bounded buffer; chunks are
  shared, so memory is at most (buffer + 2) × chunk per artifact. A
  destination whose `Put` fails, or whose buffer stays full for the lag
  threshold (default 2 min), is **dropped**: its `Put` is canceled and,
  because the reader only returns EOF for a completed stream, the
  backend's atomic finalize leaves no object. The others continue and the
  run becomes `partial`; when every destination is dropped the artifact
  fails. A slow but live destination slows the source (backpressure).
- **Result:** raw and stored sizes and SHA-256s, the per-destination
  outcome (ok, or dropped with a reason and a normalized error code), the
  peak buffered bytes and the duration.
- **Manifest:** after all artifacts of a run are finalized,
  `WriteManifest` signs a canonical-JSON manifest with the active instance
  key (purpose `manifest`) and writes `manifest.json` next to them. Restores
  call `pipeline.Verify` with the run's database records: signature by key
  ID (retired keys included), then job/run binding (replay) and the exact
  entries (paths, sizes, hashes). The file format is documented in
  [reference/artifact-format.md](reference/artifact-format.md).
- **Reading back:** `pipeline.OpenArtifact` decrypts with the bounded
  helpers (scrypt work-factor cap, X25519 identities only) and decompresses
  with a single-goroutine zstd decoder capped at a 64 MiB window.

## Scheduler and job queue (`internal/scheduler`, `internal/queue`, task 3.4, D-145)

```
serve ─ scheduler loop (every scheduler.tick_interval, default 15s, on every instance)
          BEGIN
            SELECT id, schedule, next_run_at FROM jobs
              WHERE enabled AND deleted_at IS NULL AND next_run_at <= now AND kind <> manual
              LIMIT batch FOR UPDATE SKIP LOCKED
            per job: scheduler.Due → River unique insert backup_run {job_id, scheduled_for, trigger}
                     duplicate (previous run still active) → schedule_skips 'still_running'
                     next_run_at = next occurrence after now, last_scheduled_at = now
          COMMIT
River (river_job in the app DB) ─▶ workers in serve and/or `bekup worker`
```

- **Multi-instance safety:** `FOR UPDATE SKIP LOCKED` hands every due job to
  one transaction; a job advanced by another instance no longer matches.
  The River insert, the skip record and the `next_run_at` update commit
  together, so a crash or shutdown mid-tick rolls back and the next tick of
  any instance redoes it. No session advisory locks (D-044).
- **One run per job at a time:** `backup_run` is unique by `job_id` in the
  states available, pending, scheduled, running and retryable (not
  completed). An occurrence that meets an active run is skipped and
  recorded; missed occurrences collapse into one `catch_up` run (D-143).
  Manual schedules are never selected. A schedule that no longer compiles
  resets `next_run_at` to NULL (recorded as `unschedulable`).
- **Queues** (per-process concurrency `[workers] *_queue_workers`): `backup`
  (timeout `workers.backup_timeout`, the cap of the per-job timeout; 2
  attempts), `restore` (`restore_timeout`; 1 attempt, never retried),
  `retention` (`retention_timeout`; 3), `notify` (`notify_timeout`; 10),
  `maintenance` (15 min; 3). Every worker's attempt timeout is bound to its
  queue's; `RescueStuckJobsAfter` = longest queue timeout + 1h, so a job is
  never rescued while it may still run. Completed River rows are kept 24h,
  canceled/discarded 7 days (the run history lives in bekup's own tables).
  River's reindexer is off (`bekup_app` does not own the indexes).
- **Retention** (task 3.8, D-148): `retention_sweep` (retention queue,
  unique per job) is enqueued by the run finish hook of `runstate.Store`
  (success/partial, same transaction, under a savepoint) and by a daily
  River periodic fan-out that enqueues one sweep per job with present
  artifacts and cleans old temporary upload files on disk/SFTP. The pure
  `retention.Plan` decides per destination; the worker re-checks the
  newest complete run on the backend, Stats before every delete (vanished
  objects become `missing`, audited and alerted), skips no-delete
  destinations, keeps Object-Lock-refused artifacts and caps deletes per
  sweep. See [Retention](guides/retention.md).
- **Kinds and args:** every kind is declared in `internal/queue/kinds.go`
  (queue, attempts, uniqueness, args type); `Client.InsertTx` is the only
  insert path and applies these options, so `serve --no-worker` inserts
  exactly as a worker would. Args carry IDs only: `queue.CheckArgs` (run by
  `queue.New` at startup and by a test over the catalog) rejects plain
  strings, bytes, maps, interfaces, floats and secret-looking names.
- **Processes:** `serve` runs the scheduler (`scheduler.enabled`) and the
  workers (unless `--no-worker`); `bekup worker` runs only the workers.
  Workers fetch only kinds they have registered, so a kind whose worker is
  not deployed yet waits instead of failing.
- **Shutdown:** stop fetching, let running jobs finish for
  `workers.shutdown_timeout`, then cancel their contexts and wait at most
  30s more.
- **Metrics:** the loop reports ticks, enqueued runs and skips through
  `scheduler.Metrics` (Prometheus counters, task 5.6).

## Backup runs (`internal/jobs` backup worker, `internal/runstate`, task 3.6, D-147)

```
scheduler tick / runJobNow ── one tx: River backup_run {job_id, ...} + runs row 'queued' + NOTIFY
worker (River backup queue, serve or `bekup worker`)
  Begin: take the queued run (a retry gets a new run; a stale 'running' one → failed 'interrupted')
  job missing / deleted / disabled → 'skipped'      cancel requested → 'canceled'
  run_slots: global + per-connection limit free?   no → JobSnooze(15s), run stays 'queued'
  'running' (directory timestamp = start, timeout = min(job timeout, workers.backup_timeout))
  open destinations · runner Probe (server version, databases) · resolve the selection
  per database (sequential), then the PostgreSQL globals:
     runner Dump ──chunks──▶ relay ──▶ pipeline (zstd → age → sha256 → fan-out, D-144)
                               └────▶ VerifyDump (PostgreSQL archives: streamed, trailer = total)
     EOF to the pipeline only after a complete DumpResult (exit 0, byte count)
     → item + one artifact row per destination that stored the object
  manifest.json (instance key, purpose "manifest") → every destination holding all complete items
  terminal status + warnings + log counters; release the slot
```

- **Statuses:** `success` = every item stored and verified on every job
  destination and the manifest on all of them; `partial` = at least one
  complete, verified artifact but something missing (a dropped
  destination, a failed or missing database, a manifest, a timeout after
  some items); `failed` = nothing usable; `canceled`; `skipped`. Items:
  `success`, `partial` (not on every destination), `failed`, `canceled`.
- **Verification without a second download:** the runner's raw stream is
  teed into a streamed `VerifyDump` (header `total_bytes` 0, then a
  `VerifyDumpTrailer` with the exact total; an offline container named
  `bekup-run-<run id>-verify`). MySQL/MariaDB dumps and the globals rely on
  the trailer check in `DumpResult.verified`. A verify failure fails the
  item; its objects stay as artifacts with `verified = false`, unsigned and
  outside the manifest.
- **Destinations:** a destination that cannot be opened, or whose Put fails,
  lags or mis-stores, is dropped for the rest of the run. Disk/SFTP/S3
  finalize atomically, so a dropped, failed or canceled upload leaves no
  object (S3 multipart aborted).
- **Failures:** target-level failures (unreachable, auth, TLS, pin mismatch,
  SSH, runner down, not allowlisted) stop the run; database-level ones fail
  only their item. Pin and SSH host-key mismatches are audited (system
  actor) and alerted; so is an SFTP host-key mismatch of a destination.
  Stored error texts are fixed messages per code, never server output.
- **River:** canceled → `JobCancel`; shutdown or runner unavailable/busy
  with nothing stored → error (one retry); everything else (incl. a
  timeout) completes the River job; the runs table is the history.
- **Concurrency:** `run_slots` rows under `pg_advisory_xact_lock` (no
  session locks): `workers.max_concurrent_runs` globally,
  `workers.max_runs_per_connection` per connection, across every process;
  heartbeat 30 s, a slot without heartbeat for 2 min is reclaimed (the
  holder stops, `slot_lost`).
- **Cancel:** `cancelRun` (admin, maintainer) marks the run, cancels the
  River job (same transaction, audited `run.cancel_requested`); a queued
  run ends at once, a running one stops on River's signal or the worker's
  2 s poll: runner calls are canceled (container killed), every Put aborted.
- **Run logs:** runner lines and worker events, sanitized, scrubbed of
  registered secrets and credentials before truncation, capped per run
  (`workers.run_log_max_lines`, `run_log_max_line_bytes`), batched (500 ms /
  100 lines) without ever blocking the dump.
- **Events:** each write publishes `run:<run id>:<job id>` or
  `log:<run id>:<job id>` on `bekup_run_events` in its transaction (task
  3.7 subscriptions, below).

### Run history and live run events (task 3.7, D-149)

- **Reads** go through `runstate.Reader` (read-only sqlc queries in
  `run_reads.sql`): `runs` and `artifacts` (cursor pages, newest first,
  filtered in SQL by `Policy.JobScope`), `run(id)` (items, per-destination
  artifacts with state/verified/signed, warnings, sanitized error, sizes,
  durations, the manifest summary), `runLogs(runId, afterId, first)` and
  `Job.lastRun` / `Job.recentRuns`. Single runs are authorized with the job
  stored on the row; owners get FORBIDDEN for foreign and unknown ids.
  `Run.triggeredBy` is staff only (`run.actor_view`).
- **Batching:** a per-operation `runLoader` (queries and mutations only)
  loads the items and artifacts of a whole page of runs, and the recent
  runs of a whole page of jobs, in one query each.
- **`runUpdated(jobId, runId)`:** every stream subscribes to
  `bekup_run_events` on the in-process bus (fed by the LISTEN bridge). The
  payload only pre-filters; each event is re-loaded by run id and
  authorized (`run.view` with the stored job) at delivery, then sent. After
  possibly lost notifications the stream re-sends the runs changed since
  the last resync (at most 100).
- **`runLogAppended(runId, afterId)`:** coalesced batches of at most 200
  new lines, authorized per batch (`run.logs_view`); a final `done` batch
  completes the stream once the run finished and every line was sent.
- **Combined streams (D-197):** `runActivity(runId, afterLogId)` carries
  one run's changes and its log batches in one stream (each kind
  authorized with its own operation; after the `done` batch it sends the
  final run state and completes); `activityUpdated` carries every run and
  restore the caller may view (the dashboard).
- **Stream budget:** every stream counts against
  `graphql.stream_max_per_session` (default 4). The UI keeps one account
  stream per browser (shared across tabs) plus at most one page stream per
  tab: `activityUpdated` on the dashboard, `runActivity` on a run page,
  one `runUpdated` (filtered on the client) on list pages.
- **Completeness evidence (task 3.10, D-141, D-151):** `jobs.StatsCollector`
  brackets each dump. The worker's `jobs.RunnerStats` sets `StatsOptions`
  (level from the job's `verification`) on the `Dump`/`DumpGlobals`
  request; the runner captures the stats inside that call (PostgreSQL: in
  the exported snapshot `pg_dump --snapshot` reuses) and returns them with
  the dump result. The worker re-validates them, records warnings on the
  item and the run (`runs.has_warnings`), signs the canonical stats into
  the manifest's `stats` object and stores them insert-only in
  `backup_stats`. A stats failure only adds `stats_unavailable`; the dump
  still counts.
- **Schema version (D-210):** `RunnerStats` also puts the job's
  `VersionQuery` (preset name + schema, or the custom query) on database
  `Dump` requests. The runner reads it on the stats session inside the
  statistics transaction (`dbprobe.StatsSource.SchemaVersion`: savepoint +
  `SET LOCAL` timeouts on PostgreSQL, session limits on MySQL/MariaDB,
  preset table-layout check first, first row only) and returns it in
  `DumpStats`; the worker re-validates it, writes it into the canonical
  stats (`schema_version`, `schema_version_status`) and onto
  `run_items.schema_version` / `schema_version_status`, and adds
  `version_unavailable` / `version_invalid` as item warnings without
  changing the run status.

## Web UI (`web/`)

A React + TypeScript single-page app (D-026, D-084), built with Vite and
embedded in the binary (`web/embed.go`); `serve` delivers it under the
strict CSP with Trusted Types (D-082). It talks only to `serve`: GraphQL
as POST + JSON on `/graphql`, subscriptions as Server-Sent Events on the
same path (D-124), downloads as same-origin navigations to
`/api/download/{ticket}` (D-150). There is no other backend.

- **Shell and session gate (D-179).** `RequireSession` loads `me` and
  renders the shell, the forced password change or the forced 2FA
  enrollment; the role-based navigation only shapes the UI, the server
  enforces every operation (authz matrix). One shared step-up dialog
  (`useStepUp`) handles `STEP_UP_REQUIRED` and the proactive AAL2 check of
  restores (D-185).
- **Feature pages.** Dashboard (D-192, D-196), calendar (D-185), jobs and
  runs with live logs, downloads and test restores (D-186, D-195), the
  restore wizard and history (D-185), connections and destinations with
  the pin and host-key confirmations (D-182), users (D-181, D-184), the
  notification center (D-193) and the admin settings: SMTP, shared
  channels, security alerts, audit log with chain status, SSRF allowlist,
  recovery export (D-194, D-196).
- **Live data.** Each tab keeps the event streams within the session's
  cap (`graphql.stream_max_per_session`, default 4): one shared account
  stream plus at most one page stream; every event is re-authorized on
  the server.
- **One-time material** (generated keys, signing keys, temporary
  passwords, download tickets, age identities) never enters the Apollo
  cache (`no-cache`) or browser storage and is dropped after use.

Developer details: [development.md](development.md#web-ui-web).

## Engines (`internal/engine`, task 2.1)

`internal/engine` is pure Go (no Docker, no network) and is used by the
runner only. It provides (D-031, D-033, D-036, D-123):

- **Image catalog.** `internal/engine/catalog.toml` is embedded in the
  binary: per engine and release series one official image pinned by its
  multi-arch index digest, a `legacy` flag and the fixed client binary
  names. Pinned today: PostgreSQL 13 (legacy), 14, 15, 16, 17, 18; MySQL
  8.0 (legacy), 8.4 LTS, 9.7 LTS; MariaDB 10.6 (legacy), 10.11, 11.4, 11.8,
  12.3. The runner's `[images] extra_file` adds entries with the same
  schema (any registry, digest required, cannot replace embedded keys).
  Every entry must meet the per-series client floors: the psql
  `\restrict` fix (CVE-2025-8714), MySQL `--system-command` and
  `--commands`, MariaDB `--sandbox`. Renovate updates the `image = ...`
  lines; tests reject an update that changes the series or drops below a
  floor.
- **Version rules.** Server versions are parsed from
  `server_version_num` / `VERSION()` (MySQL and MariaDB strings are never
  confused). PostgreSQL: client major >= server major, otherwise the run is
  blocked; MySQL/MariaDB fall back to an older client only with a warning.
  The restore client is chosen for max(dump client, target server).
  Minimum servers: PostgreSQL 10, MySQL 8.0, MariaDB 10.4.
- **Argv builders.** Each operation returns a `Command`: image, argv and
  the tmpfs files the stdin preamble must write (password file, pinned CA,
  client certificate). Names are validated (strict charset, no leading
  `-`); PostgreSQL tools get a quoted keyword/value `--dbname=` conninfo;
  MySQL/MariaDB tools get `--defaults-file=/tmp/bekup/my.cnf` first, `--`
  before the positional database, and restores run with
  `--system-command=OFF --commands=OFF --local-infile=0` (MySQL) or
  `--sandbox --local-infile=0` (MariaDB).
- **TLS modes.** `pinned` (default; pinned certificate as the only trust
  anchor with host-name verification), `unverified` and `disabled`; the
  latter two are admin-only, audited downgrades (D-034).
- **PostgreSQL overwrite.** A restore goes into
  `<db>__bekup_restore_<id>` (created from `template0`); the swap blocks
  and terminates connections to the target, then renames target to
  `<db>__bekup_old_<id>` and the temp database to the target in one
  transaction, with a rollback step that re-allows connections. Only these
  bekup temporary databases can be dropped.

## Runner design

Implemented in tasks 2.4–2.7a: gRPC API, request validation, target
allowlist, container spec, preamble wrapper, the Docker executor with
per-run networks, proxy and orphan reaper, the RPC handlers (2.7) and
certificate pinning (2.7a), and `FetchHostKey` (2.11: the bastion's host
key through the target-allowlist dialer, key exchange only, for the admin's
TOFU confirmation). serve's only caller of the connection RPCs is
`internal/connections` (D-135). See also D-029–D-035, D-132, D-133, D-134
and D-135 in [DECISIONS.md](DECISIONS.md).

**gRPC API** (`proto/runner/v1/runner.proto`, package `runner.v1`, task 2.4,
D-126): one service `RunnerService` with `Health`, `Probe`,
`FetchServerCertificate`, `FetchHostKey`, `Dump` and `DumpGlobals` (server
streams: data chunks ≤ 1 MiB interleaved with log lines, ending in one
result), `VerifyDump` (client stream: header, then chunks), `Restore`
(bidirectional: header + chunks in, log lines + result out),
`CreateDatabase`, `RenameSwapDatabase`, `DropTempDatabase`, `ScratchStats`, `DropScratchDatabase`, `PrePull` and `ListImages`. There is no field for
a command, argument, environment variable, image or path; images are chosen
by engine + allowlisted version key and container specs are built by the
runner. The one field carrying SQL text is `VersionQuery.custom_sql` of a
`Dump` (D-210, at most 2 KiB, revalidated by the runner and run read-only
on the stats snapshot); a preset travels by name only (the runner owns the
preset SQL, `internal/schemaversion`), and `DumpStats` returns
`schema_version` / `version_status`. A test fails for any other field
named like SQL. Credential fields are `bytes` annotated `[(runner.v1.secret) =
true]`; `runnerpb.ZeroSecrets` zeroes them and a test fails for an
unannotated credential-looking field. Go code is generated into
`internal/runnerpb` by `just gen` (buf and the protoc plugins are go.mod
tool dependencies); `just lint` runs `buf lint`/`buf format` and, once the
file is on `main`, `buf breaking`.

**Server** (`internal/runner`, D-127): the interceptor chain is logging
(method, caller, code, duration; never payloads) → panic recovery
(`INTERNAL "internal error"`) → guard (verified caller identity against the
allowlist, mandatory deadline clamped to `max_run_duration`, deny-by-default
method table, fail-fast limiter: run class = `limits.max_concurrent_runs`,
control class = 8, `Health` unlimited) → zeroing of received secret fields
after the handler. Keepalive, connection recycling and message limits are
in [mtls-certs.md](security/mtls-certs.md). The container healthcheck uses
a separate, plaintext, container-local unix socket that serves only the
standard health service (D-125). `internal/runnerclient` is the client
used by `serve` and the workers.

**SSH tunnels** (`internal/tunnel`, task 2.2, D-122): the runner dials the
bastion through its allowlist dialer, accepts only the pinned host key (and
only its key type), authenticates with a key or password it then destroys,
and forwards each connection of the per-run proxy to the single configured
target. Keepalives detect a dead bastion; a host-key mismatch is a security
event. `FetchHostKey` provides the TOFU step without authenticating.

**Request validation** (`internal/runner/validate`, task 2.5, D-130): every
RPC request is validated before anything else happens. Unknown proto fields
anywhere in a message are rejected, unary requests and stream headers are
limited to 256 KiB, and every field has a size and syntax rule: lowercase
UUID `run_id`; engine + version key from the merged image allowlist
(embedded catalog + `images.extra_file`); host, port, user and database
names per `internal/engine` (write operations refuse system databases);
passwords ≤ 1 KiB without NUL/CR/LF; `pinned` TLS with one or two parseable
DER anchors and a 32-byte leaf SPKI hash (mandatory for a leaf anchor); a
client certificate only with TLS; SSH tunnels with exactly one credential
and a mandatory host-key pin. Private keys are only checked structurally
(PEM type, decoded bytes zeroed), never parsed into unzeroable key objects.
After a `VerifyDump`/`Restore` header only non-empty chunks of ≤ 1 MiB may
follow, and exactly `total_bytes` must arrive. Errors are gRPC statuses
with static messages (`InvalidArgument`, `PermissionDenied` for the target
allowlist, `FailedPrecondition` for unresolvable hosts); they never echo
values. All validators are fuzz-tested.

**Target allowlist** (`internal/runner/targets`, D-029, D-130): see
[target-allowlist.md](security/target-allowlist.md). It is checked when a
request is validated (every resolved address must pass) and again at dial
time (`Guard.DialContext` re-checks the exact socket address), and it
denies everything while `[targets]` is empty.

**Container spec** (`internal/runner/spec`, D-031, D-130): a pure builder
returns the Docker API structs. The image is referenced by digest only;
the user is the catalog entry's non-root UID (`uid`, default 70 for the
alpine PostgreSQL images and 999 for MySQL/MariaDB); the entrypoint is the
fixed preamble wrapper and `Cmd` the engine's argv; the environment is a
constant list (`HOME=/tmp`, `PGPASSFILE`, `MYSQL_HISTFILE=/dev/null`,
`MYSQL_TEST_LOGIN_FILE=/dev/null`, ...). Hardening: read-only root, one
tmpfs `/tmp` (noexec, nosuid, nodev, 64 MiB, 0700), `CapDrop: ALL`,
`no-new-privileges`, default seccomp/AppArmor, not privileged, no mounts,
devices or ports, private IPC/cgroup namespaces, memory (no swap), CPU and
pids limits from `[docker]`, no core dumps, log driver `none`, no restart,
labels `bekup.managed=true` and `bekup.run_id`, the per-run network and an
`ExtraHosts` entry mapping the target name to the runner's proxy.

**Secret preamble** (`internal/runner/preamble`, D-032, D-130): the
container starts `/bin/sh -c <fixed script> -- <argv>`. The script reads
from stdin:

```
BEKUP-PREAMBLE 1
<type> <length>        (pgpass | mycnf | ca | clientcert | clientkey)
<base64 on one line>
...
END
<payload: restore/verify data>
```

Each type maps to a fixed file under `/tmp/bekup` (directory 0700, files
0600, written through the shell's `printf` builtin and `base64 -d`, so no
secret appears in any process's argv or environment); the decoded length is
checked, and any framing error exits with code 97 before the tool runs.
Then the script `exec`s the tool, which reads the remaining stdin. The
script works in busybox ash, dash and bash and is tested in every pinned
image.

**Docker executor** (`internal/runner/executor`, task 2.6, D-132): the
only code that talks to the Docker daemon (moby client, `docker.host` unix
socket only). One run:

```
                       runner container
           ┌────────────────────────────────────────────┐
 runner-ctl│ 10.213.251.10:7443 mTLS gRPC                │ targets network
 ──────────┤   (refuses peers from the pool / run nets)  ├──────────────> target DB
           │                                            │   (target allowlist dialer,
           │ per-run proxy 10.214.0.2:<db port>         │    or SSH tunnel to a bastion)
           │   accepts only 10.214.0.3                  │
           └──────────────┬─────────────────────────────┘
                          │ bekup-run-<run_id>: internal, IPv4, /28 from
                          │ docker.run_subnet_pool, inhibit_ipv4 (no host
                          │ address), no masquerade, no default route
           ┌──────────────┴──────────────┐
           │ dump container 10.214.0.3   │  ExtraHosts: db.example.com → 10.214.0.2
           │ pg_dump ... host=db.example.com sslmode=verify-full
           └─────────────────────────────┘
```

1. Pull the allowlisted image if missing (digest only).
2. Reserve the address plan: a /28 from `docker.run_subnet_pool` (`.2`
   runner, `.3` dump container), skipping subnets of other Docker
   networks; it becomes one of the target guard's own networks until the
   run is cleaned up.
3. Create the network and connect the runner's own container (configured
   `docker.self_container`, or found in `/proc/self/mountinfo` / the host
   name, and only accepted when it carries one of the runner's own
   addresses) with the fixed proxy address.
4. Start the proxy on that address and the target port; it accepts only
   the dump container's address and forwards each connection to the one
   target (dial-time allowlist check, or the SSH tunnel). TLS stays end to
   end, so `verify-full` checks the real server.
5. Create the hardened container (`spec`), attach stdin/stdout/stderr,
   start it, write the preamble (zeroed right after) and the restore
   payload; stdout goes to the RPC in ≤ 256 KiB chunks with backpressure,
   stderr becomes bounded, sanitized, redacted log lines.
6. Wait for the exit code. Timeout: SIGTERM, SIGKILL after 10 s;
   cancellation, stream loss or a broken restore input: SIGKILL at once
   (stdin is never closed cleanly on a broken input).
7. Clean up in reverse order on every path (container, proxy, network
   membership, network, tunnel, subnet), also on panics.

What the client dials: a DNS name is mapped to the proxy with `ExtraHosts`.
An IP literal without host verification is replaced by the proxy address.
An IP literal with `verify-full` behind an SSH tunnel gets a /29 built
around it in which the runner's proxy holds that exact address, so the IP
SAN is verified; without a tunnel this is refused (`FAILED_PRECONDITION`),
because the runner cannot hold an address it must also dial: use a DNS name
that matches the certificate or a tunnel.

The dump container has no route beyond its /28, the gateway address is not
on the host, Docker's embedded DNS does not answer for external names on
internal networks, other runs are on other subnets, and the gRPC listener
closes connections from the pool and every per-run subnet. An integration
test runs the executor inside a container and proves that a dump container
cannot reach the runner's gRPC port (control or per-run address, also on a
wildcard listener), another run's proxy or container, the gateway, its
target without the proxy, the internet or DNS, and that `verify-full` works
through the proxy (DNS name, DNS name and IP literal through an OpenSSH
bastion) and fails for a name the certificate lacks.

The orphan reaper runs at startup and every 5 minutes and removes
`bekup.managed` containers and networks whose run is not active: this
runner's after 2 minutes (immediately at startup), another runner's only
once that runner's container is gone (label `bekup.runner`).

**RPC handlers** (`internal/runner/ops*.go`, task 2.7, D-134). Every call
goes validate (D-130) → target allowlist → pin pre-flight (TLS mode
PINNED, D-133) → Go-driver pre-check → executor, and every call writes one
`runner_rpc_audit` line (identifiers, code, reason, exit code, bytes; never
payloads, server messages or secrets).

| RPC | What the runner does |
|---|---|
| `Probe` | Go driver inside the runner (`internal/runner/dbprobe`: pgx, go-sql-driver/mysql for MySQL and MariaDB) over the allowlist dialer or an SSH tunnel; returns the server version, the databases without system databases, the matching allowlisted client (`detected_version_key`), whether the requested client can dump the server and warnings. No container. |
| `Dump` | Pre-check: the database must be in the server-reported list, the client must handle the server (PostgreSQL client major ≥ server major, else `TARGET_UNSUPPORTED_SERVER`). Then the engine's dump argv in a container; stdout chunks and stderr lines stream back, one `DumpResult` ends the stream (`verified` for the MySQL/MariaDB trailer; PostgreSQL uses `VerifyDump`). |
| `DumpGlobals` | PostgreSQL only. The first `pg_dumpall --globals-only` attempt is held back (≤ 4 MiB); on a role-catalog privilege error it is discarded and retried once with `--no-role-passwords` (`roles_without_passwords`). |
| `VerifyDump` | MySQL/MariaDB dumps and PostgreSQL globals: trailer check in Go, no container. PostgreSQL archives: `pg_restore --format=custom --file=/dev/null` (reads every block) in an **offline** container: `NetworkMode: none`, no proxy, no preamble files. Exactly `total_bytes` must arrive. |
| `Restore` | Pre-check (database exists), client = max(dump client, server), the stream feeds the container's stdin through a pump with backpressure; an invalid or short stream kills the container. Hardened client flags (D-123), never retried, tool stdout never returned. psql (globals) runs under a random `\restrict` key and the dump's own `\restrict` lines are removed (D-138). |
| `CreateDatabase` | Pre-check (name not taken), `CREATE DATABASE` through psql / mysql in a container. |
| `RenameSwapDatabase` | PostgreSQL only (MySQL/MariaDB: `FAILED_PRECONDITION`). Names derived from `temp_id`; Prepare, Swap (up to 3 attempts), Rollback when it fails; each step has its own container/network name (`bekup-run-<run id>-<step>`). |
| `DropTempDatabase` | Drops a restore's derived temporary database (`<db>__bekup_restore_<temp_id>` or `<db>__bekup_old_<temp_id>`, role RESTORE / OLD; the name is never taken from the request) with `DROP DATABASE IF EXISTS` (`WITH (FORCE)` on PostgreSQL 13+); the engine builder refuses any other name. Own container step, so it can follow a canceled `Restore` of the same run at once (D-166). |
| `ScratchStats` | Test restores (task 4.6, D-169): the completeness statistics of the scratch database `bekup_test_<temp_id>` (`temp_id` `[a-z0-9]{12}`, the name is derived by the runner and must exist), captured exactly like a `Dump`'s stats (D-151/D-152 pinned session; PostgreSQL deep exact counts, MySQL/MariaDB estimates only). |
| `DropScratchDatabase` | Drops the scratch database `bekup_test_<temp_id>` (derived by the runner) with `DROP DATABASE IF EXISTS` (`WITH (FORCE)` on PostgreSQL 13+); reports `existed`. No other name can be dropped. |
| `PrePull` | Pulls the allowlisted image by digest only, reports `already_present`. |
| `ListImages` | The merged image allowlist (embedded catalog + `images.extra_file`): engine, version key, digest reference, client version, legacy, extra. |
| `FetchServerCertificate` | Captures the chain (no credentials sent) through the same validation, upstream and error normalization as the other target RPCs; audited. |

Target errors are normalized (`runnerpb.Reason` in an `ErrorInfo`, domain
`runner.bekup`, one fixed message per reason): unreachable, authentication,
TLS, pin mismatch, permission, timeout, unsupported server, database
not found / exists, SSH host key / authentication. Refused, filtered,
timed-out and non-database endpoints all read `TARGET_UNREACHABLE`, so
Probe is no port scanner. Deadlines: the interceptor's (≤
`max_run_duration`), Probe ≤ 1 min, CreateDatabase ≤ 2 min, RenameSwap ≤
5 min; containers get the remaining deadline, cancellation kills them. An
integration test runs the runner in a container against PostgreSQL 13/16/18,
MySQL 8.4/9.7 and MariaDB 10.11/11.8 (Probe per target, the PostgreSQL
compat refusal, every error category, Dump → VerifyDump → Restore with
equal data per engine, globals as a non-superuser, CreateDatabase and the
swap, an SSH-tunneled probe and dump, and that cancelling a dump removes
its container).
