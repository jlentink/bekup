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
| [D-023](#d-023-notifications-phase-1) | Notifications (Phase 1) | accepted (push channel superseded by D-206) |
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
| [D-106](#d-106-authorization-model-operation-catalog-policy-matrix-and-owner-scope-task-14) | Authorization model: operation catalog, policy matrix and owner scope (task 1.4) | accepted (job.encryption_keys_edit roles superseded by D-154; job.test_restore_edit freshness amended by D-169) |
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
| [D-161](#d-161-notification-event-catalog-and-per-user-defaults) | Notification event catalog and per-user defaults | accepted (push default superseded by D-206) |
| [D-162](#d-162-restore-staging-and-verification) | Restore staging and verification | proposed |
| [D-163](#d-163-temporary-restore-connections) | Temporary restore connections | proposed |
| [D-164](#d-164-offline-recovery-tooling-verify-decrypt-reindeximport-and-public-key-export) | Offline recovery tooling: verify, decrypt, reindex/import and public key export | proposed |
| [D-165](#d-165-mysqlmariadb-restore-in-place-overwrite-by-drop--create-in-the-client-session-hardened-client-trailer-check) | MySQL/MariaDB restore: in-place overwrite by DROP + CREATE in the client session, hardened client, trailer check | proposed |
| [D-166](#d-166-restore-worker-and-postgresql-restore-temp-database--swap-droptempdatabase-rpc-restore-logs-and-events) | Restore worker and PostgreSQL restore: temp database + swap, DropTempDatabase RPC, restore logs and events | proposed |
| [D-167](#d-167-restore-api-target-rules-fresh-second-factor-confirmations-request-time-manifest-check-cancel-and-the-stuck-restore-sweep) | Restore API: target rules, fresh second factor, confirmations, request-time manifest check, cancel and the stuck-restore sweep | proposed |
| [D-168](#d-168-privileged-restore-targets-probe-privilege-verdict-fail-closed-unknown-admin--acknowledgement) | Privileged restore targets: Probe privilege verdict, fail-closed unknown, admin + acknowledgement | proposed (amended by D-172, D-173) |
| [D-169](#d-169-scheduled-test-restores-scratch-target-internal-restores-runner-derived-scratch-databases-comparison-rules-and-results) | Scheduled test restores: scratch target, internal restores, runner-derived scratch databases, comparison rules and results | proposed (amended by D-170) |
| [D-170](#d-170-test-restores-privileged-scratch-targets-hardened-scratch-databases-and-the-implicit-public-schema-m4-review) | Test restores: privileged scratch targets, hardened scratch databases and the implicit public schema (M4 review) | proposed |
| [D-171](#d-171-neutralize-restored-postgresql-databases-security-definer-to-invoker-no-public-access-warn) | Neutralize restored PostgreSQL databases: SECURITY DEFINER to INVOKER, no PUBLIC access, warn | accepted |
| [D-172](#d-172-privileged-restore-targets-global-data-privileges-and-pg_readwrite_all_data-are-gated-corrects-d-168) | Privileged restore targets: global data privileges and pg_read/write_all_data are gated (corrects D-168) | proposed |
| [D-173](#d-173-restore-core-review-fixes-worker-privilege-re-check-stored-acknowledgement-slot-bound-temporary-targets-swap-state-cancel-race-identity-redaction) | Restore core review fixes: worker privilege re-check, stored acknowledgement, slot-bound temporary targets, swap state, cancel race, identity redaction | proposed |
| [D-176](#d-176-prometheus-metrics-dedicated-registry-bounded-labels-internal-listener-with-optional-bearer-systemhealth-task-56) | Prometheus metrics: dedicated registry, bounded labels, internal listener with optional bearer, systemHealth (task 5.6) | proposed |
| [D-177](#d-177-webhook-sender-signature-scheme-signing-keys-url-rules-result-classification-and-auto-disable-policy-task-52) | Webhook sender: signature scheme, signing keys, URL rules, result classification and auto-disable policy (task 5.2) | proposed |
| [D-178](#d-178-smtp-settings-e-mail-sender-and-user-e-mail-verification-task-51) | SMTP settings, e-mail sender and user e-mail verification (task 5.1) | proposed (transport/AUTH rules superseded by D-180) |
| [D-179](#d-179-web-ui-shell-and-auth-flows-session-gate-auth-event-bus-step-up-hook-strict-qr-re-rendering-task-61) | Web UI shell and auth flows: session gate, auth event bus, step-up hook, strict QR re-rendering (task 6.1) | proposed |
| [D-180](#d-180-smtp-follows-the-servers-tls-support) | SMTP follows the server's TLS support | accepted |
| [D-181](#d-181-user-admin-ui-one-time-temporary-passwords-ui-only-self-guards-derived-2fa-state-admin-only-owner-picker-task-68) | User admin UI: one-time temporary passwords, UI-only self guards, derived 2FA state, admin-only owner picker (task 6.8) | proposed (derived 2FA state and admin-only picker superseded by D-195) |
| [D-182](#d-182-connections-and-destinations-ui-deliberate-pin-and-host-key-confirmation-client-side-retargeting-prompt-session-only-test-results-tasks-63-64) | Connections and destinations UI: deliberate pin and host-key confirmation, client-side retargeting prompt, session-only test results (tasks 6.3, 6.4) | proposed (item 5 superseded by D-195) |
| [D-183](#d-183-notification-core-transactional-outbox-audit-driven-emitters-delivery-time-authorization-noise-rules-and-storage-task-53) | Notification core: transactional outbox, audit-driven emitters, delivery-time authorization, noise rules and storage (task 5.3) | proposed (push parts of items 3 and 6 superseded by D-206) |
| [D-184](#d-184-user-directory-for-staff-admin-activity-facts-on-user-and-the-jobs-owner-filter-task-68-follow-up) | User directory for staff, admin activity facts on User and the jobs owner filter (task 6.8 follow-up) | proposed |
| [D-185](#d-185-restore-wizard-and-calendar-ui-proactive-aal2-step-up-request-only-age-identity-client-side-calendar-filters-tasks-67-611) | Restore wizard and calendar UI: proactive AAL2 step-up, request-only age identity, client-side calendar filters (tasks 6.7, 6.11) | proposed |
| [D-186](#d-186-jobs-and-runs-ui-start-early-ledger-no-cache-one-time-keys-and-tickets-virtualized-log-tail-derived-completeness-evidence-tasks-65-66) | Jobs and runs UI: start-early ledger, no-cache one-time keys and tickets, virtualized log tail, derived completeness evidence (tasks 6.5, 6.6) | proposed (item 5 per-table remark superseded by D-195) |
| [D-187](#d-187-runitemstats-and-stored-last-test-results-of-connections-and-destinations-task-66-63-64-follow-up) | RunItem.stats and stored last test results of connections and destinations (task 6.6, 6.3, 6.4 follow-up) | proposed |
| [D-188](#d-188-missed-backup-dead-man-alerts-recorded-occurrences-derived-window-one-alert-per-outage-with-daily-reminders-task-54) | Missed-backup (dead-man) alerts: recorded occurrences, derived window, one alert per outage with daily reminders (task 5.4) | proposed |
| [D-190](#d-190-security-alerts-audit-driven-mandatory-deduplicated-burst-detection-hourly-security-checks-cli-alerts-via-the-outbox-task-55) | Security alerts: audit-driven, mandatory, deduplicated; burst detection; hourly security checks; CLI alerts via the outbox (task 5.5) | proposed |
| [D-191](#d-191-notifications-api-shared-and-personal-channels-the-preference-matrix-the-inbox-and-its-live-stream-task-53b) | Notifications API: shared and personal channels, the preference matrix, the inbox and its live stream (task 5.3b) | proposed (item 7 superseded by D-197; push in item 5 by D-206) |
| [D-192](#d-192-dashboard-ui-one-run-stream-polled-restores-client-derived-health-and-security-findings-bounded-storage-sums-task-62) | Dashboard UI: one run stream, polled restores, client-derived health and security findings, bounded storage sums (task 6.2) | proposed (items 1, 2 "missed" and 4 superseded by D-196; item 1 stream by D-197) |
| [D-193](#d-193-notification-center-ui-one-inbox-stream-in-the-bell-in-app-link-allowlist-memory-only-verification-code-no-cache-signing-keys-task-610) | Notification center UI: one inbox stream in the bell, in-app link allowlist, memory-only verification code, no-cache signing keys (task 6.10) | proposed (item 1 superseded by D-197; push column in item 5 by D-206) |
| [D-194](#d-194-admin-settings-ui-settings-hub-client-filtered-security-alerts-audit-chain-status-from-auditverifyfailed-read-only-ssrf-allowlist-guarded-passphrase-export-task-69) | Admin settings UI: settings hub, client-filtered security alerts, audit chain status from audit.verify.failed, read-only SSRF allowlist, guarded passphrase export (task 6.9) | proposed (items 2-4 superseded by D-196) |
| [D-195](#d-195-m6-ui-follow-ups-stored-test-results-per-item-statistics-shared-owner-picker-test-restore-pages-and-settings-stable-web-tests-task-612) | M6 UI follow-ups: stored test results, per-item statistics, shared owner picker, test-restore pages and settings, stable web tests (task 6.12) | proposed |
| [D-196](#d-196-runtime-ssrf-allowlist-api-inbox-filter-audit-chain-status-and-dashboard-aggregates-task-612-follow-ups) | Runtime SSRF allowlist API, inbox filter, audit chain status and dashboard aggregates (task 6.12 follow-ups) | proposed (item 8 streams superseded by D-197; allowlist scopes added by D-198) |
| [D-197](#d-197-stream-budget-one-shared-account-stream-combined-page-streams-cross-tab-relay-and-429-backoff) | Stream budget: one shared account stream, combined page streams, cross-tab relay and 429 backoff | proposed |
| [D-198](#d-198-scoped-ssrf-allowlist-signed-delivery-id-and-event-type-bucketed-personal-tests-and-other-m5-security-follow-ups) | Scoped SSRF allowlist, signed delivery id and event type, bucketed personal tests and other M5 security follow-ups | proposed |
| [D-199](#d-199-web-review-follow-ups-one-deny-by-default-route-guard-confirmed-sign-out-strict-return-paths-shared-web-modules-and-build-time-document-splitting) | Web review follow-ups: one deny-by-default route guard, confirmed sign-out, strict return paths, shared web modules and build-time document splitting | proposed |
| [D-200](#d-200-runtime-config-reload-of-a-conservative-set-of-live-keys-admin-ui-broadcast-and-sighup) | Runtime config reload of a conservative set of live keys (admin UI broadcast and SIGHUP) | proposed |
| [D-201](#d-201-dev-only-runner-for-a-native-serve-loopback-published-on-a-dedicated-network) | Dev-only runner for a native serve, loopback-published on a dedicated network | proposed (dev subnet moved by D-205) |
| [D-202](#d-202-manual-multi-arch-image-publishing-to-the-operators-own-registry-just-publish) | Manual multi-arch image publishing to the operator's own registry (`just publish`) | proposed |
| [D-203](#d-203-container-only-compose-install-bekup-setup-with-embedded-assets-an-ownership-manifest-and-a-root--cap_chown-setup-container) | Container-only compose install (`bekup setup`) with embedded assets, an ownership manifest and a root + `CAP_CHOWN` setup container | proposed (partly superseded by D-205) |
| [D-204](#d-204-release-workflow-signed-tag-only-ghcr-images-pushed-by-digest-scanned-signed-and-attested-before-tagging) | Release workflow: signed-tag-only GHCR images, pushed by digest, scanned, signed and attested before tagging | proposed (extended by D-205) |
| [D-205](#d-205-compose-first-onboarding-release-compose-files-as-overlays-of-one-source-the-bekup-proxy-link-network-and-attested-assets) | Compose-first onboarding: release compose files as overlays of one source, the bekup-proxy link network and attested assets | proposed (amendment 1: resolved-model lint, link network `--ip-range`; amendment 2: shipped services pinned, interpolation allowlist, lint is a guardrail; amendment 3: committed `deploy/compose/` files with the `BEKUP_IMAGE` placeholder) |
| [D-206](#d-206-mobile-apps-and-the-push-channel-dropped-from-the-roadmap) | Mobile apps and the push channel dropped from the roadmap | proposed |

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
- **Status:** accepted (push channel superseded by D-206)
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
- **Status:** accepted (2026-10-27, user accepted all M1 proposals); the roles of `job.encryption_keys_edit` superseded by D-154; the freshness of `job.test_restore_edit` amended by D-169 (proposed)
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
- **Status:** accepted (push default superseded by D-206)
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


## D-162: Restore staging and verification

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 4.1)
- **Context:** The plan fixes "stage → verify → restore" (D-022, D-039):
  stage the artifact on the scratch volume, verify hash, manifest signature
  and the DB records in full, then restore from the local copy; identities
  never in River args (encrypted handoff, 15-minute TTL); staging deleted on
  every path. The details (schema, grants, at-rest protection of the staged
  copy, when the identity is read, free-space rule, holds, cleanup, sharing
  the verification with downloads) were open.
- **Decision:**
  1. **Schema** (migration 00022): `restores` (artifact, run item, job,
     target connection, target DB name (NULL only for globals), mode
     `new_db|overwrite`, requested_by, status
     `queued|staging|verifying|restoring|succeeded|failed|canceled`, River
     job id (set once), staged_bytes, started/finished, sanitized
     `error_code`/`error_message` like `runs`). `bekup_app` may INSERT and
     SELECT, UPDATE only the progress columns, never DELETE; a trigger keeps
     a terminal restore terminal and the identity columns fixed.
     `restore_identity_handoff` (restore id PK, `sealed_identity` sealed with
     the keyring under AAD `restore_identity_handoff:<restore_id>:identity`,
     `expires_at` ≤ created + 15 min with `created_at` forced to `now()`):
     `bekup_app` may only INSERT, SELECT and DELETE (no UPDATE, so a sealed
     identity is never rewritten or moved); an insert trigger allows a
     handoff only for a `queued` restore.
  2. **Service API** (`internal/restore`): `CreateTx` (for
     `restoreArtifact`, task 4.5, after authorization and the fresh 2FA
     check) validates the artifact (present, verified, signed, key version
     present), matches a recipient identity against the job's recipient and
     seals it into the handoff; `SetRiverJob`, `Finish` (fixed codes via
     `ErrorCode`), `Get`.
  3. **Stage** (worker): one transaction moves `queued → staging` (exactly
     once), takes an artifact hold of kind `restore` (D-155, renewed every
     third of the TTL until `Cleanup` or the end of the worker context) and
     **takes the handoff with `DELETE … RETURNING`** (the identity is read
     once, at the start, so a long download cannot outlive the 15-minute
     TTL; an expired handoff is refused and deleted). It then checks the key
     (passphrase version / identity match) and the **free space of
     `storage.scratch_dir` ≥ stored size + wrapping overhead +
     `storage.scratch_reserve_bytes` (default 1 GiB) before downloading**,
     streams the stored object into `restore-<id>/artifact.age` (dir 0700,
     file 0600, every access through an `os.Root` of the scratch directory;
     a group/world-writable scratch directory is refused) while hashing it,
     moves to `verifying` and verifies: stored size + SHA-256 = the artifact
     record, and the run manifest on the same destination through
     `artifactverify.VerifyManifest` (signature, binding to job, run, slug,
     timestamp and exactly the signed entries, plus the entry's encryption
     mode and passphrase/recipient version = the record, plus the manifest
     object's recorded digest).
  4. **At-rest protection of the staged copy: every staged file (not only
     opt-out ones) is wrapped with an ephemeral age X25519 key that exists
     only in memory** and is destroyed by `Cleanup`. For opt-out artifacts
     this keeps plaintext off the scratch volume (as planned); for encrypted
     ones it also makes the staged copy tamper-evident after verification
     (anyone with a job's public recipient could otherwise write a
     different valid age file between verification and restore). `Open`
     unwraps it, decrypts with the job passphrase version or the handed-off
     identity (destroyed right after `Open`), decompresses with the bounded
     `pipeline.OpenArtifact` helpers, bounds the stream by the recorded dump
     size and, at its end, re-checks the stored and the dump SHA-256
     (mismatch → error instead of EOF). `Open` may be called once.
  5. **Tampering is a security event:** any verification failure is a
     `*VerificationError` (code `stored_digest_mismatch`,
     `dump_digest_mismatch`, `manifest_bad_signature`,
     `manifest_unknown_key`, `manifest_replayed`,
     `manifest_binding_mismatch`, `manifest_malformed`, `manifest_invalid`,
     `decrypt_failed`), audited as `restore.verification_failed` (system
     actor, target the restore) and passed to an `AlertFunc` (logged at
     ERROR until task 5.5 delivers security alerts); the restore fails with
     `verification_failed`.
  6. **Cleanup on every path:** `Staged.Cleanup` (idempotent) removes the
     directory, stops and releases the hold and destroys the keys; `Stage`
     runs it on every error and on panics (re-raised), and registers it with
     `context.AfterFunc` on the worker context. A failed staging fails the
     restore (canceled when the context was canceled, `artifact_hold_lost`
     when the hold expired mid-download). A sweeper (serve and worker, at
     startup and every 10 minutes) removes `restore-<uuid>` directories of
     restores that are not running and any older than
     `storage.scratch_stale_after` (default 24 h, must exceed
     `workers.restore_timeout`), never those this process is staging;
     expired handoffs are deleted every minute.
  7. **Shared verification** (`internal/artifactverify`): `VerifyManifest`,
     `DigestReader`, `MatchIdentity` and `StoredFormat`, used by downloads
     (refactored, behaviour unchanged apart from also checking the entry's
     encryption facts and checking the signature before the manifest's
     recorded digest) and restores. Offline tools without the DB (task 4.7)
     keep `pipeline.VerifySignature`.
- **Alternatives considered:** wrapping only opt-out staging (rejected:
  leaves a TOCTOU window for recipient-encrypted artifacts); verifying the
  staged file once and restoring from it unprotected (same); reading the
  identity at `Open` time (rejected: a large download outlives the 15-minute
  TTL); restoring straight from the destination stream (rejected by D-022:
  the whole artifact must be verified before the first byte reaches the
  target); a row-level-security policy for the handoff (rejected: one app
  role, the grants and the insert trigger already bound it); refusing to
  start serve without a usable scratch directory (rejected: processes that
  never restore would need it; staging checks it instead).
- **Consequences:** **Security impact:** a tampered, swapped or replayed
  artifact or manifest is detected before any byte reaches a target, is
  audited and alerted; the staged copy is never plaintext on disk and
  cannot be replaced unnoticed; identities live in the DB only sealed and
  ≤ 15 minutes, are read once and never in River args; retention cannot
  delete an artifact during its restore. Costs: the scratch volume needs
  the stored size plus the reserve per concurrent restore; one extra
  ChaCha20-Poly1305 pass per restore; the decrypted dump is in memory only
  while streaming. Two new config keys (`storage.scratch_reserve_bytes`,
  `storage.scratch_stale_after`), one audit action, one migration.
- **Related:** tasks 4.1, 4.2, 4.3, 4.5, 4.6, 4.7, 5.5; D-022, D-039, D-142,
  D-144, D-145, D-147, D-150, D-154, D-155; SECURITY.md WK-2, WK-4, WK-20;
  `internal/restore`, `internal/artifactverify`,
  `internal/db/migrations/00022_restores.sql`.

## D-163: Temporary restore connections

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent
- **Context:** Task 4.4. The plan decided that temporary restore
  connections are admin only, created in the restore wizard, stored
  encrypted with `temporary` + `expires_at`, usable only as a restore
  target and deleted (credentials crypto-shredded) after the restore or
  after N hours (default 24 h, configurable maximum), with the runner's
  target allowlist and the TLS defaults still applying. Open details: how
  the existing connection flows treat them, how expiry and the restore end
  are detected, what "crypto-shred" means for a row in PostgreSQL, and
  whether rows are hard deleted.
- **Decision:**
  1. **Service** (`internal/connections`, `temporary.go`):
     `CreateTemporary` runs the normal create validation (catalog keys,
     D-132, pinned TLS by default, a weaker mode is the usual audited
     opt-out with `connection.tls_downgrade`) and sets
     `expires_at = now + restore.temp_connection_ttl`. No runner probe at
     creation: every later use (certificate/host-key capture, test,
     restore) goes through the runner and its target allowlist. Names may
     repeat (the unique index already excludes temporary rows); an empty
     name becomes `Temporary: <host>`. At most 50 unexpired temporary
     connections exist (`CONFLICT`), a soft cap against accidents.
  2. **Isolation:** `List`, `Get`, `Update`, `Delete`, `SetTLSMode` and
     `Target` (backup runs) never return a temporary row; the jobs service
     already refuses them (D-146). The runner, pin, host-key and SSH-key
     operations accept one only when `Actor.AllowTemporary` is set, which
     the resolvers set for admins only; for maintainers a temporary id is
     `NOT_FOUND`. Restores use `GetForRestore`, `LockRestoreTargetTx`
     (`FOR SHARE` inside the restore's insert transaction, task 4.5) and
     `RestoreTarget`. An expired row is unusable at once in every query
     (`expires_at > now`), independent of the cleanup latency.
  3. **Lifetime:** `ExtendTemporary` (admin, step-up) moves `expires_at` to
     a time in the future that is at most `restore.temp_connection_max_ttl`
     after `created_at`, so extensions can never keep a connection alive
     longer than the maximum. Config `[restore]`:
     `temp_connection_ttl` (default 24h, 5m..max) and
     `temp_connection_max_ttl` (default 168h, ≤ 30 days).
  4. **Cleanup:** River kind `temporary_connection_cleanup` (maintenance
     queue, periodic every 5 minutes, unique). It deletes a temporary
     connection when it expired, or when every restore that targeted it
     (`restores.target_connection_id`, task 4.1's table) has finished at
     least 15 minutes ago (a retry may reuse it meanwhile), never while a
     run slot on it is held. The decision is re-checked under the row
     lock. `created_for_restore_id` stays reserved and unused: the restores
     table already records the target, and an explicit binding would need
     4.5 to keep a second reference in sync.
  5. **Crypto-shred and soft delete:** deletion (manual or automatic) sets
     `secret_enc` to NULL, clears every flag describing a credential
     (`has_password`, `ssh_auth`, `ssh_has_passphrase`, `ssh_public_key`,
     `client_cert_pem`) and sets `deleted_at`, in one transaction with the
     audit entry (fail closed); the in-memory copy is zeroed. Rows are not
     hard deleted: `restores.target_connection_id` references them and the
     restore history keeps the non-secret target metadata. The sealed blob
     is not first overwritten with random bytes: under MVCC an UPDATE
     writes a new tuple version, so an overwrite does not touch the old
     bytes and would only suggest a guarantee it does not give. Physical
     removal of dead tuples happens at VACUUM; WAL archives and app-DB
     backups keep the sealed blob until their own retention ends, and it
     stays protected by the keyring (D-085).
  6. **Audit:** `connection.temporary_created` (settings without
     credentials, `expires_at`; plus `secret.changed`),
     `connection.temporary_extended` (from, to),
     `connection.temporary_deleted` (admin, reason `manual`) and
     `connection.temporary_expired` (system actor, reason `expired` or
     `restore_finished`); both deletions list `destroyed_fields` and add a
     `secret.changed` entry with `destroyed: true`.
  7. **API:** `createTemporaryConnection` (single root field),
     `extendTemporaryConnection`, `deleteTemporaryConnection` (admin +
     step-up: `connection.temporary_create|extend|delete`) and
     `temporaryConnections` (admin: `connection.temporary_list`);
     `Connection.temporary` and `Connection.expiresAt`.
- **Alternatives considered:** reusing `createConnection` with a flag
  (rejected: would mix the admin-only restore path into the general
  connection API and its listings); letting `Extend` count from now
  (rejected: unbounded lifetime); hard deleting rows (rejected: the
  restores FK and the history); a random overwrite before NULL (rejected,
  see 5); a per-row key held outside the DB for true erasure (rejected for
  now: keys only in memory would not survive a restart, keys in the DB
  share the backup problem); probing the target at creation (rejected:
  pinned connections cannot be probed before the pin, and the allowlist
  applies on every use anyway); binding through `created_for_restore_id`
  (see 4).
- **Consequences:** **Security impact:** a temporary connection can only
  be created, seen, pinned, extended and deleted by an admin with a fresh
  step-up; it can never become a backup source; it is unusable the moment
  it expires and its credentials are removed from the live table within
  minutes; every lifecycle step is audited. Residual risk: dead tuples,
  WAL archives and app-DB backups retain the sealed blob until VACUUM or
  their retention; anyone holding such a copy also needs the keyring. One
  River kind, four audit actions, four authz operations, two config keys;
  the cleanup query depends on task 4.1's `restores` table
  (`target_connection_id`, `finished_at`).
- **Related:** tasks 4.1, 4.4, 4.5; D-015, D-058, D-085, D-132, D-135,
  D-140, D-145, D-146, D-147, D-162; SECURITY.md SV-7, SV-23, SC-6;
  `internal/connections/temporary.go`, `internal/connections/cleanup.go`,
  `internal/db/queries/connections.sql`.

## D-164: Offline recovery tooling: verify, decrypt, reindex/import and public key export

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 4.7)
- **Context:** D-040 asks for `bekup verify` / `bekup decrypt` with a
  passphrase or identity, a documented format, a destination
  re-index/import and escrow documentation for the master key and the
  signing-key fingerprint. Open were: where the offline tools get the
  public keys, how strictly decrypt verifies, how secrets reach the CLI,
  how a destination is described without the DB, what an import writes
  and when it may mark artifacts verified.
- **Decision:**
  1. **Package** `internal/offline` (reusable: `VerifyManifest`,
     `CheckArtifact`, `VerifyRun`, `Decrypt`, `Source`/`DirSource`/
     `DestSource`, `Scan`, `Importer`, `SafeText`); commands in
     `cmd/bekup` (`verify`, `decrypt`, `reindex`, `keys export-public`).
     The offline tools use `pipeline.VerifySignature` (no DB binding) and
     always print the job and run identity for the operator's replay check;
     DB-backed code keeps `internal/artifactverify` (D-162).
  2. **Public keys:** `bekup keys export-public` (needs the DB, reads only
     `instance_keys` public keys, no keyring) writes
     `bekup-instance-public-keys v1` (one `key <id> <base64> created …
     [retired …]` line per key, fingerprints as comments); `--pubkey`
     takes that file (`config.ReadPublicFile` rules) or a bare base64
     key, repeatable; a key line whose ID does not match its key is
     rejected. Verification without `--pubkey` is refused.
  3. **verify** parses `manifest.json` strictly, checks the signature,
     streams every listed artifact (size + SHA-256, at most size+1 bytes)
     from the run directory through an `os.Root` (`--artifact` checks only
     the given files, the rest is reported skipped) and prints a report
     (engine, versions, databases, sizes, encryption mode + passphrase /
     recipient version and recipient fingerprints, stats summary,
     warnings) or JSON; non-zero exit on any failure.
  4. **decrypt** requires either `--manifest` + `--pubkey` (signature,
     then a first streaming pass over the artifact's size + SHA-256 before
     any decryption, then the dump's raw size + SHA-256 while decrypting,
     output bounded by the signed raw size) or an explicit `--no-verify`
     (loud warning; an unverified manifest, if given, is still used for
     the digests and the passphrase version). Output goes to a 0600
     temporary file next to `--out` and is committed only after every
     check by a hard link (never replaces an existing file; `--force`
     renames); `--out -` streams to stdout (refused on a terminal).
     Secrets: `--passphrase-file` (a passphrase or a
     `bekup-job-passphrases v1` export; the version comes from the
     manifest or `--passphrase-version`, the export's job must match),
     `--identity-file` (X25519 only), `--stdin-passphrase` (terminal
     prompt without echo or the first stdin line, ≤ 64 KiB); files via
     `config.ReadSecretFile`; hidden `--passphrase`/`--password`/
     `--identity`/`--key` flags fail with guidance; key bytes are zeroed.
     Bounds: scrypt work factor 2^18 by default (`--max-work-factor` ≤
     2^20), zstd window 64 MiB (`pipeline.OpenArtifact`).
  5. **reindex** describes a destination in an offline TOML file (`type`
     + one of `[disk]`/`[s3]`/`[sftp]`, `[netguard]` allowlist; strict keys;
     credentials only as `*_file`; SFTP host key pin mandatory) and opens it
     in no-delete mode through the normal backends and the SSRF guard. It
     lists `{slug}/{timestamp}/…` (≤ 2 M objects), verifies each manifest,
     **rejects a manifest whose slug/timestamp differ from its directory**,
     and checks every artifact (or only the manifests with
     `--skip-artifacts`).
  6. **Import** (`--import --job-slug --destination-id`, bekup config,
     connects as `bekup_app`): verification uses the **database's**
     instance keys only; only runs that verify completely (signature,
     location, every artifact) are imported, because restores and
     downloads bind to exactly the signed entries (D-150, D-162) and a
     partly broken run could never be used. The job (not deleted) must
     exist with the manifest's job ID (a job recreated after a total loss has a new ID and
     is refused). One transaction per run inserts the run with the
     manifest's run ID (status success, trigger `manual`, storage timestamp
     and finish time from the manifest, manifest warnings as run warnings,
     an `imported` notice), one verified run item and one verified,
     signed artifact per entry and the manifest object (its size and
     SHA-256), plus the audit event `cli.run.imported` (fail closed).
     Existing run IDs or object keys are refused. New sqlc queries in
     `internal/db/queries/reindex.sql`; no migration. Retention applies
     to imported runs like to any other run (documented).
  7. Manifest-derived text is escaped for terminals (`SafeText`).
- **Alternatives considered:** letting decrypt run without a manifest by
  default (rejected: unauthenticated content; D-001); taking public keys
  only as fingerprints (rejected: ed25519 verification needs the key; the
  fingerprint is the escrow cross-check); importing partly broken runs as
  `partial` with unverified artifacts (rejected: the manifest binding of
  restores would refuse every artifact of such a run); importing with
  `--pubkey` keys that the DB does not know (rejected: restores verify with
  the DB keys); a new `imported` run trigger (rejected for now: needs a
  migration of the enum; the notice marks imported runs); adopting old
  runs into a recreated job (rejected: breaks the job-ID binding).
- **Consequences:** Backups stay verifiable and decryptable from the escrow
  alone. **Security impact:** secrets never come from argv; forged or
  replayed manifests are rejected or shown with their identity; tampered
  artifacts never produce output under `--out`; an import cannot plant
  unverified runs and is audited; destinations are only read. Residual:
  `--no-verify` output is unauthenticated (warned); operators must compare
  the key fingerprints and the run identity.
- **Related:** D-040, D-087, D-088, D-141, D-142, D-144, D-150, D-151,
  D-154, D-162; tasks 4.7, 4.1; SECURITY.md CL-2, CL-4, CL-5, CL-9, CL-10,
  CL-11; `internal/offline`, `cmd/bekup/offline_cmd.go`,
  `cmd/bekup/reindex_cmd.go`, `cmd/bekup/keys_cmd.go`,
  `internal/db/queries/reindex.sql`, `docs/recovery.md`,
  `docs/reference/artifact-format.md`, `docs/security/key-management.md`.

## D-165: MySQL/MariaDB restore: in-place overwrite by DROP + CREATE in the client session, hardened client, trailer check

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 4.3; awaiting the user's confirmation)
- **Context:** Task 4.3 restores MySQL and MariaDB dumps (plain SQL from
  `mysqldump` / `mariadb-dump` without `--databases`, so no `CREATE
  DATABASE` or `USE`). The plan asks for an in-place overwrite after an
  explicit warning (MySQL has no `RENAME DATABASE`, so no temp + swap)
  and for new-database creation. Open points: what "in place" means for
  objects that are not in the dump (a dump only drops and recreates the
  tables it contains), how the database is dropped without a runner RPC
  that drops non-temporary databases (`DropDatabase` only accepts bekup
  temporary names, D-123), what happens to a truncated dump that ends at
  a statement boundary, and engine mixing.
- **Decision:**
  1. **Overwrite = drop and recreate the database, then restore**, in one
     client session: serve's restorer (`internal/restore/mysql.go`)
     prefixes the restore stream with fixed SQL built from the validated
     name (`engine.QuoteMySQLIdent`; validated names contain no quotes,
     backticks or backslashes): read `DEFAULT_CHARACTER_SET_NAME` /
     `DEFAULT_COLLATION_NAME` of `DATABASE()` from
     `information_schema.SCHEMATA` (a catalog view, never a user table,
     D-152/D-153), `PREPARE` the `CREATE DATABASE` with those defaults,
     `DROP DATABASE`, `EXECUTE`, `USE`; then the dump. A missing database
     or an unreadable catalog fails before the `DROP`. Objects that are
     not in the backup (tables, views, routines, triggers, events) are
     removed; grants on the database survive (`mysql.db`). The client
     stops at the first error, so a failure after the `DROP` leaves the
     database empty or partially restored: the warning
     (`restore.MySQLOverwriteWarning`, shown with the type-to-confirm of
     task 4.5) and the restore log say so. The runner still requires the
     database to exist (D-033 list check) and runs the client with its
     own hardened argv; the runner API is unchanged.
  2. **New database:** `CreateDatabase` (the runner requires the name to
     be absent), then `Restore`. A failed restore leaves the new database
     behind (no API drops a non-temporary database); the log says so and
     the result carries the warning `partial_new_database`.
  3. **Hardened client** (built by the runner, D-123): MySQL
     `--system-command=OFF --commands=OFF --local-infile=0`, MariaDB
     `--sandbox --local-infile=0`, both `--default-character-set=utf8mb4
     --database=<db>`, the bekup option file first, no `--force`. Tests
     inspect the argv through the runner's spec builder for every
     allowlisted image, and hostile dumps (`\!`, `system`, `source`,
     `\.`, `LOAD DATA LOCAL INFILE` against a server with `local_infile`
     on, `tee`, `\T`, `pager`) are refused or have no effect on MySQL
     8.4/9.7 and MariaDB 11.4/11.8.
  4. **Trailer and size check:** the stream goes through the shared
     `RunRestoreStream` (exactly `total_bytes`, the last chunk held back
     until the staged source ended cleanly; its end re-checks the
     digests, D-162); the MySQL/MariaDB restorer additionally requires
     the dump to end with the `-- Dump completed` trailer
     (`engine.DumpTrailerOK`), otherwise the stream is canceled so the
     runner kills the client before it reads the end of the dump, and
     the restore fails (`ErrRestoreIncomplete`). A client exit code other
     than 0 is `ErrRestoreFailed`; client stderr lines (bounded, redacted
     by the runner) go to the restore log, server text never into
     errors.
  5. **Refusals before any RPC:** `KindGlobals` (PostgreSQL only;
     `engine.ErrUnsupported`), a source, target-connection or restorer
     engine mismatch (MySQL and MariaDB are strictly separate,
     `engine.ErrWrongEngine`), system databases and invalid names. The
     restore client image is chosen by the runner
     (`SelectRestoreClient`: max(dump client, server); the newest client
     with the warning `client_older_than_server` when none is new
     enough).
  6. The restorers register themselves (`init`, `RegisterEngine` for
     `mysql` and `mariadb`); `restore.MySQLFamilyRestorer` returns one
     directly (tests).
- **Alternatives considered:** Restore over the existing database without
  dropping it (extra tables, views and routines would survive: not a
  faithful restore). Dropping every object individually from catalog
  queries (keeps the database itself but needs dynamic SQL loops per
  object type and still misses some objects). A new runner RPC or a
  `replace` flag on `RestoreHeader` (typed, but separate RPCs would leave
  a window without the database and the runner's existence check, and
  the change would touch the proto concurrently with task 4.2; the
  stream prefix grants nothing beyond the dump itself, which already is
  arbitrary SQL run with the target's credentials). `DROP DATABASE IF
  EXISTS` (the database must exist; a missing one should fail before
  anything is dropped). Ignoring the trailer (a dump cut at a statement
  boundary would "succeed").
- **Consequences:** An overwrite needs the `DROP` and `CREATE` privileges
  on the target database; concurrent sessions holding metadata locks make
  the `DROP` wait. There is no rollback for a failed in-place restore
  (documented in the warning and in `docs/guides/restore.md`). A hostile
  dump still runs arbitrary SQL with the target user's rights (for
  example `USE other_db`): use a restore user limited to the target
  databases. Security impact: no new trust boundary; SECURITY.md rows
  for the MySQL/MariaDB restore path added.
- **Related:** D-033, D-036, D-123, D-134, D-152, D-153, D-162; tasks 4.2,
  4.3, 4.5; `internal/restore/mysql.go`, `internal/restore/mysqlit`,
  `internal/engine/mysql.go`, `docs/guides/restore.md`, `SECURITY.md`.

## D-166: Restore worker and PostgreSQL restore: temp database + swap, DropTempDatabase RPC, restore logs and events

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 4.2; awaiting the user's confirmation)
- **Context:** Task 4.2 builds the shared restore worker (River kind
  `restore`, D-145) and the PostgreSQL restore path. The plan fixes
  `pg_restore --exit-on-error` into `<db>__bekup_restore_<id>` and a rename
  swap for overwrites, globals via the hardened psql as a separate
  admin-only action and the restore client max(dump, server). Open were:
  how failed/canceled restores remove their temporary database and the
  replaced database after a swap (the runner had no drop RPC), how engine
  restorers plug in (task 4.3 runs concurrently), where restore logs and
  live events go, the order of slot / lock / staging, `--no-owner`'s
  default, and what a failed new-database restore leaves behind.
- **Decision:**
  1. **Engine interface** (`internal/restore/engine.go`):
     `EngineRestorer.Restore(ctx, EngineRequest, src, LogSink)` with a
     registry `RegisterEngine(engine, r)` (init-time; PostgreSQL in
     `postgres.go`, MySQL/MariaDB in `mysql.go`). `EngineRequest` carries the
     restore id, the runner client, a `TargetBuilder` (fresh unsealed target
     per RPC, released right after it), target engine and database, mode,
     kind (`database` | `globals`), source engine and version key from the
     **signed manifest**, dump format, exact size and `NoOwner`. Shared
     runner helpers: `RunRestoreStream` (header, 256 KiB chunks, **the last
     chunk held back until the staged stream ended cleanly**, so a failed
     final digest check never completes the tool's input; exit code and
     byte count checked), `CreateDatabase`, `RenameSwap`,
     `DropTempDatabase`. `TempID` = the first 48 bits of the restore id in
     hex.
  2. **New runner RPC `DropTempDatabase(target, database, temp_id, role)`**
     (class run): the runner derives the name itself
     (`<db>__bekup_restore_<id>` or `<db>__bekup_old_<id>`, roles
     RESTORE/OLD) and the engine builder refuses anything that is not a
     bekup temporary name (`DROP DATABASE IF EXISTS`, `WITH (FORCE)` on
     PostgreSQL 13+). No API can drop another database.
  3. **PostgreSQL restorer:** new database = `CREATE DATABASE … TEMPLATE
     template0` + `pg_restore --exit-on-error`; a failure leaves the new,
     partial database (it never held other data; the log says to drop it).
     Overwrite = create the temporary database, restore into it, re-check
     the context, `RenameSwapDatabase`, then drop the old database (failure
     → warning `old_database_kept`). Until the swap the target is never
     touched; on any failure, cancel or timeout the temporary database is
     dropped on a fresh bounded context (`temp_database_kept` when that
     fails). Globals: kind globals only with a SQL dump and no database,
     `psql --no-psqlrc -v ON_ERROR_STOP=1` under the runner's `\restrict`
     key against the maintenance database. pg_dumpall writes `CREATE ROLE
     <name>;` for every role and relies on it failing harmlessly for
     existing roles (always the connected one), which `ON_ERROR_STOP`
     turns into a failed restore: the restorer reads the globals dump in
     full (≤ 32 MiB, so its final digest check runs before any byte
     reaches the runner) and rewrites exactly those lines (strict
     identifier match, no `$` in the name) into `DO $bekup_role$BEGIN
     CREATE ROLE <name>; EXCEPTION WHEN duplicate_object THEN NULL;
     END$bekup_role$;`; every other error (e.g. an existing tablespace)
     still stops psql. **`--no-owner` is on by default** (`restores.no_owner`,
     fixed per restore; `CreateInput.KeepOwners` turns it off): restored
     objects belong to the target connection's role, so a backup cannot
     hand objects to roles that happen to exist on the target.
  4. **Worker order:** load the restore (only `queued` is worked: a restore
     runs at most once); take a run slot for the **target** connection
     (shared pool, D-147/D-156; snooze while busy, which River does not
     count as an attempt); re-check the target with
     `LockRestoreTargetTx` (deleted / expired → `target_unavailable`);
     audit `restore.started` (fail closed); `Stage`; re-check that the
     target's engine equals the manifest's (`engine_mismatch`), the
     artifact kind matches the request and a restorer exists; move to
     `restoring`; restore; `Cleanup` always; finish with a sanitized code
     plus the log counts and warning codes; audit `restore.finished`;
     emit the D-161 hook (`restore.started|completed|failed`, a no-op
     `Notifier` until task 5.3). River cancel → `canceled` (River job
     canceled); queue timeout → `timeout`; lost slot → `slot_lost`;
     shutdown → `interrupted`. TLS pin / SSH host-key mismatches are
     recorded through `connections.RecordRunFailure`.
  5. **Restore logs:** table `restore_logs` (same shape and caps as
     `run_logs`, written through the run-log writer: sanitized, redacted
     with the target's credentials registered for the restore's duration
     plus URL/DSN credential redaction, truncated, capped, batched);
     `restores.log_lines`, `log_lines_dropped`, `warnings` (≤ 32 codes).
     `bekup_app` may only insert and read `restore_logs`.
  6. **Events:** channel `bekup_restore_events` (fixed LISTEN query),
     payload `restore:<id>` or `log:<id>`, published in the transaction of
     every status change (staging, verifying, restoring, terminal) and log
     batch; listeners re-load with the caller's rights.
  7. `Service.EnqueueTx` inserts the River job (restore id only) in the
     restore's insert transaction for task 4.5.
- **Alternatives considered:** a generic `DropDatabase` RPC taking a name
  (rejected: the runner would trust serve's name choice); keeping the old
  database after a swap for manual rollback (rejected: doubles disk use
  silently; a failed swap already leaves the target untouched); restoring a
  new database through temp + rename too (rejected: needs another rename
  RPC; a new database holds nothing to protect); `--no-owner` off by default
  (rejected: ownership by arbitrary existing roles is a privilege surprise);
  storing restore logs in `run_logs` (rejected: different parent table and
  access rules); staging before taking the slot (rejected: a snooze after
  staging would need a second staging).
- **Consequences:** **Security impact:** the runner gains one RPC that can
  only drop bekup-derived temporary names; an overwritten PostgreSQL
  database changes only at the atomic swap; a canceled or failed restore
  leaves no temporary database; restore logs never carry the target's
  credentials. A failed new-database restore leaves a partial database
  (documented); a globals restore can change attributes and passwords of
  existing roles that are in the dump (admin only, documented). One
  migration (00023), one River kind, two audit actions, one pubsub
  channel.
- **Related:** D-022, D-036, D-123, D-134, D-138, D-145, D-147, D-152,
  D-155, D-156, D-161, D-162, D-163, D-165; tasks 4.2, 4.3, 4.5, 4.6, 5.3;
  `internal/restore/{engine,worker,postgres,runner_calls,events,notify}.go`,
  `internal/runner/ops_admin.go`, `proto/runner/v1/runner.proto`,
  `internal/db/migrations/00023_restore_worker.sql`,
  `docs/guides/restore.md`, `SECURITY.md`.

## D-167: Restore API: target rules, fresh second factor, confirmations, request-time manifest check, cancel and the stuck-restore sweep

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 4.5; awaiting the user's confirmation)
- **Context:** Task 4.5 exposes restores over GraphQL. The plan fixes the
  target rules (admins/maintainers: any same-engine connection, existing
  or new database; owners: only databases their jobs back up or a new
  database on those connections; temporary connections admin only), the
  mandatory fresh passkey/TOTP check (≤ 5 min, enrollment first, D-057,
  D-116), type-to-confirm for overwrites and the target row lock
  (D-163). Open were: which engine the API trusts before the worker
  re-checks the signed manifest, how listing the target's databases fits
  the one-runner-call-per-request rule (D-135), whether owners may cancel,
  how globals are confirmed, what prevents two restores into one
  database, what the UI is told before confirming, and what happens to
  restores whose worker crashed (left open by 4.2).
- **Decision:**
  1. **API layer** `internal/restore.API` (resolvers only translate):
     every operation authorizes with the central policy, the artifact's
     job and the target loaded from the database. Order: syntax checks
     (mode, database name: the strict charset, not a bekup temporary
     name), artifact + `restore.targets_list` with its job (a missing
     artifact is NOT_FOUND for staff, FORBIDDEN for owners), target via
     `GetForRestore` (unknown: field error for staff, FORBIDDEN for
     owners), then `restore.artifact` with `Job` + `RestoreTarget`
     (owner target rule, temporary = admin only, then the fresh second
     factor), and only then the destination read.
  2. **Request-time manifest verification.** Before relying on the
     source engine the API verifies the run manifest on the artifact's
     destination (`artifactverify.VerifyManifest`, the same check as
     downloads/staging) and takes engine, version and kind from it. A
     failure is a possible tampering: refused (CONFLICT), alerted
     (`restore_artifact_verification_failed`, stage `request`) and cached
     with the success results for 5 minutes per artifact (one alert per
     period, no destination read per click). The engine must equal the
     target's (MySQL ≠ MariaDB; VALIDATION on `targetConnectionId`); a
     system database is refused; `keepOwners` is PostgreSQL only. The
     worker still re-checks everything against the staged, verified
     copy (D-162, D-166).
  3. **Fresh second factor:** `restoreArtifact` maps to `restore.artifact`
     (FreshAAL2) and `restoreGlobals` to `restore.globals` (admin,
     FreshAAL2): only a TOTP or user-verified passkey check within
     `security.stepup_max_age` (≤ 5 min) passes; password, recovery-code
     and UP-only passkey step-ups do not (D-116); accounts without a
     factor get REQUIRES_2FA_ENROLLMENT. Both are D-117 single-root
     fields (`restoreArtifact` may carry the age identity, both read a
     destination) and `restore.artifact`/`restore.globals` are credential
     operations.
  4. **Confirmations:** OVERWRITE needs `confirmTargetName` equal to
     `targetDatabase` byte for byte; a globals restore needs
     `confirmPhrase` = `restore globals into <connection name>` because it
     can change existing roles including passwords (D-166). Globals
     restores are recorded with mode `overwrite`.
  5. **Preview:** `restorePreview` (new operation `restore.preview`: the
     target rules of `restore.artifact` without freshness) returns the
     source, the target, the confirmation text and fixed warnings:
     `overwrite_in_place` (MySQL/MariaDB, `restore.MySQLOverwriteWarning`),
     `overwrite_swap` (PostgreSQL temp + swap, sessions terminated, room
     for two copies), `new_database_partial_on_failure`, `client_version`
     (the runner picks max(dump, server)), `cross_source_target` (the
     target is not the job's connection: dump content runs with the
     target role's privileges, D-068), `temporary_target`,
     `target_not_ready`, `insecure_transport`, `keep_owners`,
     `identity_required`, `globals_change_roles`. `restoreArtifact`
     audits the warning codes it was requested with.
  6. **Targets:** `restoreTargets(artifactId)` lists candidate
     connections without runner calls (owners: connections their
     non-deleted jobs back up; admins also unexpired temporary ones;
     globals artifacts admins only, PostgreSQL targets only).
     `restoreTargetDatabases(artifactId, targetConnectionId)` is a
     separate single-root query with one runner Probe (D-135), cached
     30 s per connection (the per-user runner limiter applies to fresh
     reads); owners see only the databases their jobs back up
     (`OwnerBackedUpDatabases`), bekup's `__bekup_restore_`/`__bekup_old_`
     databases are hidden, at most 2000 names.
  7. **One transaction:** `LockRestoreTargetTx` (share lock; deleted or
     expired target refused; engine/temporary re-compared), a
     transaction-scoped advisory lock on (connection, database|globals),
     refusal while another restore into the same database (or the same
     connection's globals) is queued or running (CONFLICT), `CreateTx`
     (artifact + key checks, identity sealed into the handoff),
     `EnqueueTx`, `restore.requested` (user actor; artifact, job, run,
     kind, source engine/version/database, target connection/database,
     temporary/original flags, mode, no_owner, warning codes,
     `identity_supplied`; never the identity) and the restore event.
  8. **Cancel and reads:** `cancelRestore` = new operation
     `restore.cancel`, staff only like `run.cancel` (owners can start
     restores of their jobs but not cancel them, D-106). In one
     transaction: lock the row, CONFLICT when finished, cancel the River
     job; when it was not running the restore is finished as canceled at
     once and its identity handoff deleted; audit
     `restore.cancel_requested`. `restores`/`restore`/`restoreLogs`/
     `restoreUpdated` use `restore.view` (owners: their jobs' restores;
     `requestedBy` staff only, like `Run.triggeredBy`).
     `restoreUpdated(restoreId)` re-loads and re-authorizes on every
     `bekup_restore_events` event (coalesced, ≤ 5 per second), and
     completes after delivering the terminal status.
  9. **Stuck-restore sweep** (River kind `restore_stuck_sweep`,
     maintenance queue, every 5 minutes): a non-terminal restore that
     started more than `workers.restore_timeout` + 15 minutes ago, or
     that is idle for 15 minutes while its River job is gone or
     finalized, is failed with code `abandoned` after a re-check under
     its row lock; its identity handoff, artifact hold and run slot are
     released, the local staging directory removed (other processes'
     scratch sweepers remove theirs) and `restore.abandoned` audited
     (system actor). Queued restores waiting for a slot are never
     touched.
- **Alternatives considered:** trusting the job's current connection
  engine (rejected: a job can move to another connection; the signed
  manifest is the authority); listing every target's databases in
  `restoreTargets` (rejected: N runner calls per request, against
  D-135); letting owners cancel (rejected for consistency with D-106);
  a fixed globals phrase (rejected: typing the connection name makes the
  target explicit); allowing concurrent restores into one database
  (rejected: two MySQL in-place restores or PostgreSQL swaps would
  interleave); failing stuck queued restores by age alone (rejected: a
  restore may legitimately wait long for a slot).
- **Consequences:** **Security impact:** no restore runs without a fresh
  phishing-resistant-or-TOTP check by the requesting user; owners cannot
  reach databases outside their jobs (server-side, DB-loaded); tampered
  manifests are refused before a restore is recorded; identities stay
  sealed and are never logged, audited or returned; crashed restores no
  longer hold artifacts, slots or identities. **Open item (D-068):** the
  runner's Probe does not yet report whether a connection's role is
  privileged (superuser / SUPER / FILE), so `RestoreTarget.PrivilegedRole`
  is never set and the "privileged restore role: admin only + explicit
  acknowledgement" rule is not enforced yet; the `cross_source_target`
  warning and the audit flag partly cover it. It needs a runner change
  (Probe field + engine queries) and should be scheduled before M4
  closes. Two new authz operations, three audit actions, one River kind,
  new queries (no migration).
- **Related:** tasks 4.1–4.6, 6.7; D-013, D-015, D-057, D-067, D-068,
  D-106, D-112, D-116, D-117, D-135, D-145, D-150, D-155, D-162, D-163,
  D-165, D-166; `internal/restore/{api,read,sweep}.go`,
  `internal/graph/schema/restore.graphqls`,
  `internal/graph/resolvers/restore.go`, `internal/authz/operations.go`,
  `docs/guides/restore.md`, `SECURITY.md`.

## D-168: Privileged restore targets: Probe privilege verdict, fail-closed unknown, admin + acknowledgement

- **Date:** 2026-10-28
- **Status:** proposed (amended by D-172: global data privileges and pg_read/write_all_data gate; by D-173: worker re-check)
- **Decided by:** agent (task 4.8; awaiting the user's confirmation)
- **Context:** D-068 (accepted) requires that `Probe` reports whether a
  connection's role is privileged and that restores into such
  connections need an admin and an explicit acknowledgement, because SQL
  inside a dump runs with the restore role's rights (`SECURITY.md` OI-2).
  D-167 left this open (`authz.RestoreTarget.PrivilegedRole` was never
  set). Open were: which privileges count, how roles (MySQL 8 / MariaDB)
  are handled, where the verdict comes from at request time, and what
  happens when it cannot be determined.
- **Decision:**
  1. **Runner `Probe`** returns `privileges { status, reasons }`
     (`PrivilegeStatus` NOT_PRIVILEGED / PRIVILEGED / UNKNOWN; reasons
     are stable codes, ≤ 32 × 64 bytes, never server-provided names
     other than privilege and predefined role names). It is computed in
     the runner's hardened Go-driver session (D-152: pinned
     `search_path`/`row_security`, schema-qualified SQL, enforced by the
     qualification test; D-153: no user-table reads). A detection error
     does not fail the Probe: the verdict is UNKNOWN (`detection_failed`).
  2. **PostgreSQL privileged set:** the role's `rolsuper`,
     `rolcreaterole`, `rolreplication`, `rolbypassrls`; membership
     (direct or indirect, with or without INHERIT/SET, via
     `pg_has_role(..., 'MEMBER')`, reachable with `SET ROLE`) in a role
     with one of these attributes (`member_of_role_with:<attr>`) or in
     `pg_execute_server_program`, `pg_read_server_files`,
     `pg_write_server_files` (host access), `pg_signal_backend` (other
     sessions), `pg_read_all_stats` (every session's statements) and
     `pg_create_subscription` (outbound connections from the server)
     (`member_of:<role>`). Owning databases, `CREATEDB`,
     `pg_read_all_data` and `pg_write_all_data` are **not** privileged:
     they act on the session's database only, which the restore replaces
     anyway.
  3. **MySQL/MariaDB:** `SHOW GRANTS` (the current account; no
     `mysql.*` reads), parsed by a quote- and parenthesis-aware
     tokenizer (fuzz-tested). Global (`*.*`) privileges outside a small
     allowlist of data/schema privileges (SELECT, INSERT, UPDATE, DELETE,
     CREATE, DROP, REFERENCES, INDEX, ALTER, CREATE TEMPORARY TABLES,
     LOCK TABLES, EXECUTE, CREATE/SHOW VIEW, CREATE/ALTER ROUTINE, EVENT,
     TRIGGER, SHOW DATABASES, REPLICATION CLIENT, USAGE and a few
     harmless MariaDB/MySQL 8 monitor/flush privileges) are privileged
     (`global:<NAME>`), so SUPER, FILE, PROCESS, RELOAD, SHUTDOWN,
     CREATE USER, ALL PRIVILEGES, SET_USER_ID, SYSTEM_USER,
     SYSTEM_VARIABLES_ADMIN, MariaDB's split SUPER privileges and every
     dynamic privilege bekup does not know yet count; global `WITH GRANT
     OPTION` (`global:GRANT OPTION`); `PROXY` grants (`proxy`); any
     non-read privilege on a schema pattern matching `mysql` or on its
     tables (`system_schema_write`; LIKE wildcards honored). Role grants
     (MySQL 8 `GRANT role TO`, MariaDB roles and `SET DEFAULT ROLE`) are
     a known limitation: their privileges are not listed and `SET ROLE`
     can activate them, so the verdict is **UNKNOWN** (`roles_granted`);
     so is any line the parser does not understand (`unparsed_grant`).
  4. **Fail closed:** serve treats UNKNOWN, a missing verdict (older
     runner: `no_verdict`) and an inconsistent one like PRIVILEGED. A
     target connection that is not ready is UNKNOWN (`target_not_ready`)
     without a runner call. Reasons from the runner are re-validated
     (charset, length, count).
  5. **Restore API:** `restorePreview` and `restoreTargetDatabases` show
     `targetPrivileges { status reasons acknowledgementRequired }` (the
     preview also `crossSource`) and the `privileged_target` warning;
     they use the 30-second probe cache, and a failing preview probe
     shows UNKNOWN (`probe_failed`; pin/host-key mismatches and
     cancellations still fail). `restorePreview` is now a D-117
     single-root field (it makes a runner call, D-135).
     `restoreArtifact`/`restoreGlobals` **probe afresh** (no cache); for
     a gated target the target rules run again with
     `RestoreTarget.PrivilegedRole` (admins only: FORBIDDEN for
     maintainers and owners) and then require the input flag
     `acknowledgePrivilegedTarget: true` (VALIDATION otherwise); globals
     (admin only already) need the flag likewise. A request whose probe
     fails returns the error.
  6. **Audit and alert:** `restore.requested` records
     `target_privileges`, `target_privilege_reasons`,
     `privileged_target_acknowledged` and `cross_source` (besides
     `original_target` and the warning codes); an acknowledged restore
     into a gated target raises the security alert
     `restore_privileged_target` (logged until task 5.5 delivers it).
     The cross-source flag of D-068 stays the `cross_source_target`
     warning plus these audit fields.
  7. The connection test (`testConnection`) also returns the verdict
     (`ConnectionTestResult.privileges`), the basis for the connection
     badge in the UI.
- **Alternatives considered:** Store the verdict on the connection row
  (stale; needs a migration and a refresh policy). Treat UNKNOWN as not
  privileged (fails open on MySQL roles). Expand MySQL roles with `SHOW
  GRANTS ... USING` (MySQL-only syntax, role names from the server in
  SQL). Read `information_schema.USER_PRIVILEGES` (no role or proxy
  information, GRANTEE matching). Count `pg_read_all_data` /
  `pg_write_all_data` / global MySQL data privileges as privileged (they
  are what most restore roles need; cross-database reads on MySQL are
  documented instead). Re-check in the runner when the restore starts
  (needs the acknowledgement stored with the restore and a new request
  field; left as a follow-up).
- **Consequences:** **Security impact:** maintainers and owners can no
  longer run dump SQL with superuser-like rights; an admin must see the
  reasons and acknowledge, and the acknowledgement is audited and
  alerted. Fail-closed UNKNOWN means MySQL/MariaDB connections whose
  account has roles always need an admin. Residual risks: the verdict is
  read at request time, not when the queued restore starts (a role
  elevated in between is not caught; operators control target grants);
  MySQL global data privileges let a dump read other schemas (a
  dedicated restore account limited to the restored schemas is
  recommended in `docs/security/db-roles.md`). Every restore request now
  makes one runner Probe (per-user runner limiter applies). No
  migration; one proto field + enum (unreleased API), no new authz
  operation (the existing `PrivilegedRole` target rule).
- **Related:** D-013, D-015, D-033, D-068, D-117, D-134, D-135, D-152,
  D-153, D-167; task 4.8; `internal/runner/dbprobe/privileges.go`,
  `internal/runner/ops_probe.go`, `internal/connections/privileges.go`,
  `internal/restore/api.go`, `internal/graph/schema/restore.graphqls`,
  `docs/guides/restore.md`, `docs/security/db-roles.md`, `SECURITY.md`
  (OI-2).

## D-169: Scheduled test restores: scratch target, internal restores, runner-derived scratch databases, comparison rules and results

- **Date:** 2026-10-28
- **Status:** proposed (amended by D-170)
- **Decided by:** agent (task 4.6; awaiting the user's confirmation)
- **Context:** Task 4.6 restores backups regularly into a scratch database
  and compares the result with the source statistics of 3.10 (D-141,
  D-151, D-153), with results stored, shown and alerted, the scratch
  database always dropped and the D-161 events emitted. Open were: where
  the settings live and who may change them, which target a test restore
  writes to and how the scratch database is named and dropped without a
  runner API that can drop arbitrary databases, how the restore itself
  runs (the restore worker needs a `restores` row and normally a fresh
  second factor), how the scratch statistics are read (D-151 dropped the
  planned `CaptureStats` RPC because the dump stats live in `Dump`), the
  exact comparison rules, scheduling, and what happens on crashes.
- **Decision:**
  1. **Settings on the job** (migration 00024): `jobs.test_restore_enabled`,
     `test_restore_schedule` (a D-143 schedule; default weekly, Sunday 04:00
     UTC; MANUAL = only on demand), `test_restore_connection_id` (the scratch
     target, required while enabled) and `test_restore_next_at`. The target
     must be a permanent, non-deleted connection with the job's engine
     (MySQL ≠ MariaDB); it may be the job's own connection. It blocks the
     connection's deletion like the job's connection (`JobsUsingConnection`).
     `setJobTestRestore` = `job.test_restore_edit`, now **staff with a fresh
     second factor (FreshAAL2)**: designating a target makes bekup copy the
     job's data there, which is what `restore.artifact` guards. Audited as
     `job.test_restore_updated`. Owners see the settings (Job.testRestore).
  2. **Runner-derived scratch databases.** The scratch database is
     `bekup_test_<scratch id>` with the scratch id = the first 48 bits of
     the internal restore's id in hex (`engine.ScratchDatabaseName`,
     `^bekup_test_[a-z0-9]{12}$`). Two **new runner RPCs** (class run, in
     their own files; Probe is untouched): `ScratchStats(target, temp_id,
     stats)` and `DropScratchDatabase(target, temp_id)`. Both take only the
     id; the runner derives the name, the engine's `DropDatabase` builder
     accepts only bekup-derived names (temporary, old or scratch), so
     neither RPC can inspect or drop another database. `bekup_test_*` is a
     reserved namespace: the restore API refuses such target names and
     hides them from `restoreTargetDatabases`.
  3. **Scratch statistics** reuse the Dump capture code unchanged
     (`captureStats`): the same pinned, hostile-safe Go-driver session
     (D-152), PostgreSQL `deep` exact counts with server-side limits,
     MySQL/MariaDB estimates only (D-153; the runner never counts there).
     The statistics transaction ends at once (no dump follows).
  4. **Internal restores through the restore worker.** For every
     successful database item (never the globals) of the job's newest
     `success` run with a present, verified, signed artifact (first
     destination by position), one transaction records an internal restore
     (`Service.CreateInternalTx`: mode `new_db`, no requester, `--no-owner`,
     the scratch name derived from its id, the test restore's River job as
     `river_job_id`) and the `test_restore_items` row; the restore then runs
     in the test-restore worker's process through `Worker.RunInternal`:
     target re-check, `restore.started`/`restore.finished` (system actor,
     `test_restore_id` in the details), staging, verification against the
     signed manifest, the engine restorer; no `restore.*` notifications. No
     second-factor check: it is system work into a new, generated database
     on a target an admin/maintainer designated with a fresh second factor.
     Recipient-encrypted backups cannot be tested (bekup holds no identity:
     `identity_required`).
  5. **Always dropped.** After the comparison, on a failed or canceled
     restore, on a stats error and on a panic, the worker calls
     `DropScratchDatabase` on a fresh bounded context. A failed drop is
     audited (`test_restore.scratch_kept`), alerted and retried by the
     schedule worker (`scratch_dropped` per item).
  6. **Comparison** (`restore.CompareStats`, canonical D-151 objects on
     both sides): object counts exactly (fail); every source table must
     exist and no other table may appear (fail), unless a side's table list
     was truncated (counts only + warning); `tables_total` exactly;
     PostgreSQL counts of a `snapshot` source that are `exact` must equal
     the scratch database's exact count (fail); every other count
     (estimates, MySQL/MariaDB, a PostgreSQL source without the dump's
     snapshot) differing is a **warning with the delta**, never a failure;
     `unknown` counts, missing or unreadable source stats and source
     completeness warnings are warnings. At most 200 findings (the total is
     kept), names as recorded (percent-encoded), never data values; the
     stored diff is ≤ 200 KiB.
  7. **Verdicts and results** (`test_restores`, `test_restore_items`;
     `bekup_app` INSERT/SELECT and progress-column UPDATE only, guard
     triggers keep finished rows final, at most one queued/running test
     restore per job by a partial unique index): per database
     passed/warnings/failed/error/canceled; the test restore is canceled
     when stopped, failed when any database failed, error when any could
     not be restored or checked, else warnings or passed. Audited
     (`test_restore.requested`, user or system actor; `test_restore.finished`),
     emitted through a `TestNotifier` hook (`test_restore.passed|warnings|
     failed`, D-161; error/canceled as failed) until task 5.3, and failed/
     error verdicts raise the `run.test_restore_failed` alert (logged at
     ERROR until 5.5).
  8. **Scheduling and robustness.** River kind `test_restore` (restore
     queue, never retried, IDs only) holds one run slot on the target for
     the whole test restore (shared pool, snooze while busy). River kind
     `test_restore_schedule` (maintenance queue, every minute, unique)
     enqueues due test restores one job per transaction (a due occurrence
     while one is active is skipped; missed occurrences collapse; a job
     whose enqueue fails still advances), fails test restores no worker will
     finish (`abandoned`, like D-167's sweep) and retries leftover drops.
     The worker re-checks before starting that the job still designates the
     queued target (`target_changed`) and the engines match.
  9. **API:** `runTestRestoreNow(jobId)` (`test_restore.run`, staff),
     `testRestores(jobId, first, after)` and `testRestore(id)`
     (`test_restore.view`, staff and the job's owners; unknown ids are
     FORBIDDEN for owners), all authorized with the job from the database.
- **Alternatives considered:** a separate settings table (the plan and the
  job editor treat it as a job setting); enqueuing ordinary `restore` jobs
  and waiting for them (two River jobs to coordinate, a restore could
  outlive its test); extending `DropTempDatabase` with a scratch role (the
  4.8 work runs in parallel; new RPCs keep the existing ones unchanged);
  passing the scratch name in the request (the runner would trust serve's
  name); keeping the scratch database on failure for inspection (contrary
  to "always dropped"; the diff and restore log explain the failure);
  failing on MySQL/MariaDB row-count differences (estimates, D-153);
  `job.test_restore_edit` without a fresh second factor (a maintainer could
  copy a job's data to any same-engine connection without the restore
  checks).
- **Consequences:** **Security impact:** the runner gains two RPCs that
  can only read the statistics of, or drop, `bekup_test_<12 hex>` databases
  it derives itself; restored backup content is only ever inspected through
  the D-152 pinned session (no view or foreign table is read, no
  search_path shadowing); designating a scratch target needs a fresh
  phishing-resistant-or-TOTP check and is audited; test restores never
  overwrite anything (the scratch database must not exist). Costs: one
  restore plus `count(*)` per table (PostgreSQL) per tested database, on
  the scratch target, bounded by the restore timeout and the stats budget;
  scratch space on the target for one database at a time. Known false
  positive: a source database without the `public` schema restored into a
  database created from `template0` (which has it) fails the schema count.
  One migration, two River kinds, two authz operations (plus the changed
  rule), five audit actions, two runner RPCs.
- **Related:** D-022, D-123, D-141, D-143, D-145, D-147, D-151, D-152,
  D-153, D-155, D-156, D-161, D-162, D-166, D-167, D-168; tasks 3.10, 4.6,
  5.3, 5.5, 6.7; `internal/restore/testrestore*.go`,
  `internal/runner/ops_scratch.go`, `internal/runner/validate/scratch.go`,
  `internal/engine/names.go`, `internal/jobs/test_restore.go`,
  `internal/db/migrations/00024_test_restores.sql`,
  `internal/graph/schema/testrestore.graphqls`, `docs/guides/restore.md`,
  `docs/guides/jobs-and-schedules.md`, `SECURITY.md`.

## D-170: Test restores: privileged scratch targets, hardened scratch databases and the implicit public schema (M4 review)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (M4 review fixes; awaiting the user's confirmation)
- **Context:** The M4 reviews found that D-169 let a maintainer designate a
  scratch connection whose role is privileged, so that every scheduled test
  restore ran dump SQL with superuser-like rights without the D-068 / D-168
  gate (admin + acknowledgement) that `restoreArtifact` enforces; that the
  scratch database (owned by the possibly privileged restore role after
  `--no-owner`) could be abused through `SECURITY DEFINER` functions or by
  any role with `CONNECT` while it existed; that a PostgreSQL source
  without the `public` schema always failed (the documented false positive
  of D-169); that the worker's `FOR SHARE` connection reads ran outside a
  transaction (no lock); and that the worker's statistics limits and
  notifier were not wired from the app.
- **Decision:** (amends D-169 items 1, 3, 5, 6 and 8)
  1. **Designation probes the target.** `setJobTestRestore` with a target
     makes one runner `Probe` (`connections.Service.ProbeTarget`, permanent
     connections only, per-user runner limiter) before the transaction. A
     not-ready connection, and a missing prober, are UNKNOWN; other probe
     errors (runner down, pin/host-key mismatch, rate limit) are returned.
     A privileged or unknown verdict needs role **admin**
     (`jobs.ErrPrivilegedTargetAdminOnly` → FORBIDDEN otherwise) and the
     input `acknowledgePrivilegedTarget: true` (VALIDATION otherwise); it
     applies to every settings change that keeps or sets such a target.
     Removing the target needs no probe.
  2. **Stored with the job** (migration 00025): `jobs.test_restore_target_
     privileges`, `test_restore_privilege_reasons`,
     `test_restore_privileged_ack_by`, `test_restore_privileged_ack_at`,
     always rewritten together with the target; CHECKs keep the reasons
     bounded and allow an acknowledgement only for a designated, gated
     verdict. `job.test_restore_updated` records `target_privileges`,
     `target_privilege_reasons` and `privileged_target_acknowledged`; an
     acknowledged designation raises the security alert
     `test_restore_privileged_target`. `Job.testRestore` shows
     `targetPrivileges` and `privilegedTargetAcknowledgedAt`.
  3. **Re-check before every run.** `TestRestoreWorker.execute` probes the
     target afresh (runner `Probe`, credentials registered for redaction,
     pin/host-key mismatches recorded as security events) before it picks
     the run. It refuses with `privileged_target` (status error, nothing
     created on the target) when the probe fails, or when the role is
     gated and the stored admin acknowledgement does not cover it: an
     acknowledgement covers a verdict only if every current reason is among
     the acknowledged reasons, so a role that gained privileges (or a
     target designated before D-170, or as not privileged) needs a new
     admin designation. `test_restore.finished` records the verdict and
     whether an acknowledgement covered it.
  4. **Hardened scratch databases.** The PostgreSQL restorer (restore
     core, M4 review) creates new databases, scratch included, without
     `PUBLIC` `CONNECT`/`TEMPORARY` and calls `NeutralizeRestored` (kind
     scratch) after `pg_restore` and before the restore reports success;
     a failure fails the internal restore (`neutralize_failed`), the
     scratch database is dropped uninspected. The test-restore worker
     therefore never reads statistics of an unhardened copy and does not
     neutralize a second time.
  5. **Implicit `public` schema.** `CompareStats` treats exactly one extra
     schema in a PostgreSQL restore as equal when neither side lists a
     table in `public` and neither table list was truncated (template0
     always has `public`, `pg_dump` never drops it); every other object
     count still has to match.
  6. **Plain reads.** The worker reads the source and target connections
     with `GetTestRestoreConnection` (no `FOR SHARE` outside a
     transaction); the internal restores re-check the target under a share
     lock themselves.
  7. **Wiring.** The app passes the same statistics limits (`statsLimits`,
     today the runner defaults) to the backup statistics and the
     test-restore worker, and the test-restore notifier explicitly (the
     no-op until task 5.3).
- **Alternatives considered:** Requiring admin for every scratch target
  (the plan lets maintainers designate unprivileged targets). Re-checking
  only the status and not the reasons (a role that becomes superuser after
  an acknowledgement for `createrole` would keep running). Treating a
  failing run-time probe as UNKNOWN and letting a stored acknowledgement
  cover it (a broken check would silently allow runs). A separate
  neutralize step in the test-restore worker (duplicates the restorer's
  step and leaves a window before it). Asking the runner whether `public`
  is empty (a new stats field; the count-based rule is exact enough given
  that all other counts must match).
- **Consequences:** **Security impact:** test restores can no longer run
  dump SQL with a privileged role unless an admin acknowledged exactly
  those privileges, audited and alerted; a role elevated later stops test
  restores (fail closed) until an admin designates it again; restored
  scratch copies are not reachable by other roles and contain no
  `SECURITY DEFINER` functions while they exist. Costs: one runner Probe
  per designation and per test restore. Residual: an acknowledged
  privileged role still runs the dumps' SQL with its rights (RN-16);
  schemas are compared by count, so an empty schema missing alongside an
  absent `public` is not detected. One migration, no new authz operation
  (the admin rule is a service check like D-168's target rule), one alert
  kind, one test-restore error code.
- **Related:** D-068, D-141, D-151, D-152, D-153, D-168, D-169; task 4.6;
  `internal/jobs/test_restore.go`, `internal/restore/testrestore.go`,
  `internal/restore/testrestore_compare.go`,
  `internal/db/migrations/00025_test_restore_privileges.sql`,
  `internal/graph/schema/testrestore.graphqls`, `internal/app/restore.go`,
  `docs/guides/restore.md`, `docs/guides/jobs-and-schedules.md`,
  `SECURITY.md` (WK-23, RN-17).

## D-171: Neutralize restored PostgreSQL databases: SECURITY DEFINER to INVOKER, no PUBLIC access, warn

- **Date:** 2026-10-28
- **Status:** accepted
- **Decided by:** user (M4 security review, finding H1: "neutralize + warn")
- **Context:** A PostgreSQL restore runs `pg_restore --no-owner` by default
  (D-166), so every restored object belongs to the target connection's
  role. A `SECURITY DEFINER` function in the dump (for example `esc()`
  doing `ALTER ROLE CURRENT_USER PASSWORD 'pwned'`) then runs with the
  restore role's rights for every role that can connect and call it: a
  low-privileged database user takes over the restore role (confirmed by
  the reviewer). Before the fix, `PUBLIC` also had `CONNECT` and
  `TEMPORARY` on every database bekup created.
- **Decision:**
  1. **Neutralize before the swap or the success.** After `pg_restore`
     succeeded and before an overwrite is swapped in (or a new-database or
     test restore is reported as restored), serve calls the new runner RPC
     `NeutralizeRestoredDatabase`. In its pinned Go-driver session (D-152:
     schema-qualified SQL, enforced by the qualification test) the runner,
     in one transaction, runs `ALTER FUNCTION|PROCEDURE <regprocedure>
     SECURITY INVOKER` for **every** routine with `prosecdef` (whatever
     its owner; only built-ins of the bootstrap superuser in `pg_catalog`
     / `information_schema` are skipped), each through the extended
     protocol (exactly one statement; the signature is the server's own
     `regprocedure` rendering under the pinned search_path), then
     `REVOKE CONNECT, TEMPORARY ON DATABASE ... FROM PUBLIC`, re-checks
     that no such routine is left and that `PUBLIC` has neither privilege,
     and commits. It returns the count and at most 50 signatures (each
     ≤ 256 bytes; serve re-bounds them and replaces control characters).
  2. **The runner picks the database.** `kind` RESTORE_STAGING derives
     `<db>__bekup_restore_<temp_id>`, SCRATCH derives
     `bekup_test_<temp_id>`, NEW_DATABASE takes a database that is neither
     a bekup temporary/scratch name nor a system or maintenance database;
     in every case the session's role must **own** the database
     (`datdba`), so the RPC cannot alter databases the restore role did
     not create. MySQL/MariaDB targets are refused (FailedPrecondition).
     The RPC is in the run class of the runner limiter.
  3. **PUBLIC never gets access to a database bekup creates:** the
     runner's `CreateDatabase` (PostgreSQL) runs `REVOKE CONNECT,
     TEMPORARY ... FROM PUBLIC` as a second psql command right after
     `CREATE DATABASE` (staging, new and scratch databases).
  4. **Race check.** After the commit, a client session of another
     non-superuser role on the restored database (one that raced in
     before the REVOKE) fails the call (the neutralization stays
     committed): the restore stops as `neutralize_failed`.
  5. **Fail closed.** Any failure aborts the restore before the swap: the
     staging database is dropped and the target stays untouched
     (`neutralize_failed`); a new database stays without PUBLIC access and
     the log says to drop it; a scratch database is dropped by the test
     restore.
  6. **Warn and audit.** When routines were switched, the restore records
     the warning `secdef_neutralized`, logs each signature (bounded) and
     audits `restore.secdef_neutralized` (database, count, signatures)
     **before** the swap (fail closed: an audit failure stops the
     restore). `restorePreview` shows `restore_hardening` for every
     PostgreSQL database restore. An admin may re-enable `SECURITY DEFINER`
     deliberately after reviewing a function and grants `CONNECT` to the
     roles that need the database (documented in the restore guide).
- **Alternatives considered:** Refuse dumps with `SECURITY DEFINER`
  functions (breaks legitimate schemas; the user chose neutralize + warn).
  Restore with owners kept (the owners are arbitrary roles of the target
  server; worse). Neutralize only functions owned by the restore role
  (with `keepOwners` a function can belong to another powerful role, so
  every one is switched). Do it in a psql container (no structured
  result, server text). Leave PUBLIC's `CONNECT` (keeps the attack open
  for every role on the server).
- **Consequences:** **Security impact:** dump SQL can no longer plant code
  that runs with the restore role's rights for other callers; other roles
  cannot even connect until an admin grants access. Behaviour change:
  restored databases have no `PUBLIC` `CONNECT`/`TEMPORARY` (applications
  that relied on PUBLIC need an explicit grant after the restore) and
  `SECURITY DEFINER` routines run as their callers until re-enabled.
  Residual: views keep running with their owner's (the restore role's)
  table privileges inside the restored database; a session that raced in
  between `CREATE DATABASE` and the `REVOKE` is detected only at the end;
  MySQL/MariaDB `SQL SECURITY DEFINER` routines and views are not
  neutralized (follow-up; they need explicit `EXECUTE`/`SELECT` grants).
  One more runner call per PostgreSQL database restore. No migration;
  one RPC + enum (unreleased API), one audit action, two error codes.
- **Related:** D-033, D-068, D-152, D-162, D-166, D-168, D-169, D-170;
  task 4.9; `proto/runner/v1/runner.proto`,
  `internal/runner/dbprobe/neutralize_pg.go`,
  `internal/runner/ops_neutralize.go`, `internal/runner/validate/neutralize.go`,
  `internal/engine/postgres.go`, `internal/restore/postgres.go`,
  `internal/restore/runner_calls.go`, `internal/restore/worker.go`,
  `docs/guides/restore.md`, `SECURITY.md` (RN-18, WK-8).

## D-172: Privileged restore targets: global data privileges and pg_read/write_all_data are gated (corrects D-168)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (M4 security review M2; awaiting the user's confirmation)
- **Context:** D-168 (item 2/3) classified MySQL/MariaDB global (`*.*`)
  data/DDL privileges and PostgreSQL `pg_read_all_data` /
  `pg_write_all_data` as not privileged, reasoning that they "act on the
  session's database only, which the restore replaces anyway". That is
  wrong: a global `SELECT`/`INSERT`/`DROP`/... reaches every schema of the
  server, and `pg_read_all_data` / `pg_write_all_data` act on every
  database the role can connect to, so dump SQL could read or change
  other databases with them.
- **Decision:** (amends D-168 items 2 and 3)
  1. MySQL/MariaDB: global data/DDL privileges (SELECT, INSERT, UPDATE,
     DELETE, CREATE, DROP, REFERENCES, INDEX, ALTER, CREATE TEMPORARY
     TABLES, LOCK TABLES, EXECUTE, CREATE/SHOW VIEW, CREATE/ALTER ROUTINE,
     EVENT, TRIGGER, DELETE HISTORY, SHOW CREATE ROUTINE, SHOW_ROUTINE)
     make the verdict PRIVILEGED with the reason class
     `global_data:<NAME>` (listed after the other gating reasons, so the
     32-reason cap never hides `global:FILE` and the like). Only USAGE,
     SHOW DATABASES, REPLICATION CLIENT and a few monitoring privileges
     stay harmless globally; FLUSH_TABLES now gates (`global:`).
  2. PostgreSQL: membership in `pg_read_all_data` / `pg_write_all_data`
     gates (`member_of:<role>`). `CONNECT` on many databases is not
     detected (not cheap and not decisive): the docs require a restore
     account limited to the target database.
  3. `docs/security/db-roles.md` recommends a dedicated restore account
     limited to the target database (schema-level grants on MySQL/MariaDB,
     no `pg_*_all_data`) that is not the backup account.
- **Alternatives considered:** Keep them ungated and document (D-168:
  fails open for cross-database reads/writes). Gate only write
  privileges (a global SELECT still exfiltrates other schemas).
- **Consequences:** **Security impact:** restores through accounts that
  reach other databases need an admin and an acknowledgement. Accounts
  with global data grants (common for "root-like" restore users) now
  show as privileged. No migration; reason codes only.
- **Related:** D-068, D-168; task 4.9; `internal/runner/dbprobe/privileges.go`,
  `internal/connections/privileges.go`, `docs/security/db-roles.md`,
  `docs/guides/restore.md`, `SECURITY.md` (RN-16).

## D-173: Restore core review fixes: worker privilege re-check, stored acknowledgement, slot-bound temporary targets, swap state, cancel race, identity redaction

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (M4 security and code reviews; awaiting the user's confirmation)
- **Context:** The M4 reviews found: the privilege verdict is only taken
  at request time (a role elevated while the restore is queued is not
  caught; D-168 residual); a temporary target that expires while its
  restore runs fails the restore mid-way (for example at the swap); a
  failing `RenameSwapDatabase` call was always reported as "target
  unchanged" although the swap may have happened; the stage's
  `context.AfterFunc` cleanup closed the zstd decoder while the reader's
  owner could be inside `Read` (data race); the age identity of a request
  was not registered for log redaction; and an unused cancel cause
  (`errInternalCanceled`).
- **Decision:**
  1. **Stored verdict and acknowledgement** (migration 00026): `restores`
     gets `target_privileges`, `target_privilege_reasons` and
     `privileged_target_acknowledged` (an admin's acknowledgement of a
     gated verdict; CHECKed to need a requester and a gated verdict),
     written at request time (`restoreArtifact`, `restoreGlobals`) and
     part of the restore's immutable identity (`restores_guard`).
     Internal test restores leave them NULL (D-170 checks those).
  2. **Re-check in the worker.** After taking the run slot and the target
     lock and before `restore.started` / staging, the worker probes the
     target again (runner `Probe` through the slot holder's target load;
     a not-ready target is unknown). A gated verdict passes only when the
     row carries the acknowledgement, every current reason is one the
     admin acknowledged, and the requester is still an active admin;
     otherwise the restore fails with `privileged_target`, audited as
     `restore.privileged_target_refused` (requested and current verdicts,
     `refusal`: not_acknowledged / new_reasons / requester_not_admin) and
     alerted (`restore_privileged_target`). Probe errors fail the restore
     (pin/host-key mismatches are recorded as security events).
  3. **Temporary targets and expiry.** A restore that holds its run slot
     loads its target with `RunningRestoreTarget(id, holder)`: a temporary
     target that expired after the start stays usable while the holder's
     slot exists (the cleanup never deletes a connection while a slot is
     held); a deleted one never loads. Requests and previews are refused
     (VALIDATION on `targetConnectionId`) when the temporary target expires
     within `workers.restore_timeout`.
  4. **Swap state.** When `RenameSwapDatabase` fails, the restorer asks the
     runner's `Probe` for the databases that accept connections: target
     listed and staging gone = swapped (logged, the replaced database is
     dropped, success); both listed = unchanged (as before); anything else
     = `swap_state_unknown`. The staging drop's "existed" answer then
     decides the log text ("unchanged" only when the staging database
     still existed, otherwise "the swap has probably happened; check ...").
     Never "unchanged" without proof.
  5. **Cancel race.** `Staged.Cleanup` (which may run on the context's
     AfterFunc goroutine) only marks the open reader aborted (atomic); the
     next `Read` fails with `ErrCleanedUp` and the reader's owner closes
     it. A `-race` test covers it.
  6. **Identity redaction.** The resolver registers the request's age
     identity with `logging.DefaultRegistry` for the request's duration
     (the registry zeroes its copy) besides zeroing its own slice; the
     worker registers the unsealed handoff identity while it holds it
     (unregistered when it is destroyed).
  7. `errInternalCanceled` is removed: an internal restore runs in the
     test restore's context, whose River cancel cause it already
     classifies.
  8. **Scratch restores skip privileges.** Test restores into a scratch
     database run `pg_restore --no-privileges` (new `RestoreHeader`
     field `no_privileges`; the runner refuses it unless the database is
     a `bekup_test_<id>` scratch name): the dump's GRANT/REVOKE may name
     roles of the source server and nothing reads them. User restores
     keep the source ACLs.
- **Alternatives considered:** Re-check in the runner at `Restore` (the
  runner has no acknowledgement); trust the request-time verdict (open
  window); refuse any gated verdict in the worker (breaks acknowledged
  admin restores); extend a temporary target's expiry automatically
  (silently prolongs credential lifetime); report a failed swap as
  failed without checking (wrong in both directions).
- **Consequences:** **Security impact:** a target role elevated while a
  restore waits is caught before any dump byte reaches it; an
  acknowledgement covers exactly what the admin saw. One more runner
  Probe per restore. Temporary credentials live until a running restore
  ends even past `expires_at` (bounded by the restore timeout + the slot
  heartbeat). Migration 00026; one audit action, two error codes
  (`privileged_target`, `swap_state_unknown`).
- **Related:** D-068, D-147, D-162, D-163, D-166, D-167, D-168, D-170;
  task 4.9; `internal/restore/recheck.go`, `internal/restore/worker.go`,
  `internal/restore/postgres.go`, `internal/restore/stage.go`,
  `internal/connections/temporary.go`,
  `internal/db/migrations/00026_restore_privilege_ack.sql`,
  `internal/graph/resolvers/restore.go`, `SECURITY.md` (RN-16, SV-23).

## D-176: Prometheus metrics: dedicated registry, bounded labels, internal listener with optional bearer, systemHealth (task 5.6)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent
- **Context:** Task 5.6 adds `/metrics` on an internal port (D-024) and
  runner health in the API. Open points: which collectors, how to keep
  label cardinality bounded, how runs/restores/retention feed counters
  without scattering metric calls, whether `/metrics` needs
  authentication, how the wildcard rule of D-098 applies to
  `metrics.listen`, and who may read `systemHealth`.
- **Decision:**
  1. **Registry and library.** `prometheus/client_golang` v1.23.2 with a
     dedicated registry in `internal/monitor` (never the global default
     registerer). It includes the standard Go and process collectors
     (`go_*`, `bekup_process_*`: memory, GC, file descriptors, CPU; no
     command line or environment) and `bekup_build_info{version,goversion}`.
  2. **Bounded labels.** Every label has a fixed value set from the code
     (run status/trigger, engine, restore status/mode, test-restore
     status, skip reason, River queue/state, certificate component);
     values outside it become `other`, undeterminable ones `unknown`; all
     combinations of the event counters are pre-initialized. The only
     open label is `job` (the job slug) on
     `bekup_backup_last_success_timestamp_seconds`, emitted only with the
     new `metrics.per_job_labels` (default false) and capped at the 1000
     stalest jobs. Without it, `bekup_backup_oldest_last_success_timestamp_seconds`
     supports a missed-backup alert. No IDs, user data, host names, error
     messages or secrets in labels.
  3. **Event counters through small hooks.** `runstate.Store.WithObserver`
     (`FinishObserver`, called after the commit of `Finish` and of the
     interrupted-attempt finish in `Begin`; the engine is read back from
     the job, best effort), `restore.Options.Metrics` (after the commit of
     every terminal path: worker finish, stuck sweep, API cancel, test
     restore finish and test-restore stuck sweep), `retention.Options.Metrics`
     (per job sweep; dry runs not counted) and the existing
     `scheduler.Metrics` (D-145). Counters are per process (serve and
     `bekup worker` each expose theirs). Runs canceled before a worker
     picked them up (`CancelQueuedTx`) are not counted.
  4. **Snapshot gauges.** Queue depth (`river_job` per queue/state),
     runner up/Docker/active runs (Health RPC over mTLS), certificate
     expiry in seconds (`runner_client`, `runner_ca`, `postgres_ca`,
     `postgres_client` from the configured files, `runner_server` from
     Health), the earliest target-certificate pin expiry and the last
     successful runs come from one cached snapshot (15 s TTL, one refresh
     at a time, 10 s bound): scrape floods cost no extra DB queries or
     runner calls. Registered only in `serve`, not in `bekup worker`.
     Three new read-only sqlc queries (`monitor.sql`), no migration.
  5. **Listener.** `metrics.listen` gets the D-098 wildcard refusal
     outside dev mode with the override `metrics.allow_wildcard_listen`
     (startup warning). The server answers only `GET`/`HEAD /metrics` and
     `/healthz` (fixed timeouts, 16 KiB headers, 4 scrapes in flight,
     security headers, no UI/API routes). Compose adds an `internal`
     `metrics` network (10.213.252.0/24) where serve has the fixed
     address 10.213.252.10; no port is published.
  6. **Optional bearer authentication.** `metrics.auth_bearer_file` (a
     secret file, `config.ReadSecretFile`; ≥ 32 printable ASCII
     characters) makes `/metrics` require `Authorization: Bearer`;
     only the SHA-256 is kept and compared in constant time, the value is
     registered with the log redaction and the file bytes zeroed.
     `/healthz` stays open (it says only "ok"). Basic auth was not added:
     Prometheus' `authorization.credentials_file` covers bearer with a
     single file, and one scheme is less code.
  7. **`Query.systemHealth`** (new operation `monitor.system_health_view`,
     admin only, no freshness): runner up/Docker/version/active runs,
     certificate expiries, queue depth, earliest pin expiry, oldest last
     success, scheduler enabled and this process's last tick, from the
     same snapshot. The existing `monitor.runner_health_view` (staff) stays
     unused for a reduced dashboard view (6.2).
- **Alternatives considered:** The global default registry (any imported
  library could add metrics); per-job labels by default (unbounded
  growth, slugs reveal job names to whoever can scrape); computing run
  counters from the `runs` table at scrape time (correct across
  restarts but not monotonic and an aggregate query per scrape); mTLS
  for the scrape (heavy for operators; binding an internal network
  plus the optional bearer is enough for non-secret operational data);
  making `/metrics` authenticated by default (would break the zero-config
  compose scrape; the data contains no secrets).
- **Consequences:** **Security impact:** `/metrics` reveals operational
  data (counts, durations, sizes, versions, certificate expiry, queue
  depth, optionally job slugs) to anyone who reaches it; it is bound to
  loopback or an internal network, never published, and can require a
  bearer credential. The snapshot cache bounds the load an unauthenticated
  scraper can cause. Runner errors are not logged verbatim. New config
  keys: `metrics.allow_wildcard_listen`, `metrics.auth_bearer_file`,
  `metrics.per_job_labels`.
- **Related:** D-024, D-098, D-102, D-106, D-145; task 5.6;
  `internal/monitor`, `internal/runstate/store.go`,
  `internal/restore/metrics.go`, `internal/retention/worker.go`,
  `internal/app/monitor.go`, `internal/db/queries/monitor.sql`,
  `docker-compose.yml`, `docs/operations.md`, `SECURITY.md`.

## D-177: Webhook sender: signature scheme, signing keys, URL rules, result classification and auto-disable policy (task 5.2)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 5.2)
- **Context:** D-023/D-051 fix HMAC-signed webhooks with a timestamp, https
  only, the SSRF guard, no redirects and auto-disable after repeated
  failures, but not the header and signature format, the key format, the
  URL rules, how an attempt's result is classified for the River notify
  worker (MaxAttempts 10, D-145) or the auto-disable thresholds. Task 5.3
  owns channel storage and the retries, so 5.2 is a storage-agnostic
  library.
- **Decision:**
  1. **Package** `internal/notify/webhook`: `ValidateURL`, `Sender.Send`
     (one attempt, never retries), `FailurePolicy`, `GenerateHMACKey`,
     `Sign`/`Verify`, `BuildPayload`/`TestPayload`, `DisplayURL`.
  2. **Request:** `POST`, body = the JSON envelope
     `{"id","type","created_at","data"}` (≤ 64 KiB, `data` minimal per
     NT-2), headers `Content-Type: application/json`,
     `User-Agent: bekup/<version>`, `X-Bekup-Event` (D-161 type),
     `X-Bekup-Delivery` (stable per (event, channel) across retries, so
     receivers can deduplicate; random 128-bit hex if the caller has none),
     `X-Bekup-Timestamp` (unix seconds, taken per attempt) and
     `X-Bekup-Signature: v1=<hex HMAC-SHA256(key, "<timestamp>.<body>")>`.
     The header may carry several comma-separated `v1=` entries in the
     future (key rotation); receivers accept any match. Receivers must
     reject timestamps outside **±5 minutes** and should drop repeated
     delivery IDs within that window. Test vectors are in
     `docs/guides/notifications.md` and the unit tests.
  3. **Signing keys:** `bkwh_` + 32 bytes from `crypto/rand` in unpadded
     base64url (48 characters). The whole string is the HMAC key (receivers
     use it verbatim, no decoding step), shown to the owner exactly once at
     creation/rotation; stored sealed with the keyring (5.3, AAD on the
     channel row); `Send` accepts 32–256 byte keys.
  4. **URL rules** (input time, `ValidateURL`): ≤ 2048 bytes of printable
     ASCII (IDNs in punycode), https only (http only with `dev.enabled`,
     logged at warn without the URL), no userinfo, no fragment, a valid
     port, the shared host-name rule (D-140), and every resolved address
     must pass `netguard.ResolveAndCheck`; errors are fixed messages that
     never echo the URL or address (`ErrHostNotAllowed` /
     `ErrHostUnreachable` follow netguard's normalization). `Send`
     re-applies the syntactic rules; the dial-time guard is authoritative.
     Webhook URLs are treated as sensitive (Slack/Teams embed a bearer
     credential in the path): never logged; `DisplayURL` shows scheme,
     host and port only.
  5. **Transport:** only a `netguard` client (`NewSender` takes a
     `*netguard.Guard`, not an arbitrary client): dial-time check, no
     proxy, no redirects, TLS ≥ 1.2 system roots, 15 s per attempt
     (configurable ≤ 60 s). The response content is ignored: at most 4 KiB
     is read and discarded, the rest never read.
  6. **Classification:** success = 2xx. Retryable: network errors and
     timeouts, 5xx, 408, 429 (`Retry-After` seconds or HTTP date honored
     on 429/503, clamped to ≤ 1 h). Permanent: any 3xx (never followed),
     other 4xx, a blocked address, TLS errors, an invalid delivery.
     Aborted: the caller's context ended (shutdown/timeout), which never
     counts as a failure. Results carry fixed codes (`ok`, `http_status`,
     `redirect`, `not_allowed`, `tls`, `unreachable`, `failed`,
     `invalid_delivery`, `canceled`) and normalized errors only (raw
     errors embed the URL; refused/timed-out/unresolvable stay one
     category, D-120).
  7. **Auto-disable** (`FailurePolicy`, counters persisted by 5.3 per
     attempt): disable after **10 consecutive failed attempts spanning at
     least 24 h**, or **5 permanent failures** since the last success
     (retryable failures in between do not reset); a success resets
     everything. The decision returns a reason with a user-facing message
     for the owner's inbox notification. Re-enabling is a manual action by
     the owner (5.3/6.10).
- **Alternatives considered:** Signing headers too (more receiver
  complexity; the delivery ID is only for dedup and the body carries the
  event id/type). A raw/hex key that receivers must decode (a common
  receiver mistake). Disabling on consecutive failures alone (one outage
  with 10 River retries would disable a healthy receiver). Following
  same-host redirects (still an SSRF/credential-leak vector).
- **Consequences:** Receivers verify with a few lines of code (samples for
  Go, Python and Node in the guide). A receiver behind a redirect (e.g.
  http→https or a moved path) must be reconfigured with the final URL.
  Security impact: no SSRF through redirects, proxies or rebinding; no
  URL/address leakage through errors or logs; forged or replayed
  deliveries are detectable by receivers that check the signature,
  timestamp and delivery ID; floods towards dead receivers stop through
  auto-disable.
- **Related:** D-018, D-023, D-051, D-055, D-120, D-140, D-145, D-161;
  tasks 5.2, 5.3, 5.3b, 6.10; `internal/notify/webhook`,
  `docs/guides/notifications.md`, SECURITY.md NT-1, NT-6, WK-12.

## D-178: SMTP settings, e-mail sender and user e-mail verification (task 5.1)

- **Date:** 2026-10-28
- **Status:** proposed; the TLS-required transport and AUTH rules (items 2,
  3 and the `plaintext_refused` code) are superseded by D-180
- **Decided by:** agent (task 5.1)
- **Context:** D-023/D-051 require system SMTP for e-mail, an SSRF-guarded
  connection (D-055, D-120), outbound TLS without pinning (D-018) and a
  verified address (signed link) before e-mail delivery. The plan does not
  fix where the settings live, the TLS and AUTH rules, how messages are
  built, or the verification protocol.
- **Decision:**
  1. **Package** `internal/notify/email` (stdlib `net/smtp`, no new
     dependency): `Settings` (the `settings` row `smtp`: non-secret JSON;
     the password sealed with the keyring under AAD
     `settings:smtp:password`), `Sender` (implements `Mailer`, used by
     5.3), `Message`/`Render`/`Notice`, `TestMailer` and `Verifier`
     (+ `IsVerified` for 5.3).
  2. **Transport:** modes `starttls` and `implicit_tls`; TLS ≥ 1.2,
     `ServerName` = the configured host, the system roots or, when the
     admin sets a CA bundle (PEM certificates only, ≤ 256 KiB), **only**
     that bundle (as for S3, D-129). A server that does not advertise
     STARTTLS is refused (no downgrade). `plaintext` (no TLS, no AUTH)
     exists only for the dev mailpit: refused at save and at send time
     unless dev mode is on, logged at WARN on save and every send, flagged
     `insecure` in the API.
  3. **AUTH** only over TLS (checked twice: the session state and the
     mechanism itself), PLAIN preferred, LOGIN as fallback, only to the
     configured host; the password copy is zeroed after use and registered
     with the log redaction registry. Username and password go together;
     a stored password is never sent to a changed host/port/username
     (D-140: must be entered again).
  4. **Dial:** every connection through `netguard.Guard.DialContext`
     (dial-time check); the host is also resolved and checked at save time
     with admin-visible reasons. Private relays need the `[netguard]`
     allowlist. Timeouts: connect 1–60 s (default 10), whole session 5–300 s
     (default 60).
  5. **Messages:** bare recipient addresses (≤ 50), subject ≤ 200
     characters; CR, LF, other C0/C1 controls, U+2028/2029 and invalid
     UTF-8 in any header value are refused (never stripped); the subject
     is RFC 2047 encoded, addresses via `net/mail`; every header line is
     re-checked before writing. Bodies are multipart/alternative
     (quoted-printable) from one embedded `html/template` + `text/template`
     layout: auto-escaping, no images, no remote resources, no tracking;
     links must be absolute http(s) URLs without user info.
     `Auto-Submitted: auto-generated` is set.
  6. **Errors:** fixed codes (`not_configured`, `plaintext_refused`,
     `not_allowed`, `unreachable`, `tls_failed`, `starttls_unavailable`,
     `auth_failed`, `auth_unsupported`, `rejected`, `invalid_message`,
     `failed`) with fixed messages; the admin detail is only the blocked
     address or the SMTP reply code, never server text. Non-admin callers
     only see "the e-mail could not be sent".
  7. **API:** `smtpSettings` (`smtp.view`, admin), `updateSmtpSettings`
     (`smtp.update`, admin + step-up; audited `setting.changed` with field
     names and old/new values, the CA bundle as a flag, the password only
     by name plus `secret.changed`), `sendTestEmail(to)` (`smtp.test`,
     admin, single root, 10/min per admin, audited `smtp.test_sent`).
  8. **Verification:** `requestEmailVerification` (`account.email_verify_request`,
     every role) creates a 256-bit code, stores only
     `Keyring.MAC("email-verification", user id ‖ address ‖ code)` with
     the key ID in `email_verifications` (one row per user; a new request
     replaces the old link), and mails
     `<server.base_url>/verify-email#code=<base64url>`: the code is in the
     URL **fragment**, so it never reaches server/proxy logs or a Referer.
     Links expire after 24 hours; one link per minute per account
     (enforced in the database) plus a per-account limiter (5 per
     15 minutes); an undeliverable link is deleted. `verifyEmail(code)`
     (`account.email_verify`, **authenticated**, single root) looks the
     row up by the caller's user ID (never by the code), compares the MAC
     with `hmac.Equal`, requires the same address, an unexpired row and an
     active account, deletes the row (single use) and sets
     `users.email_verified_at`; every failure is one generic VALIDATION
     answer, counts towards deleting the row after 5 tries and is audited
     (`user.email_verification_failed` with a reason). A trigger on
     `users` clears `email_verified_at` whenever `email` changes, whatever
     code path changes it. `User.emailVerified` / `emailVerifiedAt` are
     exposed; 5.3 delivers e-mail only when `IsVerified` says so.
- **Alternatives considered:** a vetted third-party SMTP library (rejected:
  `net/smtp` covers STARTTLS/AUTH with a small surface and no new
  dependency); system roots plus the bundle (rejected: a private relay's CA
  should not widen trust for others, consistent with S3); a public
  `verifyEmail` route usable while signed out (rejected: binding the code
  to the signed-in account makes a leaked or forwarded link useless and
  keeps link scanners from verifying addresses); the code in the query
  string (rejected: logs and Referer); storing a plain SHA-256 of the code
  (rejected in favour of the keyed MAC, which is useless without the master
  key and binds the address).
- **Consequences:** e-mail cannot be sent in clear text outside dev mode
  and AUTH never leaves TLS; admins must allowlist internal relays.
  Verifying requires signing in (the link opens the UI, task 6.10 builds
  the page). `bekup_app` may only update `failed_attempts` of
  `email_verifications`. **Security impact:** new outbound channel
  (boundary B4) guarded like S3/webhooks; the SMTP password is a new sealed
  secret (A4 protects it); verification codes are high-entropy, MAC-stored,
  single use and account-bound.
- **Related:** D-018, D-023, D-051, D-055, D-095, D-120, D-129, D-140,
  D-161; tasks 5.1, 5.3, 6.9, 6.10; `internal/notify/email`,
  `internal/db/migrations/00027_email_verification.sql`,
  `internal/graph/schema/email.graphqls`, `docs/guides/notifications.md`,
  SECURITY.md NT-1, NT-3, NT-5.

## D-179: Web UI shell and auth flows: session gate, auth event bus, step-up hook, strict QR re-rendering (task 6.1)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 6.1)
- **Context:** Task 6.1 builds the app shell, sign-in, forced flows, the
  step-up dialog and the profile on the API of M1 (D-105, D-107, D-109,
  D-110, D-112, D-113, D-116) and the SSE transport (D-124). The plan
  leaves open how the UI reacts to session and restriction errors, how
  pages ask for a step-up, how the TOTP QR code is shown under the strict
  CSP (D-082: Trusted Types `'none'`, `img-src 'self'`) and how the
  password strength is estimated without the server's zxcvbn.
- **Decision:**
  1. **Session gate.** A `RequireSession` route element loads `me` (the
     only source of the signed-in user; never used for authorization) and
     shows the shell, the forced password change (`passwordChangeRequired`)
     or the forced 2FA enrollment (`twoFactorRequired` with a past
     `twoFactorEnrollmentDeadline`); a future deadline shows a banner.
     UNAUTHENTICATED → `/login?next=...`, SECOND_FACTOR_REQUIRED →
     `/login/verify`. No setup page (D-113).
  2. **Auth event bus.** An Apollo `ErrorLink` maps error codes of any
     operation (except `Login`, `Me`, `Logout`, which handle their own) to
     events: UNAUTHENTICATED → clear the cache and go to login with the
     current page as return path; SECOND_FACTOR_REQUIRED → verify page;
     PASSWORD_CHANGE_REQUIRED / REQUIRES_2FA_ENROLLMENT → refetch `me`.
     A stream ended with UNAUTHENTICATED/FORBIDDEN (or a `sessionEvents`
     event for the current session) only triggers a **session check**
     (`me`, network-only): a password change revokes the stream's old
     session while this browser holds a new one, so redirecting at once
     would sign the user out after every password change. A confirmed
     session reopens the stream (one `sessionEvents` stream per tab).
  3. **Return paths** are accepted only as same-origin relative paths: no
     scheme, no `//`, no backslash, no control characters, not `/login*`,
     at most 2 KiB (open redirect protection); anything else → `/`.
  4. **Step-up.** `useStepUp().withStepUp(op, {aal2})` runs `op`; on
     STEP_UP_REQUIRED it opens one shared dialog (`beginStepUp`, the
     account's methods; the password only for accounts without a factor;
     `aal2` offers only a UV passkey or TOTP), completes `completeStepUp`
     and retries `op` at most twice; closing rejects with
     `StepUpCancelledError`. REQUIRES_2FA_ENROLLMENT links to the 2FA
     settings. The dialog's `beginStepUp` runs once per opening (a second
     call would replace the session's passkey challenge).
  5. **TOTP QR code.** The server's SVG is never inserted as markup (no
     `innerHTML`/`DOMParser` under Trusted Types, no `blob:`/`data:` image).
     `lib/qr-svg.ts` matches the exact document shape of
     `internal/auth/qrsvg.go` (viewBox, one white rect, one path of
     `M x yhNv1h-Nz` runs) and re-renders it as React SVG elements; any
     other input shows only the manual key.
  6. **WebAuthn JSON** is converted in `lib/webauthn.ts` (base64url ↔
     ArrayBuffer, response shaped like `PublicKeyCredential.toJSON()`)
     instead of `PublicKeyCredential.parse*FromJSON`, which not every
     supported browser has.
  7. **Password strength** is an advisory client-side estimate (length in
     code points, character classes, repetition and sequences, a short
     common-word list, the account's e-mail/name parts like
     `auth.identityParts`); the server's policy check is authoritative and
     its VALIDATION message is shown on the field. No zxcvbn bundle in the
     browser.
  8. **One-time secrets in the UI** (TOTP key, recovery codes) live only in
     component state, are dropped after confirmation, and recovery codes
     need an explicit "I have saved these codes" before the dialog closes.
     Copy uses the Clipboard API; download is a local `blob:` link.
  9. **Strings** live in `src/lib/messages.ts` (English only); pages are
     lazy-loaded route chunks; sign-in and sign-out clear the Apollo cache.
- **Alternatives considered:** redirecting on every stream end code
  (signs users out after a password change); a global step-up retry in
  the Apollo link (cannot show a dialog or know whether the operation is
  safe to repeat; per-call `withStepUp` keeps retries explicit);
  rendering the QR code via `DOMParser`/`innerHTML` with a Trusted Types
  policy (would need `trusted-types` to allow a policy, weakening D-082)
  or as an `<img>` from a `blob:` URL (needs `img-src blob:`);
  bundling zxcvbn-ts (about 800 KB of dictionaries for an advisory
  meter).
- **Consequences:** Pages of M6 use `useStepUp`, `apiErrors`/`userMessage`/
  `applyFieldErrors` and `renderApp` for tests. The CSP stays exactly
  D-082; `pnpm csp-check` now walks the real sign-in, step-up, TOTP
  enrollment and profile pages against a fake API. A change of the QR SVG
  format in `internal/auth/qrsvg.go` must update `lib/qr-svg.ts` (the
  manual key keeps working meanwhile). Security impact: no new trust
  boundary; open-redirect-safe return paths; one-time secrets never
  persisted by the UI.
- **Related:** D-057, D-082, D-083, D-095, D-105, D-107, D-109, D-112,
  D-113, D-116, D-124; task 6.1; `web/src/app/`, `web/src/features/`,
  `web/src/lib/`, `web/scripts/csp-check.mjs`, `docs/development.md`.

## D-180: SMTP follows the server's TLS support

- **Date:** 2026-10-28
- **Status:** accepted
- **Decided by:** user
- **Context:** Task 5.1 required STARTTLS or implicit TLS for SMTP (plaintext
  only in dev mode). The user wants bekup to work with any SMTP server's
  settings; TLS must not be mandatory.
- **Decision:** SMTP modes: `auto` (default: use STARTTLS when the server
  offers it, otherwise send unencrypted), `starttls` (required),
  `implicit_tls`, and `none` (never encrypt). AUTH over an unencrypted
  connection is allowed when the configured mode permits plaintext. Following
  D-001's pattern for weakened settings: changing SMTP settings stays admin +
  step-up and audited (the audit records the mode); the UI shows an
  "email may be sent unencrypted" badge for `auto`/`none`; a send that ends up
  unencrypted logs a warning (no credentials logged). Certificate
  verification still applies whenever TLS is used. Supersedes the
  TLS-required part of D-178 and the plan's "STARTTLS/TLS required" wording.
- **Alternatives considered:** TLS required (previous); plaintext allowed only
  with an extra opt-in flag (rejected: the user wants it to just follow the
  server).
- **Consequences:** email content and SMTP credentials may travel unencrypted
  to servers without TLS; this is visible to admins and audited.
- **Related:** tasks 5.1, 6.9; D-001, D-095, D-178.

## D-181: User admin UI: one-time temporary passwords, UI-only self guards, derived 2FA state, admin-only owner picker (task 6.8)

- **Date:** 2026-10-28
- **Status:** proposed (derived 2FA state and admin-only picker superseded by D-195)
- **Decided by:** agent (task 6.8)
- **Context:** Task 6.8 builds the users admin, owner assignment and the
  2FA policy page on the API of D-110, D-112 and D-067. The API returns a
  temporary password once, does not expose an account's last activity or
  whether it has a second factor that the policy does not require, has no
  "jobs owned by user" filter, and `users` is admin only although
  maintainers may assign owners.
- **Decision:**
  1. **Temporary passwords** (`createUser`, `resetUserPassword`) run with
     Apollo `fetchPolicy: "no-cache"`; the value lives only in the page's
     state and in a dialog that cannot be dismissed (Escape/outside click)
     until the admin acknowledges it, then it is dropped. It shows the
     72-hour expiry and a "will not be shown again" warning.
  2. **Self guards are UI only:** on their own account an admin cannot
     change the role, disable, reset the password, reset second factors or
     delete (links to the profile instead). The server stays authoritative
     (`resetUserSecondFactors` FORBIDDEN on self, last-admin CONFLICT);
     CONFLICT from demote/disable/delete is shown as the last-admin
     explanation.
  3. **2FA column** is derived: required without a deadline = enrolled;
     a future deadline = due; a past one = overdue; otherwise "not
     required" (enrollment unknown). Last activity is shown on the detail
     page from `userSessions`, not in the list.
  4. **Owned jobs** are found client-side from `jobs(first: 200)` with
     `owners`; a note appears when more jobs exist. Assigning several
     jobs runs one `assignJobOwners` per job under a single step-up.
  5. **`UserPicker`** (`web/src/features/users/user-picker.tsx`) is the
     reusable owner picker: only role User is selectable (D-067), others
     are listed disabled with the reason; deleted accounts are hidden.
     It uses the admin-only `users` query and explains FORBIDDEN to other
     roles.
  6. **2FA policy page** (`/admin/two-factor`) previews the effects,
     marks any weakening (a role no longer required, dropping "every
     role", a longer grace period) with the insecure hatching, asks for
     an extra confirmation and then a step-up.
  7. Admin routes are wrapped in a `RequireAdmin` element (UI shaping
     only; queries do not run for other roles).
- **Alternatives considered:** keep the password in the cache (it could
  re-render after a refetch); add `lastSeenAt` / `hasSecondFactor` /
  owner filter / a maintainer-visible candidates query to the API now
  (backend change outside this UI task; recommended follow-up).
- **Consequences:** a temporary password never reaches the Apollo cache.
  Maintainers cannot use `UserPicker` until a maintainer-visible
  candidate query exists. Instances with more than 200 jobs see an
  incomplete owned-jobs list until an owner filter exists. No security
  boundary changes: the server enforces every rule.
- **Related:** tasks 6.5, 6.8; D-001, D-067, D-106, D-110, D-112, D-179.

## D-182: Connections and destinations UI: deliberate pin and host-key confirmation, client-side retargeting prompt, session-only test results (tasks 6.3, 6.4)

- **Date:** 2026-10-28
- **Status:** proposed (item 5 superseded by D-195)
- **Decided by:** agent (tasks 6.3, 6.4)
- **Context:** The connection and destination pages drive the certificate
  pinning (D-133, D-139, D-140), SSH host-key confirmation, write-only
  credentials and the TLS opt-out on the API of tasks 2.11/2.12. The plan
  leaves open how deliberate the confirmations must be in the UI, how the
  D-140 re-entry rule surfaces, and where the "last test result" and the
  privilege verdict (D-168) come from, since the API stores neither.
- **Decision:**
  1. **Pin confirmation.** The pin dialog captures the chain
     (`fetchCertificate`), lists every certificate (subject, issuer, SANs,
     validity, SPKI SHA-256 as base64 and hex, certificate SHA-256), marks
     the suggested anchor and offers only a self-signed leaf or a CA as an
     anchor; it always explains why public CAs are refused (D-139). The
     confirm button stays disabled until the admin ticks "I compared the
     fingerprints out of band"; `leafSpkiSha256` from the capture is sent
     unless "allow leaf rotation" is chosen for a CA anchor. A PEM supplied
     out of band needs the leaf SPKI or explicit leaf rotation. The same
     dialog stages a backup pin.
  2. **Host keys.** One shared dialog (`components/ssh/host-key-dialog`)
     fetches the key, shows type, fingerprint and the authorized-key line,
     warns when it differs from the pinned key and requires the comparison
     tick; the confirmation sends the host/port the key was fetched from as
     `expectedHost`/`expectedPort` (D-140). Generated keypairs show the
     public line once with an "installed" acknowledgement (it stays visible
     in the settings; it is public).
  3. **TLS downgrade.** Weakening the TLS mode (`setTlsMode`) shows the
     audit/alert/badge consequences in the hazard hatch and requires typing
     the connection name; creating a connection with a non-pinned mode
     shows the same warning. The TLS mode cannot change in the edit form.
  4. **Write-only credentials.** Stored passwords/keys show only a
     "Stored" state with Change/Remove; inputs exist only while a new value
     is entered. The form mirrors D-140: when the database endpoint (host,
     port, tunnel route) or the SFTP host/port/user/base path or the bastion
     changes while a password is stored, the input is forced open with the
     reason and a client-side check blocks saving until it is entered again
     (or removed / replaced by a key); the server remains authoritative.
  5. **Session-only results.** The last `testConnection` /
     `testDestination` result per item is kept in an in-memory store of the
     tab (`lib/session-results`), never persisted; the list's privilege
     badge and "last test" column read from it and say "Not tested"
     otherwise.
  6. **Single-root requests.** Every runner-calling mutation is its own
     operation with one root field (D-117); refetches are separate requests.
     Maintainers see the metadata and the checks the API allows; admin
     actions are hidden (the server enforces @hasRole).
  7. Form selects are styled native `<select>` elements
     (`components/ui/native-select`) for keyboard/screen-reader behaviour
     and simple form binding.
- **Alternatives considered:** auto-confirming a fetched pin or host key
  (TOFU without comparison: rejected, D-034); persisting test results
  server-side (an API change outside the UI tasks); relying on the server's
  VALIDATION alone for D-140 (works, but the prompt before submit is
  clearer).
- **Consequences:** Confirmations take one extra deliberate step. Test
  results and privilege badges disappear on reload until tested again. No
  trust boundary changes. Security impact: the UI never displays stored
  credentials and makes out-of-band comparison an explicit act.
- **Related:** tasks 6.3, 6.4; D-034, D-117, D-133, D-139, D-140, D-158,
  D-168, D-179; `web/src/features/connections`, `web/src/features/destinations`,
  `web/src/components/ssh`.

## D-183: Notification core: transactional outbox, audit-driven emitters, delivery-time authorization, noise rules and storage (task 5.3)

- **Date:** 2026-10-28
- **Status:** proposed (push parts of items 3 and 6 superseded by D-206)
- **Decided by:** agent (task 5.3)
- **Context:** D-023, D-051 and D-161 fix the channels, the catalog, the
  audiences, the defaults and the noise rules; D-177 and D-178 deliver the
  webhook and e-mail senders. Open were how producers emit events
  (atomically or not), where recipients and access are resolved, how
  preferences and channels are stored, how retries, auto-disable and
  idempotency work, and the exact noise rules.
- **Decision:**
  1. **Outbox.** `notify.Emit(ctx, q, Event{Type, Subject, Severity,
     Detail, DedupKey})` inserts a `notification_events` row with the
     producer's `*sqlc.Queries`, so an event commits or rolls back with the
     state change it reports. Events carry identifiers, a catalog type and
     a bounded detail object (≤ 16 snake_case keys, strings ≤ 200
     characters without control characters, booleans, integers, ≤ 2 KiB;
     keys naming secrets are refused); never secrets, connection strings,
     server output or database content. `DedupKey` makes an emit idempotent
     (daily digests, sign-in alerts). A **dispatcher** (every process that
     runs workers, every 2 s, `FOR UPDATE SKIP LOCKED`) enqueues the River
     `notify_fanout` job and marks the event dispatched in one transaction,
     so producers without a pgx.Tx or without a queue client (CLI) work too.
  2. **Emitters through existing hooks.** Backup runs: `runstate` emits
     `backup.started|started_early` in `Start` and the terminal event in
     `finishTx` (covers Finish, interrupted attempts and canceled queued
     runs). Job, connection and security events: an `audit.Observer`
     (`notify.ObserveAudit`, set on serve's/worker's Auditor) maps audited
     actions to events in the audit entry's transaction (`job.*`,
     `connection.pin_changed|host_key_changed|tls_downgraded`,
     `security.account`: sign-in (once per account, address and day),
     password change/reset, second-factor changes, recovery codes,
     lockouts, role changes; `security.system`: opt-outs, new admins and
     admin grants, key/cert rotation, instance keys, audit verification
     failures, pin/host-key mismatches, restore verification failures).
     Restores, test restores, retention anomalies (`artifact.missing`) and
     destination tests (`destination.test_failed`, a new `TestFailed`
     hook) emit after their commit through the existing post-commit hooks
     (`EmitNow`; failures are logged).
  3. **Fan-out** (`notify_fanout`, notify queue, unique by event id, one
     transaction): recipients are the active accounts (not deleted, not
     disabled) for which `authz.Policy.Authorize` allows the event's view
     operation with their **current** role (and the subject job:
     `run.view`, `restore.view`, `test_restore.view`, `artifact.view`,
     `job.view`, `connection.view`, `destination.view`, `account.view` +
     subject, `monitor.alert_list`). Per recipient the channels resolve
     **per-job override > per-event preference > D-161 default** (inbox on,
     e-mail for problem events, webhooks/push opt-in); mandatory security
     events always keep inbox + e-mail. It writes the inbox row (unique per
     event and user) and one delivery per (recipient, channel) with an
     idempotency key, plus one per enabled shared channel attached to the
     job whose filter (groups/events, empty = all) matches; security
     events never go to shared channels. E-mail needs a verified address
     (else a `skipped/email_unverified` delivery).
  4. **Delivery** (`notify_deliver`, unique by delivery id): re-checks the
     recipient (active, still authorized, verified address, channel enabled
     and still attached) before every attempt (`skipped/access_revoked`
     etc.), renders minimal content (title, one sentence, link from
     `server.base_url`; e-mail via the 5.1 templates, webhook envelope via
     5.2 `BuildPayload` with the delivery id as `X-Bekup-Delivery`). Retries
     follow 5.2's classification (River backoff; `Retry-After` as a snooze)
     with the delivery's own attempt counter capped at 10; permanent
     failures fail at once; SMTP not configured skips. Webhook failure
     counters are persisted per channel and the D-177 `FailurePolicy`
     (permanent threshold = `notifications.webhook_failure_threshold`)
     auto-disables the channel and writes an inbox notice to its owner (all
     admins for a shared channel).
  5. **Noise rules:** `*.started` events of one job within
     `notifications.collapse_window` collapse into the first (counted);
     per user and channel, after `notifications.max_per_user_per_hour`
     e-mail/webhook deliveries within an hour the next one is a single
     summary and further ones are skipped (`flood_collapsed`) while the
     inbox still gets everything; security events are exempt. Retention
     removals are only reported by the daily `notify_retention_digest`
     (one `retention.summary` per job and UTC day for the last 3 complete
     days, deduplicated). `notify_cleanup` prunes events and deliveries
     after 90 days and inbox rows after 180 days.
  6. **Storage** (migration 00028): `notification_events`, `notifications`,
     `notification_deliveries`, `shared_channels` (email_list | webhook),
     `job_shared_channels`, `user_channels` (webhook; push reserved;
     max 5 per user by trigger), `user_notification_prefs`. Webhook URLs
     (may embed bearer credentials) and HMAC keys are sealed with the
     keyring under `<table>:<id>:url` / `:hmac_key`; only `DisplayURL` is
     stored in clear. `bekup_app` may update only progress columns of
     events and deliveries, `read_at` of inbox rows and the mutable columns
     of channels/preferences (never owner, type or identity).
- **Alternatives considered:** River insert directly in the producers'
  transactions (needs a pgx.Tx and a queue client everywhere; the CLI and
  `*sqlc.Queries`-only paths could not emit); in-memory emit after commit
  (events lost on crashes); per-producer emit calls for job/connection/
  security actions (scattered and easy to forget; the audit log already
  names every such action); resolving recipients at emit time (would
  deliver to users who lost access in between); clear-text webhook URLs
  (Slack/Teams URLs are credentials).
- **Consequences:** every audited action of the mapped kinds now also
  writes an outbox row in its transaction (an outbox failure fails the
  action, like an audit failure). Notifications may arrive a few seconds
  after the state change. Users who lose access stop receiving events
  immediately, also for events already fanned out. Security impact: no
  secrets in events, payloads, logs or River args (IDs only); sealed
  webhook URLs and keys; webhooks only through the SSRF-guarded 5.2 sender;
  e-mail only to verified addresses; no notification can reveal a job to a
  user who may not see it.
- **Related:** D-023, D-051, D-106, D-145, D-161, D-177, D-178; tasks 5.3,
  5.3b, 5.4, 5.5, 6.10; `internal/notify`, `internal/runstate/notify.go`,
  `internal/audit` (Observer), `internal/app/notify.go`,
  `internal/db/migrations/00028_notifications.sql`,
  `docs/guides/notifications.md`, SECURITY.md NT-*.

## D-184: User directory for staff, admin activity facts on User and the jobs owner filter (task 6.8 follow-up)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 6.8 backend follow-up)
- **Context:** D-181 recorded three API gaps found while building the
  users admin: maintainers must pick job owners (D-106) but `users` is
  admin only; the admin list cannot show last activity or whether an
  account has a second factor without one query per user; and the owned
  jobs of a user can only be found client-side from the first 200 jobs.
  D-106 already reserved `user.directory_view` (admins, maintainers) for a
  minimal owner-picker directory.
- **Decision:**
  1. **`Query.userDirectory(search, first, after)`** (`@hasRole(ADMIN,
     MAINTAINER)`, operation `OpUserDirectoryView`, rule `staff`) returns
     a separate type `UserDirectoryEntry { id, displayName, email, role,
     disabled }` (not `User`, so no other field is reachable), only for
     accounts that are not deleted, ordered by e-mail like `users`
     (same cursor format, 1..200 per page, default 50). `search` is a
     trimmed case-insensitive substring of e-mail or display name, at
     most 200 characters, LIKE wildcards escaped. The sqlc query
     `ListUserDirectory` selects only those columns.
  2. **`User.lastSeenAt: Time`** is the newest `sessions.last_seen_at` of
     the account, ignoring sessions still waiting for their second factor
     (`mfa_pending`); revoked/expired rows count until housekeeping purges
     them; null when none is on record. **`User.twoFactorEnrolled:
     Boolean!`** is true with an enabled TOTP or at least one passkey
     (recovery codes alone do not count). Both are field resolvers
     guarded by `Policy.Authorize(OpUserView)` (admin only), except that
     `twoFactorEnrolled` of the caller's own account is allowed (it is
     already visible through `myTwoFactorStatus`); others get FORBIDDEN.
     They are loaded by one query (`UserActivityFacts`) for every user on
     the page: `users` hints its page ids into the per-operation batch
     loader (`runLoader`, D-149) and `UserAdmin.Activity` takes
     at most 500 ids.
  3. **`JobFilter.ownerId`** narrows `jobs` to the jobs the user owns
     (`job_owners`, an `EXISTS` in `ListJobs`), combined with the
     existing `JobScope` filter. It is staff only: for a caller with an
     owned scope the service refuses it (`ErrOwnerFilterForbidden` →
     FORBIDDEN), even with the caller's own id.
- **Alternatives considered:** open `users` to maintainers with field
  masking (easy to leak a new field later); expose last activity and
  enrollment through `userSessions` / the 2FA standing only (N+1 for a
  list); silently ignore `ownerId` for owners (harmless because the scope
  still applies, but hides misuse); counting recovery codes as enrolled
  (they are a fallback, not a factor, D-112).
- **Consequences:** maintainers can list e-mail addresses, display names,
  roles and the disabled flag of every active account (needed to assign
  owners; no password, lockout, 2FA, session or deletion state). Backup
  owners and anonymous callers cannot. The admin list costs two queries
  per page for the new fields. No trust boundary changes.
- **Related:** D-067, D-106, D-110, D-112, D-146, D-149, D-181; tasks 3.5, 6.8;
  `internal/graph/schema/users.graphqls`, `internal/graph/schema/jobs.graphqls`,
  `internal/graph/resolvers/users.go`, `internal/graph/resolvers/runs_loader.go`,
  `internal/auth/user_admin.go`, `internal/jobs/service.go`,
  `internal/db/queries/user_admin.sql`, `internal/db/queries/jobs.sql`.

## D-185: Restore wizard and calendar UI: proactive AAL2 step-up, request-only age identity, client-side calendar filters (tasks 6.7, 6.11)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (tasks 6.7, 6.11)
- **Context:** The restore API (D-167, D-168, D-171) and the calendar query
  (D-156, D-159, D-160) leave open how the UI orders the second-factor
  check, holds the age identity, gates privileged targets, shows the
  neutralized SECURITY DEFINER routines, and filters the calendar by
  connection and destination (the query only takes job ids).
- **Decision:**
  1. **Proactive AAL2 step-up.** "Verify and start the restore" always
     opens the step-up dialog with `aal2` (UV passkey or TOTP, D-116)
     *before* `restoreArtifact`/`restoreGlobals` is sent; the request is
     still wrapped in `withStepUp` for a late STEP_UP_REQUIRED. Accounts
     without TOTP or a passkey (`myTwoFactorStatus`/`myPasskeys`) and a
     REQUIRES_2FA_ENROLLMENT answer get an inline guide to
     `/profile/security`; the submit stays disabled.
  2. **Age identity in the request only.** The identity (pasted or read
     from a file of at most 16 KiB; only the `AGE-SECRET-KEY-1` line is
     kept) lives in wizard component state, is never in `restorePreview`,
     never in storage or the Apollo cache (`client.mutate`, `no-cache`),
     and is cleared once the request was sent (success or failure).
  3. **Privileged targets.** `acknowledgementRequired` blocks non-admins
     at the preview with an explanation; admins tick an acknowledgement
     sent as `acknowledgePrivilegedTarget`. Changing target, database or
     options resets the acknowledgement and the typed confirmation.
  4. **Warnings** are shown by severity: data loss or server-wide changes
     (`overwrite_in_place`, `overwrite_swap`, `globals_change_roles`,
     `privileged_target`) red and first; notes (`restore_hardening` with
     the GRANT CONNECT hint, `temporary_target`, `identity_required`)
     last. The neutralized routines are read from the restore log lines
     `switched to SECURITY INVOKER: ...` (internal/restore/postgres.go).
  5. **Calendar filters** map connection and destination to job ids from
     `jobs` on the client (`calendar(jobIds)`); a destination filter also
     hides expiries of copies on other destinations. View, date, zone and
     filters live in the URL; the zone defaults to the browser's. Views
     are Monday-first civil-date grids (WAI-ARIA grid, roving tab stop)
     computed with `Intl` (no calendar library); the range is at most 6
     weeks and clamped to 93 days. One `runUpdated` stream per page:
     known runs are updated in place, unknown ones trigger a debounced
     refetch.
- **Alternatives considered:** sending the restore first and stepping up
  on STEP_UP_REQUIRED (sends the identity twice and before the check);
  keeping the identity in react-hook-form or session storage; a calendar
  library (inline styles conflict with D-082, heavier); a server-side
  connection/destination filter (an API change for what the job list
  already answers).
- **Consequences:** Every restore costs one second-factor prompt even
  right after another one. Security impact: no new trust boundary; the
  identity's client-side lifetime is one request; privileged-target and
  confirmation rules stay enforced by the server. A change of the
  neutralization log wording must update `neutralizedFunctions`.
- **Related:** D-068, D-082, D-116, D-156, D-159, D-160, D-163, D-167,
  D-168, D-171, D-179; tasks 6.7, 6.11; `web/src/features/restore/`,
  `web/src/features/calendar/`, `web/scripts/csp-check.mjs`.

## D-186: Jobs and runs UI: start-early ledger, no-cache one-time keys and tickets, virtualized log tail, derived completeness evidence (tasks 6.5, 6.6)

- **Date:** 2026-10-28
- **Status:** proposed (item 5 per-table remark superseded by D-195)
- **Decided by:** agent (tasks 6.5, 6.6)
- **Context:** The job and run pages sit on the APIs of D-143, D-146,
  D-147, D-149, D-150, D-154, D-156/D-157/D-160 and D-184. The plan leaves
  open how "Run now" and "Start next planned run early" are told apart,
  where generated keys and download tickets live in the browser, how
  live logs stay cheap, and what the per-run completeness panel shows
  while the API exposes no per-table statistics.
- **Decision:**
  1. **Two actions, two dialogs.** "Run now (extra run)" says the
     schedule is unchanged. "Start next planned run early" shows a
     ledger correction: the planned occurrence struck through ("starts
     now") and the next planned run afterwards (staff: the first
     `previewSchedule` occurrence after `nextRunAt`; owners, who cannot
     preview: a generic line, then the server's new `nextRunAt` in the
     toast). Refusals stay in the dialog: CONFLICT "already active" →
     fixed text; VALIDATION "already started early" → the one-ahead
     rule (D-160); other codes (a disabled job: CONFLICT for runJobNow,
     VALIDATION for startNextRunEarly) show the server's message. Start
     early is disabled without a planned run.
  2. **One-time material is never cached.** createJob,
     generateJobPassphrase, generateAgeKeypair, exportJobPassphrases and
     createDownloadTicket run with `fetchPolicy: "no-cache"`; generated
     keys live only in component state until "I've saved it" (D-179
     item 8); the passphrase export downloads at once and is not shown.
     A pasted or chosen age identity is reduced to its single
     `AGE-SECRET-KEY-1` line, sent only in the ticket request and
     cleared after it; the ticket link is shown once with its 60 s
     countdown and hidden after the click.
  3. **Restore command.** The server's template gets the
     server-sanitized file name (shell-quoted unless plainly safe) and the
     database name only when it is a plain identifier; other
     placeholders stay.
  4. **Streams.** One `runUpdated` stream per page (job page and /runs:
     filtered by job or unfiltered, new runs refetch page 1; run page:
     by run id, only while active) plus, on an active run's page, one
     `runLogAppended` stream started after the stored lines
     (`runLogs` pages) from their last id; replayed lines are dropped by
     id. The log viewer renders only a fixed-height window (no library);
     "Follow" pins the tail, scrolling up pauses it.
  5. **Completeness evidence** is derived from what the API exposes:
     the exactness of the counts for the job's engine and level
     (PostgreSQL deep exact, standard estimates; MySQL/MariaDB always
     estimates, D-153), verified items, manifest coverage, completeness
     warning codes grouped across items, and the test restore of this
     run with its findings. Warnings and notices (D-155) are styled
     apart (amber, solid rule vs. neutral, dashed). Per-table stats are
     not in GraphQL; the panel points to `bekup verify`.
  6. **Owner picker** uses `userDirectory` (D-184) for admins and
     maintainers, narrowed to active role-USER accounts in the client.
  7. **Feature routes** (`features/jobs/routes.tsx`,
     `features/runs/routes.tsx`) come before the nav placeholders; the
     restore wizard is opened with `/restore?artifactId=<id>` (D-185).
- **Alternatives considered:** one "run" button with a mode switch
  (hides the schedule effect); Apollo cache eviction after showing a
  key (a window where it sits in the normalized cache); a virtualization
  library (one more dependency for a fixed-row list); subscribing to the
  log from the start for finished runs (an extra stream per page);
  inventing stats from warnings.
- **Consequences:** Download and key material never reaches the Apollo
  cache or devtools cache views. The completeness panel lacks object
  and row-count summaries until the API exposes `backup_stats` (e.g. a
  `RunItem.stats` field); a follow-up backend task is needed for the
  full 6.6 evidence summary. No trust boundary changes.
- **Related:** D-141, D-143, D-146, D-147, D-149, D-150, D-151, D-153,
  D-154, D-155, D-156, D-157, D-160, D-179, D-184, D-185; tasks 6.5, 6.6;
  `web/src/features/jobs/`, `web/src/features/runs/`,
  `web/scripts/csp-check.mjs`.

## D-187: RunItem.stats and stored last test results of connections and destinations (task 6.6, 6.3, 6.4 follow-up)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (follow-up of D-182 and D-186)
- **Context:** The M6 UI agents found two API gaps. The run page cannot
  show the completeness evidence of `backup_stats` (D-141, D-151, D-153),
  only derive it (D-186 item 5), and the connection and destination
  lists keep the last test result and privilege verdict only in the
  browser tab (D-182 item 5) because the API stores neither.
- **Decision:**
  1. **`RunItem.stats`** exposes the stored canonical stats object of an
     item, parsed into a typed view (`runstate.ParseItemStats`): level,
     consistency, object counts by kind, per-table rows with
     `count` (`EXACT`/`ESTIMATE`/`UNKNOWN`) plus `exact`/`estimate`/
     `unknown`/`approximate` flags (`rows` null when unknown), decoded
     table names, `tablesTotal`, `tablesTruncated`, and the findings split
     into `warnings` and `notices` (notice codes: `databases_excluded`).
     The parser repeats the writer's caps (10 000 tables, 32 findings, 50
     objects of ≤ 256 bytes, 4 MiB) and cuts beyond them; a malformed row
     reads as null (the signed manifest keeps the original). The field
     inherits the run's authorization (RunItem is reachable only through
     Run; owners see their own jobs' runs) and is batch-loaded per
     operation by the D-149 run loader: one `backup_stats` query per page
     of runs (`ListBackupStatsForRuns`), loaded only when requested.
  2. **Stored last test.** Migration 00029 adds `last_tested_at`,
     `last_test_ok`, `last_test_code`, `last_test_settings_at` to
     `connections` and `destinations`, and `last_test_privileges` /
     `last_test_privilege_reasons` (≤ 32 × 64 bytes, D-168 bounds) to
     `connections`, with CHECKs on completeness, the code syntax
     (`^[A-Za-z][A-Za-z0-9_]{0,63}$`) and the verdict values. `Service.Test`
     stores the outcome best effort (a failure is logged; the result is
     still returned): connections store the runner reason / serve code of a
     target failure, `INCOMPATIBLE_CLIENT`, or `TLS_PIN_MISMATCH` /
     `SSH_HOST_KEY_MISMATCH` for the security events, plus the probe's
     verdict when the probe got that far; destinations store the
     normalized class of the first failed step. Calls without a verdict
     on the target (rate limit, busy, runner unavailable, canceled, not
     ready) store nothing. Codes outside the syntax become `FAILED` /
     `failed`. Never a message or raw error.
  3. **No stale results.** The update is conditional on the row's
     `updated_at` as loaded by the test, so a test racing a settings
     change stores nothing; `last_test_settings_at` records the
     `updated_at` it ran against and `lastTest.stale` is true once they
     differ. The `set_updated_at` triggers of both tables gain a WHEN
     clause that skips updates changing only the last-test columns (with
     a new `last_tested_at`), so storing a result never bumps
     `updated_at`.
  4. **API.** `Connection.lastTest { at ok code stale privileges { status
     reasons acknowledgementRequired } }` and `Destination.lastTest { at ok
     code stale }`: metadata visible to admins and maintainers (the only
     roles that can read these types). Audit is unchanged: tests stay
     unaudited apart from the existing pin / host-key mismatch events. The
     stored verdict is informational only; restore gating keeps probing
     live (D-168, D-173).
- **Alternatives considered:** separate `connection_tests` /
  `destination_tests` tables (no trigger change, but a join on every
  list and two more tables for three columns); clearing the result on
  every update (loses the information that a test once passed); storing
  the full test result incl. messages and server version (more surface
  for little value; messages are fixed texts derivable from the code);
  returning `backup_stats` as a raw JSON scalar (untyped, unbounded for
  the client); paging tables per item (the stored cap already bounds a
  page; the field is loaded only when requested).
- **Consequences:** The run page can show the full evidence summary
  (D-186 follow-up) and the lists show the last test across reloads and
  users; the UI's session-only store (D-182 item 5) can be replaced. A
  page of runs that requests stats loads every stats object of the page
  (bounded by the caps and the page size). No trust boundary changes;
  `bekup_app` already had UPDATE on both tables. Security impact: only
  normalized codes and bounded reason codes are stored and shown, to the
  roles that already see the connection/destination metadata; owners
  read stats only for runs they may already see.
- **Related:** D-141, D-149, D-151, D-153, D-155, D-131, D-135, D-168,
  D-173, D-182, D-186; `internal/db/migrations/00029_target_last_test.sql`,
  `internal/runstate/stats.go`, `internal/connections/probe.go`,
  `internal/destinations/open.go`, `internal/graph/resolvers/runs_read.go`,
  `internal/graph/schema/{runs,connections,destinations}.graphqls`.


## D-188: Missed-backup (dead-man) alerts: recorded occurrences, derived window, one alert per outage with daily reminders (task 5.4)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 5.4)
- **Context:** The plan asks for missed-backup alerts from
  `jobs.expected_window_seconds` (D-146: NULL = derived), emitted as the
  catalog event `backup.missed` (D-161) through the outbox (D-183). Open
  were what "missed" means, the default window, which occurrences are
  judged, how skips, partial runs and early runs count, how often a broken
  job alerts, and how the scan stays bounded.
- **Decision:**
  1. **Watched jobs:** enabled, not deleted, schedule not manual (a stored
     schedule that does not compile stays watched). A periodic River job
     `missed_backup_check` (maintenance queue, unique, every 5 minutes,
     `internal/notify/missed`) scans them in keyset batches of 200 with
     three set-based queries per batch (no per-job queries except for
     reminders).
  2. **Occurrences come from what the scheduler recorded, not from a
     replay of the schedule:** scheduled, catch-up and early runs by
     `scheduled_for`, `schedule_skips` rows (`still_running`,
     `unschedulable`), and the stored `next_run_at` once its window has
     passed (`not_enqueued`: the scheduler is not running or is stuck; this
     one is judged whatever its age). A replay cannot tell occurrences
     before the job was enabled from missed ones (no enable timestamp
     exists) and would share any bug of the scheduler math it is meant to
     catch. Occurrences collapsed into one catch-up run are judged through
     that run. Only occurrences whose deadline (occurrence + window) lies in
     the last 7 days are read (at most 2,500 newest per job).
  3. **Covered vs missed:** an occurrence T is covered when its own run
     reached `success` or `partial` by T + window (also an early run that
     finished before T) or any run of the job (scheduled, manual, early)
     finished `success`/`partial` in (T, T + window]. **Partial counts as
     not missed** (the job ran; the failed databases have `backup.partial`).
     **A skip because the previous run was still running is not a miss if
     that run succeeds within the skipped occurrence's window**; failed,
     canceled, still queued/running and late runs are misses (reason
     `run_failed`, `run_canceled`, `run_queued`, `run_running`,
     `finished_late`, `skipped_still_running`, `unschedulable`,
     `not_enqueued`).
  4. **Default window** (expected_window_seconds NULL): the longest a
     backup may legitimately take: `MaxAttemptsBackup` (2) times the job's
     timeout (`timeout_seconds`, else `workers.backup_timeout`) plus one
     hour of grace (queue wait, backoff, clock skew); 13 h with the
     defaults. It is independent of the schedule interval (no alert storms
     for frequent schedules); operators who want faster alerts set the
     window per job.
  5. **One alert per outage, daily reminders, no recovery event:**
     consecutive missed occurrences (not separated by a success) form one
     outage identified by the last successful run before it ("never" when
     the job never succeeded). Its first notice has dedup key
     `backup.missed:<job>:after:<run|never>`, so re-runs and concurrent
     checks never duplicate it; while the outage lasts (no success since
     its last missed occurrence) one reminder per 24 h follows
     (`…:r<n>`, n = whole days since the first notice). A recovered
     outage first seen late (e.g. after downtime) is still notified once.
     No `backup.recovered` event (not in the catalog): the next
     `backup.succeeded` is the recovery notice. Detail: `scheduled_for`,
     `last_scheduled_for`, `missed`, `window_seconds`, `reason`, `ongoing`,
     `reminder` (fixed values, no free text).
  6. **Stuck runs** are not a separate check: River's rescuer and runstate
     finish backup runs that outlive their timeout; a run still `queued` or
     `running` after the window shows up as a miss (`run_queued` /
     `run_running`).
  7. Events are emitted with `notify.Emit` in one transaction per job;
     fan-out, recipients (`run.view` on the job) and channels follow D-183.
- **Alternatives considered:** replaying the schedule with
  `scheduler.Plan` from a lookback start (false alerts for occurrences
  before a job was (re-)enabled; would need a new `jobs` column); one event
  per missed occurrence as literally planned (hundreds of inbox entries a
  day for a broken 5-minute job); a window derived from the schedule
  interval ("before the next occurrence": too slow for daily jobs, too
  noisy for minute schedules); counting a failed run as "ran" (a dead-man
  should fire when no usable backup exists); a separate stuck-run sweep for
  backups (duplicates River's rescuer).
- **Consequences:** a broken job alerts once and then daily until a backup
  succeeds. A job that missed occurrences, was disabled and is re-enabled
  within 7 days may get one late notice for the old outage. Alerts depend
  on a worker process running (`serve` with workers or `bekup worker`);
  external monitoring of `/metrics` (oldest last success, task 5.6) stays
  recommended. Security impact: none on trust boundaries; events carry
  identifiers and fixed values only; the detector reads with `bekup_app`'s
  existing SELECT grants and writes only outbox rows.
- **Related:** D-143, D-145, D-146, D-147, D-157, D-161, D-176, D-183;
  task 5.4; `internal/notify/missed`, `internal/db/queries/missed.sql`,
  `internal/queue/kinds.go`, `docs/guides/notifications.md`,
  `docs/guides/jobs-and-schedules.md`, SECURITY.md SC-3, SC-5.


## D-190: Security alerts: audit-driven, mandatory, deduplicated; burst detection; hourly security checks; CLI alerts via the outbox (task 5.5)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 5.5)
- **Context:** Task 5.5 delivers the security alerts the earlier tasks
  left as log-only hooks (`AlertFunc`s of connections, jobs, jobkeys,
  restore and retention, the Nop notifiers of internal/auth and passkeys,
  "alert follows in 5.5" notes at startup and in the CLI). D-161 makes
  `security.account` / `security.system` mandatory (inbox + e-mail);
  D-183 introduced `notify.Emit`, the audit observer and the outbox, and
  noted that the CLI auditor had no observer. Open were which component
  emits, how repeating alerts are bounded, how failed-login bursts and
  conditions without an action (certificate expiry, audit chain damage)
  are detected, and how break-glass CLI actions reach the admins.
- **Decision:**
  1. **The audit entry is the single source.** Every security event is
     already audited (fail closed), so `notify.ObserveAudit` maps the
     audited action to the alert in the audit entry's transaction. The
     post-commit hooks keep their defaults (log line / no-op); emitting
     from them too would duplicate alerts. Gaps were closed by auditing:
     a manifest verification failure at restore-request time is now
     audited as `restore.verification_failed` (target artifact).
  2. **Mapping** (`internal/notify/security.go`, on top of D-183):
     `security.system` for opt-outs (incl. TLS downgrades), pin and SSH /
     SFTP host-key mismatches, new admins (API and CLI), admin grants,
     job owner changes (D-067), passphrase exports, key/certificate
     rotation, instance keys, restore verification failures, acknowledged
     privileged restore and test-restore targets, privileged-target
     refusals, SECURITY DEFINER neutralization, unexpected artifact
     deletions, aborted retention sweeps, a weakened 2FA policy, passkey
     sign-counter anomalies, rejected download tickets, plain HTTP /
     weakened app-DB TLS at startup (D-095, D-100), audit head export and
     chain verification failures. `security.account` additionally for
     passkey counter anomalies and the CLI password / 2FA resets. Each
     system kind has a fixed severity (info … critical). Details are fixed
     facts only (kind, ids, address, counts, dates; for CLI events the
     action, OS user and host name); never audit details wholesale.
  3. **Deduplication** (outbox dedup keys): posture alerts once per UTC
     day (shared by the startup entry and the reminder); pin / host-key
     mismatches once per object and day; aborted retention once per job,
     destination and day; a rejected download ticket once per ticket; an
     unexpected deletion once per artifact; an audit verification finding
     once per finding.
  4. **Failed-login bursts:** after every `auth.login.failed` entry the
     observer counts failures in the last 15 minutes (one indexed query):
     ≥ 20 from one address (across accounts), ≥ 10 on one existing account
     (any address) or ≥ 100 in total raise one `security.system` alert per
     address / account / instance and 15-minute bucket. The content names
     the address or the account id only, never a submitted e-mail address
     (no enumeration). Thresholds are constants (no new configuration).
  5. **Hourly security check** (River `security_check`, maintenance queue,
     `internal/monitor.SecurityChecker`): internal certificates (runner
     client/CA, app-DB CA/client, runner server via its Health RPC) and
     pinned target-DB certificates alert once at ≤ 30 days, once at ≤ 7
     days and once when expired (dedup per object, expiry and threshold; a
     renewed certificate alerts again); the audit chain is verified
     incrementally from the last verified row, anchored on the remembered
     hash (a rewrite of the verified part is caught), fully at the first
     run of a process and then daily; a failure is recorded once per
     finding as `audit.verify.failed` (critical alert through the
     observer); weakened posture gets a daily reminder. The checkpoint is
     kept in memory only (a database-stored checkpoint could be forged
     together with the rows).
  6. **CLI:** `bekup admin|certs|audit sign-head|reindex --import` use an
     auditor with `notify.ObserveAudit`, so each CLI entry writes its
     `security.system` alert (D-069; generic `cli_action` when no more
     specific kind applies) to the outbox in the same transaction; a
     running serve's dispatcher delivers it. An outbox failure fails the
     command like an audit failure.
  7. **Mandatory:** both security events keep inbox + e-mail regardless of
     preferences (D-161, prefs.go), are exempt from the flood rule and
     never go to shared channels (D-183).
- **Alternatives considered:** emitting from each post-commit hook
  (duplicates the observer, loses atomicity, and several events have no
  hook); a per-event audience of "affected user" for burst alerts (would
  tell an attacker-chosen account about attempts but not the admins);
  storing the audit verification checkpoint in `settings` (forgeable by
  the same write access that tampers rows); configurable burst thresholds
  (more surface, no clear need yet); alerting every repeated mismatch
  (alert floods from hourly backups of a broken pin).
- **Consequences:** every audited security action also writes an outbox
  row in its transaction; a failed-login attempt costs one extra counting
  query. Alerts arrive seconds after the action (dispatcher). Repeated
  conditions are bounded by the dedup keys. Security impact: admins learn
  of weakened settings, impersonation signs, tampering, break-glass use and
  credential attacks through two channels; alert content stays free of
  secrets, connection strings, codes and submitted e-mail addresses; no new
  trust boundary (the CLI already wrote to the app DB as `bekup_app`).
- **Related:** D-001, D-067, D-068, D-069, D-095, D-100, D-109, D-112,
  D-150, D-161, D-171, D-173, D-176, D-183; task 5.5;
  `internal/notify/security.go`, `internal/notify/audit.go`,
  `internal/monitor/security.go`, `internal/app/security.go`,
  `cmd/bekup/cli_audit.go`, `internal/db/queries/security_alerts.sql`,
  `docs/guides/notifications.md`, `docs/security/audit.md`, SECURITY.md.

## D-191: Notifications API: shared and personal channels, the preference matrix, the inbox and its live stream (task 5.3b)

- **Date:** 2026-10-28
- **Status:** proposed (item 7 superseded by D-197; push in item 5 superseded by D-206)
- **Decided by:** agent (task 5.3b)
- **Context:** D-183 delivered the notification core (tables, fan-out,
  delivery) and left the GraphQL API to 5.3b: shared channels with job
  attachments for admins, personal channels and the event × channel
  preference matrix for every user, the in-app inbox and a live
  `notificationReceived` stream. Open were the operations and their
  freshness, how keys and URLs are shown, how the matrix is shaped, how the
  stream learns about new rows and the audit trail.
- **Decision:**
  1. **Service** `notify.API` (serve only) behind the resolvers; every
     method takes the caller from the context and re-authorizes its
     operation (defense in depth behind the root-field gate); personal
     queries are always scoped by `user_id` in SQL
     (`internal/db/queries/notify_api.sql`).
  2. **Operations:** shared channels `notification.shared_channel_list`,
     `_edit` (create, update, rotate key, delete: admin + **step-up**,
     they carry a URL or key), new `_toggle` (enable/disable, admin, no
     step-up) and `_test`; attachments `job.shared_channels_edit` (admin);
     own channels new `notification.personal_channel_list`,
     `personal_channel_edit` (add, rotate, delete: every user + step-up,
     a webhook is an exfiltration path for a stolen session), new
     `personal_channel_enable` (re-enable after an auto-disable, no
     step-up) and `personal_channel_test`; new `notification.prefs_view`
     (catalog, matrix) and `prefs_edit` (set, reset);
     `notification.inbox` (list, unread count, mark read, delete,
     stream). Restricted principals get none of them.
  3. **Webhooks:** URLs only through `webhook.ValidateURL` (fixed error
     messages, never echoing the URL), sealed under
     `<table>:<id>:url`; the API shows only `DisplayURL`. Signing keys only
     from `webhook.GenerateHMACKey`, sealed under `:hmac_key`, returned
     **once** (`signingKey` on create/rotate) and zeroed after the
     response is built; never listed. Max 5 personal webhooks (the D-183
     trigger, mapped to VALIDATION). Test sends (`testSharedChannel`,
     `testMyWebhook`) are single-root, rate limited per user, work on
     disabled channels, return fixed codes and the HTTP status only, and
     never count towards the auto-disable policy. Re-enabling resets the
     failure counters. E-mail lists: 1..50 addresses checked with
     `email.CheckAddress`, de-duplicated; their test sends one message to
     all addresses.
  4. **Attachment filters:** groups and events of the catalog only, never
     the security group/events, and only job events (shared channels only
     receive events of the jobs they are attached to); both empty = every
     non-security event; re-attaching replaces the filter.
  5. **Catalog and matrix:** `notificationCatalog` lists the D-161 groups
     with audience, problem, mandatory, priority, severity, perJob and the
     default channels, filtered by the caller's role (job and own-account
     events for everyone, staff events for admins and maintainers,
     `security.system` for admins). `myNotificationPreferences` returns per
     visible event the effective INBOX / EMAIL / WEBHOOK cells with their
     source (DEFAULT, OVERRIDE, MANDATORY; mandatory cells locked) and the
     per-job overrides of jobs the caller may still view (overrides of
     lost jobs stay stored but are hidden and never apply, as the fan-out
     re-checks access). `setNotificationPreference` refuses mandatory
     INBOX/EMAIL (VALIDATION), events the role cannot receive, and jobs
     the caller cannot view (NOT_FOUND); a job override needs a job event.
     `resetNotificationPreferences(jobId?)` deletes all stored rows (back
     to D-161) or the overrides of one job. Push stays out of the API
     until phase 2. Preference changes are not audited (own, non-security
     settings).
  6. **Inbox:** cursor pagination `(created_at, id)` newest first (1..200,
     default 50), `unreadOnly`, `markNotificationsRead(ids ≤ 200 | all)`
     returning the remaining unread count, `deleteNotification`; rows of
     other users are invisible (NOT_FOUND / zero changes).
  7. **Live stream:** the fan-out (and the auto-disable notice) announces
     each inserted inbox row on the new pubsub channel
     `bekup_notification_events` (fixed `LISTEN` query) as
     `inbox:<notification id>:<user id>` inside the inserting transaction.
     `notificationReceived` pre-filters by the stream's user, loads the row
     **scoped to that user**, retries a few times when the local bus
     announced it before the commit, de-duplicates (bus + NOTIFY), resyncs
     after possibly lost messages, and `streams.Deliver` re-authorizes
     every event (`notification.inbox`, the stream's current principal
     must own the row); logout or revocation ends the stream.
  8. **Audit:** `notification.channel_created|updated|deleted|key_rotated|
     enabled|disabled|tested|attached|detached` (targets `shared_channels`
     / `user_channels`, details with the display URL, changed field names,
     result codes; never a URL path or key), plus `secret.changed` by name
     when a URL or key is set.
- **Alternatives considered:** returning keys again on demand (a stored
  key would have to be re-readable: refused); step-up for every
  personal-channel change including re-enable (re-enabling resumes an
  already approved URL); counting test sends towards auto-disable (a user
  could disable a shared channel by testing); a stream that re-queries the
  inbox on every hint without IDs (more load, no dedup); auditing every
  preference toggle (noise without security value).
- **Consequences:** the web UI (6.9, 6.10) has everything it needs. A lost
  signing key means rotating it. Security impact: no webhook URL path or
  signing key leaves the server after creation; every personal operation
  is bound to the caller in SQL and re-authorized; a forged or misrouted
  pubsub message cannot reveal another user's notification; shared
  channels never receive security alerts; new webhook URLs pass the SSRF
  guard at input and every dial.
- **Related:** D-023, D-051, D-106, D-108, D-117, D-161, D-177, D-178,
  D-183; tasks 5.3b, 6.9, 6.10; `internal/notify/api*.go`,
  `internal/graph/schema/notifications.graphqls`,
  `internal/graph/resolvers/notifications.go`, `internal/pubsub`,
  `docs/guides/notifications.md`, SECURITY.md NT-*.


## D-192: Dashboard UI: one run stream, polled restores, client-derived health and security findings, bounded storage sums (task 6.2)

- **Date:** 2026-10-28
- **Status:** proposed (items 1, 2 ("missed" derivation) and 4 superseded by D-196; item 1 stream superseded by D-197)
- **Decided by:** agent (task 6.2)
- **Context:** The dashboard shows job health, live runs and restores,
  failures and missed backups, the next 24 hours, alerts, and for staff
  security findings and storage per destination, for admins system health.
  The API has no dashboard aggregate: no per-job "missed" flag, no
  unfiltered restore stream, no storage sum per destination, and the
  session's stream cap (`graphql.stream_max_per_session`, default 4)
  limits live subscriptions.
- **Decision:**
  1. **One stream.** The page opens exactly one unfiltered `runUpdated`
     stream (D-149) and merges its events client-side into the active runs,
     the failures list and each job's latest run. Active **restores are
     polled** (`restores(first: 20)`, every 15 s): `restoreUpdated` is per
     restore and would spend the stream cap.
  2. **Job health is derived in the UI** (`features/dashboard/model.ts`)
     from `jobs { lastRun nextRunAt enabled encryption }` plus two bounded
     calendar queries: [now, now + 24 h) for upcoming and `overdue`
     occurrences, [now − 7 d, now) for `SKIPPED` occurrences. A job is
     "missed" when its newest skip is newer than its last run, "overdue"
     when its next run is more than 10 minutes in the past (or the calendar
     flags it). These are display hints; the authoritative dead-man alert
     stays `backup.missed` (D-188). Owners get exactly what the server
     returns for their role (no client-side owner filter).
  3. **Security findings are derived** from data staff already read:
     unencrypted jobs (opt-out or mode NONE), insecure transport (TLS
     UNVERIFIED/DISABLED), privileged or unknown target roles from the
     stored `lastTest.privileges` (D-187; marked when stale), expired /
     expiring pins (`pinStatus`), and identity mismatches
     (`lastTest.code` TLS_PIN_MISMATCH / SSH_HOST_KEY_MISMATCH, not stale)
     as critical. Weakened settings use the reserved hazard hatch (D-001).
  4. **Storage per destination** sums `artifacts(state: PRESENT)` pages on
     the client, at most 5 pages of 200 (1,000 objects); beyond that the
     figures are shown as lower bounds ("≥") with a note. Charts are SVG
     with geometry in attributes and colors in classes (CSP `style-src
     'self'`, D-082).
  5. **Alerts** use `unreadNotificationCount` and the 5 newest unread
     notifications (D-191); a notification `link` is followed only when it
     is an in-app path (`/…`, not `//` or `/\`).
- **Alternatives considered:** a server-side `dashboard` query with
  per-job health and storage sums (best long-term; not in the API yet, see
  consequences); one `restoreUpdated` stream per active restore (exhausts
  the stream cap); deriving "missed" by replaying schedules in the browser
  (the same pitfalls D-188 rejected); reading the inbox for `backup.missed`
  (depends on each user's preferences).
- **Consequences:** No new API surface and no trust boundary change; every
  query is an existing, authorized operation and the staff/admin tiles are
  requested only for those roles (the server still decides). The page
  costs a handful of queries per load (up to 5 extra artifact pages for
  staff) and polls restores/alerts/system health. Gaps to close later:
  a server aggregate for bytes per destination (and per-job "missed" from
  the D-188 checker) would make the storage tile exact and drop the
  paging. Security impact: none beyond showing staff metadata they can
  already read; server-provided links are restricted to in-app paths.
- **Related:** D-001, D-082, D-149, D-156, D-161, D-176, D-187, D-188,
  D-191; `web/src/features/dashboard/`.

## D-193: Notification center UI: one inbox stream in the bell, in-app link allowlist, memory-only verification code, no-cache signing keys (task 6.10)

- **Date:** 2026-10-28
- **Status:** proposed (item 1 superseded by D-197; push column in item 5 superseded by D-206)
- **Decided by:** agent (task 6.10)
- **Context:** Task 6.10 adds the bell, the inbox page, the personal
  notification settings and the `/verify-email#code=…` page on top of the
  D-191 API. Open were where the live stream lives, how server links are
  followed, how the e-mail code survives a sign-in without leaking, and how
  one-time webhook keys are kept out of the client's state.
- **Decision:**
  1. **One stream per tab:** the top-bar bell owns the only
     `notificationReceived` subscription (keyed on the session epoch like
     the session watcher). Arrivals update the bell's cached query (count +
     latest 8, de-duplicated by id) and reload the inbox page only when it
     is on screen; error/critical arrivals also toast, all arrivals are
     announced in a polite live region.
  2. **Links:** a notification's absolute `link` is reduced to its path and
     followed only when the path matches a fixed allowlist of app pages
     (`/runs|jobs|restores|connections|destinations[/<uuid>]`,
     `/profile[/x]`, `/admin/x`); anything else gets no link. The UI never
     navigates to another origin, even when `base_url` differs.
  3. **`/verify-email`** sits outside the session gate (the gate puts the
     full URL, fragment included, into the login return path). The page
     reads the code from the fragment before any request, replaces the
     history entry with a fragment-less URL, and sends the code only in the
     `verifyEmail` POST body. Signed out, it offers sign-in with
     `next=/verify-email`; the code waits in module memory for 15 minutes
     (never storage, never a URL) and is dropped after use or a VALIDATION
     answer.
  4. **Signing keys:** `addMyWebhook` / `rotateMyWebhookKey` run with
     `fetchPolicy: "no-cache"`; the key lives only in the dialog's state
     and is gone once the user confirms having stored it (same pattern as
     temporary passwords, D-110/D-186). The dialog links to the signature
     check in `docs/guides/notifications.md` on GitHub.
  5. **Matrix:** the per-event matrix is held as server data plus answers
     of `setNotificationPreference` applied locally; the push column is
     shown disabled until phase 2; locked cells are disabled switches with
     a lock and the security group carries the mandatory-alert note.
     Webhook URLs get early client feedback on the D-177 rules (http only
     when the server reports dev mode); the SSRF guard stays server-side.
- **Alternatives considered:** a subscription per page (more streams, D-124);
  following `link` as-is (open redirect if `base_url` is wrong or
  hostile); keeping the verification code in `sessionStorage` across the
  sign-in (persists beyond the tab's memory); putting `/verify-email` inside
  the session gate (the code would land in `?next=`).
- **Consequences:** a server link to a page outside the allowlist shows no
  link until the allowlist is extended. A full page reload during the
  sign-in detour loses the code (open the link again). Security impact:
  the verification code never appears in a URL, history entry or request
  line after the first paint; one-time keys never enter the Apollo cache;
  notification links cannot leave the app.
- **Related:** D-023, D-051, D-110, D-124, D-161, D-177, D-178, D-186,
  D-191; `web/src/features/notifications/`.

## D-194: Admin settings UI: settings hub, client-filtered security alerts, audit chain status from audit.verify.failed, read-only SSRF allowlist, guarded passphrase export (task 6.9)

- **Date:** 2026-10-28
- **Status:** proposed (items 2, 3 and 4 superseded by D-196)
- **Decided by:** agent (task 6.9)
- **Context:** Task 6.9 builds the admin pages for SMTP, shared channels,
  security alerts, the audit log with its chain status, the SSRF allowlist
  and the passphrase export. Three of them have no dedicated API: the inbox
  has no type filter (D-191), no field reports the last successful audit
  chain verification (D-190 keeps the checkpoint in memory only), and the
  runtime allowlist of D-120 has no GraphQL operations.
- **Decision:**
  1. **Layout.** `/admin/channels` and `/admin/audit` are their own pages;
     `/admin/settings` is a hub with a section rail (SMTP as the index,
     `security-alerts`, `network`, `recovery`; one URL each) linking the
     2FA policy. All routes sit behind `RequireAdmin` (UI only; the server
     enforces `@hasRole`).
  2. **Security alerts** are the admin's own inbox filtered on the client
     to `security.*` (or group SECURITY), 200 entries per page with a
     severity filter, plus the alert-type reference table from
     docs/guides/notifications.md. Links are reduced to known in-app paths.
  3. **Audit chain status** is derived from the newest
     `audit.verify.failed` entry (`auditLog(filter: {action}, first: 1)`):
     "no failure recorded" or "failed <when>", with `bekup audit verify` as
     the offline proof. The page never claims a successful verification.
  4. **SSRF allowlist** is shown read-only: an explanation that it is
     managed in `[netguard]` of bekup.toml (with the hard-deny and
     allowlistable ranges); its values are not displayed (no API).
  5. **Write-only and one-time values.** The SMTP password is never sent
     back and must be re-entered when host, port or username change;
     signing keys of `createSharedChannel`/`rotateSharedChannelKey` and the
     passphrase export use `no-cache` and live only in component state; a
     key is shown once behind an acknowledgement, the export downloads
     straight to a file after a warning, an explicit acknowledgement and
     step-up (never rendered). The event filter of shared-channel
     attachments only offers per-job, non-mandatory, non-security events
     and drops security groups/events from the input.
  6. AUTO/NONE SMTP modes show the hazard-hatched "E-mail may be sent
     unencrypted" badge (live while editing and from `mayBeUnencrypted`).
- **Alternatives considered:** server-side inbox type filter and chain/
  allowlist APIs (out of scope for a UI task; recorded as gaps); showing
  the allowlist from the config (no API exposes it); rendering the export
  content for copy (more exposure than a file download).
- **Consequences:** the alerts view may need paging to find older alerts;
  the chain status cannot show the time of the last successful check; the
  allowlist is changed on the host only. Security impact: no new API
  surface; secrets stay out of the cache and the DOM except the one-time
  key dialog; links cannot leave the app.
- **Related:** D-001, D-055, D-101, D-120, D-142, D-154, D-161, D-177,
  D-178, D-180, D-190, D-191, D-193; `web/src/features/admin/`.

## D-195: M6 UI follow-ups: stored test results, per-item statistics, shared owner picker, test-restore pages and settings, stable web tests (task 6.12)

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (task 6.12 follow-ups)
- **Context:** D-184 and D-187 added the API the M6 pages lacked
  (userDirectory, User.lastSeenAt/twoFactorEnrolled, JobFilter.ownerId,
  RunItem.stats, stored lastTest). The UI still kept test results per tab
  (D-182 item 5), derived 2FA enrollment from the policy and scanned the
  first 200 jobs for a user's owned jobs (D-181), pointed to `bekup verify`
  for per-table statistics (D-186 item 5) and had no test-restore history
  or settings UI. Web tests timed out under parallel load.
- **Decision:**
  1. **Stored test results.** Connection and destination lists show the
     stored `lastTest` (Passed/Failed, the normalized code, the time, a
     Stale marker) and the connection list the stored privilege verdict;
     detail pages show it until a test runs on the page, then the full
     result of that test (component state only) and refetch the stored one
     as a separate request (D-117). The per-tab store `lib/session-results`
     is removed.
  2. **Per-item statistics.** The run page loads `RunItem.stats` in its own
     request once the run is no longer active and shows per database the
     level and consistency, object counts, warnings (amber, solid) and
     notices (neutral, dashed) with their object names, the truncation
     notice, and the per-table rows with exact / estimate / unknown /
     approximate badges (50 rows, then "show all", with a name filter).
  3. **Users.** The admin list and user page use `lastSeenAt` and
     `twoFactorEnrolled` (enrolled, grace, overdue, none); owned jobs come
     from `jobs(filter: {ownerId})` with "show more", and the assign dialog
     searches jobs server-side.
  4. **One owner picker.** `features/users/UserPicker` reads
     `userDirectory` (admins and maintainers) and is used by the job owners
     panel: only active role-User accounts are selectable (D-067), current
     owners and (for maintainers) the caller are not offered, and the chosen
     accounts are granted in one `assignJobOwners` call behind the step-up.
  5. **Test restores.** The job page shows the latest verdict with a diff
     summary (findings by code, FAIL first) and links to
     `/jobs/:id/test-restores` (history, paged) and
     `/jobs/:id/test-restores/:testRestoreId` (databases with every finding);
     staff get "Run test restore now". The job editor's test-restore
     settings send `setJobTestRestore` behind an AAL2 step-up; scratch
     targets are permanent connections of the job's engine; the verdict
     shown is the designation's (current target) or the connection's
     stored last test; admins tick the privileged-target acknowledgement
     whenever the verdict is privileged, unknown or not yet known (the
     server probes and decides), maintainers see that such a target needs
     an admin. The stored verdict stays informational (D-187).
  6. **Titles.** The top bar matches nav items by path segment and has
     explicit titles for /restore, new/edit pages, test restores and
     /notifications.
  7. **Tests.** findBy/waitFor poll up to 10 s (`asyncUtilTimeout`), tests
     and hooks get 30 s, vitest uses at most half the cores; optional finds
     that may never match and synchronous asserts on effect-driven state
     are replaced by waitFor.
- **Alternatives considered:** keeping the per-tab store next to the
  stored result (two sources that disagree after a reload); embedding
  stats in RunDetail (every live run update would carry 10 000 tables);
  a separate owner picker in the jobs feature (two components with
  different role rules); showing the acknowledgement only after a server
  VALIDATION (a second step-up round trip).
- **Consequences:** Test outcomes survive reloads and are shared between
  staff; no messages or raw errors are shown from storage. Maintainers can
  assign owners themselves. The test-restore settings expose no new data:
  the verdict and reasons were already visible to staff. Security impact:
  none beyond D-184/D-187; the step-up, acknowledgement and role rules
  stay enforced by the server. Supersedes D-182 item 5, D-186 item 5's
  per-table remark and D-181's derived 2FA state and admin-only picker.
- **Related:** D-067, D-117, D-169, D-170, D-181, D-182, D-184, D-186,
  D-187; task 6.12; `web/src/features/{connections,destinations,runs,jobs,users,restore}/`,
  `web/src/components/shell/nav.ts`, `web/vite.config.ts`, `web/src/test/setup.ts`.

## D-196: Runtime SSRF allowlist API, inbox filter, audit chain status and dashboard aggregates (task 6.12 follow-ups)

- **Date:** 2026-10-28
- **Status:** proposed (item 8 dashboard streams superseded by D-197)
- **Decided by:** agent (task 6.12 follow-ups)
- **Context:** The M6 UI agents worked around missing API (D-192, D-194):
  the admin allowlist of D-120 had no operations (the plan puts it in
  `settings`, admin + step-up + audited), the inbox could only be filtered
  on the client, the audit chain status was guessed from the newest
  `audit.verify.failed` entry (the D-190 checkpoint lives in memory), and
  the dashboard paged artifacts to sum storage, derived "missed" from the
  calendar and polled restores because `restoreUpdated` is per restore.
  serve built three separate SSRF guards (destinations, SMTP, webhooks),
  so a runtime list would have had to be applied three times.
- **Decision:**
  1. **One guard per process; admin list in `settings`.** serve and
     `bekup worker` build one `*netguard.Guard` (`[netguard]` bootstrap,
     loopback allowlistable only in dev) shared by destinations, SMTP and
     webhooks. The admin list is stored as `settings`
     `netguard.allowlist` (`{"version":1,"cidrs":[…],"hosts":[…]}`,
     canonical forms) and managed only through the new package
     `internal/egress`.
  2. **API.** `Query.ssrfAllowlist` (admin; operation `settings.view`)
     shows the admin and the read-only bootstrap entries, `loopbackAllowed`,
     `ignoredEntries`, `maxEntries` (256 per list), `updatedAt/By`.
     `Mutation.updateSsrfAllowlist` (admin + step-up; `settings.update`,
     re-checked in the service) replaces both lists. Every entry is
     validated with the guard's own parser (`Guard.NormalizeAllowlist`:
     no /0, nothing overlapping an always-denied range, IP literals only
     as CIDRs, `*.suffix` with two labels, loopback only in dev mode,
     entries ≤ 260 bytes); the first rejected entry answers VALIDATION with
     field `cidrs.<i>` / `hosts.<i>`. Optimistic concurrency:
     `expectedUpdatedAt` must match (CONFLICT otherwise, also when a list
     exists and none was given).
  3. **Audit and alert.** The change is recorded as `setting.changed`
     (target `setting`/`netguard.allowlist`; `added_cidrs`,
     `removed_cidrs`, `added_hosts`, `removed_hosts`, `widened`) in the
     same transaction; `widened` (anything added) raises the mandatory
     `security.system` alert `ssrf_allowlist_widened` (severity error,
     every time). Narrowing is audited only.
  4. **Propagation.** The transaction also publishes the IDs-only
     settings event `pubsub.SettingsEvents` (`bekup_settings_events`,
     payload = the settings key). Every process runs `egress.Sync`: it
     applies the stored list before the components start (a read error
     stops startup), reloads on every event and on listener resyncs, and
     every 5 minutes. serve's listener carries the new channel;
     `bekup worker` opens its own LISTEN connection for it
     (`bekup-worker-listen`). Stored entries a process rejects, or an
     undecodable value, are left out and counted as `ignoredEntries`
     (fail closed: the effective list only gets narrower).
  5. **Inbox filter.** `notifications(filter: {unreadOnly, group,
     events})` (the old `unreadOnly` argument stays; either set is
     enough). The server resolves group/events to catalog types (plus
     `notice.webhook_disabled`; unknown types are VALIDATION
     `filter.events.<i>`, at most 64) and filters in SQL; group and events
     together must both match.
  6. **Audit chain status.** The hourly security check stores every
     completed verification in `settings` `audit.chain_verification`
     (time, ok, full/incremental, the finding `<kind>:<row id>`, the newest
     verified row; no actor). `Query.auditChainStatus` (admin;
     `audit.chain_status_view`) returns it with the newest audit row
     (id, hash, time) and the newest signed head (chain id, time, key id).
     It is informational only (same database as the rows); the in-memory
     checkpoint of D-190 is unchanged and `bekup audit verify` against an
     exported head stays the proof.
  7. **Dashboard aggregates.** `Query.destinationUsage` (staff;
     `destination.usage_view`): per destination not deleted, the sum of
     `size` and the count of `present` artifacts, one grouped query
     (partial index `artifacts_destination_present_idx`).
     `Job.missed` / `Job.missedSince`: the D-188 detector now records each
     job's ongoing outage in `job_missed_state` (migration 00030; upsert
     per batch, cleared when it ends, pruned for unwatched jobs);
     readers batch-load it per page of jobs and treat a successful or
     partial run finished after the detector's `checked_at` as the end of
     the outage. `Subscription.restoresUpdated` (staff all, owners their
     jobs'; `restore.view`) streams every restore change: events coalesced
     per restore (≤ 100 pending, ≤ 20 deliveries/s), a resync scoped like
     `restores` after possibly lost events, each event re-loaded and
     re-authorized at delivery.
  8. **UI.** The admin Network page edits the admin lists (bootstrap
     entries read-only, client hints only) behind the step-up, maps field
     errors to rows and offers a reload on CONFLICT; a save that adds
     entries lists them in a hazard-style warning (D-001) and stays
     disabled until the admin acknowledges it. The security-alerts view
     asks the server for group SECURITY (optionally unread only; the
     severity filter applies to the loaded page). The audit page shows
     `auditChainStatus` and never claims a check that did not run. The
     dashboard uses `destinationUsage`, `Job.missed` (a live successful
     run clears the flag) and one `restoresUpdated` stream next to the
     `runUpdated` one: two streams per page, no restore polling, no
     client sums, no calendar-derived "missed".
- **Alternatives considered:** keeping the list in bekup.toml only (no
  audit or alert of changes, needs host access and a restart); applying
  the list to three guards per process (easy to miss one); a dedicated
  allowlist table (the plan names `settings`; one JSON value keeps the
  change atomic); polling the setting only (up to minutes of divergence
  between instances); storing the verification checkpoint in the
  database and trusting it (forgeable with the rows; D-190 rejected
  this); computing "missed" in the resolver by replaying D-188 per job
  (several queries per job and page); one `restoreUpdated` stream per
  active restore (exhausts the stream cap); a per-user restore stream
  filter argument (not needed: the scope comes from the caller).
- **Consequences:** Admins can open egress to private hosts from the UI;
  every widening is audited and alerts every admin, and hard-denied
  ranges stay unreachable whatever is stored. A compromised admin session
  with a fresh step-up can widen egress to private ranges (as with the
  TOML before, but now visible in the audit log and alerts). Amended by
  D-198 (M5 security review): entries are scoped per outbound use, and
  neither admin entries without the `personal_webhooks` scope nor the
  bootstrap list widen users' personal webhooks. Workers hold
  one more database connection (LISTEN). `Job.missed` depends on a worker
  running the detector (stale by up to 5 minutes, except that a success
  clears it at once). Storage figures are bekup's own records, not the
  bucket's billing view. Security impact: new admin write surface on
  egress (B4) with step-up, validation by the guard itself, audit and
  alert; the inbox filter, usage and missed state expose nothing beyond
  existing authorizations; `restoresUpdated` is re-authorized per event
  like `restoreUpdated`. Supersedes D-192 items 1, 2 ("missed"
  derivation) and 4, and D-194 items 2-4.
- **Related:** D-001, D-055, D-101, D-120, D-149, D-155, D-161, D-167,
  D-176, D-183, D-188, D-190, D-191, D-192, D-194; task 6.12;
  `internal/egress`, `internal/netguard/allowlist.go`,
  `internal/app/egress.go`, `internal/pubsub/channels.go`,
  `internal/audit/chainstatus.go`, `internal/monitor/security.go`,
  `internal/notify/{api_inbox,security}.go`, `internal/notify/missed`,
  `internal/db/migrations/00030_dashboard_state.sql`,
  `internal/graph/schema/{network,audit,notifications,destinations,jobs,restore}.graphqls`,
  `web/src/features/{admin,dashboard}/`.

## D-197: Stream budget: one shared account stream, combined page streams, cross-tab relay and 429 backoff

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (M5/M6 code review follow-up)
- **Context:** Every subscription is one SSE connection (D-124) and a
  session may hold `graphql.stream_max_per_session` streams (default 4,
  kept). The shell opened two streams (`sessionEvents`, the bell's
  `notificationReceived`), the dashboard two more (`runUpdated`,
  `restoresUpdated`, D-192/D-196) and a run page two (`runUpdated`,
  `runLogAppended`): one tab reached the cap, a second tab got 429s that
  the client retried quickly, and over plain HTTP/1.1 (no proxy, D-095)
  two tabs held 8 SSE connections against the browser's six per origin,
  so even POSTs stalled.
- **Decision:**
  1. **One account stream.** `Subscription.accountEvents` (`@authenticated`,
     mapped to `account.sessions_view`) carries the caller's session
     notices (`session: SessionEvent`) and new inbox rows
     (`notification: Notification`); each event sets exactly one field.
     Each kind keeps its own delivery-time check (`account.sessions_view`;
     `notification.inbox` with the row's owner equal to the current
     principal) after the per-event session check; the terminal codes
     (UNAUTHENTICATED, FORBIDDEN, SESSION_ROTATED, UNAVAILABLE) come from
     the stream itself as before. Without the notification service only
     session notices are sent. `sessionEvents` and `notificationReceived`
     are removed (never released).
  2. **Combined page streams.** `streams.Feed` + `streams.Combine` merge
     two prepared sources into one stream whose events are checked and
     converted by the feed they came from (one session check per event).
     `activityUpdated` (dashboard; mapped to `run.view`) = every run as an
     unfiltered `runUpdated` + every restore as `restoresUpdated`
     (`restore.view`). `runActivity(runId, afterLogId)` (run page; mapped
     to `run.view`, `run.logs_view` also checked up front) = the run's
     changes + its log batches; after the `done` batch it sends the run
     once more (re-loaded and authorized) and completes. `runUpdated`,
     `runLogAppended`, `restoreUpdated` and `restoresUpdated` stay for
     other pages and API clients.
  3. **Cross-tab sharing (web).** `web/src/lib/stream-sharing.ts`: per
     share key one tab leads (opens the stream) and relays every result
     over the same-origin BroadcastChannel `bekup:streams:v1`; other tabs
     only receive. Election: hello → the leader answers at once; no
     answer within ~150-250 ms → claim; two leaders → the lower tab id
     wins; followers ping every 15 s and take over after 3 s without an
     answer; a leader resigns on unsubscribe and `pagehide` (immediate
     handoff). Events during a handoff are not replayed (hints; pages
     reconcile with queries). Keys always contain the user id
     (`account:<id>`, `dashboard:<id>`), so a tab never accepts another
     account's events. A relayed UNAUTHENTICATED/FORBIDDEN end or 401
     refusal runs the session check in every tab. Without BroadcastChannel
     each tab opens its own streams. Only the account and dashboard
     streams are shared (`runActivity` has a per-tab log offset).
  4. **429 backoff.** The SSE link retries 429 with 5 s doubling up to
     2 min, jittered (half to full), separate from the 1-30 s backoff of
     other failures, and the shell shows a "too many open tabs" banner
     while a stream of the tab is refused.
  5. **Budget.** One shared account stream per browser + at most one page
     stream per tab: one tab uses 2 streams, two tabs at most 3, so the
     cap of 4 leaves room for a third live tab and HTTP/1.1 stays below
     six connections for two tabs.
- **Alternatives considered:** raising the cap (does not fix the six
  connections of HTTP/1.1 and widens the per-session resource budget);
  one multiplexed stream per tab with dynamic topics (graphql-sse
  single-connection mode: a new protocol surface and re-subscription
  logic); a SharedWorker holding the streams (not in Safari on all
  versions, harder to test, CSP worker-src changes); Web Locks for the
  election (needs a secure context, absent on plain-HTTP LAN deployments
  where the connection limit matters most); dropping live restores again
  and polling (more requests, D-196 replaced polling); keeping the old
  fields next to the new ones (unreleased, more surface).
- **Consequences:** A browser holds at most one account stream and one
  dashboard stream regardless of tabs; run pages still cost one stream
  each. Leader handoff can drop events of a few hundred ms (hints only).
  Security impact: no new data or trust boundary on the server; every
  event of the combined streams is re-authorized per kind exactly like
  the single streams. The relay crosses only same-origin tabs of one
  browser, which share the session cookie and thus the account; share
  keys carry the user id, so after a sign-in as another account in one
  tab, other tabs ignore the previous account's stream. Messages carry
  only results the server authorized for that session. Supersedes D-191
  item 7 (`notificationReceived`; the delivery rules move to the inbox
  part of `accountEvents`), D-192 item 1 and D-196 item 8 (two dashboard
  streams), and D-193 item 1 (the bell's own stream).
- **Related:** D-001, D-052, D-095, D-108, D-118, D-124, D-149, D-155,
  D-191, D-192, D-193, D-196; `internal/graph/streams/combine.go`,
  `internal/graph/resolvers/subscriptions.go`,
  `internal/graph/schema/{subscriptions,runs,notifications}.graphqls`,
  `web/src/lib/{apollo,stream-sharing}.ts`,
  `web/src/components/shell/{account-stream,stream-limit-banner}.tsx`,
  `web/src/features/{dashboard,runs,notifications}/`.


## D-198: Scoped SSRF allowlist, signed delivery id and event type, bucketed personal tests and other M5 security follow-ups

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent (M5 security review follow-up)
- **Context:** The M5 security review found that the admin and bootstrap
  SSRF allowlist (D-120, D-196) also widened **personal webhooks**, which
  any user can point anywhere: one private-network entry for a MinIO let
  every user make bekup POST to that network, and `testMyWebhook` returned
  the receiver's exact HTTP status (a service and port probe). Smaller
  findings: link-local clients with an IPv6 zone could not sign in (the
  zone reached the inet columns and dedup keys); `security.*` inbox rows
  could be deleted; the SMTP transport could be weakened or its CA bundle
  swapped while the stored password stayed (D-140 only covered host, port
  and username); wildcard allowlist entries could cover public suffixes
  (`*.co.uk`) or wildcard-DNS services (`*.nip.io`); the inbox kept
  showing rows about jobs the reader lost and `security.system` rows to
  demoted admins; the webhook signature covered only timestamp and body
  (a captured request could be replayed under another delivery id past
  the receiver's dedup); the failed-login burst count scanned every audit
  row of its window; the web hard-coded the security alert kinds.
- **Decision:**
  1. **Allowlist scopes.** Every admin entry names the uses it widens:
     `destinations`, `smtp`, `shared_webhooks`, `personal_webhooks`
     (`netguard.Scope`). New entries without scopes and every migrated
     entry get all scopes **except** `personal_webhooks`; the bootstrap
     list of `[netguard]` applies to the same default scopes and can never
     reach personal webhooks. `netguard.Set` holds one `Guard` per scope
     per process; `egress` applies the stored list to all four together
     (`Set.SetAllowlist`, nothing changes on a rejected list).
     Destinations, SMTP, shared webhooks and personal webhooks each dial
     through their own guard (two webhook senders; `notify.Options.PersonalWebhooks`
     and `APIOptions.PersonalGuard`/`PersonalWebhooks` are never defaulted
     to the shared ones; personal URLs are checked at input time against
     the personal guard).
  2. **Storage and API.** `settings` `netguard.allowlist` becomes version
     2 (`{"version":2,"cidrs":[{"value","scopes"}],"hosts":[…]}`);
     migration 00031 converts version 1 with the default scopes, and the
     service reads a version-1 value the same way (an entry without scopes
     in a version-2 value makes the whole value untrusted: fail closed).
     GraphQL: `SsrfScope`, `SsrfAllowlist.cidrEntries/hostEntries`
     (value + scopes), `configScopes`, `defaultScopes`,
     `personalWebhooksWidened` (a weakened default: shown permanently);
     `SsrfAllowlistInput.cidrEntries/hostEntries` (scopes null: keep the
     stored scopes of the same value, defaults for a new one; `[]` is
     VALIDATION). The plain `cidrs`/`hosts` fields stay for compatibility
     (exactly one form per list; plain values keep their stored scopes).
     Errors name `cidrEntries.<i>.value|scopes`.
  3. **Audit and alert.** `setting.changed` gains `added_scopes`
     (`[{value, scopes}]` for kept entries that gained scopes) and
     `personal_webhooks_widened`; `widened` also covers gained scopes.
     The `ssrf_allowlist_widened` alert carries `personal_webhooks: true`
     and says that every user's personal webhooks may now reach private
     targets.
  4. **Personal test results.** `testMyWebhook` answers only `ok`,
     `not_allowed` or `failed` (one fixed message), never `statusCode`;
     the audit entry records the bucketed code. Shared-channel tests
     (admins) keep the detailed codes.
  5. **Host patterns.** Wildcards whose suffix is a public suffix (ICANN or
     private PSL section, `golang.org/x/net/publicsuffix`) are refused,
     and so is any entry under a built-in list of wildcard-DNS services
     (nip.io, sslip.io, xip.io, nip.direct, localtest.me, lvh.me, vcap.me,
     traefik.me, localhost.direct, 1u.ms, lacolhost.com).
  6. **IPv6 zones.** `httpserver.resolveClient` drops zones and unmaps
     (peer and X-Forwarded-For); notify's detail/dedup builders use the
     same canonical form (`clientIP`).
  7. **Inbox.** `security.*` rows cannot be deleted (VALIDATION on `id`,
     also refused in SQL); they can be marked read. Every inbox read
     (list, unread count, the stream's single-row load and resync) filters
     at read time by the reader's **current** role from the database:
     staff/admin-audience types the role may no longer see are hidden
     (e.g. `security.system` after losing admin), and for role `user`
     rows about jobs they do not own. Rows are kept (not pruned).
  8. **SMTP.** Weakening the mode (STARTTLS or IMPLICIT_TLS to AUTO or
     NONE) or changing the CA bundle requires the password in the same
     update (VALIDATION on `password`), like the endpoint rule of D-140.
  9. **Webhook signature.** `v1` now signs
     `"<ts>\n<X-Bekup-Delivery>\n<X-Bekup-Event>\n<body>"` (none of the
     three headers can contain a newline). The format was never released,
     so `v1` is reused; docs and test vectors are updated.
  10. **Login failures.** Partial index `audit_log_login_failed_ts_idx`
     (`audit_log (ts) WHERE action = 'auth.login.failed'`), checked with
     EXPLAIN in a test.
  11. **Alert reference.** `Query.securityAlertTypes` (admin;
     `monitor.alert_list`) lists every `security.system` kind with
     severity, description and repeat rule from the server's own tables.
  12. **Attachments for staff.** `Job.sharedChannelAttachments` (operation
     `notification.shared_channel_attachment_view`: admins and
     maintainers; null for backup owners, like `Job.owners`) lists the
     shared channels attached to a job: id, name, type, masked display URL
     of a webhook, enabled, the group/event filter; never e-mail
     addresses, URLs or keys. Batch-loaded per page of jobs (one query).
     Managing channels stays admin only.
- **Alternatives considered:** a separate personal-webhook allowlist
  (a second list to manage; scopes cover destinations and SMTP too); never
  allowing private targets for personal webhooks (some installations run
  their chat or ticket system internally: an explicit, alerted opt-in is
  enough); keeping v1 and documenting dedup on the body's event id (the
  event id is shared by every channel and retry, and a receiver that
  dedups on the delivery header stays open to replays); pruning hidden
  inbox rows on role or owner changes (irreversible and racy; read-time
  filtering is exact and cheap); counters for failed logins (more state
  to keep consistent; the partial index is enough at the rate limits).
- **Consequences:** Existing admin entries stop applying to personal
  webhooks after the upgrade (fail closed); admins who relied on it must
  add the scope (audited, alerted, shown as a badge). Webhook receivers
  must sign the delivery id and event type (pre-release change). Clients
  must re-enter the SMTP password when weakening TLS or changing the CA.
  The admin Network page needs a scope editor with a hazard warning for
  `personal_webhooks` (task for the web). Security impact: closes the
  user-reachable SSRF widening (B4/NT-1) and the test-result port probe;
  the bootstrap list can no longer widen personal webhooks at all; hard
  denials are unchanged. D-196's consequence "a change applies to every
  outbound connection at once" now holds per scope.
- **Related:** D-001, D-023, D-055, D-120, D-140, D-177, D-180, D-183,
  D-190, D-191, D-196; `internal/netguard/{scope,allowlist}.go`,
  `internal/egress`, `internal/app/{egress,notify,email,destinations}.go`,
  `internal/notify/{api,api_channels,api_personal,api_inbox,deliver,fanout,security,alert_types}.go`,
  `internal/notify/webhook/sign.go`, `internal/notify/email/settings.go`,
  `internal/httpserver/middleware.go`,
  `internal/db/migrations/00031_egress_scopes_login_failures.sql`,
  `internal/graph/schema/{network,security_alerts,job_notifications,notifications,email}.graphqls`.

## D-199: Web review follow-ups: one deny-by-default route guard, confirmed sign-out, strict return paths, shared web modules and build-time document splitting

- **Date:** 2026-10-28
- **Status:** proposed
- **Decided by:** agent
- **Context:** The M6 reviews found an open-redirect gap after dot-segment
  normalisation in the login return path, a sign-out that treated any
  failure as signed out, secret-returning TOTP/recovery mutations without
  `no-cache`, placeholder routing, per-page role guards (none on
  connections/destinations), duplicated helpers and operations, a
  hard-coded security alert list and one eager chunk with every GraphQL
  document.
- **Decision:**
  1. `safeReturnPath` re-checks the normalised path: a pathname starting
     with `//` or containing `\` / `%5c` is refused.
  2. Sign-out counts as done only on a successful `logout` or an
     UNAUTHENTICATED answer; network, rate-limit and internal errors are
     retried twice (0.5 s, 1.5 s backoff); anything else keeps the user
     signed in with an error toast.
  3. Every mutation that returns secret material runs with
     `fetchPolicy: "no-cache"` (now also BeginTotpEnrollment,
     ConfirmTotpEnrollment, RegenerateRecoveryCodes).
  4. One route guard, `RequireRole` (a layout route around every
     signed-in page), reads `handle: RouteAccess` of the matched routes
     (`lib/route-access`, derived from the nav item roles). Every handle in
     the chain must allow the role; a route without a handle is denied.
     Connections/destinations: admin+maintainer; `/admin/*`: admin; job
     editor: admin+maintainer. UI shaping only; the authz matrix decides.
  5. Shared web modules: `lib/format` (sizes always in binary IEC units
     KiB/MiB/GiB, durations, clock), `lib/age-identity` (exactly one
     58-character identity), `lib/download` (blob: text download),
     `lib/operations` (operations used by several features),
     `components/status/tag` (Tone/Tag) and one signing-key dialog.
  6. The job editor gets a notifications section: admins attach, re-filter
     and detach shared channels for the job (security events never
     offered); maintainers get a note until the API exposes attachments to
     them; everyone gets a link to their per-job overrides
     (`/profile/notifications?job=<uuid>`).
  7. The security alert reference comes from `securityAlertTypes`.
  8. Builds replace each `` graphql(`...`) `` call with an import of a
     virtual module holding only that pre-compiled document
     (`vite-plugins/graphql-documents.ts`), so documents ship with their
     lazy routes; a call without generated output fails the build.
- **Alternatives considered:** decoding and re-validating the return path
  in a loop (the normalised URL is what navigation uses); best-effort
  sign-out (a session the user believes ended could live on); guards per
  page (easy to forget, as happened); the codegen babel plugin (needs a
  babel pipeline next to oxc and still imports every document from one
  generated module, so no per-route split).
- **Consequences:** A user whose logout cannot be confirmed stays on the
  page and must retry. New routes must declare `handle` or they are
  denied (a test walks every route). Eager JavaScript drops from about
  1,273 kB to 996 kB (gzip 316 kB to 297 kB). Security impact: closes
  the protocol-relative return-path bypass, keeps TOTP secrets and
  recovery codes out of the Apollo cache, and makes the role guard deny
  by default; the server-side authorization is unchanged.
- **Related:** D-050, D-053, D-067, D-082, D-179, D-186, D-190, D-191,
  D-193, D-194, D-195, D-198; `web/src/lib/{return-path,route-access,format,age-identity,download,operations}.ts`,
  `web/src/features/auth/{require-role.tsx,use-sign-out.ts}`,
  `web/src/features/jobs/notifications-panel.tsx`,
  `web/src/components/status/tag.tsx`, `web/vite-plugins/graphql-documents.ts`.

## D-200: Runtime config reload of a conservative set of live keys (admin UI broadcast and SIGHUP)

- **Date:** 2026-10-29
- **Status:** proposed
- **Decided by:** agent (requested by the user: a config reload triggered from the admin web UI that reaches every serve and worker process, plus SIGHUP on a single process; apply only keys that are safe at runtime, report the rest as restart-required, and never replace the running config with an invalid file; plan `plans/robust-giggling-tide.md` Part A, task 6.13)
- **Context:** D-041 (implementing D-019) says "no hot reload": every
  component copies its config at startup, so any change needs a restart of
  every serve and worker process. Operators asked to tune limits, the log
  level and allowlists without a restart, from the UI for all processes or
  with SIGHUP for one. A reload must not become a way to weaken the posture
  silently, to leave a process half-configured or to reveal config values.
- **Decision:**
  1. **Conservative per-key classification (deny by default).** Only
     `bekup.toml` fields tagged `reload:"live"` change at runtime:
     `log.level`; `server.trusted_proxies`, `max_body_bytes`,
     `rate_limit_per_second`, `rate_limit_burst`,
     `user_rate_limit_per_second`, `user_rate_limit_burst`;
     `graphql.max_complexity`, `max_depth`, `max_body_bytes`;
     `netguard.allow_cidrs`, `allow_hosts`; `security.password_min_length`,
     `password_min_score`, `login_rate_limit_per_minute`,
     `login_rate_limit_burst`, `lockout_threshold`, `lockout_base_duration`,
     `lockout_max_duration`; `notifications.max_per_user_per_hour`,
     `collapse_window`, `webhook_failure_threshold`; `retention.dry_run`,
     `max_deletions_per_sweep`, `partial_cleanup_age`. Everything else is
     restart-only, in particular `server.listen`, `base_url`,
     `allow_insecure_http`, `allow_wildcard_listen`, all timeouts,
     `max_header_bytes`, `rate_limit_max_clients` (the limiters' key bound),
     `graphql.stream_*`, `database.*`, `runner.*`, `storage.*`,
     `workers.*`, `scheduler.*`, `metrics.*`, `dev.*`, the master key,
     `argon2_*`, `session_*`, `stepup_max_age`, `audit_head_sign_interval`
     and `restore.temp_connection_*`. `runner.toml` is never reloaded. The
     generated key reference has a Reload column; a test pins the list and
     the posture keys.
  2. **Merged re-validation under the startup dev mode.** A reload loads
     the file with the startup load options and the absolute path loaded
     at startup (same env, flags and D-041 file checks; a new
     `./bekup.toml` never shadows it; a process that started on dev
     defaults never searches), diffs it against the running config, builds
     `WithLive(running, loaded)` and validates that with the startup dev
     mode, so a file that turns on `dev.enabled` or `allow_insecure_http`
     cannot get its live keys accepted under relaxed rules.
  3. **Two-phase apply, audit first.** Every component exposes
     `Prepare(next) (commit, error)`; commits only do atomic stores. All
     appliers whose keys changed are prepared; any failure commits nothing
     (`failed`/`apply`). Then the outcome is recorded fail closed in one
     transaction (audit `config.reloaded` or `config.reload_failed`, system
     actor, then the `config_reload_results` row); only after that commit
     are the appliers committed and the running config swapped. If
     recording fails nothing is applied (`failed`/`record`). Every live key
     is covered by exactly one applier per process (keys a process does not
     use are declared explicitly; startup fails otherwise). A changed key
     that this process does not use (nil `Prepare`, e.g. `server.*` in a
     worker, `retention.*` in `serve --no-worker`) is taken into its
     running config but not listed as applied or weakened in its result;
     the processes that use it report it. A changed key that no running
     process uses raises no alert and no "applied" entry anywhere; it
     takes effect when a process that uses it next starts (that process
     loads the file at startup).
     `a.opts.Config` stays the startup snapshot.
  4. **Broadcast = NOTIFY-only trigger + request row.** The UI request
     (admin + fresh step-up, at most one per 10 s instance-wide under an
     advisory lock) inserts a `config_reload_requests` row, audits
     `config.reload_requested` and NOTIFYs `config.reload:<request id>` on
     `bekup_settings_events` in one transaction (NOTIFY only: the local bus
     would deliver before the commit), then reloads the requesting process.
     Every process acts on such a payload only when a matching `ui`
     request row younger than 10 minutes exists (a forged NOTIFY does
     nothing), deduplicates by request id (an answer row of this process
     that exists already rolls the transaction back: nothing is audited
     or applied again) and, at start and after a listener resync, answers
     the unanswered requests of the last 10 minutes it is responsible for
     (item 9).
  5. **Names only.** Results, audit details, alerts, logs of outcomes and
     the GraphQL API carry key names and an error kind (`file`, `syntax`,
     `invalid`, `apply`, `record`) only; unknown keys are reported as
     `<section>.unknown_key`. Values and the config file path never leave
     the process except in the redacting log. Attribute and detail names
     never end in `keys` (`logging.IsSecretKey` redacts those as secrets,
     and it stays strict): the error key names are `error_settings` in the
     audit details and the log, and the alert detail is `settings`.
  6. **Weakening alerts.** Each live key has a rule for what weakens
     security (wider trusted proxies, an added `[netguard]` entry, raised
     rate/size/GraphQL limits, a raised lockout threshold or deletion cap,
     shorter lockout durations or partial cleanup age, lower password
     bounds, `retention.dry_run` true → false, a raised
     `notifications.webhook_failure_threshold` (failing or refused
     webhooks keep retrying longer before auto-disable); `log.level`,
     `max_per_user_per_hour` and `collapse_window` never weaken); a test requires a rule for
     every live key. A non-empty `weakened` list raises the
     `config_reload_weakened` security alert (severity error) to every
     admin, deduplicated by request id + key-set hash so replicas applying
     the same change raise one alert (processes of different kinds may
     report different sets, e.g. a worker without `server.*`, and then
     raise one alert per set).
  7. **SIGHUP with the system actor.** serve and worker catch SIGHUP (so
     it no longer terminates them as PID 1; the runner is unchanged),
     collapse bursts (buffer of one) and reload at most every 2 s, locally
     only (a `signal` request row, no broadcast), audited with
     `audit.SystemActor()` and `trigger: signal` in the details.
  8. **No permanent badge.** The posture keys that have badges and startup
     audits (`base_url`, `allow_insecure_http`, `database.tls_mode`) stay
     restart-only, so the existing startup audit, banner and daily
     reminder keep applying unchanged; a weakening reload is a one-off
     alerted and audited event, not a standing state.
  9. A `loaded` row per process start doubles as the process registry.
     Liveness: each process's LISTEN connection reports
     `application_name = bekup-serve-listen:<instance id>` or
     `bekup-worker-listen:<instance id>` (56 characters); the API reads
     `pg_stat_activity` (prefix match) and marks each process
     `listening`. The status page lists the newest row of every process
     listening now (any age) plus every process seen in 7 days; stopped
     ones are shown as "not listening" history without a restart call to
     action. **One rule decides who answers a UI request**, evaluated in
     SQL on database time on both sides: a process is responsible for a
     request when its first row (the startup row, written after the file
     was read) is at most `configreload.StartupGrace` (1 minute) after
     the request's `requested_at`. The catch-up answers exactly those
     (plus the 10-minute bound), and a request's `expectedProcesses` =
     processes that answered plus listening responsible ones, so the UI
     stops polling once they answered. A process started later already
     loaded a file newer than the request and never answers it; the grace
     covers a request made between reading the file and writing the
     startup row (that process answers it, usually `unchanged`). No
     comparison of the Go host clock with the database clock is involved.
     application_name is chosen by the client and can be spoofed by
     anything that can log in to the app database: it is used for display
     and progress only, never for a security decision. serve prunes rows
     older than 30 days hourly, except the newest row of a listening
     process (and the request it answered). A process whose LISTEN
     connection is reconnecting during a prune is not "listening" then and
     may lose its newest row when that row is older than 30 days; it
     reappears with its next answer (display only: it still receives
     broadcasts, and without rows it is responsible for no catch-up until
     then). The `record` error kind is
     never stored (the write failed): it appears only in the log and
     metrics, not in the GraphQL enum. `bekup_app` has SELECT, INSERT and DELETE
     only on the two tables (migration 00032). Metric
     `bekup_config_reloads_total{status}`.
  Once accepted, this decision supersedes the "no hot reload" clause of
  D-041 (and thereby of D-019); their statuses are left for the user to
  change.
- **Alternatives considered:** reloading everything that parses (rejected:
  listeners, TLS, DB pools, sessions and the master key cannot change
  consistently mid-run, and posture keys would bypass the startup audit);
  restart-only forever (rejected by the request); validating the loaded
  file alone (rejected: a file enabling dev mode would relax the rules for
  the live keys); applying first and auditing after (rejected: an applied
  but unaudited change); broadcasting the config or the changed values in
  the NOTIFY payload (rejected: payloads carry IDs only, D-050, and a
  forged payload must not act); acting on any NOTIFY (rejected: forgeable
  by anything with database access); `audit.CLIActor()` for SIGHUP
  (rejected: every signal would raise a `cli_action` alert and the actor
  is not a CLI command; the system actor with the trigger in the details
  keeps it attributable); a permanent badge for weakened reloads (rejected:
  the weakening keys are bounded limits already alerted once; the true
  posture keys stay restart-only with their existing badges).
- **Consequences:** Operators can tune limits, allowlists and the log
  level without downtime and see per-process outcomes. Security impact: a
  new admin-triggered path that can change security-relevant limits; it is
  gated by admin + step-up and rate limited, never touches posture keys,
  re-validates under the startup dev mode, is audited fail closed per
  process and alerts every admin on a weakening change. SIGHUP needs host
  access (T10, trusted) and is audited. A process that is down misses a
  broadcast (it loads the current file when it starts); the status page
  shows who answered. `SECURITY.md` CL-7/CL-12, `docs/configuration.md`
  (Reloading), `docs/operations.md` (runbook, metric) and the alert table
  are updated. A new live key needs the tag, an applier and a weakening
  rule (AGENTS.md).
- **Related:** D-019, D-041, D-050, D-055, D-069, D-095, D-098, D-100,
  D-106, D-117, D-176, D-190, D-196, D-198; task 6.13;
  `internal/config` (`reload.go`, `errors.go`), `internal/configreload`,
  `internal/app/configreload.go`, `cmd/bekup/signal_unix.go`,
  migration `00032_config_reloads.sql`,
  `internal/graph/schema/config.graphqls`.

## D-201: Dev-only runner for a native serve, loopback-published on a dedicated network

- **Date:** 2026-10-29
- **Status:** proposed (the `runner-host` subnet moved to `10.213.254.0/24` by D-205)
- **Decided by:** agent (requested by the user: "can i also start a runner only next to the dev fe and backend i currently run")
- **Context:** The native dev workflow (`just dev` + `just run` +
  `just web-dev`, D-114) had no runner: the only dev runner lives in
  profile `full` on the production network layout, where the gRPC port
  sits on the internal `runner-ctl` network. Docker cannot publish ports
  from an internal network, so a serve on the host could not reach it, and
  a runner running natively on macOS cannot attach its dump containers to
  per-run Docker networks.
- **Decision:**
  1. `docker-compose.dev.yml` gets the service `runner-host` (profile
     `runner`), extending the production `runner` service (same image,
     hardening, Docker socket, dev `runner.toml`, certificates).
  2. Its networks are replaced by `targets` plus a dedicated, DEV ONLY
     `runner-host` network (`10.213.253.0/24`, fixed address
     `10.213.253.10`) that only this service joins. The listener binds that
     address (`BEKUP_LISTEN_ADDRESS`) and the port is published on
     `127.0.0.1:${BEKUP_DEV_RUNNER_PORT:-7443}` only.
  3. Not on `backend` or `dev-host` (no app DB access) and not listening
     on `targets` (target DBs cannot reach the gRPC port). The listener's
     per-run network refusal and mTLS (TLS 1.3, private CA, SAN checks)
     are unchanged; the native serve uses the dev `serve` client
     certificate copied to `secrets/local/` and `server_name = "runner"`.
  4. `just dev-runner` starts `runner-host` with the sample targets
     (`DOCKER_GID=0` by default on macOS); `just dev-down` and
     `just db-reset` include the profile; the native environment sets
     `BEKUP_RUNNER_ADDRESS=127.0.0.1:${BEKUP_DEV_RUNNER_PORT:-7443}`.
- **Alternatives considered:** publishing from `runner-ctl` (impossible:
  internal network); making `runner-ctl` non-internal in dev (diverges
  from production for `dev-full` too); listening on `targets` (the sample
  target DBs could reach the gRPC port); a native runner on the host
  (cannot join per-run Docker networks on Docker Desktop); running serve in
  a container only (`just dev-full`, loses the fast native loop).
- **Consequences:** Development only; production compose and the runner's
  trust boundaries are unchanged. Any local process can open a TCP
  connection to the loopback port, but only a client with a certificate
  from the dev CA gets past the mTLS handshake. Do not run it together
  with `just dev-full` (shared sample targets).
- **Related:** D-003, D-029, D-030, D-081, D-114, D-125, D-127, D-132;
  `docker-compose.dev.yml`, `justfile` (`dev-runner`),
  `scripts/lib.bash` (`dev_native_env`), `deploy/dev/bekup.local.toml`,
  `docs/development.md`.

## D-202: Manual multi-arch image publishing to the operator's own registry (`just publish`)

- **Date:** 2026-10-29
- **Status:** proposed
- **Decided by:** agent (requested by the user: "i need a just command to build the containers and send them to a docker repository of my choosing without commiting the repo url to git")
- **Context:** Operators want to run their own build of bekup (a fork,
  patches, an air-gapped mirror) before the official release workflow
  (task 7.4) exists, and alongside it afterwards. Their registry name is
  specific to their environment and must not land in git. Images must stay
  digest-pinnable (D-061), traceable to a commit and distinguishable from
  release tags. bekup ships one image (every component is a subcommand).
- **Decision:**
  1. `just publish [repo] [--dirty] [--yes] [--dry-run]` (group
     `release`, `scripts/publish.sh`, positional arguments, D-071).
  2. The repository comes from the argument, else `BEKUP_IMAGE_REPO`, else
     a gitignored and dockerignored `.just.env` (`.just.env.example` is
     committed with placeholders only). `load_local_env` in
     `scripts/lib.bash` reads only `KEY=value` lines for `BEKUP_IMAGE_REPO`
     and `BEKUP_PLATFORMS`, never sources or evaluates the file, warns about
     and ignores unknown keys, never prints values and refuses a symlink, a
     file not owned by the user, a group/world-writable file or one larger
     than 4 KiB.
  3. The repository must be `host[:port]/path` with an explicit registry
     host, lowercase, without tag, digest, scheme or `user:pass@`.
     Credentials come only from the user's own `docker login`.
  4. A clean tree pushes `<VERSION>`, `sha-<12-char sha>` and `latest`; a
     dirty tree (also files marked skip-worktree/assume-unchanged, which
     hide their changes from `git status`) is refused, `--dirty` pushes
     only `sha-<sha>-dirty` (also in the `org.opencontainers.image.revision`
     label; a mutable tag, pin by digest). A clean build uses a pristine
     export of HEAD (`git archive` into a temporary directory) as the build
     context, so ignored or local files never enter it; `--dirty` builds
     the working tree. The binary version (`VERSION` build argument) is
     `<VERSION>` only when HEAD is the tag `v<VERSION>`, otherwise SemVer
     build metadata `<VERSION>+sha.<short>` (`.dirty` appended for
     `--dirty`); the image tags stay as above and a warning is printed when
     HEAD is not the tag. The Dockerfile build stage sets `GOWORK=off` and
     `.dockerignore` excludes `go.work*` and a locally built
     `cmd/bekup/bekup`.
  5. Build on a dedicated buildx builder `bekup-publish` (docker-container
     driver) on the local Docker: the current docker context must be a
     local socket and an existing builder must have exactly one node on
     that context (otherwise it is refused until removed and recreated).
     Platforms `BEKUP_PLATFORMS` (default `linux/amd64,linux/arm64`),
     `--target runtime --pull --provenance=mode=max --sbom=true --push`,
     the VERSION/VCS_REF/BUILD_DATE build arguments, and
     `BUILDX_GIT_INFO=false` so the provenance does not record the git
     remote URL (the revision comes from VCS_REF); the SBOM generator image
     is pinned by digest. Invalid repository values are echoed with
     `printf %q`; a registry port must be 1–65535.
  6. It prints the digest and the compose pin `BEKUP_IMAGE=<repo>@sha256:…`;
     a confirmation prompt lists repo and tags (`--yes` skips it and is
     required without a terminal); `--dry-run` prints the command only.
  7. It never creates git tags or touches `CHANGELOG`; official, signed
     releases stay with the release workflow (task 7.4).
- **Alternatives considered:** `set dotenv-load` in the justfile (injects
  every variable of the file into every recipe; effectively sourcing);
  the registry in the compose `.env` (mixes runtime and build configuration);
  committing the registry (leaks private infrastructure names); plain
  `docker build` + `docker push` (single-architecture, no provenance or
  SBOM); the default builder (the docker driver cannot push
  multi-platform indexes with attestations); `latest`/version tags from a
  dirty tree (not reproducible from a commit).
- **Consequences:** Operators can publish attested multi-arch images and pin
  them by digest. Security impact: the repository string is validated (no
  shell/argv injection) and credentials never pass through the recipe's
  argv, logs or history; `.just.env` cannot execute code and never enters
  the build context; provenance (mode=max) records build arguments (no
  secrets) and base-image digests; dirty builds are visibly marked. The
  images are not signed (cosign signing stays with task 7.4). Needs docker
  buildx (optional check in `just doctor`). On macOS a `localhost:5000`
  test registry can collide with AirPlay on port 5000 (use 127.0.0.1).
- **Related:** D-020, D-025, D-061, D-071, D-072; task 7.4;
  `scripts/publish.sh`, `scripts/publish_test.sh`, `scripts/lib.bash`
  (`load_local_env`), `.just.env.example`, `docs/development.md`,
  `docs/installation/compose.md`.

## D-203: Container-only compose install (`bekup setup`) with embedded assets, an ownership manifest and a root + `CAP_CHOWN` setup container

- **Date:** 2026-10-29
- **Status:** proposed (partly superseded by D-205: setup no longer always writes the compose file; items 2, 4 and 9 for the compose file, and the file names)
- **Decided by:** agent (requested by the user: the deployment server should need only images, no source checkout; the user decided that the image carries the deploy assets, that a new `bekup setup` writes a ready deployment directory, that the image comes from a prompt or `BEKUP_IMAGE` and must be pinned by digest, that the directory holds only generated files, and that ownership is set by a root setup container with only `CAP_CHOWN`)
- **Context:** A compose install needed the repository for
  `deploy/postgres/*`, `deploy/traefik|nginx/*`, the compose file and
  `just keygen`, plus a long list of manual `chown`/`chmod` steps (D-041)
  that are easy to get wrong. Every secret must be readable by exactly the
  consuming container's UID (65532 bekup, 70 postgres) and nobody else, the
  CA key belongs offline, and the bekup image must be pinned (D-061).
- **Decision:**
  1. **Assets without drift.** The root package `bekup` (`embed.go`) embeds
     `docker-compose.yml`, `bekup.example.toml`, `runner.example.toml` and
     `deploy/{postgres,traefik,nginx}` (public files only; never
     `deploy/dev`). `.dockerignore` excludes only `docker-compose.dev.yml`
     and `docker-compose.override.yml`. Tests: the embedded files are
     byte-equal to the repository files, every `./deploy/...` mount is
     embedded and every embedded asset is mounted, `.dockerignore` excludes
     none of them, third-party image pins come only from the repository
     compose file.
  2. **The generated compose file equals the repository's** except for the
     single line `image: ${BEKUP_IMAGE:-bekup:dev}`, which becomes the
     pinned reference as a literal (a shell variable cannot override it);
     exactly one such line, no further `${BEKUP_IMAGE`, no `build:`,
     otherwise setup fails. A header says "do not edit; use
     docker-compose.override.yml".
  3. **Image.** `--image` or `BEKUP_IMAGE`, parsed by `internal/imageref`
     (`[registry/]path[:tag]@sha256:<64 hex>`, the grammar shared with the
     engine catalog). A container cannot verify its own digest, so a tag
     that names another version than the running binary is refused
     (`--allow-version-mismatch`); the error explains how to obtain the
     digest.
  4. **`bekup setup init`** (`internal/setup`) writes, from validated
     answers (strict patterns: no CR/LF, `$`, quotes, `#`, backslashes or
     spaces), the compose file, `deploy/**`, `reference/*.example.toml`
     (generated-upgradable), `.env` (compose variables, no secrets),
     minimal `bekup.toml`/`runner.toml` validated in-process with
     `config.LoadBekup`/`LoadRunner` and an empty environment, and for
     static proxy TLS `docker-compose.override.yml` + `proxy-tls/`
     (operator files, written once). Secrets: three 32-byte `crypto/rand`
     database passwords (base64url) and the two URLs (built in one buffer,
     registered with `logging.DefaultRegistry`, zeroed), the master keyring
     (`crypto.GenerateKeyring`), and `certs.Init` for runner, serve and
     postgres with a passphrase-sealed CA key (prompt twice,
     `--ca-passphrase-file` via `config.ReadSecretFile`,
     `--ca-passphrase-stdin`, or `--generate-ca-passphrase` shown once and
     only on a terminal; no unencrypted CA). Secret groups (master key,
     database, certificates) are generated whole or not at all.
     `--keys-dir` is a clean relative path inside the deployment directory
     (mount another volume there to keep secrets elsewhere).
  5. **Writing.** Symlinks are refused on every path component (Lstat);
     new files are created with `O_CREAT|O_EXCL|O_NOFOLLOW` in their final
     mode and fsynced with their directory; upgradable files are replaced
     through a temporary file and a rename; any error rolls back everything
     the run created (and restores replaced non-secret files). Existing
     files are kept: `--keep-existing` creates only missing files, `--force`
     rewrites only non-secret files, `--regenerate-secrets` needs a terminal
     and the typed confirmation `regenerate secrets` and warns about the
     postgres volume and stored credentials.
  6. **One versioned ownership manifest** (`internal/setup/manifest.go`,
     `Entry{Path, Kind, Mode, UID, GID, Class, Secret, Group}`, classes
     generated-upgradable / operator / secret) drives init, check and
     `--fix-ownership`, and the table in `docs/installation/compose.md` (a
     test keeps it in sync). Public files root 0644 (`*.sh` and directories
     0755); `secrets/`, `secrets/certs/`, component directories root 0711;
     master key, DB URLs, `serve.key`, `runner.key` 65532 0400; postgres,
     owner and app passwords and `postgres.key` 70 0400; certificates and
     bundles root 0644; `certs/ca/` root 0700 with `ca.key` 0600. A test
     ties each secret's owner to the `user:` of every compose service that
     consumes it.
  7. **Ownership modes** `auto|chown|print|none`: `auto` reads
     `/proc/self/mountinfo` (Docker Desktop file systems `fakeowner`,
     `virtiofs`, `fuse.grpcfuse`, `osxfs`, `9p` give `none`), otherwise
     `chown` as root, else `print`. Always chmod first, then `Lchown`, so
     the setup container needs only `CAP_CHOWN`. The chosen mode is printed.
     The documented container: `--user 0:0 --cap-drop ALL --cap-add CHOWN
     --network none --read-only --tmpfs /tmp --security-opt
     no-new-privileges`, the directory bind-mounted at `/deploy`.
  8. **CA offline.** The recommended flow mounts the offline media at
     `/deploy/secrets/certs/ca`, so the CA key never touches the server's
     disk; setup detects the mount. Without it, setup and `setup check`
     warn while `ca.key` is on disk.
  9. **State and upgrade.** `bekup-setup.json` (no secrets: schema, manifest
     version, bekup version, image, proxy, keys directory, ownership mode,
     SHA-256 of every generated file, the secret groups). `setup upgrade
     --image <pinned>` requires it, refuses a downgrade
     (`--allow-downgrade`), refuses local edits of generated files
     (`--force` keeps `<file>.bak-<UTC timestamp>`), re-validates the TOML
     files first, rewrites only the compose file, `deploy/**` (removing
     stale assets), `reference/*` and the state file, generates only secret
     groups new in the manifest, never touches existing secrets, `.env` or
     the TOMLs, and prints the compose diff. The same image changes nothing.
  10. **`setup check`** is read-only (unless `--fix-ownership`) and never
      opens a secret file: manifest presence/type/mode/owner (owner skipped
      in `none` mode), no symlinks, every `image:` digest-pinned, generated
      files unchanged and equal to this binary's, `.env` syntax, `base_url`
      vs `BEKUP_DOMAIN`, `trusted_proxies` vs `PROXY_FRONTEND_IP`, listen
      addresses vs the compose addresses, `docker.gid` vs `DOCKER_GID`,
      shell variables that would override `.env`, certificate expiry, and
      the CA key on disk. Non-zero exit on errors.
  11. **Not audited.** Setup runs before the database exists and without
      network; it prints the certificate fingerprints and the master key
      ID as evidence and the escrow guidance instead. Certificate rotation
      in a setup deployment: `certs rotate` in a root container with only
      `CAP_CHOWN` + `CAP_FOWNER` (`--no-audit`, recorded afterwards), then
      `setup check --fix-ownership`; a `setup rotate-certs` wrapper and
      adopting existing source-based installs are follow-ups.
  12. **Review amendments (milestone review, 2026-10-29).**
      - *Ownership without `CAP_FOWNER`:* chmod only when the mode bits
        (incl. setuid/setgid/sticky) differ; a file that belongs to another
        UID is `Lchown`'ed to the caller, chmod'ed, then `Lchown`'ed to its
        manifest owner (per entry), so `setup upgrade`, `check
        --fix-ownership` and init `--force`/`--keep-existing` work on an
        already-chowned tree with `CAP_CHOWN` only. CI runs `check
        --fix-ownership` on the chowned tree.
      - *Existing CA:* before writing anything, init checks every file
        `certs.Init` writes (`certs.InitTargets`); an existing CA (e.g. the
        offline media of an earlier install) is refused ("reuse via `certs
        rotate` + `--keep-existing`, or remove it") and only paths that were
        absent are tracked for the rollback, so a failed run never deletes
        an existing CA. `--regenerate-secrets` moves the old secrets
        (including a mounted offline CA, named in the warning) aside and
        deletes them only after the run succeeded; any failure restores them.
      - *Offline media without Unix modes (FAT/exFAT):* EPERM/ENOTSUP from
        chmod/chown of offline entries on a separate mount are a warning
        (the CA key is age-encrypted); `check` reports their modes as
        warnings; a POSIX file system (ext4 on LUKS) is recommended.
      - *Writes through `os.Root`:* the writer and the ownership code use an
        `os.Root` on the deployment directory (Lstat checks stay), so no
        racing symlink can lead outside it.
      - *Deployment directory:* a manifest entry `.` (root, at most 0755):
        `check` errors when it is group/world-writable or not root's, init
        warns and narrows it.
      - *Pin bypass:* `check` parses `docker-compose.override.yml` (yaml/v3,
        aliases and merge keys followed): ERROR for an `image:` that is not
        digest-pinned, any `build:`, `extends` from another file and a
        top-level `include`; ERROR for other compose file names next to the
        pinned one, and for `COMPOSE_FILE`, `COMPOSE_PATH_SEPARATOR`,
        `COMPOSE_PROJECT_DIRECTORY`, `COMPOSE_ENV_FILES` in `.env` or the
        environment (they select other compose files); WARN for
        `COMPOSE_PROJECT_NAME` (only renames the project: new, empty
        volumes).
      - *Image tag:* a reference without a tag needs
        `--allow-version-mismatch` (nothing to compare).
      - *Upgrade:* an operator file at a path that becomes generated is
        refused, or with `--force` backed up to `.bak-<timestamp>`.
      - *CIDRs:* `--target-cidr`/`--trusted-proxy` wider than /8 (IPv4) or
        /32 (IPv6), above all `0.0.0.0/0` and `::/0`, are refused; wider than
        /16 or /48 warns (init report and `check`).
      - *Output:* documented setup and rotation `docker run`s use
        `--log-driver none`; a generated CA passphrase goes to stderr (only
        when it is a terminal) with a log warning; `--ca-passphrase-stdin`
        stops reading at the newline.
- **Alternatives considered:** inline compose `configs.content` (puts
  scripts into the compose file, no file modes, still needs secrets
  created by hand); separate asset images (more images to pin and
  verify); copying the assets with a `just gen` step into a release
  bundle (drifts, still a second artefact); printing `sudo` commands only
  (error-prone, ownership left to chance); an unencrypted CA option
  (rejected: D-090).
- **Consequences:** A production host needs only Docker and one pinned
  image. Security impact: a root container on a host path is mitigated by
  minimal capabilities (`CAP_CHOWN` only), no network, a read-only root, a
  pinned image, symlink refusal and `O_NOFOLLOW`; a tampered setup image
  could write malicious assets, so the image must be pinned by digest and
  verified (provenance, task 7.4); secrets at rest are protected by the
  manifest modes and owners (D-041), the CA key by its passphrase and the
  offline mount; check detects drift, local edits and environment
  overrides. Desktop file systems are detected (`--ownership` overrides).
- **Related:** D-019, D-041, D-061, D-069, D-089, D-090, D-093, D-094,
  D-100, D-113, D-202; task 6.14; `embed.go`, `internal/setup`,
  `internal/imageref`, `cmd/bekup/setup_cmd.go`,
  `docs/installation/compose.md`, `SECURITY.md`.

## D-204: Release workflow: signed-tag-only GHCR images, pushed by digest, scanned, signed and attested before tagging

- **Date:** 2026-10-29
- **Status:** proposed (extended by D-205: the release also publishes the attested compose files)
- **Decided by:** agent (task 7.4 brought forward; the user decided to publish only on release tags and supplied the release signing key, OpenPGP `1563553AE7CE3A740617A07A592C7E70AA120838`)
- **Context:** `bekup setup` (D-203) installs from an image alone, and a
  tampered setup image could write malicious assets, so operators need an
  official image they can verify before running it. D-061 asks for SBOM,
  cosign keyless signing and provenance; D-072 makes `just release` push a
  signed annotated tag `vX.Y.Z` together with `main`.
- **Decision:**
  1. **Trigger:** only `push` of tags `v[0-9]+.[0-9]+.[0-9]+`
     (`.github/workflows/release.yml`); nothing is published from branches,
     no `pull_request_target`, one concurrency group per tag (queued, never
     cancelled). Registry `ghcr.io/<owner>/<repo>` (lowercased), today
     `ghcr.io/jlentink/bekup`; the only credential is the workflow's
     `GITHUB_TOKEN`. Top-level `permissions: {}`; per job `verify`/`ci`
     `contents: read`, `image` adds `packages: write`, `id-token: write`,
     `attestations: write`, `release` `contents: write`. Actions pinned by
     SHA, images (binfmt, BuildKit) by digest, cosign and Trivy versions
     pinned in `env` (Renovate), harden-runner first, no caches (no state
     from other workflows enters a release).
  2. **Tag verification (`verify`):** the tag equals `v$(cat VERSION)`, is
     annotated, points at the run's commit, and that commit is an ancestor
     of `origin/main`. `git verify-tag --raw` runs in a temporary GnuPG home
     holding only `.github/release-signers.asc` (public key, ownertrust
     ultimate, `gpg.minTrustLevel=fully`), and `scripts/release_ci.sh
     verify-signer` requires exactly one GOODSIG/VALIDSIG, no
     BAD/ERR/EXP/REV status, a SHA-2 hash and a **primary key fingerprint**
     equal to `RELEASE_SIGNER_FPR` in the workflow. Key rotation replaces
     the file and the fingerprint in one reviewed commit on `main`. SSH
     signatures are not accepted.
  3. **CI gate:** `ci.yml` gains `on: workflow_call` (other triggers
     unchanged) and runs in full on the tag; `image` needs `verify` and
     `ci`.
  4. **Build and push by digest only:** buildx (QEMU for arm64)
     `linux/amd64,linux/arm64`, `target: runtime`, `pull`, `no-cache`,
     build args `VERSION`, `VCS_REF` (commit), `BUILD_DATE` from the commit
     time and `SOURCE_DATE_EPOCH`, `provenance: mode=max`, BuildKit SBOM with
     the generator pinned once in `scripts/publish.sh` (D-202), OCI index
     annotations; `outputs: push-by-digest=true`, so no tag exists before
     the checks pass. An existing `:X.Y.Z` is refused before the build and
     again before tagging (version tags are immutable).
  5. **Scan:** Trivy (pinned action and version, default DB mirror, no
     cache) scans the digest for both platforms; any HIGH or CRITICAL
     vulnerability with a fix fails the release. `.trivyignore` holds only
     reviewed exceptions with a reason, decider and Trivy `exp:` date.
  6. **Sign and attest:** `cosign sign --yes --recursive` (keyless, GitHub
     OIDC, Rekor) on the index digest (and so every platform manifest);
     `cosign attest --type spdxjson` of each platform's BuildKit SPDX SBOM
     to that platform's manifest digest; `actions/attest-build-provenance`
     with `push-to-registry` for `gh attestation verify`.
  7. **Tag:** `docker buildx imagetools create` gives the verified digest
     `:X.Y.Z` and, only when X.Y.Z is at least the highest plain `vX.Y.Z`
     tag (`release_ci.sh is-latest` with `semver.sh gt`; pre-releases and
     other tags ignored), `:latest`. A patch for an older line never takes
     `latest`. The version tag must then resolve to the signed digest.
  8. **Release:** `gh release create --verify-tag` (no third-party release
     action) with the CHANGELOG section of X.Y.Z (`release_ci.sh
     release-notes`, inputs validated), the digest, `docker pull …@sha256:`,
     `cosign verify --certificate-identity
     https://github.com/jlentink/bekup/.github/workflows/release.yml@refs/tags/vX.Y.Z
     --certificate-oidc-issuer https://token.actions.githubusercontent.com`
     (docs give the general form
     `--certificate-identity-regexp '^https://github\.com/jlentink/bekup/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$'`),
     `gh attestation verify`, and the hardened `setup init` / `setup
     upgrade` commands with the pinned reference; GitHub's "latest release"
     follows the same rule.
  9. **Review amendments (milestone review, 2026-10-29).**
     - *Job split:* `build` (packages: write; only `docker/*` actions,
       harden-runner and checkout) pushes by digest; `scan` (packages: read)
       runs Trivy; `publish` (packages: write, id-token: write,
       attestations: write; only `sigstore/cosign-installer`,
       `actions/attest-build-provenance`, `docker/login` + imagetools)
       signs, attests and tags. The digest moves between jobs as outputs.
       harden-runner and checkout stay in the write jobs (project rule,
       decided by the user). The trivy-action pin
       `ed142fd0673e97e23eac54620cfb913e5ce36c25` was confirmed as the
       commit of the official annotated tag v0.36.0.
     - *Immutability fails closed:* `release_ci.sh tag-state` treats only a
       registry "not found" (MANIFEST_UNKNOWN, NAME_UNKNOWN, 404) as absent;
       every other error (auth, network, rate limit) fails the release.
     - *`latest`* is decided right before tagging from the release tags that
       exist then (`gh api …/git/matching-refs/tags/v` + `is-latest`) inside
       the global concurrency group `release-latest`; the GitHub release
       follows that value. (GitHub keeps one pending run per group; a third
       simultaneous release is cancelled before tagging and re-run.)
     - *Tag object:* the annotated tag object is fetched explicitly with the
       token (checkout may keep only the commit) and the `tag` name inside
       the signed object must equal `GITHUB_REF_NAME`.
     - *No caches:* the release calls `ci.yml` with `disable_cache: true`
       (setup-go, setup-node, golangci-lint caches off); ci.yml's
       concurrency group has the explicit prefix `ci-`.
     - *Verification* needs cosign v3.0 or newer (Sigstore bundle format);
       the release notes say so.
- **Alternatives considered:** pushing tags directly from the build (a
  failed scan would leave a published tag); an SSH allowed-signers file
  (the maintainer signs with OpenPGP); trusting any key in the file without
  a pinned fingerprint (a single edit of the file would add a signer);
  key-based cosign signing (a long-lived secret in CI); a third-party
  release action (more supply-chain surface than the preinstalled `gh`);
  publishing `edge`/branch images (rejected by the user); moving `latest`
  on every release (a patch for an old line would downgrade `latest`).
- **Consequences:** Operators can verify an image's origin before running
  setup with it (`cosign verify`, `gh attestation verify`, SBOMs). Security
  impact: a release needs write access to `main` **and** the release
  signing key; a compromised dependency with a fixed HIGH/CRITICAL CVE
  blocks the release; keyless signatures are bound to this workflow file and
  the tag ref and are logged publicly in Rekor; the workflow trusts the
  signer file and fingerprint from the tagged commit, so branch protection
  on `main` remains the root of trust. A failure after the tagging step
  needs a new patch version (tags are never overwritten). The package must
  be made public once after the first release. Integration + E2E CI jobs
  (task 7.2) are still open.
- **Related:** D-027, D-061, D-072, D-091, D-202, D-203; task 7.4;
  `.github/workflows/release.yml`, `.github/workflows/ci.yml`,
  `.github/release-signers.asc`, `.trivyignore`, `scripts/release_ci.sh`,
  `scripts/release_ci_test.sh`, `scripts/semver.sh`, `renovate.json`,
  `docs/development.md`, `docs/installation/compose.md`,
  `docs/installation/upgrades.md`, `SECURITY.md`.

## D-205: Compose-first onboarding: release compose files as overlays of one source, the bekup-proxy link network and attested assets

- **Date:** 2026-10-29
- **Status:** proposed
- **Decided by:** agent (recording the user's decisions: the README leads with three steps (download a copyable `compose.yaml`, `docker compose run --rm setup`, `docker compose up -d`) in three variants (own Traefik as the default, behind an existing Traefik, behind Nginx Proxy Manager); full hardening in the copyable file; a dedicated `bekup-proxy` link network for external proxies; the files only as release downloads, pinned by digest and attested; the deployment file name `compose.yaml`; nothing committed under `examples/`)
- **Context:** D-203 made `bekup setup init` write the whole deployment
  directory, compose file included, from a `docker run` with a long list of
  hardening flags. Most operators start from a compose file and already run
  a reverse proxy. They need a short, copyable path that keeps every
  hardening property, works behind Traefik or Nginx Proxy Manager without
  weakening the proxy trust rules (D-048), and lets them verify the file
  they run. Facts that shape the design: Docker assigns fixed addresses
  only on networks created with a subnet; `docker compose run` starts a
  service of an inactive profile; nothing is released yet, so names and the
  state schema can still change.
- **Decision:**
  1. **One source, one image line.** `docker-compose.yml` stays the only
     compose source (the `full` variant). The bekup image is named only in
     the top-level line `x-bekup-image: &bekup-image
     ${BEKUP_IMAGE:-bekup:dev}`; `x-bekup-hardening` and the `setup`
     service use `*bekup-image`. Release and setup replace exactly that
     line with the digest-pinned literal. Every `./deploy/...` bind uses the
     long syntax, `read_only: true` and `create_host_path: false`.
  2. **Setup service in the base file:** profile `setup` (never started by
     `up`), `user: "0:0"`, `entrypoint: [/usr/local/bin/bekup, setup]`,
     `command: [init]`, `BEKUP_IMAGE: *bekup-image`,
     `DOCKER_GID: ${DOCKER_GID:-}`, read-only root, tmpfs `/tmp` noexec,
     `cap_drop: [ALL]`, `cap_add: [CHOWN]`, `no-new-privileges`,
     `network_mode: none`, `logging: {driver: none}`, `stdin_open`/`tty`,
     `restart: "no"`, limits (1 CPU, 512 MiB for the scrypt CA sealing, 64
     pids), and `.` bound at `/deploy` with `create_host_path: false`. The
     same service runs `upgrade` and `check`.
  3. **Variants are overlays, not copies:** `traefik` (Traefik and
     `traefik-init` without profile, nginx/certbot removed),
     `traefik-external` and `npm` (no bundled proxy, no `frontend`/`edge`
     network, no published ports, serve on the external link network;
     `traefik-external` adds Traefik labels on serve) and `full`
     (header only). `internal/setup/variants.go` holds per-variant edit
     lists applied by a path-addressed line editor
     (`internal/setup/outline.go`: remove/replace a node with its leading
     comments, insert after a node, replace leading comments, a
     free-standing comment block or the header), fragments in
     `internal/setup/variants/`. Untouched text stays byte-identical;
     fragments never contain `image:` or `build:` (tests); a variant ships
     only the `deploy/**` it mounts. `bekup setup compose --variant V
     [--image <pinned>]` prints a variant; `just compose-examples` writes
     them to the gitignored `dist/compose/`.
  4. **File names:** the deployment file is `compose.yaml` and the override
     `compose.override.yaml`, setup-first too. `compose.yml`,
     `docker-compose.y{a,}ml` and the other override names next to them are
     refused by init and check (docker compose would read them). The
     repository source keeps the name `docker-compose.yml`.
  5. **External proxies use a dedicated link network.** The operator runs
     once `docker network create --internal --subnet 10.213.253.0/28
     bekup-proxy` and attaches the proxy with `ipv4_address: 10.213.253.5`
     (four lines in its compose file, then a recreate). The variants declare
     the network external (`name: ${BEKUP_PROXY_NETWORK:-bekup-proxy}`);
     serve gets `ipv4_address: ${SERVE_PROXY_IP:-10.213.253.10}`, `listen =
     "10.213.253.10:8080"` (no wildcard, D-098) and `trusted_proxies =
     ["10.213.253.5/32"]`. Validation: a Docker network name; a private IPv4
     subnet between /8 and /29 that overlaps neither `runner-ctl` nor
     `metrics`; both addresses inside, distinct, never the network, gateway
     or broadcast address. `setup check` errors unless `trusted_proxies` is
     exactly the recorded proxy /32. Traefik labels on serve only:
     `traefik.enable=true`, `traefik.docker.network`, router rule
     ``Host(`${BEKUP_DOMAIN:-bekup.invalid}`)``, entrypoint
     `${TRAEFIK_ENTRYPOINT:-websecure}`, `tls=true`, certresolver
     `${TRAEFIK_CERTRESOLVER:-letsencrypt}`, service port 8080,
     `responseforwarding.flushinterval=-1ms`, no middlewares (bekup sets
     its own headers, D-102). NPM forwards `http` to `10.213.253.10:8080`
     with Websockets off (SSE, D-124), Let's Encrypt, Force SSL, HTTP/2, its
     HSTS off, and the Advanced block `proxy_http_version 1.1;
     proxy_buffering off; proxy_cache off; proxy_read_timeout 3600s;
     client_max_body_size 2m;`. serve logs a rate-limited WARN when
     `X-Forwarded-For` comes from a peer outside `trusted_proxies` (the
     header is ignored; only the peer is logged).
  6. **Compose-first `setup init`** (the default): it reads
     `/deploy/compose.yaml` safely, takes the image from the
     `x-bekup-image` line (it must also be the YAML value; `BEKUP_IMAGE`
     must match; `CheckVersionTag`), and recognizes the variant by
     rendering each release variant with that image: *exact* (byte-equal),
     *model* (same YAML model) or *customized* (a warning; `--variant` names
     the base; the hardening lint must report no error). It **never writes
     `compose.yaml`**. Without one it refuses with the release URL;
     `--write-compose --variant traefik|traefik-external|npm|full` is the
     setup-first path (`full` keeps `--proxy traefik|nginx|none`). Prompts:
     domain, ACME e-mail (bundled Traefik) and CA passphrase; the Docker GID
     from `DOCKER_GID`, otherwise a prompt; everything else from flags
     (`--traefik-entrypoint`, `--traefik-certresolver`, `--proxy-network`,
     `--proxy-subnet`, `--proxy-ip`, `--serve-ip`, ...).
  7. **State** (`bekup-setup.json`, schema 1 changed in place):
     `compose_mode` (`operator` | `generated`), `variant`, `proxy` (new
     value `external`), the link network (`proxy_network`, `proxy_subnet`,
     `proxy_ip`, `serve_ip`) and `compose_sha256` (operator mode). In the
     manifest `compose.yaml` is `operator` compose-first and
     `generated-upgradable` setup-first.
  8. **Upgrade:** compose-first the operator replaces `compose.yaml` with
     the new release file and runs `docker compose run --rm setup upgrade`:
     it refuses a downgrade and a variant switch, re-validates the TOMLs,
     rewrites `deploy/**` and `reference/*`, generates new secret groups,
     updates the state and never writes `compose.yaml` (`--write-compose`
     is refused). Setup-first upgrades need `--write-compose --image`.
  9. **Check:** every image and the `x-bekup-image` line pinned; no
     `build`, `include` or `extends.file`; the hardening lint
     (`lintCompose`) for a customized `compose.yaml` and for
     `compose.override.yaml`: `privileged`, host network/pid/ipc/userns/
     uts/cgroup, dangerous `cap_add`, unconfined `security_opt`, the Docker
     or Podman socket outside the runner, `ports` on anything but the
     bundled proxy, bekup services on another image than `x-bekup-image`,
     the setup service exactly as shipped (the override may not touch it);
     the recognized variant equals the recorded one; a newer image than
     recorded asks for `setup upgrade`, an older one is an error;
     variant-aware TOML/`.env` consistency.
  10. **Release assets** (extends D-204): a `compose` job renders
      `compose.yaml` (own Traefik), `compose.traefik-external.yaml` and
      `compose.npm.yaml` by running the published digest (`setup compose
      --image <ref>`, `--network none`, read-only, UID 65532, no logs),
      checks them with `scripts/release_ci.sh compose-assets` (exactly one
      `x-bekup-image`, equal to the release reference; every image pinned;
      no `build`), writes `SHA256SUMS`, attests all four with
      `actions/attest-build-provenance` (`subject-path`) and signs each with
      `cosign sign-blob` (keyless bundle `<file>.sigstore.json`); the
      release job attaches them with `gh release create`. The release notes
      show the three steps. Operators verify with `gh attestation verify
      compose.yaml --repo jlentink/bekup` or `cosign verify-blob`.
  11. **C0 spike (Docker 29.8, Compose 5.6, by hand):** `docker compose run
      --rm setup` works in a directory that holds only `compose.yaml`:
      missing `deploy/`, TOMLs and secret files and a missing external
      network do not stop `run` of a `network_mode: none` service, and
      `config -q` passes too. Prompts work on a TTY with `logging: none`
      (`compose run` attaches); `-T` with piped stdin works (CI). A fixed
      `ipv4_address` on an external network created with `--subnet` works.
      `up` fails with "network bekup-proxy declared as external, but could
      not be found" until the network exists, so the docs create it once
      before the three steps (setup itself does not need it).
  12. **Dev network moved:** the dev-only `runner-host` network of D-201
      used `10.213.253.0/24`, which overlaps the link subnet; it moves to
      `10.213.254.0/24` (runner `.10`). Existing dev stacks: `just
      dev-down` and `docker network rm bekup_runner-host` once.
- **Amendment 1 (2026-10-30, final review; proposed):**
  1. **The hardening lint judges the resolved model (replaces the
     deny-list of item 9).** A security review showed the raw-YAML lint
     bypassable: only the first YAML document was read (Compose merges
     every `---` document), merge-key precedence was implemented
     backwards (explicit keys must win; in a merge list the earlier
     mapping wins), `${VAR}` values from `.env` were invisible, and the
     deny-list missed many host-access keys. setup now loads
     `compose.yaml` + `compose.override.yaml` with **compose-go**
     (`github.com/compose-spec/compose-go/v2` v2.16.2, Apache-2.0, the
     loader Docker Compose itself uses) exactly as `docker compose` would:
     multi-document merge, merge keys, aliases, `!reset`/`!override`,
     same-file `extends`, interpolation from `.env` overridden by the
     setup container's environment for referenced variables, every
     profile enabled. A deny-all resource loader refuses `include` and
     `extends.file`; `env_file`/`label_file` contents are never read.
     Multi-document compose files and overrides are refused outright
     (`singleDocument`) wherever setup reads them. New dependencies:
     compose-go plus `mattn/go-shellwords` (MIT),
     `santhosh-tekuri/jsonschema/v6` (Apache-2.0),
     `xhit/go-str2duration/v2` (BSD-3) and `go.yaml.in/yaml/v4` (rc,
     Apache-2.0/MIT); `just sec` clean.
  2. **Allowlist on the canonical project** (`lintProject`): every service
     has a digest-pinned image (bekup services and any service on the
     bekup repository: the `x-bekup-image`), no `build`; `read_only:
     true`, `cap_drop` containing `ALL`, `security_opt` only
     `no-new-privileges:true` (required; no seccomp/AppArmor/label
     options), a numeric non-root `user` (UID and GID ≠ 0) except `setup`,
     `traefik-init` and `nginx-init` (the init services only with named
     volumes and `network_mode: none`); `cap_add` only `NET_BIND_SERVICE`
     (`setup`: `CHOWN`; init services: `CHOWN`, `FOWNER`); denied:
     `privileged` (hooks too; hooks never as root), `devices`,
     `device_cgroup_rules`, `volumes_from`, `gpus`, `pre_start`,
     `provider`, `develop`, `use_api_socket`, `runtime`, `cgroup_parent`,
     `isolation`, `credential_spec`, `storage_opt`, `volume_driver`,
     `models`, `sysctls`, devices in `deploy.resources`, top-level `jobs`
     and `models`; `network_mode`/`net` only `none`, `pid`/`uts` empty,
     `ipc` empty/`none`/`private`, `cgroup` empty/`private`,
     `userns_mode: host` a **warning** (the documented runner opt-out on
     userns-remap daemons), anything else an error; bind sources only
     strictly inside the deployment directory (`setup`: the directory
     itself; the runner additionally its Docker socket
     `/var/run/docker.sock`, `/run/docker.sock` or
     `/run/user/<uid>/docker.sock`, read-only at `/var/run/docker.sock`),
     propagation only `private`/`rprivate`, no `image`/`npipe`/`cluster`
     mounts, `env_file`/`label_file` inside the directory; top-level
     volumes only `local` without `driver_opts` and not external; networks
     only `bridge` without `driver_opts`, default IPAM, never named
     `host`/`none`; top-level secrets/configs `file` only inside the
     directory, not external, no drivers; `ports` only on the bundled
     proxy. The setup service is compared with the shipped one on the
     resolved model (merged with the override; the override is blamed when
     `compose.yaml` alone matches). A test reflects over compose-go's
     service and project attributes so an upgrade that adds one fails until
     it is reviewed.
  3. **Recognition uses the same model.** *exact* stays byte-equality;
     *model* now needs both the YAML model and the resolved project (same
     `.env`) to equal a release file's, so a merge-key, `!reset` or
     literal-vs-variable trick is always *customized* and linted. The lint
     always runs on compose.yaml + override (+ `.env`), also for exact
     files and setup-first deployments.
  4. **`.env` Docker client variables:** `DOCKER_HOST`, `DOCKER_CONTEXT`,
     `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` and `DOCKER_CONFIG` in `.env`
     are check errors (docker compose would deploy to another daemon or with
     another client configuration).
  5. **Ownership fixes on descriptors:** `applyOwnership` (init, upgrade,
     `check --fix-ownership`) changes modes and owners only on a descriptor
     from `openEntry`: each component opened with `openat(O_NOFOLLOW)`
     relative to its parent (os.Root alone follows in-root symlinks),
     `O_DIRECTORY` for directories, `O_PATH` on Linux (no read permission
     needed with only `CAP_CHOWN`), the type re-checked with `fstat`;
     `fchownat(AT_EMPTY_PATH)` and `chmod` of `/proc/self/fd/<n>`.
  6. **Link network command (L3):** `docker network create --internal
     --subnet 10.213.253.0/28 --gateway 10.213.253.1 --ip-range
     10.213.253.12/30 bekup-proxy`: `--ip-range` keeps Docker's automatic
     addresses away from the fixed proxy (`.5`) and serve (`.10`)
     addresses, so a container that joins first cannot take them;
     `--gateway` keeps the gateway at the first host (with only
     `--ip-range` Docker would use the first address of the range), which
     validation already reserves. setup prints the command for custom
     values (`LinkNetwork.CreateCommand`: the largest aligned block, at most
     /30, holding neither address; no `--ip-range` for a /29 where none
     fits). Verified with Docker (API 1.47). Replaces the command of item 5.
  7. **Proxy attachment:** the docs and the setup report attach the
     operator's proxy to `bekup-proxy` in addition to its existing networks
     (`default: {}` or the networks it already lists; a list becomes a
     map), never by replacing them (the "four lines" of item 5 detached it).
  8. **Running check (M1):** `docker compose -f compose.yaml run --rm setup
     check` (the override cannot change the container that runs the check,
     which still reads and lints it) or a hardened `docker run` of the
     `x-bekup-image` with the deployment directory mounted read-only.
  9. **Release integrity (L1, I3):** the `compose` job outputs one SHA-256
     over the `sha256sum` lines of all eight assets (`release_ci.sh
     assets-digest`); the `release` job recomputes it from the downloaded
     artifact and refuses to publish on a mismatch, before `compose-assets`,
     `sha256sum -c` and `gh release create` (the release job runs only
     `gh`, no cosign). The `verify` job passes the fetch auth header through
     `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0`, never argv.
     The release notes verify a compose file under its release name (`gh
     attestation verify`, or `sha256sum -c SHA256SUMS --ignore-missing`
     plus `cosign verify-blob --bundle <file>.sigstore.json`) before
     renaming it to `compose.yaml`.
  10. **Traefik names:** the labels name the router and service `bekup`;
      label keys cannot be interpolated, so a collision with another `bekup`
      router is documented (rename the other one, customize the file with
      `--variant traefik-external`, or use the file provider).
  Consequences: operators can no longer bind host paths (disk
  destinations mount the disk inside the deployment directory or use a
  named volume; the static-TLS example uses `./proxy-tls`), and services
  they add must carry the hardening baseline. Security impact: closes the
  reviewed bypasses (H1-H4); the lint matches what Compose runs for the
  variables visible to the setup container (shell-only variables on the
  host are not visible; documented).
- **Amendment 2 (2026-10-30, security re-check; proposed):** the
  allowlist of amendment 1 still let a customized file or an override
  change what bekup's own services run (H2: `environment`, `command`,
  `entrypoint`, e.g. `BEKUP_DEV=1`, `BEKUP_TARGETS_ALLOW_CIDRS`, `--dev`),
  take values from variables of the host shell that the setup container
  cannot see (H1), reach bekup's files, volumes and secrets from added
  services (M1), and change the network isolation (M2). The lint becomes
  structural:
  1. **Shipped services are pinned.** Every service the variant ships
     (`setup`, `certs-init`, `migrate`, `admin`, `serve`, `runner`,
     `postgres` and the variant's proxy services) must equal the shipped
     variant's resolved definition (same `.env`) in **every** compose-go
     service attribute, compared generically by reflection
     (`internal/setup/composepin.go` `shippedDiff`; a compose-go upgrade
     that adds an attribute is compared automatically). Only the explicit
     operator-tunable set `operatorTunable` may differ, each value
     validated: for every service but `setup` the cpus/memory/pids of
     `deploy.resources` (nothing else under `deploy`, no unlimited pids;
     every limit the shipped service sets stays set above 0, so
     `deploy: !reset null` or `pids: 0` is an error),
     legacy `cpus`/`mem_limit`/`mem_reservation`/`pids_limit` (> 0; a
     written `pids_limit` of 0 or less is refused from the YAML, since
     compose-go reads 0 as unset),
     `restart`, `stop_grace_period`; `profiles` of the bundled proxy
     services; on `traefik`/`nginx` `PROXY_TLS_MODE` (`acme`|`static`) and
     `PROXY_HTTPS_PUBLIC_PORT`, a read-only `./proxy-tls` bind at `/certs`
     and the published side of `ports` (same container ports, tcp); on the
     runner the Docker socket source (rootful or rootless path, read-only,
     no bind options, no SELinux relabeling) and `userns_mode: host` (a
     warning); on serve the source of its backups/scratch mounts and extra
     mounts below `/var/lib/bekup/backups/` (named volumes that are not
     bekup's other volumes, or binds inside the deployment directory
     outside bekup's files). `setup` has no tunables. The override is
     blamed when `compose.yaml` alone matches.
  2. **Shipped top-level objects are pinned:** the shipped networks
     (`internal`, driver, IPAM subnets, `name`, `external` of the link
     network), volumes, secrets and configs equal the shipped ones; the
     project name stays the shipped one, both resolved and as written
     (`name: ${ACME_CA_SERVER:-bekup}` resolves to `bekup` but is an
     error).
  3. **Added services** keep amendment 1's allowlist and additionally may
     not bind the keys directory (`secrets/` and `BEKUP_KEYS_DIR`), the
     state file, `.env`, the TOML and compose files, `deploy/` or
     `proxy-tls/` (or a parent; case-insensitive), mount a shipped named
     volume, use a shipped secret or config, or read such a file as
     `env_file`/`label_file`; no `group_add`, `external_links`,
     `oom_kill_disable`, negative `oom_score_adj`, real-time CPU settings
     or unlimited pids; `ulimits` only `nofile` (≤ 2^20) and `nproc`
     (≤ 65536), `shm_size` ≤ 1 GiB; logging only `json-file`, `local` or
     `none` with size/rotation options; no `traefik.*` or `com.docker.*`
     labels (also under `deploy.labels`). Networks: only networks they
     declare themselves (or `default`), `metrics` (the documented
     Prometheus attachment; serve answers only `/metrics` and `/healthz`
     there) and, in the `full` variant, `frontend` (the documented own
     proxy with `--proxy none`); never `backend`, `runner-ctl`,
     `targets`, `egress`, `edge` or the `bekup-proxy` link; no aliases on
     a shipped network and no `container_name` or service-level
     `mac_address` when joining one; no fixed `ipv4_address`/
     `ipv6_address`/`mac_address`/`link_local_ips` on a shipped network,
     except on `frontend` in the `full` variant, where a fixed address
     must differ from every address a shipped service (enabled or not)
     uses there (`PROXY_FRONTEND_IP`, `SERVE_FRONTEND_IP`), so an added
     container cannot take bekup's address on a network serve trusts. Added
     networks/volumes may not be `external` or carry a `name:` other
     than the project default; added secrets/configs no `environment:`
     and no file among bekup's own.
  4. **Interpolation allowlist.** compose.yaml and the override may
     reference only bekup's compose variables (`composeVarAllowlist`,
     equal to the set the shipped variants use; a test derives it), found
     by walking the YAML scalars (keys and values, not comments); any
     other `${VAR}`/`$VAR`, or a `$` that is neither `$$` nor a reference,
     is an error. The walk reads node text, so explicit YAML tags are
     limited to `!!str`, `!!int`, `!!bool`, `!!float`, `!!null`, `!!map`,
     `!!seq`, `!reset` and `!override` (`!!binary` would hide a
     `${VAR}` in base64 that compose-go then interpolates; custom tags
     too). Additionally each file is loaded a second time without
     interpolation and compared with the shipped file loaded the same
     way: a variable in a shipped service attribute where the shipped
     file has none (or another), or any variable in an added service or
     added top-level object, is an error (a file that cannot be loaded
     without interpolation is an error too). Variables therefore appear
     only where the shipped file has them.
  5. **Paths:** the project is resolved in a random sentinel working
     directory (`lintRoot`), so only relative paths count as inside the
     deployment directory (`/deploy/x`, `/deploy/../deploy/x` are host
     paths, L1). init, upgrade and check refuse a symlink on the way to
     every bind source, `env_file`, `label_file` and secret/config file
     inside the directory (`lintPathSymlinks`, M3; check also walks the
     whole directory). A managed file with a hard-link count other than 1
     is a check error and refused by the descriptor check before any
     chmod/chown (L2). SELinux relabeling (`z`/`Z`) is refused on every
     bind; `userns_mode: host` only on the runner (a warning), an error
     elsewhere.
  6. **The lint is a guardrail, not a proof.** A deployment counts as
     *verified* only when `compose.yaml` is the release file byte for
     byte (*exact*), there is no `compose.override.yaml`, and the
     recorded image is the file's `x-bekup-image`. Customized files and
     overrides are linted best-effort: the lint models compose-go
     v2.16.2 (Docker Compose v5.6.0 verified; minimum Docker Compose
     v2.24.4 for `!reset`/`!override`), cannot see variables set only in
     the host shell for the shipped variables, and a check that runs in a
     container the checked file itself defines (`docker compose run
     setup check`) is not trustworthy for a tampered file: the docs
     recommend the hardened `docker run` of the attested image with the
     directory mounted read-only for that. compose-go error messages may
     quote interpolated values (bounded; `.env` holds no secrets).
  Consequences: an operator can no longer change bekup's services beyond
  the tunable set (other changes need a new release or a decision);
  `userns_mode: host` for serve/migrate/certs-init (formerly suggested
  for userns-remap) is refused: chown their files to the remapped IDs
  instead. Security impact: closes H1, H2, M1-M3 and L1-L6 of the
  re-check for files whose variables are visible to the setup
  container; the residual risk (shell variables of the shipped variable
  set, older Compose versions, a tampered file running its own check) is
  documented in SECURITY.md (B10, SC-S4).
- **Amendment 3 (2026-10-30, user decision: commit the three copyable
  compose files; proposed):** the README linked release assets that do
  not exist before the first release (404). The variants are now also
  committed, which partly reverses the "release downloads only" part of
  the decision and the rejected "unpinned compose files with
  `${BEKUP_IMAGE}`" alternative, with the controls below.
  1. **Committed, generated files.** `deploy/compose/compose.yaml`,
     `compose.traefik-external.yaml` and `compose.npm.yaml` are written by
     `just gen` (`scripts/gen_compose.sh`: `bekup setup compose --variant
     V` without `--image`) and never edited by hand (the variant headers
     say so). They equal the release assets byte for byte except for the
     `x-bekup-image` line, which is exactly `ComposePlaceholderLine`:
     `x-bekup-image: &bekup-image ${BEKUP_IMAGE:?set BEKUP_IMAGE to the
     digest-pinned bekup image repo:tag@sha256:digest, see the README}` (no
     default, so docker compose refuses to run without it: "required
     variable BEKUP_IMAGE is missing a value"). `RenderVariant` without an
     image now emits this line (formerly `${BEKUP_IMAGE:-bekup:dev}`). The
     drift test `TestCommittedComposeFiles` compares them with the render
     of the embedded `docker-compose.yml`, checks the single differing line
     against the release render, and that `deploy/compose/` is neither
     embedded (`embed.go`), nor a variant asset directory, nor a managed
     asset path, and is excluded by `.dockerignore`. The variant headers
     describe both forms (so the files stay identical apart from that
     line).
  2. **Placeholder mode in setup.** Compose-first init, upgrade and check
     accept a `compose.yaml` whose `x-bekup-image` line is exactly the
     placeholder (any other `$` in that line, another default, message,
     tag or quoting is refused; the YAML value must equal it). The image
     is then the `BEKUP_IMAGE` docker compose resolves (the setup
     container's environment, which the setup service fills with
     `BEKUP_IMAGE: *bekup-image`, else `.env`); it must pass
     `imageref.ParsePinned` and `CheckVersionTag`. Recognition renders the
     variants with the placeholder: the committed files are *exact* or
     *model* for their variant (`ComposeInfo.FromEnv`), and the lint
     compares with the placeholder render. The interpolation allowlist
     admits `BEKUP_IMAGE` only as the value of the top-level
     `x-bekup-image` key of `compose.yaml` when it is exactly the
     placeholder (never in the override, never elsewhere). Init writes
     `BEKUP_IMAGE=<pinned>` as the first variable of `.env` (also into an
     existing `.env` with `--keep-existing`), so later `docker compose`
     commands resolve it without a shell variable, and records
     `image_source: env` in `bekup-setup.json` (compose-first only).
  3. **Check (placeholder mode):** `.env` must name `BEKUP_IMAGE`,
     digest-pinned and equal to the recorded image (older: error with the
     downgrade warning; other: error asking for `setup upgrade`); a
     `BEKUP_IMAGE` in the environment that differs from `.env` is an error
     (docker compose would run it); the bekup services must resolve to the
     recorded image (the lint already requires every bekup service to use
     the `x-bekup-image`). A switch between the committed file and a
     release file without `setup upgrade` is an error. With a release file
     a `BEKUP_IMAGE` in `.env` stays a warning ("no effect").
  4. **Upgrade (placeholder mode):** the operator runs
     `BEKUP_IMAGE=<new> docker compose run --rm setup upgrade` (or edits the
     `.env` line and runs `docker compose run --rm setup upgrade`), so the
     *new* image's setup runs; `--image <new>` alone would run the old
     image and is refused with that command. Upgrade rewrites only the
     `BEKUP_IMAGE` line of `.env` (the one exception to "upgrade never
     touches .env") and records the image; switching to a release file
     removes the line and records `image_source` empty.
  5. **Release:** unchanged assets (literal pin). The `compose` job also
     runs `release_ci.sh committed-compose`: the tag's `deploy/compose/*`
     must equal the rendered assets except for the `x-bekup-image` line,
     which must be exactly the placeholder (a Go test keeps the script's
     copy of the line equal to `ComposePlaceholderLine`).
  6. **Renovate:** `renovate.json` disables `deploy/compose/**` (Renovate
     bumps only `docker-compose.yml`); its PRs fail the CI `generated` job
     and the drift test until `just gen` is run and committed on the PR
     (docs/development.md).
  7. **`just publish`** prints the pin with its first tag
     (`<repo>:<tag>@sha256:...`) and notes when the tag does not name the
     binary version (`--allow-version-mismatch`).
  Consequences: the README quick start works today with the raw GitHub URL
  of the committed file plus a digest-pinned `BEKUP_IMAGE`; the release
  download stays the attested path. Security impact: the committed file is
  not attested (fetched from `main` or a tag over HTTPS; compare it with
  the repository at the image's tag) and is never *verified* in the sense
  of amendment 2 item 6; the image is not in the file but in `.env`
  (root-owned, 0644, in the root-owned deployment directory, so whoever
  can change it can change `compose.yaml` too) and in the shell of the
  setup run. A shell `BEKUP_IMAGE` overrides `.env` for every `docker
  compose` command: setup refuses an unpinned value, `check` reports any
  override as an error, but a `docker compose up` with a deliberately set
  shell variable runs that image (the operator's own command; documented).
  Pinning by digest and the version check apply as before.
- **Alternatives considered:** committed copies of each variant under
  `examples/` (drift; rejected by the user); full YAML re-serialization of
  the source (loses comments and byte-identity of untouched text); `include`
  / `extends` from a shared file (the operator would need several files and
  `check` could not see through them); a variant `frontend` network or the
  proxy joining bekup's project network (a fixed proxy address then depends
  on bekup's project, and Docker gives fixed addresses only on networks with
  a subnet); trusting the whole link subnet (any container the operator
  attaches could forge `X-Forwarded-For`); letting setup write
  `compose.yaml` always (an upgrade could not be a plain, verifiable file
  replacement); unpinned compose files with `${BEKUP_IMAGE}` (a shell
  variable could swap the image).
- **Consequences:** Installing needs Docker, one downloaded file and two
  commands; the copyable file carries the full hardening and pins the image
  by digest, and an upgrade is a verified file replacement plus `setup
  upgrade`. Security impact: the compose file starts a root container with
  `CAP_CHOWN` and the runner with the Docker socket, so it is a release
  asset with build provenance and a cosign bundle, rendered from the scanned
  and signed image, and the docs require verifying it before use; a
  customized file must pass the hardening lint, and `check` keeps linting it
  and the override. Behind an external proxy the only shared network is the
  internal /28 link with fixed addresses and an exact /32 trust; an external
  Traefik with the Docker provider holds the Docker socket (root on the
  host; SECURITY.md RP-8, documented alternatives: socket proxy or file
  provider). The setup service runs without logs, so the CA passphrase
  prompt never reaches Docker's log files. Not verified end to end: a real
  external Traefik reading the labels (compose config and unit tests
  only).
- **Related:** D-017, D-048, D-061, D-093, D-098, D-102, D-124, D-201,
  D-203, D-204; tasks 6.14, 6.14b, 7.4; `docker-compose.yml`,
  `docker-compose.dev.yml`, `internal/setup/{outline,variants,composefile}.go`,
  `internal/setup/variants/`, `deploy/compose/`, `scripts/gen_compose.sh`,
  `renovate.json`, `cmd/bekup/setup_cmd.go`,
  `internal/httpserver/middleware.go`, `.github/workflows/release.yml`,
  `.github/workflows/ci.yml`, `scripts/release_ci.sh`, `justfile`,
  `README.md`, `docs/installation/{compose,reverse-proxy,upgrades}.md`,
  `SECURITY.md`, `AGENTS.md`.

## D-206: Mobile apps and the push channel dropped from the roadmap

- **Date:** 2026-10-29
- **Status:** proposed
- **Decided by:** agent (recording the user's decision to remove every
  iOS/Android app reference and the reserved push channel; Phase 2 keeps
  only the external API)
- **Context:** The plan listed native iOS and Android apps as Phases 3–4
  and push notifications (FCM, device registration) for Phase 2. D-023
  reserved a **push** channel for them, and D-161, D-183, D-191 and D-193
  carried it along: the `notification_channel` and `user_channel_type`
  enums of migration 00028 had a `push` value, `notify.ChannelPush`
  existed but was never delivered, and the preference matrix showed a
  disabled push column. The user decided that bekup will not ship mobile
  apps, so the reserved channel has no consumer left.
- **Decision:**
  1. **Roadmap:** Phases 3–4 (native apps) are removed from the plan;
     Phase 2 is only the isolated external API (`bekup external-api`, the
     `api.*` views, scoped API tokens, its web UI). Device registration,
     QR pairing and FCM/APNs push are out of scope.
  2. **Channels:** the notification channels are inbox, e-mail and
     webhook (personal and shared). The push parts of D-023 ("push is
     added in Phase 2"), D-161 ("webhooks/push are opt-in": now webhooks
     are opt-in), D-183 (items 3 and 6: push defaults and the reserved
     `user_channels` push type), D-191 (item 5: push out of the API until
     phase 2) and D-193 (item 5: the disabled push column) are superseded;
     the rest of those decisions stands.
  3. **Code and data:** `notify.ChannelPush` and the unused
     `notify.UserChannels` are removed; `notify.MatrixChannels` (inbox,
     e-mail, webhook) stays the single list of matrix channels and the
     GraphQL `NotificationChannelType` enum is unchanged (it never had a
     push value). Migration `00033_drop_push_channel.sql` deletes any
     stored `push` preference rows (none in practice: the API never
     accepted one, deliveries and personal channels refused it by CHECK)
     and recreates both enums without `push` (PostgreSQL cannot drop an
     enum value), re-adding the CHECK constraints that compare with enum
     literals; its Down section appends `push` again. The web matrix has
     no push column any more.
  4. A future channel goes into `notify.MatrixChannels`, the GraphQL enum
     and the `notification_channel` DB enum (a new migration), with its
     own decision.
- **Alternatives considered:** keeping the reserved value "just in case"
  (dead code and a misleading UI column for a feature that will not
  come); only narrowing CHECK constraints and leaving the enum values
  (the generated `sqlc` constants and the schema would keep advertising
  push); editing migration 00028 (forbidden: released migrations are
  forward-only).
- **Consequences:** Less surface: no reserved channel type that a later
  change could start delivering to without a review, no push service
  credentials or third-party push providers in the threat model, and
  Phase 2 of `SECURITY.md` covers only the external API. The migration
  takes short exclusive locks on `user_notification_prefs`,
  `notification_deliveries` and `user_channels` while the columns are
  rewritten (small tables; run during the normal upgrade). Security
  impact: none weakened; deliveries, preferences and authorization are
  unchanged for the remaining channels.
- **Related:** D-023, D-161, D-183, D-191, D-193; plan Phase 2;
  `internal/notify/prefs.go`, `internal/notify/api_prefs.go`,
  `internal/db/migrations/00033_drop_push_channel.sql`,
  `internal/db/schema.sql`, `internal/graph/schema/notifications.graphqls`,
  `web/src/features/notifications/`, `docs/guides/notifications.md`,
  `SECURITY.md`, `AGENTS.md`.
