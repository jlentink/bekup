-- Backup destinations (task 2.12, D-131). Only internal/destinations uses
-- these queries; secret_enc is sealed under AAD destinations:<id>:secret.

-- name: ListDestinations :many
SELECT * FROM destinations
WHERE deleted_at IS NULL
ORDER BY lower(name), id;

-- name: GetDestination :one
SELECT * FROM destinations WHERE id = $1 AND deleted_at IS NULL;

-- GetDestinationIncludingDeleted is for the workers only (backup,
-- retention, restore: destinations.Service.Open). A soft-deleted
-- destination must still open, because artifacts keep referencing it; the
-- API never shows deleted rows.
-- name: GetDestinationIncludingDeleted :one
SELECT * FROM destinations WHERE id = $1;

-- GetDestinationForUpdate locks a destination that is not deleted.
-- name: GetDestinationForUpdate :one
SELECT * FROM destinations WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- InsertDestination creates the row with an id chosen by the application,
-- so that secret_enc can be sealed under the row's AAD before the insert.
-- name: InsertDestination :one
INSERT INTO destinations (id, name, type, config, secret_enc, no_delete,
                          object_lock_mode, object_lock_retention_days, created_by)
VALUES (@id, @name, @type, @config, @secret_enc, @no_delete,
        @object_lock_mode, @object_lock_retention_days, @created_by)
RETURNING *;

-- UpdateDestination replaces the mutable fields. The host-key pin is
-- cleared when clear_host_key is true (host or port changed).
-- name: UpdateDestination :one
UPDATE destinations
SET name = @name,
    config = @config,
    secret_enc = @secret_enc,
    no_delete = @no_delete,
    object_lock_mode = @object_lock_mode,
    object_lock_retention_days = @object_lock_retention_days,
    host_key_type = CASE WHEN @clear_host_key::boolean THEN NULL ELSE host_key_type END,
    host_key_fingerprint = CASE WHEN @clear_host_key::boolean THEN NULL ELSE host_key_fingerprint END,
    host_key_authorized_key = CASE WHEN @clear_host_key::boolean THEN NULL ELSE host_key_authorized_key END,
    host_key_confirmed_at = CASE WHEN @clear_host_key::boolean THEN NULL ELSE host_key_confirmed_at END,
    host_key_confirmed_by = CASE WHEN @clear_host_key::boolean THEN NULL ELSE host_key_confirmed_by END
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SetDestinationHostKey :one
UPDATE destinations
SET host_key_type = @host_key_type,
    host_key_fingerprint = @host_key_fingerprint,
    host_key_authorized_key = @host_key_authorized_key,
    host_key_confirmed_at = now(),
    host_key_confirmed_by = @confirmed_by
WHERE id = @id AND deleted_at IS NULL AND type = 'sftp'
RETURNING *;

-- name: SetDestinationAuth :one
UPDATE destinations
SET config = @config,
    secret_enc = @secret_enc
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteDestination :execrows
UPDATE destinations SET deleted_at = now() WHERE id = @id AND deleted_at IS NULL;

-- RecordDestinationTest stores the outcome of a test (D-187) when the row
-- is unchanged since the test loaded it (settings_at = its updated_at); 0
-- rows: changed or deleted meanwhile, the result is not stored. The
-- trigger leaves updated_at alone for this update.
-- name: RecordDestinationTest :execrows
UPDATE destinations
SET last_tested_at = now(),
    last_test_ok = @ok,
    last_test_code = sqlc.narg(code),
    last_test_settings_at = updated_at
WHERE id = @id AND deleted_at IS NULL AND updated_at = @settings_at;

-- DestinationUsage returns, per destination that is not deleted, the
-- bytes and number of the present artifacts stored on it (one grouped
-- query; D-196).
-- name: DestinationUsage :many
SELECT d.id, d.name, d.type,
       COALESCE(u.stored_bytes, 0)::bigint AS stored_bytes,
       COALESCE(u.artifact_count, 0)::bigint AS artifact_count
FROM destinations d
LEFT JOIN (
    SELECT a.destination_id, sum(a.size)::bigint AS stored_bytes, count(*)::bigint AS artifact_count
    FROM artifacts a
    WHERE a.state = 'present'
    GROUP BY a.destination_id
) u ON u.destination_id = d.id
WHERE d.deleted_at IS NULL
ORDER BY lower(d.name), d.id;

-- DestinationHasPresentArtifacts reports whether backup files are recorded as
-- present on the destination (its location must not change then, D-209).
-- name: DestinationHasPresentArtifacts :one
SELECT EXISTS (SELECT 1 FROM artifacts WHERE destination_id = $1 AND state = 'present')::boolean;

-- LockDestinationLocations serializes the location checks of creates and
-- location changes (transaction-scoped advisory lock, D-209).
-- name: LockDestinationLocations :exec
SELECT pg_advisory_xact_lock(7142023363);

-- ListDestinationLocations returns the other destinations whose location a
-- new or moved destination must not overlap: every non-deleted one and the
-- soft-deleted ones that still hold present backups (D-209).
-- name: ListDestinationLocations :many
SELECT d.id, d.name, d.type, d.config
FROM destinations d
WHERE d.id <> @id
  AND (d.deleted_at IS NULL OR EXISTS (SELECT 1 FROM artifacts a WHERE a.destination_id = d.id AND a.state = 'present'))
ORDER BY d.name, d.id;

-- DestinationHasActiveRuns reports a queued or running backup run of a job
-- that writes to the destination (its location must not change then,
-- D-209).
-- name: DestinationHasActiveRuns :one
SELECT EXISTS (
    SELECT 1 FROM runs r
    JOIN job_destinations jd ON jd.job_id = r.job_id
    WHERE jd.destination_id = $1 AND r.status IN ('queued', 'running')
)::boolean;
