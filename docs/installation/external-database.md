# External PostgreSQL for bekup's own database

> **Status:** implemented (D-207, D-208, D-215, proposed): fields mode, `bekup db
> bootstrap`, `bekup db roles-sql`, `bekup migrate check` and single-account
> mode, verified against plain PostgreSQL 15, 16 and 17 with a non-superuser
> `CREATEROLE` + `CREATEDB` admin; the compose files `*.external-db.yaml`
> and the setup wizard configure it for a compose install, also for a
> database container on the same Docker host.

bekup keeps its users, jobs, run history, encrypted credentials and audit log
in its own PostgreSQL database. The bundled compose stack ships one. This page
is for running that database on a PostgreSQL you already operate: a managed
service (Amazon RDS / Aurora, Google Cloud SQL, Azure Database for PostgreSQL
Flexible Server) or your own server.

It is **not** about the databases bekup backs up (those are *connections*,
see [Connections](../guides/connections.md)).

## The easiest way: the setup wizard

Put your PostgreSQL's CA bundle (PEM) into an empty deployment directory,
for example as `db-ca.pem` (when you verify against a CA file, the
default; [TLS modes](#tls-modes)), and run the setup wizard
([compose.md](compose.md#install-with-the-setup-wizard)):

```sh
sudo docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -e BEKUP_IMAGE="$IMAGE" -e DOCKER_GID="$(stat -c %g /var/run/docker.sock)" \
  -v "$PWD":/deploy "$IMAGE" setup
```

Choose "existing: my PostgreSQL" and answer where it runs (a
[container on this Docker host](#database-container-on-this-docker-host):
its network and container name; or another server: its host name), port,
database name, the [TLS mode](#tls-modes) (for the default the CA bundle
file), an optional TLS server name (when the host is an IP address) and
how the roles come to exist (bootstrap, roles exist already, SQL for your
DBA, or the weakened single account). The wizard writes the `compose.yaml`
of the external-database form, `bekup.toml [database]` in fields mode, the
password files and the CA copy, and prints the next commands, for the
default choice:

```sh
docker compose run --rm migrate db bootstrap --admin-user <admin>
docker compose run --rm migrate migrate check
docker compose up -d
```

The same with flags (no terminal needed; `--db-auth` defaults to
`bootstrap`, `--db-location` to `remote`, `--db-tls` to `verify-ca-file`,
`--db-port` to 5432, `--db-name` to `bekup`):

```sh
sudo docker run --rm -i --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -e BEKUP_IMAGE="$IMAGE" -v "$PWD":/deploy "$IMAGE" setup init --non-interactive \
  --write-compose --variant traefik --domain backup.example.com \
  --tls-mode acme --acme-email ops@example.com \
  --database external --db-host bekup-db.example.internal --db-ca-file db-ca.pem \
  --db-auth bootstrap --db-admin-user postgres \
  --docker-gid "$(stat -c %g /var/run/docker.sock)" --ca-passphrase-stdin < ca-passphrase
```

Compose-first, save one of the `*.external-db.yaml` release files as
`compose.yaml` and run `docker compose run --rm setup`: setup recognizes the
external form and asks for the database. In every case setup runs without a
network and never connects to the database; the bootstrap and the checks
run in the `migrate` container, which has the configuration and the
password files.

What the compose files mount ([compose.md](compose.md#your-own-postgresql-external-database-files)):

| File below `secrets/` | Docker secret (target) | Services | `[database]` key |
|---|---|---|---|
| `bekup_database_password` | `bekup_database_password` | serve, admin, migrate | `password_file` |
| `bekup_database_owner_password` | `bekup_database_owner_password` | migrate | `owner_password_file` (empty file in single-account mode) |
| `bekup_database_ca.crt` | `bekup_db_ca_crt` (`bekup_db_ca.crt`) | serve, admin, migrate | `tls_ca_file` (`verify-ca-file`; an empty file for the other TLS choices) |

For a database on another server the compose `backend` network is not
internal in this form (it must reach your server), so restrict your
server's firewall and `pg_hba.conf` to the bekup host. For a
[database container](#database-container-on-this-docker-host) `backend`
stays internal. setup writes the `tls_mode` of your
[TLS choice](#tls-modes); a different mode is your explicit edit of
`bekup.toml`, which `setup check` reports.

## Database container on this Docker host

When your PostgreSQL runs as a container on the same Docker host, bekup
joins its Docker network instead of going through the host's network
(D-215). In the wizard answer "Where does your PostgreSQL run?" with "a
container on this Docker host", then enter its network and its container
name. With flags:

```sh
... setup init --non-interactive ... --database external \
  --db-location docker --db-network pg_default --db-host db \
  --db-tls verify-ca-file --db-ca-file db-ca.pem --db-auth bootstrap --db-admin-user postgres
```

- `--db-location` is `remote` (another server, the default) or `docker`.
  `--db-network` is required with `docker` and refused with `remote`. Find
  the network in a second terminal on the host (the setup container has no
  Docker access):
  `docker inspect <database container> --format '{{json .NetworkSettings.Networks}}'`.
  Docker's predefined `bridge`, `host` and `none` are refused (no container
  DNS on `bridge`).
- `--db-host` is the container's name or a network alias of it on that
  network. It must be a valid host name, so a name with `_` (for example
  `pg_db_1`) does not work: use its compose service name or an alias.
- With the [own reverse proxy](reverse-proxy.md#own-reverse-proxy) in join
  mode, the database network must not be your proxy's network (`serve`
  would join it twice, and a proxy network should not reach databases).
  Give the database a network of its own, for example `docker network
  create bekup-db` and that network in the database's compose file.

setup writes the network's name to `.env` (`BEKUP_DB_NETWORK`) and this
block into a new `compose.override.yaml` (never into `compose.yaml`; an
existing override must already carry it, otherwise `setup init` refuses
and prints it; [compose.md](compose.md#the-override-file-and-the-hardening-lint)):

```yaml
services:
  migrate:
    networks:
      database: {}
  admin:
    networks:
      database: {}
  serve:
    networks:
      database: {}
networks:
  backend:
    internal: true
  database:
    external: true
    name: ${BEKUP_DB_NETWORK:-bekup-db}
```

`migrate`, `serve` and `admin` (there is no separate worker service) join
the database's network as the external network `database`, and `backend`
stays **internal**: no route off this host is needed. The hardening lint
holds the block exactly and refuses every other service of the project on
`database`; `setup check` errors when `BEKUP_DB_NETWORK` differs from the
recorded network. The other containers on that network share it with
`migrate`, `serve` and `admin`; `serve` does not listen there (it binds its
`frontend` or link address), except in the own-proxy join mode, which
listens on every interface
([reverse-proxy.md](reverse-proxy.md#trust-by-container-name)).

## TLS modes

`--db-tls` (wizard: "TLS to your PostgreSQL") picks one of four choices
(D-215). Username and password (SCRAM-SHA-256, `require_auth`) work in
every mode; only the first two verify that bekup talks to your server.

| `--db-tls` | Wizard | `bekup.toml [database]` |
|---|---|---|
| `verify-ca-file` (default) | verify with a CA file | `tls_mode = "verify-full"`, `tls_ca_file = "/run/secrets/bekup_db_ca.crt"`: the copy of `--db-ca-file` in `secrets/bekup_database_ca.crt`; only that bundle is trusted |
| `verify-system` | verify with the system CA bundle | `tls_mode = "verify-full"`, `tls_ca_file = "/etc/ssl/certs/ca-certificates.crt"`: the bekup image's system bundle, so every public CA can vouch for the server (for a certificate from a public CA) |
| `require` | encrypted, but not verified (weakened) | `tls_mode = "require"`, `tls_ca_file = ""` |
| `disable` | no TLS (weakened) | `tls_mode = "disable"`, `tls_ca_file = ""` |

- `--db-ca-file` applies to `verify-ca-file` only, `--db-tls-server-name`
  to the two verifying choices. For the other choices
  `secrets/bekup_database_ca.crt` exists but is empty (compose still mounts
  the secret), and both `require` and `disable` write the explicit
  `tls_ca_file = ""` (the default path would verify the chain against
  that empty file).
- `verify-system` uses the bundle inside the image (the build fails when
  the runtime image lacks it), so it follows image upgrades; your
  provider's own bundle with `verify-ca-file` is narrower.
- `require`: encrypted, but the server's certificate is not verified, so
  the server's identity rests on the SCRAM exchange alone. SCRAM channel
  binding (D-104) still binds the login to the TLS channel bekup sees, so
  a relayed login fails.
- `disable`: all data crosses the network unencrypted, and without TLS
  there is no channel binding: anyone in the path can relay the SCRAM
  login to the real server and take over the session, including
  `bekup_owner`'s while `migrate` runs and the admin account's during
  `bekup db bootstrap`.
- **A private Docker network is no protection by itself.** Containers get
  `CAP_NET_RAW` by default, so any container on the database's Docker
  network can ARP-spoof the database's address and put itself in the
  path. Prefer `require` over `disable` even on a private Docker network,
  and a verifying choice wherever the server can present a certificate
  ([database container](#database-container-on-this-docker-host)).
- `require` and `disable` are **explicit weakenings**: the wizard shows the
  warning and needs an explicit confirmation, setup records the choice
  (`database_tls` in `bekup-setup.json`) and prints a warning, `setup check`
  reports it as a warning, and bekup keeps the D-100 treatment: a warning
  at every start, the audit entry `security.insecure_db_tls`, a daily alert
  to every admin and the badge on the admin configuration page
  ([App DB TLS](../security/app-db-tls.md#weakened-modes)).
- `bekup db bootstrap` refuses a connection below `verify-full` unless
  `--allow-insecure-admin-connection` is given (the administrator's
  password and the DDL would cross an unverified or unencrypted
  connection). For `require` and `disable` the bootstrap command setup
  prints carries that flag, with a warning; the `psql` line for your DBA
  (`dba-sql`) uses `sslmode=require` or `sslmode=disable` (and
  `sslmode=verify-full sslrootcert=system` for `verify-system`).

## Requirements

- **PostgreSQL 15, 16 or 17**, database encoding UTF8. Newer versions are
  warned about until tested.
- **TLS with a verifiable certificate.** bekup connects with `verify-full` by
  default and trusts only the CA file you name (or, `verify-system`, the
  image's system bundle). RSA or ECDSA certificates (not Ed25519). Without
  one, only the weakened [TLS modes](#tls-modes) `require` and `disable`
  remain.
- **SCRAM-SHA-256 passwords** (`password_encryption = scram-sha-256`, the
  default since PostgreSQL 14). bekup accepts no other authentication method
  and requires SCRAM channel binding over TLS (D-104), so MD5 hashes,
  `trust` and `cert` logins do not work.
- **A direct connection.** Poolers that terminate TLS (PgBouncer, RDS Proxy,
  Supavisor, Azure's built-in PgBouncer on port 6432, the Cloud SQL Auth
  Proxy on a plain local port) break the channel binding. Connect to the
  server itself.
- **The `citext` extension** (contrib). It is a trusted extension, so the
  database owner creates it during the first migration; some providers need
  it allow-listed first (Azure).
- **A dedicated database per bekup instance.** The role names `bekup_owner`
  and `bekup_app` are fixed (released migrations grant to them by name), so
  one cluster hosts one bekup.
- **A firewall** that lets only the hosts of `serve`, `worker` and `migrate`
  reach the port.

bekup does **not** back up its own database: use your provider's snapshots
or your own backups, and keep the master key (`bekup keys generate`) in
escrow separately. A database backup without the master key cannot decrypt
the stored credentials; the master key without the database is useless.

## How bekup connects

Set `database.host` and bekup switches to **fields mode**: the connection is
built from the keys below, never from a connection string, and every `PG*`
environment variable is ignored. Fields mode and the URL files of the
bundled stack are mutually exclusive; mixing them is a configuration error.

```toml
[database]
host = "bekup-db.example.internal"   # or an IP address (see tls_server_name)
port = 5432
name = "bekup"
user = "bekup_app"                                       # fixed
password_file = "/run/secrets/bekup_database_password"
owner_user = "bekup_owner"                               # fixed
owner_password_file = "/run/secrets/bekup_database_owner_password" # migrate only
tls_mode = "verify-full"
tls_ca_file = "/run/secrets/bekup_db_ca.crt"             # the provider's CA bundle
# tls_server_name = "bekup-db.example.internal"           # when host is an IP
```

- `password_file` / `owner_password_file` hold one password each (secret-file
  rules: owned by the user running bekup or root, not group/world-writable,
  not world-readable). Use long random printable-ASCII passwords, at least 16
  characters (bekup's setup generates them); bootstrap refuses anything else.
- Mount `owner_password_file` only where migrations run.
- `tls_ca_file` names a PEM bundle. Only its certificates are trusted, never
  the built-in system roots. For a provider whose certificate comes from a
  public CA you may name the system bundle file
  (`/etc/ssl/certs/ca-certificates.crt`; setup's `--db-tls verify-system`),
  but the provider's own bundle is narrower.

Full key reference: [configuration.md](../configuration.md#database).

## Step 1: create the roles and the database

### With an admin account: `bekup db bootstrap` (recommended)

Run it once where `bekup.toml` and the password files are available (for the
compose stack: in the `migrate` container,
`docker compose run --rm migrate db bootstrap --admin-user <admin>`; add
`-v "$PWD/admin-pw:/run/admin-pw:ro"` and `--admin-password-file
/run/admin-pw` for a password file owned by UID 65532 with mode 0400). It connects with the configured
TLS policy as an administrator, creates the two login roles with SCRAM
verifiers of your configured passwords (the passwords themselves never leave
the machine), creates the database owned by `bekup_owner`, revokes `PUBLIC`'s
privileges on it, grants `CONNECT` to `bekup_app`, creates `citext` if the
database owner could not, and then checks everything:

```sh
bekup db bootstrap --admin-user postgres --admin-password-file /run/secrets/db_admin_password
# or omit --admin-password-file to type the password (no echo)
bekup migrate up
```

- The admin account needs `CREATEROLE` and `CREATEDB`, or is the provider's
  admin (`rds_superuser`, `cloudsqlsuperuser`, `azure_pg_admin` members).
  A superuser works too. Its password is used for this one connection only,
  never stored or logged; delete the file afterwards.
- `--admin-database` (default `postgres`), `--admin-host`/`--admin-port`
  (default `database.host`/`port`) and `--tls-mode`/`--tls-ca-file`/
  `--tls-server-name` (override the config) adapt the admin connection.
  The admin connection needs `tls_mode = "verify-full"`: with `require` or
  `disable` (configured or as `--tls-mode`) bootstrap refuses unless you add
  `--allow-insecure-admin-connection`, and then prints a loud warning.
- **Existing roles are adopted only explicitly.** When `bekup_owner` or
  `bekup_app` already exist (an earlier bootstrap, your DBA, or someone
  else on a shared server), bootstrap stops and asks you to review them and
  re-run with `--adopt-existing-roles`. Adopted roles are checked (no
  dangerous attributes or memberships, `LOGIN`), never silently changed; an
  existing database must already be owned by `bekup_owner`. Re-running a
  completed bootstrap therefore needs `--adopt-existing-roles` too.
  `--reset-passwords` (with `--adopt-existing-roles`) sets the passwords of
  existing roles to the configured files (for example after rotating them,
  or to replace MD5 hashes). `--dry-run` only lists what it would do.
- **Nobody else may hold bekup's roles.** Bootstrap refuses when another
  role is a member of `bekup_owner` or `bekup_app` or holds `ADMIN OPTION`
  on them (it could take the roles over and, through `bekup_owner`, alter
  the audit log), and prints the `REVOKE` statements. Only the admin running
  bootstrap may hold them: PostgreSQL 16+ gives the account that creates a
  role `ADMIN OPTION` on it, so run later bootstraps with the same admin.
  Superuser members are only warned about. `bekup migrate check` warns about
  every such membership (it cannot tell which admin ran bootstrap).
- The admin gets `bekup_owner` temporarily (PostgreSQL requires it for
  `CREATE DATABASE ... OWNER`) and loses it again at the end, unless it held
  it before. On PostgreSQL 16+ granting an existing role needs `ADMIN
  OPTION` on it (`CREATEROLE` alone is not enough); bootstrap checks this
  first and says what to do. The revoke runs even when bootstrap is
  interrupted (Ctrl+C) or its connection died (it reconnects); if it still
  fails, bootstrap prints **ACTION REQUIRED: manual REVOKE needed** with the
  exact statement, `REVOKE bekup_owner FROM <admin>;`, to run as an
  administrator.
- After the checks it prints the next step. Before the first migration there
  is no audit log yet, so the printed report is the record of the bootstrap;
  a later bootstrap that changes something of a migrated installation is
  recorded as `cli.db.bootstrap` (and alerted to every admin, like every CLI
  action).
- Servers that log DDL (`log_statement = ddl`, pgaudit) record the SCRAM
  verifiers of `CREATE/ALTER ROLE ... PASSWORD`. Verifiers are not passwords,
  but with weak passwords they allow offline guessing: use long random ones.

### Without an admin account at hand: `bekup db roles-sql` for your DBA

```sh
bekup db roles-sql --database bekup \
  --owner-password-file bekup_owner_password --app-password-file bekup_app_password \
  > bekup-roles.sql        # SCRAM verifiers, never the passwords: keep it private
# or: bekup db roles-sql --passwords prompt > bekup-roles.sql   (psql asks for them)
```

The DBA runs it with psql as an account with `CREATEROLE` + `CREATEDB`:

```sh
psql -v ON_ERROR_STOP=1 --no-psqlrc -f bekup-roles.sql \
  "host=<host> dbname=postgres user=<admin> sslmode=verify-full sslrootcert=<ca.pem>"
```

Add `--create-extensions` when citext is not trusted on the server (the
script then creates it as the admin). The script contains exactly the
statements bootstrap would run. It grants `bekup_owner` to the executing
account for its duration and revokes it as its last statement: if psql stops
with an error in between, run `REVOKE bekup_owner FROM CURRENT_USER;` as the
same account (the script's header says so too).

### Single-account mode (weakened, opt-in)

Some providers hand out one account and no way to create roles for others.
With `database.single_account = true` (and `user`/`password_file` naming that
account; no owner keys) bekup uses that one account for migrations and the
app. The account still needs `CREATEROLE` and `CREATEDB` once:
`bekup db bootstrap` (without `--admin-*`) creates `bekup_owner` and
`bekup_app` as `NOLOGIN` roles, grants both to the account and creates the
database; every bekup session then switches with `SET ROLE` to the role it
needs, so the schema and the audit-log protections are the same. Once the
roles exist the account may drop `CREATEROLE`: a re-run of bootstrap (with
`--adopt-existing-roles`) does not grant the roles again when the account
already holds them.

**Understand the risk:** the account can switch back at any time, so whoever
takes over serve or worker can change the schema, disable the audit-log
triggers and rewrite the audit history. bekup logs a warning at every start,
records `security.insecure_db_roles`, alerts every admin daily and shows a
badge on the admin configuration page. A superuser account is refused. Use
the two-role setup whenever you can.

## Step 2: verify and migrate

```sh
bekup migrate check    # changes nothing
bekup migrate up       # runs the same preflight first, then migrates
```

In the compose stack: `docker compose run --rm migrate migrate check`; the
`migrate` service runs `migrate up` on every `docker compose up -d`.

`migrate check` reports the server version, the database owner, the citext
state, the role checks and the TLS facts (TLS version, chain and host
verified, channel binding), and fails with the fix for anything that would
break a migration. It also warns when `bekup_app` may connect to other
databases (usually through `PUBLIC`'s default `CONNECT`) and when `ALTER
ROLE/DATABASE ... SET` entries apply to bekup's roles. serve repeats the role
checks at every start and logs the TLS facts (`app database connected`).

## Provider notes

### Amazon RDS and Aurora PostgreSQL

- CA bundle: the global bundle
  `https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem` (or
  your region's bundle) as `tls_ca_file`; `host` = the instance or cluster
  endpoint (its name is in the certificate).
- Set `rds.force_ssl = 1` in the parameter group and keep
  `password_encryption = scram-sha-256`.
- Bootstrap with the master user (member of `rds_superuser`, with
  `CREATEROLE` and `CREATEDB`).
- Do not put RDS Proxy in front of bekup (it breaks the channel binding).

### Google Cloud SQL for PostgreSQL

- `verify-full` needs a server certificate that names the host. Use an
  instance with the shared CA (CAS) server CA mode, for example
  `gcloud sql instances patch <instance> --server-ca-mode=GOOGLE_MANAGED_CAS_CA`
  (check Google's documentation for your setup), download its CA chain
  (`gcloud sql instances describe <instance> --format="value(serverCaCert.cert)"`
  or the console) as `tls_ca_file`, and use the instance's DNS name as `host`
  or as `tls_server_name` with the private IP as `host`. The per-instance CA
  mode issues certificates without the host name: only `tls_mode = "require"`
  with the CA (chain verified, host name not) works there, which bekup warns
  about at every start.
- Require TLS on the instance (`--ssl-mode=ENCRYPTED_ONLY`).
- Bootstrap with the `postgres` user (member of `cloudsqlsuperuser`). Do not
  create the bekup roles in the console or with `gcloud sql users create`:
  such users become members of `cloudsqlsuperuser`, which bekup refuses.
- Connect directly over the private IP; the Cloud SQL Auth Proxy's local port
  is plain text and cannot carry the channel binding.

### Azure Database for PostgreSQL Flexible Server

- Allow-list the extension first: add `CITEXT` to the server parameter
  `azure.extensions` (otherwise `migrate check` reports it).
- `host` = `<server>.postgres.database.azure.com`, port 5432 (not the
  built-in PgBouncer on 6432). Keep `require_secure_transport = ON`.
- The server certificate comes from public CAs (DigiCert Global Root G2,
  Microsoft RSA Root Certificate Authority 2017): download both roots into
  one PEM file as `tls_ca_file`, or name the system bundle file.
- Bootstrap with the server admin (member of `azure_pg_admin`).

### Self-hosted PostgreSQL

- `ssl = on` with a certificate whose SANs contain the name (or IP) bekup
  connects to; `ssl_min_protocol_version = 'TLSv1.2'`;
  `password_encryption = 'scram-sha-256'`.
- `pg_hba.conf`, before broader rules:

  ```
  hostssl    bekup  bekup_owner,bekup_app  10.0.0.0/24  scram-sha-256
  hostnossl  all    bekup_owner,bekup_app  all          reject
  ```

- Bootstrap with any role that has `CREATEROLE` and `CREATEDB`, or with a
  superuser.

## Limits

- One bekup per PostgreSQL cluster (fixed role names).
- No connection poolers, no Unix sockets in fields mode, no `md5`/`trust`/
  `cert` authentication.
- `bekup_app` has a connection limit of 100; keep `database.max_conns` of all
  serve and worker processes below it.
- Backups, upgrades, high availability and monitoring of the server are the
  operator's job.
