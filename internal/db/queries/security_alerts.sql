-- Security alerts (task 5.5): small, bounded reads behind the failed-login
-- burst detection and the periodic security checks.

-- name: CountRecentLoginFailures :one
-- Failed sign-ins since @since: from one client address (@ip), on one
-- account (@user_id, the audit target id; unknown accounts have none) and
-- in total. Called by the audit observer for every auth.login.failed entry;
-- the window is short and the per-IP rate limit bounds the rows.
SELECT
    count(*) FILTER (WHERE ip = sqlc.narg(ip)::inet)::bigint AS by_address,
    count(*) FILTER (WHERE target_type = 'user' AND target_id = sqlc.narg(user_id)::text)::bigint AS by_account,
    count(*)::bigint AS total
FROM audit_log
WHERE action = 'auth.login.failed' AND ts > @since::timestamptz;

-- name: ListExpiringConnectionPins :many
-- Connections (not deleted) whose primary pinned certificate expires
-- before @before (already expired included), soonest first, bounded.
SELECT id, tls_pin_not_after::timestamptz AS not_after
FROM connections
WHERE deleted_at IS NULL AND tls_pin_not_after IS NOT NULL AND tls_pin_not_after < @before::timestamptz
ORDER BY tls_pin_not_after, id
LIMIT 500;
