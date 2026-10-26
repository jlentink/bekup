# Upgrades

> **Status:** partial (end of M0). The migration mechanics are implemented
> (task 0.4) and the compose `migrate` service exists (task 0.7). Image
> catalog deprecations (task 2.1), key and certificate rotation during
> upgrades (tasks 0.5, 0.6 wiring in M1/M2) and the release/verification
> steps (task 7.4) are added later; completed in task 7.5.

## Before you upgrade

1. **Read the changelog** (`CHANGELOG.md`, GitHub release notes) for the
   target version, especially breaking changes and new configuration keys.
   bekup rejects unknown TOML keys, so removed or renamed keys must be fixed
   before the new version starts.
2. **Back up bekup's own database** and keep the backup until the upgrade is
   verified. Migrations are forward-only in production, so this backup is
   the rollback path. For the compose stack, as the superuser over the
   container-local socket:
   ```sh
   docker compose exec -T postgres sh -c \
     'PGPASSWORD="$(cat /run/secrets/postgres_password)" pg_dump --host=/var/run/postgresql --username=postgres --format=custom --dbname=bekup' \
     >bekup-db-$(date +%F).dump
   ```
   (The password is read inside the container, so it never appears on a
   host command line.)
   Store the dump like a secret: it contains encrypted credentials and the
   audit log. Also make sure the master key and its escrow copy are
   available (they are needed to read the encrypted credentials after a
   restore; [key management](../security/key-management.md)).
3. Pin the new image by digest (`BEKUP_IMAGE=ghcr.io/jlentink/bekup:x.y.z@sha256:…`).
   Signature verification with `cosign verify` arrives with the release
   workflow (task 7.4).

## Applying the upgrade

```sh
docker compose pull
docker compose up -d
```

The one-shot **`migrate`** service runs first: it applies all pending
migrations as `bekup_owner` (`bekup migrate up`) and exits. `serve` starts
only after it has completed successfully (D-078). Only `migrate` holds the
owner credentials; `serve` connects as `bekup_app` and never changes the
schema. Inspect the result with:

```sh
docker compose logs migrate
docker compose run --rm migrate migrate status
```

How migrations behave (D-042, task 0.4):

- They are embedded goose SQL migrations, each in its own transaction.
- **Forward-only in production.** `bekup migrate down-one` exists for dev
  mode only. Released migrations are never edited; fixes ship as new
  migrations.
- An advisory lock on a dedicated connection ensures only one migrator runs
  at a time; other instances wait. If the process dies, the lock is
  released with its connection.
- The migrator refuses to run as any role other than `bekup_owner`, and as a
  superuser.
- River's queue schema is vendored as goose migrations, so River upgrades go
  through the same path.

If a migration fails, its transaction is rolled back, `migrate` exits
non-zero and `serve` does not start. Fix the cause (see the log) and run
`docker compose up -d` again, or restore the database backup and go back to
the previous image.

## Notes for existing installations

- **Postgres superuser over TCP.** New data volumes get `pg_hba.conf` rules
  that reject the bootstrap superuser on TCP, so it can only use the local
  socket. The init script only runs on an empty volume. For an existing
  volume, add the same lines at the **top** of `pg_hba.conf` (see
  `deploy/postgres/init/10-bekup-roles.sh`) and reload:
  ```sh
  docker compose exec postgres sh -c 'head -n 8 "$PGDATA/pg_hba.conf"'     # check the result
  docker compose exec postgres sh -c \
    'PGPASSWORD="$(cat /run/secrets/postgres_password)" psql --host=/var/run/postgresql -U postgres -c "SELECT pg_reload_conf()"'
  ```
- **Postgres major versions** of the app database (for example 17 → 18)
  need a dump and restore or `pg_upgrade`; changing the image tag alone does
  not upgrade a data directory.
- Catalog deprecations of legacy target engine versions are documented here
  by task 2.1.
