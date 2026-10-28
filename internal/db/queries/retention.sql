-- Retention (task 3.8, D-148): the inputs of the per-destination policy and
-- the artifact state changes. Artifact rows are never deleted; retention
-- only moves `present` to `deleted` or `missing` (through internal/runstate).

-- ListRetentionJobIDs lists every job (deleted ones included) that still has
-- present artifacts: the daily sweep enqueues one sweep per job.
-- name: ListRetentionJobIDs :many
SELECT j.id
FROM jobs j
WHERE EXISTS (SELECT 1 FROM artifacts a WHERE a.job_id = j.id AND a.state = 'present')
ORDER BY j.id;

-- name: GetJobRetention :one
SELECT id, slug, retention_keep_last, retention_keep_days, deleted_at
FROM jobs
WHERE id = $1;

-- ListRetentionArtifacts returns the present artifacts of a job's finished
-- runs (queued and running runs are never considered).
-- name: ListRetentionArtifacts :many
SELECT a.id, a.run_id, a.run_item_id, a.destination_id, a.kind, a.object_key, a.size, a.verified, a.lock_until,
       coalesce(r.storage_timestamp, r.started_at, r.queued_at)::timestamptz AS run_time
FROM artifacts a
JOIN runs r ON r.id = a.run_id
WHERE a.job_id = $1
  AND a.state = 'present'
  AND r.status NOT IN ('queued', 'running')
ORDER BY a.run_id, a.destination_id, a.id;

-- ListRetentionRunItems returns the items of the job's finished runs that
-- still have present artifacts (completeness: every item needs a present,
-- verified artifact on the destination).
-- name: ListRetentionRunItems :many
SELECT ri.run_id, ri.id
FROM run_items ri
JOIN runs r ON r.id = ri.run_id
WHERE r.job_id = $1
  AND r.status NOT IN ('queued', 'running')
  AND EXISTS (SELECT 1 FROM artifacts a WHERE a.run_id = r.id AND a.state = 'present')
ORDER BY ri.run_id, ri.id;

-- ListPartialCleanupDestinations returns the disk and SFTP destinations whose
-- leftover temporary files the daily sweep removes: every non-deleted one and
-- deleted ones that still hold present artifacts.
-- name: ListPartialCleanupDestinations :many
SELECT d.id
FROM destinations d
WHERE d.type IN ('disk', 'sftp')
  AND (d.deleted_at IS NULL
       OR EXISTS (SELECT 1 FROM artifacts a WHERE a.destination_id = d.id AND a.state = 'present'))
ORDER BY d.id;

-- MarkArtifactDeleted records a retention deletion (only from present).
-- name: MarkArtifactDeleted :one
UPDATE artifacts
SET state = 'deleted', deleted_at = @at::timestamptz, state_changed_at = @at::timestamptz
WHERE id = @id AND state = 'present'
RETURNING id, run_id, job_id;

-- MarkArtifactMissing records an object that vanished without bekup deleting
-- it (only from present).
-- name: MarkArtifactMissing :one
UPDATE artifacts
SET state = 'missing', state_changed_at = @at::timestamptz
WHERE id = @id AND state = 'present'
RETURNING id, run_id, job_id;
