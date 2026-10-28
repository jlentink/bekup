-- Backup jobs (task 3.5, D-146): the full job definition on top of the
-- minimal scheduler table of 00014, the destinations a job writes to, its
-- backup owners, and the foreign keys from the encryption tables of 00013.
-- Managed only by internal/jobs.
--
--   name              unique (case-insensitive) among non-deleted jobs.
--   slug              unique among all jobs (also deleted ones: it names the
--                     job's directory in every destination, D-144) and
--                     immutable after creation (trigger below).
--   connection_id     the connection backed up (never temporary; checked by
--                     the service). Connections are soft deleted, so the
--                     reference stays valid; deleting a connection used by
--                     a non-deleted job is refused (internal/connections).
--   db_mode           'all': every database on the server except `exclude`;
--                     'selected': exactly `databases` (exclude is empty).
--   include_globals   PostgreSQL only: also dump the cluster globals.
--   retention_*       keep the last N and/or the last D days of backups;
--                     at least one is set (task 3.8 applies them per
--                     destination and never deletes the newest success).
--   timeout_seconds   the job's run timeout (NULL: workers.backup_timeout);
--                     never above workers.backup_timeout (the queue cap,
--                     D-145), checked by the service.
--   verification      'standard' | 'deep' completeness evidence (task 3.10).
--   expected_window_seconds  missed-backup alert window (task 5.4; NULL:
--                     derived from the schedule).
--
-- job_destinations lists the destinations of a job in fan-out order (at
-- least one, enforced by the service); job_owners the backup owners
-- (D-011, D-067: only role user gains rights from it). Shared notification
-- channels attached to jobs come with task 5.x (job_shared_channels).
--
-- bekup_app may not DELETE jobs (soft delete, 00014); it replaces
-- job_destinations rows and adds/removes job_owners rows.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.job_db_mode AS ENUM ('all', 'selected');
CREATE TYPE public.job_verification AS ENUM ('standard', 'deep');

ALTER TABLE public.jobs
    ADD COLUMN name                    text NOT NULL,
    ADD COLUMN slug                    text NOT NULL,
    ADD COLUMN description             text NOT NULL DEFAULT '',
    ADD COLUMN connection_id           uuid NOT NULL REFERENCES public.connections (id),
    ADD COLUMN db_mode                 public.job_db_mode NOT NULL DEFAULT 'all',
    ADD COLUMN databases               text[] NOT NULL DEFAULT '{}',
    ADD COLUMN exclude                 text[] NOT NULL DEFAULT '{}',
    ADD COLUMN include_globals         boolean NOT NULL DEFAULT false,
    ADD COLUMN retention_keep_last     integer,
    ADD COLUMN retention_keep_days     integer,
    ADD COLUMN timeout_seconds         integer,
    ADD COLUMN verification            public.job_verification NOT NULL DEFAULT 'standard',
    ADD COLUMN expected_window_seconds integer,
    ADD COLUMN created_by              uuid REFERENCES public.users (id) ON DELETE SET NULL,
    ADD COLUMN updated_by              uuid REFERENCES public.users (id) ON DELETE SET NULL,
    ADD CONSTRAINT jobs_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    ADD CONSTRAINT jobs_slug_format CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    ADD CONSTRAINT jobs_description_length CHECK (char_length(description) <= 1000),
    ADD CONSTRAINT jobs_databases_mode CHECK (
        (db_mode = 'all' AND cardinality(databases) = 0)
        OR (db_mode = 'selected' AND cardinality(databases) BETWEEN 1 AND 256 AND cardinality(exclude) = 0)
    ),
    ADD CONSTRAINT jobs_exclude_count CHECK (cardinality(exclude) <= 256),
    ADD CONSTRAINT jobs_retention_required CHECK (
        retention_keep_last IS NOT NULL OR retention_keep_days IS NOT NULL
    ),
    ADD CONSTRAINT jobs_retention_range CHECK (
        (retention_keep_last IS NULL OR retention_keep_last BETWEEN 1 AND 10000)
        AND (retention_keep_days IS NULL OR retention_keep_days BETWEEN 1 AND 36500)
    ),
    ADD CONSTRAINT jobs_timeout_range CHECK (timeout_seconds IS NULL OR timeout_seconds BETWEEN 60 AND 604800),
    ADD CONSTRAINT jobs_expected_window_range CHECK (
        expected_window_seconds IS NULL OR expected_window_seconds BETWEEN 60 AND 31622400
    );

CREATE UNIQUE INDEX jobs_name_active_idx ON public.jobs (lower(name)) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX jobs_slug_idx ON public.jobs (slug);
CREATE INDEX jobs_connection_idx ON public.jobs (connection_id) WHERE deleted_at IS NULL;
CREATE INDEX jobs_list_idx ON public.jobs (lower(name), id) WHERE deleted_at IS NULL;

-- The slug names the job's storage directory: it never changes.
CREATE FUNCTION public.jobs_slug_immutable() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        RAISE EXCEPTION 'jobs: the slug of job % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
        RAISE EXCEPTION 'jobs: job % is deleted', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER jobs_slug_immutable BEFORE UPDATE ON public.jobs
    FOR EACH ROW EXECUTE FUNCTION public.jobs_slug_immutable();

CREATE TABLE public.job_destinations (
    job_id         uuid NOT NULL REFERENCES public.jobs (id),
    destination_id uuid NOT NULL REFERENCES public.destinations (id),
    position       integer NOT NULL,
    PRIMARY KEY (job_id, destination_id),
    CONSTRAINT job_destinations_position_range CHECK (position BETWEEN 0 AND 15),
    CONSTRAINT job_destinations_position_unique UNIQUE (job_id, position)
);

CREATE INDEX job_destinations_destination_idx ON public.job_destinations (destination_id);

CREATE TABLE public.job_owners (
    job_id     uuid NOT NULL REFERENCES public.jobs (id),
    user_id    uuid NOT NULL REFERENCES public.users (id),
    created_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, user_id)
);

CREATE INDEX job_owners_user_idx ON public.job_owners (user_id);

ALTER TABLE public.job_encryption
    ADD CONSTRAINT job_encryption_job_fk FOREIGN KEY (job_id) REFERENCES public.jobs (id);
ALTER TABLE public.job_passphrases
    ADD CONSTRAINT job_passphrases_job_fk FOREIGN KEY (job_id) REFERENCES public.jobs (id);
ALTER TABLE public.job_recipients
    ADD CONSTRAINT job_recipients_job_fk FOREIGN KEY (job_id) REFERENCES public.jobs (id);

REVOKE ALL ON TABLE public.job_destinations, public.job_owners FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.job_destinations, public.job_owners FROM bekup_app;
REVOKE ALL ON FUNCTION public.jobs_slug_immutable() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.job_recipients DROP CONSTRAINT job_recipients_job_fk;
ALTER TABLE public.job_passphrases DROP CONSTRAINT job_passphrases_job_fk;
ALTER TABLE public.job_encryption DROP CONSTRAINT job_encryption_job_fk;
DROP TABLE public.job_owners;
DROP TABLE public.job_destinations;
DROP TRIGGER jobs_slug_immutable ON public.jobs;
DROP FUNCTION public.jobs_slug_immutable();
DROP INDEX public.jobs_list_idx;
DROP INDEX public.jobs_connection_idx;
DROP INDEX public.jobs_slug_idx;
DROP INDEX public.jobs_name_active_idx;
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_expected_window_range,
    DROP CONSTRAINT jobs_timeout_range,
    DROP CONSTRAINT jobs_retention_range,
    DROP CONSTRAINT jobs_retention_required,
    DROP CONSTRAINT jobs_exclude_count,
    DROP CONSTRAINT jobs_databases_mode,
    DROP CONSTRAINT jobs_description_length,
    DROP CONSTRAINT jobs_slug_format,
    DROP CONSTRAINT jobs_name_length,
    DROP COLUMN updated_by,
    DROP COLUMN created_by,
    DROP COLUMN expected_window_seconds,
    DROP COLUMN verification,
    DROP COLUMN timeout_seconds,
    DROP COLUMN retention_keep_days,
    DROP COLUMN retention_keep_last,
    DROP COLUMN include_globals,
    DROP COLUMN exclude,
    DROP COLUMN databases,
    DROP COLUMN db_mode,
    DROP COLUMN connection_id,
    DROP COLUMN description,
    DROP COLUMN slug,
    DROP COLUMN name;
DROP TYPE public.job_verification;
DROP TYPE public.job_db_mode;
-- +goose StatementEnd
