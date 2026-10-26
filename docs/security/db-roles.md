# Backup and restore database roles

> **Status:** partial. The section on bekup's own app database
> (`bekup_owner` / `bekup_app`) is complete (task 0.4, D-054, D-092). The
> backup and restore roles for **target** databases are still to come: task
> 2.7a fills them in, tasks 4.2 and 4.3 complete them.

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
`bekup_app` use SCRAM-SHA-256 over the internal `backend` network. Existing
volumes: see [upgrades.md](../installation/upgrades.md#notes-for-existing-installations).
