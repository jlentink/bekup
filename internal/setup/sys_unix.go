//go:build unix

package setup

import (
	"errors"
	"io/fs"
	"syscall"
)

// Open flags: never follow a symlink in the last component, never block on
// a FIFO.
const (
	oNoFollow = syscall.O_NOFOLLOW
	oNonBlock = syscall.O_NONBLOCK
)

// fileOwner returns the numeric owner of fi.
func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// linkCount returns the hard-link count of fi.
func linkCount(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return widen(st.Nlink), true
}

// widen converts the platform's link-count type (uint16 on darwin, uint32
// or uint64 elsewhere).
func widen[T ~uint16 | ~uint32 | ~uint64](v T) uint64 { return uint64(v) }

// isEINVAL reports the fsync error of file systems without directory sync.
func isEINVAL(err error) bool { return errors.Is(err, syscall.EINVAL) }

// isPermOrUnsupported reports the errors of file systems without Unix
// modes or owners (FAT/exFAT: EPERM, ENOTSUP/EOPNOTSUPP).
func isPermOrUnsupported(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
