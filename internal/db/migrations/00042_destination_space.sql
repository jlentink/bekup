-- Stored space checks of destinations (D-227): the outcome of the newest
-- space check (the hourly destination_space_check worker or an on-demand
-- destinationSpace call that asked the backend), so the periodic summary
-- and the storage page show it without asking a backend. Written only by
-- internal/destinations (Service.recordSpace), and only when the row did
-- not change while the check ran. Metadata only: fixed codes and sizes,
-- never a raw error or server output.
--
--   space_checked_at        when the backend was asked (NULL: never).
--   space_status            available | unsupported | unavailable.
--   space_code              the fixed reason code when not available
--                           (unsupported; not_ready; a storage class such
--                           as network, timeout, permission_denied).
--   space_total_bytes,
--   space_free_bytes,
--   space_used_bytes        the reported values (NULL: not reported).
--   space_newest_set_bytes  the newest full set of backups there when
--                           checked (the D-225 low-space rule's second part).
--   space_low_cause         the D-225 verdict when checked: fraction |
--                           below_newest_set; NULL when not low.
--   space_low_since         the start of the current low-space episode
--                           (the destination.low_space dedup); kept while a
--                           check fails, cleared only after the destination
--                           has not been low for 3 hours (space_ok_since).
--   space_ok_since          inside an episode: the first of the consecutive
--                           checks that were not low (the recovery streak;
--                           NULL outside an episode or while low).
--   space_settings_at       the row's updated_at the check ran against: the
--                           result is stale once updated_at differs.
--
-- Times are the database clock (clock_timestamp() when the answer was
-- recorded, minus how long ago the backend answered), never a process
-- clock. Unchecked rows have every space_* column NULL; a checked row's
-- code is NULL exactly when it is available, 'unsupported' exactly when
-- unsupported, and any other stable code when unavailable.
--
-- Storing a space check does not bump updated_at: the set_updated_at
-- trigger skips updates that change only these or the last_test_* columns
-- (00029) and do change space_checked_at or last_tested_at.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.destinations
    ADD COLUMN space_checked_at timestamptz,
    ADD COLUMN space_status text,
    ADD COLUMN space_code text,
    ADD COLUMN space_total_bytes bigint,
    ADD COLUMN space_free_bytes bigint,
    ADD COLUMN space_used_bytes bigint,
    ADD COLUMN space_newest_set_bytes bigint,
    ADD COLUMN space_low_cause text,
    ADD COLUMN space_low_since timestamptz,
    ADD COLUMN space_settings_at timestamptz,
    ADD COLUMN space_ok_since timestamptz,
    ADD CONSTRAINT destinations_space_complete CHECK (
        CASE WHEN space_checked_at IS NULL THEN
            space_status IS NULL AND space_code IS NULL AND space_total_bytes IS NULL
            AND space_free_bytes IS NULL AND space_used_bytes IS NULL AND space_newest_set_bytes IS NULL
            AND space_low_cause IS NULL AND space_low_since IS NULL AND space_settings_at IS NULL
            AND space_ok_since IS NULL
        ELSE
            space_status IS NOT NULL AND space_settings_at IS NOT NULL
        END
    ),
    ADD CONSTRAINT destinations_space_status_value CHECK (
        space_status IS NULL OR space_status IN ('available', 'unsupported', 'unavailable')
    ),
    ADD CONSTRAINT destinations_space_code_format CHECK (
        space_code IS NULL OR space_code ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    ADD CONSTRAINT destinations_space_code_status CHECK (
        space_status IS NULL
        OR (space_status = 'available' AND space_code IS NULL)
        OR (space_status = 'unsupported' AND space_code = 'unsupported')
        OR (space_status = 'unavailable' AND space_code IS NOT NULL AND space_code <> 'unsupported')
    ),
    ADD CONSTRAINT destinations_space_sizes CHECK (
        (space_total_bytes IS NULL OR space_total_bytes >= 0)
        AND (space_free_bytes IS NULL OR space_free_bytes >= 0)
        AND (space_used_bytes IS NULL OR space_used_bytes >= 0)
        AND (space_newest_set_bytes IS NULL OR space_newest_set_bytes >= 0)
    ),
    ADD CONSTRAINT destinations_space_values_available CHECK (
        space_status IS NOT DISTINCT FROM 'available'
        OR (space_total_bytes IS NULL AND space_free_bytes IS NULL AND space_used_bytes IS NULL
            AND space_newest_set_bytes IS NULL AND space_low_cause IS NULL)
    ),
    ADD CONSTRAINT destinations_space_low_cause_value CHECK (
        space_low_cause IS NULL OR (space_low_cause IN ('fraction', 'below_newest_set') AND space_free_bytes IS NOT NULL)
    ),
    ADD CONSTRAINT destinations_space_ok_since CHECK (
        space_ok_since IS NULL OR (space_low_since IS NOT NULL AND space_low_cause IS NULL)
    );

DROP TRIGGER destinations_set_updated_at ON public.destinations;
CREATE TRIGGER destinations_set_updated_at BEFORE UPDATE ON public.destinations FOR EACH ROW
    WHEN (
        (NEW.last_tested_at IS NOT DISTINCT FROM OLD.last_tested_at
         AND NEW.space_checked_at IS NOT DISTINCT FROM OLD.space_checked_at)
        OR (to_jsonb(NEW) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at',
                                  'space_checked_at', 'space_status', 'space_code', 'space_total_bytes',
                                  'space_free_bytes', 'space_used_bytes', 'space_newest_set_bytes',
                                  'space_low_cause', 'space_low_since', 'space_settings_at',
                                  'space_ok_since'])
           IS DISTINCT FROM
           (to_jsonb(OLD) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at',
                                  'space_checked_at', 'space_status', 'space_code', 'space_total_bytes',
                                  'space_free_bytes', 'space_used_bytes', 'space_newest_set_bytes',
                                  'space_low_cause', 'space_low_since', 'space_settings_at',
                                  'space_ok_since'])
    )
    EXECUTE FUNCTION public.set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER destinations_set_updated_at ON public.destinations;
CREATE TRIGGER destinations_set_updated_at BEFORE UPDATE ON public.destinations FOR EACH ROW
    WHEN (
        NEW.last_tested_at IS NOT DISTINCT FROM OLD.last_tested_at
        OR (to_jsonb(NEW) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at'])
           IS DISTINCT FROM
           (to_jsonb(OLD) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at'])
    )
    EXECUTE FUNCTION public.set_updated_at();
ALTER TABLE public.destinations
    DROP COLUMN space_ok_since, DROP COLUMN space_settings_at, DROP COLUMN space_low_since, DROP COLUMN space_low_cause,
    DROP COLUMN space_newest_set_bytes, DROP COLUMN space_used_bytes, DROP COLUMN space_free_bytes,
    DROP COLUMN space_total_bytes, DROP COLUMN space_code, DROP COLUMN space_status, DROP COLUMN space_checked_at;
-- +goose StatementEnd
