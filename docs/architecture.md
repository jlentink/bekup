# Architecture

> **Status:** partial (end of M1). Components, networks, trust boundaries and
> data flows below describe the design and the compose topology that exist
> today. Implemented: the CLI parts (`config`, `migrate`, `keys`, `certs`,
> `audit`, `admin`, `healthcheck`), `internal/config`, `internal/logging`,
> `internal/db` (with app-DB TLS verify-full and SCRAM channel binding,
> D-100, D-104), `internal/crypto` and `internal/certs`, and in `serve`: the
> HTTP layer (task 1.1), the GraphQL API including WebSocket subscriptions
> (tasks 1.2, 1.10), authorization (1.4), authentication (passwords,
> sessions, TOTP and recovery codes, passkeys, the 2FA policy and step-up;
> tasks 1.5–1.9), the audit log with signed chain heads (1.3) and the
> pubsub bridge (1.10). Still to come: the runner, engines, storage,
> connections and destinations in M2 (tasks 2.x, with the runner's design
> section here), backup execution in M3, restore and the download and
> offline recovery flows in M4, notifications and monitoring in M5, the web
> UI in M6. The page is completed in task 7.5.

bekup is one Go binary with several subcommands (D-025) and one container
image. The authoritative threat model is
[`SECURITY.md`](../SECURITY.md) (section 5: trust boundaries and data flows;
section 6: STRIDE per component). This page summarizes it. Decisions are in
[DECISIONS.md](DECISIONS.md).

## Components

| Component | Role | Status |
|---|---|---|
| Reverse proxy (`traefik` or `nginx` profile, opt-in) | Terminates TLS for the UI, HSTS, HTTP→HTTPS redirect, size and rate limits, WebSocket passthrough (D-017, D-048). Traefik uses only its file provider; nginx is the unprivileged image plus a certbot companion. Neither mounts the Docker socket (D-093, D-094). | shipped (task 0.7b) |
| `bekup serve` | GraphQL API (POST + JSON), sessions, embedded React UI, scheduler, River workers (backup, restore, retention, notify), backup pipeline and uploads to destinations. Connects to Postgres as `bekup_app`. | HTTP layer, UI shell and health endpoints (task 1.1), GraphQL API and WebSocket subscriptions (1.2, 1.10), authz (1.4), authentication incl. 2FA and step-up (1.5–1.9), audit with signed heads (1.3); scheduler, workers and pipeline in M3–M5 |
| `bekup migrate` | One-shot: applies the embedded goose migrations as `bekup_owner`, under an advisory lock (D-042, D-078). The only process that holds the owner credentials. | implemented (task 0.4) |
| `bekup runner` | The **only** component with Docker access (D-003). Small typed mTLS gRPC API, image and target allowlists from its own `runner.toml`, builds the hardened dump-container spec itself (D-029–D-033). | tasks 2.4–2.7a |
| Dump containers | Short-lived official, digest-pinned DB images (`pg_dump`, `mysqldump`, `mariadb-dump`, restore clients). Alone on a per-run `internal` network, reaching exactly one target through the runner's proxy or SSH tunnel; secrets only through a stdin preamble (D-032). | task 2.6 |
| Postgres app DB | bekup's own state: users, jobs, runs, encrypted credentials, the hash-chained audit log, River queue tables. Least-privilege roles `bekup_owner` / `bekup_app` (D-054, D-092). | implemented (task 0.4) |
| `certs-init` (profile `setup`) | One-shot, no network: creates the private CA and the per-component mTLS certificates (D-079, D-089). | implemented (task 0.6) |
| Offline CLI | `bekup verify` / `bekup decrypt` for recovery without the bekup DB (D-040). | task 4.7 |

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
| B4 | `serve` ↔ destinations, SMTP, webhooks | dial-time SSRF guard, no redirects, TLS ≥ 1.2, SFTP host-key pins, disk confined via `os.Root` (D-049, D-055) |
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
- **Backup (task 3.6):** scheduler → River job (IDs only) → worker decrypts
  the connection secret → runner `Dump` over mTLS → per-run network +
  proxy/tunnel → dump stream → zstd → age → sha256 → fan-out to destinations
  → signed manifest.
- **Restore (tasks 4.1–4.5):** fresh 2FA check → stage the encrypted
  artifact → verify hash, signature and DB cross-check → decrypt → runner
  `Restore` (PG temp DB + swap; MySQL/MariaDB in place after a warning).
- **Download (task 3.9):** step-up → single-use, 60 s, session-bound ticket
  → streamed raw or plain file with `no-store`.
- **Notifications (tasks 5.1–5.3):** event → recipient resolver (authz at
  delivery) → inbox / SMTP / HMAC webhooks through the SSRF guard.
- **Offline recovery (task 4.7):** `bekup verify` / `bekup decrypt` on an
  operator machine, no bekup DB needed.

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
| 7 | Origin check | non-GET/HEAD/OPTIONS requests and WebSocket upgrades need `Sec-Fetch-Site: same-origin` and/or an `Origin` equal to the `base_url` origin (dev mode also accepts the Vite origins `http://localhost:5173`, `http://127.0.0.1:5173`); 403 otherwise |
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
`write_timeout` bounds every response, so long-lived routes (WebSocket
subscriptions, task 1.10; artifact downloads, task 3.9) must lift it per
request with `http.ResponseController.SetWriteDeadline`
(`httpserver.LiftDeadlines` does this for the WebSocket upgrade).

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
(Origin check, rate limit) runs first. No GET (except a WebSocket
upgrade, below), no APQ.

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

### Subscriptions over WebSocket (task 1.10, D-052, D-108)

```
browser ──GET /graphql, Upgrade, Origin, session cookie──▶ HTTP middleware (Origin, rate limit, session)
        ◀──101 graphql-transport-ws──  graph.serveWebSocket (Origin again, subprotocol, principal, caps)
                                          │ wsconn.Conn: principal of the upgrade + session watcher
auth.Sessions ──RevokeTx/RevokeAllTx/RotateTx──▶ pubsub.Publisher ──▶ local Bus (at once)
                                          └──pg_notify (on commit)──▶ Postgres ──LISTEN──▶ Listener (every instance) ──▶ Bus
Bus ──▶ wsconn.Hub ──▶ Conn: re-check session ──▶ close 4401 / 4403 / 4001
```

- **Transport.** `GET /graphql` with a WebSocket upgrade and the
  `graphql-transport-ws` subprotocol only (legacy `graphql-ws` → 400).
  The upgrade needs an allowed `Origin` (checked by the middleware and
  again by the handler), a session cookie (401 without; the principal is
  never taken from `connection_init`, whose payload must be empty or the
  socket closes with 4400) and a free slot
  (`server.max_ws_connections_per_user` → 429,
  `graphql.ws_max_connections` → 503). `connection_init` within
  `graphql.ws_init_timeout`, server pings every `graphql.ws_ping_interval`,
  client messages up to `graphql.ws_max_message_bytes`, at most
  `graphql.ws_max_subscriptions_per_connection` active subscriptions and a
  subscribe-rate limit per connection. Only subscriptions run over the
  socket and subscriptions only over the socket.
- **Session watcher.** Each connection re-checks its session by id and
  rebuilds its principal like the session middleware, including the 2FA
  enrollment check (`httpserver.SessionsResolver.Check` →
  `auth.Sessions.Check`, no touch: a socket does not extend the idle
  timeout; D-118) every `graphql.ws_revalidate_interval`, on each session event
  for its session or user (with short follow-ups, since the local event
  can precede the commit), after lost events and before delivering an
  event once the last check is older than `graphql.ws_session_check_ttl`.
  New step-up and second-factor times refresh the principal used for
  delivery-time checks. An upgrade of a principal that must enroll a
  second factor is refused (403); after shutdown upgrades get 503.
  Close codes: 4401 session ended, 4403 user/role/AAL or a restriction
  (pending second factor, temporary password, required enrollment) changed, 4001
  rotated (reconnect with the new cookie), 1013 check failed (fail
  closed), 1001 shutdown. The web client sends the user to `/login` on
  4401/4403.
- **Session events and `internal/pubsub`.** The session store publishes
  `revoked`/`rotated`/`user_revoked` inside the revoking transaction:
  in-process at once and via `pg_notify` to every instance on commit. A
  dedicated LISTEN connection per instance (reconnect with backoff and
  jitter, resync after reconnect) feeds the bus. Payloads are IDs only
  (`kind:user_id:session_id`); receivers always re-check the database.
  Run events (3.7) and in-app notifications (5.3b) reuse the bridge with
  their own channels.
- **Delivery-time authz.** Subscription resolvers send through
  `wsconn.Deliver` + `wsconn.Authorize(policy, op, ...)`: the session check
  and `Policy.Authorize` run for every event, so a caller who loses access
  stops receiving events. `Subscription.sessionEvents` (own sessions) is
  the first field.
- **CSP.** `connect-src 'self'` covers same-origin `ws:`/`wss:` (CSP3);
  `pnpm csp-check` verifies it in Chrome.

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
  Lockouts go to a `LockoutNotifier` hook (security alert in task 5.5).
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
  code use, removal and locks go to a `SecondFactorNotifier` (alerts in
  task 5.5). `auth.SecondFactors` aggregates the factor checkers (TOTP,
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
  the passkey, audited and reported to the `passkey.Notifier` (alerts in
  task 5.5). Every registration step, assertion outcome, rename and
  removal is audited. GraphQL passes WebAuthn options and responses as the
  `JSON` scalar (objects of at most 32 KiB).
- **Two-step login** (task 1.9, D-112; `auth.SecondFactorFlow`). For an
  account with a second factor, `login` creates a session flagged
  `sessions.mfa_pending` (AAL1, no step-up, 5-minute absolute expiry) and
  answers `secondFactorRequired` with the usable factors. Its principal
  (`MFAPending`) may only call `beginPasskeyLogin`,
  `completeSecondFactor(method, code | assertion)` and `logout`
  (`SECOND_FACTOR_REQUIRED` otherwise; no WebSocket). Completion verifies
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
  `mfa_reset_at` and revokes every session in one audited transaction; a
  `FactorResetNotifier` hook raises the alert (task 5.5).
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

## Runner design

Filled in by tasks 2.4–2.7a: gRPC API, per-run network lifecycle, TCP proxy
and SSH tunnel, preamble wrapper, orphan reaper. Until then, see D-029–D-035
in [DECISIONS.md](DECISIONS.md).
