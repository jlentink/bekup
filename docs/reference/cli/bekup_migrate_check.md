## bekup migrate check

Preflight: server version, database owner, citext, role checks, TLS facts (changes nothing)

```
bekup migrate check [flags]
```

### Options

```
  -h, --help   help for check
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup migrate](bekup_migrate.md)	 - Apply database migrations (as bekup_owner); default action: up

