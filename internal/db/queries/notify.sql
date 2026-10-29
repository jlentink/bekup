-- Notification core (task 5.3, D-183); only internal/notify uses these
-- queries.

-- InsertNotificationEvent adds an event to the outbox. A dedup_key that
-- was already emitted makes it a no-op (no row returned).
-- name: InsertNotificationEvent :many
INSERT INTO notification_events (type, severity, job_id, run_id, restore_id, test_restore_id, connection_id,
    destination_id, artifact_id, user_id, detail, dedup_key)
VALUES (@type, @severity, sqlc.narg(job_id), sqlc.narg(run_id), sqlc.narg(restore_id), sqlc.narg(test_restore_id),
    sqlc.narg(connection_id), sqlc.narg(destination_id), sqlc.narg(artifact_id), sqlc.narg(user_id), @detail,
    sqlc.narg(dedup_key))
ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
RETURNING id;

-- ClaimUndispatchedEvents locks events whose fan-out job is not enqueued
-- yet (the dispatcher enqueues it in the same transaction).
-- name: ClaimUndispatchedEvents :many
SELECT id FROM notification_events
WHERE dispatched_at IS NULL
ORDER BY created_at
LIMIT @max_events
FOR UPDATE SKIP LOCKED;

-- name: MarkEventsDispatched :exec
UPDATE notification_events SET dispatched_at = @at::timestamptz
WHERE id = ANY(@ids::uuid[]) AND dispatched_at IS NULL;

-- GetNotificationEventForUpdate locks an event for its fan-out.
-- name: GetNotificationEventForUpdate :one
SELECT * FROM notification_events WHERE id = $1 FOR UPDATE;

-- name: GetNotificationEvent :one
SELECT * FROM notification_events WHERE id = $1;

-- name: MarkEventFannedOut :exec
UPDATE notification_events SET fanned_out_at = @at::timestamptz
WHERE id = @id AND fanned_out_at IS NULL;

-- FindCollapseTarget returns the latest delivered (not collapsed) event of
-- the same type and job within the window before the event.
-- name: FindCollapseTarget :one
SELECT id FROM notification_events
WHERE type = @type AND job_id = @job_id::uuid AND id <> @id
  AND collapsed_into IS NULL AND fanned_out_at IS NOT NULL
  AND created_at >= @since::timestamptz AND created_at <= @until::timestamptz
ORDER BY created_at DESC
LIMIT 1;

-- name: CollapseEventInto :exec
UPDATE notification_events SET collapsed_into = @target, fanned_out_at = @at::timestamptz
WHERE id = @id;

-- name: IncrementEventCollapsed :exec
UPDATE notification_events SET collapsed_count = collapsed_count + 1 WHERE id = $1;

-- NotifyAdvisoryLock serializes fan-outs per key (user flood counters,
-- started-event collapse) until the transaction ends.
-- name: NotifyAdvisoryLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(@lock_key::text, 0));

-- ListNotifiableUsers returns the active accounts (not deleted, not
-- disabled) with their role and e-mail verification.
-- name: ListNotifiableUsers :many
SELECT id, role, email, email_verified_at FROM users
WHERE deleted_at IS NULL AND NOT disabled
ORDER BY id;

-- name: GetNotifiableUser :one
SELECT id, role, email, email_verified_at FROM users
WHERE id = $1 AND deleted_at IS NULL AND NOT disabled;

-- ListActiveAdminIDs returns the active admins.
-- name: ListActiveAdminIDs :many
SELECT id FROM users WHERE role = 'admin' AND deleted_at IS NULL AND NOT disabled ORDER BY id;

-- ListNotificationPrefs returns the preferences of users for one event:
-- the per-event rows and the per-job overrides of one job.
-- name: ListNotificationPrefs :many
SELECT user_id, job_id, channel, enabled FROM user_notification_prefs
WHERE user_id = ANY(@user_ids::uuid[]) AND event = @event
  AND (job_id IS NULL OR job_id = sqlc.narg(job_id)::uuid);

-- name: UpsertNotificationPref :exec
INSERT INTO user_notification_prefs (user_id, event, job_id, channel, enabled)
VALUES (@user_id, @event, sqlc.narg(job_id), @channel, @enabled)
ON CONFLICT (user_id, event, job_id, channel) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now();

-- ListEnabledUserWebhooks returns the enabled personal webhooks of users.
-- name: ListEnabledUserWebhooks :many
SELECT id, user_id FROM user_channels
WHERE user_id = ANY(@user_ids::uuid[]) AND type = 'webhook' AND enabled
ORDER BY user_id, id;

-- ListJobSharedChannels returns the enabled shared channels attached to a
-- job with their filters.
-- name: ListJobSharedChannels :many
SELECT c.id, c.type, j.groups, j.events
FROM job_shared_channels j
JOIN shared_channels c ON c.id = j.channel_id
WHERE j.job_id = $1 AND c.enabled
ORDER BY c.id;

-- name: GetJobSharedChannelFilter :one
SELECT j.groups, j.events FROM job_shared_channels j WHERE j.job_id = @job_id AND j.channel_id = @channel_id;

-- InsertInboxNotification adds an inbox row (once per event and user) and
-- returns its id (none when it already existed).
-- name: InsertInboxNotification :many
INSERT INTO notifications (user_id, event_id, type, severity, title, body, link, payload)
VALUES (@user_id, sqlc.narg(event_id), @type, @severity, @title, @body, sqlc.narg(link), @payload)
ON CONFLICT (event_id, user_id) WHERE event_id IS NOT NULL DO NOTHING
RETURNING id;

-- InsertNotificationDelivery adds a delivery; an existing idempotency key
-- makes it a no-op (no row returned).
-- name: InsertNotificationDelivery :many
INSERT INTO notification_deliveries (event_id, user_id, user_channel_id, shared_channel_id, channel, status, summary,
    last_code, last_error, idempotency_key)
VALUES (@event_id, sqlc.narg(user_id), sqlc.narg(user_channel_id), sqlc.narg(shared_channel_id), @channel, @status,
    @summary, sqlc.narg(last_code), sqlc.narg(last_error), @idempotency_key)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id;

-- CountRecentUserDeliveries counts the e-mail or webhook deliveries of a
-- user since a time that were not skipped, without the exempt (mandatory)
-- event types: the per-user flood rule.
-- name: CountRecentUserDeliveries :one
SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
WHERE d.user_id = @user_id AND d.channel = @channel AND d.created_at >= @since::timestamptz
  AND d.status <> 'skipped' AND NOT (e.type = ANY(@exempt::text[]));

-- name: GetNotificationDeliveryForUpdate :one
SELECT * FROM notification_deliveries WHERE id = $1 FOR UPDATE;

-- name: GetNotificationDelivery :one
SELECT * FROM notification_deliveries WHERE id = $1;

-- name: UpdateNotificationDelivery :exec
UPDATE notification_deliveries
SET status = @status, attempts = @attempts, last_code = sqlc.narg(last_code), last_error = sqlc.narg(last_error),
    sent_at = sqlc.narg(sent_at)
WHERE id = @id;

-- name: ListEventDeliveries :many
SELECT * FROM notification_deliveries WHERE event_id = $1 ORDER BY created_at, id;

-- name: GetUserChannel :one
SELECT * FROM user_channels WHERE id = $1;

-- name: GetUserChannelForUpdate :one
SELECT * FROM user_channels WHERE id = $1 FOR UPDATE;

-- name: GetSharedChannel :one
SELECT * FROM shared_channels WHERE id = $1;

-- name: GetSharedChannelForUpdate :one
SELECT * FROM shared_channels WHERE id = $1 FOR UPDATE;

-- RecordUserChannelResult persists the failure counters of a personal
-- webhook and, when the policy says so, disables it.
-- name: RecordUserChannelResult :exec
UPDATE user_channels
SET consecutive_failures = @consecutive_failures, permanent_failures = @permanent_failures,
    first_failure_at = sqlc.narg(first_failure_at), last_failure_at = sqlc.narg(last_failure_at),
    last_failure_code = sqlc.narg(last_failure_code),
    enabled = CASE WHEN @disable::boolean THEN false ELSE enabled END,
    disabled_at = CASE WHEN @disable::boolean THEN @at::timestamptz ELSE disabled_at END,
    disabled_reason = CASE WHEN @disable::boolean THEN sqlc.narg(disabled_reason) ELSE disabled_reason END
WHERE id = @id;

-- name: RecordSharedChannelResult :exec
UPDATE shared_channels
SET consecutive_failures = @consecutive_failures, permanent_failures = @permanent_failures,
    first_failure_at = sqlc.narg(first_failure_at), last_failure_at = sqlc.narg(last_failure_at),
    last_failure_code = sqlc.narg(last_failure_code),
    enabled = CASE WHEN @disable::boolean THEN false ELSE enabled END,
    disabled_at = CASE WHEN @disable::boolean THEN @at::timestamptz ELSE disabled_at END,
    disabled_reason = CASE WHEN @disable::boolean THEN sqlc.narg(disabled_reason) ELSE disabled_reason END
WHERE id = @id;

-- name: InsertUserChannel :one
INSERT INTO user_channels (id, user_id, type, name, display_url, url_enc, hmac_key_enc)
VALUES (@id, @user_id, 'webhook', @name, @display_url, @url_enc, @hmac_key_enc)
RETURNING *;

-- name: InsertSharedChannel :one
INSERT INTO shared_channels (id, name, type, config, url_enc, hmac_key_enc, created_by)
VALUES (@id, @name, @type, @config, sqlc.narg(url_enc), sqlc.narg(hmac_key_enc), sqlc.narg(created_by))
RETURNING *;

-- name: AttachSharedChannel :exec
INSERT INTO job_shared_channels (job_id, channel_id, groups, events, created_by)
VALUES (@job_id, @channel_id, @groups, @events, sqlc.narg(created_by))
ON CONFLICT (job_id, channel_id) DO UPDATE SET groups = EXCLUDED.groups, events = EXCLUDED.events;

-- GetNotifyJob returns what notifications show of a job (also a deleted
-- one).
-- name: GetNotifyJob :one
SELECT id, name, deleted_at FROM jobs WHERE id = $1;

-- name: GetNotifyConnectionName :one
SELECT name FROM connections WHERE id = $1;

-- name: GetNotifyDestinationName :one
SELECT name FROM destinations WHERE id = $1;

-- RetentionDeletionsByJob aggregates the artifacts retention deleted in
-- [from, until) per job: the daily retention.summary digest.
-- name: RetentionDeletionsByJob :many
SELECT job_id,
       count(DISTINCT run_id)::bigint AS runs,
       count(*)::bigint AS files,
       coalesce(sum(size), 0)::bigint AS bytes
FROM artifacts
WHERE state = 'deleted' AND deleted_at >= @from_at::timestamptz AND deleted_at < @until_at::timestamptz
GROUP BY job_id
ORDER BY job_id;

-- name: DeleteOldNotificationEvents :execrows
DELETE FROM notification_events WHERE created_at < @before::timestamptz AND fanned_out_at IS NOT NULL;

-- name: DeleteOldNotifications :execrows
DELETE FROM notifications WHERE created_at < @before::timestamptz;
