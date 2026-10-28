package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Scratch layout: <scratch_dir>/restore-<uuid>/artifact.age (dir 0700,
// file 0600). Every access goes through an os.Root of the scratch
// directory, so a planted symlink never leads outside it.
const (
	dirPrefix   = "restore-"
	stagedName  = "artifact.age"
	maxSweepIDs = 1000
)

func dirName(id uuid.UUID) string { return dirPrefix + id.String() }

// stagedPath is the staged file relative to the scratch root.
func stagedPath(id uuid.UUID) string { return dirName(id) + "/" + stagedName }

// checkScratch creates the scratch directory (0700) when it is missing and
// refuses one that is not a directory or that others may write to.
func (s *Service) checkScratch() error {
	if err := os.MkdirAll(s.o.ScratchDir, 0o700); err != nil {
		return fmt.Errorf("restore: create the scratch directory: %w", err)
	}
	fi, err := os.Stat(s.o.ScratchDir)
	if err != nil {
		return fmt.Errorf("restore: scratch directory: %w", err)
	}
	if !fi.IsDir() {
		return errors.New("restore: the scratch path is not a directory")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("restore: the scratch directory must not be group- or world-writable")
	}
	return nil
}

func (s *Service) openRoot() (*os.Root, error) {
	r, err := os.OpenRoot(s.o.ScratchDir)
	if err != nil {
		return nil, fmt.Errorf("restore: open the scratch directory: %w", err)
	}
	return r, nil
}

// makeDir creates the private staging directory of a restore, replacing a
// leftover of an earlier attempt.
func (s *Service) makeDir(id uuid.UUID) error {
	r, err := s.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	name := dirName(id)
	if err := r.RemoveAll(name); err != nil {
		return fmt.Errorf("restore: remove a leftover staging directory: %w", err)
	}
	if err := r.Mkdir(name, 0o700); err != nil {
		return fmt.Errorf("restore: create the staging directory: %w", err)
	}
	// Mkdir is subject to the umask; make the mode exact.
	if err := r.Chmod(name, 0o700); err != nil {
		return fmt.Errorf("restore: staging directory mode: %w", err)
	}
	return nil
}

// removeDir removes the staging directory of a restore (idempotent).
func (s *Service) removeDir(id uuid.UUID) error {
	r, err := os.OpenRoot(s.o.ScratchDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("restore: open the scratch directory: %w", err)
	}
	defer r.Close()
	if err := r.RemoveAll(dirName(id)); err != nil {
		return fmt.Errorf("restore: remove the staging directory: %w", err)
	}
	return nil
}

func (s *Service) markLive(id uuid.UUID, live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if live {
		s.live[id] = struct{}{}
	} else {
		delete(s.live, id)
	}
}

func (s *Service) isLive(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live[id]
	return ok
}

// SweepScratch removes staging directories that no live restore needs:
// those of restores that are not running any more (finished, unknown, or
// queued with a leftover) and those older than StaleAfter, never those
// this process is staging. Other entries of the scratch directory are
// left alone. It returns the number of directories removed.
func (s *Service) SweepScratch(ctx context.Context) (int, error) {
	r, err := os.OpenRoot(s.o.ScratchDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("restore: open the scratch directory: %w", err)
	}
	defer r.Close()
	d, err := r.Open(".")
	if err != nil {
		return 0, fmt.Errorf("restore: read the scratch directory: %w", err)
	}
	entries, err := d.ReadDir(-1)
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("restore: read the scratch directory: %w", err)
	}
	type cand struct {
		id   uuid.UUID
		name string
		info fs.FileInfo
	}
	var cands []cand
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), dirPrefix)
		if !ok {
			continue
		}
		id, err := uuid.Parse(rest)
		if err != nil || dirName(id) != e.Name() || s.isLive(id) {
			continue
		}
		info, err := r.Lstat(e.Name())
		if err != nil {
			continue
		}
		cands = append(cands, cand{id: id, name: e.Name(), info: info})
	}
	removed := 0
	for len(cands) > 0 {
		batch := cands[:min(len(cands), maxSweepIDs)]
		cands = cands[len(batch):]
		ids := make([]uuid.UUID, len(batch))
		for i, c := range batch {
			ids[i] = c.id
		}
		rows, err := sqlc.New(s.o.DB).ListRestoreStatuses(ctx, ids)
		if err != nil {
			return removed, fmt.Errorf("restore: restore statuses: %w", err)
		}
		active := map[uuid.UUID]bool{}
		for _, row := range rows {
			switch row.Status {
			case StatusStaging, StatusVerifying, StatusRestoring:
				active[row.ID] = true
			case StatusQueued, StatusSucceeded, StatusFailed, StatusCanceled:
				// Not running: a directory is a leftover.
			}
		}
		now := s.o.Now()
		var errs []error
		for _, c := range batch {
			if active[c.id] && now.Sub(c.info.ModTime()) < s.o.StaleAfter {
				continue
			}
			if s.isLive(c.id) {
				continue
			}
			if err := r.RemoveAll(c.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			removed++
		}
		if err := errors.Join(errs...); err != nil {
			return removed, fmt.Errorf("restore: sweep: %w", err)
		}
	}
	return removed, nil
}
