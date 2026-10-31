//go:build !unix

package setup

import (
	"errors"
	"io/fs"
	"os"
)

var errNoOwnership = errors.New("changing ownership is supported on Unix systems only")

func openEntry(*os.Root, string, bool) (*os.File, error) { return nil, errNoOwnership }

// openInput opens an input file through the os.Root (it never leaves the
// deployment directory; setup runs on Linux, this is the fallback).
func openInput(r *os.Root, rel string) (*os.File, error) {
	if err := checkRel(rel); err != nil || rel == "." {
		return nil, errors.New("invalid relative path")
	}
	return r.Open(rel)
}

func fdChown(*os.File, int, int) error { return errNoOwnership }

func fdChmod(*os.File, fs.FileMode) error { return errNoOwnership }
