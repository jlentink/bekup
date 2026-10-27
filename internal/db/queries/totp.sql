-- TOTP and recovery codes (task 1.7). Every state change is a conditional
-- UPDATE so concurrent requests cannot both succeed.

-- name: GetUserTOTPState :one
SELECT id, email, disabled, totp_secret_enc, totp_last_step, totp_enabled_at,
       totp_pending_enc, totp_pending_expires_at, mfa_failed_count, mfa_locked_until
FROM users
WHERE id = $1;

-- HasTOTP reports whether the user has a confirmed TOTP secret.
-- name: HasTOTP :one
SELECT (totp_secret_enc IS NOT NULL)::boolean AS enabled FROM users WHERE id = $1;

-- SetTOTPPending stores a new pending secret, replacing an older pending
-- one; refused (0 rows) once TOTP is enabled.
-- name: SetTOTPPending :execrows
UPDATE users
SET totp_pending_enc = @pending_enc, totp_pending_expires_at = @expires_at
WHERE id = @id
  AND totp_secret_enc IS NULL;

-- ConfirmTOTP turns the pending secret into the active one, only if the
-- pending secret is still the one that was verified and not expired.
-- name: ConfirmTOTP :execrows
UPDATE users
SET totp_secret_enc = @secret_enc,
    totp_last_step = @step,
    totp_enabled_at = now(),
    totp_pending_enc = NULL,
    totp_pending_expires_at = NULL
WHERE id = @id
  AND totp_secret_enc IS NULL
  AND totp_pending_enc = @pending_enc
  AND totp_pending_expires_at > now();

-- AcceptTOTPStep records a verified time step (replay protection): it
-- succeeds only for a step after the last accepted one and only while the
-- secret is still the one the code was checked against. A success also
-- clears the second-factor failure counter.
-- name: AcceptTOTPStep :execrows
UPDATE users
SET totp_last_step = @step, mfa_failed_count = 0, mfa_locked_until = NULL
WHERE id = @id
  AND totp_secret_enc = @secret_enc
  AND (totp_last_step IS NULL OR totp_last_step < @step);

-- DisableTOTP removes the active and any pending secret.
-- name: DisableTOTP :execrows
UPDATE users
SET totp_secret_enc = NULL,
    totp_last_step = NULL,
    totp_enabled_at = NULL,
    totp_pending_enc = NULL,
    totp_pending_expires_at = NULL
WHERE id = $1
  AND totp_secret_enc IS NOT NULL;

-- ReserveMFAAttempt reserves one second-factor check before it runs
-- (M1 review: no check-then-count race). It counts the attempt only while
-- the second factors are not locked at at_time and, when the new count
-- reaches the threshold, applies the backoff lock in the same statement:
-- base_seconds doubled for every failure beyond the threshold, capped at
-- max_seconds (auth.LockoutPolicy.LockFor). No row: locked (or no such
-- user); the check must be refused without looking at the code. A
-- successful check clears the count and the lock (AcceptTOTPStep,
-- ResetMFAFailures).
-- name: ReserveMFAAttempt :one
UPDATE users
SET mfa_failed_count = mfa_failed_count + 1,
    mfa_locked_until = CASE
        WHEN mfa_failed_count + 1 >= sqlc.arg(threshold)::int THEN
            sqlc.arg(at_time)::timestamptz + make_interval(secs => LEAST(
                sqlc.arg(max_seconds)::float8,
                sqlc.arg(base_seconds)::float8
                    * power(2::float8, LEAST(mfa_failed_count + 1 - sqlc.arg(threshold)::int, 62)::float8)))
        ELSE NULL
    END
WHERE id = sqlc.arg(id)
  AND (mfa_locked_until IS NULL OR mfa_locked_until <= sqlc.arg(at_time)::timestamptz)
RETURNING mfa_failed_count, mfa_locked_until;

-- name: ResetMFAFailures :exec
UPDATE users
SET mfa_failed_count = 0, mfa_locked_until = NULL
WHERE id = $1
  AND (mfa_failed_count <> 0 OR mfa_locked_until IS NOT NULL);

-- name: DeleteRecoveryCodes :execrows
DELETE FROM recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, key_id, code_mac) VALUES ($1, $2, $3);

-- name: ListUnusedRecoveryCodes :many
SELECT id, key_id, code_mac
FROM recovery_codes
WHERE user_id = $1
  AND used_at IS NULL
ORDER BY id;

-- UseRecoveryCode marks a code used; 0 rows when it was used concurrently.
-- name: UseRecoveryCode :execrows
UPDATE recovery_codes
SET used_at = now()
WHERE id = @id
  AND user_id = @user_id
  AND used_at IS NULL;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL;
