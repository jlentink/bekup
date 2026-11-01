# Reverse proxy and TLS

bekup never terminates TLS for the web UI itself (D-017). `bekup serve`
speaks plain HTTP on the internal `frontend` Docker network (in the
`own-proxy` variant: on your proxy's network), and its port is never
published. A reverse proxy in front of it handles HTTPS, certificates,
HSTS and the HTTP→HTTPS redirect. bekup still sends HSTS, its CSP and the
other security headers itself, as defence in depth (D-048, D-082).

Pick the compose variant that matches your proxy (D-205, D-215,
[compose.md](compose.md)):

| Variant (release asset) | Proxy | Section |
|---|---|---|
| `traefik` (`compose.yaml`) | bekup's own Traefik, started with the stack | [Quick start](#quick-start-production) |
| `own-proxy` (`compose.own-proxy.yaml`) | your reverse proxy on this Docker host (Nginx Proxy Manager, Traefik, Caddy, nginx, ...) | [Own reverse proxy](#own-reverse-proxy) |
| `full` (setup-first, flags only) | profile `traefik`, `nginx`, or (advanced, `--proxy none`) your own proxy routed to `frontend` | below, [Using your own reverse proxy](#using-your-own-reverse-proxy) |

Both release variants also exist as `*.external-db.yaml` for your own
PostgreSQL ([external-database.md](external-database.md)).
Each of the four release files is also committed in
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

Your own proxy on this Docker host has no fixed address on its own network,
so the `own-proxy` variant trusts it by its container name instead:
`[server] trusted_proxy_hosts` (`BEKUP_SERVER_TRUSTED_PROXY_HOSTS`, D-215;
[Trust by container name](#trust-by-container-name)). Either key satisfies
the "trusted proxy required outside dev mode" rule.

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

## Own reverse proxy

The `own-proxy` variant (`compose.own-proxy.yaml`, or
`compose.own-proxy.external-db.yaml` with your own PostgreSQL; D-215) runs
no proxy of its own. Your reverse proxy already runs on this Docker host,
and `serve` joins the Docker network it is on (**join mode**, the default).
Nothing changes in your proxy's own compose file, and `serve` publishes no
port.

```
your proxy ──its Docker network (external, serve as bekup-serve)──> serve:8080
```

| | Value | Set in |
|---|---|---|
| your proxy's network | for example `npm_default` | `BEKUP_PROXY_NETWORK` in `.env` (`--proxy-network`) |
| your proxy's container | for example `npm-app-1` | `[server] trusted_proxy_hosts` in `bekup.toml` (`--proxy-container`) |
| `serve` | `http://bekup-serve:8080` (network alias on your network; no fixed address) | the compose file |

### Find the network and the container name

The setup container has no Docker access. In a second terminal on the host:

```sh
docker ps --format '{{.Names}}'                                   # your proxy's container name
docker inspect <proxy container> --format '{{json .NetworkSettings.Networks}}'
docker network ls
```

Pick a user-defined network your proxy is on (with Compose usually
`<project>_default` or a network named in its compose file). setup refuses
Docker's predefined `bridge`, `host` and `none` (there is no container DNS
on `bridge`). A proxy that is only on `bridge` needs a network of its own
first: add one in the proxy's compose file and recreate the proxy (a
`docker network connect` alone is lost when compose recreates it).

Network names: letters, digits, `_ . -`, at most 63 characters. Container
names: letters, digits, `_ -`, at most 63 characters, **no dots** (so never
an IP address or a DNS name) and none of bekup's own service names
(`serve`, `runner`, `migrate`, `admin`, `setup`, `certs-init`, `postgres`,
`bekup-serve`, `worker`, `traefik`, `traefik-init`); a network alias of the
proxy works too.

### Set it up

In the wizard choose "my own reverse proxy on this Docker host", then enter
the network and the container name; "Traefik Docker labels?" stays **no**
unless your Traefik reads container labels ([Traefik](#traefik)).
Compose-first (`compose.own-proxy.yaml` saved as `compose.yaml`),
`docker compose run --rm setup` asks the same. With flags:

```sh
setup init --non-interactive --proxy own --proxy-network npm_default \
  --proxy-container npm-app-1 --domain backup.example.com ...
```

(`--proxy own` selects the variant for `--write-compose`; compose-first the
file decides.) setup writes `BEKUP_PROXY_NETWORK` to `.env` and:

```toml
[server]
listen = "0.0.0.0:8080"
allow_wildcard_listen = true
base_url = "https://backup.example.com"
trusted_proxies = []
trusted_proxy_hosts = ["npm-app-1"]
```

`setup check` errors unless exactly this shape is kept: that listen
address with `allow_wildcard_listen`, no CIDRs in `trusted_proxies`, exactly
the recorded container in `trusted_proxy_hosts`, and `BEKUP_PROXY_NETWORK`
equal to the recorded network. The setup report lists the settings for
your proxy ([below](#point-your-proxy-at-bekup)).

**Why `0.0.0.0`.** Docker assigns `serve`'s address on your network, so
`serve` binds every interface of its container. D-098 refuses wildcard
listeners by default because they expose the port on networks nobody
intended. In this shape `serve` therefore **serves only connections that
arrive on its own address on your proxy's network** (and on loopback, for
its healthcheck): it resolves its alias `bekup-serve`, keeps the answers
that are addresses of its own interfaces, and closes every other connection
before reading a byte, so containers on bekup's internal networks or on a
database network cannot reach port 8080. Until that address is known
(the alias does not resolve to `serve` itself) it refuses every
non-loopback connection and `/readyz` fails. The own-proxy `serve` service
passes `BEKUP_LISTEN_SHAPE=own-proxy-join`; only with that value,
`listen = "0.0.0.0:8080"`, `trusted_proxy_hosts` set and `trusted_proxies`
empty does `serve` log an INFO line instead of the wildcard warning; any
other wildcard keeps the warning.

### Trust by container name

`serve` uses `X-Forwarded-For` only from the containers named in
`server.trusted_proxy_hosts` (at most 8 names; no IP addresses, which belong
in `trusted_proxies`, no wildcards, no dots and none of bekup's own service
names):

- it resolves each name through Docker's embedded DNS as an absolute name
  (the host's search domains never turn a container name into a public
  name) and trusts only private IPv4 and IPv6 ULA addresses **inside the
  subnet of `serve`'s own interface on the proxy network** (found through
  its alias `bekup-serve`), each as `/32` or `/128`, in addition to
  `trusted_proxies`. A container with the proxy's name on another network
  `serve` is on (for example the database network) is never trusted;
- a name that returns more than one address of one family on that
  network is carried by two containers (a name or alias collision): it
  trusts nothing until only one remains (WARN "resolves to more than one
  address of one family");
- it resolves all names in parallel at start, every 20 s, at most every 5 s
  when `X-Forwarded-For` arrives from an untrusted peer (a recreated proxy
  has a new address), and at most every 5 s while a peer trusted through a
  name sends requests (a removed proxy's address may be handed to another
  container); a resolution older than 30 s trusts nothing until the next
  one succeeds;
- a name that does not resolve trusts nothing, and so does every name
  while `serve`'s own address on the proxy network is unknown. Changes and
  failures are logged with names and addresses only ("trusted proxy host
  resolved", "trusted proxy host does not resolve; it is not trusted");
- the key is live (D-200): a removed name loses its trust at once, an added
  one after its first resolution; adding a name counts as a weakening and
  alerts every admin ([configuration.md](../configuration.md)).

### Alias check

Every answer for `bekup-serve` must be an address of `serve` itself
(checked at start and every 20 s). If another container on your proxy's
network carries the alias `bekup-serve` too, Docker's DNS hands your proxy
either container, so requests (with passwords and session cookies) may
reach the other one. `serve` then logs the WARN "... alias takeover ...",
records `security.serve_alias_conflict` in the audit log, sends the
critical `serve_alias_conflict` security alert to every admin and fails
`/readyz` until the alias is its own again. Remove the other container's
alias. Where your proxy allows it, forward to `serve`'s container name
instead of the alias (with the shipped compose file `<project>-serve-1`,
for example `bekup-serve-1`; container names are unique per Docker daemon,
though another container could still carry one as an alias); for strict
isolation use the [link network](#advanced-dedicated-link-network).

**Residual risk** (D-215, [SECURITY.md](../../SECURITY.md) RP-9):

- Every container on your proxy's network can reach `serve:8080` directly,
  bypassing the proxy (plain HTTP, like the proxy's own hop). It cannot
  forge client addresses (only the named proxy is trusted); sign-in, CSP,
  Origin checks and rate limits apply as to every request. Keep that
  network to your proxy and the apps it fronts, or use the
  [link network](#advanced-dedicated-link-network). Containers on bekup's
  internal networks and on a
  [database container's](external-database.md#database-container-on-this-docker-host)
  network cannot: `serve` closes connections that do not arrive on its
  proxy-network address.
- An alias takeover is detected and alerted, not prevented: until you act,
  some requests may reach the other container ([Alias check](#alias-check)).
- After your proxy container is removed, Docker may hand its address to
  another container. Within the re-resolution window (a few seconds while
  requests arrive, at most 30 s otherwise) that container could forge
  `X-Forwarded-For`, which only affects the client address in rate limits
  and audit entries.
- A container with your proxy's name or alias next to the proxy makes the
  name untrusted (collision); once the proxy is gone, the other container
  is trusted. You control who joins your proxy's network.
- An admin SSRF allowlist entry for a private range that contains these
  Docker networks (for example `172.16.0.0/12`) also lets destinations,
  SMTP and webhooks reach the containers on them
  ([hardening](../security/hardening.md#outbound-connections-and-ssrf-task-23)).

### Point your proxy at bekup

Forward your domain to **`http://bekup-serve:8080`**. Whatever the proxy:

- TLS 1.2+ with AEAD ciphers and an HTTP→HTTPS redirect on its side;
- **no HSTS of its own**: bekup sends `Strict-Transport-Security` itself,
  and two headers are invalid;
- no WebSocket upgrades (live updates are Server-Sent Events, D-124), no
  response buffering, at least 3 minutes between two reads from bekup on
  `/graphql` and no total response timeout (streamed downloads);
- HTTP/2 to the browsers (each subscription is one long-lived request);
- `X-Forwarded-For` set or appended by the proxy, `X-Forwarded-Proto:
  https`; it must not trust forwarded headers from everyone;
- request bodies limited to a few MiB (bekup's own limit is 1 MiB);
- no query strings in its access log, and `/api/download/*` paths redacted.

#### Nginx Proxy Manager

Add a Proxy Host:

| Field | Value |
|---|---|
| Domain Names | your DNS name (`base_url`), e.g. `backup.example.com` |
| Scheme | `http` |
| Forward Hostname / IP | `bekup-serve` |
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
streams flowing; the 2 MiB body limit sits just above bekup's own
(`max_body_bytes`, 1 MiB), like the shipped nginx. NPM **appends** the
client address to `X-Forwarded-For` (`$proxy_add_x_forwarded_for`) and sends
`X-Forwarded-Proto https`. That is safe: bekup walks `X-Forwarded-For` from
the right, takes the hop NPM appended (NPM's container is the only trusted
peer) and ignores whatever the client put in front of it.

#### Traefik

Either route with Traefik's **file provider** (no labels, recommended):

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
          - url: http://bekup-serve:8080
        responseForwarding:
          flushInterval: -1ms
```

or let a Traefik with the **Docker provider** read labels on `serve`: answer
"Traefik Docker labels?" with yes, or pass `--proxy-labels` (default off).
setup writes them into `compose.override.yaml` (never into `compose.yaml`;
[compose.md](compose.md#the-override-file-and-the-hardening-lint)) with the
values from `.env`:

| Label | Value |
|---|---|
| `traefik.enable` | `true` |
| `traefik.docker.network` | `${BEKUP_PROXY_NETWORK:-bekup-proxy}` |
| `traefik.http.routers.bekup.rule` | ``Host(`${BEKUP_DOMAIN:-bekup.invalid}`)`` |
| `traefik.http.routers.bekup.entrypoints` | `${TRAEFIK_ENTRYPOINT:-websecure}` (`--traefik-entrypoint`) |
| `traefik.http.routers.bekup.tls` | `true` |
| `traefik.http.routers.bekup.tls.certresolver` | `${TRAEFIK_CERTRESOLVER:-letsencrypt}` (`--traefik-certresolver`) |
| `traefik.http.routers.bekup.service` | `bekup` |
| `traefik.http.services.bekup.loadbalancer.server.port` | `8080` |
| `traefik.http.services.bekup.loadbalancer.responseforwarding.flushinterval` | `-1ms` (live updates are Server-Sent Events, D-124) |

`--traefik-entrypoint` / `--traefik-certresolver` apply only with
`--proxy-labels` (the wizard asks for both). The lint holds the label block
to exactly what setup wrote, so the router and service name stay `bekup`.
Traefik's names are global across the Docker provider: if another container
already defines a router or service called `bekup`, Traefik reports a
conflict and routes neither. Rename the other one, or use the file provider.

There are no middleware labels: bekup sets HSTS, CSP and the other security
headers itself (D-102). In both cases your Traefik must:

- with the Docker provider, use `exposedByDefault: false`, so only labelled
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
that: a compromised Traefik owns the host, bekup included. Prefer the file
provider, or a read-only socket proxy that allows only the container list
and inspect calls (e.g. a hardened docker-socket-proxy with `CONTAINERS=1`
and nothing else).

#### Caddy

Caddy obtains certificates automatically and uses TLS 1.2+ with modern
ciphers by default. Its `reverse_proxy` streams responses
(`flush_interval -1` also flushes the subscription event streams at once).
Caddy **ignores** incoming `X-Forwarded-*` unless you set `trusted_proxies`,
so leave that unset unless something sits in front of it. Don't add an HSTS
header: bekup sends it.

```caddyfile
backup.example.com {
    request_body {
        max_size 2MB
    }
    reverse_proxy bekup-serve:8080 {
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

#### nginx

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

    # No add_header Strict-Transport-Security: bekup sends HSTS itself.
    client_max_body_size 2m;

    location / {
        proxy_pass http://bekup-serve:8080;   # the full variant: http://10.213.250.10:8080
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

An nginx in a container resolves `bekup-serve` through Docker's DNS when it
starts; after `serve` is recreated, reload nginx (or use a `resolver
127.0.0.11` with a variable in `proxy_pass`).

### Advanced: dedicated link network

For operators who want no other container next to `serve`, link mode
(`--proxy-network-mode link`, flags only; the D-205 layout) puts your proxy
and `serve` on a dedicated **link network**, `bekup-proxy`: external to both
projects, `internal` (no route out), a `/28` with fixed addresses. Nothing
but your proxy and `serve` is on it, and bekup trusts the proxy's fixed
address (`/32`) instead of a name.

| | Address | Set in |
|---|---|---|
| your proxy | `10.213.253.5` | your proxy's compose file |
| `serve` | `10.213.253.10:8080` | `SERVE_PROXY_IP` in `.env`, `[server] listen`, `compose.override.yaml` |
| `trusted_proxies` | `["10.213.253.5/32"]` | `bekup.toml` (written by setup; `setup check` errors on anything else) |

```sh
setup init --proxy own --proxy-network-mode link --domain backup.example.com ...
```

Another name, subnet or addresses: `--proxy-network`, `--proxy-subnet`,
`--proxy-ip`, `--serve-ip` (a private IPv4 subnet between /8 and /29 that
overlaps neither `runner-ctl` nor `metrics`; setup prints the matching
`docker network create` command). `--proxy-container` does not apply.
`serve`'s fixed address is a setup-only block that setup writes into
`compose.override.yaml`
([compose.md](compose.md#the-override-file-and-the-hardening-lint)).

Once per host, create the network:

```sh
docker network create --internal --subnet 10.213.253.0/28 \
  --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy
```

`--ip-range` limits Docker's automatic addresses to `.12`–`.15`: a
container that joins the network before your proxy or `serve` can never take
`.5` or `.10` (fixed addresses outside the range still work).

Then attach your proxy **in addition to the networks it already has**, and
recreate it (for example `docker compose up -d traefik` in its directory):

```yaml
# your proxy's compose file (service traefik; NPM: usually app)
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
the proxy used before, or it loses its other routes. A list form
(`networks: [web]`) can't carry an address: convert it to the map form
(`web: {}`). A service without a `networks:` key was on `default`; add
`default: {}`.

Point the proxy at `http://10.213.253.10:8080` with the
[settings above](#point-your-proxy-at-bekup) (NPM: Forward Hostname / IP
`10.213.253.10`; Traefik file provider: that URL). `--proxy-labels` works
here too.

### Verify an external proxy

```sh
curl -sI https://backup.example.com/ | grep -i -E '^(strict-transport-security|content-security-policy):'
```

Expect **exactly one** `Strict-Transport-Security` header and bekup's
`Content-Security-Policy` (`default-src 'self'; ... trusted-types 'none'`). Two HSTS
headers mean the proxy adds its own: turn that off. `docker compose ps`
must show no published port on any bekup service.

If serve's log shows the warning "X-Forwarded-For from a peer that is not
in server.trusted_proxies (or trusted_proxy_hosts)", requests arrive from a
peer bekup does not trust. bekup then ignores the header and sees every
client as the proxy, so per-client rate limits and audit addresses are
wrong. In join mode check that `trusted_proxy_hosts` names the proxy's
container (or an alias) on the network `serve` joined, and look for
"trusted proxy host does not resolve" (the proxy is not running there) or
"resolves only to non-private addresses". In link mode the proxy is not
attached with its fixed address `10.213.253.5`, or `trusted_proxies` does
not match it.

### Troubleshooting

- **"trusted proxy host resolves only to addresses outside serve's subnet
  on the proxy network (or to a public or CGNAT range)"**: the proxy's
  address is not in the subnet of `serve`'s interface on that network
  (the name belongs to a container on another network), or your Docker
  daemon assigns addresses outside the private ranges. bekup trusts only
  RFC 1918 IPv4 and IPv6 ULA (`fc00::/7`) addresses; Docker address pools
  in CGNAT space (`100.64.0.0/10`) or global IPv6 are never trusted (fail
  closed). Give the proxy network a private subnet
  (`docker network create --subnet 172.30.0.0/24 ...`, or
  `default-address-pools` in `daemon.json`), or use the
  [link network](#advanced-dedicated-link-network).
- **"the network alias bekup-serve does not resolve to an address of this
  container"**: `serve` is not on your proxy's network with the alias
  `bekup-serve` (check `BEKUP_PROXY_NETWORK` and that compose.yaml is the
  own-proxy release file). Until it is, `serve` refuses every non-loopback
  connection and `/readyz` fails.
- **"serve closed a connection that did not arrive on its address on the
  proxy network"**: something on another network (bekup's internal ones or
  the database network) tried `serve:8080`; only your proxy's network is
  served.
- **"resolves to more than one address of one family"**: two containers
  carry the proxy's name or alias on that network; remove the other one.

## Using your own reverse proxy

A proxy on this Docker host belongs in the
[own-proxy variant](#own-reverse-proxy); the setup wizard offers only that
and the bundled Traefik. A proxy on another machine is **not supported by
the wizard**. The advanced path is flags only (setup-first, `full` variant,
`--proxy none`): run a small proxy on this host (the own-proxy variant, or
the `traefik`/`nginx` profile) that your other proxy forwards to over TLS,
or route to bekup's `frontend` network yourself. In the latter case skip the
profiles and attach your proxy to `frontend` with a fixed address other than
bekup's (`PROXY_FRONTEND_IP` and `SERVE_FRONTEND_IP` stay reserved), pass it
as `--trusted-proxy <cidr>` (`trusted_proxies`) and forward to
`http://10.213.250.10:8080`. bekup publishes no port for `serve` (plain HTTP
across hosts would carry the session cookies unencrypted, and the lint
refuses ports on bekup's services), so reaching `frontend` from another
host is your own routing (D-215). The requirements and the
[Caddy](#caddy) and [nginx](#nginx) examples above apply with that address.

## Security checklist

- [ ] Exactly one proxy is in front of bekup (one profile in the `full`
      variant). `docker compose ps` shows no published port on `serve`,
      `runner` or `postgres`.
- [ ] Own proxy: it does not trust forwarded headers from everyone, adds
      no HSTS of its own, and serve logs no untrusted `X-Forwarded-For`
      warning. Join mode: `trusted_proxy_hosts` names only your proxy's
      container, only containers you trust are on its network, no other
      container carries the alias `bekup-serve`, and `/readyz` answers
      `ok`; forward to serve's container name where your proxy allows it.
      Link
      mode: the proxy sits at the fixed address on `bekup-proxy`
      (`10.213.253.5`).
- [ ] `trusted_proxies` names only the proxy's address (`/32`), not a subnet
      and never `0.0.0.0/0` (join mode: empty).
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
      networks (the own-proxy join mode listens on `0.0.0.0:8080` by
      design, D-215), and keep host access restricted.
