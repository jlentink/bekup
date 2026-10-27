## bekup admin

Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable

### Synopsis

Manage accounts from the host when nobody can sign in (D-069). Every action is recorded in the
audit log with actor kind cli, the OS user and the hostname. Passwords are read from a terminal
prompt (twice) or from --password-file, never from a flag value. Details: docs/operations.md.

```
bekup admin [flags]
```

### Options

```
  -h, --help   help for admin
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup admin create](bekup_admin_create.md)	 - Create an account (by default an admin) with a password you enter
* [bekup admin disable](bekup_admin_disable.md)	 - Disable an account and revoke all its sessions
* [bekup admin reset-2fa](bekup_admin_reset-2fa.md)	 - Remove every second factor of an account (lost authenticator) and revoke all its sessions
* [bekup admin reset-password](bekup_admin_reset-password.md)	 - Set a new password for an account, clear its lockout and revoke all its sessions
* [bekup admin unlock](bekup_admin_unlock.md)	 - Clear the login lockout of an account

