-- Scheduler loop (internal/scheduler, task 3.4, D-145). Explicit column
-- lists: task 3.5 adds columns to jobs.

-- LockDueJobs locks up to @batch_size due jobs. SKIP LOCKED lets several
-- serve instances tick concurrently without blocking or double-handling a
-- job; rows a concurrent transaction advanced are re-checked by Postgres
-- and no longer match. Manual schedules are never due. @exclude skips jobs
-- whose handling failed earlier in the same tick (retried next tick).
-- name: LockDueJobs :many
SELECT id, schedule, next_run_at
FROM jobs
WHERE enabled
  AND deleted_at IS NULL
  AND next_run_at IS NOT NULL
  AND next_run_at <= @now::timestamptz
  AND schedule->>'kind' <> 'manual'
  AND NOT (id = ANY(coalesce(sqlc.narg(exclude)::uuid[], '{}'::uuid[])))
ORDER BY next_run_at, id
LIMIT @batch_size::int
FOR UPDATE SKIP LOCKED;

-- AdvanceJobSchedule stores the next occurrence (NULL = unschedulable).
-- name: AdvanceJobSchedule :exec
UPDATE jobs
SET next_run_at = sqlc.narg(next_run_at)::timestamptz,
    last_scheduled_at = @scheduled_at::timestamptz
WHERE id = @id;

-- name: InsertScheduleSkip :exec
INSERT INTO schedule_skips (job_id, scheduled_for, reason)
VALUES (@job_id, sqlc.narg(scheduled_for)::timestamptz, @reason);

-- name: ListScheduleSkips :many
SELECT id, job_id, scheduled_for, reason, created_at
FROM schedule_skips
WHERE job_id = @job_id
ORDER BY created_at DESC, id DESC
LIMIT @max_rows::int;

-- DeleteScheduleSkipsBefore removes skip records older than the retention
-- period, in bounded batches.
-- name: DeleteScheduleSkipsBefore :execrows
DELETE FROM schedule_skips
WHERE id IN (
    SELECT id FROM schedule_skips
    WHERE created_at < @before::timestamptz
    ORDER BY id
    LIMIT @max_rows::int
);
