-- bekup_schema_version(): the schema version for `bekup serve` (task 1.1,
-- D-078, D-102). bekup_app has no access to goose_db_version, so this
-- SECURITY DEFINER function (owner bekup_owner) exposes only the highest
-- applied migration version. search_path is pinned and every object is
-- schema-qualified; EXECUTE is granted to bekup_app only.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION public.bekup_schema_version() RETURNS bigint
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT coalesce(max(v.version_id), 0)::bigint
    FROM public.goose_db_version AS v
    WHERE v.is_applied
$$;

REVOKE ALL ON FUNCTION public.bekup_schema_version() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.bekup_schema_version() TO bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION public.bekup_schema_version();
-- +goose StatementEnd
