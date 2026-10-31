-- Retention visibility and unrecorded files (D-209):
--
-- retention_holds         one row per job and destination on which the
--                         retention sweep keeps incomplete backups that no
--                         keep rule would keep on its own (the destination
--                         has no complete backup, or the backups newer than
--                         the newest complete one are incomplete). Written
--                         by the sweep (upsert per destination, delete when
--                         the hold ended); read by Job.retentionHolds.
--
--     reason              no_complete_run | newer_than_newest_complete.
--     held_runs           the incomplete runs kept only for that reason.
--     held_since          the time of the oldest of them.
--     newest_complete_at  the newest complete run there (NULL: none).
--     first_seen_at       when a sweep first saw the hold.
--     checked_at          when a sweep last confirmed it.
--
-- unrecorded_files        files in bekup's layout on a destination
--                         ({job slug}/{timestamp}/{file}, older than 24 h)
--                         without an artifact row, found by the daily scan.
--                         Object keys and sizes only; at most 1000 rows per
--                         destination (the scan bounds them and records the
--                         rest as truncated). Deleted by the scan when the
--                         file is gone or recorded, and by the admin delete.
--
-- unrecorded_file_scans   the outcome of the newest scan per destination.
--
-- artifacts_object_key_idx  the scan matches listed keys against the
--                         artifact rows of every destination.
--
-- bekup_app: INSERT, SELECT, DELETE and UPDATE of the state columns only.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.retention_holds (
    job_id             uuid NOT NULL REFERENCES public.jobs (id) ON DELETE CASCADE,
    destination_id     uuid NOT NULL REFERENCES public.destinations (id) ON DELETE CASCADE,
    reason             text NOT NULL,
    held_runs          integer NOT NULL,
    held_since         timestamptz NOT NULL,
    newest_complete_at timestamptz,
    first_seen_at      timestamptz NOT NULL,
    checked_at         timestamptz NOT NULL,
    PRIMARY KEY (job_id, destination_id),
    CONSTRAINT retention_holds_reason CHECK (reason IN ('no_complete_run', 'newer_than_newest_complete')),
    CONSTRAINT retention_holds_runs CHECK (held_runs > 0),
    CONSTRAINT retention_holds_newest CHECK ((reason = 'no_complete_run') = (newest_complete_at IS NULL))
);

REVOKE ALL ON TABLE public.retention_holds FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.retention_holds FROM bekup_app;
GRANT UPDATE (reason, held_runs, held_since, newest_complete_at, checked_at) ON TABLE public.retention_holds TO bekup_app;

CREATE TABLE public.unrecorded_files (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    destination_id uuid NOT NULL REFERENCES public.destinations (id) ON DELETE CASCADE,
    object_key     text NOT NULL,
    size           bigint NOT NULL,
    modified_at    timestamptz,
    first_seen_at  timestamptz NOT NULL,
    last_seen_at   timestamptz NOT NULL,
    CONSTRAINT unrecorded_files_key CHECK (
        octet_length(object_key) BETWEEN 1 AND 1024 AND object_key ~ '^[A-Za-z0-9_~%+=@-][A-Za-z0-9._~%+=@-]*(/[A-Za-z0-9_~%+=@-][A-Za-z0-9._~%+=@-]*)*$'),
    CONSTRAINT unrecorded_files_size CHECK (size >= 0),
    CONSTRAINT unrecorded_files_seen CHECK (last_seen_at >= first_seen_at)
);

CREATE UNIQUE INDEX unrecorded_files_destination_key_idx ON public.unrecorded_files (destination_id, object_key);

REVOKE ALL ON TABLE public.unrecorded_files FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.unrecorded_files FROM bekup_app;
GRANT UPDATE (size, modified_at, last_seen_at) ON TABLE public.unrecorded_files TO bekup_app;

CREATE TABLE public.unrecorded_file_scans (
    destination_id  uuid PRIMARY KEY REFERENCES public.destinations (id) ON DELETE CASCADE,
    scanned_at      timestamptz NOT NULL,
    scanned_objects integer NOT NULL,
    truncated       boolean NOT NULL,
    pruned_dirs     integer NOT NULL,
    CONSTRAINT unrecorded_file_scans_counts CHECK (scanned_objects >= 0 AND pruned_dirs >= 0)
);

REVOKE ALL ON TABLE public.unrecorded_file_scans FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.unrecorded_file_scans FROM bekup_app;
GRANT UPDATE (scanned_at, scanned_objects, truncated, pruned_dirs) ON TABLE public.unrecorded_file_scans TO bekup_app;

CREATE INDEX artifacts_object_key_idx ON public.artifacts (object_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.artifacts_object_key_idx;
DROP TABLE public.unrecorded_file_scans;
DROP TABLE public.unrecorded_files;
DROP TABLE public.retention_holds;
-- +goose StatementEnd
