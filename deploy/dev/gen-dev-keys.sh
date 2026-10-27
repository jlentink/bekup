#!/usr/bin/env bash
# Generates DEVELOPMENT keys, passwords and certificates for
# docker-compose.yml + docker-compose.dev.yml into ./secrets/ (gitignored),
# plus secrets/local/ for running bekup natively against the dev stack
# (`just dev` + `just run`, D-114). Never use these files in production.
#
# Environment (both optional; `just dev` passes them through):
#   BEKUP_DEV_KEYS_DIR  output directory instead of ./secrets (absolute, or
#                       relative to the repository root). The compose dev
#                       override reads the same variable.
#   BEKUP_DEV_PG_PORT   host port of the dev app Postgres (default 5432);
#                       written into the secrets/local/ database URLs.
#
# Usage: deploy/dev/gen-dev-keys.sh [--force] [--chown]
#   --force  regenerate everything (destroys the existing dev files; reset the
#            dev volumes afterwards: docker compose ... down -v)
#   --chown  (Linux, needs root) chown each file to the UID of the container
#            that reads it: 65532 = bekup/minio, 70 = postgres, 999 =
#            mysql/mariadb. The dev TOML configs (deploy/dev/*.dev.toml)
#            become root:root 0644, because bekup only reads config files
#            owned by root or its own UID and not group/world-writable (edit
#            them with sudo afterwards). Docker Desktop (macOS/Windows) maps
#            ownership itself and does not need this.
#
# All randomness comes from the kernel CSPRNG (/dev/urandom, ssh-keygen,
# Go crypto/rand). Directories are 0700, secret files 0600 (public
# certificates 0644 inside 0700 directories), and the umask is 077
# throughout, so no secret is ever world-readable, not even briefly. Values
# are never printed.
set -euo pipefail
umask 077

force=0
do_chown=0
for arg in "$@"; do
    case "$arg" in
        --force) force=1 ;;
        --chown) do_chown=1 ;;
        -h | --help) sed -n '2,33p' "$0"; exit 0 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

for cmd in go ssh-keygen head base64 tr grep openssl cmp; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "missing required command: $cmd" >&2; exit 1; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
s="${BEKUP_DEV_KEYS_DIR:-secrets}"
[[ "$s" == /* ]] || s="$repo_root/$s"
d="$s/dev"
c="$s/certs"
l="$s/local"
pg_port="${BEKUP_DEV_PG_PORT:-5432}"
if ! [[ "$pg_port" =~ ^[1-9][0-9]{0,4}$ ]] || ((pg_port > 65535)); then
    echo "BEKUP_DEV_PG_PORT must be a TCP port (got '$pg_port')" >&2
    exit 2
fi
mkdir -p "$s" "$d" "$c" "$l"
chmod 0700 "$s" "$d" "$c" "$l"

# 32 random bytes, URL-safe base64 without padding (safe inside URLs/DSNs).
rand32() { head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n'; }

# write <file> <content>: only if missing (or --force); never echoes content.
write() {
    local f="$1" content="$2"
    if [[ -e "$f" && $force -eq 0 ]]; then
        return 0
    fi
    rm -f "$f"
    printf '%s' "$content" >"$f"
    chmod 0600 "$f"
    echo "generated ${f#"$repo_root"/}"
}

# --- bekup app DB ------------------------------------------------------------
write "$s/postgres_password" "$(rand32)"
write "$s/bekup_owner_password" "$(rand32)"
write "$s/bekup_app_password" "$(rand32)"
# The URLs embed the passwords above, so they are (re)written together.
# No sslmode: TLS comes from bekup.toml [database] tls_mode (verify-full
# against secrets/certs/postgres/ca.crt, D-100), exactly as in production.
# URLs from before D-100 (sslmode=disable) are rewritten.
if [[ ! -e "$s/bekup_database_url" || $force -eq 1 ]] ||
    grep -q 'sslmode=disable' "$s/bekup_database_url" "$s/bekup_database_owner_url" 2>/dev/null; then
    force_saved=$force
    force=1
    write "$s/bekup_database_owner_url" \
        "postgres://bekup_owner:$(cat "$s/bekup_owner_password")@postgres:5432/bekup"
    write "$s/bekup_database_url" \
        "postgres://bekup_app:$(cat "$s/bekup_app_password")@postgres:5432/bekup"
    force=$force_saved
fi

# --- master key ----------------------------------------------------------------
# A keyring in the `bekup keys generate` format (D-085; 256 bits from
# crypto/rand, written 0600, never overwritten by bekup itself). A file left
# over in the pre-keyring format (bare base64) is replaced.
if [[ ! -e "$s/bekup_master_key" || $force -eq 1 ]] ||
    ! grep -qx 'bekup-keyring v1' "$s/bekup_master_key"; then
    rm -f "$s/bekup_master_key"
    (cd "$repo_root" && go run ./cmd/bekup keys generate --out "$s/bekup_master_key" >/dev/null)
    echo "generated ${s#"$repo_root"/}/bekup_master_key"
    rm -f "$l/bekup_master_key"
fi

# --- mTLS certificates -----------------------------------------------------------
# `bekup certs init` (task 0.6) with an UNENCRYPTED CA key and 90-day leaves:
# acceptable only for this throwaway dev PKI. Production uses
# `docker compose --profile setup run --rm certs-init` (passphrase-protected
# CA key, moved offline afterwards; docs/security/mtls-certs.md). Layout:
# secrets/certs/{ca/ca.{crt,key}, runner/runner.{crt,key}+ca.crt,
# serve/serve.{crt,key}+ca.crt, postgres/postgres.{crt,key}+ca.crt};
# identities as in the config defaults. The postgres certificate (DNS
# "postgres", plus "localhost" and 127.0.0.1 so `just run` on the host can
# verify it with verify-full through the published 127.0.0.1 port, D-114) is
# the app DB's TLS server certificate (D-100): an ECDSA leaf plus its
# one-time ECDSA issuer, so SCRAM channel binding works (D-104).
pg_sans=(--postgres-san localhost --postgres-san 127.0.0.1)
dev_pki_init() {
    local init_args=(--dir "$c" --component runner --component serve --component postgres
        "${pg_sans[@]}" --unencrypted-ca-key --leaf-days 90)
    if [[ "${1:-}" == force ]]; then
        init_args+=(--force)
    fi
    if ! out=$(cd "$repo_root" && go run ./cmd/bekup certs init "${init_args[@]}" 2>&1); then
        printf '%s\n' "$out" >&2
        exit 1
    fi
    echo "generated ${c#"$repo_root"/}/{ca,runner,serve,postgres}/ (dev CA key is unencrypted)"
}
# pg_has_host_sans: the postgres leaf names localhost and 127.0.0.1.
pg_has_host_sans() {
    local sans
    sans=$(openssl x509 -in "$c/postgres/postgres.crt" -noout -ext subjectAltName 2>/dev/null) || return 1
    grep -q 'DNS:localhost' <<<"$sans" && grep -q 'IP Address:127.0.0.1' <<<"$sans"
}
if [[ ! -e "$c/ca/ca.crt" || $force -eq 1 ]]; then
    if [[ $force -eq 1 ]]; then dev_pki_init force; else dev_pki_init; fi
elif [[ ! -e "$c/postgres/postgres.crt" ]] ||
    [[ "$(grep -c 'BEGIN CERTIFICATE' "$c/postgres/postgres.crt")" -ne 2 ]] ||
    ! pg_has_host_sans; then
    # Dev PKI from before D-100/D-104/D-114 (no postgres certificate, one
    # without the ECDSA issuer chain needed for channel binding, or one
    # without the host names for native use): issue it from the existing CA,
    # or (CA with path length 0 or without these names in its name
    # constraints) replace the throwaway dev PKI.
    if (cd "$repo_root" && go run ./cmd/bekup certs rotate --no-audit --dir "$c" --component postgres "${pg_sans[@]}" --leaf-days 90 >/dev/null 2>&1) &&
        pg_has_host_sans; then
        echo "generated ${c#"$repo_root"/}/postgres/"
    else
        echo "dev CA cannot issue the current postgres certificate: regenerating the dev PKI" >&2
        dev_pki_init force
    fi
fi

# --- sample targets, destinations and tunnel endpoints (dev compose only) ------
write "$d/pg_password" "$(rand32)"
write "$d/mysql_root_password" "$(rand32)"
write "$d/mysql_password" "$(rand32)"
write "$d/mariadb_root_password" "$(rand32)"
write "$d/mariadb_password" "$(rand32)"
write "$d/minio_root_user" "bekup-dev-admin"
write "$d/minio_root_password" "$(rand32)"

# SSH client key for the bastion and the SFTP destination (in the real flow
# the UI generates one; this one exists for smoke tests).
if [[ ! -e "$d/bekup_ssh_ed25519" || $force -eq 1 ]]; then
    rm -f "$d/bekup_ssh_ed25519" "$d/bekup_ssh_ed25519.pub"
    ssh-keygen -q -t ed25519 -N '' -C bekup-dev -f "$d/bekup_ssh_ed25519"
    echo "generated ${d#"$repo_root"/}/bekup_ssh_ed25519{,.pub}"
fi
# Stable SFTP host key, so the pinned host key survives container re-creation.
if [[ ! -e "$d/sftp_host_ed25519_key" || $force -eq 1 ]]; then
    rm -f "$d/sftp_host_ed25519_key" "$d/sftp_host_ed25519_key.pub"
    ssh-keygen -q -t ed25519 -N '' -C bekup-dev-sftp -f "$d/sftp_host_ed25519_key"
    echo "generated ${d#"$repo_root"/}/sftp_host_ed25519_key{,.pub}"
fi
chmod 0600 "$d"/*

# --- native bekup on the host (`just run`, D-114) ---------------------------------
# secrets/local/ holds what a bekup process on the host needs to reach the
# dev app Postgres through its published port 127.0.0.1:$BEKUP_DEV_PG_PORT
# (verify-full against the dev CA; the postgres certificate names
# 127.0.0.1): both role URLs with the host address, a copy of the master
# keyring and of the DB CA bundle. Separate copies, so that `--chown` can
# hand the container files to the container UIDs while these stay owned by
# the developer (bekup reads only files owned by itself or root, D-041).
local_url() { # local_url <role> <password file>
    printf 'postgres://%s:%s@127.0.0.1:%s/bekup' "$1" "$(cat "$2")" "$pg_port"
}
if [[ $force -eq 1 || ! -e "$l/bekup_database_url" || ! -e "$l/bekup_database_owner_url" ]] ||
    ! grep -q "@127\.0\.0\.1:${pg_port}/bekup\$" "$l/bekup_database_url" "$l/bekup_database_owner_url" ||
    [[ "$s/bekup_app_password" -nt "$l/bekup_database_url" || "$s/bekup_owner_password" -nt "$l/bekup_database_owner_url" ]]; then
    for f in "$s/bekup_owner_password" "$s/bekup_app_password"; do
        [[ -r "$f" ]] || {
            echo "cannot read ${f#"$repo_root"/} to write ${l#"$repo_root"/}/ (after --chown: re-run with sudo)" >&2
            exit 1
        }
    done
    force_saved=$force
    force=1
    write "$l/bekup_database_owner_url" "$(local_url bekup_owner "$s/bekup_owner_password")"
    write "$l/bekup_database_url" "$(local_url bekup_app "$s/bekup_app_password")"
    force=$force_saved
fi
# copy_local <src> <dst>: refresh dst when it differs from src (never prints
# contents). A src that is not readable (after --chown) keeps an existing dst.
copy_local() {
    if [[ ! -r "$1" ]]; then
        [[ -e "$2" ]] && return 0
        echo "cannot read ${1#"$repo_root"/} to write ${2#"$repo_root"/} (after --chown: re-run with sudo)" >&2
        exit 1
    fi
    if [[ $force -eq 0 && -e "$2" ]] && cmp -s "$1" "$2"; then
        return 0
    fi
    rm -f "$2"
    cat "$1" >"$2"
    chmod 0600 "$2"
    echo "generated ${2#"$repo_root"/}"
}
copy_local "$s/bekup_master_key" "$l/bekup_master_key"
copy_local "$c/postgres/ca.crt" "$l/postgres_ca.crt"

# --- ownership (Linux) -----------------------------------------------------------
if [[ $do_chown -eq 1 ]]; then
    if [[ "$(id -u)" -ne 0 ]]; then
        echo "--chown needs root (run with sudo)" >&2
        exit 1
    fi
    chown 65532:65532 "$s/bekup_master_key" "$s/bekup_database_url" \
        "$s/bekup_database_owner_url" \
        "$d/minio_root_user" "$d/minio_root_password"
    chown -R 65532:65532 "$c/serve" "$c/runner"
    # postgres/: the server key is read by the postgres container (UID 70);
    # its ca.crt is also mounted into serve/migrate, so keep it 0644.
    chown -R 70:70 "$c/postgres"
    chown 70:70 "$s/postgres_password" "$s/bekup_owner_password" \
        "$s/bekup_app_password" "$d/pg_password"
    # The mysql/mariadb entrypoints re-read their *_FILE secrets after
    # dropping to the image's mysql user (UID 999 in both images).
    chown 999:999 "$d/mysql_root_password" "$d/mysql_password" \
        "$d/mariadb_root_password" "$d/mariadb_password"
    # Not secret, but bekup's config file checks (D-041) reject files owned
    # by another user or writable by group/world.
    chown 0:0 "$repo_root/deploy/dev/bekup.dev.toml" "$repo_root/deploy/dev/runner.dev.toml"
    chmod 0644 "$repo_root/deploy/dev/bekup.dev.toml" "$repo_root/deploy/dev/runner.dev.toml"
    # Directories only need to be traversable by the container UIDs; their
    # listings stay private. secrets/certs/ca (CA key) stays 0700 and unshared.
    chmod 0711 "$s" "$c" "$d"
    # secrets/local/ is for the developer's native bekup, not a container.
    if [[ -n "${SUDO_UID:-}" ]]; then
        chown -R "$SUDO_UID:${SUDO_GID:-$SUDO_UID}" "$l"
    else
        echo "note: run --chown through sudo so ${l#"$repo_root"/} stays owned by you" >&2
    fi
    echo "ownership set for container UIDs (65532 bekup/minio, 70 postgres, 999 mysql/mariadb; dev TOMLs root:root 0644)"
elif [[ "$(uname -s)" == "Linux" ]]; then
    echo "note: on Linux, re-run with sudo and --chown so the containers can read their files" >&2
fi

echo "dev files ready in ${s#"$repo_root"/}/ (development only, never production)"
