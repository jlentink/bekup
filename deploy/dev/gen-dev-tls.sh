#!/usr/bin/env bash
# Generates a DEVELOPMENT TLS certificate for the reverse-proxy profiles
# (traefik | nginx) in docker-compose.dev.yml (task 0.7b). Never use it in
# production: there the proxy obtains certificates from Let's Encrypt.
#
# Usage: deploy/dev/gen-dev-tls.sh [--force] [--chown] [--install-ca]
#                                  [--out DIR] [--domain NAME]
#   --out DIR      output directory (default ./secrets/dev/tls; must match
#                  BEKUP_DEV_TLS_DIR if you change it)
#   --domain NAME  host name (default bekup.localhost; *.localhost always
#                  resolves to loopback in browsers and curl)
#   --force        overwrite an existing certificate
#   --install-ca   run `mkcert -install` first (adds mkcert's local CA to your
#                  system/browser trust stores; may ask for your password)
#   --chown        (Linux, needs root) chown the files to UID 65532, the user
#                  both proxies run as. Docker Desktop does not need this.
#
# Uses mkcert when installed (certificate trusted once `mkcert -install` has
# run). Without mkcert it falls back to a self-signed openssl certificate
# that browsers will warn about (use `curl --cacert <out>/tls.crt`).
# Writes <out>/tls.crt (0644) and <out>/tls.key (0600) in a 0700 directory;
# ECDSA P-256 keys. The key is never printed.
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out="$repo_root/secrets/dev/tls"
domain="bekup.localhost"
force=0
do_chown=0
install_ca=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --out) out="${2:?--out needs a directory}"; shift 2 ;;
        --domain) domain="${2:?--domain needs a name}"; shift 2 ;;
        --force) force=1; shift ;;
        --chown) do_chown=1; shift ;;
        --install-ca) install_ca=1; shift ;;
        -h | --help) sed -n '2,23p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

if ! [[ "$domain" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]]; then
    echo "invalid --domain: $domain" >&2
    exit 2
fi

crt="$out/tls.crt"
key="$out/tls.key"
if [[ -e "$crt" && -e "$key" && $force -eq 0 ]]; then
    echo "dev TLS certificate exists: $crt (use --force to regenerate)"
    exit 0
fi

mkdir -p "$out"
chmod 0700 "$out"
tmp="$(mktemp -d "$out/.tmp.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

if command -v mkcert >/dev/null 2>&1; then
    if [[ $install_ca -eq 1 ]]; then
        mkcert -install
    elif [[ ! -f "$(mkcert -CAROOT)/rootCA.pem" ]]; then
        echo "note: mkcert has no local CA yet; run 'mkcert -install' (or rerun with --install-ca) so browsers trust the certificate" >&2
    fi
    mkcert -ecdsa -cert-file "$tmp/tls.crt" -key-file "$tmp/tls.key" "$domain" >/dev/null 2>&1
    how="mkcert (CA: $(mkcert -CAROOT)/rootCA.pem)"
else
    command -v openssl >/dev/null 2>&1 || { echo "neither mkcert nor openssl is installed" >&2; exit 1; }
    echo "warning: mkcert not found (optional tool, see 'just doctor'); creating a SELF-SIGNED certificate instead" >&2
    openssl req -x509 -new -nodes -sha256 -days 30 \
        -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
        -subj "/CN=$domain" \
        -addext "subjectAltName=DNS:$domain" \
        -addext "basicConstraints=critical,CA:FALSE" \
        -addext "keyUsage=critical,digitalSignature" \
        -addext "extendedKeyUsage=serverAuth" \
        -keyout "$tmp/tls.key" -out "$tmp/tls.crt" 2>/dev/null
    how="openssl self-signed, 30 days (trust it explicitly, e.g. curl --cacert $crt)"
fi

chmod 0600 "$tmp/tls.key"
chmod 0644 "$tmp/tls.crt"
mv -f "$tmp/tls.key" "$key"
mv -f "$tmp/tls.crt" "$crt"

if [[ $do_chown -eq 1 ]]; then
    chown 65532:65532 "$out" "$crt" "$key"
fi

echo "dev TLS certificate for $domain written to $out via $how"
