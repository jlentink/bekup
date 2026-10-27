-- SchemaVersion returns the highest applied migration version through the
-- SECURITY DEFINER function bekup_schema_version() (migration 00006), because
-- bekup_app cannot read goose_db_version. Used by `bekup serve` at startup and
-- by /readyz (task 1.1, D-078).
-- name: SchemaVersion :one
SELECT public.bekup_schema_version()::bigint AS version;
