#!/bin/sh
# Creates bekup's least-privilege roles and database (D-054).
#
# Runs once, on the first start of an empty data volume, via the official
# image's /docker-entrypoint-initdb.d mechanism (as the bootstrap superuser
# over the local socket).
#
#   bekup_owner  owns the bekup database; used ONLY by `bekup migrate`.
#   bekup_app    runtime role for serve/worker; DML only, grants are issued
#                by the migrations (task 0.4), not here.
#
# Passwords are read from Docker secret files by psql's backtick \set, so they
# never appear in argv, the environment or the server log. They are passed to
# SQL as :'var' (psql quotes them as literals, no injection) and stored as
# SCRAM-SHA-256 hashes.
#
# The script only bootstraps roles, the database and pg_hba.conf. Since
# task 0.4 the goose migrations (run as bekup_owner by `bekup migrate`) own
# everything inside the database: schema grants, default privileges, the
# audit-log revokes and triggers (D-054, D-092).
#
# pg_hba.conf: the bootstrap superuser ($POSTGRES_USER) is rejected on every
# TCP connection, so it can only log in over the local Unix socket inside the
# container (the healthcheck and `docker compose exec` use the socket). The
# bekup roles authenticate with SCRAM-SHA-256 over TCP **only over TLS**
# (D-100): every non-TLS TCP connection for them is rejected (`hostnossl ...
# reject`); TLS itself is switched on by ../entrypoint.sh. Like the rest of
# this script, this only runs when the data volume is initialised; see
# docs/installation/upgrades.md for existing volumes.
#
# Interface (kept stable; also exercised by the internal/db integration
# test): POSTGRES_USER, PGDATA, POSTGRES_BEKUP_OWNER_PASSWORD_FILE and
# POSTGRES_BEKUP_APP_PASSWORD_FILE (defaults /run/secrets/bekup_owner_password
# and /run/secrets/bekup_app_password).
set -eu

# POSTGRES_BEKUP_* are consumed only by this script (they are not bekup
# configuration keys).
owner_pw_file="${POSTGRES_BEKUP_OWNER_PASSWORD_FILE:-/run/secrets/bekup_owner_password}"
app_pw_file="${POSTGRES_BEKUP_APP_PASSWORD_FILE:-/run/secrets/bekup_app_password}"

for f in "$owner_pw_file" "$app_pw_file"; do
    if [ ! -s "$f" ]; then
        echo "bekup init: missing or empty secret file: $f" >&2
        exit 1
    fi
done

psql -v ON_ERROR_STOP=1 --no-psqlrc --username "$POSTGRES_USER" --dbname postgres <<SQL
\\set owner_pw \`cat "$owner_pw_file"\`
\\set app_pw \`cat "$app_pw_file"\`
SET password_encryption = 'scram-sha-256';

CREATE ROLE bekup_owner WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS NOINHERIT PASSWORD :'owner_pw';
CREATE ROLE bekup_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOREPLICATION NOBYPASSRLS NOINHERIT CONNECTION LIMIT 100 PASSWORD :'app_pw';

CREATE DATABASE bekup OWNER bekup_owner ENCODING 'UTF8' TEMPLATE template0;

-- Nobody but the owner may connect by default; bekup_app gets CONNECT only.
REVOKE ALL ON DATABASE bekup FROM PUBLIC;
GRANT CONNECT ON DATABASE bekup TO bekup_app;

-- The bootstrap DBs are off limits to the bekup roles.
REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;
REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;
SQL

psql -v ON_ERROR_STOP=1 --no-psqlrc --username "$POSTGRES_USER" --dbname bekup <<'SQL'
-- Lock down the public schema: only the owner may create objects in it
-- (default since PG15; explicit for clarity). Usage for bekup_app is granted
-- by the migrations together with the table grants.
ALTER SCHEMA public OWNER TO bekup_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
SQL

# --- pg_hba.conf: superuser on the local socket only, bekup roles TLS only -----
# Prepended, because pg_hba.conf is evaluated top-down and the image's
# entrypoint has already appended a generic `host all all all scram-sha-256`.
case "$POSTGRES_USER" in
    "" | [!a-z_]* | *[!a-z0-9_]*)
        echo "bekup init: POSTGRES_USER must match ^[a-z_][a-z0-9_]*\$" >&2
        exit 1
        ;;
esac
hba="${PGDATA:?PGDATA is not set}/pg_hba.conf"
# prepend_hba <marker> <line>...: prepends a marked block once.
prepend_hba() {
    marker="$1"
    shift
    if grep -qxF "$marker" "$hba"; then
        return 0
    fi
    tmp="$hba.bekup-tmp"
    {
        printf '%s\n' "$marker" "$@" ""
        cat "$hba"
    } >"$tmp"
    chmod 0600 "$tmp"
    mv -f "$tmp" "$hba"
}
# Prepended in reverse order of evaluation: the superuser block ends up first.
prepend_hba "# bekup: bekup roles only over TLS (D-100)" \
    "hostnossl  all  bekup_owner,bekup_app  all  reject"
prepend_hba "# bekup: superuser only via the local socket" \
    "host     all          $POSTGRES_USER  all  reject" \
    "hostssl  all          $POSTGRES_USER  all  reject" \
    "host     replication  $POSTGRES_USER  all  reject"
# No pg_reload_conf() here: the init scripts run against a temporary server
# that listens on the socket only (listen_addresses=''), and the entrypoint
# restarts PostgreSQL afterwards, so the rules above are active before the
# first TCP connection. (A reload from here would log a spurious
# "listen_addresses cannot be changed" configuration error.)

echo "bekup init: roles bekup_owner and bekup_app and database bekup created"
