# Reverse proxy and TLS

bekup never terminates TLS for the web UI itself (D-017). `bekup serve`
speaks plain HTTP on the internal `frontend` Docker network, and its port is
never published. A reverse proxy in front of it handles HTTPS, certificates,
HSTS and the HTTP→HTTPS redirect. bekup still sends HSTS, its CSP and the
other security headers itself, as defence in depth (D-048, D-082).

`docker-compose.yml` ships two proxies as compose **profiles**. Enable
**exactly one**:

| Profile | Services | Certificates | Docker socket |
|---|---|---|---|
| `traefik` | `traefik-init` (one-shot), `traefik` (v3) | Let's Encrypt, built into Traefik (HTTP-01) | **no** (file provider) |
| `nginx` | `nginx-init` (one-shot), `nginx` (unprivileged image), `certbot` | Let's Encrypt via certbot (webroot, HTTP-01) | **no** |

Neither profile mounts the Docker socket. Traefik's Docker provider and
nginx-proxy/acme-companion would both need it, and socket access is
root-equivalent on the host, so both profiles use static configuration
instead (D-093, D-094).

```
internet ──80/443──> traefik | nginx ──(frontend, internal)──> serve:8080
                       │  (edge network: published ports, ACME)
                       └─ certbot (nginx profile only; outbound ACME only)
```

## Quick start (production)

1. Point a DNS `A`/`AAAA` record for your host name (for example
   `backup.example.com`) at the Docker host. Ports **80 and 443** must be
   reachable from the internet: Let's Encrypt validates over port 80
   (HTTP-01), and all other port-80 traffic is redirected to HTTPS.
2. Set the proxy variables in your shell or in a `.env` file next to
   `docker-compose.yml`:
   ```sh
   BEKUP_DOMAIN=backup.example.com
   ACME_EMAIL=ops@example.com
   ```
3. In `bekup.toml`, set the public URL and trust only the proxy:
   ```toml
   [server]
   listen = "10.213.250.10:8080"           # serve's frontend address only
   base_url = "https://backup.example.com"
   trusted_proxies = ["10.213.250.5/32"]   # the proxy's fixed address
   ```
4. Start the stack with one profile:
   ```sh
   docker compose --profile traefik up -d     # or: --profile nginx
   ```
5. Check it from outside: `curl -I https://backup.example.com` and an
   external TLS scan (see the [checklist](#security-checklist)).

While testing, use the Let's Encrypt **staging** CA to stay clear of its rate
limits, then remove the variable and the certificate volume
(`traefik-acme` or `nginx-certs`) before going live:

```sh
ACME_CA_SERVER=https://acme-staging-v02.api.letsencrypt.org/directory
```

## Proxy environment variables

These are **compose variables read only by the proxy containers**, not bekup
configuration keys. bekup itself never reads them.

| Variable | Profiles | Default | Meaning |
|---|---|---|---|
| `BEKUP_DOMAIN` | both | none (required) | public host name of the UI; must match `base_url` |
| `ACME_EMAIL` | both (ACME) | none (required) | Let's Encrypt account e-mail |
| `ACME_CA_SERVER` | both (ACME) | Let's Encrypt production | ACME directory URL (https only), for example the staging CA |
| `PROXY_TLS_MODE` | both | `acme` | `static` uses your own certificate mounted at `/certs/tls.crt` + `/certs/tls.key` (internal PKI, dev) |
| `PROXY_FRONTEND_IP` | both | `10.213.250.5` | the proxy's fixed address on `frontend` |
| `FRONTEND_SUBNET`, `SERVE_FRONTEND_IP` | both | `10.213.250.0/24`, `10.213.250.10` | see [compose.md](compose.md) |

The entrypoint scripts validate every value against a strict pattern (DNS
name, e-mail, https URL) before they write it into the configuration, and
refuse to start otherwise.

## Trusted proxies

bekup uses `X-Forwarded-For` / `X-Forwarded-Proto` only from the addresses in
`[server] trusted_proxies` (`BEKUP_SERVER_TRUSTED_PROXIES`), which is required
outside dev mode (D-048). Both profiles give the proxy the fixed address
`10.213.250.5` on `frontend`, so trust exactly that address:

```toml
trusted_proxies = ["10.213.250.5/32"]
```

Don't trust the whole `frontend` subnet. If you change `PROXY_FRONTEND_IP` or
`FRONTEND_SUBNET`, update `trusted_proxies` to match.

Both proxies **overwrite** the forwarding headers rather than appending to
them, so a client can't inject an address:

- Traefik trusts no incoming `X-Forwarded-*` (no `forwardedHeaders`
  configuration), removes header names that alias them (`X_Forwarded_For`,
  `aliasHeadersStrategy: delete`) and drops `Forwarded`.
- nginx sets `X-Forwarded-For $remote_addr`, `X-Forwarded-Proto https` and
  `X-Real-IP`, clears `Forwarded`, and drops header names with underscores
  (nginx default).

If you put another load balancer or CDN in front of the proxy, the proxy then
sees that device's address. Configure the proxy to trust that device's
forwarded headers (Traefik `entryPoints.websecure.forwardedHeaders.trustedIPs`,
nginx `real_ip` module). Never trust arbitrary sources.

## What the shipped configurations do

Both profiles enforce the same baseline:

| Area | Setting |
|---|---|
| Protocols | TLS 1.2 and 1.3 only |
| Ciphers (TLS 1.2) | ECDHE + AEAD only: AES-GCM and ChaCha20-Poly1305 (Mozilla *intermediate* without CBC and DHE). TLS 1.3 suites are all AEAD |
| Key exchange | `X25519MLKEM768` (hybrid post-quantum) first, then X25519, P-256, P-384 |
| Unknown SNI or IP address | handshake rejected (Traefik `sniStrict`, nginx `ssl_reject_handshake`); no certificate is shown to scanners |
| Session tickets | off in nginx; Traefik (Go) rotates its ticket keys automatically |
| HTTP | port 80 serves only the ACME challenge and a permanent redirect to `https://BEKUP_DOMAIN` (unknown hosts: nginx closes the connection) |
| HSTS | `max-age=63072000; includeSubDomains; preload` (2 years, preload-ready) |
| Other headers | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`; the upstream copies are replaced, never duplicated. The CSP comes from bekup only (D-082) |
| Request bodies | 2 MiB max at the proxy (`413`); bekup enforces `[server] max_body_bytes` (1 MiB) itself |
| Rate limits | light, per client IP: 50 req/s (burst 100), 100 concurrent requests/connections. bekup adds strict limits for logins and sensitive mutations |
| WebSockets | upgrade passthrough for GraphQL subscriptions on `/graphql` |
| Downloads | responses are streamed, not buffered; no total response timeout; 1 h idle read timeout from serve |
| Logging | Traefik: no access log. nginx: JSON access log without query strings and with `/api/download/{ticket}` redacted |
| Containers | UID 65532, read-only root, `cap_drop: ALL` (ports 8080/8443 inside, mapped to 80/443), `no-new-privileges`, resource limits |
| State | Traefik: `acme.json` (0600) in volume `traefik-acme`. nginx: volume `nginx-certs` (certbot writes, nginx reads only) and `nginx-acme-webroot` |

### Traefik (`deploy/traefik/`)

- `traefik.yml.tmpl`: static configuration (entrypoints, redirect, timeouts,
  ping). `static-acme.yml.tmpl` adds the Let's Encrypt resolver in ACME mode.
- `dynamic/bekup.yml.tmpl`: routers, middlewares (headers, rate limit,
  in-flight limit, body limit) and the service `http://serve:8080`.
- `dynamic/tls.yml`: TLS options. `dynamic/static-certs.yml`: static mode
  only.
- `entrypoint.sh`: validates the variables, renders the templates into
  `/tmp/traefik` (tmpfs) and starts Traefik. Traefik doesn't expand
  environment variables in its static file, which is why the templates exist.

Traefik's only body-size control (the `buffering` middleware) also buffers
the response, so it applies only to `POST`/`PUT`/`PATCH`/`DELETE` requests.
Downloads and WebSockets (both `GET`) are never buffered. Encoded `/`, `\`,
`%`, `?`, `#`, `;` and NUL in paths are rejected, which rules out path
confusion between proxy and backend.

### nginx (`deploy/nginx/`)

- `nginx.conf`: main configuration (TLS, limits, logging, resolver).
- `bekup-https.conf.tmpl`: the site. `bekup-http-only.conf.tmpl`: used only
  before the first certificate exists, and serves nothing but the ACME
  challenge.
- `entrypoint.sh`: validates the variables, renders the site with `envsubst`,
  runs nginx and checks every 5 minutes whether the certificate changed. If
  it did, the script runs `nginx -t` and reloads nginx, or keeps the old
  configuration when the test fails.
- `certbot.sh`: runs `certbot certonly --webroot --keep-until-expiring`
  every 12 hours (issuance, renewal and `BEKUP_DOMAIN` changes), with backoff
  after failures.

On first start, nginx answers `503` on port 80 and has no HTTPS listener
until certbot has obtained the certificate (usually within a minute).

## Without a proxy: plain HTTP (D-095)

The proxy profiles are **opt-in**. Without one, `serve` speaks plain HTTP:

- **Dev (the default `just dev` stack):** `docker-compose.dev.yml`
  publishes `serve` on `127.0.0.1:8080` (loopback only) with
  `base_url = "http://localhost:8080"`. Dev mode accepts the http
  `base_url` and an empty `trusted_proxies`. Nothing else is needed.
- **Production without a proxy:** only with an explicit
  `[server] allow_insecure_http = true`, plus a `ports:` mapping for `serve`
  in your own override. Then sessions, passwords and backups downloaded
  through the UI travel **unencrypted**. Use this only on a trusted,
  isolated network, never across the internet.

Whenever the effective `base_url` is not https, bekup makes it visible
(D-095; implemented in tasks 1.1, 1.5, 5.5 and 6.1):

- a WARN log line at every startup;
- a permanent, non-dismissable red banner in the UI: "Connection is not
  encrypted (no HTTPS)";
- outside dev mode, a `security.system` alert to admins and an audit entry;
- the session cookie drops the `__Host-` prefix and the `Secure` flag (they
  require https), and no HSTS header is sent;
- passkeys work only in a secure context (https or `localhost`); elsewhere
  the UI hides passkey enrollment and TOTP remains available.

## Development (local TLS)

Dev mode uses a static local certificate instead of ACME. Both proxy profiles
work with the dev override; certbot is disabled there.

```sh
deploy/dev/gen-dev-tls.sh              # mkcert if installed, else self-signed
mkcert -install                        # once, so browsers trust mkcert's CA
BEKUP_DEV_BASE_URL=https://bekup.localhost:8443 \
  docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile traefik up
# https://bekup.localhost:8443  (http://bekup.localhost:8000 redirects)
```

- `deploy/dev/gen-dev-tls.sh` writes `secrets/dev/tls/tls.crt` and `tls.key`
  (0600) for `bekup.localhost`, an ECDSA P-256 key. It uses
  [mkcert](https://github.com/FiloSottile/mkcert) when installed (`just
  doctor` lists it as optional) and otherwise falls back to a 30-day
  self-signed certificate (`curl --cacert secrets/dev/tls/tls.crt …`). Options:
  `--install-ca`, `--force`, `--out DIR` (then set `BEKUP_DEV_TLS_DIR`),
  `--domain NAME`, and `--chown` (Linux: the proxies run as UID 65532).
- `*.localhost` resolves to the loopback address in browsers and curl, so you
  don't need an `/etc/hosts` entry.
- The proxy ports bind to `127.0.0.1:8443` (HTTPS) and `127.0.0.1:8000`
  (HTTP) only. In dev, `serve` also stays published on `127.0.0.1:8080` for
  direct access (D-081). In production it is never published.
- `deploy/dev/bekup.dev.toml` already trusts `10.213.250.5/32`.
  `BEKUP_DEV_BASE_URL` sets serve's `base_url` so that origin checks match
  the proxied URL.

## Using your own reverse proxy

If you already run a proxy, skip the profiles and connect your proxy to
bekup's `frontend` network (or run bekup's `serve` where your proxy can reach
it). Then set `trusted_proxies` to your proxy's address. Requirements:

- TLS 1.2+ only, AEAD ciphers, HSTS, and an HTTP→HTTPS redirect;
- overwrite (never append) `X-Forwarded-For`, and set `X-Forwarded-Proto: https`;
- pass WebSocket upgrades on `/graphql`;
- don't buffer responses (streamed downloads) and set no total response
  timeout;
- limit request bodies to a few MiB;
- don't log query strings, and redact `/api/download/*` paths.

### Generic nginx

```nginx
map $http_upgrade $connection_upgrade { default upgrade; "" ""; }

server {
    listen 80;
    server_name backup.example.com;
    location / { return 308 https://backup.example.com$request_uri; }
}

server {
    listen 443 ssl;
    http2 on;
    server_name backup.example.com;

    ssl_certificate     /etc/ssl/bekup/fullchain.pem;
    ssl_certificate_key /etc/ssl/bekup/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
    ssl_prefer_server_ciphers off;
    ssl_session_tickets off;

    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    client_max_body_size 2m;

    location / {
        proxy_pass http://10.213.250.10:8080;   # serve on the frontend network
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Forwarded         "";
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        $connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

### Caddy

Caddy obtains certificates automatically and uses TLS 1.2+ with modern
ciphers by default. Its `reverse_proxy` handles WebSockets and streams
responses. Caddy **ignores** incoming `X-Forwarded-*` unless you set
`trusted_proxies`, so leave that unset unless something sits in front of it.

```caddyfile
backup.example.com {
    request_body {
        max_size 2MB
    }
    header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload"
    reverse_proxy 10.213.250.10:8080 {
        header_up -Forwarded
        flush_interval -1
    }
    log {
        # Do not log query strings or download tickets.
        format filter {
            fields {
                request>uri regexp ^/api/download/.* /api/download/[redacted]
            }
        }
    }
}
```

## Security checklist

- [ ] Exactly one proxy profile is enabled. `docker compose ps` shows no
      published port on `serve`, `runner` or `postgres`.
- [ ] `trusted_proxies` names only the proxy's address (`/32`), not a subnet
      and never `0.0.0.0/0`.
- [ ] `base_url` is `https://` and equals `https://BEKUP_DOMAIN`.
- [ ] An external scan (`testssl.sh https://BEKUP_DOMAIN`, or SSL Labs) shows
      only TLS 1.2/1.3, only forward-secret AEAD ciphers, and HSTS.
      Locally: `docker run --rm drwetter/testssl.sh:3.2 https://BEKUP_DOMAIN`.
- [ ] `http://BEKUP_DOMAIN` redirects to HTTPS. A request to the host's IP
      address gets no certificate (handshake rejected).
- [ ] Back up the volume `traefik-acme` or `nginx-certs` like a secret: it
      holds the TLS private key and the ACME account key.
- [ ] Submit the domain to the HSTS preload list only when **every**
      subdomain of the registrable domain serves HTTPS.
- [ ] Renovate keeps the proxy images' digests current. Apply proxy updates
      promptly, since this is the internet-facing component.
- [ ] If you change `FRONTEND_SUBNET`/`PROXY_FRONTEND_IP`, update
      `trusted_proxies` in the same change.
- [ ] Linux: the host's root user can reach container addresses on Docker
      bridges directly. Bind `serve` to its `frontend` address
      (`listen = "10.213.250.10:8080"`) so it isn't listening on the other
      networks, and keep host access restricted.
