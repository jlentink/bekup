# Hardening guide

> **Status:** partial (end of M0). The controls below exist in the shipped
> compose files and the implemented CLI/config code. Application-level
> controls are added by their tasks: HTTP headers, Origin checks and the
> plain-HTTP warning (1.1), sessions and 2FA policy (1.5–1.9), runner
> isolation (2.4–2.7a), SSRF guard (2.3), immutable storage (2.9, 3.8),
> monitoring (5.6), signed release images (7.4). Completed in task 7.5.

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

## Accounts, storage and monitoring (later tasks)

- 2FA policy and step-up (tasks 1.7–1.9): require passkeys or TOTP for
  admins and maintainers; keep the number of admins small.
- Immutable destinations (tasks 2.9, 3.8): S3 Object Lock or no-delete
  credentials, more than one destination
  ([immutable-backups.md](immutable-backups.md)).
- Target allowlist (task 2.5): restrict `[targets]` in `runner.toml` to the
  exact hosts you back up ([target-allowlist.md](target-allowlist.md)).
- Audit (task 1.3): run `bekup audit verify` regularly ([audit.md](audit.md)).
- Monitoring (task 5.6): scrape `/metrics` on the internal port only; alert
  on missed backups and certificate expiry ([operations.md](../operations.md)).
