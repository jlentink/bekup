# AGENTS.md

Guide for AI agents and human contributors working on **bekup**. This file is
a summary; the authoritative sources are:

- **Plan and task checklist:** [`plans/backup-databases-abstract-corbato.md`](plans/backup-databases-abstract-corbato.md)
- **Decision log:** [`docs/DECISIONS.md`](docs/DECISIONS.md) (D-001 ...)
- **Threat model:** [`SECURITY.md`](SECURITY.md)

Keep this file in sync whenever a decision changes the architecture or the
conventions below.

## Project summary

bekup is an open-source, self-hosted backup manager for PostgreSQL, MySQL and
MariaDB. It runs each database's native dump tools in the official,
digest-pinned Docker images, writes encrypted backups to disk, S3 and SFTP,
applies retention, restores on demand, and is managed through a React web UI
talking GraphQL to a Go backend. Module: `github.com/jlentink/bekup`, MIT
license.

**Security is the top priority, with no compromises** (D-001). When
convenience and security conflict, security wins. Any weakened setting
requires an admin with step-up auth, is audited, raises a security alert and
shows a permanent badge.

## Architecture summary

Components (one Go binary, several subcommands; D-025):

- **Reverse proxy** (opt-in compose profile) terminates TLS for the UI
  (D-017, D-048): Traefik with the **file provider, no Docker socket**
  (D-093) or **unprivileged nginx + certbot** (D-094).
  Neither proxy container has Docker access. Without a proxy, dev serves
  plain HTTP on `127.0.0.1:8080` with a persistent warning (D-095).
- **`bekup serve`**: GraphQL API, sessions, embedded UI, scheduler, River
  workers (backup, restore, retention, notify), pipeline and uploads to
  destinations. Talks to Postgres as `bekup_app`, to destinations/SMTP/webhooks
  through the SSRF guard, and to the runner over mTLS gRPC.
- **`bekup runner`**: the **only** component with Docker access (D-003). Small,
  typed mTLS gRPC API; image allowlist and target allowlist from its own
  `runner.toml`; builds hardened container specs itself (D-029–D-033).
- **Dump containers**: short-lived, alone on a per-run `internal` network,
  reaching exactly one target via the runner's proxy/SSH tunnel; secrets via a
  stdin preamble only.
- **Postgres app DB** (`internal/db`): sqlc + goose (embedded migrations,
  River schema vendored, migrations only as `bekup_owner` via
  `database.owner_url_file`); the app connects as `bekup_app` with DML only
  and INSERT/SELECT on the hash-chained `audit_log` (D-042, D-054, D-092).
  The bootstrap superuser is rejected on TCP and only usable over the
  container-local socket (D-096).

Trust boundaries:

| Boundary | Control |
|---|---|
| Browser → proxy → `serve` | TLS at the proxy; `serve` only on an internal network; trusted-proxy list; strict CSP, POST+JSON GraphQL, Origin checks (D-050) |
| `serve` → runner | mTLS (TLS 1.3, private CA, SAN check), typed RPCs only (D-030) |
| runner → Docker / target DBs | image allowlist, fixed container spec, per-run isolated networks, target allowlist, DB certificate pinning (D-029, D-031, D-034) |
| `serve` → destinations, SMTP, webhooks | dial-time SSRF guard, TLS ≥1.2, no redirects (D-049, D-055) |
| `serve` → app DB | TLS verify-full by default (D-100), least-privilege roles, envelope-encrypted secrets, append-only audit log (D-046, D-054) |

`serve` can **never** reach target DBs or Docker.

## Working rules

- **One task per agent at a time.** Parallelize only tasks marked **∥** in the
  plan, respecting their `deps:` lines.
- If you are unsure about a detail, **ask the user until you are >95% sure.**
- Mark a task done (`- [ ]` → `- [x]` in the plan) only when its acceptance
  criteria are met and you are **≥95% sure** it is fully implemented, tested
  and lint-clean. Update the plan checkboxes after every completed task.
- **Every milestone ends with two independent reviews, always:** (1) a
  dedicated security review agent and (2) a dedicated code review agent
  (correctness, AC per task, cross-component consistency, all checks run).
  Both are briefed with the code, not conclusions. All findings are fixed
  (or deferred with the user's consent) and re-checked before the milestone
  is handed to the user for committing.
- **Decision log** ([`docs/DECISIONS.md`](docs/DECISIONS.md)):
  - Record every decision **before** or together with the code that depends
    on it, and keep this file's summary in sync.
  - Append-only entries `D-NNN` with Date, Status, Decided by, Context,
    Decision, Alternatives considered, Consequences (incl. security impact),
    Related. Update the index table at the top.
  - Never edit a decision away: supersede it with a new entry and set the old
    entry's status to `superseded by D-xxx`.
  - Agents may only add `proposed` entries; `accepted` needs the user's
    confirmation. If the plan does not cover something, choose the most
    secure sensible option and record it as `proposed`.
- Update `SECURITY.md` whenever a trust boundary changes.
- **Global acceptance criteria for every task:**
  - unit tests for new logic;
  - `just lint`, `just sec` and `just test` green;
  - no secrets in logs;
  - the authz matrix updated for every new GraphQL operation;
  - a `docs/DECISIONS.md` entry for every decision made (+ summary here);
  - the relevant `docs/` page updated for user-visible changes.

## Repo layout

```
AGENTS.md README.md LICENSE SECURITY.md CHANGELOG.md VERSION justfile
Dockerfile docker-compose.yml docker-compose.dev.yml renovate.json
bekup.example.toml runner.example.toml sqlc.yaml buf.yaml buf.gen.yaml
.air.toml .golangci.yml
go.mod                  # github.com/jlentink/bekup
cmd/bekup/              # single binary entry point (cobra CLI)
proto/runner/v1/        # runner gRPC API (package runner.v1; Go code in internal/runnerpb)
internal/
  implemented:  config  logging  db  crypto  certs  audit  instancekeys
                httpserver  app (serve composition, task 1.1)  graph (1.2)
                graph/streams (SSE subscription streams: 2.13)
                authz (1.4)  auth (passwords, sessions, lockout: 1.5; factors 1.7-1.9)
                pubsub (LISTEN/NOTIFY bridge + in-process bus: 1.10)
                destinations (destination CRUD, Open/Test, host keys: 2.12)
                connections (connection CRUD, pins, host keys, runner request builder: 2.11)
                netguard (dial-time SSRF guard: 2.3)
                storage (Destination interface + Disk: 2.8; S3 storage/s3store: 2.9; SFTP: 2.10)
                tunnel (SSH client, host-key pin, TOFU fetch: 2.2)
                sshpolicy (the one SSH client policy: pins, algorithms, keys,
                  handshake, keepalive; 2.14)  hostname (the one host rule: 2.14)
                serviceutil (shared bits of connections/destinations: 2.14)
                engine (image catalog, version rules, argv builders: 2.1)
                runner (mTLS gRPC server skeleton + Health: 2.4; validate/targets/spec/
                  preamble: 2.5; executor (Docker, per-run networks, proxy, reaper): 2.6;
                  RPC handlers ops*.go + dbprobe (Go-driver pre-checks): 2.7;
                  pinning: 2.7a; FetchHostKey ops_hostkey.go: 2.11)
                runnerclient (mTLS gRPC client: 2.4)  runnerpb (generated: 2.4)
  doc.go only until their task:
    pipeline (3.1)  scheduler (3.3)  jobs (3.4-3.6)
    retention (3.8)  restore (4.1-4.5)  notify (5.1-5.3)  monitor (5.6)
web/                    # Vite + React + TS UI, embedded via go:embed (web/embed.go)
deploy/                 # postgres init, traefik/nginx proxy configs, dev/ keys + seeds
scripts/                # helpers behind the justfile (doctor, bump, release, ...)
docs/                   # user/operator docs; docs/DECISIONS.md is the decision log
plans/                  # the living plan with the task checklist
.github/workflows/      # CI (SHA-pinned actions); release workflow in task 7.4
```

Each `internal/` package has a `doc.go` describing its responsibility.

## Conventions

**Go**

- All SQL lives in `internal/db/queries/*.sql` and goes through **sqlc**; no
  ORM and no hand-built SQL strings anywhere else. Migrations are goose `.sql`
  files, forward-only in production; never edit a released migration.
- Logging with `log/slog` through the redaction handler in
  `internal/logging` (D-075); static messages, snake_case keys. Register
  known secret values with `logging.DefaultRegistry.Register`.
- Configuration comes from `internal/config` (D-073); read secret files only
  with `config.ReadSecretFile` and zero the bytes after use.
- Wrap errors with context (`fmt.Errorf("...: %w", err)`); compare with
  `errors.Is` / `errors.As`.
- Table-driven tests; fuzz tests for validators and parsers; testcontainers
  for integration tests. Tests that need Docker skip only via
  `dockertest.Skipf` (`internal/testutil/dockertest`): it skips locally
  but fails with `CI=true`.
- Pass `context.Context` everywhere; no unbounded goroutines or channels.
- **File naming:** never put `token` or `secret` in file or directory names
  (the maintainer's tooling denies AI access to such paths). Use e.g.
  `session_auth.go`, `credfiles.go`, `theme.css` instead.
- Randomness for anything security-related comes from `crypto/rand`.
- `golangci-lint` (with gosec) must be clean; `//nolint` needs a specific
  linter and a reason.

**Web**

- All UI work uses the **`frontend-design` skill** on top of the design
  system from task 0.8 (D-021). Accessible (WCAG AA), dark mode.
- Apollo Client + graphql-codegen; react-hook-form + zod.

**Security**

- Secrets are **write-only** in the API (expose `hasPassword`, never the
  value), stored envelope-encrypted, zeroed after use.
- Encrypt stored secrets only with `internal/crypto`: `Keyring.Seal/Open`
  with `AAD{Table, RowID, Field}` matching where the ciphertext is stored
  (D-085, D-086); hold secrets in `crypto.Secret` / `[]byte` and `crypto.Zero`
  them; instance signatures always carry a purpose (D-087); generated
  passphrases and age/SSH keys come from the `crypto.Generate*` helpers and
  age decryption always uses the bounded helpers (D-088). serve verifies
  the keyring against the key-check values in `settings`
  (`security.key_check_values`, one per key ID) at startup and refuses to
  start on a mismatch; new keys after `bekup keys rotate` are added and
  audited as `keys.rotated` (D-119).
- **Never log secrets**, connection strings, tokens or DB contents.
- Secrets in config only as `*_file` paths; never inline, env or argv.
- Deny by default: every GraphQL operation is mapped in the authz matrix.
- **Authorization (`internal/authz`, D-106).** To add an operation:
  (1) add an `Operation` constant in `internal/authz/operations.go`;
  (2) add its `Rule` to the policy table (roles, owner grant
  `OwnerJob`/`OwnerList`, `Fresh` step-up/AAL2, `NeedsTarget`);
  (3) add the expected row to `internal/authz/matrix_test.go`
  (admin, maintainer, maintainer-owner, owner, non-owner, anonymous +
  freshness); (4) for a GraphQL root field, give it a directive and map
  `"Type.field"` to the operation in `graph.RootFieldOperations`. Tests and
  `serve` startup fail if any step is missing. Resolvers of owner-scoped
  operations call `Policy.Authorize(ctx, op, authz.Job{ID}, ...)` with the
  job loaded from the DB (never from input); list resolvers filter by
  `Policy.JobScope`. Owner grants apply to role `user` only (D-067). The
  caller is `auth.FromContext(ctx)`; never trust a role from input.
- Never commit keys, certificates, local configs or `.env` files.
- Network exposure: outside dev mode `serve`/runner refuse wildcard listen
  addresses unless explicitly overridden (D-098); plain HTTP only in dev mode
  or with `server.allow_insecure_http`, always with a startup warning and UI
  banner (D-095).
- App DB roles: `bekup migrate` re-checks the full role/privilege state on
  every run; `serve` must call `db.CheckAppRole` at startup (D-099).
- HTTP layer (`internal/httpserver`, D-102): every response carries the
  exact D-082 CSP and the security headers; forwarded headers are used only
  from `trusted_proxies` and then stripped (use `httpserver.ClientIP(ctx)`,
  never `X-Forwarded-*` or `Host`); state-changing requests (and any
  upgrade request; none is served) must pass the Origin/`Sec-Fetch-Site`
  check; per-IP rate limit and body limit apply to all routes; long-lived
  routes lift the deadlines per request (`httpserver.LiftDeadlines`).
  New serve components plug into `internal/app`;
  `serve` refuses to start on a privileged role or a schema version other
  than the newest embedded migration (`bekup_schema_version()`).
- GraphQL (`internal/graph`, D-105): schema-first gqlgen
  (`internal/graph/schema/*.graphqls`, `just gen`); POST + JSON only;
  `graphql.max_depth`/`max_complexity`/`max_body_bytes`; introspection only
  in dev or via `Access.IntrospectionAllowed`. **Every root field needs
  `@authenticated`, `@hasRole(roles: [...])` or `@stepUp`, or an entry in
  `graph.PublicRootFields`**, and every guarded root field maps to an
  `authz.Operation` in `graph.RootFieldOperations`; serve refuses to start
  otherwise. **A root field that verifies, sets or resets credentials or
  runs argon2id goes into `graph.SingleRootFields`** (operations selecting
  it must have exactly one root field, else VALIDATION; startup fails if a
  public mutation or a field of a `graph.CredentialOperations` operation
  is missing); every further root field of an operation is charged to the
  per-user rate limit (D-117). Introspection is denied to restricted
  principals. Resolvers
  return `apierror.Error` (codes UNAUTHENTICATED, FORBIDDEN,
  STEP_UP_REQUIRED, REQUIRES_2FA_ENROLLMENT, VALIDATION, NOT_FOUND,
  RATE_LIMITED, CONFLICT, PASSWORD_CHANGE_REQUIRED, SECOND_FACTOR_REQUIRED,
  CONNECTION_FAILED, TLS_PIN_MISMATCH, SSH_HOST_KEY_MISMATCH)
  for user-facing errors; anything else is masked
  as "internal error" + request ID and logged. Never build `gqlerror`
  values in resolvers.
- Authentication (`internal/auth`, D-107): hash and verify passwords only
  with `auth.Hasher` (argon2id PHC, semaphore → `ErrBusy`/`RATE_LIMITED`)
  and check new ones with `auth.PasswordPolicy` (length, common list,
  account strings, zxcvbn). Sessions only through `auth.Sessions`: the
  token lives only in the cookie (`auth.CookiePolicy`: `__Host-` +
  `Secure` unless `base_url` is http), the DB holds sha256; rotate on
  privilege changes (`RotateTx`, `ElevateAAL`), `RevokeAllTx` on password,
  role or status changes. Resolvers set/clear the cookie via
  `auth.CookieSetterFrom(ctx)` and read client facts via
  `auth.RequestMetaFrom(ctx)` (both put there by the session middleware).
  Login failures are always the generic `ErrInvalidCredentials`, with a
  dummy verification when no real hash is checked; never log or audit
  passwords or the e-mail of an unknown account. Every password or
  second-factor check is first reserved with the atomic conditional
  UPDATE (`ReserveLoginAttempt` / `ReserveMFAAttempt`, or
  `TOTP.ReserveSecondFactorAttempt` + `RecordSecondFactorFailure` for new
  factor kinds) and a success resets the count; argon2id verifications take
  a token from `ServiceOptions.AccountLimiter` first; authenticated checks
  of a locked account answer RATE_LIMITED without argon2id; refusals while
  locked are audited once per lock window (D-117).
- Subscriptions (D-052, D-108, D-124): **Server-Sent Events, no
  WebSocket** (never add one unless 100% needed). One `POST /graphql`
  (`application/json`, `Accept: text/event-stream`) per subscription,
  graphql-sse distinct connections mode, served by `internal/graph`'s own
  transports on gqlgen's executor (never gqlgen's `handler`/`transport`
  packages: they link a WebSocket library). Cookie session + exact Origin,
  no restricted principals, caps `graphql.stream_max_per_session` /
  `_per_user` / `_total`; subscriptions only as streams and streams only
  for subscriptions. Subscription resolvers send every event through
  `streams.Deliver` with `streams.Authorize(policy, op, ...)` (session
  re-check + `Policy.Authorize` per event with the stream's current
  principal; load data by ID with the caller's rights). Re-validation
  rebuilds the principal like the session middleware (incl. the
  enrollment check) and ends the stream with FORBIDDEN when identity,
  role, AAL or a restriction changed (D-118). Session revocations/rotations publish events from
  `auth.Sessions` inside the transaction; cross-component events go
  through `internal/pubsub` (`Publisher.PublishTx`, `Listener`) with
  **IDs only** (`pubsub.Validate`); new channels get a fixed LISTEN
  query in `internal/db/queries/pubsub.sql` and an entry in
  `pubsub` channels. A stream the server ends gets a final GraphQL error
  event, then `complete`: UNAUTHENTICATED session ended, FORBIDDEN access
  changed, SESSION_ROTATED (reopen with the new cookie), UNAVAILABLE
  (shutdown or failed check; retry later). The web client is the
  `graphql-sse` Apollo link in `web/src/lib/apollo.ts`; keep pages to 2-3
  subscriptions (HTTP/1.1 allows 6 connections per origin).
- Accounts and the first admin (D-110, D-113): change users only through
  `auth.UserAdmin` (actor `auth.AdminActor` for the API,
  `auth.OperatorActor` for `bekup admin`): it audits, revokes sessions on
  role change/disable/reset/delete and keeps one active admin under row
  locks. Accounts are soft deleted (`users.deleted_at`; queries for
  sign-in filter it). Admins never choose a user's password: they get a
  one-time temporary password (72h, `password_temporary_until`) and the
  principal's `PasswordChangeRequired` limits the session to the
  account operations (`PASSWORD_CHANGE_REQUIRED`). The first admin is
  created only with `bekup admin create` on the host (compose: the
  one-shot `admin` service, D-115; never `compose run serve`): there is no web
  setup flow and no public operation may create accounts; serve only logs
  the command at WARN while no active admin exists
  (`UserAdmin.ActiveAdminExists`). Lost-factor recovery only through
  `UserAdmin.ResetSecondFactors` (`resetUserSecondFactors`: admin,
  step-up, never the own account; `bekup admin reset-2fa`): it wipes
  TOTP, passkeys and recovery codes, revokes all sessions and sets
  `users.mfa_reset_at` (a new grace period).
- Second factors: TOTP and recovery codes only through `auth.TOTP`
  (D-111): secrets sealed under `users:<id>:totp_secret` /
  `totp_pending` and never returned after enrollment; verification via
  `VerifyTOTP` / `UseRecoveryCode` (replay-safe conditional updates,
  separate second-factor lockout); recovery codes stored only as
  `Keyring.MAC` hashes; QR codes as inline SVG (`auth.QRCodeSVG`, no
  `data:` URIs). Register factor checkers in `auth.SecondFactors`.
- Passkeys only through `internal/auth/passkey` (D-109): RP ID/origin
  from `server.base_url` (unavailable on non-localhost http and IP hosts);
  ceremony state only server-side in `webauthn_ceremonies`, bound to the
  session, single use; registration/rename/delete need step-up; the
  assertion API (`BeginAssertion`/`FinishAssertion`, session-bound) refuses
  sign-counter regressions. Factor removals go through
  `auth.FactorRemovalGuard` (the 2FA policy). WebAuthn JSON travels as the GraphQL `JSON`
  scalar (objects, ≤ 32 KiB). Never put "credentials" in file names (the
  maintainer's tooling denies such paths, like `token`/`secret`).
- 2FA policy, two-step login and step-up (D-112): the policy lives in
  `settings` (`security.two_factor_policy`) behind `auth.TwoFactorPolicies`
  (default: off; admins configure it, 7-day grace once a role is
  required, counted from the latest of the requirement start, account
  creation and `mfa_reset_at`); never read it elsewhere. A
  login of an account with a factor yields an `mfa_pending` session
  (5 min, AAL1, no step-up) completed only through
  `auth.SecondFactorFlow.CompleteSecondFactor` (rotation to AAL2); step-up
  only through `SecondFactorFlow.CompleteStepUp` (a factor for accounts
  with one, the password only for accounts without). Only TOTP or a UV
  passkey (User Verified flag in the assertion) set `aal2_at` (restores,
  `FreshAAL2`); recovery codes, user-presence-only passkeys and passwords
  never do (D-116). Restricted principals (`MFAPending`, `PasswordChangeRequired`,
  `EnrollmentRequired`) are enforced in `authz.checkRestrictions`: a new
  operation that such a principal needs must be added to its allowed set
  there (and tested in `restrictions_test.go`). Every second-factor
  rejection counts towards the second-factor lockout.
- Destinations (`internal/destinations`, D-131): only the service touches
  the `destinations` table. Credentials live in one sealed blob
  (`secret_enc`, AAD `destinations:<id>:secret`, TLV-encoded bytes, never
  JSON/strings) and are write-only in the API (`has*` flags); omitted
  credential inputs keep the stored value. Validate at save time (s3store
  `Config.Validate`, `sftp.ValidateTarget`, `storage.ParsePrefix` for disk
  paths, `netguard.ResolveAndCheck` for every endpoint/host). Workers get
  backends only via `Service.Open(ctx, id)` (guard + keyring; refuses a
  destination that is not ready). Every change is admin + step-up and
  audited with changed field names (credentials by name only, plus
  `secret.changed`); `testDestination`/`fetchDestinationHostKey` are
  single-root, rate limited and return normalized results only. SFTP host
  keys are pinned only by `ConfirmHostKey`; a host/port change clears the
  pin. Jobs (3.5) must fill the `checkUnused` delete hook.
- Connections (`internal/connections`, D-135): only the service touches
  the `connections` table (temporary restore rows are excluded until 4.4).
  Credentials (DB password, SSH password/key + passphrase, TLS client key)
  live in one sealed TLV blob (AAD `connections:<id>:secret`), write-only
  in the API. Keys must exist in the embedded catalog; pinned TLS to an IP
  literal/localhost without an SSH tunnel is a VALIDATION error (D-132).
  serve never dials a target: every probe/capture/pull goes through
  `connections.Service` → runner RPCs; workers get requests only via
  `Service.Target(ctx, id)` and must call its release func right after the
  RPC (zeroes the credentials). Pins/host keys are set only by
  `ConfirmPin`/`StageBackupPin`/`ConfirmHostKey` (admin + step-up; a
  `certIndex` pin is re-captured and bound to the compared leaf SPKI);
  host/port changes drop pins, bastion moves drop the host key. Leaving
  PINNED needs `connection.tls_downgrade` and records `security.opt_out`
  + the `AlertFunc`. Runner `TLS_PIN_MISMATCH`/`SSH_HOST_KEY_MISMATCH` are
  audited, alerted and returned as the API codes of the same name; other
  runner failures are `CONNECTION_FAILED` (fixed messages). Runner-calling
  fields are single-root and rate limited. Jobs (3.5) must fill
  `checkUnused`.
- Audit: record through `internal/audit` only (`Record`, or `RecordTx` inside
  the action's `db.Tx`); audit failures fail the operation (fail closed);
  actions come from the typed catalog; details are redacted but still never
  put secrets in them; CLI break-glass events use `audit.CLIEvent` (D-069,
  D-101). Weakened startup posture is audited fail closed
  (`system.insecure_transport_started`, `security.insecure_db_tls`);
  `bekup certs rotate` records `certs.rotated` (D-118). Unused catalog
  actions are marked reserved with their task.
- App DB TLS (D-100, D-103, D-104): every pool/migrator connection takes an
  explicit `db.TLSOptions` (`db.LoadTLSOptions(cfg)`); `database.tls_mode`
  defaults to `verify-full` against `tls_ca_file` only and always beats the
  URL's `sslmode`; `require`/`disable` log a startup warning. The shipped
  Postgres gets the `postgres` cert from `bekup certs` via
  `deploy/postgres/entrypoint.sh` and rejects non-TLS logins of the bekup
  roles. SCRAM channel binding is required (D-104): the postgres cert is an
  ECDSA P-256 leaf under a one-time ECDSA issuer (key discarded); the CA
  has path length 1 but runner mTLS accepts only direct chains.
- Outbound connections from `serve` to admin- or user-supplied endpoints
  (S3, SFTP, SMTP, webhooks) go only through `internal/netguard` (D-055,
  D-120): `Guard.DialContext` (or `Guard.Dialer()` for libraries that need
  a `*net.Dialer`), HTTP via `Guard.NewHTTPClient` (no proxy, no
  redirects, TLS ≥ 1.2) with bodies read through `ReadAllLimited` /
  `LimitReader`. Validate entered hosts with `ResolveAndCheck`, but rely on
  the dial-time check. Build the guard with `netguard.FromConfig`; the
  admin allowlist is applied with `Guard.SetAllowlist` (validated; hard
  ranges and loopback outside dev can never be allowlisted). Show
  `*BlockedAddressError` details to admins only; return
  `netguard.Normalize(err)` to everyone else. Never set
  `http.Transport.Proxy` or use `http.DefaultClient` for such endpoints.
- Engines (`internal/engine`, D-123): dump/restore images come only from
  `engine.Embedded()` (+ `Catalog.WithExtra` for the runner's extra file);
  never build an `Entry` or image reference yourself. Pick clients with
  `SelectDumpClient` / `SelectRestoreClient` / `CheckDumpCompat`; build
  invocations only with the `engine.Engine` builders, which validate
  names, keep secrets in preamble `Files` (`PGPassLine`,
  `MySQLOptionFile`) and pin the hardening flags. Catalog changes must
  pass the per-series client floors (`floors.go`); Renovate edits the
  `image = "..."` lines.
- Storage (`internal/storage`, D-121): build object keys only with
  `storage.BuildKey` / `ArtifactFileName` / `ParseKey` (never concatenate
  strings; DB names go through `EncodeDBName`). Every backend implements
  `storage.Destination` and must pass `storagetest.Run`: Put is atomic
  and never overwrites (`ErrAlreadyExists`), Delete is idempotent and
  refused in no-delete mode or under a lock, lock options fail closed
  (`CheckPutOptions`), `Test` uses `storage.RunProbe`. Show users only
  `storage.Classify(err)` and its `Message()`, never raw backend errors.
  Disk access only through the `os.Root` in `storage.Disk`; symlinks are
  never followed.
- S3 destinations (`internal/storage/s3store`, D-129): `s3store.Open`
  with a mandatory `*netguard.Guard` and static keys only; never build an
  S3 client with `config.LoadDefaultConfig` (no env/shared-config/IMDS
  credentials) and never issue presigned URLs. https only unless
  `AllowHTTP` (dev mode). Uploads use the package's own multipart
  uploader (memory = part × concurrency, default 64 MiB × 2; aborted on a
  detached context on every failure; `If-None-Match: *`), not
  `feature/s3/manager`. Deletes on versioned/locked buckets remove exact
  versions; never send the governance-bypass header.
- SFTP destinations (`internal/storage/sftp`, D-128): `sftp.Open` with a
  mandatory `HostKeyPin` and a mandatory `*netguard.Guard`; it takes
  ownership of `Auth`. Same host-key/algorithm policy as
  `internal/tunnel` (duplicated locally; keep both in sync). Finalize only
  via `hardlink@openssh.com` (never `posix-rename`), every directory below
  the base is Lstat-checked (no symlinks), partials are `.<hex>.partial`.
- SSH tunnels (`internal/tunnel`, D-122): only through `tunnel.Dial` with
  a mandatory `HostKeyPin` and a mandatory `Dialer` (the runner's
  allowlist dialer; never a bare `net.Dialer` in production). `Dial`
  consumes and destroys `Auth`; only the pinned key type is negotiated
  (RSA ≥ 2048, rsa-sha2 only); a `HostKeyMismatchError` (also when the
  server does not offer the pinned type) is a security event. TOFU only
  via `tunnel.FetchHostKey` (no auth attempted) plus admin confirmation.
- Runner API (D-126, D-127): edit `proto/runner/v1/runner.proto`, then
  `just gen` (buf + protoc plugins are go.mod tools; never edit
  `internal/runnerpb/*.pb.go`); `just lint` runs buf lint/format and
  `buf breaking` against main. No free-form command/arg/env/image/path
  fields; credentials are `bytes` with `[(runner.v1.secret) = true]`
  (a test enforces it) and are zeroed with `runnerpb.ZeroSecrets`. A new
  RPC needs a class in `internal/runner` `methodClasses` (run/control/
  unlimited; unknown = denied; a test checks coverage). Every call needs a
  deadline; handlers get the caller from `runner.CallerFromContext`; logs
  never contain payloads. Clients use `internal/runnerclient` only. The
  compose healthcheck uses the runner's local health socket
  (`listen.health_socket`, D-125), never a client certificate.
- Runner requests and containers (D-130): every RPC handler first calls
  its `internal/runner/validate` validator (unknown fields, size limits,
  allowlisted engine/version, names, pins, SSH pin; static error
  messages) and uses only the validated view; secrets stay in the request
  until `runnerpb.ZeroSecrets`. The runner dials targets and bastions only
  through `targets.Guard.DialContext` (target allowlist re-checked at dial
  time; empty `[targets]` = deny all; own networks registered with
  `SetOwnNetworks`). Container specs come only from `spec.Build` (digest-only
  image, fixed hardening, constant env); secrets reach the container only
  through `preamble.Encode`/`ForCommand` on stdin, and the encoded buffer
  is zeroed after writing.
- Runner Docker access (D-132): only `internal/runner/executor` talks to
  Docker (`executor.Docker`, moby client over the `docker.host` unix
  socket). RPC handlers run containers only through `Executor.Run` with a
  `RunSpec` built from validated requests (`Command` builds the argv for the
  `ClientEndpoint` the executor chooses; never put the target host into the
  argv yourself). `Run` consumes the preamble material and tunnel
  credentials, always cleans up, and maps errors with `executor.Status`.
  Per-run networks come from `docker.run_subnet_pool`; they, the pool and
  the runner's own networks are guard "own networks" and refused by the
  gRPC listener. Verified TLS to an IP literal works only behind an SSH
  tunnel (a /29 around the literal, never overlapping the runner's or
  Docker's networks, gateways, name servers or default gateways); direct
  ones fail closed. Every Docker subnet/gateway is handed to the guard
  (`SetDockerNetworks`) and denied unless an `allow_cidrs` entry inside it
  allows it. Image `VOLUME`s get a read-only tmpfs (`spec.Params.ImageVolumes`,
  no anonymous volumes); Docker Engine < 26.0.0 is refused at `Init`; every
  psql run gets `engine.PSQLRestrictPrefix` on stdin (`Command.StdinPrefix`,
  checked by `spec.Build`); sequential containers of one run use
  `RunSpec.Step` (D-138).
- Target-DB pinning only through `internal/runner/pinning` (D-133):
  every PINNED connection first passes `Verifier.Preflight` (one pin must
  verify the chain with its anchor as the only root, host name/IP checked,
  and match the leaf SPKI); the preamble `ca` record is
  `Verifier.TrustAnchor(pins)`; the runner's own Go drivers use
  `pinning.ClientTLSConfig`; reach targets with `pinning.Connect` (guard or
  tunnel). Map errors with the RPC layer's `targetStatus` (pin mismatch =
  `runnerpb.PinMismatchStatus`: FAILED_PRECONDITION + `ErrorInfo`
  `TLS_PIN_MISMATCH` with metadata `check`, read in serve with
  `runnerpb.PinMismatchCheck`). Pin anchors must be a self-signed leaf or
  a private CA: refuse public ones with `pinning.IsPublicAnchor` (D-139).
  There are exactly three `InsecureSkipVerify` uses in bekup:
  `pinning.Checker.Fetch` (capture, handshake aborted, nothing sent;
  D-133), `dbprobe.UnverifiedTLS` (the audited admin downgrade TLS mode
  UNVERIFIED; D-123, D-134, D-135) and `internal/db` `tls_mode = require`
  (D-100, D-103); never add another. Never let a TLS config fall back to
  system roots (nil `RootCAs`).
- Runner RPC handlers (`internal/runner/ops*.go`, D-134): validate →
  `operations.precheck` (fresh upstream via `pinning.Connect`, pin
  pre-flight for PINNED, `dbprobe` Go-driver connection: server version,
  database list, D-033 "name must be in the server-reported list",
  `engine.CheckDumpCompat` / `SelectRestoreClient`) → `Executor.Run`
  (offline mode, `NetworkMode: none` and no preamble files, for
  `pg_restore` verification). Map target errors with `targetStatus` to a
  `runnerpb.Reason` (`Reason.Status()`, decoded with
  `runnerpb.ErrorReason`); anything before the database answered is
  `TARGET_UNREACHABLE` (no port-scanning oracle). Never return or log
  server messages or tool stdout of restores; log one `runner_rpc_audit`
  line per call (FetchServerCertificate and ListImages included). A new
  handler copies credentials only into buffers the executor zeroes
  (`operations.material`, `tunnelCreds`), and sends its final stream
  message with `output.Final` (never concurrently with the output reader).
  `targets.bastion_targets` restricts what a bastion may forward to
  (`Guard.CheckBehindBastion`); unlisted bastions are logged once
  (`runner_bastion_unrestricted`).
- SSH (D-140): every SSH client and every service that stores SSH
  settings uses `internal/sshpolicy` only: `Pin`/`Normalize` and
  `NewPinCallback`/`PinCheck.Violation` (or `ClientConfig`) for host keys,
  `Algorithms`/`HostKeyAlgorithms`, `ParseClientKey` (+ `Destroy`) for
  private keys, `ParseHostKeyLine` for admin-entered host keys (≤ 8 KiB),
  `ValidateUser`, `Handshake` and `Keepalive`. Never copy the policy into
  another package; a host-key mismatch is a security event
  (`storage.CodeHostKeyMismatch` for destinations). Host-key confirmations
  carry the fetched host/port (`expectedHost`/`expectedPort`, CONFLICT if
  changed); fetches only probe stored records.
- Host names (D-140): validate every host from config or the API with
  `hostname.Validate` (via `engine.ValidateHost`, netguard, tunnel, sftp);
  no package keeps its own host rule.
- Connections/destinations (D-140): share `serviceutil` (`FieldError`,
  `Limiter`, `IsUniqueViolation`, `CheckSSHPrivateKey`, `HostKeyLine`). A
  stored password is never sent to a changed endpoint: an update that
  changes host/port/route/bastion/user (SFTP: also base path) must carry
  the password again (VALIDATION otherwise) and audits
  `reentered_for_new_endpoint`. Workers open destinations through
  `Service.Open`, which also opens soft-deleted rows.
- Runner mTLS: clients are identified only by the URI SAN
  `spiffe://bekup/<component>`; the runner cert uses DNS `runner`; the CA is
  name-constrained, its key age-encrypted and kept offline; CA rotation is
  stage → promote → finish (D-089, D-090; `internal/certs`).

## Task runner

The **`justfile` is the single entry point** (task 0.1b, D-020). Run `just`
with no arguments for grouped help. Key recipes: `just doctor` (dev-machine
tool check; **a task that introduces a new tool must add it to
`scripts/doctor.sh`**), `just dev` (infra only: app Postgres with TLS on
127.0.0.1, mailpit, adminer; D-114), `just run` (native `bekup serve`, dev
mode), `just dev-migrate`, `just admin-create <email> <name>`,
`just web-dev`, `just dev-full` (containerised stack, profile `full`),
`just dev-down`, `just gen`,
`just lint`, `just sec`, `just test`, `just test-int`, `just test-e2e`,
`just build`, `just docker`, `just db-reset`, `just db-schema`, `just migrate-new <name>`,
`just keygen`, `just certs`, `just docs` (config key reference, later the
CLI/GraphQL references), `just bump <level>`, `just release`.
Recipe arguments are passed as positional parameters, never interpolated
(D-071); recipes for tooling from later tasks fail with "not available yet
(task X.Y)". `just bump` / `just release` follow D-072 (strict x.y.z, clean
`main` only, signed commit + tag, git-cliff changelog); agents never run them.
Details: [`docs/development.md`](docs/development.md).
