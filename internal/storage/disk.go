package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Disk file modes: backups are readable by the bekup user only.
const (
	diskFileMode os.FileMode = 0o600
	diskDirMode  os.FileMode = 0o700
)

// DiskConfig configures a disk destination.
type DiskConfig struct {
	// Root is storage.disk_root: an existing absolute directory that
	// confines every disk destination (os.Root).
	Root string
	// Dir is the destination's optional subdirectory below Root,
	// validated like a key prefix; it is created (0700) if missing.
	Dir string
	// NoDelete refuses every Delete with ErrDeleteNotPermitted.
	NoDelete bool
}

// Disk is a destination on the local file system. Every file system access
// goes through an os.Root opened on DiskConfig.Root/Dir, so no key (and no
// symbolic link) can reach outside it. Objects are written to a temporary
// ".<random>.partial" file in the target directory and finalized with a
// hard link (which fails if the key exists), so a partial object is never
// visible under its key and existing objects are never overwritten.
//
// Symbolic links are never followed for objects and directories that bekup
// reads, lists or deletes: they are reported as missing (Get, Stat) or
// skipped (List).
type Disk struct {
	root     *os.Root // DiskConfig.Root
	dir      *os.Root // DiskConfig.Root/Dir
	noDelete bool
	closed   atomic.Bool
}

var _ Destination = (*Disk)(nil)

// OpenDisk opens a disk destination.
func OpenDisk(cfg DiskConfig) (*Disk, error) {
	if cfg.Root == "" || !filepath.IsAbs(cfg.Root) {
		return nil, fmt.Errorf("%w: disk root must be an absolute path", ErrInvalidArgument)
	}
	dirKey, err := ParsePrefix(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("disk directory: %w", err)
	}
	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("open disk root: %w", err)
	}
	name := "."
	if !dirKey.IsZero() {
		name = dirKey.String()
		if err := root.MkdirAll(name, diskDirMode); err != nil {
			return nil, errors.Join(fmt.Errorf("create disk directory: %w", err), root.Close())
		}
		if err := checkNoSymlinkDirs(root, name); err != nil {
			return nil, errors.Join(fmt.Errorf("disk directory: %w", err), root.Close())
		}
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open disk directory: %w", err), root.Close())
	}
	return &Disk{root: root, dir: dir, noDelete: cfg.NoDelete}, nil
}

// Capabilities implements Destination.
func (d *Disk) Capabilities() Capabilities {
	return Capabilities{
		Type:            "disk",
		NoDeleteMode:    d.noDelete,
		AtomicRename:    true,
		ExclusiveCreate: true,
	}
}

// Close implements Destination.
func (d *Disk) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	return errors.Join(d.dir.Close(), d.root.Close())
}

func (d *Disk) check(ctx context.Context, key Key) error {
	if d.closed.Load() {
		return ErrClosed
	}
	if key.IsZero() {
		return fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	return ctx.Err()
}

// partialName matches the temporary files of Put.
var partialName = regexp.MustCompile(`^\.[0-9a-f]{32}\.partial$`)

func newPartialName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("temporary name: %w", err)
	}
	return "." + hex.EncodeToString(b[:]) + ".partial", nil
}

// Put implements Destination.
func (d *Disk) Put(ctx context.Context, key Key, r io.Reader, opts PutOptions) (ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return ObjectInfo{}, err
	}
	if err := CheckPutOptions(d.Capabilities(), opts); err != nil {
		return ObjectInfo{}, err
	}
	final := key.String()
	parent := key.Dir().String()
	if _, err := d.dir.Lstat(final); err == nil {
		return ObjectInfo{}, ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ObjectInfo{}, fmt.Errorf("storage: stat object: %w", err)
	}
	if opts.SizeHint > 0 {
		if err := d.checkFreeSpace(uint64(opts.SizeHint)); err != nil {
			return ObjectInfo{}, err
		}
	}

	tmp, f, err := d.createPartial(parent)
	if err != nil {
		return ObjectInfo{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()         //nolint:gosec // cleanup path; the file is removed next
			_ = d.dir.Remove(tmp) //nolint:errcheck,gosec // best effort; CleanupPartials removes leftovers
		}
	}()

	n, sum, err := CopyHashed(ctx, f, r, 0)
	if err != nil {
		return ObjectInfo{}, err
	}
	if err := VerifySHA256(opts.ExpectedSHA256, sum); err != nil {
		return ObjectInfo{}, err
	}
	if err := f.Sync(); err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: sync object: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: stat object: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if err := d.finalize(tmp, final); err != nil {
		return ObjectInfo{}, err
	}
	committed = true
	cerr := f.Close()
	_ = d.dir.Remove(tmp) //nolint:errcheck,gosec // a leftover hard link is removed by CleanupPartials
	if cerr != nil {
		return ObjectInfo{}, fmt.Errorf("storage: close object: %w", cerr)
	}
	if err := d.syncDirs(parent); err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key:         key,
		Size:        n,
		ModTime:     st.ModTime(),
		SHA256:      sum,
		ContentType: opts.ContentType,
	}, nil
}

// createPartial creates the parent directories and an exclusive temporary
// file. A concurrent Delete may remove an empty parent between MkdirAll and
// the create, so ENOENT is retried a few times.
func (d *Disk) createPartial(parent string) (string, *os.File, error) {
	var lastErr error
	for range 3 {
		if parent != "" {
			if err := d.dir.MkdirAll(parent, diskDirMode); err != nil {
				return "", nil, fmt.Errorf("storage: create directory: %w", err)
			}
			if err := checkNoSymlinkDirs(d.dir, parent); err != nil {
				return "", nil, err
			}
		}
		name, err := newPartialName()
		if err != nil {
			return "", nil, err
		}
		tmp := path.Join(parent, name)
		f, err := d.dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, diskFileMode)
		if err == nil {
			return tmp, f, nil
		}
		lastErr = err
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	return "", nil, fmt.Errorf("storage: create temporary file: %w", lastErr)
}

// finalize publishes tmp under final without ever replacing an existing
// file: a hard link fails with EEXIST. File systems without hard links fall
// back to Lstat + Rename, which has a small check-then-act window.
func (d *Disk) finalize(tmp, final string) error {
	err := d.dir.Link(tmp, final)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return ErrAlreadyExists
	case !linkUnsupported(err):
		return fmt.Errorf("storage: finalize object: %w", err)
	}
	if _, err := d.dir.Lstat(final); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: finalize object: %w", err)
	}
	if err := d.dir.Rename(tmp, final); err != nil {
		return fmt.Errorf("storage: finalize object: %w", err)
	}
	return nil
}

func linkUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EMLINK)
}

// syncDirs fsyncs dir and each of its ancestors up to the destination
// directory, so new directory entries survive a crash.
func (d *Disk) syncDirs(dir string) error {
	for {
		name := dir
		if name == "" {
			name = "."
		}
		f, err := d.dir.Open(name)
		if err != nil {
			return fmt.Errorf("storage: open directory: %w", err)
		}
		serr := f.Sync()
		cerr := f.Close()
		if serr != nil && !errors.Is(serr, syscall.EINVAL) && !errors.Is(serr, errors.ErrUnsupported) {
			return fmt.Errorf("storage: sync directory: %w", serr)
		}
		if cerr != nil {
			return fmt.Errorf("storage: close directory: %w", cerr)
		}
		if dir == "" {
			return nil
		}
		dir = parentDir(dir)
	}
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// checkNoSymlinkDirs verifies that every component of the relative
// directory p is a real directory, not a symbolic link. os.Root already
// confines links to the root; this additionally keeps bekup from writing
// through links an attacker with write access below the root planted.
func checkNoSymlinkDirs(r *os.Root, p string) error {
	cur := ""
	for seg := range strings.SplitSeq(p, "/") {
		cur = path.Join(cur, seg)
		fi, err := r.Lstat(cur)
		if err != nil {
			return fmt.Errorf("storage: stat directory: %w", err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%w: a path component is not a directory", ErrPermission)
		}
	}
	return nil
}

// lstatRegular returns the object's file info if key names a regular file
// reached only through real directories.
func (d *Disk) lstatRegular(key Key) (fs.FileInfo, error) {
	if parent := key.Dir().String(); parent != "" {
		if err := checkNoSymlinkDirs(d.dir, parent); err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrPermission) {
				return nil, ErrNotFound
			}
			return nil, err
		}
	}
	fi, err := d.dir.Lstat(key.String())
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: stat object: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	return fi, nil
}

func diskInfo(key Key, fi fs.FileInfo) ObjectInfo {
	return ObjectInfo{Key: key, Size: fi.Size(), ModTime: fi.ModTime()}
}

// Get implements Destination.
func (d *Disk) Get(ctx context.Context, key Key) (io.ReadCloser, ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return nil, ObjectInfo{}, err
	}
	fi, err := d.lstatRegular(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := d.dir.OpenFile(key.String(), os.O_RDONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ObjectInfo{}, ErrNotFound
	}
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("storage: open object: %w", err)
	}
	ofi, err := f.Stat()
	if err != nil || !os.SameFile(fi, ofi) || !ofi.Mode().IsRegular() {
		_ = f.Close() //nolint:gosec // the object changed under us; report it missing
		if err != nil {
			return nil, ObjectInfo{}, fmt.Errorf("storage: stat object: %w", err)
		}
		return nil, ObjectInfo{}, ErrNotFound
	}
	return f, diskInfo(key, ofi), nil
}

// Stat implements Destination.
func (d *Disk) Stat(ctx context.Context, key Key) (ObjectInfo, error) {
	if err := d.check(ctx, key); err != nil {
		return ObjectInfo{}, err
	}
	fi, err := d.lstatRegular(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	return diskInfo(key, fi), nil
}

// Delete implements Destination.
func (d *Disk) Delete(ctx context.Context, key Key) error {
	if err := d.check(ctx, key); err != nil {
		return err
	}
	if d.noDelete {
		return fmt.Errorf("%w: no-delete mode", ErrDeleteNotPermitted)
	}
	if _, err := d.lstatRegular(key); errors.Is(err, ErrNotFound) {
		// Missing, or not an object bekup could have written: nothing
		// to delete. Symbolic links and other non-regular entries are
		// left alone.
		return nil
	} else if err != nil {
		return err
	}
	if err := d.dir.Remove(key.String()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete object: %w", err)
	}
	parent := key.Dir().String()
	// Remove now-empty parent directories (never the destination
	// directory itself); a non-empty one ends the walk.
	for p := parent; p != ""; p = parentDir(p) {
		if err := d.dir.Remove(p); err != nil {
			break
		}
	}
	// Sync the deepest directory that still exists.
	p := parent
	for p != "" {
		if _, err := d.dir.Lstat(p); err == nil {
			break
		}
		p = parentDir(p)
	}
	return d.syncDirs(p)
}

// List implements Destination.
func (d *Disk) List(ctx context.Context, prefix Key, opts ListOptions) (ListPage, error) {
	if d.closed.Load() {
		return ListPage{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return ListPage{}, err
	}
	limit := opts.pageSize()
	l := &diskLister{ctx: ctx, r: d.dir, after: opts.StartAfter.String(), limit: limit + 1}
	start := "."
	if !prefix.IsZero() {
		fi, err := d.dir.Lstat(prefix.String())
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || (err == nil && !fi.IsDir()) {
			return ListPage{}, nil
		}
		if err != nil {
			return ListPage{}, fmt.Errorf("storage: stat prefix: %w", err)
		}
		if err := checkNoSymlinkDirs(d.dir, prefix.String()); err != nil {
			return ListPage{}, nil //nolint:nilerr // a symlinked prefix lists nothing
		}
		start = prefix.String()
	}
	if err := l.walk(start, prefix.String()); err != nil {
		return ListPage{}, err
	}
	page := ListPage{Objects: l.out}
	if len(page.Objects) > limit {
		page.Objects = page.Objects[:limit]
		page.More = true
	}
	if n := len(page.Objects); n > 0 {
		page.Next = page.Objects[n-1].Key
	}
	return page, nil
}

type diskLister struct {
	ctx   context.Context
	r     *os.Root
	after string
	limit int
	out   []ObjectInfo
}

func (l *diskLister) full() bool { return len(l.out) >= l.limit }

type listEntry struct {
	name  string
	sort  string
	isDir bool
}

// walk lists dir (relative path, "." for the root) whose key prefix is
// keyPrefix ("" for the root) in S3 key order: entries are sorted by name,
// directories as name+"/", which makes the depth-first walk produce keys
// in byte-wise lexicographic order.
func (l *diskLister) walk(dir, keyPrefix string) error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	f, err := l.r.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: open directory: %w", err)
	}
	ents, err := f.ReadDir(-1)
	cerr := f.Close()
	if err != nil {
		return fmt.Errorf("storage: read directory: %w", err)
	}
	if cerr != nil {
		return fmt.Errorf("storage: close directory: %w", cerr)
	}
	list := make([]listEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if ValidateSegment(name) != nil {
			continue // dot names (partials), foreign names
		}
		t := e.Type()
		switch {
		case t.IsDir():
			list = append(list, listEntry{name: name, sort: name + "/", isDir: true})
		case t.IsRegular():
			list = append(list, listEntry{name: name, sort: name})
		}
		// Symbolic links, devices, sockets and pipes are skipped.
	}
	slices.SortFunc(list, func(a, b listEntry) int { return strings.Compare(a.sort, b.sort) })
	for _, e := range list {
		if l.full() {
			return nil
		}
		key := e.name
		if keyPrefix != "" {
			key = keyPrefix + "/" + e.name
		}
		if len(key) > MaxKeyLen {
			continue
		}
		if e.isDir {
			sub := key + "/"
			if sub <= l.after && !strings.HasPrefix(l.after, sub) {
				continue // every key below is <= StartAfter
			}
			if strings.Count(sub, "/") >= MaxSegments {
				continue
			}
			if err := l.walk(key, key); err != nil {
				return err
			}
			continue
		}
		if key <= l.after {
			continue
		}
		k, err := ParseKey(key)
		if err != nil {
			continue
		}
		fi, err := l.r.Lstat(key)
		if err != nil || !fi.Mode().IsRegular() {
			continue // removed or replaced concurrently
		}
		l.out = append(l.out, diskInfo(k, fi))
	}
	return nil
}

// Test implements Destination.
func (d *Disk) Test(ctx context.Context, prefix Key) TestResult {
	return RunProbe(ctx, d, prefix)
}

// CleanupPartials removes temporary ".<hex>.partial" files older than
// olderThan, left behind by crashed writers. It runs in no-delete mode
// too: partial files are never backups. It returns the number removed.
func (d *Disk) CleanupPartials(ctx context.Context, olderThan time.Duration) (int, error) {
	if d.closed.Load() {
		return 0, ErrClosed
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := d.cleanupDir(ctx, ".", "", cutoff, &removed, 0)
	return removed, err
}

func (d *Disk) cleanupDir(ctx context.Context, dir, rel string, cutoff time.Time, removed *int, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > MaxSegments {
		return nil
	}
	f, err := d.dir.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: open directory: %w", err)
	}
	ents, err := f.ReadDir(-1)
	cerr := f.Close()
	if err != nil {
		return fmt.Errorf("storage: read directory: %w", err)
	}
	if cerr != nil {
		return fmt.Errorf("storage: close directory: %w", cerr)
	}
	for _, e := range ents {
		name := e.Name()
		p := name
		if rel != "" {
			p = rel + "/" + name
		}
		switch {
		case e.Type().IsDir() && ValidateSegment(name) == nil:
			if err := d.cleanupDir(ctx, p, p, cutoff, removed, depth+1); err != nil {
				return err
			}
		case e.Type().IsRegular() && partialName.MatchString(name):
			fi, err := d.dir.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
				continue
			}
			if err := d.dir.Remove(p); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("storage: remove partial file: %w", err)
			}
			*removed++
		}
	}
	return nil
}
