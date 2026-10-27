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

-- GetAuditLogCanonical returns one audit row in the hashed text form (see
-- ListAuditLogCanonical); used to check a signed chain head.
-- name: GetAuditLogCanonical :one
SELECT id, ts, actor_user_id, actor_kind, action, target_type, target_id,
       (ip IS NOT NULL)::boolean AS has_ip, coalesce(host(ip), '')::text AS ip_text,
       details::text AS details_text, prev_hash, hash
FROM audit_log
WHERE id = @id;

-- GetAuditLogChainAnchor returns the hash of the last row before @before_id
-- (the starting point of a partial verification).
-- name: GetAuditLogChainAnchor :one
SELECT id, hash FROM audit_log WHERE id < @before_id ORDER BY id DESC LIMIT 1;

-- GetAuditChainTip returns the id and hash of the newest audit row.
-- name: GetAuditChainTip :one
SELECT id, hash FROM audit_log ORDER BY id DESC LIMIT 1;

-- ListAuditLog pages through the audit log newest first. @before_id is the
-- cursor (the id of the last row of the previous page); every filter is
-- optional (NULL: no filter). @action_prefix matches "<prefix>." prefixes.
-- name: ListAuditLog :many
SELECT id, ts, actor_user_id, actor_kind, action, target_type, target_id,
       ip, details, hash
FROM audit_log
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(action_prefix)::text IS NULL OR starts_with(action, sqlc.narg(action_prefix)::text))
  AND (sqlc.narg(actor_user_id)::uuid IS NULL OR actor_user_id = sqlc.narg(actor_user_id)::uuid)
  AND (sqlc.narg(actor_kind)::text IS NULL OR actor_kind = sqlc.narg(actor_kind)::text)
  AND (sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text)
  AND (sqlc.narg(target_id)::text IS NULL OR target_id = sqlc.narg(target_id)::text)
  AND (sqlc.narg(ts_from)::timestamptz IS NULL OR ts >= sqlc.narg(ts_from)::timestamptz)
  AND (sqlc.narg(ts_to)::timestamptz IS NULL OR ts < sqlc.narg(ts_to)::timestamptz)
ORDER BY id DESC
LIMIT @max_rows;

-- name: InsertAuditChainHead :one
INSERT INTO audit_chain_heads (chain_id, chain_hash, signed_at, key_id, signature)
VALUES (@chain_id, @chain_hash, @signed_at, @key_id, @signature)
RETURNING *;

-- name: GetLatestAuditChainHead :one
SELECT * FROM audit_chain_heads ORDER BY id DESC LIMIT 1;
