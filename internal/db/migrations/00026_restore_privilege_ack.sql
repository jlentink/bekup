-- Privilege verdict and acknowledgement of a restore request (M4 review,
-- D-173): the restore worker probes the target again right before it
-- stages a restore and refuses it when the target role is now gated
-- (privileged or unknown, D-068/D-168) and the request carries no
-- acknowledgement covering the new reasons. Written only through
-- internal/restore at insert time; part of the restore's identity.
--
--   restores.target_privileges            not_privileged | privileged |
--                                         unknown as probed for the request
--                                         (NULL: internal test restores, which
--                                         the test-restore worker checks).
--   restores.target_privilege_reasons     the verdict's reason codes
--                                         (≤ 32 × 64 bytes).
--   restores.privileged_target_acknowledged
--                                         an admin acknowledged the gated
--                                         verdict (acknowledgePrivilegedTarget).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.restores
    ADD COLUMN target_privileges text,
    ADD COLUMN target_privilege_reasons text[] NOT NULL DEFAULT '{}',
    ADD COLUMN privileged_target_acknowledged boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT restores_target_privileges_value CHECK (
        target_privileges IS NULL OR target_privileges IN ('not_privileged', 'privileged', 'unknown')
    ),
    ADD CONSTRAINT restores_target_privilege_reasons_bounded CHECK (
        cardinality(target_privilege_reasons) <= 32
        AND octet_length(array_to_string(target_privilege_reasons, '')) <= 32 * 64
    ),
    -- An acknowledgement only exists for a gated verdict of a user request.
    ADD CONSTRAINT restores_privileged_target_ack CHECK (
        NOT privileged_target_acknowledged
        OR (requested_by IS NOT NULL AND target_privileges IN ('privileged', 'unknown'))
    );

CREATE OR REPLACE FUNCTION public.restores_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'canceled') AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'restores: restore % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.artifact_id IS DISTINCT FROM OLD.artifact_id OR NEW.run_item_id IS DISTINCT FROM OLD.run_item_id
       OR NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.target_connection_id IS DISTINCT FROM OLD.target_connection_id
       OR NEW.target_db_name IS DISTINCT FROM OLD.target_db_name OR NEW.mode IS DISTINCT FROM OLD.mode
       OR NEW.requested_by IS DISTINCT FROM OLD.requested_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.no_owner IS DISTINCT FROM OLD.no_owner
       OR NEW.target_privileges IS DISTINCT FROM OLD.target_privileges
       OR NEW.target_privilege_reasons IS DISTINCT FROM OLD.target_privilege_reasons
       OR NEW.privileged_target_acknowledged IS DISTINCT FROM OLD.privileged_target_acknowledged THEN
        RAISE EXCEPTION 'restores: the identity of restore % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.river_job_id IS NOT NULL AND NEW.river_job_id IS DISTINCT FROM OLD.river_job_id THEN
        RAISE EXCEPTION 'restores: the River job of restore % is already set', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION public.restores_guard() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.restores_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'canceled') AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION 'restores: restore % is already finished', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.artifact_id IS DISTINCT FROM OLD.artifact_id OR NEW.run_item_id IS DISTINCT FROM OLD.run_item_id
       OR NEW.job_id IS DISTINCT FROM OLD.job_id OR NEW.target_connection_id IS DISTINCT FROM OLD.target_connection_id
       OR NEW.target_db_name IS DISTINCT FROM OLD.target_db_name OR NEW.mode IS DISTINCT FROM OLD.mode
       OR NEW.requested_by IS DISTINCT FROM OLD.requested_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.no_owner IS DISTINCT FROM OLD.no_owner THEN
        RAISE EXCEPTION 'restores: the identity of restore % cannot change', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.river_job_id IS NOT NULL AND NEW.river_job_id IS DISTINCT FROM OLD.river_job_id THEN
        RAISE EXCEPTION 'restores: the River job of restore % is already set', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;
ALTER TABLE public.restores
    DROP CONSTRAINT restores_privileged_target_ack,
    DROP CONSTRAINT restores_target_privilege_reasons_bounded,
    DROP CONSTRAINT restores_target_privileges_value,
    DROP COLUMN privileged_target_acknowledged,
    DROP COLUMN target_privilege_reasons,
    DROP COLUMN target_privileges;
-- +goose StatementEnd
