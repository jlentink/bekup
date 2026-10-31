# Upgrades

> **Status:** partial (end of M6). The migration mechanics are implemented
> (task 0.4), the compose `migrate` service exists (task 0.7), `serve`
> refuses to start on an outdated schema (task 1.1), the postgres
> certificate rotation is described below (task 1.0, D-104), and so are
> image catalog changes (task 2.1). Runner certificate and CA rotation are in
> [mTLS certificates](../security/mtls-certs.md). Release images are
> verified with `cosign verify` (task 7.4, D-204). Re-encryption after a
> keyring rotation is added later; completed in task 7.5.

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
3. **Get and verify the new release files.** Compose-first installs need
   only the new compose file (it pins the new image by digest; see below).
   Setup-first installs pin the new image by digest, taken from the GitHub
   release notes of the version (`ghcr.io/jlentink/bekup:x.y.z@sha256:…`),
   and **verify its signature** before anything runs it:
   ```sh
   NEW=ghcr.io/jlentink/bekup:x.y.z@sha256:<digest from the release notes>
   docker pull "$NEW"
   cosign verify \
     --certificate-identity-regexp '^https://github\.com/jlentink/bekup/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     "$NEW"
   ```
   The release notes repeat this command with the exact identity of the
   version; `gh attestation verify "oci://$NEW" --repo jlentink/bekup`
   checks the build provenance instead
   ([verifying release images](compose.md#verifying-release-images)).

## Applying the upgrade

### Container-only installs (`bekup setup`)

**Compose-first** (the default since D-205): `compose.yaml` is the release
file you downloaded; it pins the image in its `x-bekup-image` line. Upgrade
by replacing it with the new release's file of the **same variant**, then
let the new image's setup service do the rest:

```sh
cd /opt/bekup
# the same asset you installed: compose.yaml, compose.traefik-external.yaml, compose.npm.yaml
# or their *.external-db.yaml forms
sudo curl -fsSLo compose.yaml.new https://github.com/jlentink/bekup/releases/download/vX.Y.Z/compose.yaml
gh attestation verify compose.yaml.new --repo jlentink/bekup
diff -u compose.yaml compose.yaml.new     # review: the image line and any service changes
sudo mv compose.yaml.new compose.yaml
docker compose run --rm setup upgrade
docker compose pull
docker compose up -d
docker compose -f compose.yaml run --rm setup check
```

Local changes live in `compose.override.yaml`, so replacing `compose.yaml`
loses nothing. If you customized `compose.yaml` itself, re-apply the change
to the new file (the [hardening lint](compose.md#the-override-file-and-the-hardening-lint)
checks it); better move it into the override.

`setup upgrade` (D-203, D-205) in a compose-first deployment:

- takes the new image from `compose.yaml` (the setup service's
  `BEKUP_IMAGE` must match) and **never writes `compose.yaml`**;
  `--write-compose` is refused;
- refuses a variant switch (for example from `npm` to `traefik`): set up a
  new directory instead and move the data;
- refuses a switch between the bundled database and your own PostgreSQL
  (a release file with or without `.external-db`, D-208): that moves bekup's
  database, which is a dump and restore into a new deployment directory,
  not an upgrade (keep the master key: the stored credentials need it);
- rewrites only the generated files: `deploy/**` (assets the new version no
  longer ships are removed), `reference/*.example.toml` and
  `bekup-setup.json`;
- never touches `.env` (except the `BEKUP_IMAGE` line with the committed
  file, below), `bekup.toml`, `runner.toml`, the override file or an
  existing secret; it re-validates the TOML files with the new version's
  validators first (fix them before upgrading: unknown keys are rejected)
  and generates only secrets that are new in the ownership manifest;
- refuses when a generated file was edited locally; `--force` keeps a copy
  as `<file>.bak-<timestamp>` and replaces it;
- refuses a version older than the one recorded in `bekup-setup.json`
  (`--allow-downgrade` only after restoring a matching database backup) and
  an image tag that names another version than the binary
  (`--allow-version-mismatch`).

Until you run `setup upgrade`, `setup check` reports the newer image in
`compose.yaml` as a warning; an older image is an error.

**Guided on a terminal** (D-208): when `setup upgrade` runs on a terminal
(`docker compose run --rm setup upgrade` allocates one), it first shows
what it will do, before writing anything: the old and new version and
image, the variant and database mode, the compose changes (setup-first: a
line diff; compose-first: how your release file was recognized), the
generated files that change or go away, files of yours it would back up
with `--force`, new secret groups and the `.env` image line. It then asks
whether you have a current backup of bekup's database and of the secrets
directory (no backup: it stops) and for a final confirmation. Afterwards
restart everything with `docker compose pull && docker compose up -d`;
`bekup.toml` and `runner.toml` are re-validated, never rewritten.
`--non-interactive` skips the questions (scripts, CI). When the new
version adds certificates, it asks for a new CA key passphrase twice (or
takes `--ca-passphrase-file` / `--ca-passphrase-stdin`). The guided screens
need stdin and stdout on a terminal; otherwise the upgrade runs unguided,
as with `--non-interactive`. In an existing deployment, `bekup setup`
without a subcommand offers the same upgrade (with
`--allow-version-mismatch` passed on when you gave it).

**Compose-first with the committed file** (`deploy/compose/`, D-205
amendment 3): `compose.yaml` takes the image from `BEKUP_IMAGE`, which setup
keeps in `.env`. Upgrade by naming the new image in the shell for one
command, so docker compose runs the **new** image's setup service; it
rewrites the `BEKUP_IMAGE` line of `.env` (the only change it makes to
`.env`) and records the image:

```sh
cd /opt/bekup
# first take the committed file of the new version (same variant), if it changed:
sudo curl -fsSLo compose.yaml.new https://raw.githubusercontent.com/jlentink/bekup/vX.Y.Z/deploy/compose/compose.yaml
diff -u compose.yaml compose.yaml.new     # review; no output: unchanged
sudo mv compose.yaml.new compose.yaml
NEW=<registry>/bekup:X.Y.Z@sha256:<digest>
BEKUP_IMAGE="$NEW" docker compose run --rm setup upgrade
docker compose pull
docker compose up -d
docker compose -f compose.yaml run --rm setup check
```

Equivalently, edit the `BEKUP_IMAGE=` line of `.env` yourself and run
`docker compose run --rm setup upgrade`. `setup upgrade --image <new>`
alone does not work: compose would still run the old image's setup (it
refuses with the command above). Until `setup upgrade` ran, `setup check`
reports a `BEKUP_IMAGE` in `.env` that differs from the recorded image, or
one set in the shell that overrides `.env`, as an error. Switching between
the committed file and a release file of the same variant is an upgrade
too: replace `compose.yaml` and run `setup upgrade` (with a release file it
removes the `BEKUP_IMAGE` line from `.env`).

### Setup-first installs

With a `compose.yaml` that setup wrote itself (`--write-compose`), run
`setup upgrade --write-compose` from the **new** image (pinned by digest
and verified with `cosign verify` as above); it also rewrites
`compose.yaml` and prints a summary of the compose changes:

```sh
NEW=ghcr.io/jlentink/bekup:1.1.0@sha256:<digest from the release notes>
cd /opt/bekup
docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -v /opt/bekup:/deploy "$NEW" setup upgrade --write-compose --image "$NEW"
docker compose pull
docker compose up -d
docker compose -f compose.yaml run --rm setup check
```

The rules above apply; a locally edited `compose.yaml` is refused (move
the change into `compose.override.yaml`).

Adopting an install that was set up from a source checkout (without
`bekup-setup.json`) is not supported yet; keep following the steps below
for those.

### Source-checkout installs

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
