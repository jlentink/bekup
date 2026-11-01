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

A production install uses one compose file, always named `compose.yaml` in
the deployment directory (local changes go into `compose.override.yaml`).
**The easiest way is the setup wizard of the image**: one `docker run`
command asks a few questions and writes everything, `compose.yaml`
included ([Install with the setup wizard](#install-with-the-setup-wizard)).

Each release also publishes four variants of the compose file, pinned to
the release image by digest and attested (D-205, D-208, D-215): two
reverse-proxy forms, each with bekup's bundled database or with your own
PostgreSQL ([external database](#your-own-postgresql-external-database-files)):

| Release asset | Variant | Reverse proxy | Database |
|---|---|---|---|
| `compose.yaml` | `traefik` | bekup's own Traefik (ports 80/443, Let's Encrypt or a static certificate) | bundled |
| `compose.own-proxy.yaml` | `own-proxy` | your reverse proxy on this Docker host (Nginx Proxy Manager, Traefik, Caddy, nginx, ...): `serve` joins its Docker network | bundled |
| `compose.external-db.yaml` | `traefik` | bekup's own Traefik | your PostgreSQL 15-17 |
| `compose.own-proxy.external-db.yaml` | `own-proxy` | your reverse proxy on this Docker host | your PostgreSQL |

All four are rendered from the repository's `docker-compose.yml` (overlays
in `internal/setup/variants`, never copies; `bekup setup compose --variant
<v> [--database external]` prints one, `just compose-examples` writes them
to `dist/compose/`). What only some deployments need (the Traefik labels on
`serve`, the link network's fixed address, a database container's network)
is never part of these files: setup writes it into `compose.override.yaml`
([below](#the-override-file-and-the-hardening-lint)). The variants
`traefik-external` and `npm` of earlier versions are retired
([upgrades.md](upgrades.md#retired-variants-traefik-external-and-npm)).
The same four files are **committed** in
[`deploy/compose/`](../../deploy/compose/) (D-205 amendment 3), generated
by `just gen` and never edited by hand. They differ from the release assets
only in the `x-bekup-image` line: instead of the pinned literal it reads
`${BEKUP_IMAGE:?...}`, so the image comes from `BEKUP_IMAGE` (a
digest-pinned reference that setup checks and writes to `.env`). Use them
before a release exists, for your own image, or when you prefer to pin the
image yourself; the release assets are the attested choice once releases
exist.
Save one as `compose.yaml` and let `bekup setup` do the rest
([Install with a compose file](#install-with-bekup-setup-container-only)).
The setup-first
alternative lets `setup` write the file itself, including the `full`
variant with nginx or your own proxy on `frontend` (the advanced flag-only
path for a proxy that is not a container on this Docker host;
[Alternative: setup-first](#alternative-setup-first)).

The repository root holds the sources:

| File | Purpose |
|---|---|
| `Dockerfile` | One image for every component: web build → static Go build → distroless `nonroot`. Plus a `dev` target (air hot reload) used only by the dev override. |
| `docker-compose.yml` | The source of every variant (the `full` variant): `setup`, `certs-init`, `migrate`, `admin`, `serve`, `runner`, `postgres`, plus the reverse-proxy profiles `traefik` and `nginx`. The bekup image appears only in its `x-bekup-image` line. |
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
| `backend` | yes | `serve`, `migrate`, `admin`, `postgres` | the app database is unreachable from everything else |
| `runner-ctl` | yes | `serve`, `runner` | mTLS gRPC only; fixed subnet so the runner can bind to it |
| `targets` | no | `runner` | dump traffic to target databases and SSH bastions; the runner's target allowlist decides what is reachable |
| `egress` | no | `serve` | uploads to destinations, SMTP and webhooks, every dial checked by the SSRF guard |
| `metrics` | yes | `serve` (fixed `10.213.252.10`), your Prometheus | `/metrics` only, never published |

This is the `traefik` variant (and `full`). The `own-proxy` variant has no
bundled proxy, no `frontend` or `edge` network and publishes no ports;
`serve` joins your proxy's Docker network instead (D-215,
[reverse-proxy.md](reverse-proxy.md#own-reverse-proxy)):

| Network | Internal | Members | Why |
|---|---|---|---|
| `proxy` (external: your proxy's network, `BEKUP_PROXY_NETWORK`) | yours | your proxy, `serve` (alias `bekup-serve`, no fixed address), whatever else you run there | the only way to reach the UI: your proxy forwards to `http://bekup-serve:8080`; `trusted_proxy_hosts` names your proxy's container |
| `backend` | yes | `serve`, `migrate`, `admin`, `postgres` | as above |
| `runner-ctl` | yes | `serve` (`10.213.251.x`), `runner` (fixed `10.213.251.10`) | as above |
| `targets` | no | `runner` | as above |
| `egress` | no | `serve` | as above |
| `metrics` | yes | `serve` (fixed `10.213.252.10`) | as above |

Only `serve` joins `proxy`; the lint refuses every other service of the
project there. Other containers on your network can reach `serve:8080`
directly but cannot forge client addresses
([residual risk](reverse-proxy.md#trust-by-container-name)). The stricter
alternative is link mode (`--proxy-network-mode link`): `proxy` is then a
dedicated internal `/28`, `bekup-proxy`, that you create once, with fixed
addresses for your proxy (`10.213.253.5`) and `serve` (`10.213.253.10`)
([reverse-proxy.md](reverse-proxy.md#advanced-dedicated-link-network)).

With a [database container](external-database.md#database-container-on-this-docker-host)
on this Docker host, `migrate`, `serve` and `admin` also join its network
as the external network `database` (`BEKUP_DB_NETWORK`), and nothing else
of the project may.

`serve` can never reach a target database or Docker, and the runner can never
reach the app database. `serve`'s port is **not published**: only the
reverse proxy on `frontend` (own-proxy: on your proxy's network) reaches it
(D-017).

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
`migrate`, `serve` and `runner` receive `BEKUP_SETUP_VERSION` and
`BEKUP_IMAGE_MODE` from `.env` (written by setup): `migrate` (also for
`db bootstrap`), `serve` and `runner` refuse to start on another version
than setup prepared the directory for
([the version guard](upgrades.md#the-version-guard), D-212).

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
network (and a database container's network, D-215), the database URL and
the DB CA (no master key, no mTLS
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
The own-proxy variant is the exception: Docker assigns `serve`'s address on
your proxy's network, so setup writes `listen = "0.0.0.0:8080"` with
`allow_wildcard_listen = true` there; `serve` then accepts connections only
on its own address on your proxy's network (and loopback for its
healthcheck) and closes every other one, so its other networks still cannot
reach port 8080 (D-215,
[reverse-proxy.md](reverse-proxy.md#set-it-up)).
Likewise bind the Prometheus endpoint to serve's address on the internal
`metrics` network (`[metrics] listen = "10.213.252.10:9464"`); it is never
published (see [Operations: metrics](../operations.md#metrics)).

## Install with the setup wizard

The main path: in an empty directory, run the wizard of the image you
deploy, then start the stack. `ghcr.io/jlentink/bekup:latest` is the
simplest start (it exists once the first release is published); for strict
change control use the image pinned by digest, its tag naming the version
(verify it first, [Verifying release images](#verifying-release-images)):

```sh
IMAGE=ghcr.io/jlentink/bekup:latest   # or ghcr.io/jlentink/bekup:<version>@sha256:<digest from the release notes>
sudo docker pull "$IMAGE"
sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
sudo docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -e BEKUP_IMAGE="$IMAGE" -e DOCKER_GID="$(stat -c %g /var/run/docker.sock)" \
  -v "$PWD":/deploy "$IMAGE" setup
# your own PostgreSQL only: the command the wizard printed, e.g.
sudo docker compose run --rm migrate db bootstrap --admin-user <admin>
sudo docker compose up -d
sudo docker compose run --rm admin create --email you@example.com --display-name "You"
```

The flags give the wizard the same hardening as the compose `setup`
service: root with only `CAP_CHOWN`, no network, a read-only root, and
`--log-driver none`, so a generated passphrase never reaches Docker's log
files. `BEKUP_IMAGE` names the image (a container cannot know its own
digest; without it the wizard asks), `DOCKER_GID` the Docker socket's group.

One question per screen; **Esc goes back, Ctrl+C quits**, and nothing is
written before the summary:

0. only for a tag such as `:latest`: one screen explains following the tag
   (the default: `compose.yaml` names the tag, `docker compose pull`
   fetches new releases, an explicit weakening, D-212) versus pinning a
   digest (it then asks for `repo:tag@sha256:<digest>`); with a digest
   there is no question
   ([Following `:latest`](upgrades.md#following-latest));
1. the reverse proxy: bekup's own Traefik, your own reverse proxy on this
   Docker host (the Docker network it is on and its container name, then
   whether to add Traefik Docker labels: default no; with yes its HTTPS
   entrypoint and certificate resolver, defaults `websecure` and
   `letsencrypt`). A proxy on another machine is not a wizard choice
   (D-215): use the advanced flags of the
   [setup-first install](#alternative-setup-first) (`--variant full
   --proxy none --trusted-proxy <cidr>`: run a small proxy on this host,
   or route to bekup's `frontend` network yourself);
2. the public host name (and, for bekup's Traefik, the Let's Encrypt
   e-mail and whether to use the staging CA);
3. the database: bundled, or your existing PostgreSQL: a container on this
   Docker host (its Docker network and container name) or another server
   (host name), port, database name, the TLS mode (verify with a CA file,
   the default: put the file into the directory first; verify with the
   system CA bundle; or, after a warning and an explicit confirmation, the
   weakened `require` or `disable`), an optional TLS server name and how the
   roles come to exist
   ([below](#your-own-postgresql-external-database-files));
4. the master encryption key: generate a new one, or import your existing
   key file (reinstall; put the file into the directory; an optional
   key-check value is compared);
5. the internal CA: generate a new one (the passphrase generated and shown
   once, or entered twice), or use your existing CA mounted at
   `secrets/certs/ca` (its passphrase is checked at once);
6. for the bundled database, consent to generate its TLS certificate and
   passwords;
7. the Docker socket group and, optionally, the target database networks
   and host names (`runner.toml [targets]`);
8. a summary of the answers and the final confirmation.

Then it shows a generated CA passphrase once on the alternate screen (store
it offline with the CA media), writes the deployment with exactly the code
of `bekup setup init` (the same options as those flags) and prints the
report (certificate fingerprints, the master key ID and the next commands)
followed by the equivalent `bekup setup init --non-interactive ...` command
line. That line never holds a secret: entered database passwords appear as
the placeholders `PASSWORD_FILE` / `OWNER_PASSWORD_FILE` (files you would
put into the directory) and an entered or existing CA passphrase as
`--ca-passphrase-stdin`; a note below the line says so.

- **Existing `compose.yaml`:** when the directory already holds a compose
  file (compose-first), the wizard keeps it and takes the variant, the
  database mode and the image from it.
- **Existing deployment:** in a directory with `bekup-setup.json`,
  `bekup setup` offers `setup check`, the [guided upgrade](upgrades.md) and
  [certificate renewal](../security/mtls-certs.md#renewal-with-bekup-setup-renew).
- **Colors and plain text:** `NO_COLOR` or `TERM=dumb` drop the colors of
  the terminal interface (it keeps the alternate screen for the
  passphrase). `--plain` switches to numbered choices and plain lines
  (`<` goes back) for screen readers and minimal terminals; it sends no
  escape sequences, so a generated passphrase stays in the scrollback (the
  choice says so: enter your own, or clear the scrollback afterwards).
  Nothing relies on color in either interface.
- **No terminal:** without `-it`, or with stdout redirected to a pipe or a
  file, the wizard refuses (a generated passphrase is never written into
  one); use `bekup setup init --non-interactive` with flags (the line the
  wizard prints after writing is the equivalent).

## Install with `bekup setup` (container only)

A production host needs **only Docker and one compose file**: no source
checkout, no `just`, no Go. The compose file names the bekup image in its
single `x-bekup-image` line (the release asset: pinned by digest; the
committed file: `BEKUP_IMAGE`), and its `setup` service runs `bekup setup`
from exactly that image (compose-first, D-205).
Setup writes everything else from the files embedded in the image
(`deploy/**` for the variant, the example configs, `.env`, minimal
`bekup.toml` / `runner.toml`), generates every secret and certificate, and
hands each file to the container user that reads it. It **never writes
`compose.yaml`**.

With the committed file (works today, also for your own image):

```sh
sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
# compose.yaml (own Traefik) or compose.own-proxy.yaml (your own reverse proxy)
sudo curl -fsSLo compose.yaml https://raw.githubusercontent.com/jlentink/bekup/main/deploy/compose/compose.yaml
BEKUP_IMAGE=<registry>/bekup:<version>@sha256:<digest> \
  DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup
docker compose up -d
docker compose run --rm admin create --email you@example.com --display-name "You"
```

`BEKUP_IMAGE` must be pinned by digest and its tag must name the version of
the image (setup refuses anything else; `--allow-version-mismatch`
overrides the version check). Get the digest from the output of
`just publish` (your own image), from the release notes, or with
`docker image inspect --format '{{index .RepoDigests 0}}' <repo>:<tag>`
after a `docker pull` (that prints `<repo>@sha256:...`; put the tag back in
front of the `@`). Without `BEKUP_IMAGE` docker compose itself refuses to
start (`required variable BEKUP_IMAGE is missing a value`). Setup writes
`BEKUP_IMAGE=<the pinned reference>` into `.env`, so later commands
(`docker compose up -d`, `setup check`) need no shell variable; a
`BEKUP_IMAGE` set in the shell overrides `.env`, and `setup check` reports
that as an error. Pin the file to a commit or tag in the URL
(`.../bekup/<tag>/deploy/compose/...`) to get exactly the file of the image
you deploy; the file of a newer commit may need a newer image.

With the release asset (once releases exist; pinned and attested):

```sh
sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
sudo curl -fsSLo compose.yaml https://github.com/jlentink/bekup/releases/latest/download/compose.yaml
gh attestation verify compose.yaml --repo jlentink/bekup
DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup
docker compose up -d
docker compose run --rm admin create --email you@example.com --display-name "You"
```

For `compose.own-proxy.yaml` look up your proxy's Docker network and
container name first (`docker inspect <proxy container> --format '{{json
.NetworkSettings.Networks}}'`; [reverse-proxy.md](reverse-proxy.md#find-the-network-and-the-container-name));
`setup` itself runs without a network. For an `*.external-db.yaml` file put
your PostgreSQL's CA bundle into the directory first when you verify
against a CA file (the default); setup asks for the database
([below](#your-own-postgresql-external-database-files)).

**Verify the release file before running it.** `gh attestation verify` checks the
GitHub build provenance (made by the release workflow for a `vX.Y.Z` tag;
it needs a signed-in GitHub CLI, `gh auth login`). Without the GitHub CLI, download `compose.yaml.sigstore.json` from the same
release and use cosign v3:

```sh
cosign verify-blob --bundle compose.yaml.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/jlentink/bekup/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  compose.yaml
```

Every release also publishes `SHA256SUMS` over the four compose files
(with its own bundle and attestation). The release workflow renders the
files by running the published, scanned and signed image
(`setup compose --image <ref>`, no network, read-only, UID 65532) and checks
that each names exactly that image once and pins every other image. A
tampered compose file could run anything as root, so never skip the check;
the image it pins is verified like any release image
([Verifying release images](#verifying-release-images)).

The `setup` service is a one-shot root container with **only
`CAP_CHOWN`**, no network, a read-only root file system and **no logs**
(`logging: none`: the passphrase prompt and a generated passphrase never
reach Docker's log files). It belongs to the `setup` profile, so `docker
compose up` never starts it, and `setup check` requires it exactly as
shipped. The same service runs `upgrade` and `check`. Run the check with
`-f compose.yaml`, so that `compose.override.yaml` cannot change the
container that runs it (the check still reads and lints the override):
`docker compose -f compose.yaml run --rm setup check`.

**What setup asks** (on a terminal; `-T` and flags otherwise):

| Variant | Prompts |
|---|---|
| `traefik` | public host name, ACME e-mail, CA key passphrase (twice, no echo) |
| `own-proxy` | your proxy's Docker network and container name, Traefik labels (default no; then entrypoint and certificate resolver), public host name, CA key passphrase |
| any `*.external-db.yaml` | additionally: a container on this Docker host (its network and name) or another server (host), port, database name, the TLS mode and, for the default, the CA bundle file, roles (`bootstrap`, `existing-roles`, `dba-sql`, `single-account`), the account for single-account, role passwords for existing roles (twice, no echo) |

The Docker socket's group comes from `DOCKER_GID` (the compose file passes
it into the container), otherwise setup asks. Everything else has defaults
or flags: `--tls-mode static` (own certificate in `proxy-tls/`, see
[reverse-proxy.md](reverse-proxy.md)), `--acme-staging`, the
[own reverse proxy](reverse-proxy.md#own-reverse-proxy) flags
`--proxy-network` (the Docker network your proxy is on),
`--proxy-container` (its container name, trusted by name),
`--proxy-labels` (Traefik Docker-provider labels on `serve`, default off)
with `--traefik-entrypoint` / `--traefik-certresolver` (defaults
`websecure` / `letsencrypt`), the advanced `--proxy-network-mode join|link`
(default `join`) with, for `link` only, `--proxy-subnet`, `--proxy-ip` and
`--serve-ip`, then `--target-cidr` /
`--target-host` (the runner may reach no database until you allow one in
`runner.toml` or here), `--keys-dir`, `--ownership`, the passphrase
sources `--ca-passphrase-file`, `--ca-passphrase-stdin`,
`--generate-ca-passphrase`, `--import-master-key <file>` (with
`--master-key-check-value`), `--use-existing-ca`, and the
[external database](#your-own-postgresql-external-database-files) flags
`--database external`, `--db-location docker|remote`, `--db-network`,
`--db-host`, `--db-port`, `--db-name`,
`--db-tls verify-ca-file|verify-system|require|disable`,
`--db-ca-file`, `--db-tls-server-name`, `--db-auth`, `--db-user`,
`--db-admin-user`, `--db-password-file`, `--db-owner-password-file`. With `--non-interactive` every answer must come
from flags. All answers are validated against strict patterns, so nothing
can be injected into `.env` or the TOML files. Full reference:
[`bekup setup init`](../reference/cli/bekup_setup_init.md).

- **Image and variant:** setup takes the image from the `x-bekup-image`
  line (the setup service's `BEKUP_IMAGE` must match; a container cannot
  verify its own digest, so a tag that names another version than the
  running binary is refused, `--allow-version-mismatch` overrides). With
  the committed file the line is exactly the `${BEKUP_IMAGE:?...}`
  placeholder and the image is the `BEKUP_IMAGE` docker compose resolved
  (shell, else `.env`); it must be digest-pinned, setup writes it to
  `.env` and records `image_source: env` in `bekup-setup.json`. It
  recognizes the variant by rendering each release variant with that
  image (the committed file: with the placeholder): **exact** (byte-equal)
  or **model** (same YAML, only comments or formatting differ) are
  accepted; a **customized** file needs `--variant <the file it is based
  on>`, gets a warning and must pass the hardening lint (below). Any other
  `x-bekup-image` line (`${BEKUP_IMAGE:-bekup:dev}` of the repository's
  `docker-compose.yml`, another default or message) is refused, and
  `BEKUP_IMAGE` may appear nowhere else in `compose.yaml` or the override.
- **What it writes:** see the [manifest](#file-ownership-and-permissions).
  `deploy/**`, `reference/*.example.toml` and `bekup-setup.json` are
  *generated*: `setup upgrade` rewrites them, so do not edit them.
  `compose.yaml`, `.env`, `bekup.toml`, `runner.toml` and
  `compose.override.yaml` are *yours*: setup never writes `compose.yaml`,
  writes the others once and afterwards only validates them. The TOML files
  are minimal, so safer defaults of new releases keep applying.
- **Setup-only blocks in `compose.override.yaml`** (D-215): the Traefik
  labels on `serve` (`--proxy-labels`), `serve`'s fixed address on the link
  network (`--proxy-network-mode link`) and the database container's
  network (`--db-location docker`) are never part of a release file (nor
  is the proxy's certificate mount of `--tls-mode static`). setup writes
  them into a new `compose.override.yaml` (compose-first and setup-first),
  so `compose.yaml` stays the byte-exact release file, and records them in
  `bekup-setup.json`. setup never rewrites an existing override (not
  `setup init`, also not with `--force`, and not `setup upgrade`): it must
  already carry the blocks, otherwise `setup init` and `setup upgrade`
  refuse and print them to add (`setup upgrade` writes a new override only
  for a [retired variant](upgrades.md#retired-variants-traefik-external-and-npm)
  without one). Merge your own changes into the existing `services:` and
  `networks:` keys (a second `services:` or `networks:` key is invalid
  YAML).
- **Local changes** go into `compose.override.yaml` (docker compose merges
  it automatically), never into `compose.yaml`: then an upgrade is a plain
  file replacement.
- **Secrets:** three 32-byte random database passwords (base64url) and the
  two connection URLs (bundled database; an external one gets its two
  password files instead), the master keyring (`bekup keys generate`
  format, or your imported one) and the private CA with the runner, serve
  and (bundled database) postgres certificates. The CA
  key is age-encrypted with your passphrase; there is no unencrypted option.
  `--generate-ca-passphrase` creates one and shows it once, only on a
  terminal.
- **Evidence instead of audit:** setup runs before the database exists and
  has no network, so it is not audited. It prints the certificate
  fingerprints and the master key ID: record them, and **escrow the master
  key** (see [key management](../security/key-management.md)).
- **Existing files** are never overwritten: `--keep-existing` creates only
  missing files, `--force` rewrites the non-secret files, and
  `--regenerate-secrets` (terminal and typed confirmation) replaces every
  secret. A failed run removes everything it created.
- **Keys directory:** `--keys-dir` (default `secrets`) is a path inside the
  deployment directory; mount another (encrypted) volume there to keep the
  secrets elsewhere. `.env` sets `BEKUP_KEYS_DIR` to match.
- **Other compose files** next to `compose.yaml` (`compose.yml`,
  `docker-compose.yaml`, `docker-compose.yml`, `compose.override.yml`,
  `docker-compose.override.y{a,}ml`) are refused: docker compose would read
  them instead of or next to the checked files.

### The override file and the hardening lint

`setup check` (and `setup init`/`upgrade`) lints the project docker compose
would actually run: `compose.yaml` and `compose.override.yaml` are loaded
with compose-go, the loader of Docker Compose itself, so every YAML
document, merge key (`<<`), alias, `!reset`/`!override` tag, `extends` and
`${VAR}` (from `.env`) is resolved first, and the lint checks the result.
A file with more than one YAML document (`---`) is refused outright.

**The lint is a guardrail, not a proof** (D-205 amendment 2). A deployment
is *verified* only when `compose.yaml` is the attested release file byte
for byte (recognized as *exact*), there is **no** `compose.override.yaml`
(or one with only the blocks setup wrote, which the lint holds exactly),
and setup recorded the file's `x-bekup-image`. The committed file from
`deploy/compose/` is recognized as *exact* too, but it is not attested:
compare it with the repository at the tag of your image (for example
`git diff <tag> -- deploy/compose/` in a checkout) when that matters. Everything else is linted
best-effort: the lint models compose-go v2.16.2 (verified with Docker
Compose v5.6.0; **minimum Docker Compose v2.24.4**, which the documented
`!override`/`!reset` overrides need; keep Compose current, older versions
may merge differently), and it cannot see variables that exist only in
the shell where you run `docker compose`. A check that runs inside a
container the checked file defines (`docker compose run --rm setup check`)
is not trustworthy for a **tampered** file: that file also defines the
checking container. To check a file you do not trust, run the image of
the **attested release** (take its digest from the release notes or a
verified release `compose.yaml`, not from the file under suspicion) with
the setup service's hardening and the directory read-only:

```sh
docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -v "$PWD":/deploy:ro ghcr.io/jlentink/bekup:<version>@sha256:<digest> setup check
```

Errors:

- **bekup's services are pinned.** Every service the variant ships
  (`setup`, `certs-init`, `migrate`, `admin`, `serve`, `runner`,
  `postgres`, and `traefik`/`traefik-init` or `nginx`/`nginx-init`/
  `certbot` where shipped) must equal the shipped definition plus the
  setup-only blocks the deployment records (above; a missing, changed or
  extended block is an error, and `setup check` prints the expected
  blocks), resolved with the same `.env`, in every attribute: image, entrypoint, command,
  environment, `env_file`, user, `group_add`, capabilities,
  `security_opt`, `read_only`, tmpfs, volumes (bind options, SELinux),
  networks (aliases, addresses), `network_mode`, namespaces, ports,
  labels, logging, healthcheck, `working_dir`, `extra_hosts`, `dns`,
  `links`/`external_links`, ulimits, `oom_score_adj`, `shm_size`,
  `sysctls`, devices, `privileged`, `x-*` keys, and every other compose
  attribute. Only these **operator-tunable** attributes may differ:

  | Service | Tunable |
  |---|---|
  | every service but `setup` | `deploy.resources` cpus/memory/pids (limits and reservations; nothing else under `deploy`, no unlimited pids; the shipped limits may change but not be removed or set to 0, so `deploy: !reset null` is an error), `cpus`, `mem_limit`, `mem_reservation`, `pids_limit` (> 0; `pids_limit: 0` is an error), `restart`, `stop_grace_period` |
  | `traefik`, `traefik-init`, `nginx`, `nginx-init`, `certbot` | `profiles` (e.g. certbot off in static TLS mode) |
  | `traefik`, `nginx` | `PROXY_TLS_MODE` (`acme`/`static`) and `PROXY_HTTPS_PUBLIC_PORT` in `environment`; a read-only `./proxy-tls` bind at `/certs`; the published address/port of `ports` (same container ports, tcp) |
  | `runner` | the Docker socket source (`/var/run/docker.sock`, `/run/docker.sock`, `/run/user/<uid>/docker.sock`; read-only at `/var/run/docker.sock`, no bind options or SELinux relabeling); `userns_mode: host` (a warning, for userns-remap daemons) |
  | `serve` | the source of its backups/scratch mounts and extra mounts below `/var/lib/bekup/backups/` (disk destinations): named volumes other than bekup's other volumes, or binds inside the deployment directory outside bekup's files |

  The `setup` service has no tunables (root with `CAP_CHOWN` only, no
  network, no logs). The shipped networks (`internal`, driver, subnets,
  name, the external `proxy` and `database` networks), volumes, secrets,
  configs and the project name are pinned too (the name also as written:
  no variables in `name:`). Only `serve` is on `proxy`, and only
  `migrate`, `serve` and `admin` are on `database`.
- **Compose variables:** only bekup's own (`ACME_CA_SERVER`, `ACME_EMAIL`,
  `BEKUP_DB_NETWORK`, `BEKUP_DOMAIN`, `BEKUP_IMAGE_MODE`, `BEKUP_KEYS_DIR`,
  `BEKUP_PROXY_NETWORK`, `BEKUP_SETUP_VERSION`, `DOCKER_GID`,
  `FRONTEND_SUBNET`, `METRICS_SUBNET`, `PROXY_FRONTEND_IP`,
  `PROXY_TLS_MODE`, `RUNNER_CTL_IP`, `RUNNER_CTL_SUBNET`,
  `SERVE_FRONTEND_IP`, `SERVE_METRICS_IP`, `SERVE_PROXY_IP`,
  `TRAEFIK_CERTRESOLVER`, `TRAEFIK_ENTRYPOINT`), and only where the shipped
  file uses them; any other `${VAR}`/`$VAR`, a `$` that is not `$$` or a
  reference, and any variable in a service, network, volume, secret or
  config you add are errors: write literal values. Explicit YAML tags
  are limited to `!!str`, `!!int`, `!!bool`, `!!float`, `!!null`,
  `!!map`, `!!seq`, `!reset` and `!override` (`!!binary` and custom tags
  are errors).
- **Services you add** (for example a Prometheus): a digest-pinned image
  (`x-bekup-image` for anything on the bekup repository), no `build`;
  `read_only: true`, `cap_drop: [ALL]`, `security_opt` with
  `no-new-privileges:true` and nothing else, a numeric non-root `user`,
  `cap_add` only `NET_BIND_SERVICE`; no `privileged` (hooks included, which
  must not run as root either), `devices`, `device_cgroup_rules`,
  `volumes_from`, `gpus`, `pre_start`, `provider`, `develop`,
  `use_api_socket`, `runtime`, `cgroup_parent`, `isolation`,
  `storage_opt`, `volume_driver`, `sysctls`, `group_add`,
  `external_links`, `oom_kill_disable`, negative `oom_score_adj`,
  real-time CPU settings or unlimited pids; `ulimits` only `nofile`
  (≤ 1048576) and `nproc` (≤ 65536); `shm_size` ≤ 1 GiB; logging only
  `json-file`, `local` or `none` with size/rotation options; no
  `traefik.*` or `com.docker.*` labels; `network_mode` only `none`, no
  `pid`/`uts`/host `ipc`/`cgroup`, no `userns_mode` (`host` is allowed only
  on the runner); bind mounts only from **relative** paths inside the
  deployment directory (an absolute path, even `/opt/bekup/x`, is a host
  path), never of `secrets/` (or `BEKUP_KEYS_DIR`), `bekup-setup.json`,
  `.env`, the TOML or compose files, `deploy/` or `proxy-tls/`; no
  propagation other than `private`, no SELinux relabeling, no
  `image`/`npipe`/`cluster` mounts; none of bekup's named volumes,
  secrets or configs; networks only their own, `metrics` (for a
  Prometheus) and, in the `full` variant, `frontend` (your own proxy with
  `--proxy none`), never bekup's `proxy` or `database` network, without
  aliases, `container_name` or `mac_address` on
  bekup's networks and without fixed addresses (`ipv4_address`,
  `ipv6_address`, `mac_address`, `link_local_ips`) there, except on
  `frontend` in the `full` variant, where the address must differ from
  the ones bekup's services use (`PROXY_FRONTEND_IP`,
  `SERVE_FRONTEND_IP`); `ports` never (only the bundled proxy
  publishes).
- **Top-level objects you add:** volumes only `local` without
  `driver_opts`, not external, no `name:`; networks only `bridge` without
  `driver_opts` or another IPAM driver, not external, no `name:`;
  secrets/configs only from files inside the directory (not bekup's own),
  not external, no drivers, no `environment:`; no `include`, `extends`
  from another file, `jobs` or `models`.
- **Paths:** a symlink on the way to any bind source, `env_file`,
  `label_file` or secret/config file inside the directory is an error
  (init and upgrade too); `setup check` refuses every symlink below the
  directory and every managed file with more than one hard link.

The recognition levels use the same resolved model: a file counts as the
release file (*model*) only when both its YAML and the project docker
compose resolves from it (with the same `.env`) equal the release file's,
so a merge-key or `!reset` trick is always *customized* and linted.
Keep compose variables in `.env`: variables set only in the shell are
invisible to the setup container, and `setup check` warns about the ones
it can see that override `.env`. `DOCKER_HOST`, `DOCKER_CONTEXT`,
`DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` and `DOCKER_CONFIG` in `.env` are
errors (docker compose would deploy to another daemon or with another
client configuration). compose-go's own error messages may quote
interpolated values (bounded; `.env` holds no secrets).

Typical allowed overrides: resource limits, a disk for disk destinations
(`./backups:/var/lib/bekup/backups/disk` with your disk mounted at
`./backups`, or a named volume), the rootless Docker socket and
`userns_mode: host` on the runner
([rootless-docker.md](rootless-docker.md)), static TLS and `ports:
!override` on the proxy ([reverse-proxy.md](reverse-proxy.md)), a
hardened Prometheus on `metrics` ([operations.md](../operations.md#metrics)).

## Your own PostgreSQL (external-database files)

The `*.external-db.yaml` files (and the wizard's "existing PostgreSQL"
choice, or `--database external`) connect bekup to a PostgreSQL 15-17 you
operate: a managed service or your own server (requirements and provider
notes: [external-database.md](external-database.md)). Compared with the
bundled form:

- there is **no `postgres` service**, no `postgres-data` volume, no
  `deploy/postgres` and no `postgres` certificate;
- `bekup.toml [database]` is in fields mode: `host`, `port`, `name`,
  `user = "bekup_app"`, `password_file`, `owner_user = "bekup_owner"`,
  `owner_password_file`, `tls_mode` and `tls_ca_file` from the TLS choice
  ([`--db-tls`](external-database.md#tls-modes): `verify-ca-file`, the
  default, `verify-system`, or the weakened `require` / `disable`) and an
  optional `tls_server_name`;
- the passwords live in `secrets/bekup_database_password` (serve, admin,
  migrate) and `secrets/bekup_database_owner_password` (migrate only),
  mounted as the Docker secrets of the same names;
- with `verify-ca-file` setup copies the CA bundle you named (a PEM file
  inside the deployment directory; only `CERTIFICATE` blocks are accepted)
  to `secrets/bekup_database_ca.crt`, mounted as `bekup_db_ca_crt` at the
  default `tls_ca_file` path; only that bundle is trusted. The other
  choices leave that file empty (it stays mounted);
- for a database on another server the **`backend` network is not
  internal**: an internal network has no route off the host. Docker cannot
  narrow it to the database's address, so serve, migrate and admin can
  reach other hosts through it (serve already can, through `egress`).
  **Allow only this host** in the database's firewall (security group) and
  `pg_hba.conf`, and keep `verify-full`;
- for a database **container on this Docker host** (`--db-location docker
  --db-network <net>`), `migrate`, `serve` and `admin` join its network
  instead (the external network `database`, `BEKUP_DB_NETWORK`, a block in
  `compose.override.yaml`) and `backend` stays internal
  ([external-database.md](external-database.md#database-container-on-this-docker-host)).

setup runs without a network, so it never connects to the database. How
bekup's roles come to exist (`--db-auth`):

| Choice | What setup does | Your next step |
|---|---|---|
| `bootstrap` (default) | generates both role passwords | `docker compose run --rm migrate db bootstrap --admin-user <admin>`: an account with `CREATEROLE` + `CREATEDB` (or the provider's admin) creates `bekup_owner`, `bekup_app` and the database; its password is asked there, used once, never stored |
| `existing-roles` | takes the two passwords from `--db-password-file` / `--db-owner-password-file` (files in the directory, copied; delete the originals) or the terminal | nothing to create |
| `dba-sql` | generates the passwords and writes `secrets/bekup-roles.sql` (root, 0600) with their SCRAM verifiers, never the passwords | hand it to your DBA (`psql -v ON_ERROR_STOP=1 --no-psqlrc -f bekup-roles.sql ...`), then delete it; `setup check` warns while it exists |
| `single-account` | takes the account's password (file or terminal); the owner password file stays empty | `docker compose run --rm migrate db bootstrap` as that account (needs `CREATEROLE` + `CREATEDB` once) |

`single-account` is an **explicit weakening**: one account runs the
migrations and the app, so a compromised serve could change the schema and
the audit log; bekup warns at every start, audits it and alerts every admin
daily (D-207). Then, in every case:

```sh
docker compose run --rm migrate migrate check   # reachability, TLS, roles, citext
docker compose up -d
```

`setup check` checks the files (presence, owners, modes), that `bekup.toml`
names exactly the mounted paths (or the system bundle), reports the
recorded `require`/`disable` choice and any other weaker `tls_mode` as a
warning, `BEKUP_DB_NETWORK` against the recorded database network,
an expired CA bundle (an error only when no certificate in it is valid),
and `PG*` or `BEKUP_DATABASE_*` variables in `.env` as errors. The database
itself is checked from the migrate container (`migrate check`). Switching
between the bundled and an external database is not an upgrade: set up a
new directory and move the data with a dump and restore
([upgrades.md](upgrades.md)).

## Alternative: setup-first

With `--write-compose --variant traefik|own-proxy|full` (or `--proxy
traefik|own`, which implies the variant), setup
also writes `compose.yaml` itself (generated-upgradable, with a "GENERATED"
header). Use it for the `full` variant (every service of the repository
file; `--proxy traefik`, `nginx`, or `none` for your own proxy on the
`frontend` network with `--trusted-proxy <cidr>`: the advanced path when
your proxy is not a container on this Docker host, so run a small proxy
on this host or route to bekup's `frontend` network yourself; serve's
port is never published,
[reverse-proxy.md](reverse-proxy.md#using-your-own-reverse-proxy)), or when you cannot
download the release file. Run setup with `docker run` and the same
hardening as the compose service:

```sh
# The image you deploy, pinned by digest (never a bare tag); verify it first.
IMAGE=ghcr.io/jlentink/bekup:1.0.0@sha256:<digest from the release notes>
sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -e BEKUP_IMAGE="$IMAGE" -v /opt/bekup:/deploy \
  -v /media/usb/bekup-ca:/deploy/secrets/certs/ca \
  "$IMAGE" setup init --write-compose --variant full \
  --docker-gid "$(stat -c %g /var/run/docker.sock)"
docker compose up -d
docker compose run --rm admin create --email you@example.com --display-name "You"
```

Run it only from an image whose signature verified: in this mode setup
writes the compose file too. `.env` selects the proxy of the `full` variant
with `COMPOSE_PROFILES` (`traefik`, `nginx`, or empty). Upgrades need
`setup upgrade --write-compose --image <new>`
([upgrades.md](upgrades.md#setup-first-installs)); setup refuses local edits
of the generated `compose.yaml` (use `compose.override.yaml`).

### The CA key stays offline

Mount your offline media (encrypted USB stick) at
`/deploy/secrets/certs/ca` while setup runs: the CA key is then written
there and never touches the server's disk.

```sh
docker compose run --rm -v /media/usb/bekup-ca:/deploy/secrets/certs/ca setup
```

Setup detects the mount. Without it, setup and `setup check` warn until
`secrets/certs/ca/` is moved off the host. Bring the media back only for
[certificate rotation](../security/mtls-certs.md).

### Ownership modes

`--ownership` decides how files get their owners (the chosen mode is always
printed):

| Mode | Effect |
|---|---|
| `auto` (default) | `none` on Docker Desktop file systems (`fakeowner`, `virtiofs`, `fuse.grpcfuse`, `osxfs`, `9p`), `chown` when running as root, else `print` |
| `chown` | chmod, then `lchown` every entry (needs only `CAP_CHOWN`) |
| `print` | chmod, then print the `chown` commands to run on the host as root |
| `none` | chmod only (Docker Desktop maps ownership itself) |

Platform notes: rootless Docker works unchanged (the container's root and
UIDs map into your subordinate range consistently). With userns-remap, the
deployment directory must belong to the remapped root UID. On SELinux hosts
add `:z` to the bind mounts of the setup container.

### Check and upgrade

```sh
# Read-only; never reads secret contents. Exits non-zero on errors.
# -f compose.yaml: the override never shapes the checking container.
docker compose -f compose.yaml run --rm setup check [--fix-ownership]
```

Without compose (or to rule out the compose files entirely), run the image
pinned in the `x-bekup-image` line with the setup service's hardening (for
a file you do not trust, use the digest of the attested release instead,
see [the hardening lint](#the-override-file-and-the-hardening-lint)):

```sh
IMAGE=$(sed -n 's/^x-bekup-image: &bekup-image //p' compose.yaml)
# the committed file (deploy/compose/): the image is in .env
case $IMAGE in '${BEKUP_IMAGE'*) IMAGE=$(sed -n 's/^BEKUP_IMAGE=//p' .env) ;; esac
docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -v "$PWD":/deploy:ro "$IMAGE" setup check
```

(`--fix-ownership` needs the mount without `:ro`.)

`setup check` verifies the manifest (presence, type, mode, owner, no
symlinks), that every image (and the `x-bekup-image` line) is
digest-pinned and the generated files are unchanged (with the committed
file: `BEKUP_IMAGE` in `.env` is pinned and equals the recorded image, no
`BEKUP_IMAGE` in the environment overrides it, and every bekup service
resolves to it), that `compose.yaml`
is still the recorded variant (a switch is an error; an image newer than
the one setup last ran with asks for `setup upgrade`, an older one is an
error), the [hardening lint](#the-override-file-and-the-hardening-lint),
that `bekup.toml`/`runner.toml` validate and agree with `.env`
(`base_url` and `BEKUP_DOMAIN`, `trusted_proxies` exactly the proxy's /32
or, own-proxy join mode, empty with `trusted_proxy_hosts` exactly the
recorded container, listen addresses, `BEKUP_PROXY_NETWORK` and the link
network, both Docker GIDs), certificate expiry, variables that
would override `.env` from the environment, and whether the CA key is still
on disk (an expiring certificate points to `bekup setup renew`), and for an
external database the checks [above](#your-own-postgresql-external-database-files).
`--fix-ownership` re-applies the manifest (for example after `certs
rotate`). Upgrades: [upgrades.md](upgrades.md#container-only-installs-bekup-setup);
on a terminal `setup upgrade` is guided (summary, backup question,
confirmation; `--non-interactive` skips it).

### Certificate renewal

`bekup setup renew` lists the internal certificates with their expiry and
renews them from the CA you mount for the run (expiring ones, `--all`, one
`--component`, or a CA rotation phase with `--ca stage|promote|finish`);
it re-applies the manifest's owners and prints the services to recreate.
Details: [mtls-certs.md](../security/mtls-certs.md#renewal-with-bekup-setup-renew).

## Configuration files

`bekup.toml` (serve, migrate, admin) and `runner.toml` (runner) are mounted
read-only at `/etc/bekup/` as compose `configs`. `bekup setup` writes
minimal ones; every key is documented in `reference/*.example.toml` and
[configuration.md](../configuration.md). They contain no secrets.

### File ownership and permissions

Compose bind-mounts `configs` and file-based `secrets` with their **host**
ownership and mode. On Linux hosts, bekup's file safety checks (D-041) only
read a config or secret file that is owned by **root** or the reading
container UID (65532 for bekup, 70 for the app Postgres), **not group- or
world-writable**, and for secrets also **not world-readable**. Otherwise
bekup refuses to start, so a file that another local user could edit can
never become bekup's configuration.

`bekup setup` applies exactly this manifest (generated from
`internal/setup/manifest.go`; a test keeps the table in sync):

<!-- setup-manifest:begin -->
| Path | Class | Owner (UID:GID) | Mode |
|---|---|---|---|
| `./` (the deployment directory) | operator | 0:0 | at most 0755 |
| `compose.yaml` (yours; generated-upgradable with `--write-compose`) | operator | 0:0 | 0644 |
| `bekup-setup.json` | generated-upgradable | 0:0 | 0644 |
| `.env` | operator | 0:0 | 0644 |
| `bekup.toml` | operator | 0:0 | 0644 |
| `runner.toml` | operator | 0:0 | 0644 |
| `compose.override.yaml` (static TLS or setup-only overlays, D-215) | operator | 0:0 | 0644 |
| `proxy-tls/` | operator | 0:0 | 0755 |
| `reference/` | generated-upgradable | 0:0 | 0755 |
| `reference/bekup.example.toml` | generated-upgradable | 0:0 | 0644 |
| `reference/runner.example.toml` | generated-upgradable | 0:0 | 0644 |
| `deploy/**` (`*.sh` and directories 0755) | generated-upgradable | 0:0 | 0644 |
| `secrets/` | secret | 0:0 | 0711 |
| `secrets/bekup_master_key` | secret | 65532:65532 | 0400 |
| `secrets/bekup_database_url` (bundled database) | secret | 65532:65532 | 0400 |
| `secrets/bekup_database_owner_url` (bundled database) | secret | 65532:65532 | 0400 |
| `secrets/postgres_password` (bundled database) | secret | 70:70 | 0400 |
| `secrets/bekup_owner_password` (bundled database) | secret | 70:70 | 0400 |
| `secrets/bekup_app_password` (bundled database) | secret | 70:70 | 0400 |
| `secrets/certs/` | secret | 0:0 | 0711 |
| `secrets/certs/serve/` | secret | 0:0 | 0711 |
| `secrets/certs/serve/serve.crt` | secret | 0:0 | 0644 |
| `secrets/certs/serve/ca.crt` | secret | 0:0 | 0644 |
| `secrets/certs/serve/serve.key` | secret | 65532:65532 | 0400 |
| `secrets/certs/runner/` | secret | 0:0 | 0711 |
| `secrets/certs/runner/runner.crt` | secret | 0:0 | 0644 |
| `secrets/certs/runner/ca.crt` | secret | 0:0 | 0644 |
| `secrets/certs/runner/runner.key` | secret | 65532:65532 | 0400 |
| `secrets/certs/postgres/` (bundled database) | secret | 0:0 | 0711 |
| `secrets/certs/postgres/postgres.crt` (bundled database) | secret | 0:0 | 0644 |
| `secrets/certs/postgres/ca.crt` (bundled database) | secret | 0:0 | 0644 |
| `secrets/certs/postgres/postgres.key` (bundled database) | secret | 70:70 | 0400 |
| `secrets/certs/ca/` (offline) | secret | 0:0 | 0700 |
| `secrets/certs/ca/ca.crt` (offline) | secret | 0:0 | 0644 |
| `secrets/certs/ca/ca.key` (offline) | secret | 0:0 | 0600 |
| `secrets/bekup_database_password` (external database) | secret | 65532:65532 | 0400 |
| `secrets/bekup_database_owner_password` (external database) | secret | 65532:65532 | 0400 |
| `secrets/bekup_database_ca.crt` (external database) | operator | 0:0 | 0644 |
| `secrets/bekup-roles.sql` (external database, `dba-sql` only) | secret | 0:0 | 0600 |
<!-- setup-manifest:end -->

Docker Desktop (macOS/Windows) maps ownership itself. For the dev stack,
`sudo deploy/dev/gen-dev-keys.sh --chown` applies the same rules to the dev
secrets.

## Secrets

Secrets are file-based Docker secrets below `BEKUP_KEYS_DIR` (default
`./secrets`), mounted at `/run/secrets/<name>`. They never appear in
environment variables, argv or the TOML files (D-019). Each service only
receives what it needs:

| Secret file | Used by | Content |
|---|---|---|
| `secrets/bekup_master_key` | serve | master keyring (`bekup keys generate`, task 0.5) |
| `secrets/bekup_database_url` | serve, admin | `postgres://bekup_app:…@postgres:5432/bekup` |
| `secrets/bekup_database_owner_url` | migrate | `postgres://bekup_owner:…@postgres:5432/bekup` |
| `secrets/postgres_password` | postgres | bootstrap superuser password |
| `secrets/bekup_owner_password`, `secrets/bekup_app_password` | postgres (init only) | role passwords (must match the URLs) |
| `secrets/certs/serve/{serve.crt,serve.key,ca.crt}` | serve | client certificate + CA bundle (as `bekup_client.{crt,key}`, `bekup_ca.crt`) |
| `secrets/certs/runner/{runner.crt,runner.key,ca.crt}` | runner | server certificate + CA bundle (as `runner.{crt,key}`, `bekup_ca.crt`) |
| `secrets/certs/postgres/{postgres.crt,postgres.key}` | postgres | app DB TLS server certificate + key (as `postgres.{crt,key}`) |
| `secrets/certs/postgres/ca.crt` | postgres, serve, migrate, admin | CA bundle (as `postgres_ca.crt` / `bekup_db_ca.crt`, the `tls_ca_file` default) |
| `secrets/bekup_database_password` (external database) | serve, admin, migrate | the `bekup_app` password (single-account: the account's) |
| `secrets/bekup_database_owner_password` (external database) | migrate | the `bekup_owner` password (empty in single-account mode) |
| `secrets/bekup_database_ca.crt` (external database) | serve, admin, migrate | your PostgreSQL's CA bundle (as `bekup_db_ca.crt`) |

The CA private key (`secrets/certs/ca/ca.key`, age-encrypted) is never
mounted into a long-running service. The URLs need no `sslmode`: TLS comes
from `[database] tls_mode` (default `verify-full`; see
[App DB TLS](../security/app-db-tls.md)).

### From a source checkout (development)

The repository's `docker-compose.yml` is the `full` variant with the
variable image line `${BEKUP_IMAGE:-bekup:dev}`. Without `bekup setup`,
create the same files by hand: the TOMLs from the
examples, the passwords and URLs, `just keygen`, and the certificates with
`docker compose --profile setup run --rm certs-init`; then apply the
manifest above with `chown`/`chmod`. Set `BEKUP_IMAGE` (shell or `.env`)
to a digest-pinned reference; the repository compose file defaults to the
locally built `bekup:dev`. Production installs should use `bekup setup`.

## Reverse-proxy profiles

In the `traefik` variant Traefik starts with the stack (no profile). In the
`full` variant (setup-first, or the repository file) TLS for the UI is
terminated by one of two compose profiles; enable exactly one of them. The
`own-proxy` variant uses your own reverse proxy. Full details
are in [reverse-proxy.md](reverse-proxy.md).

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

### Verifying release images

Official images are built only by the release workflow from a signed
`vX.Y.Z` tag on `main` (D-204) and published as
`ghcr.io/jlentink/bekup:X.Y.Z` (multi-arch: `linux/amd64`, `linux/arm64`).
The workflow pushes by digest, scans the image with Trivy, signs it with
cosign (keyless, GitHub OIDC, logged in the public Rekor transparency log),
attests an SPDX SBOM per platform and GitHub build provenance, and only then
tags it. `latest` follows the highest released version; following it is a
supported, explicitly weakened mode ([Following `:latest`](#following-latest-d-212));
for strict change control deploy by digest: the release notes of every
version list the digest and the exact commands.

```sh
IMAGE=ghcr.io/jlentink/bekup:X.Y.Z@sha256:<digest from the release notes>
# Signature: made by release.yml for a vX.Y.Z tag of jlentink/bekup.
cosign verify \
  --certificate-identity-regexp '^https://github\.com/jlentink/bekup/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$IMAGE"
# Or GitHub's build provenance attestation:
gh attestation verify "oci://$IMAGE" --repo jlentink/bekup
# SBOM of one platform (attested to that platform's manifest digest):
cosign verify-attestation --type spdxjson \
  --certificate-identity-regexp '^https://github\.com/jlentink/bekup/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/jlentink/bekup@sha256:<platform manifest digest>
```

The release notes give the exact identity of the version
(`…/release.yml@refs/tags/vX.Y.Z`); prefer it over the regular expression
when you verify a specific release. Platform manifest digests are listed by
`docker buildx imagetools inspect "$IMAGE"`.

### Following `:latest` (D-212)

The one exception to "deploy by digest" is the bekup image itself, and only
a tag of the official repository: setup accepts
`ghcr.io/jlentink/bekup:latest` (any tag of `ghcr.io/jlentink/bekup`)
without a digest when you name it explicitly, writes it as is into
`compose.yaml` (`x-bekup-image`) or `.env` (`BEKUP_IMAGE`, the committed
file), records `image_mode: following` in `bekup-setup.json` and
`BEKUP_IMAGE_MODE=following` in `.env`. Every other image (PostgreSQL,
Traefik, nginx, the dump images, your own registry) stays digest-pinned,
and the hardening lint accepts the followed tag only when the deployment
is recorded as following. Release assets are always pinned.

What protects you: `migrate`, `serve`, `worker` and `runner` refuse to
start on another version than `BEKUP_SETUP_VERSION`
([the version guard](upgrades.md#the-version-guard)), and a followed
image without it, so pulling a moved tag never runs new migrations,
deploy assets or a new runner unprepared; `setup check` warns, serve and
worker audit `security.unpinned_image` at every start, admins get a daily
security alert and the configuration page shows the badge. serve and
worker also ask the runner whether its own container image carries a
digest, so an unpinned image is treated as following even if
`BEKUP_IMAGE_MODE` says otherwise. What it
cannot prevent: a compromised publisher that moves `:latest` decides what
your next `setup upgrade` runs. Verify releases with cosign before
upgrading, or pin a digest.

### Using your own image

To run an image you built and pushed yourself (for example with
`just publish`, see [development.md](../development.md#publishing-your-own-image)),
pin it by digest, never by a mutable tag (a `sha-…-dirty` tag in particular
is overwritten by the next dirty build of the same commit):

```sh
# compose-first, simplest: the committed file from deploy/compose/ and
# BEKUP_IMAGE=registry.example.com/team/bekup:<tag>@sha256:<digest> docker compose run --rm setup
# compose-first, pinned file: render compose.yaml pinned to it with the image itself
docker run --rm --network none registry.example.com/team/bekup@sha256:<digest> \
  setup compose --variant traefik --image registry.example.com/team/bekup@sha256:<digest> >compose.yaml
# setup-first: pass it as --image / BEKUP_IMAGE to `setup init --write-compose` or `setup upgrade --write-compose`
IMAGE=registry.example.com/team/bekup@sha256:<digest printed by just publish>
# Source checkout without setup: .env next to docker-compose.yml
BEKUP_IMAGE=registry.example.com/team/bekup@sha256:<digest printed by just publish>
```

A `sha-…` tag does not equal the binary version, so `setup` needs
`--allow-version-mismatch` for it (or pin the digest without a tag).

On a private registry, run `docker login registry.example.com` on the host
before `docker compose pull`. Verify what you pin with
`docker buildx imagetools inspect <repo>@sha256:…` (platforms, SBOM and
provenance attestations).

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
