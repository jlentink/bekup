package offline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/jlentink/bekup/internal/digest"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// Verification errors. Compare with errors.Is.
var (
	// ErrNotFound: a file of the run does not exist.
	ErrNotFound = errors.New("offline: file not found")
	// ErrSizeMismatch / ErrDigestMismatch: an artifact differs from its
	// manifest entry.
	ErrSizeMismatch   = errors.New("offline: size differs from the manifest")
	ErrDigestMismatch = errors.New("offline: SHA-256 differs from the manifest")
	// ErrNoKeys: no instance public key was supplied.
	ErrNoKeys = errors.New("offline: no instance public key to verify the manifest signature")
	// ErrLocation: a validly signed manifest lies in the wrong directory
	// (another job's or run's directory).
	ErrLocation = errors.New("offline: manifest does not belong to this directory")
)

// Source reads the files of one run directory by name (a single key
// segment: an entry path or manifest.json).
type Source interface {
	Open(ctx context.Context, name string) (io.ReadCloser, error)
}

// DirSource is a local run directory. Files are opened through an os.Root,
// so no name can escape the directory.
type DirSource struct {
	root *os.Root
}

// OpenDir opens a local run directory.
func OpenDir(dir string) (*DirSource, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}
	return &DirSource{root: r}, nil
}

// Open implements Source; only regular files are opened.
func (d *DirSource) Open(_ context.Context, name string) (io.ReadCloser, error) {
	if err := storage.ValidateSegment(name); err != nil {
		return nil, err
	}
	f, err := d.root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		CloseQuietly(f)
		if err == nil {
			err = fmt.Errorf("%s is not a regular file", name)
		}
		return nil, err
	}
	return f, nil
}

// Close releases the directory.
func (d *DirSource) Close() error { return d.root.Close() }

// FileSource maps entry names to explicit local files (verify --artifact)
// and falls back to Next (nil: not found).
type FileSource struct {
	Files map[string]string
	Next  Source
}

// Open implements Source.
func (f *FileSource) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if p, ok := f.Files[name]; ok {
		fh, err := os.Open(p) //nolint:gosec // G304: an operator-supplied artifact path
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
			}
			return nil, err
		}
		fi, err := fh.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			CloseQuietly(fh)
			if err == nil {
				err = fmt.Errorf("%s is not a regular file", p)
			}
			return nil, err
		}
		return fh, nil
	}
	if f.Next == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return f.Next.Open(ctx, name)
}

// ReadManifest reads manifest.json from src (at most
// pipeline.MaxManifestBytes).
func ReadManifest(ctx context.Context, src Source) ([]byte, error) {
	rc, err := src.Open(ctx, storage.ManifestFileName)
	if err != nil {
		return nil, err
	}
	defer CloseQuietly(rc)
	return readBounded(rc, pipeline.MaxManifestBytes)
}

// ReadManifestFile reads a local manifest.json (at most
// pipeline.MaxManifestBytes).
func ReadManifestFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: an operator-supplied manifest path
	if err != nil {
		return nil, err
	}
	defer CloseQuietly(f)
	return readBounded(f, pipeline.MaxManifestBytes)
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: larger than %d bytes", pipeline.ErrManifestMalformed, limit)
	}
	return b, nil
}

// CloseQuietly closes c and ignores the error: for read-only handles,
// where a close error cannot lose data.
func CloseQuietly(c io.Closer) { ignore(c.Close()) }

func ignore(error) {}

// CheckArtifact streams r and compares its size and SHA-256 with the
// manifest entry. It reads at most StoredSize+1 bytes. It returns the
// number of bytes read and nil, ErrSizeMismatch or ErrDigestMismatch (or a
// read error).
func CheckArtifact(ctx context.Context, r io.Reader, e *pipeline.Entry) (int64, error) {
	d := digest.NewReader(io.LimitReader(digest.NewCtxReader(ctx, r, nil), e.StoredSize+1), -1)
	if _, err := io.Copy(io.Discard, d); err != nil {
		return d.N(), fmt.Errorf("read artifact: %w", err)
	}
	if d.N() != e.StoredSize {
		return d.N(), ErrSizeMismatch
	}
	if hex.EncodeToString(d.Sum()) != e.SHA256 {
		return d.N(), ErrDigestMismatch
	}
	return d.N(), nil
}

// EntryStatus is the outcome of one artifact check.
type EntryStatus string

// Entry statuses.
const (
	StatusOK             EntryStatus = "ok"
	StatusMissing        EntryStatus = "missing"
	StatusSizeMismatch   EntryStatus = "size_mismatch"
	StatusDigestMismatch EntryStatus = "sha256_mismatch"
	StatusError          EntryStatus = "error"
	// StatusSkipped: not checked (verify --artifact named other files).
	StatusSkipped EntryStatus = "skipped"
)

// EntryResult is the check of one manifest entry.
type EntryResult struct {
	Entry  pipeline.Entry `json:"entry"`
	Name   string         `json:"name"`
	Status EntryStatus    `json:"status"`
	// BytesRead is the number of stored bytes read.
	BytesRead int64 `json:"bytes_read"`
	// Detail is a fixed or sanitized remark.
	Detail string `json:"detail,omitempty"`
}

// OK reports whether the entry verified.
func (r *EntryResult) OK() bool { return r.Status == StatusOK }

// Report is the outcome of verifying one run.
type Report struct {
	// KeyID and KeyFingerprint identify the verifying instance key.
	KeyID          string `json:"key_id"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	// ManifestSHA256 is the hex SHA-256 of manifest.json; ManifestSize its
	// length.
	ManifestSHA256 string             `json:"manifest_sha256"`
	ManifestSize   int64              `json:"manifest_size"`
	Manifest       *pipeline.Manifest `json:"manifest"`
	Entries        []EntryResult      `json:"entries"`
	// Partial: some entries were skipped.
	Partial bool `json:"partial"`
	OK      bool `json:"ok"`
}

// VerifyManifest checks the signature of manifest.json against keys (see
// pipeline.VerifySignature: no binding to database records, so the caller
// shows the job and run identity) and returns the manifest.
func VerifyManifest(data []byte, keys pipeline.KeyVerifier) (*pipeline.Manifest, error) {
	if keys == nil {
		return nil, ErrNoKeys
	}
	if pk, ok := keys.(pipeline.PublicKeys); ok && len(pk) == 0 {
		return nil, ErrNoKeys
	}
	return pipeline.VerifySignature(data, keys)
}

// CheckLocation checks that a manifest found at {jobSlug}/{timestamp}/
// describes that run: a validly signed manifest of another run copied into
// the directory is rejected.
func CheckLocation(m *pipeline.Manifest, jobSlug, timestamp string) error {
	if m.JobSlug != jobSlug {
		return fmt.Errorf("%w: job slug %q, directory %q", ErrLocation, m.JobSlug, SafeText(jobSlug))
	}
	if storage.TimestampSegment(m.RunTimestamp) != timestamp {
		return fmt.Errorf("%w: run timestamp %s, directory %q", ErrLocation,
			m.RunTimestamp.Format(time.RFC3339), SafeText(timestamp))
	}
	return nil
}

// VerifyOptions configure VerifyRun.
type VerifyOptions struct {
	// Only, if set, limits the artifact checks to these entry paths; the
	// others are reported as skipped.
	Only map[string]bool
}

// VerifyRun verifies a parsed and signature-checked manifest's artifacts
// from src and fills a report. keys may be nil (no fingerprint shown).
func VerifyRun(ctx context.Context, manifestFile []byte, m *pipeline.Manifest, src Source, keys *KeySet, opts VerifyOptions) *Report {
	sum := sha256.Sum256(manifestFile)
	rep := &Report{
		KeyID: m.InstanceKeyID, ManifestSHA256: hex.EncodeToString(sum[:]), ManifestSize: int64(len(manifestFile)),
		Manifest: m, OK: true,
	}
	if keys != nil {
		if k, ok := keys.Lookup(m.InstanceKeyID); ok {
			rep.KeyFingerprint = k.Fingerprint()
		}
	}
	for i := range m.Entries {
		e := &m.Entries[i]
		r := EntryResult{Entry: *e, Name: DisplayName(e)}
		if opts.Only != nil && !opts.Only[e.Path] {
			r.Status, r.Detail = StatusSkipped, "not checked"
			rep.Partial = true
			rep.Entries = append(rep.Entries, r)
			continue
		}
		r.Status, r.BytesRead, r.Detail = checkEntry(ctx, src, e)
		if !r.OK() {
			rep.OK = false
		}
		rep.Entries = append(rep.Entries, r)
	}
	return rep
}

func checkEntry(ctx context.Context, src Source, e *pipeline.Entry) (EntryStatus, int64, string) {
	rc, err := src.Open(ctx, e.Path)
	if errors.Is(err, ErrNotFound) || errors.Is(err, storage.ErrNotFound) {
		return StatusMissing, 0, "file not found"
	}
	if err != nil {
		return StatusError, 0, SafeText(err.Error())
	}
	defer CloseQuietly(rc)
	n, err := CheckArtifact(ctx, rc, e)
	switch {
	case err == nil:
		return StatusOK, n, ""
	case errors.Is(err, ErrSizeMismatch):
		if n > e.StoredSize {
			return StatusSizeMismatch, n, fmt.Sprintf("larger than the %d bytes in the manifest", e.StoredSize)
		}
		return StatusSizeMismatch, n, fmt.Sprintf("%d bytes, manifest says %d", n, e.StoredSize)
	case errors.Is(err, ErrDigestMismatch):
		return StatusDigestMismatch, n, "content differs from the signed manifest"
	default:
		return StatusError, n, SafeText(err.Error())
	}
}
