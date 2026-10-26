-- name: InsertSession :one
INSERT INTO sessions (
    token_hash, user_id, aal, stepup_at, aal2_at,
    idle_expires_at, absolute_expires_at, ip, user_agent
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- GetSessionByTokenHash returns only sessions that are not revoked and not
-- expired (idle or absolute) at the database's current time.
-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND idle_expires_at > now()
  AND absolute_expires_at > now();

-- TouchSession records activity and extends the idle expiry, never beyond
-- the absolute expiry.
-- name: TouchSession :execrows
UPDATE sessions
SET last_seen_at = now(),
    idle_expires_at = LEAST(@idle_expires_at::timestamptz, absolute_expires_at)
WHERE id = @id
  AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE sessions
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL;
