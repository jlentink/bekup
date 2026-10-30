# Runner mTLS certificates

`serve` (and a separately deployed `worker`) talk to `bekup runner` over gRPC
with mutual TLS. The runner is the only component with Docker access, so
whoever holds a valid client certificate can drive it. This page describes
the private PKI that `bekup certs` creates, how to distribute it, and how to
rotate and monitor it. Decisions: D-030, D-089, D-090. The same CA also
issues the app database's TLS server certificate (component `postgres`,
D-100; see [App DB TLS](app-db-tls.md)).

## Properties

- **Private CA**, ed25519, path length 1 (only for the one-time postgres
  issuer, D-104; runner mTLS accepts only certificates issued directly by
  the CA), **name-constrained** (critical):
  DNS names only the server names (`runner`, `postgres` and the extra
  `--runner-san` / `--postgres-san` names), URIs only the `bekup` trust
  domain, and no IP addresses unless you list them. Default validity 5 years (max 10).
- **Leaf certificates**, ed25519, fresh key per issue, 128-bit random serial,
  default validity 1 year (max 825 days, never beyond the CA).
- **TLS 1.3 only** on both sides. Both sides trust **only** the private CA
  bundle, never the system roots. The runner requires and verifies a client
  certificate on every connection (no session resumption).
- Key files are read with the secret-file checks (owner = current user or
  root, not group/world-writable, not world-readable); certificates and CA
  bundles must not be group/world-writable.

## Identity scheme

| Component | Role | SAN | EKU |
|---|---|---|---|
| `runner` | server | `DNS:runner` (+ `--runner-san` names/IPs) | serverAuth |
| `serve` | client | `URI:spiffe://bekup/serve` | clientAuth |
| `worker` | client | `URI:spiffe://bekup/worker` | clientAuth |
| `postgres` | server (app DB TLS) | `DNS:postgres` (+ `--postgres-san` names/IPs); ECDSA P-256 leaf under a one-time ECDSA issuer, see [App DB TLS](app-db-tls.md) | serverAuth |

- A client is identified by **exactly one** URI SAN `spiffe://bekup/<name>`
  and must carry no DNS, IP or email SANs. The runner accepts a connection
  only if that identity is listed in `runner.toml`:

  ```toml
  [tls]
  allowed_client_sans = ["spiffe://bekup/serve", "spiffe://bekup/worker"]
  ```

  Remove `spiffe://bekup/worker` if you do not run a separate worker.
- The client verifies the runner's certificate against `bekup.toml`
  `[runner] server_name` (default `runner`, the compose service name). If
  the runner is reachable under another name, issue it with
  `--runner-san <name>` at init time: the name also becomes part of the CA's
  name constraints, so adding a name later requires a CA rotation.
- The EKUs keep roles apart: the runner's certificate can't be used as a
  client certificate, and client certificates can't serve TLS.

## The runner's gRPC transport (task 2.4)

How `bekup runner` and its clients use these certificates (D-126, D-127):

- **Server:** TLS 1.3 only, a verified client certificate from the private
  CA is required on every connection (no session resumption), the client's
  identity must be in `allowed_client_sans` (checked in the handshake and
  again for every call). Messages are capped at 1 MiB + 64 KiB (stream
  chunks ≤ 1 MiB), at most 64 streams per connection, 16 KiB of metadata,
  10 s for the handshake. Every call must carry a deadline (clamped to
  `limits.max_run_duration`); unknown methods are denied. Backup/restore
  calls share `limits.max_concurrent_runs` slots and control calls 8
  slots; a full class fails at once with `RESOURCE_EXHAUSTED`, `Health` is
  never limited. Connections are recycled after 30 min (idle after
  15 min); a running dump keeps its connection for up to
  `max_run_duration` + 1 min, so an expired or removed client cannot keep
  an old connection open forever. Logs carry method, caller, status code
  and duration only, never payloads.
- **Client (`serve`, `worker`):** TLS 1.3 with the CA bundle as the only
  root and the runner's DNS name from `[runner] server_name`; HTTP proxies
  from the environment are ignored; no transparent retries (only `Health`
  is retried, on `UNAVAILABLE`); keepalive pings every 60 s.
- **Certificate rotation** needs a restart of the runner (it loads its
  certificate and allowlist at startup). The runner refuses to start with a
  certificate that is not valid at that moment and warns 30 days before
  expiry.
- **Container healthcheck without a client certificate** (D-125): the
  runner also serves the standard gRPC health service, and only that, on a
  container-local unix socket (`[listen] health_socket`, default
  `/tmp/bekup-runner/health.sock` on the container's private `/tmp`
  tmpfs). The directory is `0700` and the socket `0600`, both owned by the
  runner user; the runner refuses a directory that is a symlink, belongs to
  someone else or grants group/other access. It reports `SERVING` only while
  the mTLS listener is up, the certificate is valid and no shutdown has
  started. `bekup healthcheck runner` queries it. No certificate, key or
  allowlist entry exists for the healthcheck.

## Creating the PKI

```sh
bekup certs init --dir secrets/certs            # prompts for a CA key passphrase
just certs                                       # same, into secrets/certs
docker compose --profile setup run --rm certs-init   # compose from a source checkout
```

Production installs get the PKI from `bekup setup init` (D-203,
[compose installation](../installation/compose.md#install-with-bekup-setup-container-only)):
it runs `certs init` for runner, serve and postgres with a passphrase-sealed
CA key, writes the CA directly to the offline media mounted at
`/deploy/secrets/certs/ca` (setup and `setup check` warn while `ca.key`
lies on the host's disk instead), hands every file to its consumer's UID
and prints the certificate fingerprints as evidence.

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--dir <dir>` | required | output directory |
| `--component <name>` | all | `runner`, `serve`, `worker` (repeatable) |
| `--runner-san <name\|ip>` | none | extra runner DNS names/IPs (repeatable); `runner` is always included |
| `--ca-days <n>` | 1825 | CA validity (max 3650) |
| `--leaf-days <n>` | 365 | leaf validity (max 825) |
| `--ca-passphrase-file <path>` | prompt | file holding the CA key passphrase (min 16 characters) |
| `--unencrypted-ca-key` | off | store the CA key without a passphrase (not recommended) |
| `--force` | off | overwrite existing material (creates a **new** CA) |

`init` refuses to overwrite anything unless `--force` is given. Files are
written atomically (temp file, fsync, rename).

### Output layout

```
<dir>/
  ca/ca.crt            0644  CA certificate
  ca/ca.key            0600  CA private key, age-encrypted  -> move OFFLINE
  runner/runner.crt    0644  runner server certificate
  runner/runner.key    0600  runner private key
  runner/ca.crt        0644  CA trust bundle for the runner
  serve/serve.crt      0644  serve client certificate
  serve/serve.key      0600  serve private key
  serve/ca.crt         0644  CA trust bundle for serve
  worker/...                 same for worker
```

Directories are `0700`. A component directory contains only what that
component needs and **never** the CA key. During a CA rotation `ca/` also
holds `ca.next.{crt,key}` or `ca.prev.crt`.

## CA key handling

The CA key can mint certificates the runner accepts, which is equivalent to
Docker access on the runner host.

- It is encrypted with an **age scrypt passphrase** (D-090). Enter the
  passphrase at the prompt, or pass `--ca-passphrase-file` pointing at a
  `0600` file (never on the command line or in the environment); delete that
  file afterwards. Store the passphrase in your password manager, next to
  the master-key escrow.
- After `init`, **move `<dir>/ca/` offline** (encrypted USB stick, password
  manager attachment, HSM-backed vault) and delete it from the host. It is
  needed again only for `bekup certs rotate`.
- Never mount `ca/` into a long-running container or put it on a shared
  volume. In compose only the one-shot `certs-init` or `bekup setup`
  container sees it.
- `--unencrypted-ca-key` exists for throwaway dev PKIs
  (`deploy/dev/gen-dev-keys.sh`). Do not use it in production.

## Distribution via Docker secrets

Each service gets exactly its own directory as file-based Docker secrets
(see `docker-compose.yml`):

| Host file | Service | Mounted as | Config key |
|---|---|---|---|
| `secrets/certs/serve/serve.crt` | serve | `/run/secrets/bekup_client.crt` | `[runner] cert_file` |
| `secrets/certs/serve/serve.key` | serve | `/run/secrets/bekup_client.key` | `[runner] key_file` |
| `secrets/certs/serve/ca.crt` | serve | `/run/secrets/bekup_ca.crt` | `[runner] ca_file` |
| `secrets/certs/runner/runner.crt` | runner | `/run/secrets/runner.crt` | `[tls] cert_file` |
| `secrets/certs/runner/runner.key` | runner | `/run/secrets/runner.key` | `[tls] key_file` |
| `secrets/certs/runner/ca.crt` | runner | `/run/secrets/bekup_ca.crt` | `[tls] client_ca_file` |

Use each component's own `ca.crt` bundle, not `ca/ca.crt`: the bundles are
what changes during a CA rotation, and `ca/` should be offline. On Linux the
keys must be owned by the reading container UID (65532, postgres 70), which
`bekup setup` applies; see
[compose installation](../installation/compose.md).

## Rotation

Rotation needs the CA key: restore `ca/` temporarily, rotate, then move it
offline again. Restart the affected services after each step so they load
the new files. Back up the certificate directory before rotating.

### Leaf certificates

```sh
bekup certs rotate --dir secrets/certs --component serve   # one component
bekup certs rotate --dir secrets/certs --all               # every component present
```

A rotation (and `certs init --force`) is recorded as `certs.rotated` in the
audit log, so `bekup certs rotate` needs the app database configured and
reachable; it checks this before changing any file. Where that's impossible
(e.g. the compose `certs-init` container, which has no DB access), pass
`--no-audit`: the command prints a warning and the rotation is not audited.

With a `bekup setup` deployment directory, rotate in a one-shot root
container with only `CAP_CHOWN` and `CAP_FOWNER` (the files belong to the
container users) and the offline CA mounted, then re-apply the ownership
manifest:

```sh
docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp --user 0:0 \
  --cap-drop ALL --cap-add CHOWN --cap-add FOWNER --security-opt no-new-privileges \
  -v /opt/bekup:/deploy -v /media/usb/bekup-ca:/deploy/secrets/certs/ca \
  "$IMAGE" certs rotate --dir /deploy/secrets/certs --all --no-audit
docker run --rm --log-driver none --network none --read-only --tmpfs /tmp --user 0:0 \
  --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
  -v /opt/bekup:/deploy "$IMAGE" setup check --fix-ownership
docker compose up -d --force-recreate serve runner postgres
```

`--log-driver none` keeps the container's output (the passphrase prompt)
out of Docker's logs. `setup check --fix-ownership` needs only `CAP_CHOWN`:
a file that already belongs to a container user is taken over, chmod'ed and
handed back.

**Offline CA media.** Use a POSIX file system for the CA media (for
example ext4 on a LUKS-encrypted USB stick), so `ca.key` keeps mode 0600 and
owner root. FAT/exFAT have no Unix modes or owners: `bekup setup` then
leaves the CA directory's modes and owners as they are with a warning
(`setup check` reports them as warnings, not errors), which is acceptable
only because the CA key is age-encrypted with its passphrase. Writing the
certificates may still fail on such media unless it is mounted so that
chmod succeeds (vfat `quiet`).

`bekup setup init` never reuses, overwrites or rolls back an existing CA:
with `ca.crt`/`ca.key` already in `secrets/certs/ca` (for example the media of
an earlier install) it refuses before writing anything. Issue the runner,
serve and postgres certificates from that CA with `certs rotate` and re-run
`setup init --keep-existing`, or move the CA away. `--regenerate-secrets`
replaces the CA too, **including an offline CA mounted there**; the old files
are kept aside until the run succeeded and restored on any failure.

Record the rotation in the audit log afterwards (it ran with `--no-audit`
because the container has no database access). A `setup rotate-certs`
wrapper is planned.

Each rotation creates a new key and certificate from the existing CA; the CA
and the other components are untouched. The runner's extra names are kept
unless `--runner-san` is given. `--leaf-days` sets the validity. Rotate
leaves well before expiry (for example yearly, or immediately if a key may
have leaked) and restart the component.

### CA rotation (overlap procedure)

A CA rotation runs in three phases so that components restarted at different
times can always talk to each other. Restart **all** components after each
phase before starting the next.

1. `bekup certs rotate --dir <dir> --ca stage`
   creates the next CA (`ca/ca.next.{crt,key}`, same passphrase) and writes
   a bundle with **old + next** CA into every component directory. Leaves are
   unchanged. Use `--runner-san` / `--postgres-san` here to change the
   server names (and thus the name constraints), `--ca-days` for the new
   CA's validity. Without them the current names are kept and `postgres` is
   added if missing (CAs from before D-100).
2. `bekup certs rotate --dir <dir> --ca promote`
   makes the next CA current (the old one is kept as `ca/ca.prev.crt`),
   reissues every leaf from the new CA, and keeps bundles with **new + old**.
   A not-yet-restarted component (old leaf, bundle from phase 1) still
   connects.
3. `bekup certs rotate --dir <dir> --ca finish`
   refuses while any leaf is not issued by the current CA; otherwise it
   writes bundles with only the **new** CA and deletes `ca/ca.prev.crt`.

Leaf rotation is refused between `stage` and `promote`. If a phase is
interrupted, restore the backup and repeat it; the CA key/certificate pair is
checked on every load, so a half-finished swap fails loudly instead of
producing mismatched material.

If the CA key may have leaked, run all three phases right away (restarting
everything in between) and destroy every copy of the old CA key.

## Expiry monitoring

```sh
bekup certs inspect --dir secrets/certs              # all *.crt files, recursively
bekup certs inspect secrets/certs/serve/serve.crt    # or individual files/dirs
bekup certs inspect --dir secrets/certs --warn-days 60
```

For every certificate (each one in a bundle) `inspect` prints the status,
days left, subject, issuer, SANs, validity window, serial, SHA-256
fingerprint and SPKI SHA-256 (base64, pin format). It exits **non-zero** when
any certificate is expired, not yet valid, or expires within `--warn-days`
(default 30), so it can run from cron or a CI check. It never reads private
keys.

The library type `certs.ExpiryInfo` (`internal/certs`) is the basis for the
certificate-expiry metric and alert (tasks 5.5/5.6).
