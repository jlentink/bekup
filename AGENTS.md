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
  (D-093, proposed) or **unprivileged nginx + certbot** (D-094, proposed).
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
| `serve` → app DB | least-privilege roles, envelope-encrypted secrets, append-only audit log (D-046, D-054) |

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
  implemented:  config  logging  db  crypto  certs
  doc.go only until their task:
    httpserver (1.1)  graph (1.2)  audit (1.3)  authz (1.4)  auth (1.5-1.9)
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
  age decryption always uses the bounded helpers (D-088).
- **Never log secrets**, connection strings, tokens or DB contents.
- Secrets in config only as `*_file` paths; never inline, env or argv.
- Deny by default: every GraphQL operation is mapped in the authz matrix.
- Never commit keys, certificates, local configs or `.env` files.
- Network exposure: outside dev mode `serve`/runner refuse wildcard listen
  addresses unless explicitly overridden (D-098); plain HTTP only in dev mode
  or with `server.allow_insecure_http`, always with a startup warning and UI
  banner (D-095).
- App DB roles: `bekup migrate` re-checks the full role/privilege state on
  every run; `serve` must call `db.CheckAppRole` at startup (D-099).
- Runner mTLS: clients are identified only by the URI SAN
  `spiffe://bekup/<component>`; the runner cert uses DNS `runner`; the CA is
  name-constrained, its key age-encrypted and kept offline; CA rotation is
  stage → promote → finish (D-089, D-090; `internal/certs`).

## Task runner

The **`justfile` is the single entry point** (task 0.1b, D-020). Run `just`
with no arguments for grouped help. Key recipes: `just doctor` (dev-machine
tool check; **a task that introduces a new tool must add it to
`scripts/doctor.sh`**), `just dev`, `just gen`,
`just lint`, `just sec`, `just test`, `just test-int`, `just test-e2e`,
`just build`, `just docker`, `just db-reset`, `just db-schema`, `just migrate-new <name>`,
`just keygen`, `just certs`, `just docs` (config key reference, later the
CLI/GraphQL references), `just bump <level>`, `just release`.
Recipe arguments are passed as positional parameters, never interpolated
(D-071); recipes for tooling from later tasks fail with "not available yet
(task X.Y)". `just bump` / `just release` follow D-072 (strict x.y.z, clean
`main` only, signed commit + tag, git-cliff changelog); agents never run them.
Details: [`docs/development.md`](docs/development.md).
