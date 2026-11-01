# Hardening guide

> **Status:** partial (end of M6). The controls below exist in the shipped
> compose files, the implemented CLI/config code, `serve` and the runner:
> HTTP headers, Origin checks and the plain-HTTP warning (1.1), app-DB TLS
> with channel binding (1.0), passwords and sessions (1.5), second
> factors, the 2FA policy and step-up (1.7–1.9), the audit log with signed
> heads (1.3), runner isolation (2.4–2.7a: mTLS API, image and target
> allowlists, hardened dump containers on per-run internal networks,
> target-DB certificate pinning), the SSRF guard (2.3) on the S3 and SFTP
> destinations (2.9, 2.10) and Object Lock / no-delete support in the S3
> backend (2.9), backup jobs with encryption on by default (3.2, 3.5),
> verified and signed backups with completeness evidence (3.1, 3.6, 3.10),
> retention honouring Object Lock and no-delete mode (3.8) and step-up
> download tickets (3.9); hardened restores with the fresh second-factor
> check and privileged-target gating (M4); the SSRF guard for SMTP and
> webhooks with the admin allowlist, security alerts and monitoring (M5,
> D-196); the web UI under the strict CSP (M6). Still to come: signed
> release images (7.4). Completed in task 7.5.

A checklist for running bekup securely. The threat model behind it is
[`SECURITY.md`](../../SECURITY.md); deployment details are in
[installation/compose.md](../installation/compose.md) and
[installation/reverse-proxy.md](../installation/reverse-proxy.md).

## Host and Docker

- [ ] Use a dedicated, patched Docker host. The runner holds the Docker
      socket, which is root-equivalent (D-003); nothing else does, including
      both proxy profiles (D-093, D-094).
- [ ] Set `DOCKER_GID` to the socket's group; never run the runner as root.
- [ ] Consider rootless Docker or userns-remap for the runner's daemon
      ([rootless-docker.md](../installation/rootless-docker.md)): rootless
      Docker takes host root away from the socket; userns-remap only
      remaps the dump containers.
- [ ] Pin `BEKUP_IMAGE` to a released, digest-pinned image. Every image in
      the Dockerfile and compose files is pinned by tag and digest; Renovate
      keeps them current (D-061, D-065). Verify the release signature with
      `cosign verify` before running the image
      ([verifying release images](../installation/compose.md#verifying-release-images), D-204).

## Container hardening (shipped)

Every bekup service (`certs-init`, `migrate`, `serve`, `runner`) runs with:

| Control | Effect |
|---|---|
| distroless `nonroot` image, `user: 65532:65532` | no shell or package manager, never root; the binary is root-owned `0555` (D-076) |
| `read_only: true`, `tmpfs /tmp` with `noexec,nosuid,nodev` | no persistent changes outside declared volumes |
| `cap_drop: [ALL]`, `no-new-privileges:true` | no capabilities, no setuid escalation |
| CPU, memory and PID limits; bounded `json-file` logs | no resource exhaustion of the host |
| no `ports:` on `serve`, `runner`, `postgres` | only the proxy is published (80/443) |

The proxies (Traefik, unprivileged nginx, certbot) use the same UID 65532,
read-only root, `cap_drop: [ALL]`, `no-new-privileges` and limits; their
one-shot `*-init` helpers have only `CHOWN`/`FOWNER` and no network (D-093,
D-094).

The app Postgres runs as UID 70 with a read-only root, no capabilities,
SCRAM-SHA-256 for local and TCP logins and data checksums. Its bootstrap
superuser is **rejected on every TCP connection** by `pg_hba.conf` and can
only log in over the container-local socket
(`deploy/postgres/init/10-bekup-roles.sh`). The bekup roles are
least-privilege (D-054, D-092, [db-roles.md](db-roles.md)), and only the
one-shot `migrate` container holds the `bekup_owner` URL (D-078).

The dev override keeps all of this for the bekup services. Its `serve`
mounts only the Go sources read-only, never `secrets/` (D-081).

## Network segmentation (shipped)

- [ ] Keep the five-network layout (D-077, D-093): `frontend`, `backend`
      and `runner-ctl` are `internal`; only the proxy (`edge`), `serve`
      (`egress`) and the runner (`targets`) have outbound access.
- [ ] Bind `serve` to its `frontend` address (`listen =
      "10.213.250.10:8080"`) and the runner to its `runner-ctl` address
      (`address = "10.213.251.10:7443"`), so neither listens on other
      networks, including the runner's per-run networks.
- [ ] If the fixed subnets clash with your LAN, change the compose variables
      **and** the TOML addresses together.

## Runner isolation (shipped, tasks 2.4–2.7a)

- [ ] Leave `[listen] allowed_client_sans` at the bekup components
      (`spiffe://bekup/serve`, `spiffe://bekup/worker`); the runner accepts
      only mTLS (TLS 1.3) clients with a certificate from the private CA
      (D-030, D-089).
- [ ] Keep `[images] extra_file` empty unless you need another release
      series. Every image is pinned by digest, and the runner refuses
      anything else (D-029).
- [ ] Pick a `[docker] run_subnet_pool` that overlaps neither your Docker
      networks nor `targets.allow_cidrs`. Each run gets its own `/28`
      `internal` network holding only the runner and one dump container.
      Targets inside the pool are always denied, and the gRPC port refuses
      connections from it.
- [ ] Dump containers run with a read-only root, no capabilities,
      `no-new-privileges`, the default seccomp profile, `noexec` tmpfs, no
      core dumps, no log driver and CPU/memory/PID limits. Secrets reach
      them only through the stdin preamble (D-031, D-032). Don't override
      this spec. The runner builds it itself and offers no way to pass
      other options.
- [ ] Leftover `bekup-run-*` networks and containers (after a crash) are
      removed at startup and periodically (`runner reaped leftovers` in
      the runner log). Frequent reaps point at runs dying mid-way.

## Reverse proxy and TLS (shipped)

- [ ] Enable exactly one profile (`traefik` or `nginx`). Both ship TLS
      1.2/1.3 with ECDHE+AEAD only, X25519MLKEM768 first, unknown-SNI
      handshakes rejected, HSTS (2 years, preload-ready), the HTTP→HTTPS
      redirect, 2 MiB request bodies and per-IP rate limits (D-093).
- [ ] `base_url` is `https://…` and `trusted_proxies = ["10.213.250.5/32"]`
      (the proxy's fixed address, never the subnet). Own reverse proxy in
      join mode: `trusted_proxies = []` and `trusted_proxy_hosts` names only
      your proxy's container (D-215). Keep other containers off the proxy
      network where you can, and point the proxy at serve's container name
      (for example `bekup-serve-1`) rather than the alias `bekup-serve`
      where your proxy allows it; for strict isolation use link mode.
- [ ] Run the [reverse-proxy security checklist](../installation/reverse-proxy.md#security-checklist),
      including an external `testssl.sh` scan.

### Plain HTTP

Plain HTTP is allowed in dev mode (loopback `127.0.0.1:8080`) and, outside
dev mode, only with `[server] allow_insecure_http = true` (D-095). It is
never silent: a startup WARN, a permanent red UI banner, and outside dev mode
a security alert and an audit entry (tasks 1.1, 5.5, 6.1). Don't run
production over plain HTTP unless the network path is fully trusted. See
[reverse-proxy.md](../installation/reverse-proxy.md#without-a-proxy-plain-http-d-095).

## Secrets and file permissions (shipped)

- [ ] Secrets only as files: Docker secrets under `/run/secrets/`, referenced
      by `*_file` keys. Inline secrets in TOML, env or argv are rejected
      (D-019, D-073).
- [ ] Each container receives only its own secrets (the table in
      [compose.md](../installation/compose.md#secrets)). The CA key is
      mounted only into the one-shot `certs-init` and kept offline otherwise
      (D-079, D-090).
- [ ] On Linux, `bekup.toml`, `runner.toml` and every secret file are owned
      by **root or UID 65532** (postgres secrets: UID 70) and are **not
      group- or world-writable**; secret files are also not world-readable.
      bekup refuses to start otherwise (D-041). See
      [compose.md](../installation/compose.md#file-ownership-and-permissions).
- [ ] Generate the master key with `bekup keys generate` (`0600`) and escrow
      it offline ([key-management.md](key-management.md)).
- [ ] Back up the proxy certificate volume (`traefik-acme` / `nginx-certs`)
      like a secret.
- [ ] Never commit `secrets/`, keys, certificates, local TOML files or
      `.env` files; `.gitignore` and `.dockerignore` block them (D-066).

## Passwords and sessions (task 1.5)

The defaults are the secure baseline; `bekup serve` refuses weaker
values (D-073, D-107).

- [ ] Keep `server.base_url` https: only then is the session cookie
      `__Host-bekup_session` with `Secure`. In the opted-in plain-HTTP mode
      (D-095) it is `bekup_session` without `Secure`, and sessions travel
      in clear text.
- [ ] Keep `security.session_idle_timeout` (30m) and
      `session_absolute_timeout` (12h) short; users can list and revoke
      their sessions, and a password change revokes all of them.
- [ ] Raise `security.argon2_memory_kib`/`argon2_iterations` if the host
      allows (existing hashes are upgraded at the next login), and size
      `argon2_max_concurrent` × `argon2_memory_kib` well below the `serve`
      memory limit; excess logins get `RATE_LIMITED` instead of queueing.
- [ ] Optionally raise `security.password_min_length` (≥ 12) and
      `password_min_score` (3 or 4). Common passwords and passwords
      containing the e-mail address or name are always refused.
- [ ] Loosen `security.login_rate_limit_*` and `security.lockout_*` only
      with care. The lockout is per account and temporary (1m, doubling up
      to 1h by default); watch the `auth.lockout` and `auth.login.failed`
      audit entries.

## Outbound connections and SSRF (task 2.3)

- [ ] Keep `[netguard]` `allow_cidrs` / `allow_hosts` empty unless a
      destination really lives on a private network (for example a MinIO
      or SMTP relay); then allowlist the narrowest CIDR (`10.20.0.5/32`) or
      exact host instead of whole RFC 1918 ranges. `*.suffix` entries
      allow every subdomain, so use them only for zones you control.
      With your own reverse proxy (D-215) `serve` also sits on the
      proxy's Docker network and, with a database container, on its
      network: an entry such as `172.16.0.0/12` then lets destinations,
      SMTP and webhooks reach every container on those networks too.
- [ ] Loopback, link-local, cloud metadata, multicast, broadcast and
      reserved ranges (including their IPv4-mapped, NAT64, 6to4 and Teredo
      forms) are always denied and cannot be allowlisted; loopback is
      allowlistable only in dev mode (D-120).
- [ ] bekup ignores `HTTP(S)_PROXY` for these connections: a proxy would
      resolve the target itself and bypass the dial-time check. Give
      `serve` direct egress (the `egress` network) to its destinations.
- [ ] Redirects are never followed; point webhooks and S3 endpoints at
      their final URL.
- [ ] Prefer the **admin allowlist** (Admin → Settings → Network) over
      `[netguard]` for entries that change: it is validated with exactly
      the same rules, needs an admin with a fresh step-up, is audited
      (`setting.changed` with the added and removed entries) and every
      addition raises the `ssrf_allowlist_widened` security alert to all
      admins (D-196). Keep `[netguard]` for entries that must exist before
      anyone signs in. The effective list is both lists together; a change
      reaches every `serve` and `bekup worker` process within seconds
      (settings event) and at the latest after 5 minutes. Review the
      admin list after every `ssrf_allowlist_widened` alert and remove
      entries that are no longer needed.
- [ ] Give every admin entry the **narrowest scopes** (D-198):
      `destinations`, `smtp`, `shared_webhooks` (the default for entries
      saved without scopes, and the only scopes the `[netguard]` bootstrap
      list ever applies to) and `personal_webhooks`. Avoid
      `personal_webhooks`: any user can enter a personal webhook URL, so
      such an entry lets every account make bekup send requests to that
      private target. If you must add it, scope it to one exact host or a
      `/32`; the save is audited (`personal_webhooks_widened`), alerted
      and the Network page flags it permanently.
- [ ] Wildcard entries over a public suffix (`*.co.uk`, `*.github.io`)
      and any entry under a public wildcard-DNS service (`nip.io`,
      `sslip.io`, `localtest.me`, ...) are refused: anyone could make such
      names resolve to the address of their choice.
- [ ] Stored admin entries that a process rejects (for example loopback
      after turning dev mode off) are not applied (fail closed) and are
      reported as ignored on the Network page until the list is saved
      again.

## Accounts, storage and monitoring

- 2FA policy and step-up (tasks 1.7–1.9, D-112): 2FA is **off by
  default**. Turn it on right after creating the first admin: require it
  for admins and maintainers (ideally everyone) under **Users → 2FA
  policy** (`updateTwoFactorPolicy`), with a short grace period (default 7
  days, counted from when you turn it on); keep the number of admins
  small. Accounts without a factor after the grace period can only enroll
  one (TOTP always works, also over plain http). Treat `mfa_policy.updated`
  entries with `weakened: true`, `auth.recovery_code.used`,
  `user.second_factors_reset` and `cli.admin.mfa_reset` as security
  events; reset someone's factors only after verifying their identity out
  of band. Restores always need a fresh passkey/TOTP check, whatever the
  policy.
- First admin (D-113): created only with `bekup admin create` on the host;
  there is no web setup page an attacker could race to. Run it right after
  the first start.
- Passkeys (task 1.8, D-109) are phishing-resistant and preferred over
  TOTP. They need an https `server.base_url` (or `localhost`) with a host
  name, not an IP address, and they are bound to that host name: changing
  the host of `base_url` invalidates every registered passkey, so make sure
  users keep a second factor or recovery codes before you move. Treat an
  `auth.passkey.sign_count_anomaly` audit entry (possible cloned
  authenticator) as an incident: ask the user to delete that passkey.
- Immutable destinations (tasks 2.9, 3.8): S3 Object Lock or no-delete
  credentials, more than one destination
  ([immutable-backups.md](immutable-backups.md)).
- Target allowlist (task 2.5): restrict `[targets]` in `runner.toml` to the
  exact hosts you back up, list every SSH bastion's databases in
  `bastion_targets`, and open Docker networks only with CIDRs inside them
  ([target-allowlist.md](target-allowlist.md)).
- Runner host (D-138): Docker Engine 26.0.0 or newer (older daemons are
  refused). Dump containers never get a writable volume: every image
  `VOLUME` is covered by a read-only tmpfs. psql always runs under a random
  `\restrict` key, so a restored globals script cannot run shell or
  meta-commands.
- Target TLS (task 2.7a): keep every connection in the `pinned` TLS mode;
  give database servers certificates with SANs for the configured host (or
  IP), pin a self-signed **private** root CA or a self-signed server
  certificate (public CAs are refused, D-139), verify
  the captured fingerprint out of band, stage a backup pin before planned
  rotations, and treat a pin-mismatch alert as a possible interception
  ([connections.md](../guides/connections.md#tls-and-certificate-pinning)).
- Audit (task 1.3): run `bekup audit verify` regularly ([audit.md](audit.md)).
- Monitoring (task 5.6): scrape `/metrics` on the internal port only; alert
  on missed backups and certificate expiry ([operations.md](../operations.md)).
