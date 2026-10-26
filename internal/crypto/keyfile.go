package crypto

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jlentink/bekup/internal/config"
)

// keyFileMode is the mode of every key file bekup writes.
const keyFileMode fs.FileMode = 0o600

// LoadKeyringFile reads and parses a keyring file. The file must pass the
// secret-file safety checks of config.ReadSecretFile (regular file, owned by
// the current user or root, not group/world-writable, not world-readable).
// The raw file contents are zeroed before returning.
func LoadKeyringFile(path string) (*Keyring, error) {
	data, err := config.ReadSecretFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading keyring: %w", err)
	}
	defer Zero(data)
	k, err := ParseKeyring(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return k, nil
}

// WriteKeyringFile writes k to path atomically with mode 0600: the data is
// written and fsynced to a temporary file in the same directory, which is
// then moved into place, and the directory is fsynced.
//
// With overwrite false an existing path (including a dangling symlink) is
// never replaced: the temporary file is hard-linked into place, which fails
// if path exists. With overwrite true a symlink at path is resolved first so
// the link itself is preserved and its target is replaced.
func WriteKeyringFile(path string, k *Keyring, overwrite bool) error {
	data, err := k.Marshal()
	if err != nil {
		return err
	}
	defer Zero(data)
	return writeFileAtomic(path, data, overwrite)
}

func writeFileAtomic(path string, data []byte, overwrite bool) (retErr error) {
	if path == "" {
		return errors.New("empty path")
	}
	if overwrite {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".bekup-key-*.tmp") // created 0600
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil { // only on an early error, which is already being returned
			if cerr := tmp.Close(); cerr != nil && retErr == nil {
				retErr = cerr
			}
		}
		// After a successful rename the name no longer exists; after a link
		// the temporary name must go in every case.
		if rmErr := os.Remove(tmpName); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) && retErr == nil {
			retErr = fmt.Errorf("removing temporary file: %w", rmErr)
		}
	}()
	if err := tmp.Chmod(keyFileMode); err != nil {
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temporary file: %w", err)
	}
	err = tmp.Close()
	tmp = nil
	if err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if overwrite {
		if err := os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("replacing %s: %w", path, err)
		}
	} else if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists; refusing to overwrite a key file: %w", path, fs.ErrExist)
		}
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return syncDir(dir)
}

func syncDir(dir string) (retErr error) {
	d, err := os.Open(dir) //nolint:gosec // G304: dir is the operator-chosen key file directory
	if err != nil {
		return fmt.Errorf("opening directory for sync: %w", err)
	}
	defer func() {
		if cerr := d.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("closing directory: %w", cerr)
		}
	}()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("syncing directory: %w", err)
	}
	return nil
}
