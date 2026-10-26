#!/bin/sh
# Entrypoint for the `traefik` compose profile (task 0.7b).
#
# Traefik does not expand environment variables in its static configuration
# file, so this script validates the proxy-only environment variables and
# renders the templates from /etc/traefik/bekup (read-only) into
# /tmp/traefik (tmpfs), then execs Traefik. Values are validated against
# strict patterns BEFORE they are substituted, so they cannot inject YAML or
# sed syntax.
#
# Environment (compose interpolation; NOT bekup configuration keys):
#   BEKUP_DOMAIN             public host name of the UI (required)
#   PROXY_TLS_MODE           acme (default) | static
#   ACME_EMAIL               ACME account e-mail (required in acme mode)
#   ACME_CA_SERVER           ACME directory URL (default: Let's Encrypt prod)
#   PROXY_HTTPS_PUBLIC_PORT  public HTTPS port for the redirect (default 443)
set -eu

src=/etc/traefik/bekup
out=/tmp/traefik

die() { echo "traefik-entrypoint: $*" >&2; exit 64; }

domain="${BEKUP_DOMAIN:-}"
mode="${PROXY_TLS_MODE:-acme}"
email="${ACME_EMAIL:-}"
ca="${ACME_CA_SERVER:-https://acme-v02.api.letsencrypt.org/directory}"
port="${PROXY_HTTPS_PUBLIC_PORT:-443}"

# DNS name: labels of [a-z0-9-], at least one dot, max 253 characters.
printf '%s' "$domain" | grep -Eqx '([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?' \
    && [ "${#domain}" -le 253 ] \
    || die "BEKUP_DOMAIN must be a DNS host name such as backup.example.com (got '${domain}')"
printf '%s' "$port" | grep -Eqx '[1-9][0-9]{0,4}' && [ "$port" -le 65535 ] \
    || die "PROXY_HTTPS_PUBLIC_PORT must be a port number"

case "$mode" in
    acme)
        printf '%s' "$email" | grep -Eqx '[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\.)+[A-Za-z0-9-]+' \
            || die "ACME_EMAIL must be an e-mail address in acme mode"
        printf '%s' "$ca" | grep -Eqx 'https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?' \
            || die "ACME_CA_SERVER must be an https URL"
        ;;
    static)
        [ -r /certs/tls.crt ] && [ -r /certs/tls.key ] \
            || die "static mode needs /certs/tls.crt and /certs/tls.key (readable by UID $(id -u))"
        ;;
    *) die "PROXY_TLS_MODE must be 'acme' or 'static'" ;;
esac

umask 077
rm -rf "$out"
mkdir -p "$out/dynamic"

if [ "$mode" = acme ]; then
    resolver='certResolver: letsencrypt'
else
    resolver='# static certificate: dynamic/static-certs.yml'
fi

# Static configuration.
sed -e "s|__HTTPS_PUBLIC_PORT__|${port}|g" \
    -e "s|# __CERT_RESOLVER__|${resolver}|" \
    "$src/traefik.yml.tmpl" >"$out/traefik.yml"
if [ "$mode" = acme ]; then
    sed -e "s|__ACME_EMAIL__|${email}|g" -e "s|__ACME_CA_SERVER__|${ca}|g" \
        "$src/static-acme.yml.tmpl" >>"$out/traefik.yml"
fi

# Dynamic configuration (file provider).
sed -e "s|__BEKUP_DOMAIN__|${domain}|g" "$src/dynamic/bekup.yml.tmpl" >"$out/dynamic/bekup.yml"
cp "$src/dynamic/tls.yml" "$out/dynamic/tls.yml"
if [ "$mode" = static ]; then
    cp "$src/dynamic/static-certs.yml" "$out/dynamic/static-certs.yml"
fi

echo "traefik-entrypoint: serving https://${domain} (TLS mode: ${mode})"
exec traefik --configFile="$out/traefik.yml"
