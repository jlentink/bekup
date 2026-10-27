## bekup admin reset-password

Set a new password for an account, clear its lockout and revoke all its sessions

```
bekup admin reset-password [flags]
```

### Options

```
      --email string           e-mail address of the account (required)
  -h, --help                   help for reset-password
      --password-file string   read the new password from this file (owner-only permissions; surrounding whitespace is trimmed) instead of prompting
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup admin](bekup_admin.md)	 - Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable

