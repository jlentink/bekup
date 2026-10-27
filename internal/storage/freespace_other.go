//go:build !unix

package storage

import (
	"fmt"
	"os"
)

func fileFreeSpace(*os.File) (uint64, error) {
	return 0, fmt.Errorf("%w: free space check", ErrUnsupported)
}
