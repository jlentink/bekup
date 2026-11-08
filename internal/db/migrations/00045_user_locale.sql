-- The UI language per account (D-236, task 8.2): the choice a user makes
-- in Profile → Account follows them to every browser and, from task 8.3,
-- decides the language of the e-mails and notification texts bekup sends
-- them.
--
--   users.locale  'en' or 'nl'; NULL follows the browser (the UI) and
--                 means English for e-mails (i18n.Default). Written only by
--                 auth.Service.SetLocale for the signed-in account.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.users
    ADD COLUMN locale text,
    ADD CONSTRAINT users_locale_known CHECK (locale IN ('en', 'nl'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP CONSTRAINT users_locale_known,
    DROP COLUMN locale;
-- +goose StatementEnd
