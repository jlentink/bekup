#!/bin/sh
# Creates the login role of the optional external API (D-220).
#
#   bekup_external  used ONLY by `bekup external-api` (compose profile
#                   external-api). LOGIN, NOINHERIT, CONNECTION LIMIT 20,
#                   CONNECT on the bekup database and nothing else here: the
#                   migrations grant USAGE on schema api and EXECUTE on its
#                   functions (public.bekup_sync_external_grants(), run by
#                   every `bekup migrate up`), never anything in public.
#
# Runs once, after 10-bekup-roles.sh, on the first start of an empty data
# volume (the official image's /docker-entrypoint-initdb.d mechanism, as
# the bootstrap superuser over the local socket). The external API is
# opt-in: unless POSTGRES_BEKUP_EXTERNAL_API is "enabled" (compose passes
# BEKUP_EXTERNAL_API from .env, which `bekup setup init|upgrade
# --external-api` writes), or without the password file, it creates nothing
# and exits successfully. setup always writes the password file, so compose
# can mount it whether the API is enabled or not (D-220 amendment 4).
#
# The script is idempotent, so it also adds the role to an existing volume
# (docs/installation/upgrades.md); then pass --reload so the pg_hba.conf
# rule takes effect, and run `bekup migrate up` afterwards for the grants:
#   docker compose exec postgres sh -c \
#     'PGPASSWORD="$(cat /run/secrets/postgres_password)" /docker-entrypoint-initdb.d/20-bekup-external.sh --reload'
# A re-run re-applies the attributes and sets the password from the file.
#
# The password is read from a Docker secret file by psql's backtick \set,
# so it never appears in argv, the environment or the server log; it is
# passed to SQL as :'var' (quoted by psql) and stored as a SCRAM-SHA-256
# verifier. It must differ from the bekup_owner and bekup_app passwords.
#
# pg_hba.conf: the role authenticates over TCP only with TLS (D-100): a
# `hostnossl ... reject` rule is prepended (once, marked) like the rules of
# 10-bekup-roles.sh.
#
# Interface: POSTGRES_USER, PGDATA, POSTGRES_BEKUP_EXTERNAL_API (must be
# "enabled") and POSTGRES_BEKUP_EXTERNAL_PASSWORD_FILE (default
# /run/secrets/bekup_external_password); the owner/app password files of
# 10-bekup-roles.sh are compared when present.
set -eu

reload=false
case "${1:-}" in
    "") ;;
    --reload) reload=true ;;
    *)
        echo "usage: $0 [--reload]" >&2
        exit 2
        ;;
esac

# POSTGRES_BEKUP_* are consumed only by the init scripts (they are not
# bekup configuration keys).
ext_pw_file="${POSTGRES_BEKUP_EXTERNAL_PASSWORD_FILE:-/run/secrets/bekup_external_password}"
owner_pw_file="${POSTGRES_BEKUP_OWNER_PASSWORD_FILE:-/run/secrets/bekup_owner_password}"
app_pw_file="${POSTGRES_BEKUP_APP_PASSWORD_FILE:-/run/secrets/bekup_app_password}"

if [ "${POSTGRES_BEKUP_EXTERNAL_API:-}" != enabled ]; then
    echo "bekup init: the external API is not enabled (BEKUP_EXTERNAL_API in .env); the role bekup_external is not created (optional, D-220)"
    exit 0
fi
if [ ! -e "$ext_pw_file" ]; then
    echo "bekup init: no $ext_pw_file; the external API role bekup_external is not created (optional, D-220)"
    exit 0
fi
if [ ! -s "$ext_pw_file" ]; then
    echo "bekup init: empty secret file: $ext_pw_file" >&2
    exit 1
fi
for f in "$owner_pw_file" "$app_pw_file"; do
    if [ -s "$f" ] && cmp -s "$ext_pw_file" "$f"; then
        echo "bekup init: the bekup_external password must differ from the bekup_owner and bekup_app passwords ($f)" >&2
        exit 1
    fi
done

case "${POSTGRES_USER:-}" in
    "" | [!a-z_]* | *[!a-z0-9_]*)
        echo "bekup init: POSTGRES_USER must match ^[a-z_][a-z0-9_]*\$" >&2
        exit 1
        ;;
esac

psql -v ON_ERROR_STOP=1 --no-psqlrc --username "$POSTGRES_USER" --dbname postgres <<SQL
\\set ext_pw \`cat "$ext_pw_file"\`
SET password_encryption = 'scram-sha-256';

SELECT NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'bekup_external') AS create_role \\gset
\\if :create_role
CREATE ROLE bekup_external;
\\endif
-- Re-applied on every run: an existing role gets exactly these attributes.
ALTER ROLE bekup_external WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS NOINHERIT CONNECTION LIMIT 20 PASSWORD :'ext_pw';

-- Nothing else on the database: the migrations grant the api schema.
GRANT CONNECT ON DATABASE bekup TO bekup_external;
SQL

hba="${PGDATA:?PGDATA is not set}/pg_hba.conf"
marker="# bekup: bekup_external only over TLS (D-100, D-220)"
if ! grep -qxF "$marker" "$hba"; then
    tmp="$hba.bekup-tmp"
    {
        printf '%s\n' "$marker" "hostnossl  all  bekup_external  all  reject" ""
        cat "$hba"
    } >"$tmp"
    chmod 0600 "$tmp"
    mv -f "$tmp" "$hba"
fi
# During the first start the entrypoint restarts PostgreSQL after the init
# scripts (see 10-bekup-roles.sh); a running server needs the reload.
if [ "$reload" = true ]; then
    psql -v ON_ERROR_STOP=1 --no-psqlrc --quiet --username "$POSTGRES_USER" --dbname postgres \
        -c "SELECT pg_catalog.pg_reload_conf()" >/dev/null
fi

echo "bekup init: role bekup_external ready (run bekup migrate up to grant the api schema)"
