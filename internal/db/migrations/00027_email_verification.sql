-- User e-mail verification (task 5.1, D-178). requestEmailVerification
-- stores one pending verification per user; verifyEmail redeems it once.
--
--   email            the address the link was sent to; the code is bound to
--                    it (and the MAC covers it), so a later address change
--                    voids the link.
--   code_mac         HMAC-SHA256 (Keyring.MAC, purpose email-verification)
--                    over the user id, the address and the 256-bit code.
--                    The code itself is only ever in the e-mail.
--   mac_key_id       the master key that computed code_mac (MACWith).
--   expires_at       24 hours after creation (at most 7 days in the column).
--   failed_attempts  wrong codes presented for this row; the row is deleted
--                    after 5.
--
-- An address change always re-requires verification: the users trigger
-- below clears email_verified_at whenever email changes, whichever code
-- path changes it.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.email_verifications (
    user_id         uuid PRIMARY KEY REFERENCES public.users (id) ON DELETE CASCADE,
    email           citext NOT NULL,
    code_mac        bytea NOT NULL,
    mac_key_id      bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    failed_attempts integer NOT NULL DEFAULT 0,
    CONSTRAINT email_verifications_mac_length CHECK (octet_length(code_mac) = 32),
    CONSTRAINT email_verifications_key_id_length CHECK (octet_length(mac_key_id) = 4),
    CONSTRAINT email_verifications_email_length CHECK (length(email) BETWEEN 3 AND 254),
    CONSTRAINT email_verifications_expiry CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '7 days'
    ),
    CONSTRAINT email_verifications_attempts CHECK (failed_attempts BETWEEN 0 AND 100)
);

CREATE INDEX email_verifications_expires_idx ON public.email_verifications (expires_at);

-- A changed address is never verified.
CREATE FUNCTION public.users_email_change_unverify() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.email IS DISTINCT FROM OLD.email THEN
        NEW.email_verified_at := NULL;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER users_email_change_unverify BEFORE UPDATE OF email ON public.users
    FOR EACH ROW EXECUTE FUNCTION public.users_email_change_unverify();

REVOKE ALL ON TABLE public.email_verifications FROM PUBLIC;
REVOKE TRUNCATE, REFERENCES, TRIGGER ON TABLE public.email_verifications FROM bekup_app;
REVOKE UPDATE ON TABLE public.email_verifications FROM bekup_app;
GRANT UPDATE (failed_attempts) ON TABLE public.email_verifications TO bekup_app;
REVOKE ALL ON FUNCTION public.users_email_change_unverify() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER users_email_change_unverify ON public.users;
DROP FUNCTION public.users_email_change_unverify();
DROP TABLE public.email_verifications;
-- +goose StatementEnd
