-- Last test result of connections and destinations (D-187): the outcome of
-- the newest testConnection / testDestination, so the UI can show it after
-- a reload. Written only by internal/connections and internal/destinations
-- (Service.Test), and only when the row did not change while the test ran.
-- Metadata only: a normalized code, never a raw error or server output.
--
--   last_tested_at               when the result was stored (NULL: never).
--   last_test_ok                 the test passed.
--   last_test_code               the normalized failure code (connections:
--                                runner reasons such as TARGET_UNREACHABLE,
--                                INCOMPATIBLE_CLIENT, TLS_PIN_MISMATCH;
--                                destinations: storage classes such as
--                                permission_denied); NULL when ok.
--   last_test_settings_at        the row's updated_at the test ran against:
--                                the result is stale once updated_at differs.
--   last_test_privileges         connections only: the D-068 verdict of the
--                                test's probe (not_privileged | privileged |
--                                unknown; NULL when the probe failed first).
--   last_test_privilege_reasons  its reason codes (≤ 32 × 64 bytes).
--
-- Storing a test result does not bump updated_at: the set_updated_at
-- triggers skip updates that change only these columns (and do change
-- last_tested_at), so "settings changed since the test" stays meaningful.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.connections
    ADD COLUMN last_tested_at timestamptz,
    ADD COLUMN last_test_ok boolean,
    ADD COLUMN last_test_code text,
    ADD COLUMN last_test_settings_at timestamptz,
    ADD COLUMN last_test_privileges text,
    ADD COLUMN last_test_privilege_reasons text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT connections_last_test_complete CHECK (
        (last_tested_at IS NULL) = (last_test_ok IS NULL)
        AND (last_tested_at IS NULL) = (last_test_settings_at IS NULL)
    ),
    ADD CONSTRAINT connections_last_test_code_format CHECK (
        last_test_code IS NULL OR last_test_code ~ '^[A-Za-z][A-Za-z0-9_]{0,63}$'
    ),
    ADD CONSTRAINT connections_last_test_privileges_value CHECK (
        last_test_privileges IS NULL OR last_test_privileges IN ('not_privileged', 'privileged', 'unknown')
    ),
    ADD CONSTRAINT connections_last_test_privilege_reasons_bounded CHECK (
        cardinality(last_test_privilege_reasons) <= 32
        AND octet_length(array_to_string(last_test_privilege_reasons, '')) <= 32 * 64
    );

ALTER TABLE public.destinations
    ADD COLUMN last_tested_at timestamptz,
    ADD COLUMN last_test_ok boolean,
    ADD COLUMN last_test_code text,
    ADD COLUMN last_test_settings_at timestamptz,
    ADD CONSTRAINT destinations_last_test_complete CHECK (
        (last_tested_at IS NULL) = (last_test_ok IS NULL)
        AND (last_tested_at IS NULL) = (last_test_settings_at IS NULL)
    ),
    ADD CONSTRAINT destinations_last_test_code_format CHECK (
        last_test_code IS NULL OR last_test_code ~ '^[A-Za-z][A-Za-z0-9_]{0,63}$'
    );

DROP TRIGGER connections_set_updated_at ON public.connections;
CREATE TRIGGER connections_set_updated_at BEFORE UPDATE ON public.connections FOR EACH ROW
    WHEN (
        NEW.last_tested_at IS NOT DISTINCT FROM OLD.last_tested_at
        OR (to_jsonb(NEW) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at',
                                  'last_test_privileges', 'last_test_privilege_reasons'])
           IS DISTINCT FROM
           (to_jsonb(OLD) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at',
                                  'last_test_privileges', 'last_test_privilege_reasons'])
    )
    EXECUTE FUNCTION public.set_updated_at();

DROP TRIGGER destinations_set_updated_at ON public.destinations;
CREATE TRIGGER destinations_set_updated_at BEFORE UPDATE ON public.destinations FOR EACH ROW
    WHEN (
        NEW.last_tested_at IS NOT DISTINCT FROM OLD.last_tested_at
        OR (to_jsonb(NEW) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at'])
           IS DISTINCT FROM
           (to_jsonb(OLD) - ARRAY['last_tested_at', 'last_test_ok', 'last_test_code', 'last_test_settings_at'])
    )
    EXECUTE FUNCTION public.set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER destinations_set_updated_at ON public.destinations;
CREATE TRIGGER destinations_set_updated_at BEFORE UPDATE ON public.destinations FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
DROP TRIGGER connections_set_updated_at ON public.connections;
CREATE TRIGGER connections_set_updated_at BEFORE UPDATE ON public.connections FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
ALTER TABLE public.destinations
    DROP COLUMN last_test_settings_at, DROP COLUMN last_test_code, DROP COLUMN last_test_ok, DROP COLUMN last_tested_at;
ALTER TABLE public.connections
    DROP COLUMN last_test_privilege_reasons, DROP COLUMN last_test_privileges, DROP COLUMN last_test_settings_at,
    DROP COLUMN last_test_code, DROP COLUMN last_test_ok, DROP COLUMN last_tested_at;
-- +goose StatementEnd
