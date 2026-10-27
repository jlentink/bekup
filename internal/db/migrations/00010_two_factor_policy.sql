-- Two-step login and the 2FA policy (task 1.9, D-112).
--
-- sessions.mfa_pending marks a session created by the password step of a
-- login for an account with a second factor: it is AAL1 without a step-up
-- and can do nothing but complete the second factor or log out until it
-- is replaced (rotated) by an AAL2 session. Its absolute expiry is short
-- (set by the application, 5 minutes).
--
-- users.mfa_reset_at records when an admin or the break-glass CLI removed
-- all second factors of the account: a new enrollment grace period of the
-- 2FA policy starts then.
--
-- The 2FA policy itself lives in settings (key security.two_factor_policy)
-- and needs no schema change.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.sessions
    ADD COLUMN mfa_pending boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT sessions_mfa_pending_aal1 CHECK (
        NOT mfa_pending OR (aal = 1 AND stepup_at IS NULL AND aal2_at IS NULL)
    );

ALTER TABLE public.users ADD COLUMN mfa_reset_at timestamptz;

-- Sessions created before the two-step login existed may be AAL1 sessions
-- of accounts that already have a second factor (the password alone signed
-- them in). End them: those users sign in again with their second factor.
UPDATE public.sessions s
SET revoked_at = now()
WHERE s.revoked_at IS NULL
  AND s.aal = 1
  AND EXISTS (
      SELECT 1 FROM public.users u
      WHERE u.id = s.user_id
        AND (u.totp_secret_enc IS NOT NULL
             OR EXISTS (SELECT 1 FROM public.webauthn_credentials w WHERE w.user_id = u.id))
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users DROP COLUMN mfa_reset_at;
DELETE FROM public.sessions WHERE mfa_pending;
ALTER TABLE public.sessions
    DROP CONSTRAINT sessions_mfa_pending_aal1,
    DROP COLUMN mfa_pending;
-- +goose StatementEnd
