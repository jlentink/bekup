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
- never touches `.env` except the `BEKUP_SETUP_VERSION` and
  `BEKUP_IMAGE_MODE` lines ([the version guard](#the-version-guard)) and
  the `BEKUP_IMAGE` line with the committed file (below); never
  `bekup.toml`, `runner.toml`, the override file or an
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

### The version guard

Setup writes `BEKUP_SETUP_VERSION=<version>` to `.env` at every init and
upgrade (D-212); the compose files pass it (with `BEKUP_IMAGE_MODE`) to
`migrate`, `serve` and `runner`. `bekup migrate up`, `bekup db bootstrap`
(run through the `migrate` service), `bekup serve`, `bekup worker` and
`bekup runner` compare it with their own version before they connect to
anything (the runner before it opens the Docker socket or its gRPC
listener) and **refuse to start** when it differs, in both directions:

- a newer image (a moved `:latest` tag, a replaced `compose.yaml`, a
  `BEKUP_IMAGE` in the shell) runs neither its migrations nor its startup
  until `docker compose run --rm setup upgrade` has prepared the
  deployment for it (`serve` waits for `migrate`, and the runner checks
  for itself, so nothing of the new version runs; the runner does not
  wait for `migrate`, it does not use the app database);
- an older image (a downgrade) is refused too: run the image the
  deployment was set up for, or restore a matching database backup and run
  `setup upgrade --allow-downgrade` from the older image.

The comparison is exact (a leading `v` is ignored): pre-release and build
metadata count, so a development build (`0.0.0-dev`, `0.0.0+<commit>`)
never runs against a deployment set up by another build; run
`setup upgrade` from that image first. Without the variable (deployments
set up before D-212, or run without setup) the processes start and log a
warning; `setup check` reports it, and one `docker compose run --rm setup
upgrade` records it. A followed image (`BEKUP_IMAGE_MODE=following`)
without the variable is refused outside dev mode: there would be nothing
to stop a moved tag. With a digest-pinned image nothing changes in
practice: setup runs from the image it pins, so the versions match.

### Following `:latest`

A deployment set up with `ghcr.io/jlentink/bekup:latest` (the README quick
start, D-212) names that tag in `compose.yaml` (setup-first) or in
`BEKUP_IMAGE` (the committed file) and records `image_mode: following`.
`docker compose pull` fetches whatever release `:latest` points to, but
the [version guard](#the-version-guard) keeps the upgrade a deliberate
step:

```sh
cd /opt/bekup
sudo docker compose pull                         # the new release
sudo docker compose run --rm setup upgrade       # the new image prepares the deployment (guided, asks for a backup)
sudo docker compose up -d
sudo docker compose -f compose.yaml run --rm setup check
```

`setup upgrade` needs no flags here: it rewrites the generated files for
the followed tag and writes the new `BEKUP_SETUP_VERSION`. A bare
`docker compose pull && docker compose up -d` stops at `migrate`, which
refuses with the command above; the old containers are replaced, so the UI
is down until you run it (back up before every pull).

Following is an explicit weakening: whoever controls the tag decides what
runs. `setup check` warns about it, serve and worker log a warning and
record `security.unpinned_image` in the audit log at every start, every
admin gets a daily security alert, and the admin configuration page shows
the image as **not pinned**. serve and worker do not rely on
`BEKUP_IMAGE_MODE` alone: the runner reads the image reference of its own
container from Docker and reports in its Health answer whether it carries
a digest. An unpinned runner image counts as following whatever
`BEKUP_IMAGE_MODE` says (a shell or `.env` override cannot hide it); when
the runner is not ready at startup, serve and worker start anyway and
re-check every minute until the runner answers (then hourly, and with
every health snapshot and security check), then warn, audit and alert. Verify a release before you upgrade
([cosign verify](compose.md#verifying-release-images) against the release
workflow's identity), and keep a backup.

**Pinning a digest again:** run `setup upgrade` **from the image you pin**
(or one of the same version), so the version it records matches what then
runs. Setup-first (the README quick start):

```sh
PINNED=ghcr.io/jlentink/bekup:<version>@sha256:<digest>
cd /opt/bekup
sudo docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
  --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -v /opt/bekup:/deploy "$PINNED" setup upgrade --write-compose --image "$PINNED"
sudo docker compose up -d
sudo docker compose -f compose.yaml run --rm setup check
```

It rewrites `compose.yaml` and removes `BEKUP_IMAGE_MODE` from `.env`.
With the committed file, `BEKUP_IMAGE=<pinned> docker compose run --rm
setup upgrade` (the setup service then runs from the pinned image), then
`docker compose up -d`. The digest: `docker image inspect --format
'{{index .RepoDigests 0}}' ghcr.io/jlentink/bekup:<version>` after a
`docker pull` (keep `:<version>` in front of the `@`). Switching to
following works the same way with the `:latest` reference.

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
