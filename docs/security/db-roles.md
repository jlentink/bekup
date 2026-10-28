# Backup and restore database roles

> **Status:** partial. The section on bekup's own app database
> (`bekup_owner` / `bekup_app`) is complete (task 0.4, D-054, D-092). The
> backup roles for **target** databases are still to come (task 2.7a); the
> [restore roles](#restore-roles-for-target-databases) section covers the
> privileged-target check (task 4.8, D-168).

Recommended dedicated, non-superuser roles for backing up and restoring PostgreSQL, MySQL and MariaDB databases, the privileges each needs, and the trade-offs for globals (role) dumps on managed services.

## bekup's own app database: `bekup_owner` and `bekup_app`

bekup's own Postgres database uses two least-privilege roles. They are created
by the deploy init script (`deploy/postgres/init/10-bekup-roles.sh` for the
compose stack), **never by a migration**:

| Role | Used by | May |
|---|---|---|
| `bekup_owner` | `bekup migrate` only (`database.owner_url_file`) | Owns the `bekup` database, the `public` schema and every object; runs DDL |
| `bekup_app` | `bekup serve` / `bekup worker` (`database.url_file`) | `CONNECT`; `USAGE` on `public`; `SELECT, INSERT, UPDATE, DELETE` on tables; sequences; `EXECUTE` on bekup/River functions. **On `audit_log` only `INSERT` and `SELECT`.** |

Requirements, checked by the first migration (and the owner check on every
`bekup migrate` run):

- PostgreSQL ≥ 15, database encoding UTF8.
- Migrations connect as `bekup_owner`; the migrator refuses any other role
  and superusers.
- Neither role is `SUPERUSER`, `BYPASSRLS`, `CREATEROLE` or `REPLICATION`;
  `bekup_app` is not a member of `bekup_owner`.
- The database is owned by `bekup_owner`.

Manual setup (outside the compose stack), as a superuser:

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

Keep `database.owner_url_file` out of containers that never migrate. A
compromised `bekup_app` can read and change app data but cannot change the
schema, rewrite or delete audit history, or alter migration state.

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
