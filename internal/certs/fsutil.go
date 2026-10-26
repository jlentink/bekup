package certs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ensureDir creates dir with mode 0700 if it is missing. An existing
// directory must not be a symlink and must not be group/world-writable.
func ensureDir(dir string) error {
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s exists and is not a directory", dir)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%s is group- or world-writable; refusing to write key material there", dir)
	}
	return nil
}

// writeFile writes data to path atomically (temp file in the same
// directory, fsync, chmod, rename, fsync of the directory) and records the
// path in res. The target must not be a symlink.
func writeFile(path string, data []byte, perm fs.FileMode, res *Result) (retErr error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to overwrite", path)
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if retErr == nil {
			return
		}
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	// CreateTemp uses 0600; widen to the target mode only for certificates.
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := renameSync(tmp, path); err != nil {
		return err
	}
	if res != nil {
		res.Written = append(res.Written, path)
	}
	return nil
}

// renameSync renames oldPath to newPath and fsyncs the parent directory.
func renameSync(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("renaming %s: %w", oldPath, err)
	}
	return syncDir(filepath.Dir(newPath))
}

func syncDir(dir string) (retErr error) {
	d, err := os.Open(dir) // #nosec G304 -- directory of a path we just wrote
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, d.Close()) }()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	return nil
}

func removeIfExists(path string) (bool, error) {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
