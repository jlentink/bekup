package sftp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pkgsftp "github.com/pkg/sftp"

	"github.com/jlentink/bekup/internal/storage"
)

// Destination is an SFTP backup destination.
//
// It keeps one SSH connection (opened lazily on first use, re-opened after
// it died) shared by all concurrent operations. Object paths are the
// resolved base directory plus the validated key; every directory between
// the base and an object is checked with Lstat to be a real directory, and
// objects must be regular files, so server-side symbolic links below the
// base are refused (Put) or treated as missing (Get, Stat, Delete) or
// skipped (List).
//
// Residual trust in the server: SFTP has no O_NOFOLLOW or openat, so a
// party that can modify the directory tree on the server concurrently can
// swap a checked directory for a symbolic link between the check and the
// use (TOCTOU). The server itself is trusted to enforce its own
// permissions and chroot; use a dedicated, chrooted account that nobody
// else writes to. Data integrity against a malicious server comes from
// age encryption and the signed manifest, not from this backend.
//
// Operations honor ctx between SFTP requests and during streaming; a
// single request that hangs is ended by the keepalive (3 missed replies
// close the connection).
type Destination struct {
	s      *settings
	creds  *credentials
	dialMu chan struct{} // 1-slot semaphore serializing (re)connects
	mu     sync.Mutex
	cur    *session
	closed atomic.Bool
	// exclusive reports that the last session had hardlink@openssh.com.
	exclusive atomic.Bool
	// testNoHardlink disables hardlink@openssh.com (tests only).
	testNoHardlink bool
	// fallbackWarned limits the no-hardlink warning to once.
	fallbackWarned atomic.Bool
}

var _ storage.Destination = (*Destination)(nil)

// Open validates cfg and returns a destination. It does not connect: the
// first operation (or Test) does, so connection problems are reported as
// normalized errors. Open takes ownership of cfg.Auth (see Auth).
func Open(cfg Config) (*Destination, error) {
	defer func() {
		cfg.Auth.PrivateKey.Destroy()
		cfg.Auth.Passphrase.Destroy()
	}()
	s, err := cfg.validate()
	if err != nil {
		cfg.Auth.destroy()
		return nil, err
	}
	creds, err := parseAuth(&cfg.Auth)
	if err != nil {
		cfg.Auth.destroy()
		return nil, err
	}
	return &Destination{s: s, creds: creds, dialMu: make(chan struct{}, 1)}, nil
}

// Capabilities implements storage.Destination.
func (d *Destination) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Type:         "sftp",
		NoDeleteMode: d.s.noDelete,
		AtomicRename: true,
		// Atomic only with hardlink@openssh.com (known after the first
		// connection); the rename fallback relies on the server honoring
		// SFTP v3 rename semantics.
		ExclusiveCreate: d.exclusive.Load(),
	}
}

// Close implements storage.Destination. It closes the connection and
// destroys the credentials.
func (d *Destination) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	// Wait for a dial in progress.
	d.dialMu <- struct{}{}
	defer func() { <-d.dialMu }()
	d.mu.Lock()
	sess := d.cur
	d.cur = nil
	d.mu.Unlock()
	var err error
	if sess != nil {
		err = sess.close()
	}
	d.creds.destroy()
	return err
}

// session returns a live session, connecting if needed.
func (d *Destination) session(ctx context.Context) (*session, error) {
	if d.closed.Load() {
		return nil, storage.ErrClosed
	}
	if s := d.live(); s != nil {
		return s, nil
	}
	select {
	case d.dialMu <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.dialMu }()
	if d.closed.Load() {
		return nil, storage.ErrClosed
	}
	if s := d.live(); s != nil {
		return s, nil
	}
	d.mu.Lock()
	old := d.cur
	d.cur = nil
	d.mu.Unlock()
	if old != nil {
		discard(old.close())
	}
	sess, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	d.exclusive.Store(sess.hardlink)
	d.mu.Lock()
	d.cur = sess
	d.mu.Unlock()
	return sess, nil
}

func (d *Destination) live() *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur != nil && d.cur.alive() {
		return d.cur
	}
	return nil
}

func (d *Destination) check(ctx context.Context, key storage.Key) (*session, error) {
	if d.closed.Load() {
		return nil, storage.ErrClosed
	}
	if key.IsZero() {
		return nil, fmt.Errorf("%w: empty key", storage.ErrInvalidKey)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.session(ctx)
}

// partialName matches the temporary files of Put. Dot names are never
// valid key segments, so partials are never listed as objects.
var partialName = regexp.MustCompile(`^\.[0-9a-f]{32}\.partial$`)

func newPartialName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("temporary name: %w", err)
	}
	return "." + hex.EncodeToString(b[:]) + ".partial", nil
}

// Put implements storage.Destination.
func (d *Destination) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if err := storage.CheckPutOptions(d.Capabilities(), opts); err != nil {
		return storage.ObjectInfo{}, err
	}
	sess, err := d.check(ctx, key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	final := sess.path(key.String())
	parent := key.Dir().String()

	tmp, f, err := createPartial(sess, parent)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if _, err := sess.sftp.Lstat(final); err == nil {
		discard(f.Close())
		discard(sess.sftp.Remove(tmp))
		return storage.ObjectInfo{}, storage.ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		discard(f.Close())
		discard(sess.sftp.Remove(tmp))
		return storage.ObjectInfo{}, mapErr("sftp: stat object", err)
	}
	committed, fileClosed := false, false
	defer func() {
		if committed {
			return
		}
		if !fileClosed {
			discard(f.Close())
		}
		// Best effort; CleanupPartials removes leftovers (for example
		// after the connection died).
		discard(sess.sftp.Remove(tmp))
	}()

	n, sum, err := storage.CopyHashed(ctx, f, r, 0)
	if err != nil {
		return storage.ObjectInfo{}, mapErr("sftp: write object", err)
	}
	if err := storage.VerifySHA256(opts.ExpectedSHA256, sum); err != nil {
		return storage.ObjectInfo{}, err
	}
	if sess.fsync {
		if err := f.Sync(); err != nil {
			return storage.ObjectInfo{}, mapErr("sftp: sync object", err)
		}
	}
	st, err := f.Stat()
	if err != nil {
		return storage.ObjectInfo{}, mapErr("sftp: stat object", err)
	}
	fileClosed = true
	if err := f.Close(); err != nil {
		return storage.ObjectInfo{}, mapErr("sftp: close object", err)
	}
	if st.Size() != n {
		return storage.ObjectInfo{}, fmt.Errorf("%w: the server stored %d of %d bytes", storage.ErrChecksumMismatch, st.Size(), n)
	}
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := d.finalize(sess, tmp, final); err != nil {
		return storage.ObjectInfo{}, err
	}
	committed = true
	return storage.ObjectInfo{
		Key:         key,
		Size:        n,
		ModTime:     st.ModTime(),
		SHA256:      sum,
		ContentType: opts.ContentType,
	}, nil
}

// createPartial creates the parent directories and an exclusive temporary
// file (0600). A concurrent Delete may remove an empty parent between
// mkdirs and the create, so a missing parent is retried a few times.
func createPartial(sess *session, parent string) (string, *pkgsftp.File, error) {
	var lastErr error
	for range 3 {
		if parent != "" {
			if err := mkdirs(sess, parent); err != nil {
				return "", nil, err
			}
		}
		name, err := newPartialName()
		if err != nil {
			return "", nil, err
		}
		rel := name
		if parent != "" {
			rel = parent + "/" + name
		}
		tmp := sess.path(rel)
		f, err := sess.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err == nil {
			if err := f.Chmod(0o600); err != nil {
				discard(f.Close())
				discard(sess.sftp.Remove(tmp))
				return "", nil, mapErr("sftp: chmod temporary file", err)
			}
			return tmp, f, nil
		}
		lastErr = err
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	return "", nil, mapErr("sftp: create temporary file", lastErr)
}

// finalize publishes tmp under final without replacing an existing object.
//
// With hardlink@openssh.com (OpenSSH and most servers) the link fails if
// final exists, which makes "never overwrite" atomic; the temporary name
// is removed afterwards. Without it bekup checks that final is missing and
// uses SSH_FXP_RENAME, which SFTP v3 specifies to fail on an existing
// target; a server that implements it as an overwriting POSIX rename
// leaves a small check-then-rename race. posix-rename@openssh.com is never
// used: it overwrites by design.
func (d *Destination) finalize(sess *session, tmp, final string) error {
	if sess.hardlink {
		err := sess.sftp.Link(tmp, final)
		if err == nil {
			discard(sess.sftp.Remove(tmp)) // a leftover link is removed by CleanupPartials
			return nil
		}
		if _, lerr := sess.sftp.Lstat(final); lerr == nil {
			return storage.ErrAlreadyExists
		}
		return mapErr("sftp: finalize object", err)
	}
	if !d.fallbackWarned.Swap(true) {
		d.s.logger.Warn("sftp_finalize_rename_fallback")
	}
	if _, err := sess.sftp.Lstat(final); err == nil {
		return storage.ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return mapErr("sftp: stat object", err)
	}
	if err := sess.sftp.Rename(tmp, final); err != nil {
		if _, lerr := sess.sftp.Lstat(final); lerr == nil {
			return storage.ErrAlreadyExists
		}
		return mapErr("sftp: finalize object", err)
	}
	return nil
}

// lstatRegular returns the object's file info if key names a regular file
// reached only through real directories.
func lstatRegular(sess *session, key storage.Key) (os.FileInfo, error) {
	if err := checkDirs(sess, key.Dir().String()); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, storage.ErrNotFound) || errors.Is(err, errNotDir) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	fi, err := sess.sftp.Lstat(sess.path(key.String()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, mapErr("sftp: stat object", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, storage.ErrNotFound
	}
	return fi, nil
}

func info(key storage.Key, fi os.FileInfo) storage.ObjectInfo {
	return storage.ObjectInfo{Key: key, Size: fi.Size(), ModTime: fi.ModTime()}
}

// Get implements storage.Destination.
func (d *Destination) Get(ctx context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	sess, err := d.check(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	fi, err := lstatRegular(sess, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	f, err := sess.sftp.Open(sess.path(key.String()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	}
	if err != nil {
		return nil, storage.ObjectInfo{}, mapErr("sftp: open object", err)
	}
	// SFTP cannot open without following links; compare what was opened
	// with what Lstat saw (no inode numbers in SFTP v3).
	ofi, err := f.Stat()
	if err != nil || !ofi.Mode().IsRegular() || ofi.Size() != fi.Size() || !ofi.ModTime().Equal(fi.ModTime()) {
		discard(f.Close())
		if err != nil {
			return nil, storage.ObjectInfo{}, mapErr("sftp: stat object", err)
		}
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	}
	return &reader{ctx: ctx, f: f}, info(key, ofi), nil
}

// reader checks ctx before every read.
type reader struct {
	ctx context.Context
	f   *pkgsftp.File
}

func (r *reader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.f.Read(b)
}

func (r *reader) Close() error { return r.f.Close() }

// Stat implements storage.Destination.
func (d *Destination) Stat(ctx context.Context, key storage.Key) (storage.ObjectInfo, error) {
	sess, err := d.check(ctx, key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	fi, err := lstatRegular(sess, key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	return info(key, fi), nil
}

// Delete implements storage.Destination.
func (d *Destination) Delete(ctx context.Context, key storage.Key) error {
	if d.s.noDelete {
		if d.closed.Load() {
			return storage.ErrClosed
		}
		if key.IsZero() {
			return fmt.Errorf("%w: empty key", storage.ErrInvalidKey)
		}
		return fmt.Errorf("%w: no-delete mode", storage.ErrDeleteNotPermitted)
	}
	sess, err := d.check(ctx, key)
	if err != nil {
		return err
	}
	if _, err := lstatRegular(sess, key); errors.Is(err, storage.ErrNotFound) {
		// Missing, or not an object bekup could have written (symbolic
		// links and other non-regular entries are left alone).
		return nil
	} else if err != nil {
		return err
	}
	if err := sess.sftp.Remove(sess.path(key.String())); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return mapErr("sftp: delete object", err)
	}
	removeEmptyParents(sess, key.Dir().String())
	return nil
}

// removeEmptyParents removes now-empty directories from dir upwards (never
// the destination root); the first failure (normally "not empty") ends
// the walk.
func removeEmptyParents(sess *session, dir string) {
	for p := dir; p != ""; p = parentDir(p) {
		if sess.sftp.RemoveDirectory(sess.path(p)) != nil {
			return
		}
	}
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// List implements storage.Destination.
func (d *Destination) List(ctx context.Context, prefix storage.Key, opts storage.ListOptions) (storage.ListPage, error) {
	if d.closed.Load() {
		return storage.ListPage{}, storage.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return storage.ListPage{}, err
	}
	sess, err := d.session(ctx)
	if err != nil {
		return storage.ListPage{}, err
	}
	limit := opts.PageSize
	if limit <= 0 || limit > storage.MaxListPageSize {
		limit = storage.MaxListPageSize
	}
	if !prefix.IsZero() {
		if err := checkDirs(sess, prefix.String()); err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, storage.ErrNotFound) || errors.Is(err, errNotDir) {
				return storage.ListPage{}, nil // missing, not a directory or a symlink
			}
			return storage.ListPage{}, err
		}
	}
	l := &lister{ctx: ctx, sess: sess, after: opts.StartAfter.String(), limit: limit + 1}
	if err := l.walk(prefix.String()); err != nil {
		return storage.ListPage{}, err
	}
	page := storage.ListPage{Objects: l.out}
	if len(page.Objects) > limit {
		page.Objects = page.Objects[:limit]
		page.More = true
	}
	if n := len(page.Objects); n > 0 {
		page.Next = page.Objects[n-1].Key
	}
	return page, nil
}

type lister struct {
	ctx   context.Context
	sess  *session
	after string
	limit int
	out   []storage.ObjectInfo
}

type listEntry struct {
	name  string
	sort  string
	isDir bool
	fi    os.FileInfo
}

// readDir lists dir and keeps valid key segments that are real
// directories or regular files (SFTP READDIR attributes are lstat
// attributes, so symbolic links show up as links and are skipped).
func readDir(ctx context.Context, sess *session, dir string) ([]listEntry, error) {
	ents, err := sess.sftp.ReadDirContext(ctx, sess.path(dir))
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, mapErr("sftp: read directory", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	list := make([]listEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		m := e.Mode()
		switch {
		case m.IsDir():
			list = append(list, listEntry{name: name, sort: name + "/", isDir: true, fi: e})
		case m.IsRegular():
			list = append(list, listEntry{name: name, sort: name, fi: e})
		}
	}
	slices.SortFunc(list, func(a, b listEntry) int { return strings.Compare(a.sort, b.sort) })
	return list, nil
}

// walk lists the relative directory keyPrefix ("" for the root) in S3 key
// order: entries sorted by name, directories as name+"/", so the
// depth-first walk yields keys in byte-wise lexicographic order. Memory is
// bounded by the page size plus one directory listing per level.
func (l *lister) walk(keyPrefix string) error {
	list, err := readDir(l.ctx, l.sess, keyPrefix)
	if err != nil {
		return err
	}
	for _, e := range list {
		if len(l.out) >= l.limit {
			return nil
		}
		if storage.ValidateSegment(e.name) != nil {
			continue // dot names (partials), foreign names
		}
		key := e.name
		if keyPrefix != "" {
			key = keyPrefix + "/" + e.name
		}
		if len(key) > storage.MaxKeyLen {
			continue
		}
		if e.isDir {
			sub := key + "/"
			if sub <= l.after && !strings.HasPrefix(l.after, sub) {
				continue // every key below is <= StartAfter
			}
			if strings.Count(sub, "/") >= storage.MaxSegments {
				continue
			}
			if err := l.walk(key); err != nil {
				return err
			}
			continue
		}
		if key <= l.after {
			continue
		}
		k, err := storage.ParseKey(key)
		if err != nil {
			continue
		}
		l.out = append(l.out, info(k, e.fi))
	}
	return nil
}

// Test implements storage.Destination.
func (d *Destination) Test(ctx context.Context, prefix storage.Key) storage.TestResult {
	return storage.RunProbe(ctx, d, prefix)
}

// CleanupPartials removes temporary ".<hex>.partial" files older than
// olderThan, left behind by crashed writers or dropped connections. It
// runs in no-delete mode too (partials are never backups; the server
// account may still refuse). It returns the number removed.
func (d *Destination) CleanupPartials(ctx context.Context, olderThan time.Duration) (int, error) {
	if d.closed.Load() {
		return 0, storage.ErrClosed
	}
	sess, err := d.session(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err = cleanupDir(ctx, sess, "", cutoff, &removed, 0)
	return removed, err
}

func cleanupDir(ctx context.Context, sess *session, rel string, cutoff time.Time, removed *int, depth int) error {
	if depth > storage.MaxSegments {
		return nil
	}
	ents, err := readDir(ctx, sess, rel)
	if err != nil {
		return err
	}
	for _, e := range ents {
		p := e.name
		if rel != "" {
			p = rel + "/" + e.name
		}
		switch {
		case e.isDir && storage.ValidateSegment(e.name) == nil:
			if err := cleanupDir(ctx, sess, p, cutoff, removed, depth+1); err != nil {
				return err
			}
		case !e.isDir && partialName.MatchString(e.name) && e.fi.ModTime().Before(cutoff):
			if err := sess.sftp.Remove(sess.path(p)); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return mapErr("sftp: remove partial file", err)
			}
			*removed++
		}
	}
	return nil
}

var _ storage.DirPruner = (*Destination)(nil)

// PruneEmptyDirs implements storage.DirPruner. Directory entries come from
// READDIR (lstat attributes: symbolic links are never directories here),
// each candidate is checked again with Lstat right before the removal, and
// RemoveDirectory (rmdir) never removes a non-empty directory. The base
// directory itself is never removed. It does not look at no-delete mode:
// the caller decides (the daily scan skips no-delete destinations).
func (d *Destination) PruneEmptyDirs(ctx context.Context, olderThan time.Duration, isJob func(string) bool) (int, error) {
	if d.closed.Load() {
		return 0, storage.ErrClosed
	}
	sess, err := d.session(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	jobs, err := readDir(ctx, sess, "")
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, j := range jobs {
		if !j.isDir || !storage.PruneCandidate(1, j.name, isJob) {
			continue
		}
		// The job directory's age is taken before its run directories
		// are pruned (removing them updates its mtime).
		jobOld := j.fi.ModTime().Before(cutoff)
		runs, err := readDir(ctx, sess, j.name)
		if err != nil {
			return removed, err
		}
		older := func(fi os.FileInfo) bool { return fi.ModTime().Before(cutoff) }
		prunedRuns := false
		for _, r := range runs {
			if !r.isDir || !storage.PruneCandidate(2, r.name, isJob) || !older(r.fi) {
				continue
			}
			if removeEmptyDir(sess, j.name+"/"+r.name, older) {
				removed++
				prunedRuns = true
			}
		}
		if jobOld {
			// Right before the removal the job directory must still be
			// old, or (when this pass removed run directories in it)
			// unchanged since this pass's own last change.
			jobCheck := older
			if prunedRuns {
				if after, err := sess.sftp.Lstat(sess.path(j.name)); err == nil {
					mod := after.ModTime()
					jobCheck = func(fi os.FileInfo) bool { return fi.ModTime().Equal(mod) }
				} else {
					jobCheck = func(os.FileInfo) bool { return false }
				}
			}
			if removeEmptyDir(sess, j.name, jobCheck) {
				removed++
			}
		}
		if err := ctx.Err(); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// removeEmptyDir removes the relative directory rel (never the base) when
// it is still a real directory that passes old (its age, checked right
// before the removal); rmdir refuses a non-empty one.
func removeEmptyDir(sess *session, rel string, old func(os.FileInfo) bool) bool {
	if rel == "" {
		return false
	}
	fi, err := sess.sftp.Lstat(sess.path(rel))
	if err != nil || !fi.IsDir() || !old(fi) {
		return false
	}
	return sess.sftp.RemoveDirectory(sess.path(rel)) == nil
}
