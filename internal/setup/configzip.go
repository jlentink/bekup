package setup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

// configFiles are the deployment files without key material (D-217): the
// browser setup offers them as a zip. Never add a file of a secret group
// or below the keys directory (TestConfigFilesHoldNoSecrets).
var configFiles = []string{ComposeFile, OverrideFile, EnvFile, BekupTOML, RunnerTOML, StateFileName}

// maxConfigFile bounds each file read into the zip.
const maxConfigFile = 4 << 20

// ConfigFiles returns the names of the files WriteConfigZip includes.
func ConfigFiles() []string { return append([]string(nil), configFiles...) }

// WriteConfigZip writes the configuration files present in dir to w as a
// zip (absent ones are skipped). Each file is read through an os.Root with
// O_NOFOLLOW per component (readInput): a symlink fails the zip.
func WriteConfigZip(dir string, w io.Writer, now time.Time) (retErr error) {
	if _, err := ReadState(dir); err != nil {
		return fmt.Errorf("no deployment in %s yet: %w", dir, err)
	}
	zw := zip.NewWriter(w)
	defer func() { retErr = errors.Join(retErr, zw.Close()) }()
	for _, name := range configFiles {
		data, err := readInput(dir, name, maxConfigFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		f, err := zw.CreateHeader(&zip.FileHeader{Name: "bekup/" + name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}
