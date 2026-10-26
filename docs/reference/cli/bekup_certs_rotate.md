## bekup certs rotate

Reissue component certificates, or rotate the CA in three phases

### Synopsis

Reissue leaf certificates (new keys) from the existing CA:

  bekup certs rotate --dir <dir> --component serve [--component worker]
  bekup certs rotate --dir <dir> --all

Rotate the CA itself with an overlap period; restart every component after
each phase:

  --ca stage    create the next CA; every trust bundle trusts old + next
  --ca promote  the next CA becomes current; reissue all leaves from it
  --ca finish   drop the old CA from every trust bundle

Needs the CA key (bring it back from offline storage temporarily).

```
bekup certs rotate [flags]
```

### Options

```
      --all                         reissue every component present in --dir
      --ca string                   rotate the CA: stage, promote or finish
      --ca-days int                 validity of the next CA in days (--ca stage) (default 1825)
      --ca-passphrase-file string   file with the CA key passphrase (min 16 characters); default: prompt on the terminal
      --component strings           component to reissue: runner, serve, worker (repeatable)
      --dir string                  certificate directory created by certs init (required)
  -h, --help                        help for rotate
      --leaf-days int               leaf certificate validity in days (default 365)
      --runner-san strings          replace the runner's extra DNS names/IPs (default: keep the current ones)
      --unencrypted-ca-key          write the CA key WITHOUT a passphrase (not recommended; only if the CA directory is moved offline at once)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup certs](bekup_certs.md)	 - Create, rotate and inspect the private CA and component mTLS certificates

