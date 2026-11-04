package webdav

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

// CleanupPartials removes temporary ".<hex>.partial" files older than
// olderThan below the destination root (left behind by crashed writers,
// dropped connections or a server account without delete rights) and,
// with the Nextcloud flavor, bekup's own upload collections
// ("bekup-<hex>") of the user older than olderThan. Other clients' upload
// collections are never touched. It runs in no-delete mode too (partials
// are never backups; the server account may still refuse). It returns the
// number removed.
func (d *Destination) CleanupPartials(ctx context.Context, olderThan time.Duration) (int, error) {
	if d.closed.Load() {
		return 0, storage.ErrClosed
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	if err := d.cleanupDir(ctx, lay.files, cutoff, &removed, 0); err != nil {
		return removed, err
	}
	if lay.uploads != nil {
		ups, err := d.readDir(ctx, lay.uploads)
		if err != nil {
			return removed, err
		}
		for i := range ups {
			e := &ups[i]
			if !e.coll || !uploadName.MatchString(e.name()) || e.mod.IsZero() || !e.mod.Before(cutoff) {
				continue
			}
			if err := d.deleteTemp(ctx, e.segs, true); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

func (d *Destination) cleanupDir(ctx context.Context, segs []string, cutoff time.Time, removed *int, depth int) error {
	if depth > storage.MaxSegments {
		return nil
	}
	list, err := d.readDir(ctx, segs)
	if err != nil {
		return err
	}
	for i := range list {
		e := &list[i]
		switch {
		case e.coll && storage.ValidateSegment(e.name()) == nil:
			if err := d.cleanupDir(ctx, e.segs, cutoff, removed, depth+1); err != nil {
				return err
			}
		case !e.coll && partialName.MatchString(e.name()) && !e.mod.IsZero() && e.mod.Before(cutoff):
			if err := d.deleteTemp(ctx, e.segs, false); err != nil {
				return err
			}
			*removed++
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// deleteTemp deletes a temporary resource; a missing one counts as done.
func (d *Destination) deleteTemp(ctx context.Context, segs []string, coll bool) error {
	const op = "remove a temporary file"
	code, _, err := d.do(ctx, op, request{method: http.MethodDelete, segs: segs, coll: coll}, nil)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK, http.StatusNoContent, http.StatusAccepted, http.StatusNotFound:
		return nil
	}
	return statusErr(op, code)
}

var _ storage.DirPruner = (*Destination)(nil)

// PruneEmptyDirs implements storage.DirPruner for run collections only
// ({job slug}/{timestamp}). A WebDAV DELETE of a collection is recursive
// and there is no "delete if empty", so a collection is removed only when
// a fresh PROPFIND Depth 1 shows no member at all (partials included) and
// its modification time is older than olderThan; the DELETE carries the
// collection's strong ETag as If-Match where the server reports one, so a
// server that tracks changes refuses a collection that changed in
// between. Run collections are written only by their own run, so nothing
// writes into an old empty one. Job collections are never removed: a new
// run of the job may create its run collection in them at any moment, and
// the recursive DELETE would take it along. It does not look at no-delete
// mode: the caller decides.
func (d *Destination) PruneEmptyDirs(ctx context.Context, olderThan time.Duration, isJob func(string) bool) (int, error) {
	if d.closed.Load() {
		return 0, storage.ErrClosed
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	jobs, err := d.readDir(ctx, lay.files)
	if err != nil {
		return 0, err
	}
	removed := 0
	for i := range jobs {
		j := &jobs[i]
		if !j.coll || !storage.PruneCandidate(1, j.name(), isJob) {
			continue
		}
		runs, err := d.readDir(ctx, j.segs)
		if err != nil {
			return removed, err
		}
		for k := range runs {
			r := &runs[k]
			if !r.coll || !storage.PruneCandidate(2, r.name(), isJob) || r.mod.IsZero() || !r.mod.Before(cutoff) {
				continue
			}
			ok, err := d.removeEmptyCollection(ctx, r.segs, cutoff)
			if err != nil {
				return removed, err
			}
			if ok {
				removed++
			}
		}
		if err := ctx.Err(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// removeEmptyCollection deletes segs if it is still an empty collection
// older than cutoff.
func (d *Destination) removeEmptyCollection(ctx context.Context, segs []string, cutoff time.Time) (bool, error) {
	entries, err := d.propfind(ctx, "check a folder", segs, true, "1")
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	kids, self := children(entries, segs)
	if self == nil || !self.coll || len(kids) > 0 || len(entries) != 1 || self.mod.IsZero() || !self.mod.Before(cutoff) {
		return false, nil
	}
	var h http.Header
	if self.etag != "" && self.etag[0] == '"' {
		h = http.Header{"If-Match": {self.etag}}
	}
	code, _, err := d.do(ctx, "remove an empty folder", request{method: http.MethodDelete, segs: segs, coll: true, header: h}, nil)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK, http.StatusNoContent, http.StatusAccepted:
		return true, nil
	case http.StatusNotFound, http.StatusPreconditionFailed, http.StatusLocked:
		return false, nil
	}
	return false, statusErr("remove an empty folder", code)
}
