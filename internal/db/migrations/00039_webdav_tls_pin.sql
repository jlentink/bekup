-- WebDAV certificate pin (D-222): an admin may pin the server certificate
-- a WebDAV destination presents (self-signed NAS certificates, private
-- CAs). The certificate is then the only trust anchor (no system roots,
-- no CA bundle) and the leaf's SPKI must match; host name and validity
-- are still checked. Set only through pinWebdavCertificate (admin +
-- step-up, after the certificate was captured again and compared with the
-- fingerprint the admin confirmed), cleared by clearWebdavCertificatePin
-- and whenever the URL changes. Public data; WebDAV rows only.
--
--   tls_pin_sha256       SHA-256 of the pinned certificate's
--                        SubjectPublicKeyInfo, lowercase hex.
--   tls_pin_certificate  the pinned certificate (one PEM block).
--   tls_pin_set_at/_by   when and by whom it was pinned.
--
-- Pinning bumps updated_at (the normal trigger), so an earlier test result
-- becomes stale.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.destinations
    ADD COLUMN tls_pin_sha256 text,
    ADD COLUMN tls_pin_certificate text,
    ADD COLUMN tls_pin_set_at timestamptz,
    ADD COLUMN tls_pin_set_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    ADD CONSTRAINT destinations_tls_pin_complete CHECK (
        (tls_pin_sha256 IS NULL) = (tls_pin_certificate IS NULL)
        AND (tls_pin_sha256 IS NULL) = (tls_pin_set_at IS NULL)
    ),
    ADD CONSTRAINT destinations_tls_pin_webdav_only CHECK (tls_pin_sha256 IS NULL OR type = 'webdav'),
    ADD CONSTRAINT destinations_tls_pin_format CHECK (
        tls_pin_sha256 IS NULL OR tls_pin_sha256 ~ '^[0-9a-f]{64}$'
    ),
    ADD CONSTRAINT destinations_tls_pin_certificate_size CHECK (
        tls_pin_certificate IS NULL
        OR (octet_length(tls_pin_certificate) <= 16384 AND tls_pin_certificate LIKE '-----BEGIN CERTIFICATE-----%')
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.destinations
    DROP CONSTRAINT destinations_tls_pin_certificate_size,
    DROP CONSTRAINT destinations_tls_pin_format,
    DROP CONSTRAINT destinations_tls_pin_webdav_only,
    DROP CONSTRAINT destinations_tls_pin_complete,
    DROP COLUMN tls_pin_set_by,
    DROP COLUMN tls_pin_set_at,
    DROP COLUMN tls_pin_certificate,
    DROP COLUMN tls_pin_sha256;
-- +goose StatementEnd
