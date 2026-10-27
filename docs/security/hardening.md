# Hardening guide

> **Status:** partial (end of M1). The controls below exist in the shipped
> compose files, the implemented CLI/config code and `serve`: HTTP headers,
> Origin checks and the plain-HTTP warning (1.1), app-DB TLS with channel
> binding (1.0), passwords and sessions (1.5), second factors, the 2FA
> policy and step-up (1.7–1.9) and the audit log with signed heads (1.3).
> Still to come with their tasks: runner isolation (2.4–2.7a), SSRF guard
> (2.3), immutable storage (2.9, 3.8), monitoring (5.6), signed release
> images (7.4). Completed in task 7.5.

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
      ([rootless-docker.md](../installation/rootless-docker.md), task 2.6).
- [ ] Pin `BEKUP_IMAGE` to a released, digest-pinned image. Every image in
      the Dockerfile and compose files is pinned by tag and digest; Renovate
      keeps them current (D-061, D-065). Verify release signatures with
      `cosign verify` once task 7.4 publishes them.

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

## Reverse proxy and TLS (shipped)

- [ ] Enable exactly one profile (`traefik` or `nginx`). Both ship TLS
      1.2/1.3 with ECDHE+AEAD only, X25519MLKEM768 first, unknown-SNI
      handshakes rejected, HSTS (2 years, preload-ready), the HTTP→HTTPS
      redirect, 2 MiB request bodies and per-IP rate limits (D-093).
- [ ] `base_url` is `https://…` and `trusted_proxies = ["10.213.250.5/32"]`
      (the proxy's fixed address, never the subnet).
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
  exact hosts you back up ([target-allowlist.md](target-allowlist.md)).
- Audit (task 1.3): run `bekup audit verify` regularly ([audit.md](audit.md)).
- Monitoring (task 5.6): scrape `/metrics` on the internal port only; alert
  on missed backups and certificate expiry ([operations.md](../operations.md)).
