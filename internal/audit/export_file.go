package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxHeadFileBytes bounds ReadHeadFile.
const maxHeadFileBytes = 64 << 10

// FileExporter writes the latest head as JSON to Path (atomically: temp
// file in the same directory, fsync, rename; mode 0600). Used by `bekup
// audit sign-head --export-file` and tests.
type FileExporter struct {
	Path string
}

// ExportHead implements HeadExporter.
func (f FileExporter) ExportHead(_ context.Context, h SignedHead) (err error) {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.Path)
	tmp, err := os.CreateTemp(dir, ".bekup-audit-head-*")
	if err != nil {
		return fmt.Errorf("create head file: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(tmp.Name()))
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write head file: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync head file: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close head file: %w", err)
	}
	if err := os.Rename(tmp.Name(), f.Path); err != nil {
		return fmt.Errorf("rename head file: %w", err)
	}
	return nil
}

// ReadHeadFile reads a head exported by FileExporter (at most 64 KiB,
// unknown fields rejected). The signature is not checked here.
func ReadHeadFile(path string) (SignedHead, error) {
	f, err := os.Open(path) //nolint:gosec // G304: operator-supplied path of a public, non-secret file
	if err != nil {
		return SignedHead{}, fmt.Errorf("open head file: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHeadFileBytes+1))
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return SignedHead{}, fmt.Errorf("read head file: %w", err)
	}
	if len(data) > maxHeadFileBytes {
		return SignedHead{}, errors.New("head file too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var h SignedHead
	if err := dec.Decode(&h); err != nil {
		return SignedHead{}, fmt.Errorf("parse head file: %w", err)
	}
	return h, nil
}
