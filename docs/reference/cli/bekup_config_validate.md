## bekup config validate

Load and validate the configuration; report every error at once

```
bekup config validate [flags]
```

### Options

```
  -h, --help   help for validate
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

