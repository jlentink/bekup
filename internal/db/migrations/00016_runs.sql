-- Backup runs (task 3.6, D-147): the run history, the per-database items,
-- the stored objects (artifacts), the bounded run logs and the DB-backed
-- concurrency slots. Written only through internal/runstate.
--
--   runs        one row per backup attempt. Created `queued` together with
--               its River job (scheduler, runJobNow), `running` once the
--               worker holds a concurrency slot, then exactly one terminal
--               status: success | partial | failed | canceled | skipped.
--               river_job_id links the River job (no foreign key: River
--               deletes finished rows); a retry or a rescued attempt gets a
--               new run row. storage_timestamp is the run's directory
--               timestamp ({slug}/{timestamp}/..., UTC seconds; D-144).
--               error_code / error_message are fixed, sanitized texts
--               (never server output, DSNs or secrets).
--   run_items   one row per database (or the PostgreSQL globals) of a run;
--               verified = the native verification passed (pg_restore of
--               the archive, the dump tool's completion trailer).
--   artifacts   one row per object finalized on a destination: the dumps
--               (run_item_id set) and the signed manifest.json (kind
--               manifest). Rows are never deleted and their identity never
--               changes; retention (3.8) moves `present` to `deleted`, a
--               verification may record `missing`. signing_key_id names the
--               instance key of the manifest written next to the object on
--               that destination (NULL: no manifest there). verified = false
--               marks the objects of an item whose verification failed (kept,
--               never listed in a manifest).
--   run_logs    worker events and runner log lines, redacted and capped per
--               run (workers.run_log_max_lines / run_log_max_line_bytes).
--   run_slots   the global / per-connection run semaphore: a row per running
--               backup or restore, refreshed by heartbeat; rows whose
--               heartbeat is older than the lease are ignored and replaced.
--
-- bekup_app may not DELETE runs, run_items, artifacts or run_logs, may only
-- change the state, deletion and signing columns of artifacts, and may not
-- UPDATE run_logs.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.run_trigger AS ENUM ('schedule', 'catch_up', 'manual', 'early');
CREATE TYPE public.run_status AS ENUM ('queued', 'running', 'success', 'partial', 'failed', 'canceled', 'skipped');
CREATE TYPE public.run_item_kind AS ENUM ('database', 'globals');
CREATE TYPE public.run_item_status AS ENUM ('running', 'success', 'partial', 'failed', 'canceled');
CREATE TYPE public.artifact_kind AS ENUM ('database', 'globals', 'manifest');
CREATE TYPE public.artifact_state AS ENUM ('present', 'deleted', 'missing');
CREATE TYPE public.run_log_level AS ENUM ('info', 'warn', 'error');
CREATE TYPE public.run_log_stream AS ENUM ('worker', 'runner', 'stdout', 'stderr');

CREATE TABLE public.runs (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id              uuid NOT NULL REFERENCES public.jobs (id),
    trigger             public.run_trigger NOT NULL,
    triggered_by        uuid REFERENCES public.users (id) ON DELETE SET NULL,
    scheduled_for       timestamptz,
    status              public.run_status NOT NULL DEFAULT 'queued',
    river_job_id        bigint,
    attempt             integer NOT NULL DEFAULT 1,
    queued_at           timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    finished_at         timestamptz,
    storage_timestamp   timestamptz,
    error_code          text,
    error_message       text,
    warnings            jsonb NOT NULL DEFAULT '[]'::jsonb,
    bytes_raw           bigint NOT NULL DEFAULT 0,
    bytes_stored        bigint NOT NULL DEFAULT 0,
    cancel_requested_at timestamptz,
    cancel_requested_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    log_lines           integer NOT NULL DEFAULT 0,
    log_lines_dropped   integer NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT runs_attempt_range CHECK (attempt BETWEEN 1 AND 1000),
    CONSTRAINT runs_error_code_format CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT runs_error_message_length CHECK (error_message IS NULL OR char_length(error_message) <= 1000),
    CONSTRAINT runs_warnings_array CHECK (jsonb_typeof(warnings) = 'array' AND octet_length(warnings::text) <= 262144),
    CONSTRAINT runs_bytes_positive CHECK (bytes_raw >= 0 AND bytes_stored >= 0),
    CONSTRAINT runs_log_counts CHECK (log_lines >= 0 AND log_lines_dropped >= 0),
    CONSTRAINT runs_finished CHECK (
        (status IN ('queued', 'running')) = (finished_at IS NULL)
    ),
    CONSTRAINT runs_storage_timestamp_seconds CHECK (
        storage_timestamp IS NULL OR storage_timestamp = date_trunc('second', storage_timestamp)
    )
);

CREATE TRIGGER runs_set_updated_at BEFORE UPDATE ON public.runs
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE INDEX runs_job_idx ON public.runs (job_id, queued_at DESC, id DESC);
CREATE INDEX runs_river_job_idx ON public.runs (river_job_id, created_at DESC) WHERE river_job_id IS NOT NULL;
CREATE INDEX runs_active_idx ON public.runs (status) WHERE status IN ('queued', 'running');
CREATE INDEX runs_queued_at_idx ON public.runs (queued_at DESC, id DESC);

-- A terminal run never changes status again.
CREATE FUNCTION public.runs_terminal_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status NOT IN ('queued', 'running') AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'runs: run % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.trigger IS DISTINCT FROM OLD.trigger
       OR NEW.river_job_id IS DISTINCT FROM OLD.river_job_id THEN
        RAISE EXCEPTION 'runs: the identity of run % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runs_terminal_guard BEFORE UPDATE ON public.runs
    FOR EACH ROW EXECUTE FUNCTION public.runs_terminal_guard();

CREATE TABLE public.run_items (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id        uuid NOT NULL REFERENCES public.runs (id),
    kind          public.run_item_kind NOT NULL,
    db_name       text,
    position      integer NOT NULL,
    status        public.run_item_status NOT NULL DEFAULT 'running',
    raw_size      bigint NOT NULL DEFAULT 0,
    stored_size   bigint NOT NULL DEFAULT 0,
    raw_sha256    bytea,
    sha256        bytea,
    verified      boolean NOT NULL DEFAULT false,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    error_code    text,
    error_message text,
    warnings      jsonb NOT NULL DEFAULT '[]'::jsonb,
    CONSTRAINT run_items_db_name CHECK (
        (kind = 'globals' AND db_name IS NULL)
        OR (kind = 'database' AND db_name IS NOT NULL AND octet_length(db_name) BETWEEN 1 AND 255)
    ),
    CONSTRAINT run_items_position_range CHECK (position BETWEEN 0 AND 100000),
    CONSTRAINT run_items_sizes CHECK (raw_size >= 0 AND stored_size >= 0),
    CONSTRAINT run_items_hashes CHECK (
        (raw_sha256 IS NULL OR octet_length(raw_sha256) = 32) AND (sha256 IS NULL OR octet_length(sha256) = 32)
    ),
    CONSTRAINT run_items_error_code_format CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT run_items_error_message_length CHECK (error_message IS NULL OR char_length(error_message) <= 1000),
    CONSTRAINT run_items_warnings_array CHECK (jsonb_typeof(warnings) = 'array' AND octet_length(warnings::text) <= 65536),
    CONSTRAINT run_items_finished CHECK ((status = 'running') = (finished_at IS NULL))
);

CREATE UNIQUE INDEX run_items_unique_idx ON public.run_items (run_id, kind, coalesce(db_name, ''));
CREATE INDEX run_items_run_idx ON public.run_items (run_id, position);

CREATE TABLE public.artifacts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id            uuid NOT NULL REFERENCES public.runs (id),
    run_item_id       uuid REFERENCES public.run_items (id),
    job_id            uuid NOT NULL REFERENCES public.jobs (id),
    destination_id    uuid NOT NULL REFERENCES public.destinations (id),
    kind              public.artifact_kind NOT NULL,
    object_key        text NOT NULL,
    size              bigint NOT NULL,
    sha256            bytea NOT NULL,
    version_id        text,
    encryption_mode   text,
    passphrase_version integer,
    recipient_version integer,
    signing_key_id    text,
    verified          boolean NOT NULL DEFAULT false,
    lock_until        timestamptz,
    state             public.artifact_state NOT NULL DEFAULT 'present',
    state_changed_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    CONSTRAINT artifacts_item_kind CHECK ((kind = 'manifest') = (run_item_id IS NULL)),
    CONSTRAINT artifacts_key_length CHECK (octet_length(object_key) BETWEEN 1 AND 1024),
    CONSTRAINT artifacts_size_positive CHECK (size >= 0),
    CONSTRAINT artifacts_sha256_length CHECK (octet_length(sha256) = 32),
    CONSTRAINT artifacts_version_id_length CHECK (version_id IS NULL OR octet_length(version_id) <= 1024),
    CONSTRAINT artifacts_encryption CHECK (
        (kind = 'manifest' AND encryption_mode IS NULL AND passphrase_version IS NULL AND recipient_version IS NULL)
        OR (kind <> 'manifest' AND (
            (encryption_mode = 'passphrase' AND passphrase_version >= 1 AND recipient_version IS NULL)
            OR (encryption_mode = 'recipients' AND recipient_version >= 1 AND passphrase_version IS NULL)
            OR (encryption_mode = 'none' AND passphrase_version IS NULL AND recipient_version IS NULL)
        ))
    ),
    CONSTRAINT artifacts_signing_key_format CHECK (signing_key_id IS NULL OR signing_key_id ~ '^[0-9a-f]{16}$'),
    CONSTRAINT artifacts_deleted CHECK ((state = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE UNIQUE INDEX artifacts_destination_key_idx ON public.artifacts (destination_id, object_key);
CREATE INDEX artifacts_run_idx ON public.artifacts (run_id);
CREATE INDEX artifacts_item_idx ON public.artifacts (run_item_id) WHERE run_item_id IS NOT NULL;
CREATE INDEX artifacts_job_destination_idx ON public.artifacts (job_id, destination_id, created_at DESC) WHERE state = 'present';

-- Artifacts keep their identity forever: only the state (deleted is
-- final), the deletion time and the manifest signing key (set once) change.
CREATE FUNCTION public.artifacts_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.state = 'deleted' AND NEW.state IS DISTINCT FROM OLD.state THEN
        RAISE EXCEPTION 'artifacts: artifact % is deleted', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.signing_key_id IS NOT NULL AND NEW.signing_key_id IS DISTINCT FROM OLD.signing_key_id THEN
        RAISE EXCEPTION 'artifacts: the signing key of artifact % is already set', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER artifacts_guard BEFORE UPDATE ON public.artifacts
    FOR EACH ROW EXECUTE FUNCTION public.artifacts_guard();

CREATE TABLE public.run_logs (
    id     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES public.runs (id),
    ts     timestamptz NOT NULL,
    level  public.run_log_level NOT NULL,
    stream public.run_log_stream NOT NULL,
    line   text NOT NULL,
    CONSTRAINT run_logs_line_length CHECK (octet_length(line) <= 16384)
);

CREATE INDEX run_logs_run_idx ON public.run_logs (run_id, id);

CREATE TABLE public.run_slots (
    holder_id     uuid PRIMARY KEY,
    kind          text NOT NULL,
    connection_id uuid NOT NULL,
    acquired_at   timestamptz NOT NULL DEFAULT now(),
    heartbeat_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_slots_kind CHECK (kind IN ('backup', 'restore'))
);

CREATE INDEX run_slots_connection_idx ON public.run_slots (connection_id);

REVOKE ALL ON TABLE public.runs, public.run_items, public.artifacts, public.run_logs, public.run_slots FROM PUBLIC;
REVOKE DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.runs, public.run_items FROM bekup_app;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.artifacts, public.run_logs FROM bekup_app;
GRANT UPDATE (state, state_changed_at, deleted_at, signing_key_id) ON TABLE public.artifacts TO bekup_app;
REVOKE TRUNCATE, REFERENCES, TRIGGER ON TABLE public.run_slots FROM bekup_app;
REVOKE ALL ON FUNCTION public.runs_terminal_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.artifacts_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.run_slots;
DROP TABLE public.run_logs;
DROP TRIGGER artifacts_guard ON public.artifacts;
DROP FUNCTION public.artifacts_guard();
DROP TABLE public.artifacts;
DROP TABLE public.run_items;
DROP TRIGGER runs_terminal_guard ON public.runs;
DROP FUNCTION public.runs_terminal_guard();
DROP TABLE public.runs;
DROP TYPE public.run_log_stream;
DROP TYPE public.run_log_level;
DROP TYPE public.artifact_state;
DROP TYPE public.artifact_kind;
DROP TYPE public.run_item_status;
DROP TYPE public.run_item_kind;
DROP TYPE public.run_status;
DROP TYPE public.run_trigger;
-- +goose StatementEnd
