package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"time"
)

// Destination is a backup destination backend (Disk, S3, SFTP).
//
// All methods are safe for concurrent use. Keys are always validated [Key]
// values; the zero Key is only accepted as a List/Test prefix.
type Destination interface {
	// Capabilities describes what the backend supports.
	Capabilities() Capabilities

	// Put streams r to key. The object becomes visible under key only
	// after all bytes were written, verified against
	// opts.ExpectedSHA256 (if set) and made durable; a failed or
	// canceled Put leaves no object under key. Put never overwrites: an
	// existing object yields ErrAlreadyExists. Options the backend
	// cannot honor (object lock on a backend without it) fail with
	// ErrUnsupported before any byte is read. The returned ObjectInfo
	// always carries the SHA-256 and size of the streamed bytes.
	Put(ctx context.Context, key Key, r io.Reader, opts PutOptions) (ObjectInfo, error)

	// Get opens the object for reading. The caller closes the reader.
	// A missing object yields ErrNotFound.
	Get(ctx context.Context, key Key) (io.ReadCloser, ObjectInfo, error)

	// Stat returns the object's metadata or ErrNotFound.
	Stat(ctx context.Context, key Key) (ObjectInfo, error)

	// List returns one page of the objects strictly below prefix (the
	// zero Key lists everything), in strict byte-wise lexicographic key
	// order (the S3 order). The prefix is segment-aligned: prefix "a/b"
	// matches "a/b/c" but not "a/bc". Use All to iterate every page.
	List(ctx context.Context, prefix Key, opts ListOptions) (ListPage, error)

	// Delete removes the object. Deleting a missing object succeeds
	// (idempotent). No-delete mode and active object locks yield
	// ErrDeleteNotPermitted.
	Delete(ctx context.Context, key Key) error

	// Test probes write, read and (unless in no-delete mode) delete
	// access with a small object below prefix (see RunProbe). It never
	// returns raw backend errors: each step carries a normalized code.
	Test(ctx context.Context, prefix Key) TestResult

	// Close releases the backend's resources. Later calls fail with
	// ErrClosed.
	io.Closer
}

// LockMode is an object-lock retention mode.
type LockMode string

// Object-lock retention modes (S3 semantics).
const (
	LockNone       LockMode = ""
	LockGovernance LockMode = "governance"
	LockCompliance LockMode = "compliance"
)

// Valid reports whether m is a known mode.
func (m LockMode) Valid() bool {
	switch m {
	case LockNone, LockGovernance, LockCompliance:
		return true
	default:
		return false
	}
}

// PutOptions are the per-object options of Put.
type PutOptions struct {
	// ContentType is stored where the backend supports it (S3).
	ContentType string
	// ExpectedSHA256, if set (32 bytes), must match the streamed bytes;
	// otherwise Put fails with ErrChecksumMismatch and no object exists.
	ExpectedSHA256 []byte
	// RetainUntil and LockMode set an object-lock retention. Both or
	// neither must be set. Backends without object lock fail closed with
	// ErrUnsupported.
	RetainUntil time.Time
	LockMode    LockMode
	// LegalHold places a legal hold (object-lock backends only).
	LegalHold bool
	// SizeHint is the expected size in bytes (0 = unknown). Backends may
	// use it for part sizing or an early free-space check.
	SizeHint int64
}

// Locked reports whether any object-lock option is set.
func (o PutOptions) Locked() bool {
	return !o.RetainUntil.IsZero() || o.LockMode != LockNone || o.LegalHold
}

// Checksum is a backend-native checksum (for example S3 CRC64NVME).
type Checksum struct {
	Algorithm string
	Value     string
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key     Key
	Size    int64
	ModTime time.Time
	// SHA256 is always set by Put. Get/Stat set it only when the backend
	// stores it as metadata (nil otherwise).
	SHA256         []byte
	NativeChecksum Checksum
	ContentType    string
	RetainUntil    time.Time
	LockMode       LockMode
	LegalHold      bool
	VersionID      string
}

// MaxListPageSize is the largest (and default) List page size.
const MaxListPageSize = 1000

// ListOptions control one List call.
type ListOptions struct {
	// PageSize is the maximum number of objects (0 or more than
	// MaxListPageSize means MaxListPageSize).
	PageSize int
	// StartAfter returns only keys strictly greater than this key
	// (normally ListPage.Next of the previous page).
	StartAfter Key
}

func (o ListOptions) pageSize() int {
	if o.PageSize <= 0 || o.PageSize > MaxListPageSize {
		return MaxListPageSize
	}
	return o.PageSize
}

// ListPage is one page of List results.
type ListPage struct {
	Objects []ObjectInfo
	// Next is the key of the last object in Objects; pass it as
	// ListOptions.StartAfter for the next page.
	Next Key
	// More reports whether more objects follow.
	More bool
}

// Capabilities describe a backend.
type Capabilities struct {
	// Type is "disk", "s3" or "sftp".
	Type string
	// SupportsObjectLock reports that Put honors lock options and
	// Delete may be refused by a lock.
	SupportsObjectLock bool
	// Versioning reports a versioned bucket (deletes leave versions).
	Versioning bool
	// NoDeleteMode reports that the destination is configured to never
	// delete (retention is left to lifecycle rules).
	NoDeleteMode bool
	// AtomicRename reports finalization by rename/link of a temporary
	// object (disk, SFTP) instead of a multipart commit (S3).
	AtomicRename bool
	// ExclusiveCreate reports that "never overwrite" is enforced
	// atomically by the backend, not by a check-then-write.
	ExclusiveCreate bool
	// NativeChecksum names the backend checksum algorithm ("" if none).
	NativeChecksum string
	// ServerSideEncryption reports server-side encryption at rest.
	ServerSideEncryption bool
	// MaxObjectSize is the largest object in bytes (0 = no limit).
	MaxObjectSize int64
}

// TestStep is one step of a destination test.
type TestStep struct {
	Name    string // "write", "read" or "delete"
	OK      bool
	Code    ErrorCode
	Message string // fixed text from ErrorCode.Message, never raw errors
}

// TestResult is the result of Destination.Test.
type TestResult struct {
	OK    bool
	Steps []TestStep
	// ProbeKey is the key of the probe object.
	ProbeKey Key
	// ProbeRemains reports that the probe object was left behind (no-delete
	// mode, or a failed delete).
	ProbeRemains bool
	// Note is a fixed, user-presentable remark ("" if none).
	Note string
}

// Probe step names.
const (
	StepWrite  = "write"
	StepRead   = "read"
	StepDelete = "delete"
)

// ProbeDir is the key segment below which test probes are written.
const ProbeDir = "bekup-probe"

const noDeleteNote = "no-delete mode: the probe object was not deleted; it is removed by the bucket lifecycle rules or must be removed manually"

// RunProbe implements Destination.Test for any backend: it writes a small
// object to {prefix}/bekup-probe/{timestamp}-{random}.probe, reads it back
// and compares it, and deletes it unless the destination is in no-delete
// mode.
func RunProbe(ctx context.Context, d Destination, prefix Key) TestResult {
	var res TestResult
	key, payload, err := newProbe(prefix, time.Now())
	if err != nil {
		res.Steps = append(res.Steps, failStep(StepWrite, err))
		return res
	}
	res.ProbeKey = key
	sum := sha256.Sum256(payload)
	if _, err := d.Put(ctx, key, bytes.NewReader(payload), PutOptions{
		ContentType:    "application/octet-stream",
		ExpectedSHA256: sum[:],
		SizeHint:       int64(len(payload)),
	}); err != nil {
		res.Steps = append(res.Steps, failStep(StepWrite, err))
		return res
	}
	res.ProbeRemains = true
	res.Steps = append(res.Steps, okStep(StepWrite))

	readOK := false
	if err := readProbe(ctx, d, key, payload); err != nil {
		res.Steps = append(res.Steps, failStep(StepRead, err))
	} else {
		readOK = true
		res.Steps = append(res.Steps, okStep(StepRead))
	}

	if d.Capabilities().NoDeleteMode {
		res.Note = noDeleteNote
		res.OK = readOK
		return res
	}
	if err := d.Delete(ctx, key); err != nil {
		res.Steps = append(res.Steps, failStep(StepDelete, err))
		return res
	}
	res.ProbeRemains = false
	res.Steps = append(res.Steps, okStep(StepDelete))
	res.OK = readOK
	return res
}

// probeMaxRead bounds how much of a (possibly tampered) probe is read.
const probeMaxRead = 4096

func readProbe(ctx context.Context, d Destination, key Key, want []byte) error {
	rc, _, err := d.Get(ctx, key)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(io.LimitReader(rc, probeMaxRead))
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: probe content differs", ErrChecksumMismatch)
	}
	return nil
}

func newProbe(prefix Key, now time.Time) (Key, []byte, error) {
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return Key{}, nil, fmt.Errorf("probe randomness: %w", err)
	}
	id := hex.EncodeToString(rnd[:])
	key, err := prefix.Join(ProbeDir, TimestampSegment(now)+"-"+id+".probe")
	if err != nil {
		return Key{}, nil, err
	}
	return key, []byte("bekup destination probe " + id + "\n"), nil
}

func okStep(name string) TestStep {
	return TestStep{Name: name, OK: true, Code: CodeOK, Message: CodeOK.Message()}
}

func failStep(name string, err error) TestStep {
	c := Classify(err)
	return TestStep{Name: name, Code: c, Message: c.Message()}
}

// All iterates every object below prefix across pages. Iteration stops
// at the first error, which is yielded once.
func All(ctx context.Context, d Destination, prefix Key) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		var after Key
		for {
			page, err := d.List(ctx, prefix, ListOptions{StartAfter: after})
			if err != nil {
				yield(ObjectInfo{}, err)
				return
			}
			for i := range page.Objects {
				if !yield(page.Objects[i], nil) {
					return
				}
			}
			if !page.More || page.Next.IsZero() {
				return
			}
			after = page.Next
		}
	}
}

// CheckPutOptions validates opts against the backend's capabilities. It
// fails closed: lock options on a backend without object lock yield
// ErrUnsupported instead of being ignored.
func CheckPutOptions(c Capabilities, opts PutOptions) error {
	if n := len(opts.ExpectedSHA256); n != 0 && n != sha256.Size {
		return fmt.Errorf("%w: expected SHA-256 must be %d bytes", ErrInvalidArgument, sha256.Size)
	}
	if opts.SizeHint < 0 {
		return fmt.Errorf("%w: negative size hint", ErrInvalidArgument)
	}
	if c.MaxObjectSize > 0 && opts.SizeHint > c.MaxObjectSize {
		return fmt.Errorf("%w: object exceeds the maximum object size", ErrUnsupported)
	}
	if !opts.LockMode.Valid() {
		return fmt.Errorf("%w: unknown lock mode", ErrInvalidArgument)
	}
	if opts.RetainUntil.IsZero() != (opts.LockMode == LockNone) {
		return fmt.Errorf("%w: retain-until and lock mode must be set together", ErrInvalidArgument)
	}
	if opts.Locked() && !c.SupportsObjectLock {
		return fmt.Errorf("%w: object lock", ErrUnsupported)
	}
	return nil
}

// ErrSource wraps errors returned by the Put source reader, so callers can
// tell a failing producer from a failing destination with errors.Is.
var ErrSource = errors.New("storage: reading the source failed")

// copyBufSize is the streaming buffer of CopyHashed.
const copyBufSize = 1 << 20

// CopyHashed copies r to w with a 1 MiB buffer, checking ctx before every
// read, and returns the byte count and SHA-256 of the copied bytes. A
// positive limit fails the copy with ErrUnsupported once more than limit
// bytes arrive. Source errors are wrapped with ErrSource.
func CopyHashed(ctx context.Context, w io.Writer, r io.Reader, limit int64) (int64, []byte, error) {
	h := sha256.New()
	buf := make([]byte, copyBufSize)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, nil, err
		}
		m, rerr := r.Read(buf)
		if m > 0 {
			n += int64(m)
			if limit > 0 && n > limit {
				return n, nil, fmt.Errorf("%w: object exceeds the maximum object size", ErrUnsupported)
			}
			h.Write(buf[:m])
			if _, werr := w.Write(buf[:m]); werr != nil {
				return n, nil, fmt.Errorf("storage: write: %w", werr)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return n, nil, fmt.Errorf("%w: %w", ErrSource, rerr)
		}
	}
	if err := ctx.Err(); err != nil {
		return n, nil, err
	}
	return n, h.Sum(nil), nil
}

// VerifySHA256 compares a computed digest with the expected one (if set).
func VerifySHA256(expected, got []byte) error {
	if len(expected) == 0 {
		return nil
	}
	if !bytes.Equal(expected, got) {
		return ErrChecksumMismatch
	}
	return nil
}
