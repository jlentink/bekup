-- API tokens without expiry (D-229, amends D-220): a token either expires
-- 1-366 days after creation (as before) or, as an explicit choice, never.
--
--   api_tokens.never_expires  true exactly when expires_at is NULL
--                             (api_tokens_expiry). The flag makes "never"
--                             an explicit, immutable choice: a NULL
--                             expiry written by mistake is refused by the
--                             CHECK instead of silently meaning forever.
--
-- Nothing else changes: revocation, the client address rules, a disabled,
-- deleted or demoted creator and a lost job ownership still end a token at
-- once (public.api_token_valid / api_token_job_allowed). api.authenticate
-- already returns a nullable expires_at (NULL = never expires). The
-- function signatures stay the same, so the grants of
-- public.bekup_sync_external_grants() are unchanged.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.api_tokens
    ADD COLUMN never_expires boolean NOT NULL DEFAULT false,
    ALTER COLUMN expires_at DROP NOT NULL,
    DROP CONSTRAINT api_tokens_expiry,
    ADD CONSTRAINT api_tokens_expiry CHECK (
        never_expires = (expires_at IS NULL)
        AND (expires_at IS NULL OR (expires_at > created_at AND expires_at <= created_at + interval '366 days'))
    );

-- never_expires is as immutable as expires_at.
CREATE OR REPLACE FUNCTION public.api_tokens_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.name IS DISTINCT FROM OLD.name
       OR NEW.secret_hash IS DISTINCT FROM OLD.secret_hash OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
       OR NEW.never_expires IS DISTINCT FROM OLD.never_expires THEN
        RAISE EXCEPTION 'api_tokens: token % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND (NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
                                       OR NEW.revoked_by IS DISTINCT FROM OLD.revoked_by
                                       OR NEW.allowed_cidrs IS DISTINCT FROM OLD.allowed_cidrs
                                       OR NEW.allow_any_address IS DISTINCT FROM OLD.allow_any_address) THEN
        RAISE EXCEPTION 'api_tokens: token % is already revoked', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

-- api_token_valid: unrevoked, unexpired (or never expiring), and the
-- creator is an active admin or user.
CREATE OR REPLACE FUNCTION public.api_token_valid(p_token_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.api_tokens AS t
        JOIN public.users AS u ON u.id = t.created_by
        WHERE t.id = p_token_id
          AND t.revoked_at IS NULL
          AND (t.never_expires OR t.expires_at > pg_catalog.now())
          AND NOT u.disabled
          AND u.deleted_at IS NULL
          AND u.role IN ('admin', 'user')
    )
$$;

REVOKE ALL ON FUNCTION public.api_tokens_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.api_token_valid(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_token_valid(uuid) TO bekup_app;
SELECT public.bekup_sync_external_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Tokens without expiry cannot be expressed before this migration: they
-- are revoked (by their creator, if not revoked yet) and given an expiry
-- of their creation time plus one second, so the old CHECK holds and the
-- tokens stay invalid.
ALTER TABLE public.api_tokens DISABLE TRIGGER api_tokens_guard;
UPDATE public.api_tokens
SET revoked_at = coalesce(revoked_at, now()), revoked_by = coalesce(revoked_by, created_by),
    expires_at = created_at + interval '1 second', never_expires = false
WHERE never_expires;
ALTER TABLE public.api_tokens ENABLE TRIGGER api_tokens_guard;

CREATE OR REPLACE FUNCTION public.api_token_valid(p_token_id uuid) RETURNS boolean
    LANGUAGE sql STABLE
    SET search_path = pg_catalog, pg_temp
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.api_tokens AS t
        JOIN public.users AS u ON u.id = t.created_by
        WHERE t.id = p_token_id
          AND t.revoked_at IS NULL
          AND t.expires_at > pg_catalog.now()
          AND NOT u.disabled
          AND u.deleted_at IS NULL
          AND u.role IN ('admin', 'user')
    )
$$;

CREATE OR REPLACE FUNCTION public.api_tokens_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.name IS DISTINCT FROM OLD.name
       OR NEW.secret_hash IS DISTINCT FROM OLD.secret_hash OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'api_tokens: token % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND (NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
                                       OR NEW.revoked_by IS DISTINCT FROM OLD.revoked_by
                                       OR NEW.allowed_cidrs IS DISTINCT FROM OLD.allowed_cidrs
                                       OR NEW.allow_any_address IS DISTINCT FROM OLD.allow_any_address) THEN
        RAISE EXCEPTION 'api_tokens: token % is already revoked', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

ALTER TABLE public.api_tokens
    DROP CONSTRAINT api_tokens_expiry,
    ALTER COLUMN expires_at SET NOT NULL,
    DROP COLUMN never_expires,
    ADD CONSTRAINT api_tokens_expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '366 days');
REVOKE ALL ON FUNCTION public.api_tokens_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.api_token_valid(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.api_token_valid(uuid) TO bekup_app;
-- +goose StatementEnd
