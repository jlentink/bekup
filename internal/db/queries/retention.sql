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

-- ListDailyMaintenanceDestinations returns the destinations the daily sweep
-- maintains: every non-deleted one (unrecorded-file scan; disk and SFTP also
-- leftover temporary files and empty directories) and deleted disk and SFTP
-- ones that still hold present artifacts (temporary files only).
-- name: ListDailyMaintenanceDestinations :many
SELECT d.id, d.type, (d.deleted_at IS NOT NULL)::boolean AS deleted
FROM destinations d
WHERE d.deleted_at IS NULL
   OR (d.type IN ('disk', 'sftp')
       AND EXISTS (SELECT 1 FROM artifacts a WHERE a.destination_id = d.id AND a.state = 'present'))
ORDER BY d.id;

-- ListJobSlugs returns the slug of every job, deleted ones included: the
-- job directories empty-directory pruning may touch (D-209).
-- name: ListJobSlugs :many
SELECT slug FROM jobs ORDER BY slug;

-- UpsertRetentionHold records (or confirms) that the sweep keeps incomplete
-- runs of a job on a destination that no keep rule would keep (D-209).
-- first_seen_at is kept from the first sighting.
-- name: UpsertRetentionHold :one
INSERT INTO retention_holds (job_id, destination_id, reason, held_runs, held_since, newest_complete_at, first_seen_at, checked_at)
VALUES (@job_id, @destination_id, @reason, @held_runs, @held_since::timestamptz, sqlc.narg(newest_complete_at)::timestamptz,
        @checked_at::timestamptz, @checked_at::timestamptz)
ON CONFLICT (job_id, destination_id) DO UPDATE
SET reason = excluded.reason, held_runs = excluded.held_runs, held_since = excluded.held_since,
    newest_complete_at = excluded.newest_complete_at, checked_at = excluded.checked_at
RETURNING first_seen_at;

-- DeleteRetentionHoldsExcept removes the holds of a job on every destination
-- not in @keep (the hold ended, or the destination holds nothing any more).
-- name: DeleteRetentionHoldsExcept :execrows
DELETE FROM retention_holds
WHERE job_id = @job_id AND NOT (destination_id = ANY (@keep::uuid[]));

-- DeleteStaleRetentionHolds removes the holds of job/destination pairs
-- without a present artifact (nothing is held there any more; the daily
-- fan-out sweeps only jobs with present artifacts, so no sweep would end
-- them otherwise).
-- name: DeleteStaleRetentionHolds :execrows
DELETE FROM retention_holds h
WHERE NOT EXISTS (
    SELECT 1 FROM artifacts a
    WHERE a.job_id = h.job_id AND a.destination_id = h.destination_id AND a.state = 'present'
);

-- ListRetentionHoldsForJobs returns the holds of a page of jobs with the
-- destination's name (Job.retentionHolds, batch-loaded).
-- name: ListRetentionHoldsForJobs :many
SELECT h.job_id, h.destination_id, d.name AS destination_name, d.type AS destination_type,
       (d.deleted_at IS NOT NULL)::boolean AS destination_deleted,
       h.reason, h.held_runs, h.held_since, h.newest_complete_at, h.first_seen_at, h.checked_at
FROM retention_holds h
JOIN destinations d ON d.id = h.destination_id
WHERE h.job_id = ANY (@job_ids::uuid[])
ORDER BY h.job_id, d.name, h.destination_id;

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
