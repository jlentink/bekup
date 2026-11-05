-- Periodic summary e-mail (D-226): one row per account with the user's
-- own summary settings and the schedule state of the notify_summary_tick
-- worker (internal/notify, summary*.go).
--
-- user_summary_settings
--
--     frequency        daily | every_other_day | weekly | monthly | never.
--                      NULL: the account follows the configured defaults
--                      (notifications.summary_default_*); then hour,
--                      weekday, month_day and timezone are NULL as well.
--                      Rows of accounts that never saved their settings
--                      are written by the tick (state only).
--     hour             the local hour of the summary (0-23).
--     weekday          ISO weekday of weekly summaries (1 = Monday).
--     month_day        day of monthly summaries (1-28: every month has it).
--     timezone         the IANA zone of hour/weekday/month_day
--                      (validated with the scheduler's zone loader).
--     due_basis        the canonical effective schedule next_due_at was
--                      computed from; a different basis (changed defaults)
--                      makes the tick compute it again.
--     next_due_at      when the next summary is due (NULL: never).
--     last_period_end  the end of the last period handled (sent or
--                      skipped); the next period starts there, so a gap
--                      (downtime) is covered by one summary.
--     last_sent_at     when the last summary event was emitted.
--     last_outcome     sent | no_jobs (the account could see no job: no
--                      summary, recorded) and its time.
--
-- bekup_app: SELECT, INSERT, UPDATE (no DELETE: rows go with their
-- account, ON DELETE CASCADE).
--
-- runs_finished_at_idx backs the summary's run totals (runs that finished
-- in the period).

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.summary_frequency AS ENUM ('daily', 'every_other_day', 'weekly', 'monthly', 'never');

CREATE TABLE public.user_summary_settings (
    user_id         uuid PRIMARY KEY REFERENCES public.users (id) ON DELETE CASCADE,
    frequency       public.summary_frequency,
    hour            smallint,
    weekday         smallint,
    month_day       smallint,
    timezone        text,
    due_basis       text,
    next_due_at     timestamptz,
    last_period_end timestamptz,
    last_sent_at    timestamptz,
    last_outcome    text,
    last_outcome_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_summary_settings_explicit CHECK (
        (frequency IS NULL) = (hour IS NULL)
        AND (frequency IS NULL) = (weekday IS NULL)
        AND (frequency IS NULL) = (month_day IS NULL)
        AND (frequency IS NULL) = (timezone IS NULL)
    ),
    CONSTRAINT user_summary_settings_hour CHECK (hour IS NULL OR hour BETWEEN 0 AND 23),
    CONSTRAINT user_summary_settings_weekday CHECK (weekday IS NULL OR weekday BETWEEN 1 AND 7),
    CONSTRAINT user_summary_settings_month_day CHECK (month_day IS NULL OR month_day BETWEEN 1 AND 28),
    CONSTRAINT user_summary_settings_timezone CHECK (
        timezone IS NULL OR (char_length(timezone) BETWEEN 1 AND 64 AND timezone ~ '^[A-Za-z0-9/_+-]+$')
    ),
    CONSTRAINT user_summary_settings_basis CHECK (due_basis IS NULL OR char_length(due_basis) <= 200),
    CONSTRAINT user_summary_settings_outcome CHECK (
        (last_outcome IS NULL OR last_outcome IN ('sent', 'no_jobs'))
        AND (last_outcome IS NULL) = (last_outcome_at IS NULL)
    )
);

CREATE TRIGGER user_summary_settings_set_updated_at BEFORE UPDATE ON public.user_summary_settings
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE INDEX user_summary_settings_due_idx ON public.user_summary_settings (next_due_at)
    WHERE next_due_at IS NOT NULL;

REVOKE ALL ON TABLE public.user_summary_settings FROM PUBLIC;
REVOKE DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.user_summary_settings FROM bekup_app;
REVOKE UPDATE ON TABLE public.user_summary_settings FROM bekup_app;
GRANT UPDATE (frequency, hour, weekday, month_day, timezone, due_basis, next_due_at, last_period_end,
    last_sent_at, last_outcome, last_outcome_at, updated_at) ON TABLE public.user_summary_settings TO bekup_app;

CREATE INDEX runs_finished_at_idx ON public.runs (finished_at) WHERE finished_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.runs_finished_at_idx;
DROP TABLE public.user_summary_settings;
DROP TYPE public.summary_frequency;
-- +goose StatementEnd
