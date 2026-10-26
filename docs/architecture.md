# Architecture

> **Status:** partial (end of M0). Components, networks, trust boundaries and
> data flows below describe the design and the compose topology that exist
> today. Of the bekup components, only the CLI parts (`config`, `migrate`,
> `keys`, `certs`), `internal/config`, `internal/logging`, `internal/db`,
> `internal/crypto` and `internal/certs` are implemented. `serve` arrives in
> task 1.1 and later, the runner in tasks 2.4–2.7a (with its design section
> here), and the backup, restore and download flows in M3/M4. The page is
> completed in task 7.5.

bekup is one Go binary with several subcommands (D-025) and one container
image. The authoritative threat model is
[`SECURITY.md`](../SECURITY.md) (section 5: trust boundaries and data flows;
section 6: STRIDE per component). This page summarizes it. Decisions are in
[DECISIONS.md](DECISIONS.md).

## Components

| Component | Role | Status |
|---|---|---|
| Reverse proxy (`traefik` or `nginx` profile, opt-in) | Terminates TLS for the UI, HSTS, HTTP→HTTPS redirect, size and rate limits, WebSocket passthrough (D-017, D-048). Traefik uses only its file provider; nginx is the unprivileged image plus a certbot companion. Neither mounts the Docker socket (D-093, D-094). | shipped (task 0.7b) |
| `bekup serve` | GraphQL API (POST + JSON), sessions, embedded React UI, scheduler, River workers (backup, restore, retention, notify), backup pipeline and uploads to destinations. Connects to Postgres as `bekup_app`. | task 1.1 onwards |
| `bekup migrate` | One-shot: applies the embedded goose migrations as `bekup_owner`, under an advisory lock (D-042, D-078). The only process that holds the owner credentials. | implemented (task 0.4) |
| `bekup runner` | The **only** component with Docker access (D-003). Small typed mTLS gRPC API, image and target allowlists from its own `runner.toml`, builds the hardened dump-container spec itself (D-029–D-033). | tasks 2.4–2.7a |
| Dump containers | Short-lived official, digest-pinned DB images (`pg_dump`, `mysqldump`, `mariadb-dump`, restore clients). Alone on a per-run `internal` network, reaching exactly one target through the runner's proxy or SSH tunnel; secrets only through a stdin preamble (D-032). | task 2.6 |
| Postgres app DB | bekup's own state: users, jobs, runs, encrypted credentials, the hash-chained audit log, River queue tables. Least-privilege roles `bekup_owner` / `bekup_app` (D-054, D-092). | implemented (task 0.4) |
| `certs-init` (profile `setup`) | One-shot, no network: creates the private CA and the per-component mTLS certificates (D-079, D-089). | implemented (task 0.6) |
| Offline CLI | `bekup verify` / `bekup decrypt` for recovery without the bekup DB (D-040). | task 4.7 |

## Compose topology and networks

```
                  ┌──────────── edge (published 80/443, ACME) ────────────┐
 browser ──HTTPS──> traefik | nginx (10.213.250.5)          certbot (nginx profile)
                       │
                       └── frontend (internal) ──> serve (10.213.250.10:8080)
                                                    │  ├─ backend (internal) ──> postgres  <── migrate
                                                    │  ├─ egress ──> S3 / SFTP / SMTP / webhooks (SSRF guard)
                                                    │  └─ runner-ctl (internal, mTLS gRPC)
                                                    │        └──> runner (10.213.251.10:7443) ── docker.sock ──> dockerd
                                                    │                │  └─ targets ──> target DBs, SSH bastions
                                                    │                └─ per-run internal network ──> dump container
```

| Network | Internal | Members | Purpose |
|---|---|---|---|
| `edge` | no | proxy, certbot | published ports and outbound ACME (D-093) |
| `frontend` | yes | proxy (fixed `10.213.250.5`), `serve` (fixed `10.213.250.10`) | the only path to the UI; `trusted_proxies = ["10.213.250.5/32"]` |
| `backend` | yes | `serve`, `migrate`, `postgres` | app database |
| `runner-ctl` | yes | `serve`, `runner` | mTLS gRPC only |
| `egress` | no | `serve` | destinations, SMTP, webhooks; every dial checked by the SSRF guard (D-055) |
| `targets` | no | `runner` | target databases and bastions, limited by the runner's target allowlist |

`serve` can never reach a target database or Docker; the runner can never
reach the app database. `serve`'s port is not published in production
(D-077). Details: [installation/compose.md](installation/compose.md).

**Dev stack.** `docker-compose.dev.yml` publishes `serve` on
`127.0.0.1:8080` over plain HTTP so no proxy is needed (D-095: allowed, with
a startup warning and a permanent UI banner, task 1.1/6.1), and adds sample
targets, an SSH bastion, MinIO, SFTP and mailpit (D-081). The proxy profiles
stay opt-in, also in dev.

## Trust boundaries

Summarized from [SECURITY.md §5](../SECURITY.md#5-trust-boundaries-and-data-flows):

| Boundary | Between | Main controls |
|---|---|---|
| B1 | browser ↔ reverse proxy | TLS 1.2/1.3, ECDHE+AEAD only, HSTS, redirect, size/rate limits (D-048, D-093) |
| B2 | proxy ↔ `serve` | `serve` only on internal `frontend`; forwarded headers only from `trusted_proxies`; CSP, Origin checks and headers set by `serve` too (D-050, D-082) |
| B3 | `serve` ↔ app DB | `bekup_app` DML only, migrations as `bekup_owner` in a separate container, envelope-encrypted secrets, append-only hash-chained audit log (D-046, D-054, D-078) |
| B4 | `serve` ↔ destinations, SMTP, webhooks | dial-time SSRF guard, no redirects, TLS ≥ 1.2, SFTP host-key pins, disk confined via `os.Root` (D-049, D-055) |
| B5 | `serve` ↔ runner | mTLS (TLS 1.3, private name-constrained CA, SPIFFE URI SAN check), typed RPCs (D-030, D-089) |
| B6 | runner ↔ Docker | image allowlist (digest-pinned), fixed hardened spec built by the runner (D-031) |
| B7 | dump container ↔ everything else | alone on a per-run internal network, no runner gRPC, no internet; secrets via stdin (D-029, D-032) |
| B8 | runner / dump client ↔ target DB | target allowlist, certificate pinning with a leaf-SPKI pre-flight, SSH host-key pins (D-034, D-035) |

The configuration files are a boundary as well: `serve` reads `bekup.toml`,
the runner reads `runner.toml`, so nothing `serve` controls can change the
runner's allowlists (D-019).

## Data flows

Summaries; the step-by-step flows are in
[SECURITY.md §5 "Data flows"](../SECURITY.md#data-flows).

- **Startup (implemented today):** `migrate` applies migrations as
  `bekup_owner` and exits; `serve` then starts with the `bekup_app` URL only
  and refuses to run against an outdated schema (D-078; the serve side
  lands in task 1.1).
- **Backup (task 3.6):** scheduler → River job (IDs only) → worker decrypts
  the connection secret → runner `Dump` over mTLS → per-run network +
  proxy/tunnel → dump stream → zstd → age → sha256 → fan-out to destinations
  → signed manifest.
- **Restore (tasks 4.1–4.5):** fresh 2FA check → stage the encrypted
  artifact → verify hash, signature and DB cross-check → decrypt → runner
  `Restore` (PG temp DB + swap; MySQL/MariaDB in place after a warning).
- **Download (task 3.9):** step-up → single-use, 60 s, session-bound ticket
  → streamed raw or plain file with `no-store`.
- **Notifications (tasks 5.1–5.3):** event → recipient resolver (authz at
  delivery) → inbox / SMTP / HMAC webhooks through the SSRF guard.
- **Offline recovery (task 4.7):** `bekup verify` / `bekup decrypt` on an
  operator machine, no bekup DB needed.

## Runner design

Filled in by tasks 2.4–2.7a: gRPC API, per-run network lifecycle, TCP proxy
and SSH tunnel, preamble wrapper, orphan reaper. Until then, see D-029–D-035
in [DECISIONS.md](DECISIONS.md).
