//go:build unix

package setup

import (
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
		flags := entryOpenFlags | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if !last || dir {
			flags |= unix.O_DIRECTORY
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
	if err := checkEntryFD(cur, rel, dir); err != nil {
		return nil, closeWith(cur, err)
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
