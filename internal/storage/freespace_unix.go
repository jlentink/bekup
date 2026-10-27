//go:build unix

package storage

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fileFreeSpace(f *os.File) (uint64, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("storage: statfs: %w", err)
	}
	var st unix.Statfs_t
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = unix.Fstatfs(int(fd), &st)
	}); err != nil {
		return 0, fmt.Errorf("storage: statfs: %w", err)
	}
	if serr != nil {
		return 0, fmt.Errorf("storage: statfs: %w", serr)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert // field types differ per OS
}
