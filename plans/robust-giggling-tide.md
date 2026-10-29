# Plan: finish the config reload fixes, then add `just publish`

## Context

- **Interrupted fix round.** The config reload feature (D-200), the dev runner (D-201) and the job editor change are built. Both reviews have run, and a fix agent was halfway through the findings when plan mode stopped it. Right now `internal/app/configreload_integration_test.go` does not compile (it calls the removed `CountConfigReloadListeners`). The tests, docs and checks for the fixes are missing too. The exact leftover list is in `plans/robust-giggling-tide-agent-a1057e8ab2015b70d.md`.
- **New request.** The user wants a just recipe that builds the bekup container image and pushes it to a registry of their choice, without the registry URL ever being committed.
- **One image.** bekup is one binary and one image: serve, worker, runner, migrate and admin are subcommands of the same `bekup` image (`Dockerfile` target `runtime`; compose uses `${BEKUP_IMAGE:-bekup:dev}`).

## Part 0: finish the review fixes (resume the fix agent)

Hand the leftover list to the fix agent, which keeps its context:

1. **Fix `internal/app/configreload_integration_test.go`** and extend it:
   - **Compile fixes:** `configreload.Listening`; `pg_terminate_backend` by `application_name` prefix; `o.LogLevel = new(slog.LevelVar)`.
   - **New tests:**
     - M1: audit `error_settings` / `applied` / `weakened` survive redaction;
     - liveness and pruning: stopped vs listening, the 7-day and 30-day rules;
     - `expectedProcesses`;
     - L4: one key per applier group in serve, worker and `serve --no-worker`.
2. **Check `internal/graph/config_reload_integration_test.go`** against the new `listening`, `trigger` and `expectedProcesses` fields.
3. **Docs:**
   - D-200: attribute naming; `application_name` liveness is spoofable and display only; the listing and pruning rules; unused keys are not reported; raising the webhook threshold counts as weakening; `RECORD` is not in the API.
   - `docs/security/audit.md`, `docs/configuration.md`, `docs/operations.md`, the alert row in `docs/guides/notifications.md`, SECURITY.md CL-12, and AGENTS.md if needed.
4. **Run every check:**
   - `just gen` twice;
   - build and vet, with and without the integration tag;
   - `just lint`, golangci-lint with the integration tag, `just sec`, `just docs`;
   - `go test -race ./...`;
   - the integration packages with `DOCKER_API_VERSION=1.47 -p 1`;
   - web lint, typecheck, test, build and csp-check.

Then do a short re-check of the M1/M2 fixes by me (or a quick verify agent), and delete the agent's scratch plan file.

## Part D: `just publish` (D-202, proposed)

### Configuration: never committed

- **Registry name:** `BEKUP_IMAGE_REPO`, e.g. `registry.example.com/team/bekup`, with no tag and no digest. It is read in this order:
  1. the recipe's first argument;
  2. the environment;
  3. a gitignored `.just.env` in the repo root.
- **`.just.env` file:**
  - add it to `.gitignore` and to `.dockerignore`, so it never reaches git or the build context;
  - commit `.just.env.example` with documented keys and no real values.
- **Loading `.just.env`:**
  - use a small parser in `scripts/lib.bash` (`load_local_env`) that only accepts `KEY=value` lines for the allowed keys `BEKUP_IMAGE_REPO` and `BEKUP_PLATFORMS`;
  - do not use `set dotenv-load`, which would inject every variable into every recipe;
  - check the file the way bekup checks its config: owned by the user and not group- or world-writable;
  - never `source` the file.
- **`BEKUP_PLATFORMS`** is configurable and defaults to `linux/amd64,linux/arm64`.
- **Registry login is the user's job** (`docker login <registry>`). Credentials never pass through the recipe or argv. A repo string containing `user:pass@` or a scheme is refused.

### Recipe `publish` (group `release`), in `justfile` and `scripts/publish.sh`

`scripts/publish.sh` follows the same pattern as `bump.sh` and `release.sh`:

1. **Check tools:** `need_cmd docker`, and that buildx exists. Add buildx to `scripts/doctor.sh` as an optional check ("image publishing").
2. **Resolve and validate the repo:** the image-reference regex `^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+$` (lowercase path, no tag or digest). If it is missing, fail with a hint pointing at `.just.env.example`.
3. **Check the tree:**
   - a clean tree is required;
   - with `--dirty`, only `sha-<short>-dirty` is pushed, never the version or `latest`, and the recipe warns.
4. **Tags:**
   - `<repo>:<VERSION>` (from `read_version`);
   - `<repo>:sha-<git short sha>`;
   - `<repo>:latest`.
5. **Builder:** reuse or create a dedicated buildx builder `bekup-publish` (driver `docker-container`), because multi-platform pushes need it.
6. **Build and push:**
   ```
   docker buildx build --builder bekup-publish --platform "$BEKUP_PLATFORMS" \
     --target runtime --pull \
     --build-arg VERSION=… --build-arg VCS_REF=<full sha> --build-arg BUILD_DATE=<UTC RFC 3339> \
     --provenance=mode=max --sbom=true \
     --metadata-file "$tmp/meta.json" \
     -t … -t … -t … --push .
   ```
   The web UI is built inside the image (`WEB_BUILD` default `auto`), so no host Node is needed.
7. **Report:**
   - print the pushed tags and the image **digest** (from the metadata file);
   - print the pin hint `BEKUP_IMAGE=<repo>@sha256:…` for compose, since digest pinning matches the project's convention.
8. **Never print** the contents of `.just.env` beyond the repo name.

- **just conventions:** arguments are passed as positional parameters, never interpolated (D-071). Add a `[confirm]` prompt showing the repo and tags before pushing, with `--yes` to skip it.
- **Relation to the release workflow (task 7.4):** that workflow will later handle official signed releases (cosign, GitHub). `just publish` is the operator's own push. The two must not conflict: `publish` never touches git tags or `CHANGELOG`.

### Docs and governance

- **D-202 (proposed):** manual image publishing, covering:
  - the registry kept outside git;
  - the clean-tree rule;
  - the tags;
  - multi-arch builds with provenance and SBOM attestations;
  - the digest pin hint.
  - Alternatives considered: `set dotenv-load` (leaks every variable) and putting the registry in compose `.env` (mixes runtime and build config).
- **Docs:**
  - `docs/development.md`: a recipe table row and a section "Publishing your own image";
  - `docs/installation/compose.md`: use your own image via `BEKUP_IMAGE=<repo>@sha256:…`;
  - the AGENTS.md task-runner line;
  - `just` help, which comes automatically from `[doc]`.
- **Tests:** add `scripts/publish_test.sh`, in the same style as `bump_test.sh`:
  - repo validation;
  - parsing `.just.env`: ignores unknown keys, refuses unsafe file modes, never executes the file;
  - the dirty-tree rules and tag computation;
  - a dry run (`--dry-run` prints the buildx command without running it).
  - Hook it into `just test-scripts` (the existing release-helper test recipe) and run `shellcheck` if it's installed.

## Part E: GitHub README (user request)

The user chose an SVG banner, CI/license/Go/Node badges and a balanced layout.

- **Banner and logo:** add `docs/assets/bekup-banner-light.svg`, `bekup-banner-dark.svg` and `bekup-mark.svg`.
  - Use the UI's design language from `web/src/styles/theme.css`: background oklch(0.978 0.005 85) for light and oklch(0.165 0.012 258) for dark, primary oklch(0.46 0.15 262) / oklch(0.76 0.12 255), with the Instrument Serif wordmark feel. Convert the colours to hex for SVG.
  - Turn the text into paths, or use a safe font stack, because GitHub renders SVG `<img>` without web fonts.
  - Add a subtle motif: a stacked-disk "vault" mark and the tagline "Encrypted, verified database backups — self-hosted".
  - Keep the files small and static: no scripts, no external references (GitHub sanitizes these anyway).
  - Embed them with `<picture>` and `prefers-color-scheme` sources.
  - Use the `frontend-design` skill for the visual direction.
- **Badges:** use the shields.io style.
  - CI from `.github/workflows/ci.yml` (`github.com/jlentink/bekup/actions/workflows/ci.yml/badge.svg`);
  - MIT license;
  - Go 1.27, from `go.mod`;
  - Node 22, from the Dockerfile `NODE_IMAGE`;
  - optionally an "under development" status badge;
  - keep the development notice as a callout (`> [!WARNING]`).
- **Structure** (concise, scannable, GitHub-flavored markdown):
  1. Banner, badges, a one-line pitch and quick links (Docs · Getting started · Security · Decisions).
  2. **Why bekup:** 3–4 sentences.
  3. **Security first:** a compact two-column table or emoji-free bullet grid, true to the code:
     - encrypted and signed backups, verified restores;
     - a runner sidecar as the only component with Docker access, isolated per-run networks;
     - certificate and host-key pinning, mTLS;
     - envelope-encrypted, write-only credentials;
     - passkeys, TOTP and step-up, with fresh 2FA before a restore;
     - a hash-chained audit log;
     - an SSRF guard;
     - every weakening audited and alerted.
  4. **Features:** grouped as Databases, Destinations, Scheduling and retention, Restore, Access, Notifications and monitoring, Operations.
     - Include the newer items: calendar, start early, test restores, downloads, offline tools, config reload, per-user notification preferences.
     - The "planned for the first release" wording becomes "Features" plus the status callout.
  5. **Architecture:** a Mermaid flowchart showing browser → reverse proxy → `bekup serve` (and workers) → app Postgres; serve → mTLS → `bekup runner` → Docker → per-run dump containers → target DBs; serve → SSRF guard → S3/SFTP/disk, SMTP, webhooks. Mark the trust boundaries with subgraphs, plus a one-line link to `docs/architecture.md`.
  6. **Quick start:** two tabs as `<details>` blocks.
     - "Try it locally (dev)": `just doctor`, `just dev`, `just admin-create`, `just run`, `just web-dev`, `just dev-runner`.
     - "Production (Docker Compose)": link to `docs/installation/compose.md` with the 4–5 key steps (certs init, keys generate, `admin create`, proxy profile).
     - Verify every command against the justfile and `docs/getting-started.md`.
  7. **Documentation:** a table of the main docs links.
  8. **Development and contributing:** `just` help screen, `just lint` / `just test` / `just test-int`, AGENTS.md, the decision log. Note that changes need decision entries and tests.
  9. **Security:** how to report a vulnerability, linking `SECURITY.md#1-reporting-a-vulnerability`.
  10. **License:** MIT © 2026 Jason Lentink.
- **Constraints:** every claim must match the implemented code and docs (verify feature names and commands). No screenshots. Relative links only. Check that every link target exists.
- **Verification:**
  - all relative links resolve (a small script: grep the links, test that each file or anchor exists);
  - the SVGs pass `xmllint --noout` (if installed) and contain no `<script>` or external `href`;
  - render-check in a browser with a GitHub-like markdown preview (e.g. headless Chrome on a local markdown render) in light and dark mode;
  - Mermaid syntax validated with `npx @mermaid-js/mermaid-cli` if available; otherwise keep the syntax simple and standard.

## Status (2026-10-29)

- **Part 0:** all nine findings are fixed and the new tests pass (the 8 app ConfigReload tests and the graph test). `just gen` ×2, build, vet, `just lint` and golangci-lint with the integration tag are clean.
  - Still to run: `just sec`, `just docs`, `go test -race ./...`, the full integration list (`DOCKER_API_VERSION=1.47 -p 1`), and web test, build and csp-check.
  - Delete `plans/robust-giggling-tide-agent-a1057e8ab2015b70d.md`.
- **Part D:** implemented and verified with a real two-platform push.
  - Still to do: `just lint`, the D-202 entry and the AGENTS.md task-runner line (by me).
  - Recommended: pin the SBOM generator `docker/buildkit-syft-scanner` by digest (`--sbom=generator=…@sha256:…` plus a Renovate rule, D-061).
- **Then:**
  1. Part E.
  2. One full check run.
  3. Both reviews over everything since the last review (Part 0 fixes, Part D, Part E).
  4. Handover with a commit message and the proposed decisions D-200, D-201 and D-202.

## Verification

- **Part 0:** the full check list above passes, and the integration suite is green with `DOCKER_API_VERSION=1.47`.
- **Part D:**
  - `scripts/publish_test.sh` passes;
  - `just publish --dry-run` with a test repo prints the expected tags and command;
  - a real push to a local registry: `docker run -d -p 127.0.0.1:5000:5000 registry:2`, then `BEKUP_IMAGE_REPO=localhost:5000/bekup just publish --yes`, then `docker buildx imagetools inspect localhost:5000/bekup:latest` shows both platforms and the attestations; afterwards remove that registry container;
  - `git status` shows `.just.env` ignored;
  - `just lint` stays green.
- **Reviews:** both reviews (security + code) cover Part D together with the Part 0 re-check before handover. No commits: the user commits.
