-- Restores and the identity handoff (task 4.1, D-162). Only
-- internal/restore uses these. The artifact facts of a restore come from
-- GetDownloadArtifact and the manifest lookups from
-- GetDownloadManifestArtifact / ListDownloadManifestEntries (shared through
-- internal/artifactverify).

-- name: InsertRestore :one
INSERT INTO restores (id, artifact_id, run_item_id, job_id, target_connection_id, target_db_name, mode, requested_by, no_owner,
                      target_privileges, target_privilege_reasons, privileged_target_acknowledged)
VALUES (@id, @artifact_id, @run_item_id, @job_id, @target_connection_id, sqlc.narg(target_db_name)::text, @mode,
        sqlc.narg(requested_by)::uuid, @no_owner, sqlc.narg(target_privileges)::text,
        COALESCE(sqlc.narg(target_privilege_reasons)::text[], '{}'), @privileged_target_acknowledged)
RETURNING *;

-- name: GetRestore :one
SELECT * FROM restores WHERE id = $1;

-- StartRestoreStaging moves a queued restore to staging (once: a second
-- worker attempt finds it no longer queued).
-- name: StartRestoreStaging :one
UPDATE restores SET status = 'staging', started_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING *;

-- name: AdvanceRestoreStatus :execrows
UPDATE restores SET status = @to_status
WHERE id = @id AND status = @from_status;

-- name: SetRestoreStagedBytes :execrows
UPDATE restores SET staged_bytes = @staged_bytes
WHERE id = @id AND status IN ('staging', 'verifying', 'restoring');

-- FinishRestore sets a terminal status (no-op on a finished restore)
-- with the outcome (log counts, warning codes).
-- name: FinishRestore :execrows
UPDATE restores SET status = @status, finished_at = now(),
       error_code = sqlc.narg(error_code)::text, error_message = sqlc.narg(error_message)::text,
       log_lines = @log_lines::int, log_lines_dropped = @log_lines_dropped::int, warnings = @warnings::text[]
WHERE id = @id AND status IN ('queued', 'staging', 'verifying', 'restoring');

-- name: SetRestoreRiverJob :execrows
UPDATE restores SET river_job_id = @river_job_id
WHERE id = @id AND river_job_id IS NULL;

-- ListRestoreStatuses returns the status of the given restores (scratch
-- sweeper).
-- name: ListRestoreStatuses :many
SELECT id, status FROM restores WHERE id = ANY(@ids::uuid[]);

-- InsertRestoreIdentityHandoff stores a sealed identity for a queued
-- restore; it expires ttl_seconds (at most 15 minutes) from now.
-- name: InsertRestoreIdentityHandoff :exec
INSERT INTO restore_identity_handoff (restore_id, sealed_identity, expires_at)
VALUES (@restore_id, @sealed_identity, now() + make_interval(secs => @ttl_seconds::float8));

-- TakeRestoreIdentityHandoff deletes the handoff of a restore and returns
-- it: an identity is read at most once. valid is false when it had
-- expired.
-- name: TakeRestoreIdentityHandoff :one
DELETE FROM restore_identity_handoff WHERE restore_id = $1
RETURNING sealed_identity, (expires_at > now())::boolean AS valid;

-- name: DeleteExpiredRestoreIdentityHandoffs :execrows
DELETE FROM restore_identity_handoff WHERE expires_at <= now();

-- InsertRestoreLogs appends a batch of restore log lines (arrays of equal
-- length; task 4.2).
-- name: InsertRestoreLogs :exec
INSERT INTO restore_logs (restore_id, ts, level, stream, line)
SELECT @restore_id::uuid, unnest(@ts::timestamptz[]), unnest(@levels::text[])::run_log_level,
       unnest(@streams::text[])::run_log_stream, unnest(@lines::text[]);

-- name: ListRestoreLogs :many
SELECT * FROM restore_logs WHERE restore_id = @restore_id AND id > @after_id ORDER BY id LIMIT @max_rows::int;

-- Restore API reads and writes (task 4.5). Listings take scope_all = false
-- to limit rows to the jobs @user_id owns (authz.JobScope, the same rule as
-- IsJobOwner: deleted users own nothing).

-- ListRestoresPage returns one page of restores, newest first
-- ((created_at, id) descending), after the cursor.
-- name: ListRestoresPage :many
SELECT sqlc.embed(restores), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       c.name AS target_name, c.engine AS target_engine, c.temporary AS target_temporary,
       c.deleted_at AS target_deleted_at, ri.kind AS item_kind, ri.db_name AS source_db_name, ri.run_id AS run_id,
       u.display_name AS requested_by_name, u.email AS requested_by_email
FROM restores
JOIN jobs j ON j.id = restores.job_id
JOIN connections c ON c.id = restores.target_connection_id
JOIN run_items ri ON ri.id = restores.run_item_id
LEFT JOIN users u ON u.id = restores.requested_by
WHERE (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = restores.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
  AND (sqlc.narg(job_id)::uuid IS NULL OR restores.job_id = sqlc.narg(job_id)::uuid)
  AND (sqlc.narg(artifact_id)::uuid IS NULL OR restores.artifact_id = sqlc.narg(artifact_id)::uuid)
  AND (sqlc.narg(target_connection_id)::uuid IS NULL OR restores.target_connection_id = sqlc.narg(target_connection_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR restores.status::text = sqlc.narg(status)::text)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (restores.created_at, restores.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY restores.created_at DESC, restores.id DESC
LIMIT @max_rows::int;

-- name: GetRestoreView :one
SELECT sqlc.embed(restores), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       c.name AS target_name, c.engine AS target_engine, c.temporary AS target_temporary,
       c.deleted_at AS target_deleted_at, ri.kind AS item_kind, ri.db_name AS source_db_name, ri.run_id AS run_id,
       u.display_name AS requested_by_name, u.email AS requested_by_email
FROM restores
JOIN jobs j ON j.id = restores.job_id
JOIN connections c ON c.id = restores.target_connection_id
JOIN run_items ri ON ri.id = restores.run_item_id
LEFT JOIN users u ON u.id = restores.requested_by
WHERE restores.id = $1;

-- LockRestore locks a restore row (cancelRestore, the stuck sweep).
-- name: LockRestore :one
SELECT * FROM restores WHERE id = $1 FOR UPDATE;

-- DeleteRestoreIdentityHandoff removes the identity handoff of a restore
-- that will never start (canceled or abandoned).
-- name: DeleteRestoreIdentityHandoff :execrows
DELETE FROM restore_identity_handoff WHERE restore_id = $1;

-- ListStuckRestores finds restores in a non-terminal status that no worker
-- will finish: started before @started_before (the restore timeout plus a
-- grace period has passed, so a live worker would have ended it), or idle
-- since @idle_before while their River job is gone or already finalized
-- (a crashed or discarded attempt).
-- name: ListStuckRestores :many
SELECT r.id, r.job_id, r.artifact_id, r.target_connection_id, r.status, r.started_at, r.river_job_id
FROM restores r
LEFT JOIN river_job rj ON rj.id = r.river_job_id
WHERE r.status IN ('queued', 'staging', 'verifying', 'restoring')
  AND ((r.started_at IS NOT NULL AND r.started_at < @started_before::timestamptz)
       OR (r.updated_at < @idle_before::timestamptz
           AND (rj.id IS NULL OR rj.state::text IN ('completed', 'discarded', 'cancelled'))))
ORDER BY r.created_at, r.id
LIMIT @max_rows::int;

-- IsRestoreStuck re-checks ListStuckRestores' condition for one restore
-- under its row lock (the sweep).
-- name: IsRestoreStuck :one
SELECT EXISTS (
    SELECT 1 FROM restores r
    LEFT JOIN river_job rj ON rj.id = r.river_job_id
    WHERE r.id = @id
      AND r.status IN ('queued', 'staging', 'verifying', 'restoring')
      AND ((r.started_at IS NOT NULL AND r.started_at < @started_before::timestamptz)
           OR (r.updated_at < @idle_before::timestamptz
               AND (rj.id IS NULL OR rj.state::text IN ('completed', 'discarded', 'cancelled'))))
)::boolean;

-- LockRestoreTargetName serializes restore requests for one target
-- database (or one connection's globals) until the transaction ends.
-- name: LockRestoreTargetName :exec
SELECT pg_advisory_xact_lock(hashtextextended(@lock_key::text, 0));

-- ActiveRestoreForTarget reports a queued or running restore into the same
-- target database (NULL: the connection's globals).
-- name: ActiveRestoreForTarget :one
SELECT EXISTS (
    SELECT 1 FROM restores
    WHERE target_connection_id = @target_connection_id
      AND target_db_name IS NOT DISTINCT FROM sqlc.narg(target_db_name)::text
      AND status IN ('queued', 'staging', 'verifying', 'restoring')
)::boolean;

-- ListRestoreIDsUpdatedSince returns the restores changed at or after
-- @since that the caller may see (scope as in ListRestoresPage), newest
-- change first: the resync of the restoresUpdated stream after possibly
-- lost events (D-196). Delivery re-loads and re-authorizes every one.
-- name: ListRestoreIDsUpdatedSince :many
SELECT restores.id
FROM restores
WHERE restores.updated_at >= @since::timestamptz
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = restores.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
ORDER BY restores.updated_at DESC, restores.id
LIMIT @max_rows::int;
