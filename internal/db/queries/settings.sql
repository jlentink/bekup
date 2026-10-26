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
