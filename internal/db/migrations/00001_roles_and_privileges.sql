-- Roles, database/schema privileges and default privileges (D-054, D-092).
--
-- The roles bekup_owner and bekup_app are created by the deploy init scripts
-- (docs/security/db-roles.md), never by a migration. Migrations run as
-- bekup_owner, which must own the database; the app runs as bekup_app.
-- Default privileges give bekup_app DML on every table bekup_owner creates
-- later; tables that need less (audit_log) revoke it explicitly.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    owner_oid oid;
BEGIN
    IF current_setting('server_version_num')::int < 150000 THEN
        RAISE EXCEPTION 'bekup requires PostgreSQL 15 or newer (found %)', current_setting('server_version');
    END IF;
    IF pg_catalog.getdatabaseencoding() <> 'UTF8' THEN
        RAISE EXCEPTION 'the bekup database must use UTF8 encoding (found %)', pg_catalog.getdatabaseencoding();
    END IF;
    IF current_user <> 'bekup_owner' THEN
        RAISE EXCEPTION 'migrations must run as bekup_owner (connected as %)', current_user;
    END IF;
    SELECT oid INTO owner_oid FROM pg_catalog.pg_roles WHERE rolname = 'bekup_owner';
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'bekup_app') THEN
        RAISE EXCEPTION 'role bekup_app does not exist; create it with the deploy init script';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_catalog.pg_roles
        WHERE rolname IN ('bekup_owner', 'bekup_app')
          AND (rolsuper OR rolbypassrls OR rolcreaterole OR rolreplication)
    ) THEN
        RAISE EXCEPTION 'bekup_owner and bekup_app must not be SUPERUSER, BYPASSRLS, CREATEROLE or REPLICATION';
    END IF;
    IF pg_catalog.pg_has_role('bekup_app', 'bekup_owner', 'MEMBER') THEN
        RAISE EXCEPTION 'bekup_app must not be a member of bekup_owner';
    END IF;
    IF (SELECT datdba FROM pg_catalog.pg_database WHERE datname = current_database()) <> owner_oid THEN
        RAISE EXCEPTION 'database % must be owned by bekup_owner', current_database();
    END IF;

    -- Only bekup_app (and the owner) may connect; no temporary objects.
    EXECUTE format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database());
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO bekup_app', current_database());
END
$$;

-- Nobody but the owner may create objects in or (except bekup_app) use the
-- schema. public is owned by pg_database_owner, i.e. bekup_owner.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO bekup_app;

-- Functions are not executable by PUBLIC by default.
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

-- bekup_app gets DML (never TRUNCATE, REFERENCES or TRIGGER) on new tables,
-- sequence access and EXECUTE on new functions.
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO bekup_app;
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO bekup_app;
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO bekup_app;
-- +goose StatementEnd

-- +goose Down
-- Restores exactly the state the deploy init script
-- (deploy/postgres/init/10-bekup-roles.sh) leaves behind: no default
-- privileges, public schema owned by bekup_owner without PUBLIC access, and
-- database access only for the owner plus CONNECT for bekup_app. Nothing is
-- granted back to PUBLIC.
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    REVOKE EXECUTE ON FUNCTIONS FROM bekup_app;
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM bekup_app;
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM bekup_app;
-- Re-granting the built-in default removes the pg_default_acl entry.
ALTER DEFAULT PRIVILEGES FOR ROLE bekup_owner GRANT EXECUTE ON FUNCTIONS TO PUBLIC;

REVOKE USAGE ON SCHEMA public FROM bekup_app;
-- +goose StatementEnd
