-- Offline re-index/import (task 4.7, `bekup reindex --import`): rebuilds
-- the run history of a job from the signed manifests on a destination
-- after the bekup database lost it. Used only by internal/offline, inside
-- one transaction per run together with its audit event.

-- GetJobBySlugForImport finds a job by its slug (deleted jobs too: slugs
-- are unique among all jobs and never reused).
-- name: GetJobBySlugForImport :one
SELECT id, slug, deleted_at FROM jobs WHERE slug = @slug;

-- name: RunExists :one
SELECT EXISTS (SELECT 1 FROM runs WHERE id = @id)::boolean;

-- name: ArtifactKeyExists :one
SELECT EXISTS (
    SELECT 1 FROM artifacts WHERE destination_id = @destination_id AND object_key = @object_key
)::boolean;

-- ImportRun inserts a finished run with the run id of its manifest, so the
-- manifest binding (job id, run id, timestamp) of restores holds.
-- name: ImportRun :one
INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, storage_timestamp,
                  warnings, notices, bytes_raw, bytes_stored)
VALUES (@id, @job_id, 'manual', @status, @finished_at::timestamptz, @finished_at::timestamptz,
        @finished_at::timestamptz, @storage_timestamp::timestamptz, @warnings::jsonb, @notices::jsonb,
        @bytes_raw, @bytes_stored)
RETURNING *;

-- name: ImportRunItem :one
INSERT INTO run_items (run_id, kind, db_name, position, status, started_at, finished_at, raw_size, stored_size,
                       raw_sha256, sha256, verified)
VALUES (@run_id, @kind, sqlc.narg(db_name)::text, @position, @status, @finished_at::timestamptz,
        @finished_at::timestamptz, @raw_size, @stored_size, @raw_sha256, @sha256, @verified)
RETURNING *;

-- name: ImportArtifact :one
INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256,
                       encryption_mode, passphrase_version, recipient_version, signing_key_id, verified,
                       state, state_changed_at)
VALUES (@run_id, sqlc.narg(run_item_id)::uuid, @job_id, @destination_id, @kind, @object_key, @size, @sha256,
        sqlc.narg(encryption_mode)::text, sqlc.narg(passphrase_version)::int, sqlc.narg(recipient_version)::int,
        sqlc.narg(signing_key_id)::text, @verified, @state, sqlc.narg(state_changed_at)::timestamptz)
RETURNING *;
