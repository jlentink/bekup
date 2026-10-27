# Getting started

> **Status:** partial (end of M1). The development quick start and the
> production outline below match the current files. `bekup serve` runs with
> the HTTP layer, the GraphQL API (incl. WebSocket subscriptions), sign-in,
> sessions, second factors and step-up; the web UI is still a placeholder
> shell until M6 (task 6.1 onwards), and `bekup runner` arrives in task 2.4.
> Creating the first admin (CLI only, D-113) and enrolling a second factor
> (tasks 1.7–1.9) are described below, the first connection, destination
> and job by tasks 2.11–3.5, and the page is completed in task 7.5.

There are two ways to run bekup with Docker Compose:

- **Development:** `docker-compose.yml` + `docker-compose.dev.yml` start
  the infrastructure (app Postgres, mailpit, adminer) and bekup runs
  natively on `http://localhost:8080`; no reverse proxy needed. The `full`
  profile adds the containerised app, runner, sample databases and
  destinations.
- **Production:** `docker-compose.yml` with one reverse-proxy profile
  (`traefik` or `nginx`) for HTTPS.

## Development quick start

Prerequisites: Docker (Docker Desktop on macOS/Windows), Go,
[just](https://just.systems/), openssl, and for the UI Node.js + pnpm.

```sh
just doctor                                  # checks every required tool and the Docker daemon
just dev                                     # dev keys (once), postgres + mailpit + adminer, migrations
just admin-create you@example.com "Your Name"  # the first admin; the password is prompted
just run                                     # bekup serve, natively, on http://localhost:8080
```

On Linux, run `sudo deploy/dev/gen-dev-keys.sh --chown` once before
`just dev` so the containers can read their key files.

- `just dev` starts **only the infrastructure** (D-114), all on
  `127.0.0.1`: the app Postgres on port 5432 (TLS only, as in production),
  mailpit (SMTP 1025, UI <http://127.0.0.1:8025>) and adminer
  (<http://127.0.0.1:8081>: System PostgreSQL, server `postgres`, database
  `bekup`, user `bekup_app` with the password from
  `secrets/bekup_app_password`; the Postgres superuser is socket-only and
  cannot log in there by design). It generates the dev keys into
  `secrets/` on the first run and applies the migrations.
- The first admin is created on the command line only:
  `just admin-create <email> <name>` (wraps `bekup admin create`). Then
  sign in and enroll a second factor (through the GraphQL API until the
  web UI's sign-in and account pages land in M6).
- `just run` serves over **plain HTTP** on loopback. Plain HTTP is allowed
  in dev mode, but never silently (D-095): bekup logs a WARN line at every
  startup and the UI shows a permanent red banner "Connection is not
  encrypted (no HTTPS)". Passkeys still work, because browsers treat
  `localhost` as a secure context.
- `just web-dev` runs the Vite dev server with hot reload on
  <http://localhost:5173> against `just run`; `serve` itself embeds the last
  `pnpm build` of `web/`.
- `just dev-down` stops the containers (data is kept); `just db-reset`
  wipes and re-migrates the dev database. Busy ports can be moved, e.g.
  `BEKUP_DEV_PG_PORT=15432 just dev` (details in
  [development.md](development.md#local-development)).
- `just dev-full` starts the full containerised stack (`full` profile):
  `serve` with air hot reload, the runner, sample target databases, MinIO
  (console <http://127.0.0.1:9001>), SFTP and an SSH bastion. On Docker
  Desktop use `DOCKER_GID=0 just dev-full`; on Linux
  `DOCKER_GID=$(stat -c %g /var/run/docker.sock) just dev-full`.

The full list of dev services is in
[installation/compose.md](installation/compose.md#development-stack), and
the recipes in [development.md](development.md).

### Optional: HTTPS in dev with a proxy profile

The proxy profiles are opt-in, also in dev. To test the real TLS setup
locally:

```sh
deploy/dev/gen-dev-tls.sh        # mkcert certificate for bekup.localhost (self-signed fallback)
BEKUP_DEV_BASE_URL=https://bekup.localhost:8443 \
  docker compose -f docker-compose.yml -f docker-compose.dev.yml \
  --profile full --profile traefik up      # or --profile nginx
```

Open <https://bekup.localhost:8443> (`http://bekup.localhost:8000`
redirects). The dev override uses the static local certificate instead of
ACME and binds both ports to `127.0.0.1`. See
[installation/reverse-proxy.md](installation/reverse-proxy.md).

Stop the dev stack with `just dev-down`; wipe its data with
`just db-reset` (asks first) or:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml \
  --profile full --profile web --profile traefik --profile nginx down -v --remove-orphans
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
5. Create the first admin on the host with `bekup admin create`
   ([first admin](#first-admin)), sign in, enroll a passkey or TOTP, then add
   a connection, a destination and a backup job (tasks 2.11–3.5, UI in M6).

Running production without a proxy over plain HTTP requires an explicit
`server.allow_insecure_http = true` and raises a startup warning, a UI
banner, an audit entry and a security alert (D-095). Don't do that on an
untrusted network.

## First admin

The first admin account is created **on the host with the CLI only**
(D-113); the web UI has no setup page. As long as no active admin exists,
`bekup serve` starts normally but logs a warning with the command to run
(nobody can sign in until then):

```
level=WARN msg="NO ADMIN ACCOUNT: no active admin exists, so nobody can sign in. ..." command="bekup admin create --email <address> --display-name <name> [--password-file <path>]"
```

With the compose stack (the one-shot `admin` service,
[details](installation/compose.md#account-administration-admin)):

```sh
docker compose run --rm admin create --email you@example.com --display-name "Your Name"
```

The command prompts twice for the password on the terminal (or reads it
from `--password-file`, a file with owner-only permissions; there is no
password flag). The password policy applies: at least 12 characters, not a
common password, not containing your name or address, and strong enough.
The action is recorded in the audit log with actor `cli`, the OS user and
the hostname (see
[operations](operations.md#break-glass-account-administration)). Then sign
in to the web UI with that e-mail address and password.

Enrolling a second factor (a passkey, or TOTP with an authenticator app;
D-112) is strongly recommended for every admin. 2FA is **off by default**:
an admin turns it on for all accounts or per role (`updateTwoFactorPolicy`;
the users admin UI follows in task 6.8). Accounts of a role that requires it
then get a 7-day grace period (configurable, 0–90 days), after which an
account without a factor can do nothing but enroll one. Every login of an
account with a factor asks for it after the password, and sensitive actions
ask for it again (step-up, valid for 5 minutes). Store the recovery codes
shown at TOTP enrollment offline. Someone who lost every factor gets them
reset by another admin (`resetUserSecondFactors`) or on the host with
`bekup admin reset-2fa --email ...`.

Admins then create further accounts (users admin UI in task 6.8): each new
account gets a generated **temporary password** that is shown to the admin
once, expires after 72 hours and must be changed at the first login.
