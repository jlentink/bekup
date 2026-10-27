## bekup audit sign-head

Sign the current audit chain head with the instance key and store it

### Synopsis

Signs the newest audit row's (id, hash) with the instance ed25519 key (created on first use),
stores it in audit_chain_heads and, with --export-file, writes it as JSON. Keep exported heads
off the database host: they make a rewrite of the whole database detectable.

```
bekup audit sign-head [flags]
```

### Options

```
      --export-file string   also write the signed head as JSON to this file
  -h, --help                 help for sign-head
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup audit](bekup_audit.md)	 - Verify the audit log hash chain and sign its head

