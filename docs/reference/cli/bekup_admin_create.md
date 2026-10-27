## bekup admin create

Create an account (by default an admin) with a password you enter

### Synopsis

Creates an account with the given e-mail address, display name and role (default admin). The
password policy applies. This is the only way to create the first admin (D-113): run it once
after installing, then sign in to the web UI.

```
bekup admin create [flags]
```

### Options

```
      --display-name string    display name of the account (required)
      --email string           e-mail address (sign-in name) of the account (required)
  -h, --help                   help for create
      --password-file string   read the new password from this file (owner-only permissions; surrounding whitespace is trimmed) instead of prompting
      --role string            role: admin, maintainer or user (default "admin")
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup admin](bekup_admin.md)	 - Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable

