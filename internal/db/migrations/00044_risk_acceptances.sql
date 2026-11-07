-- Accepted security risks (D-228): an admin (or, for a job they own, a
-- backup owner) accepts a weakened condition for one month, one year or
-- forever. While an acceptance covers the current condition, bekup stops
-- flagging it as an open issue (dashboard, daily reminders, startup alert);
-- the audit entries, the startup warning and the badge (now "accepted")
-- stay. Written only by internal/riskaccept.
--
--   kind            what is accepted (risk_kind): the instance posture
--                   (insecure_transport, insecure_db_tls,
--                   insecure_db_roles, unpinned_image), a connection's
--                   TLS mode (connection_tls) or privileged role
--                   (privileged_role), the SMTP transport
--                   (smtp_unencrypted) or a job's encryption opt-out
--                   (encryption_opt_out).
--   connection_id,
--   job_id          the subject: connection kinds name the connection,
--                   encryption_opt_out the job, instance kinds and SMTP
--                   neither (risk_acceptances_subject).
--   condition_hash  SHA-256 of the canonical condition built by
--                   riskaccept (the facts that, when they change, make it
--                   a different risk: TLS mode and endpoint, privilege
--                   verdict and role, ...). A changed condition is not
--                   covered: the risk is flagged again.
--   condition       a short readable form of the condition (display only).
--   duration        month | year | forever; expires_at follows on the
--                   database clock (NULL = forever; risk_acceptances_expiry).
--   reason          why the risk is accepted (1-500 characters).
--   warned_at       when the "expires in 7 days" alert went out.
--   ended_at, ended_by, end_reason
--                   the end of an acceptance: revoked (ended_by = who),
--                   superseded (a new acceptance replaced it), expired,
--                   condition_changed, resolved (the condition is gone),
--                   subject_deleted, acceptor_lost_rights. Final.
--
-- One live acceptance per kind and subject (risk_acceptances_live). A new
-- row starts now, unwarned and live (the insert guard); only the end
-- columns and warned_at change, ending is final, rows are never deleted
-- (the guard trigger; bekup_app has no DELETE).

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.risk_kind AS ENUM (
    'insecure_transport', 'insecure_db_tls', 'insecure_db_roles', 'unpinned_image',
    'connection_tls', 'privileged_role', 'smtp_unencrypted', 'encryption_opt_out'
);
CREATE TYPE public.risk_duration AS ENUM ('month', 'year', 'forever');
CREATE TYPE public.risk_end_reason AS ENUM (
    'revoked', 'superseded', 'expired', 'condition_changed', 'resolved', 'subject_deleted', 'acceptor_lost_rights'
);

CREATE TABLE public.risk_acceptances (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind           public.risk_kind NOT NULL,
    connection_id  uuid REFERENCES public.connections (id),
    job_id         uuid REFERENCES public.jobs (id),
    condition_hash bytea NOT NULL,
    condition      text NOT NULL,
    duration       public.risk_duration NOT NULL,
    reason         text NOT NULL,
    created_by     uuid NOT NULL REFERENCES public.users (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz,
    warned_at      timestamptz,
    ended_at       timestamptz,
    ended_by       uuid REFERENCES public.users (id),
    end_reason     public.risk_end_reason,
    CONSTRAINT risk_acceptances_hash_length CHECK (octet_length(condition_hash) = 32),
    CONSTRAINT risk_acceptances_condition_length CHECK (char_length(condition) BETWEEN 1 AND 300),
    CONSTRAINT risk_acceptances_reason_length CHECK (char_length(reason) BETWEEN 1 AND 500),
    CONSTRAINT risk_acceptances_subject CHECK (CASE
        WHEN kind IN ('connection_tls', 'privileged_role') THEN connection_id IS NOT NULL AND job_id IS NULL
        WHEN kind = 'encryption_opt_out' THEN job_id IS NOT NULL AND connection_id IS NULL
        ELSE connection_id IS NULL AND job_id IS NULL
    END),
    CONSTRAINT risk_acceptances_expiry CHECK (
        (duration = 'forever' AND expires_at IS NULL)
        OR (duration = 'month' AND expires_at = created_at + interval '1 month')
        OR (duration = 'year' AND expires_at = created_at + interval '1 year')
    ),
    CONSTRAINT risk_acceptances_warned CHECK (warned_at IS NULL OR expires_at IS NOT NULL),
    CONSTRAINT risk_acceptances_end CHECK (
        (ended_at IS NULL) = (end_reason IS NULL)
        AND (ended_by IS NULL OR ended_at IS NOT NULL)
        AND (end_reason IS DISTINCT FROM 'revoked' OR ended_by IS NOT NULL)
    )
);

CREATE UNIQUE INDEX risk_acceptances_live ON public.risk_acceptances (
    kind,
    coalesce(connection_id, '00000000-0000-0000-0000-000000000000'::uuid),
    coalesce(job_id, '00000000-0000-0000-0000-000000000000'::uuid)
) WHERE ended_at IS NULL;
CREATE INDEX risk_acceptances_job_idx ON public.risk_acceptances (job_id) WHERE job_id IS NOT NULL;
CREATE INDEX risk_acceptances_connection_idx ON public.risk_acceptances (connection_id) WHERE connection_id IS NOT NULL;
CREATE INDEX risk_acceptances_created_idx ON public.risk_acceptances (created_at DESC, id DESC);

-- Only warned_at (once) and the end (once, all three columns) change.
CREATE FUNCTION public.risk_acceptances_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.kind IS DISTINCT FROM OLD.kind
       OR NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.job_id IS DISTINCT FROM OLD.job_id
       OR NEW.condition_hash IS DISTINCT FROM OLD.condition_hash OR NEW.condition IS DISTINCT FROM OLD.condition
       OR NEW.duration IS DISTINCT FROM OLD.duration OR NEW.reason IS DISTINCT FROM OLD.reason
       OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'risk_acceptances: acceptance % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.warned_at IS NOT NULL AND NEW.warned_at IS DISTINCT FROM OLD.warned_at THEN
        RAISE EXCEPTION 'risk_acceptances: acceptance % was already warned about', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.ended_at IS NOT NULL AND (NEW.ended_at IS DISTINCT FROM OLD.ended_at
                                     OR NEW.ended_by IS DISTINCT FROM OLD.ended_by
                                     OR NEW.end_reason IS DISTINCT FROM OLD.end_reason
                                     OR NEW.warned_at IS DISTINCT FROM OLD.warned_at) THEN
        RAISE EXCEPTION 'risk_acceptances: acceptance % already ended', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER risk_acceptances_guard BEFORE UPDATE ON public.risk_acceptances
    FOR EACH ROW EXECUTE FUNCTION public.risk_acceptances_guard();

-- A new acceptance starts now (the database clock), neither warned nor
-- ended: no backdated or pre-ended rows (expires_at must then match the
-- duration from now, risk_acceptances_expiry).
CREATE FUNCTION public.risk_acceptances_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    NEW.created_at := pg_catalog.now();
    NEW.warned_at := NULL;
    NEW.ended_at := NULL;
    NEW.ended_by := NULL;
    NEW.end_reason := NULL;
    RETURN NEW;
END
$$;

CREATE TRIGGER risk_acceptances_insert_guard BEFORE INSERT ON public.risk_acceptances
    FOR EACH ROW EXECUTE FUNCTION public.risk_acceptances_insert_guard();

REVOKE ALL ON TABLE public.risk_acceptances FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.risk_acceptances FROM bekup_app;
GRANT UPDATE (warned_at, ended_at, ended_by, end_reason) ON TABLE public.risk_acceptances TO bekup_app;
REVOKE ALL ON FUNCTION public.risk_acceptances_guard() FROM PUBLIC, bekup_app;
REVOKE ALL ON FUNCTION public.risk_acceptances_insert_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.risk_acceptances;
DROP FUNCTION public.risk_acceptances_insert_guard();
DROP FUNCTION public.risk_acceptances_guard();
DROP TYPE public.risk_end_reason;
DROP TYPE public.risk_duration;
DROP TYPE public.risk_kind;
-- +goose StatementEnd
