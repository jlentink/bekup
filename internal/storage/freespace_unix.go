//go:build unix

package storage

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// fstatfs runs statfs on an open file.
func fstatfs(f *os.File) (*unix.Statfs_t, error) {
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("storage: statfs: %w", err)
	}
	var st unix.Statfs_t
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = unix.Fstatfs(int(fd), &st)
	}); err != nil {
		return nil, fmt.Errorf("storage: statfs: %w", err)
	}
	if serr != nil {
		return nil, fmt.Errorf("storage: statfs: %w", serr)
	}
	return &st, nil
}

func fileFreeSpace(f *os.File) (uint64, error) {
	st, err := fstatfs(f)
	if err != nil {
		return 0, err
	}
	// The field types differ per OS (Linux: Bsize int64; macOS: uint32;
	// FreeBSD: Bavail int64), hence the generic helper.
	return statfsFree(st.Bavail, st.Bsize)
}

func fileSpace(f *os.File) (Space, error) {
	st, err := fstatfs(f)
	if err != nil {
		return Space{}, err
	}
	return BlockSpace(st.Blocks, st.Bfree, st.Bavail, st.Bsize)
}
