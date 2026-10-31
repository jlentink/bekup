-- Queries used by the migrator on its dedicated owner connection.

-- CurrentRoleName returns the role of the current session.
-- name: CurrentRoleName :one
SELECT current_user::text AS role_name;

-- RoleSecurityCheck reports the attributes of a role that would weaken the
-- least-privilege model, and every role it is a member of (directly or
-- transitively) that is dangerous: a role with SUPERUSER, BYPASSRLS,
-- CREATEROLE or REPLICATION, any predefined pg_* role except
-- pg_database_owner, or one of the extra forbidden roles. No row: the role
-- does not exist.
-- name: RoleSecurityCheck :one
SELECT r.rolname::text AS role_name,
       coalesce(r.rolsuper, true)::boolean AS is_superuser,
       coalesce(r.rolbypassrls, true)::boolean AS bypass_rls,
       coalesce(r.rolcreaterole, true)::boolean AS create_role,
       coalesce(r.rolreplication, true)::boolean AS replication,
       coalesce((
           SELECT array_agg(g.rolname::text ORDER BY g.rolname)
           FROM pg_catalog.pg_roles AS g
           WHERE g.oid <> r.oid
             AND pg_catalog.pg_has_role(r.oid, g.oid, 'MEMBER')
             AND (g.rolsuper OR g.rolbypassrls OR g.rolcreaterole OR g.rolreplication
                  OR (g.rolname LIKE 'pg\_%' AND g.rolname <> 'pg_database_owner')
                  OR g.rolname::text = ANY (sqlc.arg(forbidden_roles)::text[]))
       ), '{}')::text[] AS dangerous_memberships
FROM pg_catalog.pg_roles AS r
WHERE r.rolname = sqlc.arg(role_name)::text;

-- Migration lock: 'bekupMIG' as a big-endian int64. Session-level, but only
-- ever taken on the migrator's own dedicated (unpooled) connection.
-- name: AcquireMigrationLock :exec
SELECT pg_advisory_lock(7090191340504172871);

-- name: ReleaseMigrationLock :one
SELECT pg_advisory_unlock(7090191340504172871);

-- SessionRoles returns the login role of the session, the role it acts as
-- (different only after SET ROLE: single-account mode, or a role setting
-- someone attached to the login role) and whether the login role is a
-- superuser (D-099, D-207).
-- name: SessionRoles :one
SELECT session_user::text AS session_role,
       current_user::text AS active_role,
       coalesce((SELECT r.rolsuper FROM pg_catalog.pg_roles AS r
                 WHERE r.rolname = session_user), true)::boolean AS session_superuser,
       pg_catalog.current_setting('server_version_num')::int AS server_version_num;

-- SessionSystemUser (PostgreSQL 16+ only: SYSTEM_USER is a syntax error on
-- 15) returns the authenticated identity of the session as
-- "<auth method>:<identity>", e.g. "scram-sha-256:bekup_app" (D-207): unlike
-- session_user it cannot be changed after authentication.
-- name: SessionSystemUser :one
SELECT coalesce(SYSTEM_USER, '')::text AS system_user;

-- RoleMemberships lists who may act as or administer the given roles
-- (D-207): every direct pg_auth_members row with the role as the granted
-- role, the member's name, whether it holds ADMIN OPTION and whether the
-- member is a superuser. Names only.
-- name: RoleMemberships :many
SELECT g.rolname::text AS role_name,
       m.rolname::text AS member_name,
       am.admin_option::boolean AS admin_option,
       m.rolsuper::boolean AS member_superuser
FROM pg_catalog.pg_auth_members AS am
JOIN pg_catalog.pg_roles AS g ON g.oid = am.roleid
JOIN pg_catalog.pg_roles AS m ON m.oid = am.member
WHERE g.rolname::text = ANY (sqlc.arg(role_names)::text[])
ORDER BY 1, 2, 3;

-- Preflight of `bekup migrate check` (D-207).

-- name: PreflightServerVersion :one
SELECT pg_catalog.current_setting('server_version_num')::int AS version_num,
       pg_catalog.current_setting('server_version')::text AS version;

-- name: PreflightDatabase :one
SELECT d.datname::text AS name,
       pg_catalog.pg_get_userbyid(d.datdba)::text AS owner,
       pg_catalog.pg_encoding_to_char(d.encoding)::text AS encoding
FROM pg_catalog.pg_database AS d
WHERE d.datname = pg_catalog.current_database();

-- PreflightPublicSchemaOwner: no row means the public schema is missing.
-- name: PreflightPublicSchemaOwner :one
SELECT pg_catalog.pg_get_userbyid(n.nspowner)::text AS owner
FROM pg_catalog.pg_namespace AS n
WHERE n.nspname = 'public';

-- PreflightPublicSchemaCreate: may the session (bekup_owner) create objects
-- in the public schema? (PostgreSQL 15 denies it to a NOINHERIT database
-- owner while pg_database_owner owns the schema.)
-- name: PreflightPublicSchemaCreate :one
SELECT pg_catalog.has_schema_privilege('public', 'CREATE')::boolean AS can_create;

-- PreflightCitext reports whether citext (migration 00004) is installed in
-- this database, available on the server, and trusted (then the database
-- owner can create it). azure_extensions is Azure's extension allow-list
-- (azure_allowlist false elsewhere).
-- name: PreflightCitext :one
SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_extension AS e WHERE e.extname = 'citext')::boolean AS installed,
       EXISTS (SELECT 1 FROM pg_catalog.pg_available_extensions AS a WHERE a.name = 'citext')::boolean AS available,
       coalesce((SELECT v.trusted
                 FROM pg_catalog.pg_available_extension_versions AS v
                 JOIN pg_catalog.pg_available_extensions AS a
                   ON a.name = v.name AND a.default_version = v.version
                 WHERE v.name = 'citext'), false)::boolean AS trusted,
       (pg_catalog.current_setting('azure.extensions', true) IS NOT NULL)::boolean AS azure_allowlist,
       coalesce(pg_catalog.current_setting('azure.extensions', true), '')::text AS azure_extensions;

-- PreflightConnectableDatabases lists other databases the role may connect
-- to (at most 50).
-- name: PreflightConnectableDatabases :many
SELECT d.datname::text AS name
FROM pg_catalog.pg_database AS d
WHERE d.datname <> pg_catalog.current_database()
  AND d.datallowconn
  AND pg_catalog.has_database_privilege(sqlc.arg(role_name)::text, d.oid, 'CONNECT')
ORDER BY d.datname
LIMIT 50;

-- PreflightRoleSettings lists ALTER ROLE / ALTER DATABASE ... SET entries
-- that apply to the given roles in this database (names only, never the
-- values).
-- name: PreflightRoleSettings :many
SELECT coalesce(r.rolname::text, 'ALL') AS role_name,
       coalesce(d.datname::text, 'ALL') AS database_name
FROM pg_catalog.pg_db_role_setting AS s
LEFT JOIN pg_catalog.pg_roles AS r ON r.oid = s.setrole
LEFT JOIN pg_catalog.pg_database AS d ON d.oid = s.setdatabase
WHERE (s.setrole = 0 OR r.rolname::text = ANY (sqlc.arg(role_names)::text[]))
  AND (s.setdatabase = 0 OR d.datname = pg_catalog.current_database())
ORDER BY 1, 2;
