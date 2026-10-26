# Development

> **Status:** the task runner, release, database (task 0.4), web UI (task
> 0.8) and CI (task 0.9) sections are complete for M0. The GraphQL workflow
> (gqlgen, task 1.2) and E2E tests (task 7.2) are added by their tasks.

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
`golangci-lint`, Docker, and for the web UI Node.js + pnpm. The security
scanners `govulncheck` and `osv-scanner` and, for releases, `git-cliff` are
needed for `just sec` and `just bump`; the recipes print an install hint when a
tool is missing.

Recipes whose tooling arrives in a later task fail with
`<thing>: not available yet (task X.Y)` instead of a cryptic error.

| Group | Recipe | What it does |
|---|---|---|
| dev | `just` / `just help` | Styled help screen |
| dev | `just dev` | Starts the dev stack (`docker-compose.yml` + `docker-compose.dev.yml`) |
| dev | `just gen` | Runs sqlc, gqlgen (task 1.2), graphql-codegen (`web` script `gen`) and the config key reference generator (`go run ./internal/config/gendocs`) |
| quality | `just lint` | `golangci-lint` (incl. gosec, also `integration`-tagged files), `sqlc vet` + `sqlc diff`; with `web/`: eslint + `tsc --noEmit` |
| quality | `just test` | Script tests, `go test -race ./...`; with `web/`: vitest |
| quality | `just test-scripts` | Tests for the release scripts (semver math, bump refusals, happy path) |
| quality | `just test-int` | Integration tests (`-tags integration`, testcontainers; needs Docker) |
| quality | `just test-e2e` | Playwright E2E (`web` script `test:e2e`) |
| security | `just sec` | `govulncheck` + `osv-scanner`; fails if a scanner is missing |
| build | `just build` | Web build (if present), then a static, trimmed `bin/bekup` with the version from `VERSION` |
| build | `just docker` | Builds the image `bekup:<version>` and `bekup:dev` |
| db | `just db-reset` | Asks for confirmation, wipes the dev stack volumes, starts Postgres and migrates |
| db | `just db-schema` | Regenerates `internal/db/schema.sql` (pg_dump of all migrations in a test container) |
| db | `just migrate-new <name>` | Creates `internal/db/migrations/NNNNN_<name>.sql` (name: `^[a-z][a-z0-9_]*$`) |
| keys | `just keygen [out]` | `bekup keys generate --out` into `secrets/bekup_master_key` (keyring format, 0600; never overwrites; prints escrow guidance) |
| keys | `just certs [dir] [args...]` | `bekup certs init` into `secrets/certs/` (mode 0700); extra args are passed through, e.g. `--runner-san bekup-runner` |
| docs | `just docs` | Regenerates the key reference in `docs/configuration.md`; later also `docs/reference/` (CLI reference from cobra, task 7.5; GraphQL schema, task 1.2) |
| release | `just bump [level]` | Version bump, changelog, signed commit and tag (see below) |
| release | `just release` | Asks for confirmation, verifies the signed tag, pushes `main` + tag |

Recipe arguments are passed to the shell as positional parameters, never
pasted into the script text, and are validated by the recipe (D-071). Longer
logic lives in `scripts/` (POSIX `sh`, plus `scripts/lib.bash` for the shared
recipe helpers).

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
the roles exactly like the deploy init script.

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

## Web UI (`web/`)

Stack (D-026, D-084): Vite 8, React 19, TypeScript 6 (strict +
`noUncheckedIndexedAccess` + `exactOptionalPropertyTypes`), Tailwind CSS v4,
shadcn/ui (Radix), Apollo Client 4 (HTTP + `graphql-ws`), graphql-codegen
(client preset), React Router 8 (data mode), react-hook-form + zod,
lucide-react. The package manager is pnpm, pinned through `packageManager`. Its
settings live in `web/pnpm-workspace.yaml`: exact versions, a 3-day minimum
release age, and no dependency install scripts.

```sh
cd web
pnpm install
pnpm dev          # http://localhost:5173; proxies /graphql (incl. WebSocket) and /api to :8080
pnpm gen          # graphql-codegen -> src/graphql/gen (placeholder schema until task 1.2)
pnpm lint         # eslint: typescript-eslint strict-type-checked, react-hooks, jsx-a11y strict
pnpm typecheck    # tsc -b --noEmit
pnpm test         # vitest + Testing Library (jsdom), including the WCAG contrast gate
pnpm build        # type-check + production build into web/dist (embedded by web/embed.go)
pnpm csp-check    # production CSP regression check in headless Chrome (see below)
pnpm format       # prettier (with the Tailwind class sorter)
```

Set `BEKUP_DEV_BACKEND` to proxy to a different backend.

**Vite dev server and the Origin checks (note for task 1.1).** The Vite
proxy keeps `changeOrigin: false`, so requests reach `serve` with the
browser's real `Origin: http://localhost:5173` while `server.base_url` is
`http://localhost:8080`. The Origin/`Sec-Fetch-Site` checks of task 1.1
(D-050) must therefore accept the Vite origin **in dev mode only** (for
example `http://localhost:5173` and `http://127.0.0.1:5173`), for both
GraphQL POSTs and the WebSocket upgrade. Outside dev mode, only the
`base_url` origin is accepted. Don't "fix" this with `changeOrigin: true`:
that rewrites only the `Host` header, not `Origin`, and would hide
host-header mistakes.
`web/embed.go` (`package web`) embeds `web/dist` with `//go:embed all:dist`
and exposes `web.DistFS()`. `web/dist/.gitkeep` is committed, and the build
recreates it, so `go build ./...` works before the first web build.

**GraphQL client.** `src/lib/apollo.ts` sends queries and mutations as POST +
JSON to `/graphql` with `credentials: "same-origin"`. Subscriptions use
`graphql-ws` on the same path, with `ws:`/`wss:` derived from the page
location. Until `internal/graph/schema/*.graphqls` exists (task 1.2),
`codegen.ts` uses `web/graphql/placeholder.graphqls`. Delete that file once the
real schema lands.

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
| `go` | `go build ./...`, `go vet ./...`, `go test -race ./...`, golangci-lint (incl. gosec, `--build-tags integration`), `sqlc vet`, `sqlc diff` | `just lint` + `just test` |
| `go-integration` | Integration tests with testcontainers (Docker on the runner) | `just test-int` |
| `web` | frozen `pnpm install`, `lint`, `typecheck`, `test`, `build`, `csp-check` (headless Google Chrome) | `just lint` + `just test` + `just build`, then `pnpm csp-check` |
| `security` | govulncheck + osv-scanner (pinned versions) | `just sec` |
| `generated` | `go mod tidy -diff`, `just gen`, then fails on any uncommitted change | `go mod tidy && just gen`, then commit |
| `scripts` | `just --fmt --check`, shellcheck on `scripts/` and `deploy/**/*.sh`, `just test-scripts` | same commands |
| `compose` | `docker compose config` for `docker-compose.yml` and the dev overlay, all profiles | same commands |
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

Run the workflow linters locally before pushing changes to `.github/`:

```sh
go run github.com/rhysd/actionlint/cmd/actionlint@latest
uvx zizmor .github/          # or: pipx run zizmor .github/
```

Release workflows (signed multi-arch image, SBOM, provenance) arrive in task
7.4.
