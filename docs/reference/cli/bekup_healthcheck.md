## bekup healthcheck

Probe a local bekup component (container healthchecks; exit 0 healthy, 1 unhealthy)

### Options

```
  -h, --help   help for healthcheck
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup healthcheck runner](bekup_healthcheck_runner.md)	 - Check the local runner gRPC listener (not implemented yet)
* [bekup healthcheck serve](bekup_healthcheck_serve.md)	 - GET /healthz on the address from server.listen (no shell or curl needed)

