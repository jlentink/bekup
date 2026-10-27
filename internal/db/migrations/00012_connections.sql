-- Database connections (task 2.11, D-135): the PostgreSQL, MySQL and
-- MariaDB servers bekup backs up, managed by admins through the GraphQL API
-- (internal/connections). serve never connects to them itself: every probe,
-- certificate capture and host-key capture goes through the runner.
--
-- engine / version_key / image_key are keys of the embedded image catalog
-- (internal/engine): version_key is the server's release series, image_key
-- the client image the runner uses (the runner re-checks it against its own
-- allowlist). There is no database column: the database selection is per
-- job (task 3.5).
--
-- TLS (D-034, D-133): tls_mode defaults to 'pinned'. The pin is the anchor
-- certificate (PEM; a self-signed leaf or the issuing root CA) plus the
-- expected leaf SPKI SHA-256 (NULL only for a CA anchor whose leaves may
-- rotate), confirmed by an admin with step-up; one staged backup pin
-- (planned rotation) can sit next to it. tls_pin_not_after is the earliest
-- expiry of what was confirmed (expiry warnings). A downgrade to
-- 'unverified' or 'disabled' is an audited opt-out (security.opt_out);
-- pins are kept so an upgrade back to 'pinned' needs no new capture.
--
-- SSH tunnel (D-035, D-122): the bastion's host-key pin is set only after an
-- admin confirmed the fingerprint out of band and is cleared when the
-- bastion's host or port change; the TLS pins are cleared when the target's
-- engine, host or port change.
--
-- Credentials (database password, SSH password / private key + passphrase,
-- TLS client key) live in secret_enc, envelope-encrypted with
-- internal/crypto under AAD connections:<id>:secret; they are write-only in
-- the API and never returned.
--
-- Temporary restore connections (task 4.4): temporary + expires_at +
-- created_for_restore_id are reserved here and unused until then.
-- Connections are soft deleted (deleted_at): jobs, runs and artifacts
-- (tasks 3.x) keep referencing the row. bekup_app gets DML through the
-- default privileges of 00001.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.connection_engine AS ENUM ('postgres', 'mysql', 'mariadb');
CREATE TYPE public.connection_tls_mode AS ENUM ('pinned', 'unverified', 'disabled');
CREATE TYPE public.connection_ssh_auth AS ENUM ('none', 'password', 'private_key');

CREATE TABLE public.connections (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                        text NOT NULL,
    engine                      public.connection_engine NOT NULL,
    version_key                 text NOT NULL,
    image_key                   text NOT NULL,
    host                        text NOT NULL,
    port                        integer NOT NULL,
    username                    text NOT NULL,
    -- Whether secret_enc holds a database password (the value is sealed).
    has_password                boolean NOT NULL DEFAULT false,
    -- Public half of an optional TLS client certificate (the key is sealed).
    client_cert_pem             text,
    tls_mode                    public.connection_tls_mode NOT NULL DEFAULT 'pinned',
    -- Current certificate pin.
    tls_pin_cert_pem            text,
    tls_pin_spki                bytea,
    tls_pin_not_after           timestamptz,
    tls_pinned_at               timestamptz,
    tls_pinned_by               uuid REFERENCES public.users (id) ON DELETE SET NULL,
    -- Staged backup pin (planned certificate rotation).
    tls_backup_pin_cert_pem     text,
    tls_backup_pin_spki         bytea,
    tls_backup_pin_not_after    timestamptz,
    tls_backup_pinned_at        timestamptz,
    tls_backup_pinned_by        uuid REFERENCES public.users (id) ON DELETE SET NULL,
    -- SSH tunnel.
    ssh_enabled                 boolean NOT NULL DEFAULT false,
    ssh_host                    text,
    ssh_port                    integer,
    ssh_user                    text,
    ssh_auth                    public.connection_ssh_auth NOT NULL DEFAULT 'none',
    ssh_has_passphrase          boolean NOT NULL DEFAULT false,
    -- authorized_keys line of the stored private key (public data).
    ssh_public_key              text,
    ssh_host_key_type           text,
    ssh_host_key_fingerprint    text,
    ssh_host_key_authorized_key text,
    ssh_host_key_confirmed_at   timestamptz,
    ssh_host_key_confirmed_by   uuid REFERENCES public.users (id) ON DELETE SET NULL,
    secret_enc                  bytea,
    CONSTRAINT connections_secret_flags CHECK (
        secret_enc IS NOT NULL OR (NOT has_password AND ssh_auth = 'none' AND client_cert_pem IS NULL)
    ),
    -- Temporary restore connections (task 4.4).
    temporary                   boolean NOT NULL DEFAULT false,
    expires_at                  timestamptz,
    created_for_restore_id      uuid,
    created_by                  uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    CONSTRAINT connections_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT connections_key_length CHECK (
        char_length(version_key) BETWEEN 1 AND 16 AND char_length(image_key) BETWEEN 1 AND 16
    ),
    CONSTRAINT connections_host_length CHECK (char_length(host) BETWEEN 1 AND 253),
    CONSTRAINT connections_port_range CHECK (port BETWEEN 1 AND 65535),
    CONSTRAINT connections_username_length CHECK (char_length(username) BETWEEN 1 AND 128),
    CONSTRAINT connections_client_cert_size CHECK (octet_length(client_cert_pem) <= 32768),
    CONSTRAINT connections_pin_complete CHECK (
        (tls_pin_cert_pem IS NULL) = (tls_pin_not_after IS NULL)
        AND (tls_pin_cert_pem IS NULL) = (tls_pinned_at IS NULL)
        AND (tls_pin_spki IS NULL OR tls_pin_cert_pem IS NOT NULL)
    ),
    CONSTRAINT connections_backup_pin_complete CHECK (
        (tls_backup_pin_cert_pem IS NULL) = (tls_backup_pin_not_after IS NULL)
        AND (tls_backup_pin_cert_pem IS NULL) = (tls_backup_pinned_at IS NULL)
        AND (tls_backup_pin_spki IS NULL OR tls_backup_pin_cert_pem IS NOT NULL)
    ),
    CONSTRAINT connections_pin_spki_length CHECK (
        (tls_pin_spki IS NULL OR octet_length(tls_pin_spki) = 32)
        AND (tls_backup_pin_spki IS NULL OR octet_length(tls_backup_pin_spki) = 32)
    ),
    CONSTRAINT connections_pin_size CHECK (
        octet_length(tls_pin_cert_pem) <= 32768 AND octet_length(tls_backup_pin_cert_pem) <= 32768
    ),
    CONSTRAINT connections_ssh_complete CHECK (
        NOT ssh_enabled OR (ssh_host IS NOT NULL AND ssh_port IS NOT NULL AND ssh_user IS NOT NULL)
    ),
    CONSTRAINT connections_ssh_port_range CHECK (ssh_port IS NULL OR ssh_port BETWEEN 1 AND 65535),
    CONSTRAINT connections_ssh_lengths CHECK (
        (ssh_host IS NULL OR char_length(ssh_host) BETWEEN 1 AND 253)
        AND (ssh_user IS NULL OR char_length(ssh_user) BETWEEN 1 AND 128)
        AND (ssh_public_key IS NULL OR octet_length(ssh_public_key) <= 16384)
        AND (ssh_host_key_authorized_key IS NULL OR octet_length(ssh_host_key_authorized_key) <= 8192)
    ),
    CONSTRAINT connections_host_key_complete CHECK (
        (ssh_host_key_type IS NULL) = (ssh_host_key_fingerprint IS NULL)
        AND (ssh_host_key_type IS NULL) = (ssh_host_key_authorized_key IS NULL)
        AND (ssh_host_key_type IS NULL) = (ssh_host_key_confirmed_at IS NULL)
    ),
    CONSTRAINT connections_host_key_fingerprint_format CHECK (
        ssh_host_key_fingerprint IS NULL OR ssh_host_key_fingerprint ~ '^SHA256:[A-Za-z0-9+/]{43}$'
    ),
    CONSTRAINT connections_secret_size CHECK (octet_length(secret_enc) <= 131072),
    CONSTRAINT connections_temporary_expiry CHECK (temporary = (expires_at IS NOT NULL)),
    CONSTRAINT connections_restore_ref_temporary CHECK (created_for_restore_id IS NULL OR temporary)
);

-- Names are unique (case-insensitively) among connections that are not
-- deleted and not temporary; a deleted connection's name can be reused, and
-- a temporary restore connection (task 4.4) never blocks a name.
CREATE UNIQUE INDEX connections_name_active_key ON public.connections (lower(name))
    WHERE deleted_at IS NULL AND NOT temporary;

-- The cleanup worker of task 4.4 finds expired temporary connections.
CREATE INDEX connections_temporary_expiry_idx ON public.connections (expires_at)
    WHERE temporary AND deleted_at IS NULL;

CREATE TRIGGER connections_set_updated_at BEFORE UPDATE ON public.connections
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.connections;
DROP TYPE public.connection_ssh_auth;
DROP TYPE public.connection_tls_mode;
DROP TYPE public.connection_engine;
-- +goose StatementEnd
