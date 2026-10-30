//go:build !unix

package setup

import (
	"errors"
	"io/fs"
	"os"
)

var errNoOwnership = errors.New("changing ownership is supported on Unix systems only")

func openEntry(*os.Root, string, bool) (*os.File, error) { return nil, errNoOwnership }

func fdChown(*os.File, int, int) error { return errNoOwnership }

func fdChmod(*os.File, fs.FileMode) error { return errNoOwnership }
