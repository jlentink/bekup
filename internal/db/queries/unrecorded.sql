-- Unrecorded files (D-209): files in bekup's layout on a destination that no
-- artifact row references, found by the daily scan. Only
-- internal/destinations reads and writes these tables.

-- RecordedObjectKeys returns which of the keys have an artifact row in
-- any state on any destination, soft-deleted ones included, so a key
-- recorded elsewhere is never reported as unrecorded, even if two
-- destinations ever shared a location (D-209).
-- name: RecordedObjectKeys :many
SELECT a.object_key FROM artifacts a
WHERE a.object_key = ANY (@keys::text[])
GROUP BY a.object_key;

-- ObjectKeyRecorded reports whether an artifact row on any destination
-- references the key.
-- name: ObjectKeyRecorded :one
SELECT EXISTS (SELECT 1 FROM artifacts WHERE object_key = @object_key)::boolean;

-- ListActiveRunDirs returns the directories of queued and running runs:
-- their files may not have artifact rows yet.
-- name: ListActiveRunDirs :many
SELECT j.slug, r.storage_timestamp::timestamptz AS storage_timestamp
FROM runs r
JOIN jobs j ON j.id = r.job_id
WHERE r.status IN ('queued', 'running') AND r.storage_timestamp IS NOT NULL;

-- UpsertUnrecordedFile records a file found by the scan (first_seen_at is
-- kept from the first sighting).
-- name: UpsertUnrecordedFile :exec
INSERT INTO unrecorded_files (destination_id, object_key, size, modified_at, first_seen_at, last_seen_at)
VALUES (@destination_id, @object_key, @size, sqlc.narg(modified_at)::timestamptz, @seen_at::timestamptz, @seen_at::timestamptz)
ON CONFLICT (destination_id, object_key) DO UPDATE
SET size = excluded.size, modified_at = excluded.modified_at, last_seen_at = excluded.last_seen_at;

-- DeleteUnseenUnrecordedFiles drops the rows a scan did not see again (the
-- file is gone or recorded). @upto bounds it to the listed key range of a
-- scan that stopped at its object limit ('' = the whole destination); keys
-- compare bytewise (COLLATE "C"), the order the backends list them in.
-- name: DeleteUnseenUnrecordedFiles :execrows
DELETE FROM unrecorded_files
WHERE destination_id = @destination_id AND last_seen_at < @seen_at::timestamptz
  AND (@upto::text = '' OR object_key COLLATE "C" <= @upto::text);

-- name: UpsertUnrecordedFileScan :exec
INSERT INTO unrecorded_file_scans (destination_id, scanned_at, scanned_objects, truncated, pruned_dirs)
VALUES (@destination_id, @scanned_at::timestamptz, @scanned_objects, @truncated, @pruned_dirs)
ON CONFLICT (destination_id) DO UPDATE
SET scanned_at = excluded.scanned_at, scanned_objects = excluded.scanned_objects,
    truncated = excluded.truncated, pruned_dirs = excluded.pruned_dirs;

-- name: GetUnrecordedFileScan :one
SELECT * FROM unrecorded_file_scans WHERE destination_id = $1;

-- name: UnrecordedFileTotals :one
SELECT count(*)::bigint AS files, coalesce(sum(size), 0)::bigint AS bytes
FROM unrecorded_files WHERE destination_id = $1;

-- ListUnrecordedFiles returns one page ordered by key (keyset: @after is the
-- last key of the previous page, '' for the first).
-- name: ListUnrecordedFiles :many
SELECT * FROM unrecorded_files
WHERE destination_id = @destination_id AND object_key > @after::text
ORDER BY object_key
LIMIT @max_rows::int;

-- name: LockUnrecordedFile :one
SELECT * FROM unrecorded_files WHERE id = @id AND destination_id = @destination_id FOR UPDATE;

-- name: DeleteUnrecordedFile :exec
DELETE FROM unrecorded_files WHERE id = $1;

-- ClearUnrecordedFiles forgets a destination's scan results (its location
-- changed).
-- name: ClearUnrecordedFiles :exec
WITH f AS (DELETE FROM unrecorded_files WHERE unrecorded_files.destination_id = @destination_id)
DELETE FROM unrecorded_file_scans WHERE unrecorded_file_scans.destination_id = @destination_id;

-- name: GetUnrecordedFile :one
SELECT * FROM unrecorded_files WHERE id = @id AND destination_id = @destination_id;
