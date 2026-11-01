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
own-proxy) and the database mode (the *.external-db.yaml files: your own
PostgreSQL; a customized file needs --variant and passes the hardening
lint) and never writes compose.yaml. It asks only for the domain, the ACME
e-mail (own Traefik), your proxy's network and container (own proxy), your
PostgreSQL (external database), the CA key passphrase and, unless
DOCKER_GID is set, the Docker socket's group.

Setup-first (--write-compose --variant traefik|own-proxy|full, or --proxy
traefik|own, [--database external]): setup also writes compose.yaml (full:
every service, --proxy traefik, nginx or none).

The wizard offers bekup's own Traefik or your own reverse proxy on this
Docker host. A proxy on another machine is not a wizard choice (D-215): the
advanced path is --write-compose --variant full --proxy none --trusted-proxy
<CIDR> (no bundled proxy; run a small proxy on this host, or route to
bekup's frontend network yourself). serve's port is never published.

Your own reverse proxy on this Docker host (--proxy own, D-215): serve
joins the Docker network your proxy is on (--proxy-network) under the
alias bekup-serve and trusts the proxy by its container name
(--proxy-container); point the proxy at http://bekup-serve:8080.
--proxy-labels adds the Traefik Docker-provider labels; --proxy-network-mode
link keeps the dedicated bekup-proxy link network with fixed addresses
(--proxy-subnet, --proxy-ip, --serve-ip). Setup writes these extras and a
database container's network into compose.override.yaml; compose.yaml stays
the release file.

Your own PostgreSQL (--database external, D-207, D-208, D-215): --db-host,
--db-location docker --db-network <net> for a container on this Docker host
(--db-host is then its container name or alias), --db-tls verify-ca-file
(default, with --db-ca-file: its CA bundle inside the deployment directory;
copied), verify-system, or the explicit weakenings require and disable, and
--db-auth: bootstrap (default: setup generates the two role passwords and
prints the one `docker compose run --rm migrate db bootstrap` command), existing-roles
(the roles exist: their passwords from --db-password-file /
--db-owner-password-file or the terminal), dba-sql (setup writes
<keys>/bekup-roles.sql with SCRAM verifiers for your DBA) or single-account
(--db-user: one account for everything; an explicit weakening). setup has no
network: it never connects to the database.

On a terminal, missing answers are prompted; --non-interactive requires the
flags instead. Existing files are never overwritten without --force (non-secret
files only) or --regenerate-secrets (terminal + typed confirmation). An
existing compose.override.yaml is never rewritten, also not with --force: it
must carry the blocks setup would write (setup names them).

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
      --db-ca-file string               external database, --db-tls verify-ca-file: CA bundle (PEM) of the server certificate, a path inside --dir (copied to <keys>/bekup_database_ca.crt)
      --db-host string                  external database: host name or IP address of your PostgreSQL (15-17)
      --db-location string              external database: remote (another server, default) or docker (a container on this Docker host: --db-network)
      --db-name string                  external database: name of bekup's database (default bekup)
      --db-network string               external database, --db-location docker: the Docker network the database container is on (migrate, serve and admin join it)
      --db-owner-password-file string   external database: file (inside --dir) with the bekup_owner password instead of a generated or prompted one; copied
      --db-password-file string         external database: file (inside --dir) with the bekup_app (single-account: the account's) password instead of a generated or prompted one; copied
      --db-port int                     external database: port (default 5432)
      --db-tls string                   external database: verify-ca-file (default: --db-ca-file), verify-system (the image's system CA bundle), require (WEAKENED: encrypted, not verified) or disable (WEAKENED: no TLS)
      --db-tls-server-name string       external database, verifying --db-tls: host name the server certificate names, when --db-host is an IP address
      --db-user string                  external database, single-account: the account's role name
      --dir string                      deployment directory (must exist) (default "/deploy")
      --docker-gid int                  group ID of the Docker socket on the host: stat -c %g /var/run/docker.sock (default: $DOCKER_GID)
      --domain string                   public host name of the UI (base_url https://<domain>)
      --force                           rewrite existing NON-SECRET files (deploy, reference, .env, TOMLs; compose.yaml only with --write-compose)
      --frontend-subnet string          advanced: FRONTEND_SUBNET (default 10.213.250.0/24; not own-proxy)
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
      --proxy string                    reverse proxy: traefik (bekup's own; variant traefik) or own (your reverse proxy on this Docker host; variant own-proxy); advanced, variant full: traefik, nginx or none (no bundled proxy: run a small proxy on this host, or route to bekup's frontend network yourself; --trusted-proxy)
      --proxy-container string          own proxy (join mode): your proxy's container name, trusted by name (server.trusted_proxy_hosts)
      --proxy-ip string                 advanced: the proxy's fixed address: PROXY_FRONTEND_IP (default 10.213.250.5) or, link mode, your proxy's address on the link network (default 10.213.253.5)
      --proxy-labels                    own proxy: add the Traefik Docker-provider labels on serve (compose.override.yaml)
      --proxy-network string            own proxy: the Docker network your proxy is on (serve joins it); link mode: the link network's name (default bekup-proxy)
      --proxy-network-mode string       own proxy, advanced: join (default: serve joins your proxy's network) or link (the bekup-proxy link network with fixed addresses, D-205)
      --proxy-subnet string             link mode: subnet of the link network (default 10.213.253.0/28)
      --regenerate-secrets              delete and regenerate EVERY secret and certificate (terminal + typed confirmation)
      --runner-ctl-subnet string        advanced: RUNNER_CTL_SUBNET (default 10.213.251.0/24)
      --runner-ip string                advanced: RUNNER_CTL_IP (default 10.213.251.10)
      --serve-ip string                 advanced: serve's fixed address towards the proxy: SERVE_FRONTEND_IP (default 10.213.250.10) or, link mode, SERVE_PROXY_IP (default 10.213.253.10)
      --target-cidr strings             CIDR of target databases the runner may reach (repeatable)
      --target-host strings             host name of a target database the runner may reach (repeatable)
      --tls-mode string                 bundled proxy TLS: acme (Let's Encrypt, default) or static (your certificate in proxy-tls/)
      --traefik-certresolver string     --proxy-labels: your Traefik's certificate resolver (default letsencrypt)
      --traefik-entrypoint string       --proxy-labels: your Traefik's HTTPS entrypoint (default websecure)
      --trusted-proxy strings           CIDR of your own reverse proxy on the frontend network (--proxy none; repeatable)
      --use-existing-ca                 issue the certificates from the CA mounted at <keys>/certs/ca (offline media; its passphrase is asked) instead of creating a new CA
      --variant string                  traefik, own-proxy or full (--write-compose; implied by --proxy traefik|own; compose-first only to name the base of a customized compose.yaml)
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

