//go:build unix

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// getuid is the uid that config and secret files must be owned by (or
// root). It is a variable so tests can simulate a different user.
var getuid = os.Getuid

// readSafeFile reads path after enforcing the file safety rules (D-041):
//
//   - the path is a regular file; a symlink is only followed when the
//     directory of its target is not group/world-writable (unless sticky)
//     and is owned by the current user or root;
//   - the file is owned by the current user or root;
//   - the file is not group- or world-writable;
//   - secret files must additionally not be world-readable;
//   - at most limit bytes.
//
// The ownership and mode checks run on the opened file descriptor so the
// checked file is the file that is read.
func readSafeFile(path string, secret bool, limit int64) (_ []byte, retErr error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	li, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	target := abs
	if li.Mode()&fs.ModeSymlink != 0 {
		target, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: resolving symlink: %w", path, err)
		}
		if err := checkDir(filepath.Dir(target)); err != nil {
			return nil, fmt.Errorf("%s: symlink target %s: %w", path, target, err)
		}
	}

	// O_NOFOLLOW: the resolved target must not have been swapped for a
	// symlink in between. O_NONBLOCK: never hang on a FIFO; the regular-file
	// check below rejects it.
	f, err := os.OpenFile(target, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("%s: %w", path, cerr)
		}
	}()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkFileInfo(fi, secret); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if int64(len(b)) > limit {
		zero(b)
		return nil, fmt.Errorf("%s: %w: larger than %d bytes", path, ErrUnsafeFile, limit)
	}
	return b, nil
}

func checkFileInfo(fi fs.FileInfo, secret bool) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: not a regular file", ErrUnsafeFile)
	}
	if err := checkOwner(fi); err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if perm&0o022 != 0 {
		return fmt.Errorf("%w: mode %04o is group- or world-writable (chmod go-w)", ErrUnsafeFile, perm)
	}
	if secret && perm&0o004 != 0 {
		return fmt.Errorf("%w: secret file mode %04o is world-readable (chmod o-rwx)", ErrUnsafeFile, perm)
	}
	return nil
}

func checkDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafeFile, dir)
	}
	if err := checkOwner(fi); err != nil {
		return fmt.Errorf("directory %s: %w", dir, err)
	}
	if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("%w: directory %s is group- or world-writable", ErrUnsafeFile, dir)
	}
	return nil
}

func checkOwner(fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine file owner")
	}
	uid := int(st.Uid)
	if uid != 0 && uid != getuid() {
		return fmt.Errorf("%w: owned by uid %d, expected the current user (%d) or root", ErrUnsafeFile, uid, getuid())
	}
	return nil
}
