-- Scheduled test restores (task 4.6): a job's test-restore settings, and
-- the results of every test restore with one row per restored database.
-- Written only through internal/jobs (settings) and internal/restore
-- (test restores).
--
--   jobs.test_restore_enabled        scheduled test restores are on.
--   jobs.test_restore_schedule       canonical internal/scheduler.Schedule
--                                    JSON (default: weekly, Sunday 04:00
--                                    UTC); {"kind":"manual"} = only
--                                    runTestRestoreNow.
--   jobs.test_restore_connection_id  the scratch target: a permanent
--                                    connection with the job's engine on
--                                    which bekup creates (and always drops)
--                                    bekup_test_<id> databases. Required
--                                    while enabled. It blocks the
--                                    connection's deletion like a job's own
--                                    connection.
--   jobs.test_restore_next_at        the next scheduled test restore (NULL:
--                                    not scheduled); advanced by the
--                                    test-restore scheduler.
--
--   test_restores       one test restore of a job: the run tested (the
--                       newest successful run with verified, signed
--                       database artifacts, picked when it starts), the
--                       scratch target, the trigger, the status (queued ->
--                       running -> passed | warnings | failed | error |
--                       canceled), a sanitized error code and the summary
--                       counts. At most one queued or running test restore
--                       per job (partial unique index).
--   test_restore_items  one restored database of a test restore: the run
--                       item, the internal restore (restores row), the
--                       scratch id (the runner derives bekup_test_<id>),
--                       the verdict, whether the scratch database was
--                       dropped, and the bounded comparison diff (names
--                       and counts only, never data values).
--
-- bekup_app may insert and read both tables, update only the progress
-- columns and never delete; a finished test restore or item never changes
-- status again and the identity columns are fixed (triggers).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.jobs
    ADD COLUMN test_restore_enabled       boolean NOT NULL DEFAULT false,
    ADD COLUMN test_restore_schedule      jsonb NOT NULL DEFAULT '{"kind":"cron","cron":"0 4 * * 0","timezone":"UTC"}'::jsonb,
    ADD COLUMN test_restore_connection_id uuid REFERENCES public.connections (id),
    ADD COLUMN test_restore_next_at       timestamptz,
    ADD CONSTRAINT jobs_test_restore_schedule_object CHECK (
        jsonb_typeof(test_restore_schedule) = 'object' AND octet_length(test_restore_schedule::text) <= 1024
    ),
    ADD CONSTRAINT jobs_test_restore_target CHECK (NOT test_restore_enabled OR test_restore_connection_id IS NOT NULL);

-- The test-restore scheduler's due scan.
CREATE INDEX jobs_test_restore_due_idx ON public.jobs (test_restore_next_at)
    WHERE test_restore_enabled AND deleted_at IS NULL AND test_restore_next_at IS NOT NULL;
CREATE INDEX jobs_test_restore_connection_idx ON public.jobs (test_restore_connection_id)
    WHERE test_restore_connection_id IS NOT NULL;

CREATE TYPE public.test_restore_status AS ENUM (
    'queued', 'running', 'passed', 'warnings', 'failed', 'error', 'canceled'
);
CREATE TYPE public.test_restore_trigger AS ENUM ('schedule', 'manual');

CREATE TABLE public.test_restores (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id               uuid NOT NULL REFERENCES public.jobs (id),
    target_connection_id uuid NOT NULL REFERENCES public.connections (id),
    trigger              public.test_restore_trigger NOT NULL,
    requested_by         uuid REFERENCES public.users (id) ON DELETE SET NULL,
    scheduled_for        timestamptz,
    run_id               uuid REFERENCES public.runs (id),
    status               public.test_restore_status NOT NULL DEFAULT 'queued',
    river_job_id         bigint,
    started_at           timestamptz,
    finished_at          timestamptz,
    error_code           text,
    error_message        text,
    databases            integer NOT NULL DEFAULT 0,
    passed               integer NOT NULL DEFAULT 0,
    warned               integer NOT NULL DEFAULT 0,
    failed               integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT test_restores_error_code_format CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT test_restores_error_message_length CHECK (error_message IS NULL OR char_length(error_message) <= 1000),
    CONSTRAINT test_restores_counts CHECK (databases >= 0 AND passed >= 0 AND warned >= 0 AND failed >= 0),
    CONSTRAINT test_restores_finished CHECK ((status IN ('queued', 'running')) = (finished_at IS NULL))
);

CREATE TRIGGER test_restores_set_updated_at BEFORE UPDATE ON public.test_restores
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE INDEX test_restores_job_idx ON public.test_restores (job_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX test_restores_active_idx ON public.test_restores (job_id)
    WHERE status IN ('queued', 'running');

CREATE TABLE public.test_restore_items (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    test_restore_id uuid NOT NULL REFERENCES public.test_restores (id),
    run_item_id     uuid NOT NULL REFERENCES public.run_items (id),
    restore_id      uuid REFERENCES public.restores (id),
    db_name         text NOT NULL,
    scratch_id      text NOT NULL,
    status          public.test_restore_status NOT NULL DEFAULT 'running',
    scratch_dropped boolean NOT NULL DEFAULT false,
    error_code      text,
    diff            jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    CONSTRAINT test_restore_items_db_name CHECK (octet_length(db_name) BETWEEN 1 AND 255),
    CONSTRAINT test_restore_items_scratch_id CHECK (scratch_id ~ '^[a-z0-9]{12}$'),
    CONSTRAINT test_restore_items_status CHECK (status <> 'queued'),
    CONSTRAINT test_restore_items_error_code_format CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT test_restore_items_diff CHECK (jsonb_typeof(diff) = 'object' AND octet_length(diff::text) <= 262144),
    CONSTRAINT test_restore_items_finished CHECK ((status = 'running') = (finished_at IS NULL))
);

CREATE UNIQUE INDEX test_restore_items_unique_idx ON public.test_restore_items (test_restore_id, run_item_id);
CREATE UNIQUE INDEX test_restore_items_scratch_idx ON public.test_restore_items (scratch_id);

-- A finished test restore (or item) never changes status again; what was
-- tested where never changes; the River job id is set once.
CREATE FUNCTION public.test_restores_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status NOT IN ('queued', 'running') AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'test_restores: test restore % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.target_connection_id IS DISTINCT FROM OLD.target_connection_id
       OR NEW.trigger IS DISTINCT FROM OLD.trigger OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.scheduled_for IS DISTINCT FROM OLD.scheduled_for
       OR (OLD.run_id IS NOT NULL AND NEW.run_id IS DISTINCT FROM OLD.run_id) THEN
        RAISE EXCEPTION 'test_restores: the identity of test restore % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.river_job_id IS NOT NULL AND NEW.river_job_id IS DISTINCT FROM OLD.river_job_id THEN
        RAISE EXCEPTION 'test_restores: the River job of test restore % is already set', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER test_restores_guard BEFORE UPDATE ON public.test_restores
    FOR EACH ROW EXECUTE FUNCTION public.test_restores_guard();

CREATE FUNCTION public.test_restore_items_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status <> 'running' AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'test_restore_items: item % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.test_restore_id IS DISTINCT FROM OLD.test_restore_id OR NEW.run_item_id IS DISTINCT FROM OLD.run_item_id
       OR NEW.db_name IS DISTINCT FROM OLD.db_name OR NEW.scratch_id IS DISTINCT FROM OLD.scratch_id
       OR NEW.started_at IS DISTINCT FROM OLD.started_at
       OR (OLD.restore_id IS NOT NULL AND NEW.restore_id IS DISTINCT FROM OLD.restore_id) THEN
        RAISE EXCEPTION 'test_restore_items: the identity of item % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER test_restore_items_guard BEFORE UPDATE ON public.test_restore_items
    FOR EACH ROW EXECUTE FUNCTION public.test_restore_items_guard();

REVOKE ALL ON TABLE public.test_restores, public.test_restore_items FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.test_restores, public.test_restore_items FROM bekup_app;
GRANT UPDATE (run_id, status, river_job_id, started_at, finished_at, error_code, error_message,
              databases, passed, warned, failed, updated_at)
    ON TABLE public.test_restores TO bekup_app;
GRANT UPDATE (restore_id, status, scratch_dropped, error_code, diff, finished_at)
    ON TABLE public.test_restore_items TO bekup_app;
REVOKE ALL ON FUNCTION public.test_restores_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.test_restore_items_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.test_restore_items;
DROP FUNCTION public.test_restore_items_guard();
DROP TABLE public.test_restores;
DROP FUNCTION public.test_restores_guard();
DROP TYPE public.test_restore_trigger;
DROP TYPE public.test_restore_status;
DROP INDEX public.jobs_test_restore_connection_idx;
DROP INDEX public.jobs_test_restore_due_idx;
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_test_restore_target,
    DROP CONSTRAINT jobs_test_restore_schedule_object,
    DROP COLUMN test_restore_next_at,
    DROP COLUMN test_restore_connection_id,
    DROP COLUMN test_restore_schedule,
    DROP COLUMN test_restore_enabled;
-- +goose StatementEnd
