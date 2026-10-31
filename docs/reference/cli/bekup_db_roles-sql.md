## bekup db roles-sql

Print the SQL that creates bekup_owner, bekup_app and the database (for a DBA, run with psql)

### Synopsis

Prints a psql script that creates the roles bekup_owner and bekup_app and bekup's database on a
PostgreSQL 15+ server (D-207). With --passwords verifier (default) the roles get SCRAM-SHA-256
verifiers of the passwords in --owner-password-file and --app-password-file: the passwords
themselves are never printed, but the verifiers are sensitive (keep the output private). With
--passwords prompt the script asks for them with psql's \password instead. Output: stdout only.

```
bekup db roles-sql [flags]
```

### Options

```
      --app-password-file string     file with the bekup_app password (database.password_file)
      --create-extensions            also create the citext extension as the admin (servers where it is not trusted)
      --database string              name of bekup's database (database.name) (default "bekup")
  -h, --help                         help for roles-sql
      --owner-password-file string   file with the bekup_owner password (database.owner_password_file)
      --passwords string             verifier: embed SCRAM verifiers of the password files; prompt: psql asks for the passwords (default "verifier")
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup db](bekup_db.md)	 - Set up bekup's roles and database on an external PostgreSQL (bootstrap, roles-sql)

