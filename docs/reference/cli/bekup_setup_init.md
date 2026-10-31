## bekup setup init

Set up a deployment directory (config, secrets, certificates, deploy assets)

### Synopsis

Set up a compose deployment directory: deploy/**, reference/*.example.toml,
.env, minimal bekup.toml and runner.toml, the database passwords, the master
keyring and the private CA with the runner/serve(/postgres) certificates,
then hand every file to the container user that reads it (--ownership).
The interactive wizard (`bekup setup` on a terminal) asks the same
questions and writes the same files.

Compose-first (default, D-205): compose.yaml must exist (a release file, or
the committed file from deploy/compose/). setup takes the pinned image from
its x-bekup-image line (the committed file: from BEKUP_IMAGE, which must be
digest-pinned and is written to .env), recognizes the variant (traefik,
traefik-external, npm) and the database mode (the *.external-db.yaml files:
your own PostgreSQL; a customized file needs --variant and passes the
hardening lint) and never writes compose.yaml. It asks only for the domain,
the ACME e-mail (own Traefik), your PostgreSQL (external database), the CA
key passphrase and, unless DOCKER_GID is set, the Docker socket's group.

Setup-first (--write-compose --variant traefik|traefik-external|npm|full
[--database external]): setup also writes compose.yaml (full: every
service, --proxy traefik, nginx or none).

Your own PostgreSQL (--database external, D-207, D-208): --db-host,
--db-ca-file (its CA bundle, inside the deployment directory; copied) and
--db-auth: bootstrap (default: setup generates the two role passwords and
prints the one `docker compose run --rm migrate db bootstrap` command), existing-roles
(the roles exist: their passwords from --db-password-file /
--db-owner-password-file or the terminal), dba-sql (setup writes
<keys>/bekup-roles.sql with SCRAM verifiers for your DBA) or single-account
(--db-user: one account for everything; an explicit weakening). setup has no
network: it never connects to the database.

On a terminal, missing answers are prompted; --non-interactive requires the
flags instead. Existing files are never overwritten without --force (non-secret
files only) or --regenerate-secrets (terminal + typed confirmation).

```
bekup setup init [flags]
```

### Options

```
      --acme-email string               ACME account e-mail (--tls-mode acme)
      --acme-staging                    use the Let's Encrypt staging CA (testing)
      --allow-version-mismatch          accept an image tag that differs from this bekup version
      --ca-passphrase-file string       file with the CA key passphrase (min 16 characters; checked like every secret file)
      --ca-passphrase-stdin             read the CA key passphrase from the first line of stdin
      --database string                 bundled (the postgres service, default) or external (your own PostgreSQL, D-208; compose-first: must match compose.yaml)
      --db-admin-user string            external database, bootstrap: the administrator role named in the printed bootstrap command
      --db-auth string                  external database: bootstrap (default), existing-roles, dba-sql or single-account (an explicit weakening)
      --db-ca-file string               external database: CA bundle (PEM) of the server certificate, a path inside --dir (copied to <keys>/bekup_database_ca.crt; verified with tls_mode verify-full)
      --db-host string                  external database: host name or IP address of your PostgreSQL (15-17)
      --db-name string                  external database: name of bekup's database (default bekup)
      --db-owner-password-file string   external database: file (inside --dir) with the bekup_owner password instead of a generated or prompted one; copied
      --db-password-file string         external database: file (inside --dir) with the bekup_app (single-account: the account's) password instead of a generated or prompted one; copied
      --db-port int                     external database: port (default 5432)
      --db-tls-server-name string       external database: host name the server certificate names, when --db-host is an IP address
      --db-user string                  external database, single-account: the account's role name
      --dir string                      deployment directory (must exist) (default "/deploy")
      --docker-gid int                  group ID of the Docker socket on the host: stat -c %g /var/run/docker.sock (default: $DOCKER_GID)
      --domain string                   public host name of the UI (base_url https://<domain>)
      --force                           rewrite existing NON-SECRET files (deploy, reference, .env, TOMLs; compose.yaml only with --write-compose)
      --frontend-subnet string          advanced: FRONTEND_SUBNET (default 10.213.250.0/24; not traefik-external/npm)
      --generate-ca-passphrase          generate a passphrase and show it ONCE on stderr (only when stderr is a terminal; run with --log-driver none)
  -h, --help                            help for init
      --image string                    bekup image pinned by digest, repo[:tag]@sha256:<digest>, or ghcr.io/jlentink/bekup:latest to follow the releases (D-212, an explicit weakening) (default: $BEKUP_IMAGE; compose-first: must equal the x-bekup-image line, or BEKUP_IMAGE for the committed file)
      --import-master-key string        import an existing master keyring (a file inside --dir, e.g. restored from escrow) instead of generating one
      --keep-existing                   keep existing files and create only the missing ones
      --keys-dir string                 secrets directory, relative to --dir (mount another volume there to keep secrets elsewhere) (default "secrets")
      --master-key-check-value string   with --import-master-key: the expected key-check value (kcv1:...)
      --metrics-ip string               advanced: SERVE_METRICS_IP (default 10.213.252.10)
      --metrics-subnet string           advanced: METRICS_SUBNET (default 10.213.252.0/24)
      --non-interactive                 never prompt; every answer must come from flags
      --ownership string                auto, chown (needs CAP_CHOWN), print (show the chown commands) or none (Docker Desktop) (default "auto")
      --proxy string                    variant full: reverse proxy traefik, nginx or none (your own proxy on the frontend network)
      --proxy-ip string                 advanced: the proxy's fixed address: PROXY_FRONTEND_IP (default 10.213.250.5) or, traefik-external/npm, your proxy's address on the link network (default 10.213.253.5)
      --proxy-network string            traefik-external/npm: name of the external link network (default bekup-proxy)
      --proxy-subnet string             traefik-external/npm: subnet of the link network (default 10.213.253.0/28)
      --regenerate-secrets              delete and regenerate EVERY secret and certificate (terminal + typed confirmation)
      --runner-ctl-subnet string        advanced: RUNNER_CTL_SUBNET (default 10.213.251.0/24)
      --runner-ip string                advanced: RUNNER_CTL_IP (default 10.213.251.10)
      --serve-ip string                 advanced: serve's fixed address towards the proxy: SERVE_FRONTEND_IP (default 10.213.250.10) or, traefik-external/npm, SERVE_PROXY_IP (default 10.213.253.10)
      --target-cidr strings             CIDR of target databases the runner may reach (repeatable)
      --target-host strings             host name of a target database the runner may reach (repeatable)
      --tls-mode string                 bundled proxy TLS: acme (Let's Encrypt, default) or static (your certificate in proxy-tls/)
      --traefik-certresolver string     traefik-external: your Traefik's certificate resolver (default letsencrypt)
      --traefik-entrypoint string       traefik-external: your Traefik's HTTPS entrypoint (default websecure)
      --trusted-proxy strings           CIDR of your own reverse proxy on the frontend network (--proxy none; repeatable)
      --use-existing-ca                 issue the certificates from the CA mounted at <keys>/certs/ca (offline media; its passphrase is asked) instead of creating a new CA
      --variant string                  traefik, traefik-external, npm or full (--write-compose; compose-first only to name the base of a customized compose.yaml)
      --write-compose                   setup-first: also write compose.yaml (needs --variant); default is compose-first (compose.yaml exists, never written)
```

### Options inherited from parent commands

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup setup](bekup_setup.md)	 - Write and maintain a container-only compose deployment directory (wizard on a terminal)

