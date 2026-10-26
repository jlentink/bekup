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
