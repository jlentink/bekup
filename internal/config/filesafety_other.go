//go:build !unix

package config

import (
	"errors"
	"os"
)

var getuid = os.Getuid

// readSafeFile is not supported outside unix: the ownership and permission
// checks required by D-041 cannot be enforced, so loading always fails.
func readSafeFile(path string, _ bool, _ int64) ([]byte, error) {
	return nil, errors.New(path + ": config and secret files are only supported on unix systems")
}
