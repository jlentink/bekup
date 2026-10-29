-- Database connections (task 2.11, D-135). Only internal/connections uses
-- these queries; secret_enc is sealed under AAD connections:<id>:secret.
-- Temporary restore connections (task 4.4, D-163) are excluded from every
-- query below except the *Any* / *Temporary* ones and the pin, host-key and
-- SSH-key updates with allow_temporary (the service decides).

-- name: ListConnections :many
SELECT * FROM connections
WHERE deleted_at IS NULL AND NOT temporary
ORDER BY lower(name), id;

-- name: GetConnection :one
SELECT * FROM connections WHERE id = $1 AND deleted_at IS NULL AND NOT temporary;

-- GetConnectionForUpdate locks a connection that is not deleted.
-- name: GetConnectionForUpdate :one
SELECT * FROM connections WHERE id = $1 AND deleted_at IS NULL AND NOT temporary FOR UPDATE;

-- InsertConnection creates the row with an id chosen by the application,
-- so that secret_enc can be sealed under the row's AAD before the insert.
-- name: InsertConnection :one
INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username,
                         has_password, client_cert_pem, tls_mode, ssh_enabled, ssh_host, ssh_port, ssh_user,
                         ssh_auth, ssh_has_passphrase, ssh_public_key, secret_enc, created_by, max_concurrent_runs)
VALUES (@id, @name, @engine, @version_key, @image_key, @host, @port, @username,
        @has_password, @client_cert_pem, @tls_mode, @ssh_enabled, @ssh_host, @ssh_port, @ssh_user,
        @ssh_auth, @ssh_has_passphrase, @ssh_public_key, @secret_enc, @created_by, @max_concurrent_runs)
RETURNING *;

-- UpdateConnection replaces the mutable fields (not the TLS mode, which
-- only SetConnectionTLSMode changes). clear_pins removes both certificate
-- pins (engine, host or port changed); clear_host_key removes the SSH
-- host-key pin (bastion host or port changed, or the tunnel was removed).
-- name: UpdateConnection :one
UPDATE connections
SET name = @name,
    engine = @engine,
    version_key = @version_key,
    image_key = @image_key,
    host = @host,
    port = @port,
    username = @username,
    has_password = @has_password,
    client_cert_pem = @client_cert_pem,
    ssh_enabled = @ssh_enabled,
    ssh_host = @ssh_host,
    ssh_port = @ssh_port,
    ssh_user = @ssh_user,
    ssh_auth = @ssh_auth,
    ssh_has_passphrase = @ssh_has_passphrase,
    ssh_public_key = @ssh_public_key,
    secret_enc = @secret_enc,
    max_concurrent_runs = @max_concurrent_runs,
    tls_pin_cert_pem = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_pin_cert_pem END,
    tls_pin_spki = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_pin_spki END,
    tls_pin_not_after = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_pin_not_after END,
    tls_pinned_at = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_pinned_at END,
    tls_pinned_by = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_pinned_by END,
    tls_backup_pin_cert_pem = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_backup_pin_cert_pem END,
    tls_backup_pin_spki = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_backup_pin_spki END,
    tls_backup_pin_not_after = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_backup_pin_not_after END,
    tls_backup_pinned_at = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_backup_pinned_at END,
    tls_backup_pinned_by = CASE WHEN @clear_pins::boolean THEN NULL ELSE tls_backup_pinned_by END,
    ssh_host_key_type = CASE WHEN @clear_host_key::boolean THEN NULL ELSE ssh_host_key_type END,
    ssh_host_key_fingerprint = CASE WHEN @clear_host_key::boolean THEN NULL ELSE ssh_host_key_fingerprint END,
    ssh_host_key_authorized_key = CASE WHEN @clear_host_key::boolean THEN NULL ELSE ssh_host_key_authorized_key END,
    ssh_host_key_confirmed_at = CASE WHEN @clear_host_key::boolean THEN NULL ELSE ssh_host_key_confirmed_at END,
    ssh_host_key_confirmed_by = CASE WHEN @clear_host_key::boolean THEN NULL ELSE ssh_host_key_confirmed_by END
WHERE id = @id AND deleted_at IS NULL AND NOT temporary
RETURNING *;

-- name: SetConnectionTLSMode :one
UPDATE connections SET tls_mode = @tls_mode
WHERE id = @id AND deleted_at IS NULL AND NOT temporary
RETURNING *;

-- SetConnectionPin replaces the current pin; clear_backup also removes the
-- staged backup pin (it was promoted to the current one).
-- name: SetConnectionPin :one
UPDATE connections
SET tls_pin_cert_pem = @cert_pem,
    tls_pin_spki = @spki,
    tls_pin_not_after = @not_after,
    tls_pinned_at = now(),
    tls_pinned_by = @pinned_by,
    tls_backup_pin_cert_pem = CASE WHEN @clear_backup::boolean THEN NULL ELSE tls_backup_pin_cert_pem END,
    tls_backup_pin_spki = CASE WHEN @clear_backup::boolean THEN NULL ELSE tls_backup_pin_spki END,
    tls_backup_pin_not_after = CASE WHEN @clear_backup::boolean THEN NULL ELSE tls_backup_pin_not_after END,
    tls_backup_pinned_at = CASE WHEN @clear_backup::boolean THEN NULL ELSE tls_backup_pinned_at END,
    tls_backup_pinned_by = CASE WHEN @clear_backup::boolean THEN NULL ELSE tls_backup_pinned_by END
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR @allow_temporary::boolean)
RETURNING *;

-- name: SetConnectionBackupPin :one
UPDATE connections
SET tls_backup_pin_cert_pem = @cert_pem,
    tls_backup_pin_spki = @spki,
    tls_backup_pin_not_after = @not_after,
    tls_backup_pinned_at = now(),
    tls_backup_pinned_by = @pinned_by
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR @allow_temporary::boolean)
RETURNING *;

-- name: SetConnectionHostKey :one
UPDATE connections
SET ssh_host_key_type = @host_key_type,
    ssh_host_key_fingerprint = @host_key_fingerprint,
    ssh_host_key_authorized_key = @host_key_authorized_key,
    ssh_host_key_confirmed_at = now(),
    ssh_host_key_confirmed_by = @confirmed_by
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR @allow_temporary::boolean) AND ssh_enabled
RETURNING *;

-- name: SetConnectionSSHAuth :one
UPDATE connections
SET ssh_auth = @ssh_auth,
    ssh_has_passphrase = @ssh_has_passphrase,
    ssh_public_key = @ssh_public_key,
    secret_enc = @secret_enc
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR @allow_temporary::boolean) AND ssh_enabled
RETURNING *;

-- name: SoftDeleteConnection :execrows
UPDATE connections SET deleted_at = now()
WHERE id = @id AND deleted_at IS NULL AND NOT temporary;

-- --- Temporary restore connections (task 4.4, D-163) -------------------------
-- Admin only, restore targets only. A temporary connection is usable until
-- expires_at (checked against the caller's clock, @now); the cleanup worker
-- then destroys its credentials and soft deletes it.

-- GetConnectionAny reads a connection that is not deleted, permanent or a
-- temporary one that has not expired.
-- name: GetConnectionAny :one
SELECT * FROM connections
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR expires_at > @now::timestamptz);

-- name: GetConnectionAnyForUpdate :one
SELECT * FROM connections
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR expires_at > @now::timestamptz)
FOR UPDATE;

-- GetTemporaryConnectionForUpdate locks a temporary connection that is not
-- deleted, expired or not (delete, cleanup).
-- name: GetTemporaryConnectionForUpdate :one
SELECT * FROM connections WHERE id = @id AND temporary AND deleted_at IS NULL FOR UPDATE;

-- GetConnectionAnyForShare share-locks a usable connection (permanent or
-- an unexpired temporary one) as a restore target (task 4.5): the cleanup
-- worker's row lock waits for the restore's transaction.
-- GetConnectionForSlotHolder loads a restore target for a run that holds
-- a run slot on it (holder): a permanent connection, or a temporary one
-- that is unexpired or whose slot the holder holds (the cleanup never
-- deletes a connection while a slot is held). Deleted rows never load.
-- name: GetConnectionForSlotHolder :one
SELECT c.* FROM connections c
WHERE c.id = @id AND c.deleted_at IS NULL
  AND (NOT c.temporary OR c.expires_at > @now::timestamptz
       OR EXISTS (SELECT 1 FROM run_slots s WHERE s.connection_id = c.id AND s.holder_id = @holder_id::uuid));

-- name: GetConnectionAnyForShare :one
SELECT * FROM connections
WHERE id = @id AND deleted_at IS NULL AND (NOT temporary OR expires_at > @now::timestamptz)
FOR SHARE;

-- name: ListTemporaryConnections :many
SELECT * FROM connections
WHERE temporary AND deleted_at IS NULL AND expires_at > @now::timestamptz
ORDER BY created_at DESC, id
LIMIT 200;

-- name: CountActiveTemporaryConnections :one
SELECT count(*) FROM connections
WHERE temporary AND deleted_at IS NULL AND expires_at > @now::timestamptz;

-- name: InsertTemporaryConnection :one
INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username,
                         has_password, client_cert_pem, tls_mode, ssh_enabled, ssh_host, ssh_port, ssh_user,
                         ssh_auth, ssh_has_passphrase, ssh_public_key, secret_enc, created_by, max_concurrent_runs,
                         temporary, expires_at)
VALUES (@id, @name, @engine, @version_key, @image_key, @host, @port, @username,
        @has_password, @client_cert_pem, @tls_mode, @ssh_enabled, @ssh_host, @ssh_port, @ssh_user,
        @ssh_auth, @ssh_has_passphrase, @ssh_public_key, @secret_enc, @created_by, @max_concurrent_runs,
        true, @expires_at)
RETURNING *;

-- name: SetTemporaryConnectionExpiry :one
UPDATE connections SET expires_at = @expires_at
WHERE id = @id AND temporary AND deleted_at IS NULL
RETURNING *;

-- ShredTemporaryConnection destroys the stored credentials (the sealed
-- blob and every flag describing it) and soft deletes the row; the
-- non-secret metadata stays for the restore history and the audit log.
-- name: ShredTemporaryConnection :execrows
UPDATE connections
SET secret_enc = NULL,
    has_password = false,
    client_cert_pem = NULL,
    ssh_auth = 'none',
    ssh_has_passphrase = false,
    ssh_public_key = NULL,
    deleted_at = now()
WHERE id = @id AND temporary AND deleted_at IS NULL;

-- ListTemporaryConnectionCleanup returns the temporary connections the
-- cleanup worker has to look at: expired ones and those a restore
-- (task 4.1, restores.target_connection_id) has used, with the number of
-- unfinished restores and the latest finish time. Connections with a held
-- run slot (a running backup or restore) wait until the run released it.
-- name: ListTemporaryConnectionCleanup :many
SELECT c.id,
       c.expires_at,
       (SELECT count(*) FROM restores r
         WHERE r.target_connection_id = c.id AND r.finished_at IS NULL)::bigint AS active_restores,
       (SELECT count(*) FROM restores r
         WHERE r.target_connection_id = c.id)::bigint AS restores,
       -- epoch when no restore finished (only read when restores > 0 and
       -- active_restores = 0, i.e. every restore finished).
       COALESCE((SELECT max(r.finished_at) FROM restores r
         WHERE r.target_connection_id = c.id), 'epoch'::timestamptz)::timestamptz AS last_restore_finished_at
FROM connections c
WHERE c.temporary AND c.deleted_at IS NULL
  AND (c.expires_at <= @now::timestamptz
       OR EXISTS (SELECT 1 FROM restores r WHERE r.target_connection_id = c.id))
  AND NOT EXISTS (SELECT 1 FROM run_slots s WHERE s.connection_id = c.id)
ORDER BY c.expires_at, c.id
LIMIT @max_rows;

-- GetTemporaryConnectionCleanupState re-reads the state of one candidate
-- of ListTemporaryConnectionCleanup under the connection's row lock.
-- name: GetTemporaryConnectionCleanupState :one
SELECT c.id,
       c.expires_at,
       (SELECT count(*) FROM restores r
         WHERE r.target_connection_id = c.id AND r.finished_at IS NULL)::bigint AS active_restores,
       (SELECT count(*) FROM restores r
         WHERE r.target_connection_id = c.id)::bigint AS restores,
       COALESCE((SELECT max(r.finished_at) FROM restores r
         WHERE r.target_connection_id = c.id), 'epoch'::timestamptz)::timestamptz AS last_restore_finished_at,
       EXISTS (SELECT 1 FROM run_slots s WHERE s.connection_id = c.id) AS slot_held
FROM connections c
WHERE c.id = @id AND c.temporary AND c.deleted_at IS NULL;

-- RecordConnectionTest stores the outcome of a test (D-187) when the row
-- is unchanged since the test loaded it (settings_at = its updated_at); 0
-- rows: changed or deleted meanwhile, the result is not stored. The
-- trigger leaves updated_at alone for this update.
-- name: RecordConnectionTest :execrows
UPDATE connections
SET last_tested_at = now(),
    last_test_ok = @ok,
    last_test_code = sqlc.narg(code),
    last_test_settings_at = updated_at,
    last_test_privileges = sqlc.narg(privileges),
    last_test_privilege_reasons = @privilege_reasons::text[]
WHERE id = @id AND deleted_at IS NULL AND updated_at = @settings_at;
