## bekup completion zsh

Generate the autocompletion script for zsh

### Synopsis

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(bekup completion zsh)

To load completions for every new session, execute once:

#### Linux:

	bekup completion zsh > "${fpath[1]}/_bekup"

#### macOS:

	bekup completion zsh > $(brew --prefix)/share/zsh/site-functions/_bekup

You will need to start a new shell for this setup to take effect.


```
bekup completion zsh [flags]
```

### Options

```
  -h, --help              help for zsh
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

