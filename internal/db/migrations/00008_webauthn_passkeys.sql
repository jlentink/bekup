-- WebAuthn passkeys as a second factor (task 1.8, D-109): registered
-- passkeys and the pending ceremony state (challenges) bound to a session.
-- bekup_app gets DML on both tables through the default privileges of 00001.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.webauthn_credentials (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    -- The authenticator's credential id (rawId); unique across all users.
    credential_id         bytea NOT NULL,
    -- COSE_Key public key as returned at registration.
    public_key            bytea NOT NULL,
    attestation_type      text NOT NULL DEFAULT '',
    attestation_format    text NOT NULL DEFAULT '',
    aaguid                uuid,
    -- Last accepted signature counter (uint32); 0 for authenticators
    -- without a counter.
    sign_count            bigint NOT NULL DEFAULT 0,
    transports            text[] NOT NULL DEFAULT '{}',
    attachment            text NOT NULL DEFAULT '',
    user_present          boolean NOT NULL DEFAULT false,
    user_verified         boolean NOT NULL DEFAULT false,
    backup_eligible       boolean NOT NULL DEFAULT false,
    backup_state          boolean NOT NULL DEFAULT false,
    name                  text NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    last_used_at          timestamptz,
    -- Last time an assertion with a regressed signature counter was refused
    -- (possible cloned authenticator).
    sign_count_anomaly_at timestamptz,
    CONSTRAINT webauthn_credentials_credential_id_key UNIQUE (credential_id),
    CONSTRAINT webauthn_credentials_credential_id_length CHECK (octet_length(credential_id) BETWEEN 16 AND 1023),
    CONSTRAINT webauthn_credentials_public_key_length CHECK (octet_length(public_key) BETWEEN 1 AND 4096),
    CONSTRAINT webauthn_credentials_attestation_type_length CHECK (char_length(attestation_type) <= 64),
    CONSTRAINT webauthn_credentials_attestation_format_length CHECK (char_length(attestation_format) <= 64),
    CONSTRAINT webauthn_credentials_sign_count_range CHECK (sign_count BETWEEN 0 AND 4294967295),
    CONSTRAINT webauthn_credentials_transports_count CHECK (cardinality(transports) <= 16),
    CONSTRAINT webauthn_credentials_attachment_length CHECK (char_length(attachment) <= 32),
    CONSTRAINT webauthn_credentials_name_length CHECK (char_length(name) BETWEEN 1 AND 64),
    CONSTRAINT webauthn_credentials_backup_flags CHECK (backup_eligible OR NOT backup_state)
);

CREATE INDEX webauthn_credentials_user_id_idx ON public.webauthn_credentials (user_id);

-- Pending registration/assertion ceremonies. The challenge and the other
-- ceremony parameters stay server-side (never trusted from the client); a
-- row is bound to one session, consumed (deleted) by the finish step and
-- valid until expires_at. At most one pending ceremony per session and kind.
CREATE TABLE public.webauthn_ceremonies (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES public.sessions (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    -- go-webauthn SessionData as JSON (challenge, RP ID, user handle,
    -- allowed credentials, user verification, expiry).
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CONSTRAINT webauthn_ceremonies_kind_valid CHECK (kind IN ('registration', 'assertion')),
    CONSTRAINT webauthn_ceremonies_data_size CHECK (octet_length(data::text) <= 65536),
    CONSTRAINT webauthn_ceremonies_session_kind_key UNIQUE (session_id, kind)
);

CREATE INDEX webauthn_ceremonies_expires_at_idx ON public.webauthn_ceremonies (expires_at);
CREATE INDEX webauthn_ceremonies_user_id_idx ON public.webauthn_ceremonies (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.webauthn_ceremonies;
DROP TABLE public.webauthn_credentials;
-- +goose StatementEnd
