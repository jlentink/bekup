# External API for CI pipelines

> **Status:** Phase 2 in progress (D-220, proposed). Implemented: the database layer, the workers' request consumer, `external-api.toml`, the `bekup external-api` process with the three endpoints below, `bekup healthcheck external-api`, token management in GraphQL and the web UI, and the opt-in compose profile `external-api` with the proxy routes and the setup support ([enabling it](#enabling-it)).

The external API lets a CI pipeline start a backup job, follow its run and
fail the pipeline unless the backup succeeded, for example right before a
deployment migrates the database. It is a small REST API with three calls,
authenticated with a per-job API token.

It runs as its own process, `bekup external-api`, with its own config file
(`external-api.toml`, see the [configuration reference](../configuration.md))
and its own database role `bekup_external`, which can do nothing but call
five checked database functions. The process never reads `bekup.toml`, the
master key or the runner certificates, so a bug in this public, token-
authenticated listener sits nowhere near your backup secrets.

## Enabling it

The external API is **off by default**: the `external-api` compose service
belongs to the compose profile `external-api`, and nothing about it runs,
is routed or exists in the database until you enable it (D-220 amendment
4). Single-account mode of your own PostgreSQL does not support it.

- **New deployment:** answer **yes** to the wizard's question *External API
  for CI pipelines* (the browser setup asks it too), or pass
  `--external-api` to `bekup setup init`. With the bundled database the
  role `bekup_external` is created together with the new volume; with your
  own PostgreSQL the bootstrap command setup prints creates it
  (`--external-password-file /run/secrets/bekup_external_database_password`,
  [external database](../installation/external-database.md#the-external-api-role)).
- **Existing deployment:** run `docker compose run --rm setup upgrade
  --external-api` and then the steps it prints: the role on the existing
  database, `docker compose run --rm migrate`, `docker compose up -d`
  ([upgrades](../installation/upgrades.md#enable-the-external-api)).

What setup writes:

| File | What |
|---|---|
| `.env` | `BEKUP_EXTERNAL_API=enabled` and `external-api` in `COMPOSE_PROFILES`. This one variable also turns on the bundled proxy's `/api/v1/` route, the role on a new bundled volume, and the curl examples in the UI (serve's `serverInfo.externalApiEnabled`); without it the UI says the external API is not enabled on this server. |
| `external-api.toml` | Listens on its fixed `frontend` address `10.213.250.11:8081` (`EXTERNAL_API_FRONTEND_IP`) behind the bundled proxy, or on `0.0.0.0:8081` on your own proxy's network as `bekup-external-api` (accepting connections only there and on loopback), and trusts **exactly the proxy serve trusts** (`trusted_proxies` / `trusted_proxy_hosts`). Your own PostgreSQL: the server and TLS choice of `bekup.toml`. An operator file: setup never rewrites it, `setup check` validates it. |
| `<keys>/bekup_external_password`, `<keys>/bekup_external_database_url` (bundled) or `<keys>/bekup_external_database_password` (your PostgreSQL) | The role's login. Written for **every** deployment (compose mounts them whether the API is enabled or not); the role exists only once it is enabled. |

The API is reachable at `https://<domain>/api/v1/`. The bundled Traefik or
nginx routes only that path to the external API (bodies at most 16 KiB);
everything else, `/api/download/` included, stays with the UI. Behind your
own reverse proxy add the route setup prints
([reverse proxy](../installation/reverse-proxy.md#external-api)).

`setup check` keeps it consistent: the profile, `BEKUP_EXTERNAL_API` and
`external-api.toml` exist together, and `external-api.toml` trusts the
proxy serve trusts. To pause it, `docker compose stop external-api` (and
revoke tokens you no longer need); setup has no command to disable it yet.

## Tokens

- **Per job.** An admin, or the owner of a job for their own jobs, creates a
  token for one or more jobs (with a fresh second factor). Maintainers
  cannot create tokens.
- **Format.** `bkp_` followed by 26 and 52 lower-case base32 characters,
  for example `bkp_aaisem…74_aaaq…ypq` (83 characters). The prefix lets
  secret scanners (GitHub, GitLab) recognize a leaked token.
- **Shown once.** bekup stores only a SHA-256 hash; a lost token cannot be
  recovered, create a new one.
- **Expiry.** Required, 1 to 365 days. Revocation applies to the next call.
- **Scope follows the creator.** When the creator is disabled, deleted,
  demoted, or loses ownership of the job, the token loses access at once.
- **Client address allowlist.** Each token lists the networks (IPv4/IPv6
  CIDRs, at most 32, none wider than `/8` for IPv4 or `/32` for IPv6) it
  may be used from, or explicitly allows any address (a permanent warning
  in the UI). A valid token from another address is
  refused exactly like a wrong token, and the admins and the token's creator
  get a security alert (`api_token_ip_denied`, at most once per token and
  10 minutes).

Store the token as a CI secret named `BEKUP_TOKEN`. Never put it into a URL:
the API refuses any query string, and a token that was sent in a URL should
be revoked.

## Managing tokens in the web UI

**Create a token** on the job's page, in the **API access** panel (admins
and the job's backup owners; maintainers do not see it), or under
**Administration → API access** (admins, every token):

1. **Create API token** opens the dialog. Give the token a name that says
   where it is used, keep the job (preselected on a job's page) and add other
   jobs you may use if one pipeline backs up several.
2. Choose the expiry: 30, 90 (default), 180 or 365 days.
3. List the networks the token may be used from, one per line (a bare
   address is a single host). Call `GET /api/v1/self` with a working token
   from the runner to see the address bekup sees (`clientIp`). Only if you
   cannot know the address, tick **Allow from any address**: the token then
   carries a permanent **Any address** warning badge and the admins are
   alerted.
4. **Create token** asks you to confirm your identity (step-up). The token
   is shown **once**: copy it, store it as a CI secret named `BEKUP_TOKEN`,
   tick the acknowledgement and close the dialog. bekup keeps only a hash.

The panel always shows three ready-to-copy examples for the job, filled with
your bekup address and the job's id: starting a backup, checking its status
(with what `done`, `success` and `state` mean) and the complete [CI
script](#ci-script). They read the token from `$BEKUP_TOKEN` and never
contain it.

The token list shows each token's prefix (`bkp_` and the first characters of
its public id, to recognize it in your secret store), its jobs (on a job's
page as "+ N other jobs"; a backup owner sees only their own jobs and a count
of the others), the expiry (flagged within 7 days), where it was last seen
(time and client address; a refused attempt from outside the allowlist is
flagged) and its address list or the **Any address** badge. **Edit
addresses** replaces the list (step-up; audited) and **Revoke** ends the
token at once (step-up; the next call gets 401). A backup owner may revoke or
re-address any token that covers one of their jobs. Every creation alerts
the admins (`api_token_created`); choosing any address alerts them as well
(`api_token_any_address`). An address change that widens the list (a
network the old list did not cover, or any address) alerts the admins and
the token's creator, naming who changed it (`api_token_addresses_widened`).

## Calls

The base URL is `https://<your bekup domain>/api/v1`. Send the token only as
`Authorization: Bearer $BEKUP_TOKEN`. Every answer is JSON.

### Start a run: `POST /api/v1/jobs/{jobId}/runs`

```sh
curl -fsS -X POST -H "Authorization: Bearer $BEKUP_TOKEN" \
  "https://backup.example.com/api/v1/jobs/$JOB_ID/runs"
```

The body is empty (or `{}` with `Content-Type: application/json`). An
optional `Idempotency-Key` header (1 to 64 characters of `A-Z a-z 0-9 _ -`,
for example the pipeline run id) makes a retried call safe: the same key for
the same job within 24 hours returns the original request again, with
`Idempotent-Replayed: true` and its current state.

```json
{
  "requestId": "6f1c…",
  "jobId": "0b6e…",
  "state": "pending",
  "statusUrl": "/api/v1/requests/6f1c…",
  "createdAt": "2026-11-03T12:00:00Z"
}
```

The request is decided by bekup's workers within a few seconds; `statusUrl`
(also in the `Location` header) is relative to your bekup domain.

| Status | Error code | Meaning |
|---|---|---|
| 202 | | The request was filed (or replayed). |
| 400 | `invalid_request` | A malformed request: a query string (also a token in the URL), a bad `Idempotency-Key`, a body other than empty or `{}`. |
| 401 | `unauthenticated` | Missing, malformed, unknown, wrong, revoked or expired token, or a client address outside the token's allowlist. Always the same answer, with `WWW-Authenticate: Bearer realm="bekup"`. |
| 404 | `not_found` | The job does not exist or is not in the token's scope (bekup never answers 403, so jobs cannot be discovered). |
| 409 | `run_active` | A run of the job is already queued, running or waiting for a retry. |
| 409 | `job_disabled` | The job is disabled. |
| 409 | `idempotency_mismatch` | The `Idempotency-Key` was used for another job within 24 hours. |
| 413 | `body_too_large` | The body is larger than `limits.max_body_bytes` (16 KiB). |
| 415 | `unsupported_media_type` | A body that is not `application/json`. |
| 429 | `too_many_pending` | The token has 3 requests that are not decided yet. |
| 429 | `rate_limited` | The token filed 30 requests within an hour, the per-token or per-address request rate was exceeded, or too many failed authentications came from your address (10 per 5 minutes). |
| 503 | `unavailable` | The database is unreachable; retry. |

Every 429 and 503 carries `Retry-After` (seconds).

### Follow a request: `GET /api/v1/requests/{requestId}`

```sh
curl -fsS -H "Authorization: Bearer $BEKUP_TOKEN" \
  "https://backup.example.com/api/v1/requests/$REQUEST_ID"
```

```json
{
  "requestId": "6f1c…",
  "jobId": "0b6e…",
  "state": "success",
  "done": true,
  "success": true,
  "status": "SUCCESS",
  "attempt": 1,
  "runId": "a2d4…",
  "reason": null,
  "createdAt": "2026-11-03T12:00:00Z",
  "queuedAt": "2026-11-03T12:00:03Z",
  "startedAt": "2026-11-03T12:00:04Z",
  "finishedAt": "2026-11-03T12:01:30Z"
}
```

| `state` | `done` | Meaning |
|---|---|---|
| `pending` | false | Filed; the workers have not decided it yet. |
| `rejected` | true | Decided without a run; `reason` is `run_active`, `job_disabled`, `job_gone`, `token_invalid` or `expired` (not decided within 10 minutes). |
| `queued` | false | The run waits for a worker. |
| `running` | false | The run is in progress. |
| `retrying` | false | An attempt failed or was interrupted (the worker stopped) and bekup will retry it (`attempt` counts). |
| `success` | true | Every database was backed up and stored. |
| `partial` | true | Some databases or destinations failed. |
| `failed` | true | The run failed (after its retries), or its worker stopped and no further attempt follows (`reason` is then `run_interrupted`; `status` may still read `QUEUED` or `RUNNING` until bekup finishes the run as failed). |
| `canceled` | true | The run was canceled (also when it was canceled while waiting or after its worker stopped). |
| `skipped` | true | The run did not start (the job was disabled or deleted meanwhile). |

- **Poll until `done` is true**, then decide by `success`. `success` is true
  **only** for `state = success`; `partial` is not a success.
- `status` is the exact status of the latest run (`SUCCESS`, `PARTIAL`,
  `FAILED`, `CANCELED`, `SKIPPED`, `QUEUED`, `RUNNING`), or null before the
  run exists. `queuedAt`, `startedAt` and `attempt` are those of the latest
  attempt; `finishedAt` is set once `done`.
- A request of another token, or one whose job left the token's scope,
  answers 404 like an unknown one.

### Check a token: `GET /api/v1/self`

```sh
curl -fsS -H "Authorization: Bearer $BEKUP_TOKEN" https://backup.example.com/api/v1/self
```

```json
{
  "name": "deploy pipeline",
  "expiresAt": "2027-02-01T00:00:00Z",
  "jobs": [{ "id": "0b6e…", "name": "app database" }],
  "clientIp": "203.0.113.7"
}
```

`clientIp` is the address bekup sees for your runner, the one the token's
allowlist is checked against. Use it to find the network to allow. A token
whose allowlist does not contain that address gets 401 here too, so call it
first from a token that allows any address, or read the address from the
security alert.

## CI script

Start, poll every 15 seconds until `done`, and fail unless `success`. It
needs `curl` and `jq`. Set `BEKUP_TOKEN` as a CI secret; `BEKUP_URL` (e.g.
`https://backup.example.com/api/v1`) and `BEKUP_JOB_ID` either as CI
variables or in the two default lines. The job's **API access** panel in the
web UI shows this script with both filled in (and never the token).

```sh
#!/bin/sh
# Start a bekup backup and fail unless it succeeds (needs curl and jq).
# BEKUP_TOKEN comes from your CI secrets; never write the token here.
set -eu
BEKUP_URL="${BEKUP_URL:-https://backup.example.com/api/v1}"
BEKUP_JOB_ID="${BEKUP_JOB_ID:-<job id>}"
auth="Authorization: Bearer ${BEKUP_TOKEN}"
response=$(curl -fsS -X POST -H "$auth" \
  -H "Idempotency-Key: ${CI_PIPELINE_ID:-$(date +%s)}" \
  "${BEKUP_URL}/jobs/${BEKUP_JOB_ID}/runs")
request=$(printf '%s' "$response" | jq -r .requestId)
echo "bekup request ${request}"
while :; do
  sleep 15
  status=$(curl -fsS -H "$auth" "${BEKUP_URL}/requests/${request}")
  state=$(printf '%s' "$status" | jq -r .state)
  echo "bekup state: ${state}"
  if [ "$(printf '%s' "$status" | jq -r .done)" = "true" ]; then
    break
  fi
done
if [ "$(printf '%s' "$status" | jq -r .success)" != "true" ]; then
  echo "bekup backup did not succeed: $(printf '%s' "$status" | jq -c '{state, status, reason}')" >&2
  exit 1
fi
echo "bekup backup succeeded"
```

The web UI's snippets are generated from the same text (a test compares them
with this page), so keep the two in sync.

`curl -f` makes a 401, 404, 409 or 429 fail the step at once; drop it and
read `.error` from the body if you want to wait and retry on `run_active` or
`429` instead.

## Client addresses and proxies

bekup checks the token's allowlist against the client address it sees. The
external API runs behind your TLS reverse proxy and takes the client address
from `X-Forwarded-For` **only** when the request comes from a configured
proxy (`[listen] trusted_proxies`, the bundled proxy's fixed address, or
`trusted_proxy_hosts`, your own proxy's container name on a shared Docker
network). From any other peer the header is ignored and the peer's address
counts, so a client cannot claim an allowlisted address by sending the
header itself. bekup walks `X-Forwarded-For` from the right and skips only
trusted proxies, so whatever a client puts in front of the address your
proxy appended is ignored.

**GitHub-hosted runners** (and most hosted CI) use large, changing address
ranges shared with every other customer: an allowlist of those ranges keeps
out little, and a short list breaks when the ranges change. Prefer
self-hosted runners with fixed egress addresses, or a NAT gateway with a
fixed address, and allow exactly those. If you must allow any address, keep
the token's expiry short and its jobs few.

## Running it

- Compose: the service `external-api` (profile `external-api`) with serve's
  hardening, 0.5 CPU, 128 MB, 64 processes, the `frontend` and `backend`
  networks only (your own proxy's network instead of `frontend`), no
  published port, `bekup healthcheck external-api` as its healthcheck; see
  [Compose](../installation/compose.md#the-external-api).
- Config: `external-api.toml` ([reference](../configuration.md), annotated
  example [`external-api.example.toml`](../../external-api.example.toml)).
  Outside dev mode `[listen] trusted_proxies` or `trusted_proxy_hosts` is
  required; `[database]` must log in as `bekup_external` with TLS
  `verify-full` by default; the owner keys and single-account mode are
  refused.
- Startup refuses to run when the setup version does not match (D-212), when
  the database login is not `bekup_external` or the role holds anything
  beyond its allowlist, or when the schema version differs from the binary
  (run `bekup migrate up` first).
- `/healthz` (process alive) and `/readyz` (database reachable, schema
  current, role check at most a minute old) for probes;
  `bekup healthcheck external-api` probes `/healthz` without a shell.
- Limits (`[limits]`): per client address 5 requests/s (burst 20), per token
  1 request/s (burst 10), failed authentications 10 per 5 minutes per
  address, bodies 16 KiB. On top of that the database caps each token at 3
  undecided requests and 30 requests per hour.
- The access log shows method, path, status and client address, never a
  header or the query string; a path that contains a token is logged as
  `[redacted]`.
