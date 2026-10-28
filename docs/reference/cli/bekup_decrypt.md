## bekup decrypt

Decrypt and decompress a backup artifact offline into a ready-to-use .dump/.sql

### Synopsis

Decrypts an artifact (age) and decompresses it (zstd) into the raw dump that stock pg_restore,
psql, mysql or mariadb restore. With --manifest and --pubkey the manifest signature and the
artifact's size and SHA-256 are verified first and the decrypted dump must match the signed raw
SHA-256; the output only appears under --out after every check passed. Without a manifest you
must pass --no-verify explicitly (the content is then unauthenticated).

Keys: --passphrase-file takes a file with the passphrase or a passphrase export from bekup (the
version is picked from the manifest or --passphrase-version); --identity-file takes an age
identity file (X25519 only); --stdin-passphrase prompts on a terminal or reads one line from
stdin. Passphrases are never accepted as flag values. See docs/recovery.md.

```
bekup decrypt <artifact> --out <file|-> (--passphrase-file <f> | --identity-file <f> | --stdin-passphrase) (--manifest <f> --pubkey <k> | --no-verify) [flags]
```

### Options

```
      --force                             replace an existing --out file
  -h, --help                              help for decrypt
      --identity-file string              age identity file (AGE-SECRET-KEY-1..., X25519 only; owner-only permissions)
      --manifest string                   the run's manifest.json: verify the artifact before decrypting
      --max-work-factor int               highest scrypt work factor accepted from the artifact header (18..20; bekup writes 18) (default 18)
      --no-verify                         decrypt without verifying a signed manifest (DANGEROUS: unauthenticated content)
      --out string                        output file (mode 0600, never overwritten without --force) or - for stdout (required)
      --passphrase-file string            file holding the job passphrase or a bekup passphrase export (owner-only permissions)
      --passphrase-version int            passphrase version to use from a passphrase export (default: the manifest's)
      --pubkey bekup keys export-public   instance public key: a base64 ed25519 key or a file from bekup keys export-public (repeatable; retired keys verify the manifests they signed)
      --stdin-passphrase                  read the passphrase from the terminal (no echo) or the first line of stdin
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

