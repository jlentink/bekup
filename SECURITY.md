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
| A2 | **Target DB credentials** (passwords, client certs/keys, SSH keys/passwords for tunnels) | `connections.secret_enc` in the app DB; in transit serve → runner → dump container stdin | Read (and for restore roles, write) access to production DBs | AES-256-GCM envelope with AAD `connections:<id>:secret` (D-046, D-135); write-only API (only `has*`/auth-kind flags), never logged, audited or returned; generated SSH private keys never leave the server; runner requests built per call and zeroed after it (`runnerpb.ZeroSecrets`); stdin preamble only (D-032) |
| A3 | **Destination credentials** (S3 keys, SFTP keys/passwords) | `destinations.secret_enc` | Read/delete of all backups | Envelope encryption under AAD `destinations:<id>:secret`, write-only in the API (only `has*` flags), never logged or audited, generated SSH private keys never leave the server (D-131); no-delete credentials mode (D-007) |
| A4 | **Master key** (keyring) | `master_key_file` (Docker secret, 0600) | Decrypts every stored secret (A2, A3, A5, A8, SMTP/webhook secrets, TOTP secrets) | `*_file` only, permission checks (D-041); key-check values per key ID stored in `settings` and verified at every serve start (refuses a foreign keyring; new keys audited as `keys.rotated`, D-119); rotation; escrow guidance (D-016, D-046) |
| A5 | **Job age passphrases** (versioned) and **age identities** | `job_passphrases.secret_enc`; identities are *never stored* (shown/downloaded once), only passed at restore/download time via the encrypted handoff table (15 min TTL) | Decrypt the backups | Envelope encryption; identities never in River args (D-039); passphrase export admin + step-up + audited (D-040) |
| A6 | **Manifest signing key** (ed25519 instance keys with key IDs) | `instance_keys` | Forged manifests would make tampered or replayed artifacts look valid; also signs audit chain heads | Key IDs + retained old public keys (`bekup_app` cannot DELETE `instance_keys` and may only UPDATE `retired_at`); seed sealed with the keyring (AAD `instance_keys:<id>:private`); fingerprint escrow (D-038, D-040, D-087, D-101) |
| A7 | **Runner mTLS CA and certs** | CA key outside shared volumes; per-component key + CA cert as Docker secrets | Whoever holds a valid client cert can drive the runner (and thus Docker) | Private CA, SAN identity, TLS 1.3, expiry metric/alert, rotation (D-030; task 0.6) |
| A8 | **User credentials and second factors** (argon2id hashes, TOTP secrets, WebAuthn credentials, recovery codes) | `users`, `webauthn_credentials`, `recovery_codes` | Account takeover | argon2id with rehash on parameter change, password policy, TOTP secret (and pending enrollment secret) sealed with the keyring under its own AAD and never returned after enrollment, replay protection, recovery codes stored only as HMAC-SHA256 under a keyring subkey, lockout with backoff; WebAuthn rows hold only public keys (no secret), at most 20 per user, removable only with step-up; a lost-factor reset of all factors (admin with step-up, never the own account, or `bekup admin reset-2fa`) revokes every session, starts a new enrollment grace period, is audited and alerted (D-010, D-047, D-107, D-109, D-111, D-112) |
| A9 | **Sessions, step-up state, download tickets** | `sessions` (sha256 of token, AAL, `stepup_at`, `aal2_at`, `mfa_pending`), `download_tickets` | Session hijack → act as user | `__Host-` cookie, HttpOnly/Secure/SameSite=Strict, rotation, expiry, revocation; a password-only login of an account with a second factor yields a 5-minute session that can only complete the second factor (D-112); single-use 60 s session-bound tickets (D-047, D-056) |
| A10 | **Audit log** | `audit_log` (hash chain) + signed chain heads exported to destinations/webhook | Accountability; detection of abuse | SECURITY DEFINER hash trigger, revoked UPDATE/DELETE/TRUNCATE, `bekup audit verify`, signed heads in append-only `audit_chain_heads` + export hook (D-054, D-092, D-101); details redacted before storage; writes fail closed |
| A11 | **User accounts and role assignments** (incl. job ownership) | `users`, `job_owners` | Privilege escalation | Admin-only user CRUD with step-up for every change, all sessions revoked on role change/disable/reset/delete, last-active-admin invariant under row locks, generated temporary passwords (admins never choose a user's password), soft delete keeps audit references, new-admin alerts; the first admin only via the audited host CLI (`bekup admin create`), no web setup flow (D-011, D-047, D-110, D-113; tasks 1.6, 5.5) |
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
       ══ B3 ══     ├─ pgx + TLS (role bekup_app) ───> Postgres app DB
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
| **B2** | Reverse proxy ↔ `serve` | `serve` only on internal `frontend` network (port not published); `X-Forwarded-*` trusted only from `trusted_proxies` and stripped after resolution; https `base_url` required (or explicit plain-HTTP opt-in); CSP and headers set by `serve` too; Origin/`Sec-Fetch-Site` checks, per-IP rate limit, body/header limits and server timeouts; GraphQL POST + JSON only with depth/complexity/body limits, deny-by-default access directives, introspection only in dev/for unrestricted authenticated principals, masked internal errors; credential operations (login, password, second-factor and passkey fields, user password/factor resets) must be the only root field of their request, extra root fields charged to the per-user limit (D-117); a request from a trusted proxy without `X-Forwarded-For` is logged (rate-limited WARN); server-side sessions behind a `__Host-`, HttpOnly, SameSite=Strict cookie (sha256 of the token stored), per-user and login rate limits | D-017, D-048, D-050, D-095, D-102, D-105, D-107, D-117, D-118; 0.7, 1.1, 1.2, 1.5 |
| **B3** | `serve` ↔ app DB (pool, migrator and the dedicated LISTEN connection of `internal/pubsub`) | TLS ≥1.2 with `verify-full` and required SCRAM channel binding by default (private CA only, host name checked; config beats URL `sslmode`); bekup roles rejected without TLS (`hostnossl … reject`); `bekup_app` DML only; migrations as `bekup_owner`; secrets envelope-encrypted; append-only hash-chained audit log | D-042, D-046, D-054, D-100, D-104; 0.4, 0.5, 1.0, 1.3 |
| **B4** | `serve` ↔ destinations, SMTP, webhooks | Dial-time SSRF guard (`Control` hook on the socket address; IPv4-mapped/compat, NAT64, 6to4 and Teredo forms covered), admin allowlist that can never re-allow hard-denied ranges, no proxy, no redirects, TLS ≥1.2 system roots (optional CA bundle), SFTP host-key pin (TOFU fetch of a stored destination's host only, normalized fetch errors, explicit admin confirmation bound to the fetched host/port; one SSH client policy in `internal/sshpolicy`; a mismatch is `host_key_mismatch` and audited, D-131, D-140); one host-name rule for guard, SFTP and runner (`internal/hostname`); endpoints/hosts also checked when a destination is saved; disk via `os.Root` | D-049, D-055, D-120, D-131, D-140; 2.3, 2.8–2.10, 2.12, 2.14, 5.1, 5.2 |
| **B5** | `serve` ↔ runner | mTLS (TLS 1.3, private CA pinned, SAN check in the handshake and per call, no resumption), typed RPCs without free-form command/arg/env/image/path fields, credential fields annotated and zeroed after use, unknown fields rejected, ≤1 MiB chunks (message cap 1 MiB + 64 KiB), flow-control backpressure, mandatory deadlines, deny-by-default method table, fail-fast concurrency limits, connection recycling (30 min, grace = max run + 1 min), no proxy/retries on the client; logs without payloads. The container healthcheck uses a plaintext unix socket inside the runner container (0700 dir / 0600 socket, runner-owned) that serves only the standard health status. `serve` builds its client from `[runner]` at startup (unreadable/invalid client certificates refuse startup; the connection itself is lazy) and is the only caller of the connection RPCs (Probe, FetchServerCertificate, FetchHostKey, PrePull) for the API | D-030, D-033, D-125, D-126, D-127, D-135; 0.6, 2.4, 2.5, 2.11 |
| **B6** | runner ↔ Docker daemon | Docker only over the `docker.host` unix socket; Docker Engine ≥ 26.0.0 required (CVE-2024-29018; older daemons refused at startup); image allowlist from runner's own config (digest-only references, pull-if-missing by digest); runner-built fixed hardened spec (pure builder, snapshot-tested); fixed preamble entrypoint; the runner joins only networks it created after proving the container it joins is its own (mountinfo/host name + own interface address); label-based orphan reaper at startup (completed before any run is admitted) and every 5 min that never touches a live runner's runs (`bekup.runner` label); every image `VOLUME` covered by a read-only one-page tmpfs (no anonymous volumes, no writable persistent path) | D-031, D-130, D-132, D-138; 2.1, 2.5, 2.6, 2.14 |
| **B7** | dump container ↔ runner / other networks | Alone with the runner on a per-run `internal: true`, IPv4-only /28 from `docker.run_subnet_pool` (`inhibit_ipv4`: no host address, no masquerade, no default route, no external DNS); fixed address; the per-run proxy accepts only that address and forwards to exactly one target (allowlist dialer or SSH tunnel), TLS end to end; the gRPC listener closes connections from the pool and every per-run subnet (also on wildcard binds); other runs on other subnets; a /29 around an IP literal never overlaps the runner's or Docker's networks, Docker gateways, the runner's name servers or default gateways; secrets only via stdin preamble; every psql run starts under a random per-run `\restrict` key (no meta-commands such as `\!`, `\o |cmd`, `\copy ... program` from restored scripts; integration-tested with every pinned client); integration-tested from a containerized runner | D-029, D-032, D-132, D-138; 2.6, 2.14 |
| **B8** | runner proxy / dump client ↔ target DB | Target allowlist from `runner.toml` (empty = deny all; always-denied hard ranges incl. loopback, link-local, metadata, mapped forms; runner's own networks denied; every Docker network's subnet and gateway denied unless an `allow_cidrs` entry inside that subnet (a gateway: its exact /32) allows it, `allow_hosts` never opens one; IP literals refused in `allow_hosts` at config validation; optional per-bastion target lists `bastion_targets` (unlisted bastions forward anywhere behind the deny lists, with a startup/runtime warning); checked at validation for every resolved address and again at dial time on the socket address); `verify-full`/`VERIFY_IDENTITY` with the pinned cert as the only trust anchor (preamble `ca` record; no client reads system roots) + Go pre-flight before every PINNED connection (chain verified against one pin's anchor only, host name/IP checked, **and** that pin's leaf SPKI SHA-256 in constant time; current or staged backup pin); certificate capture via our own protocol upgrade (PG SSLRequest / direct TLS, MySQL/MariaDB SSL request, bounded reads) that aborts the handshake in `VerifyConnection` and never sends credentials (one of exactly three `InsecureSkipVerify` uses, with TLS mode UNVERIFIED and the app DB's `tls_mode = require`), normalized and audited like every target RPC; pin anchors must be a self-signed leaf or a private CA (public CAs refused; the same-private-CA leaf window between pre-flight and native client is accepted residual risk); SSH tunnels with mandatory host-key pins (pinned type only, strong algorithms, mandatory allowlist dialer); bastion host-key capture (`FetchHostKey`) through the same dialer, key exchange only | D-018, D-029, D-034, D-035, D-122, D-130, D-133, D-135, D-138, D-139; 2.2, 2.5, 2.7a, 2.11, 2.14 |

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
| UI-1 | S | Phishing or credential stuffing to log in as a user | argon2id (≥ m=64 MiB, t=3, p=1, rehash on change); password policy (≥ 12 and ≤ 1024 characters, zxcvbn score ≥ 3, embedded top-100k common-password list, no e-mail/name inside); per-IP login limiter on top of the global limit; a per-account limiter before every argon2id verification (unknown addresses limited alike); per-account lockout with exponential backoff, each attempt reserved atomically before it is verified so concurrent guesses never exceed the threshold (D-117); one generic "invalid email or password" for every failure with a dummy argon2id verification for unknown/disabled/locked accounts; 2FA (passkeys phishing-resistant, TOTP), two-step login for every account with a factor; admin-configurable 2FA requirement per role with an enrollment grace period (off by default: a user decision; admins are urged to enable it), restricted principal after the grace period (D-107, D-112) | 1.5, 1.7, 1.8, 1.9 | TOTP is phishable; admins should require passkeys for privileged roles. Distributed guessing below the per-IP limit is bounded by the per-account limiter and the lockout |
| UI-2 | T/E | XSS injecting script into the UI (e.g. via job names, DB names, run logs from a malicious target DB) | React escaping; strict CSP `script-src 'self'` (style-src settled in 0.8); nosniff; no inline scripts | 0.8, 1.1, 6.x, 7.3 | A CSP-bypass bug in a dependency; mitigated by supply-chain controls |
| UI-3 | T | CSRF / cross-site actions | `SameSite=Strict` `__Host-` cookie; GraphQL POST + `application/json` only; Origin/`Sec-Fetch-Site` checks on every non-GET/HEAD/OPTIONS request (incl. the subscription event streams, which are POSTs) and every upgrade request (none is served; no WebSocket, D-124), before any handler runs; requests with neither header are refused; Vite dev origins only in dev mode (D-102) | 1.1, 1.2, 2.13 | None significant |
| UI-4 | T | Clickjacking of destructive actions (restore, opt-out) | `frame-ancestors 'none'` + `X-Frame-Options: DENY`; type-to-confirm on overwrites; step-up dialogs | 1.1, 4.5, 6.7 | None significant |
| UI-5 | I | Session token theft from the browser | HttpOnly cookie (not readable by JS); `__Host-` + Secure over https (plain-http mode only with the D-095 opt-in); 256-bit random token stored server-side as sha256 only; never returned by the API or logged | 1.5 | Malware on the user's device (out of scope); sniffing in the opted-in plain-HTTP mode |
| UI-6 | I | Sensitive data cached or leaked (downloads, referrers) | `Cache-Control: no-store` on every response except static UI assets (D-102), incl. downloads; `Referrer-Policy: no-referrer`; secrets never returned by the API; age private keys shown once and never stored | 1.1, 3.2, 3.9 | A downloaded plain file is the user's responsibility once on their device |
| UI-7 | R | User denies a sensitive action | All sensitive actions audited with user and session; step-up timestamps recorded; audit writes fail closed and commit atomically with the action (`RecordTx`, D-101) | 1.3, 1.9 | — |

### 6.2 Reverse proxy (Traefik / nginx profiles)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| RP-1 | S/I | MITM or TLS downgrade between browser and proxy | TLS 1.2+/1.3, modern ciphers, HSTS (preload-ready), HTTP→HTTPS redirect; `testssl.sh`/sslyze check in AC | 0.7b | Misconfigured custom proxies (generic examples documented) |
| RP-2 | S | Spoofed `X-Forwarded-For/Proto` to bypass rate limits or https checks | `serve` trusts forwarding headers only from `trusted_proxies` (required outside dev); `X-Forwarded-For` walked right to left skipping trusted hops; `X-Forwarded-Host` never used; all forwarding headers removed from the request after resolution; HSTS and links depend on `base_url` only (D-102) | 1.1 | Wrong CIDR configured by the operator |
| RP-3 | S/E | Direct access to `serve` bypassing the proxy | `serve` listens on the internal `frontend` network only, port never published | 0.7, 0.7b | Operator publishes the port manually |
| RP-4 | D | Oversized requests / slowloris | Proxy request size limits (Traefik exempts only the event stream router, POST `/graphql` with `Accept: text/event-stream`, from its buffering body limit because it would buffer the stream; serve caps those bodies itself, D-124); `serve` body and header limits, read-header/read/write/idle timeouts, per-client-IP token bucket (IPv6 per /64, bounded LRU, 429 + `Retry-After`) (D-102) | 0.7b, 1.1 | Volumetric DDoS is out of scope |
| RP-6 | E | Proxy holding the Docker socket (root-equivalent on the host) | Traefik uses the file provider; the nginx profile is unprivileged nginx + certbot (D-093, D-094); no proxy container mounts the Docker socket | 0.7b | None while the profiles are used as shipped |
| RP-7 | I | Plain HTTP without a proxy (D-095) | Explicit opt-in (`server.allow_insecure_http` or dev mode), startup warning, permanent UI banner, admin security alert + audit outside dev; cookies adapt; passkeys are disabled on non-localhost http (`webauthnStatus` explains, TOTP remains; D-109) | 0.3a, 1.1, 5.5, 6.1 | Credentials and sessions travel in clear text when the operator opts in |
| RP-5 | T | Compromised proxy image | Digest-pinned images (Renovate), Trivy | 0.7b, 0.9, 7.4 | A compromised proxy sees plaintext UI traffic (sessions, passwords); TLS for the UI is delegated by design (D-017) |

### 6.3 `serve`: HTTP, GraphQL, authentication and authorization

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| SV-1 | S | Session hijack / fixation | A login always issues a new token and revokes the session the browser presented; rotation on privilege change (AAL increase incl. completing the second factor, role change) keeping the absolute expiry; idle (sliding, throttled writes) + absolute expiry checked in the lookup query; revoke-all on password change; user-visible session list/revoke; duplicate session cookies refused; unknown/expired cookies cleared (D-107) | 1.5, 1.6 | — |
| SV-2 | S | TOTP code replay; brute force of passwords or second factors | Last-used-step replay protection (conditional update: a step is accepted at most once, ±1 step window, constant-time compare); per-account lockout with exponential backoff (`security.lockout_*`; wrong current passwords in `changePassword` count too) and a separate second-factor counter with the same backoff for TOTP, recovery-code and passkey-assertion failures (login second step and step-up, D-112); every check is reserved by one conditional UPDATE that counts it and applies the lock at the threshold before the code is looked at, so concurrent guesses cannot exceed the threshold (D-117); only TOTP or a user-verified passkey counts as the fresh second factor of a restore (D-116); recovery codes 60-bit, single-use under concurrency; 2FA changes need step-up (D-111) | 1.5, 1.7 | A password holder gets `lockout_threshold` guesses per backoff window against 3-in-10^6 TOTP odds |
| SV-3 | S | First-run takeover before the admin exists | No web setup flow: the first admin is created only with `bekup admin create` on the host (needs the config and the DB credentials; audited as a CLI event). Until then `serve` only logs the command at WARN, and no API operation can create an account without an admin session (D-110, D-113) | 1.6, 1.12 | Until an admin exists nobody can sign in (the UI is useless, not exposed); anyone with host access can create admins at any time (audited, alerted from 5.5) |
| SV-4 | E | Broken access control (IDOR, missing role check) | Central deny-by-default `internal/authz` (typed operation catalog, one rule each, unknown operations denied); every guarded root field maps to an operation and passes the policy precheck before its directives (`@authenticated`/`@hasRole`/`@stepUp`) and resolver, and `serve` refuses to start on an unmapped field or a `@hasRole` broader than the rule; per-object ownership via `Policy.Authorize` with the job loaded from the DB; matrix test (every operation × admin/maintainer/maintainer-owner/owner/non-owner/anonymous) fails CI on unmapped operations (D-106) | 1.4, every GraphQL task | Logic bugs in ownership predicates (task 3.5 queries); covered by matrix + 7.3 |
| SV-5 | E | Sensitive action with a stolen but old session | Step-up (≤5 min) for secret changes, restores, download tickets, user/role changes, opt-outs, 2FA changes, passphrase export, key operations; restores require a passkey/TOTP check within the window on an AAL2 session (password or recovery-code step-ups never count); accounts with a factor must step up with it, only accounts without one with the password; a login of an account with a factor is a step-up only after the second factor (D-112); the freshness per operation is part of the authz rule and pinned by the matrix test (D-106) | 1.4, 1.9, 4.5 | Step-up window of 5 min; accounts without a factor (allowed by the policy) step up with the password |
| SV-6 | E | Maintainer reaching secrets or downloads | Maintainers see non-secret metadata only, no CRUD on connections/destinations, downloads denied in the authz matrix; owner grants apply to role `user` only, so a maintainer listed as owner still cannot download, and maintainers cannot assign themselves as owners (D-067, D-106). Destinations (D-131): maintainers get name/type/mode flags/readiness only (`settings` null) and may test configured destinations (normalized results), never fetch host keys of arbitrary hosts. Connections (D-135): maintainers get name, engine, version, host, port, TLS mode, pin status/expiry and SSH flag only (`settings` null) and may test, detect versions, list databases and pre-pull the client image of configured connections (normalized results; D-106, D-140); certificate/host-key fetches, pins and every change are admin only | 1.4, 2.11 (done), 2.12 (done), 3.9 | Collusion with another account (audited + alerted) |
| SV-7 | E | Owner restoring over a DB they don't own | Owners may restore only into DBs their own jobs back up, or a new DB on those connections; temporary connections admin-only | 4.4, 4.5 | — |
| SV-8 | I | Secrets read back via the API | Secrets write-only (`hasPassword`); masked errors; introspection only for authenticated users; integration tests search every response and the audit log for the secret values (destinations, connections) | 1.2, 2.11 (done), 2.12 (done) | — |
| SV-9 | I | Host-header poisoning of links (password/email/notification links) | All links built from `base_url`, never from `Host`; https required outside dev | 0.3, 1.1 | — |
| SV-10 | D | GraphQL resource exhaustion; login floods; event stream floods | Depth/complexity/size limits; per-IP limit, stricter per-IP login limit and per-user limit for authenticated requests (every root field beyond the first is charged); credential operations must be alone in their request, so aliases cannot multiply argon2id or factor checks (D-117); argon2id semaphore with a bounded wait and waiter cap (excess → `RATE_LIMITED`, never an unbounded queue); passwords capped at 1024 characters before hashing; strictly parsed stored hashes with parameter caps; event streams capped per session, per user and per instance, every stream request counted by the per-IP and per-user limits, each stream write bounded by a deadline, keepalives detect dead peers (D-124) | 1.1, 1.2, 1.5, 2.13 | — |
| SV-11 | I/E | Subscription stream leaks data after logout or role change | Subscriptions are Server-Sent Events: one cookie-authenticated `POST /graphql` (`Accept: text/event-stream`) per subscription, exact Origin required again by the handler, nothing accepted from the client after the request, no WebSocket (D-124); session event bus (in-process + Postgres NOTIFY with IDs only) ends streams at once on logout/revoke/rotation; periodic (`graphql.stream_revalidate_interval`) and per-event (cached ≤ `graphql.stream_session_check_ttl`) re-validation rebuilds the principal like the session middleware (incl. the 2FA enrollment check), role/user/AAL or restriction change ends the stream with FORBIDDEN; streams refused for restricted principals (pending second factor, required enrollment) and after shutdown (D-118); a final error event (UNAUTHENTICATED / FORBIDDEN / SESSION_ROTATED / UNAVAILABLE) then close; authz at delivery time with the current principal (`streams.Deliver`); per-session, per-user and global caps; only subscriptions as streams and subscriptions only as streams (D-108, D-118, D-124) | 1.10, 2.13, 3.7 | Up to `stream_session_check_ttl` (5s) of cached validity before an event; up to `stream_revalidate_interval` (30s) when both the local event and NOTIFY are lost |
| SV-12 | R | Admin denies weakening security or changing roles | Audit log for every sensitive action; security alerts for opt-outs, new admins, key/cert rotation | 1.3, 5.5 | See T4 for a compromised `serve` |
| SV-13 | I | Secrets in logs | slog redaction handler; static messages; known secret values redacted from runner logs | 0.3, 3.6 | Unknown secret formats in third-party error strings |
| SV-14 | T | Download ticket reuse or theft | Single-use, 60 s, bound to the session; step-up + audit on creation | 3.9 | — |
| SV-15 | E/T | `serve` running with a privileged DB role or against a schema it was not built for | Startup refuses unless `db.CheckAppRole` passes (D-099) and `bekup_schema_version()` (SECURITY DEFINER, EXECUTE for `bekup_app` only) equals the newest embedded migration; `/readyz` re-checks the version (D-078, D-102) | 1.1 | — |
| SV-17 | D | Locking a victim out by failing logins with their e-mail address | Backoff starts at 1 m and is capped (`lockout_max_duration`); attempts while locked do not extend the lock and are audited at most once per lock window with a count (no audit flooding, D-117); per-IP login limiter; lockouts audited and reported to the notifier hook (failed-login alert in 5.5); `bekup admin` unlock (1.6). The lockout only blocks new password logins; sessions established before it stay valid, so it cannot be used to log a victim out (D-107) | 1.5, 1.6, 5.5 | An attacker with many IPs can keep new logins of an account blocked; the lock is temporary and alerted |
| SV-18 | I | Account enumeration via login errors or timing | One message and code for every failure; a dummy argon2id verification whenever no real hash is checked; over-long passwords are hashed truncated instead of failing fast; the attempted e-mail of an unknown account is not stored (D-107) | 1.5 | Database lookup timing differences (sub-millisecond, dominated by argon2id) |
| SV-16 | I | Leaks through errors and logs (stack traces, one-time tickets in URLs) | Generic error bodies, panics logged server-side only; access log never records query strings and cuts ticket/link paths to their prefix; readiness failures not returned to clients (D-102) | 1.1 | Handlers that put secrets in path segments outside the redacted prefixes |
| SV-19 | S/T | Passkey phishing, challenge replay or a cloned authenticator | RP ID and origin from `server.base_url` checked by go-webauthn (phishing-resistant); passkeys refused outside a secure context (http on a non-localhost host, IP hosts); challenges stored server-side only, bound to the session and user, 3-minute expiry, consumed by the finish step (single use); registration and management need step-up; a signature counter that does not increase (unless always 0) is refused, recorded on the passkey, audited and alerted (hook for 5.5); counter updates are conditional; every registration, assertion outcome, rename and removal audited (D-109) | 1.8, 1.9, 5.5 | A clone that is ahead of the original is detected only when the original is used again; passkeys without a counter (synced) give no clone signal |

### 6.4 Workers and pipeline (backup, restore, retention, notify; River)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| WK-1 | I | Secrets persisted in River job args / tables | Job args never contain secrets; age identities go to an encrypted handoff table with 15 min TTL, deleted after use | 3.4, 4.1 | — |
| WK-2 | T | Tampered or swapped artifact restored (T7) | sha256 verified on the staged copy; ed25519-signed manifest with key ID; cross-check against DB records (defeats replay of old manifests) | 3.1, 4.1 | Compromised `serve` can sign (T4) |
| WK-3 | I | Plaintext backups at rest on destinations | age encryption on by default; opt-out admin-only with step-up, alert, badge, audit; bounded `SetMaxWorkFactor` | 3.1, 3.2 | Opted-out jobs are plaintext (visible, deliberate) |
| WK-4 | I | Plaintext on the scratch volume during restore | Stage the encrypted artifact; opt-out jobs' staging wrapped with an ephemeral age key; size check; deletion on every path | 4.1 | Decrypted data is in memory while streaming |
| WK-5 | D | Slow or failing destination stalls a run (T7) | Bounded buffer per destination; laggards dropped → `partial`; multipart abort; per-worker timeouts | 3.1, 3.6, 3.4 | `partial` runs need operator attention (alerts) |
| WK-6 | T | Partial/corrupt object left behind | Disk/SFTP write `*.partial` + atomic rename (disk: hard-link finalize, fsync, temp removed on every failure, `CleanupPartials` for crash leftovers; D-121; SFTP: hidden `.<hex>.partial` 0600, `fsync@openssh.com` when offered, stored-size check, `hardlink@openssh.com` finalize, partial removed on every failure, `CleanupPartials`; D-128); S3: object visible only on `CompleteMultipartUpload`, `ExpectedSHA256` verified before Complete, multipart aborted on every failure incl. cancellation (detached context), `CleanupIncompleteUploads` + lifecycle `AbortIncompleteMultipartUpload` for crash leftovers (D-129); `ExpectedSHA256` checked before finalizing | 2.8 (disk done), 2.10 (SFTP done), 2.9 (S3 done), 3.1 | SFTP: a dropped connection can leave a partial until the next cleanup |
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
| DB-2 | T/R | Audit-log rewriting by the app | `SECURITY DEFINER` hash trigger under advisory xact lock; UPDATE/DELETE/TRUNCATE revoked + raising triggers; `bekup audit verify` (hash, link, genesis and signed-head checks); signed chain heads (`audit_chain_heads`, append-only) exported via `HeadExporter` (file now; destinations/webhook in 3.x/5.x) | 0.4, 1.3 | A DB superuser can rewrite the chain consistently; detected by comparing against exported signed heads (§8) |
| DB-3 | E | SQL injection | sqlc only; no hand-built SQL (lint rule + review); typed params | 0.4 | — |
| DB-4 | E | App role altering schema or grants | Migrations as `bekup_owner`; runtime as `bekup_app` (DML only); grants in migrations; CI checks | 0.4 | — |
| DB-5 | S/I/T | MITM, sniffing or tampering on the app-DB connection (e.g. a compromised container on `backend`) | TLS ≥1.2 `verify-full` + required SCRAM channel binding by default for pool and migrator: only the configured private CA, host name checked, PG* env/URL `sslmode` cannot weaken it (a stricter URL value is an error); shipped Postgres runs `ssl=on` and rejects non-TLS TCP logins of `bekup_owner`/`bekup_app`; postgres cert from the name-constrained CA (DNS `postgres`); app DB on an internal compose network (dev stack only: also published on host loopback `127.0.0.1:5432` for the natively running app, still TLS-only with the dev certificate also naming `localhost`/`127.0.0.1`, D-114); `database.url_file` secret | 0.3, 0.7, 1.0 | `tls_mode = require/disable` is possible but warned at every startup (badge + alert + audit: 5.5/6.x); `require` without a CA does not authenticate the server, but required SCRAM channel binding (ECDSA postgres certificate under a one-time ECDSA issuer) stops an active MITM from relaying the login (D-100, D-103, D-104) |
| DB-6 | T | Tampering with stored pins, allowlists or settings | Pin changes admin + step-up + audited; a pin confirmed by `certIndex` is re-captured, bound to the leaf key the admin compared and must verify as the chain's only root for the connection's host; public CAs are refused as anchors (D-139); host-key confirmations carry the fetched host/port and fail with CONFLICT if the record changed (D-140); changing a connection's host/port drops its pins, a bastion move drops its host key (D-135); runner allowlists are not in the DB at all | 2.11 (done), 2.12 (done), 2.5 | Compromised `serve`/DB superuser can change DB-held pins (see §8) |

### 6.7 Runner (`bekup runner`)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| RN-1 | S | Anyone else calling the runner | mTLS TLS 1.3, private CA pinned, client identified by SAN against `allowed client SANs` (handshake + every call); listens only on `runner-ctl` network; the health socket is container-local (private tmpfs, 0700/0600, runner-owned) and serves only SERVING/NOT_SERVING, no runner RPC (D-125) | 0.6, 2.4 | Theft of `serve`'s client cert (T4); a process already inside the runner container can read the health status |
| RN-2 | E | Command/argv injection via DB names or options | Typed RPCs, no free-form commands; DB names must be in the server-reported list (checked with the Go driver before every container, D-134), strict charset, no leading `-`; PG single-quoted keyword/value `--dbname=` conninfo; MySQL/MariaDB `--` before the positional DB and `--defaults-file` first (no image config files read); argv arrays built only by `internal/engine` from catalog entries; secrets only in preamble files (escaped option file / pgpass, NUL/CR/LF rejected); unknown proto fields rejected anywhere in a message; per-field and total size limits; static error messages that never echo values; fuzz-tested validators for every request (D-123, D-130) | 2.1, 2.5, 7.3 | — |
| RN-3 | E | Running an arbitrary image (T4 asks for `alpine` with a shell) | Image allowlist from embedded `catalog.toml` + mounted extra file only, digest-pinned (strict schema, embedded = official repos only, fixed binary names per engine, extra file cannot replace embedded keys); image key only selects an entry; `Entry` can only come from a validated catalog (D-123) | 2.1, 2.5, 7.3 | Operator-supplied extra images are trusted as pinned |
| RN-4 | E | Container escape or host access through the spec | Runner builds the spec itself (pure builder, snapshot + field tests): image by digest only, ReadonlyRootfs, single tmpfs `/tmp` noexec/nosuid/nodev 0700, CapDrop ALL, no-new-privileges, catalog non-root UID (never 0), default seccomp/AppArmor never overridden, memory (no swap)/CPU/pids limits, no mounts/devices/ports, private IPC/cgroupns, default masked `/proc` paths, not privileged, per-run `bekup-*` network only (D-130) | 2.5, 2.6, 7.3 | Kernel/runtime 0-days; rootless Docker / userns-remap recommended |
| RN-5 | I | Secrets visible in `docker inspect`, env, args, logs | Fixed reviewed wrapper reads base64 length-prefixed stdin records (fixed types → fixed paths) → 0700 dir / 0600 tmpfs files via the `printf` builtin (never in a child's argv) → `exec`; constant env allowlist (`HOME=/tmp`, `MYSQL_HISTFILE`/`PSQL_HISTORY=/dev/null`, `MYSQL_TEST_LOGIN_FILE=/dev/null`, ...); log driver `none`; no core dumps; exact-size encoder buffer zeroed by the caller; snapshot tests prove no secret in the spec; integration test in every pinned image (D-130) | 2.5, 2.6 | Secrets briefly in runner memory and in the container's tmpfs for the run |
| RN-6 | E/I | Runner used as an SSRF/port scanner into internal networks (T4) | Target allowlist (CIDRs/hosts) from `runner.toml`, deny by default (empty = deny all + startup warning); netguard's hard-denied ranges (loopback, link-local, metadata, ...) and the runner's own networks always denied, IPv4-mapped forms unmapped; every resolved address must pass at validation and the dialer's `Control` hook re-checks at dial time (DNS rebinding); `deny_hosts` refused before DNS; hosts behind a bastion checked against the deny lists and always-denied literals; target errors normalized (`runnerpb.Reason` in an `ErrorInfo`, one fixed message per reason): refused, filtered, timed-out and non-database endpoints (incl. failed non-certificate TLS handshakes) all read `TARGET_UNREACHABLE`; server messages are never returned or logged; integration-tested (closed port, unused address, SSH port as "database") (D-130, D-134) | 2.5, 2.7 (done) | Everything inside the allowlist is reachable with credentials `serve` holds; a database host behind a bastion is resolved by the bastion; response timing still differs between refused and filtered ports |
| RN-7 | S | Target DB impersonated (T8) | Pinned cert as the only trust anchor for native clients (`verify-full` / `VERIFY_IDENTITY` / `--ssl-verify-server-cert`) + Go pre-flight per pin (anchor chain + host name/IP + leaf SPKI, constant time); mismatch = `FAILED_PRECONDITION` with `ErrorInfo` `TLS_PIN_MISMATCH` (hard fail, security alert in 5.5), no container started; serve records it as `connection.tls_pin_mismatch`, raises the alert hook and answers `TLS_PIN_MISMATCH` (D-135); expiry warnings 30/7 days (`pinStatus`); integration-tested per engine: another CA fails pre-flight and native client, another leaf of the same CA fails the pre-flight, another self-signed leaf fails both (D-133); re-pin admin + step-up | 2.7a (done), 5.5 | TLS-off connections (explicit admin opt-out, badge, alert); with a CA anchor the native client alone would accept another leaf of that CA (the pre-flight gates every run; pin a self-signed leaf to make the client refuse it too) |
| RN-8 | S | SSH bastion impersonated | Mandatory host-key pin (type + SHA-256, constant-time compare) with TOFU confirmation in the UI, no bypass; only the pinned key type is negotiated, so a server not offering it is a mismatch (security event); RSA ≥ 2048 with rsa-sha2 only, no DSA/SHA-1/certificates; modern KEX/ciphers/MACs only; the fingerprint fetch aborts before any authentication; all dials through the runner's allowlist dialer (D-122); the TOFU fetch is the runner's `FetchHostKey` RPC (validated bastion, target-allowlist dialer, no authentication, normalized errors, audit line), serve never contacts the bastion; confirmation admin + step-up + audited and bound to the bastion the key was fetched from (CONFLICT otherwise, D-140); a mismatch is audited (`connection.ssh_host_key_mismatch`), alerted and returned as `SSH_HOST_KEY_MISMATCH` (D-135); tunnel and SFTP share one policy (`internal/sshpolicy`, D-140) | 2.2, 2.11 (done), 2.14 | TOFU trusts the first key the admin confirms; verify the fingerprint out of band. SSH passwords pass through an unzeroable Go string inside x/crypto/ssh; prefer keys |
| RN-9 | D | Leaked containers/networks; stuck runs; call floods | Timeout → SIGTERM, SIGKILL after 10 s; cancel/stream loss/broken restore input → SIGKILL; cleanup defer chain on every path incl. panics (unit-tested per failure step); subnets whose cleanup failed stay denied until reaped; label-based orphan reaper at startup and every 5 min; resource limits; bounded streaming (one ≤ 256 KiB chunk buffered, backpressure to the tool) with gRPC flow control; log lines/length capped; mandatory deadlines clamped to `max_run_duration`; fail-fast concurrency classes (runs, control) re-checked by the executor; subnet pool exhaustion = `RESOURCE_EXHAUSTED`; stream/metadata/message/handshake limits (D-127, D-132) | 2.4, 2.6 | An allowed client can occupy all run slots |
| RN-10 | E | Runner compromise = Docker = host root | Minimal code, distroless nonroot, `group_add` docker GID only, no DB access, no inbound except mTLS | 2.4, 2.6, 0.7 | Inherent to Docker socket access (§8) |
| RN-11 | T | `serve` changing the runner's allowlists | Separate `runner.toml`, never read from the DB | 0.3, 2.5 | — |
| RN-12 | T/E | Connection tampering through the API: a hijacked or unattended admin session points a connection at another server, weakens TLS or swaps pins/host keys; probing through test/list/fetch | Every connection create/update/delete, TLS-mode change, pin confirmation/staging, host-key confirmation and keypair generation needs admin + fresh step-up and is audited (changed fields; credentials by name only, plus `secret.changed`); leaving PINNED also needs `connection.tls_downgrade`, records `security.opt_out`, raises the alert hook and sets the `insecureTransport` badge; pinned IP-literal targets without a tunnel are refused at save time (D-132); a stored database or SSH password is never sent to a changed endpoint: moving the host/port, the tunnel route or the bastion (host/port/user) requires the password again in the same update (VALIDATION otherwise; audited as `reentered_for_new_endpoint`, D-140); runner calls rate limited per user, bounded in concurrency and time, one per request (D-117), normalized results (D-135) | 2.11 (done), 2.14, 5.5 | A fully authenticated, stepped-up admin can still repoint or downgrade a connection (audited; alerting in 5.5) |

### 6.8 Dump containers

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| DC-1 | E | Malicious target DB (T6) exploits the dump client | Container alone on a per-run internal network with only the proxy; hardened spec; no mounts; short-lived; stderr sanitized (control characters, invalid UTF-8), capped and redacted before it leaves the runner (D-132) | 2.6, 7.3 | A client RCE can still tamper with *that run's* dump output; signing happens after (in `serve`) so it can't forge older artifacts |
| DC-2 | I | Exfiltration from the container to the internet, other runs or the runner gRPC port | `internal: true` /28 per run with `inhibit_ipv4` (no host address, no route out, no external DNS on current Docker Engines); proxy accepts only the run's container and forwards to exactly one target; per-run subnets and the pool are the runner's own networks (never targets) and refused by the gRPC listener; IP-literal targets with `verify-full` only via a /29 around the literal behind a tunnel, otherwise fail closed; integration test (containerized runner) proves no access to the gRPC port (control/per-run address, wildcard bind), other runs' proxy/container, the gateway, the target without the proxy, the internet or DNS (D-132) | 2.6 | Older Docker Engines forwarded DNS from internal networks (a DNS exfiltration channel): run a current Engine; the run's own target remains reachable by design |
| DC-3 | T | Version mismatch producing unusable dumps | PG client major ≥ server major enforced (block); restore image = max(dump, target); minimum servers PG 10, MySQL 8.0, MariaDB 10.4; MySQL/MariaDB older-client fallback only with a warning (D-123) | 2.1 | MySQL/MariaDB servers newer than every pinned client (warning) |
| DC-4 | S/I | Downgraded or unverified TLS inside the container | Pinned cert via preamble as only trust anchor (`verify-full` / `VERIFY_IDENTITY` / `--ssl-verify-server-cert`, TLS ≥ 1.2; secure zero-value mode in `internal/engine`); `ExtraHosts` keeps hostname verification working through the proxy; `unverified`/`disabled` modes only as admin-only audited downgrades; `TrustAnchor` sends only the pinned anchors (D-034, D-123, D-133) | 2.1, 2.7a (done) | Connections an admin explicitly downgraded |
| DC-5 | T | Compromised official image (T9) / client without safety flags | Multi-arch index digests in `catalog.toml`, Renovate updates with minimum release age; per-series client floors enforced for every entry: PG `\restrict` fix, MySQL `--system-command` + `--commands`, MariaDB `--sandbox` (D-123) | 2.1 | Upstream image compromise before pinning; psql meta-commands in a malicious globals file during the admin-only globals restore |
| DC-6 | D/T | Silently truncated dump | VerifyDump reads the whole PostgreSQL archive (`pg_restore --file=/dev/null`, not `--list`) in an offline container (no network, no credentials); MySQL/MariaDB "Dump completed" trailer + exit code and pg_dumpall's "dump complete" trailer checked in Go; exactly `total_bytes` must arrive; non-InnoDB warning; optional scheduled test restores (D-134) | 2.7 (done), 3.6, 4.6 | MySQL/MariaDB dumps of non-InnoDB tables are not consistent |

### 6.9 Destinations (disk, S3, SFTP)

| ID | STRIDE | Threat | Mitigations | Tasks | Residual risk |
|---|---|---|---|---|---|
| DS-1 | I | Provider or thief reads backups (T7) | Client-side age encryption by default | 3.1, 3.2 | Metadata (sizes, timestamps, job slugs, DB names encoded in paths) is visible |
| DS-2 | T | Provider modifies or replays objects | sha256 + signed manifest + DB cross-check at restore | 3.1, 4.1 | Detection only; availability depends on other copies (fan-out) |
| DS-3 | D | Deletion by a compromised `serve`, stolen credentials or ransomware | S3 Object Lock / versioning (detected at open; lock options fail closed; deletes remove exact versions so locks are enforced, never a hiding delete marker; governance bypass never sent; D-129); no-delete credentials mode with bucket lifecycle rules (IAM deny + lifecycle examples in the destinations guide); unexpected-deletion alerts; multiple destinations | 2.9 (S3 done), 3.8, 5.5 | Disk/SFTP destinations have no immutability; use S3 Object Lock for ransomware resistance. SFTP no-delete mode only binds bekup: withhold the delete right on the server too (`internal-sftp -P remove,rmdir,rename,posix-rename`; D-128) |
| DS-4 | S | MITM to S3/SFTP (T8) | S3: https only (http only in dev mode, warned), TLS ≥1.2 system roots or an optional CA bundle that replaces them, static credentials only (no env/shared-config/IMDS chain), no presigned URLs (D-129); SFTP: mandatory host-key pin (type + SHA-256, constant-time; only the pinned type negotiated; a server not offering it is a mismatch), no insecure/TOFU mode, strong kex/ciphers only, client keys ed25519/ECDSA/RSA ≥ 2048 with SHA-2 (D-128; one policy with the tunnel in `internal/sshpolicy`, D-140); a mismatch is the storage code `host_key_mismatch` and a failed test records `destination.ssh_host_key_mismatch` (D-140) | 2.9 (S3 done), 2.10 (SFTP done), 2.14 | S3 not pinned by design (D-018); the S3 secret key exists as an unzeroable Go string in the SDK credentials cache while a destination is open; SFTP password auth leaves an unzeroable Go string copy during the handshake |
| DS-5 | E | SSRF via a destination endpoint (admin-entered or rebinding) | Dial-time netguard denies private/loopback/link-local/metadata by default (re-checked per socket, so rebinding fails), admin allowlist (link-local, metadata, multicast, reserved and loopback outside dev never allowlistable), no proxy, no redirects, normalised errors for non-admins (D-120) | 2.3 (done), 2.9 (S3 done: mandatory guard, guarded transport, policy errors never retried, capped non-object response bodies, missing bucket = access denied, all transport failures one code; D-129), 2.10 (SFTP done: mandatory `*netguard.Guard`) | Allowlisted internal ranges |
| DS-6 | E | Path traversal on disk and SFTP destinations | Disk: confined to `disk_root` via `os.Root`; symlinks never followed (Get/Stat/List/Put/Delete). SFTP: base resolved once per connection (REALPATH), every directory below it Lstat-checked, objects must be regular files, Get compares fstat with Lstat; symlinks refused/treated as missing/skipped (D-128). Both: validated keys only (`ParseKey`/`BuildKey`: no `..`, absolute paths, dot names, control or non-ASCII bytes); job slug / canonical `enc(db)` encoding in paths (D-121) | 2.8 (done), 2.10 (done), 3.1 | A local user with write access below `disk_root` can still delete or replace backups (no immutability on disk). SFTP has no O_NOFOLLOW/openat: a party that can modify the server tree concurrently can win a check-then-use race; the server is trusted for its permissions/chroot (use a dedicated chrooted account) |
| DS-7 | T | Overwrite or truncation of an existing backup through a key collision, a racing writer or a retried upload | Put never overwrites (`ErrAlreadyExists`; disk and SFTP finalize with an exclusive hard link, SFTP never uses `posix-rename`); canonical, injective DB-name encoding; idempotent delete; object-lock options fail closed on backends without lock (D-121, D-128); S3: HEAD pre-check + `If-None-Match: *` on PutObject and CompleteMultipartUpload (D-129) | 2.8 (done), 2.10 (done), 2.9 (done) | Disk on file systems without hard links and SFTP servers without `hardlink@openssh.com` fall back to check + rename (small race window if the server's rename overwrites; logged); S3 with `ConditionalWrites` off relies on the pre-check only (race; versioning keeps the earlier version) |
| DS-8 | T/E | Destination tampering through the API: a hijacked or unattended admin session redirects backups (endpoint, bucket, host) or removes immutability (no-delete, Object Lock); probing through "test" | Every destination create/update/delete, host-key confirmation and keypair generation needs admin + fresh step-up and is audited (changed fields; credentials by name only, plus `secret.changed`); endpoint/host resolved and checked against the SSRF guard at save time and again at dial time; changing an SFTP host/port drops the host-key pin; changing host, port, user or base path requires a stored password again in the same update (never sent to a new endpoint, D-140); host keys only pinned after an explicit confirmation bound to the fetched host/port (TOFU fetch never stores and only probes a stored destination's host, with normalized errors, D-140); test and host-key fetch rate limited per user, bounded in concurrency and time, one per request, normalized results (D-131) | 2.12 (done), 2.14 | A fully authenticated, stepped-up admin can still point a destination elsewhere (audited; alerting in 5.5) |

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
| CL-3 | E/R | `bekup admin create|reset-password|reset-2fa|unlock|disable` used as a backdoor | Requires shell access to the deployment and the DB credentials (T10, trusted); every action is audited with actor `cli`, OS user and hostname in the action's transaction; resets (password, second factors) revoke all sessions; passwords only via hidden prompt or a permission-checked file, never argv (D-069, D-110, D-112, D-113) | 1.6, 1.9, 5.5 | Alert to admins arrives in 5.5 (OI-3) |
| CL-4 | T | Offline `bekup decrypt` of a tampered or forged artifact | `bekup verify` checks sha256 + manifest signature against the escrowed signing-key fingerprint; documented format | 4.7 | Operators must check the fingerprint |
| CL-5 | D | Malicious artifact exhausting resources in offline tools | age work factor bounded (`SetMaxWorkFactor`) | 3.1, 4.7 | Large legitimate files need disk space |
| CL-6 | I | `config print` leaking secrets | `--redacted`; secrets exist only as file paths | 0.3 | — |
| CL-7 | T | Malicious config file (symlink to a writable path, group-writable) | Regular file, owned by user or root, not group/world-writable; strict TOML (unknown keys fail); no hot reload | 0.3 | — |
| CL-8 | R/T | Tampered audit log goes unnoticed | `bekup audit verify` exits non-zero on the first broken link or a failed signed head; `--head-file` checks an exported head against the DB; `sign-head --export-file` | 1.3 | Rows after the last exported head (D-101) |

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
| Keyring, AES-GCM + AAD, key-check, zeroing, instance signing keys, key generators | DB-1, CL-1, WK-2 | 0.5 | D-016, D-046, D-085–D-088, D-119 |
| Runner CA and per-component certs | RN-1 | 0.6 | D-030 |
| App DB TLS (`database.tls_mode`, ECDSA postgres cert + required channel binding, `hostssl`-only bekup roles) | DB-5 | 1.0 | D-100, D-103, D-104 |
| Segmented compose networks, secrets, read-only FS, `serve` unpublished | RP-3, DB-5, RN-10 | 0.7 | D-017 |
| Hardened reverse-proxy profiles | RP-1/4/5 | 0.7b | D-017, D-048 |
| CSP `style-src`, design system | UI-2 | 0.8 | D-050 |
| CI scanners, SHA-pinned Actions | SC-S1 | 0.9 | D-027, D-061 |
| Security headers, Origin/Sec-Fetch, size/rate limits, trusted proxies, startup role and schema checks | UI-2/3/4/6, RP-2/4/7, SV-9/10/15/16 | 1.1 | D-048, D-050, D-078, D-095, D-099, D-102 |
| GraphQL POST+JSON, limits, masked errors, authenticated introspection | UI-3, SV-8/10 | 1.2 | D-050 |
| Audit API, `bekup audit verify`, signed chain-head export | UI-7, SV-12, DB-2, CL-8 | 1.3 | D-054, D-069, D-092, D-101 |
| Deny-by-default authz (operation catalog, policy precheck per root field, owner scope, restore-target rules) + matrix test; admin-only `auditLog` query | SV-4/5/6/7 | 1.4 | D-011, D-053, D-067, D-106 |
| argon2id, password policy, sessions, lockout | UI-1, SV-1/2/10 | 1.5 | D-010, D-047 |
| First admin via CLI, admin CLI (incl. `reset-2fa`), user CRUD, second-factor reset, session revoke | SV-3, CL-3 | 1.6, 1.9, 1.12 | D-047, D-069, D-110, D-112, D-113 |
| TOTP + recovery codes; WebAuthn passkeys | UI-1, SV-2 | 1.7, 1.8 | D-010 |
| 2FA policy, step-up | SV-5 | 1.9 | D-047, D-112 |
| Subscription streams (SSE): auth, caps and re-validation | SV-11 | 1.10, 2.13 | D-052, D-108, D-124 |
| Engine catalog, version rules, argv builders | RN-2/3, DC-3/5 | 2.1 | D-036, D-123 |
| SSH host-key pinning | RN-8 | 2.2 | D-035, D-122 |
| SSRF guard | DS-5, NT-1 | 2.3 | D-055, D-120 |
| Runner gRPC + mTLS | RN-1, RN-9 | 2.4 | D-030, D-125, D-126, D-127 |
| Validators, target allowlist, hardened spec, preamble | RN-2/3/4/5/6/11 | 2.5 | D-029, D-031, D-032, D-033, D-130 |
| Per-run networks, proxy/tunnel, reaper | RN-9, DC-1/2 | 2.6 | D-029, D-132 |
| Target-DB certificate pinning | RN-7, DC-4 | 2.7a | D-018, D-034 |
| Typed RPCs, normalised target errors, Go-driver pre-checks, offline verify | RN-2, RN-6, DC-6 | 2.7 (done) | D-030, D-033, D-134 |
| Disk `os.Root`, atomic rename, key validation, no overwrite | DS-6, DS-7, WK-6 | 2.8 | D-055, D-121 |
| S3 https/Object Lock/no-delete; SFTP host-key pin, hard-link finalize, symlink checks | DS-3/4/6/7, WK-6/7 | 2.9, 2.10 | D-007, D-049, D-128, D-129 |
| Admin-only connection/destination CRUD, pin confirmation, TLS downgrade flow | SV-6/8, RN-7/8/12, DB-6, DS-8 | 2.11 (done), 2.12 (done) | D-011, D-034, D-131, D-135 |
| Pipeline: zstd → age → sha256 → bounded fan-out; signed manifest | WK-2/3/5/6, DS-1/2 | 3.1 | D-037, D-038 |
| Job keys, opt-out flow, passphrase export | WK-3 | 3.2 | D-008 |
| Scheduler math and loop, River config | SC-1/2/3, WK-1/9 | 3.3, 3.4 | D-043, D-044 |
| Backup worker, log redaction/caps, concurrency | SV-13, SC-2/4, DC-6 | 3.6 | D-045, D-060 |
| Events via NOTIFY IDs | SV-11 | 1.10 (session events), 3.7 | D-052, D-108 |
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
  detection. Until the destination/webhook exporters land (tasks 3.x/5.x),
  operators export heads with `bekup audit sign-head --export-file` and keep
  them off the database host (D-101).
- **Malicious target DB (T6) can tamper with its own backups.** Dumps are
  only as trustworthy as the server producing them; signing proves bekup
  stored it, not that the content is benign. See OI-2 for restore-time
  impact.
- **TOFU for SSH host keys and DB certificate pins.** The first pin is only
  as good as the admin's out-of-band check of the fingerprint.
- **Same-CA leaf with a private CA pin (D-139).** With a CA anchor the
  native dump client checks only the chain; another leaf of the same
  private CA passes it, while the runner's pre-flight (leaf SPKI) refuses
  it. A leaf swapped between pre-flight and the container's connection is
  accepted; issuing one requires the operator's own private CA. Public CAs
  cannot be pinned; pin a self-signed leaf to close the window.
- **Unlisted SSH bastions (D-138).** A bastion without
  `targets.bastion_targets` entries may forward to any host it reaches
  (only the deny lists apply behind it); the runner warns at startup and
  once per bastion in use.
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
