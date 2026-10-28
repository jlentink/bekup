-- Restores (task 4.1, D-162): one row per requested restore of an artifact,
-- and the encrypted handoff of a user-supplied age identity to the restore
-- worker. Written only through internal/restore.
--
--   restores    created `queued` by restoreArtifact (task 4.5) together
--               with its River job (IDs only, D-145); the worker moves it
--               queued -> staging (download to the scratch volume) ->
--               verifying (hash, signed manifest, DB cross-check) ->
--               restoring (runner Restore, tasks 4.2/4.3) and then to
--               exactly one terminal status: succeeded | failed | canceled.
--               target_db_name is NULL only for a globals restore (admin
--               only, task 4.2). error_code / error_message are fixed,
--               sanitized texts (never server output, DSNs or secrets).
--               staged_bytes is the size of the verified local copy.
--   restore_identity_handoff
--               restores of recipient-encrypted artifacts: the age identity
--               sent with restoreArtifact, sealed with the keyring under AAD
--               restore_identity_handoff:<restore_id>:identity. Inserted
--               only while the restore is queued, lives at most 15 minutes,
--               and is deleted when the worker takes it (DELETE ...
--               RETURNING: read once) or by housekeeping after expiry. It
--               never goes into River job args.
--
-- bekup_app may insert and read restores but never delete them, and may only
-- update the progress columns; a terminal restore never changes again and
-- the identity columns are fixed (trigger). On restore_identity_handoff it
-- may only insert, read and delete (no UPDATE: a sealed identity is never
-- rewritten or moved to another restore).

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.restore_mode AS ENUM ('new_db', 'overwrite');
CREATE TYPE public.restore_status AS ENUM (
    'queued', 'staging', 'verifying', 'restoring', 'succeeded', 'failed', 'canceled'
);

CREATE TABLE public.restores (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id          uuid NOT NULL REFERENCES public.artifacts (id),
    run_item_id          uuid NOT NULL REFERENCES public.run_items (id),
    job_id               uuid NOT NULL REFERENCES public.jobs (id),
    target_connection_id uuid NOT NULL REFERENCES public.connections (id),
    target_db_name       text,
    mode                 public.restore_mode NOT NULL,
    requested_by         uuid REFERENCES public.users (id),
    status               public.restore_status NOT NULL DEFAULT 'queued',
    river_job_id         bigint,
    staged_bytes         bigint NOT NULL DEFAULT 0,
    started_at           timestamptz,
    finished_at          timestamptz,
    error_code           text,
    error_message        text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT restores_target_db_name CHECK (
        target_db_name IS NULL OR octet_length(target_db_name) BETWEEN 1 AND 255
    ),
    CONSTRAINT restores_staged_bytes CHECK (staged_bytes >= 0),
    CONSTRAINT restores_error_code_format CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT restores_error_message_length CHECK (error_message IS NULL OR char_length(error_message) <= 1000),
    CONSTRAINT restores_finished CHECK (
        (status IN ('queued', 'staging', 'verifying', 'restoring')) = (finished_at IS NULL)
    ),
    CONSTRAINT restores_started CHECK (status = 'queued' OR status IN ('failed', 'canceled') OR started_at IS NOT NULL)
);

CREATE TRIGGER restores_set_updated_at BEFORE UPDATE ON public.restores
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE INDEX restores_job_idx ON public.restores (job_id, created_at DESC, id DESC);
CREATE INDEX restores_artifact_idx ON public.restores (artifact_id);
CREATE INDEX restores_target_idx ON public.restores (target_connection_id);
CREATE INDEX restores_active_idx ON public.restores (status)
    WHERE status IN ('queued', 'staging', 'verifying', 'restoring');

-- A terminal restore never changes status again; the restore's identity
-- (what is restored where, by whom) never changes; the River job id is set
-- once.
CREATE FUNCTION public.restores_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'canceled') AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'restores: restore % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.artifact_id IS DISTINCT FROM OLD.artifact_id OR NEW.run_item_id IS DISTINCT FROM OLD.run_item_id
       OR NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.target_connection_id IS DISTINCT FROM OLD.target_connection_id
       OR NEW.target_db_name IS DISTINCT FROM OLD.target_db_name OR NEW.mode IS DISTINCT FROM OLD.mode
       OR NEW.requested_by IS DISTINCT FROM OLD.requested_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'restores: the identity of restore % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.river_job_id IS NOT NULL AND NEW.river_job_id IS DISTINCT FROM OLD.river_job_id THEN
        RAISE EXCEPTION 'restores: the River job of restore % is already set', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER restores_guard BEFORE UPDATE ON public.restores
    FOR EACH ROW EXECUTE FUNCTION public.restores_guard();

CREATE TABLE public.restore_identity_handoff (
    restore_id      uuid PRIMARY KEY REFERENCES public.restores (id),
    sealed_identity bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    CONSTRAINT restore_identity_handoff_length CHECK (octet_length(sealed_identity) BETWEEN 1 AND 16384),
    CONSTRAINT restore_identity_handoff_expiry CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '15 minutes'
    )
);

CREATE INDEX restore_identity_handoff_expires_idx ON public.restore_identity_handoff (expires_at);

-- The creation time is the server's, and a handoff can only be added to a
-- queued restore (never to one that already runs or finished).
CREATE FUNCTION public.restore_identity_handoff_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    NEW.created_at := now();
    IF NOT EXISTS (SELECT 1 FROM public.restores r WHERE r.id = NEW.restore_id AND r.status = 'queued') THEN
        RAISE EXCEPTION 'restore_identity_handoff: restore % is not queued', NEW.restore_id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER restore_identity_handoff_guard BEFORE INSERT ON public.restore_identity_handoff
    FOR EACH ROW EXECUTE FUNCTION public.restore_identity_handoff_guard();

REVOKE ALL ON TABLE public.restores FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.restores FROM bekup_app;
GRANT UPDATE (status, river_job_id, staged_bytes, started_at, finished_at, error_code, error_message, updated_at)
    ON TABLE public.restores TO bekup_app;
REVOKE ALL ON FUNCTION public.restores_guard() FROM PUBLIC, bekup_app;

REVOKE ALL ON TABLE public.restore_identity_handoff FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.restore_identity_handoff FROM bekup_app;
REVOKE ALL ON FUNCTION public.restore_identity_handoff_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.restore_identity_handoff;
DROP FUNCTION public.restore_identity_handoff_guard();
DROP TABLE public.restores;
DROP FUNCTION public.restores_guard();
DROP TYPE public.restore_status;
DROP TYPE public.restore_mode;
-- +goose StatementEnd
