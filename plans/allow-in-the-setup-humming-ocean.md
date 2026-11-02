# Browser setup mode: `bekup setup web`

> **As built (2026-11-02; D-217 is authoritative):** two shipped services
> instead of one, both in profile `setup-web`. `setup-web` (run 1) keeps
> the `setup` service's hardening (root + `CAP_CHOWN`, writable `/deploy`,
> `logging: none`) and only its own `setup-web` network with the published
> port: questions, files, `setup check`, zip; it never reaches a database.
> `setup-web-db` (run 2, `setup web --database`) has migrate's non-root
> identity, config and secrets, `backend` + `setup-web`, a read-only
> `/deploy`, and joins the database container's network through the
> DB-network overlay: db bootstrap, migrate up, final check
> (`dbsetup.Verify`). Port variables: `BEKUP_SETUP_WEB_PORT` (published
> port) and `BEKUP_SETUP_WEB_HOST` (certificate SAN and printed URL); the
> plan's `BEKUP_SETUP_WEB_BIND` was not built. A pre-D-217 override without
> the `setup-web-db` block only warns. `setup check` has no
> running-container warning (no Docker access; the lint pins the ports).
> The "finish mode" of run 2 below is the `setup-web-db` service.

## Context

Installing bekup today takes three things:
- `docker compose run --rm setup` for the terminal wizard (or flags);
- `docker compose run --rm migrate db bootstrap …` for an external database;
- `docker compose up`.

You want one guided path in the browser:
1. Start the setup container in a web mode.
2. Answer everything in the browser.
3. Setup writes the files and validates the deployment (`setup check`).
4. Setup creates the database roles and runs the migrations.
5. You download a zip of the files at the end.

Your decisions:
- **Access:** the UI listens on all interfaces, protected by a one-time access code.
- **Files:** setup writes all files on the server. Secrets never reach the browser. The zip holds only the non-secret files.
- **Database:** setup connects to the database itself, for the bootstrap and the migrations.
- **Front ends:** the browser setup sits alongside the flags and the TUI wizard.

This loosens D-203 for the web service only. In web mode the setup container gets a network and a listening port. A new decision, **D-217 (proposed)**, records it. The existing `setup` service (no network, `logging: none`) stays unchanged. D-113 stays: the first admin is still created only with `bekup admin create`. The UI shows that command at the end.

## Approach: the existing wizard behind a browser front end

`wizard.RunInit(Env)` asks every question through the `wizard.UI` interface (`internal/setup/wizard/ui.go:24`: Note, Sensitive, Select, Input, Secret, Confirm). `wizard.Script` already shows that a UI without a terminal works. A new `WebUI` will implement `UI` with a question/answer channel. `RunInit` runs in a goroutine. Each call publishes one pending step, the browser renders it and posts the answer, and the call returns it. This keeps three things for free:
- every step, validation, default and Back navigation;
- the parity guarantee: `InitPlan.Flags` is built from the same answers, and `TestSetupWizardMatchesFlags` still holds;
- the equivalent CLI flags, shown at the end as a summary.

After `RunInit` returns, the server calls the existing `setup.Init(plan.Options)` against `/deploy` (the existing `os.Root` writer, ownership and rollback). Then it runs `setup.Check`, then the database step.

## Phases

### 1. Server: `internal/setup/web` (new package) + `bekup setup web` (cmd/bekup/setup_cmd.go)

**Listener and TLS**
- Listens on `0.0.0.0:8443` inside the container.
- TLS 1.3 with a self-signed ECDSA P-256 certificate, generated in memory at start and never written. The SANs come from `--host` names/IPs, plus `localhost`.

**Printed to the terminal at start**
- The access URL `https://<host>:8443/#code=<code>`. The code is in the fragment, so it is never sent in a request line or logged.
- The certificate's SHA-256 fingerprint, to compare with the browser's certificate view.
- The service keeps `logging: none`, so the code never reaches Docker's logs.

**Access code**
- 128 bits from `crypto/rand`, in base32.
- The browser posts it once to `/api/setup/session`. That sets a `__Host-` cookie (`Secure`, `HttpOnly`, `SameSite=Strict`) holding a separate 256-bit session value.
- The code is then burned. There is exactly one session, and a second login is refused.
- Code checks are constant-time. Failures get a growing delay, and after 10 failures the server exits.

**Request rules**
- Every state-changing request needs: `POST`, `Content-Type: application/json`, a custom header `X-Bekup-Setup: 1`, `Sec-Fetch-Site: same-origin`, an `Origin` equal to the request's own `https://` origin, and the session cookie.
- A body limit of 64 KiB applies.
- The security headers and CSP come from `internal/httpserver` (`ContentSecurityPolicy`, `middleware.go:377`). The middleware will be exported or reused, not copied.

**Lifetime**
- The server exits after completion, after 30 minutes idle, or after 2 hours in total.
- It never serves anything but the setup API and the setup SPA.

**API (JSON, IDs and answers only)**
- `GET /api/setup/step`: the current pending step (kind, title, body, options, default, validation hint), or the phase status.
- `POST /api/setup/answer`: an answer or Back. Validation errors come back as the wizard's own error text.
- Secrets (`UI.Secret`: the DB admin password, an imported keyring, CA passphrases) are handled as follows:
  - they travel only in the TLS POST body;
  - they are decoded straight into `[]byte` and handed to the waiting `UI.Secret` call;
  - they are zeroed after use and registered with `logging.DefaultRegistry` while held.
- `UI.Sensitive` (the CA key passphrase shown once) is served once on its step:
  - with `Cache-Control: no-store`;
  - the bytes are dropped server-side after the browser acknowledges it.
  - It is the browser counterpart of the TUI's alternate screen. Its D-203 "terminal only" rule is amended for web mode.
- `GET /api/setup/report`: the `setup.Init` report text, the `setup check` findings and the database step results.
- `GET /api/setup/config.zip`: only the non-secret files, and only after a successful Init:
  - which files: `compose.yaml`, `compose.override.yaml`, `.env`, `bekup.toml`, `runner.toml`, `bekup-setup.json`;
  - how it is built: from an allowlist built from the manifest (`internal/setup/manifest.go`, entries without key material), read through the setup `os.Root`;
  - a test asserts that no manifest entry with mode 0400 or under the keys directory can ever be included.

### 2. Front end: a separate small SPA (`web/src/setup/`, second Vite entry)
- It is embedded via `web/embed.go` (`SetupFS()`), separately from the main UI. It has no Apollo/GraphQL, and calls the setup API with plain `fetch`.
- It renders the generic step kinds. Built with the `frontend-design` skill on the existing design system (D-021), WCAG AA, dark mode.
- Screens:
  1. **Enter code:** prefilled from the URL fragment, then the fragment is removed from history.
  2. **Steps:** each wizard step, with Back.
  3. **Show once:** the passphrase screen, with a "copied it" confirm.
  4. **Results:** files written, the check findings, the database progress, "download config zip", and the next commands (`docker compose up -d`, `bekup admin create`).
- `pnpm csp-check` covers the setup pages under the production CSP.

### 3. Database step: setup connects itself
- **Shared code:**
  - Move the logic of `runBootstrap` (`cmd/bekup/db_cmd.go:151`) and `runMigrate up` (`cmd/bekup/migrate_cmd.go:51`, `openMigrator` :166) into a shared internal package, e.g. `internal/dbsetup`.
  - The CLI and web mode call the same functions, so the TLS policy, `--allow-insecure-admin-connection` semantics, `Migrator.Check` preflight, role checks and audit records stay identical.
  - The deployment guard runs first (`deployguard`, `checkDeployment`; the AGENTS rule "never add a second path that runs migrations or bootstrap without the guard").
- **External database:** the UI asks for the admin user and password (`UI.Secret` → `[]byte`, zeroed). It runs bootstrap, then `migrate up`, then `migrate check` and the role checks, and shows each result.
  - Weak TLS needs the same explicit confirmation as the CLI flag, as a Confirm step.
- **Bundled database:** postgres needs the generated files first, and setup has no Docker access, so it cannot start postgres. The UI tells you to run `docker compose up -d postgres`. It waits and retries with backoff until postgres answers on `backend`, then runs `migrate up` and `migrate check` as `bekup_owner`, using the written owner credentials.
- **Networks:** a container's networks are fixed when it starts, but the database network is an answer given in the UI. The flow therefore has two runs of the same service:
  - **Run 1** (setup-first or compose-first): answers → Init → check. If the database is reachable on the networks the container already has, the database step runs straight away.
  - **Run 2:** otherwise the results screen says: "restart with `docker compose run --rm --service-ports setup-web`". The written compose override now puts `setup-web` on the database (or proxy) network. When `bekup-setup.json` exists, web mode opens in "finish" mode: database step + `setup check`, no questions.

### 4. Compose: a new shipped service `setup-web` (profile `setup-web`)
- Same base as `setup`: the image via `*bekup-image`, `user: "0:0"`, `cap_drop: [ALL]` + `cap_add: [CHOWN]`, `read_only`, tmpfs `/tmp`, `no-new-privileges`, `logging: none`, `stdin_open`/`tty`, the `/deploy` bind.
  - Root + `CAP_CHOWN` is needed for ownership, as with `setup`. This is the residual risk of a network-facing root process, recorded in D-217.
- **Differences from `setup`:**
  - command `web`;
  - its own non-internal network `setup-web`, for the published port `${BEKUP_SETUP_WEB_BIND:-0.0.0.0}:${BEKUP_SETUP_WEB_PORT:-8443}:8443`;
  - it joins `backend`;
  - the database container network or proxy network is added through the existing setup-only overlays (`internal/setup/overlay.go`).
  - With an external remote database it reaches the database through `setup-web`, which is non-internal.
- **Lint, checks and variables:**
  - The lint pins `setup-web` attribute by attribute, like `setup` (`shippedDiff`, `TestVariantSetupService`).
  - `BEKUP_SETUP_WEB_BIND` and `BEKUP_SETUP_WEB_PORT` go into `composeVarAllowlist` and the compose.md variable table.
  - `setup check` warns if a `setup-web` container is still running or the port mapping was changed to something unexpected. The check is informational only, because there is no Docker access.
- Both database modes and all four release/committed files: change `docker-compose.yml` plus the external-db variant fragments, then `just gen`.

### 5. Decisions and docs
- **D-217 (proposed)** "Browser setup mode". It amends D-203 (network, listener and the show-once passphrase in web mode only) and D-205/D-208 (a new shipped service and overlay membership). It references D-113, which stays unchanged. Its security impact:
  - a listener on all interfaces during setup;
  - mitigations: a 128-bit single-use code, one session, a fingerprint-verified self-signed TLS certificate, exit on failures, idle and completion, and no secrets in the zip;
  - residual risks: a network-facing root process with `CAP_CHOWN`; an attacker who sees the terminal output; and someone who reaches the port before you during the first minutes. They can only guess the code, and the server exits after 10 failures.
- Also update: the D-203/D-205/D-208 status lines, `AGENTS.md` (architecture, trust-boundary table row "Operator browser → setup-web", conventions item), `SECURITY.md` (a new boundary and threat rows), `docs/installation/compose.md` (a "Browser setup" section), `docs/installation/external-database.md`, and the CLI reference (`just docs`).

## Tests
- **`WebUI`:** a full wizard flow driven over the HTTP API, compared with `wizard.Script` / the flag Options, with Back, validation errors and secret zeroing (extend `TestSetupWizardMatchesFlags` with a web-driven variant).
- **Access:**
  - a wrong, reused or second-session code;
  - exit after 10 failures;
  - a missing or foreign Origin, `Sec-Fetch-Site` or custom header;
  - body limit, idle and completion exit;
  - the code never in logs or response bodies (redaction test).
- **Show once:** the passphrase is served exactly once, `no-store`, and zeroed after the acknowledgement.
- **Zip:** the allowlist test over the manifest (no 0400, no keys dir, no secret files), the contents equal the written files, and it is refused before Init.
- **`internal/dbsetup`:** the existing bootstrap/migrate tests move along; integration tests (testcontainers, `DOCKER_API_VERSION=1.47`) cover bootstrap + migrate from web mode against the external-DB test server (`db/externaltest`) and the bundled image.
- **Compose:** `setup-web` in `TestOverlayDocs`, the lint bypass rows (`TestLintBypasses`), the compose-var allowlist, `TestCommittedComposeFiles`, and the `docker compose config` integration with `--profile setup-web`.
- **Front end:** vitest flow tests of the setup SPA, and `pnpm csp-check`.

## Verification
- `just gen`, `just lint`, `just sec`, `just test`, `just test-int`.
- **Manual, external DB:**
  1. `docker compose run --rm --service-ports setup-web` on a test host, then open the printed URL from another machine.
  2. Check the fingerprint, then go through the steps.
  3. See the check go green, then bootstrap + migrate.
  4. Download the zip and verify it contains no key material.
  5. Run `docker compose up -d` and log in after `bekup admin create`.
- **Manual, bundled DB:** the same, with run 2 after `docker compose up -d postgres`.
- **End of milestone:** a security review agent and a code review agent, briefed with the code. You commit.
