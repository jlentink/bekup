# Configuration

bekup reads its infrastructure configuration from TOML files:

- **`bekup.toml`** for `bekup serve` and `bekup worker`;
- **`runner.toml`** for `bekup runner`. It is a separate file so that serve's
  configuration can never change the runner's image and target allowlists.

Annotated examples ship in the repository root:
[`bekup.example.toml`](../bekup.example.toml) and
[`runner.example.toml`](../runner.example.toml). Settings that admins change
in the UI (SMTP, 2FA policy, SSRF allowlist) live in the database, not in
TOML. There is no hot reload: restart to apply changes.

Decisions: D-019, D-041, D-060 and the proposed D-073 to D-075 in
[`DECISIONS.md`](DECISIONS.md).

## Search path

The first match wins; files are **never merged**.

1. `--config <path>` or the `BEKUP_CONFIG` environment variable. If it is set
   and the file does not exist, bekup stops with an error. It never falls
   back to the other locations.
2. The working directory: `./bekup.toml` (or `./runner.toml`).
3. `/etc/bekup/bekup.toml` (or `/etc/bekup/runner.toml`).

If no file is found, startup fails, except in dev mode, which runs on the
built-in defaults. The loaded path is logged at startup and shown by
`bekup config print`.

## File safety checks

Every config file is checked before it is parsed. A candidate that exists but
fails a check is an error; bekup does not move on to the next location.

- It must be a **regular file**. A symlink is followed only when the
  directory of its target is owned by the current user or root and is not
  group- or world-writable (sticky directories such as `/tmp` are allowed).
- It must be **owned by the current user or root**.
- It must **not be group- or world-writable** (`chmod go-w`).
- At most 1 MiB.

**Secret files** (`*_file` keys marked "Secret file" below) follow the same
rules and must also **not be world-readable** (`chmod 0400` or `0440`). On
Kubernetes, set `defaultMode: 0400` on secret volumes.

Parsing is strict: unknown sections or keys and wrongly typed values are
errors that include the file, line and column, for example
`bekup.toml:12:1: unknown key "server.bogus"`.

## Precedence

From lowest to highest:

1. built-in defaults;
2. the TOML file;
3. `BEKUP_<SECTION>_<KEY>` environment variables;
4. command-line flags (`--log-level` sets `log.level`, `--dev` sets
   `dev.enabled`).

`bekup config print` shows the source (`default`, `file`, `env` or `flag`)
of every effective key.

## Environment overrides

Every key has an environment override: `BEKUP_` + the section + `_` + the key,
upper-cased. For example `[server] base_url` is `BEKUP_SERVER_BASE_URL` and
`[docker] gid` (runner.toml) is `BEKUP_DOCKER_GID`. Value formats:

- lists are comma-separated: `BEKUP_SERVER_TRUSTED_PROXIES=172.18.0.0/16,10.0.0.0/8`;
- durations use Go syntax: `30s`, `15m`, `2h`;
- booleans: `true`/`false`/`1`/`0`.

Two more variables are read by bekup but are not tied to a key:

| Variable | Meaning |
|---|---|
| `BEKUP_CONFIG` | Config file path, like `--config`. |
| `BEKUP_DEV` | `1`/`true` enables dev mode, like `--dev`. Enable-only: `0`/`false` does not turn off `[dev] enabled = true` from the file (use `BEKUP_DEV_ENABLED=false`, see [Dev mode](#dev-mode)). |

The two files use the same `BEKUP_` prefix; each process only reads the keys
of its own file.

### Unknown variables

At startup (and in `bekup config validate`) bekup warns about every `BEKUP_*`
variable that is not a key of the file being loaded, so typos do not silently
fall back to defaults. Only the variable name is reported, never its value.
These documented variables are not keys and never cause a warning; they are
read by the compose files, the dev tooling or the tests, not by bekup:

| Variable | Used by |
|---|---|
| `BEKUP_IMAGE` | compose: image reference |
| `BEKUP_DOMAIN` | compose proxy profiles: public host name |
| `BEKUP_DEV_BASE_URL`, `BEKUP_DEV_TLS_DIR` | dev compose |
| `BEKUP_DEV_BACKEND`, `BEKUP_WEB_BUILD`, `BEKUP_WEB_OUT_DIR` | web dev server and build |
| `BEKUP_UPDATE_SCHEMA` | tests (`just db-schema`) |

A key of the *other* file (for example `BEKUP_DOCKER_SELF_CONTAINER` in the
environment of `serve`) gets a warning that it is ignored by this command.

### Canonical names and older shorthand

The `BEKUP_<SECTION>_<KEY>` names above are the only names bekup reads
(D-097). Some older plan and decision entries use shorthand names; they refer
to these keys and are **not** read. Setting one produces a "did you mean"
warning:

| Shorthand in older text | Canonical variable | Key |
|---|---|---|
| `BEKUP_BASE_URL` | `BEKUP_SERVER_BASE_URL` | `[server] base_url` |
| `BEKUP_TRUSTED_PROXIES` | `BEKUP_SERVER_TRUSTED_PROXIES` | `[server] trusted_proxies` |
| `BEKUP_MASTER_KEY_FILE` | `BEKUP_SECURITY_MASTER_KEY_FILE` | `[security] master_key_file` |
| `BEKUP_DISK_ROOT` | `BEKUP_STORAGE_DISK_ROOT` | `[storage] disk_root` |
| `BEKUP_SCRATCH_DIR` | `BEKUP_STORAGE_SCRATCH_DIR` | `[storage] scratch_dir` |
| `BEKUP_RUNNER_CONTAINER` | `BEKUP_DOCKER_SELF_CONTAINER` | runner.toml `[docker] self_container` |

## Secrets policy

- Secret values are **never** written inline. Secret-bearing keys only exist
  as `*_file` paths, which fit Docker secrets (`/run/secrets/...`).
- Inline secret keys (`database.url`, `security.master_key`) exist for local
  development only. They are rejected (in the file and in env vars) unless
  dev mode is on, and even then bekup logs a warning. They can never be set
  with command-line flags.
- Secret files are read with the safety checks above, trimmed of surrounding
  whitespace and kept as byte slices that are zeroed after use.
- `bekup config print` always redacts secrets; `--redacted=false` is
  refused. Credentials embedded in URLs are redacted too.
- Logs go through a redaction handler: attributes with secret-looking names
  (password, secret, token, key, passphrase, authorization, cookie, dsn, ...)
  are replaced by `[REDACTED]`, URL credentials (up to the last `@` before
  the host, so passwords containing `@` are covered) and secret parameters
  in `key=value` DSNs and URL query strings (`password`, `sslpassword`,
  `sslkey`, `sslcert`, `passfile`, `*_secret`, `*_token`) are removed, and
  known secret values registered at runtime are scrubbed. Fatal CLI errors
  get the same treatment before they are printed.

## Validation

All errors are reported at once. Besides type and range checks, bekup
enforces:

- `server.base_url` is required and must be `https` outside dev mode unless
  `server.allow_insecure_http = true` (see [Plain HTTP](#plain-http)), with a
  host and without credentials, query or fragment.
- `server.trusted_proxies` is required outside dev mode, except when the
  base URL is `http` and allowed (no proxy is used then); every entry must be
  a valid CIDR. `0.0.0.0/0` and `::/0` are rejected in every CIDR list.
- Listen addresses must be `host:port`. Outside dev mode, `server.listen` and
  the runner's `listen.address` must not be a wildcard (`:8080`,
  `0.0.0.0:8080`, `[::]:8080`): bind the address of the internal network
  (the compose stack uses fixed addresses, `10.213.250.10:8080` for serve and
  `10.213.251.10:7443` for the runner). `server.allow_wildcard_listen` /
  `listen.allow_wildcard` override this and log a warning at every start.
- `metrics.listen` must not collide with `server.listen`: the addresses are
  compared as host:port, where a wildcard host overlaps every host and
  `localhost`, `127.0.0.1` and `::1` count as the same host (so `:9464` and
  `0.0.0.0:9464` collide).
- Paths must be absolute and clean; `storage.disk_root` must not be `/`.
- Limits, sizes and timeouts must be greater than zero; `stepup_max_age` is
  at most 5m.
- argon2id parameters and `password_min_length` can only be raised above the
  defaults (64 MiB, 3 iterations, 12 characters).
- runner: `docker.host` must be a `unix://` socket; `tls.allowed_client_sans`
  must not be empty and only lists `spiffe://bekup/<name>` identities (no
  wildcards; see docs/security/mtls-certs.md); `targets.allow_cidrs` entries
  that overlap an always-denied range (loopback, link-local, metadata,
  unspecified) are rejected, so `0.0.0.0/1` or `126.0.0.0/7` fail just like
  `127.0.0.1/32`. IPv4-mapped IPv6 entries are unmapped first
  (`::ffff:127.0.0.1/128` is `127.0.0.1/32`), and an entry covering the whole
  mapped range `::ffff:0:0/96` is rejected.

## Plain HTTP

bekup is meant to run behind the TLS reverse proxy. A non-`https`
`server.base_url` is accepted only in dev mode or with
`server.allow_insecure_http = true` (D-095); `trusted_proxies` may then be
empty. Traffic, session cookies and credentials are then **not encrypted**.
Every start logs the warning `plain HTTP: traffic not encrypted`, and the UI
shows a permanent red banner. `bekup config validate` lists the same
warnings.

## Dev mode

Enable with `--dev`, `BEKUP_DEV=1` or `[dev] enabled = true`. Any enabling
source wins. To turn off `[dev] enabled = true` from the file without editing
it, set `BEKUP_DEV_ENABLED=false` (the key's normal env override); this works
only if neither `--dev` nor `BEKUP_DEV=1` is given. `BEKUP_DEV=0` does
**not** turn dev mode off. Dev mode:

- runs without a config file (built-in defaults);
- allows an `http` `base_url` (default `http://localhost:8080`), no
  `trusted_proxies` and wildcard listen addresses;
- allows the inline secrets `database.url` and `security.master_key`;
- defaults to text logs.

Every start in dev mode logs a loud warning. Never use it in production.

## Commands

```sh
bekup config validate            # load and validate bekup.toml, list every error
bekup config validate --runner   # the same for runner.toml
bekup config print               # effective config, source per key, secrets redacted
bekup --config /etc/bekup/bekup.toml config print --redacted
```

## Key reference

Generated from the config structs; regenerate with
`go run ./internal/config/gendocs`. A test fails when it is out of date.

<!-- BEGIN GENERATED KEY REFERENCE (go run ./internal/config/gendocs) -->

### `bekup.toml`

#### `[server]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `listen` | `BEKUP_SERVER_LISTEN` | string | `':8080'` | Address the plain-HTTP server listens on (host:port). Only reachable on the internal proxy network; never publish it. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard_listen is set; bind the internal network address instead. |
| `allow_wildcard_listen` | `BEKUP_SERVER_ALLOW_WILDCARD_LISTEN` | bool | `false` | Allow a wildcard/unspecified server.listen address outside dev mode (logs a startup warning). Prefer binding the internal network address. |
| `base_url` | `BEKUP_SERVER_BASE_URL` | string | `(empty)` | Public URL of the UI as served by the reverse proxy; all links are built from it. Must be https unless dev mode is on or allow_insecure_http is set. |
| `allow_insecure_http` | `BEKUP_SERVER_ALLOW_INSECURE_HTTP` | bool | `false` | Accept a plain http base_url outside dev mode (D-095). Traffic, sessions and credentials are then NOT encrypted; bekup logs a warning at every startup and shows a permanent UI banner. |
| `trusted_proxies` | `BEKUP_SERVER_TRUSTED_PROXIES` | list of strings | `[]` | CIDRs of the reverse proxies whose X-Forwarded-* headers are trusted. Required outside dev mode, except with an http base_url and allow_insecure_http (no proxy). |
| `read_header_timeout` | `BEKUP_SERVER_READ_HEADER_TIMEOUT` | duration | `'10s'` | Maximum time to read request headers. |
| `read_timeout` | `BEKUP_SERVER_READ_TIMEOUT` | duration | `'30s'` | Maximum time to read a whole request, body included (at least read_header_timeout). |
| `write_timeout` | `BEKUP_SERVER_WRITE_TIMEOUT` | duration | `'1m0s'` | Maximum time to write a response. Long-lived routes (event streams of GraphQL subscriptions, artifact downloads) lift it per request. |
| `idle_timeout` | `BEKUP_SERVER_IDLE_TIMEOUT` | duration | `'2m0s'` | Maximum keep-alive idle time between requests. |
| `shutdown_timeout` | `BEKUP_SERVER_SHUTDOWN_TIMEOUT` | duration | `'30s'` | Grace period for in-flight requests on shutdown. |
| `max_header_bytes` | `BEKUP_SERVER_MAX_HEADER_BYTES` | int | `32768` | Maximum size of the request headers in bytes (4 KiB to 1 MiB). |
| `max_body_bytes` | `BEKUP_SERVER_MAX_BODY_BYTES` | int | `1048576` | Maximum request body size in bytes. |
| `rate_limit_per_second` | `BEKUP_SERVER_RATE_LIMIT_PER_SECOND` | float | `20.0` | Per-client-IP request rate (token bucket refill per second) for all HTTP requests. Exceeding it returns 429 with Retry-After. |
| `rate_limit_burst` | `BEKUP_SERVER_RATE_LIMIT_BURST` | int | `100` | Per-client-IP burst size (bucket capacity); must cover the asset requests of one UI page load. |
| `rate_limit_max_clients` | `BEKUP_SERVER_RATE_LIMIT_MAX_CLIENTS` | int | `10000` | Maximum number of client IPs tracked by the rate limiter; the least recently seen entries are evicted beyond it (memory bound). |
| `user_rate_limit_per_second` | `BEKUP_SERVER_USER_RATE_LIMIT_PER_SECOND` | float | `20.0` | Per-user request rate (token bucket refill per second) for authenticated /graphql and /api requests, on top of the per-IP limit. Exceeding it returns 429 with Retry-After. |
| `user_rate_limit_burst` | `BEKUP_SERVER_USER_RATE_LIMIT_BURST` | int | `100` | Per-user burst size (bucket capacity) for authenticated requests. |

#### `[graphql]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `max_complexity` | `BEKUP_GRAPHQL_MAX_COMPLEXITY` | int | `1000` | Maximum complexity of one GraphQL operation (1 per selected field, more for list fields that declare it); larger operations are rejected before execution (50 to 100000). |
| `max_depth` | `BEKUP_GRAPHQL_MAX_DEPTH` | int | `12` | Maximum field nesting depth of one GraphQL operation (3 to 50). |
| `max_body_bytes` | `BEKUP_GRAPHQL_MAX_BODY_BYTES` | int | `262144` | Maximum size of a GraphQL request body in bytes (1024 up to server.max_body_bytes); larger requests get 413. |
| `stream_max_per_session` | `BEKUP_GRAPHQL_STREAM_MAX_PER_SESSION` | int | `4` | Maximum concurrent event streams (GraphQL subscriptions over SSE) of one session on one instance (1 to 20, at most stream_max_per_user); further streams get 429. Browsers open at most 6 connections per origin over HTTP/1.1, so keep this well below 6 when serving without an HTTP/2 proxy. |
| `stream_max_per_user` | `BEKUP_GRAPHQL_STREAM_MAX_PER_USER` | int | `10` | Maximum concurrent event streams of one user on one instance, all sessions together (1 to 100); further streams get 429. |
| `stream_max_total` | `BEKUP_GRAPHQL_STREAM_MAX_TOTAL` | int | `1000` | Maximum concurrent event streams on one instance, all users together (1 to 100000); further streams get 503. |
| `stream_keepalive_interval` | `BEKUP_GRAPHQL_STREAM_KEEPALIVE_INTERVAL` | duration | `'25s'` | Interval of keepalive comments on idle event streams (5s to 1m). A reverse proxy must allow at least three intervals without data (nginx proxy_read_timeout). |
| `stream_revalidate_interval` | `BEKUP_GRAPHQL_STREAM_REVALIDATE_INTERVAL` | duration | `'30s'` | Interval at which every event stream re-checks its session in the database (5s to 5m). Revocations normally end streams at once through session events; this is the fallback bound. |
| `stream_session_check_ttl` | `BEKUP_GRAPHQL_STREAM_SESSION_CHECK_TTL` | duration | `'5s'` | Maximum age of a session check before a subscription event is delivered without a new one (1s up to stream_revalidate_interval). |

#### `[database]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `url_file` | `BEKUP_DATABASE_URL_FILE` | string | `'/run/secrets/bekup_database_url'` | **Secret file.** Path to a file containing the Postgres connection URL (Docker secret). The file must not be group/world-writable or world-readable. |
| `url` | `BEKUP_DATABASE_URL` | string | `(empty)` | **Dev mode only.** Inline connection URL. Dev mode only; rejected otherwise. Use url_file instead. |
| `owner_url_file` | `BEKUP_DATABASE_OWNER_URL_FILE` | string | `'/run/secrets/bekup_database_owner_url'` | **Secret file.** Path to a file containing the Postgres connection URL of the bekup_owner role, used only to apply migrations (bekup migrate). Same file rules as url_file. Do not mount it into containers that never migrate. |
| `owner_url` | `BEKUP_DATABASE_OWNER_URL` | string | `(empty)` | **Dev mode only.** Inline bekup_owner connection URL. Dev mode only; rejected otherwise. Use owner_url_file instead. |
| `max_conns` | `BEKUP_DATABASE_MAX_CONNS` | int | `25` | Maximum number of pooled connections (worker processes: at least the sum of the [workers] *_queue_workers plus 4). |
| `min_conns` | `BEKUP_DATABASE_MIN_CONNS` | int | `0` | Minimum number of idle pooled connections. |
| `conn_max_lifetime` | `BEKUP_DATABASE_CONN_MAX_LIFETIME` | duration | `'1h0m0s'` | Maximum lifetime of a pooled connection. |
| `conn_max_idle_time` | `BEKUP_DATABASE_CONN_MAX_IDLE_TIME` | duration | `'30m0s'` | Maximum idle time of a pooled connection. |
| `connect_timeout` | `BEKUP_DATABASE_CONNECT_TIMEOUT` | duration | `'10s'` | Timeout for establishing a connection. |
| `statement_timeout` | `BEKUP_DATABASE_STATEMENT_TIMEOUT` | duration | `'30s'` | Server-side statement_timeout of app connections (1s to 1h). Migrations are not limited by it. |
| `tls_mode` | `BEKUP_DATABASE_TLS_MODE` | string | `'verify-full'` | TLS for the app DB connection (D-100): verify-full (default; encrypted, server certificate verified against tls_ca_file and the URL's host name), require (encrypted; with tls_ca_file the chain is verified but not the host name, without it nothing is verified) or disable (plain text). require and disable log a warning at every startup. With TLS, SCRAM channel binding is always required. Applies to serve, worker and migrate; an sslmode in the URL may not be stricter than this and is otherwise overridden. |
| `tls_ca_file` | `BEKUP_DATABASE_TLS_CA_FILE` | string | `'/run/secrets/bekup_db_ca.crt'` | Path to the CA certificate bundle (PEM) that signs the Postgres server certificate (bekup certs: <dir>/postgres/ca.crt). Only this CA is trusted, never the system roots. Required for verify-full. |
| `tls_client_cert_file` | `BEKUP_DATABASE_TLS_CLIENT_CERT_FILE` | string | `(empty)` | Optional client certificate (PEM) presented to Postgres. Set together with tls_client_key_file. |
| `tls_client_key_file` | `BEKUP_DATABASE_TLS_CLIENT_KEY_FILE` | string | `(empty)` | **Secret file.** Optional client private key (PEM) for tls_client_cert_file. Same file rules as url_file. |

#### `[security]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `master_key_file` | `BEKUP_SECURITY_MASTER_KEY_FILE` | string | `'/run/secrets/bekup_master_key'` | **Secret file.** Path to the master keyring file (created by bekup keys generate). Must not be group/world-writable or world-readable. |
| `master_key` | `BEKUP_SECURITY_MASTER_KEY` | string | `(empty)` | **Dev mode only.** Inline master key. Dev mode only; rejected otherwise. Use master_key_file instead. |
| `session_idle_timeout` | `BEKUP_SECURITY_SESSION_IDLE_TIMEOUT` | duration | `'30m0s'` | Session idle expiry. |
| `session_absolute_timeout` | `BEKUP_SECURITY_SESSION_ABSOLUTE_TIMEOUT` | duration | `'12h0m0s'` | Session absolute expiry. Must be >= session_idle_timeout. |
| `session_touch_interval` | `BEKUP_SECURITY_SESSION_TOUCH_INTERVAL` | duration | `'1m0s'` | Minimum time between two writes of a session's last-seen time and idle expiry (sliding idle refresh without a database write on every request); 10s to 10m and less than session_idle_timeout. |
| `stepup_max_age` | `BEKUP_SECURITY_STEPUP_MAX_AGE` | duration | `'5m0s'` | How long a step-up (fresh second factor) stays valid. At most 5m. |
| `argon2_memory_kib` | `BEKUP_SECURITY_ARGON2_MEMORY_KIB` | int | `65536` | argon2id memory in KiB. Can only be raised above the default (65536). |
| `argon2_iterations` | `BEKUP_SECURITY_ARGON2_ITERATIONS` | int | `3` | argon2id iterations. Can only be raised above the default (3). |
| `argon2_parallelism` | `BEKUP_SECURITY_ARGON2_PARALLELISM` | int | `1` | argon2id parallelism (>= 1). |
| `argon2_max_concurrent` | `BEKUP_SECURITY_ARGON2_MAX_CONCURRENT` | int | `4` | Maximum concurrent argon2id hash operations (memory bound, 1 to 64). Requests beyond it wait briefly and are then refused with RATE_LIMITED; they never queue without bound. |
| `password_min_length` | `BEKUP_SECURITY_PASSWORD_MIN_LENGTH` | int | `12` | Minimum password length in characters. Can only be raised above the default (12); at most 128. |
| `password_min_score` | `BEKUP_SECURITY_PASSWORD_MIN_SCORE` | int | `3` | Minimum zxcvbn strength score (0-4) of a new password. Can only be raised above the default (3). |
| `login_rate_limit_per_minute` | `BEKUP_SECURITY_LOGIN_RATE_LIMIT_PER_MINUTE` | int | `10` | Login attempts per client IP per minute (a stricter limiter on top of the server-wide one; 1 to 600); the same rate also limits password verifications per account (logins, password step-ups and changes). Excess attempts get RATE_LIMITED. |
| `login_rate_limit_burst` | `BEKUP_SECURITY_LOGIN_RATE_LIMIT_BURST` | int | `5` | Burst of login attempts per client IP, and of password verifications per account (1 to 100). |
| `lockout_threshold` | `BEKUP_SECURITY_LOCKOUT_THRESHOLD` | int | `5` | Consecutive failed logins after which an account is temporarily locked (3 to 20). |
| `lockout_base_duration` | `BEKUP_SECURITY_LOCKOUT_BASE_DURATION` | duration | `'1m0s'` | Lock duration after lockout_threshold failures; it doubles with every further failure (10s to 1h). |
| `lockout_max_duration` | `BEKUP_SECURITY_LOCKOUT_MAX_DURATION` | duration | `'1h0m0s'` | Upper bound of the exponential lockout backoff (lockout_base_duration to 24h). |
| `audit_head_sign_interval` | `BEKUP_SECURITY_AUDIT_HEAD_SIGN_INTERVAL` | duration | `'1h0m0s'` | How often serve signs the audit chain head with the instance key when the chain has grown (1m to 24h). |

#### `[runner]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `address` | `BEKUP_RUNNER_ADDRESS` | string | `'runner:7443'` | host:port of the runner's mTLS gRPC endpoint. |
| `server_name` | `BEKUP_RUNNER_SERVER_NAME` | string | `'runner'` | Expected DNS SAN in the runner's certificate (bekup certs init always includes "runner"). |
| `cert_file` | `BEKUP_RUNNER_CERT_FILE` | string | `'/run/secrets/bekup_client.crt'` | Path to this component's client certificate (PEM). |
| `key_file` | `BEKUP_RUNNER_KEY_FILE` | string | `'/run/secrets/bekup_client.key'` | **Secret file.** Path to this component's client private key (PEM). Must not be group/world-writable or world-readable. |
| `ca_file` | `BEKUP_RUNNER_CA_FILE` | string | `'/run/secrets/bekup_ca.crt'` | Path to the private CA certificate (PEM) used to verify the runner. |
| `dial_timeout` | `BEKUP_RUNNER_DIAL_TIMEOUT` | duration | `'10s'` | Timeout for connecting to the runner. |

#### `[storage]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `disk_root` | `BEKUP_STORAGE_DISK_ROOT` | string | `'/var/lib/bekup/backups'` | Root directory that confines all disk destinations (os.Root). |
| `scratch_dir` | `BEKUP_STORAGE_SCRATCH_DIR` | string | `'/var/lib/bekup/scratch'` | Directory for temporary files (restore staging). Each restore stages its artifact in a private 0700 subdirectory, encrypted at rest with a key held only in memory, and removes it on every path. |
| `scratch_reserve_bytes` | `BEKUP_STORAGE_SCRATCH_RESERVE_BYTES` | int | `1073741824` | Bytes that must stay free on the scratch_dir file system after a restore staged its artifact (0 to 1 TiB). A restore whose artifact does not fit with this reserve is refused before anything is downloaded. |
| `scratch_stale_after` | `BEKUP_STORAGE_SCRATCH_STALE_AFTER` | duration | `'24h0m0s'` | Staging directories older than this are removed by the scratch sweeper even when their restore is still recorded as running (1h to 30 days; must exceed workers.restore_timeout). Directories of restores that are no longer running are removed at startup and every 10 minutes. |

#### `[workers]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `max_concurrent_runs` | `BEKUP_WORKERS_MAX_CONCURRENT_RUNS` | int | `4` | Maximum backup/restore runs at the same time (global). |
| `max_runs_per_connection` | `BEKUP_WORKERS_MAX_RUNS_PER_CONNECTION` | int | `1` | Maximum concurrent runs against one connection. |
| `max_concurrent_downloads` | `BEKUP_WORKERS_MAX_CONCURRENT_DOWNLOADS` | int | `4` | Maximum concurrent artifact downloads served by this process (1-64; a plain download of a passphrase-encrypted artifact needs up to 256 MiB for scrypt). |
| `max_downloads_per_user` | `BEKUP_WORKERS_MAX_DOWNLOADS_PER_USER` | int | `2` | Maximum concurrent artifact downloads of one user in this process (1..max_concurrent_downloads). |
| `backup_timeout` | `BEKUP_WORKERS_BACKUP_TIMEOUT` | duration | `'6h0m0s'` | Default timeout of a backup run and the cap on each job's timeoutSeconds. |
| `restore_timeout` | `BEKUP_WORKERS_RESTORE_TIMEOUT` | duration | `'12h0m0s'` | Timeout of a restore run. |
| `notify_timeout` | `BEKUP_WORKERS_NOTIFY_TIMEOUT` | duration | `'30s'` | Timeout of a single notification delivery. |
| `retention_timeout` | `BEKUP_WORKERS_RETENTION_TIMEOUT` | duration | `'1h0m0s'` | Timeout of a single retention pass of one job. |
| `shutdown_timeout` | `BEKUP_WORKERS_SHUTDOWN_TIMEOUT` | duration | `'2m0s'` | On shutdown, how long running jobs may finish before they are canceled (River soft stop, then hard stop). |
| `backup_queue_workers` | `BEKUP_WORKERS_BACKUP_QUEUE_WORKERS` | int | `4` | Backup jobs this process works at the same time (River queue backup, 1-10000). |
| `restore_queue_workers` | `BEKUP_WORKERS_RESTORE_QUEUE_WORKERS` | int | `2` | Restore jobs this process works at the same time (River queue restore, 1-10000). |
| `retention_queue_workers` | `BEKUP_WORKERS_RETENTION_QUEUE_WORKERS` | int | `2` | Retention jobs this process works at the same time (River queue retention, 1-10000). |
| `notify_queue_workers` | `BEKUP_WORKERS_NOTIFY_QUEUE_WORKERS` | int | `8` | Notification deliveries this process works at the same time (River queue notify, 1-10000). |
| `maintenance_queue_workers` | `BEKUP_WORKERS_MAINTENANCE_QUEUE_WORKERS` | int | `1` | Housekeeping jobs this process works at the same time (River queue maintenance, 1-10000). |
| `run_log_max_lines` | `BEKUP_WORKERS_RUN_LOG_MAX_LINES` | int | `10000` | Log lines stored per backup run (runner output and worker events, 100-1000000); further lines are counted and dropped. |
| `run_log_max_line_bytes` | `BEKUP_WORKERS_RUN_LOG_MAX_LINE_BYTES` | int | `4096` | Maximum bytes of one stored run-log line (128-16384); longer lines are truncated after redaction. |

#### `[scheduler]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `enabled` | `BEKUP_SCHEDULER_ENABLED` | bool | `true` | Run the scheduler loop in this process. |
| `tick_interval` | `BEKUP_SCHEDULER_TICK_INTERVAL` | duration | `'15s'` | How often due jobs are enqueued (>= 1s). |
| `min_interval` | `BEKUP_SCHEDULER_MIN_INTERVAL` | duration | `'5m0s'` | Shortest allowed schedule interval for new or changed jobs, incl. the gap between cron occurrences (1m to 24h). |
| `batch_size` | `BEKUP_SCHEDULER_BATCH_SIZE` | int | `100` | Due jobs locked and enqueued per scheduler transaction (1-1000). |

#### `[retention]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `dry_run` | `BEKUP_RETENTION_DRY_RUN` | bool | `false` | Compute and log every retention plan without deleting anything (instance-wide dry run). |
| `max_deletions_per_sweep` | `BEKUP_RETENTION_MAX_DELETIONS_PER_SWEEP` | int | `500` | Hard cap on the objects one retention sweep of a job may delete (1-100000); the rest waits for the next sweep. |
| `partial_cleanup_age` | `BEKUP_RETENTION_PARTIAL_CLEANUP_AGE` | duration | `'24h0m0s'` | The daily sweep removes leftover temporary upload files on disk and SFTP destinations older than this (1h to 30 days). |

#### `[restore]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `temp_connection_ttl` | `BEKUP_RESTORE_TEMP_CONNECTION_TTL` | duration | `'24h0m0s'` | Lifetime of a temporary restore connection after its creation (5m up to temp_connection_max_ttl). Expired temporary connections are deleted and their stored credentials destroyed. |
| `temp_connection_max_ttl` | `BEKUP_RESTORE_TEMP_CONNECTION_MAX_TTL` | duration | `'168h0m0s'` | Longest lifetime of a temporary restore connection counted from its creation, including extensions (at least temp_connection_ttl, at most 30 days). |

#### `[notifications]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `max_per_user_per_hour` | `BEKUP_NOTIFICATIONS_MAX_PER_USER_PER_HOUR` | int | `30` | Maximum notifications per user per hour before digesting. |
| `collapse_window` | `BEKUP_NOTIFICATIONS_COLLAPSE_WINDOW` | duration | `'15m0s'` | Repeated failures of the same job within this window collapse into one notification. |
| `webhook_failure_threshold` | `BEKUP_NOTIFICATIONS_WEBHOOK_FAILURE_THRESHOLD` | int | `5` | Consecutive failures after which a user webhook is auto-disabled. |

#### `[netguard]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `allow_cidrs` | `BEKUP_NETGUARD_ALLOW_CIDRS` | list of strings | `[]` | Private CIDRs that outbound connections (S3, SMTP, SFTP, webhooks) may reach. Empty denies all private ranges. |
| `allow_hosts` | `BEKUP_NETGUARD_ALLOW_HOSTS` | list of strings | `[]` | Host names (exact, or *.example.com for any subdomain) that may resolve to private addresses. Never re-allows loopback (outside dev mode), link-local, metadata or other always-denied ranges. |

#### `[metrics]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `enabled` | `BEKUP_METRICS_ENABLED` | bool | `true` | Serve Prometheus /metrics. |
| `listen` | `BEKUP_METRICS_LISTEN` | string | `'127.0.0.1:9464'` | Internal address for /metrics (host:port). Never publish it. |

#### `[log]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `level` | `BEKUP_LOG_LEVEL` | string | `'info'` | Log level: debug, info, warn or error. |
| `format` | `BEKUP_LOG_FORMAT` | string | `'json'` | Log format: json or text (text is the default in dev mode). |

#### `[dev]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `enabled` | `BEKUP_DEV_ENABLED` | bool | `false` | Enable dev mode (also --dev or BEKUP_DEV=1): runs without a config file, allows http base_url, no trusted_proxies, wildcard listen addresses and inline secrets. Logs a loud warning. BEKUP_DEV is enable-only; BEKUP_DEV_ENABLED=false turns off enabled = true from the file (unless --dev or BEKUP_DEV=1 is also set). |

### `runner.toml`

#### `[listen]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `address` | `BEKUP_LISTEN_ADDRESS` | string | `':7443'` | host:port of the mTLS gRPC listener. Only reachable on the internal runner network. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard is set; bind the internal network address instead. |
| `allow_wildcard` | `BEKUP_LISTEN_ALLOW_WILDCARD` | bool | `false` | Allow a wildcard/unspecified listen.address outside dev mode (logs a startup warning). Prefer binding the internal network address. |
| `health_socket` | `BEKUP_LISTEN_HEALTH_SOCKET` | string | `'/tmp/bekup-runner/health.sock'` | Absolute path of the container-local unix socket that serves only the standard gRPC health service (no TLS) for bekup healthcheck runner. Its directory is created 0700 and must belong to the runner user; the socket is 0600. Keep it on the container's private tmpfs, never on a shared volume. Empty disables it. |

#### `[tls]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `cert_file` | `BEKUP_TLS_CERT_FILE` | string | `'/run/secrets/runner.crt'` | Path to the runner's server certificate (PEM). |
| `key_file` | `BEKUP_TLS_KEY_FILE` | string | `'/run/secrets/runner.key'` | **Secret file.** Path to the runner's server private key (PEM). Must not be group/world-writable or world-readable. |
| `client_ca_file` | `BEKUP_TLS_CLIENT_CA_FILE` | string | `'/run/secrets/bekup_ca.crt'` | Path to the private CA certificate (PEM) that signs client certificates. |
| `allowed_client_sans` | `BEKUP_TLS_ALLOWED_CLIENT_SANS` | list of strings | `['spiffe://bekup/serve', 'spiffe://bekup/worker']` | Client identities (URI SANs of the form spiffe://bekup/<component>) that may call the runner. |

#### `[docker]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `host` | `BEKUP_DOCKER_HOST` | string | `'unix:///var/run/docker.sock'` | Docker daemon address. Only unix:// sockets are accepted. |
| `gid` | `BEKUP_DOCKER_GID` | int | `0` | Group ID that owns the Docker socket (0 = do not add a group). |
| `self_container` | `BEKUP_DOCKER_SELF_CONTAINER` | string | `(empty)` | ID or name of the runner's own container, used to join the per-run networks. Empty = auto-detect from /proc/self/mountinfo, then the container hostname; the container must carry one of the runner's own addresses. Set it when detection fails (e.g. a custom hostname). |
| `memory_mib` | `BEKUP_DOCKER_MEMORY_MIB` | int | `1024` | Memory limit per dump container in MiB. |
| `cpus` | `BEKUP_DOCKER_CPUS` | float | `1.0` | CPU limit per dump container (fractional CPUs). |
| `pids_limit` | `BEKUP_DOCKER_PIDS_LIMIT` | int | `256` | Process limit per dump container. |
| `pull_timeout` | `BEKUP_DOCKER_PULL_TIMEOUT` | duration | `'10m0s'` | Timeout for pulling an allowlisted image. |
| `run_subnet_pool` | `BEKUP_DOCKER_RUN_SUBNET_POOL` | string | `'10.214.0.0/16'` | Private IPv4 CIDR (inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16, prefix /8 to /24) from which every run gets its own /28 internal network. The runner treats the whole pool as its own network: targets inside it are always denied and no gRPC connection is accepted from it. Subnets already used by other Docker networks are skipped; an overlap with targets.allow_cidrs is allowed (the pool is denied first). |

#### `[images]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `extra_file` | `BEKUP_IMAGES_EXTRA_FILE` | string | `(empty)` | Optional path to an extra-images TOML file (entries pinned by digest). Empty = embedded catalog only. |

#### `[targets]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `allow_cidrs` | `BEKUP_TARGETS_ALLOW_CIDRS` | list of strings | `[]` | CIDRs that dump containers may connect to. Empty = no CIDR is allowed. |
| `allow_hosts` | `BEKUP_TARGETS_ALLOW_HOSTS` | list of strings | `[]` | Host names that dump containers may connect to. IP addresses are refused here; list them in allow_cidrs (e.g. 192.0.2.10/32). |
| `deny_cidrs` | `BEKUP_TARGETS_DENY_CIDRS` | list of strings | `[]` | CIDRs that are always denied (evaluated before the allow lists). |
| `deny_hosts` | `BEKUP_TARGETS_DENY_HOSTS` | list of strings | `[]` | Host names that are always denied (evaluated before the allow lists). |
| `bastion_targets` | `BEKUP_TARGETS_BASTION_TARGETS` | list of strings | `[]` | Per-bastion target allowlist: entries <bastion>=<CIDR or host name> (e.g. bastion.example.com=10.20.0.0/16, bastion.example.com=db.internal, 203.0.113.7=127.0.0.1/32). A bastion (matched by its name or any of its addresses) that appears here may only forward to its listed targets: IP-literal targets must lie in one of its CIDRs, host names must be listed exactly. Bastions not listed may forward to any host (only deny_cidrs/deny_hosts apply; a startup warning says so). |

#### `[limits]`

| Key | Env override | Type | Default | Description |
|---|---|---|---|---|
| `max_concurrent_runs` | `BEKUP_LIMITS_MAX_CONCURRENT_RUNS` | int | `4` | Maximum dump/restore containers at the same time. |
| `max_run_duration` | `BEKUP_LIMITS_MAX_RUN_DURATION` | duration | `'12h0m0s'` | Hard upper bound for a single container run. |
| `max_log_lines` | `BEKUP_LIMITS_MAX_LOG_LINES` | int | `10000` | Maximum log lines kept per run. |
| `max_log_line_bytes` | `BEKUP_LIMITS_MAX_LOG_LINE_BYTES` | int | `4096` | Maximum length of a single log line in bytes (longer lines are truncated). |

<!-- END GENERATED KEY REFERENCE -->
