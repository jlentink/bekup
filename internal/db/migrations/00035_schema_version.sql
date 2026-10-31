-- Schema version per database (D-210): a job may capture the schema
-- (migration) version of every database it backs up.
--
--   jobs.version_query_mode   none | preset | custom (default none: opt-in).
--   jobs.version_preset       the internal/schemaversion preset name (the
--                             SQL lives in Go, never in the database); set
--                             exactly in mode preset.
--   jobs.version_custom_sql   the custom query (a single SELECT or WITH
--                             statement, at most 2 KiB, validated by the
--                             service and again by the runner); set
--                             exactly in mode custom. Readable by staff
--                             only through the API.
--   jobs.version_schema       PostgreSQL schema of a preset's table
--                             (default public).
--
--   run_items.schema_version         the captured version (printable
--                                    ASCII, at most 64 characters), set
--                                    exactly with status ok.
--   run_items.schema_version_status  ok | none | unavailable | invalid;
--                                    NULL when the job captures none.
--
-- Written only by internal/jobs (job settings) and internal/runstate (run
-- items); bekup_app keeps its table-level rights on both tables.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.job_version_query_mode AS ENUM ('none', 'preset', 'custom');

ALTER TABLE public.jobs
    ADD COLUMN version_query_mode public.job_version_query_mode NOT NULL DEFAULT 'none',
    ADD COLUMN version_preset     text,
    ADD COLUMN version_custom_sql text,
    ADD COLUMN version_schema     text NOT NULL DEFAULT 'public',
    ADD CONSTRAINT jobs_version_query_fields CHECK (
        (version_query_mode = 'none' AND version_preset IS NULL AND version_custom_sql IS NULL)
        OR (version_query_mode = 'preset' AND version_preset IS NOT NULL AND version_custom_sql IS NULL)
        OR (version_query_mode = 'custom' AND version_preset IS NULL AND version_custom_sql IS NOT NULL)
    ),
    ADD CONSTRAINT jobs_version_preset_format CHECK (version_preset IS NULL OR version_preset ~ '^[a-z][a-z0-9-]{0,31}$'),
    ADD CONSTRAINT jobs_version_custom_sql_length CHECK (
        version_custom_sql IS NULL OR octet_length(version_custom_sql) BETWEEN 1 AND 2048
    ),
    ADD CONSTRAINT jobs_version_schema_format CHECK (version_schema ~ '^[A-Za-z_][A-Za-z0-9_]{0,62}$');

ALTER TABLE public.run_items
    ADD COLUMN schema_version        text,
    ADD COLUMN schema_version_status text,
    ADD CONSTRAINT run_items_schema_version_status CHECK (
        schema_version_status IS NULL OR schema_version_status IN ('ok', 'none', 'unavailable', 'invalid')
    ),
    ADD CONSTRAINT run_items_schema_version CHECK (
        (schema_version IS NULL OR schema_version ~ '^[ -~]{1,64}$')
        AND ((schema_version IS NOT NULL) = (schema_version_status IS NOT DISTINCT FROM 'ok'))
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.run_items
    DROP CONSTRAINT run_items_schema_version,
    DROP CONSTRAINT run_items_schema_version_status,
    DROP COLUMN schema_version_status,
    DROP COLUMN schema_version;
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_version_schema_format,
    DROP CONSTRAINT jobs_version_custom_sql_length,
    DROP CONSTRAINT jobs_version_preset_format,
    DROP CONSTRAINT jobs_version_query_fields,
    DROP COLUMN version_schema,
    DROP COLUMN version_custom_sql,
    DROP COLUMN version_preset,
    DROP COLUMN version_query_mode;
DROP TYPE public.job_version_query_mode;
-- +goose StatementEnd
