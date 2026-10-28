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
| [D-052](#d-052-websocket-subscriptions) | WebSocket subscriptions | accepted (transport superseded by D-124) |
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
| [D-103](#d-103-app-db-tls-details-url-handling-require--ca-channel-binding-postgres-certificate-and-wrapper-task-10) | App DB TLS details: URL handling, require + CA, channel binding, postgres certificate and wrapper (task 1.0) | accepted (item 3 superseded by D-104) |
| [D-104](#d-104-app-db-tls-ecdsa-postgres-issuer-and-required-scram-channel-binding) | App DB TLS: ECDSA postgres issuer and required SCRAM channel binding | accepted |
| [D-105](#d-105-graphql-server-details-transport-limits-access-convention-error-codes-task-12) | GraphQL server details: transport, limits, access convention, error codes (task 1.2) | accepted |
| [D-106](#d-106-authorization-model-operation-catalog-policy-matrix-and-owner-scope-task-14) | Authorization model: operation catalog, policy matrix and owner scope (task 1.4) | accepted (job.encryption_keys_edit roles superseded by D-154) |
| [D-107](#d-107-passwords-sessions-lockout-and-the-login-api-task-15) | Passwords, sessions, lockout and the login API (task 1.5) | accepted ("Login = step-up" superseded by D-112; lockout counting superseded by D-117) |
| [D-108](#d-108-websocket-subscription-transport-session-watcher-and-session-event-bus-task-110) | WebSocket subscription transport, session watcher and session event bus (task 1.10) | accepted (re-validation details superseded by D-118; transport, caps, close codes and web client superseded by D-124) |
| [D-109](#d-109-webauthn-passkeys-as-a-second-factor-task-18) | WebAuthn passkeys as a second factor (task 1.8) | accepted (last-factor error code superseded by D-112; AAL2 without user verification superseded by D-116) |
| [D-110](#d-110-first-run-setup-code-user-administration-and-break-glass-cli-task-16) | First-run setup code, user administration and break-glass CLI (task 1.6) | accepted (setup-code part superseded by D-113) |
| [D-111](#d-111-totp-recovery-codes-and-second-factor-failures-task-17) | TOTP, recovery codes and second-factor failures (task 1.7) | accepted (last-factor error code superseded by D-112) |
| [D-112](#d-112-2fa-policy-two-step-login-forced-enrollment-and-step-up-task-19) | 2FA policy, two-step login, forced enrollment and step-up (task 1.9) | accepted |
| [D-113](#d-113-first-admin-created-via-cli-only-web-setup-flow-removed) | First admin created via CLI only; web setup flow removed | accepted (migration 00011 detail superseded by D-118) |
| [D-114](#d-114-just-dev-runs-infra-only-postgres-mailpit-adminer-app-runs-natively) | `just dev` runs infra only (postgres, mailpit, adminer); app runs natively | accepted |
| [D-115](#d-115-compose-one-shot-admin-service-and-bekup_keys_dir-task-111) | Compose: one-shot `admin` service and `BEKUP_KEYS_DIR` (task 1.11) | accepted |
| [D-116](#d-116-passkey-aal2-requires-user-verification) | Passkey AAL2 requires user verification | accepted |
| [D-117](#d-117-credential-verification-hardening-single-root-operations-limiters-atomic-lockouts) | Credential-verification hardening: single-root operations, limiters, atomic lockouts | accepted |
| [D-118](#d-118-m1-review-fixes-websocket-principal-rebuild-shutdown-posture-audits-and-smaller-items) | M1 review fixes: WebSocket principal rebuild, shutdown, posture audits and smaller items | accepted |
| [D-119](#d-119-key-check-values-stored-per-key-id-and-verified-at-startup) | Key-check values stored per key ID and verified at startup | accepted |
| [D-120](#d-120-ssrf-guard-details-range-tables-allowlist-limits-no-proxy-error-normalization-task-23) | SSRF guard details: range tables, allowlist limits, no proxy, error normalization (task 2.3) | accepted |
| [D-121](#d-121-storage-interface-object-keys-and-the-disk-backend-task-28) | Storage interface, object keys and the disk backend (task 2.8) | accepted |
| [D-122](#d-122-ssh-tunnel-client-algorithms-host-key-pin-format-mandatory-dialer-task-22) | SSH tunnel client: algorithms, host-key pin format, mandatory dialer (task 2.2) | accepted (amended by D-140) |
| [D-123](#d-123-engine-catalog-client-floors-tls-modes-and-argv-builder-details-task-21) | Engine catalog, client floors, TLS modes and argv builder details (task 2.1) | accepted |
| [D-124](#d-124-live-events-over-sse-no-websocket) | Live events over SSE, no WebSocket | accepted |
| [D-125](#d-125-runner-container-healthcheck-over-a-local-health-socket-task-24) | Runner container healthcheck over a local health socket (task 2.4) | accepted |
| [D-126](#d-126-runner-api-proto-conventions-and-code-generation-task-24) | Runner API proto conventions and code generation (task 2.4) | accepted |
| [D-127](#d-127-runner-grpc-transport-policy-interceptors-limits-keepalive-client-task-24) | Runner gRPC transport policy: interceptors, limits, keepalive, client (task 2.4) | accepted |
| [D-128](#d-128-sftp-backend-host-key-pin-hard-link-finalize-symlink-checks-connection-policy-task-210) | SFTP backend: host-key pin, hard-link finalize, symlink checks, connection policy (task 2.10) | accepted (amended by D-140) |
| [D-129](#d-129-s3-backend-own-multipart-uploader-static-credentials-only-conditional-writes-version-exact-deletes-task-29) | S3 backend: own multipart uploader, static credentials only, conditional writes, version-exact deletes (task 2.9) | accepted |
| [D-130](#d-130-runner-request-validation-target-allowlist-hardened-container-spec-and-the-stdin-preamble-task-25) | Runner request validation, target allowlist, hardened container spec and the stdin preamble (task 2.5) | accepted |
| [D-131](#d-131-destinations-api-storage-of-credentials-step-up-for-every-change-and-the-sftp-host-key-flow-task-212) | Destinations API: storage of credentials, step-up for every change and the SFTP host-key flow (task 2.12) | accepted (amended by D-140) |
| [D-132](#d-132-runner-docker-executor-per-run-network-plan-ip-literal-targets-proxy-kill-and-reaper-policy-task-26) | Runner Docker executor: per-run network plan, IP-literal targets, proxy, kill and reaper policy (task 2.6) | accepted |
| [D-133](#d-133-target-db-certificate-pinning-capture-leaf-spki-pre-flight-trust-anchor-and-mismatch-signalling-task-27a) | Target-DB certificate pinning: capture, leaf-SPKI pre-flight, trust anchor and mismatch signalling (task 2.7a) | accepted (amended by D-138, D-139) |
| [D-134](#d-134-runner-rpcs-go-driver-pre-checks-normalized-target-errors-offline-verify-and-the-globals-fallback-task-27) | Runner RPCs: Go-driver pre-checks, normalized target errors, offline verify and the globals fallback (task 2.7) | accepted |
| [D-135](#d-135-connections-api-storage-pin-and-host-key-flows-tls-downgrade-and-runner-wiring-task-211) | Connections API: storage, pin and host-key flows, TLS downgrade and runner wiring (task 2.11) | accepted (amended by D-140) |
| [D-136](#d-136-s3-multipart-complete-detached-from-the-caller-and-verified-after-an-ambiguous-failure-m2-review) | S3 multipart Complete detached from the caller and verified after an ambiguous failure (M2 review) | accepted |
| [D-137](#d-137-docker-dependent-tests-skip-locally-but-fail-in-ci-m2-review) | Docker-dependent tests skip locally but fail in CI (M2 review) | accepted |
| [D-138](#d-138-runner-hardening-after-the-m2-review-image-volumes-psql-restrict-docker-networks-bastion-targets-docker-floor-and-rpc-fixes) | Runner hardening after the M2 review: image volumes, psql restrict, Docker networks, bastion targets, Docker floor and RPC fixes | accepted |
| [D-139](#d-139-pin-anchors-are-a-self-signed-leaf-or-a-private-ca-the-same-ca-leaf-window-is-accepted-residual-risk) | Pin anchors are a self-signed leaf or a private CA; the same-CA leaf window is accepted residual risk | accepted |
| [D-140](#d-140-ssh-policy-and-host-names-in-one-place-endpoint-bound-host-key-and-pin-confirmation-no-password-reuse-after-retargeting-m2-review) | SSH policy and host names in one place, endpoint-bound host-key and pin confirmation, no password reuse after retargeting (M2 review) | accepted |
| [D-141](#d-141-backup-completeness-evidence-source-stats-and-test-restore-comparison) | Backup completeness evidence: source stats and test-restore comparison | accepted |
| [D-142](#d-142-job-encryption-keys-tables-versioning-aad-authorization-split-and-the-service-api-task-32) | Job encryption keys: tables, versioning, AAD, authorization split and the service API (task 3.2) | accepted (item 5 key-change roles superseded by D-154) |
| [D-143](#d-143-schedule-format-and-next-run-math-anchored-intervals-wall-clock-dst-rules-for-cron-catch-up-decision-schedule-limits) | Schedule format and next-run math: anchored intervals, wall-clock DST rules for cron, catch-up decision, schedule limits | accepted |
| [D-144](#d-144-backup-pipeline-bounded-fan-out-with-lag-drop-artifact-naming-and-the-signed-manifest-format-task-31) | Backup pipeline: bounded fan-out with lag drop, artifact naming and the signed manifest format (task 3.1) | accepted |
| [D-145](#d-145-scheduler-loop-and-river-setup-minimal-jobs-table-skip-records-kind-catalog-ids-only-args-queue-timeouts-and-process-model-task-34) | Scheduler loop and River setup: minimal jobs table, skip records, kind catalog, IDs-only args, queue timeouts and process model (task 3.4) | accepted |
| [D-146](#d-146-backup-jobs-data-model-service-ownership-queries-delete-in-use-checks-and-the-graphql-api-task-35) | Backup jobs: data model, service, ownership queries, delete-in-use checks and the GraphQL API (task 3.5) | accepted |
| [D-147](#d-147-backup-run-worker-run-tables-streamed-verification-concurrency-slots-cancellation-and-run-logs-task-36) | Backup run worker: run tables, streamed verification, concurrency slots, cancellation and run logs (task 3.6) | accepted |
| [D-148](#d-148-retention-worker-per-destination-policy-completeness-safety-checks-object-lock--no-delete-interplay-and-audit-shape-task-38) | Retention worker: per-destination policy, completeness, safety checks, Object Lock / no-delete interplay and audit shape (task 3.8) | accepted |
| [D-149](#d-149-run-history-queries-and-live-run-events-scoped-reads-per-operation-batch-loader-delivery-time-re-load-and-log-streams-task-37) | Run history queries and live run events: scoped reads, per-operation batch loader, delivery-time re-load and log streams (task 3.7) | accepted |
| [D-150](#d-150-download-tickets-single-use-session-bound-tickets-use-time-re-check-manifest-pre-verification-held-back-tail-and-sealed-identity-handoff-task-39) | Download tickets: single-use session-bound tickets, use-time re-check, manifest pre-verification, held-back tail and sealed identity handoff (task 3.9) | accepted |
| [D-151](#d-151-completeness-evidence-stats-inside-the-dump-call-same-snapshot-postgresql-counts-warnings-model-and-backup_stats-task-310) | Completeness evidence: stats inside the Dump call, same-snapshot PostgreSQL counts, warnings model and backup_stats (task 3.10) | accepted |
| [D-152](#d-152-hostile-target-databases-pinned-session-settings-schema-qualified-sql-and-relation-kind-restriction-for-every-go-driver-session) | Hostile target databases: pinned session settings, schema-qualified SQL and relation-kind restriction for every Go-driver session | accepted (item 6 superseded by D-153) |
| [D-153](#d-153-mysqlmariadb-statistics-are-estimates-only-no-statement-on-user-tables-or-views) | MySQL/MariaDB statistics are estimates only: no statement on user tables or views | accepted |
| [D-154](#d-154-job-encryption-key-changes-are-admin-only-m3-review) | Job encryption key changes are admin only (M3 review) | accepted |
| [D-155](#d-155-m3-review-fixes-artifact-holds-run-notices-byte-bounded-warnings-exact-manifest-signing-scheduler-savepoints-retry-cancellation-and-coalesced-run-events) | M3 review fixes: artifact holds, run notices, byte-bounded warnings, exact manifest signing, scheduler savepoints, retry cancellation and coalesced run events | accepted |
| [D-156](#d-156-job-calendar-start-early-per-connection-run-limit-and-enabledisable) | Job calendar, start early, per-connection run limit and enable/disable | accepted |
| [D-157](#d-157-start-the-next-planned-run-early-semantics-details-task-312) | Start the next planned run early: semantics details (task 3.12) | accepted (amended by D-160) |
| [D-158](#d-158-per-connection-run-limit-details-task-313) | Per-connection run limit: details (task 3.13) | accepted |
| [D-159](#d-159-job-calendar-query-details-task-314) | Job calendar query: details (task 3.14) | accepted (amended by D-160) |
| [D-160](#d-160-m3-addendum-review-fixes-one-early-start-ahead-calendar-catch-up-time-prefix-truncation-and-bounded-reads) | M3 addendum review fixes: one early start ahead, calendar catch-up, time-prefix truncation and bounded reads | accepted |
| [D-161](#d-161-notification-event-catalog-and-per-user-defaults) | Notification event catalog and per-user defaults | accepted |

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
- **Status:** accepted (transport superseded by D-124: subscriptions run as Server-Sent Events; the goals stand)
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
- **Status:** accepted (2026-10-27, user accepted all M1 proposals); the roles of `job.encryption_keys_edit` superseded by D-154
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
- **Status:** accepted (2026-10-27, user accepted all M1 proposals) (re-validation details superseded by D-118) (transport, admission caps, close codes, web client and CSP parts superseded by D-124; the session watcher, session event bus and delivery-time authz stand)
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

## D-120: SSRF guard details: range tables, allowlist limits, no proxy, error normalization (task 2.3)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval
- **Context:** D-055 fixes the dial-time SSRF guard but not its range
  table, what the allowlist may contain, how proxies, redirects and error
  messages are handled, or how the runtime (admin UI) allowlist combines
  with `[netguard]` in the TOML.
- **Decision:**
  - **Deny table.** Every address is unmapped (`::ffff:a.b.c.d`) and its
    zone dropped before classification. *Hard* (never allowlistable):
    0.0.0.0/8, ::, loopback (127/8, ::1) outside dev mode, link-local
    (169.254/16, fe80::/10), cloud metadata (169.254.169.254,
    100.100.100.200, fd00:ec2::254), multicast, broadcast, 240/4,
    IPv4-compatible `::/96`, IPv4-translated `::ffff:0:0:0/96`, Teredo
    2001::/32, all IPv6 outside 2000::/3 except the soft ranges below, and
    NAT64 64:ff9b::/96 or 6to4 2002::/16 addresses whose embedded IPv4
    address is denied. *Soft* (allowlistable): RFC 1918, 100.64/10,
    192.0.0/24, documentation and benchmarking ranges, 192.88.99/24,
    fc00::/7, fec0::/10, 100::/64, 64:ff9b:1::/48, 2001::/23, 3fff::/20,
    5f00::/16.
  - **Allowlist.** CIDRs and host names (exact or `*.suffix` with at least
    two labels; IP literals rejected). CIDRs that are /0, cover the whole
    mapped range, overlap a hard range (except a metadata address strictly
    inside, e.g. 100.64.0.0/10, which stays denied because classification
    runs first), lie outside the allowlistable IPv6 ranges or overlap 6to4
    are rejected at load. Host entries match the name the caller dialed
    and re-allow soft-denied addresses only. The effective list is the
    TOML bootstrap list plus the runtime list, swapped atomically by
    `Guard.SetAllowlist` after validation. Loopback is allowlistable only
    when `dev.enabled` (FromConfig).
  - **Dialing.** `Guard.DialContext` (tcp/tcp4/tcp6 only) resolves on
    every call (no caching), tries at most 8 addresses with one overall
    timeout, and connects to IP literals through a `net.Dialer` whose
    `Control` hook re-checks the exact socket address. `Guard.Dialer()`
    exposes the hook for libraries that need a `*net.Dialer` (CIDR
    entries only). `ResolveAndCheck` validates input when it is entered.
  - **HTTP.** `NewHTTPTransport` has `Proxy: nil`: **no egress-proxy
    support**, because a proxy resolves and connects to the target itself
    and would bypass the dial-time check. TLS ≥ 1.2, HTTP/2, handshake 10s,
    response headers 30s, 64 KiB header cap. `NewHTTPClient` never follows
    redirects (`ErrRedirect`); `ReadAllLimited` / `LimitReader` cap bodies.
  - **Errors.** `*BlockedAddressError` (IP + reason) is for admins only.
    `Normalize` maps errors for everyone else to `ErrNotAllowed`,
    `ErrUnreachable` (refused, reset, timeout and DNS alike), `ErrTLS`,
    `ErrRedirect`, `ErrResponseTooLarge` or `ErrFailed`, so "test
    connection" cannot be used as a port scanner.
  - `netguard.allow_hosts` in the TOML accepts `*.suffix`.
- **Alternatives considered:** Resolve-then-check only (DNS rebinding).
  Honoring `HTTPS_PROXY` (bypasses the guard; an egress proxy would need
  its own SSRF policy). Rejecting any CIDR that overlaps a metadata
  address (would forbid allowlisting 100.64/10 or fc00::/7 for no gain).
  Allowing loopback allowlisting in production (lets admins point
  destinations at serve's own internal services).
- **Consequences:** Deployments that require an egress proxy cannot use
  S3/SMTP/webhook destinations through it; they must allowlist direct
  routes. Private endpoints need explicit entries. Hard-denied ranges are
  unreachable even by mistake or by a compromised admin account. Security
  impact: closes SSRF via rebinding, redirects, IPv6 transition forms and
  proxies; reduces port-scan oracles for non-admins.
- **Related:** tasks 2.3, 2.9, 2.10, 5.1, 5.2, 6.9; D-049, D-055;
  `internal/netguard`, `internal/config` (`[netguard]`).

## D-121: Storage interface, object keys and the disk backend (task 2.8)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.8; interface incl. `Test(ctx, prefix)` accepted by the coordinator)
- **Context:** Tasks 2.9 (S3), 2.10 (SFTP), 3.1 (pipeline), 3.8 (retention),
  4.1 (restore) and 2.12 (destination test) need one backend contract with
  identical semantics for atomicity, overwrite, delete and errors, and the
  plan requires disk destinations confined to `storage.disk_root` with
  atomic finalization (DS-6, WK-6).
- **Decision:**
  1. **Interface** (`internal/storage.Destination`): `Capabilities()`,
     `Put(ctx, key, r, PutOptions) (ObjectInfo, error)`,
     `Get(ctx, key) (io.ReadCloser, ObjectInfo, error)`, `Stat`,
     `List(ctx, prefix, ListOptions) (ListPage, error)` (one page, ≤1000,
     `StartAfter`, strict byte-wise key order = S3 order, objects strictly
     below a segment-aligned prefix), `Delete` (idempotent),
     `Test(ctx, prefix) TestResult`, `Close`. `storage.All` iterates pages.
     Errors are wrapped sentinels (`ErrInvalidKey`, `ErrNotFound`,
     `ErrAlreadyExists`, `ErrDeleteNotPermitted`, `ErrChecksumMismatch`,
     `ErrUnsupported`, `ErrInsufficientSpace`, `ErrPermission`,
     `ErrClosed`, `ErrInvalidArgument`, `ErrSource` for producer errors);
     `Classify` maps any error to a fixed `ErrorCode` whose `Message()` is
     the only text shown to users (raw backend errors may hold hosts,
     paths or provider messages).
  2. **Put semantics:** streaming, never visible under its key before all
     bytes are written, verified (`ExpectedSHA256`) and durable; a failed
     or canceled Put leaves no object; **never overwrites**
     (`ErrAlreadyExists`); always returns size + SHA-256. Object-lock
     options on a backend without object lock fail closed with
     `ErrUnsupported` (`CheckPutOptions`), never silently ignored.
  3. **Delete:** idempotent (missing = success); refused with
     `ErrDeleteNotPermitted` in no-delete mode or under an active lock.
  4. **Test(ctx, prefix)** (deviation from a plain `Test(ctx)`): the probe
     is written below the destination prefix the jobs use, so it checks
     the permissions that matter (bucket policies are often
     prefix-scoped): `{prefix}/bekup-probe/{timestamp}-{128-bit hex}.probe`,
     write → read/compare → delete. In no-delete mode the delete step is
     skipped, `ProbeRemains` is set and a fixed note tells the operator to
     rely on lifecycle rules. Steps carry codes, never raw errors.
     Shared by all backends via `storage.RunProbe`.
  5. **Keys:** only via `ParseKey`/`ParsePrefix`/`BuildKey`: relative,
     ≤1024 bytes, ≤32 segments of 1..255 bytes from
     `[A-Za-z0-9._~%+=@-]`, no segment starting with a dot (dot names are
     reserved for backend temporaries), no `.`/`..`, backslashes, control
     or non-ASCII bytes. Layout `{prefix}/{job-slug}/{2006-01-02T150405Z}/{file}`;
     job slugs `[a-z0-9][a-z0-9_-]{0,99}`; database names are encoded
     canonically (`[A-Za-z0-9_-]` kept, every other byte `%XX`
     upper-case, so the name never contains a dot and the encoding is
     injective); encodings over 180 bytes become `~` + base32(SHA-256)
     (not reversible; the manifest holds the real name). `Key`
     unmarshalling re-validates.
  6. **Disk backend:** every access through `os.Root` opened on
     `disk_root` (+ optional validated subdirectory); files 0600,
     directories 0700. Put writes `.<32 hex>.partial` (O_EXCL) in the
     target directory, fsyncs, then finalizes with a **hard link**
     (`Root.Link`, fails with EEXIST, so no check-then-rename race) and
     removes the temporary name, then fsyncs the directory chain; file
     systems without hard links fall back to Lstat + rename (documented
     small race). Symbolic links are never followed for objects or
     directories (Get/Stat report them missing, List skips them, Put and
     Delete refuse to go through them; `os.Root` confines them anyway).
     Delete removes now-empty parent directories. `CleanupPartials`
     removes crash leftovers older than a cutoff (also in no-delete mode).
     `FreeSpace`/`CheckFreeSpace` (statfs) back an early check for
     `SizeHint`. On case-insensitive file systems (macOS) keys differing
     only in case collide and yield `ErrAlreadyExists`, never an
     overwrite.
  7. **Conformance suite** `internal/storage/storagetest.Run` (streamed
     round trip, checksum, atomic visibility, source error, no overwrite,
     pagination/order, delete, no-delete, invalid keys, concurrent and
     same-key puts, cancel, probe, fail-closed lock options) must pass for
     every backend.
- **Alternatives considered:** rename-based finalize with a prior
  existence check (race can overwrite); following in-root symlinks (an
  attacker with write access below the root could redirect writes);
  ignoring lock options on unsupported backends (silent loss of
  immutability); a `Test(ctx)` probe at the bucket root (misses
  prefix-scoped permission problems and needs wider permissions);
  lossy/lower-case DB-name encodings (collisions).
- **Consequences:** Backends share one tested contract; retention and
  restore can rely on order, no-overwrite and idempotent delete. Disk
  destinations still have no immutability (DS-3 residual). The probe
  object remains in no-delete mode. Trust boundaries unchanged; SECURITY
  DS-6, DS-7 and WK-6 updated.
- **Related:** D-007, D-055, D-120; `internal/storage`,
  `internal/storage/storagetest`, `docs/guides/destinations.md`.

## D-122: SSH tunnel client: algorithms, host-key pin format, mandatory dialer (task 2.2)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.2; mandatory dialer, secret consumption, RSA
  policy, "type not offered = mismatch" and the password limitation accepted
  by the coordinator)
- **Context:** D-035 requires SSH tunnels with a mandatory host-key pin and a
  TOFU fingerprint flow; the runner (task 2.6) needs a small client that it
  can drive through its target-allowlist dialer (task 2.5), and task 2.11 /
  the UI need the fingerprint capture step. The plan leaves algorithms, the
  pin format, RSA handling and error semantics open.
- **Decision:** `internal/tunnel` (x/crypto/ssh):
  1. **Pin format** `HostKeyPin{Type, FingerprintSHA256, AuthorizedKey}`:
     either key type + OpenSSH `SHA256:<base64>` fingerprint, or an
     authorized_keys line, or both (they must agree). No pin =
     `ErrInvalidConfig`; there is no insecure mode. The callback compares
     type and the SHA-256 of the wire key in constant time.
  2. **Host-key algorithms** offered are only those of the pinned type
     (`ssh-ed25519`, `ecdsa-sha2-nistp256/384/521`, `ssh-rsa` as
     `rsa-sha2-512`/`rsa-sha2-256`). RSA host keys must be ≥ 2048 bits; SHA-1
     (`ssh-rsa` signatures), `ssh-dss`, `sk-*` and certificate host keys are
     never accepted. A server that does **not offer the pinned key type**
     is reported as a host-key mismatch (`HostKeyMismatchError` with an
     empty presented type), i.e. a security event, so a server cannot
     downgrade to an unpinned key type.
  3. **Transport algorithms**: KEX `mlkem768x25519-sha256`,
     `curve25519-sha256`; ciphers `chacha20-poly1305@openssh.com`,
     `aes256-gcm`, `aes128-gcm`; MACs `hmac-sha2-256/512-etm`. Older servers
     fail with `ErrHandshake`.
  4. **Mandatory `Dialer`**: every connection (Dial and FetchHostKey) goes
     through a caller-supplied dialer; a nil dialer is a configuration
     error, so the allowlist cannot be bypassed by omission.
  5. **Credentials** `Auth{PrivateKey, Passphrase, Password}` as
     `*crypto.Secret`, exactly one of key or password. `Dial` **consumes**
     them (destroys every secret before it returns, success or failure) and
     zeroes the parsed private key afterwards (ed25519 bytes, RSA big.Int
     limbs; ECDSA scalars and RSA precomputed internals are unreachable).
     Client keys: ed25519, ECDSA, RSA ≥ 2048 (signing with rsa-sha2 only);
     DSA rejected. Limitation: x/crypto/ssh takes passwords as a Go string,
     which cannot be zeroed; key authentication is preferred.
  6. **TOFU**: `FetchHostKey` runs the key exchange only, captures the host
     key and aborts in the host-key callback, so no authentication is ever
     attempted; the result is shown for out-of-band confirmation and never
     trusted automatically.
  7. **Lifecycle**: dial + handshake bounded by `DialTimeout` (default 15 s,
     max 2 min) and ctx; `keepalive@openssh.com` every 15 s with one request
     in flight, the tunnel closes after 3 intervals without a reply.
     `DialTarget` reaches only the configured target; `Forward` serves a
     listener with at most `MaxConns` (default 8) concurrent connections,
     half-closes, and waits for all handlers.
  8. **Errors** are `*Error{Kind, Op, Addr}` with kinds `ErrInvalidConfig`,
     `ErrHostKeyMismatch`, `ErrUnsupportedHostKey`, `ErrAuthFailed`,
     `ErrUnreachable`, `ErrTimeout`, `ErrHandshake`, `ErrClosed` (or a
     plain `context.Canceled`); key-parser errors are never attached, so
     messages carry no secret material. Fingerprints are public and may be
     logged and shown in the alert (task 5.5).
- **Alternatives considered:** defaulting to `net.Dialer` when no dialer is
  given (silent allowlist bypass); keeping caller ownership of the secrets
  (copies would outlive the handshake); accepting `ssh-rsa` SHA-1
  signatures for old bastions; treating a missing pinned type as a plain
  handshake error (would hide a possible downgrade/impersonation); offering
  all host-key types and comparing only the fingerprint (lets a server pick
  an unpinned type); keyboard-interactive auth (not needed, larger surface).
- **Consequences:** Bastions must run a reasonably modern OpenSSH (≥ 8.x
  for curve25519/etm; RSA host keys with rsa-sha2). Host-key mismatches
  fail the run and are surfaced as security events (task 5.5). Tunneled
  connections (SSH channels) do not support deadlines; callers bound them
  with ctx. Trust boundary B8 unchanged in shape; SECURITY RN-8 detailed.
- **Related:** D-035, D-029, D-034, D-120; tasks 2.5, 2.6, 2.11, 5.5;
  `internal/tunnel`, `docs/guides/connections.md`.

## D-123: Engine catalog, client floors, TLS modes and argv builder details (task 2.1)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (coordinator answers reflecting the user's
  security-first stance; awaiting the user's confirmation)
- **Context:** Task 2.1 implements `internal/engine`: the embedded image
  catalog, version-compatibility rules and pure argv builders that D-031,
  D-033, D-034 and D-036 describe. Several details were open: which MySQL
  and MariaDB series to pin, how to make sure every pinned client has the
  restore safety flags, how the MySQL option file is passed, which TLS modes
  exist and how PostgreSQL overwrites and drops are done safely.
- **Decision:**
  1. **Catalog schema** (`internal/engine/catalog.toml`, embedded):
     `schema_version = 1` and `[[images]]` entries with `engine`, `key`,
     `image = "repo:tag@sha256:<index digest>"`, `legacy` and
     `binaries`. Strict TOML (unknown keys rejected). The client version is
     derived from the tag (no separate field Renovate could forget); `key`
     must equal the tag's series (PG major, MySQL/MariaDB major.minor);
     MySQL/MariaDB tags need a full x.y.z. Embedded entries must use the
     official repository named like the engine; binary names must equal a
     fixed per-engine set (`pg_dump`/`pg_dumpall`/`pg_restore`/`psql`,
     `mysqldump`/`mysql`, `mariadb-dump`/`mariadb`). The extra-images file
     (`[images] extra_file`) uses the same schema, may use any registry,
     must be digest-pinned, may not replace an embedded key, max 1 MiB /
     256 entries. `Entry` values have unexported fields, so builders only
     accept entries that came from a validated catalog.
  2. **Pinned images** (multi-arch OCI index digests, 2026-10-27, checked
     identical on Docker Hub, mirror.gcr.io and public.ecr.aws): PostgreSQL
     13.23 (legacy), 14.24, 15.19, 16.15, 17.11, 18.6 (alpine); MySQL 8.0.46
     (legacy), 8.4.13 LTS, **9.7.4 LTS**, no innovation release (26.x);
     MariaDB 10.6.28 (legacy), 10.11.19, 11.4.13, 11.8.9 and **12.3.3 LTS**.
  3. **Client floors per series** (validated for embedded and extra
     entries): PG `\restrict` fix (13.22, 14.19, 15.14, 16.10, 17.6, 18+);
     MySQL `--system-command` and `--commands` (8.0.43, 8.4.6, 9.4+; 8.1-8.3
     and 9.0-9.3 rejected); MariaDB `--sandbox` (10.5.25, 10.6.18, 10.11.8,
     11.0.6, 11.1.5, 11.2.4, 11.4.2, 11.5.2+; 10.7-10.10 and 11.3 rejected).
     The flags were verified with `--help` in every pinned MySQL/MariaDB
     image and `--restrict-key` in PG 13.23 and 18.6. The `--commands`
     floors of older patch releases were not verified in images; a client
     without the flag fails closed with "unknown option".
  4. **Compatibility:** minimum servers PG 10, MySQL 8.0, MariaDB 10.4.
     PG: client major >= server major (block); dump client = lowest such
     major. MySQL/MariaDB: same series, else lowest newer, else newest older
     with a `client_older_than_server` warning. Restore client = the rule
     applied to max(dump client, target). Legacy entries add a
     `legacy_client` warning.
  5. **TLS modes:** `pinned` (zero value, default: pinned cert as the only
     trust anchor; `sslmode=verify-full` + `sslrootcert`,
     `--ssl-mode=VERIFY_IDENTITY` + `--ssl-ca`, `--ssl --ssl-ca
     --ssl-verify-server-cert`), `unverified` (encrypted, server not
     verified: `sslmode=require` without `sslrootcert`, `REQUIRED`,
     `--skip-ssl-verify-server-cert`) and `disabled`. `unverified` and
     `disabled` are admin-only, step-up, audited downgrades with an alert
     and an "insecure transport" badge (D-034). TLS modes also pin the
     protocol to TLS 1.2+.
  6. **MySQL/MariaDB option file:** `--defaults-file=/tmp/bekup/my.cnf` as
     the first option (instead of `--defaults-extra-file`), so the image's
     `/etc/mysql` configs and `~/.my.cnf` are never read. The file holds
     only `[client]` `password="..."` with `\ " ' TAB BS` escaped; NUL, CR
     and LF are rejected (escaping verified with `--print-defaults` in
     mysqldump 8.0.46 and mariadb-dump 12.3.3). PG uses a `passfile` line
     `host:port:*:user:password` (`\` and `:` escaped).
  7. **Argv details:** PG tools get a single-quoted keyword/value
     `--dbname=` conninfo (`passfile`, `gssencmode=disable`,
     `application_name=bekup`, `connect_timeout=10`); `pg_dump
     --format=custom --compress=0`; `pg_restore --list` and restores read
     stdin. MySQL dumps end with `--` before the positional database;
     restores use `--database=<db>`, MySQL `--system-command=OFF
     --commands=OFF --local-infile=0`, MariaDB `--sandbox --local-infile=0`
     (also for CREATE/DROP via `--execute`).
  8. **Temporary names and overwrite:** `<db>__bekup_restore_<id>` /
     `<db>__bekup_old_<id>` (id `[a-z0-9]{1,26}`, db truncated to fit 63
     bytes). `CREATE DATABASE ... TEMPLATE template0`. The PG swap is three
     steps: Prepare (block connections, terminate backends), Swap (one
     transaction: terminate again, rename db to old, temp to db; retried by
     the caller), Rollback (re-allow connections). `DropDatabase` only
     accepts bekup temporary names (`WITH (FORCE)` on PG 13+ servers).
     System and maintenance databases are never created, restored into,
     swapped or dropped.
- **Alternatives considered:** `--defaults-extra-file` (rejected: the
  image's config files would still be read). MySQL innovation releases
  (rejected: short support windows). Only pinned/off TLS (rejected: some
  managed services cannot be pinned; the downgrade stays explicit and
  audited). A separate `client_version` field (rejected: Renovate would
  update the tag but not the field). One `--command` swap without a
  rollback step (rejected: a failure could leave the target refusing
  connections).
- **Consequences:** Renovate digest/tag updates are validated by tests and
  at runner startup; an update that crosses a series or drops below a
  floor fails. The extra file can add series (for example PG 19) but never
  weaken the embedded ones. Residual risk: a malicious globals file can
  still use psql meta-commands during the separate, admin-only globals
  restore (artifacts are signed and the container is isolated);
  MySQL/MariaDB have no rename, so their overwrite strategy is decided in
  task 4.x. Security impact: tightens RN-2, RN-3, DC-3, DC-4, DC-5.
- **Related:** D-031, D-033, D-034, D-036, D-065; tasks 2.1, 2.5, 2.7,
  2.7a, 4.2, 4.3; `internal/engine`, `docs/architecture.md`, `SECURITY.md`.

## D-124: Live events over SSE, no WebSocket

- **Date:** 2026-10-27
- **Status:** accepted
- **Decided by:** user
- **Context:** Every live stream in bekup goes from the server to the
  browser (run status and logs, task 3.7; the notification inbox, task
  5.3b; session-ended signals). Task 1.10 built GraphQL subscriptions on a
  WebSocket (`graphql-transport-ws`, D-108). The user's rule is: no
  WebSocket unless it is 100% needed, and it is not. Tasks 3.7, 5.3b and
  M6 build on whichever transport exists, so it is swapped now (task 2.13).
- **Decision:**
  - **Transport:** GraphQL subscriptions are delivered as **Server-Sent
    Events** (the graphql-sse protocol, "distinct connections" mode): one
    `POST /graphql` with `Content-Type: application/json` and `Accept:
    text/event-stream` per subscription, answered with
    `text/event-stream` (`next` events, then `complete`). Subscriptions run
    only as event streams and event streams carry only subscriptions (a
    query or mutation with `Accept: text/event-stream` and a subscription
    over a plain POST get FORBIDDEN). The WebSocket transport, `GET
    /graphql` upgrades and every WebSocket library are removed (a `GET`
    stays 405; a POST with an `Upgrade` header stays 400).
  - **Own transports on gqlgen's executor:** `internal/graph` runs gqlgen's
    `executor` directly with its own two transports (one JSON response;
    one event stream) instead of `handler.Server` + `transport.POST/SSE`.
    gqlgen's `handler/transport` package always links
    github.com/coder/websocket, so using it would keep a WebSocket library
    in the build; the own transports are small, never echo request bodies
    (gqlgen's SSE transport logs and returns them on decode errors) and let
    the stream end with a typed error event. `go.mod` no longer requires
    any WebSocket module (`go mod why -vendor -m
    github.com/coder/websocket`: not needed; the go.sum line remains only
    for tests of gqlgen's own packages).
  - **Admission (in this order):** the HTTP layer's Origin/`Sec-Fetch-Site`
    check, rate limits, body limit and session middleware apply unchanged
    (it is a POST); the handler then requires an `Origin` header equal to
    an allowed origin (defense in depth), a principal resolved **from the
    cookie** (401), no pending second factor (401) and no required 2FA
    enrollment (403), a free slot and an operation the transport gate
    admits (only a subscription; deny-by-default directive +
    `RootFieldOperations` + `Access.CheckOperation`). Refusals are plain
    JSON with 401/403/422/429/503 before any stream starts, so clients
    never retry them as broken streams.
  - **Caps:** `graphql.stream_max_per_session` (default 4, 1 to 20; 429),
    `graphql.stream_max_per_user` (10, 1 to 100; 429) and
    `graphql.stream_max_total` (1000 per instance; 503). One stream is one
    subscription, so the per-connection subscription cap and subscribe-rate
    bucket of D-108 are gone; every stream request counts against the
    per-IP and per-user HTTP rate limits instead. Browsers allow 6
    HTTP/1.1 connections per origin: the reverse proxies serve HTTP/2, and
    pages should use at most 2 to 3 subscriptions (e.g. one aggregated per
    page).
  - **Session watcher (`internal/graph/streams`, was `wsconn`):** unchanged
    semantics: the per-stream principal, periodic
    (`graphql.stream_revalidate_interval`, 30s), per-event (session bus /
    LISTEN-NOTIFY, with follow-ups) and pre-delivery
    (`graphql.stream_session_check_ttl`, 5s) re-validation with the full
    principal rebuild incl. restrictions (D-118), `Deliver` + `Authorize`
    for delivery-time authorization, the hub's closed flag (503 after
    shutdown). Instead of WebSocket close codes a stream the server ends
    gets a **final GraphQL error event** carrying the `apierror` code, then
    `complete`, and the response closes: `UNAUTHENTICATED` (was 4401),
    `FORBIDDEN` (4403), the new `SESSION_ROTATED` (4001: reopen with the
    new cookie) and the new `UNAVAILABLE` (1013 check failed / 1001
    shutdown: retry later). Shutdown ends all streams through the hub, so
    `http.Server.Shutdown` does not wait for them.
  - **Long-lived responses:** per stream the server's read and write
    deadlines are lifted (`httpserver.LiftDeadlines`) and every write gets
    its own 30s write deadline (a client that stops reading is dropped);
    a keepalive comment every `graphql.stream_keepalive_interval` (25s, 5s
    to 1m); headers `Cache-Control: no-store` and `X-Accel-Buffering: no`.
  - **Proxies:** nginx: `location = /graphql` with `proxy_buffering off`,
    `proxy_cache off`, `proxy_read_timeout 180s` (3x the maximum
    keepalive), HTTP/1.1 to serve with the `Upgrade`/`Connection` headers
    cleared (the WebSocket `map` is removed). Traefik: a `bekup-stream`
    router (POST `/graphql` with `Accept: text/event-stream`, highest
    priority) that skips the buffering middleware, which would otherwise
    hold the whole response; `flushInterval: -1ms` stays.
  - **Web client:** an Apollo link on `graphql-sse` (exact pin 2.6.3,
    `credentials: 'same-origin'`, distinct connections, no library
    retries): `UNAUTHENTICATED`/`FORBIDDEN` and a 401 refusal call
    `onAuthLost` (navigate to `/login`); `SESSION_ROTATED` reopens at once;
    `UNAVAILABLE`, network errors, 429 and 5xx reopen with jittered
    exponential backoff (1s to 30s); other refusals end the subscription.
    `graphql-ws` is removed.
  - **CSP:** D-082 is unchanged; streams are same-origin `fetch` requests
    covered by `connect-src 'self'` on every CSP level (no `ws:`/`wss:`
    question). `pnpm csp-check` proves a same-origin stream opens with the
    page Origin and a cross-origin one is blocked.
  - **Config:** `server.max_ws_connections_per_user` and `graphql.ws_*`
    are replaced by `graphql.stream_*` without aliases (nothing is
    released; VERSION 0.0.0).
- **Alternatives considered:** Keeping the WebSocket (works, but a second
  protocol with its own CSRF special case, a GET upgrade exception in the
  Origin rules, hijacked connections outside `http.Server.Shutdown`, a
  library in the binary and the CSP2 `ws:` gap, for no client-to-server
  need). gqlgen's `transport.SSE` (rejected: links a WebSocket library and
  echoes bodies in errors). graphql-sse "single connection" mode (one
  stream plus reservation tokens and separate POSTs per operation: more
  state, a token in headers; distinct mode keeps every stream an ordinary
  cookie-authenticated POST). Native `EventSource` (GET only, no body:
  queries in URLs and a GET exception to the CSRF rules).
- **Consequences:** Every security property of D-052/D-108 is kept: cookie
  authentication only, the Origin check, caps, periodic and event-driven
  revalidation with the full principal rebuild, delivery-time authz,
  immediate termination on logout, revocation, role or restriction change,
  IDs-only NOTIFY. The attack surface shrinks: no protocol upgrade, no
  upgrade exception in the Origin middleware's path, no credentials or
  messages from the client after the request, no WebSocket code in the
  binary. Clients must open one HTTP request per subscription, which makes
  the per-session cap and HTTP/2 at the proxy relevant (documented). The
  per-IP limits of the proxies (nginx `limit_conn 100`, Traefik
  `inFlightReq 100`) count open streams.
- **Related:** task 2.13 (supersedes the transport parts of task 1.10);
  D-050, D-052, D-082, D-102, D-105, D-108, D-118; `internal/graph`
  (`handler.go`, `stream.go`), `internal/graph/streams`,
  `internal/httpserver/stream.go`, `internal/app/subscriptions.go`,
  `internal/config` (`graphql.stream_*`), `web/src/lib/apollo.ts`,
  `web/scripts/csp-check.mjs`, `deploy/nginx`, `deploy/traefik`.

## D-125: Runner container healthcheck over a local health socket (task 2.4)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.4; accepted by the coordinator, awaiting
  the user's confirmation)
- **Context:** D-080 asks for `bekup healthcheck runner`, a local check of
  the runner's gRPC listener without a client certificate. The runner only
  speaks mTLS: a TCP probe without a certificate fails the handshake and
  cannot tell a healthy runner from a broken one, and the runner's own
  certificate has only the serverAuth EKU.
- **Decision:** The runner additionally serves the **standard
  `grpc.health.v1` service, and nothing else**, as plaintext gRPC on a
  **container-local unix socket**, `runner.toml` `[listen] health_socket`
  (default `/tmp/bekup-runner/health.sock` on the container's private
  `/tmp` tmpfs; `""` disables it). The directory is created `0700`; the
  runner refuses a directory that is a symlink, belongs to another user or
  grants group/other access, removes a stale path only if it is a socket it
  owns, chmods the socket `0600` and removes it on shutdown. Validation:
  absolute clean path, ≤ 100 bytes, in a dedicated directory (not directly
  in `/`, `/tmp`, `/run`, ...), never under `/run/secrets`. The status of
  `runner.v1.RunnerService` (and `""`) is `SERVING` only after the mTLS
  listener is bound, while the runner's own certificate is valid
  (re-checked every minute) and until shutdown starts. `bekup healthcheck
  runner` loads `runner.toml`, dials `unix://<socket>` (no proxy, no
  retries, 3 s) and exits 0 only on `SERVING`. Compose keeps the exec-form
  healthcheck. Refines D-080.
- **Alternatives considered:** the runner's own certificate as a client
  certificate (needs the clientAuth EKU on the server cert, weakening D-089);
  a dedicated "healthcheck" client identity (more key material and an
  allowlist entry that could call every RPC); a TCP connect probe (cannot
  distinguish healthy from broken TLS); an HTTP health port (another
  network listener).
- **Consequences:** No network exposure and no credentials for the
  healthcheck. Anyone already inside the runner container (same UID) can
  read SERVING/NOT_SERVING, which reveals nothing. The socket must never be
  placed on a shared volume (documented). Trust boundary B5 gains a note;
  RN-1 updated.
- **Related:** D-080, D-089, D-098; task 2.4; `internal/runner/health.go`,
  `cmd/bekup/healthcheck_cmd.go`, `internal/config/runner.go`,
  `docker-compose.yml`, `docs/operations.md`.

## D-126: Runner API proto conventions and code generation (task 2.4)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.4; accepted by the coordinator, awaiting
  the user's confirmation)
- **Context:** D-030 fixes a small typed gRPC API between serve/worker and
  the runner; the plan lists the RPCs but not the message shapes, the
  generator setup or how credential fields are protected.
- **Decision:**
  1. `proto/runner/v1/runner.proto`, package `runner.v1`, one service
     `RunnerService` with the 11 RPCs of the plan: `Health`, `Probe`,
     `FetchServerCertificate`, `FetchHostKey` (unary), `Dump` and
     `DumpGlobals` (server streams: `oneof` chunk | log | result, exactly
     one result at the end), `VerifyDump` (client stream: header first,
     then chunks), `Restore` (bidirectional: header + chunks in, log lines +
     result out), `CreateDatabase`, `RenameSwapDatabase` (names derived by
     the runner from a `temp_id`, D-123), `PrePull`. Unique request/response
     messages per RPC (buf `STANDARD`); every enum has an `UNSPECIFIED`
     zero value that the runner rejects; `TlsMode` mirrors D-123 (`PINNED`
     default, `UNVERIFIED`, `DISABLED`); `HostKeyPin` mirrors D-122;
     `Target.pins` holds at most two certificate pins (current + staged
     backup, D-034).
  2. **No free-form fields:** no command, argument, environment, image or
     path field exists; a test fails on such field names.
  3. **Credential fields** are `bytes` annotated with the custom option
     `[(runner.v1.secret) = true]` (extension 51001 on `FieldOptions`).
     `runnerpb.ZeroSecrets` zeroes and clears them by reflection; a test
     fails when a credential-looking field (password, passphrase,
     private_key, key_pem, ...) lacks the option or is not `bytes`.
  4. **Limits:** data chunks ≤ 1 MiB (`runnerpb.MaxChunkBytes`), messages ≤
     1 MiB + 64 KiB in both directions.
  5. **Generation:** buf v1.74.0, protoc-gen-go v1.36.12 and
     protoc-gen-go-grpc v1.6.2 are go.mod `tool` dependencies (buf 1.75
     would pull a pre-release protobuf runtime); `buf.yaml` (v2, lint
     `STANDARD`, breaking `FILE`) and `buf.gen.yaml` (local plugins via `go
     tool`, no registry or network). Generated code in `internal/runnerpb`
     is committed. `just gen` runs `buf generate`; `just lint` and CI run
     `buf lint` and `buf format --diff`; `buf breaking` against `main` runs
     only once the proto exists on `main` (CI: pull requests).
- **Alternatives considered:** ConnectRPC (another stack next to grpc-go);
  protoc binaries via `just doctor` (unpinned, needs network); `string`
  credential fields (cannot be zeroed); a free-form "options" map (argv
  injection risk, D-030); a field-name list instead of an option (silently
  misses new fields).
- **Consequences:** The API is reviewable in one file, and wire
  compatibility is enforced once released. Zeroing covers the decoded
  messages; copies in gRPC's pooled receive buffers are outside our control
  (documented limitation). B5 updated.
- **Related:** D-030, D-033, D-034, D-122, D-123; tasks 2.4, 2.5–2.7a;
  `proto/`, `buf.yaml`, `buf.gen.yaml`, `internal/runnerpb`, `justfile`,
  `.github/workflows/ci.yml`.

## D-127: Runner gRPC transport policy: interceptors, limits, keepalive, client (task 2.4)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.4; concurrency limits and connection
  recycling accepted by the coordinator, awaiting the user's confirmation)
- **Context:** The runner is equivalent to root on the host (D-003); its
  gRPC server needs an explicit policy for identity, abuse limits and
  long-running streams, and serve/worker need a matching client.
- **Decision:**
  1. **Server** (`internal/runner`): credentials from
     `certs.ServerTLSConfig` (TLS 1.3, private CA, allowlist in the
     handshake, no resumption); refuses to start when the certificate is
     not valid now (warns 30 days ahead) or the listen address is a
     wildcard outside dev without `allow_wildcard` (D-098, re-checked).
     Limits: max message 1 MiB + 64 KiB, 64 streams per connection, 16 KiB
     metadata, 10 s handshake. Keepalive: client pings no more often than
     every 30 s (also without streams), server pings after 2 min idle
     (20 s timeout), idle connections close after 15 min, every
     connection is recycled after 30 min with a grace of
     `limits.max_run_duration` + 1 min, so a client whose certificate
     expired cannot keep a connection forever while dumps are never cut.
     Shutdown: 45 s graceful, then in-flight calls are cancelled.
  2. **Interceptor chain** (unary and stream alike): logging (method,
     caller, code, duration; never payloads or status messages; Health at
     debug) → panic recovery (`INTERNAL "internal error"`, logs only the
     panic type and stack) → guard: caller identity re-checked against the
     allowlist (`PERMISSION_DENIED`), deny-by-default method table (a test
     checks it covers exactly the service), mandatory deadline
     (`INVALID_ARGUMENT` without one) clamped to `max_run_duration`,
     non-blocking limiter (`RESOURCE_EXHAUSTED`): class *run* (`Dump`,
     `DumpGlobals`, `VerifyDump`, `Restore`, `CreateDatabase`,
     `RenameSwapDatabase`) = `limits.max_concurrent_runs`, class *control*
     (`Probe`, `FetchServerCertificate`, `FetchHostKey`, `PrePull`) = 8,
     `Health` unlimited → zeroing of received messages that carry secret
     fields after the handler (at most 4 such messages per stream).
  3. **Client** (`internal/runnerclient`): `certs.ClientTLSConfig`; refuses
     configurations below TLS 1.3, without a root pool or with
     `InsecureSkipVerify`; no proxy, no service config, no transparent
     retries; keepalive 60 s / 20 s; default deadlines for unary calls
     without one (Health 5 s, others 2 min); streams without a deadline are
     refused. Only `Health` is retried: up to three attempts with jittered
     backoff, and only on `UNAVAILABLE`.
- **Alternatives considered:** queueing instead of failing fast (hides
  overload, ties up workers); no connection age limit (expired clients keep
  access); a short age grace (cuts long dumps); retries for all RPCs (dumps
  and restores are not idempotent); certificate hot-reload (more code in
  the root-equivalent process; a restart is required instead).
- **Consequences:** Overload is visible as `RESOURCE_EXHAUSTED` and must be
  handled by the worker's own concurrency (task 3.6). Certificate rotation
  requires a runner restart (documented). B5 and RN-9 updated.
- **Related:** D-003, D-030, D-089, D-098, D-126; tasks 2.4, 2.6, 3.6;
  `internal/runner`, `internal/runnerclient`, `docs/security/mtls-certs.md`.

## D-128: SFTP backend: host-key pin, hard-link finalize, symlink checks, connection policy (task 2.10)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.10)
- **Context:** Task 2.10 adds the SFTP destination. It must satisfy the
  D-121 contract (atomic, never-overwriting Put, idempotent Delete, S3-order
  List, normalized Test errors), pin the server's host key (D-018), dial
  through the SSRF guard (D-120) and must not be redirected out of its base
  directory by server-side symbolic links.
- **Decision:**
  1. **Package** `internal/storage/sftp` on `github.com/pkg/sftp` v1.13.12
     over `x/crypto/ssh`. `Open(Config)` validates only (no network); the
     first operation connects, so `Test` reports connection problems as
     normalized steps. One SSH connection per destination, shared by
     concurrent operations, re-dialed after it died; reconnects are
     serialized.
  2. **Transport policy = the tunnel's (D-122)**, implemented locally
     because its helpers are unexported (dedup later): mandatory pin
     (`Type` + `SHA256:` fingerprint and/or authorized-key line,
     constant-time compare), only the pinned key type offered as
     host-key algorithm, host keys ed25519/ECDSA/RSA ≥ 2048 (SHA-2
     only), kex mlkem768x25519/curve25519, ChaCha20-Poly1305/AES-GCM.
     No insecure or TOFU mode here (`tunnel.FetchHostKey` serves 2.12).
     A mismatch is a `*HostKeyMismatchError` (pinned and presented
     fingerprints for admins), classified `permission_denied`.
  3. **Auth:** private key (ed25519, ECDSA, RSA ≥ 2048 with SHA-2
     signatures; optional passphrase) or password as `crypto.Secret`.
     `Open` takes ownership: PEM and passphrase are destroyed before it
     returns (the parsed key stays in memory for reconnects and is zeroed
     on `Close`); the password is kept for reconnects and destroyed on
     `Close`. Parser errors are replaced by fixed messages.
  4. **Network:** a `*netguard.Guard` is mandatory (typed, so no
     unguarded dialer can be passed); dial + handshake + SFTP init + base
     resolution are bounded by `DialTimeout` (15 s, ≤ 2 m) and ctx;
     keepalive@openssh.com every 15 s, connection closed after 3 missed
     replies (ends hung requests); `MaxConcurrentRequests` per file
     (32, ≤ 128).
  5. **Paths:** `BasePath` must be absolute and clean; it is resolved once
     per connection with SSH_FXP_REALPATH and must be a directory; the
     optional `Dir` (key-prefix syntax) is created below it. Objects are
     `root + "/" + key` with validated keys only. Every directory below
     the root is checked with Lstat (Put creates missing ones, 0700, and
     refuses non-directories); objects must be regular files; Get compares
     the opened file's fstat with the Lstat result. Symbolic links below
     the root are therefore refused (Put), reported missing (Get, Stat,
     Delete) or skipped (List, CleanupPartials).
  6. **Put:** exclusive create of `.<32 hex>.partial` (0600) in the target
     directory (a dot name, so never a valid key and never listed; the key
     name is not embedded to stay within NAME_MAX), stream with SHA-256,
     verify, `fsync@openssh.com` when advertised, check the stored size,
     then **finalize with `hardlink@openssh.com`** (fails if the key
     exists, so never-overwrite is atomic) and remove the partial name.
     Without the extension: Lstat check + SSH_FXP_RENAME (SFTP v3 says it
     fails on an existing target; a server that overwrites leaves a small
     race; logged once, `ExclusiveCreate` reported false).
     `posix-rename@openssh.com` is never used because it overwrites by
     design. On any error or cancel the partial is removed (best effort;
     `CleanupPartials(ctx, olderThan)` removes crash leftovers, also in
     no-delete mode).
  7. **Delete:** refused with `ErrDeleteNotPermitted` in no-delete mode
     (without connecting); otherwise idempotent, removes now-empty parent
     directories. Operators should also withhold the delete right on the
     server (`internal-sftp -P remove,rmdir,rename,posix-rename`).
  8. **Errors:** SFTP status codes map to the storage sentinels
     (not found, permission, unsupported); auth failures →
     `permission_denied`; netguard blocks, connection loss and handshake
     failures → `network`; timeouts → `timeout`. Raw errors stay in the
     chain for admin logs only.
- **Alternatives considered:** posix-rename after an existence check
  (overwrites in the race); check + rename as the primary path (race);
  trusting REALPATH alone for symlink detection (servers differ in whether
  they resolve links); a TOFU/insecure host-key option (MITM); one
  connection per operation (slow handshakes, more exposure); importing
  `internal/tunnel` (helpers unexported, concurrent task).
- **Consequences:** SFTP destinations pass the shared conformance suite
  against OpenSSH (atmoz/sftp) and an in-process server. Residual risk:
  SFTP has no O_NOFOLLOW/openat, so a party that can modify the server's
  tree concurrently can win a check-then-use race; the server is trusted
  for its permissions and chroot, integrity rests on age + the signed
  manifest. SFTP offers no immutability (DS-3). The host-key and
  algorithm policy exists twice (tunnel, sftp) until deduplicated.
  SECURITY DS-3, DS-4, DS-6, DS-7 and WK-6 updated; trust boundaries
  unchanged (B4).
- **Related:** D-018, D-055, D-120, D-121, D-122; `internal/storage/sftp`,
  `docs/guides/destinations.md`.

## D-129: S3 backend: own multipart uploader, static credentials only, conditional writes, version-exact deletes (task 2.9)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.9)
- **Context:** Task 2.9 needs an S3/S3-compatible `storage.Destination`
  (D-121) with https-only endpoints, the netguard (D-120), ≥64 MiB parts,
  the `WhenRequired` checksum setting, SSE, Object Lock / versioning and
  no-delete mode (D-007). Several details are not fixed by the plan.
- **Decision:**
  1. **Package `internal/storage/s3store`** on `aws-sdk-go-v2/service/s3`
     (pinned in go.mod). The client is built with `s3.New(s3.Options{...})`
     only, **never** `config.LoadDefaultConfig`: credentials come from a
     static provider over the destination's key pair (`crypto.Secret`);
     no environment, shared files, SSO, container or instance-metadata
     credentials (an IMDS fallback would be an SSRF/credential-confusion
     vector). S3 Express session auth and multi-region access points are
     disabled. The SDK needs the key as a Go string; that copy lives in
     the SDK credentials cache while the destination is open (residual).
  2. **Transport:** `Guard.NewHTTPClient` (dial-time netguard, no proxy, no
     redirects, TLS ≥ 1.2), wrapped so response bodies are capped (1 MiB
     for errors, 16 MiB for list/XML responses; successful GetObject
     bodies are streamed unlimited). Netguard denials, redirects and
     oversized responses are never retried. Endpoint: `https://host[:port]`
     without user info, path, query or fragment; `http://` only with
     `AllowHTTP` (dev mode), with a startup warning. An optional CA bundle
     **replaces** the system roots for that destination. Bucket names are
     validated (3–63 of `[a-z0-9._-]`, no IP form, no `..`).
  3. **Own multipart uploader instead of `feature/s3/manager`.** The
     manager module is deprecated, aborts with the caller's (possibly
     canceled) context so a canceled Put can leave the upload behind, does
     not pass `If-None-Match` to `CompleteMultipartUpload`, and cannot
     verify `ExpectedSHA256` before completing. bekup's uploader: objects
     ≤ part size → one `PutObject`; larger → `CreateMultipartUpload`,
     parts of `PartSize` (default 64 MiB, min 5 MiB, max 5 GiB) with at
     most `Concurrency` (default 2, max 16) part buffers in existence, so
     memory per Put is exactly part × concurrency; SHA-256 hashed while
     streaming and verified before `Complete`; any failure (source error,
     checksum mismatch, cancellation, part error, refused Complete) calls
     `AbortMultipartUpload` on `context.WithoutCancel` with a 30 s timeout.
     Maximum object size = part × 10,000 (625 GiB by default; no silent
     part-size growth, which would break the memory bound).
     `CleanupIncompleteUploads(cutoff)` aborts crash leftovers; a bucket
     lifecycle `AbortIncompleteMultipartUpload` rule is documented.
  4. **Never overwrite:** a HEAD pre-check fails early, and
     `If-None-Match: *` on `PutObject` **and** `CompleteMultipartUpload`
     makes it atomic (`ExclusiveCreate`). `ConditionalWrites=false` for
     stores that reject the header leaves only the pre-check (documented
     race; on versioned buckets the earlier version survives).
  5. **Checksums:** the SDK always runs with
     `RequestChecksumCalculation=WhenRequired` (no aws-chunked trailers);
     bekup sets headers itself. Mode `when_supported` (default): per-request
     `x-amz-checksum-sha256` (composite SHA-256 for multipart), response
     validation on. Mode `when_required`: no checksum headers except
     Content-MD5 on Object Lock buckets (protocol requirement). The full
     SHA-256 is stored as `x-amz-meta-bekup-sha256` when known before the
     upload starts (single-part, or `ExpectedSHA256` given); otherwise the
     manifest/DB hold it (S3 metadata cannot be added after Complete).
  6. **Object Lock / versioning detected at Open** (`GetBucketVersioning`,
     `GetObjectLockConfiguration`; "not configured"/`NotImplemented` mean
     off, other errors fail Open). A configured default lock mode +
     retention days on a bucket without Object Lock fails with
     `ErrUnsupported`; lock options on such buckets fail closed (D-121).
     The default retention applies to every Put without its own lock
     options, but **not** to the Test probe.
  7. **Delete on versioned/locked buckets removes the key's versions
     permanently** (`ListObjectVersions`, exact key; data versions first,
     then delete markers), never just a delete marker that would hide a
     locked object and make retention believe it freed space. Locked
     versions (retention in the future or legal hold, pre-checked with
     HEAD, plus the store's refusal) → `ErrDeleteNotPermitted`. The
     governance-bypass header is never sent. No-delete mode refuses every
     Delete.
  8. **Errors:** SDK errors map to the storage sentinels; `NoSuchBucket`
     maps to the same permission class as `AccessDenied` (no bucket
     existence oracle); every transport failure (DNS, refused, TLS,
     netguard denial) maps to `CodeNetwork`, like `netguard.Normalize`.
     The raw error stays in the chain for admins/logs only.
  9. **Test:** `storage.RunProbe` with the probe written without the
     default retention; if the bucket's own default retention locks it,
     the refused delete step is reported as passed (code
     `delete_not_permitted`) with a fixed note and `ProbeRemains`.
  10. **Storage classes** limited to immediately readable ones (archive
      classes would make restores fail); minio's `ListObjectsV2` may report
      a full page as not truncated, so a full page is confirmed with a
      one-key probe.
- **Alternatives considered:** `feature/s3/manager` (see 3) or the
  pre-1.0 `transfermanager`; `LoadDefaultConfig` with static credentials
  first (still consults env/shared config for other settings and makes the
  fallback chain one config mistake away); delete markers only (locks not
  enforced, space never freed); storing the SHA-256 via a post-upload
  CopyObject or tags (rewrites/mutable); auto-raising the part size for
  huge objects (unbounded memory).
- **Consequences:** Predictable memory (part × concurrency per Put, 128 MiB
  by default per destination upload); the IAM policy needs
  `GetBucketVersioning`, `GetBucketObjectLockConfiguration`,
  `ListBucketVersions` and `DeleteObjectVersion` (documented). Private
  endpoints need a netguard allowlist entry. Security impact: no ambient
  credentials, SSRF-guarded and redirect-free transport, fail-closed
  locks, atomic never-overwrite, aborted uploads on every failure.
  SECURITY.md DS-3/4/5/7, WK-6 updated.
- **Related:** D-007, D-018, D-049, D-055, D-120, D-121; task 2.9;
  `internal/storage/s3store`, `docs/guides/destinations.md`,
  `docs/security/immutable-backups.md`.

## D-130: Runner request validation, target allowlist, hardened container spec and the stdin preamble (task 2.5)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (security-first defaults; awaiting the user's
  confirmation)
- **Context:** Task 2.5 implements the pure parts of the runner that
  D-029, D-031, D-032 and D-033 describe: request validation, the target
  allowlist, the container spec and the secret preamble. Several details
  were open: the default of an empty allowlist, how hosts behind an SSH
  bastion are checked, what "unknown fields rejected" means for proto3, how
  pins and keys are checked without leaving key copies in memory, the exact
  container settings, the non-root user per image and the preamble framing.
- **Decision:**
  1. **Validation** (`internal/runner/validate`): every RPC request has a
     validator returning a typed view or a gRPC status: InvalidArgument for
     malformed input, PermissionDenied for allowlist denials,
     FailedPrecondition for unresolvable hosts. Messages are static and
     name only the field (never values or secrets; fuzz tests assert it).
     Rules: unknown proto fields anywhere in the message (incl. nested and
     list elements) are rejected; unary requests and stream headers are at
     most 256 KiB, chunk messages at most `runnerpb.MaxMessageBytes`;
     `run_id` is a lowercase UUID; engine/version key must be in the merged
     catalog (embedded + `images.extra_file`); host per `engine.ValidateHost`,
     port 1-65535, user per `engine.ValidateUser`, DB names per
     `engine.ValidateDBName` (write operations also refuse system databases;
     the server-reported list is checked by the executor, D-033); passwords
     at most 1 KiB without NUL/CR/LF; `pinned` TLS needs 1-2 distinct
     parseable DER anchors (16 KiB each), `leaf_spki_sha256` is empty or 32
     bytes and mandatory (and must match) when the anchor is not a CA; other
     TLS modes take no pins; a client certificate needs TLS, `cert_pem` is
     1-8 parseable CERTIFICATE blocks (32 KiB), `key_pem` exactly one
     unencrypted private-key PEM block (16 KiB). Private keys (TLS and SSH)
     are checked structurally only (PEM type, no trailing data, decoded
     bytes zeroed) and never parsed, so the validator leaves no unzeroable
     key copies; a mismatching key fails in the native client / SSH
     handshake. SSH tunnels: exactly one of password/key, passphrase only
     with a key, the internal/tunnel user rules, a mandatory host-key pin
     (`tunnel.HostKeyPin.Validate`). DumpGlobals, RenameSwap and globals
     verify/restore are PostgreSQL only; `no_owner` only for PostgreSQL
     database restores; `total_bytes` 1 byte-64 TiB; a `Stream` checks that
     only non-empty chunks of at most 1 MiB follow the header and exactly
     `total_bytes` arrive. Validated views never copy secret fields.
  2. **Target allowlist** (`internal/runner/targets`): order of checks per
     address: always denied (every range `netguard.Classify` marks hard:
     loopback, link-local incl. metadata, unspecified, multicast, reserved,
     Teredo, NAT64/6to4 embedding a denied IPv4, ...; IPv4-mapped addresses
     are unmapped first) → the runner's own networks (`SetOwnNetworks`, fed
     by task 2.6 with the control network and every per-run subnet) →
     `deny_hosts` / `deny_cidrs` → `allow_cidrs` / `allow_hosts` (exact
     names) → deny. **An empty allowlist denies every target** (secure
     default) and the runner logs a startup warning; requests then fail with
     PermissionDenied. Names in `deny_hosts` are refused before any DNS
     query. Validation resolves the host and requires **every** address to
     pass (at most 16 addresses); `Guard.DialContext` resolves again and
     re-checks each socket address in the dialer's `Control` hook (DNS
     rebinding). An IP literal in `allow_hosts` fails runner startup (use a
     CIDR). Startup also fails on an invalid `images.extra_file`.
  3. **Hosts behind an SSH bastion:** the runner dials only the bastion, so
     the bastion goes through the full allowlist; the database host is
     resolved by the bastion and only checked against `deny_hosts`,
     `deny_cidrs` and, for IP literals, the always-denied ranges except
     loopback (a database on the bastion's own loopback is a common setup;
     metadata/link-local behind the bastion stay denied).
  4. **Container spec** (`internal/runner/spec`, pure, moby `api` v1.56.1
     types): image `repository@sha256:<index digest>` only, `User` the
     catalog entry's non-root UID:GID (new optional catalog key `uid`,
     default postgres alpine 70, mysql/mariadb 999, never 0), entrypoint
     `/bin/sh -c <preamble.Script> --`, `Cmd` the engine argv (tool must be
     one of the entry's binaries; no NUL, bounded size), constant env
     (`HOME=/tmp`, fixed `PATH`, `TZ=UTC`, `PGPASSFILE`, `PGSYSCONFDIR`,
     `PGSERVICEFILE`, `PSQLRC`/`PSQL_HISTORY`/`MYSQL_HISTFILE=/dev/null`,
     `MYSQL_TEST_LOGIN_FILE=/dev/null`), `ReadonlyRootfs`, a single tmpfs
     `/tmp` (`rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=,gid=`),
     `CapDrop: ALL`, `no-new-privileges:true`, default seccomp/AppArmor
     (never overridden), `Privileged: false`, no binds/mounts/volumes/
     devices/ports/links/DNS overrides, default masked `/proc` paths,
     private IPC (16 MiB shm) and cgroup namespace, `Memory = MemorySwap`
     and `NanoCPUs`/`PidsLimit` from `[docker]`, ulimits `core=0` (no core
     dumps with secrets) and `nofile=1024`, `Init: false`, log driver
     `none` (tool output only travels over the attach stream), restart
     `no`, `AutoRemove: false`, `StopTimeout` 10 s, no TTY, `OpenStdin` +
     `StdinOnce`, healthcheck `NONE`, labels `bekup.managed=true` and
     `bekup.run_id`, `NetworkMode` the per-run network (`bekup-*` names
     only) and an optional `ExtraHosts` entry target name → proxy IP.
     Image-defined environment variables remain (image constants, no
     secrets). Snapshot tests pin the spec (catalog digests normalized).
  5. **Preamble framing v1** (`internal/runner/preamble`): `BEKUP-PREAMBLE
     1\n`, then per record `<type> <length>\n<base64>\n` (types `pgpass`,
     `mycnf`, `ca`, `clientcert`, `clientkey`, each at most once, fixed
     paths under `/tmp/bekup`; decimal length without leading zeros, at most
     64 KiB; standard base64 on one line), then `END\n`; the payload
     (restore data) follows untouched. The wrapper is a reviewed constant
     using only POSIX sh (busybox ash, dash, bash); it creates `/tmp/bekup`
     0700 with umask 077, decodes through the `printf` builtin and a pipe
     (no secret in any child's argv), checks the decoded length, `chmod
     600`, and exits 97 with a fixed message on any error before the tool
     runs; then `exec "$@"`. The Go encoder allocates the exact size once
     (the caller zeroes it); `ForCommand` sends exactly the files the engine
     command needs. Verified by an integration test in all 14 pinned images
     (files, modes, owner, payload, builtin printf, read-only root, noexec
     /tmp, malformed preambles).
- **Alternatives considered:** An empty allowlist allowing all private
  ranges (rejected: deny by default). Checking the database host behind a
  bastion against the allow lists (rejected: the runner cannot resolve it
  and loopback-on-bastion setups would break; the bastion is the dial
  target). Parsing private keys to match them to certificates (rejected:
  unzeroable key copies; the native client reports a mismatch). Image
  references with tag and digest (rejected: digest only is unambiguous).
  The default json-file log driver (rejected: tool stderr would be kept
  on the daemon's disk). Here-documents in the wrapper (rejected: bash may
  spool them to a temporary file).
- **Consequences:** A fresh runner refuses every target until `[targets]`
  is configured. Some exotic setups (IP literal in `allow_hosts`, encrypted
  TLS client keys, non-official images without `uid`) need configuration
  changes. `docker logs` shows nothing for dump containers. Security
  impact: implements RN-2/3/4/5/6/11 controls and B8 (target allowlist);
  SECURITY.md rows updated.
- **Related:** D-029, D-031, D-032, D-033, D-034, D-035, D-098, D-120,
  D-122, D-123, D-126, D-127; tasks 2.5, 2.6, 2.7, 2.7a;
  `internal/runner/validate`, `internal/runner/targets`,
  `internal/runner/spec`, `internal/runner/preamble`,
  `docs/security/target-allowlist.md`, `docs/architecture.md`.

## D-131: Destinations API: storage of credentials, step-up for every change and the SFTP host-key flow (task 2.12)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.12)
- **Context:** Task 2.12 exposes destinations (D-007, D-018, D-121, D-128,
  D-129) through GraphQL. The plan fixes "CRUD (admin, step-up), metadata
  (maintainer), soft delete, test, confirmHostKey, generateSshKeypair" but
  not the storage layout, which changes need step-up, how credentials are
  kept write-only, how maintainers may test, or the host-key flow.
- **Decision:**
  1. **Table** `destinations` (migration 00011): `type` enum
     (`disk|s3|sftp`), `config` jsonb with the non-secret per-type
     settings only, `secret_enc` (all credentials of the row in one blob,
     `Keyring.Seal` under AAD `destinations:<id>:secret`; the id is chosen
     by the application so the blob is sealed before the insert),
     `no_delete`, `object_lock_mode`/`object_lock_retention_days` (S3
     only, CHECKs), the SFTP host-key pin columns (type, fingerprint,
     authorized key, confirmed at/by; all or none; SFTP only),
     `created_by`, `deleted_at`. Names are unique case-insensitively among
     non-deleted rows (partial unique index). The blob uses a small
     tag-length-value encoding so credentials never pass through Go
     strings in bekup's own code (gqlgen still decodes inputs into
     strings; the byte copies are zeroed, inputs are registered with the
     log redaction registry during the call).
  2. **Service `internal/destinations`** is the only code touching the
     table: per-type validation at save time (S3 through
     `s3store.Config.Validate`, https only unless dev mode, endpoint host
     resolved with `netguard.ResolveAndCheck`; SFTP through
     `sftp.ValidateTarget` + `ResolveAndCheck`, private keys parsed
     (ed25519/ECDSA/RSA ≥ 2048, passphrase checked) and their public key
     stored; disk paths through `storage.ParsePrefix` below
     `storage.disk_root`; CA bundles must be PEM certificates only,
     ≤ 256 KiB). Updates validate (including DNS) before locking the row
     and fail with CONFLICT if it changed meanwhile. A settings input
     replaces the whole non-secret configuration of its type; omitted
     credentials keep the stored value; an SFTP password and key are
     mutually exclusive (setting one removes the other). Changing an SFTP
     host or port removes the host-key pin. `Open(ctx, id)` builds the
     backend for the 3.x workers with the guard and keyring; S3 keys are
     destroyed on `Close`.
  3. **Authorization:** list/view and test for admins and maintainers;
     maintainers get metadata only (`settings` null: name, type, mode
     flags, readiness, timestamps). **Every** create/update/delete,
     host-key confirmation and keypair generation is admin + step-up
     (`destination.update` and `destination.delete` now require step-up:
     a changed endpoint or bucket redirects backups as surely as a changed
     secret, and deleting a destination stops backups); an update that
     sets credentials is additionally checked against
     `destination.secret_update`. New operation
     `destination.host_key_fetch` (admin, no step-up: it only reads a
     public key). Owners get nothing here; task 3.5 shows name/type of a
     job's destinations through the job.
  4. **Test and host-key fetch** are rate limited per user (10/min, burst
     5), at most 4 run at once (else RATE_LIMITED), bounded to 60 s, and
     single-root fields (D-117 extension: one outbound probe per
     request). Test results carry only `storage.ErrorCode`s and fixed
     messages (a "connect" step precedes the probe steps), so they are fit
     for maintainers; raw errors go to the server log only. A destination
     that is not ready answers CONFLICT with a fixed reason.
  5. **SFTP host keys (TOFU):** `fetchDestinationHostKey(host, port)`
     resolves and checks the host, then runs `tunnel.FetchHostKey` through
     the guard and returns type, fingerprint and authorized-key line; it
     never stores anything. `confirmDestinationHostKey(id, line)` pins an
     accepted key type (comments dropped, options refused) and is audited
     with the old and new fingerprint. An SFTP destination is ready only
     with a pin and a credential.
  6. **Keypairs:** `generateDestinationSshKeypair(id)` creates an ed25519
     key with `crypto.GenerateSSHKeypair`, seals the private key
     (replacing a stored password or key) and returns only the public
     key and fingerprint.
  7. **Audit:** `destination.created|updated|deleted|host_key_confirmed|
     ssh_key_generated` with the changed field names (old/new values for
     short non-secret fields; CA bundle and public key by name only);
     credential changes are listed by name and additionally recorded as
     `secret.changed`. No-op updates record nothing.
  8. **Soft delete** always; the in-use check is a hook
     (`checkUnused`) that task 3.5 fills (refuse while a job targets the
     destination); artifacts keep referencing soft-deleted rows.
- **Alternatives considered:** step-up only for secret/mode changes (an
  unstepped endpoint change can exfiltrate every following backup); a
  union type for settings (harder for the generated client than one
  object with per-type fields); JSON for the sealed blob (secrets in
  strings); storing the host key at fetch time (TOFU without
  confirmation); letting maintainers fetch host keys (an SSRF-style probe
  of arbitrary hosts for a non-admin role).
- **Consequences:** Admins re-authenticate for every destination change.
  Maintainers can probe configured destinations only (rate limited,
  normalized results), never arbitrary hosts. Moving an SFTP server
  requires a new host-key confirmation. Security impact: credentials are
  write-only, sealed per row and never logged or audited; outbound
  connections stay behind the guard; trust boundary B4 unchanged
  (SECURITY.md updated).
- **Related:** D-007, D-018, D-055, D-106, D-117, D-120, D-121, D-122,
  D-128, D-129; `internal/destinations`,
  `internal/graph/schema/destinations.graphqls`,
  `docs/guides/destinations.md`.

## D-132: Runner Docker executor: per-run network plan, IP-literal targets, proxy, kill and reaper policy (task 2.6)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted the IP-literal limitation)
- **Decided by:** agent with user approval (security-first defaults; awaiting the user's
  confirmation)
- **Context:** D-029 fixes the shape (per-run `internal` network, the
  runner joins it, a TCP proxy / SSH tunnel to exactly one target,
  `ExtraHosts` so TLS verification stays end to end, orphan reaper). Task
  2.6 had to decide the address plan, how IP-literal targets keep host
  verification (there is no host name to map), how the runner proves the
  container it joins is its own, the stream, kill and cleanup semantics,
  and how leftovers are reaped without touching another runner's runs.
- **Decision:** `internal/runner/executor`, moby client over the
  `docker.host` unix socket only:
  1. **Address plan.** New key `[docker] run_subnet_pool` (default
     `10.214.0.0/16`; canonical private IPv4, /8 to /24). Every run gets
     its own /28: `.1` gateway (IPAM only), `.2` runner/proxy, `.3` dump
     container (fixed address). Subnets already used by any Docker network
     are skipped; exhaustion is `RESOURCE_EXHAUSTED`. The whole pool, the
     runner's control network (the network holding `listen.address`),
     every own address (/32) and every active per-run subnet are the
     target guard's "own networks", so no target or bastion can resolve
     into them.
  2. **Network options.** `bekup-run-<run_id>`, bridge, `Internal: true`,
     IPv4 only, `com.docker.network.bridge.inhibit_ipv4=true` (the gateway
     address is never put on the host's bridge: the host is unreachable and
     the host's routing is untouched), `enable_ip_masquerade=false`,
     labels `bekup.managed`, `bekup.run_id`, `bekup.runner` (the runner's
     container ID). The container has no default route; Docker's embedded
     DNS does not forward queries from internal networks (verified by the
     integration test; older Docker Engines forwarded them, so a current
     Engine is required).
  3. **Self-join.** The runner's container is `docker.self_container` or
     detected from `/proc/self/mountinfo` (`.../containers/<id>/hostname`)
     and then the host name, and is accepted only if one of its Docker
     addresses is an address of the runner's own interfaces. Until that
     succeeds (and Docker answers) runs fail with `UNAVAILABLE`; the runner
     keeps serving `Health` and retries every 15 s. The plan's
     `BEKUP_RUNNER_CONTAINER` override is the existing config key's env
     override `BEKUP_DOCKER_SELF_CONTAINER`.
  4. **Per-run proxy.** Listens on the runner's per-run address and the
     target port, accepts only the dump container's fixed address (others
     are closed and logged), at most 16 connections, and forwards each to
     the one target: directly through `targets.Guard.DialContext`
     (re-resolved and re-checked per connection) or through the SSH tunnel
     whose bastion leg uses the same dialer. TLS is never terminated.
  5. **What the client dials.** A DNS name: the name, mapped by
     `ExtraHosts` to the proxy. An IP literal or `localhost` without host
     verification (TLS unverified/disabled): the proxy address. An IP
     literal **with** host verification behind an SSH tunnel: a /29 built
     around the literal, the runner's proxy holds that exact address, so
     the client dials the literal and verifies the IP SAN; the /29 is
     refused (fail closed) when it is not IPv4, in an always-denied range,
     the network/broadcast address, or overlaps the pool, the runner's
     networks, any Docker network or the run's own bastion, and is
     `RESOURCE_EXHAUSTED` while another run uses an overlapping range or
     dials into it. An IP literal or `localhost` with host verification
     and **no** tunnel fails closed with `FAILED_PRECONDITION`: the runner
     cannot hold an address it must also dial in the same network
     namespace. Such targets need a DNS name that matches the certificate
     or an SSH tunnel.
  6. **gRPC isolation.** Besides binding `listen.address` to the control
     address (D-098), the gRPC listener closes every connection whose peer
     is in the pool or an active per-run subnet before the TLS handshake,
     also on wildcard listeners.
  7. **Streams.** Attach before start; the preamble is written and zeroed
     at once, then the restore payload; stdin is closed only after a clean
     end of the payload, a read error kills the container instead (never a
     truncated input that looks complete). Stdout goes to the caller in
     chunks of ≤ 256 KiB synchronously (backpressure through Docker's pipe
     to the tool, at most one chunk buffered); stderr becomes lines capped
     by `limits.max_log_line_bytes` / `max_log_lines`, with control
     characters and invalid UTF-8 replaced and the run's secret values,
     the process registry and credential patterns redacted. The executor
     consumes the preamble material and the tunnel credentials (zeroed on
     every path).
  8. **Kill.** Timeout (request or `max_run_duration`): SIGTERM, then
     SIGKILL after 10 s. Cancellation, output loss, stdin failure or a
     failed wait: SIGKILL at once. A non-zero exit is a result, not an
     error (exit 97 = preamble failure).
  9. **Cleanup.** A defer chain removes, in this order, the container
     (force), the proxy, the runner's network membership, the network, the
     tunnel and the subnet reservation, on every path including panics. If
     the network membership or network cannot be removed, the subnet stays
     denied ("zombie") until the reaper removed the network.
  10. **Reaper.** At startup and every 5 minutes: containers and networks
     with `bekup.managed=true` and a `bekup-run-` name whose run is not
     active here; this runner's leftovers after 2 minutes (immediately at
     startup), another runner's only when that runner's container no
     longer exists. Networks are emptied (force disconnect, including the
     runner) before removal.
  11. Concurrency: the executor re-checks `limits.max_concurrent_runs`
     (non-blocking, `RESOURCE_EXHAUSTED`) behind the interceptor.
- **Alternatives considered:** A forwarder container holding the target's
  IP literal (would support direct IP-literal targets with verification,
  but adds a second container per run running bekup code and a second
  network; possible later). Proxy ARP / TPROXY / policy routing in the
  runner (needs `NET_ADMIN`). Rewriting the client's host to the proxy
  address for verified IP literals (breaks verification). One shared
  network for all runs (cross-run reachability). Docker-assigned subnets
  (no fixed client address for the proxy's peer check). A /28 around IP
  literals (shadows more of the target's neighbourhood). Queueing on pool
  exhaustion (hides overload).
- **Consequences:** Dump containers can reach exactly the runner's proxy
  for their own target: not the runner's gRPC port, other runs, the host,
  the internet or DNS (integration test in a containerized runner).
  Pinned TLS to a direct IP-literal target needs a DNS name or a tunnel.
  The pool range must not be needed for targets. Trust boundaries B6/B7
  are now implemented; SECURITY.md RN rows updated.
- **Related:** D-003, D-029, D-031, D-032, D-034, D-035, D-098, D-122,
  D-125, D-127, D-130; tasks 2.6, 2.7, 2.7a;
  `internal/runner/executor`, `internal/runner/server.go`,
  `internal/config/runner.go`, `docs/architecture.md`,
  `docs/security/target-allowlist.md`.

## D-133: Target-DB certificate pinning: capture, leaf-SPKI pre-flight, trust anchor and mismatch signalling (task 2.7a)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals) (items 4, 6 and 7 amended by D-138 and D-139)
- **Decided by:** agent with user approval (security-first defaults; awaiting the user's
  confirmation)
- **Context:** D-034 and D-123 fix the model (the pinned certificate is the
  native client's only trust anchor, plus a leaf SPKI check in Go before
  every run; mismatch = hard fail + security alert; a staged backup pin;
  30/7-day expiry warnings). Task 2.7a had to decide how the chain is
  captured without trusting it or sending credentials, the exact pre-flight
  semantics with two pins, which certificate to suggest as the anchor, how
  serve recognizes a mismatch, and how expiry information reaches serve.
- **Decision:** package `internal/runner/pinning`:
  1. **TLS upgrade** per engine on a connection opened through the target
     guard (`targets.Guard.DialContext`) or the SSH tunnel
     (`pinning.Connect`): PostgreSQL sends the 8-byte SSLRequest and reads
     exactly one byte (`S` continues, `N` = "no TLS", anything else is a
     protocol error; bytes after `S` stay in the socket and break the
     handshake, CVE-2021-23222). Default `UpgradeAuto` falls back to PG 17+
     direct TLS (ALPN `postgresql`, required to be negotiated) on a fresh
     connection only when the SSLRequest got no protocol answer; an explicit
     `N` is final. MySQL/MariaDB: the HandshakeV10 greeting is read with a
     1 KiB bound (length checked from the header before any read; sequence
     id 0, protocol 10, error packet = "rejected", NUL-terminated version of
     at most 255 bytes, filler 0), the SSL and 4.1 capability flags are
     required, and the 32-byte SSLRequest (sequence id 1, no user, no
     password, no database) is sent. Dial + upgrade + handshake are bounded
     by 15 s (ctx-driven close for SSH channels without deadlines).
  2. **Capture** (`FetchServerCertificate`): the only `InsecureSkipVerify`
     in bekup. `VerifyConnection` records the chain (at most 8
     certificates) and returns an error, so the client aborts the handshake
     itself: no Finished, no application data, never credentials (unit test
     with fake servers asserting the exact pre-TLS bytes, a failed
     server-side handshake and zero application bytes). The response adds
     per certificate the PEM and the SPKI SHA-256 as base64 and hex, plus
     `suggested_anchor_index` (the leaf if self-signed, else the self-signed
     root the presented chain leads to through verified signatures, else -1:
     OpenSSL-based clients need a self-signed anchor, an intermediate alone
     fails without a partial-chain flag) and `leaf_spki_sha256` (always).
  3. **Pre-flight** before every PINNED connection: the same upgrade, then a
     handshake with `RootCAs` = exactly the pinned anchors (never empty, so
     never the system roots), `ServerName` = the target host (DNS name or IP
     literal; IP SANs are checked) and TLS >= 1.2; `VerifyConnection` then
     requires **one pin** to satisfy both checks: the chain verifies with
     that pin's anchor as the only root (host name/IP, validity, server-auth
     EKU) **and** the leaf's SPKI SHA-256 equals that pin's leaf SPKI
     (constant-time; a CA pin without a leaf SPKI checks the chain only).
     Anchor and leaf of different pins never combine. The current pin wins
     over the staged one. The connection is closed right after the
     handshake. `ClientTLSConfig` gives the runner's own Go drivers (Probe)
     the same check.
  4. **Mismatch signalling:** `*MismatchError` (matches `ErrPinMismatch`)
     with a fixed reason `leaf_spki`, `untrusted_chain`, `hostname`,
     `expired` or `no_certificate`. `pinning.Status` maps it to
     `FAILED_PRECONDITION` "target certificate does not match the pinned
     certificate" with a `google.rpc.ErrorInfo` (domain `runner.bekup`,
     reason `TLS_PIN_MISMATCH`, metadata `check` = the reason);
     `pinning.PinMismatch(err)` reads it on the serve side (task 5.5 alert).
     No certificate content is returned for a failed check.
  5. **Expiry and staged pin:** the pre-flight `Report` carries the matched
     pin index, the leaf/anchor expiry and the earliest expiry of the
     verified chain, plus the warning codes `tls_staged_pin_in_use`,
     `tls_certificate_expires_within_30_days` / `..._7_days` (only the more
     urgent one) for the run result's warnings.
  6. **Trust anchor:** `TrustAnchor(pins)` = the PEM of every pin's anchor
     (current first, then the staged one, deduplicated); it becomes the
     preamble's `ca` record. The engine argv already sets `sslrootcert` +
     `verify-full`, `--ssl-ca` + `VERIFY_IDENTITY`, `--ssl-ca` +
     `--ssl-verify-server-cert`, and never a CA path, so no client reads the
     system roots. Both anchors are delivered so a planned rotation does not
     break the native client; the per-pin pairing is enforced by the
     pre-flight.
  7. **Leaf SPKI vs. native client:** with a CA anchor the native client
     cannot check the leaf key; another leaf from the same CA passes the
     native client but fails the pre-flight, and the runner never starts the
     container then. Admins who want the native client itself to refuse
     other leaves pin a self-signed leaf (the integration test proves that
     another self-signed leaf fails both).
  8. **IP literals** (D-132): the pre-flight verifies IP SANs for IP-literal
     targets directly and through the tunnel; the dump container's limits of
     D-132 are unchanged.
  9. **Fail closed:** `pinning.FailClosed()` rejects every call; the RPC
     handlers fall back to it when no checker is wired.
- **Alternatives considered:** Capturing with pgx / go-sql-driver (they
  start authentication right after TLS and would need credentials or abort
  late; our own upgrade sends nothing). Verifying with a pool of both
  anchors only (a leaf of the staged CA could pass with the current pin's
  leaf SPKI). Pinning the immediate issuer (fails in libpq/MySQL clients
  without a self-signed root). Comparing the anchor fingerprint instead of
  verifying the chain (no host-name and validity check). Sending only the
  matching anchor to the native client (the runner would have to decide
  before the container dials; the pre-flight already enforces the pairing).
- **Consequences:** A server certificate without a SAN for the target name
  fails the pre-flight (Go does not fall back to the CN, unlike libpq);
  SHA-1-signed chains fail. A pin mismatch is distinguishable from transport
  errors without parsing messages. One extra TCP+TLS connection per RPC.
  Security impact: implements DC-6 / B7 pinning controls (SECURITY.md
  updated); the capture path is the only unverified TLS in bekup and never
  carries data.
- **Related:** D-029, D-034, D-123, D-126, D-127, D-130, D-132; tasks 2.7,
  2.7a, 2.11, 5.5; `internal/runner/pinning`, `proto/runner/v1/runner.proto`,
  `docs/guides/connections.md`, `docs/security/hardening.md`.

## D-134: Runner RPCs: Go-driver pre-checks, normalized target errors, offline verify and the globals fallback (task 2.7)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (security-first defaults; awaiting the user's
  confirmation)
- **Context:** Task 2.7 implements Probe, Dump, DumpGlobals, VerifyDump,
  Restore, CreateDatabase, RenameSwapDatabase and PrePull on top of the
  validator (D-130), the executor (D-132) and the pin checker (D-133).
  Open points: how D-033's "database must be in the server-reported list"
  and the PostgreSQL client-compat rule are enforced before a container
  runs, how errors are normalized so Probe is no port scanner, how a dump
  is verified without network or credentials, how the
  `--no-role-passwords` fallback avoids mixing two outputs, and the
  per-RPC limits.
- **Decision:**
  1. **Pre-checks with the Go drivers inside the runner** (new package
     `internal/runner/dbprobe`: pgx `pgconn`, go-sql-driver/mysql for
     MySQL and MariaDB, version parsing per engine). Every RPC that
     touches a database first opens a fresh upstream (`pinning.Connect`:
     the target allowlist dialer or an SSH tunnel), runs the pin
     pre-flight for TLS mode PINNED, then connects with the driver: the
     pinned anchors as the only roots via `pinning.ClientTLSConfig`
     (UNVERIFIED: encrypted, `InsecureSkipVerify`, TLS 1.2+; DISABLED: no
     TLS; an optional client certificate is presented). It reads the
     server version and the database list (system databases removed,
     invalid names skipped and counted, at most 10 000). Dump and Restore
     require the database in that list; CreateDatabase requires it to be
     absent; RenameSwap requires `<db>` and the derived temp name and the
     absence of the derived old name. Dump/DumpGlobals refuse a server
     the requested client cannot dump (`engine.CheckDumpCompat`:
     PostgreSQL client major >= server major); Restore selects the client
     itself (`SelectRestoreClient`: max(dump client, server)). The
     upstream is closed before the container runs; the executor opens its
     own (a second SSH handshake when tunneled). pgconn is configured
     without environment influence on the security settings (no
     fallbacks, explicit TLS, `/dev/null` passfile and service file); the
     MySQL driver disallows cleartext/old passwords, LOCAL INFILE,
     multi-statements and plaintext fallback, and its own logger is
     discarded. Limitation: both drivers take the password (and the
     parsed client key) as unzeroable Go values, like the SSH password in
     `internal/tunnel`.
  2. **Normalized target errors** (`runnerpb.Reason`, an
     `errdetails.ErrorInfo` with domain `runner.bekup` and a fixed message
     per reason): `TARGET_UNREACHABLE` (Unavailable),
     `TARGET_AUTH_FAILED`, `TARGET_TLS_FAILED`, `TLS_PIN_MISMATCH` (the
     D-133 status incl. the `check` metadata), `TARGET_PERMISSION_DENIED`,
     `TARGET_UNSUPPORTED_SERVER`, `SSH_HOST_KEY_MISMATCH`,
     `SSH_AUTH_FAILED`, `TARGET_FAILED` (FailedPrecondition),
     `TARGET_TIMEOUT` (DeadlineExceeded), `TARGET_DATABASE_NOT_FOUND`
     (NotFound), `TARGET_DATABASE_EXISTS` (AlreadyExists). Everything
     before the database answered as a database (refused, filtered,
     timeouts, DNS, resets, a non-database protocol, a TLS handshake that
     is not a certificate decision, an SSH target-leg failure) is
     `TARGET_UNREACHABLE` with one message. Certificate verification
     failures of the driver are pin mismatches. Server messages are never
     returned or logged; the runner log gets the reason plus a fixed
     detail (SQLSTATE, MySQL error number, error class).
     `runnerpb.ErrorReason` decodes the reason for serve.
  3. **Dump:** stdout chunks (256 KiB, ≤ 1 MiB) and stderr lines are
     relayed; exactly one `DumpResult` ends a successful stream (a
     non-zero exit is a result, not an error). `verified` is set for
     MySQL/MariaDB (exit 0 + "-- Dump completed" trailer); PostgreSQL
     archives are verified with VerifyDump. MySQL/MariaDB dumps warn
     `non_innodb_tables`; pin warnings (D-133) and compat warnings are
     passed on.
  4. **DumpGlobals:** the first `pg_dumpall --globals-only` attempt is
     held back in memory (at most 4 MiB, zeroed after use); if it exits
     non-zero and stderr shows a role-catalog privilege error (`pg_authid`
     is matched, so localized messages work too, plus "permission denied"
     / "must be superuser") it is discarded and the call is retried once
     with `--no-role-passwords`, announced by a runner log line and the
     `roles_without_passwords` flag and warning. Beyond 4 MiB the output
     streams and no retry is possible. `verified` checks pg_dumpall's
     "dump complete" trailer.
  5. **VerifyDump:** MySQL/MariaDB dumps and PostgreSQL globals are
     checked in Go on the streamed tail (trailers) without a container.
     PostgreSQL archives run `pg_restore --format=custom --file=/dev/null`
     (amends D-123 item 7, which used `--list`: `--list` stops after the
     table of contents and passed a truncated archive in the integration
     test) in an **offline container**: new executor/spec mode with
     `NetworkMode: none`, no per-run network, proxy or tunnel, and no
     preamble files (no credentials ever reach it), same hardening
     otherwise. The stream must deliver exactly `total_bytes`; the rest
     of the stream is still received and counted after the tool exits.
  6. **Restore:** a pump goroutine is the only receiver of the client
     stream and feeds the container's stdin through a pipe (backpressure);
     a validation or receive error closes the pipe with that error, so the
     executor kills the container instead of closing stdin. Tool stdout is
     counted, never returned (it may echo data). Restores are never
     retried.
  7. **CreateDatabase / RenameSwapDatabase:** statements run in a
     container (psql / mysql with the restore safety flags); a non-zero
     exit maps to permission / exists / not found / failed by fixed
     stderr patterns. The PostgreSQL swap runs Prepare, then Swap up to 3
     times (2 s apart, only while the failure is generic), and Rollback
     (re-allow connections, on its own bounded context) when Prepare or
     the last Swap fails. RenameSwap on MySQL/MariaDB is
     FailedPrecondition. PrePull reports `already_present` and pulls by
     digest only.
  8. **Limits:** the interceptor deadline (≤ `limits.max_run_duration`)
     bounds every call; Probe is capped at 1 min, CreateDatabase at 2 min,
     RenameSwap at 5 min; containers get the remaining deadline as their
     timeout (graceful kill), cancellation kills at once. Concurrency stays
     with the interceptor classes and the executor semaphore.
  9. **Audit:** one `runner_rpc_audit` line per call (rpc, run_id,
     caller, engine, version key, TLS mode, tunnel flag, database name,
     code, reason, exit code, bytes, duration); never payloads, server
     text or secrets. The request's passwords are registered for
     redaction of tool output; the interceptor zeroes the request.
  10. `ProbeResponse` gains `version_key_compatible` and `warnings`
     (wire-compatible additions).
- **Alternatives considered:** Pre-checks through the executor's tunnel
  (one SSH handshake, but the image must be chosen before the server
  version is known). Detecting a non-superuser upfront for DumpGlobals
  (the task asked for stderr detection; a query would also need extra
  privileges on some services). Streaming the first globals attempt
  (the fallback would follow partial output). `pg_restore --list` (misses
  truncation). Running verify containers on a per-run network (no need
  for any network). Distinct messages for refused/filtered ports
  (port-scanning oracle).
- **Consequences:** Every database operation logs in to the target twice
  (pre-check + tool) and, with a tunnel, opens two SSH sessions. Probe
  can still be timed (a refused port answers faster than a filtered one);
  the target allowlist and the admin-only API in serve bound this. A TLS
  handshake failure that is not about the certificate reads as
  "unreachable". PostgreSQL verification now costs a full read of the
  archive. Security impact: RN rows in SECURITY.md updated (D-033 list
  check implemented, normalized errors, verify containers without
  network or credentials).
- **Related:** D-029, D-031, D-033, D-034, D-036, D-123, D-126, D-127,
  D-130, D-132, D-133; tasks 2.7, 2.11, 3.6, 4.2, 4.3;
  `internal/runner/ops*.go`, `internal/runner/dbprobe`,
  `internal/runnerpb/reasons.go`, `internal/runner/executor`,
  `internal/runner/spec`, `internal/engine/postgres.go`,
  `docs/architecture.md`, `SECURITY.md`.

## D-135: Connections API: storage, pin and host-key flows, TLS downgrade and runner wiring (task 2.11)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (task 2.11; security-first defaults, awaiting the
  user's confirmation)
- **Context:** Task 2.11 exposes database connections through GraphQL on
  top of the runner RPCs (D-133, D-134). The plan fixes the roles (admin
  CRUD; maintainers use connections, may test them, never touch pins or
  host keys), pinning (admin confirms with step-up, staged backup pin,
  expiry warnings), the TLS-downgrade opt-out and the SSH keypair
  generator, but not the table layout, the confirmation semantics, how
  runner failures reach the API, which operations are single-root or the
  runner FetchHostKey RPC (still Unimplemented after 2.7).
- **Decision:**
  1. **Table** `connections` (migration 00012): engine enum, `version_key`
     (catalog key of the server's series) and `image_key` (catalog key of
     the client image sent as `Target.version_key`), host, port, username,
     no database column (selection is per job), `tls_mode` enum (default
     `pinned`), the current pin (anchor PEM, leaf SPKI SHA-256 or NULL for
     a CA pin that allows leaf rotation, `tls_pin_not_after`, pinned at/by)
     and one staged backup pin with the same columns, SSH columns (enabled,
     host, port, user, auth kind, passphrase flag, the public key of a
     stored private key, the host-key pin columns), the public TLS client
     certificate, `has_password`, `secret_enc` (database password, SSH
     password or private key + passphrase, TLS client key in one
     tag-length-value blob sealed under AAD `connections:<id>:secret`, as
     D-131), `temporary` + `expires_at` + `created_for_restore_id`
     (CHECK-coupled, unused until task 4.4; all queries exclude temporary
     rows), `created_by`, soft delete, case-insensitive unique names among
     non-deleted rows. CHECKs keep pins, host keys and SSH settings
     complete and bounded.
  2. **Service `internal/connections`** is the only code touching the
     table. Keys must exist in the embedded catalog for the engine
     (runner `images.extra_file` additions are not selectable from serve
     yet); a PostgreSQL image older than the server series is refused;
     host/port/user follow the runner's rules (`engine.Validate*`);
     passwords follow D-123 (no NUL/CR/LF, ≤ 1 KiB); client certificates
     and SSH keys are shape-checked (SSH keys parsed: ed25519, ECDSA,
     RSA ≥ 2048, passphrase verified). **D-132 rule:** TLS mode PINNED with
     an IP literal or `localhost` host and no SSH tunnel is a VALIDATION
     error that explains D-132 (use a DNS name, a tunnel or an opt-out
     mode); the same check runs on `setTlsMode`. Changing the host or port
     clears both pins; changing the bastion's host or port (or removing
     the tunnel) clears its host-key pin; the engine is immutable.
     Updates validate before locking and fail with CONFLICT when the row
     changed meanwhile. Audit: `connection.created|updated|deleted|
     tls_mode_changed|pin_confirmed|backup_pin_staged|host_key_confirmed|
     ssh_key_generated` with changed field names (old/new values for short
     non-secret fields), credential changes additionally as
     `secret.changed`; no-op updates record nothing.
  3. **Pins.** `fetchCertificate` returns the runner's captured chain
     (subject, issuer, SANs, validity, SPKI SHA-256 base64/hex, DER
     SHA-256, the suggested anchor) and stores nothing.
     `confirmCertificatePin` / `stageBackupPin` take either a `certIndex`
     (the chain is **captured again** and must present the leaf SPKI the
     admin passes, so a certificate swapped between fetch and confirm is
     refused) or an out-of-band PEM (one certificate). The anchor must be
     self-signed (leaf or root CA; intermediates fail in libpq/MySQL
     clients), valid now and ≤ 16 KiB; a leaf anchor needs its own SPKI as
     the leaf pin; a CA anchor needs the leaf SPKI unless
     `allowLeafRotation` is set. `tls_pin_not_after` is the anchor's
     expiry or, with a bound leaf from a capture, the earlier of anchor and
     leaf. Staging needs a current pin and a different pin; confirming the
     staged pin promotes it. The API reports `pinStatus` (NOT_REQUIRED,
     MISSING, VALID, EXPIRES_WITHIN_30_DAYS, EXPIRES_WITHIN_7_DAYS,
     EXPIRED) and `pinExpiresAt` to maintainers too.
  4. **SSH host keys (TOFU).** New runner implementation of
     `FetchHostKey`: validated request (bastion through the target
     allowlist), `tunnel.FetchHostKey` through `targets.Guard` (no
     authentication), 30 s cap, a `runner_rpc_audit` line with the
     bastion, key type and fingerprint; failures other than a denied
     address (PermissionDenied) or an unsupported key type (new reason
     `SSH_HOST_KEY_UNSUPPORTED`) are `TARGET_UNREACHABLE`.
     `fetchSshHostKey(id)` calls it for the connection's bastion (serve
     re-parses the returned line); `confirmSshHostKey` pins an accepted
     key type (options refused, comment dropped), audited with the old and
     new fingerprint. `generateConnectionSshKeypair` creates an ed25519 key
     (`crypto.GenerateSSHKeypair`), seals it (replacing an SSH password or
     key; other credentials untouched) and returns only the public key.
  5. **TLS downgrade.** `setTlsMode` to UNVERIFIED or DISABLED (and
     `createConnection` with such a mode) additionally needs
     `connection.tls_downgrade` (admin + step-up); a weakening change
     records `connection.tls_mode_changed` plus `security.opt_out`
     (`setting: connection.tls_mode`, from, to) and raises the alert hook;
     upgrades record only the mode change. Pins are kept across mode
     changes. `insecureTransport` on the type is the badge flag. A client
     certificate blocks DISABLED.
  6. **Runner failures.** Requests are built per call from the row and
     the unsealed credentials (copies; `runnerpb.ZeroSecrets` after the
     call; values registered with the log redaction registry during the
     call); a pinned connection without a pin, or a tunnel without host
     key or credential, is NOT READY (CONFLICT) without a runner call.
     Runner reasons map to fixed messages; `TLS_PIN_MISMATCH` and
     `SSH_HOST_KEY_MISMATCH` are recorded as `connection.tls_pin_mismatch`
     / `connection.ssh_host_key_mismatch` (with the runner's `check`, the
     host and the pinned fingerprint; fail closed if the audit write
     fails), passed to the `AlertFunc` (default: a WARN log line; task 5.5
     makes them security alerts) and returned as the new API codes
     `TLS_PIN_MISMATCH` / `SSH_HOST_KEY_MISMATCH`. Other failures are
     `CONNECTION_FAILED` with a fixed message (runner down, allowlist
     denial, invalid request, timeout), except in `testConnection`, which
     reports them as `ok: false` + code + message.
  7. **Authorization.** Read: admins and maintainers (maintainers get
     metadata; `settings` null). Every change (create, update, delete,
     setTlsMode, pin confirm/stage, host-key confirm, keypair) is admin +
     step-up: `connection.update` and `connection.delete` now require
     step-up as for destinations (D-131); credentials in an update also
     check `connection.secret_update`. Maintainers may `testConnection`,
     `detectVersion`, `listDatabases` and `prePullImage` (D-106; a
     narrowing of `prePullImage` to admins was withdrawn by the user,
     D-140). New operation `connection.host_key_fetch` (admin). `engineVersions`
     lists the catalog for the UI (`connection.list`).
  8. **Single-root and limits.** `testConnection`, `detectVersion`,
     `listDatabases`, `prePullImage`, `fetchCertificate`,
     `confirmCertificatePin`, `stageBackupPin` and `fetchSshHostKey` are
     single-root (D-117; their operations are credential operations, so
     startup fails if one is missing). Runner calls are limited per user
     (10/min, burst 5), at most 4 at once (RATE_LIMITED), and bounded to
     60 s (pre-pull 10 min).
  9. **Wiring.** `serve` builds the runner client from `[runner]` at
     startup (`runnerclient.New`, D-127); unreadable or invalid client
     certificates refuse startup, a runner that is down only fails the
     runner operations. The list-databases call is a mutation (it opens an
     outbound connection with stored credentials, like testDestination).
- **Alternatives considered:** one catalog key for version and image (no
  room for a newer PostgreSQL client than the server); storing the last
  fetched chain server-side for `certIndex` (stale state, a TOCTOU window
  without the leaf binding); trusting a pin without leaf binding by default
  (a CA pin accepts any leaf the CA issues); auto-upgrading the TLS mode on
  pin confirmation (hides the opt-out state); fetching host keys from
  serve (serve must never reach targets or bastions); a test result
  instead of an error for pin mismatches (security events must not look
  like ordinary failures).
- **Consequences:** Admins re-authenticate for every connection change and
  compare certificates and host keys out of band. Moving a server or
  bastion requires a new confirmation. IP-literal targets need a tunnel or
  an opt-out. Operators with runner-only catalog extras cannot select them
  yet (follow-up). Security impact: credentials are write-only, sealed per
  row, never logged, audited or returned; pin and host-key mismatches are
  audited and alerted; serve still never reaches targets (all I/O through
  the runner's mTLS API); trust boundaries B6/B7 gain the FetchHostKey RPC
  (SECURITY.md updated).
- **Related:** D-034, D-035, D-106, D-117, D-122, D-123, D-127, D-131,
  D-132, D-133, D-134; tasks 2.11, 4.4, 5.5, 6.3; `internal/connections`,
  `internal/runner/ops_hostkey.go`, `internal/graph/schema/connections.graphqls`,
  `internal/db/migrations/00012_connections.sql`, `docs/guides/connections.md`.

## D-136: S3 multipart Complete detached from the caller and verified after an ambiguous failure (M2 review)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (M2 code review M11; awaiting the user's
  confirmation)
- **Context:** `s3store.Put` ran `CompleteMultipartUpload` on the caller's
  context. A caller that gave up right after the server committed (or a
  lost response whose SDK retry then got `NoSuchUpload` /
  `PreconditionFailed`) reported a failure for an object that exists, and
  the abort that followed could not remove it: the backup would be
  recorded as failed while an orphaned object stayed in the bucket.
- **Decision:** Once every part is stored, Complete runs on
  `context.WithoutCancel(ctx)` bounded by `completeTimeout` (10 min; S3
  may need minutes to assemble large objects); the abort on failure stays
  detached with `abortTimeout` (30 s). After a failed or ambiguous
  Complete the object is HEADed (detached, 30 s). It counts as **this**
  upload only if its size matches and at least one strong content check
  matches while none mismatches: the `bekup-sha256` metadata (when the
  caller supplied the SHA-256 up front), the composite SHA-256 native
  checksum (base64(sha256(part checksums))-N, `when_supported` mode) or
  the composite ETag (md5 of the part MD5s -N, only when every part ETag
  is a plain MD5). Then Put succeeds (warning
  `s3_complete_verified_after_error`); otherwise the classified Complete
  error is returned and the upload aborted. Size alone never counts.
- **Alternatives considered:** keeping the caller's context (the reported
  bug); trusting `NoSuchUpload` after a retry as success (another writer
  or a lifecycle abort produce the same answer); accepting a size match
  (any same-size object would pass); reading the object back to hash it
  (doubles the egress of every ambiguous Complete for large backups).
- **Consequences:** A cancelled backup may still end as a stored,
  verified object when the server committed first; the caller then gets
  success, never a failure for an object that exists. The composite ETag
  rests on MD5, but it only decides between "our object" and "error" when
  no SHA-256 check is available, and the backup format is itself
  authenticated (age) and SHA-256-verified on restore. Security impact:
  no new trust; never-overwrite (`If-None-Match: *`) and the abort
  guarantees are unchanged.
- **Related:** D-121, D-129; task 2.9; `internal/storage/s3store/put.go`,
  `internal/storage/s3store/put_test.go`.

## D-137: Docker-dependent tests skip locally but fail in CI (M2 review)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (M2 code review L23; awaiting the user's
  confirmation)
- **Context:** The runner, pinning, executor, spec and graph integration
  harnesses called `t.Skipf` when no (Linux) Docker daemon was reachable.
  In CI that turns the whole Docker-dependent suite silently green.
- **Decision:** Every "Docker missing or unsuitable" condition goes
  through `dockertest.Skipf(t, ...)` (`internal/testutil/dockertest`):
  it skips when `CI` is unset or false and **fails** the test when `CI`
  parses as true. The `go-integration` job checks `docker info` first and
  sets `CI: "true"` explicitly (GitHub sets it too). Skips unrelated to
  Docker (inner-harness guards, `-short`) are unchanged.
- **Alternatives considered:** always failing without Docker (breaks
  `just test-int` on machines without Docker); a separate env var such as
  `BEKUP_REQUIRE_DOCKER` (another knob CI could forget; `CI` is the
  convention every CI system sets).
- **Consequences:** A broken or missing Docker daemon in CI fails the
  build loudly. Local runs keep skipping. Security impact: the isolation
  tests of the runner (D-130, D-132, D-133) can no longer be skipped
  unnoticed in CI.
- **Related:** D-020, D-132; `internal/testutil/dockertest`,
  `.github/workflows/ci.yml`, `docs/development.md`.

## D-138: Runner hardening after the M2 review: image volumes, psql restrict, Docker networks, bastion targets, Docker floor and RPC fixes

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (M2 security review M1, M2, L4, L5, L6, L9 and code
  review H1, H2, M4, M5, L15, L21, L22; task 2.14 follow-ups; awaiting the
  user's confirmation)
- **Context:** The M2 reviews found gaps in the runner: official images
  declare `VOLUME`s, so Docker created a writable, persistent anonymous
  volume (e.g. `/var/lib/postgresql/data`, `/var/lib/mysql`) despite the
  read-only root; psql executed meta-commands (`\!`, `\o |cmd`,
  `\copy ... program`) from a restored globals script; the D-132 /29 could
  shadow the runner's resolver or default gateway; the target guard let
  broad `allow_cidrs` (172.16.0.0/12) and `allow_hosts` names reach Docker
  networks and their gateways (the host); a bastion could forward to any
  host it reaches; no Docker Engine floor; FetchServerCertificate bypassed
  the RPC normalization and audit; the startup reap ran while runs were
  already admitted; the final stream message could race a blocked output
  reader; the zeroing interceptor raced the pump goroutine; serve could not
  see the runner's extra images; IP literals in `targets.allow_hosts` only
  failed at runner start; the log redactor masked `version_key`.
- **Decision:**
  1. **Image volumes.** `PrePull`/`ensureImage` inspect `Config.Volumes`
     (`Docker.ImageInspect`); every path except `/tmp` gets a read-only,
     one-page tmpfs mount (`HostConfig.Mounts`, Docker's tmpfs defaults
     noexec/nosuid/nodev) via `spec.Params.ImageVolumes`, so no anonymous
     volume is ever created. More than 32 paths, a relative, unclean or `/`
     path refuses the image (`ErrImage`). Integration test: writing to the
     data directory fails in every pinned image and the container has only
     tmpfs mounts.
  2. **psql restrict.** Every psql run reads stdin first (`--file=-`) and
     stdin starts with `\restrict <64 hex chars>` from crypto/rand
     (`engine.PSQLRestrictPrefix`, `Command.StdinPrefix`, written after the
     preamble and zeroed with it); `spec.Build` refuses a psql command
     without a valid prefix and any other tool with one. The globals
     restore removes the dump's own complete `\restrict`/`\unrestrict`
     lines (`engine.StripRestrictLines`), which would otherwise end the
     restore with ON_ERROR_STOP; removing them cannot lift the runner's
     restriction. All pinned clients (13.23-18.6) support it (catalog
     floor CVE-2025-8714); the integration test runs a hostile script
     (`\!`, `\o |cmd`, `\copy ... program`, a guessed `\unrestrict`) with
     every client 13-18 and a pg_dumpall-style script that restores.
  3. **/29 forbidden set.** `aroundTarget` also refuses a /29 holding the
     runner's `/etc/resolv.conf` name servers, its default gateways
     (`/proc/net/route`, `/proc/net/ipv6_route`) and every Docker network
     gateway.
  4. **Docker networks in the guard.** The executor hands every Docker
     network's subnets and IPAM gateways to `targets.Guard.SetDockerNetworks`
     at Init, before every run and every minute. Such an address is denied
     (`docker network`) unless an `allow_cidrs` entry lying inside that
     Docker subnet contains it; a gateway needs an entry of exactly its
     address (/32, /128). `allow_hosts` never opens one.
  5. **Bastion targets.** `targets.bastion_targets` (string list,
     `<bastion>=<CIDR or host name>`; the config format has only two levels,
     so no array of tables): a bastion matched by name or any resolved
     address may only forward to its listed targets (IP literals in a CIDR,
     names exactly; with several matching entries all must allow).
     Unlisted bastions keep the previous rule (deny lists only), with a
     startup warning when the list is empty and a `runner_bastion_unrestricted`
     warning once per unlisted bastion in use.
  6. **Docker Engine floor 26.0.0**, checked at executor Init (retried with a
     clear error): 26.0.0 fixes CVE-2024-29018 (internal networks could
     reach external DNS through the embedded resolver); patched 25.0.5 /
     23.0.11 builds are not detected reliably and are refused.
  7. **FetchServerCertificate** runs in `operations` like every target RPC:
     validator, `pinning.Connect`, capture, `targetStatus` (normalized
     `runnerpb.Reason`) and a `runner_rpc_audit` line. The pin-mismatch
     status moves to `runnerpb.PinMismatchStatus` / `PinMismatchCheck`
     (metadata `check`); `pinning.Status`, `pinning.PinMismatch`, the
     duplicate ErrorDomain/reason constants, `pinning.PinVerifier`,
     `withCap` and a dead password-clearing line are removed (amends D-133
     item 4).
  8. **Startup reap** runs before `ready` is set: no run is admitted until
     it completed.
  9. **Stream sends.** Stdout/log relays and the final message share the
     output lock; `output.Final` marks the output abandoned, so a reader
     still blocked in Send after the executor's drain timeout never sends
     concurrently with, or after, the final message (every handler return
     path abandons it).
  10. **Zeroing interceptor** guards its held messages with a mutex; a
      credential message received after the handler returned is zeroed at
      once.
  11. **Steps.** Sequential containers of one run (rename swap: prepare,
      swap1-3, rollback) use `RunSpec.Step`; network and container are
      `bekup-run-<run id>-<step>`, so a step never collides with a
      predecessor whose cleanup is pending.
  12. **ListImages** RPC (control class) returns the merged allowlist
      (engine, version key, digest reference, client version, legacy,
      extra) and `runnerclient.Client.ListImages` exposes it for serve.
  13. **Config validation** rejects IP literals (and all-numeric names) in
      `targets.allow_hosts`/`deny_hosts`.
  14. **Redactor.** Exact non-secret names (`version_key`,
      `source_version_key`, `target_version_key`, `client_version_key`,
      `image_key`, `catalog_key`, and the audit flags `host_key_cleared`,
      `host_key_changed`, `host_key_confirmed`) are not redacted by the log
      handler or the audit redactor; every other `*key*` name (including
      `host_key`) still is.
  15. **InsecureSkipVerify inventory:** exactly three uses (pin capture,
      D-133; TLS mode UNVERIFIED, D-123/D-134/D-135; app DB
      `tls_mode = require`, D-100/D-103); comments corrected (D-133's "the
      only InsecureSkipVerify" is outdated).
  16. **Kill without a drained attach.** Docker's SIGKILL waits until the
      container stopped, which blocks while its attached output is not
      read (found by the cancel integration test: a canceled dump took
      30 s to be killed). The executor closes the attach connection right
      before SIGKILL (the output is discarded anyway at that point).
  17. **SSH details shared:** the runner validates SSH user names with
      `sshpolicy.ValidateUser`; tunnel errors (`*tunnel.Error` wrapping the
      sshpolicy details) map to `SSH_HOST_KEY_MISMATCH`,
      `SSH_HOST_KEY_UNSUPPORTED` and `SSH_AUTH_FAILED` both in `targetStatus`
      and in `executor.Status`.
- **Alternatives considered:** a `VolumeDriver`/`--volumes-from` trick or
  deleting volumes after the run (they would still exist during it and on
  a crash); a writable tmpfs over volumes (gives the tool a scratch area it
  never needs); filtering psql meta-commands in Go (incomplete; psql's own
  restrict mode is authoritative); refusing globals dumps with `\restrict`
  lines (every current pg_dumpall writes them); denying every RFC 1918
  address that a Docker network uses only at dial time (no clear
  configuration rule); `[[targets.bastions]]` tables (needs a third config
  level); detecting patched 25.x/23.x builds from version strings
  (backports are distribution specific).
- **Consequences:** Operators whose databases run in Docker networks on the
  same daemon must list the Docker subnet (or a narrower CIDR inside it)
  explicitly; broad private ranges no longer reach them. Docker < 26 stops
  the executor. Globals restores lose the dump's own restrict key lines
  (the runner's key replaces them). Security impact: closes a persistent
  writable path, a psql command-execution path from restored scripts, a
  /29 shadowing of runner infrastructure, a pivot to the Docker host and
  other containers, and an optional bastion pivot; SECURITY.md B4/B8 and
  `docs/security/target-allowlist.md` updated.
- **Related:** D-029, D-031, D-123, D-130, D-132, D-133, D-134, D-139; task
  2.14; `internal/runner/{executor,spec,targets,validate,pinning}`,
  `internal/engine`, `internal/config`, `internal/logging`,
  `proto/runner/v1/runner.proto`.

## D-139: Pin anchors are a self-signed leaf or a private CA; the same-CA leaf window is accepted residual risk

- **Date:** 2026-10-27
- **Status:** accepted
- **Decided by:** user
- **Context:** D-133 lets an admin pin a CA. With a CA anchor the native
  client cannot check the leaf key, so another leaf of the same CA passes
  the native client while the Go pre-flight (leaf SPKI) refuses it; the
  reviews asked how to treat this window (task 2.7a acceptance note) and
  whether a public CA may be pinned at all.
- **Decision:** A pin anchor must be a **self-signed leaf or a private
  CA**. An anchor that is, or chains to, a certificate of the system trust
  store (a public CA) is refused: by the runner in every request
  (`validate.checkPins`) and by serve when a pin is confirmed (helper
  `pinning.IsPublicAnchor`, x509 `SystemCertPool`; the connections service
  calls it). With a private CA, the window in which another leaf of the
  same CA passes the native client between the pre-flight and the
  container's connection (the leaf SPKI is checked by the pre-flight only)
  is **accepted residual risk**: issuing such a leaf requires the
  operator's own private CA. The 2.7a acceptance note is resolved this way.
- **Alternatives considered:** allowing public CAs with a leaf SPKI pin
  (the native client would trust every certificate the public CA issues
  for the name); requiring self-signed leaves only (breaks rotation with a
  private CA); passing the leaf SPKI to the native clients (not supported
  by libpq / the MySQL clients).
- **Consequences:** Targets with publicly issued certificates must be
  pinned by their leaf only if self-signed, or use a private CA; such
  targets are otherwise limited to TLS mode UNVERIFIED (audited downgrade).
  Without a system trust store the check cannot recognize public CAs
  (the shipped images carry one). Amends D-133 items 6 and 7. Security
  impact: removes the public-CA misissuance path; the same-private-CA leaf
  window is documented residual risk.
- **Related:** D-034, D-123, D-133, D-135, D-138; tasks 2.7a, 2.11, 2.14;
  `internal/runner/validate/anchor.go`, `internal/runner/pinning`.

## D-140: SSH policy and host names in one place, endpoint-bound host-key and pin confirmation, no password reuse after retargeting (M2 review)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-27, user accepted all M2 proposals)
- **Decided by:** agent with user approval (M2 review fixes, task 2.14; security-first
  defaults, awaiting the user's confirmation)
- **Context:** The M2 reviews found the SSH host-key/algorithm policy
  duplicated in `internal/tunnel` and `internal/storage/sftp` (and partly
  in the connection/destination services), four slightly different
  host-name validators (the SFTP backend accepted names the SSRF guard
  refused), a host-key confirmation not bound to the endpoint the key was
  fetched from, a captured-chain pin confirmation that did not check that
  the leaf chains to the chosen anchor, stored passwords that stayed in
  place (and would be sent) when an endpoint was changed, a free host/port
  host-key fetch with distinguishable errors, an SFTP host-key mismatch
  reported as `permission_denied`, and soft-deleted destinations that the
  workers could no longer open.
- **Decision:**
  1. **`internal/sshpolicy`** is the only SSH client policy: transport
     algorithms, accepted host keys (ed25519, ECDSA, RSA ≥ 2048 with
     SHA-2), host-key algorithms per pinned type, the pin format
     (`Pin`, `NormalizedPin`), the pin callback (`NewPinCallback`,
     `PinCheck.Violation`, `MismatchError`), admin-entered host-key lines
     (`ParseHostKeyLine`, one line, no options, **at most 8 KiB** for
     connections and destinations alike), client keys (`ParseClientKey`
     with best-effort zeroing), user names (`ValidateUser`), the bounded
     handshake (`Handshake`) and the keepalive loop (`Keepalive`).
     `tunnel.HostKeyPin`, `tunnel.HostKeyMismatchError`,
     `sftp.HostKeyPin` and `sftp.HostKeyMismatchError` are aliases;
     `tunnel.Dial` now returns a mismatch as `*tunnel.Error` (kind
     `ErrHostKeyMismatch`) wrapping the `*MismatchError`.
  2. **`internal/hostname.Validate`** is the only host rule (IP literal
     without zone, or RFC 1123 name: labels of letters, digits and inner
     hyphens, no underscore, no trailing dot, not digits-and-dots only),
     used by the engine (`engine.ValidateHost`), the SSRF guard (after
     lowercasing and stripping one trailing dot), the tunnel and the SFTP
     backend (which also accepts a bracketed IPv6 literal).
  3. **`internal/serviceutil`** holds what connections and destinations
     share: `FieldError` (aliased by both), the per-user rate limit plus
     concurrency bound (`Limiter`, `RateLimitedError`, `ErrBusy`),
     `IsUniqueViolation`, and the API-facing SSH checks
     (`CheckSSHPrivateKey`, `HostKeyLine`).
  4. **Endpoint-bound host-key confirmation.** `fetchSshHostKey` and
     `fetchDestinationHostKey` return the host and port they probed;
     `confirmSshHostKey` / `confirmDestinationHostKey` take
     `expectedHost`/`expectedPort` and fail with CONFLICT under the row
     lock if the record points elsewhere. `fetchDestinationHostKey` now
     takes the destination **id** and probes only the stored host/port
     (no free host/port argument), and its failures are normalized like
     the runner's: refused address, unsupported key type, otherwise one
     "unreachable" category (refused, filtered, timeout, handshake
     failure, non-SSH service), reported as CONNECTION_FAILED.
  5. **Chain check at pin confirmation.** With `certIndex`, the captured
     chain must verify with the chosen anchor as the only root, for the
     connection's host, at the confirmation time (the runner's pre-flight
     check); otherwise VALIDATION. An out-of-band PEM is not checked
     against a capture (it may be staged before the server presents it).
     Public anchors are refused at confirmation (`pinning.IsPublicAnchor`,
     D-139).
  6. **No password reuse after retargeting.** A stored password is never
     sent to a new endpoint: changing a connection's host or port, adding
     or removing its tunnel or moving its bastion (host/port) requires the
     database password to be entered again or cleared in the same update;
     changing the bastion's host, port or user requires the SSH password
     again (or a key); changing an SFTP destination's host, port, user or
     base path requires its password again (or a key). Otherwise
     VALIDATION. Private keys and TLS client keys may stay (they do not
     reveal the secret to the peer). The update's audit entry lists the
     re-entered fields (`reentered_for_new_endpoint`).
  7. **`storage.CodeHostKeyMismatch`** (`host_key_mismatch`): the SFTP
     backend classifies a pin violation with it (an unsupported key as
     `unsupported`); `testDestination` records it as the security event
     `destination.ssh_host_key_mismatch` (audited, fail closed; WARN log;
     alerted in task 5.5).
  8. **Workers open deleted destinations:** `destinations.Service.Open`
     reads with the unfiltered `GetDestinationIncludingDeleted`; the API
     (list, get, test, changes) keeps hiding deleted rows.
  9. The unique name index of connections ignores temporary rows
     (`WHERE deleted_at IS NULL AND NOT temporary`).
  10. **`prePullImage` is admin and maintainer** again, as D-106 allows
      (the narrowing noted in D-135 item 7 is withdrawn by the user).
- **Alternatives considered:** keeping two synchronized copies of the SSH
  policy (drifted already); one validator per layer with tests for the
  subset relation (still drifts); binding host-key confirmation to
  `updatedAt` (any unrelated edit would invalidate a fetch); verifying an
  out-of-band PEM against a fresh capture (breaks staging a pin before a
  rotation); clearing stored passwords silently on retargeting (the
  connection would stop working without a visible reason); keeping a free
  host/port fetch for SFTP (a port-scanning oracle).
- **Consequences:** Moving a server or bastion now needs the password in
  the same update (or a key). An SFTP destination must be saved before its
  host key can be fetched. Hosts with underscores or a trailing dot are
  refused everywhere. Security impact: one audited policy for every SSH
  client; no layer accepts a host another refuses; stored passwords cannot
  be redirected to an attacker-chosen endpoint by an edit; host-key and
  certificate confirmations cannot be applied to another endpoint than
  the one the admin compared; host-key mismatches on destinations are
  security events.
- **Related:** D-034, D-035, D-106, D-120, D-122, D-128, D-131, D-133,
  D-135, D-139; task 2.14; `internal/sshpolicy`, `internal/hostname`,
  `internal/serviceutil`, `internal/connections`, `internal/destinations`.

## D-141: Backup completeness evidence: source stats and test-restore comparison

- **Date:** 2026-10-27
- **Status:** accepted
- **Decided by:** user
- **Context:** Dump verification (D-134), checksums, signed manifests and
  restore staging prove a backup is intact and untampered, but not that it
  is complete: a backup role lacking privileges silently skips objects,
  non-transactional MySQL/MariaDB tables are dumped inconsistently, and a
  non-superuser PG globals dump omits role passwords. The planned "row-count
  sanity check" for test restores was too weak to catch this.
- **Decision:** At dump time the runner records source statistics (object
  counts; per-table row counts) and completeness warnings (task 3.10).
  PostgreSQL stats are taken inside an exported snapshot that `pg_dump
  --snapshot` reuses, so they are exact; MySQL/MariaDB stats are taken in a
  consistent snapshot just before the dump and tables written meanwhile are
  marked approximate. A per-job `verification` level chooses exact counts
  (`deep`) or cheap estimates (`standard`). The stats are signed into the
  manifest. Scheduled test restores (task 4.6) compare the restored scratch
  database against them: object counts exactly, row counts exactly where the
  stats are exact, warnings with deltas otherwise; missing objects fail the
  check. Warnings and verdicts are shown per run and alerted.
- **Alternatives considered:** row-count sanity only (too weak); full
  per-table content checksums (exact but costly and engine-specific; may be
  added later as an opt-in `paranoid` level); locking MySQL tables to get an
  exact snapshot (needs RELOAD and blocks writes).
- **Consequences:** A backup can be shown to be complete, not just intact.
  `deep` verification costs a `count(*)` per table at dump time. The stats
  contain table names and counts but no data, and are covered by the
  manifest signature; they are as sensitive as the manifest itself.
- **Related:** tasks 3.1, 3.6, 3.10, 4.6, 4.7, 6.6, 7.1; D-134, D-133.

## D-142: Job encryption keys: tables, versioning, AAD, authorization split and the service API (task 3.2)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals); item 5 (maintainers may change keys) superseded by D-154
- **Decided by:** agent with user approval (task 3.2)
- **Context:** D-008, D-016 and D-040 fix the policy (age on by default,
  versioned passphrase or public recipient, generated keys shown once,
  admin-only opt-out, passphrase export) but not the storage, the
  authorization split between maintainers and admins or the API the
  pipeline (3.1), the job CRUD (3.5) and restores (4.1) build on. The jobs
  table does not exist yet.
- **Decision:**
  1. **Tables** (migration 00013): `job_encryption` (job_id PK, mode
     `passphrase|recipient|none`, `opted_out_by/at`, `opt_out_reason`;
     mode `none` iff the opt-out fields are set), `job_passphrases`
     (`(job_id, version)` PK, `sealed_passphrase`, created_by/at,
     retired_at) and `job_recipients` (`(job_id, version)` PK, canonical
     `age1...` recipient, fingerprint, label, `generated`, created_by/at,
     retired_at). A partial unique index allows one active (unretired)
     version per job and table. `job_id` has no foreign key until task 3.5
     adds `REFERENCES jobs (id)`. `bekup_app` has no DELETE/TRUNCATE on the
     three tables and only `UPDATE (retired_at)` on the key tables; a
     trigger lets `retired_at` change only from NULL to a timestamp, so a
     key is never deleted, revived or rewritten (old artifacts need old
     versions).
  2. **Sealing:** `Keyring.Seal` under AAD
     `job_passphrases:<job_id>:v<version>` (table, job id, version as the
     field), so a ciphertext moved to another job or version fails to
     open. Versions are allocated under a row lock on `job_encryption`.
  3. **Recipients:** only age X25519 recipients (no SSH, plugin or
     post-quantum recipients), stored canonical. The fingerprint is the
     lowercase hex SHA-256 of the recipient string, identical to the
     manifest's `pipeline.RecipientFingerprint`. A generated keypair's
     identity is returned once (string + age-keygen style identity file
     `bekup-job-<id>-v<n>.agekey`) and never stored or logged.
  4. **User-chosen passphrases** need at least 20 characters, no control
     characters or surrounding white space, at most 1024 bytes and zxcvbn
     score 4 (backup files face offline guessing for their whole
     lifetime). Generated passphrases are 10 EFF words (D-088).
  5. **Authorization** (existing operations, D-106): key changes
     (set/generate passphrase or keypair) are `job.encryption_keys_edit`
     (admin + maintainer, step-up: maintainers manage jobs and the
     encryption config is part of the job editor); the opt-out is
     `job.encryption_opt_out` (admin, step-up); the export is
     `job.passphrase_export` (admin, step-up). **Opting back in** (or
     setting a key while opted out, which also opts in) is a tightening
     and needs only `job.encryption_keys_edit`. Reading the encryption
     status (mode, versions, recipient, badge) is nested under the job and
     inherits `job.view`; it never contains key material. The service
     re-checks every change with `Policy.Authorize` on the context's
     principal and requires the actor to be that principal.
  6. **Audit and alerts:** `job.encryption_passphrase_set` (+
     `secret.changed`), `job.encryption_recipient_set` (public recipient,
     fingerprint, label), `job.encryption_opted_out` + `security.opt_out`
     (`setting: job.encryption`, reason), `job.encryption_opted_in`,
     `job.passphrases_exported` (versions). The opt-out and the export
     raise an alert hook (`AlertFunc`, logged until task 5.5). The export
     is returned only after its audit entry committed.
  7. **Service API** (`internal/jobkeys`): `Status`, `Passphrases`,
     `Recipients`, `SetPassphrase`, `GeneratePassphrase`, `SetRecipient`,
     `GenerateAgeKeypair`, `OptOut`, `OptIn`, `ExportPassphrases` (+ a
     documented text file format), `InitTx` (task 3.5's job creation in
     its transaction: passphrase by default, generated unless supplied;
     never opted out), and for workers `KeyMaterialFor`,
     `Encryptor(job)` (`pipeline.Encryptor`), `PassphraseForVersion` and
     `RecipientForVersion`. A job without a `job_encryption` row fails
     closed (`ErrNotConfigured`).
- **Alternatives considered:** a single key table for both kinds (rejected:
  different columns and invariants); hard deletes of old passphrases after
  retention (rejected: an artifact can outlive any guess, and a deleted
  version makes its artifacts unrecoverable); admin-only key changes
  (rejected: the plan gives maintainers the job editor; changes need
  step-up and are audited); admin-only opt-in (rejected: re-enabling
  encryption only tightens security).
- **Consequences:** Old passphrase versions live as long as the instance
  (sealed); a stolen master keyring plus a DB dump reveals all versions (as
  for every stored secret, D-046). A maintainer with step-up can replace a
  job's key with one they know, which affects only future artifacts and is
  audited. Task 3.5 must add the foreign keys, call `InitTx` in
  `createJob` and wire the GraphQL fields; task 3.6 stores
  `PassphraseVersion`/recipient version per artifact.
- **Related:** tasks 3.1, 3.2, 3.5, 3.6, 4.1, 4.7, 5.5, 6.9; D-008, D-016,
  D-040, D-046, D-085, D-086, D-088, D-106.

## D-143: Schedule format and next-run math: anchored intervals, wall-clock DST rules for cron, catch-up decision, schedule limits

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.3)
- **Context:** D-009 and D-044 fix the schedule kinds and the DST rules but
  not the stored format, the phase of multi-day intervals, the month-end
  rule in detail, how cron meets the DST rules, what "collapse into one
  catch-up run" returns, or the input limits.
- **Decision:**
  - A schedule is a compact JSON object (`internal/scheduler.Schedule`):
    `{"kind":"manual"}`; `{"kind":"interval","every":N,"unit":
    "minutes|hours|days|weeks|months","anchor":"YYYY-MM-DDTHH:MM",
    "weekday":"monday" (weeks),"day_of_month":31 (months),"timezone":
    "Europe/Amsterdam"}`; `{"kind":"cron","cron":"0 3 * * 1-5",
    "timezone":"..."}`. The anchor is a local date-time in the zone and is
    the first occurrence; it fixes the phase of "every 2 days/weeks/months"
    and the time of day. `weekday`/`day_of_month` default to the anchor's
    and are stored explicitly (canonical form). Parsing is strict (unknown
    fields, trailing data and > 1 KiB rejected).
  - Minutes/hours: anchor instant + k·interval in elapsed time. Days/weeks:
    anchor date + k·N days (weeks: from the first configured weekday on or
    after the anchor date) at the anchor time; months: anchor month + k·N,
    always targeting `day_of_month`, clamped to the last day of shorter
    months (31 → Feb 28/29 → Mar 31, never drifting).
  - Local times are resolved deterministically (not via `time.Date`, whose
    choice is unspecified): ambiguous → the earlier instant (runs once);
    nonexistent → read with the offset before the transition, i.e. shifted
    forward by the gap (02:30 → 03:30; Lord Howe 02:15 → 02:45).
  - Cron: robfig/cron/v3 parses only (5 fields, names, ranges, steps, `?`,
    plus `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`,
    `@midnight`, `@hourly`; `@every`, `@reboot`, seconds and `TZ=`/
    `CRON_TZ=` prefixes rejected; parser panics recovered; empty fields and
    expressions without any matching date rejected). Occurrences are
    computed by bekup on the wall clock with the same resolution rules,
    because robfig's `Next` skips nonexistent times, fires twice in the
    fall-back hour and drifts by 30 minutes in half-hour DST zones. A
    matching time that is shifted onto an existing occurrence runs once; in
    the repeated fall-back hour an hourly cron runs only in the first pass.
  - `Due(schedule, next_run_at, now)` returns at most one run, standing for
    the stored `next_run_at` (River unique key), flags `CatchUp` when more
    than one occurrence passed, and the new `next_run_at` = first
    occurrence strictly after now (missed ones are skipped).
  - Limits: `scheduler.min_interval` (new config key, default 5m, 1m–24h)
    applies on create/update to minute/hour intervals and to the closest
    pair of cron occurrences; computation never re-checks it, so tightening
    it does not break existing jobs. Hard caps: every ≤ 1440 minutes,
    168 hours, 366 days, 52 weeks, 24 months; anchor years 2000–2199; cron
    ≤ 128 characters; previews ≤ 50; cron search ≤ 29 years. Zones must be
    IANA names (no `Local`, no paths) and `time/tzdata` is embedded in the
    binary and the package.
- **Alternatives considered:** separate time-of-day/weekday/day fields
  without an anchor (no phase for N > 1); clamping relative to the previous
  occurrence (drifts 31 → 28 forever); robfig's own `Next` (violates
  D-044 at DST edges); `rapid` for property tests (a new dependency; seeded
  `math/rand/v2` loops with fixed seeds suffice and stay deterministic).
- **Consequences:** Schedules behave predictably across DST in every zone,
  including 30-minute and midnight transitions; one well-defined run per
  missed period. The `min_interval` floor and input caps bound the DoS
  surface (SECURITY.md SC-2/SC-3). A cron expression valid for robfig may
  be refused here (seconds, `@every`, `TZ=`, dense minutes).
- **Related:** tasks 3.3, 3.4, 3.5, 6.5; D-009, D-044; `internal/scheduler`,
  `docs/guides/jobs-and-schedules.md`.

## D-144: Backup pipeline: bounded fan-out with lag drop, artifact naming and the signed manifest format (task 3.1)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.1)
- **Context:** The plan fixes the chain (counter → zstd → age → SHA-256 →
  bounded per-destination fan-out, laggards dropped, run `partial`) and the
  manifest content (D-038, D-087, D-141), but not the buffering and drop
  semantics, the artifact name of the globals dump, the manifest schema,
  its serialization or how a verifier binds it to the database.
- **Decision:**
  1. **Fan-out.** The stored bytes are cut into immutable chunks
     (default 1 MiB, 4 KiB–16 MiB) shared by all targets; each target has a
     bounded channel (default 8 chunks, max 256) read by its `Put`. The
     fan-out holds at most (buffer + 2) × chunk bytes, plus one chunk per
     dropped target whose backend has not returned yet; a live but slow
     target applies backpressure to the source, a dropped one never does.
     A target is dropped when its `Put` fails, or when its buffer stays
     full for `LagTimeout` (default 2 min; one deadline per chunk shared by
     all blocked targets, so N slow targets cost one threshold, not N);
     after the stream ended, a target that has unread data but reads
     nothing for `LagTimeout` is dropped too. Dropping cancels the target's
     `Put` context. The reader handed to `Put` returns `io.EOF` **only**
     after every byte was queued and only to a target that was not dropped;
     on abort, cancellation or drop it returns an error, so the backends'
     atomic finalize (D-121, D-128, D-129) never publishes a partial
     object. A target whose `Put` reports success with a size or SHA-256
     different from the streamed bytes (or without reading to EOF) counts
     as dropped (`verify`) and the object is deleted best-effort. All
     targets dropped → `ErrAllDestinationsFailed`; `ctx` canceled → every
     `Put` canceled.
  2. **Compression and encryption.** zstd (klauspost) level 1–22 (default
     3), encoder concurrency 1–8 (default 2), frame CRC on. age with a
     passphrase (scrypt work factor 18 by default, at most 20, D-088) or
     1–16 X25519 recipients; mode `none` only with the explicit
     `OptedOut` flag (D-008); the zero configuration is invalid. The
     pipeline borrows the passphrase and never destroys it. The interface
     to task 3.2 is `pipeline.Encryption` / `pipeline.Encryptor`.
     Reading (`OpenArtifact`) uses the bounded crypto helpers and a zstd
     decoder with one goroutine and a 64 MiB maximum window. The SHA-256 is
     computed over both the raw dump and the stored artifact.
  3. **Names.** Databases: `{enc(db)}.{dump|sql}.zst[.age]`; globals:
     `globals.globals.sql.zst[.age]` (the extra `globals` extension part
     keeps a database named `globals` from colliding). `.age` is omitted
     only for opt-out jobs.
  4. **Manifest v1** (`docs/reference/artifact-format.md`): format version,
     bekup version, instance key ID, job ID + slug, run ID, run timestamp
     (the directory timestamp, UTC seconds), created_at, engine (kind,
     catalog version key, server version), entries (`db` as a reversible,
     never-hashed percent encoding of the full name; kind; path relative to
     the manifest; raw/stored size; stored/raw SHA-256; compression;
     encryption mode + passphrase version, or recipient version (the
     job's recipient key generation, D-142) + recipient fingerprints =
     hex SHA-256 of the canonical `age1...` string), an optional extensible
     `stats` object (task 3.10) and warnings. Validation is strict (paths
     must match the db name and encryption mode, one globals entry at
     most, bounded sizes and counts).
  5. **Canonical JSON:** UTF-8, sorted keys (byte order), no whitespace,
     no duplicate keys, Go `encoding/json` string escaping without HTML
     escaping, **integers only** (int64; other numbers must be strings),
     depth ≤ 32, ≤ 16 MiB. `manifest.json` is the canonical object
     `{"key_id","manifest","signature"}`; the signature is ed25519 over
     `"bekup-sig-v1" 0x00 "manifest" 0x00 <canonical manifest>` (D-087).
     The verifier re-canonicalizes the embedded manifest (so a reformatted
     file still verifies), then requires the typed manifest to re-serialize
     to exactly the signed bytes (no case-folded keys, unknown fields or
     alternative timestamp forms).
  6. **Verification.** `Verify(file, keys, binding)` checks the signature
     with the named key (retired keys verify while known), that the
     envelope's key ID equals the signed one, and the binding: job ID and
     run ID (required), job slug and run timestamp (when given) → a
     mismatch is `ErrReplay`; the exact set of entries with stored size and
     SHA-256 (raw SHA-256 when given) → `ErrBindingMismatch`. A binding
     without job/run IDs or entries is refused. `VerifySignature` (no
     binding) is reserved for offline tools without the database (4.7).
  7. **Writing.** `WriteManifest` signs and puts `manifest.json` to each
     target concurrently (expected SHA-256, never overwrite), only after
     the entries are finalized and only to targets holding all entries;
     per-target outcomes are returned.
- **Alternatives considered:** one `io.Pipe` per target without chunk
  sharing (copies per target, no lag detection); dropping on per-chunk
  timers (N slow targets would stall N × threshold); storing the manifest
  as base64 of the signed bytes (robust, but unreadable for operators);
  RFC 8785 JCS with floats (needs ECMAScript number formatting; integers
  suffice); signing the file bytes as written (any reformatting breaks
  verification); `globals.sql.zst.age` (collides with a database named
  `globals` for SQL dumps).
- **Consequences:** Memory per artifact is bounded and configurable; a bad
  destination costs at most one lag threshold. Restores (4.1) must call
  `Verify` with the run's DB records, then compare the staged artifact's
  SHA-256 with the verified entry. Stats producers (3.10) must use
  integers and strings only. Security impact: replayed or swapped
  manifests and artifacts are rejected; a compromised `serve` can still
  sign (T4, unchanged).
- **Related:** tasks 3.1, 3.2, 3.6, 3.10, 4.1, 4.7; D-008, D-038, D-087,
  D-088, D-121, D-128, D-129, D-141, D-142; `internal/pipeline`,
  `docs/reference/artifact-format.md`.

## D-145: Scheduler loop and River setup: minimal jobs table, skip records, kind catalog, IDs-only args, queue timeouts and process model (task 3.4)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.4)
- **Context:** D-043 and D-044 fix the River settings and the scheduler
  transaction, but jobs (task 3.5) and the backup worker (3.6) do not exist
  yet, River v0.49 has constraints the plan does not mention (required
  unique states, insert-time kind checks, an owner-only reindexer), and
  the plan leaves open where skipped occurrences go, how timeouts map to
  per-job limits, how the process model splits work and how "args never
  contain secrets" is enforced.
- **Decision:**
  1. **Minimal `jobs` table now** (migration 00014): `id`, `enabled`,
     `schedule` (canonical D-143 JSON, object ≤ 1 KiB), `next_run_at`,
     `last_scheduled_at`, `deleted_at`, timestamps, a partial due index.
     Task 3.5 extends it with `ALTER TABLE` and adds the foreign keys from
     the 3.2 key tables; `bekup_app` cannot DELETE jobs (soft delete).
     The jobs service (3.5) sets `next_run_at` (= `scheduler.Next(now)`)
     on create, schedule change and enable; the loop only handles rows
     with a non-NULL `next_run_at <= now`, never `{"kind":"manual"}`
     (filtered in SQL and by `Due`). Chosen over a `DueSource` interface
     because sqlc needs real tables and 3.5 extends rather than replaces.
  2. **Loop:** every `scheduler.tick_interval` on every serve instance
     with `scheduler.enabled` (default on; independent of workers): per
     batch (`scheduler.batch_size`, default 100, ≤ 1000; at most 20
     batches per tick) one transaction locks due jobs `FOR UPDATE SKIP
     LOCKED`, evaluates `Due` without re-checking `min_interval`, inserts
     `backup_run {job_id, scheduled_for, trigger: schedule|catch_up}`,
     and advances `next_run_at`/`last_scheduled_at`. A River duplicate
     (the job's previous run is still active) skips the occurrence; an
     undecodable/uncompilable schedule or one without a further
     occurrence sets `next_run_at` NULL. Both are recorded in a new
     `schedule_skips` table (`still_running` | `unschedulable`; logged,
     metric hook), not in the audit log (operational, not security
     events); rows are deleted after 90 days by a periodic maintenance job
     (`schedule_skip_cleanup`, every 6h). A tick is bounded by 30s;
     shutdown mid-tick rolls back. The clock is injectable; metrics go
     through `scheduler.Metrics` (no-op until 5.6).
  3. **Queues and timeouts:** `backup`, `restore`, `retention`, `notify`,
     `maintenance` with per-process concurrency `[workers]
     *_queue_workers` (new keys; defaults 4/2/2/8/1, ≤ 10000). Each
     queue has one attempt timeout: `backup_timeout` (the cap of the
     per-job timeout of 3.5; the 3.6 worker enforces the job's own
     shorter timeout inside `Work`, because River's `Timeout` cannot read
     the job row and args carry IDs only), `restore_timeout`, new
     `retention_timeout` (1h), `notify_timeout`, maintenance 15 min.
     `queue.Register` wraps every worker so its `Timeout` is the queue's
     (the worker's own is ignored). Default attempts per queue: backup 2,
     restore 1 (validated, kinds cannot raise it), retention 3, notify
     10, maintenance 3. `RescueStuckJobsAfter` = longest queue timeout +
     1h (19h with defaults).
  4. **Uniqueness:** unique kinds use `ByArgs` on the fields tagged
     `river:"unique"` (`backup_run`: `job_id` only) and `ByState` =
     available, **pending**, scheduled, running, retryable. River v0.49
     requires `pending` in any custom set; bekup never creates pending
     jobs, so this equals D-043's list. Completed, canceled and discarded
     never block.
  5. **Kind catalog and insert path:** every kind (queue, attempts
     override, unique flag, args type) is declared in
     `internal/queue/kinds.go`; `Client.InsertTx(ctx, tx, args, opts)` is
     the only insert path and applies the catalog's options, so an
     insert-only client (`serve --no-worker`) inserts exactly as a worker
     would. River's insert-time "kind not registered" check is disabled
     (`SkipUnknownJobCheck`; the catalog check replaces it) and workers set
     `FetchOnlyKnownKinds`, so a kind whose worker is not deployed (yet,
     or in an older binary during a rolling upgrade) waits instead of
     failing as unknown.
  6. **IDs-only args:** `queue.CheckArgs` walks the args type: fields may
     be only `uuid.UUID`, `time.Time`, bool, integers, named string types
     (enums) and pointers/slices/structs of those; plain strings, byte
     slices/arrays, maps, interfaces and floats are rejected, as is any
     Go or JSON field name matching
     password/passphrase/pwd/key/token/secret/identity/credential/private/
     cookie/dsn. It runs in `queue.New` (startup fails closed), in
     `Register`, and in a test over the whole catalog. River's logger is
     wrapped to drop `args`/`encoded_args`/`metadata`/`output` attributes.
  7. **River housekeeping:** completed rows kept 24h, canceled and
     discarded 7 days (the run history lives in bekup's tables);
     reindexer disabled (`bekup_app` does not own River's indexes;
     operators can `REINDEX` as `bekup_owner`).
  8. **Process model:** `serve` runs HTTP + scheduler + workers;
     `serve --no-worker` keeps the scheduler and an insert-only client;
     `bekup worker` runs only the workers (no HTTP, no scheduler) after the
     same checks as serve (CheckAppRole, schema version, keyring KCV +
     instance key, weakened DB TLS audit) with application name
     `bekup-worker`. Shutdown: River stops fetching, running jobs get
     `workers.shutdown_timeout` (new, default 2m, ≤ 1h), then their
     contexts are canceled and the process waits at most 30s more.
     Compose gives serve a 180s stop grace period. `database.max_conns`
     default rises from 10 to 25 (a worker process wants the sum of its
     queue workers + 4; startup warns otherwise).
  9. **Registration API for later tasks:** add the kind + args type to
     the catalog, implement `river.Worker[T]`, register it in
     `app.registerWorkers` with `queue.Register(ws, w)` (periodic jobs via
     `ws.AddPeriodic`).
- **Alternatives considered:** a `DueSource` interface with a test table
  (sqlc cannot generate queries against a missing table; 3.5 would rewrite
  them); recording skips in the audit log (hash-chained security log
  flooded by operational events) or only in logs (no record for the UI
  and dead-man logic); the scheduler only in processes with workers
  (couples two unrelated concerns; SKIP LOCKED makes it safe anywhere);
  the scheduler also in `bekup worker` (the plan's worker runs only
  workers; one place to toggle it); per-job timeouts in args (args are
  IDs only) or River's per-job `Timeout` reading the DB (no context,
  blocking in River's executor); a deny-list of field names alone
  (a free-form string field could still carry a secret); River's own
  `SoftStopTimeout` (the explicit stop → cancel sequence logs and bounds
  the hard stop too).
- **Consequences:** runs of one job never overlap, across any number of
  serve and worker processes; scheduled runs are never lost to a crash
  mid-tick. Until 3.6 registers the backup worker, enqueued `backup_run`
  jobs wait in `available`. Later kinds must use IDs and enums only. A
  backup run can last at most `backup_timeout`; raising a job's timeout
  above it is refused by 3.5. Security impact: args cannot carry secrets
  by construction (WK-1); stuck or hung jobs are bounded (WK-15); the
  scheduler is safe to replicate (SC-1); unschedulable jobs are visible
  (SC-5). `bekup worker` has the same trust as serve (B3/B4/B5) without
  an HTTP listener.
- **Related:** tasks 3.3, 3.5, 3.6, 3.8, 4.x, 5.3, 5.4, 5.6; D-025, D-042,
  D-043, D-044, D-099, D-119, D-143; `internal/queue`,
  `internal/scheduler`, `internal/app`, `docs/architecture.md`,
  `docs/operations.md`.

## D-146: Backup jobs: data model, service, ownership queries, delete-in-use checks and the GraphQL API (task 3.5)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.5)
- **Context:** The plan lists the job columns and the roles (D-011, D-012,
  D-067, D-106) and tasks 3.2-3.4 left hooks (D-142 `InitTx` and the key
  table foreign keys, D-145 the minimal `jobs` table, D-131/D-135
  `checkUnused`, D-106 `Ownership`). Open were: storage of durations, the
  slug rule, defaults, what update/enable/delete do to `next_run_at`,
  whether owners keep access to deleted jobs, which users may be owners,
  manual runs of disabled jobs, and the GraphQL shape.
- **Decision:**
  1. **Schema** (migration 00015, `ALTER TABLE jobs`): `name` (1-100,
     unique case-insensitively among non-deleted jobs), `slug`
     (`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`, unique over **all** jobs,
     immutable: a trigger refuses a changed slug and un-deleting a job),
     `description` (≤ 1000), `connection_id` FK, `db_mode`
     `all|selected` + `databases[]`/`exclude[]` (CHECK: `all` has no
     databases, `selected` has 1-256 and no excludes), `include_globals`,
     `retention_keep_last` (1-10000) / `retention_keep_days` (1-36500),
     at least one (CHECK), `timeout_seconds` (NULL = `workers.backup_timeout`;
     60 s-7 d in the column), `verification` `standard|deep` (task 3.10),
     `expected_window_seconds` (NULL = derived, task 5.4), `created_by`,
     `updated_by`. Durations are integer seconds, not `interval` (exact,
     no month/day ambiguity, plain `int32` in sqlc). `job_destinations`
     (job, destination, position 0-15; unique position) and `job_owners`
     (job, user, created_by/at). FKs from `job_encryption`,
     `job_passphrases`, `job_recipients` to `jobs(id)`. `bekup_app` keeps
     no DELETE on jobs; it may delete/insert `job_destinations` and
     `job_owners` rows (no UPDATE). No notification stub: shared channels
     attach to jobs in task 5.x with their own table.
  2. **Service `internal/jobs`** (the planned package; the 3.6 worker
     joins it). Every mutation re-checks the caller with the policy and
     requires actor = caller. Create validates everything (name,
     description, slug or a derived one: ASCII letters/digits lower-cased,
     other runs a hyphen, `-2`... on collision; selection with
     `engine.ValidateDBName`, sorted, no duplicates; schedule normalized
     and checked against `scheduler.min_interval`; retention; timeout ≤
     `workers.backup_timeout`; window 1 min-366 d; 1-16 distinct
     destinations), then in one transaction share-locks the connection
     (exists, not deleted, not temporary; `include_globals` only for
     PostgreSQL, default on there) and the destinations (exist, not
     deleted), inserts the job and its destinations, calls
     `jobkeys.InitTx` (encryption on: generated passphrase by default) and
     audits `job.created`. Defaults: mode `all`, enabled, `standard`.
  3. **Scheduling:** `next_run_at` = `scheduler.Next(now)` on create, on a
     changed schedule (re-planned from now; `min_interval` checked only
     when the schedule changed, D-143) and on enable; NULL for manual
     schedules, disabled and deleted jobs. Enabling does not catch up the
     disabled period. An update that leaves the canonical schedule
     unchanged keeps `next_run_at`.
  4. **Delete** is soft: `deleted_at`, `enabled = false`,
     `next_run_at = NULL`; keys, owners and destination links stay. A run
     already queued or running is **allowed to finish**; the scheduler
     ignores deleted jobs and the backup worker (3.6) must skip a job that
     is deleted (or disabled) when a queued run starts. The name becomes
     reusable, the slug never does.
  5. **Delete-in-use:** `connections`/`destinations` `checkUnused` refuse
     (CONFLICT, listing up to five job names to the admin) while a
     non-deleted job references the row. Job saves share-lock the rows and
     deletes hold `FOR UPDATE`, so neither can slip past the other.
  6. **Ownership** (`jobs.Ownership`, the real `authz.Ownership`):
     `IsJobOwner` = listed in `job_owners` and the user not deleted (also
     for a soft-deleted job, so owners keep access to its artifacts for
     downloads/restores); `BacksUpDatabase` (non-deleted job; mode `all`
     unless excluded, or listed) and `UsesConnection` (non-deleted job).
     Any non-deleted account may be listed as an owner (D-067 allows
     maintainers; only role user gains rights); a maintainer cannot add
     themselves; assignment and removal need step-up
     (`job.owners_assign`), are audited (`job.owner_added|removed`) and
     raise an alert hook (`AlertFunc`, logged until 5.5); at most 50
     owners per job. New operation `job.owners_view` (staff) for
     `Job.owners`, which is null for owners.
  7. **Run now** (`run.now`: staff and owners of the job): refused for a
     disabled job (CONFLICT; disabled means no runs at all) and for a
     deleted one (NOT_FOUND); inserts `backup_run {job_id, trigger:
     manual, triggered_by}` with `queue.InsertTx` in the transaction that
     records `job.run_requested`; a River duplicate (active run) answers
     CONFLICT "a run of this job is already active" and records nothing.
  8. **GraphQL** (`schema/jobs.graphqls`): `jobs(filter, first, after)`
     (keyset cursor on lower(name), id; JobScope-filtered in SQL), `job(id)`
     (ownership checked before the lookup, so non-owners get FORBIDDEN for
     existing and missing ids alike), `previewSchedule(input, count)`,
     `createJob` (returns a generated passphrase or keypair once),
     `updateJob`, `deleteJob`, `enableJob`, `disableJob`,
     `assignJobOwners(jobId, users)`, `removeJobOwner`, `runJobNow`, and
     the D-142 key fields (`setJobPassphrase`, `generateJobPassphrase`,
     `setJobRecipient`, `generateAgeKeypair`, `enableJobEncryption`,
     `disableJobEncryption`, `exportJobPassphrases`), which first check
     that the job exists and is not deleted. Schedules are typed
     (`ScheduleInput`/`Schedule`), not raw JSON. Owners see the
     connection's and destinations' name/type/engine only.
- **Alternatives considered:** `interval` columns (month/day semantics,
  awkward in Go); a mutable slug (would orphan or merge storage
  directories); hard delete (artifacts and audit references); refusing to
  delete a job with a queued run (blocks cleanup of a stuck job; the
  worker re-check is enough); owners losing access when a job is deleted
  (would strand restores of its backups); only role user as owners
  (rejected by D-067 for view/run use cases, harmless since others gain
  nothing); manual runs of disabled jobs (surprising; disable is the
  emergency stop); a `JSON` schedule argument (weaker typing for the UI).
- **Consequences:** Owner access, restore-target checks and list filtering
  now rest on real queries (integration-tested). Connections and
  destinations used by jobs cannot be deleted until their jobs are. Task
  3.6 must re-check `deleted_at`/`enabled` when a run starts and enforce
  min(job timeout, queue cap). Security impact: job definitions cannot be
  changed by owners, ownership grants need step-up and are audited and
  alerted, temporary connections cannot be backed up by jobs, storage
  paths cannot be redirected via the slug (SECURITY.md SV-4, SC-4, SC-6).
- **Related:** tasks 3.2, 3.3, 3.4, 3.6, 3.8, 3.10, 5.3, 5.4, 6.5, 6.8;
  D-008, D-011, D-012, D-013, D-067, D-106, D-131, D-135, D-142, D-143,
  D-144, D-145; `internal/jobs`, `internal/db/migrations/00015_jobs.sql`,
  `internal/graph/schema/jobs.graphqls`, `docs/guides/jobs-and-schedules.md`.

## D-147: Backup run worker: run tables, streamed verification, concurrency slots, cancellation and run logs (task 3.6)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.6)
- **Context:** Task 3.6 executes the backups that 3.1-3.5 prepared. The plan
  names the tables (runs, run_items, artifacts with path/size/sha256/
  passphrase_version/signing_key_id/lock_until/state, run_logs), the
  statuses, log caps, global and per-connection limits and `cancelRun`,
  but not when a run row exists, how partial/failed are decided, how a
  dump is verified without downloading it again (D-134 VerifyDump needs
  the exact size up front), how the limits work across processes without
  session advisory locks (D-044), what is retried, or how 3.7 (events),
  3.8 (retention), 3.10 (stats) and 4.1 (restore) attach.
- **Decision:**
  1. **Tables** (migration 00016, `internal/runstate` is the only writer):
     `runs` (job, trigger schedule|catch_up|manual, triggered_by,
     scheduled_for, status queued|running|success|partial|failed|
     canceled|skipped, river_job_id + attempt, queued/started/finished_at,
     `storage_timestamp` = the run's directory timestamp, sanitized
     `error_code`/`error_message`, `warnings` jsonb, bytes raw/stored,
     cancel_requested_at/by, log line counters); `run_items` (database or
     globals, sizes, raw/stored SHA-256, `verified`, error, warnings);
     `artifacts` (one row per finalized object incl. `manifest.json`:
     run, item, job, destination, kind, key, size, SHA-256, S3 version id,
     encryption mode + passphrase/recipient version, `signing_key_id` of
     the manifest next to it, `verified`, `lock_until`, state
     present|deleted|missing, deleted_at); `run_logs`; `run_slots`.
     `bekup_app` cannot DELETE runs, items, artifacts or logs, cannot
     UPDATE logs and may UPDATE only `state`, `state_changed_at`,
     `deleted_at` and `signing_key_id` of artifacts (column grants);
     triggers keep a finished run finished, `deleted` final and the
     signing key set-once. Retention (3.8) marks artifacts deleted, it
     never deletes rows.
  2. **Queued runs exist from enqueue on:** `runstate.EnqueueTx` inserts the
     `backup_run` River job and its `queued` run row in one transaction
     (scheduler and `runJobNow`, which now returns `runId`); a River
     duplicate creates no row. The worker's `Store.Begin` takes the queued
     row of its River job; a retry gets a new row (attempt n), and a row
     of an earlier attempt still `running` is finished as failed
     `interrupted` (crash, rescued job).
  3. **Run events:** every status write publishes
     `<kind>:<run id>:<job id>` (kind `run` or `log`) on the new channel
     `bekup_run_events` (`pubsub.RunEvents`, fixed LISTEN query) in the
     same transaction; serve's listener already subscribes. Task 3.7 only
     adds the GraphQL side.
  4. **Worker flow** (`internal/jobs.BackupWorker`): a missing, deleted or
     disabled job → `skipped`; a cancel requested before the start →
     `canceled`; no free slot → `river.JobSnooze(15 s + jitter)` (the run
     stays queued, no River attempt is used). Then: start (directory
     timestamp = start, UTC seconds), job timeout = min(job timeout,
     `workers.backup_timeout`) as a context deadline, encryption from
     `jobkeys.Encryptor`, open every job destination (soft-deleted ones
     included), runner Probe (server version, database list; a TLS pin or
     SSH host-key mismatch is audited with the system actor and alerted
     via the connection service), resolve the selection (`all` = listed
     minus excludes, sorted; `selected` = configured order, a missing one
     is a failed item with a warning), dump each database sequentially,
     then the PostgreSQL globals. Target-level failures (unreachable, auth,
     TLS, pin, SSH, runner down, not allowlisted) stop the run; database-
     level ones fail only their item.
  5. **Streamed verification (amends D-134 item 5):** the raw runner stream
     is teed into a concurrent `VerifyDump` while the pipeline stores it, so
     nothing is read back. `VerifyDumpHeader.total_bytes = 0` announces a
     streamed dump; a new `VerifyDumpTrailer{total_bytes}` must be the last
     message and match the received bytes (otherwise the stream is
     incomplete; sized streams refuse a trailer; Restore still needs a
     size). The offline verify container is named
     `bekup-run-<run id>-verify` (`RunSpec.Step`), so it never collides
     with the run's Dump container. PostgreSQL archives are verified this
     way (pg_restore to /dev/null); MySQL/MariaDB dumps and the globals use
     the runner's trailer check that `DumpResult.verified` already
     carries (same stream, no second transfer). The pipeline only sees EOF
     after a complete `DumpResult` (exit 0, byte count equal), so an
     incomplete dump is never finalized.
  6. **Outcomes:** a destination whose Put fails, lags or mis-stores is
     dropped for the rest of the run (it can no longer hold every entry).
     A verify failure fails the item; its stored objects stay as artifacts
     with `verified = false`, without signing key and outside the manifest.
     The manifest (instance key, purpose `manifest`; created and audited on
     first use like the audit head key; stats from the hook below; the
     run's and items' warnings) covers the complete items and goes to every
     destination that holds all of them; it is also written after a
     timeout or cancel for the items completed before, with a bounded
     finalization context. Status: `canceled`; `success` (every item stored
     and verified on every job destination, manifest on all of them, no
     timeout); `partial` (at least one complete, verified artifact);
     `failed` (none). The terminal state is written with a context that
     survives the run's and River's cancellation.
  7. **River results:** canceled → `river.JobCancel` (never retried);
     interrupted by shutdown, or runner unavailable/busy with nothing
     stored → an error with the failure code only (one retry, D-043);
     timeout and every other recorded outcome → nil (the runs table is
     the history; a timeout would only time out again).
  8. **Concurrency:** `run_slots` is a DB semaphore shared by every serve
     and worker process: acquiring runs under a transaction-scoped advisory
     lock (`pg_advisory_xact_lock`, safe on pooled connections), removes
     slots without a heartbeat for 2 min, and inserts only when the global
     (`workers.max_concurrent_runs`) and per-connection
     (`workers.max_runs_per_connection`) counts leave room; holders refresh
     every 30 s, a lost slot stops the run (`slot_lost`). Restores (4.x)
     reuse it with kind `restore`. Each process applies its own configured
     limits.
  9. **Cancellation:** `cancelRun(id)` (run.cancel: admin + maintainer;
     owners cannot cancel, D-106) marks the run, cancels its River job
     (`JobCancelTx`) and records `run.cancel_requested` in one
     transaction; a run nobody works on is finished `canceled` at once. A
     running worker stops on River's cancel signal or on its own 2 s poll
     of `cancel_requested_at`: runner calls are canceled (the runner kills
     the container), the pipeline cancels every Put (no partial object,
     S3 multipart aborted), the run ends `canceled`.
  10. **Run logs:** runner log lines and worker events go through
      `runstate.LogWriter`: control characters and invalid UTF-8
      sanitized, known secrets scrubbed (registry + `RedactCredentials`)
      **before** truncation to `workers.run_log_max_line_bytes` (new, 4096,
      128-16384), at most `workers.run_log_max_lines` (new, 10000,
      100-1000000) lines per run, batched every 500 ms or 100 lines through
      a bounded queue (a full queue drops and counts instead of blocking
      the dump), a closing notice with the dropped count; failed inserts
      never fail a run.
  11. **Secrets and errors:** the connection's credentials (each runner
      request) and the job passphrase are registered for redaction for the
      run's duration and zeroed after each call; stored and logged run
      errors are fixed texts per code (runner reasons, gRPC classes,
      storage codes), never server output, paths or DSNs. An SFTP host-key
      mismatch seen by a run is audited like a test (system actor).
  12. **Completeness hook:** `jobs.StatsCollector` opens a session before
      each dump (`PrepareDump` may adjust the request: 3.10's
      `--snapshot`) and ends it after; its stats go into the manifest's
      `stats` (`databases` by encoded name, `globals`) and its warnings
      into the item and manifest. `NoStats` until task 3.10.
  13. **Process model:** `bekup worker` now builds the destination, runner
      client, connection and job key services the worker needs (same
      startup checks as before).
- **Alternatives considered:** the worker creating the run row (queued runs
  would be invisible and not cancelable); verifying by downloading the
  stored artifact again (doubles transfer, slower) or spooling to scratch
  to learn the size first (disk per dump); a VerifyDump per MySQL dump
  (the trailer check already runs on the same stream); waiting for a slot
  inside the worker (blocks River's worker slots for other connections);
  River queue concurrency alone (per process, no per-connection limit);
  session advisory locks (break on pooled connections, D-044); retrying
  every failure (a timeout or a permission error repeats); deleting
  artifact rows on retention (restore and audit lose the history);
  writing no manifest for a canceled/timed-out run (completed artifacts
  would be unverifiable).
- **Consequences:** Runs, items, artifacts and logs grow without a DELETE
  path for the app role (log volume is capped per run; a cleanup policy
  can come later as `bekup_owner`/maintenance). A snoozed run can wait
  indefinitely while its connection is busy. Each process uses its own
  limits against the shared slot table. The runner proto gains a
  wire-compatible trailer. Security impact: dumps are verified before
  they count, unverified objects are never in a manifest, incomplete
  dumps and canceled runs never finalize objects (RN/SV rows in
  SECURITY.md), run logs and errors cannot carry registered secrets or
  credentials, history rows cannot be deleted or rewritten by `serve`, a
  maintainer can cancel but owners cannot.
- **Related:** tasks 3.6, 3.7, 3.8, 3.10, 4.1, 4.x, 5.6; D-043, D-044,
  D-087, D-106, D-134, D-142, D-144, D-145, D-146;
  `internal/runstate`, `internal/jobs/backup_*.go`, `internal/jobs/cancel.go`,
  `internal/db/migrations/00016_runs.sql`, `proto/runner/v1/runner.proto`,
  `internal/runner/validate/stream.go`, `internal/graph/schema/runs.graphqls`,
  `docs/architecture.md`, `docs/guides/jobs-and-schedules.md`,
  `docs/operations.md`, `SECURITY.md`.

## D-148: Retention worker: per-destination policy, completeness, safety checks, Object Lock / no-delete interplay and audit shape (task 3.8)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.8)
- **Context:** The plan fixes the policy (keep last N and/or younger than X
  days, per destination, never the newest successful backup, respect
  Object Lock, no-delete mode leaves deletion to lifecycle rules,
  unexpected deletions alert) but not: what "successful" means on one
  destination when a run was partial, how the sweep is triggered and
  serialized, what happens to soft-deleted jobs, how a vanished object is
  told apart from an ordinary delete (`Delete` is idempotent, D-121), how
  a buggy plan is contained, and how much goes into the audit log.
- **Decision:**
  1. **Trigger:** one River kind `retention_sweep` (retention queue,
     `retention_timeout`, 3 attempts) with args `job_id` (optional, the
     only unique field), `reason` `post_run|daily` and `dry_run`.
     **Post-run:** a `runstate.Store` finish hook enqueues the job's sweep
     inside the transaction that records a `success`/`partial` terminal
     status, under a savepoint, so a failed enqueue is logged and never
     fails the run (the backup worker itself is unchanged). **Daily:** a
     River periodic job (24 h, leader-elected, also at leader start;
     chosen over the scheduler loop because periodic housekeeping already
     uses it, D-145) enqueues the fan-out (no job id), which enqueues one
     sweep per job with present artifacts and removes leftover temporary
     upload files older than `retention.partial_cleanup_age` (default 24 h)
     on disk and SFTP destinations. Uniqueness per job means two sweeps of
     one job never run at the same time; a post-run enqueue while the
     job's sweep is still active is dropped (the daily sweep catches up).
  2. **Completeness per destination:** a finished run (never queued or
     running) is complete on destination D when it has at least one item,
     every item has a `present`, `verified` artifact on D and its manifest
     is present on D. A partial run caused by a dropped destination is
     therefore complete on the other destinations; a run with a failed
     item is complete nowhere.
  3. **Keep set per destination:** the newest complete run (always), the
     newest N complete runs, complete runs younger than X days (union),
     every run with an artifact whose recorded `lock_until` is in the
     future, and incomplete runs newer than the newest complete one.
     Everything else is eligible, including incomplete runs older than the
     newest complete one. A destination without a complete run deletes
     nothing; a job without an active policy (impossible by CHECK) deletes
     nothing. Runs are ordered by their storage timestamp (ties by id).
     The policy is a pure function (`retention.Plan`).
  4. **Soft-deleted jobs keep their retention:** their backups stay
     restorable and age out by the job's policy; the newest complete run of
     a deleted job is therefore kept indefinitely (a purge is a later,
     explicit admin action).
  5. **Deletion:** per eligible run, oldest first: the dumps, then the
     manifest only once every dump of that run is gone from D. Every
     delete is preceded by a `Stat`: `ErrNotFound` marks the artifact
     `missing`, records `retention.artifact_missing` in the same
     transaction and raises the `retention_artifact_missing` alert hook
     (task 5.5); a successful delete marks it `deleted` (with
     `deleted_at`, written even if the sweep is being canceled);
     `ErrDeleteNotPermitted` (Object Lock retention or legal hold, also
     bucket defaults unknown to bekup) keeps the artifact `present` and
     records the lock in the summary (`locked`, `locked_until_max`; the
     app role cannot update `lock_until`, D-147). Three failures in a row
     abort the destination. S3 deletes stay version-exact (D-129).
  6. **No-delete destinations** (`Capabilities().NoDeleteMode`) are
     skipped entirely, without backend calls: artifacts stay `present`
     and the skip is logged once per sweep (lifecycle rules delete the
     objects; reconciling such rows is a possible follow-up).
  7. **Safety:** (a) the plan is checked against its invariants (newest
     complete kept, nothing kept or of another destination in the delete
     list); (b) before the first delete on D, every artifact of the
     newest complete run must exist with its recorded size: a missing one
     is marked missing (audited, alerted) and, like a size change or an
     invariant violation, aborts D (`retention.aborted` + the
     `retention_sweep_aborted` alert); a transient Stat error only skips
     D (logged); (c) at most `retention.max_deletions_per_sweep` (default
     500, 1-100000) deletes per job sweep, the rest waits for the next
     sweep; (d) `retention.dry_run` (instance-wide, warned at startup) or
     the args' `dry_run` only computes and logs the plans (no backend
     call, no audit, partial cleanup skipped).
  8. **Audit shape:** per-object outcomes live in the artifacts table
     (`state`, `state_changed_at`, `deleted_at`); the audit log gets, per
     destination with eligible objects, `retention.deletion_planned`
     before the first delete (a failure aborts: fail closed) and
     `retention.swept` after (counts, up to 50 deleted run ids, capped,
     aborted), plus `retention.artifact_missing` per vanished object and
     `retention.aborted` per safety abort. System actor, target the job
     (or the artifact). Sweeps with nothing eligible record nothing.
- **Alternatives considered:** an audit entry per deleted object (floods
  the hash-chained log on large jobs; the artifacts table already is the
  per-object record); a retention log table (a migration for data the
  artifacts table holds); enqueueing from the backup worker (touches the
  run worker; not atomic with the terminal status); per-job sweeps from
  the periodic job directly (one leader job would run every job serially
  within one timeout); trusting `Delete`'s idempotence (an object deleted
  by an attacker would be silently recorded as an ordinary deletion);
  counting a partial run as complete everywhere (keep-last 1 could delete
  the only good copy of a database that failed in the newest run);
  stopping retention for deleted jobs (keeps everything forever) or
  purging them at once (destroys restorable backups).
- **Consequences:** Retention can never empty a destination that holds a
  complete backup and never deletes on a destination that holds none. A
  backup deleted outside bekup is detected when retention reaches it or
  when it belongs to the newest complete run (full periodic verification
  of kept objects is task 5.x). Object Lock refusals are retried each
  sweep until the lock expires. A restore or download of an eligible
  artifact can race a sweep (tasks 3.9 / 4.x should hold such artifacts;
  until then the window is the gap between plan and delete). Security
  impact: bounded blast radius (cap, invariants, dry run), fail-closed
  audit before deleting, unexpected deletions are audited and alerted,
  no-delete and Object Lock are honored by retention and again by the
  backends (SECURITY.md SV/DS rows).
- **Related:** tasks 3.6, 3.8, 3.9, 4.x, 5.5; D-007, D-121, D-128, D-129,
  D-145, D-146, D-147; `internal/retention`, `internal/runstate`
  (`artifacts.go`, finish hook), `internal/queue/kinds.go`,
  `internal/db/queries/retention.sql`, `internal/config` (`[retention]`),
  `docs/guides/retention.md`, `docs/security/immutable-backups.md`.

## D-149: Run history queries and live run events: scoped reads, per-operation batch loader, delivery-time re-load and log streams (task 3.7)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.7)
- **Context:** Task 3.7 adds the GraphQL side of the run tables (D-147):
  run and artifact listings, run details, run logs and live updates over
  the SSE transport (D-124). The plan fixes the names (`runUpdated`,
  `runLogAppended`), cursor pagination and IDs-only events, but not how
  backup owners are scoped on single objects, how pages avoid N+1 queries,
  what an event carries, how lost notifications are handled, how log
  streams are bounded and end, or who may see who started a run.
- **Decision:**
  1. **Reads** go through `runstate.Reader` (read only, sqlc queries in
     `internal/db/queries/run_reads.sql`); `internal/runstate` stays the
     only writer. Listings (`runs`, `artifacts`) take `authz.JobScope`
     (`run.list`, `artifact.list`): owners get only runs/artifacts of jobs
     they are listed on (same rule as `IsJobOwner`, deleted users own
     nothing). Pages are newest first with opaque cursors
     (`(queued_at, id)` / `(created_at, id)`), `first` 1..200 (default 50);
     `runLogs` pages by line id (`afterId`, `first` 1..1000, default 200).
  2. **Single objects** (`run`, `runLogs`, subscription filters) load the
     row first and authorize with the job stored on it (`run.view`,
     `run.logs_view`, never a job id from input). An unknown id answers
     like a run of another job for backup owners (FORBIDDEN), and null /
     NOT_FOUND for staff, so owners cannot probe ids.
  3. **Who started a run** (`Run.triggeredBy`: user id, display name,
     e-mail) needs the new operation `run.actor_view` (admin, maintainer);
     backup owners get null. Error and warning texts are re-sanitized
     (`runstate.SanitizeLine`) on output in addition to being fixed texts
     when written.
  4. **No N+1:** a per-operation `runLoader` (installed by
     `graph.New` via `AroundOperations` for queries and mutations only)
     batch-loads the items and artifacts of every run of a page, and the
     recent runs of every job of a `jobs` page (`Job.lastRun`,
     `Job.recentRuns(first ≤ 20)`), from ids the list resolvers hint. A
     page of 50 runs with items, artifacts and manifest costs three reader
     queries (tested). The loader caches only data its parent field was
     authorized for and never decides access; subscriptions get no loader,
     so every event reads fresh rows.
  5. **`runUpdated(jobId, runId)`** subscribes to `bekup_run_events` on the
     in-process bus (fed by the existing LISTEN bridge; no new channel).
     The payload ids only pre-filter; per event `streams.Deliver`
     re-checks the session and `streams.Authorize` loads the run by id
     and authorizes `run.view` with its stored job, and exactly that
     loaded row is sent. A backup owner who loses a job stops receiving
     its runs with the next event. After possibly lost notifications (bus
     overflow, listener reconnect) the stream re-sends up to 100 runs
     changed since the last resync (30 s clock margin), each authorized
     again. Filters the caller may not use answer FORBIDDEN up front.
  6. **`runLogAppended(runId, afterId)`** sends coalesced batches of at
     most 200 new lines (authorized per batch with `run.logs_view`; the
     run's job is immutable, ownership is looked up each time) and
     completes with a `done` batch once the run is terminal and every
     line was delivered (status read before lines: the log writer
     flushes before the run finishes). One goroutine and one bus
     subscription per stream; both end with the stream.
  7. **Stream cap:** both subscriptions count against
     `graphql.stream_max_per_session` (default 4); the UI opens one
     unfiltered or job-filtered `runUpdated` stream per page and filters
     on the client, plus at most one `runLogAppended` on a run page.
  8. Sizes are GraphQL `Float` (bytes, exact up to 2^53) rather than a new
     64-bit scalar.
- **Alternatives considered:** loading the job id from the event payload
  for authorization (a forged or stale NOTIFY could widen access);
  time-window dataloaders (non-deterministic batching); one query per
  row (N+1); pushing full run payloads in NOTIFY (8 KB limit, data
  prepared for another caller, D-050); a dedicated log channel per run
  (unbounded LISTEN set); returning NOT_FOUND to owners for unknown runs
  (an existence oracle); exposing `triggeredBy` to owners (reveals staff
  identities).
- **Consequences:** Run pages are cheap and bounded; owners can never see
  runs, artifacts, logs or events of jobs they do not own at the moment of
  delivery; staff identities stay hidden from owners. Lost notifications
  delay but do not drop updates for long. Security impact: no new trust
  boundary; the read side relies on `JobScope` and the stored job id, the
  event side on D-050/D-124 delivery-time checks. The `authz` matrix has a
  new row (`run.actor_view`).
- **Related:** tasks 3.6, 3.7, 6.2, 6.6; D-050, D-106, D-124, D-147;
  `internal/runstate/read.go`, `internal/db/queries/run_reads.sql`,
  `internal/graph/schema/runs.graphqls`,
  `internal/graph/resolvers/runs_read.go`, `runs_loader.go`,
  `runs_events.go`, `internal/graph/handler.go`.

## D-150: Download tickets: single-use session-bound tickets, use-time re-check, manifest pre-verification, held-back tail and sealed identity handoff (task 3.9)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.9)
- **Context:** The plan fixes the policy (admins + owners only, never
  maintainers, D-012/D-067; step-up; audited; raw or plain; single-use 60s
  session-bound tickets; `Cache-Control: no-store`; a sanitized file name;
  identities never stored in plaintext) and the authz operation
  `artifact.download` (D-106), but not the ticket storage, what happens to
  a ticket presented by the wrong session, whether authorization is
  re-checked when the ticket is used (60s later, with an expired step-up
  window), which artifacts qualify, how integrity is enforced on a stream
  whose headers are already sent, or the concurrency and cleanup rules.
- **Decision:**
  1. **Tickets** (migration 00017, `download_tickets`): 256 random bits
     (`crypto/rand`), unpadded base64url (43 characters) in the URL
     `/api/download/<ticket>`; only SHA-256 of the raw bytes is stored
     (unique). The row binds the session, user, artifact and mode;
     `expires_at` = creation + 60s (a CHECK caps it at 5 minutes);
     `used_at` is set once. `bekup_app` may only update `used_at` and
     clear `sealed_identity` (column grants + a trigger); rows are deleted
     by a serve housekeeping loop every 30s once expired for a minute. A
     user has at most 5 unused, unexpired tickets (`RATE_LIMITED`).
  2. **Creation** (`createDownloadTicket(artifactId, mode, ageIdentity)`,
     `@stepUp`, a D-117 single-root field because it may carry a
     credential): `artifact.download` with the artifact's job loaded from
     the DB (a missing id is `NOT_FOUND` for admins and `FORBIDDEN` for
     everyone else). The artifact must be a database/globals object,
     `present`, `verified` **and covered by a signed manifest on its
     destination** (`signing_key_id` set). Unverified or unsigned artifacts
     are always refused, also for admins (no override flag: a dump that
     failed its native verification or lacks a signed manifest is exactly
     what must not leave the system unnoticed; operators can still read
     the destination directly). Plain mode: passphrase artifacts use the
     artifact's passphrase version (checked to exist); recipient artifacts
     require `ageIdentity`, which must parse as X25519 identities
     (≤ 4 KiB) and match the artifact's recipient version, and is sealed
     with the keyring under AAD `download_tickets:<id>:identity`; raw mode
     and non-recipient artifacts refuse an identity. Audited as
     `download.ticket_created` (artifact, job, run, destination, mode,
     encryption, ticket id; never the value or the identity) in the
     insert's transaction. The response carries the URL, expiry, file
     name, format and a restore command template with placeholders only.
  3. **Redemption** (`GET /api/download/{ticket}`, behind the session
     middleware; other methods never reach it): a malformed value or a
     `Sec-Fetch-Site` other than `same-origin`/`none` is 404 without a
     database lookup. In one transaction the ticket row is locked by hash:
     unknown → 404; used → 410; expired → 410 and burned; another session,
     another user or no session → 404 and **burned** (a leaked URL is
     treated as compromised); then `artifact.download_redeem` is
     re-checked (new operation: the same roles and owner grant as
     `artifact.download` but **no freshness**, because the step-up was
     proven at creation and the ticket is single use, session bound and
     lives 60s) → on denial 404 and burned. Otherwise `used_at` is set and
     the sealed identity cleared in the same transaction, before any byte
     is streamed. Every rejection of a known ticket is audited
     (`download.ticket_rejected` with the reason).
  4. **Integrity.** Before the response starts, the run's `manifest.json`
     on the same destination is read (≤ 16 MiB), compared with its
     recorded size and SHA-256 and verified with `pipeline.Verify`
     (instance keys; binding = job, run, slug, run timestamp and every
     artifact recorded as signed with that key there). While streaming,
     the stored bytes are hashed and counted (and in plain mode the dump
     too, against the item's raw SHA-256 and size; age authenticates each
     64 KiB chunk and the zstd frames carry checksums). The last 64 KiB of
     the response are **held back** until every digest matched; on a
     mismatch the handler aborts the connection (`http.ErrAbortHandler`),
     so the client sees a truncated download (raw: short of its
     `Content-Length`; plain: an unterminated chunked body) and never a
     complete file. Browsers have no usable trailers, and a pre-pass over
     the artifact would double the transfer; the hold-back gives the same
     guarantee at one transfer (bounded memory: 128 KiB per download).
  5. **Streaming.** Raw = the stored object with `Content-Length`; plain =
     `pipeline.OpenArtifact` (scrypt work factor ≤ the default 18, X25519
     only, zstd window ≤ 64 MiB, one decoder goroutine). Headers:
     `Content-Type: application/octet-stream`, `Content-Disposition:
     attachment; filename="…"; filename*=UTF-8''…` with a strict sanitizer
     (ASCII letters, digits, `.`, `-`, `_`; no leading dot/dash, no `..`,
     ≤ 200 bytes, no Windows device names), `Cache-Control: no-store`,
     `X-Content-Type-Options: nosniff`, and the HTTP layer's CSP and
     security headers. Names: `<job-slug>_<db>_<YYYYMMDDTHHMMSSZ>.<ext>`
     (`.dump.zst.age`/`.sql.zst.age` raw, `.dump`/`.sql` plain). The
     server write deadline is lifted for the stream; each write has its
     own 2-minute deadline. At most 4 concurrent downloads per process and
     2 per user (a refused request answers 429 without consuming the
     ticket). `download.started` is recorded before the headers (failing
     it fails the download) and `download.completed` or `download.failed`
     (fixed reason codes) afterwards.
  6. **Logging.** The access log already cuts `/api/download/` paths to
     the prefix (D-102); the ticket value is never logged, audited or
     stored.
- **Alternatives considered:** storing the ticket in plaintext or only
  encrypted (a DB reader could redeem it); refusing a wrong-session use
  without burning (lets a leaked URL be retried until the owner uses it);
  re-requiring a fresh step-up at use time (fails legitimate downloads
  whose ticket was created near the end of the window, without adding
  security over the 60s single-use binding); no use-time re-check (a
  revoked owner could still use a ticket); an admin override for
  unverified artifacts (rejected, see 2); verifying with a full pre-pass
  (double transfer and latency) or not at all before streaming (a swapped
  DB digest would go unnoticed); HTTP trailers (ignored by browsers);
  passing the identity in the GET (it would sit in URLs and logs);
  a River job for the cleanup (a serve loop is enough for a table only
  serve uses).
- **Consequences:** A download needs a step-up within the last
  `security.stepup_max_age` and is usable for 60s from the same browser
  session; a leaked URL is useless to anyone else and burns itself. A
  passphrase download costs up to 256 MiB for scrypt, bounded by the
  concurrency limits. Tampered artifacts or manifests never produce a
  complete file; the failure is audited. A session rotation (e.g.
  `ElevateAAL`) between creation and use invalidates the ticket (create
  a new one). An identity lives only sealed in the row until redemption
  or, if unused, at most about two minutes until cleanup; the GraphQL
  string itself cannot be zeroed (as for passwords). Security impact:
  SECURITY.md rows for tickets, identities and download integrity; task
  6.6 builds the dialog on this API (`createDownloadTicket` then a
  same-origin navigation to `url`).
- **Related:** tasks 3.6, 3.9, 4.1, 6.6; D-012, D-050, D-067, D-082,
  D-085, D-086, D-088, D-102, D-106, D-112, D-117, D-142, D-144, D-147;
  `internal/downloads`, `internal/db/migrations/00017_download_tickets.sql`,
  `internal/graph/schema/download.graphqls`, `internal/httpserver`
  (`Options.Download`), `docs/guides/downloads.md`.

## D-151: Completeness evidence: stats inside the Dump call, same-snapshot PostgreSQL counts, warnings model and backup_stats (task 3.10)

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.10)
- **Context:** D-141 (user decision) fixes what is captured (object counts,
  per-table row counts, completeness warnings; PostgreSQL in the dump's
  snapshot, MySQL/MariaDB approximate; `standard | deep`) and that the
  stats are signed into the manifest. It leaves open how the snapshot is
  handed to `pg_dump`, what crosses the runner API, the limits, the
  warning codes, where the stats are stored and how a run shows them.
- **Decision:**
  1. **No separate RPC.** The plan named a `CaptureStats` RPC; the stats
     are instead a phase of `Dump` (and `DumpGlobals`): `DumpRequest.stats`
     / `DumpGlobalsRequest.stats` (`StatsOptions{enabled, level
     standard|deep, per_table_timeout_ms (default 5 min, ≤ 1 h), budget_ms
     (default 30 min, ≤ 6 h)}`) and one `DumpStats` message in the
     response stream **before the first chunk** (at most once; anything
     else is a protocol error the worker fails the item on). A separate
     RPC would have to keep a database transaction open across calls.
  2. **PostgreSQL same snapshot.** The runner opens a Go-driver session to
     the dumped database through the same target allowlist / SSH tunnel,
     pin pre-flight and TLS rules as every check (D-134), runs `BEGIN
     ISOLATION LEVEL REPEATABLE READ READ ONLY`, lifts
     `idle_in_transaction_session_timeout` and `transaction_timeout` for
     the transaction (`set_config(..., true)` where the server has them),
     `SELECT pg_export_snapshot()`, reads the catalog and counts inside it,
     sends the stats, then starts `pg_dump --snapshot=<id>` and **keeps the
     transaction open until the dump container finished** (then ROLLBACK).
     The id is validated (`^[0-9A-F]{1,16}(-[0-9A-F]{1,16}){1,3}$`) before
     it reaches argv (`engine.DumpSnapshot`; MySQL/MariaDB refuse a
     snapshot). `pg_dump` connects as the same user to the same database,
     as snapshot import requires. If the capture fails the dump runs
     without `--snapshot`, the result carries `stats_unavailable` and the
     item gets that warning.
  3. **Counting.** Object counts exclude system schemas and extension
     members (PG: schemas, tables r+p, views, matviews, sequences,
     functions, procedures, non-internal triggers, extensions; MySQL/
     MariaDB from information_schema: tables, views, sequences, functions,
     procedures, triggers, events; globals: roles without `pg_*`,
     tablespaces without `pg_default`/`pg_global`). Row stats cover tables
     holding data (PG relkind r, `count(*) FROM ONLY`). `deep`: exact
     `count(*)`, identifiers quoted with `engine.QuotePGObject` /
     `QuoteMySQLObject` (catalog names: valid UTF-8, no NUL, ≤ 63 bytes /
     64 characters); each count is limited server-side to min(per-table
     timeout, remaining budget) (PG: `SAVEPOINT` + `SET LOCAL
     statement_timeout`, rolled back on failure so the snapshot survives;
     MySQL: `MAX_EXECUTION_TIME` hint; MariaDB: `SET STATEMENT
     max_statement_time`); up to two helper sessions import the snapshot
     (`SET TRANSACTION SNAPSHOT`) or start their own consistent snapshot
     (MySQL/MariaDB) from 16 tables on. `standard`: `reltuples` (−1 =
     unknown) / `TABLE_ROWS`, marked `estimate`. MySQL/MariaDB counts run
     in `START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY` right
     before the dump, the session ends before the dump starts, and every
     table is `approximate`.
  4. **Bounds.** At most 10 000 tables (first by schema/name, byte order)
     and ~768 KiB of table entries per message (`tables_total` + 
     `tables_truncated` + a `stats_partial` warning otherwise), at most 32
     warnings with ≤ 50 object names each. Only names and counts cross the
     API, never data values. The worker re-validates everything (codes,
     name sizes, kinds, duplicates) and discards malformed stats
     (`stats_invalid`).
  5. **Warnings** (stable codes, per item and in the manifest):
     `insufficient_privileges` (PG: tables/sequences without SELECT and
     schemas without USAGE per `has_*_privilege`; functions are not
     flagged because `pg_dump` reads them from the catalog without
     EXECUTE; MySQL/MariaDB: visible tables that fail a zero-row `SELECT`
     probe (standard) or the count (deep); objects an account cannot see
     at all are invisible to information_schema and to the dump tool alike
     and cannot be detected), `non_transactional_tables` (engines other
     than InnoDB/RocksDB/TokuDB/SEQUENCE; replaces the runner's
     `non_innodb_tables` summary when stats exist), `row_count_timeout`,
     `stats_budget_exceeded`, `row_count_failed`, `stats_partial`,
     `roles_without_passwords` (the globals fallback joins the stats
     warnings), `stats_unavailable`, and the run-level
     `databases_excluded` (mode `all`: databases on the server left out by
     the exclude list).
  6. **Storage.** The worker's `jobs.RunnerStats` (the `StatsCollector` of
     D-147) puts the job's level into the request, turns the message into
     a canonical object (integers and strings only, flags as `1`):
     `{"level","consistency":"snapshot"|"approximate","objects":{...},
     "tables":{"<enc schema>.<enc table>":{"rows":N,"count":"exact"|
     "estimate"|"unknown","approximate":1}},"tables_total",
     "tables_truncated","warnings":[{"code","count","objects",
     "truncated"}]}` (names percent-encoded with `pipeline.EncodeName`,
     MySQL keys without schema), stores it in the new **`backup_stats`**
     table (one row per complete, verified item; `bekup_app` INSERT/SELECT
     only) and in the manifest's `stats.databases.<enc db>` / `globals`. If
     all stats together exceed 8 MiB the manifest keeps the counts and
     warnings but drops the table lists (`"tables_omitted":1`).
  7. **Run status.** Completeness warnings never change the status
     (success/partial/failed stay as in D-147); `runs.has_warnings`
     (generated: the run's warnings, which include every item warning, are
     not empty) flags such runs for the UI and alerts.
- **Alternatives considered:** a separate `CaptureStats` RPC (a transaction
  spanning RPCs, or an unrelated snapshot); `SERIALIZABLE READ ONLY
  DEFERRABLE` (may wait for a safe snapshot on busy servers; REPEATABLE
  READ gives the same snapshot to pg_dump); closing the exporting
  transaction once pg_dump reported its start (needs parsing tool output;
  holding to the end is simpler and costs one idle session);
  client-side cancellation for count timeouts (cancel requests need a new
  connection through the tunnel/guard); a `stats jsonb` column on
  `run_items` (the plan names a table; a separate insert-only table keeps
  the evidence immutable without new column grants); flagging functions
  without EXECUTE (false positives).
- **Consequences:** `deep` costs a `count(*)` per table (bounded by the
  budget) and up to three runner sessions; the PostgreSQL exporting
  transaction holds back vacuum for the dump's duration, as `pg_dump`
  itself does. Test restores (4.6) compare against `backup_stats` /
  the signed manifest: exact where `consistency` is `snapshot` and
  `count` is `exact`. Security impact: no new network path (the stats
  session uses the existing Go-driver path and rules); only catalog
  names and counts leave the target, bounded in size; the snapshot id is
  validated before it reaches argv and catalog names are quoted with the
  complete escaping rule; the evidence rows are insert-only for `serve`
  and covered by the manifest signature.
- **Related:** tasks 3.10, 4.6, 4.7, 6.6, 7.1; D-033, D-034, D-134, D-141,
  D-144, D-147; `proto/runner/v1/runner.proto`,
  `internal/runner/ops_stats.go`, `internal/runner/dbprobe/stats*.go`,
  `internal/runner/validate/stats.go`, `internal/engine` (`DumpSnapshot`,
  `QuotePGObject`, `QuoteMySQLObject`), `internal/jobs/backup_stats.go`,
  `internal/db/migrations/00018_backup_stats.sql`,
  `docs/reference/artifact-format.md`, `docs/guides/jobs-and-schedules.md`.

## D-152: Hostile target databases: pinned session settings, schema-qualified SQL and relation-kind restriction for every Go-driver session

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals) (item 6 superseded by D-153)
- **Decided by:** agent with user approval (security review fix, milestone 3)
- **Context:** The runner's Go-driver sessions to PostgreSQL targets
  (probe, database list, version, the stats session that exports the
  snapshot and the helper sessions that import it; D-134, D-151) ran
  unqualified functions and operators (`count(*)`, `||`, `=`, `<>`,
  `!~`, `IN`) without pinning `search_path`. A target database's owner
  (not a superuser) can `CREATE AGGREGATE evil.count(*)`, shadow
  operators and catalog functions, and `ALTER DATABASE ... SET
  search_path = evil, pg_catalog`; the next stats capture then runs that
  code as the backup role, which is often a superuser (reproduced:
  "running as postgres, superuser=on"; a superuser can `COPY ... TO
  PROGRAM` even in a READ ONLY transaction). Deep counts additionally
  executed row-level security policy expressions as the backup role, and
  a table swapped for a view (or a foreign table) between the catalog
  read and its count would have run the view's functions as the backup
  role (the CVE-2024-7348 pattern).
- **Decision:**
  1. **Startup pins.** Every PostgreSQL session of `internal/runner/dbprobe`
     sends `search_path=pg_catalog, pg_temp` and `row_security=off` as
     startup parameters. Startup parameters take precedence over
     `ALTER DATABASE` / `ALTER ROLE` settings; after authentication the
     session reads both back with `pg_catalog.current_setting` and is
     refused (closed, `FAILED "session settings"`) if they are not in
     effect (a server or pooler ignoring them fails closed), then sets
     both again with `pg_catalog.set_config` (belt and braces).
     `pg_temp` is last: temporary relations cannot shadow catalog
     relations, and functions/operators are never looked up in it.
  2. **Relation-kind restriction.** Each session sets
     `restrict_nonsystem_relation_kind = 'view, foreign-table'` through
     `set_config` from `pg_settings` where the server has it (12.20,
     13.16, 14.13, 15.8, 16.4, 17+), exactly as `pg_dump` does. A row
     count therefore can only read a stored relation; a view or foreign
     table swapped in is an error. On a server without the setting the
     connection reports `SafeRowCounts() == false` and refuses
     `CountRows` (`ErrCountUnsafe`): `deep` stats keep the catalog part,
     every table is `unknown` with `row_count_failed` and the runner logs
     `runner_stats_counts_disabled`. Catalog reads stay allowed (they
     touch only `pg_catalog` relations).
  3. **Schema-qualified SQL.** Every statement names functions and
     aggregates `pg_catalog.f(...)`, operators `OPERATOR(pg_catalog.op)`
     (`x IN (...)` becomes `x OPERATOR(pg_catalog.=) ANY
     ('{...}'::pg_catalog.type[])`, since IN resolves `=` by name), casts
     `::pg_catalog.type`, collations `pg_catalog."C"`; the count is
     `SELECT pg_catalog.count(*) FROM ONLY <QuotePGObject schema>.<name>`.
     A unit test (`TestPGQueriesQualified`) fails on any unqualified
     function, operator, cast, collation or `IN`.
  4. **Row-level security.** With `row_security=off` a table whose
     policies apply to the backup role makes the count fail (42501)
     instead of evaluating the policies; it is reported `unknown` with
     the existing `insufficient_privileges` warning (the role needs
     BYPASSRLS, and `pg_dump` refuses such a table for the same reason).
     Superusers and BYPASSRLS roles bypass policies, so none run.
  5. **The dump tools.** The pinned `pg_dump`/`pg_dumpall` clients (13.23+)
     set `search_path = ''` as their first statement, run with
     `row_security = off` (the runner never passes
     `--enable-row-security`) and set `restrict_nonsystem_relation_kind`
     where the server has it; nothing to change. `mysqldump` /
     `mariadb-dump` do not call stored functions and do not select view
     data.
  6. **MySQL/MariaDB** (superseded by D-153: estimates only, no statement on user tables). Built-ins (`COUNT`, `CAST`, `VERSION`) cannot be
     shadowed (a stored function named like a built-in is only reachable
     qualified), the catalog queries read `information_schema`, SELECT
     fires no triggers and only base tables are counted. Residual risk
     (documented, accepted pending the user's confirmation):
     `information_schema` is outside the consistent snapshot, so an owner
     who replaces a listed table with a `SQL SECURITY INVOKER` view
     between listing and count has it evaluated with the backup
     account's rights inside the READ ONLY transaction; `mysqldump` has
     the same window. The backup account must not hold administrative
     privileges (SUPER, SYSTEM_VARIABLES_ADMIN, FILE, ...).
- **Alternatives considered:** only `SET search_path` after connect
  (a per-database setting is applied before the first statement and an
  unqualified first statement would already resolve through it); an
  empty `search_path` (equivalent for qualified SQL, but `pg_catalog,
  pg_temp` keeps `pg_temp` from being implicitly first); a lock plus
  OID/relkind re-check before each count on unpatched servers (racy:
  renaming the schema needs no lock on the table); counting on unpatched
  servers anyway (code execution as the backup role); a new warning code
  for the disabled counts (`row_count_failed` already says "unknown",
  and the worker knows only the existing codes).
- **Consequences:** Security impact: closes code execution as the backup
  role through search_path shadowing, RLS policies and view/foreign-table
  swaps in the runner's own sessions. `deep` stats need a patched server
  (any PostgreSQL minor release since August 2024); unpatched servers
  get catalog stats only. A connection pooler that rejects
  `search_path`/`row_security` startup parameters cannot be used for the
  Go-driver checks (fails closed). Three extra round trips per session.
  Integration-tested against a hostile database on PostgreSQL 13, 16 and
  18 (shadowed `count(*)`, operators and functions, `ALTER DATABASE SET
  search_path`, an RLS policy and a view calling trap functions; a
  positive control shows the traps fire for ordinary sessions; stats and
  dumps as a superuser and as a read-only role fire none).
- **Related:** D-001, D-033, D-134, D-141, D-151; SECURITY.md RN-13,
  RN-14; `internal/runner/dbprobe/postgres.go`,
  `internal/runner/dbprobe/stats_pg.go`, `internal/runner/dbprobe/stats_mysql.go`,
  `internal/runner/ops_stats.go`,
  `internal/runner/ops_hostile_integration_inner_test.go`.

## D-153: MySQL/MariaDB statistics are estimates only: no statement on user tables or views

- **Date:** 2026-10-27
- **Status:** accepted
- **Decided by:** user
- **Context:** D-152 item 6 left a residual risk for MySQL/MariaDB: exact
  counts (`deep`) and the zero-row SELECT privilege probe (`standard`)
  named user tables, and `information_schema` is outside the consistent
  snapshot, so a database owner could swap a listed table for a
  `SQL SECURITY INVOKER` view and have it evaluated with the backup
  account's rights. MySQL/MariaDB counts were `approximate` anyway (the
  dump takes its own snapshot), and the test-restore comparison (task
  4.6) treats them as approximate.
- **Decision:** The runner never runs a statement that reads a
  MySQL/MariaDB user table or view, at either level. Row stats are the
  `information_schema.TABLES.TABLE_ROWS` estimates (`estimate`,
  `approximate`); object counts stay exact. `deep` behaves like
  `standard` for MySQL/MariaDB and the stats are recorded at level
  `standard`; the job setting stays (docs/UI note: `deep` only adds
  exactness for PostgreSQL). `non_transactional_tables` comes from
  `information_schema.TABLES.ENGINE`; `insufficient_privileges` from the
  account's grants in `information_schema` (`USER_PRIVILEGES`,
  `SCHEMA_PRIVILEGES` with pattern match, `TABLE_PRIVILEGES`; grantee from
  `CURRENT_USER()`); with an active role (`CURRENT_ROLE()` not
  `NONE`/NULL) the privilege check is skipped because role grants are not
  visible there. `dbprobe` MySQL connections report `SafeRowCounts() ==
  false` and refuse `CountRows`. This supersedes D-152 item 6 (the
  residual risk is removed); the rest of D-152 stands.
- **Alternatives considered:** keeping exact counts with a documented
  residual (D-152 item 6); `HANDLER ... OPEN` to pin base tables (its
  metadata lock is released on conflicting DDL); general-log or role
  introspection for privileges (`SHOW GRANTS` parsing).
- **Consequences:** MySQL/MariaDB stats lose exact InnoDB counts (they
  were approximate already). Privilege warnings may miss column-level or
  role-based grants (false positives only when no global/database/table
  SELECT grant is visible; skipped with active roles); the dump tool
  still fails on a real missing privilege. Security impact: the runner
  can no longer be made to evaluate database-owner code on MySQL/MariaDB
  targets; `mysqldump`'s own list-then-SELECT window remains a property
  of the native tool (least-privileged backup account recommended).
  Integration-tested: the server's general log shows no statement
  addressing a user table during `standard` and `deep` captures, with a
  positive control.
- **Related:** D-141, D-151, D-152; SECURITY.md RN-14;
  `internal/runner/dbprobe/stats_mysql.go`, `internal/runner/ops_stats.go`,
  `internal/runner/ops_stats_integration_inner_test.go`,
  `docs/guides/jobs-and-schedules.md`.

## D-154: Job encryption key changes are admin only (M3 review)

- **Date:** 2026-10-27
- **Status:** accepted
- **Decided by:** user
- **Context:** D-142 item 5 and D-106 let maintainers change a job's
  encryption keys (`job.encryption_keys_edit`: set or generate a
  passphrase or an age recipient, opt an opted-out job back in) with
  step-up, so a maintainer could replace a job's key with one they know.
  The M3 review raised it; the user decided.
- **Decision:**
  1. `job.encryption_keys_edit` is **admin only**, with a fresh step-up
     (policy rule `adminOnly` + `FreshStepUp`, matrix row `YFFFFU` / `S`).
     The GraphQL mutations `setJobPassphrase`, `generateJobPassphrase`,
     `setJobRecipient`, `generateAgeKeypair` and `enableJobEncryption` carry
     `@hasRole(roles: [ADMIN]) @stepUp`.
  2. `internal/jobkeys` re-checks it itself: after `Policy.Authorize` and
     the actor check, a key change by a principal whose role is not admin
     fails with `ErrAdminOnly` (FORBIDDEN), independent of the policy table.
  3. Job creation: every role that may create jobs (`job.create`) still
     gets the default, a **generated passphrase**, automatically. Choosing
     the key at creation (a passphrase, RECIPIENT mode or a recipient in
     `CreateJobInput.encryption`) is a key change and additionally needs
     `job.encryption_keys_edit` (admin, fresh step-up).
  4. Opt-out and passphrase export stay admin only (D-142); opting back in
     is a key change and therefore admin only too.
- **Alternatives considered:** keeping maintainers with step-up and audit
  (D-142; rejected by the user); letting maintainers choose a key at
  creation only (it is the same capability on a new job).
- **Consequences:** Maintainers manage jobs but never their keys; key
  rotation needs an admin. Security impact: a compromised or malicious
  maintainer account can no longer redirect future backups to a key it
  controls. Supersedes D-142 item 5 (key-change roles) and the
  corresponding D-106 matrix entry.
- **Related:** D-106, D-142; tasks 3.2, 3.5, 3.11; `internal/authz/operations.go`,
  `internal/authz/matrix_test.go`, `internal/jobkeys/service.go`,
  `internal/graph/schema/jobs.graphqls`, SECURITY.md (job keys row).

## D-155: M3 review fixes: artifact holds, run notices, byte-bounded warnings, exact manifest signing, scheduler savepoints, retry cancellation and coalesced run events

- **Date:** 2026-10-27
- **Status:** accepted (2026-10-28, user accepted all M3 proposals)
- **Decided by:** agent with user approval (task 3.11)
- **Context:** The M3 dual review and the 3.11 follow-ups found gaps in
  D-144, D-145, D-147, D-148, D-149, D-150 and D-151: a retention sweep
  could delete an artifact while it was being downloaded; the configured
  `databases_excluded` finding made `has_warnings` permanently true; long
  multibyte warnings could exceed the `warnings` column CHECKs and make
  `Finish` fail; `MarkSigned` signed every verified object of the run on a
  destination instead of the manifest's entries; the completeness warnings
  of a failed dump were dropped; `workers.max_concurrent_downloads` was not
  wired; one failing job blocked a whole scheduler batch; `cancelRun` could
  not stop a pending retry; the download hold-back buffer could be
  reallocated by `append` with plaintext left in the old array; and an
  unfiltered `runUpdated` stream loaded and authorized every run event.
- **Decision:**
  1. **Artifact holds** (migration 00019, `artifact_holds`: artifact,
     kind `download|restore`, holder id, expiry ≤ 1 h; `bekup_app` may
     INSERT/SELECT/DELETE and UPDATE only `expires_at`). A download takes a
     hold when its ticket is redeemed (in the redemption transaction, under
     a `FOR SHARE` lock of the present artifact row), renews it every third
     of `HoldTTL` (default 5 min) while it streams and releases it at the
     end; an expired hold is the safety net (a hold that could not be
     renewed stops the stream, `download.failed` reason
     `artifact_hold_lost`). Retention deletes each object inside a
     transaction that locks the artifact row `FOR NO KEY UPDATE` and checks
     for an unexpired hold first: a held artifact is skipped (`held` in the
     sweep result and the `retention.swept` audit details) and retried by
     the next sweep, and while the row is locked no hold can be taken, so a
     hold and a deletion never interleave. A run's manifest is only deleted
     once all its dumps are gone, so a held dump keeps it. Restores (4.x)
     use the same API (`runstate.AcquireHoldTx` kind `restore`). Expired
     holds are removed by the download cleanup.
  2. **Run notices:** `runs.notices` (jsonb array ≤ 64 KiB) holds
     informational findings that are not warnings. `databases_excluded` is a
     notice now, so `has_warnings` flags real problems only. The manifest no
     longer lists it as a warning; its `stats` object gains
     `"databases_excluded": [<encoded db>, ...]` (strings only, at most
     1000 names, D-144 canonical rules), so the exclusion is still signed.
     GraphQL `Run.notices: [RunWarning!]!`.
  3. **Byte-bounded warnings:** each warning message is capped at 500
     characters and 1024 bytes, its database name at 255 bytes; the encoded
     list is budgeted per entry (encoded size + 16 bytes for jsonb's extra
     spaces) to 200 KiB for runs and 60 KiB for items and notices, well
     below the CHECKs (256/64/64 KiB), with one closing
     `warnings_truncated` entry. `Finish`/`FinishItem` can no longer fail on
     warnings.
  4. **Exact manifest signing:** `AddArtifact` returns the artifact id; the
     worker passes exactly the artifact ids of the manifest's entries on a
     destination to `MarkSigned`, which signs only those (and fails without
     recording anything unless each is a verified, unsigned object of the
     run on that destination).
  5. **Failed dumps keep completeness warnings:** a dump that fails or is
     unverified still validates the runner's stats and records their
     warnings on the item; only the `backup_stats` row and the manifest
     stats are skipped.
  6. **Download limits:** `workers.max_concurrent_downloads` (1-64, default
     4) and the new `workers.max_downloads_per_user` (1..the former,
     default 2) configure the downloads service of each serve process.
  7. **Scheduler savepoints:** each due job is handled under its own
     savepoint; a failing job is rolled back alone (no River job, no run
     row, `next_run_at` unchanged), logged, excluded from the rest of the
     tick and retried by the next tick (`LockDueJobs` takes an exclude
     list). A failure of the batch transaction itself still fails the batch.
  8. **Cancel a pending retry:** `cancelRun` of a finished run that is the
     newest attempt of its River job, while the job waits (`available`,
     `scheduled`, `pending` or `retryable`; the River row is locked first),
     cancels that job, so no new attempt starts. The run keeps its final
     status; the response has `retryCanceled: true`; audited as
     `run.cancel_requested` with `retry_canceled`.
  9. **Hold-back buffer:** fixed capacity, never reallocated; `Write`
     copies its input in pieces that fit and keeps the tail in the same
     array, so `wipe` zeroes every byte of plaintext it held.
  10. **Coalesced run events:** each `runUpdated` stream keeps a set of
      pending run ids (a run already pending is not queued again; delivery
      re-loads its current state anyway), at most 100 pending (beyond that
      it falls back to a resync from the database), and delivers at most
      one run every 50 ms. Filters stay optional (the UI's run list needs
      the unfiltered stream); the cost of an unfiltered stream is bounded
      per distinct run instead of per event.
- **Alternatives considered:** a `holds` column on artifacts (no history of
  holders, harder renewal); retention skipping artifacts with recent
  download tickets (tickets end at redemption, streams last longer);
  holding no row lock during the delete (a hold could be taken between
  the check and the delete); a separate `notices` table (one jsonb column
  matches `warnings`); character-based caps only (multibyte input exceeds
  the byte CHECKs); requiring a filter for non-admins on `runUpdated` (owners
  need a cross-job list; coalescing bounds the cost without breaking it);
  advancing a failing job's `next_run_at` (loses the occurrence).
- **Consequences:** Retention holds one pool connection and a row lock
  per object while deleting it (bounded by a 5-minute storage timeout).
  A download that cannot renew its hold for 5 minutes is aborted. Security
  impact: an artifact being read can no longer be deleted under the
  reader; signing keys can only mark objects the manifest lists; run
  warnings can no longer make the terminal write fail (no run stuck
  `running` on hostile catalog names); no plaintext copy survives in
  freed memory; one subscriber cannot amplify run events into unbounded
  database load.
- **Related:** D-144, D-145, D-147, D-148, D-149, D-150, D-151; tasks
  3.6-3.11, 4.x; `internal/db/migrations/00018_backup_stats.sql`,
  `internal/db/migrations/00019_artifact_holds.sql`,
  `internal/runstate/holds.go`, `internal/runstate/store.go`,
  `internal/retention/worker.go`, `internal/downloads`,
  `internal/jobs/backup_*.go`, `internal/jobs/cancel.go`,
  `internal/queue/client.go`, `internal/scheduler/loop.go`,
  `internal/graph/resolvers/runs_events.go`, `internal/config`.

## D-156: Job calendar, start early, per-connection run limit and enable/disable

- **Date:** 2026-10-28
- **Status:** accepted
- **Decided by:** user
- **Context:** The user asked for a calendar view of backup jobs, starting a
  planned job early, a "max concurrent backups" setting per server, and
  enabling/disabling jobs. Enable/disable (`enableJob`/`disableJob`) and an
  extra on-demand run (`runJobNow`) already existed in the backend; a global
  per-connection limit existed in config.
- **Decision:**
  - **Start early:** offer both "run now (extra run)" (unchanged
    `runJobNow`) and "start the next planned run early"
    (`startNextRunEarly`): the next planned occurrence runs now (trigger
    `early`) and is skipped at its original time; the schedule continues
    from the following occurrence. A conflict (a run already active) changes
    nothing.
  - **Server = connection:** a per-connection `max_concurrent_runs`
    (NULL = the global `workers.max_runs_per_connection`), admin + step-up
    to change.
  - **Calendar:** planned occurrences (incl. paused, started-early and
    skipped markers), past/active runs by status, and retention expiries
    (exact where determined by Object Lock or keep-days, projected where they
    depend on future runs under keep-last); month, week and day views;
    owners see only their jobs.
  - **Timing:** backend now (tasks 3.12–3.14, before M4), UI in M6 (6.11
    calendar page; enable/disable and both run actions in 6.5/6.6; the
    connection limit in 6.3).
- **Alternatives considered:** start early as an extra run only, or as the
  pull-forward only (rejected: the user wants both); a per-host limit across
  connections (rejected: per connection is simpler and explicit).
- **Consequences:** a new run trigger value `early`; the calendar query is
  bounded (≤ 93 days, capped events) to stay cheap; projected expiries are
  labelled as projections.
- **Related:** tasks 3.12, 3.13, 3.14, 6.3, 6.5, 6.6, 6.11; D-143, D-146,
  D-147, D-148.

## D-157: Start the next planned run early: semantics details (task 3.12)

- **Date:** 2026-10-28
- **Status:** accepted (2026-10-28, user accepted the M3 addendum proposals) (amended by D-160: only the genuinely next occurrence can be started early; the security-impact statement below is corrected there)
- **Decided by:** agent with user approval (task 3.12)
- **Context:** D-156 decides that `startNextRunEarly` runs the next planned
  occurrence now and skips it at its original time. Details it leaves open:
  how the pulled-forward occurrence is recorded (calendar, task 3.14), the
  error codes of refusals, the authorization, the audit entry, and what
  "the following occurrence" is when the job is already overdue.
- **Decision:**
  - **Record:** a new `run_trigger` value `early` (queue trigger `early`);
    the run's `scheduled_for` is the original `next_run_at` and
    `triggered_by` the requesting user. A run with trigger `early` and its
    `scheduled_for` is the "started early" marker of that occurrence; no
    extra column or table. The `runs` migration (uncommitted M3) gains the
    enum value in place.
  - **One transaction** under the job's row lock (`FOR UPDATE`; the
    scheduler locks due jobs with `SKIP LOCKED`): enqueue (River job +
    queued run row) → advance `next_run_at` (and `last_scheduled_at` =
    now) → audit. River's unique key refusing the insert is CONFLICT
    ("a run of this job is already active") and the transaction changes
    nothing.
  - **New next_run_at:** `Plan.Next(original)`, exactly one occurrence
    later (wall-clock schedules keep their local time across DST changes;
    monthly day-31 schedules clamp). Only when the job was overdue
    (original ≤ now, the scheduler had not handled it yet) and that next
    occurrence has passed too, it is `Plan.Next(now)`, matching the
    scheduler's collapse of missed occurrences (D-143). A schedule without
    a further occurrence is refused (VALIDATION) like the scheduler refuses
    to run it.
  - **Refusals:** disabled job, manual schedule, or no `next_run_at` →
    VALIDATION (field `id`) with a message naming the reason (manual points
    to `runJobNow`); deleted or unknown jobs → NOT_FOUND like every other
    job operation (no hint that a deleted job exists).
  - **Authz:** new operation `run.start_early` with the same rule as
    `run.now` (admin, maintainer, owner of the job with role user; no
    step-up). An early start only moves an occurrence the owner could also
    trigger as an extra run.
  - **Audit:** `job.run_started_early` (name, `queue_job_id`, `run_id`,
    `scheduled_for` = the original occurrence, `next_run_at` = the new one);
    refusals record nothing.
- **Alternatives considered:** a `runs.started_early_for` column or a
  `schedule_skips` row with reason `started_early` (rejected: duplicates
  what `trigger = early` + `scheduled_for` already say); refusing overdue
  jobs (rejected: the scheduler may simply not have ticked yet);
  VALIDATION for deleted jobs (rejected: inconsistent with the other job
  operations and reveals deleted rows); step-up (rejected: the same
  privilege as `runJobNow`).
- **Consequences:** the scheduler never sees the original slot as due
  because `next_run_at` moved in the same transaction. A missed scheduled
  run after an early start is not possible; a conflict leaves the schedule
  untouched. Security impact: none beyond `runJobNow` (same authorization,
  audited, owners limited to their jobs).
- **Related:** task 3.12; D-143, D-145, D-146, D-147, D-156.

## D-158: Per-connection run limit: details (task 3.13)

- **Date:** 2026-10-28
- **Status:** accepted (2026-10-28, user accepted the M3 addendum proposals)
- **Decided by:** agent with user approval
- **Context:** D-156 decides a per-connection `max_concurrent_runs`
  (NULL = `workers.max_runs_per_connection`, admin + step-up). It does not
  say where the value is applied, how it interacts with a global cap that
  is lowered later, how it is cleared over GraphQL, or whether restores
  count against it.
- **Decision:**
  1. **Column** `connections.max_concurrent_runs integer NULL`, CHECK
     1..64 (migration 00020). The service also refuses values above
     `workers.max_concurrent_runs` of the serve process (VALIDATION).
  2. **Applied in the slot query:** `TryInsertRunSlot` reads the column
     under the run-slot advisory lock (D-147) and falls back to the
     acquiring process's `workers.max_runs_per_connection`. No worker reads
     or caches it, so a change applies to the next acquisition. Lowering
     the limit never stops active runs; new runs snooze until the
     connection's slot count is below the new limit. The global
     `workers.max_concurrent_runs` check stays in the same query, so a
     connection's limit can never exceed the global cap in practice.
  3. **Backups and restores share the limit** (both take run slots on the
     connection; restores in M4).
  4. **API:** `Connection.maxConcurrentRuns` (nullable) and
     `effectiveMaxConcurrentRuns` = own value or the global default,
     clamped to `workers.max_concurrent_runs`; visible to admins and
     maintainers (metadata, not under `settings`). `createConnection` and
     `updateConnection` accept `maxConcurrentRuns`; `updateConnection`
     takes `clearMaxConcurrentRuns` to return to the default (the
     `clear*` pattern of the other inputs). Both mutations keep their
     admin + step-up rule; no new operation.
  5. **Validation only when set:** a stored value above a since lowered
     global cap does not block unrelated updates (the slot query caps it).
  6. **Audit:** `connection.created` settings and `connection.updated`
     `changed`/`values` carry `max_concurrent_runs` with old/new values.
- **Alternatives considered:** passing the limit from the worker (needs a
  connection lookup in `internal/jobs` and could act on a stale value);
  a nullable-input distinction instead of a clear flag (gqlgen omittable
  fields; inconsistent with the other inputs); rejecting stored values
  above a lowered cap on every update (blocks unrelated edits).
- **Consequences:** a one-line subquery in the slot statement (indexed by
  primary key). Security impact: none; the limit only bounds load on the
  target, changes need admin + step-up and are audited.
- **Related:** task 3.13, 6.3; D-135, D-140, D-147, D-156.

## D-159: Job calendar query: details (task 3.14)

- **Date:** 2026-10-28
- **Status:** accepted (2026-10-28, user accepted the M3 addendum proposals) (amended by D-160: overdue catch-up, time-prefix truncation, run look-back by start time, bounded reads)
- **Decided by:** agent with user approval (task 3.14)
- **Context:** D-156 decides the calendar's content (planned, paused,
  started-early and skipped occurrences, runs, exact and projected
  retention expiries), the 93-day bound and owner scoping. Open were the
  exact expiry semantics, the bounds, the scope details and how the data is
  loaded without per-job queries.
- **Decision:**
  - **API:** `calendar(from, to, timezone, jobIds)` → flat
    `CalendarEvent`s (kind PLANNED / RUN / SKIPPED / EXPIRY) ordered by
    start, plus `truncated`. Times are UTC; `timezone` (validated like a
    schedule zone: IANA name, never `Local`) is only echoed for the
    client's day bucketing. `to` > `from`, `to − from` ≤ 93 × 24 h, both in
    the scheduler's years 2000–2199; `jobIds` ≤ 500, `null` = every
    visible job, `[]` = none. Authz `calendar.view`: every role, jobs
    filtered by `JobScope`; invisible ids are dropped silently (no
    probing). Only non-deleted jobs appear.
  - **Planned:** occurrences in [max(from, now), to) from `next_run_at`
    (enabled jobs) or from the schedule (disabled jobs: `paused`), ≤ 500
    per job. A run with trigger `early` whose `scheduled_for` is in the
    range is shown as that occurrence with `startedEarly` (D-157); a
    computed occurrence or skip record at the same slot is suppressed.
  - **Runs:** start = `started_at`, else `scheduled_for` (not for early
    runs), else `queued_at`; end = `finished_at` (active runs last until
    now). The scan looks back 8 days before `from` (the job timeout is at
    most 7 days) plus every still active run.
  - **Expiries:** per (run, destination) copy of present artifacts, the
    earliest time retention may remove it, computed by
    `retention.Expiries` from the same keep rules as `retention.Plan`
    (shared helpers, and a property test checks the projection against
    `Plan`): the later of keep-days (run time + days), the time of the
    N-th future complete run when keep-last or the newest-complete rule
    (or the newer-incomplete rule) still keeps it, and Object Lock
    `lock_until`. `projected` is set whenever the time depends on future
    runs; future runs are the job's planned occurrences assuming success,
    only for enabled scheduled jobs (paused or manual jobs get no projected
    expiries). Already eligible copies show at now ("next sweep").
    Completeness per copy is aggregated in SQL with `Plan`'s meaning.
  - **Loading and bounds:** four batched queries for the whole job set
    (jobs ≤ 500, runs ≤ 5,000, skips ≤ 5,000, copy summaries ≤ 50,000; a
    cut-off job gets no expiries), the projection ≤ 100,000 schedule
    computations per request, ≤ 5,000 events. Hitting any bound sets
    `truncated`.
- **Alternatives considered:** loading every artifact and running
  `retention.Plan` per job (rejected: unbounded rows); `projected` only
  when the projected date is the latest one (rejected: the date is still
  conditional on future runs); returning local times (rejected: UTC plus
  the zone is unambiguous across DST); showing deleted jobs (rejected: the
  calendar is about live jobs; history stays in `runs`).
- **Consequences:** a read-only, constant-query endpoint whose cost is
  bounded per request (on top of the GraphQL complexity, rate limit and
  body limits). Security impact: no new data beyond what `runs`,
  `artifacts` and `jobs` already show to the same caller; owners see only
  their jobs; detail texts are fixed strings or stored codes.
- **Related:** task 3.14, 6.11; D-143, D-145, D-146, D-148, D-156, D-157.

## D-160: M3 addendum review fixes: one early start ahead, calendar catch-up, time-prefix truncation and bounded reads

- **Date:** 2026-10-28
- **Status:** accepted (2026-10-28, user accepted the M3 addendum proposals)
- **Decided by:** agent with user approval (M3 addendum review fixes)
- **Context:** The reviews of tasks 3.12–3.14 found: (security, medium) an
  owner could call `startNextRunEarly` again after each early run finished
  and push `next_run_at` weeks ahead, effectively pausing the planned
  backups of a job whose schedule owners cannot edit; D-157 wrongly stated
  "no security impact beyond `runJobNow`". (code) The calendar projected an
  overdue job's future runs from the missed occurrences instead of one
  catch-up run at now; the event cap kept the first events computed, not
  the earliest; the run scan looked back by `queued_at`, missing long-queued
  runs that started recently; an overdue job's pending run was invisible.
  (security, low) The calendar's copy query counted run items in correlated
  subqueries, its `trigger = 'early'` branch could scan a job's whole run
  history, and the reads had no statement timeout.
- **Decision:**
  1. **At most one occurrence ahead.** `startNextRunEarly` refuses
     (VALIDATION, field `id`, "the next planned run was already started
     early") when the stored `next_run_at` is later than the schedule's
     natural next occurrence after now (`scheduler.Next(schedule, now)`),
     i.e. the next occurrence was already pulled forward. Once the original
     time of the pulled-forward occurrence has passed, the stored value is
     the natural next one again and an early start is allowed. The check
     runs under the job's row lock before anything is enqueued; refusals
     record nothing. An active run still answers CONFLICT when the
     occurrence is the natural next one.
  2. **Calendar catch-up (D-143 semantics).** For an enabled job whose
     `next_run_at` ≤ now, the projection counts one catch-up run at now and
     continues from `Plan.Next(now)`, like `scheduler.Plan.Due`; the
     calendar shows that pending run as a PLANNED event at now with the new
     `CalendarEvent.overdue` flag and a fixed detail text, followed by the
     occurrences after now.
  3. **Truncation is a time prefix.** The event collector keeps the
     `MaxEvents` earliest events (start, then job, kind, run and
     destination ids for a deterministic order) whatever order they are
     computed in; it holds at most 2 × `MaxEvents` (sort and cut when full,
     then refuse events after the last kept one).
  4. **Run look-back by start.** `ListCalendarRuns` bounds finished runs by
     `coalesce(started_at, queued_at) >= scan_from` (a run lasts at most the
     job timeout after it started).
  5. **Bounded reads.** `ListCalendarRuns` is a `UNION` of three
     index-bounded branches (look-back: new index `runs_job_start_idx` on
     `(job_id, coalesce(started_at, queued_at))`; active runs:
     `runs_active_idx`; started-early slots: new partial index
     `runs_job_early_idx` on `(job_id, scheduled_for) WHERE trigger =
     'early'`; migration 00021). `ListCalendarCopies` computes each run's
     item count once (CTE) instead of per copy; the present artifacts use
     the existing partial `artifacts_job_destination_idx` (leading
     `job_id`, `WHERE state = 'present'`), checked with EXPLAIN on a seeded
     database, so no new artifacts index. The four reads run in one
     transaction started with `SetCalendarReadLimits` (read only,
     `statement_timeout` = `calendar.QueryTimeout`, 5 s, transaction-local);
     a timeout fails the request (masked internal error).
  6. **Docs.** Backups and restores share the per-connection slot pool (a
     limit of 1 makes a restore wait behind a backup); a stored
     `maxConcurrentRuns` may exceed a since lowered global cap (shown
     clamped, the global cap is enforced by `TryInsertRunSlot`); artifact
     holds can briefly outlive a shown expiry; `runJobNow` answers CONFLICT
     but `startNextRunEarly` VALIDATION for a disabled job (established
     behaviour of D-146/D-157, documented for the M6 UI rather than
     changed).
- **Alternatives considered:** a per-job cooldown or a counter of early
  starts (rejected: time-based state the schedule already implies);
  allowing owners only one early start per day (rejected: arbitrary and
  still drifts for sub-daily schedules); suppressing the overdue event
  (rejected: the pending run is real and imminent); a separate query for
  early runs (rejected: one more round trip; the union keeps the constant
  query count); a configurable calendar timeout (rejected for now: a
  constant is enough for a bounded read, configurable later if needed).
- **Consequences:** **Security impact (corrects D-157):** an early start
  does change the planned schedule, unlike `runJobNow`; without this rule a
  backup owner could defer a job's planned backups indefinitely. With it,
  `next_run_at` is never more than one occurrence beyond the natural
  schedule, so planned backups are delayed by at most one interval and
  never skipped in sequence; every early start stays audited. The
  calendar's cost per request is bounded by indexes and a 5 s statement
  timeout in a read-only transaction (SV-22). Two more indexes on `runs`
  (slightly more write cost per run). A new GraphQL field
  `CalendarEvent.overdue` (additive).
- **Related:** tasks 3.12, 3.13, 3.14, 6.6, 6.11; D-143, D-146, D-155,
  D-157, D-158, D-159; SV-22.

## D-161: Notification event catalog and per-user defaults

- **Date:** 2026-10-28
- **Status:** accepted
- **Decided by:** user
- **Context:** Users should choose exactly which notifications they receive
  (e.g. backup failed, backup successful, backup started, restore started).
  The planned per-user preference matrix (D-023, D-051) only knew a handful of
  outcome events.
- **Decision:** A typed event catalog in four groups plus the mandatory
  security events: backup lifecycle (`backup.started`, `succeeded`,
  `succeeded_with_warnings`, `partial`, `failed`, `canceled`, `missed`,
  `started_early`), restore lifecycle (`restore.started`, `completed`,
  `failed`; `test_restore.passed`, `warnings`, `failed`), retention & storage
  (`retention.summary` as a daily digest, `artifact.missing`,
  `destination.test_failed`), job & config changes (`job.created`, `updated`,
  `disabled`, `enabled`, `deleted`, `encryption_changed`;
  `connection.pin_changed`, `tls_downgraded`, `host_key_changed`; staff, and
  owners only for their own jobs' job events). Recipients are users who may
  see the subject at delivery time and whose preference for (event, channel)
  is on; per-job overrides win. **New-user defaults: every event visible to
  the user's role goes to the in-app inbox; email only for problem events;
  webhooks/push are opt-in.** `*.started` events are low priority and
  collapsed per job within a short window; retention removals are a daily
  digest, never per file. Security alerts stay mandatory (inbox + email).
- **Alternatives considered:** problem-only defaults; nothing by default
  (both rejected by the user in favour of "everything in-app").
- **Consequences:** a busier in-app inbox by default (mitigated by the
  collapse rules and per-event toggles); emitters wired through the existing
  runstate/pubsub/audit hooks so new events don't scatter calls.
- **Related:** tasks 4.6, 5.3, 5.3b, 5.4, 6.10; amends the event list of
  D-051 (and D-023's scope).

