# Reverse proxy and TLS

bekup never terminates TLS for the web UI itself (D-017). `bekup serve`
speaks plain HTTP on the internal `frontend` Docker network, and its port is
never published. A reverse proxy in front of it handles HTTPS, certificates,
HSTS and the HTTP→HTTPS redirect. bekup still sends HSTS, its CSP and the
other security headers itself, as defence in depth (D-048, D-082).

Pick the compose variant that matches your proxy (D-205,
[compose.md](compose.md)):

| Variant (release asset) | Proxy | Section |
|---|---|---|
| `traefik` (`compose.yaml`) | bekup's own Traefik, started with the stack | [Quick start](#quick-start-production) |
| `traefik-external` (`compose.traefik-external.yaml`) | your existing Traefik (Docker provider, labels on `serve`) | [Existing Traefik](#existing-traefik) |
| `npm` (`compose.npm.yaml`) | Nginx Proxy Manager | [Nginx Proxy Manager](#nginx-proxy-manager) |
| `full` (setup-first) | profile `traefik`, `nginx`, or your own proxy | below, [Using your own reverse proxy](#using-your-own-reverse-proxy) |

Each release variant is also committed in
[`deploy/compose/`](../../deploy/compose/) under the same file name (raw
URL `https://raw.githubusercontent.com/jlentink/bekup/main/deploy/compose/<file>`),
with the image taken from `BEKUP_IMAGE` instead of a pinned literal
([compose.md](compose.md#install-with-bekup-setup-container-only)).

The repository's `docker-compose.yml` (the `full` variant) ships two
proxies as compose **profiles**. Enable **exactly one**:

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

With `bekup setup` (the container-only install,
[compose.md](compose.md#install-with-bekup-setup-container-only)) steps 2–4
below are done for you. In the `traefik` variant Traefik has no profile and
starts with `docker compose up -d`; setup asks for the domain and the ACME
e-mail (`--tls-mode static`, `--acme-staging` while testing) and writes
`BEKUP_DOMAIN`, `PROXY_TLS_MODE`, `ACME_EMAIL` and `ACME_CA_SERVER` to
`.env` and `base_url` and `trusted_proxies` to `bekup.toml`.
`--tls-mode static` also writes the `compose.override.yaml` below with
`./proxy-tls` as the certificate directory. In the setup-first `full`
variant `--proxy traefik|nginx` selects the profile through
`COMPOSE_PROFILES` in `.env`; `--proxy none` starts no proxy: attach your
own to the `frontend` network with a fixed address other than bekup's
(`PROXY_FRONTEND_IP` and `SERVE_FRONTEND_IP` stay reserved; the lint
refuses them) and pass it as `--trusted-proxy`. `bekup setup check` verifies that `.env` and
`bekup.toml` agree.

Without setup (source checkout):

1. Point a DNS `A`/`AAAA` record for your host name (for example
   `backup.example.com`) at the Docker host. Ports **80 and 443** must be
   reachable from the internet: Let's Encrypt validates over port 80
   (HTTP-01), and all other port-80 traffic is redirected to HTTPS.
2. Set the proxy variables in a `.env` file next to the compose file
   (a variable set in the shell overrides `.env`):
   ```sh
   COMPOSE_PROFILES=traefik      # or nginx; selects the proxy profile
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
   docker compose up -d     # COMPOSE_PROFILES from .env; or: --profile traefik
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
| `COMPOSE_PROFILES` | – | none | `traefik` or `nginx`: selects the proxy profile (compose itself reads it) |
| `PROXY_FRONTEND_IP` | both | `10.213.250.5` | the proxy's fixed address on `frontend` |
| `FRONTEND_SUBNET`, `SERVE_FRONTEND_IP` | both | `10.213.250.0/24`, `10.213.250.10` | see [compose.md](compose.md) |

The entrypoint scripts validate every value against a strict pattern (DNS
name, e-mail, https URL) before they write it into the configuration, and
refuse to start otherwise.

### Own certificate (static mode)

`PROXY_TLS_MODE=static` needs the certificate chain and key at
`/certs/tls.crt` and `/certs/tls.key` inside the proxy container (readable
by UID 65532; key `0600`). The compose file does not mount `/certs`
itself, so add it in a `compose.override.yaml` next to it (compose
reads that file automatically; `bekup setup init --tls-mode static` writes
it with `source: ./proxy-tls` and sets `PROXY_TLS_MODE=static` in `.env`):

```yaml
services:
  traefik:            # or nginx (then also disable certbot, see below)
    environment:
      PROXY_TLS_MODE: static
    volumes:
      - type: bind
        source: ./proxy-tls     # tls.crt + tls.key; exactly ./proxy-tls, read-only (the lint refuses anything else)
        target: /certs
        read_only: true
  certbot:            # nginx profile only: no ACME in static mode
    profiles: !override [disabled]
```

To publish the proxy on other host ports or addresses (for example
`127.0.0.1:8443`), override `ports` with `!override` the same way and set
`PROXY_HTTPS_PUBLIC_PORT` in the proxy's `environment` to the public HTTPS
port, so the HTTP→HTTPS redirect points at it.

These are the only proxy changes `setup check` accepts on the shipped
`traefik`/`nginx` services: `PROXY_TLS_MODE` and `PROXY_HTTPS_PUBLIC_PORT`
in `environment`, the read-only `./proxy-tls` bind at `/certs`, the
published side of `ports` (container ports 8080/8443, tcp), `profiles`,
resource limits and `restart` ([the hardening
lint](compose.md#the-override-file-and-the-hardening-lint)).

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
| Live events | GraphQL subscriptions are Server-Sent Events (`POST /graphql` with `Accept: text/event-stream`, D-124): never buffered, HTTP/2 to the browser (HTTP/1.1 allows only 6 connections per origin), 3 min read timeout on `/graphql` (serve sends a keepalive at least every minute). No WebSocket upgrades are passed |
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
the response, so it applies only to `POST`/`PUT`/`PATCH`/`DELETE` requests,
and not to event streams: the router `bekup-stream` (POST `/graphql` with
`Accept: text/event-stream`, highest priority) skips it, and bekup caps
those bodies itself (`graphql.max_body_bytes`). Downloads (`GET`) are never
buffered either. `flushInterval: -1ms` forwards every event at once.
The `websecure` entrypoint keeps `readTimeout: 60s`. It bounds only the
reading of a request (Traefik has no separate header timeout, so it is
the slowloris guard) and doesn't end responses that are still streaming.
Event streams outlive it over both HTTP/2 and HTTP/1.1, also when a
stream stays quiet for more than 60 s. This was checked with the pinned
Traefik image and this configuration (M2 review L19): a 100 s stream on
each protocol, a 75 s quiet HTTP/1.1 stream, and a client trickling its
headers, which was cut off after 60 s. Don't set `readTimeout` to `0`. Encoded `/`, `\`,
`%`, `?`, `#`, `;` and NUL in paths are rejected, which rules out path
confusion between proxy and backend.

### nginx (`deploy/nginx/`)

- `nginx.conf`: main configuration (TLS, limits, logging, resolver).
- `location = /graphql` streams subscription events: `proxy_buffering off`,
  `proxy_cache off`, `proxy_read_timeout 180s` (three times the longest
  allowed `graphql.stream_keepalive_interval`); bekup also sends
  `X-Accel-Buffering: no`. `Upgrade`/`Connection` are cleared: there are no
  WebSockets.
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

- **Dev:** `just run` (native, D-114) and the `full` compose profile
  serve on `127.0.0.1:8080` (loopback only) with
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
  docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile full --profile traefik up
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

## Existing Traefik

The `traefik-external` variant (`compose.traefik-external.yaml`, D-205)
runs no proxy of its own. Your Traefik reaches `serve` over a dedicated
**link network**, `bekup-proxy`: external to both projects, `internal`
(no route out), a `/28` with fixed addresses. Nothing but your proxy and
`serve` is on it, and `serve` publishes no port.

| | Address | Set in |
|---|---|---|
| your Traefik | `10.213.253.5` | Traefik's compose file |
| `serve` | `10.213.253.10:8080` | `SERVE_PROXY_IP` in `.env`, `[server] listen` |
| `trusted_proxies` | `["10.213.253.5/32"]` | `bekup.toml` (written by setup; `setup check` errors on anything else) |

Once per host, create the network:

```sh
docker network create --internal --subnet 10.213.253.0/28 \
  --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy
```

`--ip-range` limits Docker's automatic addresses to `.12`–`.15`: a
container that joins the network before Traefik or `serve` can never take
`.5` or `.10` (fixed addresses outside the range still work). setup prints
the command for another subnet or other addresses.

Then attach Traefik **in addition to the networks it already has**, and
recreate it (`docker compose up -d traefik` in its directory):

```yaml
# Traefik's compose file
services:
  traefik:
    networks:
      default: {}       # keep: the network it had without a networks: key
      web: {}           # keep: every network it already lists (example)
      bekup-proxy:
        ipv4_address: 10.213.253.5
networks:
  web:
    external: true      # unchanged
  bekup-proxy:
    external: true
```

A service's `networks:` replaces its default network, so list every network
Traefik used before, or it loses its other routes. A list form
(`networks: [web]`) can't carry an address: convert it to the map form
(`web: {}`). A service without a `networks:` key was on `default`; add
`default: {}`.

The variant puts these labels on `serve` only (values from `.env`, written
by setup):

| Label | Value |
|---|---|
| `traefik.enable` | `true` |
| `traefik.docker.network` | `${BEKUP_PROXY_NETWORK:-bekup-proxy}` |
| `traefik.http.routers.bekup.rule` | ``Host(`${BEKUP_DOMAIN}`)`` |
| `traefik.http.routers.bekup.entrypoints` | `${TRAEFIK_ENTRYPOINT:-websecure}` (`--traefik-entrypoint`) |
| `traefik.http.routers.bekup.tls` | `true` |
| `traefik.http.routers.bekup.tls.certresolver` | `${TRAEFIK_CERTRESOLVER:-letsencrypt}` (`--traefik-certresolver`) |
| `traefik.http.routers.bekup.service` | `bekup` |
| `traefik.http.services.bekup.loadbalancer.server.port` | `8080` |
| `traefik.http.services.bekup.loadbalancer.responseforwarding.flushinterval` | `-1ms` (live updates are Server-Sent Events, D-124) |

**Router and service name.** The labels name the router and the service
`bekup`. Traefik's names are global across the Docker provider: if another
container already defines a router or service called `bekup`, Traefik
reports a conflict and routes neither. Compose can't interpolate label
keys, so the name is not a variable: rename the other router, or customize
`compose.yaml` (rename `bekup` in these label keys, pass `--variant
traefik-external` to setup; the hardening lint then checks the file), or use
the file provider below.

There are no middleware labels: bekup sets HSTS, CSP and the other security
headers itself (D-102). Your Traefik must:

- use the Docker provider with `exposedByDefault: false`, so only labelled
  containers are routed;
- never set `forwardedHeaders.insecure` and never trust `0.0.0.0/0` (or any
  wide range) in `forwardedHeaders.trustedIPs` on the entrypoint: otherwise
  clients can forge `X-Forwarded-For`, which bekup then believes;
- have no entrypoint-wide header middlewares that add or rewrite
  `Strict-Transport-Security`, `Content-Security-Policy` or other security
  headers (they would duplicate or weaken bekup's);
- not set `respondingTimeouts.readTimeout: 0` (unlimited request reads);
  long-lived *responses* need no special timeout with the flush interval
  above;
- redirect HTTP to HTTPS on its own entrypoints.

**The Docker socket.** Traefik's Docker provider reads labels through the
Docker socket, and socket access is root on the host. bekup cannot limit
that: a compromised Traefik owns the host, bekup included. Prefer a
read-only socket proxy that allows only the container list and inspect
calls (e.g. a hardened docker-socket-proxy with `CONTAINERS=1` and nothing
else), or skip the labels and use Traefik's **file provider**:

```yaml
# Traefik dynamic configuration (file provider)
http:
  routers:
    bekup:
      rule: Host(`backup.example.com`)
      entryPoints: [websecure]
      service: bekup
      tls:
        certResolver: letsencrypt
  services:
    bekup:
      loadBalancer:
        servers:
          - url: http://10.213.253.10:8080
        responseForwarding:
          flushInterval: -1ms
```

With the file provider the labels on `serve` are ignored (no Docker
provider reads them). See [Verify](#verify-an-external-proxy).

## Nginx Proxy Manager

The `npm` variant (`compose.npm.yaml`, D-205) uses the same link network as
[Existing Traefik](#existing-traefik): create `bekup-proxy` once (same
command, with `--ip-range`) and give NPM's container (usually the service
`app`) `bekup-proxy` with `ipv4_address: 10.213.253.5` **next to its
existing networks** (`default: {}` when it had no `networks:` key; a list
becomes the map form), with the network declared `external: true`;
recreate NPM. Then add a Proxy Host:

| Field | Value |
|---|---|
| Domain Names | your DNS name (`BEKUP_DOMAIN`), e.g. `backup.example.com` |
| Scheme | `http` |
| Forward Hostname / IP | `10.213.253.10` |
| Forward Port | `8080` |
| Cache Assets | off |
| Block Common Exploits | on |
| Websockets Support | **off** (bekup uses Server-Sent Events, never WebSocket, D-124) |
| SSL Certificate | Request a new SSL certificate (Let's Encrypt) |
| Force SSL | on |
| HTTP/2 Support | on |
| HSTS Enabled | **off** (bekup sends HSTS itself; two headers are invalid) |

Advanced tab (custom Nginx configuration):

```nginx
proxy_http_version 1.1;
proxy_buffering off;
proxy_cache off;
proxy_read_timeout 3600s;
client_max_body_size 2m;
```

`proxy_buffering off` and the long read timeout keep the live update
streams flowing; the 2 MiB body limit sits just above bekup's own (`max_body_bytes`, 1 MiB), like the shipped nginx. NPM **appends** the
client address to `X-Forwarded-For` (`$proxy_add_x_forwarded_for`) and sends
`X-Forwarded-Proto https`. That is safe: bekup walks `X-Forwarded-For` from
the right, takes the hop NPM appended (NPM is the only trusted address) and
ignores whatever the client put in front of it.

### Verify an external proxy

```sh
curl -sI https://backup.example.com/ | grep -i -E '^(strict-transport-security|content-security-policy):'
```

Expect **exactly one** `Strict-Transport-Security` header and bekup's
`Content-Security-Policy` (`default-src 'self'; ... trusted-types 'none'`). Two HSTS
headers mean the proxy adds its own: turn that off. `docker compose ps`
must show no published port on any bekup service.

If serve's log shows the warning "X-Forwarded-For from a peer that is not
in server.trusted_proxies", requests arrive from an address other than
`10.213.253.5`: the proxy is not attached with its fixed address (or
`trusted_proxies` does not match). bekup then ignores the header and sees
every client as the proxy, so per-client rate limits and audit addresses
are wrong. A different subnet or addresses: `setup init --proxy-subnet
--proxy-ip --serve-ip` (and `--proxy-network` for another name).

## Using your own reverse proxy

If you already run a proxy, use the [Existing Traefik](#existing-traefik) or
[Nginx Proxy Manager](#nginx-proxy-manager) variant above, or (setup-first,
`full` variant) skip the profiles and connect your proxy to
bekup's `frontend` network (or run bekup's `serve` where your proxy can reach
it). Then set `trusted_proxies` to your proxy's address. Requirements:

- TLS 1.2+ only, AEAD ciphers, HSTS, and an HTTP→HTTPS redirect;
- overwrite (never append) `X-Forwarded-For`, and set `X-Forwarded-Proto: https`;
- don't buffer responses (subscription event streams on `/graphql`,
  streamed downloads), allow at least 3 minutes between two reads from
  bekup on `/graphql` and set no total response timeout;
- serve HTTP/2 to browsers (each subscription is one long-lived request);
- limit request bodies to a few MiB;
- don't log query strings, and redact `/api/download/*` paths.

### Generic nginx

```nginx
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
        proxy_set_header Connection        "";
        proxy_buffering off;               # event streams and downloads
        proxy_cache off;
        proxy_read_timeout 3600s;          # >= 180s for /graphql event streams
    }
}
```

### Caddy

Caddy obtains certificates automatically and uses TLS 1.2+ with modern
ciphers by default. Its `reverse_proxy` streams responses
(`flush_interval -1` also flushes the subscription event streams at once). Caddy **ignores** incoming `X-Forwarded-*` unless you set
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

- [ ] Exactly one proxy is in front of bekup (one profile in the `full`
      variant). `docker compose ps` shows no published port on `serve`,
      `runner` or `postgres`.
- [ ] External proxy: it sits at the fixed address on `bekup-proxy`
      (`10.213.253.5`), does not trust forwarded headers from everyone,
      adds no HSTS of its own, and serve logs no untrusted
      `X-Forwarded-For` warning.
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
