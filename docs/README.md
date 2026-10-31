<img src="assets/bekup-mark.svg" alt="" width="48" height="48" align="left">

# bekup documentation

User and operator documentation for bekup. Pages marked as stubs are filled
in by the task named on the page (see the
[plan](../plans/backup-databases-abstract-corbato.md)).

## Start here

- [Getting started](getting-started.md): quick start with Docker Compose,
  first admin (CLI), first job.
- [Architecture](architecture.md): components, trust boundaries, runner
  design, data flows.
- [Decision log](DECISIONS.md): every design decision (D-001 ...).

## Installation

- [Docker Compose and the setup wizard](installation/compose.md)
- [Reverse proxy and TLS](installation/reverse-proxy.md)
- [External PostgreSQL for bekup's own database](installation/external-database.md)
- [Rootless Docker and userns-remap](installation/rootless-docker.md)
- [Upgrades](installation/upgrades.md)

## Configuration

- [Configuration reference](configuration.md): `bekup.toml`, `runner.toml`,
  env overrides, `*_file` secrets, dev-mode guardrails.

## Security

- [Threat model and vulnerability reporting](../SECURITY.md) ([overview](security/README.md))
- [Hardening guide](security/hardening.md)
- [Key management and escrow](security/key-management.md)
- [Runner mTLS certificates](security/mtls-certs.md)
- [App DB TLS](security/app-db-tls.md)
- [Runner target allowlist](security/target-allowlist.md)
- [Backup and restore database roles](security/db-roles.md)
- [Immutable backups](security/immutable-backups.md)
- [Audit log verification](security/audit.md)

## Guides

- [Connections](guides/connections.md)
- [Destinations](guides/destinations.md)
- [Jobs and schedules](guides/jobs-and-schedules.md)
- [Retention](guides/retention.md)
- [Backup encryption](guides/encryption.md)
- [Downloads](guides/downloads.md)
- [Restore](guides/restore.md)
- [Notifications](guides/notifications.md)

## Operations and recovery

- [Operations](operations.md): metrics, alerts, troubleshooting.
- [Disaster recovery](recovery.md): restoring without the bekup database.

## Contributing

- [Development](development.md)
- [Reference](reference/README.md) (generated CLI and GraphQL reference)
- [AGENTS.md](../AGENTS.md): guide for contributors and AI agents.
