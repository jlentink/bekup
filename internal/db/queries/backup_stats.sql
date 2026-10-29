-- name: InsertBackupStats :exec
-- Backup completeness evidence (task 3.10, D-141): written by the backup
-- worker only, never updated or deleted.
INSERT INTO backup_stats (run_item_id, run_id, job_id, level, consistency, tables_total, warning_count, stats)
VALUES (@run_item_id, @run_id, @job_id, @level, @consistency, @tables_total, @warning_count, @stats);

-- name: ListBackupStatsByRun :many
SELECT bs.*, ri.kind AS item_kind, ri.db_name AS item_db_name
FROM backup_stats bs JOIN run_items ri ON ri.id = bs.run_item_id
WHERE bs.run_id = $1
ORDER BY ri.position, ri.id;

-- name: GetBackupStatsByItem :one
SELECT * FROM backup_stats WHERE run_item_id = $1;

-- name: ListBackupStatsForRuns :many
-- The completeness evidence of a batch of runs (RunItem.stats, D-187).
SELECT run_item_id, run_id, level, consistency, tables_total, warning_count, stats, created_at
FROM backup_stats
WHERE run_id = ANY(@run_ids::uuid[]);
