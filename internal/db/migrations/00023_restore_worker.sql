-- The restore worker (task 4.2): restore logs, the pg_restore --no-owner
-- option and the outcome columns of restores. Written only through
-- internal/restore.
--
--   restores.no_owner      PostgreSQL database restores: pg_restore
--                          --no-owner (default on; part of the restore's
--                          identity, fixed after insert).
--   restores.log_lines / log_lines_dropped
--                          the stored and the dropped (cap, queue, failed
--                          insert) log lines of the restore.
--   restores.warnings      stable warning codes of the outcome (at most 32).
--   restore_logs           worker events and runner log lines of a restore,
--                          sanitized, redacted and capped like run_logs
--                          (workers.run_log_max_lines / run_log_max_line_bytes).
--
-- bekup_app may insert and read restore_logs but never update or delete
-- them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.restores
    ADD COLUMN no_owner          boolean NOT NULL DEFAULT true,
    ADD COLUMN log_lines         integer NOT NULL DEFAULT 0,
    ADD COLUMN log_lines_dropped integer NOT NULL DEFAULT 0,
    ADD COLUMN warnings          text[]  NOT NULL DEFAULT '{}',
    ADD CONSTRAINT restores_log_counts CHECK (log_lines >= 0 AND log_lines_dropped >= 0),
    ADD CONSTRAINT restores_warnings_count CHECK (cardinality(warnings) <= 32);

CREATE OR REPLACE FUNCTION public.restores_guard() RETURNS trigger
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
       OR NEW.requested_by IS DISTINCT FROM OLD.requested_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.no_owner IS DISTINCT FROM OLD.no_owner THEN
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
REVOKE ALL ON FUNCTION public.restores_guard() FROM PUBLIC, bekup_app;

GRANT UPDATE (log_lines, log_lines_dropped, warnings) ON TABLE public.restores TO bekup_app;

CREATE TABLE public.restore_logs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    restore_id uuid NOT NULL REFERENCES public.restores (id),
    ts         timestamptz NOT NULL,
    level      public.run_log_level NOT NULL,
    stream     public.run_log_stream NOT NULL,
    line       text NOT NULL,
    CONSTRAINT restore_logs_line_length CHECK (octet_length(line) <= 16384)
);

CREATE INDEX restore_logs_restore_idx ON public.restore_logs (restore_id, id);

REVOKE ALL ON TABLE public.restore_logs FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.restore_logs FROM bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.restore_logs;
REVOKE UPDATE (log_lines, log_lines_dropped, warnings) ON TABLE public.restores FROM bekup_app;
CREATE OR REPLACE FUNCTION public.restores_guard() RETURNS trigger
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
ALTER TABLE public.restores
    DROP CONSTRAINT restores_warnings_count,
    DROP CONSTRAINT restores_log_counts,
    DROP COLUMN warnings,
    DROP COLUMN log_lines_dropped,
    DROP COLUMN log_lines,
    DROP COLUMN no_owner;
-- +goose StatementEnd
