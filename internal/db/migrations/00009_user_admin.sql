-- User administration (task 1.6, D-110).
--
-- users: soft delete (audit rows reference user ids, so rows are kept) and
-- temporary passwords set by an admin (must be changed at the next login,
-- expire after a fixed time). The e-mail address stays unique among
-- accounts that are not deleted, so a deleted account's address can be
-- reused.
--
-- (The web first-run setup table of task 1.6 was folded out before M1 was
-- released: the first admin is created with `bekup admin create`, D-113.)

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.users
    ADD COLUMN deleted_at timestamptz,
    -- Non-NULL: the current password is a temporary one set by an admin; it
    -- must be changed after login and cannot be used after this time.
    ADD COLUMN password_temporary_until timestamptz,
    ADD CONSTRAINT users_deleted_disabled CHECK (deleted_at IS NULL OR disabled),
    ADD CONSTRAINT users_temporary_needs_password CHECK (password_temporary_until IS NULL OR password_hash IS NOT NULL);

ALTER TABLE public.users DROP CONSTRAINT users_email_key;
CREATE UNIQUE INDEX users_email_active_key ON public.users (email) WHERE deleted_at IS NULL;
CREATE INDEX users_active_admins_idx ON public.users (id)
    WHERE role = 'admin' AND NOT disabled AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX public.users_active_admins_idx;
DROP INDEX public.users_email_active_key;
ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE public.users
    DROP CONSTRAINT users_temporary_needs_password,
    DROP CONSTRAINT users_deleted_disabled,
    DROP COLUMN password_temporary_until,
    DROP COLUMN deleted_at;
-- +goose StatementEnd
