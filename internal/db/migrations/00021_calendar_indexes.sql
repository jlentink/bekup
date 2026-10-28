-- Calendar read indexes (task 3.14 review, D-160): every branch of
-- ListCalendarRuns is bounded by an index instead of scanning a job's
-- whole run history.
--
--   runs_job_start_idx  runs of a job by start (else queue) time: the
--                       look-back branch (coalesce(started_at, queued_at)
--                       >= scan_from).
--   runs_job_early_idx  started-early runs of a job by their original slot
--                       (trigger = 'early', scheduled_for in the range).
--
-- Active runs use runs_active_idx; the present artifacts of the jobs use
-- artifacts_job_destination_idx (partial on state = 'present', leading
-- job_id); run item counts use run_items_run_idx.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX runs_job_start_idx ON public.runs (job_id, (coalesce(started_at, queued_at)));
CREATE INDEX runs_job_early_idx ON public.runs (job_id, scheduled_for) WHERE trigger = 'early';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.runs_job_early_idx;
DROP INDEX public.runs_job_start_idx;
-- +goose StatementEnd
