# Installation with Docker Compose

> **Status:** verified end to end in task 1.11: `migrate` (as
> `bekup_owner`, TLS 1.3 + SCRAM channel binding), `serve` (healthy,
> unpublished, bound to its `frontend` address only), the `admin` one-shot,
> and both reverse-proxy profiles `traefik` / `nginx` (HTTPS, redirect,
> HSTS, CSP, TLS 1.2/1.3 AEAD only; since task 2.13 subscriptions are
> unbuffered Server-Sent Events instead of WebSocket upgrades). Since task 2.4
> the `runner` container starts its mTLS gRPC listener and turns healthy
> (`bekup healthcheck runner` on its local health socket, D-125); since
> tasks 2.5–2.7a it serves its full RPC set (probe, certificate and
> host-key capture, dump, verify, restore) with per-run isolated networks.
> TLS for the UI comes from the opt-in reverse-proxy profiles
> ([reverse-proxy.md](reverse-proxy.md)); the dev stack works without a
> proxy over plain HTTP on `127.0.0.1:8080`.

bekup ships three files at the repository root:

| File | Purpose |
|---|---|
| `Dockerfile` | One image for every component: web build → static Go build → distroless `nonroot`. Plus a `dev` target (air hot reload) used only by the dev override. |
| `docker-compose.yml` | Production example: `certs-init`, `migrate`, `serve`, `runner`, `postgres`, plus the reverse-proxy profiles `traefik` and `nginx`. |
| `docker-compose.dev.yml` | Development override: by default only the app Postgres (published on loopback), mailpit and adminer for a natively running bekup; profile `full` adds hot reload, the runner, sample databases, destinations and an SSH bastion (D-114). |

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
| exec-form healthchecks (`bekup healthcheck serve|runner`) | work without a shell; `serve` probes `/healthz` (task 1.1, D-080, D-102), `runner` queries the standard gRPC health service on its container-local unix socket `listen.health_socket` (task 2.4, D-125) |

The app database (`postgres:17-alpine`) runs as UID 70 directly, with a
read-only root filesystem, no capabilities, SCRAM-SHA-256 for both local and
TCP logins and data checksums. Its init script
(`deploy/postgres/init/10-bekup-roles.sh`) creates the database `bekup` owned
by `bekup_owner` and the runtime role `bekup_app` (CONNECT only; table grants
come from the migrations, task 0.4). Passwords are read from secret files and
never appear in argv, the environment or the logs. The script also puts
`pg_hba.conf` rules first that reject the bootstrap superuser on every TCP
connection, so it can only log in over the container-local socket, and
rules that reject every non-TLS TCP login of `bekup_owner` / `bekup_app`
(only on a new data volume; see [upgrades.md](upgrades.md#notes-for-existing-installations)).

The app database speaks **TLS only** to bekup (D-100): `postgres` starts
through `deploy/postgres/entrypoint.sh`, which enables `ssl=on` with TLS ≥ 1.2
and the `postgres` certificate from `certs-init`; `serve` and `migrate`
verify it with `[database] tls_mode = "verify-full"` against
`secrets/certs/postgres/ca.crt`. Details, weakened modes and rotation:
[App DB TLS](../security/app-db-tls.md).

### Migrations

`migrate` is a one-shot container that applies the migrations as
`bekup_owner` and exits; `serve` starts only after it succeeds. Only `migrate`
receives the owner credentials, so the long-running `serve` (role
`bekup_app`) can never alter the schema or the audit-log protections (D-054,
D-078).

### Account administration (`admin`)

`bekup admin` (first admin, password resets, 2FA resets, unlock, disable;
D-069, D-113) runs in the one-shot service `admin` (profile `admin`,
started on demand by `docker compose run`):

```sh
docker compose run --rm admin create --email you@example.com --display-name "Your Name"
docker compose run --rm admin reset-password --email you@example.com
```

The password is prompted twice on the terminal. To use `--password-file`
instead, mount the file (owner-only permissions) into the container:

```sh
docker compose run --rm -v "$PWD/admin-pw:/run/admin-pw:ro" admin \
  create --email you@example.com --display-name "Your Name" --password-file /run/admin-pw
```

`admin` connects as `bekup_app` like `serve` but gets only the `backend`
network, the database URL and the DB CA (no master key, no mTLS
certificate, no ports). It exists because `docker compose run serve ...`
cannot start while `serve` runs: the new container would claim `serve`'s
fixed `frontend` address ("Address already in use").

In the development stack use `just admin-create <email> <name>` instead
(the native binary against the dev Postgres). The containerised service
also works there (`docker compose -f docker-compose.yml -f
docker-compose.dev.yml --profile admin run --rm admin create ...`): the dev
override makes it wait only for a healthy `postgres`, because `migrate` is
in the `full` profile and `just dev` applies the migrations natively.

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

Do not set `hostname:` on the runner: it finds its own container through
`/proc/self/mountinfo` or the default hostname to join per-run networks, and
accepts it only if the container carries one of the runner's own addresses.
If that fails (the runner logs "runner executor not ready" and runs fail
with `UNAVAILABLE`), set `[docker] self_container` in `runner.toml`.

Every run gets its own internal `/28` network from `[docker]
run_subnet_pool` (default `10.214.0.0/16`, labels `bekup.managed=true`,
names `bekup-run-<run id>`). Pick a range that your Docker networks and
databases do not use. Leftovers (after a crash) are removed at startup and
every five minutes. The runner requires **Docker Engine 26.0.0 or newer**
and refuses older daemons at startup (they forwarded DNS queries from
internal networks to the host's resolvers, CVE-2024-29018). Docker networks
on the same daemon (their subnets and gateways) are denied as targets unless
`[targets] allow_cidrs` lists a CIDR inside them
([target-allowlist.md](../security/target-allowlist.md)).

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
at `/run/secrets/<name>`. To keep them elsewhere (for example on an
encrypted volume outside the project directory), set the compose variable
`BEKUP_KEYS_DIR` (shell or `.env`; absolute, or relative to the compose
file); the paths below are then relative to it, including the `certs/`
directory that `certs-init` writes. They never appear in environment variables, argv or
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
| `secrets/certs/postgres/{postgres.crt,postgres.key}` | postgres | app DB TLS server certificate + key (as `postgres.{crt,key}`) |
| `secrets/certs/postgres/ca.crt` | postgres, serve, migrate | CA bundle (as `postgres_ca.crt` / `bekup_db_ca.crt`, the `tls_ca_file` default) |

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
sudo chown -R 70:70 secrets/certs/postgres   # key 0600; ca.crt stays 0644 for serve/migrate
sudo chmod 0400 secrets/bekup_* secrets/postgres_password
sudo chmod 0711 secrets secrets/certs
```

Docker Desktop maps ownership itself; `0600` files owned by your user work
there.

The URLs in `bekup_database_url` / `bekup_database_owner_url` need no
`sslmode`: TLS comes from `[database] tls_mode` (default `verify-full`), and
an `sslmode` stricter than it is rejected (see
[App DB TLS](../security/app-db-tls.md)).

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

6. Create the first admin (D-113) with the one-shot `admin` service (see
   [Account administration](#account-administration-admin)), then sign in.

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
just dev                            # infra only; bekup runs natively (`just run`)
DOCKER_GID=0 just dev-full          # everything in containers; on Linux use the socket GID
```

On Linux run `sudo deploy/dev/gen-dev-keys.sh --chown` once first.

**Default profile (D-114).** Without a profile the dev override starts only
`postgres`, `mailpit` and `adminer`, for a bekup that runs natively on the
host ([development.md](../development.md#local-development)):

| Service | Purpose | Host access |
|---|---|---|
| `postgres` | the app DB, unchanged from production (TLS-only bekup roles, SCRAM with channel binding, socket-only superuser); DEV ONLY published on loopback through the extra non-internal `dev-host` network. The dev certificate also names `localhost` and `127.0.0.1` for `verify-full` from the host | `127.0.0.1:5432` (`BEKUP_DEV_PG_PORT`) |
| `mailpit` | SMTP sink | SMTP `127.0.0.1:1025`, UI http://127.0.0.1:8025 |
| `adminer` | DB browser, digest-pinned, read-only rootfs, no capabilities, `no-new-privileges`; login form restricted to the app DB (`deploy/dev/adminer/login-servers.php`); libpq with `verify-full` against the dev CA and required channel binding. Log in as `bekup_app` or `bekup_owner`, database `bekup` | http://127.0.0.1:8081 |

**Profile `full`.** The containerised stack below (`just dev-full`, or
`--profile full`). The proxy and `web` profiles build on it.

**No proxy needed.** The `full` profile publishes `serve` directly on
`http://127.0.0.1:8080` (loopback only, `base_url =
"http://localhost:8080"`). Plain HTTP is accepted in dev mode, with a WARN
log line at every startup and a permanent red "Connection is not encrypted
(no HTTPS)" banner in the UI (D-095; tasks 1.1 and 6.1). The `traefik` and
`nginx` profiles are **opt-in**: add `--profile traefik` (or `nginx`) to
test HTTPS with a local certificate on `https://bekup.localhost:8443`
([reverse-proxy.md](reverse-proxy.md#development-local-tls)).

`docker-compose.dev.yml` keeps the production hardening of the bekup services
and adds in the `full` profile:

| Service | Purpose | Host access |
|---|---|---|
| `serve` | air hot reload (`dev` Dockerfile target), dev mode; only the Go sources (`go.mod`, `go.sum`, `.air.toml`, `cmd/`, `internal/`, `web/embed.go`, `web/dist/`) are mounted, **read-only**, so it cannot read `secrets/`, `certs/`, local TOML or `.env` files | http://127.0.0.1:8080 (plain HTTP, no proxy) |
| `web` (profile `web`) | Vite dev server, proxies the API to `serve` | http://127.0.0.1:5173 |
| `pg15`, `mysql`, `mariadb` | sample targets (PG 15, MySQL 8.4, MariaDB 11.4) seeded from `deploy/dev/seed/` (databases `shop` and `analytics`) | none (runner only) |
| `pg15-tunneled` | PG target reachable **only** through the bastion (`tunnel-only` network) | none |
| `bastion` | OpenSSH, user `tunnel`, key-only, port 2222, TCP forwarding enabled by a pinned init script (no runtime `DOCKER_MODS` download) | none |
| `minio` + `minio-init` | S3 with buckets `bekup-dev` (versioned) and `bekup-dev-locked` (Object Lock, GOVERNANCE 1 day) | console http://127.0.0.1:9001 |
| `sftp` | SFTP destination, user `bekup`, key-only, writable `upload/`, stable host key | none |
| `traefik` / `nginx` (profile) | HTTPS with a local certificate (`deploy/dev/gen-dev-tls.sh`), no ACME | https://bekup.localhost:8443 |

`deploy/dev/gen-dev-keys.sh` writes every dev password, the master key
placeholder, a dev PKI from `bekup certs init` (runner + serve, unencrypted
dev CA key, 90-day leaves), the SSH client key
(`secrets/dev/bekup_ssh_ed25519`, accepted by the bastion and the SFTP server)
and the SFTP host key, plus `secrets/local/` for native bekup processes (role
URLs via `127.0.0.1`, copies of the master keyring and DB CA). Files are
`0600`, directories `0700`, and existing files are kept unless you pass
`--force`. `BEKUP_DEV_KEYS_DIR` moves `./secrets` (also for the compose dev
override). The dev configs are
`deploy/dev/bekup.dev.toml` and `deploy/dev/runner.dev.toml`. On Linux,
`sudo deploy/dev/gen-dev-keys.sh --chown` gives every file the owner its
container needs (65532 bekup/minio, 70 postgres, 999 mysql/mariadb) and
makes the dev TOML files `root:root 0644`; `secrets/local/` stays owned by
the user who ran `sudo`.

`minio-init` passes the MinIO credentials to `mc` only through the
`MC_HOST_dev` environment variable of its short-lived shell, read from the
secret files; they never appear on a command line or in `docker inspect`.

The upstream MinIO images are no longer published on Docker Hub or quay.io;
the dev stack uses Chainguard's source-built `cgr.dev/chainguard/minio`
(D-081). The sample targets don't use TLS yet; certificate-pinning tests get
TLS-enabled targets in task 2.7a.

Stop the dev stack with `just dev-down`; wipe it with `just db-reset` or:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml \
  --profile full --profile web --profile traefik --profile nginx down -v --remove-orphans
```
