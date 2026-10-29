-- Notification core (task 5.3, D-183): the D-161 event outbox, the in-app
-- inbox, per-(recipient, channel) deliveries, shared channels (admin),
-- personal channels and the per-user preferences.
--
--   notification_events      the transactional outbox. Producers insert an
--                            event (identifiers, a fixed type and a small,
--                            bounded detail object; never secrets) in the
--                            transaction of the state change; the dispatcher
--                            enqueues its fan-out job (dispatched_at) and
--                            the fan-out marks it done (fanned_out_at).
--                            dedup_key makes an emit idempotent (daily
--                            digests, sign-in alerts); collapsed_into links
--                            a low-priority event (*.started) to the one it
--                            was collapsed into.
--   notifications            the in-app inbox (one row per event and user).
--   notification_deliveries  one row per (event, recipient, channel) with a
--                            unique idempotency key; status, attempts and a
--                            fixed error code / message only.
--   shared_channels          admin-managed e-mail lists and webhooks. A
--                            webhook URL (may embed a bearer credential) and
--                            its HMAC key are sealed with the keyring under
--                            shared_channels:<id>:url / :hmac_key; config
--                            holds only non-secret data (addresses, the
--                            display URL).
--   job_shared_channels      shared channels attached to a job with an
--                            event filter (groups / events; both empty = all
--                            non-security events).
--   user_channels            personal webhooks (max 5 per user; push is
--                            reserved for phase 2). The e-mail channel is
--                            implicit: the user's verified address.
--   user_notification_prefs  (user, event, optional job, channel) → enabled.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.notification_channel AS ENUM ('inbox', 'email', 'webhook', 'push');
CREATE TYPE public.notification_delivery_status AS ENUM ('queued', 'sent', 'failed', 'skipped');
CREATE TYPE public.shared_channel_type AS ENUM ('email_list', 'webhook');
CREATE TYPE public.user_channel_type AS ENUM ('webhook', 'push');

CREATE TABLE public.notification_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type            text NOT NULL,
    severity        text NOT NULL,
    job_id          uuid,
    run_id          uuid,
    restore_id      uuid,
    test_restore_id uuid,
    connection_id   uuid,
    destination_id  uuid,
    artifact_id     uuid,
    user_id         uuid REFERENCES public.users (id) ON DELETE CASCADE,
    detail          jsonb NOT NULL DEFAULT '{}'::jsonb,
    dedup_key       text,
    collapsed_into  uuid REFERENCES public.notification_events (id) ON DELETE SET NULL,
    collapsed_count integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    dispatched_at   timestamptz,
    fanned_out_at   timestamptz,
    CONSTRAINT notification_events_type_format CHECK (type ~ '^[a-z][a-z_]*\.[a-z][a-z_]*$' AND length(type) <= 64),
    CONSTRAINT notification_events_severity CHECK (severity IN ('info', 'warning', 'error', 'critical')),
    CONSTRAINT notification_events_detail_object CHECK (jsonb_typeof(detail) = 'object'),
    CONSTRAINT notification_events_detail_size CHECK (octet_length(detail::text) <= 4096),
    CONSTRAINT notification_events_dedup_length CHECK (dedup_key IS NULL OR length(dedup_key) BETWEEN 1 AND 200),
    CONSTRAINT notification_events_collapsed_count CHECK (collapsed_count >= 0)
);

CREATE UNIQUE INDEX notification_events_dedup_key ON public.notification_events (dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX notification_events_undispatched_idx ON public.notification_events (created_at) WHERE dispatched_at IS NULL;
CREATE INDEX notification_events_collapse_idx ON public.notification_events (type, job_id, created_at DESC);
CREATE INDEX notification_events_created_idx ON public.notification_events (created_at);

CREATE TABLE public.notifications (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    -- Inbox rows outlive their event (the events are pruned earlier).
    event_id   uuid REFERENCES public.notification_events (id) ON DELETE SET NULL,
    type       text NOT NULL,
    severity   text NOT NULL,
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    link       text,
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz,
    CONSTRAINT notifications_type_format CHECK (type ~ '^[a-z][a-z_]*\.[a-z][a-z_]*$' AND length(type) <= 64),
    CONSTRAINT notifications_severity CHECK (severity IN ('info', 'warning', 'error', 'critical')),
    CONSTRAINT notifications_title_length CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT notifications_body_length CHECK (char_length(body) <= 1000),
    CONSTRAINT notifications_link_length CHECK (link IS NULL OR char_length(link) <= 2048),
    CONSTRAINT notifications_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT notifications_payload_size CHECK (octet_length(payload::text) <= 8192)
);

CREATE UNIQUE INDEX notifications_event_user_key ON public.notifications (event_id, user_id) WHERE event_id IS NOT NULL;
CREATE INDEX notifications_user_idx ON public.notifications (user_id, created_at DESC);
CREATE INDEX notifications_unread_idx ON public.notifications (user_id) WHERE read_at IS NULL;
CREATE INDEX notifications_created_idx ON public.notifications (created_at);

CREATE TABLE public.shared_channels (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 text NOT NULL,
    type                 public.shared_channel_type NOT NULL,
    config               jsonb NOT NULL DEFAULT '{}'::jsonb,
    url_enc              bytea,
    hmac_key_enc         bytea,
    enabled              boolean NOT NULL DEFAULT true,
    disabled_at          timestamptz,
    disabled_reason      text,
    consecutive_failures integer NOT NULL DEFAULT 0,
    permanent_failures   integer NOT NULL DEFAULT 0,
    first_failure_at     timestamptz,
    last_failure_at      timestamptz,
    last_failure_code    text,
    created_by           uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shared_channels_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT shared_channels_config_object CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT shared_channels_config_size CHECK (octet_length(config::text) <= 16384),
    CONSTRAINT shared_channels_sealed CHECK (
        (type = 'webhook' AND url_enc IS NOT NULL AND hmac_key_enc IS NOT NULL)
        OR (type = 'email_list' AND url_enc IS NULL AND hmac_key_enc IS NULL)
    ),
    CONSTRAINT shared_channels_sealed_size CHECK (
        octet_length(url_enc) <= 8192 AND octet_length(hmac_key_enc) <= 1024
    ),
    CONSTRAINT shared_channels_counters CHECK (consecutive_failures >= 0 AND permanent_failures >= 0),
    CONSTRAINT shared_channels_disabled_reason_length CHECK (disabled_reason IS NULL OR length(disabled_reason) <= 64),
    CONSTRAINT shared_channels_failure_code_length CHECK (last_failure_code IS NULL OR length(last_failure_code) <= 64)
);

CREATE UNIQUE INDEX shared_channels_name_key ON public.shared_channels (lower(name));

CREATE TRIGGER shared_channels_set_updated_at BEFORE UPDATE ON public.shared_channels
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE public.job_shared_channels (
    job_id     uuid NOT NULL REFERENCES public.jobs (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES public.shared_channels (id) ON DELETE CASCADE,
    groups     text[] NOT NULL DEFAULT '{}'::text[],
    events     text[] NOT NULL DEFAULT '{}'::text[],
    created_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, channel_id),
    CONSTRAINT job_shared_channels_filter_size CHECK (cardinality(groups) <= 16 AND cardinality(events) <= 64)
);

CREATE INDEX job_shared_channels_channel_idx ON public.job_shared_channels (channel_id);

CREATE TABLE public.user_channels (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    type                 public.user_channel_type NOT NULL,
    name                 text NOT NULL,
    display_url          text NOT NULL,
    url_enc              bytea NOT NULL,
    hmac_key_enc         bytea NOT NULL,
    enabled              boolean NOT NULL DEFAULT true,
    disabled_at          timestamptz,
    disabled_reason      text,
    consecutive_failures integer NOT NULL DEFAULT 0,
    permanent_failures   integer NOT NULL DEFAULT 0,
    first_failure_at     timestamptz,
    last_failure_at      timestamptz,
    last_failure_code    text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    -- Push channels are reserved for phase 2.
    CONSTRAINT user_channels_webhook_only CHECK (type = 'webhook'),
    CONSTRAINT user_channels_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT user_channels_display_url_length CHECK (char_length(display_url) BETWEEN 1 AND 300),
    CONSTRAINT user_channels_sealed_size CHECK (octet_length(url_enc) <= 8192 AND octet_length(hmac_key_enc) <= 1024),
    CONSTRAINT user_channels_counters CHECK (consecutive_failures >= 0 AND permanent_failures >= 0),
    CONSTRAINT user_channels_disabled_reason_length CHECK (disabled_reason IS NULL OR length(disabled_reason) <= 64),
    CONSTRAINT user_channels_failure_code_length CHECK (last_failure_code IS NULL OR length(last_failure_code) <= 64)
);

CREATE INDEX user_channels_user_idx ON public.user_channels (user_id);

CREATE TRIGGER user_channels_set_updated_at BEFORE UPDATE ON public.user_channels
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- At most 5 personal webhooks per user (D-023), whatever code path inserts.
CREATE FUNCTION public.user_channels_limit() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('bekup.user_channels:' || NEW.user_id::text, 0));
    IF (SELECT count(*) FROM public.user_channels WHERE user_id = NEW.user_id AND type = NEW.type) >= 5 THEN
        RAISE EXCEPTION 'a user may have at most 5 personal webhooks'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'user_channels_max_per_user';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER user_channels_limit BEFORE INSERT ON public.user_channels
    FOR EACH ROW EXECUTE FUNCTION public.user_channels_limit();

CREATE TABLE public.user_notification_prefs (
    user_id    uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    event      text NOT NULL,
    job_id     uuid REFERENCES public.jobs (id) ON DELETE CASCADE,
    channel    public.notification_channel NOT NULL,
    enabled    boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_notification_prefs_event_format CHECK (event ~ '^[a-z][a-z_]*\.[a-z][a-z_]*$' AND length(event) <= 64)
);

CREATE UNIQUE INDEX user_notification_prefs_key ON public.user_notification_prefs (user_id, event, job_id, channel) NULLS NOT DISTINCT;

CREATE TABLE public.notification_deliveries (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id          uuid NOT NULL REFERENCES public.notification_events (id) ON DELETE CASCADE,
    user_id           uuid REFERENCES public.users (id) ON DELETE CASCADE,
    user_channel_id   uuid REFERENCES public.user_channels (id) ON DELETE CASCADE,
    shared_channel_id uuid REFERENCES public.shared_channels (id) ON DELETE CASCADE,
    channel           public.notification_channel NOT NULL,
    status            public.notification_delivery_status NOT NULL DEFAULT 'queued',
    summary           boolean NOT NULL DEFAULT false,
    attempts          integer NOT NULL DEFAULT 0,
    last_code         text,
    last_error        text,
    idempotency_key   text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    sent_at           timestamptz,
    CONSTRAINT notification_deliveries_recipient CHECK (
        (user_id IS NOT NULL AND shared_channel_id IS NULL)
        OR (user_id IS NULL AND user_channel_id IS NULL AND shared_channel_id IS NOT NULL)
    ),
    CONSTRAINT notification_deliveries_channel CHECK (channel IN ('email', 'webhook')),
    CONSTRAINT notification_deliveries_webhook_target CHECK (
        channel <> 'webhook' OR user_channel_id IS NOT NULL OR shared_channel_id IS NOT NULL
    ),
    CONSTRAINT notification_deliveries_attempts CHECK (attempts BETWEEN 0 AND 100),
    CONSTRAINT notification_deliveries_code_length CHECK (last_code IS NULL OR length(last_code) <= 64),
    CONSTRAINT notification_deliveries_error_length CHECK (last_error IS NULL OR length(last_error) <= 300),
    CONSTRAINT notification_deliveries_key_length CHECK (length(idempotency_key) BETWEEN 1 AND 200)
);

CREATE UNIQUE INDEX notification_deliveries_idempotency_key ON public.notification_deliveries (idempotency_key);
CREATE INDEX notification_deliveries_event_idx ON public.notification_deliveries (event_id);
CREATE INDEX notification_deliveries_flood_idx ON public.notification_deliveries (user_id, channel, created_at)
    WHERE user_id IS NOT NULL;
CREATE INDEX notification_deliveries_created_idx ON public.notification_deliveries (created_at);

CREATE TRIGGER notification_deliveries_set_updated_at BEFORE UPDATE ON public.notification_deliveries
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Least privilege for bekup_app (D-042): events and deliveries are never
-- rewritten, only their progress columns change; inbox rows only get
-- read_at; identities of channels and preferences never move to another
-- user or type.
REVOKE ALL ON TABLE public.notification_events, public.notifications, public.notification_deliveries,
    public.shared_channels, public.job_shared_channels, public.user_channels,
    public.user_notification_prefs FROM PUBLIC;
REVOKE TRUNCATE, REFERENCES, TRIGGER ON TABLE public.notification_events, public.notifications,
    public.notification_deliveries, public.shared_channels, public.job_shared_channels,
    public.user_channels, public.user_notification_prefs FROM bekup_app;

REVOKE UPDATE ON TABLE public.notification_events FROM bekup_app;
GRANT UPDATE (dispatched_at, fanned_out_at, collapsed_into, collapsed_count) ON TABLE public.notification_events TO bekup_app;

REVOKE UPDATE ON TABLE public.notifications FROM bekup_app;
GRANT UPDATE (read_at) ON TABLE public.notifications TO bekup_app;

REVOKE UPDATE ON TABLE public.notification_deliveries FROM bekup_app;
GRANT UPDATE (status, summary, attempts, last_code, last_error, sent_at, updated_at) ON TABLE public.notification_deliveries TO bekup_app;

REVOKE UPDATE ON TABLE public.shared_channels FROM bekup_app;
GRANT UPDATE (name, config, url_enc, hmac_key_enc, enabled, disabled_at, disabled_reason, consecutive_failures,
    permanent_failures, first_failure_at, last_failure_at, last_failure_code, updated_at)
    ON TABLE public.shared_channels TO bekup_app;

REVOKE UPDATE ON TABLE public.job_shared_channels FROM bekup_app;
GRANT UPDATE (groups, events) ON TABLE public.job_shared_channels TO bekup_app;

REVOKE UPDATE ON TABLE public.user_channels FROM bekup_app;
GRANT UPDATE (name, display_url, url_enc, hmac_key_enc, enabled, disabled_at, disabled_reason, consecutive_failures,
    permanent_failures, first_failure_at, last_failure_at, last_failure_code, updated_at)
    ON TABLE public.user_channels TO bekup_app;

REVOKE UPDATE ON TABLE public.user_notification_prefs FROM bekup_app;
GRANT UPDATE (enabled, updated_at) ON TABLE public.user_notification_prefs TO bekup_app;

REVOKE ALL ON FUNCTION public.user_channels_limit() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.notification_deliveries;
DROP TABLE public.user_notification_prefs;
DROP TABLE public.user_channels;
DROP FUNCTION public.user_channels_limit();
DROP TABLE public.job_shared_channels;
DROP TABLE public.shared_channels;
DROP TABLE public.notifications;
DROP TABLE public.notification_events;
DROP TYPE public.user_channel_type;
DROP TYPE public.shared_channel_type;
DROP TYPE public.notification_delivery_status;
DROP TYPE public.notification_channel;
-- +goose StatementEnd
