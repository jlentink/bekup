-- Instance signing keys (D-087; internal/audit instance key loader).

-- name: GetActiveInstanceKey :one
SELECT * FROM instance_keys WHERE retired_at IS NULL;

-- name: GetInstanceKey :one
SELECT * FROM instance_keys WHERE id = @id;

-- InsertInstanceKey adds a new active key; it inserts nothing when another
-- active key already exists (instance_keys_one_active_idx).
-- name: InsertInstanceKey :execrows
INSERT INTO instance_keys (id, public_key, sealed_private_key)
VALUES (@id, @public_key, @sealed_private_key)
ON CONFLICT ((true)) WHERE retired_at IS NULL DO NOTHING;

-- name: ListInstancePublicKeys :many
SELECT id, public_key, created_at, retired_at FROM instance_keys ORDER BY created_at, id;
