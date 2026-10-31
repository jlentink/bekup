## bekup db bootstrap

Create bekup's roles and database with an administrator account (idempotent)

### Synopsis

Connects to the external PostgreSQL of bekup.toml (fields mode: database.host, ...) with an
administrator account and creates the login roles bekup_owner and bekup_app with SCRAM verifiers
of database.owner_password_file and database.password_file, the database owned by bekup_owner
(PUBLIC revoked, CONNECT for bekup_app) and, if bekup_owner cannot, the citext extension (D-207).
The admin password comes from --admin-password-file or a terminal prompt; it is used for this
connection only, never stored or logged. Works with CREATEROLE + CREATEDB accounts and managed
admin roles (rds_superuser, cloudsqlsuperuser, azure_pg_admin). Existing roles are checked, not
changed (--reset-passwords sets their passwords); existing roles are only accepted with
--adopt-existing-roles, and other members or ADMIN OPTION holders of them are refused. Then it
runs the role checks and the migrate preflight. With database.single_account = true it uses the
single account itself (weakened). The admin connection needs tls_mode verify-full unless
--allow-insecure-admin-connection is given. Like migrate up it refuses to run on another
version than BEKUP_SETUP_VERSION (D-212).

```
bekup db bootstrap [flags]
```

### Options

```
      --admin-database string             maintenance database the administrator connects to (default "postgres")
      --admin-host string                 host of the administrator connection (default: database.host)
      --admin-password-file string        file with the administrator password (default: prompt on the terminal)
      --admin-port int                    port of the administrator connection (default: database.port)
      --admin-user string                 administrator role to connect as (two roles: required)
      --adopt-existing-roles              accept bekup_owner/bekup_app roles that already exist (an earlier bootstrap, a DBA); they are checked, never changed silently
      --allow-insecure-admin-connection   allow the admin connection with tls_mode require or disable (the admin password then crosses an unverified or unencrypted connection)
      --dry-run                           only show what would be done
  -h, --help                              help for bootstrap
      --reset-passwords                   set the passwords of existing bekup_owner/bekup_app roles to the configured ones (needs --adopt-existing-roles)
      --tls-ca-file string                override database.tls_ca_file
      --tls-mode string                   override database.tls_mode for this command (and the checks after it)
      --tls-server-name string            override database.tls_server_name
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup db](bekup_db.md)	 - Set up bekup's roles and database on an external PostgreSQL (bootstrap, roles-sql)

