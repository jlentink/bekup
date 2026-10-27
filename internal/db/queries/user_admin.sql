-- User administration (task 1.6, D-110): admin CRUD in the API and the
-- break-glass `bekup admin` CLI. Deleted accounts are soft deleted.

-- name: GetActiveUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- GetUserForUpdate locks a user row that is not deleted.
-- name: GetUserForUpdate :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash, role, password_temporary_until)
VALUES (@email, @display_name, @password_hash, @role, @password_temporary_until)
RETURNING *;

-- LockActiveAdmins locks every active (not disabled, not deleted) admin row
-- and returns their ids. Callers that remove an admin hold these locks until
-- commit, so two concurrent demotions cannot both see another admin left.
-- name: LockActiveAdmins :many
SELECT id FROM users
WHERE role = 'admin' AND NOT disabled AND deleted_at IS NULL
ORDER BY id
FOR UPDATE;

-- ListUsers pages users ordered by e-mail, then id. after_email/after_id is
-- the last row of the previous page (both NULL: first page).
-- name: ListUsers :many
SELECT * FROM users
WHERE (sqlc.narg(include_deleted)::boolean IS TRUE OR deleted_at IS NULL)
  AND (sqlc.narg(role)::user_role IS NULL OR role = sqlc.narg(role)::user_role)
  AND (sqlc.narg(disabled)::boolean IS NULL OR disabled = sqlc.narg(disabled)::boolean)
  AND (sqlc.narg(search)::text IS NULL
       OR email::text ILIKE '%' || sqlc.narg(search)::text || '%' ESCAPE '\'
       OR display_name ILIKE '%' || sqlc.narg(search)::text || '%' ESCAPE '\')
  AND (sqlc.narg(after_email)::text IS NULL
       OR (lower(email::text), id) > (lower(sqlc.narg(after_email)::text), sqlc.narg(after_id)::uuid))
ORDER BY lower(email::text), id
LIMIT @lim;

-- name: UpdateUserDisplayName :exec
UPDATE users SET display_name = @display_name WHERE id = @id AND deleted_at IS NULL;

-- name: UpdateUserRole :exec
UPDATE users SET role = @role WHERE id = @id AND deleted_at IS NULL;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = @disabled WHERE id = @id AND deleted_at IS NULL;

-- SetUserPassword sets a password (temporary until the given time, or
-- permanent when NULL) and clears the lockout state.
-- name: SetUserPassword :execrows
UPDATE users
SET password_hash = @password_hash,
    password_temporary_until = @password_temporary_until,
    failed_login_count = 0,
    locked_until = NULL
WHERE id = @id AND deleted_at IS NULL;

-- name: UnlockUser :execrows
UPDATE users
SET failed_login_count = 0, locked_until = NULL
WHERE id = @id AND deleted_at IS NULL;

-- SoftDeleteUser marks the account deleted and disabled and drops its
-- password. The row stays for audit integrity.
-- name: SoftDeleteUser :execrows
UPDATE users
SET deleted_at = now(), disabled = true, password_hash = NULL, password_temporary_until = NULL,
    failed_login_count = 0, locked_until = NULL
WHERE id = @id AND deleted_at IS NULL;

-- ResetUserSecondFactorState removes the TOTP secret (active and pending),
-- clears the second-factor lockout and records the reset time (task 1.9:
-- an admin or the CLI removes every second factor; a new 2FA enrollment
-- grace period starts at mfa_reset_at). The caller holds the row lock.
-- name: ResetUserSecondFactorState :execrows
UPDATE users
SET totp_secret_enc = NULL,
    totp_last_step = NULL,
    totp_enabled_at = NULL,
    totp_pending_enc = NULL,
    totp_pending_expires_at = NULL,
    mfa_failed_count = 0,
    mfa_locked_until = NULL,
    mfa_reset_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: DeleteUserWebAuthnCredentials :execrows
DELETE FROM webauthn_credentials WHERE user_id = $1;

-- name: DeleteUserWebAuthnCeremonies :execrows
DELETE FROM webauthn_ceremonies WHERE user_id = $1;

-- ActiveAdminExists reports whether an active (not disabled, not deleted)
-- admin account exists; serve warns at startup when none does (D-113).
-- name: ActiveAdminExists :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE role = 'admin' AND NOT disabled AND deleted_at IS NULL
) AS exists;
