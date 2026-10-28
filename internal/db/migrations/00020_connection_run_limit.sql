-- Per-connection run limit (task 3.13, D-156): the maximum number of
-- backup/restore runs that may hold a run slot (D-147) against one
-- connection at the same time.
--
--   max_concurrent_runs  NULL = the global workers.max_runs_per_connection
--                        of the process acquiring the slot; otherwise
--                        1..64. internal/connections additionally refuses
--                        values above workers.max_concurrent_runs (the
--                        global cap, which the slot query enforces too).
--
-- runstate's TryInsertRunSlot reads the column under the run-slot advisory
-- lock, so a changed limit applies to the next acquisition: lowering it
-- never stops active runs, new runs wait (snooze) until the count of slots
-- on the connection is below the new limit. bekup_app keeps DML through the
-- default privileges of 00001.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.connections
    ADD COLUMN max_concurrent_runs integer,
    ADD CONSTRAINT connections_max_concurrent_runs_range
        CHECK (max_concurrent_runs IS NULL OR max_concurrent_runs BETWEEN 1 AND 64);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.connections DROP COLUMN max_concurrent_runs;
-- +goose StatementEnd
