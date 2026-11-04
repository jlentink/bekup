-- External API (D-220): the run trigger `api` (runs requested with an API
-- token) and the audit actor kind `api` (entries written on behalf of a
-- token). Migration 00037 adds the tables and functions; the values are
-- added on their own because a new enum value cannot be used in the
-- transaction that adds it (goose runs each migration in its own
-- transaction, so 00037 and later code see the committed value).
--
--   runs.trigger          schedule | catch_up | manual | early | api
--   audit_log.actor_kind  user | system | cli | api (CHECK constraint:
--                         the column is text, see 00004)

-- +goose Up
-- +goose StatementBegin
ALTER TYPE public.run_trigger ADD VALUE 'api';

ALTER TABLE public.audit_log
    DROP CONSTRAINT audit_log_actor_kind_valid,
    ADD CONSTRAINT audit_log_actor_kind_valid CHECK (actor_kind IN ('user', 'system', 'cli', 'api'));
-- +goose StatementEnd

-- +goose Down
-- Dev and tests only (forward-only in production). Fails while runs or
-- audit entries use the value. PostgreSQL cannot drop an enum value, so the
-- type is recreated (like 00033); the partial index of 00021 compares with
-- an enum literal and is rebuilt around the type change.
-- +goose StatementBegin
ALTER TABLE public.audit_log
    DROP CONSTRAINT audit_log_actor_kind_valid,
    ADD CONSTRAINT audit_log_actor_kind_valid CHECK (actor_kind IN ('user', 'system', 'cli'));

DROP INDEX public.runs_job_early_idx;
ALTER TYPE public.run_trigger RENAME TO run_trigger_old;
CREATE TYPE public.run_trigger AS ENUM ('schedule', 'catch_up', 'manual', 'early');
ALTER TABLE public.runs
    ALTER COLUMN trigger TYPE public.run_trigger USING trigger::text::public.run_trigger;
DROP TYPE public.run_trigger_old;
CREATE INDEX runs_job_early_idx ON public.runs (job_id, scheduled_for) WHERE trigger = 'early';
-- +goose StatementEnd
