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
