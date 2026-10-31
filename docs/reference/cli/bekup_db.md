## bekup db

Set up bekup's roles and database on an external PostgreSQL (bootstrap, roles-sql)

### Synopsis

Prepare an external PostgreSQL 15-17 for bekup (D-207; docs/installation/external-database.md).
`bootstrap` creates the two least-privilege roles and the database with an administrator
account; `roles-sql` prints the same statements for a DBA. Then run `bekup migrate up`.

```
bekup db [flags]
```

### Options

```
  -h, --help   help for db
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup db bootstrap](bekup_db_bootstrap.md)	 - Create bekup's roles and database with an administrator account (idempotent)
* [bekup db roles-sql](bekup_db_roles-sql.md)	 - Print the SQL that creates bekup_owner, bekup_app and the database (for a DBA, run with psql)

