-- Monitoring reads (task 5.6): small, bounded aggregates for the internal
-- Prometheus endpoint and Query.systemHealth. Never per-user data.

-- name: MonitorQueueJobs :many
-- River jobs per queue and state. River prunes finished rows (completed
-- after a day, canceled/discarded after a week), so the table stays small.
SELECT queue, state::text AS state, count(*)::bigint AS jobs
FROM river_job
GROUP BY queue, state
ORDER BY queue, state
LIMIT 200;

-- name: MonitorEarliestPinNotAfter :many
-- The earliest expiry of a pinned target-database certificate (primary
-- pins of connections that are not deleted); no row without pins.
SELECT tls_pin_not_after
FROM connections
WHERE deleted_at IS NULL AND tls_pin_not_after IS NOT NULL
ORDER BY tls_pin_not_after
LIMIT 1;

-- name: MonitorLastSuccessPerJob :many
-- The finish time of the latest successful run of every enabled job that
-- has one, stalest first (at most 1000 jobs: the ones a missed-backup
-- alert cares about). Jobs without any successful run are not listed.
SELECT j.slug, s.finished_at
FROM jobs j
CROSS JOIN LATERAL (
    SELECT r.finished_at
    FROM runs r
    WHERE r.job_id = j.id AND r.status = 'success' AND r.finished_at IS NOT NULL
    ORDER BY r.queued_at DESC, r.id DESC
    LIMIT 1
) s
WHERE j.deleted_at IS NULL AND j.enabled
ORDER BY s.finished_at, j.slug
LIMIT 1000;
