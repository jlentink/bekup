## bekup config

Validate or print the effective configuration

### Options

```
  -h, --help     help for config
      --runner   use runner.toml instead of bekup.toml
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup config print](bekup_config_print.md)	 - Print the effective configuration with the source of every key (secrets redacted)
* [bekup config validate](bekup_config_validate.md)	 - Load and validate the configuration; report every error at once

