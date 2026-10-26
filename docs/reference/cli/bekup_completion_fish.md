## bekup completion fish

Generate the autocompletion script for fish

### Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	bekup completion fish | source

To load completions for every new session, execute once:

	bekup completion fish > ~/.config/fish/completions/bekup.fish

You will need to start a new shell for this setup to take effect.


```
bekup completion fish [flags]
```

### Options

```
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup completion](bekup_completion.md)	 - Generate the autocompletion script for the specified shell

