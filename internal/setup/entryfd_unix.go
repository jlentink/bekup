//go:build unix

package setup

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// openEntry opens a manifest entry for a metadata change without ever
// following a symlink: every component below the os.Root is opened with
// openat(O_NOFOLLOW) relative to its parent's descriptor (os.Root itself
// follows symlinks that stay inside the root), directories with
// O_DIRECTORY, and the type is re-checked on the final descriptor. A
// symlink swapped in after the manifest walk fails the open (ELOOP or
// ENOTDIR) or the type check.
func openEntry(r *os.Root, rel string, dir bool) (*os.File, error) {
	f, err := openNoFollow(r, rel, entryOpenFlags, dir)
	if err != nil {
		return nil, err
	}
	if err := checkEntryFD(f, rel, dir); err != nil {
		return nil, closeWith(f, err)
	}
	return f, nil
}

// openInput opens an operator-named input file (CA bundle, password file,
// keyring to import) for reading below the os.Root, with the same walk:
// no component is ever a symlink, and the caller checks the type on the
// descriptor (readLimited), so nothing is checked first and opened later.
func openInput(r *os.Root, rel string) (*os.File, error) {
	if err := checkRel(rel); err != nil || rel == "." {
		return nil, fmt.Errorf("invalid relative path %q", rel)
	}
	f, err := openNoFollow(r, rel, unix.O_RDONLY|unix.O_NONBLOCK, false)
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return nil, fmt.Errorf("%s: a symlink or a non-directory in the path; refusing to follow it", rel)
	}
	return f, err
}

// openNoFollow walks rel below r with openat(O_NOFOLLOW) per component and
// opens the last one with lastFlags (O_DIRECTORY when dir).
func openNoFollow(r *os.Root, rel string, lastFlags int, dir bool) (*os.File, error) {
	if err := checkRel(rel); err != nil && rel != "." {
		return nil, err
	}
	cur, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	var parts []string
	if rel != "." && rel != "" {
		parts = strings.Split(rel, "/")
	}
	for i, p := range parts {
		last := i == len(parts)-1
		flags := unix.O_NOFOLLOW | unix.O_CLOEXEC
		switch {
		case !last:
			flags |= entryOpenFlags | unix.O_DIRECTORY
		case dir:
			flags |= lastFlags | unix.O_DIRECTORY
		default:
			flags |= lastFlags
		}
		var fd int
		err := fdControl(cur, func(parent int) error {
			var oerr error
			for {
				fd, oerr = unix.Openat(parent, p, flags, 0)
				if oerr != unix.EINTR {
					return oerr
				}
			}
		})
		if cerr := cur.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			return nil, &os.PathError{Op: "openat", Path: rel, Err: err}
		}
		cur = os.NewFile(uintptr(fd), rel)
	}
	return cur, nil
}

func fdControl(f *os.File, fn func(fd int) error) error {
	sc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if err := sc.Control(func(fd uintptr) { opErr = fn(int(fd)) }); err != nil {
		return err
	}
	return opErr
}
