#!/bin/sh
# TLS entrypoint wrapper for bekup's app database (D-100).
#
# PostgreSQL refuses a server key that is not owned by its own user (or
# root) or that is group/world-accessible. Docker secrets keep the host
# file's owner and mode, so this wrapper copies the certificate, key and CA
# bundle into a private runtime directory (a tmpfs in docker-compose.yml)
# owned by the server user with mode 0600, then starts the official image's
# entrypoint with TLS switched on:
#
#   ssl=on, ssl_min_protocol_version=TLSv1.2, ssl_cert_file, ssl_key_file,
#   ssl_ca_file (lets bekup present an optional client certificate)
#
# The pg_hba.conf rules that accept the bekup roles only over TLS are
# written by init/10-bekup-roles.sh on the first start.
#
# Interface (also exercised by the internal/db integration test):
#   POSTGRES_BEKUP_TLS_CERT_FILE  default /run/secrets/postgres.crt
#   POSTGRES_BEKUP_TLS_KEY_FILE   default /run/secrets/postgres.key
#   POSTGRES_BEKUP_TLS_CA_FILE    default /run/secrets/postgres_ca.crt
#   POSTGRES_BEKUP_TLS_DIR        default /var/lib/postgresql/tls
# The files come from `bekup certs init --component postgres`
# (<dir>/postgres/{postgres.crt,postgres.key,ca.crt}); postgres.crt is the
# chain ECDSA leaf + its one-time issuer, which PostgreSQL serves as is so
# that SCRAM channel binding works (D-104). Missing files are a
# hard error: the shipped stack never starts without TLS.
set -eu
umask 077

cert_src="${POSTGRES_BEKUP_TLS_CERT_FILE:-/run/secrets/postgres.crt}"
key_src="${POSTGRES_BEKUP_TLS_KEY_FILE:-/run/secrets/postgres.key}"
ca_src="${POSTGRES_BEKUP_TLS_CA_FILE:-/run/secrets/postgres_ca.crt}"
dir="${POSTGRES_BEKUP_TLS_DIR:-/var/lib/postgresql/tls}"

# Readability is checked by reading, not with `test -r`: Docker Desktop
# shows bind-mounted files as root:root with the host mode (0600 for the
# key) while still letting this UID read them, so access(2) would wrongly
# report them unreadable.
for f in "$cert_src" "$key_src" "$ca_src"; do
    if [ ! -s "$f" ] || ! head -c 1 "$f" >/dev/null 2>&1; then
        echo "bekup postgres: missing, empty or unreadable TLS file: $f (run bekup certs init --component postgres; see docs/security/app-db-tls.md)" >&2
        exit 1
    fi
done

mkdir -p "$dir"
chmod 0700 "$dir"
for pair in "$cert_src:server.crt" "$key_src:server.key" "$ca_src:ca.crt"; do
    src="${pair%:*}"
    dst="$dir/${pair##*:}"
    rm -f "$dst"
    cat "$src" >"$dst"
    chmod 0600 "$dst"
done
# Started as root (e.g. without `user:`), the official entrypoint drops to
# the postgres user: hand the copies over to it.
if [ "$(id -u)" = 0 ]; then
    chown -R postgres:postgres "$dir"
fi

if [ "${1:-}" = postgres ]; then
    set -- "$@" \
        -c ssl=on \
        -c ssl_min_protocol_version=TLSv1.2 \
        -c "ssl_cert_file=$dir/server.crt" \
        -c "ssl_key_file=$dir/server.key" \
        -c "ssl_ca_file=$dir/ca.crt"
fi
exec docker-entrypoint.sh "$@"
