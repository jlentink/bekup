-- Notifications API (task 5.3b, D-183, D-191): shared channels (admin),
-- personal webhooks, the preference matrix and the in-app inbox. Only
-- internal/notify uses these queries. Every personal query is scoped by
-- user_id so one account can never read or change another's rows.

-- --- shared channels ---------------------------------------------------------

-- name: ListSharedChannels :many
SELECT * FROM shared_channels ORDER BY lower(name), id LIMIT 500;

-- ListSharedChannelAttachments returns the jobs (not deleted) the given
-- shared channels are attached to, with their filters.
-- name: ListSharedChannelAttachments :many
SELECT a.channel_id, a.job_id, j.name AS job_name, a.groups, a.events
FROM job_shared_channels a
JOIN jobs j ON j.id = a.job_id
WHERE a.channel_id = ANY(@channel_ids::uuid[]) AND j.deleted_at IS NULL
ORDER BY a.channel_id, lower(j.name), a.job_id;

-- ListJobSharedChannelAttachments returns the shared channels attached to
-- the given jobs with their filters (Job.sharedChannelAttachments, staff
-- only, D-198): no addresses, URLs or keys (config holds only the
-- webhook's display URL or the e-mail list, which the caller drops).
-- name: ListJobSharedChannelAttachments :many
SELECT a.job_id, c.id AS channel_id, c.name, c.type, c.config, c.enabled, a.groups, a.events
FROM job_shared_channels a
JOIN shared_channels c ON c.id = a.channel_id
WHERE a.job_id = ANY(@job_ids::uuid[])
ORDER BY a.job_id, lower(c.name), c.id;

-- UpdateSharedChannel changes the name and config; a non-NULL url_enc
-- replaces the sealed URL.
-- name: UpdateSharedChannel :one
UPDATE shared_channels
SET name = @name, config = @config, url_enc = coalesce(sqlc.narg(url_enc), url_enc)
WHERE id = @id
RETURNING *;

-- name: UpdateSharedChannelKey :execrows
UPDATE shared_channels SET hmac_key_enc = @hmac_key_enc WHERE id = @id AND type = 'webhook';

-- EnableSharedChannel turns a channel on and starts its failure counters
-- afresh.
-- name: EnableSharedChannel :one
UPDATE shared_channels
SET enabled = true, disabled_at = NULL, disabled_reason = NULL, consecutive_failures = 0, permanent_failures = 0,
    first_failure_at = NULL, last_failure_at = NULL, last_failure_code = NULL
WHERE id = @id
RETURNING *;

-- name: DisableSharedChannel :one
UPDATE shared_channels
SET enabled = false, disabled_at = CASE WHEN enabled THEN @at::timestamptz ELSE disabled_at END,
    disabled_reason = CASE WHEN enabled THEN 'manual' ELSE disabled_reason END
WHERE id = @id
RETURNING *;

-- name: DeleteSharedChannel :execrows
DELETE FROM shared_channels WHERE id = $1;

-- name: DetachSharedChannel :execrows
DELETE FROM job_shared_channels WHERE job_id = @job_id AND channel_id = @channel_id;

-- GetJobForAttach returns a job that is not deleted (attachments).
-- name: GetJobForAttach :one
SELECT id, name FROM jobs WHERE id = $1 AND deleted_at IS NULL;

-- --- personal webhooks --------------------------------------------------------

-- name: ListUserChannelsOfUser :many
SELECT * FROM user_channels WHERE user_id = $1 AND type = 'webhook' ORDER BY created_at, id;

-- name: GetUserChannelOfUserForUpdate :one
SELECT * FROM user_channels WHERE id = @id AND user_id = @user_id FOR UPDATE;

-- name: UpdateUserChannelKey :execrows
UPDATE user_channels SET hmac_key_enc = @hmac_key_enc WHERE id = @id AND user_id = @user_id;

-- ReenableUserChannel turns a personal webhook on again and starts its
-- failure counters afresh.
-- name: ReenableUserChannel :one
UPDATE user_channels
SET enabled = true, disabled_at = NULL, disabled_reason = NULL, consecutive_failures = 0, permanent_failures = 0,
    first_failure_at = NULL, last_failure_at = NULL, last_failure_code = NULL
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteUserChannelOfUser :execrows
DELETE FROM user_channels WHERE id = @id AND user_id = @user_id;

-- name: CountEnabledUserWebhooks :one
SELECT count(*) FROM user_channels WHERE user_id = $1 AND type = 'webhook' AND enabled;

-- GetUserContact returns the own e-mail address and its verification.
-- name: GetUserContact :one
SELECT email, email_verified_at FROM users WHERE id = $1 AND deleted_at IS NULL;

-- --- preferences ------------------------------------------------------------------

-- name: ListUserNotificationPrefs :many
SELECT event, job_id, channel, enabled FROM user_notification_prefs
WHERE user_id = $1
ORDER BY event, job_id NULLS FIRST, channel;

-- name: DeleteUserNotificationPrefs :execrows
DELETE FROM user_notification_prefs WHERE user_id = $1;

-- name: DeleteUserJobNotificationPrefs :execrows
DELETE FROM user_notification_prefs WHERE user_id = @user_id AND job_id = @job_id::uuid;

-- name: ListJobNamesByIDs :many
SELECT id, name FROM jobs WHERE id = ANY(@ids::uuid[]) AND deleted_at IS NULL;

-- --- inbox ------------------------------------------------------------------------

-- ListUserNotifications returns a page of the own inbox, newest first,
-- after the (created_at, id) cursor; @types (NULL: every type) limits it
-- to the given event types (the inbox filter, D-196).
-- name: ListUserNotifications :many
SELECT * FROM notifications
WHERE notifications.user_id = @user_id
  AND (NOT @unread_only::boolean OR read_at IS NULL)
  -- Read-time visibility (M5 review L5): @hidden_types are the types the
  -- reader's current role may no longer see; with @owned_jobs_only, rows
  -- about a job the reader no longer owns are left out.
  AND NOT (notifications.type = ANY(coalesce(@hidden_types::text[], ARRAY[]::text[])))
  AND (NOT @owned_jobs_only::boolean OR NOT EXISTS (
      SELECT 1 FROM notification_events e
      WHERE e.id = notifications.event_id AND e.job_id IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM job_owners o WHERE o.job_id = e.job_id AND o.user_id = notifications.user_id)))
  AND (sqlc.narg(types)::text[] IS NULL OR type = ANY(sqlc.narg(types)::text[]))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @max_rows;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications
WHERE notifications.user_id = @user_id AND read_at IS NULL
  -- Read-time visibility (M5 review L5): @hidden_types are the types the
  -- reader's current role may no longer see; with @owned_jobs_only, rows
  -- about a job the reader no longer owns are left out.
  AND NOT (notifications.type = ANY(coalesce(@hidden_types::text[], ARRAY[]::text[])))
  AND (NOT @owned_jobs_only::boolean OR NOT EXISTS (
      SELECT 1 FROM notification_events e
      WHERE e.id = notifications.event_id AND e.job_id IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM job_owners o WHERE o.job_id = e.job_id AND o.user_id = notifications.user_id)));

-- name: GetUserNotification :one
SELECT * FROM notifications WHERE notifications.id = @id AND notifications.user_id = @user_id
  -- Read-time visibility (M5 review L5): @hidden_types are the types the
  -- reader's current role may no longer see; with @owned_jobs_only, rows
  -- about a job the reader no longer owns are left out.
  AND NOT (notifications.type = ANY(coalesce(@hidden_types::text[], ARRAY[]::text[])))
  AND (NOT @owned_jobs_only::boolean OR NOT EXISTS (
      SELECT 1 FROM notification_events e
      WHERE e.id = notifications.event_id AND e.job_id IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM job_owners o WHERE o.job_id = e.job_id AND o.user_id = notifications.user_id)));

-- ListUserNotificationsSince returns the own inbox rows created at or
-- after a time (stream resync), oldest first.
-- name: ListUserNotificationsSince :many
SELECT * FROM notifications
WHERE notifications.user_id = @user_id AND notifications.created_at >= @since::timestamptz
  -- Read-time visibility (M5 review L5): @hidden_types are the types the
  -- reader's current role may no longer see; with @owned_jobs_only, rows
  -- about a job the reader no longer owns are left out.
  AND NOT (notifications.type = ANY(coalesce(@hidden_types::text[], ARRAY[]::text[])))
  AND (NOT @owned_jobs_only::boolean OR NOT EXISTS (
      SELECT 1 FROM notification_events e
      WHERE e.id = notifications.event_id AND e.job_id IS NOT NULL
        AND NOT EXISTS (SELECT 1 FROM job_owners o WHERE o.job_id = e.job_id AND o.user_id = notifications.user_id)))
ORDER BY created_at, id
LIMIT @max_rows;

-- name: MarkUserNotificationsRead :execrows
UPDATE notifications SET read_at = @at::timestamptz
WHERE user_id = @user_id AND id = ANY(@ids::uuid[]) AND read_at IS NULL;

-- name: MarkAllUserNotificationsRead :execrows
UPDATE notifications SET read_at = @at::timestamptz WHERE user_id = @user_id AND read_at IS NULL;

-- DeleteUserNotification deletes one own notification; security.*
-- rows are kept (mark read only, M5 review L2).
-- name: DeleteUserNotification :execrows
DELETE FROM notifications WHERE id = @id AND user_id = @user_id AND type NOT LIKE 'security.%';

-- name: GetUserNotificationType :one
SELECT type FROM notifications WHERE id = @id AND user_id = @user_id;
