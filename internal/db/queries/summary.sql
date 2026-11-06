-- Periodic summary e-mail (D-226): the per-user settings and schedule
-- state (user_summary_settings, written only by internal/notify) and the
-- scoped reads of the summary content. Content reads take scope_all =
-- false to limit rows to the jobs @user_id owns (authz.JobScope, the same
-- rule as run_reads.sql: deleted users own nothing).

-- --- settings and schedule state ---------------------------------------------

-- InsertMissingSummarySettings adds a state-only row (the configured
-- defaults apply) for active accounts without one, so the tick can keep
-- their schedule state.
-- name: InsertMissingSummarySettings :execrows
INSERT INTO user_summary_settings (user_id)
SELECT u.id FROM users u
WHERE u.deleted_at IS NULL AND NOT u.disabled
  AND NOT EXISTS (SELECT 1 FROM user_summary_settings s WHERE s.user_id = u.id)
ORDER BY u.id
LIMIT @max_rows::int
ON CONFLICT (user_id) DO NOTHING;

-- ListSummarySettingsToPlan locks rows of active accounts whose next due
-- time was never computed, or that follow the defaults and were computed
-- from other defaults than @default_basis.
-- name: ListSummarySettingsToPlan :many
SELECT s.* FROM user_summary_settings s
JOIN users u ON u.id = s.user_id
WHERE u.deleted_at IS NULL AND NOT u.disabled
  AND (s.due_basis IS NULL OR (s.frequency IS NULL AND s.due_basis <> @default_basis::text))
ORDER BY s.user_id
LIMIT @max_rows::int
FOR UPDATE OF s SKIP LOCKED;

-- name: SetSummaryPlan :exec
UPDATE user_summary_settings
SET due_basis = @due_basis::text, next_due_at = sqlc.narg(next_due_at)::timestamptz
WHERE user_id = @user_id;

-- ClaimDueSummary locks the most overdue summary of an active account
-- (other ticks skip it); @skip_ids are the accounts that failed in this
-- tick (retried at the next one).
-- name: ClaimDueSummary :one
SELECT sqlc.embed(user_summary_settings), u.role
FROM user_summary_settings
JOIN users u ON u.id = user_summary_settings.user_id
WHERE user_summary_settings.next_due_at <= @now::timestamptz
  AND u.deleted_at IS NULL AND NOT u.disabled
  AND NOT (user_summary_settings.user_id = ANY (coalesce(@skip_ids::uuid[], '{}'::uuid[])))
ORDER BY user_summary_settings.next_due_at, user_summary_settings.user_id
LIMIT 1
FOR UPDATE OF user_summary_settings SKIP LOCKED;

-- RecordSummaryHandled stores the outcome of one due summary and the next
-- due time.
-- name: RecordSummaryHandled :exec
UPDATE user_summary_settings
SET due_basis = @due_basis::text,
    next_due_at = sqlc.narg(next_due_at)::timestamptz,
    last_period_end = @period_end::timestamptz,
    last_sent_at = coalesce(sqlc.narg(sent_at)::timestamptz, last_sent_at),
    last_outcome = @outcome::text,
    last_outcome_at = @outcome_at::timestamptz
WHERE user_id = @user_id;

-- name: GetSummarySettings :one
SELECT * FROM user_summary_settings WHERE user_id = $1;

-- name: GetSummarySettingsForUpdate :one
SELECT * FROM user_summary_settings WHERE user_id = $1 FOR UPDATE;

-- UpsertSummarySettings stores a user's own settings with the schedule
-- computed from them.
-- name: UpsertSummarySettings :exec
INSERT INTO user_summary_settings (user_id, frequency, hour, weekday, month_day, timezone, due_basis,
                                   next_due_at, last_period_end)
VALUES (@user_id, @frequency, @hour, @weekday, @month_day, @timezone::text, @due_basis::text,
        sqlc.narg(next_due_at)::timestamptz, sqlc.narg(last_period_end)::timestamptz)
ON CONFLICT (user_id) DO UPDATE
SET frequency = EXCLUDED.frequency, hour = EXCLUDED.hour, weekday = EXCLUDED.weekday,
    month_day = EXCLUDED.month_day, timezone = EXCLUDED.timezone, due_basis = EXCLUDED.due_basis,
    next_due_at = EXCLUDED.next_due_at, last_period_end = EXCLUDED.last_period_end;

-- --- content (scoped to the recipient) -----------------------------------------

-- CountSummaryJobs counts the jobs the recipient may see (not deleted).
-- name: CountSummaryJobs :one
SELECT count(*)::bigint FROM jobs j
WHERE j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = j.id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL));

-- ListSummaryJobs returns the jobs the recipient may see (not deleted),
-- by name.
-- name: ListSummaryJobs :many
SELECT j.id, j.name, j.enabled, j.schedule, j.created_at
FROM jobs j
WHERE j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = j.id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
ORDER BY lower(j.name), j.id
LIMIT @max_rows::int;

-- SummaryJobActivity summarizes, per visible job, the runs that finished
-- in (@period_from, @period_until]: counts per status (and of successful
-- runs with warnings), the bytes stored, the newest finish time and the
-- error code of the newest failed run ('' for none; one index probe per
-- job with a failure, runs_job_failed_idx).
-- name: SummaryJobActivity :many
WITH a AS (
    SELECT r.job_id,
           count(*) FILTER (WHERE r.status = 'success')::bigint AS succeeded,
           count(*) FILTER (WHERE r.status = 'partial')::bigint AS partial,
           count(*) FILTER (WHERE r.status = 'failed')::bigint AS failed,
           count(*) FILTER (WHERE r.status = 'canceled')::bigint AS canceled,
           count(*) FILTER (WHERE r.status = 'skipped')::bigint AS skipped,
           count(*) FILTER (WHERE r.status = 'success' AND r.has_warnings)::bigint AS with_warnings,
           COALESCE(sum(r.bytes_stored), 0)::bigint AS bytes_stored,
           max(r.finished_at)::timestamptz AS last_finished_at
    FROM runs r
    JOIN jobs j ON j.id = r.job_id
    WHERE r.finished_at > @period_from::timestamptz AND r.finished_at <= @period_until::timestamptz
      AND j.deleted_at IS NULL
      AND (@scope_all::boolean OR EXISTS (
            SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
            WHERE o.job_id = r.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
    GROUP BY r.job_id
)
SELECT a.job_id, a.succeeded, a.partial, a.failed, a.canceled, a.skipped, a.with_warnings, a.bytes_stored,
       a.last_finished_at::timestamptz AS last_finished_at,
       COALESCE(lf.error_code, '')::text AS last_failure_code
FROM a
LEFT JOIN LATERAL (
    SELECT f.error_code FROM runs f
    WHERE a.failed > 0 AND f.job_id = a.job_id AND f.status = 'failed'
      AND f.finished_at > @period_from::timestamptz AND f.finished_at <= @period_until::timestamptz
    ORDER BY f.finished_at DESC, f.id DESC
    LIMIT 1
) lf ON true;

-- SummaryTestRestoreCounts counts the test restores of visible jobs that
-- finished in (@period_from, @period_until], per job and status.
-- name: SummaryTestRestoreCounts :many
SELECT t.job_id, t.status::text AS status, count(*)::bigint AS test_restores
FROM test_restores t
JOIN jobs j ON j.id = t.job_id
WHERE t.finished_at > @period_from::timestamptz AND t.finished_at <= @period_until::timestamptz
  AND j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = t.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
GROUP BY t.job_id, t.status;

-- SummaryRestoreCounts counts the restores of visible jobs (requested
-- restores; the internal restores of test restores are counted by
-- SummaryTestRestoreCounts) that finished in (@period_from,
-- @period_until], per status.
-- name: SummaryRestoreCounts :many
SELECT r.status::text AS status, count(*)::bigint AS restores
FROM restores r
JOIN jobs j ON j.id = r.job_id
WHERE r.finished_at > @period_from::timestamptz AND r.finished_at <= @period_until::timestamptz
  AND j.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM test_restore_items i WHERE i.restore_id = r.id)
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = r.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
GROUP BY r.status;

-- ListActiveRunJobs returns the jobs of @job_ids with a queued or running
-- run (not judged "no success" while a run is under way).
-- name: ListActiveRunJobs :many
SELECT DISTINCT r.job_id FROM runs r
WHERE r.job_id = ANY (@job_ids::uuid[]) AND r.status IN ('queued', 'running');

-- CountSecurityAlertsBetween counts the security.system alerts raised in
-- (@period_from, @period_until] (admins' summaries only).
-- name: CountSecurityAlertsBetween :one
SELECT count(*)::bigint FROM notification_events
WHERE type = 'security.system'
  AND created_at > @period_from::timestamptz AND created_at <= @period_until::timestamptz;
