-- name: InsertSession :one
INSERT INTO sessions (
    token_hash, user_id, aal, stepup_at, aal2_at,
    idle_expires_at, absolute_expires_at, ip, user_agent, mfa_pending
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
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

-- GetSessionWithUserByTokenHash is GetSessionByTokenHash joined with the
-- session's user (the session middleware, task 1.5).
-- name: GetSessionWithUserByTokenHash :one
SELECT sqlc.embed(s), sqlc.embed(u)
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.idle_expires_at > now()
  AND s.absolute_expires_at > now();

-- GetActiveSessionForUpdate locks an active session (rotation, AAL changes).
-- name: GetActiveSessionForUpdate :one
SELECT * FROM sessions
WHERE id = $1
  AND revoked_at IS NULL
  AND idle_expires_at > now()
  AND absolute_expires_at > now()
FOR UPDATE;

-- TouchSession records activity and client facts and extends the idle
-- expiry, never beyond the absolute expiry.
-- name: TouchSession :execrows
UPDATE sessions
SET last_seen_at = now(),
    idle_expires_at = LEAST(@idle_expires_at::timestamptz, absolute_expires_at),
    ip = @ip,
    user_agent = @user_agent
WHERE id = @id
  AND revoked_at IS NULL;

-- MarkSessionStepUp records a step-up; never on a session that is still
-- waiting for its second factor (task 1.9).
-- name: MarkSessionStepUp :execrows
UPDATE sessions
SET stepup_at = now()
WHERE id = $1
  AND NOT mfa_pending
  AND revoked_at IS NULL
  AND idle_expires_at > now()
  AND absolute_expires_at > now();

-- MarkSessionSecondFactor records a step-up with a second factor on an
-- AAL2 session (task 1.9): step-up and second-factor times become now.
-- name: MarkSessionSecondFactor :execrows
UPDATE sessions
SET stepup_at = now(), aal2_at = now()
WHERE id = $1
  AND aal = 2
  AND NOT mfa_pending
  AND revoked_at IS NULL
  AND idle_expires_at > now()
  AND absolute_expires_at > now();

-- ListActiveUserSessions returns the user's sessions that are neither
-- revoked nor expired, most recently used first.
-- name: ListActiveUserSessions :many
SELECT * FROM sessions
WHERE user_id = $1
  AND revoked_at IS NULL
  AND idle_expires_at > now()
  AND absolute_expires_at > now()
ORDER BY last_seen_at DESC, id
LIMIT 200;

-- RevokeUserSession revokes one session of a user (a session of another
-- user is never touched).
-- name: RevokeUserSession :execrows
UPDATE sessions
SET revoked_at = now()
WHERE id = @id
  AND user_id = @user_id
  AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
UPDATE sessions
SET revoked_at = now()
WHERE user_id = $1
  AND revoked_at IS NULL;

-- name: RevokeOtherUserSessions :execrows
UPDATE sessions
SET revoked_at = now()
WHERE user_id = @user_id
  AND id <> @keep_id
  AND revoked_at IS NULL;

-- DeleteStaleSessions removes sessions that expired or were revoked before
-- the cutoff (housekeeping; revoked rows are kept a while for the session
-- list and investigations).
-- name: DeleteStaleSessions :execrows
DELETE FROM sessions
WHERE absolute_expires_at < @cutoff
   OR idle_expires_at < @cutoff
   OR revoked_at < @cutoff;

-- name: RevokeSession :execrows
UPDATE sessions
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL;
