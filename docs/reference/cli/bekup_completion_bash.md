## bekup completion bash

Generate the autocompletion script for bash

### Synopsis

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(bekup completion bash)

To load completions for every new session, execute once:

#### Linux:

	bekup completion bash > /etc/bash_completion.d/bekup

#### macOS:

	bekup completion bash > $(brew --prefix)/etc/bash_completion.d/bekup

You will need to start a new shell for this setup to take effect.


```
bekup completion bash
```

### Options

```
  -h, --help              help for bash
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

