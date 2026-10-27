# Destinations

> **Status:** partial. The backends (disk, S3, SFTP; tasks 2.8–2.10) and
> the GraphQL API (task 2.12) exist; the UI follows in task 6.4. Until
> then, destinations are managed through the GraphQL API described in
> [Managing destinations](#managing-destinations).

A destination is a reusable place where bekup writes backup artifacts:
local disk (confined to the disk root), S3 and S3-compatible storage
(Object Lock, no-delete mode, custom CA) and SFTP with pinned host keys.

## What every destination guarantees

- **Atomic writes.** An artifact appears under its final name only after
  every byte was written, checked against its SHA-256 and made durable. A
  failed or canceled upload leaves nothing under the final name.
- **No overwrites.** bekup never replaces an existing object. A name
  collision fails the upload instead.
- **Fail closed on immutability.** Object Lock retention or legal hold is
  only requested on destinations that support it; on others the upload
  fails instead of silently writing an unprotected object.
- **No-delete mode.** A destination in no-delete mode never deletes
  anything: retention then relies on the storage's own lifecycle rules.

## Layout

```
{prefix}/{job-slug}/{2026-10-26T030000Z}/{database}.dump.zst.age
{prefix}/{job-slug}/{2026-10-26T030000Z}/manifest.json
```

The timestamp is the run start in UTC. Database names are percent-encoded:
letters, digits, `_` and `-` stay as they are, every other byte becomes
`%XX` (for example `my.db` → `my%2Edb`). Names whose encoding would exceed
180 bytes are stored as `~` plus a hash; the manifest holds the real name.
Job slugs, database names and timestamps are visible to whoever can list
the destination (the contents are encrypted).

## Local disk

Disk destinations live below `storage.disk_root` in `bekup.toml` (default
`/var/lib/bekup/backups`); a destination may name a subdirectory. bekup
cannot write outside that root, never follows symbolic links in it and
creates files with mode `0600` and directories with mode `0700`. While an
artifact is written it is a hidden `.<random>.partial` file next to its
final name; leftovers after a crash are never treated as backups and are
removed by the periodic maintenance (scheduled with retention, task 3.8).

When the size of an upload is known in advance, bekup checks the free
space of the file system first.

Notes:

- Disk destinations offer no immutability: anyone with write access to the
  directory can delete backups. Use S3 Object Lock for ransomware
  resistance and keep at least one copy off the host.
- On case-insensitive file systems (macOS), names that differ only in case
  collide; bekup reports a conflict and never overwrites.
- The file system must support hard links for the strictest no-overwrite
  guarantee; without them bekup falls back to an existence check plus
  rename.

## S3 and S3-compatible storage

An S3 destination needs a bucket, a region and a static access key pair.
Optional: an endpoint URL for S3-compatible stores (minio, Ceph, Wasabi,
...), path-style addressing, a key prefix inside the bucket, a CA bundle,
server-side encryption, a storage class and the immutability settings
below.

| Setting | Notes |
|---|---|
| Endpoint | Empty for AWS. Must be `https://host[:port]` (no path, query or user info). `http://` is accepted only in dev mode and logs a warning. |
| Region | The signing region (minio: `us-east-1`). |
| Path-style | `{endpoint}/{bucket}` instead of `{bucket}.{endpoint}`; needed by most S3-compatible stores and for bucket names with dots. |
| Prefix | Every object lives below it; the probe as well. |
| Access key | Static keys only. bekup never uses environment variables, shared config files, container credentials or instance metadata, so an EC2/ECS role cannot be borrowed by accident. The key is stored envelope-encrypted and never shown again. |
| CA bundle | PEM; when set it **replaces** the system roots for this destination (for a private CA such as a minio behind an internal PKI). Without it the system roots are used. Certificates are not pinned (D-018). |
| Server-side encryption | None, SSE-S3 (`AES256`) or SSE-KMS (optional key ID / alias / ARN). This is in addition to bekup's own age encryption. |
| Storage class | `STANDARD`, `STANDARD_IA`, `ONEZONE_IA`, `INTELLIGENT_TIERING`, `GLACIER_IR`, `REDUCED_REDUNDANCY`. Archive classes (`GLACIER`, `DEEP_ARCHIVE`) are refused because a restore could not read them directly; use lifecycle transitions if you need them. |
| Checksums | `when_supported` (default): a SHA-256 checksum with every upload request, verified by the store, and response checksums are validated. `when_required`: only what the protocol needs (Content-MD5 on Object Lock buckets), for stores without the flexible checksum headers. |
| Conditional writes | On by default: uploads send `If-None-Match: *`, so the store itself refuses to overwrite an existing object. Turn off only for stores that reject the header; bekup then relies on an existence check before the upload, and two uploads racing for the same name could both succeed (on a versioned bucket the earlier one survives as an older version). |
| Part size / concurrency | Default 64 MiB parts, 2 in parallel. Memory per upload is part size × concurrency (128 MiB by default). The largest object is part size × 10,000 (625 GiB by default); raise the part size for bigger dumps. The minimum is 5 MiB. |

### Network access

Every request goes through bekup's SSRF guard. Public endpoints work out
of the box. Endpoints on private addresses (a minio in your LAN, a
`10.x`/`192.168.x` appliance) must be added to the network allowlist
(`[netguard]` in `bekup.toml` or the admin allowlist); link-local and
cloud-metadata addresses can never be allowed. Proxies are not supported
and redirects are never followed (a wrong region shows up as an error
instead of a silent redirect).

### How bekup writes

Small artifacts (up to one part) are uploaded with a single request. Larger
ones use a multipart upload: the object only appears when the upload is
completed, after bekup has checked the SHA-256 of everything it streamed.
If anything fails, including a cancelled run, bekup aborts the multipart
upload so neither a partial object nor billable parts stay behind. A crash
of bekup itself can still leave an incomplete upload; add a lifecycle rule
that aborts incomplete multipart uploads after one day (example below).

The SHA-256 is stored as object metadata (`x-amz-meta-bekup-sha256`) when it
is known before the upload starts (single-request uploads); for multipart
uploads the signed manifest and the bekup database hold it, and the store
keeps a composite SHA-256 checksum of the parts. Downloads and restores
always stream through bekup; bekup never creates presigned URLs.

### Required permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "BucketInfo",
      "Effect": "Allow",
      "Action": [
        "s3:ListBucket",
        "s3:ListBucketVersions",
        "s3:ListBucketMultipartUploads",
        "s3:GetBucketVersioning",
        "s3:GetBucketObjectLockConfiguration"
      ],
      "Resource": "arn:aws:s3:::my-bekup-bucket"
    },
    {
      "Sid": "Objects",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:GetObjectVersion",
        "s3:GetObjectRetention",
        "s3:GetObjectLegalHold",
        "s3:PutObjectRetention",
        "s3:PutObjectLegalHold",
        "s3:AbortMultipartUpload",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion"
      ],
      "Resource": "arn:aws:s3:::my-bekup-bucket/*"
    }
  ]
}
```

bekup checks the bucket's versioning and Object Lock configuration when it
opens the destination, so the two `GetBucket...` permissions are required.
Drop the `PutObjectRetention`/`PutObjectLegalHold` actions if you do not
use Object Lock. Scope the object resource to your prefix
(`my-bekup-bucket/prefix/*`) if the bucket is shared.

### Object Lock

Object Lock makes backups undeletable until their retention date, even for
bekup's own credentials and (in compliance mode) for the bucket owner. It
can only be enabled when the bucket is created:

```sh
aws s3api create-bucket --bucket my-bekup-bucket --region eu-central-1 \
  --create-bucket-configuration LocationConstraint=eu-central-1 \
  --object-lock-enabled-for-bucket
# minio:
mc mb --with-lock myminio/my-bekup-bucket
```

bekup detects Object Lock automatically. To have bekup lock every artifact,
set the destination's lock mode (governance or compliance) and a default
retention in days; a destination with a lock mode refuses to open on a
bucket without Object Lock. You can also (or instead) configure a default
retention on the bucket itself.

When bekup deletes an artifact (retention, task 3.8) on a versioned or
Object Lock bucket it deletes the object's versions permanently instead of
adding a delete marker, so storage is really freed and a lock is really
respected: a locked version makes the delete fail with "deleting is not
permitted". bekup never sets the governance-bypass header. See
[Immutable backups](../security/immutable-backups.md).

### No-delete mode

In no-delete mode bekup never deletes anything from the destination. Give
the access key no delete permission at all, so that even a compromised
bekup cannot remove backups, and let the bucket's lifecycle rules expire
old backups:

```json
{
  "Sid": "DenyDeletes",
  "Effect": "Deny",
  "Action": ["s3:DeleteObject", "s3:DeleteObjectVersion", "s3:PutLifecycleConfiguration",
             "s3:PutBucketVersioning", "s3:PutBucketPolicy", "s3:BypassGovernanceRetention"],
  "Resource": ["arn:aws:s3:::my-bekup-bucket", "arn:aws:s3:::my-bekup-bucket/*"]
}
```

Lifecycle rules for retention in no-delete mode (example: keep backups for
35 days, clean up probes after a day and abort stale multipart uploads):

```json
{
  "Rules": [
    {
      "ID": "expire-backups",
      "Status": "Enabled",
      "Filter": {"Prefix": "prod/"},
      "Expiration": {"Days": 35},
      "NoncurrentVersionExpiration": {"NoncurrentDays": 1}
    },
    {
      "ID": "expire-probes",
      "Status": "Enabled",
      "Filter": {"Prefix": "prod/bekup-probe/"},
      "Expiration": {"Days": 1}
    },
    {
      "ID": "abort-incomplete-uploads",
      "Status": "Enabled",
      "Filter": {},
      "AbortIncompleteMultipartUpload": {"DaysAfterInitiation": 1}
    }
  ]
}
```

```sh
aws s3api put-bucket-lifecycle-configuration --bucket my-bekup-bucket \
  --lifecycle-configuration file://lifecycle.json
```

Lifecycle expiry ignores bekup's "keep the newest successful backup" rule:
choose an expiry comfortably longer than your backup interval, and monitor
missed runs (task 5.6).

## SFTP

An SFTP destination needs the server's host name and port, a user, either
an SSH private key (ed25519 recommended; ECDSA and RSA ≥ 2048 bits also
work, optionally passphrase protected) or a password, a **pinned host key**
and a base directory on the server (an absolute path such as `/upload`)
with an optional subdirectory that bekup creates.

**Host key pinning is mandatory.** bekup only talks to the server whose
host key matches the pin (key type and SHA-256 fingerprint); there is no
"accept any key" option. A changed host key fails every connection with a
host-key mismatch (code `host_key_mismatch`, a security event: a failed
test records `destination.ssh_host_key_mismatch` in the audit log) until
an admin confirms the new key. Get the fingerprint
out of band, for example on the server:

```
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
# 256 SHA256:3kN...Qd4 root@backup (ED25519)
```

Only strong algorithms are negotiated (curve25519 / ML-KEM hybrid key
exchange, ChaCha20-Poly1305 or AES-GCM); servers that only offer legacy
algorithms are refused.

The connection goes through bekup's SSRF guard: a server on a private
network needs an entry in the admin network allowlist.

### A restricted account

Give bekup a dedicated, chrooted account that can only use SFTP and that
nobody else writes to. With OpenSSH:

```
# /etc/ssh/sshd_config
Match User bekup
    ChrootDirectory /srv/sftp/bekup      # owned by root, not writable by bekup
    ForceCommand internal-sftp -u 0077
    AllowTcpForwarding no
    X11Forwarding no
    PermitTTY no
    PasswordAuthentication no
```

```
install -d -o root -g root -m 0755 /srv/sftp/bekup
install -d -o bekup -g bekup -m 0700 /srv/sftp/bekup/upload
# authorized_keys: the public key bekup generated for this destination
```

Use `/upload` as the base directory.

### How bekup writes

While an artifact is written it is a hidden `.<random>.partial` file
(mode `0600`, directories `0700`) next to its final name. bekup asks the
server to flush it to disk (`fsync@openssh.com`, if supported) and then
publishes it with a hard link (`hardlink@openssh.com`), which fails if the
name already exists, so an existing backup is never overwritten even by
two concurrent uploads. Servers without hard links fall back to an
existence check plus a plain SFTP rename; bekup logs a warning, and a
server that implements that rename as an overwriting rename leaves a small
race. Partial files are never listed as backups and are removed after a
failure or by the periodic maintenance.

bekup never follows symbolic links below the base directory: uploads
through a linked directory are refused, linked files are treated as
missing and skipped in listings. The base directory itself is resolved
once per connection.

### No-delete via server permissions

No-delete mode stops bekup from deleting, but a stolen key could still
delete over SFTP. For real protection also take the delete right away on
the server, for example with OpenSSH's request deny list:

```
Match User bekup
    ForceCommand internal-sftp -u 0077 -P remove,rmdir,rename,posix-rename
```

Keep `hardlink` allowed (bekup needs it to publish uploads) and enable
no-delete mode on the destination. Without the delete right bekup cannot
remove the hidden `.partial` name after publishing (it stays as a second
name of the finished backup, using no extra space) or after a failed
upload, so clean up `.partial` leftovers and old backups with a job on the
server. SFTP
offers no immutability: anyone with delete rights on the server can still
remove backups; use S3 Object Lock for ransomware resistance.

Residual trust: SFTP cannot open files without following links, so
someone who can change the directory tree on the server at the same time
could still redirect an access between bekup's check and its use. The
server is trusted to enforce its permissions and chroot; the backups'
integrity rests on age encryption and the signed manifest.

## Testing a destination

The destination test writes a small probe object under
`{prefix}/bekup-probe/`, reads it back and deletes it. In no-delete mode the
probe is left in place (remove it with a lifecycle rule). Errors are shown
as short, fixed messages such as "the destination refused access" rather
than raw provider responses.

On S3 the probe is written without bekup's default retention. If the bucket
itself has a default Object Lock retention, the probe cannot be deleted:
the test then reports the delete step as passed with a note, and the probe
expires through the lifecycle rules. A missing bucket and a refused request
show the same "refused access" message, and all network failures (DNS,
refused connection, TLS, blocked by the network allowlist) the same
"could not be reached" message; admins find the details in the server log.

The result lists the steps `connect`, `write`, `read` and `delete`, each
with a normalized code (`ok`, `permission_denied`, `host_key_mismatch`,
`network`, `timeout`, ...) and a fixed message. Admins and maintainers may test; tests are
limited to 10 per minute per user (burst 5), a few at a time, and one per
request.

## Managing destinations

Destinations are managed by **admins**; every change (create, update,
delete, host-key confirmation, SSH keypair generation) needs a fresh
step-up and is recorded in the audit log. **Maintainers** see each
destination's name, type, no-delete/Object Lock flags and readiness (the
`settings` field is null for them) and may run the test. Backup owners
see the destinations of their own jobs through the job (task 3.5).

Credentials are **write-only**: you send a secret access key, password or
private key when creating or updating a destination, and from then on the
API only tells you whether one is stored (`hasSecretAccessKey`,
`hasPassword`, `hasPrivateKey`). Leave a credential field out of an update
to keep the stored value, with one exception: when an update changes an
SFTP destination's host, port, user or base path, a stored password must
be entered again (or replaced by a private key) in the same update,
otherwise it fails with `VALIDATION` (a stored password is never sent to
a new endpoint, D-140; the audit entry lists `reentered_for_new_endpoint`).
A private key stays. Credentials are stored encrypted with the master
key and never appear in responses, logs or the audit log (the audit log
lists which fields changed, credentials by name only).

GraphQL operations (all POST to `/graphql`):

| Field | Who | Purpose |
|---|---|---|
| `destinations`, `destination(id)` | admin, maintainer | List / view (settings for admins only) |
| `createDestination(input)` | admin, step-up | Create; exactly the `disk`, `s3` or `sftp` input matching `type` |
| `updateDestination(id, input)` | admin, step-up | A settings input replaces the whole non-secret configuration of its type |
| `deleteDestination(id)` | admin, step-up | Soft delete (the name can be reused) |
| `testDestination(id)` | admin, maintainer | Probe write/read/delete (see above) |
| `fetchDestinationHostKey(id)` | admin | Show the host key the SFTP destination's stored host presents (trust on first use; nothing is stored) |
| `confirmDestinationHostKey(id, authorizedKey, expectedHost, expectedPort)` | admin, step-up | Pin the host key of an SFTP destination (fails with `CONFLICT` if host/port changed since the fetch) |
| `generateDestinationSshKeypair(id)` | admin, step-up | New ed25519 key for an SFTP destination; returns the public key only |

Validation happens when you save: names are unique (case-insensitive);
disk paths must be relative paths below `storage.disk_root` without `..`;
S3 endpoints must be `https://host[:port]` (plain http only in dev mode)
and, like SFTP hosts, must resolve to addresses the network allowlist
permits; a custom CA bundle must contain PEM certificates only; SFTP
private keys must be ed25519, ECDSA or RSA ≥ 2048 bits (with the right
passphrase). A destination that cannot be used yet reports
`ready: false` and a `notReadyReason`.

Example: an S3 destination on minio with a private CA and a default
Object Lock retention:

```graphql
mutation {
  createDestination(input: {
    name: "Offsite minio"
    type: S3
    s3: {
      endpoint: "https://minio.internal:9000"
      region: "us-east-1"
      bucket: "bekup"
      prefix: "prod"
      pathStyle: true
      accessKeyId: "bekup-writer"
      secretAccessKey: "<secret>"
      caBundlePem: "-----BEGIN CERTIFICATE-----\n..."
      objectLockMode: GOVERNANCE
      objectLockRetentionDays: 30
    }
  }) { id ready settings { s3 { hasSecretAccessKey } } }
}
```

### Setting up an SFTP destination

1. Create it with host, port, user and base path (no credentials yet):
   `createDestination(input: {name: "Backup host", type: SFTP, sftp:
   {host: "backup.example.com", user: "bekup", basePath: "/upload"}})`.
   It is not ready yet.
2. `fetchDestinationHostKey(id)` connects to the stored host and port and
   shows the key the server presents (`type`, `fingerprintSha256`,
   `authorizedKey`, and the `host`/`port` it was fetched from). Compare the
   fingerprint with the one shown on the server
   (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`); never confirm a key
   you have not checked. Failures are deliberately coarse: "could not be
   reached or did not complete the SSH key exchange" (refused, filtered,
   timed out, not an SSH server), "not allowed" (SSRF guard) or
   "host key type not accepted".
3. `confirmDestinationHostKey(id, authorizedKey: "<authorizedKey from step
   2>", expectedHost: "<host from step 2>", expectedPort: <port>)` pins
   it; if the destination was changed in between, it fails with
   `CONFLICT`. Host-key lines are at most 8 KiB, one line, no options.
4. `generateDestinationSshKeypair(id)` creates a key and returns the
   `publicKey` line; add it to the account's `authorized_keys` on the
   server (or set a password or your own private key with
   `updateDestination`).
5. `testDestination(id)`.

Changing the host or port of an SFTP destination removes the pinned host
key: confirm the new server's key again.

A deleted destination disappears from the API, but backup, retention and
restore workers can still open it for the artifacts stored there.
