## bekup audit

Verify the audit log hash chain and sign its head

### Synopsis

Verify the tamper-evident audit log and manage signed chain heads.
Details: docs/security/audit.md.

```
bekup audit [flags]
```

### Options

```
  -h, --help   help for audit
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup audit sign-head](bekup_audit_sign-head.md)	 - Sign the current audit chain head with the instance key and store it
* [bekup audit verify](bekup_audit_verify.md)	 - Recompute the audit hash chain and check the signed chain head (non-zero exit on tampering)

