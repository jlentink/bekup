-- TOTP enrollment state, second-factor failure counter and recovery codes
-- (task 1.7, D-111). users.totp_secret_enc and users.totp_last_step exist
-- since 00004. bekup_app gets DML on recovery_codes through the default
-- privileges of 00001.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.users
    -- Pending (unconfirmed) TOTP secret, envelope-encrypted (internal/crypto,
    -- AAD users:<id>:totp_pending). Moved to totp_secret_enc (re-sealed under
    -- its own AAD) once a valid code confirms the enrollment.
    ADD COLUMN totp_pending_enc        bytea,
    ADD COLUMN totp_pending_expires_at timestamptz,
    ADD COLUMN totp_enabled_at         timestamptz,
    -- Consecutive failed second-factor checks (TOTP or recovery code) and
    -- the resulting backoff; separate from the password lockout.
    ADD COLUMN mfa_failed_count        integer NOT NULL DEFAULT 0,
    ADD COLUMN mfa_locked_until        timestamptz,
    ADD CONSTRAINT users_mfa_failed_count_nonneg CHECK (mfa_failed_count >= 0),
    ADD CONSTRAINT users_totp_pending_complete
        CHECK ((totp_pending_enc IS NULL) = (totp_pending_expires_at IS NULL)),
    ADD CONSTRAINT users_totp_enabled_complete
        CHECK ((totp_secret_enc IS NULL) = (totp_enabled_at IS NULL)),
    ADD CONSTRAINT users_totp_secret_size CHECK (octet_length(totp_secret_enc) <= 256),
    ADD CONSTRAINT users_totp_pending_size CHECK (octet_length(totp_pending_enc) <= 256);

-- One row per recovery code. Only a keyed hash is stored:
-- HMAC-SHA256 under a subkey of the master key key_id (internal/crypto
-- Keyring.MAC, purpose "recovery-code"), over the user id and the code.
CREATE TABLE public.recovery_codes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    key_id     text NOT NULL,
    code_mac   bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz,
    CONSTRAINT recovery_codes_key_id_format CHECK (key_id ~ '^[0-9a-f]{8}$'),
    CONSTRAINT recovery_codes_code_mac_length CHECK (octet_length(code_mac) = 32),
    CONSTRAINT recovery_codes_code_mac_key UNIQUE (user_id, code_mac)
);

CREATE INDEX recovery_codes_user_id_idx ON public.recovery_codes (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.recovery_codes;

ALTER TABLE public.users
    DROP CONSTRAINT users_totp_pending_size,
    DROP CONSTRAINT users_totp_secret_size,
    DROP CONSTRAINT users_totp_enabled_complete,
    DROP CONSTRAINT users_totp_pending_complete,
    DROP CONSTRAINT users_mfa_failed_count_nonneg,
    DROP COLUMN mfa_locked_until,
    DROP COLUMN mfa_failed_count,
    DROP COLUMN totp_enabled_at,
    DROP COLUMN totp_pending_expires_at,
    DROP COLUMN totp_pending_enc;
-- +goose StatementEnd
