-- Instance signing keys and signed audit chain heads (task 1.3; D-087,
-- D-092, D-101). Also adds the indexes the audit log listing filters on.

-- +goose Up
-- +goose StatementBegin

-- instance_keys: ed25519 instance signing keys (D-038, D-087). The id is the
-- first 8 bytes of SHA-256(public_key) in lowercase hex; the 32-byte seed is
-- sealed with the master keyring under AAD instance_keys:<id>:private.
-- Public keys are kept forever (old signatures must stay verifiable), so
-- bekup_app may not DELETE rows and may only UPDATE retired_at.
CREATE TABLE public.instance_keys (
    id                 text PRIMARY KEY,
    public_key         bytea NOT NULL,
    sealed_private_key bytea NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    retired_at         timestamptz,
    CONSTRAINT instance_keys_id_format CHECK (id ~ '^[0-9a-f]{16}$'),
    CONSTRAINT instance_keys_id_matches_key CHECK (id = left(encode(sha256(public_key), 'hex'), 16)),
    CONSTRAINT instance_keys_public_key_length CHECK (octet_length(public_key) = 32),
    CONSTRAINT instance_keys_public_key_key UNIQUE (public_key),
    CONSTRAINT instance_keys_sealed_private_key_length CHECK (octet_length(sealed_private_key) BETWEEN 1 AND 1024),
    CONSTRAINT instance_keys_retired_after_created CHECK (retired_at IS NULL OR retired_at >= created_at)
);

-- At most one active (unretired) key: concurrent creators race on this index.
CREATE UNIQUE INDEX instance_keys_one_active_idx ON public.instance_keys ((true)) WHERE retired_at IS NULL;

REVOKE ALL ON TABLE public.instance_keys FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.instance_keys FROM bekup_app;
GRANT UPDATE (retired_at) ON TABLE public.instance_keys TO bekup_app;

-- audit_chain_heads: periodically signed (chain_id, chain_hash) pairs of the
-- audit_log chain (docs/security/audit.md). Append-only for bekup_app.
CREATE TABLE public.audit_chain_heads (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- audit_log.id of the signed row (no foreign key: audit_log is append-only).
    chain_id   bigint NOT NULL,
    chain_hash bytea NOT NULL,
    signed_at  timestamptz NOT NULL,
    key_id     text NOT NULL REFERENCES public.instance_keys (id),
    signature  bytea NOT NULL,
    CONSTRAINT audit_chain_heads_chain_id_positive CHECK (chain_id > 0),
    CONSTRAINT audit_chain_heads_chain_hash_length CHECK (octet_length(chain_hash) = 32),
    CONSTRAINT audit_chain_heads_signature_length CHECK (octet_length(signature) = 64)
);

CREATE INDEX audit_chain_heads_chain_id_idx ON public.audit_chain_heads (chain_id);

REVOKE ALL ON TABLE public.audit_chain_heads FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.audit_chain_heads FROM bekup_app;

-- Audit log listing filters (internal/audit List; GraphQL auditLog).
CREATE INDEX audit_log_action_id_idx ON public.audit_log (action, id);
CREATE INDEX audit_log_actor_user_id_id_idx ON public.audit_log (actor_user_id, id) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_log_target_id_idx ON public.audit_log (target_type, target_id, id) WHERE target_type IS NOT NULL;
CREATE INDEX audit_log_ts_idx ON public.audit_log (ts);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.audit_log_ts_idx;
DROP INDEX public.audit_log_target_id_idx;
DROP INDEX public.audit_log_actor_user_id_id_idx;
DROP INDEX public.audit_log_action_id_idx;
DROP TABLE public.audit_chain_heads;
DROP TABLE public.instance_keys;
-- +goose StatementEnd
