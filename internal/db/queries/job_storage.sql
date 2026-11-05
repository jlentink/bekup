-- Storage per job (D-225): what a job's backups occupy on its
-- destinations, read only. Like the run history reads, every query takes
-- scope_all = false to limit rows to the jobs @user_id owns (authz.JobScope,
-- the same rule as IsJobOwner: deleted users own nothing); the API also
-- authorizes the job itself first.

-- JobStorageTotals returns, per job of @job_ids that has present
-- artifacts, their bytes and number and the number of backups (runs)
-- they belong to (Job.storage on a page of jobs: one aggregate).
-- name: JobStorageTotals :many
SELECT a.job_id,
       sum(a.size)::bigint AS stored_bytes,
       count(*)::bigint AS artifact_count,
       count(DISTINCT a.run_id)::bigint AS backup_count
FROM artifacts a
WHERE a.job_id = ANY(@job_ids::uuid[]) AND a.state = 'present'
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = a.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
GROUP BY a.job_id;

-- JobStorageByDestination returns a job's present artifacts per
-- destination (also deleted destinations that still hold them), ordered
-- by name.
-- name: JobStorageByDestination :many
SELECT d.id AS destination_id, d.name AS destination_name, d.type AS destination_type,
       d.deleted_at AS destination_deleted_at,
       sum(a.size)::bigint AS stored_bytes,
       count(*)::bigint AS artifact_count,
       count(DISTINCT a.run_id)::bigint AS backup_count
FROM artifacts a
JOIN destinations d ON d.id = a.destination_id
WHERE a.job_id = @job_id AND a.state = 'present'
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = a.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
GROUP BY d.id, d.name, d.type, d.deleted_at
ORDER BY lower(d.name), d.id;

-- JobStorageRuns returns the size of the newest @max_rows successful or
-- partial runs of a job that wrote artifacts, newest first. A run's size
-- is one copy: the largest per-destination sum of its artifacts in any
-- state (fan-out writes the same files to every destination; retention
-- may have deleted them since). present_bytes is what is still present
-- (all copies).
-- name: JobStorageRuns :many
SELECT r.id AS run_id, r.status, r.queued_at, r.finished_at,
       COALESCE((SELECT max(p.bytes) FROM (
           SELECT sum(a.size) AS bytes FROM artifacts a WHERE a.run_id = r.id GROUP BY a.destination_id
       ) p), 0)::bigint AS run_bytes,
       COALESCE((SELECT sum(a.size) FROM artifacts a WHERE a.run_id = r.id AND a.state = 'present'), 0)::bigint AS present_bytes
FROM runs r
WHERE r.job_id = @job_id AND r.status IN ('success', 'partial')
  AND EXISTS (SELECT 1 FROM artifacts a WHERE a.run_id = r.id)
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = r.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
ORDER BY r.queued_at DESC, r.id DESC
LIMIT @max_rows::int;

-- JobStorageNewestSuccess returns the newest successful run of a job that
-- still has present artifacts, with the size of one copy of them (the
-- largest per-destination sum of its present artifacts).
-- name: JobStorageNewestSuccess :one
SELECT r.id AS run_id, r.queued_at, r.finished_at,
       COALESCE((SELECT max(p.bytes) FROM (
           SELECT sum(a.size) AS bytes FROM artifacts a
           WHERE a.run_id = r.id AND a.state = 'present' GROUP BY a.destination_id
       ) p), 0)::bigint AS run_bytes
FROM runs r
WHERE r.job_id = @job_id AND r.status = 'success'
  AND EXISTS (SELECT 1 FROM artifacts a WHERE a.run_id = r.id AND a.state = 'present')
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = r.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
ORDER BY r.queued_at DESC, r.id DESC
LIMIT 1;
