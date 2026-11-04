-- Queries of `bekup external-api` (D-220), connected as bekup_external.
-- The role may only call the SECURITY DEFINER functions of schema api
-- (migration 00037); each authenticates the token itself with the token
-- id and the SHA-256 secret hash computed by the caller. The result
-- columns come from the api.*_result views, so every field is nullable.
-- Every call passes the client address (as the external API determined
-- it through its trusted proxies): a token with an address allowlist is
-- refused from any other address (or without one) exactly like a wrong
-- secret, and the refusal may record a denial (D-220 amendment 1). Run
-- them outside an explicit transaction or in a READ COMMITTED one.

-- ExternalAuthenticate: no row means unknown, wrong secret, revoked,
-- expired, a creator who may no longer hold a token or a client address
-- outside the token's allowlist (401).
-- name: ExternalAuthenticate :one
SELECT * FROM api.authenticate(sqlc.arg(token_id)::uuid, sqlc.arg(secret_hash)::bytea, sqlc.narg(client_ip)::inet);

-- ExternalTokenJobs lists the jobs currently in the token's scope.
-- name: ExternalTokenJobs :many
SELECT * FROM api.token_jobs(sqlc.arg(token_id)::uuid, sqlc.arg(secret_hash)::bytea, sqlc.narg(client_ip)::inet);

-- ExternalRequestRun files a run request; refusal is NULL on success (see
-- api.request_run for the codes).
-- name: ExternalRequestRun :one
SELECT * FROM api.request_run(sqlc.arg(token_id)::uuid, sqlc.arg(secret_hash)::bytea, sqlc.arg(job_id)::uuid,
                              sqlc.narg(idempotency_key)::text, sqlc.narg(ip)::inet);

-- ExternalRequestStatus returns the raw facts of one of the token's
-- requests; no row: not found (404). The Go side aggregates the state.
-- name: ExternalRequestStatus :one
SELECT * FROM api.request_status(sqlc.arg(token_id)::uuid, sqlc.arg(secret_hash)::bytea, sqlc.arg(request_id)::uuid,
                                 sqlc.narg(client_ip)::inet);

-- ExternalSchemaVersion is bekup_schema_version() through the api schema
-- (startup and /readyz of the external API).
-- name: ExternalSchemaVersion :one
SELECT api.schema_version()::bigint AS version;
