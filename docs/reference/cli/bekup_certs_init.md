## bekup certs init

Create a new private CA and the component certificates

### Synopsis

Create a private ed25519 CA (name-constrained to the runner names and the
spiffe://bekup trust domain) and ed25519 leaf certificates. Layout:

  <dir>/ca/ca.crt, ca.key         CA (move ca.key offline!)
  <dir>/runner/runner.{crt,key}, ca.crt
  <dir>/serve/serve.{crt,key}, ca.crt
  <dir>/worker/worker.{crt,key}, ca.crt

The CA key is encrypted with an age passphrase unless --unencrypted-ca-key.
Existing material is never overwritten without --force.

```
bekup certs init [flags]
```

### Options

```
      --ca-days int                 CA validity in days (max 3650) (default 1825)
      --ca-passphrase-file string   file with the CA key passphrase (min 16 characters); default: prompt on the terminal
      --component strings           components to issue: runner, serve, worker (repeatable; default all)
      --dir string                  output directory (required)
      --force                       overwrite existing certificate material (creates a NEW CA: every component must be redeployed)
  -h, --help                        help for init
      --leaf-days int               leaf certificate validity in days (max 825) (default 365)
      --runner-san strings          extra DNS name or IP for the runner certificate (repeatable; "runner" is always included; also limits the CA's name constraints)
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

