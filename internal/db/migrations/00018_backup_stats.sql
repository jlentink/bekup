-- Backup completeness evidence (task 3.10, D-141, D-151): the source
-- statistics the runner captured for each dumped database (or the
-- PostgreSQL globals), written by the backup worker after a complete,
-- verified dump. The same canonical object is signed into the run's
-- manifest ("stats"); this table lets test restores (4.6) and the UI
-- (6.6) read it without fetching the manifest.
--
--   stats          the canonical stats object of the item (integers and
--                  strings only, D-144): level, consistency, object counts,
--                  per-table row counts (bounded), warnings. Names and
--                  counts only, never data values.
--   consistency    snapshot (PostgreSQL: the dump ran in the stats'
--                  exported snapshot, counts are exact) | approximate
--                  (MySQL/MariaDB, globals: taken right before the dump).
--   warning_count  completeness warnings of the item (privileges,
--                  non-transactional tables, timeouts, ...).
--
-- runs.has_warnings is true when the run recorded any warning (run or item
-- level, completeness warnings included); the status stays success or
-- partial. runs.notices holds informational findings that are not
-- warnings (e.g. databases_excluded: databases the job's exclude list
-- leaves out on purpose), so has_warnings stays meaningful.
--
-- bekup_app may only INSERT and SELECT backup_stats: the evidence is never
-- changed or deleted.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.backup_stats (
    run_item_id   uuid PRIMARY KEY REFERENCES public.run_items (id),
    run_id        uuid NOT NULL REFERENCES public.runs (id),
    job_id        uuid NOT NULL REFERENCES public.jobs (id),
    level         public.job_verification NOT NULL,
    consistency   text NOT NULL,
    tables_total  integer NOT NULL DEFAULT 0,
    warning_count integer NOT NULL DEFAULT 0,
    stats         jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT backup_stats_consistency CHECK (consistency IN ('snapshot', 'approximate')),
    CONSTRAINT backup_stats_counts CHECK (tables_total >= 0 AND warning_count >= 0),
    CONSTRAINT backup_stats_object CHECK (jsonb_typeof(stats) = 'object' AND octet_length(stats::text) <= 4194304)
);

CREATE INDEX backup_stats_run_idx ON public.backup_stats (run_id);
CREATE INDEX backup_stats_job_idx ON public.backup_stats (job_id, created_at DESC);

REVOKE ALL ON TABLE public.backup_stats FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.backup_stats FROM bekup_app;

ALTER TABLE public.runs
    ADD COLUMN has_warnings boolean NOT NULL GENERATED ALWAYS AS (jsonb_array_length(warnings) > 0) STORED,
    ADD COLUMN notices jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT runs_notices_array CHECK (jsonb_typeof(notices) = 'array' AND octet_length(notices::text) <= 65536);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.runs DROP COLUMN notices, DROP COLUMN has_warnings;
DROP TABLE public.backup_stats;
-- +goose StatementEnd
