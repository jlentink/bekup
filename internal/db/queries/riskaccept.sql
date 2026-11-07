-- Accepted security risks (D-228), only for internal/riskaccept. The
-- acceptance rows are written only here; the subject reads (connections,
-- jobs) are read-only views of what is weakened now.

-- InsertRiskAcceptance stores a new acceptance; expires_at follows from
-- the duration on the database clock (risk_acceptances_expiry).
-- name: InsertRiskAcceptance :one
INSERT INTO risk_acceptances (kind, connection_id, job_id, condition_hash, condition, duration, reason, created_by, expires_at)
VALUES (@kind, sqlc.narg(connection_id), sqlc.narg(job_id), @condition_hash, @condition, @duration, @reason, @created_by,
        CASE @duration::risk_duration
            WHEN 'month' THEN now() + interval '1 month'
            WHEN 'year' THEN now() + interval '1 year'
        END)
RETURNING id, created_at, expires_at;

-- LockLiveRiskAcceptance locks the live acceptance of a kind and subject
-- (no row: none).
-- name: LockLiveRiskAcceptance :one
SELECT id, created_by, expires_at
FROM risk_acceptances
WHERE kind = @kind
  AND connection_id IS NOT DISTINCT FROM sqlc.narg(connection_id)::uuid
  AND job_id IS NOT DISTINCT FROM sqlc.narg(job_id)::uuid
  AND ended_at IS NULL
FOR UPDATE;

-- EndRiskAcceptance ends a live acceptance once; no row: unknown or ended.
-- name: EndRiskAcceptance :one
UPDATE risk_acceptances
SET ended_at = now(), ended_by = sqlc.narg(ended_by), end_reason = @end_reason::risk_end_reason
WHERE id = @id AND ended_at IS NULL
RETURNING id, kind, connection_id, job_id, condition, duration, created_by, expires_at;

-- MarkRiskAcceptanceWarned records the "expires soon" warning once; no
-- row: already warned, ended or not expiring within the window.
-- name: MarkRiskAcceptanceWarned :one
UPDATE risk_acceptances
SET warned_at = now()
WHERE id = @id AND ended_at IS NULL AND warned_at IS NULL
  AND expires_at IS NOT NULL AND expires_at <= now() + @warn_within::interval
RETURNING id, kind, connection_id, job_id, condition, duration, created_by, expires_at;

-- GetRiskAcceptance reads one acceptance with its display facts.
-- name: GetRiskAcceptance :one
SELECT a.id, a.kind, a.connection_id, a.job_id, a.condition_hash, a.condition, a.duration, a.reason,
       a.created_by, a.created_at, a.expires_at, a.warned_at, a.ended_at, a.ended_by, a.end_reason,
       (a.ended_at IS NULL AND (a.expires_at IS NULL OR a.expires_at > now()))::boolean AS active
FROM risk_acceptances a
WHERE a.id = @id;

-- ListRiskAcceptances lists acceptances, newest first: every one
-- (owner_id NULL) or those on jobs the user owns; live_only keeps the
-- active ones (not ended, not expired), ended_only the others.
-- name: ListRiskAcceptances :many
SELECT a.id, a.kind, a.connection_id, a.job_id, a.condition_hash, a.condition, a.duration, a.reason,
       a.created_by, a.created_at, a.expires_at, a.warned_at, a.ended_at, a.ended_by, a.end_reason,
       (a.ended_at IS NULL AND (a.expires_at IS NULL OR a.expires_at > now()))::boolean AS active
FROM risk_acceptances a
WHERE (sqlc.narg(owner_id)::uuid IS NULL OR EXISTS (
          SELECT 1 FROM job_owners o
          JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
          WHERE o.job_id = a.job_id AND o.user_id = sqlc.narg(owner_id)::uuid))
  AND (NOT @live_only::boolean OR (a.ended_at IS NULL AND (a.expires_at IS NULL OR a.expires_at > now())))
  AND (NOT @ended_only::boolean OR NOT (a.ended_at IS NULL AND (a.expires_at IS NULL OR a.expires_at > now())))
ORDER BY a.created_at DESC, a.id DESC
LIMIT @max_rows;

-- ListLiveRiskAcceptances returns every acceptance that has not ended
-- (expired ones included, flagged) with whether it covers anything now:
-- unexpired and its acceptor still holds the right (an active admin, or
-- for a job's encryption opt-out an active user who still owns the job;
-- maintainers never).
-- name: ListLiveRiskAcceptances :many
SELECT a.id, a.kind, a.connection_id, a.job_id, a.condition_hash, a.condition, a.duration, a.reason,
       a.created_by, a.created_at, a.expires_at, a.warned_at,
       (a.expires_at IS NULL OR a.expires_at > now())::boolean AS unexpired,
       (NOT u.disabled AND u.deleted_at IS NULL AND (
            u.role = 'admin'
            OR (u.role = 'user' AND a.kind = 'encryption_opt_out' AND EXISTS (
                SELECT 1 FROM job_owners o WHERE o.job_id = a.job_id AND o.user_id = u.id))
       ))::boolean AS acceptor_allowed
FROM risk_acceptances a
JOIN users u ON u.id = a.created_by
WHERE a.ended_at IS NULL
ORDER BY a.created_at, a.id;

-- ListRiskAcceptanceUsers returns the display facts of acceptors.
-- name: ListRiskAcceptanceUsers :many
SELECT id, email::text AS email, display_name, (deleted_at IS NOT NULL)::boolean AS deleted
FROM users
WHERE id = ANY (@ids::uuid[]);

-- ListRiskConnections returns the permanent, not deleted connections
-- that carry an acceptable risk now: a TLS mode other than pinned, or a
-- stored privilege verdict of privileged or unknown. With ids, only those
-- (deleted ones included, flagged: the sweep checks its subjects).
-- name: ListRiskConnections :many
SELECT c.id, c.name, c.host, c.port, c.username, c.tls_mode::text AS tls_mode,
       c.ssh_enabled, c.ssh_host, c.ssh_port, c.ssh_user,
       c.last_test_privileges, c.last_test_privilege_reasons,
       (c.deleted_at IS NOT NULL)::boolean AS deleted
FROM connections c
WHERE NOT c.temporary
  AND CASE WHEN sqlc.narg(ids)::uuid[] IS NULL
           THEN c.deleted_at IS NULL
                AND (c.tls_mode <> 'pinned' OR c.last_test_privileges IN ('privileged', 'unknown'))
           ELSE c.id = ANY (sqlc.narg(ids)::uuid[])
      END
ORDER BY lower(c.name), c.id
LIMIT 10000;

-- ListRiskJobs returns the not deleted jobs whose encryption is opted
-- out. With ids, only those (deleted and opted-in ones included).
-- name: ListRiskJobs :many
SELECT j.id, j.name, e.opted_out_at, (j.deleted_at IS NOT NULL)::boolean AS deleted
FROM jobs j
LEFT JOIN job_encryption e ON e.job_id = j.id
WHERE CASE WHEN sqlc.narg(ids)::uuid[] IS NULL
           THEN j.deleted_at IS NULL AND e.mode = 'none'
           ELSE j.id = ANY (sqlc.narg(ids)::uuid[])
      END
ORDER BY lower(j.name), j.id
LIMIT 10000;

-- LockRiskConnection share-locks a connection for an acceptance (its
-- settings cannot change until the acceptance is stored).
-- name: LockRiskConnection :one
SELECT c.id, c.name, c.host, c.port, c.username, c.tls_mode::text AS tls_mode,
       c.ssh_enabled, c.ssh_host, c.ssh_port, c.ssh_user,
       c.last_test_privileges, c.last_test_privilege_reasons
FROM connections c
WHERE c.id = @id AND c.deleted_at IS NULL AND NOT c.temporary
FOR SHARE;

-- LockRiskJob share-locks a job and its encryption row for an acceptance.
-- name: LockRiskJob :one
SELECT j.id, j.name, e.opted_out_at
FROM jobs j
JOIN job_encryption e ON e.job_id = j.id
WHERE j.id = @id AND j.deleted_at IS NULL
FOR SHARE OF j, e;

-- CountRiskSummary counts the active acceptances, those without an end
-- date, and the active API tokens without expiry (the admins' summary
-- line).
-- name: CountRiskSummary :one
SELECT
    (SELECT count(*) FROM risk_acceptances a
     WHERE a.ended_at IS NULL AND (a.expires_at IS NULL OR a.expires_at > now()))::bigint AS accepted,
    (SELECT count(*) FROM risk_acceptances a
     WHERE a.ended_at IS NULL AND a.expires_at IS NULL)::bigint AS accepted_forever,
    (SELECT count(*) FROM api_tokens t
     WHERE t.never_expires AND public.api_token_valid(t.id))::bigint AS tokens_never_expiring;

-- EndLiveRiskAcceptancesForConnection ends the live acceptances of a
-- connection (a TLS or endpoint change, D-228) in the change's
-- transaction.
-- name: EndLiveRiskAcceptancesForConnection :many
UPDATE risk_acceptances
SET ended_at = now(), end_reason = 'condition_changed'
WHERE connection_id = @connection_id::uuid AND kind::text = ANY (@kinds::text[]) AND ended_at IS NULL
RETURNING id, kind, connection_id, job_id, condition, duration, created_by, expires_at;
