-- Download tickets (task 3.9, D-150). Only internal/downloads uses these.

-- GetDownloadArtifact loads an artifact with what a download needs: the
-- run's directory timestamp, the job slug, the engine and the item's
-- database name and raw digest.
-- name: GetDownloadArtifact :one
SELECT sqlc.embed(a), r.storage_timestamp AS run_storage_timestamp, j.slug AS job_slug,
       c.engine AS connection_engine, ri.db_name AS item_db_name, ri.raw_sha256 AS item_raw_sha256,
       ri.raw_size AS item_raw_size
FROM artifacts a
JOIN runs r ON r.id = a.run_id
JOIN jobs j ON j.id = a.job_id
JOIN connections c ON c.id = j.connection_id
LEFT JOIN run_items ri ON ri.id = a.run_item_id
WHERE a.id = $1;

-- GetDownloadManifestArtifact returns the manifest.json object of a run on
-- one destination.
-- name: GetDownloadManifestArtifact :one
SELECT * FROM artifacts
WHERE run_id = @run_id AND destination_id = @destination_id AND kind = 'manifest'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- ListDownloadManifestEntries returns the artifacts a run's manifest on one
-- destination lists (verified and signed there; any state), with the raw
-- digest of their item, for the manifest binding.
-- name: ListDownloadManifestEntries :many
SELECT a.object_key, a.size, a.sha256, ri.raw_sha256 AS item_raw_sha256
FROM artifacts a
JOIN run_items ri ON ri.id = a.run_item_id
WHERE a.run_id = @run_id AND a.destination_id = @destination_id AND a.kind <> 'manifest'
  AND a.verified AND a.signing_key_id = @signing_key_id::text
ORDER BY a.object_key;

-- name: CountActiveDownloadTickets :one
SELECT count(*)::bigint FROM download_tickets
WHERE user_id = @user_id AND used_at IS NULL AND expires_at > @now::timestamptz;

-- name: InsertDownloadTicket :one
INSERT INTO download_tickets (id, ticket_hash, session_id, user_id, artifact_id, mode, sealed_identity, created_at, expires_at)
VALUES (@id, @ticket_hash, @session_id, @user_id, @artifact_id, @mode, sqlc.narg(sealed_identity)::bytea,
        @created_at::timestamptz, @expires_at::timestamptz)
RETURNING id, created_at, expires_at;

-- GetDownloadTicketForUpdate locks a ticket by the hash of its value.
-- name: GetDownloadTicketForUpdate :one
SELECT * FROM download_tickets WHERE ticket_hash = $1 FOR UPDATE;

-- RedeemDownloadTicket marks a ticket used and clears its identity (also
-- used to burn a ticket presented by the wrong session).
-- name: RedeemDownloadTicket :execrows
UPDATE download_tickets SET used_at = @used_at::timestamptz, sealed_identity = NULL
WHERE id = @id AND used_at IS NULL;

-- DeleteExpiredDownloadTickets removes tickets that expired before the
-- cutoff (used or not), and with them any identity left in an unused one.
-- name: DeleteExpiredDownloadTickets :execrows
DELETE FROM download_tickets WHERE expires_at < @cutoff::timestamptz;
