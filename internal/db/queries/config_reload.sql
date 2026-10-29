-- Runtime config reload (D-200, internal/configreload). Key names only;
-- never values or the config file path.

-- LockConfigReloadRequests serializes UI reload requests (transaction-
-- scoped advisory lock), so the 10 s rate limit below holds across
-- concurrent requests and instances.
-- name: LockConfigReloadRequests :exec
SELECT pg_advisory_xact_lock(7142023362);

-- InsertConfigReloadUIRequest records an admin's request unless another
-- UI request was made within the last 10 seconds (no row: rate limited).
-- name: InsertConfigReloadUIRequest :one
INSERT INTO config_reload_requests (requested_by, trigger, instance_id)
SELECT @requested_by::uuid, 'ui', @instance_id::uuid
WHERE NOT EXISTS (
    SELECT 1 FROM config_reload_requests
    WHERE trigger = 'ui' AND requested_at > now() - interval '10 seconds')
RETURNING *;

-- InsertConfigReloadSignalRequest records a SIGHUP of one process.
-- name: InsertConfigReloadSignalRequest :one
INSERT INTO config_reload_requests (id, trigger, instance_id)
VALUES (@id::uuid, 'signal', @instance_id::uuid)
RETURNING *;

-- GetRecentConfigReloadUIRequest returns a UI request younger than
-- max_age (a NOTIFY is acted on only when it names such a row).
-- name: GetRecentConfigReloadUIRequest :one
SELECT * FROM config_reload_requests
WHERE id = @id AND trigger = 'ui' AND requested_at > now() - make_interval(secs => @max_age_seconds::float8);

-- GetConfigReloadRequest returns one request.
-- name: GetConfigReloadRequest :one
SELECT * FROM config_reload_requests WHERE id = @id;

-- LatestConfigReloadRequest returns the newest UI request.
-- name: LatestConfigReloadRequest :one
SELECT * FROM config_reload_requests WHERE trigger = 'ui' ORDER BY requested_at DESC LIMIT 1;

-- InsertConfigReloadResult records one process's outcome (0 rows: this
-- process already answered the request).
-- name: InsertConfigReloadResult :execrows
INSERT INTO config_reload_results (
    request_id, instance_id, process_kind, hostname, version, process_started_at,
    status, error_kind, applied_keys, restart_keys, error_keys, weakened_keys)
VALUES (
    sqlc.narg(request_id), @instance_id, @process_kind, @hostname, @version, @process_started_at,
    @status, sqlc.narg(error_kind), @applied_keys::text[], @restart_keys::text[],
    @error_keys::text[], @weakened_keys::text[])
ON CONFLICT (request_id, instance_id) DO NOTHING;

-- ConfigReloadResultsByRequest returns the answers to one request (with
-- the request's trigger).
-- name: ConfigReloadResultsByRequest :many
SELECT sqlc.embed(x), r.trigger AS request_trigger
FROM config_reload_results x
LEFT JOIN config_reload_requests r ON r.id = x.request_id
WHERE x.request_id = @request_id
ORDER BY x.finished_at, x.id
LIMIT 256;

-- LatestConfigReloadResultPerInstance returns the newest row of every
-- process that is listening now (@listening, any age) or was seen within
-- the last 7 days (at most 100, newest first), with the trigger of the
-- request it answered (NULL for a startup row).
-- name: LatestConfigReloadResultPerInstance :many
SELECT sqlc.embed(x), r.trigger AS request_trigger
FROM config_reload_results x
LEFT JOIN config_reload_requests r ON r.id = x.request_id
WHERE x.id IN (
    SELECT DISTINCT ON (instance_id) id
    FROM config_reload_results
    WHERE finished_at > now() - interval '7 days' OR instance_id = ANY(@listening::uuid[])
    ORDER BY instance_id, finished_at DESC, id DESC)
ORDER BY x.finished_at DESC, x.id DESC
LIMIT 100;

-- ConfigReloadInstancesResponsible returns the given instances that
-- answer a UI request made at the given time (D-200): those whose
-- first row (the startup row; DB time) is at most grace_seconds after
-- the request (some row that early exists). The same rule as UnansweredConfigReloadRequests; a process
-- started later loaded the file after the request and never answers it.
-- name: ConfigReloadInstancesResponsible :many
SELECT DISTINCT instance_id FROM config_reload_results
WHERE instance_id = ANY(@instance_ids::uuid[])
  AND finished_at <= sqlc.arg(requested_at)::timestamptz + make_interval(secs => sqlc.arg(grace_seconds)::float8);

-- UnansweredConfigReloadRequests returns the UI requests of the last 10
-- minutes this process (instance_id) has not answered yet and is
-- responsible for (made at most grace_seconds before its first row, DB
-- time; the rule of ConfigReloadInstancesResponsible), oldest first
-- (catch-up at start, after a lost NOTIFY or a reconnect). No row of the
-- process: nothing.
-- name: UnansweredConfigReloadRequests :many
SELECT r.* FROM config_reload_requests r
WHERE r.trigger = 'ui'
  AND r.requested_at > now() - interval '10 minutes'
  AND EXISTS (
      SELECT 1 FROM config_reload_results f
      WHERE f.instance_id = @instance_id::uuid
        AND f.finished_at <= r.requested_at + make_interval(secs => sqlc.arg(grace_seconds)::float8))
  AND NOT EXISTS (
      SELECT 1 FROM config_reload_results x
      WHERE x.request_id = r.id AND x.instance_id = @instance_id::uuid)
ORDER BY r.requested_at
LIMIT 20;

-- PruneConfigReloadRequests deletes requests (with their results) older
-- than 30 days, except a request whose answer is the newest row of a
-- process listening now (@listening): a long-running process stays listed.
-- name: PruneConfigReloadRequests :execrows
DELETE FROM config_reload_requests q
WHERE q.requested_at < now() - interval '30 days'
  AND NOT EXISTS (
      SELECT 1 FROM (
          SELECT DISTINCT ON (instance_id) request_id
          FROM config_reload_results
          WHERE instance_id = ANY(@listening::uuid[])
          ORDER BY instance_id, finished_at DESC, id DESC
      ) keep
      WHERE keep.request_id = q.id);

-- PruneConfigReloadResults deletes result and startup rows older than 30
-- days, except the newest row of every process listening now (@listening).
-- name: PruneConfigReloadResults :execrows
DELETE FROM config_reload_results x
WHERE x.finished_at < now() - interval '30 days'
  AND x.id NOT IN (
      SELECT DISTINCT ON (instance_id) id
      FROM config_reload_results
      WHERE instance_id = ANY(@listening::uuid[])
      ORDER BY instance_id, finished_at DESC, id DESC);

-- ConfigReloadListeners lists the application_name of every connection
-- that can receive a reload broadcast: the LISTEN connections of serve and
-- worker in this database (all run as bekup_app, so their rows are
-- visible), "<prefix>:<instance id>". Display and progress only: any
-- client can claim such a name (D-200).
-- name: ConfigReloadListeners :many
SELECT application_name::text AS application_name FROM pg_catalog.pg_stat_activity
WHERE datname = pg_catalog.current_database()
  AND (application_name LIKE 'bekup-serve-listen%' OR application_name LIKE 'bekup-worker-listen%')
LIMIT 1000;
