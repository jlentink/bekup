-- M5 security review follow-ups (D-198):
--
-- audit_log_login_failed_ts_idx  a partial index for the failed-login
--     burst detection (CountRecentLoginFailures runs for every
--     auth.login.failed entry and counts the entries of the last 15
--     minutes; without it the query scans every audit row of the window).
--
-- netguard.allowlist  the admin SSRF allowlist gains scopes: every entry
--     names the outbound uses it widens (destinations, smtp,
--     shared_webhooks, personal_webhooks). Stored version-1 lists become
--     version 2 with every entry scoped to all uses except
--     personal_webhooks, so no existing entry widens what users' personal
--     webhooks may reach. updated_at is left alone (the change is not an
--     admin edit; it is recorded here). The service decodes version 1 the
--     same way, so a list stored by an older binary cannot widen
--     personal webhooks either.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX audit_log_login_failed_ts_idx ON public.audit_log (ts) WHERE action = 'auth.login.failed';

UPDATE public.settings
SET value = jsonb_build_object(
    'version', 2,
    'cidrs', COALESCE((
        SELECT jsonb_agg(jsonb_build_object('value', e.v, 'scopes', '["destinations","smtp","shared_webhooks"]'::jsonb) ORDER BY e.n)
        FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(value -> 'cidrs') = 'array' THEN value -> 'cidrs' ELSE '[]'::jsonb END)
            WITH ORDINALITY AS e (v, n)), '[]'::jsonb),
    'hosts', COALESCE((
        SELECT jsonb_agg(jsonb_build_object('value', e.v, 'scopes', '["destinations","smtp","shared_webhooks"]'::jsonb) ORDER BY e.n)
        FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(value -> 'hosts') = 'array' THEN value -> 'hosts' ELSE '[]'::jsonb END)
            WITH ORDINALITY AS e (v, n)), '[]'::jsonb))
WHERE key = 'netguard.allowlist' AND value ->> 'version' = '1';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The scoped list stays: an older binary cannot decode it and applies
-- only the bootstrap list (fail closed) until an admin saves a new one.
DROP INDEX public.audit_log_login_failed_ts_idx;
-- +goose StatementEnd
