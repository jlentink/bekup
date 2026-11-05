## bekup setup web

Browser setup: the setup wizard over HTTPS with a one-time code (D-217)

### Synopsis

Serves the setup wizard to one browser session over HTTPS (D-217): the
terminal shows the address with a one-time access code and the fingerprint
of the self-signed certificate; compare it before you continue in the
browser. Setup stops when it is finished, after 30 minutes without
activity, after 2 hours, or after 10 wrong codes.

Two runs, from the compose file:

  docker compose run --rm --service-ports setup-web      # questions, files, check
  docker compose up -d postgres                          # bundled database only
  docker compose run --rm --service-ports setup-web-db   # roles and migrations

The first run writes the deployment like `bekup setup` and offers the
non-secret files (compose, setup's .env variables, TOML, state) as a zip;
secrets never leave the server. The second (--database) runs as the bekup
user with migrate's configuration and secrets and, of the deployment
directory, only the state file: db bootstrap for your own PostgreSQL, then
migrate up.
The first admin is still created on the host (bekup admin create, D-113).

```
bekup setup web [flags]
```

### Options

```
      --allow-version-mismatch   accept an image tag that differs from this bekup version
      --database                 the database run (setup-web-db service): db bootstrap and migrate up
      --dir string               deployment directory (must exist) (default "/deploy")
  -h, --help                     help for web
      --host strings             host name or address the browser opens setup with (certificate and printed URL; default BEKUP_SETUP_WEB_HOST)
      --listen string            listen address inside the container (default ":8443")
      --ownership string         auto, chown, print or none (default "auto")
      --public-port int          published port shown in the URL (default BEKUP_SETUP_WEB_PORT or 8443) (default 8443)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory (wizard on a terminal)

