## bekup migrate

Apply database migrations (as bekup_owner); default action: up

### Synopsis

Apply the embedded database migrations as bekup_owner, using database.owner_url_file.
An advisory lock ensures only one instance migrates at a time; others wait.

```
bekup migrate [flags]
```

### Options

```
  -h, --help   help for migrate
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup migrate down-one](bekup_migrate_down-one.md)	 - Roll back the most recent migration (dev mode only)
* [bekup migrate status](bekup_migrate_status.md)	 - Show applied and pending migrations
* [bekup migrate up](bekup_migrate_up.md)	 - Apply all pending migrations (default)

