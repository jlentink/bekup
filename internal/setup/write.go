package setup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// writer creates the files of one setup run below root and can undo all of
// it. Every operation goes through an *os.Root opened on the deployment
// directory, so no path (and no symlink swapped in by a racing process) can
// lead outside it; on top of that every path is checked for symlinks
// component by component (Lstat) and refused. New files are created with
// O_CREAT|O_EXCL|O_NOFOLLOW in their final mode and fsynced together with
// their directory; replaced files go through a temporary file and a rename.
// Paths are slash-separated and relative to root.
type writer struct {
	root     string   // absolute path of the deployment directory
	r        *os.Root // every write goes through it
	created  []string // relative paths in creation order (files and dirs)
	replaced []replacedFile
	aside    []asideFile
}

type replacedFile struct {
	rel  string
	data []byte
	mode fs.FileMode
}

// asideFile is an existing file moved to a hidden sibling (setAside): the
// rollback moves it back, commit deletes it.
type asideFile struct{ rel, tmp string }

func newWriter(root string) (*writer, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("deployment directory: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("deployment directory %s is not a directory (symlinks are refused)", abs)
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("deployment directory: %w", err)
	}
	return &writer{root: abs, r: r}, nil
}

// close releases the root handle.
func (w *writer) close() error { return w.r.Close() }

// abs maps a slash-separated relative path to an absolute one below root
// (for reports and for certs.Init, which works on paths).
func (w *writer) abs(rel string) (string, error) {
	return resolve(w.root, rel)
}

func resolve(root, rel string) (string, error) {
	if err := checkRel(rel); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// checkRel accepts "." and clean relative paths that stay below the root.
func checkRel(rel string) error {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("invalid relative path %q", rel)
	}
	return nil
}

// osRel converts a checked relative path for the os.Root methods.
func osRel(rel string) string { return filepath.FromSlash(rel) }

// checkNoSymlinks Lstats every existing component of rel below root and
// refuses symlinks and non-directories in the middle. It reports whether
// the final component exists (and its info). Read-only callers use it on a
// path; the writer uses lstat (the same rule through its os.Root).
func checkNoSymlinks(root, rel string) (fs.FileInfo, error) {
	if err := checkRel(rel); err != nil {
		return nil, err
	}
	return walkNoSymlinks(rel, func(sub string) (fs.FileInfo, error) {
		return os.Lstat(filepath.Join(root, filepath.FromSlash(sub)))
	})
}

// lstat is checkNoSymlinks through the writer's os.Root.
func (w *writer) lstat(rel string) (fs.FileInfo, error) {
	return lstatIn(w.r, rel)
}

func lstatIn(r *os.Root, rel string) (fs.FileInfo, error) {
	if err := checkRel(rel); err != nil {
		return nil, err
	}
	return walkNoSymlinks(rel, func(sub string) (fs.FileInfo, error) { return r.Lstat(osRel(sub)) })
}

func walkNoSymlinks(rel string, lstat func(string) (fs.FileInfo, error)) (fs.FileInfo, error) {
	parts := strings.Split(rel, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		fi, err := lstat(sub)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink; refusing to follow it", sub)
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", sub)
		}
		if i == len(parts)-1 {
			return fi, nil
		}
	}
	return nil, nil
}

// mkdirAll creates rel and its missing parents with mode (an existing
// directory is kept; ownership.go applies the manifest mode later).
func (w *writer) mkdirAll(rel string, mode fs.FileMode) error {
	if err := checkRel(rel); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		fi, err := w.lstat(sub)
		if err != nil {
			return err
		}
		if fi != nil {
			if !fi.IsDir() {
				return fmt.Errorf("%s exists and is not a directory", sub)
			}
			continue
		}
		if err := w.r.Mkdir(osRel(sub), mode); err != nil {
			return fmt.Errorf("creating %s: %w", sub, err)
		}
		w.created = append(w.created, sub)
		// The umask may have narrowed the mode.
		if err := w.r.Chmod(osRel(sub), mode); err != nil {
			return fmt.Errorf("chmod %s: %w", sub, err)
		}
		if err := w.syncDir(path.Dir(sub)); err != nil {
			return err
		}
	}
	return nil
}

// create writes a new file; it fails if anything exists at rel.
func (w *writer) create(rel string, data []byte, mode fs.FileMode) (retErr error) {
	if dir := path.Dir(rel); dir != "." {
		if err := w.mkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if _, err := w.lstat(rel); err != nil {
		return err
	}
	// O_NOFOLLOW refuses a symlink planted in between; the os.Root keeps
	// every component below the deployment directory.
	f, err := w.r.OpenFile(osRel(rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, mode)
	if err != nil {
		return fmt.Errorf("creating %s: %w", rel, err)
	}
	w.created = append(w.created, rel)
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("closing %s: %w", rel, cerr)
		}
	}()
	// The umask may have narrowed the mode; set it on the descriptor.
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", rel, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", rel, err)
	}
	return w.syncDir(path.Dir(rel))
}

// replace atomically replaces (or creates) the non-secret file rel through
// a temporary file and a rename; the previous content is kept in memory
// for the rollback.
func (w *writer) replace(rel string, data []byte, mode fs.FileMode) (retErr error) {
	fi, err := w.lstat(rel)
	if err != nil {
		return err
	}
	if fi == nil {
		return w.create(rel, data, mode)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rel)
	}
	old, err := w.read(rel, maxManagedFile)
	if err != nil {
		return err
	}
	if err := w.renameInto(rel, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	w.replaced = append(w.replaced, replacedFile{rel: rel, data: old, mode: fi.Mode().Perm()})
	return nil
}

// remove deletes the non-secret file rel (kept in memory for the rollback).
func (w *writer) remove(rel string) error {
	fi, err := w.lstat(rel)
	if err != nil || fi == nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rel)
	}
	old, err := w.read(rel, maxManagedFile)
	if err != nil {
		return err
	}
	if err := w.r.Remove(osRel(rel)); err != nil {
		return err
	}
	w.replaced = append(w.replaced, replacedFile{rel: rel, data: old, mode: fi.Mode().Perm()})
	return w.syncDir(path.Dir(rel))
}

// setAside moves the existing file rel to a hidden sibling
// (.<name>.setup-old-<random>) without reading it (secrets): the rollback
// moves it back, commit deletes it. Used by --regenerate-secrets, so a
// failure midway never leaves the deployment without its old secrets.
func (w *writer) setAside(rel string) error {
	fi, err := w.lstat(rel)
	if err != nil || fi == nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rel)
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	tmp := path.Join(path.Dir(rel), "."+path.Base(rel)+".setup-old-"+suffix)
	if err := w.r.Rename(osRel(rel), osRel(tmp)); err != nil {
		return fmt.Errorf("moving %s aside: %w", rel, err)
	}
	w.aside = append(w.aside, asideFile{rel: rel, tmp: tmp})
	return w.syncDir(path.Dir(rel))
}

// commit deletes the files moved aside (the run succeeded). It continues
// past errors and returns the paths it could not delete.
func (w *writer) commit() ([]string, error) {
	var left []string
	var errs []error
	for _, a := range w.aside {
		if err := w.r.Remove(osRel(a.tmp)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, a.tmp)
			errs = append(errs, err)
		}
	}
	w.aside = nil
	return left, errors.Join(errs...)
}

// track records paths created by someone else (certs.Init) for the
// rollback. Only paths that did not exist before may be tracked.
func (w *writer) track(rel ...string) { w.created = append(w.created, rel...) }

// rollback removes everything this run created (newest first), moves the
// files set aside back and restores replaced files. It continues past
// errors and returns them joined.
func (w *writer) rollback() error {
	var errs []error
	for i := len(w.created) - 1; i >= 0; i-- {
		if err := w.removeIfExists(w.created[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(w.aside) - 1; i >= 0; i-- {
		a := w.aside[i]
		if err := w.r.Rename(osRel(a.tmp), osRel(a.rel)); err != nil {
			errs = append(errs, fmt.Errorf("restoring %s (kept as %s): %w", a.rel, a.tmp, err))
		}
	}
	for i := len(w.replaced) - 1; i >= 0; i-- {
		r := w.replaced[i]
		if err := w.renameInto(r.rel, r.data, r.mode); err != nil {
			errs = append(errs, err)
		}
	}
	w.created, w.replaced, w.aside = nil, nil, nil
	return errors.Join(errs...)
}

func randomSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading randomness: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// renameInto writes data to a temporary file next to rel (mode set on the
// descriptor, fsynced) and renames it over rel, then fsyncs the directory.
// The temporary file never survives a failure.
func (w *writer) renameInto(rel string, data []byte, mode fs.FileMode) (retErr error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	name := path.Join(path.Dir(rel), "."+path.Base(rel)+".setup-"+suffix)
	tmp, err := w.r.OpenFile(osRel(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
	if err != nil {
		return err
	}
	closed, renamed := false, false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, tmp.Close())
		}
		if !renamed {
			retErr = errors.Join(retErr, w.removeIfExists(name))
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	closed = true
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := w.r.Rename(osRel(name), osRel(rel)); err != nil {
		return err
	}
	renamed = true
	return w.syncDir(path.Dir(rel))
}

func (w *writer) removeIfExists(rel string) error {
	if err := w.r.Remove(osRel(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// read reads the regular non-secret file rel through the root.
func (w *writer) read(rel string, limit int64) (_ []byte, retErr error) {
	f, err := w.r.OpenFile(osRel(rel), os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	return readLimited(f, rel, limit)
}

func (w *writer) syncDir(rel string) (retErr error) {
	d, err := w.r.Open(osRel(rel))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, d.Close()) }()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) && !isEINVAL(err) {
		return fmt.Errorf("syncing %s: %w", rel, err)
	}
	return nil
}

// maxManagedFile bounds the non-secret files setup reads back.
const maxManagedFile = 4 << 20

// readRegular reads a regular, non-symlink file up to limit bytes.
func readRegular(abs string, limit int64) (_ []byte, retErr error) {
	// #nosec G304 -- a path below the deployment directory; O_NOFOLLOW.
	f, err := os.OpenFile(abs, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	return readLimited(f, abs, limit)
}

func readLimited(f *os.File, name string, limit int64) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	buf, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	return buf, nil
}
