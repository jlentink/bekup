-- WebDAV destinations (D-222): the destination type `webdav`. Migration
-- 00039 adds the certificate pin columns, whose CHECK compares with the
-- new value; a new enum value cannot be used in the transaction that adds
-- it (goose runs each migration in its own transaction, so 00039 and the
-- code see the committed value).
--
--   destinations.type  disk | s3 | sftp | webdav

-- +goose Up
-- +goose StatementBegin
ALTER TYPE public.destination_type ADD VALUE 'webdav';
-- +goose StatementEnd

-- +goose Down
-- Dev and tests only (forward-only in production). Fails while a WebDAV
-- destination exists. PostgreSQL cannot drop an enum value, so the type is
-- recreated (like 00033 and 00036); the CHECK constraints of 00011 compare
-- with enum literals and are rebuilt around the type change.
-- +goose StatementBegin
ALTER TABLE public.destinations
    DROP CONSTRAINT destinations_host_key_sftp_only,
    DROP CONSTRAINT destinations_object_lock_s3_only;
ALTER TYPE public.destination_type RENAME TO destination_type_old;
CREATE TYPE public.destination_type AS ENUM ('disk', 's3', 'sftp');
ALTER TABLE public.destinations
    ALTER COLUMN type TYPE public.destination_type USING type::text::public.destination_type;
DROP TYPE public.destination_type_old;
ALTER TABLE public.destinations
    ADD CONSTRAINT destinations_object_lock_s3_only CHECK (object_lock_mode IS NULL OR type = 's3'),
    ADD CONSTRAINT destinations_host_key_sftp_only CHECK (host_key_type IS NULL OR type = 'sftp');
-- +goose StatementEnd
