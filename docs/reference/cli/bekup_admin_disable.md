## bekup admin disable

Disable an account and revoke all its sessions

### Synopsis

Disables the account (it can no longer sign in) and revokes all its sessions. The last active
admin cannot be disabled: create another admin first.

```
bekup admin disable [flags]
```

### Options

```
      --email string   e-mail address of the account (required)
  -h, --help           help for disable
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup admin](bekup_admin.md)	 - Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable

