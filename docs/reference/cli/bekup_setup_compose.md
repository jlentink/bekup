## bekup setup compose

Print a compose file variant (the release assets; local examples)

### Synopsis

Print one compose file variant rendered from the compose file embedded in this
binary (D-205): traefik (bekup with its own Traefik; release asset
compose.yaml), traefik-external (behind your Traefik;
compose.traefik-external.yaml), npm (behind Nginx Proxy Manager;
compose.npm.yaml) or full (every service; setup-first). --database external
(D-208) renders the form for your own PostgreSQL (no postgres service; release
assets compose.external-db.yaml, compose.traefik-external.external-db.yaml,
compose.npm.external-db.yaml).

With --image the x-bekup-image line names that digest-pinned reference (the
release workflow renders its assets this way); without it the line takes the
image from BEKUP_IMAGE with no default (the committed files in
deploy/compose/, written by just gen; setup requires BEKUP_IMAGE to be
digest-pinned and keeps it in .env). just compose-examples writes all
variants to dist/compose/.

```
bekup setup compose --variant traefik|traefik-external|npm|full [--database bundled|external] [--image <pinned>] [flags]
```

### Options

```
      --database string   bundled (the postgres service) or external (your own PostgreSQL, D-208) (default "bundled")
  -h, --help              help for compose
      --image string      pin the x-bekup-image line to this repo[:tag]@sha256:<digest>
      --variant string    traefik, traefik-external, npm or full (required)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory (wizard on a terminal)

