package offline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jlentink/bekup/internal/digest"
	"github.com/jlentink/bekup/internal/pipeline"
)

// Decryption errors.
var (
	// ErrRawMismatch: the decrypted dump differs from the manifest's raw
	// size or raw SHA-256.
	ErrRawMismatch = errors.New("offline: decrypted dump differs from the manifest")
	// ErrOutputExists: the output file exists and overwriting was not
	// requested.
	ErrOutputExists = errors.New("offline: output file exists (use --force to replace it)")
)

// DecryptResult describes a finished decryption.
type DecryptResult struct {
	StoredSize   int64
	StoredSHA256 string
	RawSize      int64
	RawSHA256    string
}

// Decrypt streams the artifact src through age decryption and zstd
// decompression (pipeline.OpenArtifact: bounded scrypt work factor,
// X25519 identities only, bounded zstd window) into dst. When e is set,
// the stored bytes must match its size and SHA-256 and the dump its raw
// size and raw SHA-256 (ErrSizeMismatch, ErrDigestMismatch,
// ErrRawMismatch), and no more than RawSize bytes are written. On error
// dst may hold partial output: the caller discards it.
func Decrypt(ctx context.Context, dst io.Writer, src io.Reader, d pipeline.Decryption, e *pipeline.Entry) (DecryptResult, error) {
	var res DecryptResult
	limit := int64(-1)
	var stored *digest.Reader
	if e != nil {
		stored = digest.NewReader(io.LimitReader(digest.NewCtxReader(ctx, src, nil), e.StoredSize+1), -1)
		limit = e.RawSize
	} else {
		stored = digest.NewReader(digest.NewCtxReader(ctx, src, nil), -1)
	}
	plain, err := pipeline.OpenArtifact(stored, d)
	if err != nil {
		return res, err
	}
	defer CloseQuietly(plain)
	var rawSrc io.Reader = plain
	if limit >= 0 {
		rawSrc = io.LimitReader(plain, limit+1)
	}
	raw := digest.NewReader(rawSrc, -1)
	if _, err := io.Copy(dst, raw); err != nil {
		return res, fmt.Errorf("decrypt: %w", err)
	}
	// age's final chunk and the zstd frame end still count for the stored
	// digest.
	if _, err := io.Copy(io.Discard, stored); err != nil {
		return res, fmt.Errorf("read artifact: %w", err)
	}
	res = DecryptResult{
		StoredSize: stored.N(), StoredSHA256: hex.EncodeToString(stored.Sum()),
		RawSize: raw.N(), RawSHA256: hex.EncodeToString(raw.Sum()),
	}
	if e == nil {
		return res, nil
	}
	switch {
	case res.StoredSize != e.StoredSize:
		return res, ErrSizeMismatch
	case res.StoredSHA256 != e.SHA256:
		return res, ErrDigestMismatch
	case res.RawSize != e.RawSize || res.RawSHA256 != e.RawSHA256:
		return res, ErrRawMismatch
	}
	return res, nil
}

// OutputFile is a decryption output written to a temporary file next to
// the target (mode 0600) and moved into place by Commit only after every
// check passed, so a failed or tampered decryption never leaves a file
// under the target name.
type OutputFile struct {
	*os.File
	target string
	tmp    string
	force  bool
	done   bool
}

// CreateOutput prepares an output file. Without force an existing target
// is refused up front (and again at Commit, atomically).
func CreateOutput(target string, force bool) (*OutputFile, error) {
	if target == "" {
		return nil, errors.New("offline: empty output path")
	}
	fi, err := os.Lstat(target)
	switch {
	case err == nil && fi.IsDir():
		return nil, fmt.Errorf("offline: output %s is a directory", target)
	case err == nil && !force:
		return nil, fmt.Errorf("%w: %s", ErrOutputExists, target)
	case err == nil && !fi.Mode().IsRegular():
		return nil, fmt.Errorf("offline: output %s is not a regular file", target)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp := filepath.Join(dir, "."+base+"."+hex.EncodeToString(rnd[:])+".partial")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: operator-chosen output path
	if err != nil {
		return nil, fmt.Errorf("create output: %w", err)
	}
	return &OutputFile{File: f, target: target, tmp: tmp, force: force}, nil
}

// Commit syncs the file and moves it to the target. Without force it
// never replaces an existing file (hard link, which fails if the target
// exists).
func (o *OutputFile) Commit() error {
	if o.done {
		return errors.New("offline: output already finished")
	}
	o.done = true
	if err := o.Sync(); err != nil {
		o.cleanup()
		return fmt.Errorf("sync output: %w", err)
	}
	if err := o.Close(); err != nil {
		ignore(os.Remove(o.tmp))
		return fmt.Errorf("close output: %w", err)
	}
	if o.force {
		if err := os.Rename(o.tmp, o.target); err != nil {
			ignore(os.Remove(o.tmp))
			return fmt.Errorf("move output into place: %w", err)
		}
		return nil
	}
	err := os.Link(o.tmp, o.target)
	ignore(os.Remove(o.tmp))
	switch {
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %s", ErrOutputExists, o.target)
	case err != nil:
		return fmt.Errorf("move output into place: %w", err)
	}
	return nil
}

// Abort discards the output.
func (o *OutputFile) Abort() {
	if o.done {
		return
	}
	o.done = true
	o.cleanup()
}

func (o *OutputFile) cleanup() {
	CloseQuietly(o)
	ignore(os.Remove(o.tmp))
}
