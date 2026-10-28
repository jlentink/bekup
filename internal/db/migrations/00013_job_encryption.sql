-- Job encryption keys (task 3.2, D-142): the per-job age encryption of
-- backup files (D-016). Managed only by internal/jobkeys.
--
-- job_encryption holds the mode of each job: 'passphrase' (age scrypt with
-- the active row of job_passphrases), 'recipient' (age X25519 with the
-- active row of job_recipients; the private key is supplied at restore time
-- and never stored) or 'none' (an admin opt-out with step-up, audited as
-- security.opt_out, alerted and shown as a badge; who, when and why are
-- recorded on the row).
--
-- job_passphrases are versioned: a new passphrase retires the previous one,
-- but old versions are kept forever because older artifacts (artifacts
-- .passphrase_version, task 3.6) can only be decrypted with them. The
-- passphrase is sealed with the master keyring under AAD
-- job_passphrases:<job_id>:v<version>, so a sealed value cannot be moved to
-- another job or version. job_recipients are versioned the same way (public
-- data; kept so a restore can name the identity it needs).
--
-- The jobs table does not exist yet: task 3.5 adds the foreign keys from
-- job_id to jobs (id). Jobs are soft deleted, so the rows here outlive them.
--
-- bekup_app: no DELETE or TRUNCATE on any of the three tables; on the two
-- key tables UPDATE only of retired_at, and a trigger allows retired_at
-- only to go from NULL to a timestamp (a retired key is never revived).

-- +goose Up
-- +goose StatementBegin
CREATE TYPE public.job_encryption_mode AS ENUM ('passphrase', 'recipient', 'none');

CREATE TABLE public.job_encryption (
    job_id          uuid PRIMARY KEY,
    mode            public.job_encryption_mode NOT NULL,
    opted_out_by    uuid REFERENCES public.users (id) ON DELETE SET NULL,
    opted_out_at    timestamptz,
    opt_out_reason  text,
    updated_by      uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT job_encryption_opt_out_complete CHECK (
        (mode = 'none') = (opted_out_at IS NOT NULL)
        AND (mode = 'none') = (opt_out_reason IS NOT NULL)
        AND (opted_out_by IS NULL OR mode = 'none')
    ),
    CONSTRAINT job_encryption_reason_length CHECK (
        opt_out_reason IS NULL OR char_length(opt_out_reason) BETWEEN 1 AND 500
    )
);

CREATE TRIGGER job_encryption_set_updated_at BEFORE UPDATE ON public.job_encryption
    FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE TABLE public.job_passphrases (
    job_id            uuid NOT NULL,
    version           integer NOT NULL,
    sealed_passphrase bytea NOT NULL,
    created_by        uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    retired_at        timestamptz,
    PRIMARY KEY (job_id, version),
    CONSTRAINT job_passphrases_version_range CHECK (version BETWEEN 1 AND 1000000),
    CONSTRAINT job_passphrases_sealed_length CHECK (octet_length(sealed_passphrase) BETWEEN 1 AND 8192),
    CONSTRAINT job_passphrases_retired_after_created CHECK (retired_at IS NULL OR retired_at >= created_at)
);

-- Exactly one active passphrase per job (at most one by index; the service
-- always inserts the new version in the same transaction that retires the
-- old one).
CREATE UNIQUE INDEX job_passphrases_one_active_idx ON public.job_passphrases (job_id) WHERE retired_at IS NULL;

CREATE TABLE public.job_recipients (
    job_id      uuid NOT NULL,
    version     integer NOT NULL,
    recipient   text NOT NULL,
    -- Lowercase hex SHA-256 of the recipient string (the manifest's
    -- recipient fingerprint, internal/pipeline).
    fingerprint text NOT NULL,
    label       text NOT NULL DEFAULT '',
    -- Whether bekup generated the keypair (the identity was shown once).
    generated   boolean NOT NULL DEFAULT false,
    created_by  uuid REFERENCES public.users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    retired_at  timestamptz,
    PRIMARY KEY (job_id, version),
    CONSTRAINT job_recipients_version_range CHECK (version BETWEEN 1 AND 1000000),
    CONSTRAINT job_recipients_recipient_format CHECK (recipient ~ '^age1[02-9ac-hj-np-z]{58}$'),
    CONSTRAINT job_recipients_fingerprint_format CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT job_recipients_label_length CHECK (char_length(label) <= 100),
    CONSTRAINT job_recipients_retired_after_created CHECK (retired_at IS NULL OR retired_at >= created_at)
);

CREATE UNIQUE INDEX job_recipients_one_active_idx ON public.job_recipients (job_id) WHERE retired_at IS NULL;

-- A retired key stays retired and keeps its retirement time.
CREATE FUNCTION public.job_keys_retire_only() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF OLD.retired_at IS NOT NULL THEN
        RAISE EXCEPTION '%: version % of job % is already retired', TG_TABLE_NAME, OLD.version, OLD.job_id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.retired_at IS NULL THEN
        RAISE EXCEPTION '%: only retiring a key is allowed', TG_TABLE_NAME
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER job_passphrases_retire_only BEFORE UPDATE ON public.job_passphrases
    FOR EACH ROW EXECUTE FUNCTION public.job_keys_retire_only();
CREATE TRIGGER job_recipients_retire_only BEFORE UPDATE ON public.job_recipients
    FOR EACH ROW EXECUTE FUNCTION public.job_keys_retire_only();

REVOKE ALL ON TABLE public.job_encryption, public.job_passphrases, public.job_recipients FROM PUBLIC;
REVOKE DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.job_encryption FROM bekup_app;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.job_passphrases, public.job_recipients FROM bekup_app;
GRANT UPDATE (retired_at) ON TABLE public.job_passphrases, public.job_recipients TO bekup_app;
REVOKE ALL ON FUNCTION public.job_keys_retire_only() FROM PUBLIC, bekup_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.job_recipients;
DROP TABLE public.job_passphrases;
DROP FUNCTION public.job_keys_retire_only();
DROP TABLE public.job_encryption;
DROP TYPE public.job_encryption_mode;
-- +goose StatementEnd
