-- Periodic summary read indexes (D-226 amendment 1): every content query
-- of a summary is bounded by its period instead of scanning the whole
-- history.
--
--   runs_job_failed_idx              the newest failed run of a job in the
--                                    period (SummaryJobActivity's LATERAL
--                                    ... LIMIT 1); failed runs only.
--   restores_finished_at_idx         restores that finished in the period
--                                    (SummaryRestoreCounts).
--   test_restore_items_restore_idx   whether a restore is the internal
--                                    restore of a test restore (the NOT
--                                    EXISTS of SummaryRestoreCounts).
--   test_restores_finished_at_idx    test restores that finished in the
--                                    period (SummaryTestRestoreCounts).
--
-- Runs in the period use runs_finished_at_idx (00040).

-- +goose Up
-- +goose StatementBegin
CREATE INDEX runs_job_failed_idx ON public.runs (job_id, finished_at DESC, id DESC) WHERE status = 'failed';
CREATE INDEX restores_finished_at_idx ON public.restores (finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX test_restore_items_restore_idx ON public.test_restore_items (restore_id) WHERE restore_id IS NOT NULL;
CREATE INDEX test_restores_finished_at_idx ON public.test_restores (finished_at) WHERE finished_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.test_restores_finished_at_idx;
DROP INDEX public.test_restore_items_restore_idx;
DROP INDEX public.restores_finished_at_idx;
DROP INDEX public.runs_job_failed_idx;
-- +goose StatementEnd
