## bekup setup renew

List and renew the internal certificates (runner, serve, postgres; CA rotation)

### Synopsis

List the internal certificates of the deployment directory with their expiry
and renew them (D-208). Warnings start 30 days before expiry, the threshold
of the hourly security check and setup check.

The CA key stays offline, so nothing renews automatically: mount the CA
media at <keys>/certs/ca for the run, e.g.

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -v "$PWD":/deploy -v /media/usb/bekup-ca:/deploy/secrets/certs/ca "$IMAGE" setup renew

On a terminal it is guided. Otherwise (or with --non-interactive):

  setup renew --list                 show the certificates, change nothing
  setup renew                        renew the expiring leaf certificates
  setup renew --all                  renew every leaf certificate
  setup renew --component serve      renew exactly these
  setup renew --ca stage|promote|finish   one phase of the CA rotation (D-090)

It sets the owners and modes of the manifest again and prints the services
to recreate (docker compose up -d --force-recreate <services>). A CA
rotation is resumable: the files of the CA directory tell the next phase;
recreate every service after each phase. setup has no database access, so
the renewal is not audited: it prints the new fingerprints as evidence.

```
bekup setup renew [flags]
```

### Options

```
      --all                         renew every leaf certificate, not only the expiring ones
      --ca string                   run one phase of the CA rotation: stage, promote or finish
      --ca-passphrase-file string   file with the CA key passphrase
      --ca-passphrase-stdin         read the CA key passphrase from the first line of stdin
      --component strings           renew exactly this leaf certificate: runner, serve or postgres (repeatable)
      --dir string                  deployment directory created by setup init (default "/deploy")
  -h, --help                        help for renew
      --list                        list the certificates and their expiry; change nothing
      --non-interactive             never start the guided renewal
      --ownership string            auto, chown, print or none (default "auto")
      --plain                       plain-text interface for the guided renewal (NO_COLOR only drops the colors)
      --warn-days int               renew (and warn about) certificates expiring within this many days (default 30)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory (wizard on a terminal)

