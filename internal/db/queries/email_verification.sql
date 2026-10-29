-- User e-mail verification (task 5.1, D-178); only internal/notify/email
-- uses these queries.

-- GetEmailVerificationForUpdate locks the pending verification of a user.
-- name: GetEmailVerificationForUpdate :one
SELECT * FROM email_verifications WHERE user_id = $1 FOR UPDATE;

-- name: InsertEmailVerification :exec
INSERT INTO email_verifications (user_id, email, code_mac, mac_key_id, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- DeleteEmailVerification removes the pending verification of a user.
-- name: DeleteEmailVerification :execrows
DELETE FROM email_verifications WHERE user_id = $1;

-- DeleteEmailVerificationCode removes one specific pending verification
-- (a link whose e-mail could not be sent).
-- name: DeleteEmailVerificationCode :execrows
DELETE FROM email_verifications WHERE user_id = $1 AND code_mac = $2;

-- name: RecordEmailVerificationFailure :one
UPDATE email_verifications SET failed_attempts = failed_attempts + 1
WHERE user_id = $1
RETURNING failed_attempts;

-- MarkEmailVerified sets email_verified_at while the account still has the
-- verified address (0 rows: the address changed or the account is gone).
-- name: MarkEmailVerified :execrows
UPDATE users SET email_verified_at = now()
WHERE id = @id AND email = @email AND deleted_at IS NULL;

-- GetEmailVerificationState returns what decides whether a user may get
-- e-mail: the address and when it was verified.
-- name: GetEmailVerificationState :one
SELECT email, email_verified_at, disabled FROM users WHERE id = $1 AND deleted_at IS NULL;
