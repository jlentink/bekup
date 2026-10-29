-- Dashboard aggregates (D-196):
--
-- job_missed_state  the ongoing missed-backup outage of a job as the
--                   missed_backup_check detector (internal/notify/missed,
--                   D-188) saw it on its last pass: one row per job with an
--                   ongoing outage, deleted once the outage ends or the job
--                   is no longer watched. Read by Job.missed/missedSince.
--                   Written only by the detector (bekup_app: INSERT,
--                   UPDATE of the state columns, DELETE).
--
--     missed_since      the first missed occurrence of the outage.
--     last_missed_at    the newest missed occurrence.
--     reason            the D-188 reason code of the newest missed occurrence.
--     checked_at        when the detector last confirmed the outage; a
--                       successful run that finished after it ends the
--                       outage for readers before the next pass.
--
-- artifacts_destination_present_idx backs destinationUsage (bytes and
-- object count of the present artifacts per destination).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.job_missed_state (
    job_id         uuid PRIMARY KEY REFERENCES public.jobs (id) ON DELETE CASCADE,
    missed_since   timestamptz NOT NULL,
    last_missed_at timestamptz NOT NULL,
    reason         text NOT NULL,
    checked_at     timestamptz NOT NULL,
    CONSTRAINT job_missed_state_order CHECK (last_missed_at >= missed_since),
    CONSTRAINT job_missed_state_reason_format CHECK (reason ~ '^[a-z][a-z_]{0,63}$')
);

REVOKE ALL ON TABLE public.job_missed_state FROM PUBLIC;
REVOKE TRUNCATE, REFERENCES, TRIGGER ON TABLE public.job_missed_state FROM bekup_app;
REVOKE UPDATE ON TABLE public.job_missed_state FROM bekup_app;
GRANT UPDATE (missed_since, last_missed_at, reason, checked_at) ON TABLE public.job_missed_state TO bekup_app;

CREATE INDEX artifacts_destination_present_idx ON public.artifacts (destination_id) INCLUDE (size)
    WHERE state = 'present';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.artifacts_destination_present_idx;
DROP TABLE public.job_missed_state;
-- +goose StatementEnd
