-- External API access as bekup_app (D-220): API tokens (internal/apiaccess)
-- and the run-request and access-denial consumer of the workers. The secret hash is written
-- once and never read back; "last used" is the newest request of a token.

-- InsertAPIToken: allowed_cidrs (1-32 distinct networks, no /0, no
-- IPv4-mapped networks; nil = none) or allow_any_address, exactly one of
-- them (CHECKs api_tokens_allowed_cidrs and api_tokens_addresses; D-220
-- amendment 1).
-- name: InsertAPIToken :one
INSERT INTO api_tokens (id, name, secret_hash, created_by, expires_at, never_expires, allowed_cidrs, allow_any_address)
VALUES (@id, @name, @secret_hash, @created_by, sqlc.narg(expires_at), @never_expires, coalesce(@allowed_cidrs::cidr[], '{}'), @allow_any_address)
RETURNING id, name, created_by, created_at, expires_at, revoked_at, revoked_by, allowed_cidrs, allow_any_address, never_expires;

-- InsertAPITokenJobs sets the (immutable) scope of a new token.
-- name: InsertAPITokenJobs :execrows
INSERT INTO api_token_jobs (token_id, job_id)
SELECT @token_id::uuid, j.id
FROM unnest(@job_ids::uuid[]) AS j (id);

-- name: GetAPIToken :one
SELECT t.id, t.name, t.created_by, t.created_at, t.expires_at, t.revoked_at, t.revoked_by,
       t.allowed_cidrs, t.allow_any_address, t.never_expires,
       lu.created_at AS last_used_at
FROM api_tokens t
LEFT JOIN run_requests lu ON lu.id = (
    SELECT r.id FROM run_requests r WHERE r.token_id = t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1
)
WHERE t.id = @id;

-- GetAPITokenForUpdate locks a token (revocation).
-- name: GetAPITokenForUpdate :one
SELECT t.id, t.name, t.created_by, t.created_at, t.expires_at, t.revoked_at, t.revoked_by,
       t.allowed_cidrs, t.allow_any_address, t.never_expires
FROM api_tokens t
WHERE t.id = @id
FOR UPDATE;

-- ListAPITokens lists every token, newest first (the admin page).
-- name: ListAPITokens :many
SELECT t.id, t.name, t.created_by, t.created_at, t.expires_at, t.revoked_at, t.revoked_by,
       t.allowed_cidrs, t.allow_any_address, t.never_expires,
       lu.created_at AS last_used_at
FROM api_tokens t
LEFT JOIN run_requests lu ON lu.id = (
    SELECT r.id FROM run_requests r WHERE r.token_id = t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1
)
ORDER BY t.created_at DESC, t.id DESC
LIMIT @max_rows;

-- ListAPITokensForJob lists the tokens whose scope includes the job.
-- name: ListAPITokensForJob :many
SELECT t.id, t.name, t.created_by, t.created_at, t.expires_at, t.revoked_at, t.revoked_by,
       t.allowed_cidrs, t.allow_any_address, t.never_expires,
       lu.created_at AS last_used_at
FROM api_tokens t
JOIN api_token_jobs tj ON tj.token_id = t.id AND tj.job_id = @job_id
LEFT JOIN run_requests lu ON lu.id = (
    SELECT r.id FROM run_requests r WHERE r.token_id = t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1
)
ORDER BY t.created_at DESC, t.id DESC
LIMIT @max_rows;

-- ListAPITokensForOwner lists the tokens whose scope includes a job the
-- user owns (a backup owner's view without a job filter), newest first.
-- name: ListAPITokensForOwner :many
SELECT t.id, t.name, t.created_by, t.created_at, t.expires_at, t.revoked_at, t.revoked_by,
       t.allowed_cidrs, t.allow_any_address, t.never_expires,
       lu.created_at AS last_used_at
FROM api_tokens t
LEFT JOIN run_requests lu ON lu.id = (
    SELECT r.id FROM run_requests r WHERE r.token_id = t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM api_token_jobs tj
    JOIN job_owners o ON o.job_id = tj.job_id
    JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
    WHERE tj.token_id = t.id AND o.user_id = @user_id
)
ORDER BY t.created_at DESC, t.id DESC
LIMIT @max_rows;

-- ListOwnedJobIDs returns the jobs among @job_ids the user (not deleted)
-- owns: the part of a token's scope a backup owner may see and act for.
-- name: ListOwnedJobIDs :many
SELECT o.job_id
FROM job_owners o
JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
WHERE o.user_id = @user_id AND o.job_id = ANY (@job_ids::uuid[])
ORDER BY o.job_id;

-- LockJobsForAPIToken share-locks the non-deleted jobs among @job_ids for
-- a new token's scope (a concurrent delete waits for the insert).
-- name: LockJobsForAPIToken :many
SELECT id, name
FROM jobs
WHERE id = ANY (@job_ids::uuid[]) AND deleted_at IS NULL
ORDER BY id
FOR SHARE;

-- ListAPITokenCreators returns the display facts of token creators.
-- name: ListAPITokenCreators :many
SELECT id, email::text AS email, display_name, (deleted_at IS NOT NULL)::boolean AS deleted
FROM users
WHERE id = ANY (@ids::uuid[]);

-- ListAPITokenJobs returns the scope of the given tokens with the job
-- names (deleted jobs included and flagged).
-- name: ListAPITokenJobs :many
SELECT tj.token_id, tj.job_id, j.name AS job_name, (j.deleted_at IS NOT NULL)::boolean AS job_deleted
FROM api_token_jobs tj
JOIN jobs j ON j.id = tj.job_id
WHERE tj.token_id = ANY (@token_ids::uuid[])
ORDER BY tj.token_id, lower(j.name), j.id;

-- RevokeAPIToken revokes a token once; no row: unknown or already revoked.
-- name: RevokeAPIToken :one
UPDATE api_tokens
SET revoked_at = now(), revoked_by = @revoked_by
WHERE id = @id AND revoked_at IS NULL
RETURNING id, name, created_by, created_at, expires_at, revoked_at, revoked_by, allowed_cidrs, allow_any_address, never_expires;

-- UpdateAPITokenAddresses replaces the client addresses of an unrevoked
-- token (the same CHECKs as InsertAPIToken); no row: unknown or revoked.
-- The caller locks the token first (GetAPITokenForUpdate) for the old
-- values of its audit entry (api_token.addresses_changed).
-- name: UpdateAPITokenAddresses :one
UPDATE api_tokens
SET allowed_cidrs = coalesce(@allowed_cidrs::cidr[], '{}'), allow_any_address = @allow_any_address
WHERE id = @id AND revoked_at IS NULL
RETURNING id, name, created_by, created_at, expires_at, revoked_at, revoked_by, allowed_cidrs, allow_any_address, never_expires;

-- ListAPITokenLastSeen: per token the client address it was last seen
-- from, the later of its newest run request (accepted address) and its
-- newest access denial (refused address; denied = true). Tokens never
-- seen have no row; client_ip is NULL when the external API passed none.
-- name: ListAPITokenLastSeen :many
SELECT tok.id::uuid AS token_id, s.client_ip, s.seen_at::timestamptz AS seen_at, s.denied::boolean AS denied
FROM unnest(@token_ids::uuid[]) AS tok (id)
JOIN LATERAL (
    SELECT x.client_ip, x.seen_at, x.denied
    FROM (
        (SELECT r.ip AS client_ip, r.created_at AS seen_at, false AS denied
         FROM run_requests r WHERE r.token_id = tok.id
         ORDER BY r.created_at DESC, r.id DESC LIMIT 1)
        UNION ALL
        (SELECT d.client_ip, d.denied_at, true
         FROM api_access_denials d WHERE d.token_id = tok.id
         ORDER BY d.denied_at DESC, d.id DESC LIMIT 1)
    ) AS x
    ORDER BY x.seen_at DESC, x.denied DESC
    LIMIT 1
) AS s ON true
ORDER BY tok.id;

-- APITokenValid: unrevoked, unexpired and the creator may still hold it.
-- name: APITokenValid :one
SELECT public.api_token_valid(@token_id::uuid)::boolean AS valid;

-- APITokenJobAllowed is the scope rule (public.api_token_job_allowed),
-- re-checked by the consumer for every request.
-- name: APITokenJobAllowed :one
SELECT public.api_token_job_allowed(@token_id::uuid, @job_id::uuid)::boolean AS allowed;

-- ClaimPendingRunRequests locks the oldest undecided requests that no other
-- consumer holds (one consumer per request; decide them in the same
-- transaction), skipping @exclude (requests this consumer failed to decide
-- in its current pass). expired: older than @max_age_seconds by the
-- database clock.
-- name: ClaimPendingRunRequests :many
SELECT r.id, r.token_id, r.job_id, r.ip, r.created_at,
       (r.created_at < now() - make_interval(secs => @max_age_seconds::double precision))::boolean AS expired
FROM run_requests r
WHERE r.outcome = 'pending'
  AND r.id <> ALL (coalesce(@exclude::uuid[], '{}'::uuid[]))
ORDER BY r.created_at, r.id
LIMIT @max_rows
FOR UPDATE OF r SKIP LOCKED;

-- AcceptRunRequest records the River job and the queued run of a request.
-- name: AcceptRunRequest :execrows
UPDATE run_requests
SET outcome = 'accepted', river_job_id = @river_job_id::bigint, run_id = @run_id::uuid, decided_at = now()
WHERE id = @id AND outcome = 'pending';

-- RejectRunRequest records why a request did not start a run (run_active,
-- job_disabled, job_gone, token_invalid, expired).
-- name: RejectRunRequest :execrows
UPDATE run_requests
SET outcome = 'rejected', reason = @reason::text, decided_at = now()
WHERE id = @id AND outcome = 'pending';

-- PruneRunRequests deletes decided requests created before @before, at
-- most @max_rows per call.
-- name: PruneRunRequests :execrows
DELETE FROM run_requests
WHERE id IN (
    SELECT r.id FROM run_requests r
    WHERE r.created_at < @before::timestamptz AND r.outcome <> 'pending'
    ORDER BY r.created_at
    LIMIT @max_rows
);

-- ClaimAPIAccessDenials locks the oldest unprocessed access denials that
-- no other consumer holds (process them in the same transaction),
-- skipping @exclude, with the token's name and creator.
-- name: ClaimAPIAccessDenials :many
SELECT d.id, d.token_id, d.client_ip, d.denied_at, t.name AS token_name, t.created_by
FROM api_access_denials d
JOIN api_tokens t ON t.id = d.token_id
WHERE d.processed_at IS NULL
  AND d.id <> ALL (coalesce(@exclude::uuid[], '{}'::uuid[]))
ORDER BY d.denied_at, d.id
LIMIT @max_rows
FOR UPDATE OF d SKIP LOCKED;

-- MarkAPIAccessDenialProcessed: the denial was audited and alerted.
-- name: MarkAPIAccessDenialProcessed :execrows
UPDATE api_access_denials
SET processed_at = now()
WHERE id = @id AND processed_at IS NULL;

-- PruneAPIAccessDenials deletes processed denials from before @before, at
-- most @max_rows per call (unprocessed ones are left to the consumer).
-- name: PruneAPIAccessDenials :execrows
DELETE FROM api_access_denials
WHERE id IN (
    SELECT d.id FROM api_access_denials d
    WHERE d.denied_at < @before::timestamptz AND d.processed_at IS NOT NULL
    ORDER BY d.denied_at
    LIMIT @max_rows
);
