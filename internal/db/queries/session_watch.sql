-- Session re-validation for long-lived connections (WebSocket
-- subscriptions, task 1.10). Looks the session up by id, never by token,
-- and does not touch it: an open socket is not user activity, so the idle
-- expiry keeps running.

-- CheckActiveSession returns the active session id with its user, so the
-- caller can rebuild the full principal (role, AAL, step-up and
-- second-factor times, restrictions; M1 review L2).
-- name: CheckActiveSession :one
SELECT sqlc.embed(s), sqlc.embed(u)
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = $1
  AND s.revoked_at IS NULL
  AND s.idle_expires_at > now()
  AND s.absolute_expires_at > now();
