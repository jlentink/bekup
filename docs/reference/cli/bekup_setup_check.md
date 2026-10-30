## bekup setup check

Verify a deployment directory (read-only; never reads secret contents)

### Synopsis

Check the deployment directory against the ownership manifest (presence,
type, mode, owner, no symlinks), that every image is digest-pinned and the
generated files are unchanged, that bekup.toml / runner.toml validate and
agree with .env (base_url, trusted_proxies, listen addresses, the link
network of the external-proxy variants, Docker GID), certificate expiry,
shell variables overriding .env, and whether the CA key is still on disk.

Compose-first (D-205) it also recognizes compose.yaml (the recorded variant,
the pinned x-bekup-image; a newer image asks for setup upgrade). With the
committed file from deploy/compose/, BEKUP_IMAGE in .env must be pinned,
equal the recorded image and not be overridden by the environment. The
hardening lint checks the project docker compose would run: compose.yaml,
compose.override.yaml and .env resolved with the Docker Compose loader
(every document, merge key, !reset/!override and ${VAR}): bekup's own
services, networks, volumes, secrets and configs must equal the shipped
variant's except for the operator-tunable attributes (resource limits,
restart, the documented proxy, runner and serve overrides); services you
add keep an allowlist (pinned image, read-only, cap_drop ALL,
no-new-privileges, non-root, binds only from relative paths inside the
deployment directory and never of bekup's files, no host namespaces,
devices, privileged hooks, ports, bekup's volumes, secrets or internal
networks); only bekup's compose variables, and only where the shipped file
uses them. Multi-document files and DOCKER_HOST-style variables in .env are
refused. The lint is a guardrail: only the unchanged release file without
an override counts as verified (docs/installation/compose.md):

  docker compose -f compose.yaml run --rm setup check

-f compose.yaml keeps compose.override.yaml out of the container that runs
the check (an override could otherwise change the setup service itself);
check still reads and lints the override. A tampered compose.yaml also
defines the checking container: without compose (or for a file you do not
trust), run the attested release image with the same hardening:

  docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -v /opt/bekup:/deploy:ro "$IMAGE" setup check

Exits non-zero on errors. --fix-ownership re-applies the
manifest's modes and owners first (needs CAP_CHOWN).

```
bekup setup check [flags]
```

### Options

```
      --dir string         deployment directory created by setup init (default "/deploy")
      --fix-ownership      re-apply the manifest's modes and owners (e.g. after certs rotate)
  -h, --help               help for check
      --ownership string   auto, chown, print or none (owner checks are skipped with none) (default "auto")
      --warn-days int      warn about certificates expiring within this many days (default 30)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory

