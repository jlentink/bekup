package storage

import (
	"context"
	"strings"
	"time"
)

// Layout is the object layout bekup writes below a destination's root:
// {job slug}/{run timestamp}/{file} (BuildKey without a prefix; the
// destination's own prefix or directory is applied by the backend).

// LayoutKey is a key of bekup's layout, split into its parts.
type LayoutKey struct {
	JobSlug string
	// Time is the run timestamp of the directory (UTC, seconds).
	Time time.Time
	// RunDir is "{job slug}/{timestamp}" (the run's directory).
	RunDir string
	File   string
}

// ParseLayoutKey reports whether k is a key of bekup's layout: exactly
// three segments, a valid job slug, a run timestamp segment and a file
// name. Probe objects (bekup-probe/{name}) and foreign keys are not.
func ParseLayoutKey(k Key) (LayoutKey, bool) {
	segs := k.Segments()
	if len(segs) != 3 {
		return LayoutKey{}, false
	}
	if ValidateJobSlug(segs[0]) != nil {
		return LayoutKey{}, false
	}
	t, err := ParseTimestampSegment(segs[1])
	if err != nil || TimestampSegment(t) != segs[1] {
		return LayoutKey{}, false
	}
	return LayoutKey{JobSlug: segs[0], Time: t, RunDir: segs[0] + "/" + segs[1], File: segs[2]}, true
}

// RunDir returns the run directory "{job slug}/{timestamp}" of a run.
func RunDir(jobSlug string, runTimestamp time.Time) string {
	return jobSlug + "/" + TimestampSegment(runTimestamp)
}

// DirPruner is implemented by the backends with real directories (disk and
// SFTP). PruneEmptyDirs removes empty directories of bekup's layout: run
// directories ({job slug}/{timestamp}) and job directories ({job slug})
// whose modification time is older than olderThan, and only for job slugs
// isJob accepts (nil: every valid slug). It never removes the destination
// root, never follows or removes symbolic links, never descends deeper than
// the run directories and never removes a non-empty directory. It returns
// the number of directories removed.
type DirPruner interface {
	PruneEmptyDirs(ctx context.Context, olderThan time.Duration, isJob func(slug string) bool) (int, error)
}

// PruneCandidate reports whether a directory entry name at depth (1: job
// directory, 2: run directory) belongs to bekup's layout.
func PruneCandidate(depth int, name string, isJob func(string) bool) bool {
	switch depth {
	case 1:
		if ValidateJobSlug(name) != nil {
			return false
		}
		return isJob == nil || isJob(name)
	case 2:
		t, err := ParseTimestampSegment(name)
		return err == nil && TimestampSegment(t) == name && !strings.Contains(name, "/")
	default:
		return false
	}
}
