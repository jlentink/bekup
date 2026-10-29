# Upgrades

> **Status:** partial (end of M6). The migration mechanics are implemented
> (task 0.4), the compose `migrate` service exists (task 0.7), `serve`
> refuses to start on an outdated schema (task 1.1), the postgres
> certificate rotation is described below (task 1.0, D-104), and so are
> image catalog changes (task 2.1). Runner certificate and CA rotation are in
> [mTLS certificates](../security/mtls-certs.md). Re-encryption after a
> keyring rotation and the release/verification steps (task 7.4) are added
> later; completed in task 7.5.

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
- **App DB TLS (D-100).** The stack now runs the app database with TLS and
  `serve`/`migrate` verify it (`tls_mode = "verify-full"`). For an existing
  installation:
  1. Issue the `postgres` certificate. A CA created before this release
     only permits the runner names and has path length 0 (no room for the
     postgres certificate's ECDSA issuer, D-104), so first rotate the CA (with
     `secrets/certs/ca/` restored; restart the stack after each phase):
     ```sh
     docker compose --profile setup run --rm certs-init certs rotate --dir=/certs --ca stage    # adds "postgres"
     docker compose --profile setup run --rm certs-init certs rotate --dir=/certs --ca promote
     docker compose --profile setup run --rm certs-init certs rotate --dir=/certs --component postgres
     docker compose --profile setup run --rm certs-init certs rotate --dir=/certs --ca finish
     sudo chown -R 70:70 secrets/certs/postgres     # Linux
     ```
     (With a CA that already permits `postgres`, the `--component postgres`
     step alone is enough.)
  2. Take the new `docker-compose.yml` (entrypoint wrapper, TLS tmpfs and
     the `postgres_crt`, `postgres_key`, `bekup_db_ca_crt` secrets).
  3. Remove `sslmode=...` from `secrets/bekup_database_url` and
     `secrets/bekup_database_owner_url` (a weaker value is ignored, a
     stricter one such as `verify-full` is fine but redundant).
  4. Existing data volumes keep accepting the bekup roles without TLS
     until you add the rule from `deploy/postgres/init/10-bekup-roles.sh`
     directly **below** the superuser block at the top of `pg_hba.conf`,
     then reload as shown above:
     ```sh
     docker compose exec postgres sh -c 'f="$PGDATA/pg_hba.conf"; { head -n 5 "$f";
       printf "%s\n" "# bekup: bekup roles only over TLS (D-100)" "hostnossl  all  bekup_owner,bekup_app  all  reject" "";
       tail -n +6 "$f"; } > "$f.new" && chmod 0600 "$f.new" && mv "$f.new" "$f"'
     docker compose exec postgres sh -c 'head -n 10 "$PGDATA/pg_hba.conf"'   # check the result
     ```
     (`head -n 5` assumes the four-line superuser block plus its blank
     line; check before reloading.)
  Dev setups: re-run `deploy/dev/gen-dev-keys.sh` (it issues the postgres
  certificate, regenerating the throwaway dev PKI if its CA is too old, and
  rewrites the URLs) and reset the dev volumes with `docker compose ... down -v`.
- **Postgres major versions** of the app database (for example 17 → 18)
  need a dump and restore or `pg_upgrade`; changing the image tag alone does
  not upgrade a data directory.
- **Engine image catalog.** The dump/restore images are embedded in the
  binary (`internal/engine/catalog.toml`, digest-pinned), so an upgrade can
  update, add, flag as `legacy` or remove a release series. Read the
  CHANGELOG for catalog changes before upgrading. The version rules in
  [architecture.md](../architecture.md#engines-internalengine-task-21)
  decide which client serves which server (PostgreSQL needs a client major
  ≥ the server major, otherwise the run is blocked). A removed series can
  be added back with `[images] extra_file` in `runner.toml` if it still
  meets the client floors.
