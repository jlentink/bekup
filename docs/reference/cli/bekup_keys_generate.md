## bekup keys generate

Create a new master keyring file (256-bit key, mode 0600, never overwrites)

```
bekup keys generate --out <path> [flags]
```

### Options

```
  -h, --help         help for generate
      --out string   path of the keyring file to create (must not exist)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup keys](bekup_keys.md)	 - Generate and rotate the master keyring

