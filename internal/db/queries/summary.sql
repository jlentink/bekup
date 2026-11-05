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

-- SummaryRunCounts counts the runs of visible jobs that finished in
-- (@period_from, @period_until], per job and status.
-- name: SummaryRunCounts :many
SELECT r.job_id, r.status::text AS status, count(*)::bigint AS runs
FROM runs r
JOIN jobs j ON j.id = r.job_id
WHERE r.finished_at > @period_from::timestamptz AND r.finished_at <= @period_until::timestamptz
  AND j.deleted_at IS NULL
  AND (@scope_all::boolean OR EXISTS (
        SELECT 1 FROM job_owners o JOIN users ou ON ou.id = o.user_id
        WHERE o.job_id = r.job_id AND o.user_id = @user_id::uuid AND ou.deleted_at IS NULL))
GROUP BY r.job_id, r.status;

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
