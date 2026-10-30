-- Drop the reserved push channel (D-206): push existed only for the native
-- app clients, which left the roadmap. PostgreSQL cannot remove an enum
-- value, so both enums are recreated without 'push':
--
--   notification_channel  inbox | email | webhook
--                         (user_notification_prefs.channel,
--                         notification_deliveries.channel)
--   user_channel_type     webhook (user_channels.type)
--
-- Push was never delivered: notification_deliveries and user_channels
-- already refused it by CHECK, and the API never accepted it for
-- preferences, so the DELETE below is a safety net that matches no rows in
-- practice. The CHECK constraints that compare with an enum literal are
-- dropped and re-added around the type change (their stored expressions
-- name the old type). Indexes on the columns are rebuilt by ALTER COLUMN
-- TYPE; column privileges are kept.

-- +goose Up
-- +goose StatementBegin
DELETE FROM public.user_notification_prefs WHERE channel = 'push';

ALTER TABLE public.notification_deliveries
    DROP CONSTRAINT notification_deliveries_channel,
    DROP CONSTRAINT notification_deliveries_webhook_target;
ALTER TABLE public.user_channels DROP CONSTRAINT user_channels_webhook_only;

ALTER TYPE public.notification_channel RENAME TO notification_channel_old;
CREATE TYPE public.notification_channel AS ENUM ('inbox', 'email', 'webhook');
ALTER TABLE public.user_notification_prefs
    ALTER COLUMN channel TYPE public.notification_channel USING channel::text::public.notification_channel;
ALTER TABLE public.notification_deliveries
    ALTER COLUMN channel TYPE public.notification_channel USING channel::text::public.notification_channel;
DROP TYPE public.notification_channel_old;

ALTER TYPE public.user_channel_type RENAME TO user_channel_type_old;
CREATE TYPE public.user_channel_type AS ENUM ('webhook');
ALTER TABLE public.user_channels
    ALTER COLUMN type TYPE public.user_channel_type USING type::text::public.user_channel_type;
DROP TYPE public.user_channel_type_old;

ALTER TABLE public.notification_deliveries
    ADD CONSTRAINT notification_deliveries_channel CHECK (channel IN ('email', 'webhook')),
    ADD CONSTRAINT notification_deliveries_webhook_target CHECK (
        channel <> 'webhook' OR user_channel_id IS NOT NULL OR shared_channel_id IS NOT NULL
    );
ALTER TABLE public.user_channels ADD CONSTRAINT user_channels_webhook_only CHECK (type = 'webhook');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Appending restores the 00028 definitions exactly ('push' was the last
-- value of both enums); the new value is not used in this transaction.
ALTER TYPE public.notification_channel ADD VALUE 'push';
ALTER TYPE public.user_channel_type ADD VALUE 'push';
-- +goose StatementEnd
