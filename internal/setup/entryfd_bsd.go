//go:build unix && !linux

package setup

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// entryOpenFlags on development systems without O_PATH.
const entryOpenFlags = unix.O_RDONLY | unix.O_NONBLOCK

func fdChown(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }

func fdChmod(f *os.File, mode fs.FileMode) error { return f.Chmod(mode) }
