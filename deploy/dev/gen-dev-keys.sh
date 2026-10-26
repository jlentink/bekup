#!/usr/bin/env bash
# Generates DEVELOPMENT keys, passwords and certificates for
# docker-compose.yml + docker-compose.dev.yml into ./secrets/ (gitignored).
# Never use these files in production.
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
        -h | --help) sed -n '2,21p' "$0"; exit 0 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

for cmd in go ssh-keygen head base64 tr grep; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "missing required command: $cmd" >&2; exit 1; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
s="$repo_root/secrets"
d="$s/dev"
c="$s/certs"
mkdir -p "$s" "$d" "$c"
chmod 0700 "$s" "$d" "$c"

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
# sslmode=disable only because `backend` is an internal network and this is
# dev; see docs/installation/compose.md.
if [[ ! -e "$s/bekup_database_url" || $force -eq 1 ]]; then
    force_saved=$force
    force=1
    write "$s/bekup_database_owner_url" \
        "postgres://bekup_owner:$(cat "$s/bekup_owner_password")@postgres:5432/bekup?sslmode=disable"
    write "$s/bekup_database_url" \
        "postgres://bekup_app:$(cat "$s/bekup_app_password")@postgres:5432/bekup?sslmode=disable"
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
    echo "generated secrets/bekup_master_key"
fi

# --- mTLS certificates -----------------------------------------------------------
# `bekup certs init` (task 0.6) with an UNENCRYPTED CA key and 90-day leaves:
# acceptable only for this throwaway dev PKI. Production uses
# `docker compose --profile setup run --rm certs-init` (passphrase-protected
# CA key, moved offline afterwards; docs/security/mtls-certs.md). Layout:
# secrets/certs/{ca/ca.{crt,key}, runner/runner.{crt,key}+ca.crt,
# serve/serve.{crt,key}+ca.crt}; identities as in the config defaults.
if [[ ! -e "$c/ca/ca.crt" || $force -eq 1 ]]; then
    init_args=(--dir "$c" --component runner --component serve --unencrypted-ca-key --leaf-days 90)
    if [[ $force -eq 1 ]]; then
        init_args+=(--force)
    fi
    if ! out=$(cd "$repo_root" && go run ./cmd/bekup certs init "${init_args[@]}" 2>&1); then
        printf '%s\n' "$out" >&2
        exit 1
    fi
    echo "generated secrets/certs/{ca,runner,serve}/ (dev CA key is unencrypted)"
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
    echo "generated secrets/dev/bekup_ssh_ed25519{,.pub}"
fi
# Stable SFTP host key, so the pinned host key survives container re-creation.
if [[ ! -e "$d/sftp_host_ed25519_key" || $force -eq 1 ]]; then
    rm -f "$d/sftp_host_ed25519_key" "$d/sftp_host_ed25519_key.pub"
    ssh-keygen -q -t ed25519 -N '' -C bekup-dev-sftp -f "$d/sftp_host_ed25519_key"
    echo "generated secrets/dev/sftp_host_ed25519_key{,.pub}"
fi
chmod 0600 "$d"/*

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
    echo "ownership set for container UIDs (65532 bekup/minio, 70 postgres, 999 mysql/mariadb; dev TOMLs root:root 0644)"
elif [[ "$(uname -s)" == "Linux" ]]; then
    echo "note: on Linux, re-run with sudo and --chown so the containers can read their files" >&2
fi

echo "dev files ready in secrets/ (development only, never production)"
