# Security policy and threat model

bekup holds credentials for production databases and full copies of their
contents, and it can **write** to those databases (restore). A compromise is
catastrophic, so security is the top priority, with no compromises (D-001).

This document is the threat model. It was written first (task 0.2) and is kept
current: **every change to a trust boundary updates this file** (plan working
rules). The authoritative design lives in
[`plans/backup-databases-abstract-corbato.md`](plans/backup-databases-abstract-corbato.md)
and the decision log [`docs/DECISIONS.md`](docs/DECISIONS.md). Task IDs below
(`0.3`, `2.7a`, ...) refer to the plan's checklist; `D-NNN` refers to the
decision log.

> **Status:** bekup is pre-release (`VERSION` 0.0.0). Most mitigations listed
> here are *planned* and land with the task named next to them. A mitigation
> is only in effect once its task is checked off in the plan.

Contents:

1. [Reporting a vulnerability](#1-reporting-a-vulnerability)
2. [Security principles](#2-security-principles)
3. [Assets](#3-assets)
4. [Actors and adversaries](#4-actors-and-adversaries)
5. [Trust boundaries and data flows](#5-trust-boundaries-and-data-flows)
6. [STRIDE analysis per component](#6-stride-analysis-per-component)
7. [Mitigation to task map](#7-mitigation-to-task-map)
8. [Residual risks and out of scope](#8-residual-risks-and-out-of-scope)
9. [Open items](#9-open-items)
10. [Deployment hardening summary](#10-deployment-hardening-summary)

---

## 1. Reporting a vulnerability

**Do not open a public issue, pull request or discussion for a security
problem.**

Report it privately through **GitHub private security advisories**:
<https://github.com/jlentink/bekup/security/advisories/new>
("Security" tab → "Report a vulnerability").

Please include the affected version (`bekup version`) or commit, the component
(`serve`, `runner`, web UI, CLI, compose files), reproduction steps or a proof
of concept, and the impact you expect. Do not test against systems you do not
own, and do not access or keep data that isn't yours.

### Supported versions

| Version | Supported |
|---|---|
| Latest release (`main` before the first release) | Yes |
| Older releases | No; upgrade to the latest release |

Before 1.0 only the latest release receives fixes. From 1.0 on, the latest
minor release line receives security fixes; this table is updated when that
changes.

### Response targets

| Step | Target |
|---|---|
| Acknowledge the report | 3 business days |
| Initial triage and severity (CVSS) | 7 days |
| Fix for critical/high severity | 30 days |
| Fix for medium/low severity | 90 days |
| Coordinated public disclosure | when a fix is released, at most 90 days after the report unless agreed otherwise |

Fixes ship as a new signed release (cosign + provenance, task 7.4) with a
GitHub security advisory and, where applicable, a CVE. Reporters are credited
unless they prefer otherwise.

---

## 2. Security principles

From D-001 and the plan:

- **Security wins.** When convenience and security conflict, security wins.
- **Secure by default; weakening is loud.** Any weakened setting (backup
  encryption opt-out, TLS-off/unpinned connection) needs an **admin with
  step-up auth**, shows a **permanent warning badge**, raises a **security
  alert** and is **audited** (D-001, D-008, D-034).
- **Least privilege and separation.** The web backend (`serve`) never talks to
  Docker or to target databases; the **runner** is the only component with
  Docker access, has no app-DB access and exposes only typed RPCs (D-003,
  D-030). App DB roles are split (`bekup_owner` / `bekup_app`, D-054). Roles in
  the UI are deny-by-default (D-011, D-053).
- **Defence in depth.** Proxy TLS *and* security headers in `serve` (D-048);
  native-client TLS pinning *and* a Go SPKI pre-flight check (D-034);
  manifest signature *and* DB cross-check (D-038).
- **Secrets are never inline and never readable back.** Only `*_file` paths in
  config (D-019, D-041); write-only in GraphQL (`hasPassword`); envelope
  encrypted at rest (D-046); never in env, argv, logs, River job args or
  container inspect output (D-032, D-043, D-060).
- **Pin what we own.** Every TLS link bekup is responsible for is pinned
  (runner mTLS CA, target DB certs, SSH host keys). Third-party services (S3,
  SMTP, webhooks) use system roots with TLS ≥1.2 (D-018, D-049).
- **Verify before trust.** Restores stage, verify hash + signature + DB
  records, then restore (D-022, D-039).
- **Recoverable without bekup.** Documented artifact format and offline
  `bekup verify` / `bekup decrypt` (D-040).
- **Supply chain is part of the attack surface.** Digest-pinned images,
  SHA-pinned Actions, scanners, SBOM and signed releases (D-061).

---

## 3. Assets

| # | Asset | Where it lives | Why it matters | Primary protections |
|---|---|---|---|---|
| A1 | **Backup artifacts** (full DB contents, globals incl. role password hashes) | Destinations (disk, S3, SFTP); scratch volume during restore; browser during downloads | Confidentiality of all data; availability for recovery | age encryption on by default (D-008); sha256 + signed manifest (D-038); Object Lock / no-delete mode (D-007); downloads only for admins/owners with step-up (D-012, D-056) |
| A2 | **Target DB credentials** (passwords, client certs/keys, SSH keys/passwords for tunnels) | `connections.secret_enc` in the app DB; in transit serve → runner → dump container stdin | Read (and for restore roles, write) access to production DBs | AES-256-GCM envelope with AAD (D-046); write-only API; stdin preamble only (D-032); zeroed after use |
| A3 | **Destination credentials** (S3 keys, SFTP keys/passwords) | `destinations.secret_enc` | Read/delete of all backups | Envelope encryption; no-delete credentials mode (D-007) |
| A4 | **Master key** (keyring) | `master_key_file` (Docker secret, 0600) | Decrypts every stored secret (A2, A3, A5, A8, SMTP/webhook secrets, TOTP secrets) | `*_file` only, permission checks (D-041); key-check value at startup; rotation; escrow guidance (D-016, D-046) |
| A5 | **Job age passphrases** (versioned) and **age identities** | `job_passphrases.secret_enc`; identities are *never stored* (shown/downloaded once), only passed at restore/download time via the encrypted handoff table (15 min TTL) | Decrypt the backups | Envelope encryption; identities never in River args (D-039); passphrase export admin + step-up + audited (D-040) |
| A6 | **Manifest signing key** (ed25519 instance keys with key IDs) | `instance_keys` | Forged manifests would make tampered or replayed artifacts look valid; also signs audit chain heads | Key IDs + retained old public keys; fingerprint escrow (D-038, D-040) |
| A7 | **Runner mTLS CA and certs** | CA key outside shared volumes; per-component key + CA cert as Docker secrets | Whoever holds a valid client cert can drive the runner (and thus Docker) | Private CA, SAN identity, TLS 1.3, expiry metric/alert, rotation (D-030; task 0.6) |
| A8 | **User credentials and second factors** (argon2id hashes, TOTP secrets, WebAuthn credentials, recovery codes) | `users`, `webauthn_credentials`, `recovery_codes` | Account takeover | argon2id, TOTP secret encrypted, replay protection, lockout (D-010, D-047) |
| A9 | **Sessions, step-up state, setup token, download tickets** | `sessions` (sha256 of token), `download_tickets` | Session hijack → act as user | `__Host-` cookie, HttpOnly/Secure/SameSite=Strict, rotation, expiry, revocation; single-use 60 s session-bound tickets (D-047, D-056) |
| A10 | **Audit log** | `audit_log` (hash chain) + signed chain heads exported to destinations/webhook | Accountability; detection of abuse | SECURITY DEFINER hash trigger, revoked UPDATE/DELETE/TRUNCATE, `bekup audit verify`, exported signed heads (D-054) |
| A11 | **User accounts and role assignments** (incl. job ownership) | `users`, `job_owners` | Privilege escalation | Admin-only user CRUD, step-up for role changes, session rotation on privilege change, new-admin alerts (D-011, D-047; tasks 1.6, 5.5) |
| A12 | **Target databases themselves** | Customer infrastructure | **Restore = write access**; a wrong or malicious restore destroys or corrupts production data | Restore gate (fresh 2FA, D-014/D-057), target rules (D-013), type-to-confirm, PG temp DB + swap, hardened restore clients (D-036) |
| A13 | **Configuration and pins** (`bekup.toml`, `runner.toml`, image/target allowlists, DB cert pins, SSH host-key pins, SSRF allowlist) | Config files (read-only mounts); `connections`/`destinations`/`settings` rows | Weakening any of these widens the attack surface | Separate runner config serve can't change (D-019); strict parsing, file-safety checks (D-041); pin changes admin + step-up + audited (D-034, D-035) |
| A14 | **Availability of the backup service** (schedules, runs, alerts) | `serve`, runner, River, scheduler | Silent backup failure = data loss later | Dead-man alerts (D-024), metrics, retention keep-newest safety (D-005) |

---

## 4. Actors and adversaries

| # | Actor | Capabilities assumed | Goal | In scope |
|---|---|---|---|---|
| T1 | **Internet attacker** | Reaches the reverse proxy over HTTPS; no account | Account takeover, RCE, data theft, DoS | Yes |
| T2 | **Malicious low-privilege user (backup owner)** | Valid account, owns some jobs; may control a target DB of their own jobs | Read other jobs' data, write to other DBs, escalate to maintainer/admin | Yes |
| T3 | **Compromised or malicious maintainer** | All jobs/runs/restores, owner assignment, connection/destination *metadata*; no secrets, no downloads, no CRUD on connections/destinations | Exfiltrate data, destroy data via restore, escalate | Yes |
| T4 | **Compromised `serve`** (RCE in the Go backend or a dependency) | Everything `serve` holds: master key, `bekup_app` DB role, runner client cert, signing key, destination access | Steal secrets/backups, drive the runner, rewrite history | Yes; damage is **bounded**, not prevented (see §8) |
| T5 | **Compromised runner** | Docker socket ⇒ effectively root on the Docker host | Anything on that host | Partially: we minimise the runner's attack surface; post-compromise is out of scope (§8) |
| T6 | **Malicious or compromised target DB server** | Controls responses to `Probe`, dump output, error messages, TLS handshake | Exploit dump clients, the runner or `serve`; inject content that executes on restore; fill storage | Yes |
| T7 | **Malicious or compromised destination/storage provider** | Read/modify/delete/replay stored objects; slow or fail uploads | Read backups, make bekup restore tampered data, destroy backups | Yes |
| T8 | **Network attacker (MITM)** | On-path between any two components or to external services | Intercept credentials/data, impersonate DBs, SSH/SFTP hosts, runner | Yes |
| T9 | **Supply-chain attacker** | Compromises a Go/npm dependency, a base/dump image, a GitHub Action or the release pipeline | Backdoor bekup or the dump tools | Yes |
| T10 | **Host operator** (root on the Docker host, or access to Docker secrets/volumes) | Read memory, secrets, volumes; run `bekup admin` | Anything | **Out of scope** (trusted); partially mitigated by audit and alerts on break-glass actions |

---

## 5. Trust boundaries and data flows

### Components and boundaries

```
 [T1 internet]          ══ B1 ══                       ══ B2 ══
 browser ──HTTPS──> Traefik | nginx (TLS termination, profile) ──HTTP (net: frontend, internal)──┐
                    ┌──────────────────────────────────────────────────────────────────────────────────┘
                    bekup serve  (GraphQL, sessions, UI, scheduler, River workers: backup/restore/
                    │             retention/notify; pipeline; uploads to destinations)
       ══ B3 ══     ├─ pgx (role bekup_app) ─────────> Postgres app DB
       ══ B4 ══     ├─ HTTPS/SFTP/SMTP (SSRF-guarded) > destinations, SMTP, webhooks      [T7, T8]
       ══ B5 ══     └─ gRPC + mTLS (net: runner-ctl) ─> bekup runner ── docker.sock ──> dockerd   ══ B6 ══
                                                         │ per run: creates an internal Docker network,
                                                         │ joins it, runs a TCP proxy / SSH tunnel to ONE target
                                                         └─> dump container (only on the per-run internal net;   ══ B7 ══
                                                             ExtraHosts: db-host → runner proxy IP) ──> target DB  ══ B8 ══ [T6, T8]
```

| Boundary | Between | Controls | Decisions / tasks |
|---|---|---|---|
| **B1** | Browser ↔ reverse proxy | TLS 1.2+/1.3, modern ciphers, HSTS, HTTP→HTTPS redirect, size limits | D-017, D-048; 0.7b |
| **B2** | Reverse proxy ↔ `serve` | `serve` only on internal `frontend` network (port not published); `X-Forwarded-*` trusted only from `trusted_proxies`; https `base_url` required; CSP and headers set by `serve` too | D-017, D-048, D-050; 0.7, 1.1 |
| **B3** | `serve` ↔ app DB | `bekup_app` DML only; migrations as `bekup_owner`; secrets envelope-encrypted; append-only hash-chained audit log | D-042, D-046, D-054; 0.4, 0.5, 1.3 |
| **B4** | `serve` ↔ destinations, SMTP, webhooks | Dial-time SSRF guard, admin allowlist, no redirects, TLS ≥1.2 system roots (optional CA bundle), SFTP host-key pin; disk via `os.Root` | D-049, D-055; 2.3, 2.8–2.10, 5.1, 5.2 |
| **B5** | `serve` ↔ runner | mTLS (TLS 1.3, private CA pinned, SAN check), typed RPCs, unknown fields rejected, ≤1 MiB chunks, flow-control backpressure | D-030, D-033; 0.6, 2.4, 2.5 |
| **B6** | runner ↔ Docker daemon | Image allowlist from runner's own config (digest-pinned); runner-built fixed hardened spec; label-based orphan reaper | D-031; 2.1, 2.5, 2.6 |
| **B7** | dump container ↔ runner / other networks | Alone on a per-run `internal: true` network; can only reach the per-run proxy; no runner gRPC, no other runs, no internet; secrets only via stdin preamble | D-029, D-032; 2.6 |
| **B8** | runner proxy / dump client ↔ target DB | Target allowlist (loopback, link-local, metadata always denied); `verify-full`/`VERIFY_IDENTITY` with the pinned cert as the only trust anchor + Go leaf-SPKI pre-flight; SSH tunnels with mandatory host-key pins | D-018, D-029, D-034, D-035; 2.2, 2.5, 2.7a |

Separate configuration files are themselves a boundary: `serve` reads
`bekup.toml`, the runner reads `runner.toml`, so nothing `serve` controls (DB
rows, its own config) can change the runner's image or target allowlists
(D-019).

### Data flows

**Backup** (task 3.6):
1. Scheduler (or `runNow`) enqueues a River job (IDs only, no secrets).
2. Worker decrypts the connection secret (A2) in memory and calls runner
   `Dump` over mTLS (B5).
3. Runner validates the request, checks the target against its allowlist,
   checks the pinned leaf SPKI, creates the per-run network + proxy/tunnel,
   starts the allowlisted image with the fixed spec, writes secrets to the
   container's stdin preamble (B6, B7, B8), then zeroes its buffers.
4. The dump streams back (≤1 MiB chunks) → counter → zstd → age → sha256 →
   bounded fan-out to each destination (B4). Signed manifest written last.
5. `VerifyDump` (e.g. `pg_restore --list`); statuses/logs (redacted, capped)
   into the app DB; events → notifications.

**Restore** (tasks 4.1–4.5):
1. `restoreArtifact` requires a fresh 2FA check (≤5 min) and target rules;
   user-supplied age identity goes into the encrypted handoff table (15 min
   TTL), never River args.
2. Worker stages the *encrypted* artifact on the scratch volume, verifies
   sha256 + manifest signature + DB cross-check, then decrypts → decompresses
   → streams to runner `Restore` (client-streaming).
3. Runner runs the hardened restore client (PG into a temp DB + swap;
   MySQL/MariaDB in place after explicit warning). Staging files deleted on
   every path.

**Download** (task 3.9): `createDownloadTicket` (admin/owner, step-up,
audited) → single-use 60 s session-bound ticket → `GET /api/download/{ticket}`
streams raw (encrypted) or plain (decrypted + decompressed) with `no-store`.

**Notifications** (tasks 5.1–5.3): event → recipient resolver (authz at
delivery time) → per-(recipient, channel) River jobs → inbox / SMTP / HMAC
webhook through the SSRF guard (B4). Payloads carry minimal data, never
secrets or DB contents.

**Offline recovery** (task 4.7): `bekup verify` / `bekup decrypt` run on an
operator machine against artifacts, with no access to the bekup DB.

---

## 6. STRIDE analysis per component

Legend: **S** spoofing, **T** tampering, **R** repudiation, **I** information
disclosure, **D** denial of service, **E** elevation of privilege. Each row
lists the threat, the mitigations, the implementing task(s) and the residual
risk.

### 6.1 Browser / web UI

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| UI-1 | S | Phishing or credential stuffing to log in as a user | argon2id, password policy (≥12, strength check, common-password list), lockout with backoff, generic timing-safe errors, 2FA (passkeys phishing-resistant, TOTP), admin-configurable 2FA requirement | 1.5, 1.7, 1.8, 1.9 | TOTP is phishable; admins should require passkeys for privileged roles |
| UI-2 | T/E | XSS injecting script into the UI (e.g. via job names, DB names, run logs from a malicious target DB) | React escaping; strict CSP `script-src 'self'` (style-src settled in 0.8); nosniff; no inline scripts | 0.8, 1.1, 6.x, 7.3 | A CSP-bypass bug in a dependency; mitigated by supply-chain controls |
| UI-3 | T | CSRF / cross-site actions | `SameSite=Strict` `__Host-` cookie; GraphQL POST + `application/json` only; Origin/`Sec-Fetch-Site` checks on mutations and WS upgrade | 1.1, 1.2, 1.10 | None significant |
| UI-4 | T | Clickjacking of destructive actions (restore, opt-out) | `frame-ancestors 'none'`; type-to-confirm on overwrites; step-up dialogs | 1.1, 4.5, 6.7 | None significant |
| UI-5 | I | Session token theft from the browser | HttpOnly cookie (not readable by JS); Secure; token stored server-side as sha256 | 1.5 | Malware on the user's device (out of scope) |
| UI-6 | I | Sensitive data cached or leaked (downloads, referrers) | `Cache-Control: no-store` on downloads; `Referrer-Policy: no-referrer`; secrets never returned by the API; age private keys shown once and never stored | 1.1, 3.2, 3.9 | A downloaded plain file is the user's responsibility once on their device |
| UI-7 | R | User denies a sensitive action | All sensitive actions audited with user and session; step-up timestamps recorded | 1.3, 1.9 | — |

### 6.2 Reverse proxy (Traefik / nginx profiles)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| RP-1 | S/I | MITM or TLS downgrade between browser and proxy | TLS 1.2+/1.3, modern ciphers, HSTS (preload-ready), HTTP→HTTPS redirect; `testssl.sh`/sslyze check in AC | 0.7b | Misconfigured custom proxies (generic examples documented) |
| RP-2 | S | Spoofed `X-Forwarded-For/Proto` to bypass rate limits or https checks | `serve` trusts forwarding headers only from `trusted_proxies` (required outside dev) | 1.1 | Wrong CIDR configured by the operator |
| RP-3 | S/E | Direct access to `serve` bypassing the proxy | `serve` listens on the internal `frontend` network only, port never published | 0.7, 0.7b | Operator publishes the port manually |
| RP-4 | D | Oversized requests / slowloris | Proxy request size limits; `serve` size limits and rate limits | 0.7b, 1.1 | Volumetric DDoS is out of scope |
| RP-6 | E | Proxy holding the Docker socket (root-equivalent on the host) | Traefik uses the file provider; the nginx profile is unprivileged nginx + certbot (D-093, D-094); no proxy container mounts the Docker socket | 0.7b | None while the profiles are used as shipped |
| RP-7 | I | Plain HTTP without a proxy (D-095) | Explicit opt-in (`server.allow_insecure_http` or dev mode), startup warning, permanent UI banner, admin security alert + audit outside dev; cookies/passkeys adapt | 0.3a, 1.1, 5.5, 6.1 | Credentials and sessions travel in clear text when the operator opts in |
| RP-5 | T | Compromised proxy image | Digest-pinned images (Renovate), Trivy | 0.7b, 0.9, 7.4 | A compromised proxy sees plaintext UI traffic (sessions, passwords); TLS for the UI is delegated by design (D-017) |

### 6.3 `serve`: HTTP, GraphQL, authentication and authorization

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| SV-1 | S | Session hijack / fixation | Session rotation on login and privilege change; idle + absolute expiry; revoke-all on password change; user-visible session list/revoke | 1.5, 1.6 | — |
| SV-2 | S | TOTP code replay; brute force of second factors | Last-used-step replay protection; lockout with backoff; recovery codes single-use | 1.5, 1.7 | — |
| SV-3 | S | First-run takeover before the admin exists | One-time setup token with 1 h TTL | 1.6 | Operator must keep the token private |
| SV-4 | E | Broken access control (IDOR, missing role check) | Central deny-by-default `internal/authz`, `@hasRole` + per-object ownership; matrix test fails CI on unmapped operations | 1.4, every GraphQL task | Logic bugs in ownership predicates; covered by matrix + 7.3 |
| SV-5 | E | Sensitive action with a stolen but old session | Step-up (≤5 min) for secret changes, restores, download tickets, user/role changes, opt-outs, 2FA changes, passphrase export, key operations; restores require passkey/TOTP (password alone is not enough) | 1.9, 4.5 | Step-up window of 5 min |
| SV-6 | E | Maintainer reaching secrets or downloads | Maintainers see non-secret metadata only, no CRUD on connections/destinations, downloads denied in the authz matrix | 1.4, 2.11, 2.12, 3.9 | See open item OI-1 (owner self-assignment) |
| SV-7 | E | Owner restoring over a DB they don't own | Owners may restore only into DBs their own jobs back up, or a new DB on those connections; temporary connections admin-only | 4.4, 4.5 | — |
| SV-8 | I | Secrets read back via the API | Secrets write-only (`hasPassword`); masked errors; introspection only for authenticated users | 1.2, 2.11, 2.12 | — |
| SV-9 | I | Host-header poisoning of links (password/email/notification links) | All links built from `base_url`, never from `Host`; https required outside dev | 0.3, 1.1 | — |
| SV-10 | D | GraphQL resource exhaustion; login floods; WS floods | Depth/complexity/size limits; per-IP and per-user rate limits; argon2id semaphore; WS connections per user capped | 1.1, 1.2, 1.5, 1.10 | — |
| SV-11 | I/E | WS subscription leaks data after logout or role change | Cookie auth + Origin on upgrade; periodic and per-event session re-validation; authz at delivery time; socket closed on logout/revoke/role change; NOTIFY carries IDs only | 1.10, 3.7 | Short window until the next periodic check |
| SV-12 | R | Admin denies weakening security or changing roles | Audit log for every sensitive action; security alerts for opt-outs, new admins, key/cert rotation | 1.3, 5.5 | See T4 for a compromised `serve` |
| SV-13 | I | Secrets in logs | slog redaction handler; static messages; known secret values redacted from runner logs | 0.3, 3.6 | Unknown secret formats in third-party error strings |
| SV-14 | T | Download ticket reuse or theft | Single-use, 60 s, bound to the session; step-up + audit on creation | 3.9 | — |

### 6.4 Workers and pipeline (backup, restore, retention, notify; River)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| WK-1 | I | Secrets persisted in River job args / tables | Job args never contain secrets; age identities go to an encrypted handoff table with 15 min TTL, deleted after use | 3.4, 4.1 | — |
| WK-2 | T | Tampered or swapped artifact restored (T7) | sha256 verified on the staged copy; ed25519-signed manifest with key ID; cross-check against DB records (defeats replay of old manifests) | 3.1, 4.1 | Compromised `serve` can sign (T4) |
| WK-3 | I | Plaintext backups at rest on destinations | age encryption on by default; opt-out admin-only with step-up, alert, badge, audit; bounded `SetMaxWorkFactor` | 3.1, 3.2 | Opted-out jobs are plaintext (visible, deliberate) |
| WK-4 | I | Plaintext on the scratch volume during restore | Stage the encrypted artifact; opt-out jobs' staging wrapped with an ephemeral age key; size check; deletion on every path | 4.1 | Decrypted data is in memory while streaming |
| WK-5 | D | Slow or failing destination stalls a run (T7) | Bounded buffer per destination; laggards dropped → `partial`; multipart abort; per-worker timeouts | 3.1, 3.6, 3.4 | `partial` runs need operator attention (alerts) |
| WK-6 | T | Partial/corrupt object left behind | Disk/SFTP write `*.partial` + atomic rename; S3 multipart abort; checksums | 2.8–2.10, 3.1 | — |
| WK-7 | T/D | Retention deletes the only good backup, or an attacker triggers deletion | Never deletes the newest successful backup; respects Object Lock; no-delete mode skips deletion; unexpected deletions raise alerts | 3.8, 2.9, 5.5 | A provider-side deletion is detected, not prevented, unless Object Lock is used |
| WK-8 | E | Restore that executes OS commands or reads local files via a malicious dump (T6) | `pg_restore --exit-on-error`; globals via `psql --no-psqlrc -v ON_ERROR_STOP=1` admin-only and only `\restrict`-fixed clients (CVE-2025-8714); `mysql --system-command=OFF --local-infile=0`; `mariadb --sandbox --local-infile=0`; restore inside the isolated container | 4.2, 4.3, 7.3 | SQL in the dump runs with the restore role's privileges: see OI-2 |
| WK-9 | D | Restore retried and applied twice | `MaxAttempts` restore = 1; unique ByState | 3.4 | — |
| WK-10 | T | Wrong restore target | Type-to-confirm on overwrites; PG temp DB + rename swap; explicit warning for MySQL/MariaDB in-place | 4.2, 4.3, 4.5 | MySQL/MariaDB in-place overwrite is not atomic |
| WK-11 | I | Notifications leak data to someone who lost access | Recipients computed with authz at delivery time; minimal payloads; verified email addresses only | 5.1, 5.3 | Data already delivered cannot be recalled |
| WK-12 | S/T | Forged webhook deliveries towards receivers | HMAC with timestamp | 5.2 | Receivers must verify the signature and timestamp |
| WK-13 | D | Notification floods | Per-user rate limits/digest; repeated failures collapse; failing webhooks auto-disabled | 5.3 | — |

### 6.5 Scheduler

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| SC-1 | T/D | Duplicate or lost runs across several `serve` instances | `SELECT … FOR UPDATE SKIP LOCKED` + River unique insert + `next_run_at` advance in one transaction; no session advisory locks on pooled connections | 3.4 | — |
| SC-2 | D | Run storms after downtime | Missed runs collapse into one catch-up run; global and per-connection concurrency limits | 3.3, 3.4, 3.6 | — |
| SC-3 | D | Silent schedule failure (DST, month-end, misconfiguration) | IANA zones with defined DST/month-end rules, property tests; `previewSchedule`; dead-man (`run.missed`) alerts from `expected_window` | 3.3, 3.5, 5.4 | Dead-man alerts depend on `serve` running; external monitoring of `/metrics` recommended |
| SC-4 | R | Who started a run | `runs.trigger` + `triggered_by`; audit for manual runs | 3.6 | — |

### 6.6 Postgres app DB

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| DB-1 | I | DB dump/backup of the app DB leaks secrets | Envelope AES-256-GCM with key ID; master key outside the DB; AAD = `table:row_id:field` (blocks ciphertext swapping between rows/fields) | 0.5 | Master key + DB together = all secrets |
| DB-2 | T/R | Audit-log rewriting by the app | `SECURITY DEFINER` hash trigger under advisory xact lock; UPDATE/DELETE/TRUNCATE revoked + raising triggers; `bekup audit verify`; periodic signed chain heads exported to destinations/webhook | 0.4, 1.3 | A DB superuser can rewrite the chain consistently; detected by comparing against exported signed heads (§8) |
| DB-3 | E | SQL injection | sqlc only; no hand-built SQL (lint rule + review); typed params | 0.4 | — |
| DB-4 | E | App role altering schema or grants | Migrations as `bekup_owner`; runtime as `bekup_app` (DML only); grants in migrations; CI checks | 0.4 | — |
| DB-5 | S/I | MITM on the app-DB connection | App DB on an internal compose network; `database.url_file` secret | 0.3, 0.7 | TLS to the app DB is the operator's choice when it is external (see hardening guide) |
| DB-6 | T | Tampering with stored pins, allowlists or settings | Pin changes admin + step-up + audited; runner allowlists are not in the DB at all | 2.11, 2.12, 2.5 | Compromised `serve`/DB superuser can change DB-held pins (see §8) |

### 6.7 Runner (`bekup runner`)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| RN-1 | S | Anyone else calling the runner | mTLS TLS 1.3, private CA pinned, client identified by SAN against `allowed client SANs`; listens only on `runner-ctl` network | 0.6, 2.4 | Theft of `serve`'s client cert (T4) |
| RN-2 | E | Command/argv injection via DB names or options | Typed RPCs, no free-form commands; DB names must be in the server-reported list, strict charset, no leading `-`; PG quoted `dbname=` conninfo; argv arrays; unknown fields rejected; fuzz-tested validators | 2.1, 2.5, 7.3 | — |
| RN-3 | E | Running an arbitrary image (T4 asks for `alpine` with a shell) | Image allowlist from embedded `catalog.toml` + mounted extra file only, digest-pinned; image key only selects an entry | 2.1, 2.5, 7.3 | — |
| RN-4 | E | Container escape or host access through the spec | Runner builds the spec itself: ReadonlyRootfs, tmpfs `/tmp` noexec/nosuid, CapDrop ALL, no-new-privileges, non-root UID, default seccomp, memory/CPU/pids limits, no mounts, not privileged | 2.5, 2.6, 7.3 | Kernel/runtime 0-days; rootless Docker / userns-remap recommended |
| RN-5 | I | Secrets visible in `docker inspect`, env, args, logs | Base64 length-prefixed stdin preamble → 0600 tmpfs files → `exec`; `MYSQL_HISTFILE=/dev/null`, `HOME=/tmp`; buffers zeroed; snapshot tests | 2.5, 2.6 | Secrets briefly in runner memory |
| RN-6 | E/I | Runner used as an SSRF/port scanner into internal networks (T4) | Target allowlist (CIDRs/hosts) from `runner.toml`; loopback, link-local, metadata always denied; `Probe` errors normalised | 2.5, 2.7 | Everything inside the allowlist is reachable with credentials `serve` holds |
| RN-7 | S | Target DB impersonated (T8) | Pinned cert as the only trust anchor for native clients (`verify-full` / `VERIFY_IDENTITY` / `--ssl-verify-server-cert`) + Go leaf-SPKI pre-flight; mismatch = hard fail + security alert; re-pin admin + step-up | 2.7a, 5.5 | TLS-off connections (explicit admin opt-out, badge, alert) |
| RN-8 | S | SSH bastion impersonated | Mandatory host-key pin with TOFU confirmation in the UI, no bypass | 2.2, 2.11 | TOFU trusts the first key the admin confirms; verify the fingerprint out of band |
| RN-9 | D | Leaked containers/networks; stuck runs | Kill on cancel/timeout/stream loss; label-based orphan reaper on start; resource limits; bounded streaming with gRPC flow control | 2.6 | — |
| RN-10 | E | Runner compromise = Docker = host root | Minimal code, distroless nonroot, `group_add` docker GID only, no DB access, no inbound except mTLS | 2.4, 2.6, 0.7 | Inherent to Docker socket access (§8) |
| RN-11 | T | `serve` changing the runner's allowlists | Separate `runner.toml`, never read from the DB | 0.3, 2.5 | — |

### 6.8 Dump containers

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| DC-1 | E | Malicious target DB (T6) exploits the dump client | Container alone on a per-run internal network with only the proxy; hardened spec; no mounts; short-lived | 2.6, 7.3 | A client RCE can still tamper with *that run's* dump output; signing happens after (in `serve`) so it can't forge older artifacts |
| DC-2 | I | Exfiltration from the container to the internet, other runs or the runner gRPC port | `internal: true` network per run; proxy forwards to exactly one target; integration test proves isolation | 2.6 | — |
| DC-3 | T | Version mismatch producing unusable dumps | PG client major ≥ server major enforced (block); restore image = max(dump, target) | 2.1 | — |
| DC-4 | S/I | Downgraded or unverified TLS inside the container | Pinned cert via preamble as only trust anchor; `ExtraHosts` keeps hostname verification working through the proxy | 2.7a | — |
| DC-5 | T | Compromised official image (T9) | Multi-arch index digests in `catalog.toml`, Renovate updates with minimum release age; only `\restrict`-fixed PG clients | 2.1 | Upstream image compromise before pinning |
| DC-6 | D/T | Silently truncated dump | `pg_restore --list`; MySQL "Dump completed" trailer + exit code; non-InnoDB warning; optional scheduled test restores | 2.7, 3.6, 4.6 | MySQL/MariaDB dumps of non-InnoDB tables are not consistent |

### 6.9 Destinations (disk, S3, SFTP)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| DS-1 | I | Provider or thief reads backups (T7) | Client-side age encryption by default | 3.1, 3.2 | Metadata (sizes, timestamps, job slugs, DB names encoded in paths) is visible |
| DS-2 | T | Provider modifies or replays objects | sha256 + signed manifest + DB cross-check at restore | 3.1, 4.1 | Detection only; availability depends on other copies (fan-out) |
| DS-3 | D | Deletion by a compromised `serve`, stolen credentials or ransomware | S3 Object Lock / versioning; no-delete credentials mode with bucket lifecycle rules; unexpected-deletion alerts; multiple destinations | 2.9, 3.8, 5.5 | Disk/SFTP destinations have no immutability; use S3 Object Lock for ransomware resistance |
| DS-4 | S | MITM to S3/SFTP (T8) | S3: https only, TLS ≥1.2 system roots (optional CA bundle); SFTP: pinned host key | 2.9, 2.10 | S3 not pinned by design (D-018) |
| DS-5 | E | SSRF via a destination endpoint (admin-entered or rebinding) | Dial-time netguard denies private/loopback/link-local/metadata by default, admin allowlist, no redirects | 2.3, 2.9, 2.10 | Allowlisted internal ranges |
| DS-6 | E | Path traversal on disk destinations | Confined to `disk_root` via `os.Root`; job slug / `enc(db)` encoding in paths | 2.8, 3.1 | — |

### 6.10 Notifications (SMTP, webhooks, in-app)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| NT-1 | E | SSRF through personal or shared webhooks / SMTP host | netguard at dial time, https only, no redirects; personal webhooks max 5 per user, test send, audited | 2.3, 5.1, 5.2 | — |
| NT-2 | I | Sensitive data in emails/webhooks | Minimal payloads (job name, status, time, link); never secrets or DB contents | 5.3 | Job names are visible to the mail/webhook provider |
| NT-3 | S | Alerts sent to an attacker-controlled address | Email address verification (signed link), re-verify on change | 5.1 | — |
| NT-4 | T/D | Attacker suppresses security alerts | Own-account and system security alerts can't be fully disabled (inbox + email always) | 5.3, 5.5 | An attacker with full admin can still change SMTP settings (audited) |
| NT-5 | S | MITM on SMTP/webhooks | STARTTLS/implicit TLS required, TLS ≥1.2, system roots | 5.1, 5.2 | Not pinned (D-018) |

### 6.11 CLI and offline tools (`migrate`, `admin`, `keys`, `certs`, `audit`, `verify`, `decrypt`, `config`)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| CL-1 | I | Master key / CA key leaked by tooling | Key files written 0600 with escrow guidance; CA key never placed on a shared volume; config/secret file permission checks | 0.3, 0.5, 0.6 | Operator handling of escrow copies |
| CL-2 | I | Secrets in shell history or process lists | Secrets only via `*_file` (config); per AGENTS.md, secrets never via argv | 0.3, 4.7 | — |
| CL-3 | E/R | `bekup admin create|reset-password` used as a backdoor | Requires shell access to the deployment (T10, trusted) | 1.6, 5.5 | See OI-3 (audit + alert for break-glass) |
| CL-4 | T | Offline `bekup decrypt` of a tampered or forged artifact | `bekup verify` checks sha256 + manifest signature against the escrowed signing-key fingerprint; documented format | 4.7 | Operators must check the fingerprint |
| CL-5 | D | Malicious artifact exhausting resources in offline tools | age work factor bounded (`SetMaxWorkFactor`) | 3.1, 4.7 | Large legitimate files need disk space |
| CL-6 | I | `config print` leaking secrets | `--redacted`; secrets exist only as file paths | 0.3 | — |
| CL-7 | T | Malicious config file (symlink to a writable path, group-writable) | Regular file, owned by user or root, not group/world-writable; strict TOML (unknown keys fail); no hot reload | 0.3 | — |

### Cross-cutting: supply chain (T9) and build

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| SC-S1 | T/E | Malicious dependency or Action | Renovate with minimum release age, Actions pinned by SHA, generators pinned as `go tool` deps, `govulncheck`, `gosec`, `osv-scanner`, Trivy | 0.1, 0.9, 7.4 | Zero-day malicious package released and adopted after the waiting period |
| SC-S2 | T | Tampered release image | Distroless nonroot, read-only rootfs, SBOM (syft), cosign keyless signing + provenance; signed commits/tags from `just bump` | 0.1b, 7.4 | Users must run `cosign verify` |

---

## 7. Mitigation to task map

| Mitigation | Threats | Tasks | Decisions |
|---|---|---|---|
| Config search path, strict TOML, `*_file`-only secrets, file-permission checks, dev guardrails, redacted print, slog redaction | CL-1/2/6/7, SV-9, SV-13 | 0.3 | D-019, D-041, D-060 |
| App DB roles, grants, audit hash trigger + revokes | DB-2/3/4 | 0.4 | D-042, D-054 |
| Keyring, AES-GCM + AAD, key-check, zeroing, instance signing keys, key generators | DB-1, CL-1, WK-2 | 0.5 | D-016, D-046, D-085–D-088 |
| Runner CA and per-component certs | RN-1 | 0.6 | D-030 |
| Segmented compose networks, secrets, read-only FS, `serve` unpublished | RP-3, DB-5, RN-10 | 0.7 | D-017 |
| Hardened reverse-proxy profiles | RP-1/4/5 | 0.7b | D-017, D-048 |
| CSP `style-src`, design system | UI-2 | 0.8 | D-050 |
| CI scanners, SHA-pinned Actions | SC-S1 | 0.9 | D-027, D-061 |
| Security headers, Origin/Sec-Fetch, size/rate limits, trusted proxies | UI-2/3/4/6, RP-2, SV-9/10 | 1.1 | D-048, D-050 |
| GraphQL POST+JSON, limits, masked errors, authenticated introspection | UI-3, SV-8/10 | 1.2 | D-050 |
| Audit API, `bekup audit verify`, signed chain-head export | UI-7, SV-12, DB-2 | 1.3 | D-054 |
| Deny-by-default authz + matrix test | SV-4/6/7 | 1.4 | D-011, D-053 |
| argon2id, password policy, sessions, lockout | UI-1, SV-1/2/10 | 1.5 | D-010, D-047 |
| Setup token, admin CLI, user CRUD, session revoke | SV-3, CL-3 | 1.6 | D-047 |
| TOTP + recovery codes; WebAuthn passkeys | UI-1, SV-2 | 1.7, 1.8 | D-010 |
| 2FA policy, step-up | SV-5 | 1.9 | D-047 |
| WS auth and re-validation | SV-11 | 1.10 | D-052 |
| Engine catalog, version rules, argv builders | RN-2/3, DC-3/5 | 2.1 | D-036 |
| SSH host-key pinning | RN-8 | 2.2 | D-035 |
| SSRF guard | DS-5, NT-1 | 2.3 | D-055 |
| Runner gRPC + mTLS | RN-1 | 2.4 | D-030 |
| Validators, target allowlist, hardened spec, preamble | RN-2/3/4/5/6/11 | 2.5 | D-029, D-031, D-032, D-033 |
| Per-run networks, proxy/tunnel, reaper | RN-9, DC-1/2 | 2.6 | D-029 |
| Target-DB certificate pinning | RN-7, DC-4 | 2.7a | D-018, D-034 |
| Typed RPCs, normalised Probe errors | RN-6, DC-6 | 2.7 | D-030 |
| Disk `os.Root`, atomic rename | DS-6, WK-6 | 2.8 | D-055 |
| S3 https/Object Lock/no-delete; SFTP host-key pin | DS-3/4, WK-7 | 2.9, 2.10 | D-007, D-049 |
| Admin-only connection/destination CRUD, pin confirmation, TLS downgrade flow | SV-6/8, RN-7/8, DB-6 | 2.11, 2.12 | D-011, D-034 |
| Pipeline: zstd → age → sha256 → bounded fan-out; signed manifest | WK-2/3/5/6, DS-1/2 | 3.1 | D-037, D-038 |
| Job keys, opt-out flow, passphrase export | WK-3 | 3.2 | D-008 |
| Scheduler math and loop, River config | SC-1/2/3, WK-1/9 | 3.3, 3.4 | D-043, D-044 |
| Backup worker, log redaction/caps, concurrency | SV-13, SC-2/4, DC-6 | 3.6 | D-045, D-060 |
| Events via NOTIFY IDs | SV-11 | 3.7 | D-052 |
| Retention safety | WK-7, DS-3 | 3.8 | D-005, D-007 |
| Download tickets | UI-6, SV-14 | 3.9 | D-012, D-056 |
| Restore staging + verification + identity handoff | WK-1/2/4, DS-2 | 4.1 | D-022, D-039 |
| Hardened PG / MySQL / MariaDB restore | WK-8/10 | 4.2, 4.3 | D-036 |
| Temporary restore connections | SV-7 | 4.4 | D-015, D-058 |
| Restore gate (fresh 2FA, target rules, type-to-confirm) | SV-5/7, WK-10 | 4.5 | D-013, D-014, D-057 |
| Scheduled test restores | DC-6 | 4.6 | D-024 |
| Offline verify/decrypt | CL-4/5 | 4.7 | D-040 |
| SMTP + email verification; webhooks with HMAC | NT-1/3/5, WK-12 | 5.1, 5.2 | D-023, D-051 |
| Notification core, non-disableable security alerts | WK-11/13, NT-2/4 | 5.3 | D-051 |
| Dead-man alerts | SC-3 | 5.4 | D-024 |
| Security alerts to admins | RN-7, DS-3, SV-12, NT-4 | 5.5 | D-024 |
| `/metrics` on an internal port | SC-3, RN-9 | 5.6 | D-024 |
| Security test suite (authz, fuzzing, runner escape, malicious dumps, SSRF, ZAP) | UI-2, SV-4, RN-2/3/4, WK-8, DS-5 | 7.3 | — |
| Signed release, SBOM, provenance | SC-S2 | 7.4 | D-061 |
| Final reviews | all | 7.6 | — |

---

## 8. Residual risks and out of scope

**Accepted residual risks**

- **Compromised `serve` (T4).** `serve` holds the master key, the `bekup_app`
  role, the runner client certificate and the manifest signing key. An
  attacker who owns `serve` can read every stored secret, read and delete
  backups on non-immutable destinations, sign manifests, and ask the runner
  to dump *or restore into* any connection whose credentials it holds,
  within the runner's target allowlist. Bounded by: no Docker access, no
  arbitrary images or argv, the target allowlist in a file `serve` can't
  change, Object Lock / no-delete credentials for destinations, exported
  signed audit-chain heads, and security alerts.
- **Compromised runner (T5) = Docker access = root on the Docker host.**
  Inherent to running containers through the Docker socket. Mitigated by
  minimising the runner (small code, typed API, mTLS-only ingress, distroless
  nonroot, no DB access) and documenting rootless Docker / userns-remap.
- **DB superuser rewriting the audit chain.** A Postgres superuser (or the
  `bekup_owner` role) can rewrite the whole chain consistently. This is
  **detected, not prevented**: signed chain heads are exported periodically
  to destinations/webhook, so a rewritten history no longer matches earlier
  exported heads. Entries after the last exported head can be lost without
  detection.
- **Malicious target DB (T6) can tamper with its own backups.** Dumps are
  only as trustworthy as the server producing them; signing proves bekup
  stored it, not that the content is benign. See OI-2 for restore-time
  impact.
- **TOFU for SSH host keys and DB certificate pins.** The first pin is only
  as good as the admin's out-of-band check of the fingerprint.
- **Not pinned by design:** S3, SMTP and webhooks rely on system roots
  (D-018, D-049). A mis-issued certificate for those endpoints enables MITM;
  backups are still age-encrypted and webhooks HMAC-signed.
- **TLS for the UI is delegated to the reverse proxy** (D-017); a compromised
  proxy sees plaintext UI traffic.
- **Opt-outs.** Jobs opted out of encryption and connections without pinned
  TLS are weaker by explicit admin choice (badge, alert, audit).
- **Memory.** Go cannot guarantee secrets are wiped from memory (GC copies);
  zeroing is best effort.
- **Metadata leakage** on destinations (object names include job slug and
  encoded DB name, sizes, timestamps) and in notifications (job names).
- **MySQL/MariaDB in-place restore** is not atomic; a failure can leave a
  partially restored database (explicit warning).
- **Plain downloads** are decrypted data on the user's device; after the
  download, protection is the user's responsibility.
- **Maintainers can restore into other connections.** A maintainer can
  restore any job's backup into any same-engine connection (by design,
  D-013); if they can read that target outside bekup, this is a data path.
  Audited; admins control which connections exist.
- **Availability.** Loss of the master key (without escrow) makes stored
  secrets and passphrase-encrypted backups unrecoverable; loss of an age
  identity makes recipient-encrypted backups unrecoverable. Escrow is
  documented, not enforced.

**Out of scope**

- A **root-on-host operator** (T10) or anyone with access to Docker secrets,
  volumes or `docker exec`: they are trusted. `bekup admin` actions are
  partially covered by OI-3.
- Compromise of the user's own device or browser.
- Volumetric DDoS against the public endpoint.
- Security of the target database servers and storage providers themselves
  beyond what bekup verifies (pins, hashes, signatures).
- Phase 2 (external API, push) and Phases 3–4 (mobile apps): this document
  is updated when those phases start.

---

## 9. Open items

Threats the plan does not fully cover. Each has a `proposed` decision; none of
them changes the design until the user accepts it.

| ID | Gap | Proposed mitigation | Decision |
|---|---|---|---|
| OI-1 | **Maintainer → download escalation via owner assignment.** Maintainers may assign owners, and owners may download. If a maintainer can make themselves (or an account they control) an owner, the "no downloads" rule for maintainers is bypassed. | Download permission requires the **Owner relationship and a non-Maintainer role** (a Maintainer listed as owner still gets no downloads); maintainers cannot add themselves as owners; every owner change is audited and raises a security alert to admins. Covered by the authz matrix. | [D-067](docs/DECISIONS.md#d-067-owner-assignment-cannot-grant-maintainers-downloads) |
| OI-2 | **Restore executes dump content with the restore role's privileges.** Client hardening blocks `\!`, `system` and `LOAD DATA LOCAL`, but SQL in a dump (e.g. `COPY … FROM PROGRAM`, `ALTER SYSTEM`, `CREATE FUNCTION` in an untrusted language, MySQL `SELECT … INTO OUTFILE`) runs with the target role's privileges. A malicious source DB (T6, e.g. one an owner controls) restored by an admin/maintainer into another connection with a superuser role could compromise that DB server. | `Probe` reports whether the connection's role is a superuser (PG `rolsuper`, MySQL/MariaDB `SUPER`/`FILE`/`ALL`); restores into such connections show a warning and require an admin; restores whose source connection differs from the target are flagged in the wizard and audit. Docs keep recommending a non-superuser restore role. | [D-068](docs/DECISIONS.md#d-068-restore-privilege-warnings-for-superuser-and-cross-source-restores) |
| OI-3 | **Break-glass CLI actions are not visible.** `bekup admin create|reset-password` run on the host bypasses step-up and could silently create or take over an admin. | CLI admin actions write an audit entry (actor `cli`) and raise a `security.system` alert to all admins (sessions are already revoked on password change per D-047). | [D-069](docs/DECISIONS.md#d-069-audit-and-alert-on-break-glass-cli-actions) |
| OI-4 | **Unbounded dump size.** A malicious or runaway target DB (T6) can stream unbounded data, filling destinations (cost, DoS for other jobs) until the job timeout. | Optional per-job **maximum artifact size** (bytes counted by the pipeline counter); exceeding it fails the run and alerts. Plus a warning when an artifact grows far beyond the job's recent average. | [D-070](docs/DECISIONS.md#d-070-per-job-maximum-artifact-size) |

---

## 10. Deployment hardening summary

Detailed guides live in [`docs/security/`](docs/security/README.md):

| Topic | Summary | Guide |
|---|---|---|
| Host and Docker | Keep the Docker host dedicated and patched; consider rootless Docker or userns-remap for the runner's daemon; read-only root filesystems; segmented compose networks; never publish `serve`'s port | [hardening.md](docs/security/hardening.md), [rootless-docker.md](docs/installation/rootless-docker.md) |
| Reverse proxy and TLS | Use the shipped `traefik` or `nginx` profile; TLS 1.2+/1.3, HSTS; set `trusted_proxies` exactly; https `base_url` | [reverse-proxy.md](docs/installation/reverse-proxy.md) |
| Keys and escrow | Generate the master key with `bekup keys generate` (0600, Docker secret); escrow it and the signing-key fingerprint offline; rotate with `bekup keys rotate` | [key-management.md](docs/security/key-management.md) |
| Runner mTLS | `bekup certs init`; keep the CA key off shared volumes; monitor expiry; rotate with `bekup certs rotate` | [mtls-certs.md](docs/security/mtls-certs.md) |
| Target allowlist | Restrict `[targets]` in `runner.toml` to the exact DB hosts/CIDRs you back up | [target-allowlist.md](docs/security/target-allowlist.md) |
| Database roles | Dedicated non-superuser backup and restore roles; pin DB certificates; avoid TLS opt-outs | [db-roles.md](docs/security/db-roles.md) |
| Immutable backups | S3 Object Lock / versioning or no-delete credentials with lifecycle rules; more than one destination | [immutable-backups.md](docs/security/immutable-backups.md) |
| Audit | Run `bekup audit verify` regularly; keep exported signed chain heads on immutable storage | [audit.md](docs/security/audit.md) |
| Accounts | Require 2FA (passkeys preferred) for admins and maintainers; keep the number of admins small | [hardening.md](docs/security/hardening.md) |
| Monitoring | Scrape `/metrics` on the internal port only; alert on missed backups, cert expiry, pin mismatches | [operations.md](docs/operations.md) |
| Supply chain | Verify release images with `cosign verify` before deploying | [hardening.md](docs/security/hardening.md) |
