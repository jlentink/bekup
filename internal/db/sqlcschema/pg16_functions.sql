-- Type information for sqlc only (never applied to a database): built-in
-- PostgreSQL 16+ functions that sqlc's bundled catalog does not know yet.
-- sqlc reads this file in addition to the migrations (sqlc.yaml, schema).

-- SYSTEM_USER (PostgreSQL 16+): the authenticated identity of the session,
-- "<auth method>:<identity>" (D-207, SessionSystemUser in queries/migrate.sql).
CREATE FUNCTION pg_catalog."system_user"() RETURNS text LANGUAGE sql STABLE AS $$ SELECT NULL::text $$;
