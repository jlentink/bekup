## bekup reindex

List and verify the runs on a destination; with --import, record them for a job after a database loss

### Synopsis

Lists a destination (disk, S3 or SFTP, described by an offline TOML file with *_file credentials,
see docs/recovery.md), groups its objects into runs, verifies each run's signed manifest (location,
signature) and streams every artifact to check its size and SHA-256. The destination is opened
read-only (no-delete mode) and never written.

Without --import it needs no database and prints an inventory (verified with --pubkey).
With --import (needs the bekup config and database) the runs of --job-slug are verified against
the database's instance keys and recorded as runs and artifacts of that job on --destination-id,
so the restore UI can use them. Only runs whose signature verifies are imported; artifacts are
marked verified only when they match the signed manifest. Runs already in the database are
skipped. Every import is recorded in the audit log (cli.run.imported). --dry-run shows what
would be imported. Retention applies to imported runs like to any other run of the job.

```
bekup reindex --destination-config <toml> [--job-slug <slug>] (--pubkey <k> | --import --destination-id <id>) [flags]
```

### Options

```
      --destination-config string         offline destination description (TOML, see docs/recovery.md) (required)
      --destination-id string             with --import: the bekup destination (UUID) these objects live on
      --dry-run                           with --import: verify and show what would be imported, write nothing
  -h, --help                              help for reindex
      --import                            import the verified runs into the bekup database (needs the bekup config)
      --job-slug string                   only the runs of this job (required with --import)
      --json                              print the inventory as JSON
      --pubkey bekup keys export-public   instance public key: a base64 ed25519 key or a file from bekup keys export-public (repeatable; retired keys verify the manifests they signed)
      --skip-artifacts                    verify only the manifests, do not read the artifacts (not with --import)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

