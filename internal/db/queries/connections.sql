-- Database connections (task 2.11, D-135). Only internal/connections uses
-- these queries; secret_enc is sealed under AAD connections:<id>:secret.
-- Temporary restore connections (task 4.4) are excluded until that task
-- adds its own queries.

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
                         ssh_auth, ssh_has_passphrase, ssh_public_key, secret_enc, created_by)
VALUES (@id, @name, @engine, @version_key, @image_key, @host, @port, @username,
        @has_password, @client_cert_pem, @tls_mode, @ssh_enabled, @ssh_host, @ssh_port, @ssh_user,
        @ssh_auth, @ssh_has_passphrase, @ssh_public_key, @secret_enc, @created_by)
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
WHERE id = @id AND deleted_at IS NULL AND NOT temporary
RETURNING *;

-- name: SetConnectionBackupPin :one
UPDATE connections
SET tls_backup_pin_cert_pem = @cert_pem,
    tls_backup_pin_spki = @spki,
    tls_backup_pin_not_after = @not_after,
    tls_backup_pinned_at = now(),
    tls_backup_pinned_by = @pinned_by
WHERE id = @id AND deleted_at IS NULL AND NOT temporary
RETURNING *;

-- name: SetConnectionHostKey :one
UPDATE connections
SET ssh_host_key_type = @host_key_type,
    ssh_host_key_fingerprint = @host_key_fingerprint,
    ssh_host_key_authorized_key = @host_key_authorized_key,
    ssh_host_key_confirmed_at = now(),
    ssh_host_key_confirmed_by = @confirmed_by
WHERE id = @id AND deleted_at IS NULL AND NOT temporary AND ssh_enabled
RETURNING *;

-- name: SetConnectionSSHAuth :one
UPDATE connections
SET ssh_auth = @ssh_auth,
    ssh_has_passphrase = @ssh_has_passphrase,
    ssh_public_key = @ssh_public_key,
    secret_enc = @secret_enc
WHERE id = @id AND deleted_at IS NULL AND NOT temporary AND ssh_enabled
RETURNING *;

-- name: SoftDeleteConnection :execrows
UPDATE connections SET deleted_at = now()
WHERE id = @id AND deleted_at IS NULL AND NOT temporary;
