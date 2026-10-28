# Disaster recovery

This page explains how to get your data back when the bekup database (or
the whole bekup host) is gone: what to escrow **before** a disaster, and
how to verify, decrypt and restore backups with `bekup verify`, `bekup
decrypt`, `bekup reindex` and the stock database tools. Everything here
works **without the bekup database and without the master keyring**
(task 4.7, decisions D-040 and D-164).

The file layout and manifest format are documented in
[reference/artifact-format.md](reference/artifact-format.md); third-party
tools can verify and decrypt backups from that page alone.

## 1. What to escrow now

Do this once after installing, and again after every key change. Keep the
copies **offline and away from the bekup host and the backups** (a password
manager vault, an encrypted USB stick in a safe, a printed sheet).

| Item | How to get it | Needed for |
|---|---|---|
| **Instance public keys** and their fingerprints | `bekup keys export-public --out bekup-instance-keys.txt` (prints the fingerprints) | verifying manifest signatures offline (`--pubkey`) |
| **Job passphrases** (passphrase-mode jobs) | admin: `exportJobPassphrases` (step-up, audited, security alert), one file per job | decrypting passphrase-encrypted backups |
| **age identities** (recipient-mode jobs) | the one-time private key download when the keypair was generated, or your own `age-keygen` identity | decrypting recipient-encrypted backups |
| **Master keyring** | `bekup keys generate` output (see [key management](security/key-management.md)) | rebuilding bekup from a database backup; not needed by the offline tools |
| **Destination access** | bucket name, endpoint, credentials, SFTP host key fingerprint | reaching the backups |

Notes:

- The public key file is not secret, but it is **integrity-relevant**: an
  attacker who can swap it can make forged backups verify. Record the
  fingerprints (`SHA256:...`) independently of the file, for example on
  the printed escrow sheet, and compare them before you trust the file.
- New instance keys are rare (they are created on first start and when a
  database without keys is used). Export again whenever `bekup keys
  export-public` shows a key you have not escrowed. Retired keys stay valid
  for the manifests they signed, so keep every key.
- Re-export a job's passphrases after every passphrase change: the export
  holds every version, and each artifact names the version that decrypts
  it.
- Without the passphrase export, passphrase-encrypted backups can only be
  decrypted by a bekup instance that still has the database **and** the
  master keyring.

## 2. Find the run you need

A destination holds one directory per run:

```
{prefix}/{job-slug}/{YYYY-MM-DDTHHMMSSZ}/
    {database}.dump.zst.age      PostgreSQL (pg_dump custom format)
    {database}.sql.zst.age       MySQL / MariaDB (plain SQL)
    globals.globals.sql.zst.age  PostgreSQL roles and tablespaces (optional)
    manifest.json                signed run manifest, written last
```

A directory without `manifest.json` is an incomplete or failed run. Copy
the whole run directory (or at least `manifest.json` and the artifacts you
need) to the machine where you recover; keep the original file names.

For a disk destination you can work on the directory directly. For S3 and
SFTP, download the objects with your usual tools, or let `bekup reindex`
list and verify them in place (section 5).

## 3. Verify

```sh
bekup verify /recovery/app-db/2026-10-27T030000Z --pubkey bekup-instance-keys.txt
```

`bekup verify` takes the run directory (or its `manifest.json`) and:

1. parses `manifest.json` strictly and checks its ed25519 signature with
   the instance public key named by its key ID (`--pubkey` takes the file
   from `bekup keys export-public` or a bare base64 key; repeat it for
   several keys);
2. streams every listed artifact and compares its size and SHA-256 with
   the signed manifest;
3. prints the job and run identity, the engine and versions, every
   database with its sizes and encryption (passphrase version or recipient
   key version and recipient fingerprints), the completeness statistics
   (object and row counts, warnings such as `insufficient_privileges`) and
   the run warnings.

It exits non-zero on any failure: unknown key, bad signature, missing,
truncated or modified artifact. `--artifact <file>` checks only the given
files (for example the one database you downloaded); `--json` prints the
report as JSON.

**Check the identity yourself.** A valid signature proves that your bekup
instance wrote the manifest. It does not prove that the run is the newest
one of the job, or that it belongs to the job you think: an attacker with
write access to the storage could put an older, validly signed run in
place. Compare the job slug and ID, the run timestamp and the run ID with
what you expect (bekup's own restore checks this against its database).

## 4. Decrypt

```sh
# passphrase job, with the passphrase export of the job
bekup decrypt app.dump.zst.age --out app.dump \
    --passphrase-file bekup-job-<job-id>-passphrases.txt \
    --manifest manifest.json --pubkey bekup-instance-keys.txt

# recipient job, with the age identity
bekup decrypt app.dump.zst.age --out app.dump \
    --identity-file bekup-job-<job-id>-v1.agekey \
    --manifest manifest.json --pubkey bekup-instance-keys.txt
```

What `bekup decrypt` does:

- With `--manifest` and `--pubkey` it verifies the manifest signature and
  the artifact's size and SHA-256 **before** decrypting, and refuses on any
  mismatch. While decrypting it checks that the dump matches the signed raw
  size and SHA-256. The output appears under `--out` only after every check
  passed (it is written to a temporary file next to it first, mode
  `0600`).
- Without a manifest you must pass `--no-verify` explicitly. The output is
  then **unauthenticated**: anyone who could write to the storage, or who
  knows the job's age recipient (a public key), could have replaced the
  artifact. bekup prints a warning; review such a dump before restoring it.
- Keys are never accepted as flag values (they would end up in the process
  list and shell history):
  - `--passphrase-file` takes a file holding the passphrase, or a bekup
    passphrase export; with an export the version comes from the manifest
    (or `--passphrase-version` without a manifest), and the export's job
    must match the manifest's job;
  - `--identity-file` takes an age identity file (X25519
    `AGE-SECRET-KEY-1...` lines only);
  - `--stdin-passphrase` prompts on the terminal without echo, or reads
    the first line of stdin.

  Key files must be owned by you and not group- or world-accessible
  (`chmod 600`).
- `--out -` writes to stdout (refused when stdout is a terminal); an
  existing `--out` file is never replaced unless `--force` is given.
- Untrusted input is bounded: the age scrypt work factor is capped at 2^18
  (`--max-work-factor` up to 2^20), only X25519 identities are accepted
  (no plugins), and the zstd window is capped at 64 MiB.

You can use the stock tools instead (`age -d ... | zstd -d`); they do not
check the manifest, so run `bekup verify` first.

## 5. Restore with the stock tools

`bekup decrypt` prints the matching command. Restore into a **new**
database first and check it before you touch production.

**PostgreSQL** (custom-format archive, `.dump`):

```sh
createdb restored
pg_restore --no-owner --exit-on-error --dbname=restored app.dump
```

Use a `pg_restore` at least as new as the server that made the dump (the
manifest's `engine.version_key` names the dump client series). The globals
file (`globals.sql`, roles without passwords and tablespaces) is plain SQL:
review it, then run `psql --dbname=postgres --file globals.sql` before the
databases if you need the roles.

**MySQL / MariaDB** (plain SQL, `.sql`):

```sh
mysql -e 'CREATE DATABASE restored'
mysql restored < app.sql          # MariaDB: mariadb restored < app.sql
```

Compare the restored database with the completeness statistics that
`bekup verify` shows (tables and row counts; `exact` counts on PostgreSQL
match the dump exactly).

## 6. Re-index a destination

`bekup reindex` lists a destination, groups the objects into runs and
verifies each run's manifest (signature, and that it lies in its own
`{job-slug}/{timestamp}` directory, so a manifest copied into another run's
directory is rejected) and streams every artifact to check its size and
SHA-256. The destination is opened read-only (no-delete mode) and is never
written.

Describe the destination in a small TOML file. Credentials are only ever
`*_file` paths to files with owner-only permissions:

```toml
# disk: the directory holding the {job-slug}/ directories
# (storage.disk_root plus the destination's path)
type = "disk"
[disk]
path = "/var/lib/bekup/backups/primary"
```

```toml
type = "s3"
[s3]
endpoint = "https://minio.example.com:9000"   # omit for AWS S3
region = "eu-central-1"
bucket = "backups"
prefix = "bekup"                              # the destination's key prefix, if any
path_style = true
access_key_id = "AKIA..."
secret_access_key_file = "/root/recovery/s3-key"
ca_bundle_file = "/root/recovery/minio-ca.pem" # optional private CA
checksum = "when_supported"                   # or "when_required"

[netguard]                                    # private addresses are blocked unless listed
allow_cidrs = ["10.0.0.0/8"]
allow_hosts = ["minio.example.com"]
```

```toml
type = "sftp"
[sftp]
host = "backup.example.com"
port = 22
user = "bekup"
base_path = "/upload"
dir = "primary"                               # optional subdirectory
host_key_type = "ssh-ed25519"
host_key_fingerprint = "SHA256:..."           # mandatory host key pin
private_key_file = "/root/recovery/sftp-key"  # or password_file
# private_key_passphrase_file = "..."
```

Inventory (no database needed):

```sh
bekup reindex --destination-config dest.toml --pubkey bekup-instance-keys.txt
bekup reindex --destination-config dest.toml --pubkey bekup-instance-keys.txt --job-slug app-db --json
```

`--skip-artifacts` verifies only the manifests. The command exits non-zero
when a run with a manifest fails verification; directories without a
manifest are listed as incomplete.

### Importing runs into bekup

When the bekup database lost runs (for example after restoring the
database from an older backup of its own), `--import` records the runs
that are on a destination as runs and artifacts of the job again, so the
restore UI and downloads can use them:

```sh
bekup --config /etc/bekup/bekup.toml reindex --destination-config dest.toml \
    --job-slug app-db --import --destination-id <destination uuid> --dry-run
bekup --config /etc/bekup/bekup.toml reindex --destination-config dest.toml \
    --job-slug app-db --import --destination-id <destination uuid>
```

- It needs the normal bekup config (it connects as `bekup_app`) and
  verifies against the **instance keys in the database** (`--pubkey` is
  not used). The job with `--job-slug` must exist (not deleted) and have the manifest's
  job ID (restores bind to the job ID; a job recreated after a total loss
  has a new ID, so its old runs cannot be imported: use `bekup decrypt`
  for those). `--destination-id` is the bekup destination these objects
  live on (not deleted); its type must match the TOML file.
- Only runs that verify completely (signature, location, every artifact)
  are imported; artifacts are recorded verified and signed. A run with a
  missing or modified artifact is reported and skipped (its intact
  artifacts can still be decrypted offline).
- Runs already in the database are skipped. Each import is one transaction
  with the audit event `cli.run.imported` (actor `cli`, OS user and host).
- Imported runs belong to the job like any other run: **retention applies
  to them**. Raise the job's retention (or disable it) first if you need
  old runs kept.

## 7. Total loss checklist

1. Get the escrow: public key file (compare the fingerprints), passphrase
   exports or age identities, destination credentials.
2. `bekup reindex --destination-config ... --pubkey ...` to see which runs
   exist and verify.
3. Copy the run you want, `bekup verify` it and check the job and run
   identity.
4. `bekup decrypt` each database (and the globals) with `--manifest` and
   `--pubkey`.
5. Restore with `pg_restore` / `psql` / `mysql` / `mariadb` into a new
   database, check it against the statistics, then switch over.
6. Rebuild bekup: restore its database (if you have a backup of it) with
   the escrowed master keyring, or install fresh; then `bekup reindex
   --import` for jobs whose IDs survived.
