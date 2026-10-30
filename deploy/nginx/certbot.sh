#!/bin/sh
# ACME companion for the `nginx` compose profile (task 0.7b).
#
# Obtains and renews the certificate for BEKUP_DOMAIN with certbot's webroot
# (HTTP-01) method. It runs as UID 65532, has no inbound ports, no Docker
# socket and shares only two volumes with nginx: the challenge webroot and
# the certificate directory. nginx notices a new certificate by itself
# (deploy/nginx/entrypoint.sh) and reloads.
#
# Environment (compose interpolation; NOT bekup configuration keys):
#   BEKUP_DOMAIN    public host name of the UI (required)
#   ACME_EMAIL      ACME account e-mail (required)
#   ACME_CA_SERVER  ACME directory URL (default: Let's Encrypt production)
set -eu

log() { echo "certbot-loop: $*" >&2; }
die() { log "$*"; exit 64; }

domain="${BEKUP_DOMAIN:-}"
email="${ACME_EMAIL:-}"
ca="${ACME_CA_SERVER:-https://acme-v02.api.letsencrypt.org/directory}"

if ! { printf '%s' "$domain" | grep -Eqx '([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?' &&
    [ "${#domain}" -le 253 ]; }; then
    die "BEKUP_DOMAIN must be a DNS host name such as backup.example.com (got '${domain}')"
fi
printf '%s' "$email" | grep -Eqx '[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\.)+[A-Za-z0-9-]+' \
    || die "ACME_EMAIL must be an e-mail address"
printf '%s' "$ca" | grep -Eqx 'https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?' \
    || die "ACME_CA_SERVER must be an https URL"

export HOME=/tmp
umask 077
mkdir -p /tmp/certbot/work /tmp/certbot/logs

sleep_pid=""
trap '[ -n "$sleep_pid" ] && kill "$sleep_pid" 2>/dev/null; exit 0' TERM INT QUIT
pause() {
    sleep "$1" &
    sleep_pid=$!
    wait "$sleep_pid" || true
    sleep_pid=""
}

# Backoff after failures stays well below Let's Encrypt's limit of 5 failed
# validations per hostname and hour.
delay=60
while :; do
    # --keep-until-expiring: issues on first run (or when BEKUP_DOMAIN
    # changed) and renews only when due (certbot follows ACME ARI hints).
    if certbot certonly \
        --non-interactive --agree-tos --no-eff-email \
        --email "$email" --server "$ca" \
        --config-dir /etc/letsencrypt \
        --work-dir /tmp/certbot/work --logs-dir /tmp/certbot/logs \
        --webroot --webroot-path /var/www/acme \
        --cert-name bekup -d "$domain" \
        --key-type ecdsa --elliptic-curve secp256r1 \
        --keep-until-expiring; then
        delay=60
        pause 43200
    else
        log "certificate request failed; retrying in ${delay}s"
        pause "$delay"
        delay=$((delay * 4))
        if [ "$delay" -gt 3600 ]; then delay=3600; fi
    fi
done
