## bekup external-api

Run the token-authenticated REST API for CI pipelines (reads external-api.toml)

### Synopsis

Run the external API (D-220): POST /api/v1/jobs/{jobId}/runs starts a backup job's run,
GET /api/v1/requests/{requestId} reports its state and GET /api/v1/self the token's scope and
the client address bekup sees. Clients authenticate with a per-job API token in the
Authorization: Bearer header only. It serves plain HTTP behind the TLS reverse proxy
(listen.trusted_proxies / trusted_proxy_hosts) and connects to the app database only as
bekup_external; it never reads bekup.toml, the master key or runner certificates.
It refuses to start on another version than BEKUP_SETUP_VERSION (D-212), when the
database role holds anything beyond its allowlist, or when the schema version differs from
this binary (run `bekup migrate up` first). SIGINT/SIGTERM drain in-flight requests within
listen.shutdown_timeout.

```
bekup external-api [flags]
```

### Options

```
  -h, --help   help for external-api
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

