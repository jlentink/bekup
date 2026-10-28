-- Privileged test-restore targets (M4 review, D-170 amending D-169 with
-- D-068/D-168): the privilege verdict of a job's scratch target as probed
-- when it was designated, and an admin's acknowledgement of a privileged
-- (or unknown) target role. Written only through internal/jobs
-- (SetTestRestore, which always rewrites all four columns together with
-- the target); the test-restore worker re-probes the target before every
-- run and refuses it when the role is gated and its reasons are not
-- covered by the stored acknowledgement.
--
--   jobs.test_restore_target_privileges    not_privileged | privileged |
--                                          unknown (NULL: no target).
--   jobs.test_restore_privilege_reasons    the verdict's reason codes
--                                          (≤ 32 × 64 bytes).
--   jobs.test_restore_privileged_ack_by    the admin who acknowledged.
--   jobs.test_restore_privileged_ack_at    when (NULL: no acknowledgement;
--                                          only set for a gated verdict).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.jobs
    ADD COLUMN test_restore_target_privileges text,
    ADD COLUMN test_restore_privilege_reasons text[] NOT NULL DEFAULT '{}',
    ADD COLUMN test_restore_privileged_ack_by uuid REFERENCES public.users (id) ON DELETE SET NULL,
    ADD COLUMN test_restore_privileged_ack_at timestamptz,
    ADD CONSTRAINT jobs_test_restore_privileges_value CHECK (
        test_restore_target_privileges IS NULL
        OR test_restore_target_privileges IN ('not_privileged', 'privileged', 'unknown')
    ),
    ADD CONSTRAINT jobs_test_restore_privilege_reasons_bounded CHECK (
        cardinality(test_restore_privilege_reasons) <= 32
        AND octet_length(array_to_string(test_restore_privilege_reasons, '')) <= 32 * 64
    ),
    -- An acknowledgement only exists for a designated, gated target.
    ADD CONSTRAINT jobs_test_restore_privileged_ack CHECK (
        test_restore_privileged_ack_at IS NULL
        OR (test_restore_connection_id IS NOT NULL
            AND test_restore_target_privileges IN ('privileged', 'unknown'))
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.jobs
    DROP CONSTRAINT jobs_test_restore_privileged_ack,
    DROP CONSTRAINT jobs_test_restore_privilege_reasons_bounded,
    DROP CONSTRAINT jobs_test_restore_privileges_value,
    DROP COLUMN test_restore_privileged_ack_at,
    DROP COLUMN test_restore_privileged_ack_by,
    DROP COLUMN test_restore_privilege_reasons,
    DROP COLUMN test_restore_target_privileges;
-- +goose StatementEnd
