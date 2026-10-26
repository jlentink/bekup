# Getting started

> **Status:** partial (end of M0). The development quick start and the
> production outline below match the current files. `bekup serve` itself
> arrives in task 1.1, so the dev stack starts but the UI does not answer
> yet; `bekup runner` arrives in task 2.4. First-run setup (setup token,
> admin, passkey) is filled in by tasks 1.6–1.8, the first connection,
> destination and job by tasks 2.11–3.5, and the page is completed in task
> 7.5.

There are two ways to run bekup with Docker Compose:

- **Development:** `docker-compose.yml` + `docker-compose.dev.yml`. Plain
  HTTP on `http://localhost:8080`, no reverse proxy needed, sample databases
  and destinations included.
- **Production:** `docker-compose.yml` with one reverse-proxy profile
  (`traefik` or `nginx`) for HTTPS.

## Development quick start

Prerequisites: Docker (Docker Desktop on macOS/Windows), Go,
[just](https://just.systems/), and for the UI Node.js + pnpm.

```sh
just doctor                      # checks every required tool and the Docker daemon
deploy/dev/gen-dev-keys.sh       # once: dev passwords, master key, dev PKI, SSH keys
                                 # Linux: sudo deploy/dev/gen-dev-keys.sh --chown
DOCKER_GID=0 just dev            # Docker Desktop; on Linux:
                                 # DOCKER_GID=$(stat -c %g /var/run/docker.sock) just dev
```

Then open **<http://localhost:8080>** (once `serve` exists, task 1.1).

- `serve` is published on **`127.0.0.1:8080` only** (loopback), over
  **plain HTTP**. There is no reverse proxy in the default dev stack.
- Plain HTTP is allowed in dev mode, but never silently (D-095): bekup logs
  a WARN line at every startup and the UI shows a permanent, non-dismissable
  red banner "Connection is not encrypted (no HTTPS)" (tasks 1.1 and 6.1).
  Passkeys still work, because browsers treat `localhost` as a secure
  context.
- `just dev` runs `serve` with air hot reload. Its source mount is
  read-only and limited to the Go sources; the dev serve cannot read
  `secrets/`.
- Other dev services on loopback: MinIO console <http://127.0.0.1:9001>,
  mailpit <http://127.0.0.1:8025>, and the optional Vite dev server
  <http://127.0.0.1:5173> (`--profile web`, or `pnpm dev` in `web/`).
  Sample target databases have no host ports; only the runner reaches them.

The full list of dev services is in
[installation/compose.md](installation/compose.md#development-stack), and
the recipes in [development.md](development.md).

### Optional: HTTPS in dev with a proxy profile

The proxy profiles are opt-in, also in dev. To test the real TLS setup
locally:

```sh
deploy/dev/gen-dev-tls.sh        # mkcert certificate for bekup.localhost (self-signed fallback)
BEKUP_DEV_BASE_URL=https://bekup.localhost:8443 \
  docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile traefik up
                                 # or --profile nginx
```

Open <https://bekup.localhost:8443> (`http://bekup.localhost:8000`
redirects). The dev override uses the static local certificate instead of
ACME and binds both ports to `127.0.0.1`. See
[installation/reverse-proxy.md](installation/reverse-proxy.md).

Stop and wipe the dev stack:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml down -v --remove-orphans
```

## Production outline

The detailed steps are in [installation/compose.md](installation/compose.md)
and [installation/reverse-proxy.md](installation/reverse-proxy.md):

1. Create `bekup.toml` and `runner.toml` from the `*.example.toml` files
   (see [configuration.md](configuration.md)). On Linux they must be owned
   by root (or UID 65532) and not group- or world-writable.
2. Create the secret files in `secrets/`: database role passwords and URLs,
   and the master key with `just keygen` (or `bekup keys generate`). Escrow
   the master key offline ([key management](security/key-management.md)).
3. Create the mTLS certificates once:
   `docker compose --profile setup run --rm certs-init`, then move
   `secrets/certs/ca/` offline ([mTLS certificates](security/mtls-certs.md)).
4. Set `DOCKER_GID`, `BEKUP_DOMAIN` and `ACME_EMAIL`, and start the stack
   with exactly one proxy profile:
   `docker compose --profile traefik up -d` (or `--profile nginx`).
   `migrate` applies the database migrations before `serve` starts.
5. Complete first-run setup with the one-time setup token (task 1.6), enroll
   a passkey or TOTP (tasks 1.7–1.9), then add a connection, a destination
   and a backup job (tasks 2.11–3.5, UI in M6).

Running production without a proxy over plain HTTP requires an explicit
`server.allow_insecure_http = true` and raises a startup warning, a UI
banner, an audit entry and a security alert (D-095). Don't do that on an
untrusted network.
