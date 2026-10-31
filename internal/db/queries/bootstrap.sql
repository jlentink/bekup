-- Queries of `bekup db bootstrap` on the admin connection (D-207). The DDL
-- itself (roles, database, grants) cannot take bind parameters; it is built
-- by internal/db/rolesql.go from constants, a validated database name and
-- validated SCRAM verifiers.

-- name: BootstrapAdminFacts :one
SELECT r.rolname::text AS role_name,
       coalesce(r.rolsuper, false)::boolean AS is_superuser,
       coalesce(r.rolcreaterole, false)::boolean AS create_role,
       coalesce(r.rolcreatedb, false)::boolean AS create_db,
       coalesce(r.rolinherit, false)::boolean AS inherit,
       pg_catalog.current_setting('server_version_num')::int AS server_version_num,
       pg_catalog.current_setting('server_version')::text AS server_version
FROM pg_catalog.pg_roles AS r
WHERE r.rolname = current_user;

-- BootstrapRoleFacts: no row means the role does not exist.
-- name: BootstrapRoleFacts :one
SELECT r.rolname::text AS role_name,
       coalesce(r.rolcanlogin, false)::boolean AS can_login,
       coalesce(r.rolsuper, false)::boolean AS is_superuser,
       coalesce(r.rolcreaterole, false)::boolean AS create_role,
       coalesce(r.rolcreatedb, false)::boolean AS create_db,
       coalesce(r.rolreplication, false)::boolean AS replication,
       coalesce(r.rolbypassrls, false)::boolean AS bypass_rls
FROM pg_catalog.pg_roles AS r
WHERE r.rolname = sqlc.arg(role_name)::text;

-- BootstrapDatabaseFacts: no row means the database does not exist.
-- name: BootstrapDatabaseFacts :one
SELECT pg_catalog.pg_get_userbyid(d.datdba)::text AS owner,
       pg_catalog.pg_encoding_to_char(d.encoding)::text AS encoding
FROM pg_catalog.pg_database AS d
WHERE d.datname = sqlc.arg(database_name)::text;

-- BootstrapCanSetRole (PostgreSQL 16+): may the current user SET ROLE to
-- the role (needed for CREATE DATABASE ... OWNER) and does it inherit the
-- role's privileges (needed for the database grants as a non-owner)?
-- name: BootstrapCanSetRole :one
SELECT (pg_catalog.pg_has_role(current_user, sqlc.arg(role_name)::text, 'SET')
        AND pg_catalog.pg_has_role(current_user, sqlc.arg(role_name)::text, 'USAGE'))::boolean AS can_set;

-- BootstrapHasAdminOption: may the current user grant the role (ADMIN
-- OPTION; PostgreSQL 16+ requires it for GRANT, 15 also accepts CREATEROLE)?
-- name: BootstrapHasAdminOption :one
SELECT pg_catalog.pg_has_role(current_user, sqlc.arg(role_name)::text, 'MEMBER WITH ADMIN OPTION')::boolean AS has_admin;

-- BootstrapIsMember (PostgreSQL 15): membership allows SET ROLE.
-- name: BootstrapIsMember :one
SELECT pg_catalog.pg_has_role(current_user, sqlc.arg(role_name)::text, 'MEMBER')::boolean AS is_member;
