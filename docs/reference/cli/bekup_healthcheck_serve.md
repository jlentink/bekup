## bekup healthcheck serve

GET /healthz on the address from server.listen (no shell or curl needed)

```
bekup healthcheck serve [flags]
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

* [bekup healthcheck](bekup_healthcheck.md)	 - Probe a local bekup component (container healthchecks; exit 0 healthy, 1 unhealthy)

