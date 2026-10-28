-- Job encryption keys (task 3.2, D-142). Only internal/jobkeys uses these
-- queries; sealed_passphrase is sealed under AAD
-- job_passphrases:<job_id>:v<version>. There are no DELETE queries: keys
-- are retired, never deleted (bekup_app has no DELETE privilege).

-- EnsureJobEncryption creates the encryption row of a job that has none
-- (mode passphrase until a key or an opt-out says otherwise).
-- name: EnsureJobEncryption :exec
INSERT INTO job_encryption (job_id, mode, updated_by)
VALUES (@job_id, 'passphrase', @updated_by)
ON CONFLICT (job_id) DO NOTHING;

-- name: InsertJobEncryption :one
INSERT INTO job_encryption (job_id, mode, updated_by)
VALUES (@job_id, @mode, @updated_by)
RETURNING *;

-- name: GetJobEncryption :one
SELECT * FROM job_encryption WHERE job_id = $1;

-- GetJobEncryptionForUpdate serializes key changes of one job.
-- name: GetJobEncryptionForUpdate :one
SELECT * FROM job_encryption WHERE job_id = $1 FOR UPDATE;

-- GetJobEncryptionForShare reads the mode while blocking key changes, so
-- the mode and the active key a backup reads belong together.
-- name: GetJobEncryptionForShare :one
SELECT * FROM job_encryption WHERE job_id = $1 FOR SHARE;

-- SetJobEncryptionMode switches between the key modes and clears an
-- opt-out.
-- name: SetJobEncryptionMode :one
UPDATE job_encryption
SET mode = @mode, opted_out_by = NULL, opted_out_at = NULL, opt_out_reason = NULL, updated_by = @updated_by
WHERE job_id = @job_id
RETURNING *;

-- name: OptOutJobEncryption :one
UPDATE job_encryption
SET mode = 'none', opted_out_by = @actor, opted_out_at = now(), opt_out_reason = @reason, updated_by = @actor
WHERE job_id = @job_id
RETURNING *;

-- name: GetActiveJobPassphrase :one
SELECT * FROM job_passphrases WHERE job_id = $1 AND retired_at IS NULL;

-- name: GetJobPassphrase :one
SELECT * FROM job_passphrases WHERE job_id = @job_id AND version = @version;

-- name: ListJobPassphrases :many
SELECT * FROM job_passphrases WHERE job_id = $1 ORDER BY version;

-- name: NextJobPassphraseVersion :one
SELECT (COALESCE(max(version), 0) + 1)::integer AS version FROM job_passphrases WHERE job_id = $1;

-- Retire* use clock_timestamp(): a change that waited for the job's lock
-- may have started before the version it retires was created.
-- name: RetireJobPassphrases :execrows
UPDATE job_passphrases SET retired_at = clock_timestamp() WHERE job_id = $1 AND retired_at IS NULL;

-- name: InsertJobPassphrase :one
INSERT INTO job_passphrases (job_id, version, sealed_passphrase, created_by)
VALUES (@job_id, @version, @sealed_passphrase, @created_by)
RETURNING *;

-- name: GetActiveJobRecipient :one
SELECT * FROM job_recipients WHERE job_id = $1 AND retired_at IS NULL;

-- name: GetJobRecipient :one
SELECT * FROM job_recipients WHERE job_id = @job_id AND version = @version;

-- name: ListJobRecipients :many
SELECT * FROM job_recipients WHERE job_id = $1 ORDER BY version;

-- name: NextJobRecipientVersion :one
SELECT (COALESCE(max(version), 0) + 1)::integer AS version FROM job_recipients WHERE job_id = $1;

-- name: RetireJobRecipients :execrows
UPDATE job_recipients SET retired_at = clock_timestamp() WHERE job_id = $1 AND retired_at IS NULL;

-- name: InsertJobRecipient :one
INSERT INTO job_recipients (job_id, version, recipient, fingerprint, label, generated, created_by)
VALUES (@job_id, @version, @recipient, @fingerprint, @label, @generated, @created_by)
RETURNING *;
