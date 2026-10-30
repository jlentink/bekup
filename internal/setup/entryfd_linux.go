//go:build linux

package setup

import (
	"io/fs"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// entryOpenFlags: O_PATH needs no read permission (the setup container
// has CAP_CHOWN only) and opens a symlink itself under O_NOFOLLOW (then
// refused by the type check).
const entryOpenFlags = unix.O_PATH

// fdChown changes the owner of the opened entry (fchownat AT_EMPTY_PATH
// works on O_PATH descriptors).
func fdChown(f *os.File, uid, gid int) error {
	return fdControl(f, func(fd int) error { return unix.Fchownat(fd, "", uid, gid, unix.AT_EMPTY_PATH) })
}

// fdChmod changes the mode of the opened entry through its /proc magic
// link, which names exactly the opened inode (fchmod does not take O_PATH
// descriptors).
func fdChmod(f *os.File, mode fs.FileMode) error {
	return fdControl(f, func(fd int) error {
		return os.Chmod("/proc/self/fd/"+strconv.Itoa(fd), mode)
	})
}
