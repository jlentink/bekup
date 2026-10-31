package destinations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Unrecorded files (D-209). The daily retention sweep hands every
// non-deleted destination to ScanUnrecorded, which
//
//   - prunes empty run and job directories of bekup's layout older than
//     UnrecordedMinAge (disk and SFTP, not in no-delete mode; os.Root /
//     Lstat, never the destination root, only directories of known job
//     slugs), and
//   - lists the directories of known job slugs on the destination (disk,
//     SFTP and S3; at most MaxScanObjects objects; files outside them are
//     not bekup's to judge) and records the files of bekup's layout
//     ({job slug}/{timestamp}/{file}) older than UnrecordedMinAge that no
//     artifact row on any destination references and that do not belong
//     to a queued or running run, at most MaxUnrecordedPerDestination per
//     destination.
//
// Nothing is deleted automatically: admins list the files
// (UnrecordedFiles) and delete them one by one (DeleteUnrecordedFile, with
// step-up, audited, re-checked at delete time, refused in no-delete mode).

const (
	// UnrecordedMinAge is the minimum age of a recorded file and of a
	// pruned directory: younger ones may belong to a run in progress.
	UnrecordedMinAge = 24 * time.Hour
	// MaxScanObjects bounds the objects one scan lists per destination.
	MaxScanObjects = 200000
	// MaxUnrecordedPerDestination bounds the recorded files per
	// destination; more found set the scan's truncated flag.
	MaxUnrecordedPerDestination = 1000
	// MaxUnrecordedPage bounds one page of UnrecordedFiles.
	MaxUnrecordedPage = 200
	// scanBatch is the number of keys checked against the artifact rows at
	// once.
	scanBatch = 500
)

// Errors of the unrecorded-file operations.
var (
	// ErrUnrecordedNotFound: no such unrecorded file on the destination.
	ErrUnrecordedNotFound = errors.New("destinations: unrecorded file not found")
	// ErrNoDeleteMode: the destination never deletes.
	ErrNoDeleteMode = errors.New("destinations: the destination is in no-delete mode")
	// ErrUnrecordedNowRecorded: a backup record references the key now,
	// or it belongs to a queued or running run.
	ErrUnrecordedNowRecorded = errors.New("destinations: the file belongs to a backup")
	// ErrUnrecordedTooNew: the file changed within UnrecordedMinAge.
	ErrUnrecordedTooNew = errors.New("destinations: the file changed recently")
)

// ScanUnrecorded prunes empty layout directories and records the
// unrecorded files of the open destination d (see above). The scan's
// result replaces the destination's previous one; a scan that stopped at
// MaxScanObjects keeps the rows beyond its last listed key.
func (s *Service) ScanUnrecorded(ctx context.Context, id uuid.UUID, d storage.Destination) error {
	now := s.now()
	cutoff := now.Add(-UnrecordedMinAge)
	log := s.o.Logger.With(slog.String("destination_id", id.String()))
	q := sqlc.New(s.o.DB)

	slugs, err := q.ListJobSlugs(ctx)
	if err != nil {
		return fmt.Errorf("destinations: list job slugs: %w", err)
	}
	known := make(map[string]bool, len(slugs))
	for _, sl := range slugs {
		known[sl] = true
	}
	pruned := 0
	if p, ok := d.(storage.DirPruner); ok && !d.Capabilities().NoDeleteMode {
		n, err := p.PruneEmptyDirs(ctx, UnrecordedMinAge, func(slug string) bool { return known[slug] })
		pruned = n
		switch {
		case err != nil:
			log.WarnContext(ctx, "pruning empty directories failed", slog.Int("removed", n), slog.String("code", string(storage.Classify(err))))
		case n > 0:
			log.InfoContext(ctx, "empty backup directories removed", slog.Int("removed", n))
		}
	}

	active, err := activeRunDirs(ctx, q)
	if err != nil {
		return err
	}
	var (
		scanned   int
		truncated bool
		upto      string
		found     []storage.ObjectInfo
		batch     []storage.ObjectInfo
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		keys := make([]string, len(batch))
		for i := range batch {
			keys[i] = batch[i].Key.String()
		}
		rec, err := q.RecordedObjectKeys(ctx, keys)
		if err != nil {
			return fmt.Errorf("destinations: check recorded keys: %w", err)
		}
		recorded := make(map[string]bool, len(rec))
		for _, k := range rec {
			recorded[k] = true
		}
		for i := range batch {
			if recorded[batch[i].Key.String()] {
				continue
			}
			if len(found) >= MaxUnrecordedPerDestination {
				truncated = true
				break
			}
			found = append(found, batch[i])
		}
		batch = batch[:0]
		return nil
	}
	// Only the directories of known job slugs (deleted jobs included) are
	// listed: other files at the location are not bekup's to judge. The
	// slugs go in key order (slug + "/" bytewise), so the listed keys keep
	// the backends' order and upto stays a valid bound.
	sortSlugDirs(slugs)
	stop := false
	for _, slug := range slugs {
		if stop {
			break
		}
		prefix, err := storage.ParsePrefix(slug)
		if err != nil {
			continue // not a valid layout segment: never written by bekup
		}
		for o, err := range storage.All(ctx, d, prefix) {
			if err != nil {
				if storage.Classify(err) == storage.CodeHostKeyMismatch {
					if aerr := s.RecordRunFailure(ctx, id, storage.CodeHostKeyMismatch); aerr != nil {
						log.ErrorContext(ctx, "recording a host-key mismatch failed", slog.Any("error", aerr))
					}
				}
				return err
			}
			if scanned >= MaxScanObjects {
				truncated, stop = true, true
				break
			}
			scanned++
			upto = o.Key.String()
			lk, ok := storage.ParseLayoutKey(o.Key)
			if !ok || active[lk.RunDir] || o.ModTime.IsZero() || !o.ModTime.Before(cutoff) || o.Size < 0 {
				continue
			}
			batch = append(batch, o)
			if len(batch) >= scanBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if scanned < MaxScanObjects {
		upto = "" // the whole destination was listed
	}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		for i := range found {
			o := &found[i]
			mod := o.ModTime.UTC()
			if err := q.UpsertUnrecordedFile(ctx, sqlc.UpsertUnrecordedFileParams{
				DestinationID: id, ObjectKey: o.Key.String(), Size: o.Size, ModifiedAt: &mod, SeenAt: now,
			}); err != nil {
				return fmt.Errorf("destinations: record unrecorded file: %w", err)
			}
		}
		if _, err := q.DeleteUnseenUnrecordedFiles(ctx, sqlc.DeleteUnseenUnrecordedFilesParams{DestinationID: id, SeenAt: now, Upto: upto}); err != nil {
			return fmt.Errorf("destinations: drop unseen unrecorded files: %w", err)
		}
		return q.UpsertUnrecordedFileScan(ctx, sqlc.UpsertUnrecordedFileScanParams{
			DestinationID: id, ScannedAt: now, ScannedObjects: int32(min(scanned, MaxScanObjects)), //nolint:gosec // G115: bounded
			Truncated: truncated, PrunedDirs: int32(min(pruned, 1<<30)), //nolint:gosec // G115: bounded
		})
	})
	if err != nil {
		log.WarnContext(ctx, "recording the unrecorded-file scan failed", slog.Any("error", err))
		return err
	}
	if len(found) > 0 {
		log.InfoContext(ctx, "unrecorded files on the destination", slog.Int("files", len(found)), slog.Bool("truncated", truncated))
	}
	return nil
}

// sortSlugDirs orders job slugs like their directories' keys: by slug +
// "/" bytewise ("a-b/..." sorts before "a/...").
func sortSlugDirs(slugs []string) {
	slices.SortFunc(slugs, func(a, b string) int { return strings.Compare(a+"/", b+"/") })
}

// activeRunDirs returns the run directories of queued and running runs.
func activeRunDirs(ctx context.Context, q *sqlc.Queries) (map[string]bool, error) {
	rows, err := q.ListActiveRunDirs(ctx)
	if err != nil {
		return nil, fmt.Errorf("destinations: list active runs: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[storage.RunDir(r.Slug, r.StorageTimestamp)] = true
	}
	return out, nil
}

// UnrecordedFile is one recorded unrecorded file.
type UnrecordedFile struct {
	ID          uuid.UUID
	Key         string
	Size        int64
	ModifiedAt  *time.Time
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// UnrecordedPage is one page of a destination's unrecorded files.
type UnrecordedPage struct {
	// ScannedAt is the newest scan (nil: never scanned).
	ScannedAt  *time.Time
	Truncated  bool
	PrunedDirs int
	TotalCount int
	TotalBytes int64
	Files      []UnrecordedFile
	// Next is the key to continue after ("" on the last page).
	Next string
}

// UnrecordedFiles returns one page (ordered by key, after the key after;
// "" for the first page) of a non-deleted destination's unrecorded files.
func (s *Service) UnrecordedFiles(ctx context.Context, id uuid.UUID, first int, after string) (UnrecordedPage, error) {
	if first <= 0 {
		first = 50
	}
	if first > MaxUnrecordedPage {
		return UnrecordedPage{}, fieldErr("first", "must be between 1 and %d", MaxUnrecordedPage)
	}
	if after != "" {
		if _, err := storage.ParseKey(after); err != nil {
			return UnrecordedPage{}, fieldErr("after", "invalid cursor")
		}
	}
	q := sqlc.New(s.o.DB)
	if _, err := s.load(ctx, q, id, false); err != nil {
		return UnrecordedPage{}, err
	}
	var out UnrecordedPage
	scan, err := q.GetUnrecordedFileScan(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return out, fmt.Errorf("destinations: load scan: %w", err)
	default:
		at := scan.ScannedAt
		out.ScannedAt, out.Truncated, out.PrunedDirs = &at, scan.Truncated, int(scan.PrunedDirs)
	}
	tot, err := q.UnrecordedFileTotals(ctx, id)
	if err != nil {
		return out, fmt.Errorf("destinations: count unrecorded files: %w", err)
	}
	out.TotalCount, out.TotalBytes = int(tot.Files), tot.Bytes
	rows, err := q.ListUnrecordedFiles(ctx, sqlc.ListUnrecordedFilesParams{DestinationID: id, After: after, MaxRows: int32(first + 1)})
	if err != nil {
		return out, fmt.Errorf("destinations: list unrecorded files: %w", err)
	}
	if len(rows) > first {
		rows = rows[:first]
		out.Next = rows[first-1].ObjectKey
	}
	out.Files = make([]UnrecordedFile, 0, len(rows))
	for _, r := range rows {
		out.Files = append(out.Files, UnrecordedFile{
			ID: r.ID, Key: r.ObjectKey, Size: r.Size, ModifiedAt: r.ModifiedAt, FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt,
		})
	}
	return out, nil
}

// DeleteUnrecordedFile deletes one unrecorded file of a non-deleted
// destination. It checks that the file still exists and has not changed
// within UnrecordedMinAge, then re-checks under the row lock of the file's
// record that no artifact row on any destination references the key and
// that it is not in the directory of a queued or running run, and commits
// the audit entry (an audit failure refuses the delete) before the file is
// deleted outside the transaction; the record is removed afterwards.
// No-delete destinations refuse (ErrNoDeleteMode). A storage failure is
// returned as a normalized code, never as a raw error.
func (s *Service) DeleteUnrecordedFile(ctx context.Context, actor Actor, destID, fileID uuid.UUID) (storage.ErrorCode, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), destID, false)
	if err != nil {
		return "", err
	}
	if r.row.NoDelete {
		return "", ErrNoDeleteMode
	}
	if v := r.view(); !v.Ready {
		return "", &NotReadyError{Reason: v.NotReadyReason}
	}
	release, err := s.acquire(actor)
	if err != nil {
		return "", err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	defer cancel()
	d, err := s.open(ctx, r)
	if err != nil {
		var nr *NotReadyError
		if errors.As(err, &nr) {
			return "", err
		}
		return s.storageCode(ctx, actor, r, err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			s.o.Logger.DebugContext(ctx, "closing destination failed", slog.String("destination_id", destID.String()))
		}
	}()
	if d.Capabilities().NoDeleteMode {
		return "", ErrNoDeleteMode
	}
	// The file is checked on the backend first (no row lock is held
	// during network I/O), then the records are re-checked under the row
	// lock and the audit entry is committed (fail closed), then the file is
	// deleted outside any transaction, then the row is removed (the
	// pattern of the retention sweep).
	row, err := sqlc.New(s.o.DB).GetUnrecordedFile(ctx, sqlc.GetUnrecordedFileParams{ID: fileID, DestinationID: destID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnrecordedNotFound
	}
	if err != nil {
		return "", fmt.Errorf("destinations: load unrecorded file: %w", err)
	}
	key, err := storage.ParseKey(row.ObjectKey)
	if err != nil {
		return "", fmt.Errorf("destinations: stored key invalid: %w", err)
	}
	lk, ok := storage.ParseLayoutKey(key)
	if !ok {
		return "", errors.New("destinations: stored key outside the layout")
	}
	info, err := d.Stat(ctx, key)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		// Already gone: forget it.
		if err := sqlc.New(s.o.DB).DeleteUnrecordedFile(ctx, row.ID); err != nil {
			return "", fmt.Errorf("destinations: forget unrecorded file: %w", err)
		}
		return storage.CodeOK, nil
	case err != nil:
		return s.deleteFailed(ctx, actor, r, classify(err))
	case info.ModTime.IsZero() || !info.ModTime.Before(s.now().Add(-UnrecordedMinAge)):
		return "", ErrUnrecordedTooNew
	}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		locked, err := q.LockUnrecordedFile(ctx, sqlc.LockUnrecordedFileParams{ID: fileID, DestinationID: destID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnrecordedNotFound
		}
		if err != nil {
			return fmt.Errorf("destinations: lock unrecorded file: %w", err)
		}
		if locked.ObjectKey != row.ObjectKey {
			return ErrUnrecordedNotFound
		}
		recorded, err := q.ObjectKeyRecorded(ctx, row.ObjectKey)
		if err != nil {
			return fmt.Errorf("destinations: check recorded key: %w", err)
		}
		active, err := activeRunDirs(ctx, q)
		if err != nil {
			return err
		}
		if recorded || active[lk.RunDir] {
			return ErrUnrecordedNowRecorded
		}
		return s.record(ctx, q, actor, audit.ActionDestinationUnrecordedFileDeleted, destID, audit.Details{
			"name": r.row.Name, "object": row.ObjectKey, "size": info.Size,
		}, nil)
	})
	if err != nil {
		return "", err
	}
	if err := d.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return s.deleteFailed(ctx, actor, r, classify(err))
	}
	if err := sqlc.New(s.o.DB).DeleteUnrecordedFile(ctx, row.ID); err != nil {
		// The file is gone; the next scan drops the row.
		s.o.Logger.WarnContext(ctx, "forgetting a deleted unrecorded file failed", slog.String("destination_id", destID.String()))
	}
	return storage.CodeOK, nil
}

// deleteFailed logs a storage failure of DeleteUnrecordedFile and records a
// host-key mismatch as the security event it is.
func (s *Service) deleteFailed(ctx context.Context, actor Actor, r *record, code storage.ErrorCode) (storage.ErrorCode, error) {
	s.o.Logger.WarnContext(ctx, "deleting an unrecorded file failed",
		slog.String("destination_id", r.row.ID.String()), slog.String("code", string(code)))
	if code == storage.CodeHostKeyMismatch {
		if err := s.hostKeyMismatch(ctx, actor, r); err != nil {
			return "", err
		}
	}
	return code, nil
}

// storageCode normalizes a failure to open the backend like Test does (a
// blocked or unreachable endpoint is a network error) and records a
// host-key mismatch as the security event it is.
func (s *Service) storageCode(ctx context.Context, actor Actor, r *record, err error) (storage.ErrorCode, error) {
	code := classify(err)
	if code == storage.CodeHostKeyMismatch {
		if err := s.hostKeyMismatch(ctx, actor, r); err != nil {
			return "", err
		}
	}
	return code, nil
}

// classify is storage.Classify with blocked or unreachable endpoints (the
// SSRF guard) reported as network errors.
func classify(err error) storage.ErrorCode {
	if n := netguard.Normalize(err); errors.Is(n, netguard.ErrNotAllowed) || errors.Is(n, netguard.ErrUnreachable) {
		return storage.CodeNetwork
	}
	return storage.Classify(err)
}
