## bekup verify

Verify a run's signed manifest and its artifacts offline (no database needed)

### Synopsis

Verifies the ed25519 signature of a run's manifest.json with the instance public key(s) given by
--pubkey, then streams every artifact the manifest lists and compares its size and SHA-256.
Works on a disk destination's run directory or on files downloaded from S3/SFTP. Prints the
engine, databases, sizes, encryption and the completeness statistics; exits non-zero on any
failure. Check that the job and run shown are the ones you expect. See docs/recovery.md.

```
bekup verify <manifest.json | run directory> [flags]
```

### Options

```
      --artifact stringArray              check this artifact file (matched to the manifest by file name; repeatable); only the given artifacts are checked
  -h, --help                              help for verify
      --json                              print the report as JSON
      --pubkey bekup keys export-public   instance public key: a base64 ed25519 key or a file from bekup keys export-public (repeatable; retired keys verify the manifests they signed)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

