# Audit log verification

> **Status:** partial. The table, grants and hash chain (task 0.4), the
> audit API, `bekup audit verify` and signed chain heads (task 1.3, D-101)
> are complete, and the admin-only GraphQL `auditLog` query (task 1.4) reads
> it. `serve` signs the head periodically (`security.audit_head_sign_interval`).
> TODO: head export to destinations/webhooks (tasks 3.x/5.x), archival policy.

How the tamper-evident audit log works (hash chain computed in the database,
no UPDATE/DELETE for the app role), how to run `bekup audit verify`, how the
signed chain head is exported, and the archival policy.

## Table and protections (D-054, D-092)

`audit_log` (migration `00004_core_tables.sql`):

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint` PK | assigned by the trigger from `audit_log_id_seq` **under the chain lock**, so id order is chain order |
| `ts` | `timestamptz` | set by the trigger to `clock_timestamp()` |
| `actor_user_id` | `uuid` NULL | no foreign key (rows outlive users) |
| `actor_kind` | `text` | `user`, `system` or `cli` |
| `action` | `text` | 1–128 chars |
| `target_type`, `target_id` | `text` NULL | |
| `ip` | `inet` NULL | host addresses only (/32, /128) |
| `details` | `jsonb` | default `{}`, at most 64 KiB as text |
| `prev_hash` | `bytea` | 32 bytes, unique (no forks) |
| `hash` | `bytea` | 32 bytes, unique |

- `bekup_app` has only `INSERT` and `SELECT`. `UPDATE`, `DELETE` and
  `TRUNCATE` are revoked from it and from `PUBLIC`, and statement triggers
  raise on `UPDATE`, `DELETE` and `TRUNCATE` for every role, the owner
  included.
- The `BEFORE INSERT` trigger `audit_log_chain()` is `SECURITY DEFINER`
  (owner `bekup_owner`, `SET search_path = pg_catalog, pg_temp`, all objects
  schema-qualified). It:
  1. refuses to run outside `READ COMMITTED` (a transaction snapshot could
     miss the previous row and fork the chain);
  2. takes `pg_advisory_xact_lock(7090191340503389508)` (`'bekupAUD'` as a
     big-endian int64), held until commit/rollback;
  3. reads the hash of the row with the highest id (32 zero bytes if none);
  4. sets `id`, `ts`, `prev_hash` and `hash`, ignoring any values the
     client supplied.

Inserts are therefore serialized; keep audit inserts at the end of short
transactions. A rolled-back insert leaves a gap in `id`, which is harmless:
the chain is defined by `prev_hash`, not by consecutive ids.

## Canonical serialization v1

`hash = SHA-256(C)` where `C` is (all `||` are byte concatenation):

```
C = "bekup-audit-v1"            (14 ASCII bytes, no length prefix)
    || F(prev_hash)              raw 32 bytes
    || F(id)                     decimal, e.g. "42"
    || F(ts)                     decimal microseconds since 1970-01-01T00:00:00Z, e.g. "1793018096789012"
    || F(actor_user_id)          lowercase hyphenated UUID, e.g. "0b9f6f3e-6d2a-4c1e-9a53-3e0d2c1b4a59"
    || F(actor_kind)
    || F(action)
    || F(target_type)
    || F(target_id)
    || F(ip)                     Postgres host(ip), e.g. "10.0.0.1", "2001:db8::1"
    || F(details)                Postgres jsonb text output (details::text)

F(NULL) = 0x00
F(v)    = 0x01 || uint32 big-endian octet length of v || v
```

Text values are UTF-8 (the database must be UTF8). `details` is hashed in the
normalized form Postgres prints for `jsonb` (`{"a": 1, "name": "é"}`: keys
sorted by length then bytes, `", "` and `": "` separators); verifiers must
use the text returned by Postgres and never re-encode the JSON. `ip` likewise
uses Postgres' `host()` text.

The SQL implementation is `public.audit_log_canonical(...)` (callable by
`bekup_app` for diagnostics); the Go implementation is
`internal/db/auditchain` (`Canonical`, `Hash`, `Verify`), fed by the sqlc
query `ListAuditLogCanonical`, which returns `host(ip)` and `details::text`.
A golden vector in `auditchain_test.go` and the integration test
`TestCanonicalGolden` keep both in lockstep.

Verification walks rows in id order: the first row's `prev_hash` must be 32
zero bytes, each later row's `prev_hash` must equal the previous row's
`hash`, and every `hash` must equal `SHA-256(C)` of its row.

## Writing entries (`internal/audit`, D-101)

`audit.Auditor.Record(ctx, ev)` inserts one entry; `RecordTx(ctx, q, ev)`
inserts it with the caller's `*sqlc.Queries` inside a `db.Tx`, so the
action and its entry commit or roll back together. The client never
supplies `id`, `ts` or hashes: the trigger computes them.

- **Fail closed.** Any write failure returns an error wrapping
  `audit.ErrRecordFailed`; the audited operation must fail with it.
- **Actor:** `UserActor(id)`, `SystemActor()` or `CLIActor()`.
  `audit.CLIEvent(...)` builds break-glass CLI events (D-069) with the OS
  user (`os_user`, `os_uid`) and `hostname` in the details.
- **Actions** come from a typed catalog (`internal/audit/actions.go`);
  unknown actions are rejected. Areas: `auth.*`, `user.*`, `session.*`,
  `secret.changed`, `setting.changed`, `security.*`, `system.*`,
  `cli.admin.*`, `keys.*` (incl. `keys.check_value_stored`), `certs.rotated`, `audit.*`,
  `download.*`, `connection.*`, `mfa_policy.*`.
- User administration (D-110) records `user.created`,
  `user.updated`, `user.role_changed`, `user.disabled`, `user.enabled`,
  `user.password_reset`, `user.unlocked`, `user.deleted`,
  `user.second_factors_reset` (lost-factor recovery, D-112: counts of the
  removed factors and revoked sessions) and `session.revoked_all`; the
  break-glass CLI records `cli.admin.created` (also for the first admin,
  D-113), `cli.admin.password_reset`, `cli.admin.mfa_reset`,
  `cli.admin.unlocked` and `cli.admin.disabled` with actor `cli`.
  Passwords and temporary passwords are never recorded. (The `setup.*`
  actions of the removed web setup flow no longer exist.)
- Passkeys (task 1.8, D-109): `auth.passkey.registration_begun`,
  `auth.passkey.registration_failed` (`reason`), `auth.mfa.enrolled` and
  `auth.mfa.removed` with `factor: "passkey"`, `auth.passkey.renamed`,
  `auth.passkey.assertion_succeeded`, `auth.passkey.assertion_failed`
  (`reason`: `no_ceremony`, `invalid_response`, `verification_failed`,
  `unknown_passkey`) and `auth.passkey.sign_count_anomaly` (possible cloned
  authenticator, with the stored and received counters). Details carry the
  passkey id and name, never key material or the credential id.
- Two-step login, step-up and the 2FA policy (task 1.9, D-112):
  `auth.login.succeeded` (with `second_factor_pending`),
  `auth.login.second_factor` (`method`: `totp`, `passkey`,
  `recovery_code`; never the code), `auth.stepup.succeeded` (`method`,
  incl. `password`), `auth.stepup.failed` (password step-ups),
  `auth.mfa.removal_refused` (last factor while 2FA is required) and
  `mfa_policy.updated` (`old`, `new`, `weakened`).
- `system.insecure_transport_started` (actor `system`, target `server`) is
  recorded at every `serve` startup with a plain-http `base_url` outside dev
  mode (D-095), with `base_url` and `allow_insecure_http` in the details.
  If it cannot be recorded, `serve` refuses to start.
- `security.insecure_db_tls` (actor `system`, target `server`) is recorded
  at every `serve` startup outside dev mode with `database.tls_mode`
  `require` or `disable` (D-100, D-118), with `tls_mode`, `ca_file_set`
  and `chain_verified`; fail closed like the entry above.
- `certs.rotated` (actor `cli`) is recorded by `bekup certs rotate` and by
  `bekup certs init --force` (details: `operation` = `leaf`, `ca_stage`,
  `ca_promote`, `ca_finish` or `init_force`, `dir`, `components`). The
  command opens the app database before changing any file and stops if it
  cannot; `--no-audit` skips the entry with a warning (D-118).
- `keys.check_value_stored` and `keys.rotated` (actor `system`, target
  `keyring`) are recorded by `serve` when it stores the key-check values
  of the master keyring for the first time and when it finds new keys
  after `bekup keys rotate` (key IDs only, D-119).
- **Refusals while locked are aggregated** (D-117). Login, password
  step-up/change and second-factor attempts refused because the account
  (or its second factors) is locked are recorded at most once per user and
  lock window (`reason: "locked"`, `aggregated: true`, `locked_until`). The
  number of further refusals is kept in memory and reported as
  `refused_while_locked` in the account's next `auth.login.failed`,
  `auth.stepup.failed`, `auth.password.change_failed`, `auth.mfa.failed`
  or lockout entry (lost on a restart; the next refusal is then recorded
  again). An attacker hammering a locked account therefore cannot flood
  the hash-chained log.
- **Reserved actions** exist in the catalog before the feature that
  records them: `secret.changed` (tasks 2.11, 2.12, 5.1),
  `setting.changed` (2.3, 5.1, 6.9), `security.opt_out` (2.11, 2.12;
  alert 5.5), `security.insecure_http` (5.5), `audit.verify.failed`
  (5.6), `download.ticket_created` (3.9) and `connection.pin_confirmed`
  (2.7a, 2.11).
- **Details never hold secrets.** They are normalized through JSON, then
  values under secret-looking keys (`password`, `token`, `key`,
  `newPassword`, ...; not `key_id`, `master_key_file`) become
  `[REDACTED]`, and every string loses URL/key=value credentials and every
  value registered in `logging.DefaultRegistry`. Max 16 levels, 64 KiB.
- `Auditor.List(ctx, filter, cursor, limit)` pages newest first (cursor =
  last id of the previous page) with filters on action, action prefix,
  actor, target and a time range; it backs the GraphQL `auditLog` query
  (admin only: `@hasRole(roles: [ADMIN])` + `audit.log_view`; opaque
  cursors, `first` 1..200, filters `action`, `actionPrefix`, `actorUserId`,
  `actorKind`, `targetType`, `targetId`, `from`, `to`; details are returned
  as a JSON string and the chain hash as hex).

## Signed chain heads (D-087, D-101)

A head is the newest row's `(id, hash)` signed with the active instance
ed25519 key (`instance_keys`, created and audited on first use; the seed is
sealed with the master keyring under AAD `instance_keys:<id>:private`).
The signed bytes are the D-087 envelope
`"bekup-sig-v1" 0x00 "audit-head" 0x00 M` with

```
M = "bekup-audit-head-v1\n"
    "chain_id=<decimal>\n"
    "chain_hash=<64 lowercase hex>\n"
    "signed_at=<decimal microseconds since the Unix epoch>\n"
```

Heads are stored in `audit_chain_heads` (append-only for `bekup_app`) and
handed to a `HeadExporter`. The export (and `--export-file`) format is JSON:

```json
{"version": "bekup-audit-head-v1", "chain_id": 42, "chain_hash": "<hex>",
 "signed_at": "2026-10-26T12:00:00.123456Z", "key_id": "<16 hex>",
 "public_key": "<hex>", "signature": "<hex>"}
```

`bekup audit sign-head [--export-file head.json]` signs once (needs the
master keyring); `HeadSigner.Run` signs periodically when the chain grew.
Keep exported heads **off the database host** (immutable storage): they are
what makes a consistent rewrite by a database superuser detectable.

## `bekup audit verify`

```
bekup audit verify [--from-id N] [--json] [--head-file head.json]
```

Connects as `bekup_app`, streams the chain in id order in batches of 500
(bounded memory) and for every row checks, in order:

1. the recomputed hash equals the stored hash (`hash_mismatch`: the row was
   altered);
2. the first row's `prev_hash` is 32 zero bytes (`genesis`: rows deleted at
   the start or a forged first row);
3. `prev_hash` equals the previous row's hash (`link_mismatch`: rows
   deleted, inserted or rewritten before it; the message names the id gap).

It stops at the first broken link and reports its id and timestamp. Unused
ids alone are not tampering (rolled-back inserts consume ids) and are only
counted. With `--from-id N` the walk starts at the first row with id >= N,
anchored on the stored hash of the row before it (earlier rows are not
verified).

Then it checks the newest head in `audit_chain_heads` and, with
`--head-file`, an exported head: the signature verifies with the stored
public key (whose id must match), and the row at `chain_id` still exists,
hashes correctly and has hash `chain_hash`. A truncated tail or a
consistently rehashed chain fails here. For a head file, the embedded public
key must also equal the stored key with that id. Without any stored head a
warning is printed (tail truncation is then undetectable).

Exit status: 0 when everything verifies, non-zero on any break or head
failure (and on operational errors). `--json` prints the full report.
