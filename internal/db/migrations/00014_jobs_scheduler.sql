-- Scheduler foundation (task 3.4, D-145): the minimal jobs table the
-- scheduler loop needs, and the record of skipped occurrences.
--
-- jobs holds only the columns the scheduler reads and writes. Task 3.5
-- extends the table (connection, databases, retention, owners, timeout,
-- ...) with ALTER TABLE in its own migration and adds the foreign keys from
-- job_encryption / job_passphrases / job_recipients (task 3.2) to jobs (id).
-- Jobs are soft deleted (deleted_at); bekup_app cannot DELETE them.
--
--   schedule          canonical internal/scheduler.Schedule JSON (D-143);
--                     {"kind":"manual"} is never scheduled.
--   next_run_at       the next occurrence; NULL = not scheduled. The jobs
--                     service (3.5) sets it on create / schedule change /
--                     enable; the scheduler advances it; an unusable
--                     schedule resets it to NULL.
--   last_scheduled_at when the scheduler last handled an occurrence (a run
--                     was enqueued or skipped).
--
-- schedule_skips records occurrences the scheduler did not enqueue:
-- 'still_running' (the previous run of the job is still queued, running or
-- awaiting a retry; River's unique insert refused the new one) and
-- 'unschedulable' (the stored schedule no longer compiles or has no further
-- occurrence; next_run_at is reset to NULL). Rows are deleted after the
-- retention period by the maintenance worker. Tasks 3.6/3.7/5.4 may fold
-- this into the runs history.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.jobs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    enabled           boolean NOT NULL DEFAULT false,
    schedule          jsonb NOT NULL DEFAULT '{"kind":"manual"}'::jsonb,
    next_run_at       timestamptz,
    last_scheduled_at timestamptz,
    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT jobs_schedule_object CHECK (
        jsonb_typeof(schedule) = 'object' AND octet_length(schedule::text) <= 1024
    )
);

CREATE TRIGGER jobs_set_updated_at BEFORE UPDATE ON public.jobs
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- The scheduler's due scan.
CREATE INDEX jobs_due_idx ON public.jobs (next_run_at)
    WHERE enabled AND deleted_at IS NULL AND next_run_at IS NOT NULL;

CREATE TABLE public.schedule_skips (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id        uuid NOT NULL REFERENCES public.jobs (id) ON DELETE CASCADE,
    scheduled_for timestamptz,
    reason        text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schedule_skips_reason CHECK (reason IN ('still_running', 'unschedulable'))
);

CREATE INDEX schedule_skips_job_idx ON public.schedule_skips (job_id, created_at DESC);
CREATE INDEX schedule_skips_created_idx ON public.schedule_skips (created_at);

REVOKE ALL ON TABLE public.jobs, public.schedule_skips FROM PUBLIC;
REVOKE DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.jobs FROM bekup_app;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.schedule_skips FROM bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.schedule_skips;
DROP TABLE public.jobs;
-- +goose StatementEnd
