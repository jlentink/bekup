# Downloads

> **Status:** the download API (task 3.9: `createDownloadTicket` and
> `GET /api/download/{ticket}`) exists. The download dialog with the
> restore command follows in task 6.6. Decision: D-150.

Who can download backups, the `RAW` and `PLAIN` modes, the single-use
download tickets and how to restore a downloaded file.

## Who may download

| Role | Downloads |
|---|---|
| Admin | Every artifact. |
| Backup owner (role `user`, listed in the job's owners) | Artifacts of their own jobs, also after the job was deleted. |
| Maintainer | **Never**, not even when listed as an owner of the job (D-012, D-067). |
| Other users | Never. |

Every download needs a fresh **step-up** (re-confirming your identity with
your passkey or authenticator code, or your password if your account has no
second factor) within `security.stepup_max_age` (default 5 minutes). Ticket
creation, every rejected ticket and every download start, completion or
failure are recorded in the audit log.

Only artifacts that are **present**, passed their **verification** (the
native `pg_restore` check or the dump tool's completion trailer) and are
listed in the run's **signed manifest** on their destination can be
downloaded. There is no override, also not for admins: a dump that failed
verification is not handed out as a "backup".

## Modes

| Mode | File | Use it for |
|---|---|---|
| `RAW` | The artifact exactly as stored: `<job>_<database>_<timestamp>.dump.zst.age` (PostgreSQL) or `.sql.zst.age` (MySQL/MariaDB, PostgreSQL globals); without `.age` for jobs whose encryption an admin turned off. | Keeping an encrypted offline copy, or decrypting yourself with `age` and `zstd` (or `bekup decrypt`, see [recovery](../recovery.md)). |
| `PLAIN` | The decrypted and decompressed dump: `<job>_<database>_<timestamp>.dump` (a PostgreSQL custom archive for `pg_restore`) or `.sql` (a plain SQL script for `mysql`, `mariadb` or `psql`). | Restoring elsewhere right away. |

A `PLAIN` file is your **unencrypted database** on your device; protecting
it from then on is your responsibility. Prefer `RAW` when you only need a
copy.

### Plain downloads of recipient-encrypted backups

If the job encrypts to an **age recipient** (a public key), bekup cannot
decrypt the backup on its own: it never stores the private key. For a
`PLAIN` download, paste the matching identity (`AGE-SECRET-KEY-1...`, or
the content of the identity file) into the download request
(`ageIdentity`). bekup checks that it matches the recipient the backup was
encrypted to, keeps it only **sealed** with the master key inside the
ticket, and deletes it when the download starts (an unused ticket's row is
deleted at most about two minutes after it expired). The identity is never
logged, audited or returned. `RAW` downloads and passphrase-encrypted
backups do not take an identity (the request is refused if you send one);
passphrase backups are decrypted with the stored passphrase version the
artifact was written with.

## Download tickets

A download is two steps, so that the file itself is fetched with a plain
browser navigation (no secrets in JavaScript memory, no CORS):

1. `createDownloadTicket(artifactId, mode, ageIdentity)` (GraphQL mutation,
   step-up) returns:

   | Field | Meaning |
   |---|---|
   | `url` | `/api/download/<ticket>`: a relative URL with a 256-bit single-use ticket. Never log or share it. |
   | `expiresAt` | 60 seconds after creation. |
   | `fileName` | The sanitized name the file is saved under. |
   | `format` | `dump` or `sql`. |
   | `restoreCommand` | A command template for the file (placeholders in angle brackets). |

2. Open `url` in the **same browser session** (a same-origin navigation or
   a link the user clicks) within 60 seconds.

Ticket rules:

- **Single use.** The ticket is consumed when the download starts; a
  second request answers `410 Gone`.
- **Bound to your session.** A request from another session, another user
  or without a session answers `404` **and burns the ticket**, so a leaked
  URL is useless and cannot be retried. Signing in again or a session
  rotation in between also invalidates it; create a new ticket.
- **Expiry.** After 60 seconds: `410 Gone`.
- **Access is checked again** when the ticket is used: a user who lost the
  ownership (or role) since creating it gets `404`.
- Requests from other sites (`Sec-Fetch-Site: cross-site` / `same-site`)
  are refused without consuming the ticket.
- At most **5 unused tickets** per user (`RATE_LIMITED`); at most
  `workers.max_concurrent_downloads` (default 4) downloads run at once per
  server process and `workers.max_downloads_per_user` (default 2) per user
  (`429` with `Retry-After`, the ticket stays valid).
- While a download streams, the artifact is **held**: retention never
  deletes it, and skips it until a later sweep. The hold is released when
  the download ends (and expires on its own a few minutes after a crash).
- The server stores only the SHA-256 of a ticket, and the access log shows
  `/api/download/[redacted]`.

Responses carry `Content-Type: application/octet-stream`,
`Content-Disposition: attachment` with the sanitized file name,
`Cache-Control: no-store`, `X-Content-Type-Options: nosniff` and the usual
security headers (strict CSP, `Referrer-Policy: no-referrer`). `RAW`
responses have a `Content-Length`; `PLAIN` responses are streamed.

## Integrity

Before the first byte is sent, bekup reads the run's `manifest.json` from
the same destination and verifies its signature and that it matches the
job, run and artifacts recorded in the database. While streaming, it hashes
the stored bytes (and for `PLAIN` also the dump) and compares them with the
recorded SHA-256 values; age additionally authenticates every 64 KiB chunk
and zstd checks every frame. The **last 64 KiB are held back** until every
check passed. If anything does not match, the connection is aborted: your
browser reports a failed or incomplete download (never a complete-looking
file), and `download.failed` with the reason `integrity_mismatch` or
`manifest_invalid` is audited. Treat such a failure as possible tampering
or storage corruption: check the destination and use another copy.

## Restoring a downloaded file

`restoreCommand` shows the right command; the placeholders are yours to
fill in (bekup never puts database names into shell commands).

PostgreSQL custom archive (`.dump`):

```sh
createdb mydb_restored
pg_restore --exit-on-error --no-owner --dbname mydb_restored nightly_app_20261027T030000Z.dump
```

PostgreSQL globals (`.sql`), as a superuser and reviewed first, since it
creates roles:

```sh
psql --set ON_ERROR_STOP=1 --dbname postgres --file nightly_globals_20261027T030000Z.sql
```

MySQL / MariaDB (`.sql`):

```sh
mysql mydb_restored < shop_shop_20261027T030000Z.sql
```

A `RAW` file first needs decrypting and decompressing:

```sh
age --decrypt nightly_app_20261027T030000Z.dump.zst.age | zstd --decompress --stdout > app.dump        # passphrase
age --decrypt --identity key.txt nightly_app_20261027T030000Z.dump.zst.age | zstd -d -c > app.dump     # recipient
```

Use client tools of the same or a newer major version than the server the
backup came from. Restores **into bekup-managed databases** go through the
restore wizard instead (task 4.x), which stages and verifies the artifact
and applies the hardened client flags.

## API reference

```graphql
createDownloadTicket(artifactId: UUID!, mode: DownloadMode!, ageIdentity: String): DownloadTicket! @stepUp
```

The field must be the only root field of its request (like the other
credential-carrying mutations, D-117). Errors: `STEP_UP_REQUIRED`,
`FORBIDDEN` (maintainers, non-owners, unknown ids for non-admins),
`NOT_FOUND` (unknown id, admins), `CONFLICT` (artifact deleted, missing,
unverified, not signed, or its key version unavailable), `VALIDATION`
(identity missing, malformed, not matching, or not accepted for this mode),
`RATE_LIMITED`.

`GET /api/download/{ticket}` answers `200` with the file, `404` (unknown,
foreign session, no access), `410` (used or expired), `429` (too many
concurrent downloads), or `502` (destination unreachable, manifest or
integrity check failed before streaming).
