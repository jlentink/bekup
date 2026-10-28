-- Backup runs (task 3.6, D-147). Only internal/runstate writes these
-- tables; tasks 3.7 (run queries), 3.8 (retention) and 4.x (restores) read
-- them.

-- name: InsertRun :one
INSERT INTO runs (job_id, trigger, triggered_by, scheduled_for, river_job_id, attempt)
VALUES (@job_id, @trigger, sqlc.narg(triggered_by)::uuid, sqlc.narg(scheduled_for)::timestamptz,
        sqlc.narg(river_job_id)::bigint, @attempt)
RETURNING *;

-- name: GetRun :one
SELECT * FROM runs WHERE id = $1;

-- name: GetRunForUpdate :one
SELECT * FROM runs WHERE id = $1 FOR UPDATE;

-- LatestRunForRiverJob locks the newest run of a River job (the row the
-- enqueue created, or the run of the previous attempt).
-- name: LatestRunForRiverJob :one
SELECT * FROM runs
WHERE river_job_id = @river_job_id::bigint
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- StartRun moves a queued run to running.
-- name: StartRun :one
UPDATE runs
SET status = 'running', started_at = @started_at::timestamptz,
    storage_timestamp = @storage_timestamp::timestamptz, attempt = @attempt
WHERE id = @id AND status = 'queued'
RETURNING *;

-- FinishRun records the terminal status of a run that is still active.
-- name: FinishRun :one
UPDATE runs
SET status = @status, finished_at = @finished_at::timestamptz,
    error_code = sqlc.narg(error_code)::text, error_message = sqlc.narg(error_message)::text,
    warnings = @warnings::jsonb, notices = @notices::jsonb, bytes_raw = @bytes_raw, bytes_stored = @bytes_stored,
    log_lines = @log_lines, log_lines_dropped = @log_lines_dropped
WHERE id = @id AND status IN ('queued', 'running')
RETURNING *;

-- RequestRunCancel marks an active run for cancellation (idempotent: the
-- first request's time and user are kept).
-- name: RequestRunCancel :one
UPDATE runs
SET cancel_requested_at = coalesce(cancel_requested_at, @requested_at::timestamptz),
    cancel_requested_by = coalesce(cancel_requested_by, sqlc.narg(requested_by)::uuid)
WHERE id = @id AND status IN ('queued', 'running')
RETURNING *;

-- name: RunCancelRequested :one
SELECT (cancel_requested_at IS NOT NULL)::boolean FROM runs WHERE id = $1;

-- name: SetRunLogCounts :exec
UPDATE runs SET log_lines = @log_lines, log_lines_dropped = @log_lines_dropped WHERE id = @id;

-- GetJobForRun loads a job (deleted ones too: the worker skips them) with
-- the engine and dump client series of its connection.
-- name: GetJobForRun :one
SELECT sqlc.embed(jobs), c.engine AS connection_engine, c.image_key AS connection_image_key,
       c.deleted_at AS connection_deleted_at
FROM jobs JOIN connections c ON c.id = jobs.connection_id
WHERE jobs.id = $1;

-- name: InsertRunItem :one
INSERT INTO run_items (run_id, kind, db_name, position)
VALUES (@run_id, @kind, sqlc.narg(db_name)::text, @position)
RETURNING *;

-- name: FinishRunItem :one
UPDATE run_items
SET status = @status, finished_at = @finished_at::timestamptz, raw_size = @raw_size, stored_size = @stored_size,
    raw_sha256 = sqlc.narg(raw_sha256)::bytea, sha256 = sqlc.narg(sha256)::bytea, verified = @verified,
    error_code = sqlc.narg(error_code)::text, error_message = sqlc.narg(error_message)::text,
    warnings = @warnings::jsonb
WHERE id = @id AND status = 'running'
RETURNING *;

-- name: ListRunItems :many
SELECT * FROM run_items WHERE run_id = $1 ORDER BY position, id;

-- name: InsertArtifact :one
INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256, version_id,
                       encryption_mode, passphrase_version, recipient_version, signing_key_id, verified, lock_until)
VALUES (@run_id, sqlc.narg(run_item_id)::uuid, @job_id, @destination_id, @kind, @object_key, @size, @sha256,
        sqlc.narg(version_id)::text, sqlc.narg(encryption_mode)::text, sqlc.narg(passphrase_version)::int,
        sqlc.narg(recipient_version)::int, sqlc.narg(signing_key_id)::text, @verified,
        sqlc.narg(lock_until)::timestamptz)
RETURNING *;

-- SetArtifactsSigned records the manifest signing key on exactly the
-- objects the manifest on one destination lists (after manifest.json was
-- finalized there).
-- name: SetArtifactsSigned :execrows
UPDATE artifacts
SET signing_key_id = @signing_key_id
WHERE run_id = @run_id AND destination_id = @destination_id AND id = ANY(@ids::uuid[])
  AND kind <> 'manifest' AND signing_key_id IS NULL AND verified;

-- name: ListRunArtifacts :many
SELECT * FROM artifacts WHERE run_id = $1 ORDER BY created_at, id;

-- InsertRunLogs appends a batch of log lines (arrays of equal length).
-- name: InsertRunLogs :exec
-- Set-returning functions in one select list advance in lockstep.
INSERT INTO run_logs (run_id, ts, level, stream, line)
SELECT @run_id::uuid, unnest(@ts::timestamptz[]), unnest(@levels::text[])::run_log_level,
       unnest(@streams::text[])::run_log_stream, unnest(@lines::text[]);

-- name: ListRunLogs :many
SELECT * FROM run_logs WHERE run_id = @run_id AND id > @after_id ORDER BY id LIMIT @max_rows::int;

-- --- concurrency slots (D-147) ---------------------------------------------

-- LockRunSlots serializes slot changes for the rest of the transaction
-- (transaction-scoped advisory lock: safe on pooled connections).
-- name: LockRunSlots :exec
SELECT pg_advisory_xact_lock(7142023361);

-- DeleteStaleRunSlots drops slots whose holder stopped sending heartbeats
-- (crashed worker).
-- name: DeleteStaleRunSlots :execrows
DELETE FROM run_slots WHERE heartbeat_at < now() - make_interval(secs => @lease_seconds::double precision);

-- TryInsertRunSlot takes a slot when both the global and the per-connection
-- limit leave room (run under LockRunSlots). The per-connection limit is the
-- connection's max_concurrent_runs (task 3.13, D-156) or, when NULL,
-- @max_per_connection (workers.max_runs_per_connection).
-- name: TryInsertRunSlot :execrows
INSERT INTO run_slots (holder_id, kind, connection_id)
SELECT @holder_id, @kind, @connection_id
WHERE (SELECT count(*) FROM run_slots) < @max_global::bigint
  AND (SELECT count(*) FROM run_slots WHERE connection_id = @connection_id)
      < COALESCE((SELECT c.max_concurrent_runs::bigint FROM connections c WHERE c.id = @connection_id),
                 @max_per_connection::bigint)
ON CONFLICT (holder_id) DO NOTHING;

-- name: HeartbeatRunSlot :execrows
UPDATE run_slots SET heartbeat_at = now() WHERE holder_id = $1;

-- name: DeleteRunSlot :exec
DELETE FROM run_slots WHERE holder_id = $1;

-- name: CountRunSlots :one
SELECT count(*)::bigint FROM run_slots;

-- LockRiverJobState locks a River job row and returns its state (cancelRun
-- of a run whose job waits for a retry; internal/queue only).
-- name: LockRiverJobState :one
SELECT state::text AS state FROM river_job WHERE id = $1 FOR UPDATE;
