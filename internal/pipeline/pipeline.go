package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jlentink/bekup/internal/storage"
)

// Limits and defaults of a pipeline run.
const (
	// DefaultCompressionLevel is the zstd level used when Spec leaves it 0.
	DefaultCompressionLevel = 3
	// MaxCompressionLevel is the highest zstd level (levels above 19 map to
	// the encoder's best mode).
	MaxCompressionLevel = 22
	// DefaultCompressionConcurrency is the number of zstd encoder
	// goroutines per stream when Spec leaves it 0.
	DefaultCompressionConcurrency = 2
	// MaxCompressionConcurrency bounds the zstd encoder goroutines (and
	// their window buffers) per stream.
	MaxCompressionConcurrency = 8

	// DefaultChunkSize is the fan-out chunk size.
	DefaultChunkSize = 1 << 20
	// MinChunkSize and MaxChunkSize bound Spec.ChunkSize.
	MinChunkSize = 4 << 10
	MaxChunkSize = 16 << 20
	// DefaultBufferChunks is the per-destination buffer depth in chunks.
	DefaultBufferChunks = 8
	// MaxBufferChunks bounds Spec.BufferChunks.
	MaxBufferChunks = 256
	// DefaultLagTimeout is how long a destination may keep its buffer full
	// (or, after the stream ended, read nothing while data is pending)
	// before it is dropped.
	DefaultLagTimeout = 2 * time.Minute
	// MaxTargets bounds the destinations of one artifact.
	MaxTargets = 16

	// DefaultContentType is the Put content type when a target sets none.
	DefaultContentType = "application/octet-stream"
	// CompressionZstd is the only compression algorithm (manifest value).
	CompressionZstd = "zstd"

	readBufSize = 256 << 10
)

// Run errors. Compare with errors.Is.
var (
	// ErrInvalidSpec reports an invalid Spec (Run did not start).
	ErrInvalidSpec = errors.New("pipeline: invalid spec")
	// ErrSourceFailed wraps errors of the dump stream (the source reader).
	ErrSourceFailed = errors.New("pipeline: reading the dump stream failed")
	// ErrAllDestinationsFailed reports that every destination was dropped.
	ErrAllDestinationsFailed = errors.New("pipeline: every destination failed")
	// ErrProcessing wraps compression or encryption failures.
	ErrProcessing = errors.New("pipeline: compression or encryption failed")
)

// Target is one destination of an artifact.
type Target struct {
	// Name labels the target in results (normally the destination ID).
	// Names must be unique within a Spec.
	Name string
	Dest storage.Destination
	// Key is the full object key (see ArtifactKey).
	Key storage.Key
	// Options are passed to Put. ExpectedSHA256 must be empty (the
	// digest is not known before the stream ends); SizeHint is optional.
	Options storage.PutOptions
}

// Spec configures one pipeline run (one artifact).
type Spec struct {
	Encryption Encryption
	// CompressionLevel is the zstd level 1..22 (0 = 3).
	CompressionLevel int
	// CompressionConcurrency is the number of zstd encoder goroutines
	// 1..MaxCompressionConcurrency (0 = 2).
	CompressionConcurrency int
	Targets                []Target
	// ChunkSize is the fan-out chunk size (0 = 1 MiB).
	ChunkSize int
	// BufferChunks is each destination's buffer depth (0 = 8).
	BufferChunks int
	// LagTimeout is the lag threshold (0 = DefaultLagTimeout).
	LagTimeout time.Duration
}

func (s *Spec) normalize() error {
	if s.CompressionLevel == 0 {
		s.CompressionLevel = DefaultCompressionLevel
	}
	if s.CompressionConcurrency == 0 {
		s.CompressionConcurrency = DefaultCompressionConcurrency
	}
	if s.ChunkSize == 0 {
		s.ChunkSize = DefaultChunkSize
	}
	if s.BufferChunks == 0 {
		s.BufferChunks = DefaultBufferChunks
	}
	if s.LagTimeout == 0 {
		s.LagTimeout = DefaultLagTimeout
	}
	switch {
	case s.CompressionLevel < 1 || s.CompressionLevel > MaxCompressionLevel:
		return fmt.Errorf("%w: compression level must be 1..%d", ErrInvalidSpec, MaxCompressionLevel)
	case s.CompressionConcurrency < 1 || s.CompressionConcurrency > MaxCompressionConcurrency:
		return fmt.Errorf("%w: compression concurrency must be 1..%d", ErrInvalidSpec, MaxCompressionConcurrency)
	case s.ChunkSize < MinChunkSize || s.ChunkSize > MaxChunkSize:
		return fmt.Errorf("%w: chunk size must be %d..%d", ErrInvalidSpec, MinChunkSize, MaxChunkSize)
	case s.BufferChunks < 1 || s.BufferChunks > MaxBufferChunks:
		return fmt.Errorf("%w: buffer chunks must be 1..%d", ErrInvalidSpec, MaxBufferChunks)
	case s.LagTimeout < 0:
		return fmt.Errorf("%w: negative lag timeout", ErrInvalidSpec)
	case len(s.Targets) == 0 || len(s.Targets) > MaxTargets:
		return fmt.Errorf("%w: 1..%d targets required", ErrInvalidSpec, MaxTargets)
	}
	if err := s.Encryption.Validate(); err != nil {
		return err
	}
	names := make(map[string]bool, len(s.Targets))
	targets := make([]Target, len(s.Targets))
	for i := range s.Targets {
		t := s.Targets[i]
		switch {
		case t.Name == "" || names[t.Name]:
			return fmt.Errorf("%w: target %d needs a unique name", ErrInvalidSpec, i+1)
		case t.Dest == nil:
			return fmt.Errorf("%w: target %q has no destination", ErrInvalidSpec, t.Name)
		case t.Key.IsZero():
			return fmt.Errorf("%w: target %q has no key", ErrInvalidSpec, t.Name)
		case len(t.Options.ExpectedSHA256) != 0:
			return fmt.Errorf("%w: target %q sets an expected SHA-256", ErrInvalidSpec, t.Name)
		}
		names[t.Name] = true
		if t.Options.ContentType == "" {
			t.Options.ContentType = DefaultContentType
		}
		targets[i] = t
	}
	s.Targets = targets
	return nil
}

// Outcome is the result of one destination.
type Outcome string

// Destination outcomes.
const (
	OutcomeOK      Outcome = "ok"
	OutcomeDropped Outcome = "dropped"
)

// DropReason says why a destination was dropped.
type DropReason string

// Drop reasons.
const (
	ReasonNone DropReason = ""
	// ReasonError: Put failed.
	ReasonError DropReason = "error"
	// ReasonLag: the destination kept its buffer full (or read nothing
	// while data was pending) beyond the lag threshold.
	ReasonLag DropReason = "lag"
	// ReasonAborted: the run failed or was canceled.
	ReasonAborted DropReason = "aborted"
	// ReasonVerify: Put reported success, but the stored size or SHA-256
	// differs from the streamed bytes (the object is deleted best-effort).
	ReasonVerify DropReason = "verify"
)

// DestinationResult is the outcome of one target.
type DestinationResult struct {
	Name    string
	Key     storage.Key
	Outcome Outcome
	Reason  DropReason
	// Code is the normalized, user-presentable error class
	// (storage.CodeOK for successful targets).
	Code storage.ErrorCode
	// Err is the raw error. It may hold backend details (hosts, paths):
	// never show it to users; log it only through the redacting logger.
	Err error
	// Info is the stored object (successful targets only).
	Info storage.ObjectInfo
}

// Result describes a pipeline run. Run returns it also on error, with the
// destination outcomes known so far.
type Result struct {
	// RawSize and RawSHA256 cover the dump stream as read from the
	// source (before compression).
	RawSize   int64
	RawSHA256 []byte
	// StoredSize and SHA256 cover the stored artifact bytes (after
	// compression and encryption).
	StoredSize   int64
	SHA256       []byte
	Compression  string
	Encryption   EntryEncryption
	Destinations []DestinationResult
	// PeakBufferedBytes is the largest number of fan-out bytes held at
	// once (dispatched chunks still referenced by a destination).
	PeakBufferedBytes int64
	Duration          time.Duration
}

// OK returns the successful destinations.
func (r *Result) OK() []DestinationResult {
	var out []DestinationResult
	for i := range r.Destinations {
		if r.Destinations[i].Outcome == OutcomeOK {
			out = append(out, r.Destinations[i])
		}
	}
	return out
}

// Partial reports that at least one destination succeeded and at least one
// was dropped (the run becomes "partial").
func (r *Result) Partial() bool {
	ok := len(r.OK())
	return ok > 0 && ok < len(r.Destinations)
}

// Entry returns the manifest entry of this artifact (dbName is ignored for
// globals).
func (r *Result) Entry(kind EntryKind, dbName, path string) Entry {
	db := EncodeName(dbName)
	if kind == KindGlobals {
		db = ""
	}
	return Entry{
		DB:          db,
		Kind:        kind,
		Path:        path,
		RawSize:     r.RawSize,
		StoredSize:  r.StoredSize,
		SHA256:      hexString(r.SHA256),
		RawSHA256:   hexString(r.RawSHA256),
		Compression: r.Compression,
		Encryption:  r.Encryption,
	}
}

// Run streams src through counter + SHA-256 → zstd → age → SHA-256 and
// fans the artifact out to every target with a bounded buffer each.
//
// A target whose Put fails, or whose buffer stays full for longer than
// LagTimeout, is dropped: its Put is canceled (the backends' atomic
// finalize leaves no object) and the others continue. Run fails with
// ErrAllDestinationsFailed when every target is dropped, and with the
// context's error when ctx ends; in both cases every Put is canceled and no
// object is finalized. Memory is bounded: the fan-out holds at most
// (BufferChunks + 2) × ChunkSize bytes, plus one chunk per dropped target
// whose backend has not yet returned from Put. A slow but live target
// applies backpressure to src; a dropped one never does.
//
// src is read until io.EOF; Run never closes it.
func Run(ctx context.Context, src io.Reader, spec Spec) (Result, error) {
	start := time.Now()
	if err := spec.normalize(); err != nil {
		return Result{}, err
	}
	res := Result{Compression: CompressionZstd, Encryption: spec.Encryption.manifestEncryption()}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fo := newFanout(runCtx, &spec)
	fo.start()

	rawN, rawSum, err := produce(runCtx, src, &spec, fo)
	res.RawSize = rawN
	if err == nil {
		err = fo.finish()
	} else {
		fo.abort(err)
	}
	res.StoredSize, res.SHA256 = fo.storedSize, fo.storedSum
	res.RawSHA256 = rawSum
	res.Destinations = fo.results(ctx)
	res.PeakBufferedBytes = fo.peak.Load()
	res.Duration = time.Since(start)
	if err != nil {
		res.RawSHA256, res.SHA256 = nil, nil
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			err = fmt.Errorf("%w (%w)", ctxErr, err)
		}
		return res, err
	}
	if len(res.OK()) == 0 {
		res.RawSHA256, res.SHA256 = nil, nil
		return res, ErrAllDestinationsFailed
	}
	return res, nil
}

// produce runs the source → zstd → age → fan-out chain and returns the raw
// byte count and SHA-256. On success the fan-out holds every artifact byte
// (finish still has to flush it).
func produce(ctx context.Context, src io.Reader, spec *Spec, fo *fanout) (int64, []byte, error) {
	encW, err := spec.Encryption.newEncryptWriter(fo)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrProcessing, err)
	}
	zw, err := zstd.NewWriter(encW,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(spec.CompressionLevel)),
		zstd.WithEncoderConcurrency(spec.CompressionConcurrency),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: zstd: %w", ErrProcessing, err)
	}
	fail := func(err error) error {
		// Make every further write into the fan-out fail at once, then
		// release the encoder goroutines.
		fo.fail(err)
		_ = zw.Close()   //nolint:gosec // G104: the run already failed; the close only releases goroutines
		_ = encW.Close() //nolint:gosec // G104: as above
		return err
	}

	h := sha256.New()
	buf := make([]byte, readBufSize)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, nil, fail(err)
		}
		m, rerr := src.Read(buf)
		if m > 0 {
			n += int64(m)
			h.Write(buf[:m])
			if _, werr := zw.Write(buf[:m]); werr != nil {
				return n, nil, fail(fo.cause(werr))
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return n, nil, fail(fmt.Errorf("%w: %w", ErrSourceFailed, rerr))
		}
	}
	if err := ctx.Err(); err != nil {
		return n, nil, fail(err)
	}
	if err := zw.Close(); err != nil {
		return n, nil, fail(fo.cause(err))
	}
	if err := encW.Close(); err != nil {
		return n, nil, fail(fo.cause(err))
	}
	return n, h.Sum(nil), nil
}
