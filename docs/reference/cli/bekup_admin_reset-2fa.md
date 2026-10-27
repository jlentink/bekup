## bekup admin reset-2fa

Remove every second factor of an account (lost authenticator) and revoke all its sessions

### Synopsis

Lost-factor recovery (D-112): removes the account's TOTP authenticator, passkeys and recovery
codes, clears its second-factor lockout and revokes all its sessions. The account then signs in
with its password alone; when the 2FA policy requires a factor for its role, a new enrollment
grace period starts now. Only use it after verifying the person's identity out of band.

```
bekup admin reset-2fa [flags]
```

### Options

```
      --email string   e-mail address of the account (required)
  -h, --help           help for reset-2fa
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup admin](bekup_admin.md)	 - Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable

