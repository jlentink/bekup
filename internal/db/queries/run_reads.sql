-- Run history reads for the GraphQL API (task 3.7). Read only; the run
-- tables are written by internal/runstate (runs.sql). Listings take
-- scope_all = false to limit rows to the jobs @user_id owns (authz.JobScope,
-- the same rule as IsJobOwner: deleted users own nothing).

-- ListRunsPage returns one page of runs, newest first ((queued_at, id)
-- descending), after the cursor.
-- name: ListRunsPage :many
SELECT sqlc.embed(runs), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       u.display_name AS triggered_by_name, u.email AS triggered_by_email
FROM runs
JOIN jobs j ON j.id = runs.job_id
LEFT JOIN users u ON u.id = runs.triggered_by
WHERE (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = runs.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
  AND (sqlc.narg(job_id)::uuid IS NULL OR runs.job_id = sqlc.narg(job_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR runs.status::text = sqlc.narg(status)::text)
  AND (sqlc.narg(run_trigger)::text IS NULL OR runs.trigger::text = sqlc.narg(run_trigger)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR runs.queued_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR runs.queued_at < sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(after_queued_at)::timestamptz IS NULL
       OR (runs.queued_at, runs.id) < (sqlc.narg(after_queued_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY runs.queued_at DESC, runs.id DESC
LIMIT @max_rows::int;

-- name: GetRunView :one
SELECT sqlc.embed(runs), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       u.display_name AS triggered_by_name, u.email AS triggered_by_email
FROM runs
JOIN jobs j ON j.id = runs.job_id
LEFT JOIN users u ON u.id = runs.triggered_by
WHERE runs.id = $1;

-- ListRecentRunsForJobs returns the newest @per_job runs of each job
-- (Job.lastRun / Job.recentRuns, one query per page of jobs).
-- name: ListRecentRunsForJobs :many
SELECT sqlc.embed(runs), j.name AS job_name, j.slug AS job_slug, j.deleted_at AS job_deleted_at,
       u.display_name AS triggered_by_name, u.email AS triggered_by_email
FROM runs
JOIN jobs j ON j.id = runs.job_id
LEFT JOIN users u ON u.id = runs.triggered_by
WHERE runs.id IN (
    SELECT x.id FROM jobs jj
    CROSS JOIN LATERAL (
        SELECT r2.id FROM runs r2 WHERE r2.job_id = jj.id
        ORDER BY r2.queued_at DESC, r2.id DESC LIMIT @per_job::int
    ) x
    WHERE jj.id = ANY(@job_ids::uuid[]))
ORDER BY runs.job_id, runs.queued_at DESC, runs.id DESC;

-- ListRunIDsUpdatedSince finds runs changed since a time (run event
-- streams after possibly lost notifications; every run is authorized
-- again before it is sent).
-- name: ListRunIDsUpdatedSince :many
SELECT runs.id FROM runs
WHERE runs.updated_at >= @since::timestamptz
  AND (sqlc.narg(job_id)::uuid IS NULL OR runs.job_id = sqlc.narg(job_id)::uuid)
  AND (sqlc.narg(run_id)::uuid IS NULL OR runs.id = sqlc.narg(run_id)::uuid)
ORDER BY runs.updated_at, runs.id
LIMIT @max_rows::int;

-- name: ListRunItemsForRuns :many
SELECT * FROM run_items WHERE run_id = ANY(@run_ids::uuid[]) ORDER BY run_id, position, id;

-- name: ListArtifactsForRuns :many
SELECT sqlc.embed(artifacts), d.name AS destination_name, d.type AS destination_type,
       d.deleted_at AS destination_deleted_at, ri.db_name AS item_db_name,
       j.name AS job_name, j.slug AS job_slug, runs.storage_timestamp AS run_storage_timestamp
FROM artifacts
JOIN destinations d ON d.id = artifacts.destination_id
JOIN jobs j ON j.id = artifacts.job_id
JOIN runs ON runs.id = artifacts.run_id
LEFT JOIN run_items ri ON ri.id = artifacts.run_item_id
WHERE artifacts.run_id = ANY(@run_ids::uuid[])
ORDER BY artifacts.created_at, artifacts.id;

-- ListArtifactsPage is the backup browser: artifacts newest first
-- ((created_at, id) descending) after the cursor.
-- name: ListArtifactsPage :many
SELECT sqlc.embed(artifacts), d.name AS destination_name, d.type AS destination_type,
       d.deleted_at AS destination_deleted_at, ri.db_name AS item_db_name,
       j.name AS job_name, j.slug AS job_slug, runs.storage_timestamp AS run_storage_timestamp
FROM artifacts
JOIN destinations d ON d.id = artifacts.destination_id
JOIN jobs j ON j.id = artifacts.job_id
JOIN runs ON runs.id = artifacts.run_id
LEFT JOIN run_items ri ON ri.id = artifacts.run_item_id
WHERE (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = artifacts.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
  AND (sqlc.narg(job_id)::uuid IS NULL OR artifacts.job_id = sqlc.narg(job_id)::uuid)
  AND (sqlc.narg(destination_id)::uuid IS NULL OR artifacts.destination_id = sqlc.narg(destination_id)::uuid)
  AND (sqlc.narg(run_id)::uuid IS NULL OR artifacts.run_id = sqlc.narg(run_id)::uuid)
  AND (sqlc.narg(state)::text IS NULL OR artifacts.state::text = sqlc.narg(state)::text)
  AND (sqlc.narg(kind)::text IS NULL OR artifacts.kind::text = sqlc.narg(kind)::text)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (artifacts.created_at, artifacts.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY artifacts.created_at DESC, artifacts.id DESC
LIMIT @max_rows::int;

-- name: GetRunLogCounts :one
SELECT runs.job_id, runs.status, runs.log_lines_dropped FROM runs WHERE runs.id = $1;
