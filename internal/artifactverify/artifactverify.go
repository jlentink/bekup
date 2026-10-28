// Package artifactverify holds the verification steps that every reader of
// a stored backup artifact with access to the bekup database shares
// (downloads, task 3.9; restores, task 4.1; D-150, D-162):
//
//   - VerifyManifest reads the run's manifest.json from the artifact's
//     destination and verifies it against the database records: the
//     instance signature (pipeline.Verify), the binding to the job, run and
//     the exact signed entries (defeats replayed manifests), the
//     encryption facts of the artifact's entry, and the manifest object's
//     own recorded digest.
//   - DigestReader hashes and counts a stream and refuses more bytes than
//     recorded.
//   - MatchIdentity checks a user-supplied age identity against the
//     recipient a job encrypted to.
//
// Offline tooling without the database (task 4.7) cannot cross-check and
// uses pipeline.VerifySignature instead.
package artifactverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/digest"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// Errors. VerifyManifest errors match exactly one of ErrManifest (the
// manifest is missing, unsigned, forged, replayed or does not match the
// records: a possible tampering) or ErrDestination (the destination could
// not be read); anything else is an internal failure. ErrManifest errors
// also wrap the pipeline cause (pipeline.ErrManifestBadSignature,
// pipeline.ErrReplay, ...).
var (
	ErrManifest    = errors.New("artifactverify: manifest verification failed")
	ErrDestination = errors.New("artifactverify: destination unavailable")
	// ErrTooLong: a stream is longer than its recorded size
	// (digest.ErrTooLong).
	ErrTooLong = digest.ErrTooLong
	// ErrIdentityInvalid: not an age X25519 identity.
	ErrIdentityInvalid = errors.New("artifactverify: not an age X25519 identity")
	// ErrIdentityMismatch: no identity matches the recipient.
	ErrIdentityMismatch = errors.New("artifactverify: the identity does not match the recipient")
)

// Artifact is a stored database/globals object as recorded in the
// database.
type Artifact struct {
	ID            uuid.UUID
	JobID         uuid.UUID
	RunID         uuid.UUID
	DestinationID uuid.UUID
	ObjectKey     string
	Size          int64
	SHA256        []byte
	// SigningKeyID names the instance key of the manifest on the
	// destination (required).
	SigningKeyID string
	// EncryptionMode is "passphrase", "recipients" or "none"; the version
	// matching the mode must be set.
	EncryptionMode    string
	PassphraseVersion int
	RecipientVersion  int
	JobSlug           string
	RunTimestamp      time.Time
}

// FromDownloadRow builds an Artifact from a GetDownloadArtifact row.
func FromDownloadRow(row *sqlc.GetDownloadArtifactRow) Artifact {
	a := &row.Artifact
	out := Artifact{
		ID: a.ID, JobID: a.JobID, RunID: a.RunID, DestinationID: a.DestinationID, ObjectKey: a.ObjectKey,
		Size: a.Size, SHA256: a.Sha256, JobSlug: row.JobSlug,
	}
	if a.SigningKeyID != nil {
		out.SigningKeyID = *a.SigningKeyID
	}
	if a.EncryptionMode != nil {
		out.EncryptionMode = *a.EncryptionMode
	}
	if a.PassphraseVersion != nil {
		out.PassphraseVersion = int(*a.PassphraseVersion)
	}
	if a.RecipientVersion != nil {
		out.RecipientVersion = int(*a.RecipientVersion)
	}
	if row.RunStorageTimestamp != nil {
		out.RunTimestamp = *row.RunStorageTimestamp
	}
	return out
}

// manifestErr marks a verification failure.
func manifestErr(err error) error { return fmt.Errorf("%w: %w", ErrManifest, err) }

func manifestMsg(msg string) error { return fmt.Errorf("%w: %s", ErrManifest, msg) }

// VerifyManifest reads the manifest.json of a's run on its destination
// (dest, opened by the caller) and verifies it with the instance keys:
// signature, binding to the job, run, slug, run timestamp and exactly the
// entries recorded as signed there, the encryption facts of a's entry and
// the manifest object's recorded size and SHA-256. It returns the verified
// manifest. q reads the records (the caller's pool or transaction).
func VerifyManifest(ctx context.Context, q *sqlc.Queries, dest storage.Destination, a Artifact) (*pipeline.Manifest, error) {
	if a.SigningKeyID == "" || a.RunTimestamp.IsZero() || a.JobSlug == "" {
		return nil, manifestMsg("the artifact is not covered by a signed manifest")
	}
	m, err := q.GetDownloadManifestArtifact(ctx, sqlc.GetDownloadManifestArtifactParams{RunID: a.RunID, DestinationID: a.DestinationID})
	if err != nil {
		return nil, manifestErr(fmt.Errorf("manifest record: %w", err))
	}
	if m.State != sqlc.ArtifactStatePresent || m.Size <= 0 || m.Size > pipeline.MaxManifestBytes {
		return nil, manifestMsg("manifest not available")
	}
	entries, err := q.ListDownloadManifestEntries(ctx, sqlc.ListDownloadManifestEntriesParams{
		RunID: a.RunID, DestinationID: a.DestinationID, SigningKeyID: a.SigningKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("artifactverify: manifest entries: %w", err)
	}
	expect := pipeline.Binding{
		JobID: a.JobID.String(), RunID: a.RunID.String(), JobSlug: a.JobSlug, RunTimestamp: a.RunTimestamp,
	}
	path := ""
	for _, e := range entries {
		name, err := lastSegment(e.ObjectKey)
		if err != nil {
			return nil, err
		}
		expect.Entries = append(expect.Entries, pipeline.ExpectedEntry{
			Path: name, SHA256: e.Sha256, StoredSize: e.Size, RawSHA256: e.ItemRawSha256,
		})
		if e.ObjectKey == a.ObjectKey {
			path = name
		}
	}
	if path == "" {
		return nil, manifestMsg("the artifact is not among the signed entries")
	}
	key, err := storage.ParseKey(m.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("artifactverify: manifest key: %w", err)
	}
	rc, _, err := dest.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDestination, err)
	}
	data, err := io.ReadAll(io.LimitReader(rc, m.Size+1))
	cerr := rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDestination, err)
	}
	if cerr != nil {
		return nil, fmt.Errorf("%w: %w", ErrDestination, cerr)
	}
	keys, err := instancekeys.Verifier(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("artifactverify: instance keys: %w", err)
	}
	// Signature and binding first (the most specific error), then the
	// manifest object's own record.
	mf, err := pipeline.Verify(data, keys, expect)
	if err != nil {
		return nil, manifestErr(err)
	}
	if sum := sha256.Sum256(data); int64(len(data)) != m.Size || !bytes.Equal(sum[:], m.Sha256) {
		return nil, manifestMsg("manifest digest mismatch")
	}
	if err := checkEntry(mf, path, a); err != nil {
		return nil, err
	}
	return mf, nil
}

// checkEntry compares the signed entry of the artifact with its record
// (digest and encryption facts).
func checkEntry(m *pipeline.Manifest, path string, a Artifact) error {
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.Path != path {
			continue
		}
		if e.SHA256 != hex.EncodeToString(a.SHA256) || e.StoredSize != a.Size {
			return manifestMsg("the signed entry differs from the artifact record")
		}
		enc := e.Encryption
		ok := string(enc.Mode) == a.EncryptionMode
		switch a.EncryptionMode {
		case string(pipeline.EncryptPassphrase):
			ok = ok && a.PassphraseVersion > 0 && enc.PassphraseVersion == a.PassphraseVersion
		case string(pipeline.EncryptRecipients):
			ok = ok && a.RecipientVersion > 0 && enc.RecipientVersion == a.RecipientVersion
		case string(pipeline.EncryptNone):
		default:
			ok = false
		}
		if !ok {
			return manifestMsg("the signed encryption differs from the artifact record")
		}
		return nil
	}
	return manifestMsg("the artifact is not among the signed entries")
}

func lastSegment(objectKey string) (string, error) {
	k, err := storage.ParseKey(objectKey)
	if err != nil {
		return "", fmt.Errorf("artifactverify: object key: %w", err)
	}
	segs := k.Segments()
	return segs[len(segs)-1], nil
}

// DigestReader hashes (SHA-256) and counts what is read through it and
// fails with ErrTooLong once more than its limit arrived (digest.Reader).
type DigestReader = digest.Reader

// NewDigestReader wraps r with a limit of limit bytes.
func NewDigestReader(r io.Reader, limit int64) *DigestReader {
	return digest.NewReader(r, limit)
}

// MatchIdentity checks that identity (age identity-file content, X25519
// only) holds the private key of recipient ("age1..."). It never echoes
// the input in errors.
func MatchIdentity(identity []byte, recipient string) error {
	ids, err := crypto.ParseAgeIdentities(identity)
	if err != nil {
		return ErrIdentityInvalid
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok && x.Recipient().String() == recipient {
			return nil
		}
	}
	return ErrIdentityMismatch
}
