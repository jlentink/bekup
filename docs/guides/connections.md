# Connections

A connection is a PostgreSQL, MySQL or MariaDB server that bekup backs up.
Admins manage connections on the **Connections** page of the web UI
(D-182) or through the GraphQL API (D-135). serve never
connects to a database or bastion itself: every test, version check,
database list, image pull, certificate capture and host-key capture runs on
the runner over its mTLS API.

## In the web UI

- **Connections** lists every connection with its engine and version,
  host, TLS mode and pin state (expiry warnings at 30 and 7 days), SSH
  tunnel, the role's privilege verdict from the last test, and the
  concurrent-run limit (effective / configured). Maintainers
  see the same list read only.
- A connection's page runs **Test connection** (version, client
  compatibility, warnings and the privilege verdict with its reasons),
  **Detect version**, **List databases** and **Pre-pull image**, each as its
  own request.
- **Pin certificate** captures the chain, shows every certificate (subject,
  issuer, names, validity, SPKI and certificate fingerprints) with the
  suggested anchor marked, and pins it only after you tick that you compared
  the leaf's SPKI fingerprint out of band and confirm your identity. A root
  CA the server does not send can be pasted as PEM. **Stage backup pin**
  prepares a planned rotation. Public CAs are refused (D-139).
- **Change TLS mode** asks you to type the connection name before any
  weakening; the opt-out is audited, alerts every admin and adds the
  permanent *Insecure transport* badge.
- For a tunnel, **Fetch and confirm host key** shows the bastion's key type,
  fingerprint and key line for comparison; **Generate keypair** stores a new
  ed25519 key encrypted and shows the public line to install.
- Passwords and keys are write-only: the form only shows that one is
  stored. When you change the host, port or tunnel (or the bastion or its
  user), the form asks for the stored password again before saving.

## Who may do what

| Action | Admin | Maintainer | Backup owner / user |
|---|---|---|---|
| List / view (name, engine, version, host, port, TLS mode, pin status and expiry, SSH enabled, run limit) | yes | yes (metadata only) | no |
| Full settings (image key, user, credential flags, pins, SSH settings) | yes | no | no |
| `testConnection`, `detectVersion`, `listDatabases`, `prePullImage` | yes | yes | no |
| `fetchCertificate`, `fetchSshHostKey` | yes | no | no |
| Create, update, delete, `setTlsMode`, confirm/stage pins, confirm host key, generate SSH keypair | yes, **with step-up** | no | no |
| Temporary restore connections (`temporaryConnections`; create, extend, delete with step-up) | yes | no | no |

Every change is audited (`connection.*`; credential changes additionally as
`secret.changed`, never with values). Credentials are **write-only**: the API
accepts a password, SSH password or key and TLS client key, and only ever
answers whether one is stored (`hasPassword`, `ssh.authMethod`, ...). They
are stored envelope-encrypted per connection.

## Adding a connection (admin flow)

1. **Create** with `createConnection`: name, engine (`POSTGRES`, `MYSQL`,
   `MARIADB`), `versionKey` (the server's series from `engineVersions`, e.g.
   `17`, `8.4`, `11.8`), optionally `imageKey` (the client image; default:
   the version key, and for PostgreSQL never older than the server), host,
   port (default 5432 / 3306), user, password. The TLS mode is `PINNED`
   unless you choose otherwise (see [Downgrades](#downgrades)). There is no
   database field: jobs select databases.
2. **Fetch the certificate** with `fetchCertificate`. The runner captures
   the chain without sending credentials and bekup shows each certificate
   (subject, issuer, SANs, validity, SPKI SHA-256 in base64 and hex, DER
   SHA-256) and the suggested anchor. Nothing is stored yet.
3. **Compare out of band**, then **confirm** with `confirmCertificatePin`
   (step-up): pass `certIndex` (normally `suggestedAnchorIndex`) and the
   `leafSpkiSha256` you compared. bekup captures the chain **again** and
   refuses the pin if the server meanwhile presents another leaf key, or
   if the captured chain does not verify with the chosen certificate as
   its only root for the connection's host (the check the runner applies
   before every connection; D-140). If the server does not send its root
   CA, pass the root as `certificatePem` instead of `certIndex` (an
   out-of-band PEM is not checked against a capture, so it can be staged
   before the server presents it). A CA pin may set
   `allowLeafRotation` to accept any leaf that CA issues for the host
   (weaker; the default binds the leaf key).
4. **Test** with `testConnection` (also open to maintainers). It logs in
   with the stored credentials through the runner and reports `ok`, the
   server version, the detected catalog key, whether the client image fits
   (`compatible`), a legacy flag and warning codes. Ordinary failures
   (unreachable, wrong password, TLS refused, ...) come back as `ok: false`
   with a fixed `code` and `message`. The outcome is stored on the
   connection as `lastTest { at ok code stale privileges { status reasons } }`
   (D-187; admins and maintainers): the normalized code only (plus
   `TLS_PIN_MISMATCH` / `SSH_HOST_KEY_MISMATCH` for those security events),
   never a raw error. `stale` turns true once the connection's settings
   change; a test that races such a change, or one the runner could not
   take (busy, unavailable), stores nothing.
5. Optionally `detectVersion` (suggests the catalog key to use),
   `listDatabases` (for job building) and `prePullImage` (pulls the client
   image by digest on the runner ahead of the first run).

A connection is `ready` once it has a confirmed pin (in `PINNED` mode) and,
with an SSH tunnel, a confirmed host key and an SSH credential;
`notReadyReason` says what is missing. Runner operations on a connection
that is not ready answer `CONFLICT`.

Changing the host or port removes both pins (confirm again); the engine
cannot change.

**Stored passwords never follow an edited endpoint** (D-140). When an
update changes the database host or port, adds or removes the SSH tunnel,
or moves the bastion (host or port), a stored database password must be
entered again (`password`) or removed (`clearPassword`) in the same
update; when it changes the bastion's host, port or user, a stored SSH
password must be entered again (or replaced by a private key). Otherwise
the update fails with `VALIDATION`. Private keys and the TLS client key
stay. The audit entry lists the re-entered fields
(`reentered_for_new_endpoint`). Runner calls are rate limited per user (10 per minute) and
each must be the only field of its GraphQL operation.

## Temporary restore connections

A temporary restore connection points a restore at a server that bekup does
not back up (task 4.4, D-163). It is **admin only** and is meant to be
created from the restore wizard; see
[Restore: requesting a restore](restore.md#requesting-a-restore).
In short: it is validated like any connection (TLS `PINNED` by default,
D-132), never appears in `connections` or `connection`, cannot be used by a
job or edited with `updateConnection`/`setTlsMode`/`deleteConnection`, and
is deleted with its stored credentials destroyed when it expires
(`restore.temp_connection_ttl`, default 24 h) or shortly after the restore
that used it finished. Maintainers never see it. The `Connection` type
carries `temporary` and `expiresAt`.

## Concurrent runs per connection

Each connection limits how many backup and restore runs work against it at
the same time (task 3.13, D-156, D-158). By default the global
`[workers] max_runs_per_connection` (default 1) applies; set
`maxConcurrentRuns` on `createConnection` or `updateConnection` to give one
server its own limit, and `clearMaxConcurrentRuns: true` to return to the
default. Changing it is admin only with a fresh step-up, like every
connection change, and is audited (`connection.updated` lists
`max_concurrent_runs` with the old and new value).

- Allowed values: 1 to 64, and never more than `[workers]
  max_concurrent_runs` (the global cap); anything else answers
  `VALIDATION`.
- `maxConcurrentRuns` is the connection's own value (null = default);
  `effectiveMaxConcurrentRuns` is the limit that applies. Admins and
  maintainers see both.
- A run that finds the limit reached stays `queued` and retries about
  every 15 seconds until a slot frees up; it never fails because of the
  limit.
- **Backups and restores share the limit.** Both take a run slot on the
  connection, so with a limit of 1 a restore waits behind a running backup
  of the same connection (and a backup behind a restore). Raise the limit,
  or cancel the backup, if a restore must start at once.
- A stored limit above a since lowered `[workers] max_concurrent_runs` is
  kept but clamped: `effectiveMaxConcurrentRuns` shows the lower value, and
  the global cap is always enforced when a run takes its slot.
- **Lowering the limit never stops running backups.** New runs wait until
  fewer runs than the new limit are active on the connection.

Raise the limit only if the database server can take several dumps at
once (CPU, I/O and connection slots on the server). See
[Operations](../operations.md#scaling-background-work) for how it relates
to the global settings.

### Errors

| Code | Meaning |
|---|---|
| `TLS_PIN_MISMATCH` | The server's certificate no longer matches the pin. Recorded as `connection.tls_pin_mismatch` and raised as a security alert. Re-pin only after verifying the new certificate. |
| `SSH_HOST_KEY_MISMATCH` | The bastion presented another host key. Recorded as `connection.ssh_host_key_mismatch` and alerted. |
| `CONNECTION_FAILED` | The runner is unreachable, its target allowlist refuses the host, or the database failed (fixed message). |
| `CONFLICT` | Not ready, name taken, or changed concurrently. |
| `VALIDATION` | Invalid input (the message names the field). |

## TLS and certificate pinning

Every connection uses TLS with **certificate pinning** by default (TLS mode
`pinned`, D-034, D-123, D-133). The runner, never serve, talks to the
database.

- **Capture.** When you add or re-pin a connection, bekup asks the runner
  to fetch the certificate chain the server presents. The runner speaks just
  enough of the database protocol to start TLS (PostgreSQL `SSLRequest`, or
  direct TLS for PostgreSQL 17+; the MySQL/MariaDB SSL request), records the
  chain and aborts the handshake. **No user name, password or database is
  ever sent** during the capture. You see each certificate's subject,
  issuer, SANs, validity and SPKI SHA-256 (base64 and hex), plus a PEM.
- **What gets pinned.** bekup suggests the anchor: a **self-signed server
  certificate** pins itself; otherwise the **self-signed root CA** that the
  presented chain leads to. If the server does not send its root CA, supply
  it yourself (an intermediate alone does not work with the native
  clients). The leaf's SPKI SHA-256 is always pinned as well. The anchor
  must be a self-signed server certificate or a **private** CA: a public
  CA (one in the system trust store, or chaining to it) is refused, because
  the native client would then trust every certificate that CA issues for
  the host name (D-139). With a private CA, another leaf of the same CA
  passes the native client but not the runner's pre-flight; pin the
  self-signed server certificate if you need the native client itself to
  refuse other leaves.
- **Confirm.** Compare the fingerprint out of band (for example
  `openssl x509 -in server.crt -noout -pubkey | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | base64`)
  before you confirm. Confirming or changing a pin needs an admin with
  step-up and is audited.
- **Enforcement.** Before every connection (test, dump, restore, create,
  swap) the runner opens a TLS connection itself and requires that the chain
  verifies against the pinned anchor (as the only root, including the host
  name or IP address and the validity) **and** that the server's public key
  equals the pinned leaf key. Only then does it start the dump container,
  whose client (`pg_dump`, `mysqldump`, `mariadb-dump`, ...) receives the
  pinned certificate as its **only** CA file (`sslrootcert` +
  `sslmode=verify-full`, `--ssl-ca` + `--ssl-mode=VERIFY_IDENTITY`,
  `--ssl-ca` + `--ssl-verify-server-cert`). System CA roots are never used.
- **Mismatch = security event.** A certificate from another CA, another
  leaf key (even from the pinned CA), a name/IP mismatch or an expired
  certificate fails the run hard and raises a security alert. Re-pin only
  after you verified the new certificate.
- **Planned rotation: stage a backup pin.** Before the server's certificate
  changes, upload the new anchor with `stageBackupPin` (step-up; it needs a
  current pin). Both pins are accepted (each with its own leaf key); runs
  report `tls_staged_pin_in_use` once the server switched. Promote it by
  confirming the same pin with `confirmCertificatePin` (the staged slot is
  then cleared).
- **Expiry.** `pinStatus` turns `EXPIRES_WITHIN_30_DAYS`,
  `EXPIRES_WITHIN_7_DAYS` and `EXPIRED` (also visible to maintainers), and
  runs warn 30 and 7 days before the pinned chain expires.
- **Certificates need SANs.** The server certificate must name the host
  you configure as a DNS SAN (or the IP address as an IP SAN); a
  certificate with only a common name fails the pre-flight. SHA-1 signed
  certificates are refused.
- **Leaf key vs. CA pin.** With a CA pin, the native client alone would
  accept any certificate of that CA; the runner's leaf-key check stops such
  a certificate before the client runs. If you want the native client to
  refuse other leaves on its own as well, use a self-signed server
  certificate as the pin.
- <a id="downgrades"></a>**Downgrades.** `setTlsMode` to `UNVERIFIED` (TLS
  without verification) or `DISABLED` (no TLS), or creating a connection
  with such a mode, is an admin-only, step-up downgrade: it is audited as
  `security.opt_out` (`setting: connection.tls_mode`), raises a security
  alert and sets the `insecureTransport` badge. Pins are kept, so switching
  back to `PINNED` needs no new capture. Prefer fixing the server
  certificate.

## SSH tunnels

A connection can reach its database through an SSH bastion. The runner
opens the tunnel (`internal/tunnel`, D-035, D-122); serve never connects to
the bastion.

- **Set up.** Add the tunnel with `updateConnection` (or at creation):
  `ssh: { host, port, user, password | privateKey [+ privateKeyPassphrase] }`.
  `disableSsh: true` removes the tunnel with its credentials and host-key
  pin; changing the bastion's host or port removes the host-key pin.
- **Host-key pin (mandatory).** There is no way to skip host-key checking.
  `fetchSshHostKey` makes the runner fetch the host key the bastion
  presents (key exchange only, no login attempt, through the runner's target
  allowlist) and shows its type, SHA-256 fingerprint and authorized-key
  line. Compare it out of band, for example with
  `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the bastion, before
  you confirm it with `confirmSshHostKey` (step-up; you can also paste the
  public key line yourself). The fetch result names the bastion it came
  from (`host`, `port`); pass them as `expectedHost` / `expectedPort` when
  confirming: if the connection's bastion changed in the meantime, the
  confirmation fails with `CONFLICT` (D-140). Host-key lines are at most
  8 KiB, one line, without options.
- **Generated keys.** `generateConnectionSshKeypair` (step-up) creates an
  ed25519 key, stores the private key encrypted (replacing a stored SSH
  password or key) and returns only the public key for the bastion's
  `authorized_keys`.
- **Accepted host keys:** ed25519 (preferred), ECDSA P-256/384/521, RSA of at
  least 2048 bits with SHA-2 signatures. DSA keys, SHA-1 RSA signatures and
  host certificates are refused.
- **Mismatch = security event.** If the bastion presents a different key,
  or no longer offers a key of the pinned type, the run fails and a
  security alert is raised. Re-pin only after you verified the new key.
- **Authentication:** an SSH private key (optionally passphrase-protected;
  ed25519, ECDSA or RSA ≥ 2048 bits) or a password. Keys are preferred: a password briefly exists as an
  unzeroable string inside the SSH library.
- **Algorithms:** curve25519 / ML-KEM hybrid key exchange,
  chacha20-poly1305 or AES-GCM, and HMAC-SHA2 (etm). Bastions need a
  reasonably modern OpenSSH and `AllowTcpForwarding yes` for the bekup
  user.
- The bastion address must be in the runner's target allowlist; the tunnel
  forwards to exactly one database host and port.

## Database host: names and IP addresses

Host names follow one rule everywhere in bekup (`internal/hostname`,
D-140): an IP address without zone, or a DNS name of letters, digits and
inner hyphens (no underscores, no trailing dot, not digits and dots only).

Prefer a DNS name that the server certificate covers. With pinned TLS
(`verify-full`), an IP address as the database host works only through an
SSH tunnel (the certificate then needs that IP address as a SAN); a direct
connection to an IP address with pinned TLS is refused. See
[the target allowlist](../security/target-allowlist.md#ip-literal-targets-and-tls-verification).
