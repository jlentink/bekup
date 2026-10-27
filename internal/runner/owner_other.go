//go:build !unix

package runner

import (
	"errors"
	"io/fs"
)

// checkOwner cannot verify ownership outside unix, so the health socket is
// refused there.
func checkOwner(fs.FileInfo) error {
	return errors.New("the health socket is only supported on unix systems")
}
