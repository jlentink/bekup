# Security documentation

The **threat model** lives in [`SECURITY.md`](../../SECURITY.md) at the repo
root: vulnerability reporting, assets, adversaries, trust boundaries (B1-B8),
a STRIDE analysis per component, the mitigation-to-task map, residual risks
and open items. It is updated whenever a trust boundary changes.

Report vulnerabilities privately via
[GitHub security advisories](https://github.com/jlentink/bekup/security/advisories/new),
never in a public issue.

## Guides

- [Hardening guide](hardening.md)
- [Key management and escrow](key-management.md)
- [Runner mTLS certificates](mtls-certs.md)
- [Runner target allowlist](target-allowlist.md)
- [Backup and restore database roles](db-roles.md)
- [Immutable backups](immutable-backups.md)
- [Audit log verification](audit.md)
