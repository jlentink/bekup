## bekup keys export-public

Export the instance public keys (manifest signatures) for escrow and offline verification

### Synopsis

Writes every instance signing public key (active and retired) from the bekup database in the
format `bekup verify --pubkey` reads, with their fingerprints. Only public keys are read; no
keyring is needed. Escrow the file with the master keyring and record the fingerprints
independently (docs/recovery.md, docs/security/key-management.md).

```
bekup keys export-public [--out <file>] [flags]
```

### Options

```
      --force        replace an existing --out file
  -h, --help         help for export-public
      --out string   write to this file instead of stdout (never overwritten without --force)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup keys](bekup_keys.md)	 - Generate and rotate the master keyring; export the instance public keys

