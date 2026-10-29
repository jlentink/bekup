-- Missed-backup (dead-man) detector (internal/notify/missed, task 5.4,
-- D-188); only that package uses these queries. All reads are bounded per
-- job (keyset batches of jobs, a lookback window, a cap of occurrences).

-- ListMissedCheckJobs returns a keyset batch of the jobs the detector
-- watches: enabled, not deleted and not manual.
-- name: ListMissedCheckJobs :many
SELECT id, schedule, next_run_at, timeout_seconds, expected_window_seconds
FROM jobs
WHERE enabled
  AND deleted_at IS NULL
  AND schedule->>'kind' <> 'manual'
  AND id > @after::uuid
ORDER BY id
LIMIT @batch_size::int;

-- MissedCheckOccurrences returns, per job, the planned occurrences the
-- scheduler handled whose deadline lies in the checked range: scheduled,
-- catch-up and early runs by scheduled_for, and schedule_skips rows
-- (still_running, unschedulable). @since and @until are per job (the
-- occurrence must lie in (since, until], until = now - window). Scheduled
-- and catch-up runs are queued at or after their occurrence, so the
-- started/queued bound (with an hour of clock margin) lets the job's
-- start index bound the scan; early runs use their own index. At most
-- @max_per_job of the newest occurrences per job are returned.
-- name: MissedCheckOccurrences :many
WITH p AS (
    SELECT unnest(@job_ids::uuid[]) AS job_id, unnest(@since::timestamptz[]) AS since,
           unnest(@until::timestamptz[]) AS until
), a AS (
    SELECT r.job_id, r.scheduled_for AS occurrence, 'run'::text AS source, r.status::text AS status,
           r.finished_at AS finished_at
    FROM p JOIN runs r ON r.job_id = p.job_id
    WHERE r.trigger IN ('schedule', 'catch_up')
      AND COALESCE(r.started_at, r.queued_at) > p.since - interval '1 hour'
      AND r.scheduled_for > p.since AND r.scheduled_for <= p.until
    UNION ALL
    SELECT r.job_id, r.scheduled_for, 'run', r.status::text, r.finished_at
    FROM p JOIN runs r ON r.job_id = p.job_id
    WHERE r.trigger = 'early'
      AND r.scheduled_for > p.since AND r.scheduled_for <= p.until
    UNION ALL
    SELECT s.job_id, COALESCE(s.scheduled_for, s.created_at), 'skip', s.reason, NULL::timestamptz
    FROM p JOIN schedule_skips s ON s.job_id = p.job_id
    WHERE COALESCE(s.scheduled_for, s.created_at) > p.since
      AND COALESCE(s.scheduled_for, s.created_at) <= p.until
), n AS (
    SELECT a.*, row_number() OVER (PARTITION BY a.job_id ORDER BY a.occurrence DESC, a.source) AS rn
    FROM a
)
SELECT n.job_id::uuid AS job_id, n.occurrence::timestamptz AS occurrence, n.source::text AS source,
       n.status::text AS status, n.finished_at AS finished_at
FROM n
WHERE n.rn <= @max_per_job::int
ORDER BY n.job_id, n.occurrence, n.source;

-- MissedCheckSuccesses returns, per job, the successful and partial runs
-- that finished after @since (per job; started no earlier than
-- @max_run_seconds before it, the longest a run can take) and the latest
-- one that finished at or before it (runs of a job never overlap, so the
-- queue order is the finish order). Ordered by job and finish time.
-- name: MissedCheckSuccesses :many
WITH p AS (
    SELECT unnest(@job_ids::uuid[]) AS job_id, unnest(@since::timestamptz[]) AS since
), s AS (
    SELECT r.job_id, r.id, r.finished_at
    FROM p JOIN runs r ON r.job_id = p.job_id
    WHERE r.status IN ('success', 'partial')
      AND COALESCE(r.started_at, r.queued_at) > p.since - make_interval(secs => @max_run_seconds::bigint)
      AND r.finished_at > p.since
    UNION ALL
    SELECT p.job_id, l.id, l.finished_at
    FROM p CROSS JOIN LATERAL (
        SELECT r.id, r.finished_at FROM runs r
        WHERE r.job_id = p.job_id AND r.status IN ('success', 'partial') AND r.finished_at <= p.since
        ORDER BY r.queued_at DESC, r.id DESC
        LIMIT 1
    ) l
)
SELECT s.job_id::uuid AS job_id, s.id::uuid AS run_id, s.finished_at::timestamptz AS finished_at
FROM s
ORDER BY s.job_id, s.finished_at, s.id;

-- MissedEventCreatedAt returns when the backup.missed event with a dedup
-- key was emitted (reminders count from it).
-- name: MissedEventCreatedAt :one
SELECT created_at FROM notification_events
WHERE dedup_key = @dedup_key::text AND type = 'backup.missed';

-- UpsertJobMissedState records the ongoing outage of a job (D-196): the
-- detector's view after its last pass, read by Job.missed.
-- name: UpsertJobMissedState :exec
INSERT INTO job_missed_state (job_id, missed_since, last_missed_at, reason, checked_at)
VALUES (@job_id::uuid, @missed_since::timestamptz, @last_missed_at::timestamptz, @reason::text, @checked_at::timestamptz)
ON CONFLICT (job_id) DO UPDATE
SET missed_since = EXCLUDED.missed_since,
    last_missed_at = EXCLUDED.last_missed_at,
    reason = EXCLUDED.reason,
    checked_at = EXCLUDED.checked_at;

-- ClearJobMissedState deletes the state of checked jobs whose outage
-- ended (or that have none).
-- name: ClearJobMissedState :exec
DELETE FROM job_missed_state WHERE job_id = ANY(@job_ids::uuid[]);

-- PruneJobMissedState deletes the state of jobs the detector no longer
-- watches (disabled, deleted or manual).
-- name: PruneJobMissedState :execrows
DELETE FROM job_missed_state s
USING jobs j
WHERE s.job_id = j.id
  AND (NOT j.enabled OR j.deleted_at IS NOT NULL OR j.schedule->>'kind' = 'manual');

-- ListJobMissedState returns the ongoing outages of a batch of jobs
-- (Job.missed / missedSince). A successful or partial run that finished
-- after the detector's last confirmation ends the outage for readers at
-- once (the next pass then deletes the row).
-- name: ListJobMissedState :many
SELECT s.job_id, s.missed_since, s.last_missed_at, s.reason
FROM job_missed_state s
JOIN jobs j ON j.id = s.job_id
WHERE s.job_id = ANY(@job_ids::uuid[])
  AND j.enabled
  AND j.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM runs r
      WHERE r.job_id = s.job_id
        AND r.status IN ('success', 'partial')
        AND r.finished_at > s.checked_at
  );
