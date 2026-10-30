#!/bin/sh
# Entrypoint for the `nginx` compose profile (task 0.7b).
#
# Validates the proxy-only environment, renders the site configuration from
# the read-only templates in /etc/nginx/bekup into /tmp/nginx/conf.d (tmpfs),
# runs nginx and reloads it whenever the certificate changes (certbot renews
# it in a separate container that shares only the certificate volume, so no
# container needs the Docker socket or a shared PID namespace).
#
# Environment (compose interpolation; NOT bekup configuration keys):
#   BEKUP_DOMAIN             public host name of the UI (required)
#   PROXY_TLS_MODE           acme (default; certificate from certbot) | static
#   PROXY_HTTPS_PUBLIC_PORT  public HTTPS port for redirects (default 443)
#   PROXY_CERT_CHECK_SECONDS how often to look for a renewed certificate (300)
set -eu

src=/etc/nginx/bekup
out=/tmp/nginx/conf.d

log() { echo "nginx-entrypoint: $*" >&2; }
die() { log "$*"; exit 64; }

domain="${BEKUP_DOMAIN:-}"
mode="${PROXY_TLS_MODE:-acme}"
port="${PROXY_HTTPS_PUBLIC_PORT:-443}"
interval="${PROXY_CERT_CHECK_SECONDS:-300}"

if ! { printf '%s' "$domain" | grep -Eqx '([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?' &&
    [ "${#domain}" -le 253 ]; }; then
    die "BEKUP_DOMAIN must be a DNS host name such as backup.example.com (got '${domain}')"
fi
if ! { printf '%s' "$port" | grep -Eqx '[1-9][0-9]{0,4}' && [ "$port" -le 65535 ]; }; then
    die "PROXY_HTTPS_PUBLIC_PORT must be a port number"
fi
printf '%s' "$interval" | grep -Eqx '[1-9][0-9]{0,5}' \
    || die "PROXY_CERT_CHECK_SECONDS must be a positive number"

case "$mode" in
    acme)
        cert=/etc/letsencrypt/live/bekup/fullchain.pem
        key=/etc/letsencrypt/live/bekup/privkey.pem
        ;;
    static)
        cert=/certs/tls.crt
        key=/certs/tls.key
        if ! { [ -r "$cert" ] && [ -r "$key" ]; }; then
            die "static mode needs /certs/tls.crt and /certs/tls.key (readable by UID $(id -u))"
        fi
        ;;
    *) die "PROXY_TLS_MODE must be 'acme' or 'static'" ;;
esac

if [ "$port" = 443 ]; then suffix=""; else suffix=":$port"; fi
export BEKUP_DOMAIN="$domain" TLS_CERT="$cert" TLS_KEY="$key" \
    HTTPS_PUBLIC_PORT="$port" HTTPS_PORT_SUFFIX="$suffix"

have_cert() { [ -r "$cert" ] && [ -r "$key" ]; }
# Changes whenever certbot writes a new certificate or key.
fingerprint() { if have_cert; then cat "$cert" "$key" | sha256sum; else echo none; fi; }

# render: writes the site configuration for the current certificate state.
render() {
    if have_cert; then tmpl="$src/bekup-https.conf.tmpl"; else tmpl="$src/bekup-http-only.conf.tmpl"; fi
    # shellcheck disable=SC2016 # the variable list is for envsubst, not the shell
    envsubst '${BEKUP_DOMAIN} ${TLS_CERT} ${TLS_KEY} ${HTTPS_PUBLIC_PORT} ${HTTPS_PORT_SUFFIX}' \
        <"$tmpl" >"$out/bekup.conf"
}

umask 077
mkdir -p "$out"
render
current="$(fingerprint)"
if have_cert; then
    log "serving https://${domain}${suffix} (TLS mode: ${mode})"
else
    log "no certificate yet: serving only the ACME challenge on port 80 until certbot obtains one"
fi
nginx -t -q

nginx -g 'daemon off;' &
nginx_pid=$!
sleep_pid=""

stop() {
    log "stopping"
    if [ -n "$sleep_pid" ]; then kill "$sleep_pid" 2>/dev/null || true; fi
    nginx -s quit 2>/dev/null || kill -TERM "$nginx_pid" 2>/dev/null || true
    wait "$nginx_pid" 2>/dev/null || true
    exit 0
}
trap stop TERM INT QUIT

while kill -0 "$nginx_pid" 2>/dev/null; do
    sleep "$interval" &
    sleep_pid=$!
    wait "$sleep_pid" || true
    sleep_pid=""
    next="$(fingerprint)"
    [ "$next" = "$current" ] && continue
    cp "$out/bekup.conf" /tmp/nginx/bekup.conf.prev
    render
    if nginx -t -q; then
        nginx -s reload
        current="$next"
        log "certificate changed: configuration reloaded"
    else
        log "new certificate/configuration rejected by nginx -t; keeping the previous one"
        cp /tmp/nginx/bekup.conf.prev "$out/bekup.conf"
    fi
done

wait "$nginx_pid"
