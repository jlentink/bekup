-- Backup destinations (task 2.12, D-131): reusable disk, S3 and SFTP
-- targets managed by admins through the GraphQL API (internal/destinations).
--
-- config holds the non-secret, per-type settings only (validated by the
-- application). Credentials (S3 secret access key, SFTP password, private
-- key and its passphrase) live in secret_enc, envelope-encrypted with
-- internal/crypto under AAD destinations:<id>:secret; they are write-only in
-- the API and never returned.
--
-- The SFTP host-key pin (host_key_*) is set only through
-- confirmDestinationHostKey after an out-of-band check of the fingerprint
-- and is cleared whenever host or port change. Destinations are soft
-- deleted (deleted_at): artifacts and jobs (tasks 3.x) keep referencing the
-- row. bekup_app gets DML through the default privileges of 00001.

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.destination_type AS ENUM ('disk', 's3', 'sftp');

CREATE TABLE public.destinations (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                       text NOT NULL,
    type                       public.destination_type NOT NULL,
    config                     jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_enc                 bytea,
    -- Never delete (retention is left to lifecycle rules / the server).
    no_delete                  boolean NOT NULL DEFAULT false,
    -- S3 default Object Lock retention applied to every upload.
    object_lock_mode           text,
    object_lock_retention_days integer,
    -- SFTP host-key pin (public data).
    host_key_type              text,
    host_key_fingerprint       text,
    host_key_authorized_key    text,
    host_key_confirmed_at      timestamptz,
    host_key_confirmed_by      uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_by                 uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    deleted_at                 timestamptz,
    CONSTRAINT destinations_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT destinations_config_object CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT destinations_config_size CHECK (octet_length(config::text) <= 524288),
    CONSTRAINT destinations_secret_size CHECK (octet_length(secret_enc) <= 131072),
    CONSTRAINT destinations_object_lock_mode CHECK (
        object_lock_mode IS NULL OR object_lock_mode IN ('governance', 'compliance')
    ),
    CONSTRAINT destinations_object_lock_complete CHECK (
        (object_lock_mode IS NULL) = (object_lock_retention_days IS NULL)
    ),
    CONSTRAINT destinations_object_lock_days CHECK (
        object_lock_retention_days IS NULL OR object_lock_retention_days BETWEEN 1 AND 36500
    ),
    CONSTRAINT destinations_object_lock_s3_only CHECK (object_lock_mode IS NULL OR type = 's3'),
    CONSTRAINT destinations_host_key_complete CHECK (
        (host_key_type IS NULL) = (host_key_fingerprint IS NULL)
        AND (host_key_type IS NULL) = (host_key_authorized_key IS NULL)
        AND (host_key_type IS NULL) = (host_key_confirmed_at IS NULL)
    ),
    CONSTRAINT destinations_host_key_sftp_only CHECK (host_key_type IS NULL OR type = 'sftp'),
    CONSTRAINT destinations_host_key_fingerprint_format CHECK (
        host_key_fingerprint IS NULL OR host_key_fingerprint ~ '^SHA256:[A-Za-z0-9+/]{43}$'
    )
);

-- Names are unique (case-insensitively) among destinations that are not
-- deleted; a deleted destination's name can be reused.
CREATE UNIQUE INDEX destinations_name_active_key ON public.destinations (lower(name))
    WHERE deleted_at IS NULL;

CREATE TRIGGER destinations_set_updated_at BEFORE UPDATE ON public.destinations
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.destinations;
DROP TYPE public.destination_type;
-- +goose StatementEnd
