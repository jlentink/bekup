# Audit log verification

> **Status:** partial. The table, grants and hash chain are complete (task
> 0.4). TODO: task 1.3 (audit API, `bekup audit verify`, signed chain-head
> export, archival policy).

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
