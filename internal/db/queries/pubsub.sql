-- LISTEN/NOTIFY bridge (internal/pubsub, task 1.10). Payloads carry
-- IDs only; the channel names are fixed here, one LISTEN query per
-- channel, so no SQL is ever built from strings.

-- Notify sends a notification on channel. Inside a transaction Postgres
-- delivers it only on commit (and drops it on rollback).
-- name: Notify :exec
SELECT pg_notify(@channel::text, @payload::text);

-- name: ListenSessionEvents :exec
LISTEN bekup_session_events;

-- name: ListenRunEvents :exec
LISTEN bekup_run_events;

-- name: ListenRestoreEvents :exec
LISTEN bekup_restore_events;
