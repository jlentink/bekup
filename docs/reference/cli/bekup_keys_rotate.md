## bekup keys rotate

Add a new primary key to the keyring file (old keys stay for decryption)

### Synopsis

Adds a freshly generated key to the keyring and makes it the primary (encrypting) key.
The file is rewritten atomically with mode 0600. Every existing key is kept, so data
encrypted with older keys stays readable. Re-encrypting the stored credentials with the
new primary key is wired to the database in a later task; until then, never remove old keys.

```
bekup keys rotate --keyring <path> [flags]
```

### Options

```
  -h, --help             help for rotate
      --keyring string   path of the existing keyring file
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup keys](bekup_keys.md)	 - Generate and rotate the master keyring

