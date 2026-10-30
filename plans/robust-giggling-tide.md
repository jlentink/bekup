# Plan: finish the fix round, remove mobile-app references, compose-first onboarding

## Context

Since commit `7564cdd` these are built but uncommitted: the CI fixes, `bekup setup` (D-203) and the GHCR release workflow (D-204). Both reviews ran and a fix round was half done when plan mode paused it. The user then asked for:

1. **Remove every iOS and Android app reference.** Also remove the reserved **push** channel, which existed only for the mobile apps (user decision). Phase 2 keeps only the external API.
2. **Simple onboarding for Docker Compose users.** The README leads with three steps:
   1. download a copyable `compose.yaml`;
   2. `docker compose run --rm setup`;
   3. `docker compose up -d`.

   There are three variants: bekup with its own Traefik (the default), behind an **existing Traefik**, and behind **Nginx Proxy Manager**. User decisions:
   - full hardening in the copyable file;
   - a dedicated `bekup-proxy` link network for external proxies;
   - the files come only as release downloads (pinned by digest, attested);
   - the deployment file name is `compose.yaml`.

## Part A: finish the paused review-fix round (first)

Resume the fix agent with its notes (`plans/robust-giggling-tide-agent-a26fab53cbccb5a41.md`).

- **Already done:** ownership EPERM, offline CA protection, FAT/exFAT, `override.go`, the deployment root check, `os.Root`, tagless refs, L7 backups, CIDR limits.
- **To do:**
  - **`release.yml` split:** build/push by digest (write) → Trivy scan (read only) → sign/attest/tag (write). `harden-runner` and `checkout` stay in the write jobs (decided).
    - recompute `latest` via `gh api …/git/matching-refs/tags/v` in a global `release-latest` concurrency group;
    - the immutability check fails closed except on a 404;
    - the tag object's name must equal `GITHUB_REF_NAME`;
    - fetch the tag with the token;
    - disable CI caches when called by the release;
    - state the cosign minimum version;
    - verify the trivy-action pin.
  - **Remaining Lows:** L1 `--log-driver none`; passphrase warning on stderr; remaining tests (regenerate rollback, CA refusal, override, L7, L8); `pinHelp` in upgrade; `readPassphraseLine` stops at the newline; SECURITY.md B10 and the AGENTS.md boundary row; the `--fix-ownership` step in the `setup-install` CI job.
  - **Docs:** the D-203/D-204 updates and the 5.7 note (three load-sensitive tests examined, carried to 7.1).
- **Skip** the README, `compose.md` and `upgrades.md` rewrites and the shadow-file tests on the old names: Part C replaces them.
- **Verify:** the Docker reproductions of both Highs with a rebuilt image, actionlint/zizmor, `just lint`/`sec`/`test-scripts`, race tests, setup integration tests.

## Part B: remove mobile-app references and the push channel

- **Plan** (`plans/backup-databases-abstract-corbato.md`):
  - delete the "Phase 3 — Native iOS · Phase 4 — Native Android" section;
  - in Phase 2, drop push/FCM/device registration and the "API style for mobile" wording, keeping only the external API;
  - remove push from the P1 notifications row (l.45) and from the intro line (l.4);
  - remove "push" from user-facing wording anywhere else in the plan.
- **`SECURITY.md:587`:** "Phase 2 (external API)" only; drop "Phases 3–4 (mobile apps)".
- **Push channel code and data:**
  - GraphQL `internal/graph/schema/notifications.graphqls`: remove the `PUSH` value from the channel enum and the "push follows in phase 2" docstrings; `just gen` regenerates `generated.go`, `models_gen.go`, `docs/reference/schema.graphql` and `web/src/graphql/gen`.
  - `internal/notify/prefs.go`, `api_prefs.go`, the matrix channel list (`notify.MatrixChannels`) and every switch/case on push.
  - **DB:** a new migration `00033_drop_push_channel.sql` (migrations 00028+ are committed, so 00028 stays unedited):
    - delete any `push` preference rows (there are none in practice);
    - narrow the CHECK constraints that list `push`;
    - write the Down section;
    - update `internal/db/schema.sql`.
  - **Web:**
    - `features/notifications/model.ts` (matrix columns without push), `messages.ts` (`pushNote` removed), the preference matrix and its tests, csp-check fixtures;
    - leave the session user-agent detection alone (`account/model.ts`, `sessions-section.tsx`, `user-sessions.tsx`): it labels phone browsers, not apps.
  - **Docs:** `docs/guides/notifications.md` (the push row), `AGENTS.md:791` (the "A new matrix channel (push, phase 2)" convention becomes "A new matrix channel goes into `notify.MatrixChannels` …"), and the D-161/D-191-era texts in `docs/DECISIONS.md`.

    For accepted decisions, don't edit text away: add a short `proposed` **D-206** "Mobile apps and the push channel dropped from the roadmap", which supersedes the push parts of the affected decisions (D-161 channel list, D-191 matrix), and set their status notes accordingly. If an accepted entry is fully about push, mark it `superseded by D-206`.
  - **Leave alone:** the word lists (`common_passwords.txt`, `eff_large_wordlist.txt`) and the HMAC `ipad` test variable.
- **Verify:**
  - `git grep --untracked -i -w -E 'ios|android|iphone|ipad|fcm|apns|mobile|testflight|swiftui|jetpack'` shows only the allowed hits (word lists, `ipad` HMAC, user-agent labels);
  - `git grep -i -w push` shows no notification-channel uses;
  - `just gen` ×2, lint, tests, web tests, migration up/down in the db integration tests.

## Part C: compose-first onboarding (D-205 proposed)

Based on the design agent's proposal. The facts that drive it:
- Docker gives fixed IPs only on networks created with a subnet.
- `compose run` starts a service from an inactive profile.
- Nothing is released yet, so names and the state schema can change freely.

### C1. Single source and variants

- **Image line:** `docker-compose.yml` gets one top-level line `x-bekup-image: &bekup-image ${BEKUP_IMAGE:-bekup:dev}`, and `x-bekup-hardening` uses `image: *bekup-image`. Release and setup rendering replace exactly that line.
- **Setup service in the base file:**
  - `profiles: [setup]`, `user: "0:0"`, `entrypoint: [/usr/local/bin/bekup, setup]`, `command: [init]`;
  - `environment`: `BEKUP_IMAGE: *bekup-image` and `DOCKER_GID: ${DOCKER_GID:-}`;
  - hardening: `read_only`, tmpfs `/tmp` (noexec), `cap_drop: [ALL]`, `cap_add: [CHOWN]`, `no-new-privileges`, `network_mode: none`, `logging: {driver: none}`, `stdin_open`/`tty`, `restart: "no"`, limits;
  - volume `source: .`, `target: /deploy`, `create_host_path: false`.
- Every `./deploy/...` bind uses long syntax with `create_host_path: false`.
- **Variants are overlays, not copies.** They live in `internal/setup/variants.go` plus `internal/setup/variants/{traefik,traefik-external,npm}/*.yaml`, applied by a small path-addressed line editor (`internal/setup/outline.go`): delete or replace a service or key, insert after a key, replace a comment or the header.
  - Untouched base text stays byte-identical.
  - Overlays never contain `image:` (a test enforces it).
- **New commands:** `bekup setup compose --variant V [--image pinned]` prints a variant, and `just compose-examples` writes `dist/compose/` (gitignored). Nothing is committed under `examples/` (decided).
- **File names:** the deployment file is `compose.yaml` everywhere (setup-first too) and the override is `compose.override.yaml`. The shadow list becomes `compose.yml`, `docker-compose.y{a,}ml` and the other override names. The repo source stays `docker-compose.yml`.

### C2. External proxies (variants b and c): dedicated link network (decided)

- **Network:** `docker network create --internal --subnet 10.213.253.0/28 bekup-proxy` (once). The user's Traefik or NPM joins it with `ipv4_address: 10.213.253.5` (four lines plus a recreate).
- **bekup side:** bekup declares it external (`name: ${BEKUP_PROXY_NETWORK:-bekup-proxy}`). serve gets `ipv4_address: ${SERVE_PROXY_IP:-10.213.253.10}`, `listen = "10.213.253.10:8080"` and `trusted_proxies = ["10.213.253.5/32"]`.
  - No wildcard listen (D-098 untouched).
  - No variant `frontend`/`edge` network and no published ports.
- **Validation:**
  - a private subnet between /8 and /29, with no overlap with `runner-ctl`/`metrics`;
  - both addresses inside the subnet and distinct, never the network, gateway or broadcast address;
  - `trusted_proxies` exactly the proxy's /32 (a `check` ERROR otherwise).
- **Existing Traefik** (labels on serve only):
  - `traefik.enable=true`;
  - `traefik.docker.network=${BEKUP_PROXY_NETWORK:-bekup-proxy}`;
  - the router rule ``Host(`${BEKUP_DOMAIN:-bekup.invalid}`)``;
  - entrypoint `${TRAEFIK_ENTRYPOINT:-websecure}`;
  - `tls=true` and certresolver `${TRAEFIK_CERTRESOLVER:-letsencrypt}`;
  - service port 8080;
  - `responseforwarding.flushinterval=-1ms`;
  - no middlewares (bekup sets its own headers, D-102).

  The docs cover:
  - `exposedByDefault=false`;
  - never `forwardedHeaders.insecure` or a 0.0.0.0/0 `trustedIPs`;
  - no entrypoint-wide header middlewares and no `readTimeout: 0`;
  - the Docker-socket risk, recommending a socket proxy or the file provider (with a file-provider snippet).
- **NPM:** the Proxy Host forwards `http` to `10.213.253.10:8080`.
  - Websockets **off** (SSE, D-124).
  - Let's Encrypt, Force SSL and HTTP/2 on; NPM's HSTS off (bekup sends HSTS).
  - Advanced block: `proxy_http_version 1.1; proxy_buffering off; proxy_cache off; proxy_read_timeout 3600s; client_max_body_size 2m;`.
- **Optional:** a rate-limited WARN in `internal/httpserver` for X-Forwarded-For from an untrusted peer.

### C3. Setup in compose-first mode

- **`setup init`** (default when `/deploy/compose.yaml` exists and there is no state file):
  - read the file safely;
  - take the image from the `x-bekup-image` line; `BEKUP_IMAGE` must match; `CheckVersionTag`;
  - recognise the variant by rendering each embedded variant with that image: byte-equal / same YAML model / customised (customised gives a warning, requires `--variant` and runs the hardening lint);
  - **never write `compose.yaml`**;
  - write `.env`, the TOMLs, secrets, certificates, `deploy/**`, `reference/*` and the state file.
- **No `compose.yaml`:** refuse with the release URL, or use `--write-compose --variant traefik|traefik-external|npm|full`. `full` is today's setup-first path, keeping nginx and own-proxy (`--proxy`).
- **Prompts:**
  - bundled: domain, ACME e-mail, CA passphrase;
  - external variants: domain, CA passphrase;
  - Docker GID from the `DOCKER_GID` environment variable, otherwise a prompt.

  Everything else comes from flags: `--traefik-entrypoint`, `--traefik-certresolver`, `--proxy-network`, `--proxy-subnet`, `--proxy-ip`, `--serve-ip` and the existing ones. New validators cover network names and entrypoint/certresolver names.
- **State:** adds `compose_mode` (`operator`|`generated`), `variant`, the link-network answers and the compose hash; schema 1 changes in place. In the manifest, `compose.yaml` is `operator` in compose-first and `generated-upgradable` in setup-first.
- **`setup upgrade`, compose-first:**
  - the user replaces `compose.yaml` with the new release file, then runs `docker compose run --rm setup upgrade`;
  - it refuses a downgrade or a variant switch, re-validates the TOMLs, rewrites `deploy/**` and `reference/*`, generates new secret groups and updates the state;
  - it never writes `compose.yaml`; `--write-compose` exists only for the `docker run` path.
- **`setup check`, compose-first:**
  - every image and the `x-bekup-image` line pinned;
  - no `build`, `include` or `extends.file`;
  - the hardening lint for customised files and the override: `privileged`, host network/pid/ipc, `docker.sock` outside runner, `ports:` on internal services, and the setup service hardening exact;
  - recognition; a newer image than recorded gives a hint to upgrade;
  - variant-aware TOML/`.env` consistency.

### C4. Release assets (extends D-204; after the Part A split)

- **Assets:** `compose.yaml` (own Traefik), `compose.traefik-external.yaml`, `compose.npm.yaml` and `SHA256SUMS`, rendered by running the published digest (`setup compose`, network none, read-only, uid 65532).
- **Checks:** `release_ci.sh compose-assets`: exactly one pinned `x-bekup-image` equal to the release ref, and every image pinned. Tests are included.
- **Provenance:** `actions/attest-build-provenance` (`subject-path`) plus `cosign sign-blob` bundles; the assets are uploaded with `gh release create`.
- **Release notes:** the three steps.

### C5. README quick start (exact text from the design)

The README starts with: Linux host, Docker Engine 26+, Compose 2.24+, a DNS name.

**Own Traefik** (open by default):
1. Save the compose file and verify it:
   ```sh
   sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
   sudo curl -fsSLo compose.yaml https://github.com/jlentink/bekup/releases/latest/download/compose.yaml
   gh attestation verify compose.yaml --repo jlentink/bekup
   ```
2. `DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup`
3. `docker compose up -d`, then `docker compose run --rm admin create --email you@example.com --display-name "Your Name"`

**Behind your existing Traefik:**
- Once: `docker network create …`, and four lines added to Traefik's compose file.
- Same three steps, with `compose.traefik-external.yaml` saved as `compose.yaml`.

**Behind Nginx Proxy Manager:**
- Once: the same network setup.
- Same three steps with `compose.npm.yaml`.
- Then the NPM proxy-host field table and the Advanced block.

Footer: nginx, own proxy or full setup → setup-first (`docs/installation/compose.md#alternative-setup-first`).

### C6. Docs and governance

- **D-205 (proposed):**
  - compose-first flow;
  - `compose.yaml` naming;
  - the `bekup-proxy` link network;
  - variants as overlays;
  - release assets with attestations.

  It supersedes the parts of D-203 that make setup always write compose, and extends D-204. Update the D-203/D-204 status lines and the index.
- **`docs/installation/compose.md`:** compose-first as the main path, with "Alternative: setup-first (`--write-compose`)", network tables per variant and the manifest table.
- **`reverse-proxy.md`:** sections "Existing Traefik" and "Nginx Proxy Manager" with exact values and verification (`curl -sI`: one HSTS header, bekup's CSP).
- **`upgrades.md`:** download the new `compose.yaml`, diff, `docker compose run --rm setup upgrade`, `pull`, `up -d`, `setup check`.
- **Other docs:** `getting-started.md`, the CLI reference, CHANGELOG.
- **SECURITY.md:**
  - B2b: the proxy ↔ serve link network;
  - RP-8: an external Traefik holds the Docker socket;
  - B10 extended with the compose-run setup container.
- **AGENTS.md conventions:**
  - the image only via `x-bekup-image`;
  - variants only as overlays (never full copies, no `image:`);
  - deploy binds with `create_host_path: false`;
  - the deployment file is `compose.yaml`;
  - the external-proxy boundary row;
  - `just compose-examples`.
- **Plan:** task **6.14b** "Compose-first onboarding (D-205)" with acceptance criteria.

### C7. Tests

- **Line editor:** table tests and a fuzz test.
- **Base style lint.**
- **Variants:**
  - exact service set, pins, no `build`;
  - untouched blocks byte-equal to the base;
  - external variants: no frontend network, only serve on the proxy network, no published ports;
  - labels only in `traefik-external`;
  - the setup service hardening exact.
- **Recognition levels.**
- **Compose-first init per variant:** `compose.yaml` untouched, values correct, refusal without a file, `--write-compose`.
- **Upgrade:** the replaced-file path, a no-op, refusal of a variant switch.
- **Check:** the /32 rule and the lint errors.
- **Scripts:** `release_ci_test.sh compose-assets`.
- **Integration:** `TestIntegrationComposeConfig` for the three variants.
- **CI `setup-install` end to end, bundled variant:**
  1. Render with the local-registry digest.
  2. Self-signed `bekup.test` certificate (static TLS).
  3. `docker compose run --rm -T setup init --non-interactive … --ca-passphrase-stdin`.
  4. `up -d --wait`.
  5. `curl --resolve … /healthz`.
  6. `admin create` with a password file.
  7. `setup check`, then `down -v`.
- **CI `setup-install` end to end, NPM variant:** create the network, init, `up --wait`, then curl from a pinned curl container at 10.213.253.5.
- **Spike first (C0):** check the Compose behaviour by hand (missing files or network at `run` time, tty/`-T` with `logging: none`, fixed IP on an external subnet network) and confirm Traefik v3 labels. Adjust the README order if Compose validates the external network at `run` time.

## Order and agents

1. **Part A** (the resumed fix agent) and **Part B** (a new agent) in parallel.
   - Part B owns: notify, graph notification schema, migration 00033, web notifications, `docs/guides/notifications.md`, the plan Phase 2–4 sections, `SECURITY.md:587`, the push line in AGENTS.md and D-206.
   - Part A owns: setup, release, ci.yml, and the D-203/D-204 edits.
   - Shared files (DECISIONS index, SECURITY.md, AGENTS.md) get edited at different lines, each agent re-reading before editing.
2. **Part C** after Part A: same packages and the release workflow.
3. Then the full check run and both reviews (security and code) over A+B+C, the fixes, and the handover. No commits; the user commits and pushes.

## Verification

- Every part's own checks (above).
- **Final full run:**
  - `just gen` ×2;
  - build and vet (+ integration);
  - `just lint` (Linux pass + shellcheck);
  - `just sec`, `just docs`, `go mod tidy -diff`, `just test-scripts`;
  - `go test -race ./...`;
  - `DOCKER_API_VERSION=1.47 go test -race -tags integration -p 1 ./...`;
  - compose config for every profile;
  - the web suite and csp-check;
  - actionlint and zizmor.
- **By hand:** the three README flows against a local registry (the bundled variant with static TLS, and the NPM variant with a curl container acting as the proxy).
- **After the user pushes:** CI green; the first real release is the user's `just bump` + `just release`.
