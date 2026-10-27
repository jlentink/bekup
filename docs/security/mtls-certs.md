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

## Creating the PKI

```sh
bekup certs init --dir secrets/certs            # prompts for a CA key passphrase
just certs                                       # same, into secrets/certs
docker compose --profile setup run --rm certs-init   # compose (runner + serve + postgres)
```

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
  volume. In compose only the one-shot `certs-init` container sees it.
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
files must be owned by the container UID (65532); see
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
