-- WebAuthn passkeys (task 1.8): registered passkeys and pending ceremonies.

-- name: ListWebAuthnCredentials :many
SELECT * FROM webauthn_credentials
WHERE user_id = $1
ORDER BY created_at, id
LIMIT 100;

-- name: CountWebAuthnCredentials :one
SELECT count(*) FROM webauthn_credentials
WHERE user_id = $1;

-- name: UserHasWebAuthnCredential :one
SELECT EXISTS (SELECT 1 FROM webauthn_credentials WHERE user_id = $1);

-- name: InsertWebAuthnCredential :one
INSERT INTO webauthn_credentials (
    user_id, credential_id, public_key, attestation_type, attestation_format,
    aaguid, sign_count, transports, attachment, user_present, user_verified,
    backup_eligible, backup_state, name
) VALUES (
    @user_id, @credential_id, @public_key, @attestation_type, @attestation_format,
    @aaguid, @sign_count, @transports, @attachment, @user_present, @user_verified,
    @backup_eligible, @backup_state, @name
)
RETURNING *;

-- RecordWebAuthnAssertion stores the state after a successful assertion.
-- The counter only moves forward (or stays 0 for authenticators without
-- one); no row means a concurrent assertion already advanced it, which is
-- treated as a regression.
-- name: RecordWebAuthnAssertion :one
UPDATE webauthn_credentials
SET sign_count = @sign_count,
    user_present = @user_present,
    user_verified = @user_verified,
    backup_state = @backup_state,
    last_used_at = now()
WHERE id = @id
  AND user_id = @user_id
  AND (sign_count < @sign_count OR (sign_count = 0 AND @sign_count::bigint = 0))
RETURNING *;

-- name: MarkWebAuthnSignCountAnomaly :execrows
UPDATE webauthn_credentials
SET sign_count_anomaly_at = now()
WHERE id = @id
  AND user_id = @user_id;

-- name: RenameWebAuthnCredential :one
UPDATE webauthn_credentials
SET name = @name
WHERE id = @id
  AND user_id = @user_id
RETURNING *;

-- name: DeleteWebAuthnCredential :one
DELETE FROM webauthn_credentials
WHERE id = @id
  AND user_id = @user_id
RETURNING *;

-- LockUserForFactorChange serializes second-factor changes of one user
-- (enrollment limits, last-factor protection).
-- name: LockUserForFactorChange :one
SELECT id FROM users
WHERE id = $1
FOR UPDATE;

-- PutWebAuthnCeremony stores the pending ceremony of a session, replacing
-- an earlier one of the same kind (only the newest challenge is valid).
-- name: PutWebAuthnCeremony :exec
INSERT INTO webauthn_ceremonies (session_id, user_id, kind, data, expires_at)
VALUES (@session_id, @user_id, @kind, @data, @expires_at)
ON CONFLICT (session_id, kind) DO UPDATE
SET id = gen_random_uuid(),
    user_id = EXCLUDED.user_id,
    data = EXCLUDED.data,
    created_at = now(),
    expires_at = EXCLUDED.expires_at;

-- TakeWebAuthnCeremony consumes the pending ceremony of a session and user
-- (single use): it is deleted whether or not it is still valid; expired
-- rows are returned to no one.
-- name: TakeWebAuthnCeremony :one
WITH taken AS (
    DELETE FROM webauthn_ceremonies
    WHERE session_id = @session_id
      AND user_id = @user_id
      AND kind = @kind
    RETURNING *
)
SELECT * FROM taken
WHERE expires_at > now();

-- name: DeleteExpiredWebAuthnCeremonies :execrows
DELETE FROM webauthn_ceremonies
WHERE expires_at < now();
