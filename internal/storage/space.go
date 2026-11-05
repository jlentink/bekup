package storage

import (
	"context"
	"fmt"
	"math"
)

// Space is what a backend reports about the storage space behind a
// destination (D-225). Each value is meaningful only when its Known flag
// is set: servers report some, all or none of them. Total and Free are
// the file system's (disk, SFTP) or the account quota's (WebDAV); Used is
// everything stored there, not only bekup's backups.
type Space struct {
	Total, Free, Used                int64
	TotalKnown, FreeKnown, UsedKnown bool
}

// Known reports whether the backend reported any value.
func (s Space) Known() bool { return s.TotalKnown || s.FreeKnown || s.UsedKnown }

// SpaceReporter is implemented by backends that can report their space:
// disk (statfs on the destination directory), SFTP (the
// statvfs@openssh.com extension) and WebDAV (RFC 4331 quota properties).
// S3 has no such concept and does not implement it. Space fails with
// ErrUnsupported when this server does not report it (no extension, no
// quota properties); other errors are classified like any backend error
// (show only Classify(err), never the error text).
type SpaceReporter interface {
	Space(ctx context.Context) (Space, error)
}

// BlockSpace builds a Space from file system block counts (statfs,
// statvfs): total = blocks × bsize, free = bavail × bsize (the space
// available to the unprivileged bekup user) and used = (blocks − bfree)
// × bsize. Values beyond int64 are capped at math.MaxInt64; a
// non-positive block size, negative counts or more free than total blocks
// are refused.
func BlockSpace[A, F, V, B statfsInt](blocks A, bfree F, bavail V, bsize B) (Space, error) {
	if blocks < 0 || bfree < 0 || bavail < 0 || uint64(bfree) > uint64(blocks) || uint64(bavail) > uint64(blocks) {
		return Space{}, errStatfsValues
	}
	total, err := statfsFree(blocks, bsize)
	if err != nil {
		return Space{}, err
	}
	free, err := statfsFree(bavail, bsize)
	if err != nil {
		return Space{}, err
	}
	used, err := statfsFree(uint64(blocks)-uint64(bfree), bsize)
	if err != nil {
		return Space{}, err
	}
	return Space{
		Total: capInt64(total), Free: capInt64(free), Used: capInt64(used),
		TotalKnown: true, FreeKnown: true, UsedKnown: true,
	}, nil
}

func capInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// Space implements SpaceReporter: statfs on the destination directory
// (ErrUnsupported on platforms without statfs).
func (d *Disk) Space(ctx context.Context) (Space, error) {
	if d.closed.Load() {
		return Space{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return Space{}, err
	}
	f, err := d.dir.Open(".")
	if err != nil {
		return Space{}, fmt.Errorf("storage: open directory: %w", err)
	}
	sp, serr := fileSpace(f)
	cerr := f.Close()
	if serr != nil {
		return Space{}, serr
	}
	if cerr != nil {
		return Space{}, fmt.Errorf("storage: close directory: %w", cerr)
	}
	return sp, nil
}

var _ SpaceReporter = (*Disk)(nil)
