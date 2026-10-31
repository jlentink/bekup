## bekup serve

Run the web UI, GraphQL API, scheduler and background workers

### Synopsis

Run the HTTP server (embedded web UI, GraphQL API, health endpoints), the scheduler loop
(scheduler.enabled) and the River background workers as bekup_app. --no-worker runs serve
without workers (scale them out with `bekup worker`). Refuses to start when the database role
is privileged, the schema version differs from this binary (run `bekup migrate` first), the
master keyring does not match or BEKUP_SETUP_VERSION names another version (D-212).
SIGINT/SIGTERM drain in-flight requests within server.shutdown_timeout and running jobs
within workers.shutdown_timeout. SIGHUP re-reads bekup.toml and applies the keys that are
safe to change at runtime (D-200).

```
bekup serve [flags]
```

### Options

```
  -h, --help                     help for serve
      --no-worker bekup worker   do not run background workers in this process (run bekup worker instead)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

