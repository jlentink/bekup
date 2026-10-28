# Artifact and manifest format

This page describes how bekup stores backups on a destination, precisely
enough to verify and decrypt them **without bekup and without its
database** (offline recovery). The format is produced by
`internal/pipeline` (task 3.1, decision D-144). Format version: **1**.

## Layout

Every run writes one directory per destination:

```
{prefix}/{job-slug}/{timestamp}/
    {enc(db)}.dump.zst.age        PostgreSQL database (pg_dump custom format)
    {enc(db)}.sql.zst.age         MySQL / MariaDB database (plain SQL)
    globals.globals.sql.zst.age   PostgreSQL globals (roles, tablespaces), optional
    manifest.json                 signed run manifest, written last
```

- `prefix` is the destination's optional key prefix; `job-slug` is the
  job's slug: 1–63 lower-case letters, digits and inner hyphens
  (`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`, enforced by the database
  check `jobs_slug_format`), unique among all jobs including deleted ones
  and never changed (D-146). Tools reading a destination should still
  accept the looser storage key rule `[a-z0-9][a-z0-9_-]*` (at most 100
  bytes) that the storage layer validates; `timestamp` is the run time in
  UTC as `YYYY-MM-DDTHHMMSSZ` (for example `2026-10-27T030000Z`).
- `enc(db)` keeps the bytes `A-Z a-z 0-9 _ -` and writes every other byte as
  `%XX` (upper-case hex), so `my.db` becomes `my%2Edb`. If that is longer
  than 180 bytes the file name uses `~` plus the lower-case unpadded base32
  SHA-256 of the name instead; the full name is always in the manifest.
- The globals file has the extra extension part `globals`, so it can never
  collide with a database called `globals`.
- Jobs whose encryption an admin opted out of have no `.age` suffix.
- Objects are only ever created complete (atomic finalize) and never
  overwritten. A directory without `manifest.json` is an incomplete run.

## Artifact encoding

An artifact is, from the outside in:

1. **age v1** ([age-encryption.org/v1](https://age-encryption.org/v1)),
   either with one scrypt recipient (job passphrase; work factor 18, at most
   20) or with one or more X25519 recipients (`age1...`). Omitted for
   opt-out jobs.
2. **zstd** (RFC 8878) frames with content checksums, window at most 8 MiB.
3. The **raw dump** as written by the engine's dump tool.

With the standard tools:

```sh
# passphrase jobs (age prompts for the passphrase)
age -d app.dump.zst.age | zstd -d > app.dump
# recipient jobs
age -d -i job-key.agekey app.dump.zst.age | zstd -d > app.dump
# or with bekup: verifies the signed manifest first, bounded scrypt work
# factor, X25519 identities only, zstd window cap (docs/recovery.md)
bekup decrypt app.dump.zst.age --out app.dump --identity-file job-key.agekey \
    --manifest manifest.json --pubkey bekup-instance-keys.txt
```

**Verify before you trust or restore**: check the manifest signature, then
compare `sha256sum` of the artifact with the entry's `sha256`. After
decryption and decompression, the raw dump's SHA-256 must equal
`raw_sha256`.

## `manifest.json`

`manifest.json` is a JSON object with exactly three members:

| Member | Content |
|---|---|
| `key_id` | ID of the instance signing key: the first 8 bytes of SHA-256(ed25519 public key), 16 lower-case hex characters |
| `manifest` | the manifest object (below) |
| `signature` | standard base64 (with padding) of the 64-byte ed25519 signature |

bekup writes the whole file in canonical JSON (one line, sorted keys).

### Manifest object (format version 1)

| Field | Type | Meaning |
|---|---|---|
| `format_version` | integer | `1` |
| `bekup_version` | string | version of bekup that wrote the run |
| `instance_key_id` | string | the signing key ID (must equal `key_id`) |
| `job_id` | string | job ID (UUID) |
| `job_slug` | string | job slug (directory name) |
| `run_id` | string | run ID (UUID) |
| `run_timestamp` | string | RFC 3339 UTC, whole seconds; matches the directory timestamp |
| `created_at` | string | RFC 3339 UTC time the manifest was signed |
| `engine` | object | `kind` (`postgres`, `mysql`, `mariadb`), `version_key` (dump client release series, for example `18`), `server_version` (as reported by the server) |
| `entries` | array | one object per artifact, see below |
| `stats` | object, optional | source statistics and completeness evidence (task 3.10, D-141); covered by the signature |
| `warnings` | array of strings, optional | warnings recorded for the run |

Entry fields:

| Field | Type | Meaning |
|---|---|---|
| `db` | string | database name in the reversible `%XX` encoding above, **never hashed**; absent for globals |
| `kind` | string | `database` or `globals` |
| `path` | string | file name, relative to the directory holding `manifest.json` |
| `raw_size` | integer | bytes of the raw dump |
| `stored_size` | integer | bytes of the stored artifact |
| `sha256` | string | lower-case hex SHA-256 of the stored artifact |
| `raw_sha256` | string | lower-case hex SHA-256 of the raw dump |
| `compression` | string | `zstd` |
| `encryption` | object | `mode` (`passphrase`, `recipients`, `none`); `passphrase_version` (passphrase mode: which job passphrase version decrypts it); `recipient_version` (recipient mode: which job recipient key version, i.e. which identity, decrypts it); `recipient_fingerprints` (recipient mode: lower-case hex SHA-256 of each canonical lower-case `age1...` recipient string) |

Unknown fields are rejected; a future format change increments
`format_version`.

### `stats` (completeness evidence, task 3.10)

The source statistics the runner captured right before each dump (D-141,
D-151). They contain names and counts only, never data values, and use
integers and strings only (flags are the integer `1` and absent when
false):

```json
{"databases":{"app":{"consistency":"snapshot","level":"deep",
  "objects":{"extensions":1,"functions":4,"materialized_views":0,"procedures":0,
             "schemas":2,"sequences":2,"tables":3,"triggers":1,"views":1},
  "tables":{"public.users":{"count":"exact","rows":1500},
            "private.keys":{"count":"unknown"}},
  "tables_total":3,
  "warnings":[{"code":"insufficient_privileges","count":1,"objects":["private.keys"]}]}},
 "globals":{"consistency":"approximate","level":"deep",
            "objects":{"roles":7,"tablespaces":0}}}
```

| Field | Meaning |
|---|---|
| `databases` | per database, keyed by the `%XX`-encoded name (like `db` of the entries) |
| `globals` | the PostgreSQL globals (`objects`: `roles` without the predefined `pg_*` roles, `tablespaces` without `pg_default`/`pg_global`) |
| `level` | `standard` (catalog estimates) or `deep` (exact `count(*)`), the job's `verification` |
| `consistency` | `snapshot`: PostgreSQL, the dump ran in the exported snapshot of these stats (`pg_dump --snapshot`), so `exact` counts equal the dump's; `approximate`: MySQL/MariaDB (the dump tool takes its own snapshot right after) and the globals |
| `objects` | object counts outside the system schemas, extension members excluded. PostgreSQL: `schemas`, `tables` (plain + partitioned), `views`, `materialized_views`, `sequences`, `functions` (incl. aggregates), `procedures`, `triggers` (non-internal), `extensions`; MySQL/MariaDB: `schemas` (1), `tables`, `views`, `sequences` (MariaDB), `functions`, `procedures`, `triggers`, `events` |
| `tables` | per table holding data, keyed `<enc schema>.<enc table>` (PostgreSQL) or `<enc table>` (MySQL/MariaDB), each part `%XX`-encoded: `count` = `exact`, `estimate` (`reltuples` / `TABLE_ROWS`) or `unknown` (timeout, budget, no privilege, never analyzed; then no `rows`), `rows`, `approximate` = 1 on MySQL/MariaDB |
| `tables_total` | number of such tables in the database |
| `tables_truncated` | 1 when `tables` lists fewer (at most 10 000 tables / ~768 KiB per database) |
| `tables_omitted` | 1 when the per-table lists were dropped because the stats of the run exceeded 8 MiB |
| `warnings` | completeness warnings: `code`, `count`, up to 50 `objects` (unencoded qualified names), `truncated` = 1 when more objects are affected |
| `databases_excluded` | top level, optional: the `%XX`-encoded names (at most 1000) of the databases a job backing up "all databases" excluded on purpose (a run notice, not a warning) |

Warning codes: `insufficient_privileges` (PostgreSQL: tables and sequences
without SELECT, schemas without USAGE for the backup role; MySQL/MariaDB:
visible tables the user cannot SELECT), `non_transactional_tables`
(MyISAM, Aria, MEMORY, CSV, ... are dumped without a consistent
snapshot), `row_count_timeout`, `stats_budget_exceeded`,
`row_count_failed`, `stats_partial` and `roles_without_passwords` (globals
dumped with `--no-role-passwords`). The same object is stored per item in
the `backup_stats` table; test restores (4.6) compare a restore against
it, and `bekup verify` (4.7) shows it so an operator can.

### Canonical JSON

The signed bytes are the canonical serialization of the `manifest` object:

- UTF-8; no whitespace outside strings; no duplicate keys;
- object members sorted by key, comparing the UTF-8 bytes;
- strings escaped like Go's `encoding/json` without HTML escaping: `\"`,
  `\\`, `\b`, `\f`, `\n`, `\r`, `\t`, other control characters as `\u00xx`
  (lower-case hex), U+2028/U+2029 as `\u2028`/`\u2029`, everything else
  literally;
- numbers are integers in the signed 64-bit range without leading zeros,
  fraction or exponent (`-0` is invalid); anything else is a string;
- `true`, `false`, `null` literally; nesting depth at most 32; at most
  16 MiB.

### Verifying the signature

1. Parse `manifest.json`; reject duplicate keys.
2. Serialize the `manifest` member canonically (bekup's own output already
   is canonical, so for an unmodified file this is the exact byte range of
   the member).
3. Build the message `bekup-sig-v1` `0x00` `manifest` `0x00`
   `<canonical manifest bytes>`.
4. Verify the ed25519 signature with the public key whose ID is `key_id`.
   Check that the ID is the first 8 bytes (hex) of SHA-256 of that key and
   that `manifest.instance_key_id` equals `key_id`.

The instance public keys and their fingerprints (SHA-256 of the raw public
key, shown as hex and as `SHA256:<base64>`) are stored in the bekup
database (`instance_keys`); `bekup keys export-public` writes them in the
file format below. Escrow them with the master key backup so that offline
verification is possible (see [recovery](../recovery.md)). Retired keys
stay valid for the manifests they signed.

### Instance public key file

`bekup keys export-public` writes, and `bekup verify --pubkey` /
`bekup decrypt --pubkey` / `bekup reindex --pubkey` read:

```
bekup-instance-public-keys v1
# <key id> (active|retired): fingerprint SHA256:<base64>, hex <hex>
key <key id> <standard base64 of the 32-byte ed25519 public key> created <RFC 3339> [retired <RFC 3339>]
```

Blank lines and `#` comments are ignored; the key ID must be the ID of the
key. A file (or `--pubkey` value) holding just one bare base64 key is
accepted too. The passphrase export of a job (`bekup-job-passphrases v1`)
is described in [the encryption guide](../guides/encryption.md).

### Binding (replay protection)

A valid signature only proves that *some* bekup run of this instance wrote
the manifest. bekup's restore additionally requires that the job ID, run ID
(and slug and timestamp) match the run it restores, and that the entries
(paths, stored sizes and SHA-256) are exactly those recorded in its
database. A validly signed manifest of an older run, or of another job,
placed into the directory is therefore rejected. Offline tools without the
database show the job and run identity so the operator can make that check;
`bekup reindex` additionally rejects a manifest whose job slug and run
timestamp do not match the `{job-slug}/{timestamp}` directory it lies in.
