## bekup setup

Write and maintain a container-only compose deployment directory

### Synopsis

Create, upgrade and check a compose deployment directory from the bekup image
alone (no source checkout; D-203, D-205).

Compose-first (the default): save a compose file as compose.yaml
(compose.yaml, compose.traefik-external.yaml or compose.npm.yaml: a release
asset from https://github.com/jlentink/bekup/releases/latest, pinned and
attested, or the committed file from deploy/compose/ in the repository,
which takes the image from BEKUP_IMAGE) and run its setup service, a
one-shot root container with only CAP_CHOWN, no network and no logs:

  DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup
  docker compose up -d

With the committed file, prefix the first command with
BEKUP_IMAGE=<repo>:<tag>@sha256:<digest>; setup writes it to .env.

setup writes everything except compose.yaml: .env, bekup.toml, runner.toml,
the secrets and certificates, deploy/** and reference/*.

Setup-first (--write-compose --variant ...): setup also writes compose.yaml,
run with docker run and the same hardening:

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -e BEKUP_IMAGE="$IMAGE" -v /opt/bekup:/deploy \
    "$IMAGE" setup init --write-compose --variant full --docker-gid "$(stat -c %g /var/run/docker.sock)"

--log-driver none keeps the container's output (the passphrase prompt, a
generated passphrase) out of Docker's logs. See docs/installation/compose.md.

```
bekup setup [flags]
```

### Options

```
  -h, --help   help for setup
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
* [bekup setup upgrade](bekup_setup_upgrade.md)	 - Rewrite the generated files (deploy/**, reference/*) for a new image

