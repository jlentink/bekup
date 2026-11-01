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
  `download.*`, `connection.*`, `destination.*`, `job.*`, `mfa_policy.*`,
  `config.*`.
- Job encryption (task 3.2, D-142; target type `job`, target id the job
  id): `job.encryption_passphrase_set` (`version`, `previous_version`,
  `generated`, `via` set/generate/create; also `secret.changed` with
  `fields: [job_passphrase]`), `job.encryption_recipient_set` (`version`,
  public `recipient`, `fingerprint`, `label`, `generated`, `via`),
  `job.encryption_opted_out` (`from`, `reason`; also `security.opt_out`
  with `setting: job.encryption`), `job.encryption_opted_in` (`mode`) and
  `job.passphrases_exported` (`versions`). Passphrases and identities are
  never recorded.
- Jobs (task 3.5, D-146; target type `job`, target id the job id):
  `job.created` (name, slug, connection id, selection, schedule JSON,
  retention, timeout, window, verification, destination ids, encryption
  mode), `job.updated` (`fields` and `changes` with old/new values; the
  description only as changed), `job.deleted` (name, slug), `job.enabled`,
  `job.disabled`, `job.owner_added` / `job.owner_removed` (`user_id`,
  `role`; both also raise a security alert to admins, D-067) and
  `job.run_requested` (River `queue_job_id`; the actor is the requesting
  user; the queued `run_id`) and `job.run_started_early`
  (`startNextRunEarly`, task 3.12, D-157: `queue_job_id`, `run_id`,
  `scheduled_for` = the planned occurrence that runs now, `next_run_at` =
  the job's new next run). A refused run request (already active,
  disabled, manual schedule) records nothing.
- Runs (task 3.6, D-147; target type `run`, target id the run id):
  `run.cancel_requested` (`job_id`, `status` of the run at the request,
  `river_state` of its River job; `retry_canceled` when the run had
  finished and its pending retry was canceled, D-155). The run history itself (statuses,
  items, artifacts, logs) lives in the run tables, not in the audit log.
- Downloads (task 3.9, D-150; target type `artifact`, target id the
  artifact id; details `ticket_id`, `mode`, `job_id`, `run_id`,
  `destination_id`, `encryption`, `kind`, never the ticket value or an age
  identity): `download.ticket_created` (step-up mutation, written in the
  ticket's transaction), `download.ticket_rejected` (`reason` reused,
  expired, session_mismatch or forbidden, `ticket_user_id`, `anonymous`;
  the actor is the presenting user, or the system for a request without a
  session; written in the transaction that burns the ticket),
  `download.started` (before the response headers; a failure fails the
  download), then `download.completed` or `download.failed` (`reason`:
  artifact_unavailable, key_unavailable, destination_unavailable,
  manifest_invalid, integrity_mismatch, decrypt_failed, client_aborted,
  artifact_hold_lost, internal). An unknown or malformed ticket records nothing.
- Retention (task 3.8, D-148; system actor, target type `job`, target id
  the job id): `retention.deletion_planned` before the first delete on a
  destination (`destination_id`, `reason` post_run/daily,
  `newest_complete_run`, `kept_runs`, up to 50 `eligible_runs` +
  `eligible_runs_more`, `eligible_objects`, `max_deletions`; if it cannot be
  written nothing is deleted), `retention.swept` afterwards (`deleted`,
  `deleted_runs` (≤ 50) + `deleted_runs_more`, `missing`, `locked`, `held` (in use by a
  download or restore, D-155), `locked_until_max`, `failed`, `capped`, `aborted`), `retention.aborted`
  when a safety check stopped retention on a destination (`aborted`:
  `newest_complete_missing`, `newest_complete_changed`,
  `plan_invariant_violated`) and `retention.artifact_missing` (target type
  `artifact`, the artifact id; `job_id`, `run_id`, `destination_id`,
  `object` (the object key), `newest_complete`) for an object that vanished without
  bekup deleting it. Dry runs and sweeps with nothing to delete record
  nothing; per-object outcomes are the artifact rows' state.
- Restores (task 4.1, D-162; system actor, target type `restore`, target id
  the restore id): `restore.verification_failed` when a staged artifact
  fails the verification (`reason`: stored_digest_mismatch,
  dump_digest_mismatch, manifest_bad_signature, manifest_unknown_key,
  manifest_replayed, manifest_binding_mismatch, manifest_malformed,
  manifest_invalid, decrypt_failed; `artifact_id`, `job_id`, `run_id`,
  `destination_id`, `object` (the object key)). It is also raised as a security alert.
  The restore worker (task 4.2, D-166) records `restore.started` when it
  takes a queued restore (`job_id`, `artifact_id`,
  `target_connection_id`, `target_database`, `mode`, `no_owner`,
  `requested_by`, `target_engine`, `temporary_target`, `attempt`; fail
  closed: the restore does not start without it) and `restore.finished`
  when it ends (the same fields plus `status`, `error_code`, `warnings`,
  `log_lines`, `log_lines_dropped`). The request (task 4.5, D-167; user
  actor, with the IP) is `restore.requested` (`job_id`, `artifact_id`,
  `run_id`, `kind`, `source_engine`, `source_version`, `source_database`,
  `target_connection_id`, `target_connection`, `target_database`,
  `temporary_target`, `original_target`, `mode`, `no_owner`, `warnings`
  (the preview warning codes), `identity_supplied`, `cross_source`,
  `target_privileges` (`not_privileged`, `privileged`, `unknown`),
  `target_privilege_reasons`, `privileged_target_acknowledged` (task 4.8,
  D-168); never the age identity), recorded in the same transaction as
  the restore;
  `restore.cancel_requested` (user actor; `job_id`, `status`,
  `river_state`) for `cancelRestore`; and `restore.abandoned` (system
  actor; `job_id`, `artifact_id`, `target_connection_id`,
  `target_database`, `mode`, `previous_status`, `started_at`,
  `error_code`) when the stuck-restore sweep fails a restore no worker
  finished; `restore.secdef_neutralized` (system actor; the restore's
  fields plus `database`, `functions_neutralized`, `functions` (at most
  50 signatures), `functions_listed`) when a PostgreSQL restore switched
  `SECURITY DEFINER` routines to `SECURITY INVOKER`, recorded before the
  swap (fail closed, D-171); and `restore.privileged_target_refused`
  (system actor; `requested_privileges`, `requested_privilege_reasons`,
  `privileged_target_acknowledged`, `target_privileges`,
  `target_privilege_reasons`, `refusal`) when the worker's re-check found
  a privileged or unknown role the request did not acknowledge (D-173).
- Schema version (D-210): `job.version_query_changed` (user actor,
  target the job: `name`, `mode`, `previous_mode`, `custom_sql_changed`;
  presets `preset`, `schema`; custom queries `custom_sql_sha256` and
  `custom_sql_bytes`, never the query itself). Setting or changing a
  custom query raises the security alert `job_custom_version_query`.
- Test restores (task 4.6, D-169): `job.test_restore_updated` (user
  actor, target the job: `name`, `enabled`, `schedule`,
  `target_connection_id`, `target_connection`,
  `target_is_job_connection`); `test_restore.requested` (user actor for
  `runTestRestoreNow`, system actor for the schedule; `job_id`,
  `target_connection_id`, `trigger`, `queue_job_id`, `scheduled_for`);
  `test_restore.finished` (system actor: `job_id`, `target_connection_id`,
  `trigger`, `status`, `error_code`, `run_id`, `databases` with each
  database's `status` and `error_code`); `test_restore.scratch_kept`
  (system actor; `scratch_database`, `restore_id`, `error_code`) when a
  scratch database could not be dropped (also alerted, retried); and
  `test_restore.abandoned` (system actor) from the stuck sweep. The
  internal restores of a test restore record `restore.started` and
  `restore.finished` with `test_restore_id` and `scratch` in the details
  and no `requested_by`.
- Destinations (task 2.12, D-131): `destination.created` (settings
  without credentials, `credentials_set` names), `destination.updated`
  (`changed` field names, `values` with old/new for short non-secret
  fields; CA bundle and public key by name only; `host_key_cleared`),
  `destination.deleted`, `destination.host_key_confirmed` (type,
  fingerprint, `previous_fingerprint`) and `destination.ssh_key_generated`
  (public key fingerprint), and the security event
  `destination.ssh_host_key_mismatch` (host, port, pinned fingerprint;
  recorded when a test meets another host key, D-140). An update that
  moves an SFTP destination lists the re-entered password in
  `reentered_for_new_endpoint`. Credential changes are listed by name only and
  additionally recorded as `secret.changed` (`fields`, `via`). No-op
  updates record nothing. `destination.unrecorded_file_deleted` (D-209):
  an admin deleted an unrecorded file (`name`, `object` (the object key), `size`);
  recorded in the transaction of the delete, so a failed delete leaves no
  entry and an audit failure refuses the delete.
- Connections (task 2.11, D-135): `connection.created` (settings without
  credentials, `credentials_set` names), `connection.updated` (`changed`
  field names, `values` old/new for short non-secret fields,
  `tls_pins_cleared`, `host_key_cleared`, `reentered_for_new_endpoint`
  when a changed endpoint required the passwords again, D-140),
  `connection.deleted`,
  `connection.tls_mode_changed` (from, to, `downgrade`; a downgrade also
  records `security.opt_out` with `setting: connection.tls_mode`),
  `connection.pin_confirmed` / `connection.backup_pin_staged` (subject,
  issuer, anchor SHA-256, leaf SPKI or `leaf_rotation_allowed`, expiry,
  `source` captured/pem, `previous_anchor_sha256`, `promoted_backup_pin`),
  `connection.host_key_confirmed` (type, fingerprint,
  `previous_fingerprint`, bastion), `connection.ssh_key_generated` and the security
  events `connection.tls_pin_mismatch` (`check`, host, port, operation) and
  `connection.ssh_host_key_mismatch` (bastion, pinned fingerprint).
  Credentials appear by name only (and as `secret.changed`).
  Temporary restore connections (task 4.4, D-163):
  `connection.temporary_created` (as `connection.created`, plus
  `expires_at`), `connection.temporary_extended` (from, to),
  `connection.temporary_deleted` (admin, reason `manual`) and
  `connection.temporary_expired` (actor `system`, reason `expired` or
  `restore_finished`); both deletions list `destroyed_fields` and add a
  `secret.changed` entry with `destroyed: true`.
- User administration (D-110) records `user.created`,
  `user.updated`, `user.role_changed`, `user.disabled`, `user.enabled`,
  `user.password_reset`, `user.unlocked`, `user.deleted`,
  `user.second_factors_reset` (lost-factor recovery, D-112: counts of the
  removed factors and revoked sessions) and `session.revoked_all`; the
  break-glass CLI records `cli.admin.created` (also for the first admin,
  D-113), `cli.admin.password_reset`, `cli.admin.mfa_reset`,
  `cli.admin.unlocked` and `cli.admin.disabled` with actor `cli`.
  Passwords and temporary passwords are never recorded; `user.created`
  and `user.password_reset` say only whether one was issued
  (`temporary_login_issued`). `bekup reindex
  --import` (task 4.7) records `cli.run.imported` per imported run (target
  the run id; `job_id`, `job_slug`, `destination_id`, `run_timestamp`,
  `manifest` (the manifest's object key), `manifest_sha256`,
  `signing_key_id`, `artifacts`). (The `setup.*`
  actions of the removed web setup flow no longer exist.)
- SSRF allowlist (D-196): `setting.changed` (target `setting`/
  `netguard.allowlist`; details `setting`, `added_cidrs`, `removed_cidrs`,
  `added_hosts`, `removed_hosts`, `widened`). `widened: true` raises the
  `ssrf_allowlist_widened` security alert.
- Runtime config reload (task 6.13, D-200; target type `config`):
  `config.reload_requested` (user actor, IP; target and `request_id` the
  request id), `config.reloaded` (system actor; target the process's
  instance id; details `request_id`, `trigger` ui/signal, `instance_id`,
  `process_kind`, `status`, and the key-name lists `applied`,
  `restart_required`, `weakened`) and `config.reload_failed` (system actor;
  `error_kind` file/syntax/invalid/apply and the key names
  `error_settings`; never a `*_keys` name, which the redactor treats as a
  secret). A live key a process does not use is not listed by it.
  Never values or the config file path. A non-empty `weakened` list raises
  the `config_reload_weakened` security alert. Each process records its
  entry fail closed before it applies anything.
- E-mail (task 5.1, D-178, D-180): `setting.changed` (target `setting`/`smtp`;
  `changed` field names, `values` old/new, the CA bundle only as a flag,
  `created`, always the `mode` and `may_be_unencrypted`) plus `secret.changed` (`fields: ["password"]`, `change`
  `set`/`cleared`) when the SMTP password changes; `smtp.test_sent`
  (recipient, `ok`, failure `code`, `encrypted`, `tls_version`; never
  server text);
  `user.email_verification_requested` (address, `expires_at`),
  `user.email_verified` (address) and `user.email_verification_failed`
  (`reason`: `wrong_code`, `expired`, `address_changed`,
  `account_disabled`, `no_pending_verification`; `failed_attempts`).
  Codes and passwords are never recorded.
- Notification channels (task 5.3b, D-191; targets `shared_channels` or
  `user_channels`): `notification.channel_created` (name, type, address
  count or `display_url`), `notification.channel_updated` (changed
  `fields`), `notification.channel_deleted`,
  `notification.channel_key_rotated`, `notification.channel_enabled`
  (`previous_reason`) / `notification.channel_disabled`,
  `notification.channel_tested` (`ok`, fixed `code`),
  `notification.channel_attached` (`job_id`, `groups`, `events`) /
  `notification.channel_detached` (`job_id`), plus `secret.changed`
  (`fields: ["url", "signing_key"]`, `via`) when a webhook URL or signing
  key is set. URL paths and signing keys are never recorded.
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
- `security.insecure_db_roles` (actor `system`, target `server`) is recorded
  at every `serve` and `worker` startup outside dev mode in single-account
  mode (`database.single_account`, D-207), with `single_account`, `process`
  and `login_role_findings` (dangerous attributes and memberships of the one
  account, e.g. `CREATEROLE`); fail closed like the entries above.
- `security.serve_alias_conflict` (actor `system`, target `server`) is
  recorded by `serve` in the own-proxy compose shapes when its network
  alias `bekup-serve` also resolves to an address that is not one of its
  own interfaces (another container took the alias on the proxy network;
  D-215), with `alias`, `addresses` (the other container's addresses only)
  and `process`; once per new set of addresses (a failed write is retried
  at the next check, every 20 s). It raises the critical
  `serve_alias_conflict` security alert.
- `security.unpinned_image` (actor `system`, target `server`) is recorded
  at every `serve` and `worker` startup outside dev mode when the bekup
  image follows a tag instead of a digest (`BEKUP_IMAGE_MODE=following`, or
  the runner reports that its own container image has no digest; D-212),
  with `image_mode`, `source` (`env`, `runner` or `env_and_runner`),
  `version`, `setup_version` and `process`; fail closed like the entries
  above. When the runner's verdict arrives only after startup (the runner
  was not ready), the process records it once then (a failure is logged and
  retried at the next runner check).
- `cli.db.bootstrap` (actor `cli`, target `database`, the database name) is
  recorded by `bekup db bootstrap` when it created or changed roles, the
  database or citext of an installation whose schema is already migrated
  (through `bekup_app`, after the change; details: `actions`, `admin_role`,
  `single_account`, `reset_passwords`; never passwords or verifiers). Before
  the first migration there is no audit log; the command's printed report
  is the record (D-207).
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
  records them: `secret.changed` (recorded by 2.11, 2.12, 5.1),
  `setting.changed` (recorded by 5.1; 2.3, 6.9), `security.opt_out` (2.11, 2.12),
  `security.insecure_http` (reserved for a runtime switch), `audit.verify.failed`
  (recorded by the hourly security check, 5.5) and `connection.pin_confirmed` (2.7a, 2.11).
- **Security alerts** (task 5.5, D-190). serve's, the workers' and the
  CLI's auditors have the observer `notify.ObserveAudit`: every
  security-relevant entry also writes its mandatory `security.system`
  (admins) or `security.account` (the affected user) notification event to
  the outbox **in the entry's transaction** (an outbox failure fails the
  entry and so the action, like an audit failure). Every break-glass CLI
  entry (actor kind `cli`) raises a `security.system` alert with the
  action, OS user and host name (D-069). After each `auth.login.failed`
  the observer counts the failures of the last 15 minutes and raises a
  burst alert (per address, account or instance; once per 15 minutes).
  The alert list is in [Notifications](../guides/notifications.md#security-alerts).
  A manifest failure at restore-request time is recorded as
  `restore.verification_failed` with target type `artifact` (`stage:
  request`).
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

## Periodic verification and alerts (task 5.5, D-190)

The hourly `security_check` job (maintenance queue) verifies the chain
the same way: the whole chain at the first run of each worker process and
then once a day, in between only the rows added since the last verified
row. The incremental run first checks that the last verified row still
exists with the hash remembered in memory, so a rewrite of already
verified rows is caught; the checkpoint is never stored in the database
(it could be forged together with the rows). A failure (`hash_mismatch`,
`genesis`, `link_mismatch`, a failing signed head, or `anchor_missing` /
`anchor_rewritten`) is recorded once per finding as `audit.verify.failed`
(system actor, target `audit_log`, details `finding`, `problem`,
`row_id`, `full`), which raises a **critical** `security.system` alert to
every admin (deduplicated per finding across processes). The chain is not
"repaired": investigate with `bekup audit verify --head-file` against an
exported head.

The outcome of every completed verification (time, ok, full or
incremental, the finding and the newest verified row) is stored in
`settings` (`audit.chain_verification`) and shown to admins with the newest
entry and the newest signed head (`auditChainStatus`, the audit page;
D-196). This status is for orientation only (it lives in the same database
as the rows); the in-memory checkpoint above is unchanged and the proof is
still `bekup audit verify` against an exported head.
