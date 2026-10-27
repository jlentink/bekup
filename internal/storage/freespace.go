package storage

import (
	"errors"
	"fmt"
	"os"
)

// FreeSpace returns the bytes available to the bekup user on the file
// system of the destination directory (ErrUnsupported on platforms
// without statfs).
func (d *Disk) FreeSpace() (uint64, error) {
	if d.closed.Load() {
		return 0, ErrClosed
	}
	f, err := d.dir.Open(".")
	if err != nil {
		return 0, fmt.Errorf("storage: open directory: %w", err)
	}
	n, serr := fileFreeSpace(f)
	cerr := f.Close()
	if serr != nil {
		return 0, serr
	}
	if cerr != nil {
		return 0, fmt.Errorf("storage: close directory: %w", cerr)
	}
	return n, nil
}

// CheckFreeSpace fails with ErrInsufficientSpace unless at least
// need+reserve bytes are available on the file system of dir.
func CheckFreeSpace(dir string, need, reserve uint64) error {
	f, err := os.Open(dir) //nolint:gosec // G304: dir is an operator-configured directory
	if err != nil {
		return fmt.Errorf("storage: open directory: %w", err)
	}
	n, serr := fileFreeSpace(f)
	cerr := f.Close()
	if serr != nil {
		return serr
	}
	if cerr != nil {
		return fmt.Errorf("storage: close directory: %w", cerr)
	}
	return enoughSpace(n, need, reserve)
}

func enoughSpace(avail, need, reserve uint64) error {
	total := need + reserve
	if total < need { // overflow
		return ErrInsufficientSpace
	}
	if avail < total {
		return fmt.Errorf("%w: %d bytes needed, %d available", ErrInsufficientSpace, total, avail)
	}
	return nil
}

// checkFreeSpace is the early check of Put; platforms without statfs skip
// it (the write then fails with ENOSPC instead).
func (d *Disk) checkFreeSpace(need uint64) error {
	n, err := d.FreeSpace()
	if errors.Is(err, ErrUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	return enoughSpace(n, need, 0)
}
