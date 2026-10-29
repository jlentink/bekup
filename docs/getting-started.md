# Getting started

> **Status:** current for the end of M6. The web UI is the primary
> interface: everything below, from the first connection to a restore and
> the notification settings, is done in the browser. Only the first admin
> is created on the command line (D-113). The GraphQL API stays available
> for automation ([advanced: the GraphQL API](#advanced-the-graphql-api)).
> The page is completed in task 7.5.

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
  sign in on <http://localhost:8080> and enroll a second factor under
  **Profile → Two-factor** ([your first backup](#your-first-backup)).
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
   a connection, a destination and a backup job in the web UI
   ([your first backup](#your-first-backup)).

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
an admin turns it on for all accounts or per role in the web UI under
**Administration → Users → 2FA policy** (`/admin/two-factor`; API:
`updateTwoFactorPolicy`). Weakening the policy asks for an extra
confirmation and raises a security alert. Accounts of a role that requires it
then get a 7-day grace period (configurable, 0–90 days), after which an
account without a factor can do nothing but enroll one. Every login of an
account with a factor asks for it after the password, and sensitive actions
ask for it again (step-up, valid for 5 minutes). Store the recovery codes
shown at TOTP enrollment offline. Someone who lost every factor gets them
reset by another admin (user page → **Reset second factors**, or
`resetUserSecondFactors`; never on your own account) or on the host with
`bekup admin reset-2fa --email ...`.

Admins then create further accounts under **Administration → Users →
New user**: each new account gets a generated **temporary password** that is
shown to the admin once (copy it before closing the dialog; it cannot be
retrieved again), expires after 72 hours and must be changed at the first
login. The same page changes roles (revokes the account's sessions),
disables, unlocks, resets passwords, revokes sessions, deletes accounts
(the last active admin cannot be demoted, disabled or deleted) and assigns
backup jobs to accounts with the User role (owner grants, D-067). Every
change asks for a step-up and is audited.

## The dashboard

The home page (**Overview → Dashboard**) adapts to the role. On a fresh
install it walks staff through the first connection, destination and job.
Afterwards everyone sees the health of every visible job (last result,
next run, missed/overdue markers, warnings; backup owners only their own
jobs), the runs and restores in progress (live), failures and missed
backups of the last 7 days, the runs planned for the next 24 hours and
their unread alerts. Admins and maintainers also see the security findings
(unencrypted jobs, insecure transport, privileged target roles, expired or
expiring certificate pins, identity mismatches) and the bytes stored per
destination (bekup's own records of the files it wrote, D-196). Admins also see the system health: runner and Docker,
scheduler tick, work queues and certificate expiries (D-192). Every tile
links to its detail page.

## Your first backup

The steps below follow the order the dashboard's getting-started tile
suggests on a fresh install. Connections and destinations are managed by
admins; jobs by admins and maintainers. Actions that change security
settings ask you to confirm your identity again (step-up: a passkey or
authenticator code, valid for 5 minutes).

1. **Sign in and secure your account.** Sign in with the e-mail address and
   password of the first admin. Under **Profile → Two-factor** enroll a
   passkey or TOTP (store the recovery codes offline; they are shown
   once). Under **Profile → Notifications** verify your account's e-mail
   address if you want e-mail notifications.
2. **Add a connection.** **Infrastructure → Connections → New
   connection**: engine, host, port, the backup role's user name and
   password (write-only: it is never shown again), optionally an SSH
   tunnel through a bastion. The connection starts in the pinned TLS mode
   (D-034): open it and click **Pin certificate**. bekup captures the
   server's certificate chain through the runner and lists every
   certificate with its fingerprints; compare the leaf's SPKI fingerprint
   with the database server out of band (e.g. `openssl x509 -pubkey ...`
   on the server), tick the confirmation and click **Confirm pin**. For a
   tunnel, **Fetch and confirm host key** works the same way for the
   bastion's SSH key. Then **Test connection**: it shows the server
   version, client compatibility and the privilege verdict of the role (a
   superuser or otherwise privileged role is flagged). Details:
   [connections](guides/connections.md).
3. **Add a destination.** **Infrastructure → Destinations → New
   destination**: local disk (inside the disk root), S3 (endpoint, bucket,
   prefix, optional Object Lock) or SFTP (fetch and confirm the host key,
   or **Generate keypair** and install the public key). Run the test on
   the destination's page; it writes, reads back and deletes a small probe
   object.
   Details: [destinations](guides/destinations.md).
4. **Create a job.** **Backups → Jobs → New job**: the connection, all or
   selected databases (plus the PostgreSQL globals), the schedule (the
   builder shows the next occurrences in your time zone), retention, the
   destinations and the verification level. Encryption is on by default:
   with **Generate a passphrase (recommended)** bekup shows the generated
   passphrase **once** after saving; store it offline before you confirm
   ("I've saved it somewhere safe"). An age recipient (public key) can be
   chosen instead. Details: [jobs and schedules](guides/jobs-and-schedules.md),
   [encryption](guides/encryption.md).
5. **Run it.** On the job's page, **Run now (extra run)** queues a run at
   once without touching the schedule (**Start next planned run early**
   pulls the next planned run forward instead). The run appears in the
   job's run history; open it to watch its status and **live log**
   (**Follow** keeps the newest lines in view). When it has finished, the
   run page lists each database's files per destination with their
   verification and signature state and the completeness evidence.
6. **Download a backup** (admins and the job's backup owners; never
   maintainers). On the run page click **Download** next to a file, choose
   the raw (encrypted) file or a plain (decrypted) one, confirm your
   identity and click the one-time link within 60 seconds. The dialog also
   shows a command to restore the file by hand. Details:
   [downloads](guides/downloads.md).
7. **Restore.** **Backups → Restores → New restore** (or **Restore** next
   to a file on a run page) opens the wizard: the backup, the target connection and
   database, the options, a preview with every warning, the typed
   confirmation and a fresh passkey or authenticator check, then the live
   progress. Details: [restore](guides/restore.md).
8. **Set up notifications.** The bell in the top bar shows your in-app
   notifications live. Under **Profile → Notifications** add up to
   five webhooks (each signing key is shown once) and choose per event and
   per job what reaches which channel. Admins configure SMTP under
   **Administration → Settings** and team channels under **Administration →
   Shared channels**. Security alerts always reach every admin. Details:
   [notifications](guides/notifications.md).

The calendar (**Overview → Calendar**) shows the planned runs, the past
runs and when backups expire; **Administration → Audit log** shows who did
what.

## Advanced: the GraphQL API

Everything in the UI goes through the GraphQL API (D-105), which you can
use for automation or debugging. The simplest client is the browser
itself: sign in, open the developer console **on the bekup page** and send
POST + JSON requests to `/graphql`. The browser adds the session cookie and
the `Origin` / `Sec-Fetch-Site` headers the server requires (D-050, D-102);
requests from other origins or tools without the session cookie are
refused.

```js
const gql = (query, variables = {}) =>
  fetch("/graphql", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, variables }),
  }).then((r) => r.json());
```

For example, a daily 03:00 UTC backup of every database, keeping the
newest 14 backups (admin or maintainer; look up the IDs with
`gql("{ connections { id name } destinations { id name } }")`):

```graphql
mutation {
  createJob(input: {
    name: "Production DB"
    connectionId: "<connection id>"
    schedule: {kind: CRON, cron: "0 3 * * *", timezone: "UTC"}
    retention: {keepLast: 14}
    destinationIds: ["<destination id>"]
  }) {
    job { id slug nextRunAt scheduleDescription }
    generatedPassphrase   # shown once: store it offline
  }
}
```

Mutations that need a fresh step-up answer `STEP_UP_REQUIRED`; complete
it first, e.g.
`gql('mutation { completeStepUp(method: TOTP, code: "123456") { stepUpAt } }')`.
`mutation { runJobNow(id: "<job id>") { queuedAt } }` starts a run. The
full schema is in [reference/schema.graphql](reference/schema.graphql);
the guides list the operations of each area.
