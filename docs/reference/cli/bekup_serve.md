## bekup serve

Run the web UI, GraphQL API and scheduler

### Synopsis

Run the HTTP server (embedded web UI, GraphQL API, health endpoints; workers and the
scheduler follow in later releases) as bekup_app. Refuses to start when the database role is
privileged or the schema version differs from this binary (run `bekup migrate` first).
SIGINT/SIGTERM drain in-flight requests within server.shutdown_timeout.

```
bekup serve [flags]
```

### Options

```
  -h, --help   help for serve
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

