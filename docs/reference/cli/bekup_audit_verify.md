## bekup audit verify

Recompute the audit hash chain and check the signed chain head (non-zero exit on tampering)

### Synopsis

Streams the audit log in id order, recomputes every hash, checks the prev_hash links and the
genesis rule, and checks the newest signed chain head (and --head-file, an exported head).
Reports the first broken link and exits non-zero when anything does not verify.

```
bekup audit verify [flags]
```

### Options

```
      --from-id int        start at this audit id (the link to the preceding row is still checked); default: the whole chain
      --head-file string   also check an exported signed head (from sign-head --export-file or an export destination)
  -h, --help               help for verify
      --json               print the report as JSON
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup audit](bekup_audit.md)	 - Verify the audit log hash chain and sign its head

