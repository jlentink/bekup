# Restore

> **Status:** partial. Staging and verification (task 4.1), the restore
> worker and the PostgreSQL restore (4.2), the MySQL/MariaDB restore (4.3),
> temporary restore connections (4.4), the restore API (4.5) and scheduled
> test restores (4.6) are implemented; the wizard (6.7) follows.

How to restore a backup: allowed targets per role, the mandatory fresh
second-factor check, type-to-confirm for overwrites, stage-verify-restore,
PostgreSQL temp-DB swap versus MySQL/MariaDB in-place overwrite, temporary
restore connections (admin only) and scheduled test restores.

## Requesting a restore

Restores are requested through the GraphQL API (D-167); the web wizard
(task 6.7) builds on the same calls.

### Who may restore what

| Role | Targets |
|---|---|
| Admin | Any connection with the backup's engine: an existing database (overwrite) or a new one; also temporary restore connections; PostgreSQL globals restores. |
| Maintainer | Any permanent connection with the backup's engine, existing or new database. No temporary connections, no globals. |
| Backup owner (role user) | Only backups of jobs they own, and only into a database one of their jobs backs up (overwrite) or a new database on a connection their jobs use. |

Into a connection whose role is **privileged** (or whose privileges are
unknown), only admins may restore, with an explicit acknowledgement: see
[Privileged restore targets](#privileged-restore-targets).

The engine of the backup is taken from its **signed manifest**, which is
verified before the request is accepted (a tampered manifest is refused
and raises a security alert). MySQL and MariaDB are different engines.
Every rule is checked on the server with the job and target loaded from
the database.

### The flow

1. **`restoreTargets(artifactId)`** lists the connections you may restore
   the backup to, with the backup's source facts (engine, version,
   database, whether the job's age identity is needed). The connection the
   job backs up is marked `original`.
2. **`restoreTargetDatabases(artifactId, targetConnectionId)`** lists the
   existing databases of one target through the runner (owners: only the
   databases their jobs back up; bekup's temporary `__bekup_restore_` /
   `__bekup_old_` databases are hidden). The list is cached for 30
   seconds. It makes a runner call, so it must be the only field of its
   request.
   It also returns the target role's privilege verdict
   (`targetPrivileges`, read with the list).
3. **`restorePreview(input)`** checks the request (target rules, engine,
   database name) without running it and returns the **warnings** to show,
   the **confirmation text**, the target role's privilege verdict
   (`targetPrivileges { status reasons acknowledgementRequired }`) and
   `crossSource`. It reads the target through the runner (cached for 30
   seconds), so it must be the only field of its request:

   | Warning | When |
   |---|---|
   | `overwrite_in_place` | MySQL/MariaDB overwrite: the database is dropped and restored in place, no rollback (the full text is quoted below). |
   | `overwrite_swap` | PostgreSQL overwrite: temporary database, then a swap that terminates the target's sessions; room for two copies needed. |
   | `new_database_partial_on_failure` | New database: a failed restore leaves the partial database behind. |
   | `client_version` | Always: the runner restores with the newer of the backup's and the server's client; an older target server may fail. |
   | `cross_source_target` | The target is not the job's own connection: SQL in the dump runs with the target role's privileges; only restore dumps you trust. |
   | `privileged_target` | The target connection's role is privileged, or bekup could not tell (see below): admins only, with `acknowledgePrivilegedTarget`. |
   | `temporary_target`, `target_not_ready`, `insecure_transport` | Facts about the target connection. |
   | `keep_owners` | PostgreSQL with object owners kept. |
   | `identity_required` | The backup is encrypted to an age recipient. |
   | `globals_change_roles` | Globals restore: existing roles in the backup are changed, including passwords. |

4. **Check your second factor.** Every restore requires a passkey check
   with user verification or an authenticator (TOTP) code within the last
   5 minutes (`security.stepup_max_age`), whatever the 2FA policy. A
   password step-up, a recovery code or a passkey without user
   verification is **not** enough. Without a second factor enrolled the
   request answers `REQUIRES_2FA_ENROLLMENT`: enroll one first.
5. **`restoreArtifact(input)`** queues the restore:
   - `mode: NEW_DB` creates `targetDatabase` (it must not exist);
     `mode: OVERWRITE` replaces it and requires `confirmTargetName` equal
     to `targetDatabase` exactly (type the name).
   - `keepOwners` (PostgreSQL only) restores the object owners.
   - `ageIdentity` is required for, and only accepted by, recipient
     encrypted backups. It is stored sealed for at most 15 minutes, read
     once by the worker and never logged, audited or returned.
   - The request must be the only field of its operation.
   - Only one restore into the same database (or the same connection's
     globals) can be queued or running at a time (`CONFLICT`).
   - `acknowledgePrivilegedTarget: true` is required (admins only) when
     the target role is privileged or unknown.
   - The request is audited as `restore.requested` (target, mode, the
     warning codes shown, `cross_source`, the privilege verdict and
     whether it was acknowledged; never the identity).
6. **Follow it** with `restore(id)`, `restoreLogs(restoreId)` and the
   `restoreUpdated(restoreId)` event stream, which re-checks your access
   for every event and ends after the restore finished. `restores(filter)`
   lists restores (owners: their own jobs' restores).

**Globals** (PostgreSQL, admins only): preview with `restorePreview`
(`artifactId` + `targetConnectionId`, no mode or database), then
`restoreGlobals(input)` with `confirmPhrase` exactly
`restore globals into <connection name>` and the same fresh second-factor
check. A globals restore needs a role that can create and alter roles, so
its target is normally privileged: add `acknowledgePrivilegedTarget: true`.

**Cancel:** admins and maintainers may cancel a queued or running restore
with `cancelRestore(id)` (audited `restore.cancel_requested`; backup owners
cannot). A queued restore ends as `canceled` at once; a running one is
stopped by its worker (see below).

**Crashed workers:** a restore whose worker died is failed with code
`abandoned` by a sweep every 5 minutes once `workers.restore_timeout` plus
15 minutes have passed since it started (or 15 minutes after its queue job
ended without a result). Its hold on the backup, its concurrency slot and
any sealed identity are released, the event is audited as
`restore.abandoned`. A temporary (`__bekup_restore_`) or partially restored
new database may remain on the target; an overwritten PostgreSQL database
is only replaced at the final swap.

### Privileged restore targets

SQL inside a dump runs with the rights of the target connection's role. The
restore clients are hardened against client-side escapes, but a dump from a
malicious source database (for example one a backup owner controls)
restored with a superuser role could run `COPY ... FROM PROGRAM`,
`ALTER SYSTEM`, `SELECT ... INTO OUTFILE` and the like and take over the
target server (D-068, D-168).

The runner's `Probe` therefore classifies the connection's role, inside its
hardened read-only session:

| Engine | Privileged when the role has |
|---|---|
| PostgreSQL | `SUPERUSER`, `CREATEROLE`, `REPLICATION` or `BYPASSRLS`; membership (direct or indirect, usable with `SET ROLE`) in a role with one of these, or in `pg_execute_server_program`, `pg_read_server_files`, `pg_write_server_files`, `pg_signal_backend`, `pg_read_all_stats`, `pg_create_subscription`, `pg_read_all_data` or `pg_write_all_data` (the last two reach every database the role can connect to). Owning databases and `CREATEDB` are fine. `CONNECT` on other databases is not checked. |
| MySQL / MariaDB | From `SHOW GRANTS`: any global (`*.*`) privilege beyond data and schema work (e.g. `SUPER`, `FILE`, `PROCESS`, `RELOAD`, `SHUTDOWN`, `CREATE USER`, `ALL PRIVILEGES`, `SET_USER_ID`, `SYSTEM_USER`, `SYSTEM_VARIABLES_ADMIN`, MariaDB's `SET USER`, `BINLOG ADMIN`, ... and any dynamic privilege bekup does not know; reason `global:<NAME>`); global (`*.*`) **data and schema** privileges (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP`, `ALTER`, `INDEX`, `EXECUTE`, `EVENT`, `TRIGGER`, `CREATE/ALTER ROUTINE`, `CREATE/SHOW VIEW`, ...: they reach every schema of the server; reason `global_data:<NAME>`); global `WITH GRANT OPTION`; `PROXY` grants; write privileges on the `mysql` schema. Only `USAGE`, `SHOW DATABASES`, `REPLICATION CLIENT` and a few monitoring privileges are fine globally. |

The verdict is **unknown** when bekup cannot tell completely: roles granted
to a MySQL/MariaDB account (`roles_granted`: their privileges are not
listed and the dump could `SET ROLE` them), a grant line it does not
understand, a failed check, or a target connection that is not ready.
Unknown is treated like privileged (fail closed).

For a privileged or unknown target:

- `restorePreview` and `restoreTargetDatabases` show
  `targetPrivileges.status` (`PRIVILEGED` / `UNKNOWN`), the reason codes
  (e.g. `superuser`, `member_of:pg_execute_server_program`,
  `member_of_role_with:superuser`, `member_of:pg_read_all_data`,
  `global:FILE`, `global_data:SELECT`, `roles_granted`) and the
  `privileged_target` warning. `testConnection` returns the same verdict.
- `restoreArtifact` / `restoreGlobals` check the role again (a fresh runner
  call, never the cache). Maintainers and backup owners get `FORBIDDEN`;
  admins get `VALIDATION` on `acknowledgePrivilegedTarget` until they set
  it to `true`.
- The acknowledged request is audited (`target_privileges`,
  `target_privilege_reasons`, `privileged_target_acknowledged`,
  `cross_source`) and raises the security alert
  `restore_privileged_target`.

The request stores the verdict, its reasons and the admin's
acknowledgement on the restore (D-173). When the worker takes the restore
(after it got its run slot, before anything is staged) it probes the role
**again**: if the role is privileged or unknown now, the restore only goes
ahead when it was acknowledged by an admin who is still an active admin
and every current reason is one that admin acknowledged. Otherwise it
fails with `privileged_target`, audited as
`restore.privileged_target_refused` (with `refusal`:
`not_acknowledged`, `new_reasons` or `requester_not_admin`) and alerted;
request it again to see and acknowledge the new verdict. Use a
dedicated, non-superuser restore role (see
[Database roles](../security/db-roles.md#restore-roles-for-target-databases));
restores into it need no acknowledgement and stay open to maintainers and
backup owners.

## Stage → verify → restore

bekup never restores straight from a destination. Every restore first
copies the stored backup to the scratch volume, verifies it in full and
only then streams it into the target database (D-022, D-162). A backup
that fails verification never reaches a database.

A restore moves through these statuses:

| Status | What happens |
|---|---|
| `queued` | Requested; waiting for a restore worker. |
| `staging` | The stored object is downloaded to the scratch volume. |
| `verifying` | Its SHA-256 and the run's signed manifest are checked against bekup's records. |
| `restoring` | The dump is decrypted, decompressed and streamed to the target (tasks 4.2/4.3). |
| `succeeded` / `failed` / `canceled` | Final. A failed restore carries a fixed error code, never server output. |

### What is verified

Before a single byte is restored:

1. **Free space.** The scratch volume must hold the stored size of the
   backup plus `storage.scratch_reserve_bytes` (default 1 GiB). Otherwise
   the restore fails with `insufficient_scratch_space` before anything is
   downloaded.
2. **Stored digest.** The downloaded bytes are hashed while they are
   written; size and SHA-256 must equal the artifact record.
3. **Signed manifest.** The run's `manifest.json` is read from the same
   destination and its ed25519 instance signature is verified. It must
   belong to exactly this job and run (a validly signed manifest of another
   run, a *replayed* manifest, is refused), list exactly the objects bekup
   recorded as signed for the run there, and name the same encryption mode
   and key version as the artifact record.
4. **While restoring**, the decrypted dump is bounded by its recorded size
   and, at its end, both the stored and the dump SHA-256 are checked again.

Any mismatch fails the restore with `verification_failed`, writes the audit
event `restore.verification_failed` (with the reason, for example
`stored_digest_mismatch`, `manifest_bad_signature` or `manifest_replayed`)
and raises a **security alert**: a backup that does not match what bekup
signed may have been tampered with on the destination. Investigate the
destination before retrying; do not try to "fix" the record.

### Keys

- **Passphrase-encrypted backups** are decrypted with the job's passphrase
  of the version the backup was written with (old versions stay available,
  D-142).
- **Recipient-encrypted backups** need the private age identity
  (`AGE-SECRET-KEY-1...`). It is sent with the restore request, checked
  against the job's recipient, and stored only sealed with the instance
  keyring in a handoff table for at most **15 minutes**; it never goes into
  the job queue. The worker reads it once when the restore starts (the row
  is deleted in the same step) and destroys it right after decryption
  starts. If the restore does not start within 15 minutes the identity
  expires (`identity_expired`); request the restore again.
- **Unencrypted (opted-out) backups** need no key.

### The scratch volume

`storage.scratch_dir` (default `/var/lib/bekup/scratch`) holds one private
directory per running restore (`restore-<id>/`, mode 0700, one 0600 file).
The staged copy is always wrapped with a one-time age key that exists only
in the worker's memory: even the staging of an unencrypted backup is never
plaintext on disk, and a staged file cannot be swapped unnoticed after it
was verified.

- Size the volume for the largest stored backup you restore, times the
  number of concurrent restores (`workers.restore_queue_workers`), plus the
  reserve.
- The directory must not be group- or world-writable; bekup creates it with
  mode 0700 when it is missing.
- Staging files are removed when the restore ends, fails, is canceled, or
  its worker panics. A sweeper in `bekup serve` and `bekup worker` removes
  leftovers (for example after a crash) at startup and every 10 minutes;
  directories of restores still running are only removed once older than
  `storage.scratch_stale_after` (default 24 h, longer than
  `workers.restore_timeout`).

### Retention during a restore

A restore holds its backup for its whole duration (D-155): retention skips
a held backup and deletes it only on a later sweep, so a long restore never
loses its source.

## The restore worker

Restores run in the `restore` queue (`workers.restore_queue_workers`,
`workers.restore_timeout`) and are **never retried**: a failed restore
stays failed; request a new one (D-145, D-166).

- **Concurrency.** A restore takes a run slot for its **target**
  connection from the same pool as backups (`workers.max_concurrent_runs`,
  the connection's run limit or `workers.max_runs_per_connection`). While
  none is free it waits in `queued`.
- **Target re-check.** When it starts, the target connection must still
  exist (a temporary one must not have expired), its role's privilege
  verdict is checked again (see
  [Privileged restore targets](#privileged-restore-targets)), and after
  verification its engine must equal the engine in the backup's signed
  manifest (`engine_mismatch` otherwise; MySQL and MariaDB are different
  engines). Once started, a restore keeps using a temporary target that
  expires meanwhile (the cleanup never deletes a connection while a run
  holds a slot on it); a deleted one stops it. Requests into a temporary
  connection that expires within `workers.restore_timeout` are refused:
  extend it first.
- **Client version.** The runner restores with the newer of the backup's
  client and the target server's version (for example a PostgreSQL 16
  backup into a PostgreSQL 17 server uses the 17 client).
- **Restore log.** Worker events and the restore tool's output are stored
  per restore (same caps as run logs: `workers.run_log_max_lines`,
  `workers.run_log_max_line_bytes`), with the target's credentials and
  credentials in URLs redacted. Live views update through restore events.
- **Cancel and timeout.** Canceling a running restore (or reaching
  `workers.restore_timeout`) kills the restore container at once; a
  PostgreSQL overwrite then drops its temporary database and the target
  stays as it was.
- **Audit.** `restore.started` and `restore.finished` (status, error code,
  warnings) are recorded with the system actor; the request itself is
  audited by `restoreArtifact`.

Error codes a restore can end with, besides the verification codes above:
`engine_mismatch`, `unsupported_restore`, `target_unavailable`,
`target_not_ready`, `privileged_target` (the role became privileged
after the request), `restore_failed` (the tool reported an error; see the
restore log), `neutralize_failed` and `swap_state_unknown` (PostgreSQL,
below), `restore_incomplete`, `timeout`, `canceled`, `interrupted`
(the worker stopped), `slot_lost`, the runner's target reasons
(`target_unreachable`, `target_auth_failed`, `tls_pin_mismatch`,
`target_permission_denied`, `target_database_exists`, ...) and
`runner_unavailable` / `runner_busy`.

## PostgreSQL

PostgreSQL database backups are custom-format archives, restored with
`pg_restore --exit-on-error --no-password` in an isolated container (D-166).
By default `--no-owner` is added: every restored object belongs to the
target connection's role, so a backup cannot hand objects to whatever roles
happen to exist on the target. Request the restore with owners kept when
the target has the same roles as the source.

The connection's role needs `CREATEDB` (a new database or the temporary
database of an overwrite is created with `CREATE DATABASE ... TEMPLATE
template0`) and, for an overwrite, the right to rename and drop the target
database (its owner or a superuser).

### Hardening of the restored database

With `--no-owner` every restored function belongs to the restore role, so
a `SECURITY DEFINER` function from the dump would run **with the restore
role's rights for every role that may call it** (for example a function
doing `ALTER ROLE CURRENT_USER PASSWORD ...` lets any database user take
over the restore role). bekup therefore hardens every restored PostgreSQL
database before it becomes visible (D-171):

- `PUBLIC` gets no `CONNECT` and no `TEMPORARY` on any database bekup
  creates (revoked right after `CREATE DATABASE`). Only the restore role
  (the owner) and superusers can connect until you grant access.
- After `pg_restore` succeeded and **before** the swap (or before a new
  database is reported as restored), the runner switches every `SECURITY
  DEFINER` function and procedure to `SECURITY INVOKER`, in one
  transaction of its hardened session, and checks that none is left and
  that `PUBLIC` has no access. Only databases owned by the restore role are
  accepted.
- If routines were switched, the restore ends with the warning
  `secdef_neutralized`, the restore log lists them (up to 50 signatures),
  and `restore.secdef_neutralized` is audited before the swap.
- If the hardening fails (or a session of another non-superuser role was
  connected to the restored database meanwhile), the restore stops with
  `neutralize_failed` **before** the swap: an overwritten database stays
  unchanged; a new database stays on the server without `PUBLIC` access
  and should be dropped.

After the restore, grant `CONNECT` (and whatever else they need) to the
roles that use the database. An admin may re-enable `SECURITY DEFINER`
deliberately for functions they reviewed (`ALTER FUNCTION ... SECURITY
DEFINER`), ideally with a pinned `search_path` and owned by a role with
only the rights the function needs. `restorePreview` shows the warning
`restore_hardening` for every PostgreSQL database restore. MySQL/MariaDB
`SQL SECURITY DEFINER` routines and views are not changed (they need
explicit `EXECUTE` / `SELECT` grants; review them after a restore).
Test restores additionally restore into their scratch database with
`pg_restore --no-privileges` (the dump's grants are skipped; the runner
allows this option only for `bekup_test_` scratch databases).

### Restore into a new database

bekup creates the database (it must not exist yet) and restores into it.
`pg_restore` stops at the first error. If the restore fails, the new,
partially restored database is left behind (warning
`partial_new_database`); drop it or restore again into another name.

### Overwrite via a temporary database and swap

An existing database is never written directly:

1. bekup creates `<db>__bekup_restore_<id>`, restores into it and
   hardens it (see above). The target stays online and untouched
   meanwhile.
2. Only after `pg_restore` and the hardening finished without error, the
   runner blocks new
   connections to the target, **terminates its sessions** and, in one
   transaction, renames the target to `<db>__bekup_old_<id>` and the
   temporary database to `<db>` (retried briefly while terminated sessions
   exit; connections are allowed again if the swap fails).
3. The replaced database is dropped. If that fails, the restore still
   succeeds with the warning `old_database_kept`; drop
   `<db>__bekup_old_<id>` by hand.

If the restore fails, times out or is canceled at any point before the
swap, the temporary database is dropped and the target keeps its data
(warning `temp_database_kept` if even the drop failed). If the swap call
itself fails, bekup asks the runner which databases accept connections:
when the target is there and the temporary database is gone, the swap did
happen (logged; the restore succeeds and the replaced database is
dropped); when both are there, the target is unchanged. Otherwise the
restore fails with `swap_state_unknown` and the log names the databases
to check; bekup never reports "unchanged" without proof (if the temporary
database still existed when it was dropped, the swap had not happened).
Overwriting replaces the database as a whole, including its database-level
grants: grant `CONNECT` to the application roles again afterwards. The runner can
only drop these bekup-named temporary databases; it derives the names
itself from the restore id.

Plan for the disk space of a second copy of the database during an
overwrite, and for the short interruption at the swap: clients connected to
the target are disconnected.

### Globals (admin only)

A PostgreSQL backup job can include the cluster globals (roles and their
attributes, memberships, tablespaces). Restoring them is a **separate
action for admins**; it never runs together with a database restore and
takes no database name.

The globals script is run with `psql --no-psqlrc -v ON_ERROR_STOP=1`
against the maintenance database, under a one-time `\restrict` key, so no
psql meta-command in the backup (`\!`, `\copy ... program`, `\i`, ...) can
run (D-138). `pg_dumpall` writes `CREATE ROLE <name>;` for every role and
expects that statement to fail for roles that already exist; bekup turns
these lines into statements that skip an existing role, so the `ALTER ROLE`
that follows updates it instead. Any other error (for example an existing
tablespace) stops the script; statements before it stay applied.

> A globals restore changes existing roles that are in the backup,
> including their passwords and attributes, and possibly the role the
> target connection uses. Restore globals only into a server you are
> rebuilding.

## MySQL and MariaDB

MySQL and MariaDB backups are plain SQL dumps (`mysqldump` /
`mariadb-dump` without `--databases`). They are restored through the runner
into the engine's own client in a short-lived, isolated container (D-165):

- **MySQL:** `mysql --system-command=OFF --commands=OFF --local-infile=0
  --default-character-set=utf8mb4 --database=<db>`
- **MariaDB:** `mariadb --sandbox --local-infile=0
  --default-character-set=utf8mb4 --database=<db>`

These flags make the client refuse shell commands (`\!`, `system`),
`source`, `tee` and `LOAD DATA LOCAL INFILE` in a dump, so a malicious
backup cannot run commands or read files in the container. The SQL in the
dump still runs with the rights of the target connection's user: use a
restore user that is limited to the databases it may restore.

Rules:

- **Same engine only.** A MySQL backup is never restored into MariaDB or
  the other way round; the restore is refused before anything runs.
- **No globals.** MySQL and MariaDB have no separate globals backup; a
  globals restore is refused.
- **Client version.** The runner picks the restore client itself: the
  newer of the backup's client and the target server's version. If the
  catalog has no client that new, it uses the newest one and the restore
  carries the warning `client_older_than_server`.
- **Complete dumps only.** The dump must end with the `-- Dump completed`
  line and have exactly the recorded size. Otherwise the restore is
  canceled before the client reads the end of the dump, and it fails. The
  client also stops at the first failing statement. Its error lines show up
  in the restore log.

### Restore into a new database

bekup creates the database (it must not exist yet) and restores the backup
into it. If the restore fails, the new, partially restored database is
left behind (the restore log and the warning `partial_new_database` say
so). Drop it by hand or restore again into another name.

### Overwrite in place

MySQL and MariaDB cannot rename a database, so an overwrite cannot use a
temporary database and a swap like PostgreSQL does. The existing database
is **replaced in place**, after an explicit warning and the type-to-confirm
of its name:

> MySQL and MariaDB databases are overwritten in place: the target database
> is dropped and recreated (same character set and collation), then the
> backup is restored into it. Every table, view, routine, trigger and event
> that is not in the backup is permanently removed. If the restore fails,
> the database stays empty or partially restored; there is no automatic
> rollback. Grants on the database are kept.

All of this happens in one client session. bekup reads the database's
default character set and collation from `information_schema.SCHEMATA`,
prepares the `CREATE DATABASE`, then runs `DROP DATABASE`, recreates the
database and restores the dump into it. If the database does not exist, or
its defaults cannot be read, the restore fails before anything is dropped.

- The connection's user needs the `DROP` and `CREATE` privileges on the
  database (plus whatever the dump itself needs).
- Sessions that have open transactions on the database's tables make the
  `DROP` wait until they finish.
- Take a fresh backup of the target first if you might need its current
  contents.

## Scheduled test restores

A backup is only proven complete when it restores. A job can therefore be
given a **scratch connection** on which bekup regularly restores the job's
newest backup, checks it against the statistics recorded when the backup
was taken (see [completeness evidence](jobs-and-schedules.md#completeness-evidence-verification-levels-and-warnings))
and drops it again (D-169). The settings are described in
[jobs and schedules](jobs-and-schedules.md#test-restores).

### Privileged scratch connections

SQL inside a tested backup runs with the scratch connection's role, as in
every restore (D-068). Designating a scratch connection therefore probes
its role through the runner (D-170), with the same verdict the restore
wizard shows:

- **Not privileged:** admins and maintainers can designate it (with a
  fresh passkey or authenticator check).
- **Privileged or unknown** (superuser-like attributes or roles,
  administrative MySQL/MariaDB grants, MySQL/MariaDB roles, a connection
  that is not ready): only an **admin** can designate it, and only with
  `acknowledgePrivilegedTarget: true` (maintainers get `FORBIDDEN`, admins
  without it `VALIDATION`). The acknowledgement is audited
  (`job.test_restore_updated` with `target_privileges`,
  `target_privilege_reasons` and `privileged_target_acknowledged`), raises
  the security alert `test_restore_privileged_target`, and is stored with
  the verdict (`Job.testRestore.targetPrivileges`,
  `privilegedTargetAcknowledgedAt`). Changing the settings of a job with
  such a target later again needs an admin and the acknowledgement.

Before **every** test restore the worker probes the role again. If it is
privileged or unknown and the stored admin acknowledgement does not cover
all of its current reasons (for example a role that became superuser after
it was designated, or a target designated without a privileged verdict),
or the probe fails, the test restore ends as `ERROR` with
`privileged_target` before anything is created on the target; an admin
must designate the connection again with the acknowledgement. A dedicated,
unprivileged scratch role is strongly recommended
([database roles](../security/db-roles.md)).

### What a test restore does

1. It takes the job's newest **successful** run whose database backups are
   present, verified and covered by a signed manifest. The PostgreSQL
   globals are never test-restored.
2. For every database of that run it restores the backup into a **new**
   database `bekup_test_<id>` on the scratch connection. It is an ordinary
   restore (staging, the full verification against the signed manifest,
   the engine's restore tool, `--no-owner` on PostgreSQL) recorded as a
   restore without a requester; it shows up in the job's restores and its
   log is kept. It never overwrites anything: the database must not exist.
   On PostgreSQL the scratch database is created without `CONNECT` and
   `TEMPORARY` for `PUBLIC`, and after `pg_restore` every `SECURITY
   DEFINER` function is switched to `SECURITY INVOKER` before the restore
   counts as successful (the restore role owns the restored objects);
   if that hardening fails, the database is not inspected and is dropped.
3. It reads the statistics of the restored database through the runner,
   at the same level as the backup's evidence: exact row counts on
   PostgreSQL (in the same hardened session the backup statistics use),
   catalog estimates on MySQL and MariaDB.
4. It compares them with the backup's statistics (below).
5. It **always drops** the scratch database: after the check, when the
   restore failed, when the statistics could not be read and when the
   test restore was canceled. If the drop fails, the database stays
   marked as not dropped, an audit entry (`test_restore.scratch_kept`) and
   an alert are raised, and bekup retries the drop every minute.

The scratch database's name is derived by the runner from the restore's
id; the runner's `ScratchStats` and `DropScratchDatabase` calls accept only
that id, so they can never inspect or drop any other database. Names
starting with `bekup_test_` are reserved: restores refuse them as targets
and the database lists hide them.

### The comparison

| Check | Result when it differs |
|---|---|
| Object counts (schemas, tables, views, materialized views, sequences, functions, procedures, triggers, extensions; MySQL/MariaDB events) | **failed** |
| A table of the backup is missing, or an extra table appears | **failed** |
| PostgreSQL row count recorded exactly in the dump's own snapshot | **failed** |
| Row count recorded as an estimate (PostgreSQL `standard` level, every MySQL/MariaDB table) | warning with the difference |
| A count is unknown on either side (timeout, budget, no privilege) | warning |
| The backup recorded completeness warnings (privileges, non-transactional tables, ...) | warning |
| The backup has no statistics | warning (the restore itself was still tested) |

MySQL and MariaDB statistics are always estimates (D-153), so a test
restore of those engines never fails on row counts; it fails on missing
objects or tables. When a table list was too long to itemize, only the
counts are compared and a warning says so. The findings list names and
counts only (at most 200 per database), never data.

A PostgreSQL source database without a `public` schema has one schema
less than its restored copy (new databases always have `public`, and
`pg_dump` never drops it). That single difference is treated as equal
when neither side has a table in `public` and neither table list was
truncated (D-170); every other object count must still match exactly.

### Results

Every test restore records, per database, the verdict (`PASSED`,
`WARNINGS`, `FAILED`, `ERROR` when it could not be restored or checked,
`CANCELED`), the findings, the internal restore and whether the scratch
database was dropped. The test restore's own status is `FAILED` when any
database failed, `ERROR` when any could not be checked, then `WARNINGS` or
`PASSED`. Each test restore is audited (`test_restore.requested`,
`test_restore.finished`), emits `test_restore.passed`, `warnings` or
`failed` (D-161; errors and cancels count as failed), and a failed or
erroring one raises the `run.test_restore_failed` alert.

Test restores share the concurrency slots with backups and restores (one
slot on the scratch connection for the whole test restore), run in the
restore queue with `workers.restore_timeout` for all databases together,
and a job has at most one queued or running test restore. A test restore
whose worker crashed is ended as `ERROR` (`abandoned`) by the periodic
check, and its scratch databases are dropped by the retry.

Backups encrypted to an age recipient cannot be test-restored: bekup does
not hold the identity (`identity_required`).

### GraphQL

| Field | Who | Notes |
|---|---|---|
| `Job.testRestore` | everyone who sees the job | settings, scratch connection, next run |
| `setJobTestRestore(jobId, input)` | admins, maintainers, fresh passkey/TOTP; privileged or unknown scratch roles: admins with `acknowledgePrivilegedTarget` | see [jobs and schedules](jobs-and-schedules.md#test-restores) |
| `runTestRestoreNow(jobId)` | admins, maintainers | CONFLICT while one is queued or running |
| `testRestores(jobId, first, after)` | admins, maintainers, the job's owners | newest first |
| `testRestore(id)` | admins, maintainers, the job's owners | with the databases and findings |
