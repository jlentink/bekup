# Installation with Docker Compose

> **Status:** foundation from tasks 0.7/0.7b. The files and the hardening
> described here exist, and `migrate`, `certs init` and `keys generate`
> work. `serve` (task 1.1), `runner` (task 2.4) and `healthcheck` (D-080,
> tasks 1.1/2.4) are still stubs, so the stack does not run end to end yet
> (re-verified in task 1.11). TLS for the UI comes from the opt-in
> reverse-proxy profiles `traefik` / `nginx`
> ([reverse-proxy.md](reverse-proxy.md)); the dev stack works without a
> proxy over plain HTTP on `127.0.0.1:8080`.

bekup ships three files at the repository root:

| File | Purpose |
|---|---|
| `Dockerfile` | One image for every component: web build → static Go build → distroless `nonroot`. Plus a `dev` target (air hot reload) used only by the dev override. |
| `docker-compose.yml` | Production example: `certs-init`, `migrate`, `serve`, `runner`, `postgres`, plus the reverse-proxy profiles `traefik` and `nginx`. |
| `docker-compose.dev.yml` | Development override: hot reload, sample databases, destinations, SSH bastion, mailpit. |

## Architecture in compose

```
traefik | nginx ──frontend──> serve ──backend──> postgres
                                     │ └──egress──> S3 / SFTP / SMTP / webhooks (SSRF-guarded)
                                     └──runner-ctl (mTLS gRPC)──> runner ──docker.sock──> dockerd
                                                                    └──targets──> target databases
```

| Network | Internal | Members | Why |
|---|---|---|---|
| `frontend` | yes | reverse proxy (fixed `10.213.250.5`), `serve` (fixed `10.213.250.10`) | the only way to reach the UI; fixed proxy address for `trusted_proxies` |
| `edge` | no | reverse proxy, `certbot` | published ports 80/443 and outbound ACME (a published port needs a non-internal network) |
| `backend` | yes | `serve`, `migrate`, `postgres` | the app database is unreachable from everything else |
| `runner-ctl` | yes | `serve`, `runner` | mTLS gRPC only; fixed subnet so the runner can bind to it |
| `targets` | no | `runner` | dump traffic to target databases and SSH bastions; the runner's target allowlist decides what is reachable |
| `egress` | no | `serve` | uploads to destinations, SMTP and webhooks, every dial checked by the SSRF guard |

`serve` can never reach a target database or Docker, and the runner can never
reach the app database. `serve`'s port is **not published**: only the
reverse proxy on `frontend` reaches it (D-017).

The two fixed subnets (`frontend` 10.213.250.0/24, `runner-ctl`
10.213.251.0/24) lie outside Docker's default address pools so that they
don't collide with networks Docker allocates automatically. If they clash with
your LAN, set `FRONTEND_SUBNET`, `SERVE_FRONTEND_IP`, `PROXY_FRONTEND_IP`,
`RUNNER_CTL_SUBNET` and `RUNNER_CTL_IP` (in your shell or a `.env` next to the compose file; these are
compose variables, not bekup configuration keys) and update the TOML files to
match.

## Hardening of the bekup containers

Every bekup service gets the same baseline:

| Setting | Effect |
|---|---|
| distroless `static-debian12:nonroot`, `user: 65532:65532` | no shell, no package manager, never root; the binary is root-owned and read-only |
| `read_only: true` + `tmpfs: /tmp` (`noexec,nosuid,nodev`) | nothing can be dropped or persisted outside the declared volumes |
| `cap_drop: [ALL]` | no Linux capabilities at all |
| `security_opt: no-new-privileges:true` | no privilege gain through setuid binaries |
| resource limits (`cpus`, `memory`, `pids`) | a runaway process cannot starve the host |
| bounded `json-file` logs | logs cannot fill the disk |
| `restart: unless-stopped` | long-running services come back after a crash or reboot |
| exec-form healthchecks (`bekup healthcheck serve|runner`) | work without a shell (subcommand planned, see D-080) |

The app database (`postgres:17-alpine`) runs as UID 70 directly, with a
read-only root filesystem, no capabilities, SCRAM-SHA-256 for both local and
TCP logins and data checksums. Its init script
(`deploy/postgres/init/10-bekup-roles.sh`) creates the database `bekup` owned
by `bekup_owner` and the runtime role `bekup_app` (CONNECT only; table grants
come from the migrations, task 0.4). Passwords are read from secret files and
never appear in argv, the environment or the logs. The script also puts
`pg_hba.conf` rules first that reject the bootstrap superuser on every TCP
connection, so it can only log in over the container-local socket (only on
a new data volume; see [upgrades.md](upgrades.md#notes-for-existing-installations)).

### Migrations

`migrate` is a one-shot container that applies the migrations as
`bekup_owner` and exits; `serve` starts only after it succeeds. Only `migrate`
receives the owner credentials, so the long-running `serve` (role
`bekup_app`) can never alter the schema or the audit-log protections (D-054,
D-078).

### The runner and the Docker socket

The runner is the only container with `/var/run/docker.sock` (D-003). Access
to the socket is equivalent to root on the host; the runner compensates with a
small typed mTLS API, an image allowlist and a fixed hardened container spec.
Mounting the socket `:ro` does **not** limit the Docker API. For additional
isolation see [rootless-docker.md](rootless-docker.md).

The runner joins the socket's group through `group_add`. Set `DOCKER_GID` to
the socket's group ID:

```sh
stat -c %g /var/run/docker.sock        # Linux; typically 999 or 998
export DOCKER_GID=$(stat -c %g /var/run/docker.sock)
```

On Docker Desktop (macOS/Windows) the socket is `root:root` inside the VM, so
use `DOCKER_GID=0`.

Do not set `hostname:` on the runner: it finds its own container through the
default hostname to join per-run networks. If that fails, set
`[docker] self_container` in `runner.toml`.

Bind the runner's listener to its `runner-ctl` address
(`[listen] address = "10.213.251.10:7443"`) and `serve` to its `frontend`
address (`[server] listen = "10.213.250.10:8080"`), so neither port is
reachable from the other networks, including the runner's per-run networks.

## Configuration files

`bekup.toml` (serve, migrate) and `runner.toml` (runner) are mounted
read-only at `/etc/bekup/` as compose `configs`. Create them next to the
compose file from `bekup.example.toml` / `runner.example.toml` (see
[configuration.md](../configuration.md)). They contain no secrets.

### File ownership and permissions

Compose bind-mounts `configs` and file-based `secrets` with their **host**
ownership and mode. On Linux hosts, bekup's file safety checks (D-041) only
read a config or secret file that is:

- owned by **root** or by the **container UID 65532** (the user bekup runs
  as; the app Postgres reads its secrets as UID 70);
- **not group- or world-writable**;
- for secret files additionally **not world-readable**.

Otherwise bekup refuses to start, so a file that another local user could
edit can never become bekup's configuration. Recommended:

```sh
sudo chown root:root bekup.toml runner.toml && sudo chmod 0644 bekup.toml runner.toml
```

The secret files follow in [Secrets](#secrets). Docker Desktop
(macOS/Windows) maps ownership itself; files owned by your user work there.
For the dev stack, `sudo deploy/dev/gen-dev-keys.sh --chown` applies the
same rules to the dev secrets and the dev TOML files.

## Secrets

Secrets are file-based Docker secrets from `./secrets/` (gitignored), mounted
at `/run/secrets/<name>`. They never appear in environment variables, argv or
the TOML files (D-019). Each service only receives what it needs:

| Secret file | Used by | Content |
|---|---|---|
| `secrets/bekup_master_key` | serve | master keyring (`bekup keys generate`, task 0.5) |
| `secrets/bekup_database_url` | serve | `postgres://bekup_app:…@postgres:5432/bekup` |
| `secrets/bekup_database_owner_url` | migrate | `postgres://bekup_owner:…@postgres:5432/bekup` |
| `secrets/postgres_password` | postgres | bootstrap superuser password |
| `secrets/bekup_owner_password`, `secrets/bekup_app_password` | postgres (init only) | role passwords (must match the URLs) |
| `secrets/certs/serve/{serve.crt,serve.key,ca.crt}` | serve | client certificate + CA bundle (as `bekup_client.{crt,key}`, `bekup_ca.crt`) |
| `secrets/certs/runner/{runner.crt,runner.key,ca.crt}` | runner | server certificate + CA bundle (as `runner.{crt,key}`, `bekup_ca.crt`) |

The CA private key (`secrets/certs/ca/ca.key`, age-encrypted) is only ever
mounted into the one-shot `certs-init` container; no long-running service sees
it. Move `secrets/certs/ca/` offline after `certs-init` and bring it back only
for rotation (see [mTLS certificates](../security/mtls-certs.md)).

Compose bind-mounts file secrets with their host ownership, so on Linux each
file must be readable by the UID of the container that uses it, without being
world-readable (bekup rejects world-readable secret files):

```sh
sudo chown 65532:65532 secrets/bekup_*
sudo chown -R 65532:65532 secrets/certs/serve secrets/certs/runner
sudo chown 70:70 secrets/postgres_password secrets/bekup_owner_password secrets/bekup_app_password
sudo chmod 0400 secrets/bekup_* secrets/postgres_password
sudo chmod 0711 secrets secrets/certs
```

Docker Desktop maps ownership itself; `0600` files owned by your user work
there.

The URLs in `bekup_database_url` / `bekup_database_owner_url` currently use the
internal `backend` network without TLS. See the open question in D-078 about
TLS for the app database.

## First start (target flow)

1. Create `bekup.toml` and `runner.toml` (see above).
2. Create the database passwords and URLs in `secrets/` (32 random bytes
   each, e.g. `head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '='`),
   and the master key with `just keygen` (task 0.5).
3. Generate the mTLS certificates once (task 0.6). `secrets/certs` must exist
   and be writable by UID 65532; you are prompted for a CA key passphrase:
   ```sh
   mkdir -p -m 0700 secrets/certs && sudo chown 65532:65532 secrets/certs   # Linux
   docker compose --profile setup run --rm certs-init
   ```
   Then move `secrets/certs/ca/` offline (see
   [mTLS certificates](../security/mtls-certs.md)).
4. Set `DOCKER_GID` and start the stack:
   ```sh
   docker compose up -d
   ```
5. Start it with a reverse-proxy profile to reach the UI over HTTPS (see
   [below](#reverse-proxy-profiles)):
   ```sh
   BEKUP_DOMAIN=backup.example.com ACME_EMAIL=ops@example.com \
     docker compose --profile traefik up -d      # or --profile nginx
   ```

Pin `BEKUP_IMAGE` (a compose variable) to a released, digest-pinned
`ghcr.io/jlentink/bekup` reference; the default `bekup:dev` is a locally
built image (`just docker`).

## Reverse-proxy profiles

TLS for the UI is terminated by one of two compose profiles. Enable exactly
one of them; full details are in [reverse-proxy.md](reverse-proxy.md).

| Profile | Services | Notes |
|---|---|---|
| `traefik` | `traefik-init`, `traefik` | Traefik v3, Let's Encrypt (HTTP-01), file provider only |
| `nginx` | `nginx-init`, `nginx`, `certbot` | unprivileged nginx, certbot webroot companion |

- Neither profile mounts the Docker socket (D-093, D-094).
- The proxy publishes 80/443 (mapped to unprivileged ports 8080/8443 inside
  the container) on the `edge` network. It reaches `serve` over `frontend`
  from the fixed address `PROXY_FRONTEND_IP` (default `10.213.250.5`).
  `[server] trusted_proxies` must be exactly `["10.213.250.5/32"]`.
- Proxy-only compose variables: `BEKUP_DOMAIN`, `ACME_EMAIL`,
  `ACME_CA_SERVER`, `PROXY_TLS_MODE`. These are not bekup configuration keys.
- The proxies run with the same hardening as bekup: UID 65532, read-only
  root, no capabilities, `no-new-privileges`, resource limits. The one-shot
  `*-init` services only `chown` the certificate volumes and have no network.
- Dev: `deploy/dev/gen-dev-tls.sh` creates a mkcert (or self-signed)
  certificate for `bekup.localhost`. The dev override switches both profiles
  to static-certificate mode on `127.0.0.1:8443` / `:8000` and disables
  certbot.

## Image pinning and builds

Every image in the Dockerfile and both compose files is pinned by tag **and**
multi-arch digest; Renovate (`docker:pinDigests`) keeps the digests current
(D-061, D-065). Never drop a digest when bumping a tag.

```sh
docker build --build-arg VERSION=$(cat VERSION) -t bekup:dev .   # or: just docker
docker build --build-arg WEB_BUILD=skip -t bekup:dev .           # Go only, placeholder UI
```

The web stage builds `web/` with pnpm (`--frozen-lockfile`) when
`web/package.json` exists, and otherwise embeds a placeholder page, so the
image always builds. The version comes from `--build-arg VERSION` or the
`VERSION` file and is validated as SemVer before it is passed to `-ldflags`.

## Development stack

```sh
deploy/dev/gen-dev-keys.sh          # once; add `sudo … --chown` on Linux
DOCKER_GID=0 just dev               # Docker Desktop; on Linux use the socket GID
```

**No proxy needed.** The dev override publishes `serve` directly on
`http://127.0.0.1:8080` (loopback only, `base_url =
"http://localhost:8080"`). Plain HTTP is accepted in dev mode, with a WARN
log line at every startup and a permanent red "Connection is not encrypted
(no HTTPS)" banner in the UI (D-095; tasks 1.1 and 6.1). The `traefik` and
`nginx` profiles are **opt-in**: add `--profile traefik` (or `nginx`) to
test HTTPS with a local certificate on `https://bekup.localhost:8443`
([reverse-proxy.md](reverse-proxy.md#development-local-tls)).

`docker-compose.dev.yml` keeps the production hardening of the bekup services
and adds:

| Service | Purpose | Host access |
|---|---|---|
| `serve` | air hot reload (`dev` Dockerfile target), dev mode; only the Go sources (`go.mod`, `go.sum`, `.air.toml`, `cmd/`, `internal/`, `web/embed.go`, `web/dist/`) are mounted, **read-only**, so it cannot read `secrets/`, `certs/`, local TOML or `.env` files | http://127.0.0.1:8080 (plain HTTP, no proxy) |
| `web` (profile `web`) | Vite dev server, proxies the API to `serve` | http://127.0.0.1:5173 |
| `pg15`, `mysql`, `mariadb` | sample targets (PG 15, MySQL 8.4, MariaDB 11.4) seeded from `deploy/dev/seed/` (databases `shop` and `analytics`) | none (runner only) |
| `pg15-tunneled` | PG target reachable **only** through the bastion (`tunnel-only` network) | none |
| `bastion` | OpenSSH, user `tunnel`, key-only, port 2222, TCP forwarding enabled by a pinned init script (no runtime `DOCKER_MODS` download) | none |
| `minio` + `minio-init` | S3 with buckets `bekup-dev` (versioned) and `bekup-dev-locked` (Object Lock, GOVERNANCE 1 day) | console http://127.0.0.1:9001 |
| `sftp` | SFTP destination, user `bekup`, key-only, writable `upload/`, stable host key | none |
| `mailpit` | SMTP sink | http://127.0.0.1:8025 |
| `traefik` / `nginx` (profile) | HTTPS with a local certificate (`deploy/dev/gen-dev-tls.sh`), no ACME | https://bekup.localhost:8443 |

`deploy/dev/gen-dev-keys.sh` writes every dev password, the master key
placeholder, a dev PKI from `bekup certs init` (runner + serve, unencrypted
dev CA key, 90-day leaves), the SSH client key
(`secrets/dev/bekup_ssh_ed25519`, accepted by the bastion and the SFTP server)
and the SFTP host key. Files are `0600`, directories `0700`, and existing files
are kept unless you pass `--force`. The dev configs are
`deploy/dev/bekup.dev.toml` and `deploy/dev/runner.dev.toml`. On Linux,
`sudo deploy/dev/gen-dev-keys.sh --chown` gives every file the owner its
container needs (65532 bekup/minio, 70 postgres, 999 mysql/mariadb) and
makes the dev TOML files `root:root 0644`.

`minio-init` passes the MinIO credentials to `mc` only through the
`MC_HOST_dev` environment variable of its short-lived shell, read from the
secret files; they never appear on a command line or in `docker inspect`.

The upstream MinIO images are no longer published on Docker Hub or quay.io;
the dev stack uses Chainguard's source-built `cgr.dev/chainguard/minio`
(D-081). The sample targets don't use TLS yet; certificate-pinning tests get
TLS-enabled targets in task 2.7a.

Stop and wipe the dev stack with:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v --remove-orphans
```
