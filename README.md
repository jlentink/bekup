# bekup

> **Status: under development.** bekup is not ready for production use yet.
> Interfaces, configuration and file formats may change without notice.

bekup is an open-source, self-hosted backup manager for **PostgreSQL**,
**MySQL** and **MariaDB**. It schedules and runs backups with each database's
**native dump tools, version-matched by running them in the official Docker
images**, writes them to **disk, S3 and SFTP**, applies retention, and
restores on demand. It is managed through a React web UI backed by a Go
GraphQL API.

## Security first

Security is the top priority, with no compromises. When convenience and
security conflict, security wins:

- Backups are **encrypted by default** (age) and accompanied by a **signed
  manifest**; restores are staged and verified before anything is written.
- Only a small, dedicated **runner sidecar** has Docker access. The web
  backend never talks to Docker or to your databases; dump containers run on
  isolated per-run networks and reach exactly one allowlisted target.
- TLS to your databases uses **pinned certificates**; SSH and SFTP use pinned
  host keys; the runner link uses mutual TLS.
- Credentials are stored with envelope encryption, are write-only in the API
  and are never logged. Secrets are only accepted as files, never inline.
- Passkeys and TOTP second factors, step-up authentication for sensitive
  actions, and a **fresh second-factor check for every restore**.
- A tamper-evident, hash-chained audit log.
- Weakening any of this requires an explicit admin action that is audited,
  alerted on and permanently flagged in the UI.

See the [architecture](docs/architecture.md) and the
[decision log](docs/DECISIONS.md) for details. A threat model and the
vulnerability reporting process will be published in `SECURITY.md`.

## Features (planned for the first release)

- PostgreSQL, MySQL and MariaDB, using the official images per server version.
- Dump all databases (one file per database, plus Postgres globals) or a
  selection.
- Reusable destinations (disk, S3 with Object Lock, SFTP) with fan-out.
- Interval or cron schedules with time zones, missed-backup alerts.
- Retention by count and/or age that never deletes the newest good backup.
- Role-based access (Admin, Maintainer, backup owner), downloads and restores.
- Notifications via in-app inbox, email and signed webhooks; Prometheus
  metrics.
- Offline recovery tools (`bekup verify`, `bekup decrypt`) that work without
  the bekup database.

## Documentation

- [Documentation index](docs/README.md)
- [Getting started](docs/getting-started.md)
- [Configuration](docs/configuration.md)
- [Security guides](docs/README.md#security)
- [Development](docs/development.md) and [AGENTS.md](AGENTS.md)

## License

[MIT](LICENSE) © 2026 Jason Lentink
