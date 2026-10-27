-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: InsertUser :one
INSERT INTO users (email, display_name, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ReserveLoginAttempt reserves one password verification before argon2id
-- runs (task 1.5 lockout; M1 review: no check-then-count race). It counts
-- the attempt only while the account is not locked at at_time and, when
-- the new count reaches the threshold, applies the backoff lock in the
-- same statement (see ReserveMFAAttempt). No row: locked (or no such
-- user). A verified password clears the count (ResetLoginFailures,
-- UpdateUserPassword).
-- name: ReserveLoginAttempt :one
UPDATE users
SET failed_login_count = failed_login_count + 1,
    locked_until = CASE
        WHEN failed_login_count + 1 >= sqlc.arg(threshold)::int THEN
            sqlc.arg(at_time)::timestamptz + make_interval(secs => LEAST(
                sqlc.arg(max_seconds)::float8,
                sqlc.arg(base_seconds)::float8
                    * power(2::float8, LEAST(failed_login_count + 1 - sqlc.arg(threshold)::int, 62)::float8)))
        ELSE NULL
    END
WHERE id = sqlc.arg(id)
  AND (locked_until IS NULL OR locked_until <= sqlc.arg(at_time)::timestamptz)
RETURNING failed_login_count, locked_until;

-- ResetLoginFailures clears the lockout state after a successful login
-- (no write when there is nothing to clear).
-- name: ResetLoginFailures :exec
UPDATE users
SET failed_login_count = 0, locked_until = NULL
WHERE id = $1
  AND (failed_login_count <> 0 OR locked_until IS NOT NULL);

-- UpdateUserPassword sets a new (permanent) password hash and clears the
-- lockout state and a temporary-password mark (task 1.6).
-- name: UpdateUserPassword :execrows
UPDATE users
SET password_hash = @password_hash, password_temporary_until = NULL,
    failed_login_count = 0, locked_until = NULL
WHERE id = @id;

-- RehashUserPassword replaces the hash only if it is still the one that was
-- verified (a concurrent password change wins).
-- name: RehashUserPassword :execrows
UPDATE users
SET password_hash = @new_hash
WHERE id = @id
  AND password_hash = @old_hash;
