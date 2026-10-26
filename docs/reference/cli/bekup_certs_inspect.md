## bekup certs inspect

Show subject, SANs, validity and fingerprints; fail if anything expires soon

### Synopsis

Print every certificate in --dir (all *.crt files, recursively) and/or the
given files or directories. Exits non-zero when a certificate is expired, not
yet valid, or expires within --warn-days days.

```
bekup certs inspect [path...] [flags]
```

### Options

```
      --dir string      certificate directory to inspect
  -h, --help            help for inspect
      --warn-days int   fail when a certificate expires within this many days (default 30)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup certs](bekup_certs.md)	 - Create, rotate and inspect the private CA and component mTLS certificates

