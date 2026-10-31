# TLS for the app database

bekup encrypts and verifies the connection between `serve` / `worker` /
`migrate` and its own Postgres (D-100). The shipped compose stack runs with
TLS on, in production and in dev.

## Configuration

`[database]` in `bekup.toml` (reference: [configuration.md](../configuration.md)):

| Key | Default | Meaning |
|---|---|---|
| `tls_mode` | `verify-full` | `verify-full`: encrypted; the server certificate must chain to `tls_ca_file` **and** match the host name (the URL host, `database.host`, or `tls_server_name`). `require`: encrypted; with `tls_ca_file` the chain is verified but not the host name, without it nothing is verified. `disable`: plain text. |
| `tls_ca_file` | `/run/secrets/bekup_db_ca.crt` | CA bundle (PEM). Only the certificates in this file are trusted, never the built-in system roots. For a managed Postgres name the provider's CA bundle (or, only if the provider's certificate comes from a public CA, the system bundle file such as `/etc/ssl/certs/ca-certificates.crt`; D-207). Required for `verify-full`. |
| `tls_server_name` | empty | `verify-full` only: verify the certificate against this name instead of the connection host, and send it as SNI. For a host given as an IP address while the certificate names a DNS name (D-207). |
| `tls_client_cert_file`, `tls_client_key_file` | empty | Optional client certificate and key (both or neither) for Postgres `clientcert` setups. The key file follows the secret-file rules. |

The policy applies to the runtime pool, the LISTEN connection, every
migrator connection and the admin connection of `bekup db bootstrap`; all of
them are built by one connection builder (D-207). Connections use TLS 1.2 or
newer and accept only SCRAM-SHA-256 authentication (`require_auth`): a server
that asks for a cleartext or MD5 password, or for none, is refused before
anything is sent. serve logs the TLS facts once at startup (`app database
connected`: mode, TLS version, chain and host verified, channel binding; no
host name), and `bekup migrate check` prints them.

### Fields mode (external Postgres)

In fields mode (`database.host`, `port`, `name`, `user`, `password_file`;
D-207) no connection string exists at all: bekup builds the connection from
the keys, and **every `PG*` environment variable is ignored** (`PGHOST`,
`PGSSLMODE`, `PGOPTIONS`, `PGSERVICE`, ...). A host that is an IP address
needs an IP SAN in the server certificate, or `tls_server_name` with a DNS
name the certificate carries. Unix sockets are not supported in fields mode.

### Weakened modes

`require` and `disable` are accepted but weaken the deployment: bekup logs a
warning at **every startup** (also in dev mode); outside dev mode every start
records `security.insecure_db_tls` (fail closed) and alerts every admin once
a day, and the admin configuration page (**Settings → Configuration → App
database**) shows the weakened mode (D-100, D-207). The shipped Postgres rejects
non-TLS logins of the bekup roles, so `disable` only works against an
external database configured that way.

### The connection URL

The URL in `database.url_file` / `owner_url_file` must be a
`postgres://` (or `postgresql://`) URL. The configured `tls_mode` always
wins:

- an `sslmode` that is weaker than `tls_mode` (for example the old
  `sslmode=disable`) is ignored, and `PGSSLMODE` etc. in the environment
  have no effect;
- an `sslmode` that is **stricter** than `tls_mode` (for example
  `sslmode=verify-full` with `tls_mode = "require"`) is an error, because
  the intent is ambiguous;
- `sslrootcert`, `sslcert`, `sslkey`, `sslpassword`, `sslcrl`, ... in the URL
  are rejected: use the `tls_*` keys;
- the host in the URL (`postgres` in compose) is the name that `verify-full`
  checks against the certificate's SANs;
- `channel_binding` may only be `require` (see below), `require_auth` only
  `scram-sha-256`;
- the query may carry **only** `sslmode`, `channel_binding`, `require_auth`,
  `connect_timeout`, `target_session_attrs` and `application_name` (an
  allowlist; besides `host=` naming a Unix-socket directory), each once.
  Everything else is rejected, in particular `options`, `search_path`,
  `role`, `session_authorization` and any other run-time parameter,
  `service`, `servicefile`, `passfile`, Kerberos, `pool_*` and pgx's
  statement-cache settings: bekup sets the session (`search_path=public`,
  timeouts, isolation level) and the pool itself;
- runtime parameters only come from the URL itself, never from `PGOPTIONS`,
  `PGAPPNAME`, `PGTZ` or a service file, and neither do pgx's own settings
  (statement cache, query exec mode) or `target_session_attrs` (other libpq defaults of keys absent from the URL,
  such as `PGPASSFILE` for a URL without a password, still apply in URL mode;
  fields mode ignores the environment entirely);
- a Unix-socket host cannot use TLS: TLS modes refuse it, use
  `tls_mode = "disable"` for socket connections.

### SCRAM channel binding (required)

Whenever TLS is on, bekup requires SCRAM channel binding
(`SCRAM-SHA-256-PLUS`, `tls-server-end-point`, RFC 5929; D-104): the
password exchange is bound to the server certificate the client actually
received. A man in the middle can then neither relay the login nor move the
session to another TLS channel, even with `tls_mode = "require"` and no CA.
A URL `channel_binding` other than `require` is an error.

This needs a server certificate whose signature has a defined hash, which is
why the postgres certificate is ECDSA (next section). An external Postgres
must use an RSA or ECDSA certificate (not Ed25519) and SCRAM authentication;
servers that authenticate bekup otherwise (`trust`, `cert`, `md5`) cannot
provide the binding and bekup refuses to connect. A connection pooler that
terminates TLS (PgBouncer, RDS Proxy, Supavisor, Azure's built-in PgBouncer)
breaks the binding as well: connect bekup directly to PostgreSQL (D-207).

Manual `psql` sessions work with libpq's defaults (`channel_binding=prefer`
picks the binding automatically); add `channel_binding=require` to enforce
it, e.g.:

```sh
psql "host=postgres dbname=bekup user=bekup_app sslmode=verify-full sslrootcert=ca.crt channel_binding=require"
```

## The postgres certificate

`bekup certs init` issues a `postgres` server component from the same
private CA as the runner mTLS certificates (see
[mTLS certificates](mtls-certs.md)):

```
<dir>/postgres/postgres.crt   chain: server certificate (DNS SAN "postgres" + --postgres-san)
                              followed by its one-time issuer
<dir>/postgres/postgres.key   the server certificate's ECDSA P-256 key (0600)
<dir>/postgres/ca.crt         CA bundle (the Ed25519 root); database.tls_ca_file of serve/migrate
```

**Why a chain.** The channel-binding hash comes from the leaf's signature
algorithm, i.e. from the key of whoever signed it. A leaf signed directly by
the Ed25519 root has an Ed25519 signature, for which RFC 5929 defines no
hash. So each time the postgres certificate is issued, bekup creates a
**one-time ECDSA P-256 issuer** (an intermediate CA signed by the root,
path length 0, serverAuth only, name-constrained to exactly the leaf's
names, valid exactly as long as the leaf), signs the ECDSA P-256 leaf with
it (ecdsa-with-SHA256) and **discards the issuer key** immediately. There is
no intermediate key to store or protect: "rotating the postgres issuer" is
simply `bekup certs rotate --component postgres` (or a CA promote), which
always creates a new one. Clients trust only the root; the rest of the PKI
stays Ed25519. The root CA has path length 1 to allow this issuer; the
runner mTLS configuration still rejects any chain with an intermediate.

```sh
bekup certs init --dir secrets/certs --component runner --component serve --component postgres
bekup certs rotate --dir secrets/certs --component postgres            # new key + cert, same SANs
bekup certs rotate --dir secrets/certs --component postgres --postgres-san db.internal
```

**Name constraints.** The CA is name-constrained to the server names it
may sign. New CAs always permit `runner` and `postgres` plus the extra
`--runner-san` / `--postgres-san` names given at `init`. A name outside the
constraints is refused when the certificate is issued, with a hint to stage
a CA rotation that includes it (`bekup certs rotate --ca stage
--postgres-san ...`, then `promote` and `finish`).

A CA created **before** the postgres component existed only permits the
runner names and has path length 0, so it can sign neither the postgres
names nor the issuer. Staging a CA rotation without `--runner-san` /
`--postgres-san` keeps the current names and **adds `postgres`**; after
`promote` issue the certificate with `bekup certs rotate --component
postgres` (see [upgrades.md](../installation/upgrades.md#notes-for-existing-installations)).

During a CA rotation `postgres/ca.crt` holds both CAs, exactly like the
other components' bundles, so serve/migrate keep verifying the database
throughout. Restart postgres, serve and migrate after each phase.

## Compose

- `postgres` starts through `deploy/postgres/entrypoint.sh`. PostgreSQL
  only accepts a server key owned by its own user (or root) with mode 0600,
  while Docker secrets keep the host file's owner and mode. The wrapper
  copies certificate chain, key and CA bundle from `/run/secrets` into a private
  tmpfs (`/var/lib/postgresql/tls`, 0700, UID 70), then starts the image's
  entrypoint with `ssl=on`, `ssl_min_protocol_version=TLSv1.2` and the
  `ssl_*_file` settings. Missing files stop the container: the stack never
  runs without TLS.
- `deploy/postgres/init/10-bekup-roles.sh` (new data volumes only) puts
  `hostnossl all bekup_owner,bekup_app all reject` near the top of
  `pg_hba.conf`: every non-TLS TCP login of the bekup roles is refused, and
  the remaining SCRAM-SHA-256 rule then only matches TLS connections. The
  bootstrap superuser stays socket-only.
- `serve` and `migrate` receive `postgres/ca.crt` as
  `/run/secrets/bekup_db_ca.crt` (the `tls_ca_file` default) and use the
  default `tls_mode = "verify-full"` against the host `postgres`.
- On Linux the postgres files must be readable by UID 70:
  `sudo chown -R 70:70 secrets/certs/postgres` (the CA bundle stays 0644,
  so serve/migrate can read it too).

## Verifying

```sh
docker compose logs migrate | grep -i tls      # no "app database TLS" warning
docker compose exec postgres sh -c \
  'PGPASSWORD="$(cat /run/secrets/postgres_password)" psql --host=/var/run/postgresql -U postgres \
   -c "SELECT usename, ssl, version FROM pg_stat_ssl JOIN pg_stat_activity USING (pid) WHERE usename LIKE '\''bekup_%'\''"'
```

Every `bekup_app` / `bekup_owner` session must show `ssl = t`.
