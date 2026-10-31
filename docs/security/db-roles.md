# Backup and restore database roles

> **Status:** partial. The section on bekup's own app database
> (`bekup_owner` / `bekup_app`) is complete (task 0.4, D-054, D-092; external
> databases and single-account mode: D-207). The
> backup roles for **target** databases are still to come (task 2.7a); the
> [restore roles](#restore-roles-for-target-databases) section covers the
> privileged-target check (task 4.8, D-168).

Recommended dedicated, non-superuser roles for backing up and restoring PostgreSQL, MySQL and MariaDB databases, the privileges each needs, and the trade-offs for globals (role) dumps on managed services.

## bekup's own app database: `bekup_owner` and `bekup_app`

bekup's own Postgres database uses two least-privilege roles with **fixed
names** (released migrations grant to them literally, so there is one bekup
per cluster; D-207). They are created by the deploy init script
(`deploy/postgres/init/10-bekup-roles.sh` for the bundled compose stack) or,
on an external PostgreSQL, by `bekup db bootstrap` / the script of
`bekup db roles-sql` ([External database](../installation/external-database.md)),
**never by a migration**:

| Role | Used by | May |
|---|---|---|
| `bekup_owner` | `bekup migrate` only (`database.owner_url_file`, or `owner_user` + `owner_password_file` in fields mode) | Owns the `bekup` database, the `public` schema and every object; runs DDL |
| `bekup_app` | `bekup serve` / `bekup worker` (`database.url_file`, or `user` + `password_file`) | `CONNECT`; `USAGE` on `public`; `SELECT, INSERT, UPDATE, DELETE` on tables; sequences; `EXECUTE` on bekup/River functions. **On `audit_log` only `INSERT` and `SELECT`.** |

Requirements, checked by the first migration, the role check on every
`bekup migrate` run and `bekup migrate check` (D-099, D-207):

- PostgreSQL ≥ 15 (15 to 17 are tested), database encoding UTF8.
- Migrations connect as `bekup_owner`; the migrator refuses any other role.
- The session must have **logged in** as the role itself: a role setting
  (`ALTER ROLE x SET role = bekup_app`) cannot make another login, possibly a
  superuser, pass as `bekup_app` or `bekup_owner` (`session_user` is checked,
  D-207). On PostgreSQL 16+ the authenticated identity is compared too
  (`SYSTEM_USER` must be `scram-sha-256:<role>`, or `md5:<role>` for an `md5`
  pg_hba entry, which still runs SCRAM for a SCRAM verifier): a superuser
  that switched its session with `SET SESSION AUTHORIZATION` or a
  `session_authorization` setting is refused. PostgreSQL 15 has no
  `SYSTEM_USER`, so there only `session_user` is checked. serve applies the
  same check at startup.
- Members of `bekup_owner`/`bekup_app` and holders of `ADMIN OPTION` on them
  can act as or take over bekup's roles. `bekup db bootstrap` refuses every
  such membership except the admin running it (PostgreSQL 16+ gives a role's
  creator `ADMIN OPTION`) and, in single-account mode, the account; superuser
  members are warned about. `bekup migrate check` warns about every
  membership other than the single-account login. Existing roles are only
  adopted with `--adopt-existing-roles`.
- Neither role is `SUPERUSER`, `BYPASSRLS`, `CREATEROLE` or `REPLICATION` or
  a member (directly or through other roles) of a role with one of these
  attributes or of a predefined `pg_*` role; `bekup_app` is not a member of
  `bekup_owner`. Managed admin roles (`rds_superuser`, `cloudsqlsuperuser`,
  `azure_pg_admin`) have `CREATEROLE`, so a bekup role that is a member of one
  is refused.
- The database is owned by `bekup_owner`, and `bekup_owner` can create
  objects in `public`.
- The `citext` extension is installed or creatable by `bekup_owner` (a
  trusted extension since PostgreSQL 13).

Every session uses `search_path=public` as a startup parameter and accepts
only SCRAM-SHA-256 authentication (D-207); with TLS, SCRAM channel binding is
required (D-104), so the roles need SCRAM password verifiers, never MD5.

On an external PostgreSQL, `bekup_owner` is created with `INHERIT`: as the
database owner it must get the privileges of `pg_database_owner`, which owns
the `public` schema of a new database, and PostgreSQL 15 applies `NOINHERIT`
to that implicit membership as well (only a superuser could hand the schema
over, which the bundled init script does). `INHERIT` grants nothing a
membership would not allow through `SET ROLE` anyway, and the checks above
refuse every dangerous membership. `bekup_app` stays `NOINHERIT`.

Manual setup (outside the compose stack), as a superuser (on an external
server without a superuser use `bekup db bootstrap` or `bekup db roles-sql`):

```sql
CREATE ROLE bekup_owner WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS NOINHERIT PASSWORD '...';
CREATE ROLE bekup_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS NOINHERIT PASSWORD '...';
CREATE DATABASE bekup OWNER bekup_owner ENCODING 'UTF8' TEMPLATE template0;
\c bekup
ALTER SCHEMA public OWNER TO bekup_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
```

What the migrations set up (`internal/db/migrations/00001_roles_and_privileges.sql`):

- `REVOKE ALL ON DATABASE <db> FROM PUBLIC`, `GRANT CONNECT` to `bekup_app`
  only (no `TEMPORARY`: the app cannot create temp objects that could shadow
  functions).
- `REVOKE ALL ON SCHEMA public FROM PUBLIC`, `GRANT USAGE` to `bekup_app`: the
  app cannot create objects.
- Default privileges for objects created by `bekup_owner`: DML on tables,
  `USAGE, SELECT` on sequences and `EXECUTE` on functions for `bekup_app`;
  `EXECUTE` is no longer granted to `PUBLIC`. `TRUNCATE`, `REFERENCES` and
  `TRIGGER` are never granted.
- `audit_log`: `UPDATE, DELETE, TRUNCATE` revoked from `bekup_app` and
  `PUBLIC`, plus triggers that raise on `UPDATE`, `DELETE` and `TRUNCATE` for
  every role (including the owner). The audit sequence and trigger functions
  are not accessible to `bekup_app`; the chain trigger is `SECURITY DEFINER`
  (owned by `bekup_owner`, `search_path` pinned to `pg_catalog, pg_temp`). See
  [audit.md](audit.md).
- The goose version table is created before the default privileges exist, so
  `bekup_app` has no access to it.

Keep `database.owner_url_file` / `owner_password_file` out of containers that
never migrate. A compromised `bekup_app` can read and change app data but
cannot change the schema, rewrite or delete audit history, or alter migration
state.

### Single-account mode (weakened)

With `database.single_account = true` (fields mode only; D-207) bekup uses
**one** database account for migrations and the app, for providers that hand
out a single account. `bekup db bootstrap` creates `bekup_owner` and
`bekup_app` as `NOLOGIN` roles and grants both to that account; every
migrator session runs `SET ROLE bekup_owner` and every app session
`SET ROLE bekup_app` right after connecting, so the schema, the grants and
the audit-log protections are exactly those of the two-role setup and all
checks above still run against the two roles. The account itself must not be
a superuser (refused); its other dangerous attributes and memberships are
listed in the startup warning, the audit entry and `bekup migrate check`.

**The weakening:** the account can always switch back (`RESET ROLE`) or to
`bekup_owner`, so anyone who takes over serve or worker can change the
schema, disable the audit-log triggers and rewrite the audit history. bekup
therefore logs a warning at every start, records
`security.insecure_db_roles` (fail closed) and alerts every admin daily, and
the admin configuration page shows a permanent badge. Prefer the two login
roles whenever the provider allows creating roles.

In the compose stack, the init script also puts `pg_hba.conf` rules first
that reject the bootstrap superuser (`postgres`) on every TCP connection, so
it can only log in over the container-local Unix socket; `bekup_owner` and
`bekup_app` use SCRAM-SHA-256 over the internal `backend` network **only over
TLS** (`hostnossl ... reject`, D-100; see [App DB TLS](app-db-tls.md)). Existing
volumes: see [upgrades.md](../installation/upgrades.md#notes-for-existing-installations).

## Restore roles for target databases

SQL inside a dump runs with the rights of the connection's role when it is
restored. Give connections that are used as **restore targets** a dedicated
role without server-level privileges, so a malicious dump cannot reach
beyond the restored database (D-068). The restore account must:

- be **limited to the target database(s)**: schema-level grants on
  MySQL/MariaDB, `CONNECT` on the target databases only and no
  `pg_read_all_data` / `pg_write_all_data` on PostgreSQL;
- **not be the backup account**: the backup role reads every database it
  backs up, so a dump restored with it could read or change all of them.
  Use a separate connection (same host, different user) as the restore
  target. bekup checks the role on every restore
request, again when the queued restore starts (D-173), and on
`testConnection`: a privileged role (or one whose privileges
it cannot determine) makes restores into the connection admin-only with an
explicit acknowledgement, audited and alerted (D-168; see
[Privileged restore targets](../guides/restore.md#privileged-restore-targets)).

**PostgreSQL**: a role with `LOGIN` and `CREATEDB` (new databases, and the
temporary database of an overwrite; the swap renames databases it owns), no
`SUPERUSER`, `CREATEROLE`, `REPLICATION` or `BYPASSRLS`, and no membership in
`pg_execute_server_program`, `pg_read_server_files`, `pg_write_server_files`,
`pg_signal_backend`, `pg_read_all_stats` (also through `pg_monitor`),
`pg_create_subscription`, `pg_read_all_data` or `pg_write_all_data` (they
reach every database the role can connect to):

```sql
CREATE ROLE bekup_restore LOGIN CREATEDB PASSWORD '...';
-- To overwrite an existing database, the role must own it:
ALTER DATABASE app OWNER TO bekup_restore;
```

Grant it `CONNECT` only on the databases it restores (bekup does not check
`CONNECT` on other databases; revoke `PUBLIC`'s default `CONNECT` on
databases that should stay out of reach). Objects are restored owned by
this role (`keepOwners` off). Because of that, bekup hardens every
restored database before it becomes visible (D-171): `SECURITY DEFINER`
functions become `SECURITY INVOKER` (otherwise every caller would run them
with this role's rights) and `PUBLIC` gets no `CONNECT` / `TEMPORARY`;
grant access to the application roles after a restore (see
[Hardening of the restored database](../guides/restore.md#hardening-of-the-restored-database)). Globals
restores create and change roles and need a privileged role: use a separate
admin-only connection for them.

**MySQL / MariaDB**: an account with the data and schema privileges on the
schemas it restores, without global administrative privileges (`SUPER`,
`FILE`, `PROCESS`, `RELOAD`, `SHUTDOWN`, `CREATE USER`, `GRANT OPTION`,
`SET_USER_ID`, `SYSTEM_USER`, `*_ADMIN` dynamic privileges, MariaDB's
`SET USER` / `* ADMIN` privileges), without `PROXY` grants or write access
to the `mysql` schema, and **without roles** (bekup cannot see a role's
privileges, so an account with roles counts as unknown = privileged):

```sql
CREATE USER 'bekup_restore'@'%' IDENTIFIED BY '...' REQUIRE SSL;
GRANT ALL PRIVILEGES ON `app`.* TO 'bekup_restore'@'%';
GRANT ALL PRIVILEGES ON `app\_restored`.* TO 'bekup_restore'@'%';
```

Grant data and schema privileges **on the restored schemas only**, never on
`*.*`: with `SELECT` on `*.*` a dump's SQL could copy other schemas' data
into the restored one, with `INSERT`/`UPDATE`/`DELETE`/`DROP` on `*.*` it
could change or drop them. Global (`*.*`) data and schema privileges
therefore count as privileged (reason `global_data:<NAME>`): restores into
such a connection need an admin and the acknowledgement. Only `USAGE`,
`SHOW DATABASES`, `REPLICATION CLIENT` and a few monitoring privileges are
fine globally.
Views, routines and triggers in a dump carry a `DEFINER`; without `SUPER` /
`SET_USER_ID` (`SET USER` on MariaDB) a definer other than the restore
account is refused, so such dumps only restore as an admin into an
acknowledged privileged connection.
