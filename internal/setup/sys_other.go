//go:build !unix

package setup

import "io/fs"

// bekup setup targets Linux hosts (and Docker Desktop); on other systems
// the symlink-safe open flags do not exist and ownership is not checked.
const (
	oNoFollow = 0
	oNonBlock = 0
)

func fileOwner(fs.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }

func linkCount(fs.FileInfo) (uint64, bool) { return 0, false }

func isEINVAL(error) bool { return false }

func isPermOrUnsupported(error) bool { return false }
