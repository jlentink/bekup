## bekup setup

Write and maintain a container-only compose deployment directory (wizard on a terminal)

### Synopsis

Create, upgrade and check a compose deployment directory from the bekup image
alone (no source checkout; D-203, D-205, D-208).

On a terminal, `bekup setup` without a subcommand starts the setup wizard: it asks
for the reverse proxy, the domain, the database (bundled, or your own
PostgreSQL), the master key, the internal CA and the Docker group, shows a
summary and writes everything, compose.yaml included (it is kept when it
exists). In an existing deployment it offers check, upgrade and certificate
renewal. The main path, in an empty directory:

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -e BEKUP_IMAGE="$IMAGE" -e DOCKER_GID="$(stat -c %g /var/run/docker.sock)" \
    -v "$PWD":/deploy "$IMAGE" setup
  docker compose up -d

--log-driver none keeps the container's output (a generated passphrase) out
of Docker's logs. The wizard needs stdin and stdout on a terminal (a
generated passphrase is shown once on the alternate screen, never into a
pipe or the scrollback). NO_COLOR and TERM=dumb drop the colors; --plain
uses a plain-text interface (numbered choices, no escape sequences; a
generated passphrase then stays in the scrollback).
Without a terminal use the subcommands with flags (setup init
--non-interactive ..., setup renew, setup upgrade, setup check).

Compose-first: save a release compose file as compose.yaml and run its
setup service (the init subcommand with prompts):

  DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup

See docs/installation/compose.md.

```
bekup setup [flags]
```

### Options

```
      --allow-version-mismatch   accept an image tag that differs from this bekup version
      --dir string               deployment directory (must exist) (default "/deploy")
  -h, --help                     help for setup
      --ownership string         auto, chown, print or none (default "auto")
      --plain                    plain-text interface: numbered choices, no escape sequences (a generated passphrase stays in the scrollback)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup](bekup.md)	 - bekup: secure, self-hosted database backup manager
* [bekup setup check](bekup_setup_check.md)	 - Verify a deployment directory (read-only; never reads secret contents)
* [bekup setup compose](bekup_setup_compose.md)	 - Print a compose file variant (the release assets; local examples)
* [bekup setup init](bekup_setup_init.md)	 - Set up a deployment directory (config, secrets, certificates, deploy assets)
* [bekup setup renew](bekup_setup_renew.md)	 - List and renew the internal certificates (runner, serve, postgres; CA rotation)
* [bekup setup upgrade](bekup_setup_upgrade.md)	 - Rewrite the generated files (deploy/**, reference/*) for a new image

