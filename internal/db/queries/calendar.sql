-- Job calendar reads (internal/calendar, task 3.14, D-156). Read only and
-- batched: one query per kind for the whole set of visible jobs (no
-- per-job queries). Every query is bounded by @max_rows; the caller asks
-- for one row more than it uses to detect truncation.

-- ListCalendarJobs returns the visible, non-deleted jobs: scope_all = false
-- limits them to the jobs @user_id owns (authz.JobScope; deleted users own
-- nothing), job_ids (NULL: all) narrows them further, unknown or invisible
-- ids are silently dropped.
-- name: ListCalendarJobs :many
SELECT j.id, j.name, j.enabled, j.schedule, j.next_run_at, j.retention_keep_last, j.retention_keep_days
FROM jobs j
WHERE j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = j.id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
  AND (sqlc.narg(job_ids)::uuid[] IS NULL OR j.id = ANY(sqlc.narg(job_ids)::uuid[]))
ORDER BY lower(j.name), j.id
LIMIT @max_rows::int;

-- ListCalendarRuns returns the runs of the jobs that may intersect
-- [range_from, range_to), as the union of three index-bounded branches
-- (D-160): (1) runs that started (else were queued) after @scan_from,
-- began before the end of the range and did not finish before its start
-- (a run lasts at most the 7-day job timeout after it started; the
-- look-back bounds the scan; index runs_job_start_idx); (2) every still
-- active run (runs_active_idx); (3) runs with trigger 'early' whose
-- original slot (scheduled_for) lies in the range: they mark the
-- pulled-forward planned occurrence (runs_job_early_idx). None of the
-- branches scans a job's whole history.
-- name: ListCalendarRuns :many
SELECT b.id, b.job_id, b.status, b.trigger::text AS run_trigger, b.scheduled_for, b.queued_at,
       b.started_at, b.finished_at, b.error_code, coalesce(b.started_at, b.queued_at) AS sort_at
FROM runs b
WHERE b.job_id = ANY(@job_ids::uuid[])
  AND coalesce(b.started_at, b.queued_at) >= @scan_from::timestamptz
  AND least(b.queued_at, coalesce(b.scheduled_for, b.queued_at)) < @range_to::timestamptz
  AND (b.finished_at IS NULL OR b.finished_at >= @range_from::timestamptz)
UNION
SELECT b.id, b.job_id, b.status, b.trigger::text AS run_trigger, b.scheduled_for, b.queued_at,
       b.started_at, b.finished_at, b.error_code, coalesce(b.started_at, b.queued_at) AS sort_at
FROM runs b
WHERE b.status IN ('queued', 'running')
  AND b.job_id = ANY(@job_ids::uuid[])
  AND least(b.queued_at, coalesce(b.scheduled_for, b.queued_at)) < @range_to::timestamptz
UNION
SELECT b.id, b.job_id, b.status, b.trigger::text AS run_trigger, b.scheduled_for, b.queued_at,
       b.started_at, b.finished_at, b.error_code, coalesce(b.started_at, b.queued_at) AS sort_at
FROM runs b
WHERE b.trigger = 'early'
  AND b.job_id = ANY(@job_ids::uuid[])
  AND b.scheduled_for >= @range_from::timestamptz AND b.scheduled_for < @range_to::timestamptz
ORDER BY sort_at, id
LIMIT @max_rows::int;

-- ListCalendarSkips returns the skipped occurrences of the jobs in
-- [range_from, range_to) (by scheduled_for, else created_at).
-- name: ListCalendarSkips :many
SELECT s.id, s.job_id, s.scheduled_for, s.reason, s.created_at
FROM schedule_skips s
WHERE s.job_id = ANY(@job_ids::uuid[])
  AND coalesce(s.scheduled_for, s.created_at) >= @range_from::timestamptz
  AND coalesce(s.scheduled_for, s.created_at) < @range_to::timestamptz
ORDER BY coalesce(s.scheduled_for, s.created_at), s.id
LIMIT @max_rows::int;

-- ListCalendarCopies summarizes the present artifacts of the jobs' finished
-- runs per (run, destination) for the retention projection: the run time
-- (as retention orders runs), whether the copy is complete (the meaning of
-- retention.Plan: at least one item, every item with a present, verified
-- artifact on the destination, and the manifest present there) and the
-- latest Object Lock retain-until (the epoch: none). The item count of
-- each run is computed once (item_counts), not per copy. Ordered by job so
-- that a truncated result can drop the last, partial job.
-- name: ListCalendarCopies :many
WITH present AS (
    SELECT a.job_id, a.run_id, a.destination_id, a.kind, a.run_item_id, a.verified, a.lock_until
    FROM artifacts a
    WHERE a.job_id = ANY(@job_ids::uuid[])
      AND a.state = 'present'
), item_counts AS (
    SELECT ri.run_id, count(*) AS n
    FROM run_items ri
    WHERE ri.run_id IN (SELECT DISTINCT p.run_id FROM present p)
    GROUP BY ri.run_id
)
SELECT p.job_id, p.run_id, p.destination_id,
       coalesce(r.storage_timestamp, r.started_at, r.queued_at)::timestamptz AS run_time,
       (bool_or(p.kind = 'manifest')
        AND coalesce(ic.n, 0) > 0
        AND count(DISTINCT p.run_item_id) FILTER (WHERE p.verified) = coalesce(ic.n, 0))::boolean AS complete,
       coalesce(max(p.lock_until), 'epoch'::timestamptz)::timestamptz AS lock_until
FROM present p
JOIN runs r ON r.id = p.run_id
LEFT JOIN item_counts ic ON ic.run_id = p.run_id
WHERE r.status NOT IN ('queued', 'running')
GROUP BY p.job_id, p.run_id, p.destination_id, r.storage_timestamp, r.started_at, r.queued_at, ic.n
ORDER BY p.job_id, p.run_id, p.destination_id
LIMIT @max_rows::int;

-- SetCalendarReadLimits makes the calendar's transaction read only and
-- bounds every statement in it (D-160): @timeout is a statement_timeout
-- value such as '5s'. Must be the first statement of the transaction.
-- name: SetCalendarReadLimits :exec
SELECT set_config('transaction_read_only', 'on', true), set_config('statement_timeout', @timeout::text, true);
