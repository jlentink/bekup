## bekup config print

Print the effective configuration with the source of every key (secrets redacted)

```
bekup config print [flags]
```

### Options

```
  -h, --help       help for print
      --redacted   redact secrets (always on; --redacted=false is refused) (default true)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
      --runner             use runner.toml instead of bekup.toml
```

### SEE ALSO

* [bekup config](bekup_config.md)	 - Validate or print the effective configuration

