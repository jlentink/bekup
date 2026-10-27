# Decision log

This is the decision log for bekup. Every decision, whether made by the user or
during implementation, is recorded here **before** or together with the code
that depends on it.

Rules (from the plan's Working rules):

- Entries are **append-only** and numbered `D-001`, `D-002`, ...
- Each entry has **Date**, **Status** (`proposed` / `accepted` /
  `superseded by D-xxx`), **Decided by** (`user` / `agent with user approval`
  / `agent` for proposals), **Context**, **Decision**, **Alternatives
  considered**, **Consequences** (including security impact) and **Related**
  (tasks, files).
- Decisions are never edited away. A change is a new entry that supersedes the
  old one; only the old entry's **Status** line (and the index) is updated.
- Agents may only add `proposed` entries. `accepted` requires the user's
  confirmation.
- Keep the summary in [`AGENTS.md`](../AGENTS.md) in sync, and update
  `SECURITY.md` whenever a trust boundary changes.

Task numbers (for example `0.3`, `2.7a`) refer to the checklist in
[`plans/backup-databases-abstract-corbato.md`](../plans/backup-databases-abstract-corbato.md).

## Index

| ID | Title | Status |
|---|---|---|
| [D-001](#d-001-security-first-no-compromises) | Security first, no compromises | accepted |
| [D-002](#d-002-name-module-and-license) | Name, module and license | accepted |
| [D-003](#d-003-dump-execution-via-a-dedicated-runner-sidecar) | Dump execution via a dedicated runner sidecar | accepted |
| [D-004](#d-004-dump-all-and-database-selection) | "Dump all" and database selection | accepted |
| [D-005](#d-005-retention-policy) | Retention policy | accepted |
| [D-006](#d-006-reusable-destinations-with-fan-out) | Reusable destinations with fan-out | accepted |
| [D-007](#d-007-immutable-backups) | Immutable backups | accepted |
| [D-008](#d-008-backup-file-encryption-on-by-default) | Backup file encryption on by default | accepted |
| [D-009](#d-009-schedules) | Schedules | accepted |
| [D-010](#d-010-authentication-and-second-factors) | Authentication and second factors | accepted |
| [D-011](#d-011-roles) | Roles | accepted |
| [D-012](#d-012-downloads) | Downloads | accepted |
| [D-013](#d-013-restore-targets) | Restore targets | accepted |
| [D-014](#d-014-fresh-second-factor-for-every-restore) | Fresh second factor for every restore | accepted |
| [D-015](#d-015-temporary-restore-connections) | Temporary restore connections | accepted |
| [D-016](#d-016-key-generation) | Key generation | accepted |
| [D-017](#d-017-admin-ui-tls-terminated-by-a-reverse-proxy) | Admin UI TLS terminated by a reverse proxy | accepted (partly superseded by D-093/D-094/D-095) |
| [D-018](#d-018-bekups-own-tls-links-and-pinning) | bekup's own TLS links and pinning | accepted |
| [D-019](#d-019-toml-configuration-and-search-path) | TOML configuration and search path | accepted |
| [D-020](#d-020-justfile-as-the-single-task-entry-point) | justfile as the single task entry point | accepted |
| [D-021](#d-021-web-design-with-the-frontend-design-skill) | Web design with the frontend-design skill | accepted |
| [D-022](#d-022-restore-safety-stage-verify-restore) | Restore safety: stage, verify, restore | accepted |
| [D-023](#d-023-notifications-phase-1) | Notifications (Phase 1) | accepted |
| [D-024](#d-024-monitoring-phase-1) | Monitoring (Phase 1) | accepted |
| [D-025](#d-025-packaging-single-go-binary) | Packaging: single Go binary | accepted |
| [D-026](#d-026-web-stack) | Web stack | accepted |
| [D-027](#d-027-cicd-on-github-actions) | CI/CD on GitHub Actions | accepted |
| [D-028](#d-028-go-library-set) | Go library set | accepted |
| [D-029](#d-029-runner-network-isolation-and-target-allowlist) | Runner network isolation and target allowlist | accepted |
| [D-030](#d-030-runner-mtls-grpc-api) | Runner mTLS gRPC API | accepted |
| [D-031](#d-031-image-allowlist-and-fixed-container-spec) | Image allowlist and fixed container spec | accepted |
| [D-032](#d-032-secret-delivery-via-stdin-preamble) | Secret delivery via stdin preamble | accepted |
| [D-033](#d-033-runner-request-validation) | Runner request validation | accepted |
| [D-034](#d-034-target-db-certificate-pinning-mechanics) | Target-DB certificate pinning mechanics | accepted |
| [D-035](#d-035-ssh-tunnels-with-mandatory-host-key-pins) | SSH tunnels with mandatory host-key pins | accepted |
| [D-036](#d-036-engine-specific-dump-and-restore-rules) | Engine-specific dump and restore rules | accepted |
| [D-037](#d-037-backup-pipeline-and-artifact-layout) | Backup pipeline and artifact layout | accepted |
| [D-038](#d-038-signed-manifests) | Signed manifests | accepted |
| [D-039](#d-039-restore-staging-and-identity-handoff) | Restore staging and identity handoff | accepted |
| [D-040](#d-040-offline-recovery-tooling) | Offline recovery tooling | accepted |
| [D-041](#d-041-configuration-loading-details) | Configuration loading details | accepted |
| [D-042](#d-042-sqlc-and-goose-with-vendored-river-schema) | sqlc and goose with vendored River schema | accepted |
| [D-043](#d-043-river-configuration) | River configuration | accepted |
| [D-044](#d-044-scheduler-semantics) | Scheduler semantics | accepted |
| [D-045](#d-045-concurrency-limits) | Concurrency limits | accepted |
| [D-046](#d-046-credential-envelope-encryption) | Credential envelope encryption | accepted |
| [D-047](#d-047-authentication-details-and-step-up) | Authentication details and step-up | accepted |
| [D-048](#d-048-reverse-proxy-hardening-and-trusted-proxies) | Reverse-proxy hardening and trusted proxies | accepted (partly superseded by D-093/D-094) |
| [D-049](#d-049-outbound-tls-without-pinning) | Outbound TLS without pinning | accepted |
| [D-050](#d-050-http-and-graphql-hardening) | HTTP and GraphQL hardening | accepted |
| [D-051](#d-051-notifications-engine) | Notifications engine | accepted |
| [D-052](#d-052-websocket-subscriptions) | WebSocket subscriptions | accepted |
| [D-053](#d-053-central-deny-by-default-authorization) | Central deny-by-default authorization | accepted |
| [D-054](#d-054-least-privilege-db-roles-and-audit-hash-chain) | Least-privilege DB roles and audit hash chain | accepted |
| [D-055](#d-055-ssrf-guard-and-disk-confinement) | SSRF guard and disk confinement | accepted |
| [D-056](#d-056-download-tickets) | Download tickets | accepted |
| [D-057](#d-057-restore-gate) | Restore gate | accepted |
| [D-058](#d-058-temporary-connection-implementation) | Temporary connection implementation | accepted |
| [D-059](#d-059-justfile-details-and-version-bumping) | justfile details and version bumping | accepted |
| [D-060](#d-060-logging-redaction-and-limits) | Logging redaction and limits | accepted |
| [D-061](#d-061-supply-chain-measures) | Supply-chain measures | accepted |
| [D-062](#d-062-data-model-conventions) | Data model conventions | accepted |
| [D-063](#d-063-documentation-in-docs) | Documentation in docs/ | accepted |
| [D-064](#d-064-golangci-lint-configuration) | golangci-lint configuration | accepted |
| [D-065](#d-065-renovate-update-policy) | Renovate update policy | accepted |
| [D-066](#d-066-gitignore-blocks-key-and-secret-material) | .gitignore blocks key and secret material | accepted |
| [D-067](#d-067-owner-assignment-cannot-grant-maintainers-downloads) | Owner assignment cannot grant maintainers downloads | accepted |
| [D-068](#d-068-restore-privilege-warnings-for-superuser-and-cross-source-restores) | Restore privilege warnings for superuser and cross-source restores | accepted |
| [D-069](#d-069-audit-and-alert-on-break-glass-cli-actions) | Audit and alert on break-glass CLI actions | accepted |
| [D-070](#d-070-per-job-maximum-artifact-size) | Per-job maximum artifact size | accepted |
| [D-071](#d-071-justfile-conventions) | justfile conventions | accepted |
| [D-072](#d-072-version-bump-and-release-policy) | Version bump and release policy | accepted |
| [D-073](#d-073-configuration-keys-defaults-and-guardrails-task-03) | Configuration keys, defaults and guardrails (task 0.3) | accepted |
| [D-074](#d-074-config-print-is-always-redacted) | config print is always redacted | accepted |
| [D-075](#d-075-internallogging-redaction-rules) | internal/logging redaction rules | accepted |
| [D-076](#d-076-container-image-build) | Container image build | accepted |
| [D-077](#d-077-compose-network-segmentation-and-interface-binding) | Compose network segmentation and interface binding | accepted |
| [D-078](#d-078-separate-migrate-service-serve-never-holds-owner-credentials) | Separate migrate service; serve never holds owner credentials | accepted |
| [D-079](#d-079-mtls-certificate-distribution-in-compose) | mTLS certificate distribution in compose | accepted |
| [D-080](#d-080-bekup-healthcheck-subcommand-for-distroless-healthchecks) | `bekup healthcheck` subcommand for distroless healthchecks | accepted |
| [D-081](#d-081-development-compose-stack) | Development compose stack | accepted (default dev workflow superseded by D-114) |
| [D-082](#d-082-csp-for-the-web-ui-style-src-self-without-nonce) | CSP for the web UI: style-src 'self' without nonce | accepted |
| [D-083](#d-083-visual-direction-and-design-system-vault-ledger) | Visual direction and design system (Vault Ledger) | accepted |
| [D-084](#d-084-web-toolchain-pins-and-pnpm-supply-chain-settings) | Web toolchain pins and pnpm supply-chain settings | accepted |
| [D-085](#d-085-master-keyring-file-format-and-key-ids) | Master keyring file format and key IDs | accepted |
| [D-086](#d-086-credential-ciphertext-format-subkeys-and-key-check-value) | Credential ciphertext format, subkeys and key-check value | accepted |
| [D-087](#d-087-instance-signing-key-ids-and-domain-separated-signatures) | Instance signing key IDs and domain-separated signatures | accepted |
| [D-088](#d-088-passphrase-generator-and-age-work-factor-bounds) | Passphrase generator and age work-factor bounds | accepted |
| [D-089](#d-089-runner-mtls-identity-scheme-and-pki-profile) | Runner mTLS identity scheme and PKI profile | accepted |
| [D-090](#d-090-ca-key-protection-and-ca-rotation-procedure) | CA key protection and CA rotation procedure | accepted |
| [D-091](#d-091-ci-workflow-hardening-and-job-layout) | CI workflow hardening and job layout | accepted |
| [D-092](#d-092-app-db-framework-details-roles-checks-audit-chain-format-owner-url-task-04) | App DB framework details: roles checks, audit chain format, owner URL (task 0.4) | accepted |
| [D-093](#d-093-reverse-proxy-profiles-socket-free-traefik-fixed-proxy-address-local-dev-tls) | Reverse-proxy profiles: socket-free Traefik, fixed proxy address, local dev TLS | accepted |
| [D-094](#d-094-nginx-profile-unprivileged-nginx--certbot-instead-of-nginx-proxy) | nginx profile: unprivileged nginx + certbot instead of nginx-proxy | accepted |
| [D-095](#d-095-plain-http-allowed-with-explicit-opt-in-and-persistent-warning) | Plain HTTP allowed with explicit opt-in and persistent warning | accepted |
| [D-096](#d-096-compose-hardening-from-the-m0-review-superuser-socket-only-dev-source-allowlist-pinned-bastion-setup) | Compose hardening from the M0 review: superuser socket-only, dev source allowlist, pinned bastion setup | accepted |
| [D-097](#d-097-canonical-bekup_section_key-env-names-unknown-variable-warnings-dev-mode-env-switch-off) | Canonical `BEKUP_<SECTION>_<KEY>` env names, unknown-variable warnings, dev-mode env switch-off | accepted |
| [D-098](#d-098-wildcard-listeners-refused-target-allowlist-overlap-checks-extended-log-redaction-m0-review) | Wildcard listeners refused, target allowlist overlap checks, extended log redaction (M0 review) | accepted |
| [D-099](#d-099-db-role-checks-on-every-migrate-run-and-at-serve-startup-faithful-00001-down) | DB role checks on every migrate run and at serve startup; faithful 00001 down | accepted |
| [D-100](#d-100-tls-for-the-app-database-connection-configurable-verified-by-default) | TLS for the app database connection: configurable, verified by default | accepted |
| [D-101](#d-101-audit-api-fail-closed-policy-signed-chain-heads-and-instance_keys-task-13) | Audit API fail-closed policy, signed chain heads and `instance_keys` (task 1.3) | accepted |
| [D-102](#d-102-http-layer-details-middleware-trusted-proxies-origin-policy-rate-limits-health-and-schema-checks-task-11) | HTTP layer details: middleware, trusted proxies, Origin policy, rate limits, health and schema checks (task 1.1) | accepted |
| [D-103](#d-103-app-db-tls-details-url-handling-require--ca-channel-binding-postgres-certificate-and-wrapper-task-10) | App DB TLS details: URL handling, require + CA, channel binding, postgres certificate and wrapper (task 1.0) | proposed (item 3 superseded by D-104) |
| [D-104](#d-104-app-db-tls-ecdsa-postgres-issuer-and-required-scram-channel-binding) | App DB TLS: ECDSA postgres issuer and required SCRAM channel binding | accepted |
| [D-105](#d-105-graphql-server-details-transport-limits-access-convention-error-codes-task-12) | GraphQL server details: transport, limits, access convention, error codes (task 1.2) | accepted |
| [D-106](#d-106-authorization-model-operation-catalog-policy-matrix-and-owner-scope-task-14) | Authorization model: operation catalog, policy matrix and owner scope (task 1.4) | accepted |
| [D-107](#d-107-passwords-sessions-lockout-and-the-login-api-task-15) | Passwords, sessions, lockout and the login API (task 1.5) | proposed ("Login = step-up" superseded by D-112; lockout counting superseded by D-117) |
| [D-108](#d-108-websocket-subscription-transport-session-watcher-and-session-event-bus-task-110) | WebSocket subscription transport, session watcher and session event bus (task 1.10) | proposed (re-validation details superseded by D-118) |
| [D-109](#d-109-webauthn-passkeys-as-a-second-factor-task-18) | WebAuthn passkeys as a second factor (task 1.8) | proposed (last-factor error code superseded by D-112; AAL2 without user verification superseded by D-116) |
| [D-110](#d-110-first-run-setup-code-user-administration-and-break-glass-cli-task-16) | First-run setup code, user administration and break-glass CLI (task 1.6) | proposed (setup-code part superseded by D-113) |
| [D-111](#d-111-totp-recovery-codes-and-second-factor-failures-task-17) | TOTP, recovery codes and second-factor failures (task 1.7) | proposed (last-factor error code superseded by D-112) |
| [D-112](#d-112-2fa-policy-two-step-login-forced-enrollment-and-step-up-task-19) | 2FA policy, two-step login, forced enrollment and step-up (task 1.9) | accepted |
| [D-113](#d-113-first-admin-created-via-cli-only-web-setup-flow-removed) | First admin created via CLI only; web setup flow removed | accepted (migration 00011 detail superseded by D-118) |
| [D-114](#d-114-just-dev-runs-infra-only-postgres-mailpit-adminer-app-runs-natively) | `just dev` runs infra only (postgres, mailpit, adminer); app runs natively | accepted |
| [D-115](#d-115-compose-one-shot-admin-service-and-bekup_keys_dir-task-111) | Compose: one-shot `admin` service and `BEKUP_KEYS_DIR` (task 1.11) | accepted |
| [D-116](#d-116-passkey-aal2-requires-user-verification) | Passkey AAL2 requires user verification | accepted |
| [D-117](#d-117-credential-verification-hardening-single-root-operations-limiters-atomic-lockouts) | Credential-verification hardening: single-root operations, limiters, atomic lockouts | accepted |
| [D-118](#d-118-m1-review-fixes-websocket-principal-rebuild-shutdown-posture-audits-and-smaller-items) | M1 review fixes: WebSocket principal rebuild, shutdown, posture audits and smaller items | accepted |
| [D-119](#d-119-key-check-values-stored-per-key-id-and-verified-at-startup) | Key-check values stored per key ID and verified at startup | accepted |

---

## D-001: Security first, no compromises

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** bekup holds credentials for production databases and copies of
  their full contents. A compromise is catastrophic.
- **Decision:** Security is the top priority. When convenience and security
  conflict, security wins. Any weakened setting (encryption or TLS opt-out)
  requires an explicit admin action with step-up auth, shows a permanent
  warning badge, raises a security alert and is audited.
- **Alternatives considered:** None recorded.
- **Consequences:** Secure defaults everywhere; some setups (unpinned TLS,
  unencrypted backups) need deliberate, visible admin action. Every later
  decision is evaluated against this principle.
- **Related:** all tasks; `SECURITY.md` (task 0.2).

## D-002: Name, module and license

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The project needs a name, a Go module path and an open-source
  license.
- **Decision:** The project is called **bekup**, the Go module is
  `github.com/jlentink/bekup`, and it is MIT licensed.
- **Alternatives considered:** None recorded.
- **Consequences:** Permissive license; imports use the module path above.
  No direct security impact.
- **Related:** task 0.1; `go.mod`, `LICENSE`.

## D-003: Dump execution via a dedicated runner sidecar

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Native dump tools must match the server version, which means
  running official Docker images. Docker socket access is equivalent to root
  on the host.
- **Decision:** Dumps run in short-lived official images (multi-arch,
  digest-pinned), started **only by a dedicated `bekup runner` sidecar**. The
  sidecar is the single component with Docker access and exposes a narrow,
  typed, mTLS gRPC API. The web backend never talks to Docker or to target
  DBs.
- **Alternatives considered:** A Docker socket proxy in front of `serve`
  (rejected: still gives the internet-facing component broad Docker API
  access); bundling the client binaries in the bekup image (rejected: cannot
  version-match every server version).
- **Consequences:** An extra container and an mTLS link to operate. Security:
  the internet-facing `serve` process holds neither Docker access nor network
  reach to target DBs; the runner is a small, auditable trust boundary.
- **Related:** tasks 2.4–2.7a; D-029–D-033; `internal/runner`,
  `internal/runnerclient`, `proto/runner/v1`.

## D-004: "Dump all" and database selection

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Jobs may back up a whole server or specific databases.
- **Decision:** "Dump all" produces one file per database, plus a Postgres
  globals file. "Dump one/several" means an explicit DB selection.
- **Alternatives considered:** None recorded.
- **Consequences:** Per-database restore and retention granularity; globals
  are restored separately (admin-only, see D-036).
- **Related:** tasks 3.5, 3.6; `internal/jobs`.

## D-005: Retention policy

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Old backups must be pruned without risking data loss.
- **Decision:** Keep last N and/or keep younger than X days, per destination.
  Retention never deletes the newest successful backup and respects Object
  Lock.
- **Alternatives considered:** GFS (grandfather-father-son) rotation
  (rejected for Phase 1 in favour of the simpler count/age rules).
- **Consequences:** Simple, predictable rules. Security/safety: a
  misconfiguration can never delete the last good backup.
- **Related:** task 3.8; `internal/retention`.

## D-006: Reusable destinations with fan-out

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The same storage targets are shared by many jobs, and one dump
  may need to go to several places.
- **Decision:** Destinations are reusable entities. One job targets one or
  more; the dump runs once and fans out.
- **Alternatives considered:** None recorded.
- **Consequences:** One dump load on the target DB per run; per-destination
  failure handling (see D-037). Destination secrets live in one place.
- **Related:** tasks 2.8–2.12, 3.1; `internal/storage`, `internal/pipeline`.

## D-007: Immutable backups

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Ransomware and compromised credentials may try to delete
  backups.
- **Decision:** Support S3 Object Lock / versioning plus a "no-delete
  credentials" mode where retention is handled by bucket lifecycle rules.
  Unexpected deletions trigger an alert.
- **Alternatives considered:** None recorded.
- **Consequences:** bekup can run with credentials that cannot delete data.
  Security: a compromised bekup cannot destroy locked backups.
- **Related:** tasks 2.9, 3.8, 5.5; `docs/security/immutable-backups.md`.

## D-008: Backup file encryption on by default

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Backup files contain full database contents and are stored on
  third-party storage.
- **Decision:** Encryption is **on by default** using age: either a versioned
  per-job passphrase stored encrypted, or a public key whose private key is
  supplied at restore time. Only an **Admin** can opt a job out (step-up,
  audited, badge, security alert).
- **Alternatives considered:** None recorded.
- **Consequences:** Storage providers never see plaintext. Recipient-key jobs
  keep the private key out of bekup entirely. Opting out follows D-001.
- **Related:** tasks 3.1, 3.2; `internal/pipeline`, `internal/crypto`;
  `docs/guides/encryption.md`.

## D-009: Schedules

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Users need intuitive schedules as well as cron power.
- **Decision:** Interval + anchor ("every 2 days at 03:00", "every month on
  day 1", with IANA timezone) **or** cron; jobs can also be manual-only.
- **Alternatives considered:** None recorded.
- **Consequences:** Needs careful DST and month-end handling (D-044).
- **Related:** tasks 3.3–3.5; `internal/scheduler`.

## D-010: Authentication and second factors

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The UI controls production database credentials and data.
- **Decision:** Local users (argon2id) with server-side sessions. Second
  factors: **passkeys (WebAuthn) + TOTP** + recovery codes. The 2FA
  requirement is **admin-configurable** (globally and per role).
- **Alternatives considered:** None recorded.
- **Consequences:** No external IdP dependency in Phase 1. Sessions can be
  revoked server-side. See D-047 for parameters.
- **Related:** tasks 1.5–1.9; `internal/auth`.

## D-011: Roles

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Different people operate backups, own specific databases, and
  administer secrets.
- **Decision:** **Admin**: everything. **Maintainer**: all jobs, runs,
  restores and owner assignment; *uses* connections/destinations (non-secret
  metadata only), no CRUD on them, **no downloads**. **Backup owner** (per
  job): view, run now, download, restore.
- **Alternatives considered:** None recorded.
- **Consequences:** Least privilege: maintainers never see secrets or
  download data. Enforced centrally (D-053).
- **Related:** tasks 1.4, 6.8; `internal/authz`.

## D-012: Downloads

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Downloading a backup exfiltrates a full database.
- **Decision:** **Admins + owners only** (owners only for their own jobs). Two
  modes: `raw` (as stored, encrypted) and `plain` (decrypted and decompressed
  into a ready-to-use file: `.dump` for pg_restore, `.sql` for
  MySQL/MariaDB).
- **Alternatives considered:** None recorded.
- **Consequences:** Maintainers cannot download. Downloads go through
  single-use tickets with step-up (D-056).
- **Related:** tasks 3.9, 6.6; `docs/guides/downloads.md`.

## D-013: Restore targets

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** A restore can overwrite production data.
- **Decision:** Admin/Maintainer: the original or any connection with the same
  engine (existing DB or new DB). **Owner: only DBs their own jobs back up, or
  a new DB on those connections.** Overwrites require type-the-name
  confirmation.
- **Alternatives considered:** None recorded.
- **Consequences:** Owners cannot use restores to write into databases they
  do not own.
- **Related:** tasks 4.5, 6.7; `internal/restore`.

## D-014: Fresh second factor for every restore

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Restores are destructive and high-impact.
- **Decision:** **Every restore requires a fresh second-factor check**
  (passkey/TOTP, at most 5 minutes old), whatever the global 2FA policy. Users
  without 2FA are sent to enrollment first.
- **Alternatives considered:** None recorded.
- **Consequences:** A stolen session or password alone cannot trigger a
  restore. See D-057.
- **Related:** tasks 1.9, 4.5, 6.7.

## D-015: Temporary restore connections

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Admins sometimes need to restore into a server that is not a
  configured connection.
- **Decision:** **Admin only.** Created inside the restore wizard, saved
  encrypted with `temporary=true` + `expires_at`, usable **only as a restore
  target**, deleted automatically after the restore completes or after N hours
  (default 24h, configurable max). Credentials are crypto-shredded on
  deletion. The runner target allowlist and TLS defaults still apply.
- **Alternatives considered:** None recorded.
- **Consequences:** No lingering credentials for one-off targets. See D-058.
- **Related:** tasks 4.4, 6.7.

## D-016: Key generation

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Weak or hand-made keys undermine every cryptographic control.
- **Decision:** `just keygen` / `bekup keys generate` (256-bit master key
  file, mode 0600, escrow instructions) + `bekup certs init` (mTLS). In the
  UI: generate a strong job passphrase, or an **age keypair** whose private key
  is shown and downloaded once and never stored; generate **ed25519 SSH
  keypairs** for tunnels/SFTP (private key stored encrypted, public key shown
  for `authorized_keys`). All from `crypto/rand`.
- **Alternatives considered:** None recorded.
- **Consequences:** Users never need to craft keys by hand. Age private keys
  never touch the bekup DB.
- **Related:** tasks 0.5, 0.6, 2.11, 2.12, 3.2;
  `docs/security/key-management.md`.

## D-017: Admin UI TLS terminated by a reverse proxy

- **Date:** 2026-10-26
- **Status:** accepted; superseded in part by D-093, D-094 (proxy implementation) and D-095 (plain HTTP)
- **Decided by:** user
- **Context:** The UI must be served over HTTPS with automatic certificates.
- **Decision:** TLS is **terminated by a reverse proxy** (Traefik or
  nginx-proxy, shipped as compose profiles `traefik` / `nginx`). `bekup serve`
  speaks plain HTTP on an internal Docker network only (never published),
  trusts forwarding headers only from the configured proxy, and refuses a
  non-https `BEKUP_BASE_URL` outside dev mode.
- **Alternatives considered:** Built-in TLS for the UI (rejected).
- **Consequences:** Certificate management uses mature tooling. Security: the
  plain-HTTP hop exists only on an internal network; forwarded headers are
  trusted only from configured proxies (D-048).
- **Related:** tasks 0.7, 0.7b, 1.1; `docs/installation/reverse-proxy.md`.

## D-018: bekup's own TLS links and pinning

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Apart from the UI, every TLS link is bekup's responsibility.
- **Decision:** **Runner mTLS**: private CA pinned + SAN check. **Target
  DBs**: `verify-full` / `VERIFY_IDENTITY` where the native client trusts
  **only the pinned certificate** (captured on first use, confirmed by an
  admin; never the system roots), plus a **leaf SPKI pin check in Go by the
  runner before every run**. **SSH/SFTP**: pinned host keys. S3, SMTP and
  webhooks use standard verification against the system roots (TLS ≥1.2,
  https-only; optional custom CA bundle for private S3 such as minio) and are
  **not pinned**.
- **Alternatives considered:** Trusting system roots for target DBs
  (rejected); a TLS-terminating DB proxy in the runner (rejected: TLS must run
  end to end between the native client and the DB); pinning S3/SMTP/webhooks
  (rejected: providers rotate certificates).
- **Consequences:** A compromised or mis-issued public CA cannot intercept DB
  traffic. Admins must confirm pins and handle rotations (D-034).
- **Related:** tasks 0.6, 2.2, 2.4, 2.7a, 2.10; D-034, D-035, D-049.

## D-019: TOML configuration and search path

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Infrastructure config must be simple, reviewable and must not
  leak secrets.
- **Decision:** **TOML is the main config format.** `bekup.toml` for
  serve/worker and `runner.toml` for the runner (separate files, so serve's
  config can never change the runner's allowlists). **Search path:**
  `--config <path>` → local directory (`./bekup.toml`) → `/etc/bekup/`; the
  first match wins. Precedence: built-in defaults < TOML file < `BEKUP_*` env
  overrides < CLI flags. **Secrets are never inline**: only `*_file` paths
  (Docker secrets) are accepted for secret keys.
- **Alternatives considered:** None recorded.
- **Consequences:** Secrets never sit in config files, env or process
  listings. See D-041 for loading details.
- **Related:** task 0.3; `internal/config`; `docs/configuration.md`.

## D-020: justfile as the single task entry point

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Contributors and agents need one consistent way to run tasks.
- **Decision:** The **`justfile` is the single entry point** for every
  recurring task (dev, gen, lint, sec, test, build, docker, db, keygen, certs,
  release, **version bumping**). Running `just` with no arguments prints a
  styled, grouped help screen.
- **Alternatives considered:** None recorded.
- **Consequences:** CI and local runs use the same recipes. See D-059.
- **Related:** task 0.1b; `justfile`; `docs/development.md`.

## D-021: Web design with the frontend-design skill

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The UI should be distinctive and consistent.
- **Decision:** All web UI work uses the **`frontend-design` skill** (design
  system in task 0.8, every page in M6).
- **Alternatives considered:** None recorded.
- **Consequences:** Consistent design system; accessible (WCAG AA) and dark
  mode as defined in M6.
- **Related:** tasks 0.8, 6.1–6.10; `web/`.

## D-022: Restore safety: stage, verify, restore

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Restoring a tampered or truncated artifact can corrupt or
  compromise a database.
- **Decision:** **Stage → verify → restore**: download the encrypted artifact
  to a scratch volume, verify the hash and manifest signature in full, then
  restore from the local copy. PG overwrites go into a temp DB and are swapped
  in at the end; MySQL/MariaDB overwrite in place after an explicit warning.
- **Alternatives considered:** Streaming restore directly from the
  destination (implicitly rejected: nothing is verified before data is
  written).
- **Consequences:** Needs scratch space sized for the largest artifact.
  Security: tampered artifacts never reach the target DB. See D-039.
- **Related:** tasks 4.1–4.3; `internal/restore`.

## D-023: Notifications (Phase 1)

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Operators and owners need to know about failures and security
  events through channels of their choice.
- **Decision:** Channels: **in-app inbox** (always on), **email** (system
  SMTP), **webhook** (HMAC-signed, with timestamp); **push** is added in Phase
  2. Two layers: (1) **admin-managed shared channels** attached to jobs; (2)
  **per-user preferences** per event type (and optionally per job) choosing
  among their own channels, or off. Personal webhooks are allowed for every
  user (https only, SSRF guard, HMAC, max 5 per user, test send, audited).
  **Security alerts can't be fully disabled**: own-account alerts (and system
  security alerts for admins) always go to the inbox + email; users can only
  add channels.
- **Alternatives considered:** None recorded.
- **Consequences:** User-controlled outbound requests require the SSRF guard
  (D-055). See D-051 for the engine.
- **Related:** tasks 5.1–5.3b, 6.9, 6.10; `internal/notify`.

## D-024: Monitoring (Phase 1)

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Silent backup failure is the most common way backups fail.
- **Decision:** Missed-backup alerts (dead-man), security alerts to admins,
  Prometheus `/metrics` (internal port), post-dump checks and optional
  scheduled test restores.
- **Alternatives considered:** None recorded.
- **Consequences:** `/metrics` is not exposed publicly.
- **Related:** tasks 4.6, 5.4–5.6; `internal/monitor`;
  `docs/operations.md`.

## D-025: Packaging: single Go binary

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Simple distribution and deployment.
- **Decision:** A single Go binary with subcommands (`serve`, `worker`,
  `runner`, `migrate`, `admin`, `keys`, `certs`, `audit`, `verify`, `decrypt`,
  later `external-api`). The React build is embedded via `go:embed`.
- **Alternatives considered:** None recorded.
- **Consequences:** One image; each role runs as its own container with only
  the privileges it needs.
- **Related:** tasks 0.3, 0.7, 0.8; `cmd/bekup`.

## D-026: Web stack

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Choice of frontend tooling.
- **Decision:** Vite, React, TypeScript, Tailwind v4, shadcn/ui, **Apollo
  Client** + graphql-codegen, React Router, react-hook-form + zod.
- **Alternatives considered:** None recorded.
- **Consequences:** Typed GraphQL end to end. The CSP `style-src` is settled
  against Radix/sonner in task 0.8.
- **Related:** task 0.8; `web/`.

## D-027: CI/CD on GitHub Actions

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Quality and security checks must run on every change.
- **Decision:** GitHub Actions from M0 (lint, security scans, tests); on tag,
  a signed multi-arch image is published to GHCR.
- **Alternatives considered:** None recorded.
- **Consequences:** Actions are SHA-pinned (D-061).
- **Related:** tasks 0.9, 7.4; `.github/workflows/`.

## D-028: Go library set

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Platform choice from the plan.
- **Decision:** `pelletier/go-toml/v2` (config), `chi`, `gqlgen`, `pgx/v5` +
  `sqlc`, `goose` (embedded, forward-only in production),
  `riverqueue/river`, `grpc-go`, moby client (runner only), `x/crypto/ssh`,
  `pkg/sftp`, `aws-sdk-go-v2`, `klauspost/compress/zstd`, `filippo.io/age`,
  `go-webauthn/webauthn`, `pquerna/otp`, `robfig/cron/v3` (parser only),
  `cobra`, `log/slog`, `prometheus/client_golang`. Generators are pinned as
  `go tool` dependencies. `time/tzdata` is embedded.
- **Alternatives considered:** An ORM (rejected, see D-042).
- **Consequences:** The Docker client is imported only by the runner. Pinned
  generators make codegen reproducible.
- **Related:** all Go tasks; `go.mod`.

## D-029: Runner network isolation and target allowlist

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Dump containers run third-party tools with DB credentials and
  must not reach anything but their one target.
- **Decision:** Per run, the runner creates an `internal: true` Docker network,
  joins it, and runs a TCP proxy / SSH tunnel to exactly **one** target. The
  dump container sits alone on that network with `ExtraHosts` mapping the
  real DB hostname to the runner's proxy IP, so TLS `verify-full` still works
  end to end. The runner enforces a **target allowlist** (CIDRs/hosts in
  `runner.toml`); loopback, link-local and metadata ranges are always denied.
  `serve` cannot reach target DBs or Docker.
- **Alternatives considered:** A TLS-terminating DB proxy (rejected: breaks
  end-to-end verification); running dumps on a shared network (rejected).
- **Consequences:** Dump containers cannot reach the runner's gRPC port,
  other runs' tunnels or the internet. Even a compromised `serve` can only ask
  for allowlisted targets.
- **Related:** tasks 2.5, 2.6; `internal/runner`;
  `docs/security/target-allowlist.md`.

## D-030: Runner mTLS gRPC API

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** `serve` and the runner need an authenticated, narrow channel.
- **Decision:** gRPC over mTLS (TLS 1.3). `bekup certs init|rotate` creates a
  private CA plus per-component ed25519 certs. The CA key never ends up on a
  shared volume; each component gets only its own key + the CA cert via a
  Docker secret; clients are identified by SAN; cert expiry is a metric and
  alert. Keepalive configured; stream chunks ≤1 MiB; backpressure via gRPC
  flow control (no unbounded channels). Typed RPCs only: `Probe`
  (normalized errors so it can't be used as a port scanner), `Dump`,
  `DumpGlobals`, `Restore` (client-streaming), `CreateDatabase`,
  `RenameSwapDatabase` (PG), `VerifyDump`, `PrePull`, `Health`.
- **Alternatives considered:** Free-form commands (rejected).
- **Consequences:** Small, auditable attack surface; certificate rotation must
  be operated.
- **Related:** tasks 0.6, 2.4, 2.7; `proto/runner/v1/runner.proto`;
  `docs/security/mtls-certs.md`.

## D-031: Image allowlist and fixed container spec

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The runner must never run arbitrary images or container
  configurations.
- **Decision:** The image allowlist comes only from the runner's own config:
  an embedded `catalog.toml` (engine → version → image@multi-arch-index-digest,
  client binary names, `legacy` flag for EOL versions) plus an optional
  mounted extra-images file; never from the DB. Containers use a fixed spec:
  argv arrays only, `ReadonlyRootfs`, tmpfs `/tmp` (noexec, nosuid),
  `HOME=/tmp`, `MYSQL_HISTFILE=/dev/null`, `CapDrop: ALL`,
  `no-new-privileges`, non-root UID, default seccomp, memory/CPU/pids limits,
  no mounts, not privileged, label `bekup.run_id`. An orphan reaper removes
  labelled leftovers on start; containers are killed on cancel, timeout or
  stream loss. The runner is distroless nonroot with a `group_add` docker GID.
- **Alternatives considered:** None recorded.
- **Consequences:** Container escape surface is minimal. Docs cover rootless
  Docker / userns-remap as extra hardening.
- **Related:** tasks 2.1, 2.5, 2.6; `internal/engine`, `internal/runner`;
  `docs/installation/rootless-docker.md`.

## D-032: Secret delivery via stdin preamble

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Secrets in env or argv are visible via `docker inspect` and
  process listings.
- **Decision:** A fixed wrapper reads **base64, length-prefixed** records from
  stdin (password; optional CA/client cert/key) and writes them to 0600 files
  in tmpfs (MySQL option-file values escaped), then `exec`s the tool with fixed
  argv. The runner zeroes its buffers after the call.
- **Alternatives considered:** Environment variables or argv (rejected).
- **Consequences:** Secrets never appear in env, args or inspect output.
- **Related:** tasks 2.5, 2.6.

## D-033: Runner request validation

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Argument injection into native tools is a key threat.
- **Decision:** DB names must be in the server-reported list, pass a strict
  charset check and must not start with `-`. PG gets a quoted `dbname=`
  conninfo (never a raw name/URI). Hosts and ports are validated against the
  target allowlist. Unknown fields are rejected. Validators are fuzz-tested.
- **Alternatives considered:** None recorded.
- **Consequences:** Some exotic DB names may be unsupported by design.
- **Related:** task 2.5; `internal/runner`.

## D-034: Target-DB certificate pinning mechanics

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements the pinning stance of D-018.
- **Decision:** *Capture*: `Probe.FetchServerCertificate` in the runner
  (pgx / go-sql-driver with a capturing `VerifyPeerCertificate`, handling the
  PG SSLRequest and MySQL in-protocol TLS upgrade) returns chain, subject/SAN,
  validity and SPKI SHA-256; an admin confirms the pin (step-up, audited). The
  pin is the self-signed leaf or the issuing CA. *Enforcement*: the pinned
  cert is delivered via the stdin preamble as the client's **only** trust
  anchor (`sslrootcert` + `sslmode=verify-full`; `--ssl-ca` +
  `--ssl-mode=VERIFY_IDENTITY` for MySQL; `--ssl-ca` +
  `--ssl-verify-server-cert` for MariaDB), and the runner checks the **leaf
  SPKI pin** in Go before every run. *Mismatch*: hard failure + security
  alert; re-pinning needs admin + step-up and is audited; a backup pin can be
  staged before rotation; expiry warnings at 30/7 days. *Downgrade*: TLS-off
  or unpinned needs an admin, is audited, alerts and shows an "insecure
  transport" badge.
- **Alternatives considered:** System roots (rejected, D-018); TLS-terminating
  proxy (rejected, D-029).
- **Consequences:** Certificate rotations on DB servers need planning (backup
  pin). Docs recommend dedicated non-superuser backup and restore roles.
- **Related:** tasks 2.7a, 2.11, 5.5, 6.3; `docs/guides/connections.md`,
  `docs/security/db-roles.md`.

## D-035: SSH tunnels with mandatory host-key pins

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Many DBs are reachable only through a bastion.
- **Decision:** SSH tunnels use `x/crypto/ssh` (key or password) with a
  **mandatory host-key pin** confirmed via a TOFU fingerprint in the UI; there
  is no bypass. Tunnels terminate inside the runner's per-run proxy. SFTP
  destinations also use pinned host keys.
- **Alternatives considered:** Optional host-key checking (rejected).
- **Consequences:** Host-key mismatches fail runs and raise security alerts.
- **Related:** tasks 2.2, 2.6, 2.10, 5.5; `internal/tunnel`.

## D-036: Engine-specific dump and restore rules

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Each engine needs correct, safe tool invocations. MySQL and
  MariaDB are strictly separate engines.
- **Decision:** **Postgres**: `pg_dump -Fc -Z0`; **client major ≥ server major
  is enforced** (block, not warn); restore image = max(dump, target version);
  globals via `pg_dumpall --globals-only` with automatic fallback to
  `--no-role-passwords`; post-dump `pg_restore --list`; restore with
  `pg_restore --exit-on-error` (`--no-owner` option) into a temp DB then
  rename-swap; globals restore via `psql --no-psqlrc -v ON_ERROR_STOP=1` is a
  separate admin-only action; only client versions with the `\restrict` fix
  (CVE-2025-8714) are pinned. **MySQL**: `mysqldump --single-transaction
  --routines --triggers --events --hex-blob --no-tablespaces
  --default-character-set=utf8mb4 --set-gtid-purged=OFF` (no `--databases`),
  warn on non-InnoDB tables, check the "Dump completed" trailer, restore with
  `mysql --system-command=OFF --local-infile=0
  --default-character-set=utf8mb4 <db>`. **MariaDB**: `mariadb-dump` with
  equivalent flags, restore with `mariadb --sandbox --local-infile=0`; binary
  names come from the catalog.
- **Alternatives considered:** Warning instead of blocking on PG version
  mismatch (rejected).
- **Consequences:** Malicious dumps cannot run shell commands or read local
  files on restore. Piped `-Fc` gives no parallel restore.
- **Related:** tasks 2.1, 4.2, 4.3, 7.3; `internal/engine`.

## D-037: Backup pipeline and artifact layout

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Streaming large dumps to several destinations safely.
- **Decision:** Runner stream → counter → zstd → age (default on;
  `SetMaxWorkFactor` bounded) → SHA-256 → fan-out with a **bounded buffer per
  destination**. A destination that fails or lags beyond a threshold is
  dropped and the run becomes `partial`. Disk/SFTP write `*.partial` and
  rename atomically. S3 uses multipart with ≥64 MiB parts (configurable),
  aborts on failure, sets `RequestChecksumCalculation=WhenRequired` for
  S3-compatible stores, and applies Object Lock retention headers. Layout:
  `{prefix}/{job-slug}/{timestamp}/{enc(db)}.{dump|sql}.zst.age` +
  `manifest.json`.
- **Alternatives considered:** None recorded.
- **Consequences:** Bounded memory (part size × concurrency); no partially
  written files appear as valid artifacts.
- **Related:** tasks 2.8–2.10, 3.1, 3.6; `internal/pipeline`,
  `internal/storage`.

## D-038: Signed manifests

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Artifacts on external storage can be swapped or replayed.
- **Decision:** A minimal manifest (artifact names, sizes, sha256,
  engine/version, timestamps, run/job IDs, encryption recipient fingerprint,
  passphrase version) is **signed with an ed25519 instance key carrying a key
  ID**; old public keys are kept. Restore cross-checks the manifest against
  DB records, defeating replay of old manifests.
- **Alternatives considered:** None recorded.
- **Consequences:** The signing-key fingerprint must be escrowed for offline
  verification.
- **Related:** tasks 0.5, 3.1, 4.1, 4.7; `docs/reference/` (manifest format).

## D-039: Restore staging and identity handoff

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-022.
- **Decision:** Stage the encrypted artifact to `BEKUP_SCRATCH_DIR`
  (size-checked; plaintext-compressed staging for opt-out jobs is wrapped
  with an ephemeral age key) → verify hash + signature + DB cross-check →
  decrypt → decompress → runner `Restore`. User-supplied age identities
  **never go into River job args**; they go into an encrypted handoff table
  with a 15-minute TTL and are deleted after use. Staging files are deleted
  in all paths.
- **Alternatives considered:** Passing identities in job args (rejected).
- **Consequences:** No plaintext at rest on scratch; no private keys in the
  job queue.
- **Related:** task 4.1; `internal/restore`.

## D-040: Offline recovery tooling

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Backups must be usable when the bekup DB itself is lost.
- **Decision:** `bekup verify` / `bekup decrypt` with a passphrase or
  identity, a documented file format, destination **re-index/import**,
  passphrase export (admin, step-up, audited), and documentation for
  master-key escrow and the signing-key fingerprint.
- **Alternatives considered:** None recorded.
- **Consequences:** Passphrase export is a high-risk operation, guarded per
  D-001.
- **Related:** tasks 3.2, 4.7; `docs/recovery.md`.

## D-041: Configuration loading details

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-019.
- **Decision:** Files are never merged. An explicit `--config` (or
  `BEKUP_CONFIG`) that is missing is a hard error with no fallback. If no file
  is found, startup fails (except in dev mode). Every candidate must be a
  regular file (no symlink to a writable location), owned by the running user
  or root, and not group- or world-writable; secret files must not be
  world-readable. Strict parsing (unknown keys are errors with line/column).
  Env overrides map `BEKUP_<SECTION>_<KEY>` 1:1 to TOML keys. Inline secrets
  are rejected outside dev mode. Validation reports all errors at once
  (https base_url outside dev, trusted_proxies required, ...). `bekup config
  validate` and `bekup config print --redacted` (with source per key).
  Annotated example files ship and a test checks they cover every key. No hot
  reload. Runtime admin settings (SMTP, 2FA policy, SSRF allowlist) live in
  the DB `settings` table, not in TOML.
- **Alternatives considered:** Hot reload (rejected: inconsistent mid-run
  state); merging multiple files (rejected).
- **Consequences:** Misconfiguration fails fast and loudly; tampering with
  config via a writable file is refused.
- **Related:** task 0.3; `internal/config`; `docs/configuration.md`.

## D-042: sqlc and goose with vendored River schema

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Access to bekup's own Postgres DB.
- **Decision:** **sqlc + SQL migrations; no ORM.** goose with plain `.sql`
  files in `internal/db/migrations/` embedded via `embed.FS`, applied by
  `bekup migrate` and at startup (advisory-locked), run as `bekup_owner`;
  forward-only in production; released migrations are never edited. **River's
  schema is vendored into the goose migrations** (`river migrate-get`): one
  migration system. All queries live in `internal/db/queries/*.sql`; sqlc
  generates `internal/db/sqlc`; no hand-built SQL anywhere else. Transactions
  use a `db.Tx` helper. CI runs `sqlc vet`/`sqlc diff` and an up → down → up
  check plus a `schema.sql` comparison.
- **Alternatives considered:** An ORM (rejected); River's own migrator next
  to goose (rejected: two version histories).
- **Consequences:** SQL injection surface is limited to sqlc parameters.
- **Related:** task 0.4; `internal/db`; `docs/development.md`.

## D-043: River configuration

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Background jobs must not duplicate or hang.
- **Decision:** Explicit per-worker `Timeout` (backup = job timeout, restore =
  its own); `MaxAttempts` backup 2, **restore 1**, notify 10;
  `RescueStuckJobsAfter` > longest timeout; unique `ByState` =
  available/scheduled/running/retryable. Job args never contain secrets.
- **Alternatives considered:** None recorded.
- **Consequences:** Restores are never retried automatically.
- **Related:** task 3.4; `internal/jobs`, `internal/scheduler`.

## D-044: Scheduler semantics

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-009 safely across multiple `serve` instances.
- **Decision:** No session advisory lock on pooled connections. Each 15s tick
  runs `SELECT … FOR UPDATE SKIP LOCKED` + River unique insert + `next_run_at`
  advance in one transaction. Sub-day intervals are absolute durations;
  intervals of a day or more use wall-clock time in the job's IANA zone
  (month-end clamped, nonexistent times shifted forward, ambiguous times take
  the first occurrence). robfig's DST behaviour is property-tested. Missed
  runs collapse into one catch-up run. Timestamps are `timestamptz`, zones are
  IANA names.
- **Alternatives considered:** A session advisory lock (rejected: unsafe with
  pooled connections).
- **Consequences:** Safe horizontal scaling of `serve`.
- **Related:** tasks 3.3, 3.4; `internal/scheduler`.

## D-045: Concurrency limits

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Resource exhaustion and DoS protection.
- **Decision:** Limits on global runs, per-connection runs, downloads,
  argon2id hashing (semaphore) and WS connections per user.
- **Alternatives considered:** None recorded.
- **Consequences:** Bounded load on targets and on bekup.
- **Related:** tasks 1.5, 1.10, 3.6, 3.9.

## D-046: Credential envelope encryption

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Connection, destination and channel secrets are stored in the
  DB.
- **Decision:** AES-256-GCM envelope; keyring from `BEKUP_MASTER_KEY_FILE`
  (env var only in dev mode); key ID in the ciphertext; AAD =
  `table:row_id:field`; a key-check value is verified at startup; `bekup keys
  generate|rotate`. Secrets are **write-only** in GraphQL (`hasPassword`) and
  zeroed after use.
- **Alternatives considered:** None recorded.
- **Consequences:** A DB dump alone does not reveal credentials; ciphertexts
  cannot be swapped between rows/fields. The master key must be escrowed.
- **Related:** task 0.5; `internal/crypto`;
  `docs/security/key-management.md`.

## D-047: Authentication details and step-up

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-010.
- **Decision:** argon2id (m=64MiB, t=3, p=1, rehash on change). Password
  policy: ≥12 chars, strength check, common-password list. `__Host-` cookie
  (HttpOnly, Secure, SameSite=Strict); token stored as sha256; sessions carry
  AAL and `stepup_at`, rotate on login and privilege change, have idle +
  absolute expiry, are all revoked on password change and can be
  listed/revoked by the user. Lockout with backoff; generic, timing-safe
  errors. WebAuthn (RP ID from `BEKUP_BASE_URL`) + TOTP (replay protection via
  last used step) + recovery codes. **Step-up** (≤5 min) for secret changes,
  restores, download tickets, user/role changes, opt-outs, 2FA changes,
  passphrase export and key operations. Setup token: one-time, 1h TTL.
- **Alternatives considered:** None recorded.
- **Consequences:** Sensitive operations need recent re-authentication.
- **Related:** tasks 1.5–1.9; `internal/auth`.

## D-048: Reverse-proxy hardening and trusted proxies

- **Date:** 2026-10-26
- **Status:** accepted; superseded in part by D-093, D-094
- **Decided by:** user
- **Context:** Implements D-017.
- **Decision:** Traefik (Let's Encrypt via labels) or nginx-proxy +
  acme-companion as compose profiles, shipped hardened: TLS 1.2+/1.3 only,
  modern ciphers, HSTS preload-ready, HTTP→HTTPS redirect, request size
  limits, WebSocket passthrough. `serve` listens only on the internal
  `frontend` network. `BEKUP_TRUSTED_PROXIES` (CIDRs) is required outside dev
  mode; `X-Forwarded-*` from anyone else is ignored. bekup also sets HSTS and
  security headers itself.
- **Alternatives considered:** Built-in TLS (rejected, D-017).
- **Consequences:** Defence in depth if the proxy is misconfigured.
- **Related:** tasks 0.7b, 1.1; `docs/installation/reverse-proxy.md`.

## D-049: Outbound TLS without pinning

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** S3, SMTP and webhooks talk to third parties.
- **Decision:** System roots, `MinVersion` TLS 1.2, https/STARTTLS/implicit
  TLS required, with an optional admin-provided CA bundle (e.g. private S3).
- **Alternatives considered:** Pinning (rejected, D-018).
- **Consequences:** Plaintext SMTP/HTTP is not supported.
- **Related:** tasks 2.9, 5.1, 5.2.

## D-050: HTTP and GraphQL hardening

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Browser-facing attack surface.
- **Decision:** `BEKUP_BASE_URL` must be https outside dev; all links are built
  from it (never from Host). Strict CSP (`script-src 'self'`; `style-src`
  settled in 0.8), HSTS, frame-ancestors none, nosniff, Referrer-Policy
  no-referrer. gqlgen allows **POST + `application/json` only**.
  Origin/`Sec-Fetch-Site` checks on mutations and the WS upgrade.
  Depth/complexity/size limits, introspection only for authenticated users,
  masked errors, per-IP and per-user rate limits.
- **Alternatives considered:** GET and multipart GraphQL transports
  (rejected: CSRF surface).
- **Consequences:** CSRF and Host-header injection are mitigated.
- **Related:** tasks 0.8, 1.1, 1.2; `internal/httpserver`, `internal/graph`.

## D-051: Notifications engine

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-023.
- **Decision:** Events (`run.succeeded|failed|partial`, `run.missed`,
  `restore.completed|failed`, `retention.anomaly`, `security.account`,
  `security.system`) → recipients = shared channels on the job + every user
  who **may currently see the job** (authz at delivery time) with matching
  preferences → one River notify job per (recipient, channel), idempotent per
  event. Preference resolution: per-job override > per-event default > role
  defaults. Email requires a verified address (signed link). Payloads carry
  minimal data, never secrets or DB contents. Per-user rate limiting/digest;
  repeatedly failing webhooks are auto-disabled with an inbox notice.
- **Alternatives considered:** None recorded.
- **Consequences:** Users who lose access stop receiving events immediately.
- **Related:** tasks 5.3, 5.3b; `internal/notify`;
  `docs/guides/notifications.md`.

## D-052: WebSocket subscriptions

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Live run logs and notifications.
- **Decision:** Cookie auth on upgrade; the session is re-validated
  periodically and on every event; authz at delivery time; the socket closes
  on logout, revoke or role change. NOTIFY payloads carry IDs only.
- **Alternatives considered:** None recorded.
- **Consequences:** No stale-privilege data leaks over long-lived sockets.
- **Related:** tasks 1.10, 3.7.

## D-053: Central deny-by-default authorization

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-011 consistently.
- **Decision:** Central `internal/authz`, deny-by-default, `@hasRole` +
  per-object ownership checks; a matrix test covers every operation × role
  and CI fails when an operation is unmapped.
- **Alternatives considered:** None recorded.
- **Consequences:** Every new GraphQL operation must update the matrix
  (global AC).
- **Related:** task 1.4; `internal/authz`.

## D-054: Least-privilege DB roles and audit hash chain

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The app must not be able to rewrite history.
- **Decision:** `bekup_owner` runs migrations; `bekup_app` does runtime DML.
  The audit log hash is computed in a `SECURITY DEFINER` trigger under an
  advisory xact lock; UPDATE/DELETE/TRUNCATE are revoked and raising triggers
  added; `bekup audit verify`; the chain head is signed periodically and
  exported to destinations/webhook; an archival policy applies.
- **Alternatives considered:** None recorded.
- **Consequences:** A compromised app role cannot silently alter the audit
  log.
- **Related:** tasks 0.4, 1.3; `internal/db`, `internal/audit`;
  `docs/security/audit.md`.

## D-055: SSRF guard and disk confinement

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Admins and users configure outbound URLs and hosts.
- **Decision:** `internal/netguard`: a dial-time `Control` hook (defeats DNS
  rebinding) denies private/loopback/link-local/metadata ranges by default,
  with an admin allowlist. Used for webhooks, S3 endpoints, SMTP and SFTP; no
  redirects. Disk destinations are confined to `BEKUP_DISK_ROOT` via
  `os.Root`.
- **Alternatives considered:** Resolve-then-check validation (implicitly
  rejected: vulnerable to DNS rebinding).
- **Consequences:** Private S3/SMTP endpoints need an explicit allowlist
  entry.
- **Related:** tasks 2.3, 2.8; `internal/netguard`, `internal/storage`.

## D-056: Download tickets

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-012.
- **Decision:** Mutation `createDownloadTicket` (step-up, audited; for
  `plain` mode with recipient encryption the identity is sent in the POST)
  returns a single-use 60s ticket bound to the session; `GET
  /api/download/{ticket}` streams with `Cache-Control: no-store` and a
  sanitized Content-Disposition filename. `plain` decrypts and decompresses;
  the dialog shows the matching restore command.
- **Alternatives considered:** None recorded.
- **Consequences:** Download URLs are useless if leaked.
- **Related:** tasks 3.9, 6.6.

## D-057: Restore gate

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-014.
- **Decision:** `restoreArtifact` needs a session with `aal2_at` ≤5 min old
  from a passkey/TOTP check (a password alone is not enough). Users without
  2FA get `REQUIRES_2FA_ENROLLMENT`.
- **Alternatives considered:** None recorded.
- **Consequences:** Restore UX includes a 2FA prompt every time.
- **Related:** task 4.5.

## D-058: Temporary connection implementation

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-015.
- **Decision:** `connections.temporary` + `expires_at` +
  `created_for_restore_id`; excluded from job creation and listings (except in
  the restore wizard); a cleanup worker deletes them after the restore or on
  expiry and zeroes `secret_enc`; creation and use are audited.
- **Alternatives considered:** None recorded.
- **Consequences:** Temporary credentials are crypto-shredded.
- **Related:** task 4.4.

## D-059: justfile details and version bumping

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Implements D-020.
- **Decision:** `[group]`/`[doc]` attributes; the default recipe prints a
  banner + colored grouped help with NO_COLOR support. `just bump
  [patch|minor|major|<x.y.z>]` updates `VERSION`, `web/package.json` and
  `CHANGELOG.md` (git-cliff from Conventional Commits), then makes a signed
  commit + annotated tag, refusing when the tree is dirty or not on main. The
  version is injected via `-ldflags` and shown in the UI and `bekup version`.
- **Alternatives considered:** None recorded.
- **Consequences:** Releases are signed and reproducible from tags.
- **Related:** tasks 0.1b, 7.4; `justfile`, `VERSION`, `CHANGELOG.md`.

## D-060: Logging redaction and limits

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Logs must never leak secrets.
- **Decision:** `log/slog` with a redaction handler; known secret values are
  redacted from runner log lines; caps on run-log lines and line length.
- **Alternatives considered:** None recorded.
- **Consequences:** "No secrets in logs" is part of every task's AC.
- **Related:** tasks 0.3, 3.6.

## D-061: Supply-chain measures

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Dependencies, images and CI are attack vectors.
- **Decision:** Images pinned by digest (Renovate), Actions pinned by SHA,
  `govulncheck`, `gosec`, `osv-scanner`, Trivy, SBOM (syft), cosign keyless
  signing + provenance. bekup images are distroless nonroot with a read-only
  root filesystem.
- **Alternatives considered:** None recorded.
- **Consequences:** Every update is explicit and reviewable; releases are
  verifiable with `cosign verify`.
- **Related:** tasks 0.1, 0.7, 0.9, 7.4; `renovate.json`,
  `.github/workflows/`.

## D-062: Data model conventions

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Core tables are created in task 0.4; each feature task adds its
  own migration.
- **Decision:** Secrets are stored only in `*_enc` columns (D-046). Soft
  delete for connections/destinations/jobs that still have artifacts.
  Timestamps are `timestamptz`, zones IANA names. Table set as listed in the
  plan's "Data model" section.
- **Alternatives considered:** None recorded.
- **Consequences:** Artifacts never lose their connection/destination
  context.
- **Related:** task 0.4 and feature tasks; `internal/db/migrations/`.

## D-063: Documentation in docs/

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Users and operators need complete, current documentation.
- **Decision:** `docs/` holds Markdown user and operator documentation
  (rendered on GitHub, static-site-ready) per the plan's repo layout. A task
  that adds a user-visible feature, config option or CLI command updates the
  matching page (global AC). CLI and GraphQL references are generated by
  `just docs`.
- **Alternatives considered:** None recorded.
- **Consequences:** Docs grow with the code; task 7.5 does a completion pass.
- **Related:** tasks 0.1, 7.5; `docs/`.

## D-064: golangci-lint configuration

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.1)
- **Context:** Task 0.1 requires a `.golangci.yml` with gosec; the plan does
  not specify the linter set or exclusions.
- **Decision:** golangci-lint v2 format with `default: none` and an explicit
  list including gosec (audit mode), errcheck (type assertions and blank
  checks on), govet (all analyzers except fieldalignment/shadow),
  staticcheck, revive, gocritic, bodyclose, noctx, sqlclosecheck,
  rowserrcheck, errorlint, misspell, sloglint (static messages, snake_case
  keys), contextcheck, nilerr and others. `nolintlint` requires a specific
  linter and an explanation. Generated code (sqlc, gqlgen, protoc) is
  excluded. In `_test.go` files only `forcetypeassert` and gosec G101/G404 are
  relaxed. Formatters: gofmt + goimports with the module as local prefix.
- **Alternatives considered:** golangci-lint defaults (`standard`) only
  (rejected: misses security linters).
- **Consequences:** Strict lint baseline from day one; suppressions are
  visible and justified.
- **Related:** tasks 0.1, 0.1b, 0.9; `.golangci.yml`.

## D-065: Renovate update policy

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.1)
- **Context:** The plan requires digest-pinned images and SHA-pinned Actions
  via Renovate but does not define the update policy.
- **Decision:** Extend `config:recommended`, `helpers:pinGitHubActionDigests`
  and `docker:pinDigests`; pin npm versions exactly; group minor/patch updates
  per ecosystem; require dashboard approval for major updates; a **3-day
  minimum release age** for regular updates (security updates bypass it and
  run at any time); OSV vulnerability alerts on; the `go` directive is bumped
  manually; a regex manager keeps digests in `catalog.toml` up to date. Weekly
  schedule (Monday before 06:00).
- **Alternatives considered:** Immediate updates (rejected: exposure to
  compromised releases that are pulled within days).
- **Consequences:** Small delay on non-security updates in exchange for
  protection against malicious releases.
- **Related:** tasks 0.1, 2.1; `renovate.json`.

## D-066: .gitignore blocks key and secret material

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.1)
- **Context:** Local configs, keys and certificates must never be committed.
- **Decision:** `.gitignore` excludes `bekup.toml`, `runner.toml` (but not the
  `*.example.toml` files), `.env*` (except `.env.example`), `*.key`, `*.pem`,
  `*.p12`, `*.pfx`, `*.age`, `id_ed25519*`, `secrets/`, top-level `certs/`,
  `scratch/` and `data/`, plus `.idea/` and `.claude/settings.local.json`.
- **Alternatives considered:** Ignoring only config files (rejected).
- **Consequences:** Test fixtures that need keys or certificates must be
  generated at test time instead of committed (or force-added deliberately
  with review).
- **Related:** task 0.1; `.gitignore`.

## D-067: Owner assignment cannot grant maintainers downloads

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.2)
- **Context:** Maintainers may assign job owners but have **no downloads**
  (D-011); owners may download their own jobs' backups (D-012). The plan does
  not say whether a maintainer can be (or make themselves) an owner, which
  would bypass the no-download rule. Found while writing the threat model
  (`SECURITY.md` OI-1).
- **Decision:** Download permission requires the owner relationship **and** a
  role other than Maintainer: a Maintainer listed in `job_owners` still gets
  no downloads (admins keep downloads via their role). Maintainers cannot add
  themselves as owners. Every owner change is audited and raises a security
  alert to admins. The authz matrix covers "maintainer who is also owner".
- **Alternatives considered:** Forbid maintainers from ever being owners
  (simpler, but blocks legitimate view/run-now use); rely on audit only
  (detects, doesn't prevent).
- **Consequences:** Closes a privilege-escalation path from Maintainer to data
  access. A maintainer can still assign another account they control
  (collusion); that is audited and alerted.
- **Related:** tasks 1.4, 3.5, 3.9, 5.5, 6.8; `SECURITY.md` §9.

## D-068: Restore privilege warnings for superuser and cross-source restores

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.2)
- **Context:** Hardened restore clients block client-side escapes (`\!`,
  `system`, `LOAD DATA LOCAL`), but SQL inside a dump runs with the restore
  role's privileges. A dump from a malicious source DB (for example one a
  backup owner controls) restored into another connection whose role is a
  superuser could run `COPY ... FROM PROGRAM`, `ALTER SYSTEM`, untrusted
  language functions or `SELECT ... INTO OUTFILE` on that server
  (`SECURITY.md` OI-2).
- **Decision:** `Probe` reports whether the connection's role is privileged
  (PG `rolsuper`; MySQL/MariaDB `SUPER`, `FILE` or `ALL PRIVILEGES`). The
  connection shows a "privileged restore role" badge. A restore into such a
  connection requires an Admin and an explicit acknowledgement. A restore
  whose source connection differs from the target is flagged in the restore
  wizard and recorded in the audit entry. Docs keep recommending dedicated
  non-superuser restore roles.
- **Alternatives considered:** Block restores into superuser connections
  (breaks globals restore and some managed setups); sanitize dump content
  (not reliable).
- **Consequences:** Makes the "dump content is code" risk visible and limits
  who can take it. No change for same-source restores with least-privilege
  roles.
- **Related:** tasks 2.7, 2.11, 4.2, 4.3, 4.5, 6.3, 6.7, 7.3;
  `docs/security/db-roles.md`; `SECURITY.md` §9.

## D-069: Audit and alert on break-glass CLI actions

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.2)
- **Context:** `bekup admin create|reset-password` (task 1.6) runs on the host
  and bypasses step-up. The plan alerts on new admins (task 5.5) but does not
  say CLI actions are audited, so a password reset via the CLI could take
  over an account silently (`SECURITY.md` OI-3).
- **Decision:** Every `bekup admin` action writes an audit entry with actor
  `cli` (plus OS user and hostname when available) and raises a
  `security.system` alert to all admins. Password resets revoke the
  account's sessions as for any password change (D-047).
- **Alternatives considered:** Treat the host operator as fully trusted and
  log nothing (rejected: no detection of misuse or of a stolen host
  account).
- **Consequences:** Break-glass use becomes visible. It is not a control
  against a root-on-host operator, who remains out of scope.
- **Related:** tasks 1.3, 1.6, 5.5; `SECURITY.md` §9.

## D-070: Per-job maximum artifact size

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.2)
- **Context:** A malicious or runaway target DB can stream unbounded data
  until the job timeout, filling destinations (cost, and denial of service
  for other jobs). The plan bounds buffers and the scratch volume, but not
  the total artifact size (`SECURITY.md` OI-4).
- **Decision:** Jobs get an optional **maximum artifact size**, enforced on
  the pipeline's byte counter; exceeding it aborts the run (`failed`), cleans
  up partial uploads and raises an alert. The run also warns when an
  artifact is far larger than the job's recent average.
- **Alternatives considered:** Rely on the job timeout only (bytes can still
  be large); a global cap (too coarse).
- **Consequences:** Limits storage exhaustion from a hostile target; admins
  must raise the cap when databases grow legitimately.
- **Related:** tasks 3.1, 3.5, 3.6, 5.3; `SECURITY.md` §9.

## D-071: justfile conventions

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.1b)
- **Context:** D-020 makes the `justfile` the single entry point, but leaves
  the recipe mechanics, argument handling and behaviour before later tasks
  land open.
- **Decision:** `set shell := ["bash", "-euo", "pipefail", "-c"]`; multi-line
  recipes use `[script]` with the same interpreter and source
  `scripts/lib.bash`. **`set positional-arguments`**: recipe arguments reach
  the shell as `"$1"` and are never interpolated into recipe source; names and
  paths are validated (e.g. migration names `^[a-z][a-z0-9_]{0,62}$`).
  `VERSION` is validated as plain `x.y.z` before use in `-ldflags`/image tags.
  Recipes whose tooling arrives later fail with `<thing>: not available yet
  (task X.Y)`; `just sec` **fails** when a scanner is missing (never a silent
  pass). Destructive/publishing recipes (`db-reset`, `release`) need a
  `[confirm]`. `just build` produces a static (`CGO_ENABLED=0`), `-trimpath`,
  `-s -w` binary in `bin/`. Integration tests use the build tag
  `integration`. Go generators are taken from `go tool` when pinned in
  `go.mod`, else from `PATH`. The web package manager follows the lockfile
  (pnpm for `pnpm-lock.yaml`), dependencies are installed only with a frozen
  lockfile, and the recipes call the `web/package.json` scripts `gen`, `lint`,
  `typecheck`, `test`, `build`, `test:e2e`. Proposed CLI shapes for later
  tasks: `bekup keys generate --out <file>` (default `secrets/master.key`,
  never overwritten), `bekup certs init --dir <dir>` (default `certs/`, 0700),
  `bekup migrate --dev`, `bekup docs cli --dir docs/reference/cli`; the
  GraphQL reference is the concatenation of `internal/graph/schema/*.graphqls`
  into `docs/reference/schema.graphql`.
- **Alternatives considered:** Interpolating `{{ arg }}` into recipes
  (rejected: shell injection via arguments); recipes that silently skip
  missing scanners (rejected: `just sec` could pass without scanning);
  a Makefile (rejected by D-020).
- **Consequences:** Argument handling is injection-safe; later tasks must
  either implement the proposed CLI shapes or update the recipes. Requires
  just ≥ 1.44 (`[script]`).
- **Related:** tasks 0.1b, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 1.2, 7.2; `justfile`,
  `scripts/lib.bash`, `scripts/help.sh`, `docs/development.md`.

## D-072: Version bump and release policy

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.1b)
- **Context:** The plan requires `just bump` (semver, git-cliff changelog,
  signed commit + tag, refusing on a dirty tree or outside main) but leaves
  the exact rules open.
- **Decision:** Versions are strict `MAJOR.MINOR.PATCH` (no `v` prefix,
  leading zeros, pre-release or build metadata; ≤ 9 digits per component); an
  explicit version must be **greater** than the current one. `bump` refuses
  when: not in a git repo, no commits, not on `main` (incl. detached HEAD),
  any tracked change **or untracked file**, the tag exists, git-cliff is
  missing. It updates `VERSION`, `web/package.json` `version` and regenerates
  the whole `CHANGELOG.md` with git-cliff (`cliff.toml`: Conventional Commits
  → Keep a Changelog sections; release, deps and housekeeping commits
  skipped), then `git commit -S -m "chore(release): vX.Y.Z"` and
  `git tag -s -a vX.Y.Z`. Any failure before the commit hard-resets the
  (previously clean) tree. `bump` never pushes. `just release` (confirmed)
  pushes `main` and the tag with `git push --atomic` only if `HEAD` of a clean
  `main` is the **annotated** tag `v$(cat VERSION)` and `git verify-tag`
  succeeds.
- **Alternatives considered:** Allowing pre-releases (deferred until needed);
  pushing from `bump` (rejected: no chance to review the signed commit);
  editing CHANGELOG.md by hand (rejected: drift).
- **Consequences:** Every release tag is signed and verified before
  publishing; the hand-written `[Unreleased]` section in CHANGELOG.md is
  replaced by generated content at the first bump.
- **Related:** tasks 0.1b, 7.4; `scripts/semver.sh`, `scripts/bump.sh`,
  `scripts/release.sh`, `cliff.toml`, `scripts/*_test.sh`.

## D-073: Configuration keys, defaults and guardrails (task 0.3)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.3)
- **Context:** The plan defines the config sections, search path and
  precedence (D-019, D-041) but not every key, bound or edge case.
- **Decision:**
  - Config files have exactly two levels (section, key); every key has a
    1:1 `BEKUP_<SECTION>_<KEY>` override (lists comma-separated, Go
    durations). `BEKUP_DEV=1` is an alias for `--dev`. The extra-images list
    of the runner is a file path (`images.extra_file`) so that every key stays
    scalar.
  - Inline secret keys exist only as dev-only escape hatches
    (`database.url`, `security.master_key`); they are rejected outside dev
    mode in file and env, and **never** accepted as CLI flags (argv), even in
    dev mode.
  - File safety: the check runs on the opened descriptor (`O_NOFOLLOW`,
    `O_NONBLOCK`, fstat); a symlink is followed only if the target's directory
    is owned by the user or root and is not group/world-writable (sticky
    directories allowed). Config files are capped at 1 MiB, secret files at
    64 KiB. Group-readable secret files (0440/0640) are accepted; only
    world-readable ones are refused. An existing but unsafe candidate is an
    error (no fallback to the next location).
  - Security bounds: argon2id memory/iterations and `password_min_length` can
    only be raised above the plan's values; `stepup_max_age` <= 5m;
    `setup_token_ttl` <= 1h; `/0` CIDRs are rejected in every list
    (trusted_proxies, netguard and runner targets); runner `allow_cidrs`
    inside the always-denied ranges (loopback, link-local/metadata,
    unspecified) are rejected; `docker.host` must be a `unix://` socket;
    client SANs must not contain wildcards; `storage.disk_root` must not be
    `/`; `base_url` must not carry credentials, query or fragment.
  - Defaults: `server.listen = ":8080"` (internal network only), metrics
    enabled on `127.0.0.1:9464`, secret paths under `/run/secrets/`, empty
    target and netguard allowlists (deny all).
- **Alternatives considered:** Allowing `tcp://` Docker hosts (rejected for
  now: a plain TCP daemon socket is root-equivalent and unauthenticated);
  allowing weaker argon2 parameters for small hosts (rejected); accepting
  inline secrets via flags in dev mode (rejected: argv is visible to every
  local user).
- **Consequences:** Misconfigurations that weaken a control fail at startup.
  Operators who use a docker-socket-proxy over TCP need a new decision.
  Kubernetes secret volumes need `defaultMode: 0400`.
- **Related:** task 0.3; `internal/config`; `docs/configuration.md`;
  D-019, D-041.

## D-074: config print is always redacted

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.3)
- **Context:** The plan specifies `bekup config print --redacted` but not
  whether an unredacted mode exists.
- **Decision:** `config print` always redacts inline secrets and URL
  credentials; `--redacted` defaults to true and `--redacted=false` is
  refused. Secret *files* are never read by `config print` or
  `config validate`.
- **Alternatives considered:** An unredacted mode behind a flag (rejected:
  output ends up in terminals, tickets and CI logs).
- **Consequences:** Debugging a secret value requires reading the secret file
  directly, with its own permissions.
- **Related:** task 0.3; `cmd/bekup/config_cmd.go`.

## D-075: internal/logging redaction rules

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.3)
- **Context:** D-060 requires a slog redaction handler and scrubbing of known
  secret values but does not define the rules.
- **Decision:** A new package `internal/logging` wraps every slog handler.
  Attribute keys are split into words (separators and camelCase); a key is
  secret when a word is or contains password, passwd, pass, passphrase,
  secret, token, key, apikey, private, authorization, cookie, dsn,
  credential(s), otp/totp, or the key names a connection string
  (database_url, db_url, connection_string); metadata suffixes (`_file`,
  `_path`, `_id`, `_ttl`, `_length`, ...) are exempt so that, for example,
  `master_key_file` stays visible. Values of secret keys become
  `[REDACTED]` (inside groups too). All string, error, `[]byte` and other
  values have URL userinfo passwords and `password=`/`secret=`/`token=`
  key-value pairs removed, and every value registered in a `Registry`
  (minimum 6 bytes, stored as its own byte copy, removed and zeroed on
  unregister) is scrubbed from messages and attributes. Attributes added with
  `With` are redacted at Handle time so later registrations apply. JSON by
  default, text in dev mode.
- **Alternatives considered:** An allowlist of loggable keys (rejected: too
  brittle at this stage); redaction only at the call site (rejected: easy to
  forget).
- **Consequences:** Some harmless keys (for example `cache_key`) are
  over-redacted; that is the safe direction. Secrets shorter than 6 bytes are
  not scrubbed by value.
- **Related:** task 0.3; `internal/logging`; D-060.

## D-076: Container image build

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** The plan fixes "distroless nonroot, read-only rootfs, one image"
  but not the build details.
- **Decision:** Multi-stage BuildKit `Dockerfile`: `node:22-alpine` web stage
  (pnpm via corepack at the `packageManager` version, `--frozen-lockfile`;
  `WEB_BUILD=auto|skip`, placeholder `index.html` when `web/` is missing or
  skipped) → `golang:1.27-alpine` build stage (`CGO_ENABLED=0`, `-trimpath`,
  `-buildvcs=false`, `-s -w -buildid=`, `GOTOOLCHAIN=local`,
  `GOFLAGS=-mod=readonly`, `go mod verify`, cross-compiles via
  `$BUILDPLATFORM`) → `gcr.io/distroless/static-debian12:nonroot`. The version
  comes from `--build-arg VERSION` or `VERSION` and must match strict SemVer
  before it reaches `-ldflags`. The binary is root-owned `0555` at
  `/usr/local/bin/bekup`; `USER 65532:65532`; no `CMD` (the subcommand is
  always explicit); OCI labels. All base images pinned by tag + multi-arch
  digest. A separate `dev` target (Go toolchain + air, UID 65532) exists only
  for the dev override. `.dockerignore` keeps secrets, keys, local configs,
  `.git` and build outputs out of the context.
- **Alternatives considered:** `scratch` (rejected: no CA bundle/tzdata/passwd
  without extra work); a default `CMD ["serve"]` (rejected: the wrong component
  could start silently); failing the build without `web/` (rejected: blocks
  Go-only work).
- **Consequences:** Reproducible, minimal, shell-less image; a compromised
  process cannot replace the binary. `just docker` passes `VERSION`.
- **Related:** tasks 0.7, 7.4; `Dockerfile`, `.dockerignore`; D-025, D-061.

## D-077: Compose network segmentation and interface binding

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** The plan names the `frontend`, `backend`, `runner-ctl` and
  `targets` networks, but `serve` must also reach destinations, SMTP and
  webhooks, and the runner's gRPC port must not be reachable from per-run
  networks or targets.
- **Decision:** Five networks: `frontend`, `backend`, `runner-ctl`
  (all `internal: true`), `targets` (runner only) and an additional
  **`egress`** network (serve only) for destinations/SMTP/webhooks, guarded by
  the SSRF guard. `frontend` (10.213.250.0/24) and `runner-ctl`
  (10.213.251.0/24) get fixed subnets outside Docker's default pools, and
  `serve` (10.213.250.10) and `runner` (10.213.251.10) fixed addresses, so the
  operator can bind `[server] listen` and `[listen] address` to those
  interfaces only and name the proxy subnet in `trusted_proxies`. Overridable
  via compose variables (`FRONTEND_SUBNET`, `SERVE_FRONTEND_IP`,
  `RUNNER_CTL_SUBNET`, `RUNNER_CTL_IP`). No bekup port is published.
- **Alternatives considered:** Putting `serve` on `targets` for egress
  (rejected: would let serve reach target DBs); subnets inside 172.16/12
  (rejected: collided with Docker's automatic allocations in testing);
  listening on all interfaces (rejected: dump containers on per-run networks
  could reach the runner's gRPC port).
- **Consequences:** Each component reaches only what it needs. Operators whose
  LAN uses 10.213.250-251.0/24 must override the variables and TOML values.
- **Related:** tasks 0.7, 0.7b, 1.1, 2.4, 2.6; `docker-compose.yml`; D-003,
  D-017, D-029, D-048, D-055.

## D-078: Separate migrate service; serve never holds owner credentials

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** The plan applies migrations as `bekup_owner` via `bekup
  migrate` "and automatically at startup". Auto-migrating inside `serve`
  would give the long-running, internet-facing process the owner credentials.
- **Decision:** In the shipped compose stack, a one-shot `migrate` service
  (`bekup migrate up`, `BEKUP_DATABASE_URL_FILE` pointing at
  `bekup_database_owner_url`) runs before `serve`
  (`service_completed_successfully`). Only `migrate` receives the owner URL;
  `serve` gets the `bekup_app` URL only, so its startup auto-migration must
  degrade to "verify schema version, refuse to start if behind" when it lacks
  owner rights (to be implemented in task 0.4). The app Postgres runs as UID
  70 with a read-only rootfs, `cap_drop: ALL`, SCRAM-SHA-256 for local and TCP
  logins and data checksums; its init script creates `bekup_owner` (database
  owner) and `bekup_app` (CONNECT only, connection limit), revokes PUBLIC
  access to the database and the public schema, and reads passwords via psql
  `\set` backticks from secret files.
- **Alternatives considered:** Auto-migration in `serve` with owner
  credentials (rejected: violates least privilege, D-054).
- **Consequences:** A compromised `serve` cannot alter the schema or the
  audit-log protections. **Open question:** TLS between `serve`/`migrate` and
  the app Postgres on the internal `backend` network (not covered by the plan;
  currently plain on an internal network).
- **Related:** tasks 0.4, 0.7; `docker-compose.yml`,
  `deploy/postgres/init/10-bekup-roles.sh`; D-042, D-054.

## D-079: mTLS certificate distribution in compose

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** The CA key must never be on a shared volume and each component
  gets only its own key + the CA certificate (plan, Runner).
- **Decision:** `certs-init` is a one-shot service in the `setup` profile
  (`docker compose --profile setup run --rm certs-init`) with no network,
  writing into `./secrets/certs/{ca,bekup-serve,bekup-runner}/`. `serve` and
  `runner` receive their own `tls.{crt,key}` plus `ca/ca.crt` as file-based
  Docker secrets under the names the config defaults expect
  (`bekup_client.*`, `runner.*`, `bekup_ca.crt`). `ca.key` is never mounted
  into a long-running container. The `certs init` flags in compose are
  provisional until task 0.6.
- **Alternatives considered:** Named volumes per component (rejected: they
  don't match the `/run/secrets/*` defaults and blur the "Docker secret"
  boundary); running `certs-init` on every `up` (rejected: compose needs the
  secret files before it creates the containers).
- **Consequences:** Certificate generation is an explicit setup step; on Linux
  the files must be chowned to UID 65532.
- **Related:** tasks 0.6, 0.7; `docker-compose.yml`; D-030.

## D-080: `bekup healthcheck` subcommand for distroless healthchecks

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** Distroless has no shell or curl, so compose healthchecks must
  exec the bekup binary; the plan defines `/healthz`, `/readyz` and the runner
  `Health` RPC but no CLI.
- **Decision:** Add `bekup healthcheck serve` (GET `/readyz` on the configured
  listen address, short timeout, exit 0/1) and `bekup healthcheck runner`
  (local check of the gRPC listener without needing a client certificate).
  Compose already uses them in exec form. Until implemented, the healthchecks
  report unhealthy; nothing depends on `service_healthy` of serve/runner yet.
- **Alternatives considered:** Shipping curl/wget (rejected: adds a shell-less
  but larger attack surface); no healthchecks (rejected).
- **Consequences:** Needs implementation in task 1.1 (serve) and 2.4
  (runner).
- **Related:** tasks 0.7, 1.1, 2.4; `docker-compose.yml`.

## D-081: Development compose stack

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals); partly
  superseded by D-114 (the full stack is no longer the default `just dev`;
  it is the `full` profile)
- **Decided by:** agent with user approval (task 0.7)
- **Context:** The dev override needs sample targets, destinations, an SSH
  bastion and hot reload without weakening the production baseline.
- **Decision:** The bekup services keep the full hardening in dev; `serve`
  runs air from the `dev` target as UID 65532 with the source mounted
  **read-only** and build output in a volume. All host ports bind to
  `127.0.0.1`. Sample targets (PG 15, MySQL 8.4, MariaDB 11.4) have no host
  ports; `pg15-tunneled` sits only on an internal `tunnel-only` network with
  the bastion. Dev secrets come from `deploy/dev/gen-dev-keys.sh` (`umask 077`,
  0600/0700, never printed, `--chown` for Linux); the name avoids "secret"
  because the user's tool permissions deny edits to such paths. MinIO uses
  `cgr.dev/chainguard/minio` (pinned by digest) because the upstream images are
  no longer published on Docker Hub/quay.io; bucket setup uses the
  `minio-client:latest-dev` image with credentials only in the short-lived
  process. Dev targets run without TLS until task 2.7a adds TLS-enabled
  targets.
- **Alternatives considered:** Running the dev serve as root with a writable
  bind mount (rejected); Bitnami/other MinIO images (rejected: no longer
  maintained or not source-built).
- **Consequences:** Dev mirrors production isolation; Chainguard only offers
  `:latest` for free, so Renovate tracks the digest.
- **Related:** task 0.7; `docker-compose.dev.yml`, `deploy/dev/`.

## D-082: CSP for the web UI: style-src 'self' without nonce

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.8)
- **Context:** D-050 requires a strict CSP with `script-src 'self'` and leaves
  `style-src` to task 0.8. sonner injects a `<style>` at import time,
  react-remove-scroll (used by Radix Dialog/Menu/Select) injects `<style>`
  elements through react-style-singleton, and Radix Select/ScrollArea render
  `<style dangerouslySetInnerHTML>`. All are blocked by `style-src 'self'`
  (Radix Select also throws under Trusted Types). `style=` set through React
  props uses the CSSOM and is not restricted. zod v4 probes `new Function()`.
- **Decision:** The UI is served with
  `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; worker-src 'none'; manifest-src 'self'; require-trusted-types-for 'script'; trusted-types 'none'`.
  No `'unsafe-inline'`, no nonce, no hashes, so `index.html` stays static.
  To get there: a build-time Vite plugin (`web/vite-plugins/csp-strict-styles.ts`)
  removes the sonner and Radix `<style>` injections and **fails the build** if a
  dependency update changes them; `react-style-singleton` is aliased to a no-op
  shim; the equivalent CSS ships statically (`web/src/styles/csp-compat.css`,
  `sonner/dist/styles.css`, `scrollbar-gutter: stable` instead of the scrollbar
  gap fix); zod runs with `jitless: true`; the theme bootstrap is an external
  `/theme-init.js`; Vite never inlines assets (`assetsInlineLimit: 0`); fonts are
  self-hosted (fontsource). `pnpm csp-check` builds with `/design` enabled, serves
  it with this CSP, drives headless Chrome through every page and overlay in
  light and dark mode, asserts zero violations and that the body scroll lock
  works, and includes a positive control. Without the compatibility layer the
  same check reports sonner, react-remove-scroll and Radix Select violations.
  ESLint bans `dangerouslySetInnerHTML` and `innerHTML`.
- **Alternatives considered:** `style-src 'self' 'unsafe-inline'` (rejected:
  lets injected CSS exfiltrate data); a per-request nonce plus `setNonce`
  (rejected: needs HTML templating on every request, and sonner ignores it);
  hashes (rejected: brittle, and they don't cover dynamic scroll-lock CSS).
- **Consequences:** The strictest practical CSP, including Trusted Types.
  Dependency upgrades that change style injection fail the build until someone
  reviews them. `connect-src 'self'` covers same-origin `ws:`/`wss:` in current
  browsers (CSP3). If a later page needs `data:` images (for example a TOTP QR
  code), render it as inline SVG instead. Task 1.1 must send exactly this header,
  and `csp-check` should run in CI (task 0.9).
- **Related:** D-050, D-026; tasks 0.8, 0.9, 1.1; `web/scripts/csp-check.mjs`.

## D-083: Visual direction and design system (Vault Ledger)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.8, frontend-design skill)
- **Context:** D-021 asks for a distinctive, accessible design system with dark
  mode, to be used by every M6 page.
- **Decision:** "Vault Ledger": ink on warm vellum in light mode, paper-white on
  vault slate in dark mode, with one accent (ledger ink blue) for interaction and
  "running". The other hues are reserved for run status (success, partial, failed,
  running, missed, queued/neutral) and the hazard-orange "insecure" state.
  Typography: Instrument Sans (UI), Instrument Serif (display titles),
  JetBrains Mono (hashes, sizes, timestamps, mono eyebrow labels), all
  self-hosted. Status is always shape + color + text. A diagonal hazard hatch is
  reserved for weakened-security badges (`unencrypted`, `insecure transport`).
  Tokens are OKLCH CSS variables (`web/src/styles/theme.css`). A vitest gate
  checks WCAG AA contrast for both themes (text 4.5:1, UI marks 3:1). The theme
  is light, dark or system, stored in localStorage and applied before first paint.
  The dev-only `/design` route is the living style guide.
- **Alternatives considered:** The stock shadcn neutral theme (rejected: generic,
  and no status or security vocabulary).
- **Consequences:** M6 pages build on these tokens and components (`StatusBadge`,
  `StatusDot`, `SecurityBadge`, `PageHeader`, app shell). Any token change must
  keep the contrast test green.
- **Related:** D-021, D-001; tasks 0.8, 6.1–6.10; `web/src/styles/`, `web/src/components/`.

## D-084: Web toolchain pins and pnpm supply-chain settings

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.8)
- **Context:** D-026 names the web stack. Exact versions and package-manager
  policy were left open, and D-061/D-065 ask for pinning and a release-age delay.
- **Decision:** pnpm 11.1.2 is pinned through `packageManager`. Every dependency
  has an exact version. pnpm 11 reads its settings from `web/pnpm-workspace.yaml`:
  `saveExact: true`, `minimumReleaseAge: 4320` (3 days, matching D-065) and
  `strictDepBuilds: true` (no install scripts unless allowed). The latest
  compatible versions are used: React 19.3, Vite 8, Tailwind 4.3, Apollo Client
  4, React Router 8 (data mode), zod 4 and vitest 5. **TypeScript 6.0** is used
  instead of 7.0, because typescript-eslint supports `<6.1`. ESLint 10 is used,
  with a peer-range override for eslint-plugin-jsx-a11y, which works but has not
  declared ESLint 10 support yet. TypeScript is strict, plus
  `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes` and
  `noPropertyAccessFromIndexSignature`. ESLint uses typescript-eslint
  strict-type-checked, react-hooks and jsx-a11y strict. Until task 1.2 adds the
  server schema, graphql-codegen falls back to `web/graphql/placeholder.graphqls`.
  Generated files get `// @ts-nocheck`. `web/dist/.gitkeep` keeps `go:embed`
  compiling, and the build recreates it.
- **Alternatives considered:** TypeScript 7 (rejected for now: no lint
  support); ESLint 9 (rejected: end of life).
- **Consequences:** Move to TypeScript 7 once typescript-eslint supports it.
  Drop the jsx-a11y peer override when the plugin declares ESLint 10 support.
  Delete the placeholder schema in task 1.2.
- **Related:** D-026, D-061, D-065; tasks 0.8, 1.2; `web/package.json`, `web/pnpm-workspace.yaml`.

## D-085: Master keyring file format and key IDs

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.5)
- **Context:** D-046 requires a keyring with key IDs and `bekup keys
  generate|rotate`, but no file format, ID scheme or write semantics.
- **Decision:** The keyring (`master_key_file`) is a strict text format,
  version 1: a mandatory header line `bekup-keyring v1`, one line
  `key <id> <created RFC 3339> <base64 of 32 bytes>` per key and exactly one
  `primary <id>` line; `#` comments and blank lines are ignored. Key IDs are 4
  random bytes as 8 lowercase hex characters (unique within the keyring). The
  primary key encrypts; all keys decrypt. Parsing rejects unknown directives,
  duplicate IDs or key material, non-canonical base64, more than 256 keys
  and lines over 4 KiB; errors give line numbers but never echo line content.
  The file is read only through `config.ReadSecretFile` (permission and
  ownership checks). Writes are atomic: temp file 0600 in the same directory,
  fsync, then a hard link (`keys generate`, which therefore never overwrites,
  even a dangling symlink) or a rename (`keys rotate`, which resolves a
  symlinked path first), then a directory fsync. `keys rotate` only adds a
  new primary key; removing old keys waits for DB re-encryption (later task).
  In dev mode the inline `master_key` holds the same format.
- **Alternatives considered:** A raw 32-byte/base64 key file (rejected: no
  rotation); TOML (rejected: a larger parser surface for a secret file, and
  inline TOML secrets are already forbidden); per-key files in a directory
  (rejected: harder to escrow as one unit).
- **Consequences:** One file to escrow, re-escrow required after every
  rotation. 32-bit IDs only need to be unique within one keyring.
- **Related:** task 0.5; `internal/crypto/keyring.go`, `keyfile.go`,
  `cmd/bekup/keys_cmd.go`; `docs/security/key-management.md`; D-016, D-046.

## D-086: Credential ciphertext format, subkeys and key-check value

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.5)
- **Context:** D-046 fixes AES-256-GCM, a key ID in the ciphertext, AAD
  `table:row_id:field` and a startup key-check value, but not the byte
  format, whether per-record data keys are used, or how the KCV is computed.
- **Decision:** Ciphertext v1 = `0x01 | key id (4) | random 96-bit nonce |
  GCM ciphertext+tag`. The GCM additional data is the 5-byte header followed
  by `table:row_id:field`; table and field must match
  `^[a-z][a-z0-9_]{0,62}$` and the row ID `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
  so the encoding is unambiguous. Master keys are never used directly: HKDF-
  SHA256 derives one subkey for AES-256-GCM and one for the KCV (distinct
  info strings). **No per-record DEKs**: credentials are tiny, rotation
  re-encrypts the record either way, and a wrapped DEK adds a second AEAD
  layer and format surface without a security gain at this volume (random
  nonces stay far below the 2^32-messages-per-key bound). The KCV is
  `kcv1:<key id>:<base64url(HMAC-SHA256(kcv subkey, "bekup key-check value
  v1"))>`, compared in constant time; it is stored in `settings` and checked
  at startup (DB wiring later). All authentication failures return one
  generic error. Plaintexts are capped at 1 MiB.
- **Alternatives considered:** Per-record DEKs wrapped by the master key
  (rejected, see above; can be added as ciphertext v2 if bulk data is ever
  stored); KCV = encryption of a constant (rejected: HMAC under a separate
  subkey reveals nothing about the encryption key).
- **Consequences:** Ciphertexts cannot be moved between rows/fields or have
  their header changed; a wrong or truncated keyring is detected before any
  decryption.
- **Related:** task 0.5; `internal/crypto/aead.go`, `kcv.go`; D-046.

## D-087: Instance signing key IDs and domain-separated signatures

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.5)
- **Context:** D-038 requires ed25519 instance keys with key IDs signing
  manifests (and audit chain heads, task 1.3), with old public keys kept.
- **Decision:** The key ID is the first 8 bytes of SHA-256(public key) as
  16 hex characters, so an ID can never be paired with another public key.
  The 32-byte seed is stored sealed with the keyring under AAD
  `instance_keys:<id>:private`; unsealing re-derives the public key and
  checks it against the stored one. Signatures cover
  `"bekup-sig-v1" 0x00 <purpose> 0x00 <message>`, so a manifest signature can
  never be accepted as an audit chain-head signature or vice versa.
  Fingerprints are SHA-256 of the raw public key, shown as hex and as
  `SHA256:<base64>`.
- **Alternatives considered:** Random key IDs (rejected: an ID/key mismatch
  becomes possible); Ed25519ctx contexts (rejected: poor support in other
  tools needed for independent offline verification); unprefixed signatures
  (rejected: cross-protocol confusion).
- **Consequences:** Third-party verifiers must prepend the documented prefix.
- **Related:** task 0.5, 1.3, 3.1, 4.7; `internal/crypto/signing.go`; D-038.

## D-088: Passphrase generator and age work-factor bounds

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.5)
- **Context:** D-016 asks for strong generated job passphrases; the plan
  requires a bounded `SetMaxWorkFactor` for age scrypt decryption.
- **Decision:** Generated passphrases are 10 words (minimum 10) drawn
  uniformly with `crypto/rand` from the embedded EFF large wordlist (7776
  words, CC BY 3.0 US, attributed in `NOTICE`), joined by single spaces (no
  word contains a space, so entropy is exactly 10 x log2(7776) = 129.2 bits).
  age passphrase encryption writes scrypt work factor 18 (age's default) and
  accepts at most 20; decryption defaults to a maximum of 18 and can never be
  raised above 20, so a crafted header cannot force more than 1 GiB of scrypt
  work. Identity-based decryption accepts only X25519 identities (no scrypt
  or plugin identities).
- **Alternatives considered:** Random base32 (rejected: harder to read out
  and type during offline recovery); hyphen separators (rejected: four EFF
  words contain hyphens, making the split ambiguous).
- **Consequences:** Passphrases are long (about 70 characters) but easy to
  transcribe. Artifacts written with work factor 19-20 need an explicit
  higher bound to decrypt.
- **Related:** task 0.5, 3.1, 3.2, 4.7; `internal/crypto/generate.go`,
  `age.go`; D-008, D-016.

## D-089: Runner mTLS identity scheme and PKI profile

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.6)
- **Context:** D-030 requires a private CA, per-component ed25519 certs and
  client identification "by SAN", but does not fix the SAN form, the
  certificate profile or the file layout.
- **Decision:** Clients (`serve`, `worker`) are identified by exactly one
  **URI SAN `spiffe://bekup/<component>`** and carry no DNS/IP/email SANs and
  only the `clientAuth` EKU. The runner's certificate carries DNS SANs
  (`runner`, the compose service name, plus optional `--runner-san` names or
  IPs) and only the `serverAuth` EKU. The runner accepts a client only when
  its verified leaf has exactly one such URI that is in
  `tls.allowed_client_sans` (defaults `spiffe://bekup/serve`,
  `spiffe://bekup/worker`; config validation accepts only this form);
  `runner.server_name` defaults to `runner`. The CA is ed25519, path length
  0, **name-constrained** (critical) to the runner's DNS names, the `bekup`
  URI domain and either the listed IPs or no IPs at all; CN carries a random
  suffix so CAs are distinguishable during rotation. Validity: CA 5 years
  (max 10), leaves 1 year (max 825 days), never beyond the CA. Serials are
  128-bit random. Both sides use TLS 1.3 only, trust only the private CA
  bundle (never system roots), the server disables session tickets, and key
  files are read with `config.ReadSecretFile`, certificates/bundles with the
  new `config.ReadPublicFile` (D-041 ownership/mode checks). Layout:
  `<dir>/ca/ca.{crt,key}` and `<dir>/<component>/{<component>.crt,
  <component>.key, ca.crt}`; component directories never contain a CA key.
  Files are written atomically; keys 0600, certificates 0644, directories
  0700; `init` never overwrites without `--force`.
- **Alternatives considered:** DNS SANs such as `bekup-serve` for clients
  (rejected: conflates client identity with host names, and a DNS-named
  client cert is closer to being usable as a server cert); CN-based identity
  (rejected: deprecated, unconstrained); no name constraints (rejected: a
  leaked CA key could mint certificates for arbitrary names).
- **Consequences:** Adding a runner name outside the CA's constraints needs
  a CA rotation (`rotate --ca stage --runner-san ...`). The previous defaults
  `bekup-serve`/`bekup-worker`/`bekup-runner` are replaced; existing
  `runner.toml` files with the old SANs fail validation with a clear message.
- **Related:** task 0.6, 2.4, 5.5/5.6; `internal/certs`,
  `cmd/bekup/certs_cmd.go`, `internal/config` (runner/bekup defaults,
  validation, `ReadPublicFile`); D-030, D-041;
  `docs/security/mtls-certs.md`.

## D-090: CA key protection and CA rotation procedure

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.6)
- **Context:** The CA key must never reach a shared volume (D-030), but it
  is written to disk by `bekup certs init` and needed again for rotation.
  Encrypted PKCS#8 for ed25519 is poorly supported, and the plan leaves the
  CA rotation overlap procedure open.
- **Decision:** The CA key (PKCS#8 PEM) is **sealed with an age scrypt
  passphrase** (work factor 18, decryption bounded at 18 per D-088) and
  ASCII-armored in `ca/ca.key`. Encryption is the default: the passphrase
  comes from `--ca-passphrase-file` (read with `ReadSecretFile`) or a
  no-echo terminal prompt entered twice; it must be at least 16 characters.
  Without a terminal or file the command fails; an unencrypted key requires
  the explicit `--unencrypted-ca-key` flag and prints a warning. Never via
  argv or environment. `init` always prints a warning to move the CA key
  offline. CA rotation has three phases, each followed by restarting all
  components: `rotate --ca stage` (new CA in `ca/ca.next.*`, every bundle
  trusts old + new), `rotate --ca promote` (new CA becomes current, old one
  kept as `ca/ca.prev.crt`, all leaves reissued from the new CA, bundles
  trust new + old) and `rotate --ca finish` (refuses while any leaf is not
  issued by the current CA; bundles trust only the new CA). Leaf rotation is
  refused while a CA rotation is staged.
- **Alternatives considered:** Unencrypted CA key with guidance only
  (rejected: a copied file is immediately usable); encrypted PKCS#8 (rejected:
  weak/unsupported tooling for ed25519); a single-step CA swap (rejected:
  components restarted at different times would fail to connect).
- **Consequences:** Operators must keep the passphrase (password manager /
  escrow alongside the master key). Non-interactive setups (compose
  `certs-init` without a TTY) need a passphrase file, which must itself be
  removed afterwards. age is already a dependency (D-008).
- **Related:** task 0.6; `internal/certs/cakey.go`, `store.go`;
  `cmd/bekup/certs_cmd.go`; D-030, D-088; `docs/security/mtls-certs.md`.

## D-091: CI workflow hardening and job layout

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.9)
- **Context:** D-027 and D-061 require GitHub Actions CI with SHA-pinned
  actions, but leave the job layout, token permissions, runner hardening and
  tool pinning open.
- **Decision:** One workflow, `.github/workflows/ci.yml`, triggered by
  `pull_request`, `push` to `main` and `merge_group`. It has no
  `pull_request_target` and uses no secrets. The token is read-only
  (`permissions: contents: read`), and every checkout sets
  `persist-credentials: false`. Every action is pinned by full commit SHA with
  a `# vX.Y.Z` comment. Tool versions are literal pins: in action inputs
  (golangci-lint v2.14.0 to match local, just, zizmor), or in the workflow
  `env` for `go install`/`go run` tools (govulncheck, osv-scanner, actionlint),
  which the Go checksum database verifies. `step-security/harden-runner` is
  the first step of every job, in egress `audit` mode. The jobs are `go`,
  `go-integration`, `web` (including `csp-check` in the runner's Google
  Chrome), `security` (`just sec`), `generated` (`go mod tidy -diff` +
  `just gen` + a clean-tree check), `scripts` (just fmt, shellcheck, script
  tests), `compose` (`docker compose config` over all profiles) and
  `workflows` (actionlint + zizmor). zizmor runs without SARIF upload, so the
  token never needs `security-events: write`. Superseded PR runs are
  cancelled. Runs on `main` are not. No Dependabot: Renovate already pins and
  updates the github-actions manager (D-065).
- **Alternatives considered:** `golang/govulncheck-action` and the
  osv-scanner reusable workflow (rejected: extra transitive actions, and the
  reusable workflow uploads SARIF, which needs `security-events: write`);
  harden-runner in `block` mode (deferred: the egress allowlist is not known
  yet, and a wrong guess breaks CI); zizmor with SARIF upload to code scanning
  (rejected for now: needs a write-scoped token).
- **Consequences:** CI behaves like the local `just` recipes. A compromised
  action tag cannot change what runs, and a compromised step gets only a
  read-only token. Follow-ups: switch harden-runner to `block` with an
  allowlist once audit logs show the endpoints. Add a Renovate regex manager
  for the versions in the workflow `env`, because Renovate does not update
  them today. Branch protection (required checks, merge queue) must be set up
  in the GitHub settings.
- **Related:** D-027, D-061, D-065; tasks 0.9, 7.4; `.github/workflows/ci.yml`,
  `.github/pull_request_template.md`, `docs/development.md`.

## D-092: App DB framework details: roles checks, audit chain format, owner URL (task 0.4)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.4)
- **Context:** D-042 and D-054 fix sqlc + goose, the two roles and a
  trigger-computed audit hash chain, but not the details below.
- **Decision:**
  - **Owner credentials:** new config keys `database.owner_url_file`
    (default `/run/secrets/bekup_database_owner_url`) and the dev-only inline
    `database.owner_url`; only `bekup migrate` reads them. New key
    `database.statement_timeout` (default 30s, 1s to 1h). App connections
    also set `idle_in_transaction_session_timeout = 60s` and
    `default_transaction_isolation = read committed`, and
    `application_name`.
  - **Migration lock:** a session advisory lock (`'bekupMIG'`) on a
    dedicated, unpooled connection held only by the migrator; goose runs on a
    separate single connection. The migrator refuses to run unless connected
    as `bekup_owner` without SUPERUSER/BYPASSRLS. `migrate down-one` only in
    dev mode.
  - **Migration 00001 preconditions:** PostgreSQL >= 15, UTF8, current user
    `bekup_owner` owning the database, `bekup_app` exists, neither role is
    SUPERUSER/BYPASSRLS/CREATEROLE/REPLICATION, app not a member of owner.
    It revokes database and `public` schema access from PUBLIC
    (`bekup_app`: CONNECT + USAGE only, no TEMPORARY), stops granting
    function EXECUTE to PUBLIC, and sets default privileges (DML, sequences,
    EXECUTE) for `bekup_app`; never TRUNCATE/REFERENCES/TRIGGER.
  - **River:** vendored River v0.49.0 migrations 002-008 as two goose files
    (002-004, 005-008, split because of `ALTER TYPE ... ADD VALUE`); River's
    001 (`river_migration`) is skipped.
  - **Audit chain:** `id` has no column default; the trigger assigns it from
    the sequence **after** taking the chain lock, so id order equals chain
    order. The trigger also overrides `ts` with `clock_timestamp()` (no
    backdating), refuses non-READ-COMMITTED transactions, uses 32 zero bytes
    as the genesis `prev_hash`, and `prev_hash` and `hash` are UNIQUE (a
    fork is impossible). Canonical serialization v1 is length-prefixed with
    a NULL marker and a version domain string (docs/security/audit.md);
    `details` is hashed as Postgres' jsonb text output and `ip` as
    `host(ip)` so Go never has to re-implement Postgres formatting.
    Statement-level triggers raise on UPDATE/DELETE/TRUNCATE for every role.
    `actor_user_id` has no foreign key. `ip` must be a host address;
    `details` is capped at 64 KiB.
  - **Core tables:** `users.password_hash` is nullable (no password set
    yet = no password login); length/format CHECKs on free-text columns;
    `sessions.token_hash` must be 32 bytes; `settings.key` must match
    `^[a-z][a-z0-9_.]{0,127}$`; `updated_at` maintained by triggers.
  - **schema.sql** is produced by `pg_dump --schema-only --no-comments` in
    the test container (version header and `\restrict` lines stripped) and
    checked by an integration test; `just db-schema` regenerates it.
- **Alternatives considered:** goose's built-in session locker (rejected:
  session lock on a pooled `*sql.DB` connection); a `bigserial` default on
  `audit_log.id` (rejected: ids allocated before the lock would not follow
  chain order); hashing JSON re-encoded in Go (rejected: fragile);
  allowing migrations as a superuser (rejected: the SECURITY DEFINER trigger
  would run as superuser).
- **Consequences:** Audit inserts are serialized database-wide and must run
  in READ COMMITTED. Deployments need a PostgreSQL >= 15 database owned by
  `bekup_owner` and an owner URL secret for the migrate step. `serve` cannot
  read `goose_db_version` as `bekup_app`; the startup schema-version check
  of D-078 needs a dedicated mechanism in task 1.1. Changing the canonical
  serialization requires a new version string.
- **Related:** task 0.4; D-042, D-054, D-073, D-078; `internal/db`,
  `sqlc.yaml`, `docs/security/db-roles.md`, `docs/security/audit.md`,
  `docs/development.md`.

## D-093: Reverse-proxy profiles: socket-free Traefik, fixed proxy address, local dev TLS

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7b)
- **Context:** D-017/D-048 ask for `traefik` and `nginx` compose profiles
  ("Let's Encrypt via labels") but leave the details open: how the proxy is
  configured, which address `trusted_proxies` names, how ports are
  published from an `internal` network, and how dev gets TLS. Traefik's
  label-based configuration needs the Docker provider, and with it the Docker
  socket (root-equivalent).
- **Decision:** Traefik v3 (pinned tag + digest) is configured only through
  its **file provider** with the static service URL `http://serve:8080`.
  There is no Docker provider and no socket, so "labels" become a static
  dynamic-configuration file. Traefik doesn't expand environment variables
  in its static file, so a small entrypoint script validates the proxy-only
  variables (`BEKUP_DOMAIN`, `ACME_EMAIL`, `ACME_CA_SERVER`,
  `PROXY_TLS_MODE`; strict regexes, never bekup config keys) and renders
  templates from `deploy/traefik/` into a tmpfs. Let's Encrypt uses
  **HTTP-01** on port 80, which also carries the permanent redirect.
  TLS-ALPN-01 was not chosen because of its interaction with `sniStrict`.
  Both profiles:
  - share the TLS baseline: TLS 1.2/1.3; ECDHE+AEAD only (no CBC, no DHE);
    X25519MLKEM768 first; handshakes with unknown SNI rejected; HSTS 2y +
    includeSubDomains + preload; nosniff and X-Frame-Options DENY, with the
    upstream copies replaced. The CSP stays bekup's own (D-082).
  - share the limits: 2 MiB request bodies (Traefik: buffering middleware
    only on POST/PUT/PATCH/DELETE routes, because it also buffers
    responses); light per-IP rate (50/s, burst 100) and concurrency (100)
    limits; WebSocket passthrough; streamed, unbuffered responses with no
    total write timeout for downloads.
  - overwrite forwarded headers: Traefik trusts no incoming `X-Forwarded-*`,
    `aliasHeadersStrategy: delete`, `Forwarded` dropped, and encoded
    special characters in paths rejected.
  - run as UID 65532 on unprivileged ports 8080/8443 (host 80/443), with a
    read-only root, `cap_drop: ALL`, no-new-privileges and resource limits.
    A one-shot `*-init` container (only CHOWN/FOWNER, no network) hands the
    certificate volumes to UID 65532; `acme.json` is 0600 in a 0700 dir.
  - attach to `frontend` with the **fixed address `10.213.250.5`**
    (`PROXY_FRONTEND_IP`). `trusted_proxies` examples name exactly
    `10.213.250.5/32`, not the subnet. A new non-internal **`edge`**
    network carries the published ports and ACME traffic, because Docker
    cannot publish ports from an `internal` network.
  - Dev: `deploy/dev/gen-dev-tls.sh` creates a `bekup.localhost` certificate
    with mkcert (or a self-signed openssl fallback) in `secrets/dev/tls/`.
    The dev override switches both profiles to static-certificate mode on
    `127.0.0.1:8443`/`:8000` and disables certbot. `PROXY_TLS_MODE=static`
    also serves production users with their own PKI.
  - Logs: no Traefik access log; the nginx access log is JSON without query
    strings, with `/api/download/{ticket}` redacted.
- **Alternatives considered:** Traefik Docker provider with labels (rejected:
  needs the socket); a docker-socket-proxy (rejected: still grants the
  container-listing API to an internet-facing process); trusting the whole
  `frontend` subnet (rejected: anything attached to it could spoof client
  IPs); `NET_BIND_SERVICE` to bind 80/443 directly (rejected: not needed with
  port mapping); TLS-ALPN-01 (possible, but HTTP-01 is the more robust
  default because port 80 is open anyway for the redirect).
- **Consequences:** No proxy container has Docker access. Adding hosts or
  routes means editing `deploy/traefik/dynamic/`. Running both profiles at
  once fails (same ports and IP), which is intended. nginx's *error* log can
  still contain a request line with a download ticket; tickets are
  single-use, 60 s and session-bound (D-056), so the exposure is negligible.
  On Linux, root on the host can reach container IPs on bridges directly.
  Binding `serve` to its `frontend` address limits that to the proxy
  network. testssl.sh 3.2 against both profiles: only TLS 1.2/1.3, only FS
  AEAD ciphers, no vulnerabilities found.
- **Related:** task 0.7b; D-017, D-048, D-052, D-056, D-077, D-081, D-082,
  D-094; `docker-compose.yml`, `docker-compose.dev.yml`, `deploy/traefik/`,
  `deploy/nginx/`, `deploy/dev/gen-dev-tls.sh`, `bekup.example.toml`,
  `deploy/dev/bekup.dev.toml`, `docs/installation/reverse-proxy.md`.

## D-094: nginx profile: unprivileged nginx + certbot instead of nginx-proxy

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (task 0.7b)
- **Context:** D-048 names nginx-proxy + acme-companion for the `nginx`
  profile. nginx-proxy generates its configuration with docker-gen from the
  Docker socket. acme-companion also needs the socket, both to discover
  containers and to signal/exec into the nginx-proxy container for reloads.
  A read-only socket mount does not restrict the Docker API (see the runner
  note in `docker-compose.yml`), and a socket proxy would still have to allow
  exec/kill. Two internet-adjacent containers would therefore hold
  root-equivalent access, which contradicts D-001/D-003 (only the runner
  touches Docker).
- **Decision:** The `nginx` profile is a **plain hardened nginx**
  (`nginxinc/nginx-unprivileged`, pinned) with a static configuration from
  `deploy/nginx/`, plus a **certbot** companion (`certbot/certbot`, pinned)
  using the webroot (HTTP-01) method. The two share only two volumes: the
  challenge webroot, and the certificate directory (read-only for nginx).
  certbot runs `certonly --keep-until-expiring` every 12 hours with backoff.
  nginx's entrypoint watches the certificate's checksum and, after
  `nginx -t`, reloads itself, so no shared PID namespace, signal channel or
  socket is needed. Before the first certificate exists, nginx serves only
  the ACME challenge on port 80. Both containers run as UID 65532 with a
  read-only root and no capabilities; certbot has no inbound port and sits
  only on `edge`.
- **Alternatives considered:** nginx-proxy + acme-companion with the socket
  mounted read-only (rejected: `:ro` gives no API restriction; this would be
  two extra root-equivalent containers); the same behind a
  docker-socket-proxy (rejected: the reload path needs exec or kill
  permissions); acme.sh (equivalent; certbot is the more widely audited
  default); a shared PID namespace for `kill -HUP` (rejected: it exposes
  nginx's processes to the companion).
- **Consequences:** The profile still provides "nginx with automatic Let's
  Encrypt", but has no automatic virtual-host discovery. That's not needed
  for a single bekup host. Certificate renewals reach nginx within 5 minutes
  (`PROXY_CERT_CHECK_SECONDS`). The user should confirm this deviation from
  D-048's wording.
- **Related:** task 0.7b; D-001, D-003, D-048, D-093; `docker-compose.yml`,
  `deploy/nginx/`, `docs/installation/reverse-proxy.md`.

## D-095: Plain HTTP allowed with explicit opt-in and persistent warning

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** Developers must be able to run bekup without a reverse proxy,
  and the user wants plain HTTP to be allowed (not refused) everywhere, as
  long as it is clearly warned about. Previously a non-https `base_url` was
  refused outside dev mode (D-040/D-048 wording).
- **Decision:** A non-https `server.base_url` is accepted when dev mode is on
  **or** when `server.allow_insecure_http = true` (default `false`;
  production must opt in explicitly). Whenever the effective base URL is not
  https: (1) a WARN log line at every startup; (2) a persistent, non-dismissable
  red banner in the web UI ("Connection is not encrypted (no HTTPS)"),
  driven by a `serverInfo.insecureTransport` GraphQL field and by the
  browser's own `window.isSecureContext`/`location.protocol`; (3) outside dev
  mode, a `security.system` alert to admins at startup and an audit entry.
  Consequences handled in code: the session cookie drops the `__Host-`
  prefix and `Secure` flag only when the base URL is http (browsers reject
  them otherwise, except on localhost); HSTS is not sent over http;
  `trusted_proxies` may be empty when no proxy is used. WebAuthn passkeys
  only work in a secure context (https or `localhost`); the UI hides passkey
  enrollment with an explanation otherwise, and TOTP remains available.
  Dev compose publishes `serve` on `127.0.0.1:8080` so no proxy is needed;
  the proxy profiles stay opt-in.
- **Alternatives considered:** HTTP in dev mode only (rejected by the user);
  refusing HTTP entirely (rejected: blocks proxy-less dev use).
- **Consequences:** Operators can knowingly run without TLS; sessions and
  credentials then travel in clear text on the network. The warning is
  impossible to miss and the choice is audited and alerted. Security impact
  documented in SECURITY.md and docs/installation/reverse-proxy.md.
- **Related:** tasks 0.3 (config key), 1.1, 1.2, 1.9, 5.5, 6.1; D-001,
  D-040, D-048, D-093.

## D-096: Compose hardening from the M0 review: superuser socket-only, dev source allowlist, pinned bastion setup

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (M0 review fixes)
- **Context:** The M0 review found three gaps in the compose stack: the app
  Postgres superuser could log in over TCP from any container on `backend`;
  the dev `serve` mounted the whole working tree (`.:/src:ro`), including
  `secrets/` (owner DB URL, superuser password, unencrypted dev CA key, dev
  TLS key); and the dev bastion fetched an unpinned linuxserver mod
  (`DOCKER_MODS`) from ghcr.io at runtime. `minio-init` also passed the MinIO
  credentials to `mc` on argv.
- **Decision:**
  - `deploy/postgres/init/10-bekup-roles.sh` prepends `host`, `hostssl` and
    `host replication` rules that `reject` `$POSTGRES_USER` from any address,
    so the bootstrap superuser can only use the container-local socket. No
    explicit reload: the entrypoint restarts the server after the init
    scripts, before any TCP listener exists. Existing volumes need the
    manual step in `docs/installation/upgrades.md`.
  - The dev `serve` mounts an **allowlist** of read-only paths (`go.mod`,
    `go.sum`, `.air.toml`, `cmd/`, `internal/`, `web/embed.go`,
    `web/dist/`) instead of the repository root, so new secret material in
    the working tree is excluded by default. New top-level Go source
    directories (task 2.4: `proto/`) must be added explicitly. A tmpfs
    overlay over `/src/secrets` was rejected because Docker cannot create
    mount points inside a read-only bind mount when a directory (e.g.
    `certs/`) does not exist, and because it is deny-list based.
  - The bastion enables `AllowTcpForwarding` through the image's own
    `/custom-cont-init.d` hook with an inline compose `config` (root-owned,
    0555), replacing `DOCKER_MODS`; nothing is downloaded at runtime.
  - `minio-init` builds `MC_HOST_dev` from the secret files inside its shell;
    credentials never appear on argv or in the compose model.
- **Alternatives considered:** tmpfs overlays over secret directories
  (rejected, see above); pinning the mod by digest (not reliably supported
  by the linuxserver mod loader); a derived bastion image (more build
  surface for a dev-only service).
- **Consequences:** A compromised `serve`/`migrate` or any container on
  `backend` cannot use the superuser even with its password. The dev `serve`
  cannot read other components' secrets. The dev stack no longer depends on
  runtime downloads from ghcr.io.
- **Related:** D-054, D-078, D-081; `deploy/postgres/init/10-bekup-roles.sh`,
  `docker-compose.dev.yml`, `docs/installation/compose.md`,
  `docs/installation/upgrades.md`.

## D-097: Canonical `BEKUP_<SECTION>_<KEY>` env names, unknown-variable warnings, dev-mode env switch-off

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (M0 review)
- **Context:** Older plan and decision text uses shorthand variable names
  (`BEKUP_BASE_URL`, `BEKUP_TRUSTED_PROXIES`, `BEKUP_MASTER_KEY_FILE`,
  `BEKUP_DISK_ROOT`, `BEKUP_SCRATCH_DIR`, `BEKUP_RUNNER_CONTAINER`) that
  bekup never reads (D-073 maps every key 1:1 to `BEKUP_<SECTION>_<KEY>`). A
  mistyped or shorthand variable silently fell back to the default, and it
  was unclear whether dev mode enabled in the file could be switched off via
  the environment.
- **Decision:**
  - The `BEKUP_<SECTION>_<KEY>` names are canonical and the only ones read.
    The shorthand names in older entries refer to them: `BEKUP_BASE_URL` =
    `BEKUP_SERVER_BASE_URL`, `BEKUP_TRUSTED_PROXIES` =
    `BEKUP_SERVER_TRUSTED_PROXIES`, `BEKUP_MASTER_KEY_FILE` =
    `BEKUP_SECURITY_MASTER_KEY_FILE`, `BEKUP_DISK_ROOT` =
    `BEKUP_STORAGE_DISK_ROOT`, `BEKUP_SCRATCH_DIR` =
    `BEKUP_STORAGE_SCRATCH_DIR`, `BEKUP_RUNNER_CONTAINER` =
    `BEKUP_DOCKER_SELF_CONTAINER` (runner.toml). Older entries are not edited.
  - Every `BEKUP_*` variable that is not a key of the loaded file produces a
    startup warning (also printed by `bekup config validate`); shorthand
    names get a "did you mean" hint, keys of the other file an "ignored by
    this command" note. Only names are reported, never values. Allowlisted:
    `BEKUP_CONFIG`, `BEKUP_DEV` and documented non-key variables of compose,
    dev tooling and tests (`config.NonKeyEnvVars`).
  - `BEKUP_DEV` stays enable-only. `BEKUP_DEV_ENABLED=false` (the key's
    normal override) turns off `[dev] enabled = true` from the file, unless
    `--dev` or `BEKUP_DEV=1` is also given (any enabling source wins).
- **Alternatives considered:** Accepting the shorthand names as aliases
  (rejected: two names per key, ambiguous precedence); failing on unknown
  variables (rejected: shared environments and compose files legitimately
  carry unrelated `BEKUP_*` variables); making `BEKUP_DEV=0` disable dev mode
  (rejected: an enable-only alias is simpler to reason about).
- **Consequences:** Typos are visible at startup. A warning does not stop
  startup, so operators must read the logs. Security impact: none weakened;
  misconfigurations that would silently keep defaults are surfaced.
- **Related:** D-073; `internal/config/env.go`, `internal/config/load.go`,
  `docs/configuration.md`.

## D-098: Wildcard listeners refused, target allowlist overlap checks, extended log redaction (M0 review)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (M0 review)
- **Context:** The M0 review found that the generic defaults `:8080` /
  `:7443` bind every interface while compose expects fixed internal
  addresses; that `metrics.listen` vs `server.listen` was compared as plain
  strings; that runner `allow_cidrs` only rejected prefixes *inside* an
  always-denied range (so `0.0.0.0/1` or `::ffff:127.0.0.1/128` passed); and
  that log redaction missed passwords containing `@` and libpq key-material
  parameters. D-095 also needed its config part (task 0.3a).
- **Decision:**
  - **Plain HTTP (0.3a, implements D-095):** `server.allow_insecure_http`
    (default false). A non-https `base_url` is valid in dev mode or with the
    flag; `trusted_proxies` may then be empty. `(*Bekup).InsecureTransport()`
    and `(*Bekup).Warnings()` (startup warnings, incl. "plain HTTP: traffic
    not encrypted") are provided; `Result.Warnings` aggregates all startup
    warnings for `serve` to log.
  - **Wildcard listeners:** code defaults stay generic, but outside dev mode
    `server.listen` and the runner's `listen.address` must not be a
    wildcard/unspecified address (`:port`, `0.0.0.0`, `[::]`,
    `::ffff:0.0.0.0`) unless `server.allow_wildcard_listen` /
    `listen.allow_wildcard` is set, which logs a startup warning. The example
    files use the compose addresses (`10.213.250.10:8080`,
    `10.213.251.10:7443`), marked as examples.
  - **Listener collisions:** listen addresses are compared as host:port with
    numeric ports; a wildcard host overlaps every host and `localhost`,
    `127.0.0.1` and `::1` are one host.
  - **Runner target allowlist:** a prefix is rejected when it **overlaps**
    any always-denied range. IPv4-mapped IPv6 prefixes are unmapped first;
    a prefix covering all of `::ffff:0:0/96` is rejected; `/0` stays
    rejected. (The runner's dial-time check must unmap addresses too, task
    2.x.)
  - **Log redaction (extends D-075):** URL passwords run up to the last `@`
    of the URL token (over-redacting a later `@` in the same token is
    accepted); `password`, `*password`, `*secret`, `*token`, `sslpassword`,
    `sslkey`, `sslcert` and `passfile` are redacted in `key=value` DSNs and
    URL query strings, including quoted values with escapes. The fatal CLI
    error print goes through `RedactCredentials` and the registry scrub.
- **Alternatives considered:** Changing the code defaults to fixed
  addresses (rejected: they are deployment-specific); a warning instead of a
  refusal for wildcard listeners (rejected: security first, explicit opt-in);
  redacting only up to the first `@` (rejected: leaks the rest of the
  password).
- **Consequences:** Existing configs with `:8080` / `:7443` fail outside dev
  mode until they bind an address or opt in. Some harmless values (an `@` in
  a query string, `sslcert` paths) are redacted in logs.
- **Related:** D-073, D-075, D-077, D-095; tasks 0.3, 0.3a;
  `internal/config/validate.go`, `internal/logging/handler.go`,
  `cmd/bekup/main.go`, `bekup.example.toml`, `runner.example.toml`.

## D-099: DB role checks on every migrate run and at serve startup; faithful 00001 down

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user accepted all M0 proposals)
- **Decided by:** agent with user approval (M0 review)
- **Context:** D-092 checked SUPERUSER/BYPASSRLS on each migrate run but the
  full role check (CREATEROLE, REPLICATION, app-not-member-of-owner) only ran
  inside migration 00001, i.e. once. Role memberships (including predefined
  roles) were not checked at all, `serve` had no startup check, and the
  00001 down step granted PUBLIC more than the init script leaves.
- **Decision:**
  - `bekup migrate` runs the full check on **every** run before touching the
    schema: connected as `bekup_owner`; `bekup_app` exists; neither role has
    SUPERUSER, BYPASSRLS, CREATEROLE or REPLICATION; neither is a member
    (directly or transitively, `pg_has_role(..., 'MEMBER')`) of a role with
    those attributes or of **any predefined `pg_*` role** except
    `pg_database_owner` (which the owner holds implicitly); `bekup_app` is
    not a member of `bekup_owner`.
  - Rationale for denying all predefined roles: `pg_read_server_files`,
    `pg_write_server_files` and `pg_execute_server_program` are
    superuser-equivalent; `pg_write_all_data` bypasses the audit-log grants;
    `pg_read_all_data` exposes other databases in the cluster;
    `pg_signal_backend`, `pg_checkpoint` and `pg_maintain` allow disrupting
    sessions or locking tables; `pg_create_subscription` makes the server
    connect out; the monitoring roles disclose settings and other sessions'
    queries. bekup needs none of them, and a pattern also covers roles added
    by future PostgreSQL versions.
  - `db.CheckAppRole(ctx, db)` performs the same check for `bekup_app` and
    verifies the session runs as `bekup_app`; `serve` calls it at startup
    (task 1.1) and refuses to run otherwise.
  - Migration 00001 down restores exactly the init-script state (no default
    privileges, no PUBLIC access to the schema or database, `bekup_app`
    keeps CONNECT); an integration test compares the privileges before up
    and after down. The integration tests run the real
    `deploy/postgres/init/` scripts in the test container.
- **Alternatives considered:** A fixed list of dangerous predefined roles
  (rejected: misses future roles); checking only at first apply (rejected:
  roles can be changed later).
- **Consequences:** Granting any predefined role to the bekup roles (for
  example `pg_monitor` for monitoring) stops migrate and serve; use a
  separate monitoring role instead.
- **Related:** D-054, D-092, D-096; tasks 0.4, 1.1; `internal/db/migrate.go`,
  `internal/db/queries/migrate.sql`,
  `internal/db/migrations/00001_roles_and_privileges.sql`,
  `internal/db/main_integration_test.go`.

## D-100: TLS for the app database connection: configurable, verified by default

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user
- **Context:** The connection between `serve`/`worker`/`migrate` and bekup's
  own Postgres ran in plain text on the internal `backend` network. The user
  chose to make TLS for it configurable.
- **Decision:** New config `database.tls_mode` = `verify-full` (default) |
  `require` | `disable`, plus `database.tls_ca_file` (and optional client
  cert/key files). `verify-full` verifies the server certificate against the
  configured CA (not the system roots) and the host name. `require` and
  `disable` are allowed but, like plain HTTP (D-095), produce a startup
  warning, a UI security badge for admins and, outside dev mode, a
  `security.system` alert + audit entry. The shipped compose stack runs with
  TLS on: `bekup certs init` gains a `postgres` server component (DNS
  `postgres`) from the private CA, mounted into the postgres container
  (`ssl=on`, TLS ≥1.2, pg_hba `hostssl` only for the bekup roles). Dev
  compose uses the same setup, so dev matches production.
- **Alternatives considered:** always on (rejected by the user in favour of
  configurable); plain on an isolated network (previous state).
- **Consequences:** a compromised container on `backend` can no longer sniff
  or tamper with app-DB traffic unless TLS is explicitly weakened, which is
  visible and alerted.
- **Related:** task 1.0; D-001, D-054, D-089, D-092, D-095.

## D-101: Audit API fail-closed policy, signed chain heads and `instance_keys` (task 1.3)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (task 1.3)
- **Context:** D-054/D-092 fix the chain format and D-087 the signing keys,
  but not the write API semantics, the redaction of details, the signed-head
  format and storage, the `instance_keys` schema or what `audit verify`
  treats as tampering.
- **Decision:**
  - **Fail closed:** `audit.Auditor.Record`/`RecordTx` return an error
    wrapping `ErrRecordFailed` whenever the entry is not stored, and callers
    must fail the audited operation. `RecordTx` writes inside the caller's
    transaction so the action and its entry commit or roll back together.
  - **Action catalog:** actions are typed constants (`internal/audit/actions.go`);
    unknown actions are rejected. Adding an action means adding a constant.
  - **Details redaction:** details are normalized through JSON, every value
    under a key matching `logging.IsSecretKey` becomes `[REDACTED]`, every
    string is scrubbed with `logging.RedactCredentials` and the secret
    registry (`logging.DefaultRegistry` by default), NUL becomes U+FFFD,
    nesting is capped at 16 levels and the result at 64 KiB (an error, not
    truncation). Target type/id are scrubbed and length-checked as well.
  - **instance_keys:** `id` (16 hex, CHECKed to equal the D-087 ID of
    `public_key`), `public_key` (32 bytes, unique), `sealed_private_key`
    (keyring-sealed seed, AAD `instance_keys:<id>:private`), `created_at`,
    `retired_at`. A unique partial index allows one active key, so
    concurrent creators converge. `bekup_app` may INSERT/SELECT and UPDATE
    only `retired_at`; no DELETE (old public keys must stay). The key is
    created lazily on the first head signature and that creation is audited
    (`keys.instance_key.created`). Package `internal/instancekeys`;
    rotation and re-sealing are left to later tasks.
  - **Signed heads:** `audit_chain_heads` (id, chain_id, chain_hash,
    signed_at, key_id → instance_keys, signature), append-only for
    `bekup_app`. The signed message is the text
    `bekup-audit-head-v1\nchain_id=<n>\nchain_hash=<hex>\nsigned_at=<unix µs>\n`
    under D-087 purpose `audit-head`. The export format is JSON with the
    same fields plus the public key (hex). `HeadExporter` is the hook for
    destinations/webhooks (tasks 3.x/5.x); `NopExporter` and an atomic
    `FileExporter` ship now. `HeadSigner.Run` signs periodically when the
    chain tip moved; export failures are logged and audited
    (`audit.head_export.failed`) but the stored head is kept.
  - **Verification:** `bekup audit verify` streams in batches of 500 rows,
    checks each row's hash, then its link; the first row must start from
    the 32-zero genesis hash. Id gaps alone are **not** tampering (rolled-back
    inserts consume ids); a deleted row is reported as a `link_mismatch` at
    the following id. The newest stored head and an optional `--head-file`
    are checked: valid signature, key id matching the key, the signed row
    still present with the signed hash. For a file head, the embedded public
    key must also equal the stored key with that id. Any failure exits
    non-zero; the absence of a stored head is a warning only.
- **Alternatives considered:** best-effort (fail open) auditing (rejected:
  an attacker who can break audit writes could act unrecorded); hashing
  details client-side (rejected: D-092 keeps the hash in the database);
  a binary length-prefixed head message (rejected: the text form is just as
  unambiguous for numeric/hex fields and easier for third-party verifiers);
  treating id gaps as tampering (rejected: false positives on every
  rollback).
- **Consequences:** An audit-log outage blocks audited operations. A DB
  superuser rewrite is detected only against heads exported before it;
  rows after the last exported head can be lost undetected (SECURITY.md
  §8). The first head signature needs the master keyring.
- **Related:** task 1.3; D-038, D-054, D-069, D-075, D-087, D-092;
  `internal/audit`, `internal/instancekeys`,
  `internal/db/migrations/00005_audit_signing.sql`, `cmd/bekup/audit_cmd.go`,
  `docs/security/audit.md`.

## D-102: HTTP layer details: middleware, trusted proxies, Origin policy, rate limits, health and schema checks (task 1.1)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (task 1.1)
- **Context:** D-048, D-050, D-078, D-080, D-082, D-095 and D-099 set the
  goals for `bekup serve`'s HTTP layer and startup checks but leave the
  mechanics open: how forwarded headers are evaluated, how Origin checks
  treat requests without an Origin, the rate-limit algorithm and bounds,
  timeouts, caching, what the health endpoints check, and how serve reads
  the schema version without access to `goose_db_version`.
- **Decision:**
  - **Middleware order** (`internal/httpserver`, documented in its package
    comment): request ID → client IP → access log → panic recovery →
    security headers → rate limit → Origin check → body limit → router.
  - **Request IDs** are 128-bit `crypto/rand` hex values generated for every
    request (`X-Request-ID` response header); a client-supplied ID is
    ignored.
  - **Trusted proxies:** forwarding headers count only when the TCP peer is
    in `server.trusted_proxies`. `X-Forwarded-For` is walked from the right;
    trusted hops are skipped and the first untrusted address is the client;
    an unparsable entry stops the walk at the last trusted hop.
    `X-Forwarded-Proto` (http/https only) is informational. `X-Forwarded-Host`
    is never used. All forwarding headers (`X-Forwarded-*`, `Forwarded`,
    `X-Real-IP`, ...) are deleted from the request afterwards so no later
    code can read spoofed values. HSTS and every link depend on
    `server.base_url` only.
  - **Origin policy** for every method except GET/HEAD/OPTIONS and for every
    WebSocket upgrade: a present `Sec-Fetch-Site` must be `same-origin`; a
    present `Origin` must equal the `base_url` origin exactly (scheme, host,
    port; default port omitted), plus `http://localhost:5173` and
    `http://127.0.0.1:5173` in dev mode only; a request with neither header
    is refused. All refusals are 403.
  - **Rate limit:** in-memory token bucket per client IP (IPv6 grouped per
    /64) over all routes, including static files and health endpoints; new
    keys `server.rate_limit_per_second` (default 20), `rate_limit_burst`
    (100) and `rate_limit_max_clients` (10000, least recently seen evicted).
    Over the limit: 429 with `Retry-After` in whole seconds. Per-user limits
    follow with sessions (task 1.5).
  - **Limits and timeouts:** `server.max_body_bytes` applies to every route
    (Content-Length checked up front, then `http.MaxBytesReader`; 413). New
    keys `read_timeout` (30s), `write_timeout` (60s), `idle_timeout` (120s)
    and `max_header_bytes` (32 KiB). Long-lived routes (WebSockets, task
    1.10; downloads, task 3.9) must lift the write deadline per request with
    `http.ResponseController`.
  - **Headers:** exactly the D-082 CSP on every response; `nosniff`,
    `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, COOP and CORP
    `same-origin`, `Permissions-Policy` disabling accelerometer, camera,
    display-capture, geolocation, gyroscope, magnetometer, microphone, midi,
    payment and usb (WebAuthn keeps its `'self'` default). HSTS
    `max-age=63072000; includeSubDomains` only when `base_url` is https; the
    `preload` token stays a proxy-level choice. `Cache-Control: no-store` is
    the default for every response; content-hashed files under `/assets/`
    get `public, max-age=31536000, immutable`, all other UI files `no-cache`
    with an ETag.
  - **Access log:** method, path, status, bytes, duration, client IP and
    request ID. The query string is never logged; paths under
    `/api/download/`, `/api/downloads/`, `/setup/`, `/verify-email/` and
    `/reset-password/` are logged as the prefix only; paths are capped at
    256 characters. Panics are logged with the stack; the client gets a
    generic 500.
  - **SPA fallback:** index.html only for GET/HEAD requests that accept
    `text/html` and are not under `/api`, `/graphql`, `/healthz`, `/readyz`
    or `/assets/`. `/graphql` and `/api` are 404 placeholders until their
    tasks mount handlers.
  - **Health:** `/healthz` is liveness without I/O; `/readyz` pings the DB
    and re-checks the schema version (3s timeout) and returns only
    "ok"/"not ready", logging the reason. **`bekup healthcheck serve` probes
    `/healthz`, not `/readyz` as D-080 stated:** the container healthcheck
    then reports whether the process serves HTTP, without DB load every 30s
    and without flapping on a DB blip; `/readyz` stays available to
    monitoring. The probe derives the address from `server.listen` (wildcard
    hosts → loopback), ignores `HTTP(S)_PROXY`, follows no redirects and
    times out after 3s. If accepted, D-080's status should note this.
  - **Schema version:** migration 00006 adds `public.bekup_schema_version()`
    (`SECURITY DEFINER`, owner `bekup_owner`, `search_path = pg_catalog,
    pg_temp`, returns the highest applied goose version), with EXECUTE
    revoked from PUBLIC and granted to `bekup_app` only. `serve` refuses to
    start when the database is behind the newest embedded migration ("run
    `bekup migrate up`") or ahead of it ("newer bekup migrated this
    database; downgrades are not supported"). A database without the
    function counts as version 0 (behind).
  - **Startup order and composition:** `internal/app.New` opens the
    `bekup_app` pool, runs `db.CheckAppRole` and the schema check, then
    binds `server.listen`; only then does `Run` start the components
    (`Component` interface, errgroup). The first failing component or
    SIGINT/SIGTERM stops all of them; the HTTP server drains within
    `server.shutdown_timeout` and then closes remaining connections.
    GraphQL (1.2), River workers (3.4) and the scheduler (3.3) plug in there.
  - **Audit chain-head signing in serve:** `serve` loads the master keyring
    at startup and refuses to start without it (fail closed); when an active
    instance key exists it must open with that keyring (a wrong keyring is a
    startup error, not a silent signing failure later). The D-101
    `HeadSigner` runs as a component every `security.audit_head_sign_interval`
    (new key, default 1h, 1m to 24h) with the no-op exporter until exporters
    are configurable.
- **Alternatives considered:** allowing state-changing requests without an
  Origin header (rejected: no legitimate non-browser client exists yet, and
  refusing is the safe default); trusting the leftmost X-Forwarded-For entry
  (rejected: client-controlled); `golang.org/x/time/rate` per IP (equivalent;
  a 60-line bucket with explicit LRU bounds avoids a dependency); probing
  `/readyz` in the container healthcheck (see above); letting `serve` run
  with a newer schema (rejected: old code against a newer schema can
  misbehave silently).
- **Consequences:** CSRF via cross-site POSTs and WebSocket hijacking is
  blocked at the HTTP layer before any handler runs; spoofed forwarding
  headers from anyone but the configured proxy have no effect on rate
  limits or logs. Clients behind one NAT IPv4 address or one IPv6 /64 share
  a bucket; the defaults leave room for normal use. Operators must run
  `bekup migrate` before starting a new `serve` version; during a rolling
  upgrade older instances report not ready once the schema moves ahead.
- **Related:** task 1.1; D-048, D-050, D-078, D-080, D-082, D-095, D-098,
  D-099; `internal/httpserver`, `internal/app`, `internal/db/schemaversion.go`,
  `internal/db/migrations/00006_schema_version_function.sql`,
  `cmd/bekup/serve_cmd.go`, `cmd/bekup/healthcheck_cmd.go`,
  `docs/architecture.md`, `docs/operations.md`.

## D-103: App DB TLS details: URL handling, require + CA, channel binding, postgres certificate and wrapper (task 1.0)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals); item 3
  (channel binding), the key type in item 4 and the Unix-socket rule in item 1
  superseded by D-104
- **Decided by:** agent with user approval (task 1.0)
- **Context:** D-100 fixes the modes and the compose setup but leaves
  several implementation choices open.
- **Decision:**
  1. **Config wins over the URL.** `database.url_file` / `owner_url_file`
     must be `postgres://` / `postgresql://` URLs (keyword/value DSNs are
     rejected as invalid). pgx's own TLS settings (from `sslmode`, PG*
     environment variables) are always replaced by the configured policy,
     including for fallback hosts; pgx's plaintext fallback of
     `sslmode=prefer/allow` is dropped. A URL `sslmode` weaker than
     `tls_mode` is ignored; a **stricter** one is an error (ambiguous
     intent). `sslrootcert`, `sslcert`, `sslkey`, `sslpassword`,
     `sslcertmode`, `sslcrl`, `sslcrldir` in the URL are rejected. Unix
     socket hosts never use TLS.
  2. **`require` with `tls_ca_file` verifies the chain** (libpq
     `verify-ca` behaviour) but not the host name; without a CA it only
     encrypts. Both log distinct startup warnings. `tls_ca_file` defaults to
     `/run/secrets/bekup_db_ca.crt` and is required for `verify-full` in
     every mode, dev included (there is no system-roots fallback).
  3. *(Superseded by D-104: channel binding is now required with an ECDSA
     postgres certificate.)* **SCRAM channel binding is off unless the URL sets
     `channel_binding`.** The private CA is ed25519 (D-089); RFC 5929 defines
     no `tls-server-end-point` hash for ed25519 signatures, so neither pgx
     nor libpq can compute the binding, and PostgreSQL (which advertises
     SCRAM-SHA-256-PLUS on every TLS connection) rejects the downgrade
     signal pgx would send. With `verify-full` the server is authenticated
     by its certificate. Operators of an external Postgres with RSA/ECDSA
     certificates can set `channel_binding=require` in the URL.
  4. **postgres certificate.** New server component `postgres` (DNS
     `postgres` + `--postgres-san`), ed25519, serverAuth, from the same CA.
     Every new CA's name constraints include `runner` and `postgres` (plus
     extra names), even when the postgres component is not issued. `certs
     rotate --ca stage` without SAN flags keeps the current constraints and
     adds `postgres`, which is the upgrade path for older CAs; issuing a name
     outside the constraints fails with a hint to stage a CA rotation.
     `postgres/ca.crt` (the bundle, both CAs during a rotation) is what
     serve/migrate mount as `tls_ca_file`.
  5. **Server side.** `deploy/postgres/entrypoint.sh` copies cert/key/CA
     from `/run/secrets` into a 0700 tmpfs (`/var/lib/postgresql/tls`)
     owned by UID 70 with mode 0600 and appends `ssl=on`,
     `ssl_min_protocol_version=TLSv1.2`, `ssl_cert_file`, `ssl_key_file`,
     `ssl_ca_file`; missing files are fatal. The init script prepends
     `hostnossl all bekup_owner,bekup_app all reject` below the superuser
     block (new volumes; existing volumes: manual step in upgrades.md).
     Compose does not set `BEKUP_DATABASE_TLS_MODE`; `verify-full` is the
     default, so `bekup.toml` stays the single place to change it.
  6. `PoolOptions.TLS` / `NewMigrator(..., TLSOptions, ...)` are mandatory:
     the zero value is rejected, so no code path can connect without an
     explicit policy. `PoolOptionsFromConfig` now takes the whole config and
     returns an error (it reads the TLS files).
- **Alternatives considered:** keyword/value DSN support (more parsing
  surface for no benefit); erroring on any URL `sslmode` (breaks every
  existing URL file for no security gain); an ECDSA CA or a second CA for
  postgres to keep channel binding (more PKI to operate; certificate
  verification already prevents MITM); fixing `pg_hba.conf` of existing
  volumes automatically in the wrapper (silent config rewrites).
- **Consequences:** a compromised container on `backend` can neither read
  nor tamper with app-DB traffic in the default setup. Without channel
  binding, `require` without a CA is open to an active MITM; it is warned.
  Upgrading needs a CA rotation for CAs from before this release.
- **Related:** task 1.0; D-089, D-090, D-095, D-099, D-100.

## D-104: App DB TLS: ECDSA postgres issuer and required SCRAM channel binding

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user (ECDSA postgres certificate + required channel
  binding), implementation details by agent (task 1.0)
- **Context:** D-103 item 3 turned SCRAM channel binding off because the
  Ed25519 PKI (D-089) has no RFC 5929 `tls-server-end-point` hash. The user
  chose to keep the binding and change the postgres certificate instead.
- **Decision:**
  1. The binding hash comes from the leaf's *signatureAlgorithm*, i.e. from
     the issuer's key, so an ECDSA leaf signed by the Ed25519 root would
     still fail. `bekup certs` therefore issues the postgres certificate as
     an **ECDSA P-256 leaf signed with ecdsa-with-SHA256 by a one-time ECDSA
     P-256 issuer**: an intermediate CA signed by the Ed25519 root,
     `pathlen 0`, EKU serverAuth only, name-constrained to exactly the
     leaf's DNS names/IPs (all IPs excluded when none, no URI SANs), valid
     exactly as long as the leaf. The issuer key is generated, used once and
     dropped (never written anywhere), so there is no intermediate key to
     protect, and rotating the postgres certificate (`certs rotate
     --component postgres`, CA promote) always creates a new issuer. This
     is simpler and safer than a stored intermediate key or a second root.
  2. `postgres/postgres.crt` holds the chain leaf + issuer (served by
     PostgreSQL); clients still trust only the Ed25519 root
     (`postgres/ca.crt`). The rest of the PKI stays Ed25519.
  3. New CAs have **path length 1** (was 0) to allow the issuer. Runner mTLS
     keeps D-089's direct-issuance rule explicitly: both runner TLS configs
     reject any verified chain longer than leaf + root. CAs with path length 0
     cannot issue the postgres certificate; `certs rotate --ca stage`
     creates a path-length-1 CA (upgrade path, with D-103 item 4).
  4. bekup's pool and migrator connections set pgx `channel_binding =
     require` whenever TLS is on (verify-full, require). A URL
     `channel_binding` other than `require` is an error (the config wins,
     like `sslmode`); `channel_binding=require` with `tls_mode = disable` is
     an error; TLS modes refuse Unix-socket hosts (use `disable` there).
     Manual `psql` sessions work with libpq's defaults.
- **Alternatives considered:** keep binding off (D-103 item 3; rejected by
  the user); ECDSA leaf directly under the Ed25519 root (does not work, see
  1); a persistent ECDSA intermediate with a stored key (more key material
  to protect and rotate); a separate ECDSA root for postgres (second trust
  anchor and offline key); switching the whole PKI to ECDSA.
- **Consequences:** the SCRAM exchange is bound to the certificate the
  client saw, so even `tls_mode = require` without a CA cannot be relayed by
  an active MITM who lacks the password, and a stolen session cannot be
  moved to another TLS channel. The root's path length grows by one; the
  root key stays offline and the runner refuses intermediate chains.
  Existing CAs need a CA rotation before the postgres certificate can be
  issued.
- **Related:** task 1.0; D-089, D-090, D-100, D-103.

## D-105: GraphQL server details: transport, limits, access convention, error codes (task 1.2)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (task 1.2)
- **Context:** D-050 fixes the goals for the GraphQL endpoint (POST + JSON
  only, depth/complexity/size limits, masked errors, introspection only for
  authenticated users) and D-095 needs a `serverInfo.insecureTransport`
  field and a startup audit entry. Mechanics, defaults and the conventions
  that tasks 1.4, 1.5, 1.9 and 1.10 build on were open.
- **Decision:**
  - **Layout:** gqlgen pinned as a `go tool` dependency; schema-first in
    `internal/graph/schema/*.graphqls`, generated code in
    `internal/graph/generated` and `internal/graph/model/models_gen.go`,
    resolvers in `internal/graph/resolvers` (follow-schema layout),
    `gqlgen.yml` at the root; `just gen` runs gqlgen, then graphql-codegen
    against the same schema (the web placeholder schema is gone). `Time`
    (RFC 3339) and `UUID` (canonical form only; google/uuid) are custom
    scalars whose bad input is a `VALIDATION` error; both are strings in
    the web client.
  - **Transport:** a wrapper in front of gqlgen accepts only `POST` (405
    with `Allow: POST`) with exactly one `Content-Type: application/json`
    (optional `charset=utf-8`; anything else 415), reads at most
    `graphql.max_body_bytes` (new key, default 256 KiB, at most
    `server.max_body_bytes`; 413) and requires exactly one JSON object with
    only `query`, `operationName`, `variables`, `extensions` and a non-empty
    query (400 with a generic message: gqlgen's own decode error echoes the
    body). Only gqlgen's POST transport is registered; no GET, multipart,
    form, SSE or APQ. `/graphql/*` is 404. The WebSocket transport is added
    in task 1.10 in `graph.New`.
  - **Limits:** `graphql.max_depth` (default 12; field nesting, fragments
    expanded with memoization; introspection meta fields not counted),
    `graphql.max_complexity` (default 1000; gqlgen `FixedComplexityLimit`,
    1 per field), a parser token limit of 10 000 and an LRU query cache of
    1000 documents. Limit violations are protocol errors (HTTP 422) with
    codes `QUERY_TOO_DEEP` / `COMPLEXITY_LIMIT_EXCEEDED`.
  - **Introspection:** allowed in dev mode or when the hook
    `Access.IntrospectionAllowed(ctx)` returns true (task 1.5: authenticated
    sessions); otherwise an operation selecting `__schema`/`__type` fails
    as a whole with `INTROSPECTION_DISABLED` (gqlgen's flag stays set as a
    backstop); `__typename` always works. Field suggestions ("Did you mean")
    are disabled outside dev mode because they leak the schema.
  - **Access convention (deny by default):** directives
    `@authenticated`, `@hasRole(roles: [Role!]!)` and `@stepUp` on
    `FIELD_DEFINITION`, `enum Role { ADMIN MAINTAINER USER }`. Every root
    field carries one of them or is listed in `graph.PublicRootFields`
    (today only `Query.serverInfo`). `graph.New` (hence `serve`) refuses to
    start when a root field is unmapped, a public field also carries a
    directive, or an allowlist entry is stale; a root-field middleware also
    denies unmapped fields at runtime (`FORBIDDEN`). Directives delegate to
    the `graph.Access` interface; until task 1.5 it is `DenyAll`, which
    denies every directive with `UNAUTHENTICATED`. Nested fields inherit
    their root field's access; ownership checks happen in resolvers via
    `internal/authz` (1.4). `@hasRole([])` always denies.
  - **Errors:** user-facing errors are `apierror.Error` with
    `extensions.code` in `UNAUTHENTICATED`, `FORBIDDEN`, `STEP_UP_REQUIRED`,
    `REQUIRES_2FA_ENROLLMENT`, `VALIDATION` (+ `extensions.field`),
    `NOT_FOUND`, `RATE_LIMITED`, `CONFLICT`. Protocol errors (parse,
    validation, limits) pass unchanged. Everything else, including panics
    and gqlgen runtime errors during field execution, becomes
    `"internal error"` with code `INTERNAL` and `extensions.requestId` (the
    response's `X-Request-ID`); the real error (and a panic's stack) is
    logged with the same request ID. Transport rejections use
    `BAD_REQUEST`. Resolvers never build `gqlerror` values themselves.
  - **D-095 audit:** at every `serve` startup outside dev mode with a non-https
    `base_url`, the new catalog action `system.insecure_transport_started`
    (actor `system`, target `server`, details `base_url`,
    `allow_insecure_http`) is recorded before listening; if it cannot be
    recorded, `serve` refuses to start (fail closed, D-101). The admin
    alert is task 5.5.
  - **Web banner:** `Query.serverInfo { version insecureTransport devMode }`
    is public; the app shell and the login page show a permanent,
    non-dismissable red banner "Connection is not encrypted (no HTTPS)"
    when `insecureTransport` is true or the page itself is not https (so it
    also shows when the API is unreachable).
- **Alternatives considered:** gqlgen's own Content-Type check only (lets
  its decode errors echo the body and turns oversize bodies into 200
  errors); counting introspection in the depth limit (blocks standard
  introspection queries for authenticated tooling); enforcing access only
  through directives (a forgotten directive would expose the field);
  logging-only for the insecure start (D-095 requires an audit entry).
- **Consequences:** no CSRF-able GraphQL transport; bounded parse and
  execution cost per request; no schema leakage to anonymous callers
  through introspection or suggestions; internal errors never reach the
  client but stay traceable through the request ID. Every new root field
  must declare its access or `serve` will not start. A database outage at
  startup with plain HTTP outside dev mode prevents `serve` from starting
  (it would not start without the DB anyway).
- **Related:** task 1.2; D-011, D-050, D-082, D-095, D-101, D-102; tasks
  1.4, 1.5, 1.9, 1.10, 5.5.

## D-106: Authorization model: operation catalog, policy matrix and owner scope (task 1.4)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (task 1.4)
- **Context:** D-011 to D-015, D-047, D-053, D-057 and D-067 fix who may do
  what in broad terms. The plan leaves open how the policy is expressed,
  the exact rule for operations the decisions do not name (run cancel,
  owner assignment, personal channels, connection tests by maintainers,
  ...), how the GraphQL layer is tied to the policy and the order of the
  checks.
- **Decision:**
  - **Typed catalog:** `authz.Operation` constants (`internal/authz/operations.go`)
    cover every Phase 1 operation; each has exactly one `Rule` (roles,
    owner grant, freshness, restore-target requirement). Unknown operations
    are denied, even for admins. `Policy.Authorize(ctx, op, resources...)`
    checks authentication → role/ownership → restore target → freshness
    and returns UNAUTHENTICATED / FORBIDDEN / STEP_UP_REQUIRED /
    REQUIRES_2FA_ENROLLMENT; failing ownership/factor lookups are internal
    errors (masked, still a denial). Denial messages are generic.
  - **Principal:** `auth.Principal{UserID, Role, SessionID, AAL, StepUpAt,
    AAL2At}` in the request context (task 1.5 attaches it); an incomplete
    principal counts as anonymous. A step-up/AAL2 timestamp more than 5 s
    in the future is not fresh. FreshAAL2 also requires `AAL >= 2`.
  - **Owner grant:** applies to role `user` only (admins need none;
    maintainers never gain anything from `job_owners`, D-067). `OwnerJob`
    requires at least one `authz.Job` resource and ownership of every one
    (resolvers pass the job loaded from the DB for runs/artifacts/restores);
    `OwnerList` allows the call and the resolver filters by
    `Policy.JobScope`. Object facts come from the `Ownership` interface
    (`IsJobOwner`, `BacksUpDatabase`, `UsesConnection`; DB-backed in task
    3.5) and enrolled factors from `SecondFactors` (tasks 1.7/1.8); until
    then `NoOwnership`/`NoSecondFactors` deny.
  - **Restore targets:** `OpRestoreArtifact` needs exactly one
    `RestoreTarget`. Temporary connections and connections with a
    privileged restore role are admin only (D-015, D-068); an owner may
    overwrite only a database one of their jobs backs up or create a new
    database on a connection one of their jobs uses (D-013). Same-engine
    and type-to-confirm checks stay in task 4.5. Restores and the globals
    restore need a passkey/TOTP check within the step-up window
    (REQUIRES_2FA_ENROLLMENT without a factor, D-057).
  - **Rules not spelled out by earlier decisions (most restrictive
    sensible choice):** owners may view, run now, download and restore
    their jobs but **not cancel runs** (not in D-011); job owner assignment
    needs **step-up** (it grants data access) and a maintainer may not add
    themselves (`AuthorizeOwnerAssignment`, D-067); maintainers may test
    connections/destinations, list databases and pre-pull images (needed
    to build jobs) but not fetch/confirm certificate pins or host keys;
    maintainers may manage job encryption keys (step-up) but opt-outs and
    passphrase export are admin only; adding personal notification
    channels and changing one's own password or second factors need
    step-up; revoking other users' sessions needs step-up; security alerts,
    the audit log, chain status, key fingerprints, shared channels, SMTP,
    SSRF allowlist and settings are admin only; maintainers may read a
    minimal user directory for the owner picker.
  - **Step-up window:** `security.stepup_max_age` (≤5 min), clamped to
    5 min in the policy as well; the same window bounds the AAL2 check.
  - **GraphQL binding:** `graph.RootFieldOperations` maps every guarded
    root field to an operation. `graph.New` refuses to start when a guarded
    field is unmapped, a mapping is stale or unknown, a public field is
    mapped, or a field's `@hasRole` names a role the operation's rule never
    allows. The root-field middleware runs `Access.CheckOperation`
    (`Policy.Precheck`: authentication, role eligibility, freshness when
    the role alone grants) before the directives and the resolver.
    `graph.PolicyAccess` implements the directives (@authenticated,
    @hasRole, @stepUp) and allows introspection to any authenticated
    principal; it replaces `DenyAll` in `serve`.
  - **Matrix:** `internal/authz/matrix_test.go` lists the expected outcome
    of every operation for admin, maintainer, maintainer-owner, owner,
    non-owner and anonymous plus the freshness, written independently of
    the policy table; the test parses `operations.go` and fails when a
    constant lacks a rule or a row. `graph` tests fail when a root field is
    neither public nor mapped.
  - **auditLog:** `Query.auditLog(filter, first, after)` is
    `@hasRole(roles: [ADMIN])` → `audit.log_view`, over `Auditor.List`:
    opaque base64url cursors (`audit:<id>`), `first` 1..200 (default 50),
    filters validated against the column limits, details as a JSON string,
    hash as hex.
- **Alternatives considered:** directives only (a missing or too-lax
  directive would expose a field; the precheck and the startup cross-check
  catch it); a YAML/TOML matrix fixture (Go keeps the matrix type-checked
  against the constants); implicit admin bypass in code (an explicit admin
  entry per rule plus the matrix invariant keeps the table auditable);
  letting owners cancel their runs (not granted by D-011; can be added).
- **Consequences:** Every new operation needs a constant, a rule, a matrix
  row and, for GraphQL, a root-field mapping, or tests/`serve` fail. Until
  task 1.5 attaches principals, every guarded field answers
  UNAUTHENTICATED. Owner access depends on the correctness of the task 3.5
  ownership queries, which the matrix exercises only through the fake.
- **Related:** task 1.4; D-011, D-012, D-013, D-014, D-015, D-047, D-053,
  D-057, D-067, D-068, D-101, D-105; tasks 1.5, 1.9, 2.11, 3.5, 3.9, 4.5,
  5.3; `internal/auth/principal.go`, `internal/authz`,
  `internal/graph/policy_access.go`, `internal/graph/schema/audit.graphqls`.

## D-107: Passwords, sessions, lockout and the login API (task 1.5)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) ("Login = step-up" superseded by D-112; lockout counting superseded by D-117)
- **Decided by:** agent with user approval (task 1.5)
- **Context:** D-010, D-047, D-050, D-095 and D-102 fix the goals for
  authentication (argon2id with rehash, ≥12-character policy with a
  strength check and a common-password list, a `__Host-` cookie with the
  token stored as sha256, AAL and step-up, rotation, idle and absolute
  expiry, revoke-all on password change, lockout with backoff, generic and
  timing-safe errors, per-user rate limits). The mechanics, the libraries,
  the bounds, the cookie lifetime, how a login relates to step-up and how
  failures are audited were open.
- **Decision:**
  - **Hashing:** `golang.org/x/crypto/argon2` (argon2id), PHC string
    `$argon2id$v=19$m=…,t=…,p=…$salt$hash` (unpadded standard base64),
    16-byte salt, 32-byte key. Stored hashes are parsed strictly (field
    order, no leading zeros, m ≤ 4 GiB, t ≤ 64, salt 8–64 and key 16–64
    bytes) so a planted hash cannot make a verification arbitrarily
    expensive. A successful login with other parameters or sizes rehashes
    (conditional update; skipped, not failed, when the semaphore is full).
    `security.argon2_iterations` gets an upper bound of 64 and
    `argon2_max_concurrent` 1–64. The semaphore waits at most 2 s with at
    most 4 × the bound waiting; beyond that `RATE_LIMITED`. Inputs above
    4096 bytes are never hashed in full; in verification they are hashed
    truncated and then rejected, so timing does not depend on the account.
  - **Policy:** 12 (`password_min_length`, now at most 128) to 1024
    characters (Unicode code points), no control characters or invalid
    UTF-8; the **SecLists xato-net top-100k list** (MIT, embedded verbatim,
    attributed in NOTICE) checked case-insensitively (only entries that
    could pass the length check are loaded); the e-mail address, its local
    part and the words of the display name (≥ 3 characters) must not occur
    in the password; strength via **`github.com/ccojocar/zxcvbn-go`** (MIT,
    the maintained fork of the zxcvbn port used by gosec) on the first 100
    characters with the account strings as user inputs, minimum score 3
    (new key `security.password_min_score`, 3 or 4). zxcvbn adds
    dictionary, keyboard-pattern, sequence, repeat, date and l33t
    detection that a hand-written entropy estimate would miss.
  - **Sessions:** 32 random bytes from `crypto/rand`, unpadded base64url in
    the cookie; `sessions.token_hash` = sha256 of the raw bytes. Cookie
    `__Host-bekup_session` with `Secure; HttpOnly; SameSite=Strict;
    Path=/`, no `Domain` and **no Expires/Max-Age** (a browser-session
    cookie; the server enforces expiry) for an https `base_url`;
    `bekup_session` without `Secure` only for a plain-http one (D-095); the
    other name is ignored. Idle expiry slides and is written at most every
    `security.session_touch_interval` (new key, default 1m, 10s–10m and
    below the idle timeout) together with last-seen IP and User-Agent
    (control characters dropped, 256 characters); it never passes the
    absolute expiry. The lookup query itself filters revoked and expired
    sessions; disabled users get no principal and the cookie is cleared.
    A lockout only blocks new password logins: sessions established before
    it stay valid (they were authenticated before the guessing started),
    so failing logins with someone's e-mail cannot log them out.
  - **Rotation:** a login always creates a new session and revokes the one
    the browser presented. `Sessions.RotateTx` and
    `ElevateAAL(ctx, sessionID, AAL2)` (task 1.9) issue a new token,
    revoke the old row and keep the absolute expiry, so rotation never
    extends a session. A password change revokes all sessions of the user
    and issues the caller a new one with the same AAL, step-up time and
    absolute expiry. Role changes and disabling (task 1.6) revoke all
    sessions of the target user (`RevokeAllTx`); the role is read from
    `users` on every request anyway.
  - **Login = step-up:** a successful login sets `stepup_at` (a login is a
    fresh authentication), so `changePassword`, whose operation requires
    step-up (D-106), works for `security.stepup_max_age` after a login and
    additionally requires the current password. Task 1.9 may make AAL2
    users complete the second factor before `stepup_at` is set.
  - **Middleware** (`internal/httpserver`, `/graphql` and `/api` only, after
    the D-102 chain): no cookie → anonymous without a lookup; more than one
    session cookie → anonymous and cleared; `auth.ErrNoSession` →
    anonymous and cleared; any other lookup error → 503 (never anonymous by
    accident). Authenticated requests pass a **per-user token bucket**
    (new keys `server.user_rate_limit_per_second` 20 and
    `user_rate_limit_burst` 100). The limiter of D-102 is generalized
    (`httpserver.RateLimiter[K]`) and reused.
  - **Login protection:** a stricter per-IP login limiter (new keys
    `security.login_rate_limit_per_minute` 10 and `login_rate_limit_burst`
    5; every `login` field of a document counts). Lockout per account:
    new keys `security.lockout_threshold` (5, 3–20),
    `lockout_base_duration` (1m, 10s–1h) and `lockout_max_duration` (1h, up
    to 24h): from the threshold on, the lock lasts base × 2^(failures −
    threshold), capped. While locked, the password is not checked (dummy
    verification instead), the attempt is refused generically and does not
    extend the lock; a success resets the counter. A wrong current
    password in `changePassword` counts too. A `LockoutNotifier` hook is
    called after commit (task 5.5).
  - **Generic, timing-safe failures:** every login failure answers
    `UNAUTHENTICATED` "invalid email or password"; a dummy argon2id
    verification (a hash of a random password with the current
    parameters) runs for unknown, disabled, locked and password-less
    accounts and invalid input; tests count dummy verifications instead of
    measuring time.
  - **Audit:** success (`auth.login.succeeded`, actor = user) in the
    session's transaction; failures (`auth.login.failed`) with actor kind
    `system` (the caller is not authenticated; `audit_log` has no anonymous
    kind), target = the account if known, details `{reason, failed_count}`
    with reason in `unknown_user`, `bad_password`, `disabled`, `locked`,
    `no_password`, `invalid_input` — never the password and never the
    e-mail of an unknown account (users type passwords into that field);
    `auth.lockout`, `auth.logout`, `session.revoked`,
    `auth.password.changed` (with the number of revoked sessions) and the
    new `auth.password.change_failed`, each in the transaction of the
    change it records. Rate-limited attempts are logged, not audited
    (they would let anyone flood the audit chain).
  - **GraphQL:** `login(email, password): LoginResult { user
    secondFactorRequired availableFactors }` (public, `PublicRootFields`),
    `logout`, `revokeSession(id)`, `changePassword(currentPassword,
    newPassword)` (`@stepUp`), `me`, `mySessions` mapped to the existing
    `account.*` operations. Policy violations are `VALIDATION` on
    `newPassword`, a wrong current password `VALIDATION` on
    `currentPassword`, a foreign or unknown session `NOT_FOUND`.
  - **Housekeeping:** `serve` deletes sessions expired or revoked more than
    7 days ago, hourly.
  - **Tests:** `HasherOptions.WeakForTests` allows cheap argon2 parameters,
    honored only when `testing.Testing()` is true.
- **Alternatives considered:** `github.com/trustelem/zxcvbn` (older, less
  maintained) or a home-made entropy estimate (misses dictionary and
  keyboard patterns); the full 100k list in memory (≈ 100k strings; only
  ~500 entries can ever match a ≥ 12-character password); persistent
  cookies with Expires = absolute expiry (survive browser restarts for no
  benefit); failing a request when the session touch fails (a DB hiccup
  would log users out; the idle window is only shortened); counting
  attempts while locked (lets an attacker extend a lock indefinitely);
  suspending the sessions of locked users (turns the lockout into a cheap
  targeted logout of anyone whose e-mail is known); auditing rate-limited
  attempts.
- **Consequences:** Account takeover by guessing is bounded by the per-IP
  limiter and the per-account backoff. Anyone who knows an e-mail address
  can keep new password logins of that account temporarily blocked from
  many IPs (existing sessions keep working); locks are capped, cannot be
  extended by attempts while locked, are audited and reported to the
  notifier hook (alert in 5.5), and `bekup admin` can unlock (1.6). Over plain HTTP the session cookie is
  not `Secure` (D-095). A login grants step-up for up to 5 minutes.
  Sessions survive a browser restart only if the browser restores session
  cookies.
- **Related:** task 1.5; D-010, D-047, D-050, D-073, D-095, D-101, D-102,
  D-105, D-106; tasks 1.6, 1.9, 1.10, 5.5; `internal/auth`,
  `internal/httpserver/session.go`, `internal/httpserver/ratelimit.go`,
  `internal/graph/schema/auth.graphqls`, `internal/graph/resolvers/auth.go`,
  `internal/app/auth.go`, `internal/db/queries/{users,sessions}.sql`,
  `NOTICE`.

## D-108: WebSocket subscription transport, session watcher and session event bus (task 1.10)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) (re-validation details superseded by D-118)
- **Decided by:** agent with user approval (task 1.10)
- **Context:** D-052 fixes the goals for subscriptions (cookie auth on the
  upgrade, periodic and per-event session re-validation, authz at delivery
  time, close on logout/revoke/role change, NOTIFY with IDs only) and D-050
  the Origin check of the upgrade. The protocol, the limits, the close
  codes, how revocations reach open sockets (in one instance and across
  instances), which operations may use the socket and how CSP treats it
  were open.
- **Decision:**
  - **Protocol:** only `graphql-transport-ws` (the graphql-ws library) on
    `GET /graphql` with an upgrade; the legacy `subscriptions-transport-ws`
    (`graphql-ws` subprotocol) and upgrades without a subprotocol get 400.
    gqlgen's transport runs on github.com/coder/websocket with compression
    disabled. Non-upgrade GETs stay 405 and a POST carrying an `Upgrade`
    header gets 400 (gqlgen would route it to the WebSocket transport).
  - **Admission (in this order):** the HTTP Origin middleware (D-102), then
    the GraphQL handler again requires an `Origin` header equal to an
    allowed origin (`base_url`, plus the Vite origins in dev mode; the
    library re-checks against their hosts), the subprotocol, a principal
    resolved by the session middleware **from the cookie** (401 otherwise)
    and a free slot: `server.max_ws_connections_per_user` (default 5, 1 to
    100; 429) and the new `graphql.ws_max_connections` (default 1000 per
    instance; 503). `connection_init` must come within
    `graphql.ws_init_timeout` (10s) and must not carry a payload: a
    non-empty payload closes the socket with 4400 instead of being
    interpreted, so credentials can never come from the socket. Server
    pings every `graphql.ws_ping_interval` (25s; no pong within twice the
    interval drops the peer); client messages are capped at
    `graphql.ws_max_message_bytes` (64 KiB; 1009 above); the server's read
    and write deadlines are lifted for the socket only.
  - **Operations:** only subscriptions over the socket (queries and
    mutations there would bypass the per-user HTTP rate limit and could
    not set or clear the session cookie: FORBIDDEN), and subscriptions only
    over the socket (POST would hold a request open: FORBIDDEN). gqlgen
    does not run root-field middleware for subscriptions, so an operation
    context check applies the same deny-by-default gate (directive +
    `RootFieldOperations` + `Access.CheckOperation`). Per connection at
    most `graphql.ws_max_subscriptions_per_connection` (20) active
    subscriptions and a subscribe-rate bucket (5/s, burst 20); excess
    subscribe messages fail with RATE_LIMITED.
  - **Session watcher (`internal/graph/wsconn`):** every connection keeps
    the principal of the upgrade and re-checks its session by id
    (`Sessions.Check`, which never touches the session: an open socket is
    not user activity, so the idle expiry keeps running) every
    `graphql.ws_revalidate_interval` (30s), on every session event of its
    session or user (plus follow-ups after 0.2s, 1s and 3s, because a local
    event can precede the commit of the revoking transaction), after lost
    events (bus overflow, LISTEN reconnect; spread over up to 5s) and
    before delivering an event when the last good check is older than
    `graphql.ws_session_check_ttl` (5s; a cheap cached check). Close codes:
    **4401** session ended (logout, revoked, expired, user disabled),
    **4403** user, role or AAL differs from the upgrade, **4001** session
    rotated (closed on the event itself; the browser holds the new cookie
    and reconnects), **1013** the check failed (fail closed; retryable),
    **1001** server shutdown (hijacked sockets are not covered by
    `http.Server.Shutdown`, so the hub closes them).
  - **Session event bus:** `auth.Sessions` publishes `revoked`, `rotated`
    and `user_revoked` events inside the transaction of `RevokeTx`,
    `RevokeAllTx`, `RotateTx`/`ElevateAAL` and the login that replaces a
    browser's session (`SessionOptions.Events`). A publish failure fails
    the transaction (a revocation never commits without its event). The
    reusable `internal/pubsub` delivers them: `Publisher.PublishTx` calls
    `pg_notify` in the transaction (delivered to every instance on commit,
    dropped on rollback) and publishes on the in-process `Bus` at once; a
    `Listener` on a dedicated connection (`db.NewConnector`, same TLS and
    redaction as the pool) LISTENs on `bekup_session_events` with
    exponential backoff and jitter (0.5s to 30s) and signals a resync after
    every (re)connect. Payloads are `kind:user_id:session_id`, validated to
    identifier characters and at most 256 bytes; receivers treat them as
    hints and re-check the database. Channel names and their LISTEN
    statements are fixed sqlc queries (no SQL built from strings). Tasks
    3.7 and 5.3b add their channels to the same catalog.
  - **Delivery-time authz:** subscription resolvers send through
    `wsconn.Deliver` with `wsconn.Authorize(policy, op, resources)`: before
    each event the session check above, then `Policy.Authorize` with the
    connection's principal and the resources loaded by ID; failing events
    are dropped silently. Demo field: `Subscription.sessionEvents` (own
    sessions revoked/rotated/all revoked, `account.sessions_view`, no new
    operation).
  - **Web client:** Apollo's `GraphQLWsLink` without `connectionParams`;
    4401/4403 navigate to `/login` and stop retrying, 4400 stops retrying,
    everything else retries.
  - **CSP:** D-082 is unchanged. CSP Level 3 lets `connect-src 'self'`
    match `ws:`/`wss:` on the page's own host; `pnpm csp-check` now proves
    in Chrome that a same-origin WebSocket opens without a violation and a
    cross-origin one is blocked (Firefox and Safari were not run; current
    versions document the same CSP3 rule). Old browsers with CSP 2
    semantics (`'self'` without ws/wss, notably older Safari releases)
    would block subscriptions; adding `ws:`/`wss:` scheme sources would
    allow any host and is rejected.
- **Alternatives considered:** authenticating with a token in the
  `connection_init` payload (needs a script-readable credential; rejected
  by D-047/D-052); keeping the legacy protocol (unmaintained, no
  ping/pong); queries and mutations over the socket (bypass the cookie and
  rate-limit handling); in-process events only (other instances would rely
  on the 30s fallback) or NOTIFY only (closure would depend on the LISTEN
  connection even with one instance); an after-commit hook in `db.Tx`
  (touches every transaction; the follow-up checks cover the gap);
  touching the session on re-checks (an idle tab would never time out);
  continuing on check errors (fail open).
- **Consequences:** A revoked, expired or downgraded session loses its
  socket within milliseconds in the common case, at the latest after
  `ws_revalidate_interval`, and receives no event after its access is gone
  (at most `ws_session_check_ttl` of cached validity before an event).
  A database outage closes all sockets with 1013 (clients retry and get
  503 until it is back). An open socket does not keep a session alive.
  Each instance holds one more database connection for LISTEN. Anyone who
  can NOTIFY on the app database can only trigger re-checks, not grant
  access.
- **Related:** task 1.10; D-047, D-050, D-052, D-082, D-102, D-105, D-106,
  D-107; tasks 3.7, 5.3b, 6.x; `internal/pubsub`, `internal/graph/ws.go`,
  `internal/graph/wsconn`, `internal/graph/schema/subscriptions.graphqls`,
  `internal/auth/session_events.go`, `internal/db/queries/{pubsub,session_watch}.sql`,
  `internal/db/listen.go`, `internal/httpserver/websocket.go`,
  `internal/app/subscriptions.go`, `web/src/lib/apollo.ts`,
  `web/scripts/csp-check.mjs`.

## D-109: WebAuthn passkeys as a second factor (task 1.8)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) (last-factor error code superseded by D-112; AAL2 without user verification superseded by D-116)
- **Decided by:** agent with user approval (task 1.8)
- **Context:** D-010 makes passkeys (WebAuthn) and TOTP the second factors;
  the plan fixes `go-webauthn/webauthn`, "RP ID from base_url", step-up for
  2FA changes and, per D-095, that passkeys need a secure context. Open
  were the relying-party derivation, the ceremony parameters, where the
  challenge lives, the user handle, sign-counter handling, limits, how
  other code learns about enrolled factors and the API shape.
- **Decision:**
  - **Library:** `github.com/go-webauthn/webauthn` v0.18.2 (pinned in
    go.mod). It checks challenge, origin, RP ID hash, user presence,
    signature, user handle, the allowed-credential list, backup-flag
    consistency and the server-side ceremony expiry.
  - **Relying party** (`passkey.RelyingPartyFor`): RP ID = the host name of
    `server.base_url` (lowercase, no port); the only accepted origin is the
    base URL's origin, plus `http://localhost:5173` (Vite) in dev mode when
    the RP ID is `localhost`. Cross-origin (iframe) use is off. Passkeys are
    **unavailable** when the base URL is `http` on a host other than
    `localhost`/`*.localhost` (no secure context, D-095) or when the host is
    an IP address (not a valid RP ID). `webauthnStatus` reports
    `webauthnAvailable: false` with the reason; every ceremony then fails
    (FORBIDDEN); listing and deleting keep working. `serve` logs a WARN line
    at startup.
  - **Ceremony parameters:** attestation `"none"` (no attestation
    verification against metadata, no AAGUID allowlist), `residentKey:
    "preferred"`, **`userVerification: "preferred"`** for registration and
    assertion (the passkey is the possession factor next to the password;
    PIN-less security keys stay usable; the UV flag is stored per passkey
    and recorded in the assertion audit entry so a later policy can require
    it), `excludeCredentials` = the user's passkeys, timeout **3 minutes**,
    also enforced server-side.
  - **Challenge storage:** the library's `SessionData` (challenge, RP ID,
    user handle, allowed credentials, UV requirement, expiry) is stored in
    the new table `webauthn_ceremonies`, bound to the **session id** and the
    user, one pending ceremony per session and kind (a new begin replaces
    the old one), `expires_at` = begin + 3 minutes. The finish step deletes
    the row first in its own statement (**single use**, also when the
    verification then fails), so a challenge cannot be replayed, used from
    another session or for another user, or after expiry. Expired rows are
    purged on every begin and cascade with their session.
  - **User handle:** the user's UUID (16 random bytes from
    `gen_random_uuid()`), no personal data. `user.name` = e-mail,
    `user.displayName` = display name (or e-mail).
  - **Storage** (`webauthn_credentials`, migration 00008): credential id
    (unique across users, 16-1023 bytes), COSE public key, attestation type
    and format, AAGUID, sign count, transports, attachment, UP/UV/BE/BS
    flags, name (1-64 characters, no control characters), `created_at`,
    `last_used_at`, `sign_count_anomaly_at`. At most **20 passkeys** per
    user (checked under a `users` row lock). Browser responses are limited
    to 32 KiB of JSON.
  - **Sign counter:** an assertion whose counter is not greater than the
    stored one is **refused** (`ErrSignCountRegression`) unless both are 0
    (authenticators without a counter, e.g. synced passkeys). The refusal
    sets `sign_count_anomaly_at` (shown in `myPasskeys`), is audited
    (`auth.passkey.sign_count_anomaly` with both counters), logged at WARN
    and reported to the `passkey.Notifier` hook (security alert, task 5.5).
    The stored counter is not changed, so the genuine authenticator keeps
    working; the passkey is not disabled automatically (that would turn a
    stale clone into a lockout of the real user). Counter updates are
    conditional (`sign_count < new`), so two concurrent assertions with the
    same counter cannot both succeed.
  - **Step-up:** `beginPasskeyRegistration`, `finishPasskeyRegistration`,
    `renamePasskey` and `deletePasskey` carry `@stepUp` and map to
    `account.second_factor_edit` (FreshStepUp); the service checks the
    step-up again. `webauthnStatus` and `myPasskeys` map to `account.view`
    (own account data; no new operation was needed).
  - **Assertion API for task 1.9:** `Service.BeginAssertion(ctx, sessionID,
    userID)` and `FinishAssertion(ctx, sessionID, userID, responseJSON)`;
    the session id is explicit (instead of only the user id) because the
    challenge is bound to the session. `FinishAssertion` does not elevate
    the session; 1.9 calls `Sessions.ElevateAAL` and decides about failure
    counting.
  - **Factor registry:** `internal/auth/factors.go`: each factor service
    implements `auth.FactorChecker` (Kind, HasFactor) and registers with
    `auth.SecondFactors`, which implements `authz.SecondFactors` (so
    `REQUIRES_2FA_ENROLLMENT` sees passkeys and TOTP) and lists enrolled
    kinds. `auth.FactorRemovalGuard` (default `AllowFactorRemoval`) is the
    last-factor hook for 1.9's policy: it gets the factor kinds the user
    would keep and can refuse with `auth.ErrLastFactor`.
  - **Audit:** `auth.passkey.registration_begun`,
    `auth.passkey.registration_failed` (reason), `auth.mfa.enrolled` /
    `auth.mfa.removed` with `factor: "passkey"`, `auth.passkey.renamed`,
    `auth.passkey.assertion_succeeded`, `auth.passkey.assertion_failed`
    (reason: `no_ceremony`, `invalid_response`, `verification_failed`,
    `unknown_passkey`), `auth.passkey.sign_count_anomaly`. Library error
    details are logged at INFO, never returned to the client. Assertion
    begins are not audited (no state change).
  - **GraphQL:** `scalar JSON` (an object in, raw JSON out; inputs at most
    32 KiB); options are returned as `{"publicKey": {...}}` for
    `PublicKeyCredential.parseCreationOptionsFromJSON`, responses are
    `PublicKeyCredential.toJSON()`. Errors: FORBIDDEN (unavailable, last
    factor), STEP_UP_REQUIRED, VALIDATION on `name`/`response` (any
    verification failure, generic message), CONFLICT (limit), NOT_FOUND.
- **Alternatives considered:** `userVerification: "required"` (stronger,
    but excludes PIN-less keys; can be added as a policy later);
    attestation `"direct"` with an MDS allowlist (privacy cost, operational
    burden, little value for self-hosted use); storing the challenge in a
    signed cookie (client-held state, replay needs extra bookkeeping) or in
    memory (lost on restart, not shared between instances); disabling a
    passkey on a counter regression (locks out the genuine user); a new
    `account.second_factor_view` operation (the existing `account.view`
    already covers own account data); a random 64-byte user handle column
    (the random UUID already carries no personal data).
- **Consequences:** Passkeys work on https deployments and on
  `http://localhost` only; elsewhere users fall back to TOTP and the UI
  explains why. Phishing resistance comes from the origin and RP ID checks;
  a cloned authenticator that is behind the original is refused and raises
  an alert; one that is ahead is only detected when the original is used
  again. Changing `server.base_url` to another host invalidates every
  registered passkey (the RP ID changes); users then need another factor or
  a reset.
- **Related:** task 1.8; D-010, D-047, D-095, D-105, D-106, D-107, tasks
  1.7, 1.9, 5.5, 6.1; `internal/auth/passkey`, `internal/auth/factors.go`,
  `internal/db/migrations/00008_webauthn_passkeys.sql`,
  `internal/db/queries/webauthn.sql`,
  `internal/graph/schema/webauthn.graphqls`,
  `internal/graph/resolvers/webauthn.go`, `internal/graph/model/json.go`,
  `internal/app/passkey.go`.

## D-110: First-run setup code, user administration and break-glass CLI (task 1.6)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) (setup-code part superseded by D-113: the setup code, `setupStatus`/`completeSetup`, `setup_state` and the `setup.*` audit actions are removed)
- **Decided by:** agent with user approval (task 1.6)
- **Context:** The plan fixes a one-time setup token with a 1h TTL printed
  to the log, `bekup admin create|reset-password` (D-069: audited with actor
  `cli`), admin user CRUD with step-up for user and role changes (D-047,
  D-106) and session list/revoke. Open were the code format and storage,
  what "permanently disabled" means, how an admin gives a new user a
  password, hard vs. soft delete, the last-admin rule and the CLI's
  password input.
- **Decision:**
  - **Setup code:** 160 random bits (`crypto/rand`), shown as base32 in 8
    groups of 4 (`ABCD-EFGH-...`; input is case-, space- and
    dash-insensitive). Only `sha256("bekup-setup-code-v1\0" || code)` is
    stored, in the single-row table `setup_state` with `code_expires_at` =
    issue time + 1h. `serve` issues a new code at **every startup** while
    setup is open (the previous code's plain text is gone), records
    `setup.code_issued` and logs the code once at WARN under the key
    `setup_code` with instructions. Expired codes are not renewed while
    running: restart `serve` (or use `bekup admin create`).
  - **Setup flow:** `Query.setupStatus { required }` and
    `Mutation.completeSetup(code, email, displayName, password)` are public.
    completeSetup is limited to 5 attempts/min per client IP (each attempt
    may cost an argon2id hash), checks the code with a constant-time
    comparison of the hashes, applies the password policy, then in one
    transaction holding `SELECT ... FOR UPDATE` on the setup row re-checks
    everything, creates the admin, sets `completed_at` and drops the hash,
    so exactly one concurrent caller wins. It records `setup.completed`
    (wrong/expired codes: `setup.failed`, actor system, reason only) and
    signs the admin in (AAL1, fresh step-up). Setup is required only while
    `completed_at` is NULL **and** no (non-deleted) admin exists;
    completion is final: a trigger refuses to reset `completed_at` and
    `bekup_app` has no INSERT/DELETE on `setup_state`. Creating an admin
    with the CLI also closes setup, and the migration closes it for
    databases that already have an admin. Afterwards completeSetup answers
    FORBIDDEN.
  - **New users get a temporary password, not an admin-chosen one:**
    `createUser` and `resetUserPassword` generate a 160-bit temporary
    password (same alphabet), returned **once** in the mutation result,
    stored only as an argon2id hash, valid for **72 hours**
    (`users.password_temporary_until`). A login with it works only before
    that time and yields a session whose principal has
    `PasswordChangeRequired`; the policy then denies every operation except
    `account.view`, `account.logout`, `account.password_change` and the own
    sessions with the new code `PASSWORD_CHANGE_REQUIRED`
    (`LoginResult.passwordChangeRequired`, `User.passwordChangeRequired`).
    changePassword clears the mark. Admins never learn a user's lasting
    password.
  - **Soft delete:** `deleteUser` sets `deleted_at`, `disabled`, drops the
    password hash and revokes all sessions; the row stays because audit
    entries reference user ids. E-mail uniqueness moves to a partial unique
    index over non-deleted accounts (the address can be reused). Deleted
    accounts cannot sign in, are hidden from `users` unless
    `includeDeleted`, and every mutation on them answers NOT_FOUND.
    Second-factor rows stay (unusable).
  - **Last active admin:** demoting, disabling or deleting the last admin
    that is neither disabled nor deleted fails with CONFLICT (CLI: an
    error). The check locks all active admin rows (`FOR UPDATE`) before
    the target row, so two admins demoting each other concurrently cannot
    both succeed. Admins cannot delete themselves or reset their own
    password through the admin path (`changePassword` instead).
  - **Privilege changes:** role changes, disabling, password resets and
    deletion revoke every session of the account (`session.revoked_all`);
    a display-name change does not. If an admin's own sessions are revoked,
    the response clears their cookie.
  - **Authz:** all user-admin mutations need step-up (already in D-106,
    including create, delete and display-name changes); new operation
    `user.enable` (admin, step-up); `unlockUser` maps to `user.update`.
  - **Audit actions:** new `user.password_reset`, `user.unlocked`,
    `cli.admin.disabled`, `setup.code_issued`, `setup.failed`,
    `setup.completed`. Details never contain passwords or codes.
  - **CLI (D-069):** `bekup admin create --email --display-name [--role
    admin]`, `reset-password --email`, `unlock --email`, `disable --email`.
    The password comes from a hidden terminal prompt (twice, compared in
    constant time) or `--password-file` (secret-file permission rules,
    surrounding whitespace trimmed); with no terminal and no file the
    command refuses; there is no password flag. CLI-set passwords are
    permanent and must pass the policy. The commands connect as
    `bekup_app` with the normal config and record `audit.CLIEvent` entries
    (`cli.admin.*`, actor `cli`, OS user, hostname). The admin alert of
    D-069 is a TODO for task 5.5.
- **Alternatives considered:** admin-chosen initial passwords (the admin
  would know the user's password and weak ones would be tempting);
  invitation links by e-mail (needs SMTP, task 5.x; can be added later);
  hard delete (breaks the link from audit entries to accounts, cascades
  sessions and factors silently); renewing the setup code in the
  background every hour (a new secret in the logs every hour for an
  unattended instance); keeping the plain code in the DB to re-print it
  on restart (a DB reader could take over the instance); an argon2id hash
  for the setup code (unnecessary for a 160-bit random value).
- **Consequences:** Anyone who can read `serve`'s log during the first hour
  after a start (while no admin exists) can create the first admin; the
  log should be treated as sensitive until setup completes. Temporary
  passwords travel through the admin's screen once and expire after 72h.
  Two `serve` instances starting at the same time each issue a code and
  only the later one works. A host operator with the config can still
  create admins and reset passwords; every such action is audited (and
  alerted from task 5.5). Deleted accounts' e-mail addresses remain in the
  audit log and the `users` row.
- **Related:** task 1.6; D-047, D-069, D-101, D-105, D-106, D-107; task
  5.5, 6.8; `internal/auth/setup_code.go`, `internal/auth/user_admin.go`,
  `internal/auth/user_input.go`, `internal/authz/policy.go`,
  `internal/db/migrations/00009_user_admin_setup.sql`,
  `internal/db/queries/{setup,user_admin}.sql`,
  `internal/graph/schema/{setup,users}.graphqls`,
  `internal/graph/resolvers/users.go`, `internal/app/users.go`,
  `cmd/bekup/admin_cmd.go`.

## D-111: TOTP, recovery codes and second-factor failures (task 1.7)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) (last-factor error code superseded by D-112)
- **Decided by:** agent with user approval (task 1.7)
- **Context:** The plan asks for TOTP with replay protection via the last
  used step plus recovery codes (D-010), stored secrets sealed with the
  keyring (D-085, D-086), step-up for 2FA changes (D-047, D-106) and a CSP
  without `data:` images (D-082). Parameters, the QR code rendering, the
  enrollment flow, how recovery codes are stored and how second-factor
  failures are throttled were open.
- **Decision:**
  - **Library and parameters:** `github.com/pquerna/otp` (Apache-2.0,
    NOTICE carried in ours) with **SHA-1, 6 digits, 30 s** — the RFC 6238
    profile every authenticator app implements (SHA-256/8 digits are
    ignored or mis-handled by many apps). Secrets are **160 bits** from
    `crypto/rand` (RFC 4226's size for HMAC-SHA1). Verification accepts
    **±1 step** (90 s window); the three candidate codes are all computed
    and compared in constant time.
  - **Replay protection:** a code is accepted only for a step **greater
    than `users.totp_last_step`**, recorded with a conditional `UPDATE …
    WHERE totp_last_step < $step AND totp_secret_enc = $verified_secret`;
    of concurrent checks of one code exactly one wins, and a code whose
    secret was replaced meanwhile fails. The confirmation code's step is
    recorded too.
  - **Two-phase enrollment** (`beginTotpEnrollment`, step-up): the secret
    is sealed into `users.totp_pending_enc` (AAD `users:<id>:totp_pending`,
    10-minute `totp_pending_expires_at`) and returned once as otpauth URI,
    **inline SVG QR code** and base32 manual key. `confirmTotpEnrollment`
    (step-up) checks a code against it, **re-seals** it as
    `users.totp_secret_enc` (AAD `users:<id>:totp_secret`) in a conditional
    update that requires the same pending ciphertext, and issues recovery
    codes. The separate AAD means a pending ciphertext can never act as an
    active secret. The secret is never returned again. Starting while TOTP
    is on is refused (disable first); a wrong confirmation code is audited
    but not counted (the caller is signed in and chose the secret).
  - **QR code:** encoded with `github.com/boombuler/barcode/qr` (MIT,
    already a dependency of pquerna/otp; level M) and rendered by bekup as
    a self-contained SVG: one background `rect` and one `path` of
    horizontal runs, no scripts, styles, links or `data:` URIs, so the UI
    can inline it under the D-082 CSP.
  - **Recovery codes:** 10 codes of 12 Crockford-base32 characters
    (**60 bits**), shown as `xxxx-xxxx-xxxx`; input is case-insensitive,
    ignores spaces/dashes and maps i/l→1, o→0. Generated on TOTP
    confirmation and by `regenerateRecoveryCodes` (step-up, requires an
    enrolled factor); each set replaces all older codes and is shown once.
    Stored as **HMAC-SHA256** under an HKDF subkey of a master key
    (`crypto.Keyring.MAC`, purpose `recovery-code`) over
    `bekup-recovery-code-v1 ∥ user id ∥ code`, with the key ID per row
    (`recovery_codes.key_id`), so codes survive a key rotation while the
    old key stays in the keyring. A code is compared in constant time
    against every unused row and spent with `UPDATE … SET used_at = now()
    WHERE used_at IS NULL` (single use under concurrency). The remaining
    count is in `myTwoFactorStatus`. Recovery codes are not a factor of
    their own: disabling the last factor deletes them.
  - **Failures:** every failed TOTP or recovery-code check (wrong,
    malformed, replayed, no factor) answers the same `ErrInvalidCode`,
    increments **`users.mfa_failed_count`** (separate from the password
    counter, so second-factor guessing cannot lock out password logins and
    vice versa) and uses the same backoff settings
    (`security.lockout_*`): from the threshold on, second-factor checks
    are refused without looking at the code (`RATE_LIMITED` with a retry
    time) until `mfa_locked_until`; refused attempts are audited but
    neither counted nor extend the lock; a success resets the counter.
  - **Disable:** `disableTotp` (step-up) asks a `FactorRemovalGuard` with
    the remaining factor kinds (task 1.9 refuses the last factor when 2FA
    is required: `FORBIDDEN`); removes active and pending secret.
  - **Audit and alerts:** `auth.mfa.enrollment_started`,
    `auth.mfa.enrolled` / `auth.mfa.removed` (details.factor `totp`),
    `auth.mfa.verified`, `auth.mfa.failed` (factor, reason, count),
    `auth.mfa.lockout`, `auth.recovery_code.used` (remaining count),
    `auth.recovery_code.regenerated`, each in the transaction of its
    change; never a code or secret. `SecondFactorNotifier` hooks
    (`RecoveryCodeUsed`, `SecondFactorRemoved`, `SecondFactorLocked`) fire
    after commit for task 5.5.
  - **API for task 1.9:** `(*auth.TOTP).VerifyTOTP(ctx, userID, code)` and
    `UseRecoveryCode(ctx, userID, code)` (both replay-safe; the caller
    elevates the session), `HasFactor`/`Kind` (registered in
    `auth.SecondFactors`, which implements `authz.SecondFactors`), `Status`.
  - **GraphQL** (`internal/graph/schema/totp.graphqls`): the four
    mutations `@stepUp` → `account.second_factor_edit`; `myTwoFactorStatus`
    `@authenticated` → `account.view`. The service re-checks the step-up
    (defense in depth).
- **Alternatives considered:** SHA-256/8-digit TOTP (poor app support);
  a wider skew window (more guessing surface and replay window); returning
  a PNG QR code as a `data:` URI (blocked by the CSP) or serving it from a
  URL (the secret would sit in a cacheable response); `rsc.io/qr` (an
  extra dependency for the same encoder); argon2id for recovery codes
  (60-bit random codes need no stretching; a keyed hash already makes a
  leaked table useless without the master key, and argon2id would cost
  ~10 hashes of 64 MiB per attempt); plain SHA-256 (offline-testable from
  a DB leak); counting second-factor failures in the password counter
  (lets a password holder lock the account's logins, mixes two attack
  surfaces); revoking other sessions on TOTP changes (left to task 1.9's
  policy).
- **Consequences:** TOTP secrets and pending secrets are only readable
  with the master key and only under their own AAD; a database leak gives
  neither TOTP secrets nor usable recovery codes. Removing a master key
  from the keyring invalidates recovery codes hashed with it (users
  regenerate them). Codes are accepted for up to 90 s and each step once.
  An attacker with a password gets at most `lockout_threshold` guesses per
  backoff window against three valid codes in 10^6.
- **Related:** task 1.7; D-010, D-047, D-082, D-085, D-086, D-106, D-107;
  tasks 1.8, 1.9, 5.5, 6.1; `internal/auth/{totp,recovery_codes,qrsvg}.go`,
  `internal/crypto/mac.go`, `internal/db/migrations/00007_totp_recovery_codes.sql`,
  `internal/db/queries/totp.sql`, `internal/graph/schema/totp.graphqls`,
  `internal/graph/resolvers/totp.go`, `internal/app/totp.go`, `NOTICE`.

## D-112: 2FA policy, two-step login, forced enrollment and step-up (task 1.9)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (task 1.9); the default policy ("admin-configurable,
  off by default") is the user's decision
- **Context:** The plan asks for an admin-configurable 2FA policy (global
  and per role, with an enrollment grace period), a two-step login, forced
  enrollment, step-up (≤ 5 min) for sensitive operations and the "Restore
  2FA" rule (every restore needs a fresh passkey/TOTP check, whatever the
  policy). D-107 made a password login count as step-up because no second
  factor existed yet and left the revisit to this task; D-109/D-111 left
  the last-factor guard (`FactorRemovalGuard`) and failure counting of
  passkey assertions to it. D-095: passkeys are unavailable on plain http.
- **Decision:**
  - **Policy storage:** `settings` key `security.two_factor_policy`, JSON
    `{version: 1, require_all, roles, grace_period_days, since}`; `since`
    records per role when the requirement started (turning it on sets now,
    keeping it keeps the start, turning it off drops it). The grace period
    (0-90 days) runs from `max(since[role], users.created_at,
    users.mfa_reset_at)`. **Default (no row): no role requires 2FA; the
    grace period is 7 days** and applies once an admin requires 2FA for a
    role (existing accounts then count from that moment). This default is
    the user's decision ("admin-configurable, off by default"); the
    hardening guide tells admins to enable it right after the first
    admin exists. TOTP always works, so an enabled policy is always
    satisfiable. GraphQL
    `twoFactorPolicy` (admin, `mfa_policy.view`) and
    `updateTwoFactorPolicy` (admin + `@stepUp`, `mfa_policy.update`),
    audited as `mfa_policy.updated` with old/new values and `weakened`; a
    weakening change (a role no longer required, longer grace) is logged
    at WARN and reported to `TwoFactorPolicyNotifier` (alert in task 5.5).
    Policy reads for the per-request check are cached for 5 s per
    instance (an update invalidates the local cache at once).
  - **Two-step login:** `login` of an account with at least one factor
    creates a session with the new column `sessions.mfa_pending` (AAL1,
    no `stepup_at`, absolute expiry 5 min; a CHECK constraint forbids
    pending sessions with AAL2 or step-up/second-factor times) and answers
    `secondFactorRequired` with the factor kinds (recovery codes while
    unused ones are left; passkeys only when available). Such a principal
    (`Principal.MFAPending`) may only run `account.second_factor_login`
    (`beginPasskeyLogin`, `completeSecondFactor`) and `logout`; everything
    else answers the new code **`SECOND_FACTOR_REQUIRED`**, and the
    WebSocket upgrade answers 401. `completeSecondFactor(method, code |
    assertion)` verifies TOTP / a recovery code / a passkey assertion
    (bound to the pending session) and rotates the session
    (`CompleteSecondFactorTx`: old row revoked with a `rotated` event →
    WebSockets close 4001; new token, AAL2, `stepup_at` = now, a full
    absolute lifetime). Every rejected factor, including passkey
    assertions (wrapped as `auth.ErrAssertionRejected`), counts towards
    the second-factor lockout of D-111; a successful passkey resets it.
    Audit: `auth.login.succeeded` (with `second_factor_pending`) for the
    password step, `auth.login.second_factor` (method, session ids; never
    the code) for the second.
  - **Recovery codes are a fallback, not a full factor:** completing a
    login or a step-up with one gives AAL2 / a fresh step-up but **no
    `aal2_at`**, so it never satisfies the restore check; it is audited
    (`auth.recovery_code.used`) and raises the existing
    `RecoveryCodeUsed` alert hook.
  - **Step-up** (`beginStepUp`, `completeStepUp(method, code | password |
    assertion)`, `account.step_up`): accounts with a factor must use one
    (TOTP or passkey: `stepup_at` and `aal2_at` = now; an AAL1 session is
    elevated with rotation, an AAL2 one updated in place; recovery code:
    `stepup_at` only); the password alone is refused. Accounts without a
    factor step up by re-entering the password (a wrong one counts
    towards the login lockout, audited `auth.stepup.failed`). Successes are
    audited `auth.stepup.succeeded` with the method.
  - **Supersedes D-107 "Login = step-up"** for accounts with a factor: the
    password step sets no step-up; only the completed second factor does.
    For accounts without a factor a login still counts as a fresh step-up
    (the password was just entered and is their only credential;
    requiring it again within seconds adds nothing). The `@stepUp`
    directive and `FreshStepUp` work unchanged with the configurable
    window (≤ 5 min).
  - **Restore rule (checked):** `FreshAAL2` needs AAL ≥ 2 **and** an
    `aal2_at` within the step-up window (not in the future); password and
    recovery-code step-ups never set `aal2_at`; without a factor the
    answer is `REQUIRES_2FA_ENROLLMENT`, independent of the policy.
  - **Forced enrollment:** the session middleware applies the policy to
    every principal (`TwoFactorPolicies.ApplyEnrollment` via
    `httpserver.SessionsResolver.Enrollment`; a lookup error answers 503).
    After the grace period an account of a required role without a factor
    gets `Principal.EnrollmentRequired`: only `account.view`,
    `account.logout`, `account.step_up` (password) and
    `account.second_factor_edit` (enroll TOTP or a passkey) are allowed;
    everything else answers `REQUIRES_2FA_ENROLLMENT`. Within the grace
    period access is full and `User.twoFactorEnrollmentDeadline` /
    `twoFactorRequired` report the standing; `LoginResult` has
    `twoFactorEnrollmentRequired`. The temporary-password restriction of
    D-110 uses the same mechanism (`account.step_up` added to its allowed
    set); when both apply, the password change comes first.
  - **Last-factor guard:** `TwoFactorPolicies.CheckFactorRemoval` refuses
    removing the last factor while the policy requires 2FA for the user's
    role (also during the grace period) with **CONFLICT** (instead of the
    FORBIDDEN of D-109/D-111) and records `auth.mfa.removal_refused`;
    otherwise removal works as before (step-up, `auth.mfa.removed`,
    `SecondFactorRemoved` / passkey `EventRemoved` alert hooks).
  - **Lost-factor recovery:** `UserAdmin.ResetSecondFactors` removes every
    second factor of an account in one transaction under the user row
    lock: TOTP (also a pending enrollment), passkeys and open WebAuthn
    ceremonies, all recovery codes; it clears the second-factor lockout,
    sets `users.mfa_reset_at = now()` (a new enrollment grace period
    starts then, so a reset account of a required role is not locked out
    at once unless the grace period is 0) and revokes every session
    (`RevokeAllTx`, WebSockets close). API:
    `resetUserSecondFactors(userId)` (admin, `@stepUp`, existing
    operation `user.second_factor_reset`; the own account answers
    FORBIDDEN: an admin removes their own factors one by one with step-up
    or asks another admin; unknown or deleted accounts NOT_FOUND),
    audited as `user.second_factors_reset` with counts only. Break-glass:
    `bekup admin reset-2fa --email` (warning on stderr, audited as
    `cli.admin.mfa_reset` with actor `cli`). Every reset is logged at WARN
    and reported to `FactorResetNotifier` (alert in task 5.5).
  - **Migration 00010** adds `mfa_pending` and `users.mfa_reset_at` and
    revokes active AAL1 sessions of accounts that already have a factor
    (they were signed in by the password alone before this change).
- **Alternatives considered:** keeping "login = step-up" for everyone
  (a stolen password would grant step-up for accounts with a factor);
  requiring a password re-entry for step-up of accounts without a factor
  right after login (no security gain); computing the enrollment
  restriction at login and storing it on the session (an admin's policy
  change or the deadline passing would not apply to running sessions);
  default policy "admins required" (the agent's original proposal; the
  user chose off by default so that the operator decides when to require
  it); restarting the grace period with a new flag instead of a
  timestamp (a timestamp reuses the existing deadline logic and is
  auditable); letting an admin reset their own factors (a stolen admin
  session with a fresh password step-up could strip the factors);
  letting recovery codes satisfy restores (a printed code would
  replace the authenticator for the most dangerous operation); a
  separate pre-auth cookie for the pending state (a second cookie
  lifecycle; a flagged session row reuses rotation, revocation and the
  WebSocket events); not counting passkey assertion failures (unbounded
  guessing is impossible anyway, but counting keeps one lockout for all
  second factors).
- **Consequences:** Accounts with a factor need it at every login and for
  every step-up; a stolen password alone yields a 5-minute session that
  can do nothing. Out of the box no account is forced to enroll (the
  user's decision; weaker than requiring it for admins, mitigated by the
  hardening guide and by restores always needing a passkey/TOTP check);
  once an admin requires 2FA for a role, its accounts without a factor
  get the grace period from that moment. A user who lost every factor
  signs in with a recovery code (but cannot restore) or gets the factors
  reset by another admin or the CLI; a reset account signs in with its
  password alone until it enrolls again, so resets are audited, logged
  and alerted and should follow an out-of-band identity check. Policy changes reach other
  instances within 5 s. An open WebSocket keeps the principal of its
  upgrade, so an enrollment deadline passing during a socket only affects
  it at the next reconnect (it only carries the user's own session
  events).
- **Related:** task 1.9; D-010, D-047, D-057, D-095, D-106, D-107, D-108,
  D-109, D-110, D-111; tasks 5.5, 6.1, 6.8;
  `internal/auth/{two_factor_policy,second_factor_flow,session_store,service,user_admin}.go`,
  `cmd/bekup/admin_cmd.go`, `internal/db/queries/user_admin.sql`,
  `internal/graph/schema/users.graphqls`,
  `internal/authz/policy.go`, `internal/db/migrations/00010_two_factor_policy.sql`,
  `internal/graph/schema/twofactor.graphqls`,
  `internal/graph/resolvers/twofactor.go`, `internal/httpserver/session.go`,
  `internal/app/two_factor.go`.

## D-113: First admin created via CLI only; web setup flow removed

- **Date:** 2026-10-26
- **Status:** accepted (migration 00011 detail superseded by D-118)
- **Decided by:** user
- **Context:** D-110 (task 1.6) created the first admin through a public
  web flow: `serve` logged a one-time setup code at every start while no
  admin existed, and the public `completeSetup` mutation exchanged it for
  the first admin. Its weak points: anyone who can read the `serve` log
  during that hour can take over the instance, two public root fields
  exist only for a one-time event, and the flow needs its own state
  table, rate limiter, audit actions and UI page. `bekup admin create`
  (D-069, D-110) already creates admins on the host.
- **Decision:** supersedes the setup-code part of D-110 (the rest of
  D-110 stays):
  - The first admin is created **only** with `bekup admin create --email
    ... --display-name ...` on the host (password from a hidden terminal
    prompt or `--password-file`, never a flag), audited as
    `cli.admin.created` with actor `cli`, OS user and hostname. The
    command itself is unchanged.
  - Removed: `Query.setupStatus` and `Mutation.completeSetup` (schema,
    `graph.PublicRootFields`), `auth.Setup` and the setup-code generation
    at serve startup, its per-IP limiter, the audit actions
    `setup.code_issued` / `setup.failed` / `setup.completed`, and the
    config key `security.setup_token_ttl` (unreleased, so no deprecation
    period; a config that still sets it fails strict parsing with an
    unknown-key error).
  - **`setup_state` is dropped** by the new migration 00011 (00009 is
    not edited): no code reads it, and an unused table on which
    `bekup_app` keeps UPDATE is surface without purpose. Dropping it is
    safe: it only held a code hash and a completion mark, the first
    admin's creation is in the audit log, and the down migration
    recreates the 00009 objects.
  - When `serve` starts and no **active** admin (not disabled, not
    deleted) exists, it logs one WARN `NO ADMIN ACCOUNT ...` with the
    exact command (`bekup admin create --email <address> --display-name
    <name> [--password-file <path>]`, plus the compose form) and keeps
    running: nobody can sign in until an admin exists, the UI shows only
    the login page.
- **Alternatives considered:** keeping the web setup code (log readers
  can take over the instance; public attack surface); an
  environment-variable or config bootstrap password (a secret outside
  `*_file` handling, easy to leave behind); refusing to start `serve`
  without an admin (breaks health checks and compose ordering, and the
  CLI works against the database anyway); keeping `setup_state` unused
  (dead, writable table).
- **Consequences:** First-run takeover through the web is no longer
  possible (SV-3); creating the first admin needs host access with the
  config and DB credentials (the same trust as the existing break-glass
  CLI, T10). Installation needs one extra documented CLI step. The web UI
  (task 6.1) has no setup page. Security impact: smaller public surface,
  no secret in logs.
- **Related:** D-069, D-110, D-112; tasks 1.6, 1.12, 6.1;
  `internal/app/users.go`, `internal/auth/user_admin.go`,
  `internal/db/migrations/00011_drop_setup_state.sql`,
  `internal/db/queries/user_admin.sql`, `internal/graph/access.go`,
  `cmd/bekup/admin_cmd.go`, `docs/getting-started.md`,
  `docs/operations.md`, `SECURITY.md`.

## D-114: just dev runs infra only (postgres, mailpit, adminer); app runs natively

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-26, user decision)
- **Decided by:** user
- **Context:** Under D-081 `just dev` started the whole containerised stack
  (air serve, runner, sample targets, MinIO, SFTP, bastion, mailpit). For
  the current milestones that is slow to start and to rebuild, and it hides
  the app behind a container build. The first admin is now created with the
  CLI only (`bekup admin create`), which needs a native process that can
  reach the app DB.
- **Decision:** This supersedes the part of D-081 that makes the full stack
  the default dev workflow; the hardening rules of D-081 stay.
  1. `docker-compose.dev.yml` uses compose profiles. The default (no
     profile) starts only the app Postgres (TLS, hostssl-only for the bekup
     roles, SCRAM with channel binding, superuser socket-only, unchanged),
     mailpit and adminer, all published on `127.0.0.1` only: Postgres on
     `127.0.0.1:5432` (DEV ONLY; production never publishes it), mailpit
     SMTP `1025` and UI `8025`, adminer `8081`. Ports can be moved with
     `BEKUP_DEV_PG_PORT`, `BEKUP_DEV_SMTP_PORT`, `BEKUP_DEV_MAILPIT_PORT`,
     `BEKUP_DEV_ADMINER_PORT`. Published ports need a non-internal network,
     so Postgres and adminer also join a dev-only `dev-host` network.
  2. Profile `full` (`just dev-full`) holds air serve, migrate, the runner,
     the sample targets, MinIO, SFTP and the bastion, unchanged.
  3. The app runs natively: `just dev` generates missing dev keys, starts
     the default profile, waits for health and migrates natively;
     `just run` builds `bin/bekup-dev`, migrates and runs `bekup serve` in
     dev mode on `http://localhost:8080` (D-095 warning);
     `just dev-migrate`, `just admin-create <email> <name>` (password
     prompted on the terminal), `just web-dev` (Vite against :8080),
     `just dev-down`. The native config is `deploy/dev/bekup.local.toml`;
     the absolute `*_file` paths come from environment overrides set by the
     justfile, and only the migration step receives the owner URL file.
  4. Native processes verify the DB with `verify-full` against the dev CA:
     `gen-dev-keys.sh` issues the dev postgres certificate with the extra
     SANs `localhost` and `127.0.0.1` (`--postgres-san`; an older dev PKI
     is rotated or regenerated) and writes `secrets/local/` (role URLs with
     `127.0.0.1:<port>`, copies of the master keyring and DB CA, owned by
     the developer so `--chown` cannot break native use). `BEKUP_DEV_KEYS_DIR`
     moves `./secrets` for the script and the compose dev override.
  5. Adminer (official image, pinned by digest) runs read-only with all
     capabilities dropped and `no-new-privileges`; its login form offers
     only the app DB (`login-servers` plugin); libpq (PG 18) connects with
     `PGSSLMODE=verify-full`, the dev CA and `PGCHANNELBINDING=require`.
     Logins: `bekup_app` or `bekup_owner` with the dev passwords, database
     `bekup`; the superuser cannot log in over TCP by design (D-096).
  6. `deploy/postgres/entrypoint.sh` checks TLS files by reading them
     instead of `test -r` (Docker Desktop shows bind mounts as root-owned
     0600 yet lets UID 70 read them).
- **Alternatives considered:** keep the full stack as the default (slow,
  container rebuilds for every Go change); a one-shot `migrate` container in
  the default profile (the native `bekup migrate` already covers it and
  tests the same TLS path as `serve`); publishing Postgres without TLS for
  dev (rejected: dev would no longer exercise D-100/D-104); `sslmode=require`
  from the host (rejected: no server authentication); adminer with a free
  server field (rejected: a generic proxy into the dev networks).
- **Consequences:** Fast edit/run cycles with the real TLS and role setup.
  Security impact: in dev only, the app DB listens on host loopback; it
  still accepts the bekup roles only over TLS with SCRAM and never the
  superuser over TCP, so a local process needs a role password. Dev key
  files for native use live in `secrets/local/` (0600, 0700 directory).
  The dev postgres certificate (and the dev CA's name constraints) include
  `localhost`/`127.0.0.1`. Proxy profiles in dev need `--profile full`.
  Production files and behaviour are unchanged.
- **Related:** D-081 (partly superseded), D-095, D-096, D-100, D-104, D-110;
  task 1.13; `docker-compose.dev.yml`, `deploy/dev/{gen-dev-keys.sh,bekup.local.toml,adminer/}`,
  `deploy/postgres/entrypoint.sh`, `justfile`, `scripts/lib.bash`,
  `docs/development.md`, `docs/getting-started.md`.

## D-115: Compose: one-shot admin service and BEKUP_KEYS_DIR (task 1.11)

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval
- **Context:** The end-to-end verification of the compose stack (task 1.11)
  found two gaps. (1) The documented way to create the first admin with
  compose (D-113), `docker compose run --rm -it serve admin create ...`,
  fails while `serve` runs: the run container inherits `serve`'s fixed
  `frontend` address (`SERVE_FRONTEND_IP`, needed for `[server] listen`)
  and Docker refuses with "Address already in use". The same hint was in
  serve's NO ADMIN ACCOUNT warning. (2) `docker-compose.yml` could only
  read its secret files from `./secrets`, so the key material always had to
  live inside the project directory, and the stack could not be tested with
  throwaway keys elsewhere.
- **Decision:**
  1. `docker-compose.yml` gets a one-shot service `admin` (profile `admin`,
     so `up` never starts it; `docker compose run --rm admin <subcommand>`
     enables it) with entrypoint `bekup admin`. It has the bekup hardening
     baseline, only the `backend` network, `bekup.toml`, the app DB URL and
     the DB CA; no master key, no mTLS client certificate, no ports, no
     fixed address. It waits for `migrate`. `--password-file` works by
     mounting the file with `-v`. serve's warning and the docs
     (getting-started, operations, compose) now name this command; the dev
     override builds the same service.
  2. Every secret path in `docker-compose.yml` (and the `certs-init` bind
     mount) is `${BEKUP_KEYS_DIR:-./secrets}/...`. The default is
     unchanged.
  3. Static proxy certificates in production are mounted through a
     `docker-compose.override.yml` (documented in reverse-proxy.md); the
     shipped file does not mount `/certs`.
- **Alternatives considered:** `docker compose exec serve bekup admin ...`
  (works only while serve runs and cannot read a `--password-file` from the
  host); dropping serve's fixed address (it is what lets `listen` bind to
  the `frontend` interface only, D-098); `run --no-deps` with a different
  network (compose `run` cannot change networks); reusing `migrate` (it
  holds the owner credentials, which account administration must not get).
- **Consequences:** The D-113 flow works as documented with compose. The
  `admin` container holds the same `bekup_app` credentials as `serve`
  (D-054 unchanged) and less of everything else; it exists only while a
  command runs. `BEKUP_KEYS_DIR` lets operators keep key material outside
  the project directory (e.g. an encrypted volume); file ownership/mode
  rules (D-041) apply unchanged. No trust boundary changes.
- **Related:** D-041, D-054, D-069, D-098, D-110, D-113, D-114; task 1.11;
  `docker-compose.yml`, `docker-compose.dev.yml`, `internal/app/users.go`,
  `docs/installation/compose.md`, `docs/installation/reverse-proxy.md`,
  `docs/getting-started.md`, `docs/operations.md`.

## D-116: Passkey AAL2 requires user verification

- **Date:** 2026-10-26
- **Status:** accepted
- **Decided by:** user (M1 review, finding L3)
- **Context:** D-109/D-112 let every verified passkey assertion set the
  session's second-factor time (`aal2_at`), which restores require
  (`FreshAAL2`, D-057). Ceremonies ask for `userVerification: preferred`,
  so an authenticator may answer with user presence only (a touch, no PIN
  or biometrics): whoever holds the unlocked key could then satisfy the
  restore gate.
- **Decision:** Only TOTP or a passkey assertion whose signed
  authenticator data carries the User Verified (UV) flag sets `aal2_at`.
  A user-presence-only assertion still completes the two-step login (the
  session becomes AAL2 with a fresh step-up, like a recovery code) and a
  step-up (step-up time only; an AAL1 session is not raised). Ceremonies
  keep `userVerification: preferred`. `passkey.Service.VerifyAssertion`
  returns `auth.AssertionResult{UserVerified}`; the audit entries of the
  login and step-up record `second_factor_time`.
- **Alternatives considered:** requiring UV for every passkey use
  (locks out authenticators without PIN/biometrics); keeping the D-109
  behavior (a touch would be enough for restores).
- **Consequences:** Restores need TOTP or a UV passkey within the step-up
  window; users with UP-only keys are asked to step up with TOTP or a
  PIN-protected key. No trust boundary changes. Supersedes the AAL2 part
  of D-109.
- **Related:** D-057, D-109, D-112; `internal/auth/second_factor_flow.go`,
  `internal/auth/passkey/service.go`.

## D-117: Credential-verification hardening: single-root operations, limiters, atomic lockouts

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (M1 review findings H1, M1, M2)
- **Context:** The M1 security review found that (H1) one GraphQL request
  could alias `login`/`completeStepUp`/`changePassword` many times and run
  argon2id once per alias, bypassing the per-request limits; (M1) the
  password and second-factor lockouts checked the lock, verified, and only
  then counted the failure, so concurrent guesses could all be verified
  before the lock applied; (M2) every attempt refused while locked wrote
  an audit entry, so an attacker could flood the hash-chained log.
- **Decision:**
  1. **Single-root credential operations.** `graph.SingleRootFields`
     lists every root field that verifies, sets or resets credentials or
     runs argon2id (login, changePassword, the two-step login and step-up
     fields, TOTP/passkey enrollment and management, createUser,
     resetUserPassword, resetUserSecondFactors). An operation selecting one
     of them must select exactly one root field (aliases, repeats and
     fragments counted; `__typename` ignored), else VALIDATION before any
     resolver runs. Startup fails unless every public mutation and every
     root field of a credential operation (`graph.CredentialOperations`)
     is listed. Every root field beyond the first of any operation is
     charged to the caller's per-user rate limit
     (`httpserver.ChargeUser`).
  2. **Per-account password limiter.** Before every argon2id verification
     (login, password step-up, password change) `auth.Service` takes a
     token from a per-account limiter keyed `user:<id>` (logins for
     unknown or malformed addresses: `email:<lowercased>` / a shared key,
     so the limiter is no enumeration oracle), using the
     `security.login_rate_limit_*` rate; separate from the per-IP login
     limiter and the HTTP limiters. Over the limit: RATE_LIMITED.
  3. **Atomic lockouts.** Each password or second-factor check is
     reserved before it runs by one conditional UPDATE that counts it only
     while the account (or its second factors) is not locked and applies
     the backoff lock in the same statement when the threshold is reached
     (`ReserveLoginAttempt`, `ReserveMFAAttempt`; the DB computes
     `LockFor`). No row: refused without verifying. A success clears the
     count (also after a password step-up, which previously did not
     reset it). Concurrent guesses can never exceed the threshold.
  4. **Authenticated checks skip argon2id while locked.** A password
     step-up or password change of a locked account answers RATE_LIMITED
     with the remaining lock time without any (dummy) verification: the
     caller owns the account, so there is nothing to hide. Logins keep the
     dummy verification and the generic error.
  5. **Aggregated refusal audit.** Attempts refused while locked are
     recorded at most once per user and lock window (`aggregated: true`);
     further refusals are counted in memory and reported as
     `refused_while_locked` in the account's next failure, lockout or
     first-refusal entry. The count is lost on restart (the next refusal
     is then recorded again); the memory is bounded (10 000 entries,
     recording instead of dropping when full).
- **Alternatives considered:** a depth/complexity cost per credential
  field (still allows several per request); `SELECT ... FOR UPDATE`
  around verification (holds a row lock during argon2id); per-attempt
  audit rows with a DB-side rate limit (more schema, same effect).
- **Consequences:** Clients must send credential mutations alone (the
  web UI does). An attacker can exhaust a known account's limiter or lock
  it (as before with the lockout; sessions stay valid, D-107). Audit
  volume under attack is bounded. No trust boundary changes; the GraphQL
  and auth rules in SECURITY.md and AGENTS.md are extended.
- **Related:** D-050, D-105, D-107, D-111, D-112; `internal/graph/rootfields.go`,
  `internal/auth/service.go`, `internal/auth/recovery_codes.go`,
  `internal/auth/locked_refusals.go`, `internal/db/queries/users.sql`,
  `internal/db/queries/totp.sql`, `internal/httpserver/session.go`.

## D-118: M1 review fixes: WebSocket principal rebuild, shutdown, posture audits and smaller items

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (M1 review findings L1, L2, L4 and code review M1, L3, L4)
- **Context:** Several smaller findings of the M1 reviews.
- **Decision:**
  1. **WebSocket re-validation rebuilds the principal** (supersedes the
     D-108 comparison of role and AAL only): the hub's validator
     (`httpserver.SessionsResolver.Check`) loads session and user by id
     and applies the 2FA enrollment check exactly like the session
     middleware. A change of user, role, AAL, pending second factor,
     temporary password or required enrollment closes the socket with
     4403; new step-up/second-factor times only refresh the connection's
     principal, which `wsconn.Deliver` puts into the context of every
     delivery-time check. An upgrade by a principal that must enroll a
     second factor is refused (403).
  2. **Hub after shutdown:** `closeAll` sets a closed flag under the hub
     lock; `Acquire` then answers `ErrHubClosed` (503), so no socket can
     slip in after the shutdown closed the others.
  3. **Login result and introspection:** `LoginResult.user` is null while
     `secondFactorRequired` is true; introspection outside dev mode is
     allowed only to unrestricted principals (not pending second factor,
     temporary password or required enrollment; refines D-050).
  4. **Trusted proxy without X-Forwarded-For:** logged as WARN at most
     every 5 minutes (health checks exempt): the proxy address is then
     every client's address.
  5. **Posture and key audits:** serve records
     `security.insecure_db_tls` at every startup outside dev mode with
     `database.tls_mode` require/disable (fail closed, like D-095);
     `bekup certs rotate` (and `certs init --force`) records
     `certs.rotated` as a CLI event, opening the app DB before touching
     any file (fail closed; `--no-audit` skips it with a warning, e.g.
     for the postgres certificate of a stopped database). The remaining
     unused catalog actions are marked reserved with their task.
  6. **Migration fold:** M1 is unreleased, so the `setup_state` objects
     are removed from migration 00009 (now `00009_user_admin.sql`) and
     00011 is deleted (supersedes the 00011 detail of D-113); the final
     schema is unchanged. Development databases from before must be
     recreated (`just db-reset`).
- **Alternatives considered:** closing sockets on every step-up (needless
  reconnects); recording `certs.rotated` best-effort (silently missing
  entries); keeping 00011 (an unreleased migration pair that only adds and
  drops a table).
- **Consequences:** Open sockets follow the same restrictions as HTTP
  requests. Operators rotating certificates need DB access or must pass
  `--no-audit` consciously. No trust boundary changes.
- **Related:** D-050, D-095, D-100, D-108, D-112, D-113; `internal/graph/wsconn`,
  `internal/graph/ws.go`, `internal/httpserver/middleware.go`,
  `internal/app/audit.go`, `cmd/bekup/certs_cmd.go`, `internal/audit/actions.go`.

## D-119: Key-check values stored per key ID and verified at startup

- **Date:** 2026-10-26
- **Status:** accepted (2026-10-27, user accepted all M1 proposals)
- **Decided by:** agent with user approval (M1 review; implements D-086)
- **Context:** D-086 and the plan require the master keyring's key-check
  value (KCV) to be stored in `settings` and verified at startup; it was
  computed by `internal/crypto` but never stored or checked. After
  `bekup keys rotate` the keyring has a new primary key, so a single
  stored KCV of the old primary would no longer describe the ring.
- **Decision:** `settings` key `security.key_check_values` holds
  `{"kcvs": [...]}`, one KCV per master key ID the database has seen.
  serve checks it at startup before anything is decrypted, inside one
  transaction (row lock):
  - none stored: the KCVs of every key in the ring are stored
    (insert-if-absent; a concurrent first start re-checks), audited as
    `keys.check_value_stored`;
  - stored: every stored KCV whose key is in the ring must verify and at
    least one must; otherwise serve refuses to start ("the master key does
    not match this database"). A stored key missing from the ring is
    tolerated with a warning (retired after re-encryption);
  - ring keys without a stored KCV (after `bekup keys rotate`) are added
    and recorded as `keys.rotated`.
- **Alternatives considered:** one KCV of the primary only (breaks after
  rotation or needs a CLI update with DB access); verifying against any key
  without storing new ones (rotations would go unrecorded).
- **Consequences:** A wrong, truncated or foreign keyring stops serve with
  a clear error instead of failing later at decryption. KCVs reveal
  nothing about the encryption subkeys (D-086). No trust boundary changes.
- **Related:** D-046, D-085, D-086, D-101; `internal/app/keycheck.go`,
  `internal/crypto/kcv.go`, `docs/security/key-management.md`.
