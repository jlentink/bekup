## bekup certs

Create, rotate and inspect the private CA and component mTLS certificates

### Synopsis

Manage the runner mTLS PKI: a private ed25519 CA plus one certificate per
component (runner = server, serve and worker = clients identified by the URI
SAN spiffe://bekup/<component>). See docs/security/mtls-certs.md.

### Options

```
  -h, --help   help for certs
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup certs init](bekup_certs_init.md)	 - Create a new private CA and the component certificates
* [bekup certs inspect](bekup_certs_inspect.md)	 - Show subject, SANs, validity and fingerprints; fail if anything expires soon
* [bekup certs rotate](bekup_certs_rotate.md)	 - Reissue component certificates, or rotate the CA in three phases

