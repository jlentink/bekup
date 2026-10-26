-- InsertAuditLog appends an audit row. id, ts, prev_hash and hash are always
-- assigned by the audit_log_chain trigger.
-- name: InsertAuditLog :one
INSERT INTO audit_log (
    actor_user_id, actor_kind, action, target_type, target_id, ip, details
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetLatestAuditLog :one
SELECT * FROM audit_log ORDER BY id DESC LIMIT 1;

-- ListAuditLogCanonical returns audit rows in chain order with ip and details
-- in the exact text form the hash covers (auditchain.Row).
-- name: ListAuditLogCanonical :many
SELECT id, ts, actor_user_id, actor_kind, action, target_type, target_id,
       (ip IS NOT NULL)::boolean AS has_ip, coalesce(host(ip), '')::text AS ip_text,
       details::text AS details_text, prev_hash, hash
FROM audit_log
WHERE id > @after_id
ORDER BY id
LIMIT @max_rows;
