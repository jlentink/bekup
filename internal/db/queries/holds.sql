-- Artifact holds (task 3.11): leases that keep retention from deleting an
-- artifact while a download (or a restore, 4.x) reads it. Only
-- internal/runstate uses these.

-- AcquireArtifactHold takes a hold on a present artifact under a share lock
-- of its row (retention locks the row FOR NO KEY UPDATE while it deletes the
-- object). No row: the artifact is not present.
-- name: AcquireArtifactHold :one
WITH a AS (
    SELECT art.id AS art_id FROM artifacts art WHERE art.id = @artifact_id::uuid AND art.state = 'present' FOR SHARE
)
INSERT INTO artifact_holds (artifact_id, kind, holder_id, expires_at)
SELECT a.art_id, @kind::text, @holder_id, now() + make_interval(secs => @ttl_seconds::double precision)
FROM a
ON CONFLICT (artifact_id, kind, holder_id)
DO UPDATE SET expires_at = EXCLUDED.expires_at
RETURNING artifact_holds.id, artifact_holds.expires_at;

-- RenewArtifactHold moves an unexpired hold's expiry forward.
-- name: RenewArtifactHold :execrows
UPDATE artifact_holds
SET expires_at = now() + make_interval(secs => @ttl_seconds::double precision)
WHERE id = @id AND expires_at > now();

-- name: ReleaseArtifactHold :execrows
DELETE FROM artifact_holds WHERE id = $1;

-- DeleteExpiredArtifactHolds removes holds that expired before the cutoff.
-- name: DeleteExpiredArtifactHolds :execrows
DELETE FROM artifact_holds WHERE expires_at < @cutoff::timestamptz;

-- LockArtifactForDeletion locks a present artifact for a retention delete
-- and reports whether an unexpired hold protects it. No row: not present.
-- name: LockArtifactForDeletion :one
SELECT a.id,
       EXISTS (
           SELECT 1 FROM artifact_holds h WHERE h.artifact_id = a.id AND h.expires_at > now()
       )::boolean AS held
FROM artifacts a
WHERE a.id = $1 AND a.state = 'present'
FOR NO KEY UPDATE OF a;

-- ReleaseArtifactHoldsOfHolder removes the holds of one holder (the stuck
-- restore sweep, task 4.5).
-- name: ReleaseArtifactHoldsOfHolder :execrows
DELETE FROM artifact_holds WHERE kind = @kind::text AND holder_id = @holder_id;
