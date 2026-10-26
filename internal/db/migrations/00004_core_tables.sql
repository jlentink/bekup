-- Core tables: users, sessions, settings and the hash-chained audit_log
-- (task 0.4; D-054, D-092). Feature tasks add their own tables in later
-- migrations. bekup_app gets DML through the default privileges of 00001;
-- audit_log is restricted to INSERT + SELECT below.

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;

-- Maintains updated_at on UPDATE.
CREATE FUNCTION public.set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;

CREATE TYPE public.user_role AS ENUM ('admin', 'maintainer', 'user');

CREATE TABLE public.users (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email              citext NOT NULL,
    display_name       text NOT NULL DEFAULT '',
    -- NULL: no password set yet (the account cannot log in with a password).
    password_hash      text,
    role               public.user_role NOT NULL DEFAULT 'user',
    disabled           boolean NOT NULL DEFAULT false,
    locked_until       timestamptz,
    failed_login_count integer NOT NULL DEFAULT 0,
    -- Envelope-encrypted (internal/crypto, AAD users:<id>:totp_secret).
    totp_secret_enc    bytea,
    -- Last accepted TOTP time step (replay protection).
    totp_last_step     bigint,
    email_verified_at  timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_email_length CHECK (char_length(email::text) BETWEEN 3 AND 254),
    CONSTRAINT users_display_name_length CHECK (char_length(display_name) <= 200),
    CONSTRAINT users_password_hash_length CHECK (char_length(password_hash) <= 512),
    CONSTRAINT users_failed_login_count_nonneg CHECK (failed_login_count >= 0),
    CONSTRAINT users_totp_last_step_nonneg CHECK (totp_last_step >= 0)
);

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON public.users
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE public.sessions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- sha256 of the session token; the token itself is never stored.
    token_hash          bytea NOT NULL,
    user_id             uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    -- Authenticator assurance level: 1 = password, 2 = second factor.
    aal                 smallint NOT NULL DEFAULT 1,
    stepup_at           timestamptz,
    aal2_at             timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    idle_expires_at     timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    ip                  inet,
    user_agent          text,
    revoked_at          timestamptz,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_aal_valid CHECK (aal IN (1, 2)),
    CONSTRAINT sessions_user_agent_length CHECK (char_length(user_agent) <= 512),
    CONSTRAINT sessions_expiry_order CHECK (idle_expires_at <= absolute_expires_at)
);

CREATE INDEX sessions_user_id_idx ON public.sessions (user_id);
CREATE INDEX sessions_absolute_expires_at_idx ON public.sessions (absolute_expires_at);

CREATE TABLE public.settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL DEFAULT 'null'::jsonb,
    -- Envelope-encrypted secret part (internal/crypto, AAD settings:<key>:secret).
    secret_enc bytea,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    CONSTRAINT settings_key_format CHECK (key ~ '^[a-z][a-z0-9_.]{0,127}$')
);

CREATE TRIGGER settings_set_updated_at BEFORE UPDATE ON public.settings
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- ---------------------------------------------------------------------------
-- audit_log: append-only, hash-chained (D-054). See docs/security/audit.md for
-- the canonical serialization; internal/db/auditchain recomputes it in Go.
-- ---------------------------------------------------------------------------

-- id is assigned by the chain trigger under the chain lock (not by a column
-- default), so id order is chain order even with concurrent inserts.
CREATE SEQUENCE public.audit_log_id_seq AS bigint;

CREATE TABLE public.audit_log (
    id            bigint PRIMARY KEY,
    ts            timestamptz NOT NULL DEFAULT now(),
    -- No foreign key: audit rows outlive users, and SET NULL would be an UPDATE.
    actor_user_id uuid,
    actor_kind    text NOT NULL,
    action        text NOT NULL,
    target_type   text,
    target_id     text,
    ip            inet,
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    prev_hash     bytea NOT NULL,
    hash          bytea NOT NULL,
    CONSTRAINT audit_log_actor_kind_valid CHECK (actor_kind IN ('user', 'system', 'cli')),
    CONSTRAINT audit_log_action_length CHECK (char_length(action) BETWEEN 1 AND 128),
    CONSTRAINT audit_log_target_type_length CHECK (char_length(target_type) <= 128),
    CONSTRAINT audit_log_target_id_length CHECK (char_length(target_id) <= 256),
    CONSTRAINT audit_log_ip_host CHECK (masklen(ip) = CASE family(ip) WHEN 4 THEN 32 ELSE 128 END),
    CONSTRAINT audit_log_details_size CHECK (octet_length(details::text) <= 65536),
    CONSTRAINT audit_log_prev_hash_length CHECK (octet_length(prev_hash) = 32),
    CONSTRAINT audit_log_hash_length CHECK (octet_length(hash) = 32),
    -- A fork (two rows with the same predecessor) is impossible.
    CONSTRAINT audit_log_prev_hash_key UNIQUE (prev_hash),
    CONSTRAINT audit_log_hash_key UNIQUE (hash)
);

ALTER SEQUENCE public.audit_log_id_seq OWNED BY public.audit_log.id;

-- Canonical serialization v1 of an audit row (docs/security/audit.md):
--   "bekup-audit-v1" || F(prev_hash) || F(id) || F(ts) || F(actor_user_id)
--   || F(actor_kind) || F(action) || F(target_type) || F(target_id) || F(ip)
--   || F(details)
-- where F(NULL) = 0x00 and F(v) = 0x01 || uint32_be(octet_length(v)) || v,
-- and the values are: prev_hash raw bytes; id decimal; ts as decimal
-- microseconds since the Unix epoch; actor_user_id lowercase hyphenated uuid;
-- ip as host(ip); details as details::text; all text UTF-8.
-- F(v): a NULL marker or a length-prefixed value.
CREATE FUNCTION public.audit_log_field(v bytea) RETURNS bytea
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT CASE
        WHEN v IS NULL THEN decode('00', 'hex')
        ELSE decode('01', 'hex') || int4send(octet_length(v)) || v
    END
$$;

CREATE FUNCTION public.audit_log_canonical(
    p_id bigint, p_ts timestamptz, p_actor_user_id uuid, p_actor_kind text,
    p_action text, p_target_type text, p_target_id text, p_ip inet,
    p_details jsonb, p_prev_hash bytea
) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT convert_to('bekup-audit-v1', 'UTF8')
        || public.audit_log_field(p_prev_hash)
        || public.audit_log_field(convert_to(p_id::text, 'UTF8'))
        || public.audit_log_field(convert_to(((extract(epoch FROM p_ts) * 1000000)::bigint)::text, 'UTF8'))
        || public.audit_log_field(convert_to(p_actor_user_id::text, 'UTF8'))
        || public.audit_log_field(convert_to(p_actor_kind, 'UTF8'))
        || public.audit_log_field(convert_to(p_action, 'UTF8'))
        || public.audit_log_field(convert_to(p_target_type, 'UTF8'))
        || public.audit_log_field(convert_to(p_target_id, 'UTF8'))
        || public.audit_log_field(convert_to(host(p_ip), 'UTF8'))
        || public.audit_log_field(convert_to(p_details::text, 'UTF8'))
$$;

-- BEFORE INSERT: serialize writers with a transaction-level advisory lock,
-- then assign id, ts, prev_hash and hash. Client-supplied values for these
-- columns are ignored. SECURITY DEFINER (owner bekup_owner) so bekup_app needs
-- no access to the sequence; search_path is pinned and every object is
-- schema-qualified.
CREATE FUNCTION public.audit_log_chain() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    prev bytea;
BEGIN
    -- Under REPEATABLE READ/SERIALIZABLE the snapshot could miss the row
    -- committed by the previous lock holder and fork the chain.
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'audit_log inserts require READ COMMITTED isolation'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    -- Chain lock: 'bekupAUD' as a big-endian int64.
    PERFORM pg_advisory_xact_lock(7090191340503389508);
    SELECT a.hash INTO prev FROM public.audit_log AS a ORDER BY a.id DESC LIMIT 1;
    NEW.id := nextval('public.audit_log_id_seq');
    NEW.ts := clock_timestamp();
    NEW.details := coalesce(NEW.details, '{}'::jsonb);
    NEW.prev_hash := coalesce(prev, decode(repeat('00', 32), 'hex'));
    NEW.hash := sha256(public.audit_log_canonical(
        NEW.id, NEW.ts, NEW.actor_user_id, NEW.actor_kind, NEW.action,
        NEW.target_type, NEW.target_id, NEW.ip, NEW.details, NEW.prev_hash));
    RETURN NEW;
END
$$;

CREATE FUNCTION public.audit_log_append_only() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is not allowed', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END
$$;

CREATE TRIGGER audit_log_chain BEFORE INSERT ON public.audit_log
    FOR EACH ROW EXECUTE FUNCTION public.audit_log_chain();
CREATE TRIGGER audit_log_no_update_delete BEFORE UPDATE OR DELETE ON public.audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION public.audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON public.audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION public.audit_log_append_only();

-- bekup_app: INSERT + SELECT only (default privileges granted DML).
REVOKE ALL ON TABLE public.audit_log FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.audit_log FROM bekup_app;
REVOKE ALL ON SEQUENCE public.audit_log_id_seq FROM PUBLIC, bekup_app;
-- Trigger functions are not called directly by anyone.
REVOKE ALL ON FUNCTION public.audit_log_chain() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.audit_log_append_only() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.set_updated_at() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.audit_log;
DROP FUNCTION public.audit_log_append_only();
DROP FUNCTION public.audit_log_chain();
DROP FUNCTION public.audit_log_canonical(bigint, timestamptz, uuid, text, text, text, text, inet, jsonb, bytea);
DROP FUNCTION public.audit_log_field(bytea);
DROP TABLE public.settings;
DROP TABLE public.sessions;
DROP TABLE public.users;
DROP TYPE public.user_role;
DROP FUNCTION public.set_updated_at();
DROP EXTENSION citext;
-- +goose StatementEnd
