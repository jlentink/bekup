## bekup keys

Generate and rotate the master keyring; export the instance public keys

### Synopsis

Manage the master keyring that encrypts every credential bekup stores (AES-256-GCM).
Format, rotation and escrow: docs/security/key-management.md.

```
bekup keys [flags]
```

### Options

```
  -h, --help   help for keys
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup keys export-public](bekup_keys_export-public.md)	 - Export the instance public keys (manifest signatures) for escrow and offline verification
* [bekup keys generate](bekup_keys_generate.md)	 - Create a new master keyring file (256-bit key, mode 0600, never overwrites)
* [bekup keys rotate](bekup_keys_rotate.md)	 - Add a new primary key to the keyring file (old keys stay for decryption)

