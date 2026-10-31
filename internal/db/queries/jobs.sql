-- Backup jobs (task 3.5, D-146). Only internal/jobs writes these tables;
-- the ownership queries back authz.Ownership (internal/jobs.Ownership) and
-- the in-use queries the delete checks of internal/connections and
-- internal/destinations.

-- name: InsertJob :one
INSERT INTO jobs (id, name, slug, description, connection_id, db_mode, databases, exclude,
                  include_globals, retention_keep_last, retention_keep_days, timeout_seconds,
                  verification, expected_window_seconds, schedule, enabled, next_run_at,
                  created_by, updated_by)
VALUES (@id, @name, @slug, @description, @connection_id, @db_mode, @databases::text[], @exclude::text[],
        @include_globals, sqlc.narg(retention_keep_last)::int, sqlc.narg(retention_keep_days)::int,
        sqlc.narg(timeout_seconds)::int, @verification, sqlc.narg(expected_window_seconds)::int,
        @schedule, @enabled, sqlc.narg(next_run_at)::timestamptz, @created_by, @created_by)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1 AND deleted_at IS NULL;

-- GetJobForUpdate locks a job that is not deleted.
-- name: GetJobForUpdate :one
SELECT * FROM jobs WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- GetJobForShare locks a job that is not deleted against concurrent
-- changes and deletion (run now, owner changes).
-- name: GetJobForShare :one
SELECT * FROM jobs WHERE id = $1 AND deleted_at IS NULL FOR SHARE;

-- name: JobSlugTaken :one
SELECT EXISTS (SELECT 1 FROM jobs WHERE slug = @slug)::boolean;

-- ListJobs returns one page of non-deleted jobs ordered by (lower(name),
-- id) after the cursor. scope_all = false limits the page to the jobs
-- @user_id owns (authz.JobScope). owner_id (staff only, checked by the
-- service) narrows the page to the jobs that user owns.
-- name: ListJobs :many
SELECT j.* FROM jobs j
WHERE j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o WHERE o.job_id = j.id AND o.user_id = @user_id::uuid))
  AND (sqlc.narg(owner_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM job_owners fo WHERE fo.job_id = j.id AND fo.user_id = sqlc.narg(owner_id)::uuid))
  AND (sqlc.narg(enabled)::boolean IS NULL OR j.enabled = sqlc.narg(enabled)::boolean)
  AND (sqlc.narg(connection_id)::uuid IS NULL OR j.connection_id = sqlc.narg(connection_id)::uuid)
  AND (sqlc.narg(search)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(search)::text)) > 0
       OR strpos(j.slug, lower(sqlc.narg(search)::text)) > 0)
  AND (sqlc.narg(after_name)::text IS NULL
       OR (lower(j.name), j.id) > (sqlc.narg(after_name)::text, sqlc.narg(after_id)::uuid))
ORDER BY lower(j.name), j.id
LIMIT @max_rows::int;

-- name: UpdateJob :one
UPDATE jobs
SET name = @name,
    description = @description,
    connection_id = @connection_id,
    db_mode = @db_mode,
    databases = @databases::text[],
    exclude = @exclude::text[],
    include_globals = @include_globals,
    retention_keep_last = sqlc.narg(retention_keep_last)::int,
    retention_keep_days = sqlc.narg(retention_keep_days)::int,
    timeout_seconds = sqlc.narg(timeout_seconds)::int,
    verification = @verification,
    expected_window_seconds = sqlc.narg(expected_window_seconds)::int,
    schedule = @schedule,
    next_run_at = sqlc.narg(next_run_at)::timestamptz,
    updated_by = @updated_by
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SetJobEnabled :one
UPDATE jobs
SET enabled = @enabled,
    next_run_at = sqlc.narg(next_run_at)::timestamptz,
    updated_by = @updated_by
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteJob marks a job deleted and unschedules it. Its key, owner and
-- destination rows stay (artifacts and the audit log reference them).
-- name: SoftDeleteJob :execrows
UPDATE jobs
SET deleted_at = now(), enabled = false, next_run_at = NULL, updated_by = @updated_by
WHERE id = @id AND deleted_at IS NULL;

-- LockConnectionForJob locks the connection a job is saved with against a
-- concurrent delete (FOR SHARE; the delete takes FOR UPDATE).
-- name: LockConnectionForJob :one
SELECT id, name, engine, version_key, temporary, deleted_at
FROM connections WHERE id = $1 FOR SHARE;

-- LockDestinationsForJob locks the destinations a job is saved with.
-- name: LockDestinationsForJob :many
SELECT id, deleted_at FROM destinations WHERE id = ANY(@ids::uuid[]) FOR SHARE;

-- name: GetJobConnectionInfo :one
SELECT id, name, engine, version_key, deleted_at FROM connections WHERE id = $1;

-- name: ListJobDestinations :many
SELECT jd.destination_id, jd.position, d.name, d.type, d.deleted_at
FROM job_destinations jd JOIN destinations d ON d.id = jd.destination_id
WHERE jd.job_id = $1
ORDER BY jd.position;

-- name: DeleteJobDestinations :exec
DELETE FROM job_destinations WHERE job_id = $1;

-- name: InsertJobDestination :exec
INSERT INTO job_destinations (job_id, destination_id, position)
VALUES (@job_id, @destination_id, @position);

-- name: ListJobOwners :many
SELECT o.user_id, o.created_at, u.email, u.display_name, u.role, u.disabled
FROM job_owners o JOIN users u ON u.id = o.user_id
WHERE o.job_id = $1 AND u.deleted_at IS NULL
ORDER BY u.email;

-- name: CountJobOwners :one
SELECT count(*)::int FROM job_owners o JOIN users u ON u.id = o.user_id
WHERE o.job_id = $1 AND u.deleted_at IS NULL;

-- name: GetOwnerCandidates :many
SELECT id, email, role FROM users WHERE id = ANY(@ids::uuid[]) AND deleted_at IS NULL;

-- name: InsertJobOwner :execrows
INSERT INTO job_owners (job_id, user_id, created_by)
VALUES (@job_id, @user_id, @created_by)
ON CONFLICT (job_id, user_id) DO NOTHING;

-- name: DeleteJobOwner :execrows
DELETE FROM job_owners WHERE job_id = @job_id AND user_id = @user_id;

-- IsJobOwner: the user (not deleted) is listed as an owner of the job. The
-- job may be soft deleted: its artifacts stay restorable by its owners.
-- name: IsJobOwner :one
SELECT EXISTS (
    SELECT 1 FROM job_owners o JOIN users u ON u.id = o.user_id
    WHERE o.job_id = @job_id AND o.user_id = @user_id AND u.deleted_at IS NULL
)::boolean;

-- BacksUpDatabase: a non-deleted job owned by the user backs up the
-- database on the connection (mode 'all' unless excluded, or selected).
-- name: OwnerBacksUpDatabase :one
SELECT EXISTS (
    SELECT 1 FROM jobs j
    JOIN job_owners o ON o.job_id = j.id
    JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
    WHERE o.user_id = @user_id
      AND j.connection_id = @connection_id
      AND j.deleted_at IS NULL
      AND ((j.db_mode = 'all' AND NOT (@database::text = ANY(j.exclude)))
           OR (j.db_mode = 'selected' AND @database::text = ANY(j.databases)))
)::boolean;

-- name: OwnerUsesConnection :one
SELECT EXISTS (
    SELECT 1 FROM jobs j
    JOIN job_owners o ON o.job_id = j.id
    JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
    WHERE o.user_id = @user_id AND j.connection_id = @connection_id AND j.deleted_at IS NULL
)::boolean;

-- OwnerConnectionIDs lists the connections that non-deleted jobs owned by
-- the user back up (restore targets of backup owners, task 4.5).
-- name: OwnerConnectionIDs :many
SELECT DISTINCT j.connection_id FROM jobs j
JOIN job_owners o ON o.job_id = j.id
JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
WHERE o.user_id = @user_id AND j.deleted_at IS NULL
ORDER BY j.connection_id;

-- OwnerBackedUpDatabases returns the names among @names that a non-deleted
-- job owned by the user backs up on the connection (OwnerBacksUpDatabase
-- for a list; restore target listings, task 4.5).
-- name: OwnerBackedUpDatabases :many
SELECT n.name::text AS name FROM unnest(@names::text[]) AS n(name)
WHERE EXISTS (
    SELECT 1 FROM jobs j
    JOIN job_owners o ON o.job_id = j.id
    JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
    WHERE o.user_id = @user_id
      AND j.connection_id = @connection_id
      AND j.deleted_at IS NULL
      AND ((j.db_mode = 'all' AND NOT (n.name = ANY(j.exclude)))
           OR (j.db_mode = 'selected' AND n.name = ANY(j.databases))))
ORDER BY n.name;

-- JobsUsingConnection lists (up to 5) non-deleted jobs that back up the
-- connection or use it as their test-restore scratch target (delete-in-use
-- check).
-- name: JobsUsingConnection :many
SELECT id, name FROM jobs
WHERE (connection_id = $1 OR test_restore_connection_id = $1) AND deleted_at IS NULL
ORDER BY lower(name), id
LIMIT 5;

-- JobsUsingDestination lists (up to 5) non-deleted jobs that write to the
-- destination (delete-in-use check).
-- name: JobsUsingDestination :many
SELECT j.id, j.name FROM jobs j JOIN job_destinations jd ON jd.job_id = j.id
WHERE jd.destination_id = $1 AND j.deleted_at IS NULL
ORDER BY lower(j.name), j.id
LIMIT 5;

-- SetJobVersionQuery changes a job's schema-version capture (D-210).
-- name: SetJobVersionQuery :one
UPDATE jobs
SET version_query_mode = @mode,
    version_preset = sqlc.narg(preset)::text,
    version_custom_sql = sqlc.narg(custom_sql)::text,
    version_schema = @schema_name::text,
    updated_by = @updated_by
WHERE id = @id AND deleted_at IS NULL
RETURNING *;
