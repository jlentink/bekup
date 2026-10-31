## bekup runner

Run the Docker runner (mTLS gRPC, reads runner.toml)

### Synopsis

Run the runner sidecar: the only component with Docker access. It serves the typed
runner.v1 gRPC API over mTLS (TLS 1.3, private CA, allowed client SANs from runner.toml)
and the standard gRPC health service on listen.health_socket for `bekup healthcheck runner`.
It runs dump/restore containers on per-run internal networks through the Docker socket
(docker.host) and removes leftovers of earlier runs at startup and every 5 minutes.
It refuses to start, before it touches Docker or listens, on another version than
BEKUP_SETUP_VERSION (and, outside dev mode, on BEKUP_IMAGE_MODE=following without it; D-212).
SIGINT/SIGTERM drain in-flight calls for up to 45s.

```
bekup runner [flags]
```

### Options

```
  -h, --help   help for runner
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager

