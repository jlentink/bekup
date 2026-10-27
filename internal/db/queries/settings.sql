-- name: GetSetting :one
SELECT * FROM settings WHERE key = $1;

-- name: PutSetting :one
INSERT INTO settings (key, value, secret_enc, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    secret_enc = EXCLUDED.secret_enc,
    updated_by = EXCLUDED.updated_by
RETURNING *;

-- GetSettingForUpdate locks a setting row (read-modify-write updates).
-- name: GetSettingForUpdate :one
SELECT * FROM settings WHERE key = $1 FOR UPDATE;

-- InsertSettingIfAbsent stores a setting only while the key is unset (0
-- rows: someone else stored it first).
-- name: InsertSettingIfAbsent :execrows
INSERT INTO settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING;
