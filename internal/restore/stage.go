package restore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	streamdigest "github.com/jlentink/bekup/internal/digest"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// Staged is a restore's verified local copy of its artifact. Call Cleanup
// when done (it also runs when the context given to Stage ends).
type Staged struct {
	s        *Service
	restore  sqlc.Restore
	row      sqlc.GetDownloadArtifactRow
	manifest *pipeline.Manifest
	format   pipeline.DumpFormat
	size     int64

	mu        sync.Mutex
	ephemeral *crypto.Secret // the age identity wrapping the staged file
	recipient string         // its recipient (public; only in memory too)
	identity  *crypto.Secret // the user's identity (recipient mode)
	opened    bool
	cleaned   bool
	reader    *plainReader
	// unregIdentity removes the user's identity from the log redaction
	// registry (set while identity is held).
	unregIdentity func()

	hold        *runstate.Hold
	stopHold    func()
	stopAfter   func() bool
	stageCancel context.CancelCauseFunc
	cleanupOnce sync.Once
}

// Restore is the restore row as staged.
func (st *Staged) Restore() sqlc.Restore { return st.restore }

// Artifact is the staged artifact's record.
func (st *Staged) Artifact() sqlc.Artifact { return st.row.Artifact }

// Manifest is the verified, signed manifest of the artifact's run (source
// statistics for test restores, task 4.6).
func (st *Staged) Manifest() *pipeline.Manifest { return st.manifest }

// Format is the dump format inside the artifact.
func (st *Staged) Format() pipeline.DumpFormat { return st.format }

// StoredSize is the size of the verified stored object.
func (st *Staged) StoredSize() int64 { return st.size }

// RawSize is the recorded size of the plaintext dump (0 when unknown).
func (st *Staged) RawSize() int64 {
	if st.row.ItemRawSize == nil {
		return 0
	}
	return max(*st.row.ItemRawSize, 0)
}

// Stage runs the staging and verification steps of a queued restore (the
// restore worker's first step): queued -> staging -> verifying. On success
// the restore is verifying and the returned Staged holds the verified
// copy; the caller moves on with BeginRestore and Open and must call
// Cleanup (which also runs when ctx ends, so ctx must be the worker's
// context that lives for the whole restore). On failure the staging is
// removed, the restore is failed (canceled when ctx was canceled) with a
// sanitized code, and a verification failure is audited and alerted. A
// restore that is no longer queued is left untouched (ErrNotQueued).
func (s *Service) Stage(ctx context.Context, restoreID uuid.UUID) (staged *Staged, err error) {
	st := &Staged{s: s, restore: sqlc.Restore{ID: restoreID}, stopHold: func() {}, stopAfter: func() bool { return false }}
	owned := false
	defer func() {
		if p := recover(); p != nil {
			st.Cleanup()
			if owned {
				s.finishFailure(ctx, restoreID, ErrPanic)
			}
			panic(p)
		}
		if err != nil {
			st.Cleanup()
			if owned {
				s.finishFailure(ctx, restoreID, err)
			}
			staged = nil
		}
	}()
	if err := s.begin(ctx, st); err != nil {
		// Only a restore this call moved to staging is finished here; one
		// that is still queued (the transaction failed) is the caller's.
		owned = st.restore.Status == StatusStaging
		return nil, err
	}
	owned = true
	// From here on the staging directory may exist: the sweeper of this
	// process skips it and Cleanup removes it, also when ctx ends.
	st.stopAfter = context.AfterFunc(ctx, st.Cleanup)
	if err := s.prepareKeys(ctx, st); err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	st.mu.Lock()
	st.stageCancel = cancel
	st.mu.Unlock()
	dest, err := s.o.Destinations.Open(sctx, st.row.Artifact.DestinationID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDestination, err)
	}
	defer func() {
		if cerr := dest.Close(); cerr != nil {
			s.o.Logger.WarnContext(ctx, "closing the destination failed", slog.Any("error", cerr))
		}
	}()
	digest, err := s.download(sctx, st, dest)
	if err != nil {
		if cause := context.Cause(sctx); errors.Is(cause, runstate.ErrHoldLost) {
			return nil, cause
		}
		return nil, err
	}
	if err := s.advance(ctx, restoreID, StatusStaging, StatusVerifying); err != nil {
		return nil, err
	}
	if err := s.verify(sctx, st, dest, digest); err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.stageCancel = nil
	st.mu.Unlock()
	if _, err := sqlc.New(s.o.DB).SetRestoreStagedBytes(ctx, sqlc.SetRestoreStagedBytesParams{
		ID: restoreID, StagedBytes: st.size,
	}); err != nil {
		return nil, fmt.Errorf("restore: record staged bytes: %w", err)
	}
	return st, nil
}

// begin moves the restore to staging, loads its artifact, holds it and
// takes the identity handoff, in one transaction.
func (s *Service) begin(ctx context.Context, st *Staged) error {
	var handoff *sqlc.TakeRestoreIdentityHandoffRow
	var held bool
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := q.StartRestoreStaging(ctx, st.restore.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotQueued
		}
		if err != nil {
			return fmt.Errorf("restore: start staging: %w", err)
		}
		st.restore = r
		if err := s.publish(ctx, q, EventRestore, r.ID); err != nil {
			return err
		}
		if st.row, err = q.GetDownloadArtifact(ctx, r.ArtifactID); err != nil {
			return fmt.Errorf("restore: load artifact: %w", err)
		}
		// Hold the artifact for the whole restore (D-155): retention skips
		// a held artifact, and the hold is taken under a share lock of the
		// present artifact row, so it cannot interleave with a deletion.
		h, ok, err := runstate.AcquireHoldTx(ctx, q, r.ArtifactID, runstate.HoldRestore, r.ID, s.o.HoldTTL)
		if err != nil {
			return err
		}
		if ok {
			st.hold, held = &h, true
		}
		// Read the identity once: the row is gone when this commits.
		ho, err := q.TakeRestoreIdentityHandoff(ctx, r.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("restore: take identity handoff: %w", err)
		default:
			handoff = &ho
		}
		return nil
	})
	if err != nil {
		st.restore.Status = ""
		return err
	}
	defer func() {
		if handoff != nil {
			crypto.Zero(handoff.SealedIdentity)
		}
	}()
	st.restore.Status = StatusStaging
	if held {
		st.stopHold = s.keepHold(ctx, st)
	}
	if !held || st.row.Artifact.RunItemID == nil || *st.row.Artifact.RunItemID != st.restore.RunItemID {
		return ErrUnavailable
	}
	if err := checkArtifact(&st.row); err != nil {
		return ErrUnavailable
	}
	st.size = st.row.Artifact.Size
	plainExt, _, err := artifactverify.StoredFormat(st.row.Artifact.ObjectKey, encryptionOf(&st.row.Artifact) != encNone)
	if err != nil {
		return ErrUnavailable
	}
	st.format = pipeline.DumpFormat(plainExt)
	if encryptionOf(&st.row.Artifact) != encRecipients {
		return nil
	}
	switch {
	case handoff == nil:
		return ErrIdentityRequired
	case !handoff.Valid:
		return ErrIdentityExpired
	}
	id, err := s.o.Keyring.Open(handoff.SealedIdentity, identityAAD(st.restore.ID))
	if err != nil {
		return fmt.Errorf("restore: open identity handoff: %w", err)
	}
	st.mu.Lock()
	st.identity = crypto.NewSecret(id)
	// Redact it from every log line while the restore holds it.
	st.unregIdentity = logging.DefaultRegistry.Register(id)
	st.mu.Unlock()
	return nil
}

// dropIdentityLocked destroys the user's identity and removes it from the
// redaction registry (st.mu held).
func (st *Staged) dropIdentityLocked() {
	if st.identity != nil {
		st.identity.Destroy()
		st.identity = nil
	}
	if st.unregIdentity != nil {
		st.unregIdentity()
		st.unregIdentity = nil
	}
}

// prepareKeys checks the artifact's key before anything is downloaded and
// creates the ephemeral key of the staged file.
func (s *Service) prepareKeys(ctx context.Context, st *Staged) error {
	a := &st.row.Artifact
	switch encryptionOf(a) {
	case encRecipients:
		st.mu.Lock()
		id := st.identity
		st.mu.Unlock()
		if id == nil {
			return ErrIdentityRequired
		}
		if err := s.matchIdentity(ctx, a, id.Bytes()); err != nil {
			if errors.Is(err, artifactverify.ErrIdentityInvalid) {
				return ErrIdentityMismatch
			}
			return err
		}
	case encPassphrase:
		p, err := s.passphrase(ctx, a)
		if err != nil {
			return err
		}
		p.Destroy()
	}
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		return fmt.Errorf("restore: ephemeral key: %w", err)
	}
	st.mu.Lock()
	st.ephemeral = kp.Identity
	st.mu.Unlock()
	st.recipient = kp.Recipient
	return nil
}

// stagingOverhead bounds what the ephemeral age wrapping adds to n bytes
// (header, nonce and a 16-byte tag per 64 KiB chunk).
func stagingOverhead(n int64) uint64 {
	return uint64(n/(64<<10)+1)*16 + 4096 //nolint:gosec // G115: n >= 0 (artifacts_size_positive)
}

// download checks the free space, then streams the stored object into the
// staging file (wrapped with the ephemeral key) while hashing it. It
// returns the digest of the stored bytes.
func (s *Service) download(ctx context.Context, st *Staged, dest storage.Destination) (*artifactverify.DigestReader, error) {
	a := &st.row.Artifact
	if a.Size < 0 {
		return nil, ErrUnavailable
	}
	if err := s.checkScratch(); err != nil {
		return nil, err
	}
	need := uint64(a.Size) + stagingOverhead(a.Size)
	reserve := uint64(max(s.o.ScratchReserve, 0))
	switch err := storage.CheckFreeSpace(s.o.ScratchDir, need, reserve); {
	case errors.Is(err, storage.ErrUnsupported):
		s.o.Logger.WarnContext(ctx, "the scratch volume's free space cannot be checked on this platform")
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInsufficientSpace, err)
	}
	key, err := storage.ParseKey(a.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("restore: object key: %w", err)
	}
	s.markLive(st.restore.ID, true)
	if err := s.makeDir(st.restore.ID); err != nil {
		return nil, err
	}
	rc, info, err := dest.Get(ctx, key)
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, fmt.Errorf("%w: %w", ErrDestination, err)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			s.o.Logger.DebugContext(ctx, "closing the artifact reader failed", slog.Any("error", cerr))
		}
	}()
	if info.Size > 0 && info.Size != a.Size || len(info.SHA256) > 0 && !bytes.Equal(info.SHA256, a.Sha256) {
		return nil, s.tampered(ctx, st, &VerificationError{Code: VerifyStoredDigest,
			Err: errors.New("restore: stored object differs from the record")})
	}
	digest := artifactverify.NewDigestReader(streamdigest.NewCtxReader(ctx, rc, ErrDestination), a.Size)
	r, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.OpenFile(stagedPath(st.restore.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("restore: create the staging file: %w", err)
	}
	werr := s.writeStaged(f, digest, st.recipient)
	cerr := f.Close()
	switch {
	case ctx.Err() != nil:
		return nil, context.Cause(ctx)
	case errors.Is(werr, artifactverify.ErrTooLong):
		return nil, s.tampered(ctx, st, &VerificationError{Code: VerifyStoredDigest, Err: werr})
	case werr != nil:
		// Read errors of the destination carry ErrDestination (streamdigest.CtxReader);
		// anything else is a local write failure.
		return nil, fmt.Errorf("restore: stage the artifact: %w", werr)
	case cerr != nil:
		return nil, fmt.Errorf("restore: close the staging file: %w", cerr)
	}
	return digest, nil
}

// writeStaged copies src into f through an age encryption to the
// ephemeral recipient.
func (s *Service) writeStaged(f *os.File, src io.Reader, recipient string) error {
	bw := bufio.NewWriterSize(f, 256<<10)
	w, err := crypto.EncryptToRecipients(bw, recipient)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return bw.Flush()
}

// verify checks the staged digest against the record and the signed
// manifest; failures are audited and alerted.
func (s *Service) verify(ctx context.Context, st *Staged, dest storage.Destination, digest *artifactverify.DigestReader) error {
	a := &st.row.Artifact
	if !digest.Matches(a.Size, a.Sha256) {
		return s.tampered(ctx, st, &VerificationError{Code: VerifyStoredDigest, Err: errors.New("restore: stored digest mismatch")})
	}
	m, err := artifactverify.VerifyManifest(ctx, sqlc.New(s.o.DB), dest, artifactverify.FromDownloadRow(&st.row))
	switch {
	case err == nil:
	case errors.Is(err, artifactverify.ErrManifest):
		return s.tampered(ctx, st, manifestFailure(err))
	case errors.Is(err, artifactverify.ErrDestination):
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fmt.Errorf("%w: %w", ErrDestination, err)
	default:
		return err
	}
	st.manifest = m
	return nil
}

// tampered audits and alerts a verification failure and returns ve.
func (s *Service) tampered(ctx context.Context, st *Staged, ve *VerificationError) error {
	a := &st.row.Artifact
	details := audit.Details{
		"reason": ve.Code, "artifact_id": a.ID.String(), "job_id": a.JobID.String(), "run_id": a.RunID.String(),
		"destination_id": a.DestinationID.String(), "object_key": a.ObjectKey,
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := s.o.Auditor.Record(actx, audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionRestoreVerificationFailed,
		TargetType: "restore", TargetID: st.restore.ID.String(), Details: details,
	}); err != nil {
		s.o.Logger.ErrorContext(actx, "recording a restore verification failure failed", slog.Any("error", err))
	}
	s.o.Logger.ErrorContext(actx, "restore artifact verification failed", slog.String("reason", ve.Code),
		slog.String("restore_id", st.restore.ID.String()), slog.String("artifact_id", a.ID.String()))
	s.o.Alert(actx, Alert{
		Kind: AlertArtifactTampered, RestoreID: st.restore.ID, JobID: a.JobID, ArtifactID: a.ID,
		Details: map[string]any{"reason": ve.Code, "run_id": a.RunID.String(), "destination_id": a.DestinationID.String()},
	})
	return ve
}

// advance moves the restore from one status to the next.
func (s *Service) advance(ctx context.Context, id uuid.UUID, from, to Status) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		n, err := q.AdvanceRestoreStatus(ctx, sqlc.AdvanceRestoreStatusParams{ID: id, FromStatus: from, ToStatus: to})
		if err != nil {
			return fmt.Errorf("restore: set status %s: %w", to, err)
		}
		if n == 0 {
			return fmt.Errorf("restore: restore %s is no longer %s", id, from)
		}
		return s.publish(ctx, q, EventRestore, id)
	})
}

// finishFailure fails (or cancels) a restore whose staging failed.
func (s *Service) finishFailure(ctx context.Context, id uuid.UUID, cause error) {
	status := StatusFailed
	if errors.Is(cause, context.Canceled) && !errors.Is(cause, runstate.ErrHoldLost) {
		status = StatusCanceled
	}
	if _, err := s.Finish(ctx, id, status, cause); err != nil {
		s.o.Logger.ErrorContext(context.WithoutCancel(ctx), "recording the restore failure failed", slog.Any("error", err))
	}
	s.o.Logger.WarnContext(context.WithoutCancel(ctx), "restore staging failed", slog.String("restore_id", id.String()),
		slog.String("code", ErrorCode(cause)))
}

// keepHold renews the artifact hold until the returned stop is called (or
// ctx ends) and then releases it. A hold that expired before it could be
// renewed cancels a staging in progress; after the staging the verified
// local copy no longer needs the stored object, so a lost hold is logged.
func (s *Service) keepHold(ctx context.Context, st *Staged) func() {
	hctx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(s.o.HoldTTL / 3)
		defer t.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-t.C:
				err := runstate.RenewHold(hctx, s.o.DB, st.hold.ID, s.o.HoldTTL)
				switch {
				case errors.Is(err, runstate.ErrHoldLost):
					s.o.Logger.ErrorContext(hctx, "restore hold expired", slog.String("restore_id", st.restore.ID.String()))
					st.mu.Lock()
					c := st.stageCancel
					st.mu.Unlock()
					if c != nil {
						c(runstate.ErrHoldLost)
					}
					return
				case err != nil && hctx.Err() == nil:
					s.o.Logger.WarnContext(hctx, "renewing the restore hold failed", slog.Any("error", err))
				}
			}
		}
	}()
	return func() {
		cancel()
		<-finished
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer rcancel()
		if err := runstate.ReleaseHold(rctx, s.o.DB, st.hold.ID); err != nil {
			// It expires on its own.
			s.o.Logger.WarnContext(rctx, "releasing the restore hold failed", slog.Any("error", err))
		}
	}
}

// BeginRestore moves the verified restore to restoring (the runner phase,
// tasks 4.2/4.3).
func (st *Staged) BeginRestore(ctx context.Context) error {
	return st.s.advance(ctx, st.restore.ID, StatusVerifying, StatusRestoring)
}

// Cleanup removes the staging directory, stops and releases the artifact
// hold and destroys the in-memory keys. It is idempotent and safe to call
// at any time (an open reader fails afterwards).
func (st *Staged) Cleanup() {
	st.cleanupOnce.Do(func() {
		st.stopAfter()
		st.mu.Lock()
		st.cleaned = true
		if st.reader != nil {
			// Cleanup may run on another goroutine (the context's
			// AfterFunc) while the reader's owner is inside Read: it only
			// marks the reader aborted, so the next Read fails, and the
			// owner closes it (the zstd decoder must not be closed during
			// a Read).
			st.reader.abort()
			st.reader = nil
		}
		if st.ephemeral != nil {
			st.ephemeral.Destroy()
			st.ephemeral = nil
		}
		st.dropIdentityLocked()
		st.mu.Unlock()
		if err := st.s.removeDir(st.restore.ID); err != nil {
			// The sweeper retries.
			st.s.o.Logger.Error("removing the restore staging directory failed", slog.String("restore_id", st.restore.ID.String()),
				slog.Any("error", err))
		}
		st.s.markLive(st.restore.ID, false)
		st.stopHold()
	})
}

// Open returns the dump inside the staged artifact: the staged file is
// unwrapped with the ephemeral key, decrypted with the job's passphrase
// version or the handed-off identity (which is destroyed right after) and
// decompressed with the bounded helpers of pipeline.OpenArtifact. The
// stream is bounded by the recorded dump size; at its end the stored and
// the dump digests are checked again, and a mismatch is returned instead
// of io.EOF (and audited and alerted). Open may be called once.
func (st *Staged) Open(ctx context.Context) (io.ReadCloser, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.cleaned:
		return nil, ErrCleanedUp
	case st.opened:
		return nil, ErrAlreadyOpened
	case st.ephemeral == nil:
		return nil, ErrCleanedUp
	}
	st.opened = true
	a := &st.row.Artifact
	var dec pipeline.Decryption
	switch encryptionOf(a) {
	case encPassphrase:
		p, err := st.s.passphrase(ctx, a)
		if err != nil {
			return nil, err
		}
		defer p.Destroy()
		dec.Passphrase = p.Bytes()
	case encRecipients:
		if st.identity == nil {
			return nil, ErrIdentityRequired
		}
		// The identity is used once: age unwraps the file key in
		// OpenArtifact, so it is destroyed when Open returns.
		defer st.dropIdentityLocked()
		dec.Identities = st.identity.Bytes()
	default:
		dec.None = true
	}
	root, err := st.s.openRoot()
	if err != nil {
		return nil, err
	}
	f, err := root.Open(stagedPath(st.restore.ID))
	cerr := root.Close()
	if err != nil {
		return nil, fmt.Errorf("restore: open the staged file: %w", err)
	}
	if cerr != nil {
		st.s.closeQuiet(f)
		return nil, fmt.Errorf("restore: close the scratch root: %w", cerr)
	}
	outer, err := crypto.DecryptWithIdentities(bufio.NewReaderSize(f, 256<<10), st.ephemeral.Bytes())
	if err != nil {
		st.s.closeQuiet(f)
		return nil, fmt.Errorf("restore: unwrap the staged file: %w", err)
	}
	stored := artifactverify.NewDigestReader(outer, a.Size)
	plain, err := pipeline.OpenArtifact(stored, dec)
	if err != nil {
		st.s.closeQuiet(f)
		if encryptionOf(a) == encRecipients {
			return nil, fmt.Errorf("%w: %w", ErrIdentityMismatch, err)
		}
		return nil, st.s.tampered(ctx, st, &VerificationError{Code: VerifyDecrypt, Err: err})
	}
	limit := int64(math.MaxInt64)
	if st.row.ItemRawSize != nil && *st.row.ItemRawSize > 0 {
		limit = *st.row.ItemRawSize
	}
	pr := &plainReader{st: st, ctx: context.WithoutCancel(ctx), f: f, plain: plain, stored: stored,
		raw: artifactverify.NewDigestReader(plain, limit)}
	st.reader = pr
	return pr, nil
}

// plainReader is the stream returned by Open. Read and Close belong to
// the caller of Open (one goroutine); Staged.Cleanup only calls abort,
// which is safe concurrently with Read.
type plainReader struct {
	st      *Staged
	ctx     context.Context
	f       *os.File
	plain   io.ReadCloser
	stored  *artifactverify.DigestReader
	raw     *artifactverify.DigestReader
	done    error
	once    sync.Once
	aborted atomic.Bool
}

// abort makes every later Read fail with ErrCleanedUp.
func (p *plainReader) abort() { p.aborted.Store(true) }

func (p *plainReader) Read(b []byte) (int, error) {
	if p.aborted.Load() {
		return 0, ErrCleanedUp
	}
	if p.done != nil {
		return 0, p.done
	}
	n, err := p.raw.Read(b)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		p.done = p.finish()
	case errors.Is(err, artifactverify.ErrTooLong):
		p.done = p.st.s.tampered(p.ctx, p.st, &VerificationError{Code: VerifyDumpDigest, Err: err})
	default:
		p.done = err
	}
	return n, p.done
}

// finish drains the rest of the stored stream (age's last chunk, the zstd
// frame end) and compares both digests with the records.
func (p *plainReader) finish() error {
	if _, err := io.Copy(io.Discard, p.stored); err != nil {
		if errors.Is(err, artifactverify.ErrTooLong) {
			return p.st.s.tampered(p.ctx, p.st, &VerificationError{Code: VerifyStoredDigest, Err: err})
		}
		return err
	}
	row := &p.st.row
	if !p.stored.Matches(row.Artifact.Size, row.Artifact.Sha256) {
		return p.st.s.tampered(p.ctx, p.st, &VerificationError{Code: VerifyStoredDigest, Err: errors.New("restore: stored digest mismatch")})
	}
	if row.ItemRawSize != nil && *row.ItemRawSize > 0 && p.raw.N() != *row.ItemRawSize ||
		len(row.ItemRawSha256) > 0 && !p.raw.Matches(p.raw.N(), row.ItemRawSha256) {
		return p.st.s.tampered(p.ctx, p.st, &VerificationError{Code: VerifyDumpDigest, Err: errors.New("restore: dump digest mismatch")})
	}
	return io.EOF
}

func (p *plainReader) Close() error {
	var err error
	p.once.Do(func() {
		err = errors.Join(p.plain.Close(), p.f.Close())
	})
	return err
}

// closeQuiet closes a file whose read is abandoned (the error changes
// nothing; it is logged at debug level).
func (s *Service) closeQuiet(f *os.File) {
	if err := f.Close(); err != nil {
		s.o.Logger.Debug("closing a staged file failed", slog.Any("error", err))
	}
}
