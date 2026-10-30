## bekup setup upgrade

Rewrite the generated files (deploy/**, reference/*) for a new image

### Synopsis

Rewrite only the generated files for a new image: deploy/** (stale assets are
removed), reference/* and bekup-setup.json. .env, the TOML files
(re-validated with the new validators) and existing secrets are never
touched; secrets that are new in the manifest are generated. Local edits of
generated files are refused, or with --force copied to <file>.bak-<timestamp>
first. Back up before upgrading (docs/installation/upgrades.md).

Compose-first (D-205): replace compose.yaml with the new release file first,
then run its setup service; the new image comes from compose.yaml, the
variant must stay the same, and compose.yaml is never written:

  docker compose run --rm setup upgrade
  docker compose pull && docker compose up -d

With the committed file from deploy/compose/ (the image comes from
BEKUP_IMAGE), name the new image in the shell, so compose runs the new
image's setup, which rewrites the BEKUP_IMAGE line of .env:

  BEKUP_IMAGE=<repo>:<tag>@sha256:<digest> docker compose run --rm setup upgrade

Setup-first: --write-compose --image <new pinned image> also rewrites
compose.yaml (run with docker run, see bekup setup --help).

```
bekup setup upgrade [flags]
```

### Options

```
      --allow-downgrade             allow a bekup version older than the recorded one
      --allow-version-mismatch      accept an image tag that differs from this bekup version
      --ca-passphrase-file string   file with the CA key passphrase (min 16 characters; checked like every secret file)
      --ca-passphrase-stdin         read the CA key passphrase from the first line of stdin
      --dir string                  deployment directory created by setup init (default "/deploy")
      --force                       replace locally edited generated files (a .bak-<timestamp> copy is kept)
      --generate-ca-passphrase      generate a passphrase and show it ONCE on stderr (only when stderr is a terminal; run with --log-driver none)
  -h, --help                        help for upgrade
      --image string                the new bekup image pinned by digest (default: $BEKUP_IMAGE; compose-first: must equal the x-bekup-image line, or BEKUP_IMAGE for the committed file)
      --ownership string            auto, chown, print or none (default "auto")
      --write-compose               setup-first deployments: rewrite compose.yaml for --image (refused compose-first)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory

