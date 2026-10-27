//go:build unix

package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkOwner requires fi to be owned by this process's effective user.
func checkOwner(fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine the owner")
	}
	if uid := os.Geteuid(); int64(st.Uid) != int64(uid) {
		return fmt.Errorf("owned by uid %d, not by the runner user (uid %d)", st.Uid, uid)
	}
	return nil
}
