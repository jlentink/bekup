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
bekup.example.toml runner.example.toml sqlc.yaml .air.toml .golangci.yml
go.mod                  # github.com/jlentink/bekup
cmd/bekup/              # single binary entry point (cobra CLI)
proto/runner/v1/        # runner gRPC API (task 2.4; does not exist yet)
internal/
  implemented:  config  logging  db  crypto  certs  audit  instancekeys
                httpserver  app (serve composition, task 1.1)  graph (1.2)
                authz (1.4)  auth (passwords, sessions, lockout: 1.5; factors 1.7-1.9)
                pubsub (LISTEN/NOTIFY bridge + in-process bus: 1.10)
  doc.go only until their task:
    engine (2.1)  tunnel (2.2)  netguard (2.3)  runner, runnerclient (2.4-2.7)
    storage (2.8-2.10)  pipeline (3.1)  scheduler (3.3)  jobs (3.4-3.6)
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
  for integration tests.
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
  never `X-Forwarded-*` or `Host`); state-changing requests and WebSocket
  upgrades must pass the Origin/`Sec-Fetch-Site` check; per-IP rate limit
  and body limit apply to all routes; long-lived routes lift the write
  deadline per request. New serve components plug into `internal/app`;
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
  RATE_LIMITED, CONFLICT, PASSWORD_CHANGE_REQUIRED, SECOND_FACTOR_REQUIRED)
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
- Subscriptions (D-052, D-108): WebSocket on `GET /graphql` with
  `graphql-transport-ws` only, cookie session + exact Origin at the
  upgrade, never credentials from `connection_init`; only subscriptions
  over the socket. Subscription resolvers send every event through
  `wsconn.Deliver` with `wsconn.Authorize(policy, op, ...)` (session
  re-check + `Policy.Authorize` per event with the connection's current
  principal; load data by ID with the caller's rights). Re-validation
  rebuilds the principal like the session middleware (incl. the
  enrollment check) and closes with 4403 when identity, role, AAL or a
  restriction changed (D-118). Session revocations/rotations publish events from
  `auth.Sessions` inside the transaction; cross-component events go
  through `internal/pubsub` (`Publisher.PublishTx`, `Listener`) with
  **IDs only** (`pubsub.Validate`); new channels get a fixed LISTEN
  query in `internal/db/queries/pubsub.sql` and an entry in
  `pubsub` channels. Close codes: 4401 session ended, 4403 access
  changed, 4001 rotated (reconnect).
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
