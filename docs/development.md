# Development

> **Status:** the task runner, release, database (task 0.4), web UI (task
> 0.8), CI (task 0.9) and GraphQL (task 1.2) sections are complete. E2E
> tests (task 7.2) are added by their task.

How to work on bekup: prerequisites, `just` recipes, code generation (sqlc,
gqlgen, graphql-codegen), testing (unit, integration with testcontainers, E2E),
and the release and version-bumping process. See also
[AGENTS.md](../AGENTS.md).

## Task runner: `just`

The [`justfile`](../justfile) is the single entry point for every recurring
task (D-020). Run `just` without arguments for a grouped, colored help screen
with the current version; `just --list` prints the plain list. Colors are off
when `NO_COLOR` is set or the output is not a terminal.

Run **`just doctor`** first on a new machine: it checks every required tool
(minimum versions, the pnpm version pinned in `web/package.json`, the Go
version from `go.mod`), whether the Docker daemon is reachable and whether git
signing is set up, and prints install hints for macOS/Linux. It exits non-zero
when a required tool is missing or outdated.

Prerequisites: [just](https://just.systems/) ≥ 1.44, Go (see `go.mod`),
`golangci-lint` (exactly the version pinned as `GOLANGCI_LINT_VERSION` in
`ci.yml`; `just lint` refuses another one unless
`GOLANGCI_LINT_ANY_VERSION=1`), Docker, and for the web UI Node.js + pnpm. The security
scanners `govulncheck` and `osv-scanner` and, for releases, `git-cliff` are
needed for `just sec` and `just bump`; the recipes print an install hint when a
tool is missing.

Recipes whose tooling arrives in a later task fail with
`<thing>: not available yet (task X.Y)` instead of a cryptic error.

| Group | Recipe | What it does |
|---|---|---|
| dev | `just` / `just help` | Styled help screen |
| dev | `just dev` | Starts the dev infrastructure (app Postgres with TLS, mailpit, adminer; all on `127.0.0.1`), generating missing dev keys first, and applies the migrations natively (see [Local development](#local-development)) |
| dev | `just run` | Builds `bin/bekup-dev`, migrates and runs `bekup serve` natively in dev mode on `http://localhost:8080` |
| dev | `just run-watch` | Like `just run`, but with [air](https://github.com/air-verse/air) (`.air.toml`): rebuilds, applies new migrations and restarts `serve` when Go, SQL, GraphQL, proto or TOML files change (needs `air`, see `just doctor`) |
| dev | `just dev-runner` | Starts only the runner (mTLS gRPC on `127.0.0.1:7443`) and the sample target DBs for a native `serve` (D-201) |
| dev | `just dev-migrate` | Applies the migrations natively as `bekup_owner` |
| dev | `just admin-create <email> <name>` | Creates an admin account on the dev DB (`bekup admin create`; password prompted on the terminal) |
| dev | `just web-dev` | Vite dev server with hot reload on `http://localhost:5173`, proxying the API to `:8080` |
| dev | `just dev-full` | The full containerised stack (profile `full`: air serve, runner, sample DBs, MinIO, SFTP, bastion) |
| dev | `just dev-down` | Stops every dev container of every profile; volumes are kept |
| dev | `just gen` | Runs sqlc, `buf generate` (runner API: `proto/` → `internal/runnerpb`), gqlgen, graphql-codegen (`web` script `gen`), the config key reference generator (`go run ./internal/config/gendocs`) and `scripts/gen_compose.sh`, which renders the committed compose files `deploy/compose/*` from `docker-compose.yml` (`bekup setup compose --variant <v>`, D-205 amendment 3; never edit them by hand) |
| quality | `just lint` | `golangci-lint` (incl. gosec) for `GOOS=linux` with and without the `integration` tag, plus a native pass on non-Linux hosts (OS-specific files differ); shellcheck (`scripts/shellcheck.sh`: the local shellcheck if it is the pinned version, else the digest-pinned `koalaman/shellcheck` image via Docker); `sqlc vet` + `sqlc diff`, `buf lint` + `buf format --diff` (+ `buf breaking` against `main` once `proto/` exists there); with `web/`: eslint + `tsc --noEmit` |
| quality | `just test` | Script tests, `go test -race ./...`; with `web/`: vitest |
| quality | `just test-scripts` | Tests for the release scripts (semver math, bump refusals, happy path, `just publish`) |
| quality | `just test-int` | Integration tests (`-tags integration`, testcontainers; needs Docker) |
| quality | `just test-e2e` | Playwright E2E (`web` script `test:e2e`) |
| security | `just sec` | `govulncheck` + `osv-scanner`; fails if a scanner is missing |
| build | `just build` | Web build (if present), then a static, trimmed `bin/bekup` with the version from `VERSION` |
| build | `just compose-examples [image]` | Renders the compose variants (`traefik`, `own-proxy`, `full`, each bundled and `--database external`) into `dist/compose/` (gitignored) with `bekup setup compose`; with a pinned image the `x-bekup-image` line names it (like the release assets), otherwise it is the `${BEKUP_IMAGE:?...}` placeholder of the committed `deploy/compose/` files (D-205) |
| build | `just docker` | Builds the image `bekup:<version>` and `bekup:dev` |
| db | `just db-reset` | Asks for confirmation, wipes the dev stack volumes, starts the dev infrastructure and migrates natively |
| db | `just db-schema` | Regenerates `internal/db/schema.sql` (pg_dump of all migrations in a test container) |
| db | `just migrate-new <name>` | Creates `internal/db/migrations/NNNNN_<name>.sql` (name: `^[a-z][a-z0-9_]*$`) |
| keys | `just keygen [out]` | `bekup keys generate --out` into `secrets/bekup_master_key` (keyring format, 0600; never overwrites; prints escrow guidance) |
| keys | `just certs [dir] [args...]` | `bekup certs init` into `secrets/certs/` (mode 0700); extra args are passed through, e.g. `--runner-san bekup-runner` |
| docs | `just docs` | Regenerates the key reference in `docs/configuration.md`; later also `docs/reference/` (CLI reference from cobra, task 7.5; GraphQL schema, task 1.2) |
| release | `just bump [level]` | Version bump, changelog, signed commit and tag (see below) |
| release | `just release` | Asks for confirmation, verifies the signed tag, pushes `main` + tag |
| release | `just publish [repo] [--dirty] [--yes] [--dry-run]` | Builds a multi-arch image (provenance + SBOM) and pushes it to your own registry; prints the digest pin (see [below](#publishing-your-own-image), D-202) |

Recipe arguments are passed to the shell as positional parameters, never
pasted into the script text, and are validated by the recipe (D-071). Longer
logic lives in `scripts/` (POSIX `sh`, plus `scripts/lib.bash` for the shared
recipe helpers).

## Local development

`just dev` starts **only the infrastructure** in containers; bekup itself
runs natively on your machine (D-114):

```sh
just doctor                                  # once: required tools, Docker daemon
just dev                                     # dev keys if missing, postgres + mailpit + adminer, migrate
just admin-create you@example.com "Your Name"  # first admin (the password is prompted)
just run                                     # bekup serve on http://localhost:8080 (dev mode)
just web-dev                                 # optional: UI with hot reload on http://localhost:5173
just dev-down                                # stop the containers (data is kept)
```

| Service | Address (loopback only) | Notes |
|---|---|---|
| app Postgres | `127.0.0.1:5432` | TLS only for `bekup_owner` / `bekup_app` (hostssl, SCRAM with channel binding); the superuser is socket-only, as in production |
| mailpit | SMTP `127.0.0.1:1025`, UI <http://127.0.0.1:8025> | `deploy/dev/bekup.local.toml` allows `127.0.0.1/32` in the SSRF guard |
| adminer | <http://127.0.0.1:8081> | System **PostgreSQL**, server `postgres`, database **`bekup`**, user `bekup_app` (password in `secrets/bekup_app_password`) or `bekup_owner` (`secrets/bekup_owner_password`) |

- **Dev keys.** `just dev` runs `deploy/dev/gen-dev-keys.sh` when something
  is missing (it never overwrites existing files without `--force`). Besides
  the container files in `secrets/` it writes `secrets/local/` for native
  processes: both role URLs pointing at `127.0.0.1:<port>`, and copies of
  the master keyring, the DB CA bundle and serve's runner client
  certificate (`bekup_client.{crt,key}`, `bekup_ca.crt`). The dev postgres certificate
  names `postgres`, `localhost` and `127.0.0.1`, so the host connects with
  `verify-full` against the dev CA, exactly like production. A dev PKI from
  before D-114 is re-issued automatically (and the postgres container
  re-created).
- **Native config.** `just run`, `just run-watch`, `just dev-migrate` and `just admin-create`
  use `deploy/dev/bekup.local.toml` (dev mode, `listen =
  "127.0.0.1:8080"`, `base_url = "http://localhost:8080"`); the justfile
  sets the absolute `*_file` paths into `secrets/local/` through
  `BEKUP_DATABASE_URL_FILE`, `BEKUP_DATABASE_TLS_CA_FILE` and
  `BEKUP_SECURITY_MASTER_KEY_FILE` and `BEKUP_RUNNER_{CERT,KEY,CA}_FILE`.
  Only the migration step gets `BEKUP_DATABASE_OWNER_URL_FILE`; `serve`
  never sees the owner URL. `just dev` starts no runner: `serve` points at
  `127.0.0.1:${BEKUP_DEV_RUNNER_PORT:-7443}` (`BEKUP_RUNNER_ADDRESS`),
  connects lazily and logs `runner health check failed`; connection tests,
  backups and restores fail until a runner listens there.
- **Local storage.** The native environment points `storage.disk_root` at
  `.dev-data/backups` and `storage.scratch_dir` at `.dev-data/scratch`
  (gitignored, created 0700). Add a **Disk** destination with a relative
  path such as `local`; its encrypted backups land in
  `.dev-data/backups/local/`. `just db-reset` does not touch `.dev-data/`:
  delete it by hand to start over.
- **Dev runner for native serve.** `just dev-runner` (compose profile
  `runner`, D-201) starts only the runner (`runner-host`) and the sample
  targets (`pg15`, `pg15-tunneled`, `mysql`, `mariadb`, `bastion`) next to
  `just dev`, `just run` and `just web-dev`. Its mTLS gRPC port is published
  on `127.0.0.1:${BEKUP_DEV_RUNNER_PORT:-7443}` from a dedicated
  `runner-host` network (`10.213.254.0/24`, runner at `.10`; moved off
  `10.213.253.0/24` by D-205, which reserves that range for the
  `bekup-proxy` link network of the own-proxy link mode, D-215: after updating, run `just dev-down` and
  `docker network rm bekup_runner-host` once; the production `runner-ctl` network is internal, so
  Docker cannot publish from it); it is not on `backend`/`dev-host` and does
  not listen on `targets`. The recipe sets `DOCKER_GID=0` on macOS (Docker
  Desktop) unless set. Use the host names above in connections; the
  runner's allowlist is `deploy/dev/runner.dev.toml`. Do not run it together
  with `just dev-full` (same sample targets).
- **Plain HTTP.** `serve` logs the D-095 warning at every start and the UI
  shows the red "not encrypted" banner; both are expected in dev. Passkeys
  work, because browsers treat `localhost` as a secure context. `serve`
  embeds `web/dist`; without `pnpm build` in `web/` use `just web-dev`.
- **Ports in use?** Move them with `BEKUP_DEV_PG_PORT`,
  `BEKUP_DEV_ADMINER_PORT`, `BEKUP_DEV_MAILPIT_PORT` and
  `BEKUP_DEV_SMTP_PORT` (e.g. `BEKUP_DEV_PG_PORT=15432 just dev`); set them
  for `just run` and friends too. `BEKUP_DEV_KEYS_DIR` moves `./secrets`.
- **psql from the host:**
  `psql "host=127.0.0.1 port=5432 dbname=bekup user=bekup_app sslmode=verify-full sslrootcert=secrets/local/postgres_ca.crt channel_binding=require"`.
- **Linux.** Run `sudo deploy/dev/gen-dev-keys.sh --chown` once so the
  containers can read their files; `secrets/local/` stays owned by you.
- **Full stack.** `just dev-full` (compose profile `full`) starts the
  containerised `serve` with air hot reload after `migrate`, the runner,
  the sample targets, MinIO, SFTP and the SSH bastion (D-081); on Docker
  Desktop run it as `DOCKER_GID=0 just dev-full`. The reverse-proxy and
  `web` profiles need `--profile full` as well. See
  [installation/compose.md](installation/compose.md#development-stack).

## Database: migrations, sqlc and roles

bekup's own Postgres database (D-042, D-054) lives in `internal/db`:

| Path | What |
|---|---|
| `internal/db/migrations/NNNNN_*.sql` | goose migrations (`-- +goose Up` / `-- +goose Down`), embedded via `embed.FS` |
| `internal/db/queries/*.sql` | **every** SQL query, with sqlc annotations (`-- name: X :one`) |
| `internal/db/sqlc/` | sqlc output; generated, committed, never edited |
| `internal/db/schema.sql` | `pg_dump --schema-only` of all migrations, kept for review |
| `internal/db/auditchain/` | Go recomputation of the audit log hash chain |
| `sqlc.yaml` | sqlc config (pgx/v5, interfaces, pointer nullables, type overrides) |

**Roles.** Migrations run as `bekup_owner` (owner of the database and every
object), the app as `bekup_app` (DML only). Both are created by the deploy
init script, never by a migration; see
[security/db-roles.md](security/db-roles.md). `bekup migrate` reads the owner
URL from `database.owner_url_file` (dev mode: `BEKUP_DATABASE_OWNER_URL`); the
app pool uses `database.url_file`. The migrator refuses any other role and
superusers.

**Adding a migration.**

1. `just migrate-new add_jobs` creates the next numbered file.
2. Write the Up and Down SQL. Wrap anything containing `$$` bodies or `DO`
   blocks in `-- +goose StatementBegin` / `-- +goose StatementEnd`
   (wrapping the whole section is fine: it runs as one simple-protocol
   statement). Each migration runs in its own transaction.
3. New tables get `SELECT, INSERT, UPDATE, DELETE` for `bekup_app` through
   the default privileges of `00001`. If the app needs less, `REVOKE` in the
   same migration (as `audit_log` does). Use `timestamptz`, schema-qualify
   objects in functions, and give every function a pinned `search_path`.
4. Add queries to `internal/db/queries/*.sql`, run `just gen`, then
   `just db-schema` and review the `schema.sql` diff.
5. `just test-int` runs the migrations up → down → up on a fresh Postgres,
   compares `schema.sql` and runs the privilege tests.

Migrations are forward-only in production; never edit a released migration
(add a new one). `bekup migrate down-one` exists for dev mode only.

**Applying.** `bekup migrate` (= `migrate up`), `bekup migrate status`,
`bekup --dev migrate down-one`. The migrator holds a Postgres advisory lock on
its own dedicated, unpooled connection while it works, so concurrent
instances wait instead of racing; if the process dies the connection closes
and the lock is released.

**Queries and transactions.** Use the generated `sqlc.Queries` with the pool
or a transaction; multi-statement work goes through
`db.Tx(ctx, pool, func(q *sqlc.Queries) error { ... })`, which commits on
nil, rolls back on error or panic. App connections run with
`statement_timeout` (`database.statement_timeout`),
`idle_in_transaction_session_timeout` = 60s and `READ COMMITTED`.

**River.** River's schema is vendored as goose migrations (`00002`, `00003`),
generated with the River CLI of the version pinned in `go.mod`
(`go run github.com/riverqueue/river/cmd/river@<version> migrate-get
--version N --up/--down`). When upgrading River, add a new goose migration
with the SQL of the new River versions; never use River's own migrator.

**Integration tests** use the `integration` build tag and testcontainers
(`postgres:17-alpine`, digest-pinned like `docker-compose.yml`); they create
the roles exactly like the deploy init script. A test that needs a (Linux)
Docker daemon skips **only** through
`dockertest.Skipf(t, ...)` (`internal/testutil/dockertest`), never with a
bare `t.Skipf`. Locally it skips without Docker; with `CI=true` (set in the
`go-integration` job) it fails, so a missing daemon in CI can't turn the
integration suite silently green.

Harnesses that run a real `bekup runner` container wait with
`runnertest.WaitDockerOK` (`internal/testutil/runnertest`) until the
runner's Health RPC reports `DOCKER_STATUS_OK`: the "runner listening" log
line comes before the executor has found its own container, and dump or
restore RPCs sent in between fail. SFTP tests start the `atmoz/sftp`
server through `sftptest.Start` (`internal/storage/sftp/sftptest`): the
image is amd64 only, so it is requested as `linux/amd64` (emulated on
arm64 hosts), 22/tcp is published on 127.0.0.1 only, readiness waits for
a real SSH banner, and a failed container start is retried with a fresh
container (up to three times, with the container log in the error).

Tests that pull images through the Docker API use
`imagepull.Ensure` / `imagepull.Pull` (`internal/testutil/imagepull`),
never `ImagePull` directly: registries rate-limit bursts of pulls (Docker
Hub answers `toomanyrequests` when many packages start their containers at
once), so rate-limit errors are retried with exponential backoff and
jitter (about 5 s doubling to 60 s, roughly three minutes in all); any
other pull error fails at once. testcontainers-go retries its own pulls.
In CI the `go-integration` and `setup-install` jobs also configure the
Docker daemon with the pull-through mirror `mirror.gcr.io`
(`registry-mirrors` in `/etc/docker/daemon.json`; dockerd falls back to
Docker Hub when the mirror fails). Test images are digest-pinned, so the
mirror cannot substitute their content; only testcontainers' Ryuk reaper is
pulled by tag.

`just test` runs the unit tests with `-timeout 20m` (the CI `go` job does
the same; `just test-int` uses `-timeout 30m`). Keep tests fast anyway:
mark independent tests and subtests `t.Parallel()` (never together with
`t.Setenv` or a process-wide change such as `syscall.Umask`), and keep the
scrypt sealing of the CA key (work factor 18, about 256 MiB per run) out
of tests that do not test it: `internal/certs` tests pass the unexported
`workFactor` option, `internal/setup` tests use `fastCerts` (an unencrypted
test CA) except `TestInitRealPassphrase`; `TestSealKeyProductionWorkFactor`
asserts the production default.

## Releases and version bumping

The version lives in [`VERSION`](../VERSION) (plain `MAJOR.MINOR.PATCH`) and is
injected into the binary with `-ldflags "-X main.version=…"`.

```sh
just bump            # patch: 1.2.3 -> 1.2.4
just bump minor      # 1.2.3 -> 1.3.0
just bump major      # 1.2.3 -> 2.0.0
just bump 1.4.0      # explicit; must be greater than the current version
just release         # push main + v1.4.0 (triggers the release workflow)
```

`just bump` (`scripts/bump.sh`) **refuses** when the repository has no
commits, `HEAD` is not on `main`, the working tree has any change or untracked
file, the tag already exists, the version is not a plain `x.y.z` greater than
the current one, or `git-cliff` is missing. Otherwise it:

1. writes the new version to `VERSION` and `web/package.json`;
2. regenerates `CHANGELOG.md` with git-cliff ([`cliff.toml`](../cliff.toml):
   Conventional Commits → Keep a Changelog sections; `chore(release)`,
   `chore(deps)`, `build`, `ci`, `test`, `style` and `chore` commits are left
   out);
3. creates a **signed** commit `chore(release): vX.Y.Z` (`git commit -S`) and a
   **signed annotated** tag `vX.Y.Z`.

If anything fails before the commit, the working tree is restored. Nothing is
pushed. Commit and tag signing must be configured in git (GPG or SSH; for SSH
also set `gpg.ssh.allowedSignersFile` so signatures can be verified locally).
Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/)
so they show up in the changelog.

`just release` (`scripts/release.sh`) checks that `HEAD` of a clean `main` is
the annotated tag `v$(cat VERSION)` and that its signature verifies, then runs
`git push --atomic origin main vX.Y.Z`.
The tag starts the [release workflow](#release-workflow-releaseyml), which
accepts only tags signed with the pinned release key (OpenPGP).

## Publishing your own image

`just publish` (`scripts/publish.sh`, D-202) builds the `runtime` image for
several platforms and pushes it to a registry **you** run or rent. It is the
operator's own push; official releases (signed, task 7.4) come from the
release workflow, and `publish` never touches git tags or `CHANGELOG.md`.

The registry is never committed. It is taken from, in this order:

1. the recipe argument: `just publish registry.example.com/team/bekup`;
2. the environment variable `BEKUP_IMAGE_REPO`;
3. the gitignored file `.just.env` in the repository root (copy
   [`.just.env.example`](../.just.env.example); `chmod 0600 .just.env`).

`.just.env` is parsed, never sourced: only `KEY=value` lines for
`BEKUP_IMAGE_REPO` and `BEKUP_PLATFORMS` are read (optional `export` prefix
and matching quotes; no expansion), other keys are ignored with a warning, and
the file is refused when it is a symlink, not owned by you, or group- or
world-writable. It is also excluded from the Docker build context.

The repository is `host[:port]/path`, lowercase, with an explicit registry
host (`docker.io/<user>/bekup` for Docker Hub), and without tag, digest,
scheme or `user:pass@`. Credentials only come from your own
`docker login <registry>`; they never pass through the recipe.

```sh
docker login registry.example.com
just publish --dry-run                                  # show tags and the buildx command
just publish                                            # confirm, then build and push
BEKUP_PLATFORMS=linux/arm64 just publish --yes          # one platform, no prompt
```

- **Tags:** on a clean tree `<repo>:<VERSION>`, `<repo>:sha-<12-char sha>` and
  `<repo>:latest`. A dirty tree (changes, untracked non-ignored files, or
  files marked `skip-worktree`/`assume-unchanged`, which hide their changes
  from `git status`) is refused; with `--dirty` only `<repo>:sha-<sha>-dirty`
  is pushed, never the version or `latest`. A warning is shown when `HEAD` is
  not the tag `v<VERSION>`. `sha-…-dirty` tags are mutable: another dirty
  build of the same commit overwrites them, so always pin by digest.
- **Version and context:** a clean build uses a pristine export of `HEAD`
  (`git archive` into a temporary directory) as the build context, so
  ignored or local files (`go.work`, a built `cmd/bekup/bekup`, local
  configs) can never enter the image; `--dirty` builds the working tree. The
  binary reports `<VERSION>` only on the release tag `v<VERSION>`, otherwise
  SemVer build metadata: `<VERSION>+sha.<12-char sha>`, and
  `<VERSION>+sha.<sha>.dirty` with `--dirty`. The Go build runs with
  `GOWORK=off`.
- **Build:** a dedicated buildx builder `bekup-publish` (driver
  `docker-container`, created on first use) runs
  `docker buildx build --platform $BEKUP_PLATFORMS --target runtime --pull
  --provenance=mode=max --sbom=generator=<pinned scanner> --push` with the
  `VERSION`, `VCS_REF` and `BUILD_DATE` build args and
  `BUILDX_GIT_INFO=false` (the provenance gets the revision from `VCS_REF`,
  never the git remote URL). The builder must run on the local Docker: the
  current docker context must be a local socket, and an existing
  `bekup-publish` builder must have exactly one node on that context
  (otherwise remove it with `docker buildx rm bekup-publish` so it is
  recreated). The SBOM generator
  (`docker/buildkit-syft-scanner`) is pinned by digest in `scripts/publish.sh`
  and kept current by Renovate. The web UI is built inside the image, so no host
  Node is needed. Platforms default to `linux/amd64,linux/arm64` (Go
  cross-compiles, no emulation).
- **Result:** the pushed tags, the image digest and a compose pin:
  `BEKUP_IMAGE=<repo>:<first tag>@sha256:…`, ready for the committed
  `deploy/compose/` files (a tag that does not name the binary version,
  for example on a commit without the release tag, needs
  `setup --allow-version-mismatch`; see
  [compose installation](installation/compose.md#using-your-own-image)).
  Inspect it with `docker buildx imagetools inspect <repo>@sha256:…`.
- A confirmation prompt shows the repository and tags; `--yes` skips it, and
  without a terminal `--yes` is required.

## Web UI (`web/`)

Stack (D-026, D-084): Vite 8, React 19, TypeScript 6 (strict +
`noUncheckedIndexedAccess` + `exactOptionalPropertyTypes`), Tailwind CSS v4,
shadcn/ui (Radix), Apollo Client 4 (HTTP + `graphql-sse`), graphql-codegen
(client preset), React Router 8 (data mode), react-hook-form + zod,
lucide-react. The package manager is pnpm, pinned through `packageManager`. Its
settings live in `web/pnpm-workspace.yaml`: exact versions, a 3-day minimum
release age, and no dependency install scripts.

```sh
cd web
pnpm install
pnpm dev          # http://localhost:5173; proxies /graphql (incl. subscription event streams) and /api to :8080
pnpm gen          # graphql-codegen -> src/graphql/gen (from internal/graph/schema)
pnpm lint         # eslint: typescript-eslint strict-type-checked, react-hooks, jsx-a11y strict
pnpm typecheck    # tsc -b --noEmit
pnpm test         # vitest + Testing Library (jsdom), including the WCAG contrast gate
pnpm build        # type-check + production build into web/dist (embedded by web/embed.go)
pnpm csp-check    # production CSP regression check in headless Chrome (see below)
pnpm format       # prettier (with the Tailwind class sorter)
```

Set `BEKUP_DEV_BACKEND` to proxy to a different backend.

**Vite dev server and the Origin checks.** The Vite
proxy keeps `changeOrigin: false`, so requests reach `serve` with the
browser's real `Origin: http://localhost:5173` while `server.base_url` is
`http://localhost:8080`. The Origin/`Sec-Fetch-Site` checks of
`internal/httpserver` (D-050, D-102) therefore accept
`http://localhost:5173` and `http://127.0.0.1:5173` **in dev mode only**,
for state-changing requests (GraphQL POSTs, including the subscription
event streams).
Outside dev mode, only the `base_url` origin is accepted. Don't "fix" this with `changeOrigin: true`:
that rewrites only the `Host` header, not `Origin`, and would hide
host-header mistakes.
`web/embed.go` (`package web`) embeds `web/dist` with `//go:embed all:dist`
and exposes `web.DistFS()`. `web/dist/.gitkeep` is committed, and the build
recreates it, so `go build ./...` works before the first web build.

**GraphQL client.** `src/lib/apollo.ts` sends queries and mutations as POST +
JSON to `/graphql` with `credentials: "same-origin"`. Subscriptions are
Server-Sent Events on the same path (D-124): the Apollo link built on
`graphql-sse` (distinct connections mode) POSTs each subscription with
`Accept: text/event-stream`. A stream the server ends carries a final
error code: `UNAUTHENTICATED`/`FORBIDDEN` trigger a session check (`me`,
then `/login` only if the session really ended: a password change revokes
the stream's old session while the browser keeps a new one),
`SESSION_ROTATED` reopens at once, `UNAVAILABLE` and network errors reopen
with backoff. There is no WebSocket; don't add one. `graphql-ws` still shows
up in `web/pnpm-lock.yaml`, but only as a transitive dependency of
`@graphql-codegen/cli` (through `@graphql-tools/url-loader`, a dev
dependency) that pnpm also uses to satisfy Apollo's *optional*
`graphql-ws` peer. It can't be dropped without breaking codegen. No app
code imports it, and the production bundle doesn't contain it. ESLint
(`no-restricted-imports`) rejects imports of `graphql-ws`,
`@apollo/client/link/subscriptions` and `subscriptions-transport-ws`.
**Stream budget.** Without a proxy the browser talks HTTP/1.1 (6
connections per origin), and the server allows
`graphql.stream_max_per_session` streams (4 by default; more get `429`).
Each tab therefore holds one shared account stream plus at most one page
stream. Pages filter or merge events on the client instead of opening a
stream per item, and pages that need more data poll or refetch. `codegen.ts` generates the typed `graphql()` helper in
`src/graphql/gen/` from the server schema `internal/graph/schema/*.graphqls`
(`just gen` runs gqlgen first, then graphql-codegen). The scalars `Time` and
`UUID` are strings on the client.

**App structure (D-179; feature pages tasks 6.2-6.12).**

```
src/app/              AppProviders, the route table (router.tsx), RootLayout
                      (auth events, step-up host), the session epoch
src/features/<area>/  one folder per area: routes.tsx (lazy route objects),
                      pages and components, operations.ts (GraphQL documents),
                      a pure model.ts where there is logic, its own strings
                      and tests
  auth/               session gate, sign-in operations, ChallengeForm,
                      step-up dialog (useStepUp), forced flows, role guards
  account/            password + strength meter, TOTP, passkeys, recovery codes, sessions
  dashboard/ calendar/ jobs/ runs/ restore/ connections/ destinations/
  users/ notifications/ admin/ (smtp, channels, security-alerts, audit,
                      network, recovery, two-factor)
src/components/shell/ app shell, sidebar, top bar, role nav (nav.ts: items,
                      roles, page titles), banners, SessionWatcher, skeletons
src/components/       ui/ (shadcn/ui primitives, native-select), status/
                      (StatusBadge, SecurityBadge), ssh/ (host-key and keypair
                      dialogs), confirm dialog, copy value, empty state, page header
src/routes/           login, login/verify, profile/*, 404 and error pages, /design (dev)
src/lib/              apollo (links, cache, SSE), api-error, auth-events,
                      return-path, webauthn, qr-svg, password-strength,
                      messages (shell/auth/account strings), format, theme,
                      route-access, zod config, csp/ (style shim)
src/test/             setup, mock-api (renderApp + fixtures), WebAuthn and matchMedia stubs
scripts/              csp-check.mjs, codegen post-processing
vite-plugins/         csp-strict-styles (build-time CSP hardening)
```

- **Routes.** `RootLayout` wraps `/login`, `/login/verify`,
  `/verify-email` (outside the session gate, D-193), `/design` (dev) and
  the signed-in tree. The signed-in tree sits behind `RequireSession`,
  which loads `me` and renders the shell, the forced password change
  (`passwordChangeRequired`) or the forced 2FA enrollment
  (`twoFactorRequired` and a past `twoFactorEnrollmentDeadline`). During
  the grace period the shell shows a deadline banner. Each feature exports
  its lazy route objects from `features/<area>/routes.tsx`, which
  `app/router.tsx` mounts. Every signed-in route declares who may open it
  as its `handle` (`access.nav("/jobs")`, `access.staff`, `access.admin`,
  `access.all` from `lib/route-access.ts`, derived from the nav roles in
  `components/shell/nav.ts`, which also holds the page titles); the one
  `RequireRole` layout route renders a page only for those roles and
  denies routes without a handle. This only shapes the UI; the server
  enforces access. There is no setup page (D-113).
- **Errors (`lib/api-error.ts`).** `apiErrors(err)` normalizes Apollo errors
  to `{code, message, field, requestId}`. `userMessage(err)` shows the
  server's message for codes whose messages are user-safe (VALIDATION,
  RATE_LIMITED, CONFLICT, ...), fixed text otherwise, and the request ID for
  INTERNAL. `applyFieldErrors(err, form.setError, fields)` puts VALIDATION
  errors on react-hook-form fields. The Apollo error link
  (`authEventFor`) maps UNAUTHENTICATED → login with a validated return path
  (`lib/return-path.ts`: same-origin relative paths only),
  SECOND_FACTOR_REQUIRED → `/login/verify`, PASSWORD_CHANGE_REQUIRED /
  REQUIRES_2FA_ENROLLMENT → refetch `me` (the gate shows the forced flow).
  `Login`, `Me` and `Logout` handle their own errors. Sign-in and sign-out
  clear the Apollo cache.
- **Step-up.** Wrap any mutation that may need a fresh confirmation:

  ```tsx
  const { withStepUp } = useStepUp();
  try {
    await withStepUp(() => updateThing({ variables }), { aal2: isRestore });
  } catch (err) {
    reportActionError(err); // ignores a closed dialog (StepUpCancelledError)
  }
  ```

  On `STEP_UP_REQUIRED` the dialog runs `beginStepUp`, offers the account's
  methods (passkey with user verification, TOTP, recovery code; the
  password only for accounts without a factor), completes `completeStepUp`
  and retries the operation (at most twice). `aal2: true` offers only a
  passkey or TOTP (restores, D-057/D-116). `REQUIRES_2FA_ENROLLMENT`
  shows a toast that links to `/profile/security`.
- **WebAuthn (`lib/webauthn.ts`).** Converts the server's
  `{"publicKey": ...}` JSON (base64url) for `navigator.credentials.get` /
  `create` and encodes the response like `PublicKeyCredential.toJSON()`.
  Passkey UI is hidden with the server's reason when
  `webauthnStatus.webauthnAvailable` is false and with a note when the
  browser can't do WebAuthn.
- **TOTP QR code.** The server's SVG can't be inserted as markup under
  Trusted Types, so `lib/qr-svg.ts` parses its exact shape and the page
  re-renders it as React SVG. Anything else falls back to the manual key.
- **Strings.** English only, kept apart from the markup so a translation
  layer can replace them: the shell, auth and account strings in
  `lib/messages.ts`; every feature keeps its own (a `messages.ts` in the
  feature folder or a `T` table at the top of the component).
- **Tests.** `renderApp(path, handlers)` from `src/test/mock-api.tsx`
  renders the real routes and links over a fake GraphQL server that answers
  by operation name (`baseHandlers`, `meFixture`, `ok`, `gqlError`). Use it
  for flows; use plain unit tests for `lib/` and the features' `model.ts`.
  `findBy`/`waitFor` poll up to 10 s and tests get 30 s, because vitest runs
  in parallel (D-195); assert effect-driven state with `waitFor`.
  `pnpm csp-check` walks the real pages against a fake API under the
  production CSP: sign-in → second factor → profile, step-up, TOTP
  enrollment, the dashboard, connections, destinations, jobs, runs, the
  restore wizard and history, the calendar, the notification center and
  `/verify-email`, the admin settings, channels and audit log, and the
  404 page. Extend it when you add a page or an overlay.

**GraphQL server (`internal/graph`, D-105).** Add or change types in
`internal/graph/schema/<area>.graphqls` (extend `Query`/`Mutation` with
`extend type`), run `just gen` (gqlgen updates `generated/`,
`model/models_gen.go` and the resolver stubs in
`resolvers/<area>.resolvers.go`, keeping existing bodies; then the web
types), and implement the resolvers. Every new root field needs an access
directive (`@authenticated`, `@hasRole(roles: [ADMIN])`, `@stepUp`) or an
entry in `graph.PublicRootFields`, otherwise `serve` and the graph tests
fail. Return `apierror.New(code, msg)` for errors the user should see.
In dev mode introspection and field suggestions are on. CI fails if
`just gen` changes anything.

**Content Security Policy (D-082).** The UI runs under `style-src 'self'`
without `'unsafe-inline'` or a nonce, and with Trusted Types. Third-party
runtime `<style>` injection is removed at build time by
`vite-plugins/csp-strict-styles.ts` (the build fails if a library changes) and
replaced by static CSS in `src/styles/csp-compat.css`. Rules for new code: no
inline scripts or styles in `index.html`; no `dangerouslySetInnerHTML` or
`innerHTML` (ESLint enforces this); no `eval`; React `style={...}` props are
fine (CSSOM). Run `pnpm csp-check` after adding or upgrading a UI library. It
needs Google Chrome; set `CHROME_PATH` to use a different Chromium.

**Design system (D-083).** All UI work uses the `frontend-design` skill on top
of the tokens and components here. `pnpm dev`, then open
<http://localhost:5173/design> for the living style guide. The route is
dev-only and is not in production builds. Tokens are in
`src/styles/theme.css`, and their contrast is checked by
`src/styles/theme.test.ts`. See [`web/README.md`](../web/README.md) for the
visual direction.

## Continuous integration (GitHub Actions)

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on every pull
request, on pushes to `main` and in the merge queue (D-027, D-091). Superseded
runs of the same branch are cancelled. Runs on `main` are never cancelled. Jobs
call the same `just` recipes you run locally where possible, so a green
`just lint test sec` locally should mean a green CI run.

| Job | What it runs | Local equivalent |
|---|---|---|
| `go` | `go build ./...`, `go vet ./...`, `go test -race -timeout 20m ./...`, golangci-lint `GOLANGCI_LINT_VERSION` (incl. gosec; with and without `--build-tags integration`), `sqlc vet`, `sqlc diff` | `just lint` + `just test` |
| `go-integration` | Integration tests with testcontainers (Docker Hub pulls through the `mirror.gcr.io` mirror; Docker on the runner, checked first; `CI=true`, so Docker-dependent tests fail instead of skipping) | `just test-int` |
| `web` | frozen `pnpm install`, `lint`, `typecheck`, `test`, `build`, `csp-check` (headless Google Chrome) | `just lint` + `just test` + `just build`, then `pnpm csp-check` |
| `security` | govulncheck + osv-scanner (pinned versions) | `just sec` |
| `generated` | `go mod tidy -diff`, `just gen`, then fails on any uncommitted change (generated code and `deploy/compose/*`) | `go mod tidy && just gen`, then commit |
| `scripts` | `just --fmt --check`, `scripts/shellcheck.sh` (`shellcheck -x` 0.9.0 on `scripts/*.sh`, `scripts/lib.bash` and `deploy/**/*.sh`; findings of every severity fail), `just test-scripts` | same commands (`just lint` runs the shellcheck script) |
| `compose` | `docker compose config` for `docker-compose.yml` and the dev overlay, all profiles | same commands |
| `setup-install` | Docker Hub pulls through the `mirror.gcr.io` mirror. Builds the image, pushes it to a loopback registry for a digest-pinned reference. Setup-first: `bekup setup init --write-compose --variant full` (root + `CAP_CHOWN`, no network), the owners of key files, `docker compose config`, `setup check`, a no-op `setup upgrade --write-compose` and `check --fix-ownership` (D-203). Compose-first, end to end as in the README (D-205): the own-Traefik variant with a static self-signed `bekup.test` certificate (`compose run --rm -T setup`, `up -d --wait`, `/healthz` and the security headers through Traefik, the first admin, `setup check`, `down -v`) and the own reverse proxy variant (D-215): a pinned curl container as the proxy on its own Docker network, `serve` joining it as `bekup-serve` and trusting the proxy by container name (`/healthz` and the CSP through it, a second container's `X-Forwarded-For` ignored with the untrusted-peer warning, the wildcard warning suppressed, `setup check` through compose and with a read-only `docker run`) | the steps in the job, see [compose.md](installation/compose.md#install-with-bekup-setup-container-only) |
| `workflows` | actionlint (with shellcheck on `run:` blocks) and zizmor | `actionlint` and `zizmor .github/` |

CI security rules (D-061, D-091). A change to `.github/` that breaks one of them
fails review, and usually the `workflows` job:

- **Every action is pinned by its full commit SHA** with a `# vX.Y.Z` comment.
  Renovate updates both. To add an action, look up the SHA with
  `gh api repos/<owner>/<repo>/git/ref/tags/<tag>`. For an annotated tag, use
  `git ls-remote https://github.com/<owner>/<repo> 'refs/tags/<tag>^{}'` to get
  the commit SHA.
- Tool versions are pinned as well: in action inputs, or in the workflow `env`
  for tools installed with `go install`/`go run`, which the Go checksum
  database verifies.
- The workflow token is read-only (`permissions: contents: read`). CI uses no
  secrets and has no `pull_request_target` trigger. Every checkout sets
  `persist-credentials: false`.
- `step-security/harden-runner` is the first step of every job. It runs in
  egress **audit** mode for now. Switch it to `block` with an allowlist once the
  set of endpoints is known.
- Never interpolate `${{ github.event.* }}` or other attacker-controlled
  values into `run:` scripts. Pass them through `env:` instead (zizmor checks
  this).

**Renovate and generated files.** `deploy/compose/*` is rendered from
`docker-compose.yml` by `just gen` (D-205 amendment 3). Renovate ignores
`deploy/compose/**` (`renovate.json`) and bumps only the source, so a
Renovate PR that changes an image digest in `docker-compose.yml` fails the
`generated` job ("Run 'just gen' and commit the result") and the setup drift
test (`TestCommittedComposeFiles`) until someone runs `just gen` on the PR
branch and commits `deploy/compose/`. Never edit those files by hand.

Run the workflow linters locally before pushing changes to `.github/`:

```sh
go run github.com/rhysd/actionlint/cmd/actionlint@latest
uvx zizmor .github/          # or: pipx run zizmor .github/
```

`ci.yml` can also be called with `workflow_call`: the release workflow runs
the whole CI on the release tag before it publishes anything, with the input
`disable_cache: true` (no setup-go, setup-node or golangci-lint cache, so no
state written by other workflows enters a release).

### Release workflow (`release.yml`)

[`.github/workflows/release.yml`](../.github/workflows/release.yml) (task 7.4,
D-204) runs only for a pushed tag `vX.Y.Z` (`just release`). Nothing is ever
published from a branch. Its logic lives in
[`scripts/release_ci.sh`](../scripts/release_ci.sh) (tests:
`scripts/release_ci_test.sh`, part of `just test-scripts`).

| Job | What it does | Permissions |
|---|---|---|
| `verify` | Fetches the annotated tag object and `main` (into `refs/remotes/origin/main`) with the token (on a tag push checkout may store only the commit and creates no `origin/main`; the auth header reaches git through `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0`, never argv); the tag equals `v$(cat VERSION)`, is annotated, the name inside the tag object equals the pushed tag (`release_ci.sh tag-object-name`), it points at the run's commit and that commit is on `main`; the tag signature verifies with `git verify-tag` against [`.github/release-signers.asc`](../.github/release-signers.asc) in a temporary GnuPG home, and the signing key's **primary fingerprint** equals `RELEASE_SIGNER_FPR` in the workflow | `contents: read` |
| `ci` | The full `ci.yml` (`workflow_call`, `disable_cache: true`) | `contents: read` |
| `build` | Needs `verify` and `ci`. Only `docker/*` actions. Refuses when `:X.Y.Z` exists (`release_ci.sh tag-state`: only a registry "not found" counts as absent, any other error fails); builds `linux/amd64` + `linux/arm64` (`target: runtime`, `pull`, no cache, `SOURCE_DATE_EPOCH` and `BUILD_DATE` from the commit time, max-mode provenance, SBOM with the generator pinned in `scripts/publish.sh`) and **pushes by digest only** | `contents: read`, `packages: write` |
| `scan` | Trivy on both platforms of the digest (HIGH/CRITICAL with a fix fail, exceptions only in [`.trivyignore`](../.trivyignore) with an expiry) | `contents: read`, `packages: read` |
| `publish` | Global concurrency group `release-latest`. `cosign sign --recursive` (keyless), `cosign attest` of each platform's SPDX SBOM, `actions/attest-build-provenance`; re-checks that `:X.Y.Z` is absent, decides `latest` from the release tags that exist at that moment (`gh api …/git/matching-refs/tags/v` + `release_ci.sh is-latest`), tags the digest `:X.Y.Z` (and `:latest`) and checks the tag resolves to the signed digest | `contents: read`, `packages: write`, `id-token: write`, `attestations: write` |
| `compose` | Needs `verify`, `build`, `publish` (D-205). Renders the four compose files (`compose.yaml`, `compose.own-proxy.yaml` and their `*.external-db.yaml` forms for your own PostgreSQL, D-208, D-215) by running the published digest (`release_ci.sh render-compose`: `setup compose --image <ref>`, `--network none`, read-only, UID 65532, no capabilities, no logs), checks them (`release_ci.sh compose-assets`: exactly one `x-bekup-image` naming the release reference, every image pinned, no `build`), requires the tag's committed `deploy/compose/*` to equal them except for the `x-bekup-image` line, which must be the `BEKUP_IMAGE` placeholder (`release_ci.sh committed-compose`, D-205 amendment 3), writes `SHA256SUMS`, attests the five files (four compose files + `SHA256SUMS`) (`actions/attest-build-provenance`, `subject-path`), signs each with `cosign sign-blob` (bundle `<file>.sigstore.json`), outputs the digest over all ten assets (the five files and their bundles) (`release_ci.sh assets-digest`) and hands them over as a one-day workflow artifact | `contents: read`, `packages: read`, `id-token: write`, `attestations: write` |
| `release` | Needs `compose`; runs only `gh`. Downloads the artifact, refuses it unless `release_ci.sh assets-digest` equals the `compose` job's output, re-runs `compose-assets` and `sha256sum -c SHA256SUMS`, then `gh release create` with the ten assets and notes: the CHANGELOG section of `X.Y.Z`, the pinned image, `docker pull`, `cosign verify` (cosign v3.0 or newer) and `gh attestation verify`, the compose-first install (verify the file under its release name, then save it as `compose.yaml`; `cosign verify-blob` alternative), the own reverse proxy's network and `http://bekup-serve:8080`, and the `setup upgrade` / `setup check` commands; "latest release" follows `publish` | `contents: write` |

`latest` moves only to the highest plain `vX.Y.Z` tag: a patch release for an
older line (for example `1.2.5` after `1.3.0`) gets only its version tag.
Pre-release tags never match the trigger. `latest` is decided right before
tagging inside the global `release-latest` group, so an older line that
finishes late never takes it. (GitHub keeps only one pending run per
concurrency group: a third simultaneous release cancels the pending one
before it tagged anything; re-run it.) Runs of the same tag queue instead
of racing, and an existing version tag is never overwritten (a re-run after a
failure past the tagging step fails; publish a new patch version instead).

After the very first release, check the package settings on GitHub: make
`ghcr.io/jlentink/bekup` public and link it to the repository.

**Release signing key.** Release tags must be signed (OpenPGP) by the key in
`.github/release-signers.asc` (public key only, exported with
`gpg --armor --export <fingerprint>`), and the workflow pins its primary
fingerprint (`RELEASE_SIGNER_FPR`). Both come from the tagged commit, which
must be on `main`, so branch protection on `main` is the root of trust. To
rotate or extend the key: export the new public key into the file, update
`RELEASE_SIGNER_FPR`, and merge both in one reviewed commit on `main`
**before** tagging with the new key. A new signing subkey of the pinned key
or an extended expiry needs only a refreshed file (same fingerprint). An
SSH-signed tag is refused.
