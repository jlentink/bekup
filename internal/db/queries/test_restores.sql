-- Scheduled test restores (task 4.6). The settings live on jobs and are
-- written only by internal/jobs; test_restores / test_restore_items only by
-- internal/restore.

-- SetJobTestRestore changes a job's test-restore settings.
-- name: SetJobTestRestore :one
UPDATE jobs
SET test_restore_enabled = @enabled,
    test_restore_schedule = @schedule,
    test_restore_connection_id = sqlc.narg(connection_id)::uuid,
    test_restore_next_at = sqlc.narg(next_at)::timestamptz,
    test_restore_target_privileges = sqlc.narg(target_privileges)::text,
    test_restore_privilege_reasons = @privilege_reasons::text[],
    test_restore_privileged_ack_by = sqlc.narg(privileged_ack_by)::uuid,
    test_restore_privileged_ack_at = sqlc.narg(privileged_ack_at)::timestamptz,
    updated_by = @updated_by
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- GetTestRestoreConnection reads a connection the test-restore worker
-- checks before it starts (a plain read: the worker holds no transaction;
-- the run slot and the restore worker's own share lock protect the run).
-- name: GetTestRestoreConnection :one
SELECT id, name, engine, temporary, deleted_at FROM connections WHERE id = $1;

-- LockDueTestRestores locks the jobs whose next test restore is due.
-- name: LockDueTestRestores :many
SELECT id, test_restore_schedule, test_restore_next_at, test_restore_connection_id
FROM jobs
WHERE test_restore_enabled AND deleted_at IS NULL
  AND test_restore_next_at IS NOT NULL AND test_restore_next_at <= @now::timestamptz
ORDER BY test_restore_next_at, id
LIMIT @max_rows::int
FOR UPDATE SKIP LOCKED;

-- AdvanceTestRestore sets the next scheduled test restore of a job.
-- name: AdvanceTestRestore :exec
UPDATE jobs SET test_restore_next_at = sqlc.narg(next_at)::timestamptz WHERE id = @id;

-- InsertTestRestore records a queued test restore; no row when the job
-- already has a queued or running one.
-- name: InsertTestRestore :one
INSERT INTO test_restores (id, job_id, target_connection_id, trigger, requested_by, scheduled_for)
VALUES (@id, @job_id, @target_connection_id, @trigger, sqlc.narg(requested_by)::uuid, sqlc.narg(scheduled_for)::timestamptz)
ON CONFLICT (job_id) WHERE status IN ('queued', 'running') DO NOTHING
RETURNING *;

-- name: SetTestRestoreRiverJob :execrows
UPDATE test_restores SET river_job_id = @river_job_id WHERE id = @id AND river_job_id IS NULL;

-- name: GetTestRestore :one
SELECT * FROM test_restores WHERE id = $1;

-- name: LockTestRestore :one
SELECT * FROM test_restores WHERE id = $1 FOR UPDATE;

-- StartTestRestore moves a queued test restore to running.
-- name: StartTestRestore :execrows
UPDATE test_restores SET status = 'running', started_at = now()
WHERE id = @id AND status = 'queued';

-- SetTestRestoreRun records the run a test restore tests (once).
-- name: SetTestRestoreRun :execrows
UPDATE test_restores SET run_id = @run_id WHERE id = @id AND run_id IS NULL AND status = 'running';

-- FinishTestRestore sets the terminal status of an unfinished test restore.
-- name: FinishTestRestore :execrows
UPDATE test_restores
SET status = @status, finished_at = now(),
    error_code = sqlc.narg(error_code)::text, error_message = sqlc.narg(error_message)::text,
    databases = @databases, passed = @passed, warned = @warned, failed = @failed
WHERE id = @id AND status IN ('queued', 'running');

-- PickTestRestoreRun is the newest successful run of a job with at least
-- one present, verified, signed database artifact.
-- name: PickTestRestoreRun :one
SELECT r.id FROM runs r
WHERE r.job_id = $1 AND r.status = 'success'
  AND EXISTS (
      SELECT 1 FROM run_items ri JOIN artifacts a ON a.run_item_id = ri.id
      WHERE ri.run_id = r.id AND ri.kind = 'database' AND ri.status = 'success' AND ri.verified
        AND a.state = 'present' AND a.verified AND a.signing_key_id IS NOT NULL
  )
ORDER BY r.storage_timestamp DESC NULLS LAST, r.queued_at DESC, r.id DESC
LIMIT 1;

-- ListTestRestoreCandidates lists a run's successful database items with
-- one restorable artifact each (the first destination of the job that
-- holds a present, verified, signed copy) and the item's backup_stats.
-- name: ListTestRestoreCandidates :many
SELECT ri.id AS run_item_id, ri.db_name, art.id AS artifact_id,
       bs.stats AS stats, bs.consistency AS consistency
FROM run_items ri
JOIN LATERAL (
    SELECT a.id FROM artifacts a
    LEFT JOIN job_destinations jd ON jd.job_id = a.job_id AND jd.destination_id = a.destination_id
    WHERE a.run_item_id = ri.id AND a.state = 'present' AND a.verified AND a.signing_key_id IS NOT NULL
    ORDER BY jd.position NULLS LAST, a.created_at, a.id
    LIMIT 1
) art ON true
LEFT JOIN backup_stats bs ON bs.run_item_id = ri.id
WHERE ri.run_id = @run_id AND ri.kind = 'database' AND ri.status = 'success' AND ri.verified
ORDER BY ri.position, ri.id
LIMIT @max_rows::int;

-- name: InsertTestRestoreItem :one
INSERT INTO test_restore_items (test_restore_id, run_item_id, restore_id, db_name, scratch_id)
VALUES (@test_restore_id, @run_item_id, @restore_id, @db_name, @scratch_id)
RETURNING *;

-- FinishTestRestoreItem records an item's verdict (once).
-- name: FinishTestRestoreItem :execrows
UPDATE test_restore_items
SET status = @status, error_code = sqlc.narg(error_code)::text, diff = @diff,
    scratch_dropped = @scratch_dropped, finished_at = now()
WHERE id = @id AND status = 'running';

-- SetTestRestoreItemDropped records that an item's scratch database is gone.
-- name: SetTestRestoreItemDropped :exec
UPDATE test_restore_items SET scratch_dropped = true WHERE id = @id;

-- name: ListTestRestoreItems :many
SELECT * FROM test_restore_items WHERE test_restore_id = $1 ORDER BY started_at, id;

-- ListTestRestoreViews returns one page of a job's test restores, newest
-- first, after the keyset cursor, with the job, target and requester names.
-- name: ListTestRestoreViews :many
SELECT sqlc.embed(t), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       c.name AS target_name, c.engine AS target_engine, c.temporary AS target_temporary, c.deleted_at AS target_deleted_at,
       coalesce(u.display_name, '')::text AS requested_by_name, coalesce(u.email::text, '')::text AS requested_by_email
FROM test_restores t
JOIN jobs j ON j.id = t.job_id
JOIN connections c ON c.id = t.target_connection_id
LEFT JOIN users u ON u.id = t.requested_by
WHERE t.job_id = @job_id
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT @max_rows::int;

-- name: GetTestRestoreView :one
SELECT sqlc.embed(t), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       c.name AS target_name, c.engine AS target_engine, c.temporary AS target_temporary, c.deleted_at AS target_deleted_at,
       coalesce(u.display_name, '')::text AS requested_by_name, coalesce(u.email::text, '')::text AS requested_by_email
FROM test_restores t
JOIN jobs j ON j.id = t.job_id
JOIN connections c ON c.id = t.target_connection_id
LEFT JOIN users u ON u.id = t.requested_by
WHERE t.id = $1;

-- LatestTestRestore is a job's newest finished test restore.
-- name: LatestTestRestore :one
SELECT * FROM test_restores
WHERE job_id = $1 AND status NOT IN ('queued', 'running')
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- ListStuckTestRestores finds unfinished test restores that no worker will
-- finish: started before @started_before, or idle since @idle_before while
-- their River job is gone or finalized.
-- name: ListStuckTestRestores :many
SELECT t.id FROM test_restores t
LEFT JOIN river_job rj ON rj.id = t.river_job_id
WHERE t.status IN ('queued', 'running')
  AND ((t.started_at IS NOT NULL AND t.started_at < @started_before::timestamptz)
       OR (t.updated_at < @idle_before::timestamptz
           AND (rj.id IS NULL OR rj.state::text IN ('completed', 'discarded', 'cancelled'))))
ORDER BY t.created_at, t.id
LIMIT @max_rows::int;

-- IsTestRestoreStuck re-checks ListStuckTestRestores' condition under the
-- row lock.
-- name: IsTestRestoreStuck :one
SELECT EXISTS (
    SELECT 1 FROM test_restores t
    LEFT JOIN river_job rj ON rj.id = t.river_job_id
    WHERE t.id = @id AND t.status IN ('queued', 'running')
      AND ((t.started_at IS NOT NULL AND t.started_at < @started_before::timestamptz)
           OR (t.updated_at < @idle_before::timestamptz
               AND (rj.id IS NULL OR rj.state::text IN ('completed', 'discarded', 'cancelled'))))
)::boolean;

-- ListUndroppedScratch lists the items of finished (or abandoned) test
-- restores whose scratch database may still exist.
-- name: ListUndroppedScratch :many
SELECT i.id, i.scratch_id, i.test_restore_id, t.job_id, t.target_connection_id
FROM test_restore_items i
JOIN test_restores t ON t.id = i.test_restore_id
JOIN connections c ON c.id = t.target_connection_id
WHERE NOT i.scratch_dropped AND t.status NOT IN ('queued', 'running') AND c.deleted_at IS NULL
ORDER BY i.started_at, i.id
LIMIT @max_rows::int;

-- FailRunningTestRestoreItems ends the unfinished items of an abandoned
-- test restore.
-- name: FailRunningTestRestoreItems :exec
UPDATE test_restore_items SET status = 'error', error_code = @error_code, finished_at = now()
WHERE test_restore_id = @test_restore_id AND status = 'running';
