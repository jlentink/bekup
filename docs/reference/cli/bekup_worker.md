## bekup worker

Run background workers only

### Synopsis

Run only the River background workers (backups, restores, retention, notifications,
housekeeping) as bekup_app, without HTTP and without the scheduler, to scale work out next to
`bekup serve --no-worker`. Same startup checks as serve (database role, schema version,
master keyring). SIGINT/SIGTERM stop fetching jobs, let running jobs finish within
workers.shutdown_timeout, then cancel them.

```
bekup worker [flags]
```

### Options

```
  -h, --help   help for worker
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

