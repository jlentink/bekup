-- Download tickets (task 3.9, D-150). createDownloadTicket stores one row
-- per ticket; GET /api/download/{ticket} redeems it once.
--
--   ticket_hash      SHA-256 of the 256-bit ticket. The ticket itself is
--                    only ever in the mutation's response and the URL.
--   session_id       the session that created the ticket; only a request
--                    of the same session may redeem it.
--   mode             raw (the stored artifact) or plain (decrypted and
--                    decompressed).
--   sealed_identity  plain downloads of recipient-encrypted artifacts: the
--                    age identity sent with the mutation, sealed with the
--                    keyring under AAD download_tickets:<id>:identity. It is
--                    cleared when the ticket is redeemed (or burned) and the
--                    row is deleted shortly after expiry.
--   expires_at       60 seconds after creation (at most 5 minutes in the
--                    column).
--   used_at          set once by the redemption; a used ticket never
--                    serves again.
--
-- bekup_app may insert, read and delete rows, and may only update
-- used_at (NULL -> a time) and sealed_identity (-> NULL).

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.download_mode AS ENUM ('raw', 'plain');

CREATE TABLE public.download_tickets (
    id              uuid PRIMARY KEY,
    ticket_hash     bytea NOT NULL,
    session_id      uuid NOT NULL REFERENCES public.sessions (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    artifact_id     uuid NOT NULL REFERENCES public.artifacts (id),
    mode            public.download_mode NOT NULL,
    sealed_identity bytea,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    CONSTRAINT download_tickets_hash_key UNIQUE (ticket_hash),
    CONSTRAINT download_tickets_hash_length CHECK (octet_length(ticket_hash) = 32),
    CONSTRAINT download_tickets_expiry CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '5 minutes'
    ),
    CONSTRAINT download_tickets_identity_plain CHECK (sealed_identity IS NULL OR mode = 'plain'),
    CONSTRAINT download_tickets_identity_length CHECK (
        sealed_identity IS NULL OR octet_length(sealed_identity) BETWEEN 1 AND 16384
    ),
    CONSTRAINT download_tickets_used_cleared CHECK (used_at IS NULL OR sealed_identity IS NULL)
);

CREATE INDEX download_tickets_user_idx ON public.download_tickets (user_id, expires_at);
CREATE INDEX download_tickets_expires_idx ON public.download_tickets (expires_at);
CREATE INDEX download_tickets_artifact_idx ON public.download_tickets (artifact_id);

-- A redeemed ticket stays redeemed and a cleared identity stays cleared.
CREATE FUNCTION public.download_tickets_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.used_at IS NOT NULL AND NEW.used_at IS DISTINCT FROM OLD.used_at THEN
        RAISE EXCEPTION 'download_tickets: ticket % is already used', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.sealed_identity IS NOT NULL AND NEW.sealed_identity IS DISTINCT FROM OLD.sealed_identity THEN
        RAISE EXCEPTION 'download_tickets: the identity of ticket % can only be cleared', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER download_tickets_guard BEFORE UPDATE ON public.download_tickets
    FOR EACH ROW EXECUTE FUNCTION public.download_tickets_guard();

REVOKE ALL ON TABLE public.download_tickets FROM PUBLIC;
REVOKE UPDATE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.download_tickets FROM bekup_app;
GRANT UPDATE (used_at, sealed_identity) ON TABLE public.download_tickets TO bekup_app;
REVOKE ALL ON FUNCTION public.download_tickets_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER download_tickets_guard ON public.download_tickets;
DROP FUNCTION public.download_tickets_guard();
DROP TABLE public.download_tickets;
DROP TYPE public.download_mode;
-- +goose StatementEnd
