# Destinations

Admins manage destinations on the **Destinations** page of the web UI
(D-182) or through the GraphQL API described in
[Managing destinations](#managing-destinations). The list shows each
destination's type, Object Lock / no-delete mode and the stored result of
the last test (passed or failed, its code, when, and a *Stale* marker after
a configuration change; D-187, D-195); maintainers see it read only and may
run tests.
The form adapts to the type (S3: endpoint, region, bucket, prefix, path
style, encryption, checksums, Object Lock, custom CA; SFTP: server, user,
base path and sign-in; WebDAV: URL, flavor, user, password, folder and
optional CA bundle; disk: the directory). Secret keys and passwords are
write-only; changing an SFTP destination's server, user or base path, or a
WebDAV destination's URL, flavor or username, asks for its stored password
again (D-140). SFTP host keys are fetched, compared and confirmed
explicitly, and **Generate keypair** shows the public key to install;
WebDAV certificates are pinned from a test result the same way.

A destination is a reusable place where bekup writes backup artifacts:
local disk (confined to the disk root), S3 and S3-compatible storage
(Object Lock, no-delete mode, custom CA), SFTP with pinned host keys and
WebDAV (Nextcloud, ownCloud and TransIP STACK, NAS boxes, Apache/nginx/
rclone/Caddy, hosted WebDAV such as Hetzner Storage Box).

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

## One location per destination

Every destination owns its location: bekup's retention, empty-folder
cleanup and unrecorded-file scan treat every backup file there as the
destination's own. Two destinations therefore may not share a location,
and neither may lie inside the other (D-209). Creating or moving a
destination onto another one's location fails with `VALIDATION` on the
location field, naming the other destination; a deleted destination keeps
its location while it still holds backups. "The same location" means:

| Type | Compared |
|---|---|
| Local disk | the directory below `storage.disk_root` (`nightly` and `nightly/db` overlap, `nightly` and `nightly2` do not) |
| S3 | the endpoint (empty and every `*.amazonaws.com` endpoint are AWS) and the bucket, then the key prefix (an empty prefix contains every other one) |
| SFTP | the host and port (plus the user when the base path is relative to the user's home), then base path + subdirectory |
| WebDAV | the host and port, then the URL path + folder, compared case-insensitively; Nextcloud/ownCloud destinations (and generic URLs into `remote.php/webdav` or `remote.php/dav/files/<user>`) per user and server root. The user is the login name and, for the `NEXTCLOUD` flavor, the user id the server reports for it when the destination is saved (an e-mail or LDAP login and the user name are the same user) |

Saving a `NEXTCLOUD` destination asks the server once for the account's
user id (the files live below it, and it may differ from the login name).
When the server cannot be asked while saving (offline, a certificate that
still needs a pin, a wrong password), the check falls back to the login
name, so two different logins of one user (for example `alice@example.com`
and `alice`) are not recognized as the same location until the
destination is saved again while the server answers. Use one login per
Nextcloud user for bekup, and save the destination again after pinning
its certificate. Generic URLs into `remote.php/webdav` are compared by the
login name only.

To take over backups that already exist at a location (for example
written by another bekup installation), import them with
[`bekup reindex --import`](../reference/cli/bekup_reindex.md) instead of creating
a second destination on top of them.

## Changing the location

The location of a destination (the disk directory; the S3 endpoint,
region, bucket and prefix; the SFTP host, port, user, base path and
directory) can only change while **no backup files are recorded as present
there** and **no run of a job using the destination is queued or
running**. Otherwise the update fails with `VALIDATION` on that field
(D-209): bekup addresses every backup by its key below the location, so
after a move retention, downloads and restores would look in the wrong
place and the old files would stay behind forever. To move backups
elsewhere, create a new destination and point the jobs at it; the old one
empties as retention removes its backups (or delete the old destination
once its backups are no longer needed). Moving the files on the storage
yourself and keeping the location unchanged (for example a new disk
mounted at the same path) is fine. Other settings (name, credentials,
no-delete, storage class, ...) can change at any time. After a location
change the destination's unrecorded-file list starts empty.

## Unrecorded files

Once a day bekup lists the job folders on every destination and records
files in its layout that have no backup record (for example the leftovers of a backup that
was interrupted after writing a file). Admins see them on the destination
page under **Unrecorded files** and can delete them one by one (step-up,
audited, re-checked; never automatic and never in no-delete mode). Empty
backup folders are removed at the same time. Details:
[Retention](retention.md#unrecorded-files-and-empty-folders).

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
missed runs ([missed-backup alerts](jobs-and-schedules.md#missed-backup-alerts)
and the [metrics](../operations.md#metrics)).

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

After closing the partial file bekup checks with a stat of its path that
the server holds every byte it wrote. Cloud storage with SFTP access, such
as **TransIP STACK** (ownCloud behind an SFTP gateway), is supported: such
gateways report a wrong size while a file is still open and may store an
upload a moment after it was closed, so bekup re-checks a short or not yet
visible file for about 3 seconds. If the server keeps reporting another
size the upload fails and the run log says what the server reported, for
example `destination … dropped (error: the data did not match the expected
checksum: the server reported 0 B of 41.2 MiB after closing)`. Reading a
backup back (restores, downloads, verification) works on such gateways
too: when the server misreports an open file, bekup accepts it only if
its directory entry is unchanged and stops with an error if the server
sends fewer or more bytes than that entry says. These checks are only
plausibility checks: integrity rests on age encryption and the signed
manifest, and test restores read the backups back from the destination
and verify them.

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

## WebDAV

A WebDAV destination stores backups on a WebDAV server through bekup's own
small client (no WebDAV library). It connects through the network
allowlist like S3 and SFTP, speaks **https only** (plain http only in dev
mode) and signs in with **HTTP Basic authentication over HTTPS** (a
username and a password or app password). bekup never follows a redirect
(a redirect fails with "the server answered with a redirect": check the
URL, https and the exact folder address) and never sends the password to
another host.

Two flavors:

| Flavor | URL | Uploads |
|---|---|---|
| `GENERIC` (default) | the existing folder that holds the backups, e.g. `https://u123456.your-storagebox.de/backups` or `https://nas.local:5006/backups` | one streaming `PUT` |
| `NEXTCLOUD` | the server address, e.g. `https://cloud.example.com` (or with its path, `https://example.com/nextcloud`), `https://name.stackstorage.com` | the Nextcloud/ownCloud chunked-upload API: 64 MiB chunks, so PHP and proxy upload limits never see a multi-GB dump |

The optional **folder** (`path`) lies below the URL (generic) or below the
user's files (Nextcloud) and is created if missing; with the generic
flavor the URL's folder itself must exist. The test suggests the Nextcloud
flavor when the server answers like Nextcloud or ownCloud.

### How bekup writes

Every upload goes to a hidden temporary file `.<random>.partial` next to
its final name, is hashed while it streams, its stored size is checked,
and only then is it renamed to the final name with `MOVE` and
**`Overwrite: F`**, which tells the server to refuse when the name exists.
WebDAV has no "create only if new" that every server supports, so
"never overwrite" depends on the server honoring that header. bekup
proves it before the first upload of every run and in every test (the
**overwrite probe**, step `overwrite`): it writes two hidden files and
moves one onto the other with `Overwrite: F`; the server must refuse and
leave the other file unchanged. A server (or a proxy in front of it) that
replaces the file is **never written to**: uploads fail with
`overwrite_ignored`, and after such a test the destination is not ready
until a new test passes. Fix the server (for example a reverse proxy that
drops the `Overwrite` header) and test again.

With the Nextcloud flavor bekup creates an upload folder
`bekup-<random>` below `remote.php/dav/uploads/<user>/`, uploads the
chunks and moves them into place with `Overwrite: F` and the total size
(`OC-Total-Length`), which the server checks. One upload holds one chunk
(64 MiB) in memory. If the server has no chunked-upload endpoint, bekup
falls back to the single `PUT`; the test shows the mode it used.

The daily maintenance removes leftover partials and bekup's own upload
folders older than 24 hours (never other clients' uploads) and removes
empty run folders. It never deletes job folders: a WebDAV delete of a
folder is recursive, and a new backup run may be creating its folder there
at that moment. Deletes of backups touch files only. WebDAV has no
immutability; no-delete mode works as everywhere (give the account no
delete rights on the server too where possible).

### TLS: system roots, CA bundle or a pinned certificate

By default the server certificate is checked against the system roots. A
**CA bundle** (PEM) replaces them for a private CA. For a self-signed
certificate (typical for a Synology or QNAP box on the LAN) **pin the
certificate**:

1. Create the destination and run **Test**. The `connect` step fails with
   "the server's TLS certificate is not trusted"; the result shows the
   certificate's SPKI SHA-256 (the hash of its public key), its SHA-256 and
   its validity.
2. Compare the fingerprint out of band, for example on the server:
   `openssl x509 -in cert.pem -noout -pubkey | openssl pkey -pubin -outform der | openssl dgst -sha256`
   (SPKI) or `openssl x509 -in cert.pem -noout -fingerprint -sha256`
   (certificate), or in the NAS's certificate settings.
3. Choose **Pin this certificate** (admin, step-up). bekup connects again,
   and pins the certificate only if it still has that key.

From then on only this certificate is accepted (not the system roots, not
the CA bundle); the host name and validity are still checked, so the
certificate must name the host or IP address of the URL. NAS default
certificates are often issued for another name: create one for the
address you use (DSM: *Control Panel → Security → Certificate → Add →
Create a self-signed certificate*, with the host name or IP as subject
alternative name; QTS: *Control Panel → Security → SSL Certificate &
Private Key*). When the server's certificate is renewed, the test reports
`tls_pin_mismatch` (a security event: audited as
`destination.tls_pin_mismatch` and alerted to every admin); check the new
certificate and pin it again. Changing the URL removes the pin.

### Servers

- **Nextcloud and ownCloud.** Use the `NEXTCLOUD` flavor with the server
  address and create an **app password** for bekup (Nextcloud: *Settings →
  Security → Devices & sessions*; ownCloud: *Settings → Security → App
  passwords*); with two-factor authentication it is required. bekup finds
  the account's user id itself (it may differ from the login name).
- **Folder listings.** bekup matches the server's listing with the folder
  it asked for, also when the server spells the folder in another letter
  case or Unicode form (a case-insensitive server, names stored
  decomposed). A listing that names neither the folder nor anything in it
  fails with "the server's listing names the folder differently than
  requested" instead of looking empty: check the spelling of the URL and
  folder.
- **TransIP STACK** is built on ownCloud: flavor `NEXTCLOUD`, URL
  `https://<name>.stackstorage.com`, your STACK user and a STACK app
  password if two-factor authentication is on. The test shows whether the
  chunked uploads work (`uploadMode: CHUNKED`); otherwise bekup uses the
  single `PUT`. The generic flavor with
  `https://<name>.stackstorage.com/remote.php/webdav` works too, but then
  every upload is one request.
- **Synology / QNAP.** Enable the WebDAV server (DSM: *WebDAV Server*
  package, HTTPS port 5006), create a shared folder and a dedicated user,
  then use `GENERIC` with `https://<nas>:5006/<shared folder>` and pin the
  certificate (above).
- **Hetzner Storage Box.** Enable WebDAV for the box (or a sub-account),
  then `GENERIC` with `https://<user>.your-storagebox.de/<folder>` (the
  folder must exist); the certificate is publicly trusted.
- **Apache mod_dav, nginx (dav + dav_ext), rclone, Caddy (webdav plugin).**
  `GENERIC` with the folder URL. The overwrite probe tells you whether the
  server honours `Overwrite: F`.

### Network access

Like S3 and SFTP, the server's host must resolve to an address the
network allowlist permits; a NAS on the LAN needs an allowlist entry
(*Administration → Network*).

## Storage and free space

The destinations list shows a **Stored** column: the size of the backup
files bekup tracks as present on each destination and the number of
backups (runs) they belong to (one aggregate, `destinationUsage`; admins
and maintainers). The destination page has a **Storage** section with the
same numbers and, where the server reports it, the space behind the
destination: "x used by bekup · y free of z" with a bar (bekup's backups,
other files, free).

| Type | What is reported |
|---|---|
| Local disk | total, free and used of the file system holding the destination directory (statfs; Linux, macOS, BSD) |
| SFTP | total, free and used of the server's file system, if the server offers the `statvfs@openssh.com` extension (OpenSSH does); otherwise "not reported" |
| WebDAV | the RFC 4331 quota (`quota-available-bytes`, `quota-used-bytes`) of the destination folder: Nextcloud and ownCloud report the account quota; total = used + available. Plain servers without the properties, and an unlimited Nextcloud quota (no free value), show what is known or "not reported" |
| S3 | never: a bucket has no fixed size |

"Free" is the space available to the account bekup uses (for SFTP and
disk the blocks available to unprivileged users); "used" counts every file
there, not only bekup's. The space is asked only when the destination page
is opened (`destinationSpace`, admins and maintainers), through the same
guarded, pinned connection as a test, with a 15 second timeout and the
test rate limit; the answer is cached for 5 minutes per destination (1
minute after a failure; changing the destination's settings asks again).
Failures show a fixed reason such as "could not be reached", never the
server's text.

The page warns that **free space is low** when less than a tenth of the
total is free, or when the free space is smaller than the newest full set
of backups on the destination (per job, the files of its newest backup
there): the next backups may then not fit (D-225).

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

The result lists the steps `connect`, `write`, `read` and `delete` (WebDAV:
`connect`, `overwrite`, `write`, `read`, `delete`), each with a normalized
code (`ok`, `permission_denied`, `host_key_mismatch`, `tls_pin_mismatch`,
`overwrite_ignored`, `network`, `timeout`, ...) and a fixed message, in some
cases followed by a short detail bekup composes itself (an HTTP status
number, a size, never server text). Admins and maintainers may test; tests are
limited to 10 per minute per user (burst 5), a few at a time, and one per
request.

The outcome of the newest test is stored on the destination as
`lastTest { at ok code stale }` (D-187; admins and maintainers): `code` is
the normalized class of the first failed step (null when ok). `stale`
turns true once the destination's settings change; a test that races such
a change stores nothing.

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
| `createDestination(input)` | admin, step-up | Create; exactly the `disk`, `s3`, `sftp` or `webdav` input matching `type` |
| `updateDestination(id, input)` | admin, step-up | A settings input replaces the whole non-secret configuration of its type |
| `deleteDestination(id)` | admin, step-up | Soft delete (the name can be reused) |
| `testDestination(id)` | admin, maintainer | Probe write/read/delete (see above) |
| `fetchDestinationHostKey(id)` | admin | Show the host key the SFTP destination's stored host presents (trust on first use; nothing is stored) |
| `confirmDestinationHostKey(id, authorizedKey, expectedHost, expectedPort)` | admin, step-up | Pin the host key of an SFTP destination (fails with `CONFLICT` if host/port changed since the fetch) |
| `generateDestinationSshKeypair(id)` | admin, step-up | New ed25519 key for an SFTP destination; returns the public key only |
| `pinWebdavCertificate(id, fingerprint)` | admin, step-up | Pin the certificate a WebDAV server presents, if its SPKI SHA-256 is `fingerprint` (captured again; `CONFLICT` if it changed); only field of its request |
| `clearWebdavCertificatePin(id)` | admin, step-up | Remove a WebDAV certificate pin (audited and alerted) |
| `destinationUnrecordedFiles(destinationId, first, after)` | admin | The unrecorded files found by the daily scan (paged by key), with totals and the last scan time (D-209) |
| `deleteUnrecordedFile(destinationId, id)` | admin, step-up | Delete one unrecorded file (re-checked, audited; refused in no-delete mode); only field of its request |

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

### Setting up a WebDAV destination

```graphql
mutation {
  createDestination(input: {
    name: "STACK"
    type: WEBDAV
    webdav: {
      url: "https://name.stackstorage.com"
      flavor: NEXTCLOUD
      username: "name"
      password: "<app password>"
      path: "bekup"
    }
  }) { id ready }
}
```

Then `testDestination(id)`: the result's `webdav` field holds the
certificate (`spkiSha256`, `fingerprintSha256`, validity), `trusted`, the
recognized `server` and `suggestedFlavor`, the `uploadMode` and the
`overwriteProtection` (`REFUSED` is required). For a self-signed
certificate, compare `spkiSha256` out of band and call
`pinWebdavCertificate(id, fingerprint: "<spkiSha256>")`, then test again.

A deleted destination disappears from the API, but backup, retention and
restore workers can still open it for the artifacts stored there.
