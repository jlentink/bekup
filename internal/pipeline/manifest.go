package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/storage"
)

// Manifest format constants.
const (
	// ManifestFormatVersion is the manifest schema version this code
	// writes and the only one it reads.
	ManifestFormatVersion = 1
	// ManifestSignaturePurpose is the instance-signature purpose (D-087).
	ManifestSignaturePurpose = "manifest"
	// ManifestContentType is the Put content type of manifest.json.
	ManifestContentType = "application/json"
	// MaxManifestBytes bounds manifest.json (stats of very large
	// databases included).
	MaxManifestBytes = 16 << 20
	// MaxEntries bounds the entries of one manifest.
	MaxEntries = 10000
	// MaxWarnings and MaxWarningLen bound the warnings.
	MaxWarnings   = 1000
	MaxWarningLen = 2048
)

// Manifest errors. Compare with errors.Is.
var (
	// ErrManifestMalformed: manifest.json is not a valid manifest.
	ErrManifestMalformed = errors.New("pipeline: malformed manifest")
	// ErrManifestBadSignature: the signature does not verify.
	ErrManifestBadSignature = errors.New("pipeline: manifest signature verification failed")
	// ErrManifestUnknownKey: the manifest names a key ID that is not known.
	ErrManifestUnknownKey = errors.New("pipeline: manifest signed with an unknown key")
	// ErrBindingMismatch: the signed manifest does not match what the
	// caller expects from the database.
	ErrBindingMismatch = errors.New("pipeline: manifest does not match the expected run")
	// ErrReplay: a validly signed manifest of another job or run (a
	// replayed manifest). Errors matching ErrReplay also match
	// ErrBindingMismatch.
	ErrReplay = errors.New("pipeline: manifest belongs to another job or run")
)

// EntryKind is what an entry holds.
type EntryKind string

// Entry kinds.
const (
	KindDatabase EntryKind = "database"
	KindGlobals  EntryKind = "globals"
)

// EngineInfo describes the source engine.
type EngineInfo struct {
	// Kind is the engine ("postgres", "mysql", "mariadb").
	Kind string `json:"kind"`
	// VersionKey is the catalog release series of the dump client ("18").
	VersionKey string `json:"version_key"`
	// ServerVersion is the server version reported by the target.
	ServerVersion string `json:"server_version"`
}

// EntryEncryption describes how an entry is encrypted (never key
// material).
type EntryEncryption struct {
	Mode EncryptionMode `json:"mode"`
	// PassphraseVersion is set for passphrase mode.
	PassphraseVersion int `json:"passphrase_version,omitempty"`
	// RecipientVersion (the job's recipient key version) and
	// RecipientFingerprints (RecipientFingerprint of each recipient) are
	// set for recipient mode.
	RecipientVersion      int      `json:"recipient_version,omitempty"`
	RecipientFingerprints []string `json:"recipient_fingerprints,omitempty"`
}

// Entry is one artifact of a run.
type Entry struct {
	// DB is the database name in the reversible percent encoding of
	// EncodeName (empty for globals). Unlike the file name it is never
	// hashed, so over-long names survive.
	DB   string    `json:"db,omitempty"`
	Kind EntryKind `json:"kind"`
	// Path is the artifact's file name, relative to the directory holding
	// manifest.json.
	Path       string `json:"path"`
	RawSize    int64  `json:"raw_size"`
	StoredSize int64  `json:"stored_size"`
	// SHA256 covers the stored bytes, RawSHA256 the dump before
	// compression (lowercase hex).
	SHA256      string          `json:"sha256"`
	RawSHA256   string          `json:"raw_sha256"`
	Compression string          `json:"compression"`
	Encryption  EntryEncryption `json:"encryption"`
}

// Manifest is the signed description of one backup run (format version 1).
// See docs/reference/artifact-format.md.
type Manifest struct {
	FormatVersion int    `json:"format_version"`
	BekupVersion  string `json:"bekup_version"`
	// InstanceKeyID is the signing key's ID; WriteManifest/SignManifest
	// set it.
	InstanceKeyID string `json:"instance_key_id"`
	JobID         string `json:"job_id"`
	JobSlug       string `json:"job_slug"`
	RunID         string `json:"run_id"`
	// RunTimestamp is the run's directory timestamp (UTC, whole seconds).
	RunTimestamp time.Time  `json:"run_timestamp"`
	CreatedAt    time.Time  `json:"created_at"`
	Engine       EngineInfo `json:"engine"`
	Entries      []Entry    `json:"entries"`
	// Stats is an extensible JSON object (task 3.10 source statistics).
	// It is opaque here but covered by the signature. It must follow the
	// canonical JSON rules: integers only, strings for everything else.
	Stats    json.RawMessage `json:"stats,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

var (
	idRe         = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	keyIDRe      = regexp.MustCompile(`^[0-9a-f]{16}$`)
	hex256Re     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionKeyRe = regexp.MustCompile(`^\d[0-9.]{0,15}$`)
)

func printableASCII(s string, maxLen int, allowSpace bool) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x21 || c > 0x7e {
			if !allowSpace || c != ' ' {
				return false
			}
		}
	}
	return true
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrManifestMalformed, fmt.Sprintf(format, args...))
}

// Validate checks every field of the manifest.
func (m *Manifest) Validate() error {
	switch {
	case m.FormatVersion != ManifestFormatVersion:
		return malformed("unsupported format version %d", m.FormatVersion)
	case !printableASCII(m.BekupVersion, 64, false):
		return malformed("invalid bekup version")
	case !keyIDRe.MatchString(m.InstanceKeyID):
		return malformed("invalid instance key id")
	case !idRe.MatchString(m.JobID):
		return malformed("invalid job id")
	case !idRe.MatchString(m.RunID):
		return malformed("invalid run id")
	case storage.ValidateJobSlug(m.JobSlug) != nil:
		return malformed("invalid job slug")
	case m.RunTimestamp.IsZero() || m.RunTimestamp.Location() != time.UTC || !m.RunTimestamp.Equal(m.RunTimestamp.Truncate(time.Second)):
		return malformed("run timestamp must be UTC whole seconds")
	case m.CreatedAt.IsZero() || m.CreatedAt.Location() != time.UTC:
		return malformed("created_at must be UTC")
	case !engine.Kind(m.Engine.Kind).Valid():
		return malformed("unknown engine")
	case !versionKeyRe.MatchString(m.Engine.VersionKey):
		return malformed("invalid engine version key")
	case !printableASCII(m.Engine.ServerVersion, 128, true):
		return malformed("invalid server version")
	case len(m.Entries) == 0 || len(m.Entries) > MaxEntries:
		return malformed("1..%d entries required", MaxEntries)
	case len(m.Warnings) > MaxWarnings:
		return malformed("more than %d warnings", MaxWarnings)
	}
	paths := make(map[string]bool, len(m.Entries))
	globals := 0
	for i := range m.Entries {
		e := &m.Entries[i]
		if err := e.validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i+1, err)
		}
		if paths[e.Path] {
			return malformed("entry %d: duplicate path", i+1)
		}
		paths[e.Path] = true
		if e.Kind == KindGlobals {
			globals++
		}
	}
	if globals > 1 {
		return malformed("more than one globals entry")
	}
	for i, w := range m.Warnings {
		if w == "" || len(w) > MaxWarningLen {
			return malformed("warning %d: must be 1..%d bytes", i+1, MaxWarningLen)
		}
	}
	if len(m.Stats) > 0 {
		c, err := Canonicalize(m.Stats)
		if err != nil {
			return malformed("stats: %v", err)
		}
		if len(c) == 0 || c[0] != '{' {
			return malformed("stats must be a JSON object")
		}
	}
	return nil
}

func (e *Entry) validate() error {
	if storage.ValidateSegment(e.Path) != nil || e.Path == storage.ManifestFileName {
		return malformed("invalid path")
	}
	switch e.Kind {
	case KindDatabase:
		name, err := DecodeName(e.DB)
		if err != nil {
			return malformed("invalid db name")
		}
		enc, err := storage.EncodeDBName(name)
		if err != nil || !strings.HasPrefix(e.Path, enc+".") || strings.HasPrefix(e.Path, enc+"."+GlobalsFileBase+".") {
			return malformed("path does not match the db name")
		}
	case KindGlobals:
		if e.DB != "" || !strings.HasPrefix(e.Path, GlobalsFileBase+"."+GlobalsFileBase+".") {
			return malformed("invalid globals entry")
		}
	default:
		return malformed("unknown entry kind")
	}
	switch {
	case e.RawSize < 0 || e.StoredSize < 0:
		return malformed("negative size")
	case !hex256Re.MatchString(e.SHA256) || !hex256Re.MatchString(e.RawSHA256):
		return malformed("invalid sha256")
	case e.Compression != CompressionZstd:
		return malformed("unsupported compression")
	}
	enc := e.Encryption
	switch enc.Mode {
	case EncryptPassphrase:
		if enc.PassphraseVersion < 1 || enc.RecipientVersion != 0 || len(enc.RecipientFingerprints) != 0 {
			return malformed("invalid passphrase encryption")
		}
	case EncryptRecipients:
		if enc.PassphraseVersion != 0 || enc.RecipientVersion < 1 || len(enc.RecipientFingerprints) == 0 || len(enc.RecipientFingerprints) > MaxRecipients {
			return malformed("invalid recipient encryption")
		}
		for _, fp := range enc.RecipientFingerprints {
			if !hex256Re.MatchString(fp) {
				return malformed("invalid recipient fingerprint")
			}
		}
	case EncryptNone:
		if enc.PassphraseVersion != 0 || enc.RecipientVersion != 0 || len(enc.RecipientFingerprints) != 0 {
			return malformed("invalid encryption none")
		}
	default:
		return malformed("unknown encryption mode")
	}
	if strings.HasSuffix(e.Path, ".age") != (enc.Mode != EncryptNone) {
		return malformed("path suffix does not match the encryption mode")
	}
	return nil
}

// manifestFile is the on-storage form of manifest.json.
type manifestFile struct {
	KeyID     string          `json:"key_id"`
	Manifest  json.RawMessage `json:"manifest"`
	Signature string          `json:"signature"`
}

// CanonicalBytes validates m and returns its canonical serialization (the
// bytes that are signed).
func (m *Manifest) CanonicalBytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, malformed("encode: %v", err)
	}
	return Canonicalize(raw)
}

// SignManifest sets m.InstanceKeyID to the key's ID, signs the canonical
// manifest with the "manifest" purpose and returns the manifest.json bytes:
// the canonical object {"key_id","manifest","signature"} (base64 std
// signature over "bekup-sig-v1" 0x00 "manifest" 0x00 <canonical manifest>).
func SignManifest(key *crypto.SigningKey, m *Manifest) ([]byte, error) {
	if key == nil {
		return nil, crypto.ErrSigningKey
	}
	m.InstanceKeyID = key.ID
	canon, err := m.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	sig, err := key.Sign(ManifestSignaturePurpose, canon)
	if err != nil {
		return nil, fmt.Errorf("sign manifest: %w", err)
	}
	raw, err := json.Marshal(manifestFile{KeyID: key.ID, Manifest: canon, Signature: base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		return nil, fmt.Errorf("encode manifest file: %w", err)
	}
	return Canonicalize(raw)
}

// SignedManifest is a parsed, not yet verified manifest.json.
type SignedManifest struct {
	KeyID     string
	Signature []byte
	// Canonical is the canonical manifest (the signed bytes).
	Canonical []byte
	Manifest  Manifest
}

// ParseManifestFile parses manifest.json strictly (unknown or duplicate
// fields, non-canonical values and trailing data are rejected). It does
// not verify the signature: use Verify.
func ParseManifestFile(data []byte) (*SignedManifest, error) {
	v, err := parseJSON(data)
	if err != nil {
		return nil, malformed("%v", err)
	}
	outer, ok := v.(jsonObject)
	if !ok || len(outer) != 3 {
		return nil, malformed("manifest.json must be an object with key_id, manifest and signature")
	}
	keyID, ok1 := outer["key_id"].(string)
	sigB64, ok2 := outer["signature"].(string)
	inner, ok3 := outer["manifest"].(jsonObject)
	if !ok1 || !ok2 || !ok3 {
		return nil, malformed("manifest.json must be an object with key_id, manifest and signature")
	}
	if !keyIDRe.MatchString(keyID) {
		return nil, malformed("invalid key id")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(sigB64)
	if err != nil || len(sig) != 64 {
		return nil, malformed("invalid signature encoding")
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, inner); err != nil {
		return nil, malformed("%v", err)
	}
	canon := buf.Bytes()
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, malformed("decode: %v", err)
	}
	// One-to-one: the typed manifest must serialize back to exactly the
	// signed bytes (rejects case-folded keys, non-canonical timestamps,
	// explicit empty optional fields and similar ambiguities).
	raw, err := json.Marshal(&m)
	if err != nil {
		return nil, malformed("re-encode: %v", err)
	}
	again, err := Canonicalize(raw)
	if err != nil || !bytes.Equal(again, canon) {
		return nil, malformed("not in canonical manifest form")
	}
	return &SignedManifest{KeyID: keyID, Signature: sig, Canonical: canon, Manifest: m}, nil
}

// KeyVerifier verifies instance signatures by key ID. *crypto.Verifier
// (instancekeys.Verifier: active and retired keys) and PublicKeys
// implement it.
type KeyVerifier interface {
	Verify(id, purpose string, message, sig []byte) error
}

// PublicKeys maps instance key IDs to public keys.
type PublicKeys map[string][]byte

// Verify implements KeyVerifier. The ID must match the key.
func (p PublicKeys) Verify(id, purpose string, message, sig []byte) error {
	pub, ok := p[id]
	if !ok {
		return fmt.Errorf("%w: signing key %q", crypto.ErrUnknownKeyID, id)
	}
	v := crypto.NewVerifier()
	got, err := v.Add(pub)
	if err != nil {
		return err
	}
	if got != id {
		return fmt.Errorf("%w: id does not match public key", crypto.ErrSigningKey)
	}
	return v.Verify(id, purpose, message, sig)
}

// ExpectedEntry is an artifact as recorded in the database.
type ExpectedEntry struct {
	Path       string
	SHA256     []byte // stored bytes
	StoredSize int64
	// RawSHA256 is checked when set.
	RawSHA256 []byte
}

// Binding is what the caller expects from its own records. JobID, RunID
// and at least one entry are required; JobSlug and RunTimestamp are
// checked when set. The manifest's entries must equal Entries exactly (same
// set of paths, sizes and hashes).
type Binding struct {
	JobID        string
	RunID        string
	JobSlug      string
	RunTimestamp time.Time
	Entries      []ExpectedEntry
}

// BindingError is a binding mismatch. It matches ErrBindingMismatch, and
// ErrReplay when the manifest belongs to another job or run.
type BindingError struct {
	Field  string
	Replay bool
}

func (e *BindingError) Error() string {
	if e.Replay {
		return "pipeline: manifest belongs to another job or run (" + e.Field + ")"
	}
	return "pipeline: manifest does not match the expected run (" + e.Field + ")"
}

// Is matches ErrBindingMismatch and (for replays) ErrReplay.
func (e *BindingError) Is(target error) bool {
	return target == ErrBindingMismatch || (e.Replay && target == ErrReplay)
}

// Verify parses manifest.json, checks the signature with the key named by
// its key ID (retired keys verify while keys holds them) and then checks
// the binding against the caller's records. It returns the verified
// manifest. Errors match ErrManifestMalformed, ErrManifestUnknownKey,
// ErrManifestBadSignature, ErrReplay or ErrBindingMismatch.
func Verify(data []byte, keys KeyVerifier, expect Binding) (*Manifest, error) {
	if expect.JobID == "" || expect.RunID == "" || len(expect.Entries) == 0 {
		return nil, fmt.Errorf("%w: binding needs job id, run id and entries", ErrBindingMismatch)
	}
	m, err := VerifySignature(data, keys)
	if err != nil {
		return nil, err
	}
	if err := checkBinding(m, expect); err != nil {
		return nil, err
	}
	return m, nil
}

// VerifySignature checks only the signature and the manifest's validity.
// It does NOT defeat replay: a validly signed manifest of another run
// passes. Only offline tooling without the bekup database (task 4.7) may
// use it, and must show the job/run identity to the operator; everything
// else uses Verify.
func VerifySignature(data []byte, keys KeyVerifier) (*Manifest, error) {
	if keys == nil {
		return nil, ErrManifestUnknownKey
	}
	sm, err := ParseManifestFile(data)
	if err != nil {
		return nil, err
	}
	if err := keys.Verify(sm.KeyID, ManifestSignaturePurpose, sm.Canonical, sm.Signature); err != nil {
		switch {
		case errors.Is(err, crypto.ErrUnknownKeyID):
			return nil, fmt.Errorf("%w: %s", ErrManifestUnknownKey, sm.KeyID)
		case errors.Is(err, crypto.ErrBadSignature):
			return nil, ErrManifestBadSignature
		default:
			return nil, fmt.Errorf("%w: %w", ErrManifestBadSignature, err)
		}
	}
	m := &sm.Manifest
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.InstanceKeyID != sm.KeyID {
		return nil, malformed("key id differs from the signed instance key id")
	}
	return m, nil
}

func checkBinding(m *Manifest, b Binding) error {
	switch {
	case m.JobID != b.JobID:
		return &BindingError{Field: "job_id", Replay: true}
	case m.RunID != b.RunID:
		return &BindingError{Field: "run_id", Replay: true}
	case b.JobSlug != "" && m.JobSlug != b.JobSlug:
		return &BindingError{Field: "job_slug", Replay: true}
	case !b.RunTimestamp.IsZero() && !m.RunTimestamp.Equal(b.RunTimestamp.UTC().Truncate(time.Second)):
		return &BindingError{Field: "run_timestamp", Replay: true}
	case len(m.Entries) != len(b.Entries):
		return &BindingError{Field: "entries"}
	}
	byPath := make(map[string]*Entry, len(m.Entries))
	for i := range m.Entries {
		byPath[m.Entries[i].Path] = &m.Entries[i]
	}
	seen := make(map[string]bool, len(b.Entries))
	for _, x := range b.Entries {
		e, ok := byPath[x.Path]
		if !ok || seen[x.Path] {
			return &BindingError{Field: "entries.path"}
		}
		seen[x.Path] = true
		if e.StoredSize != x.StoredSize {
			return &BindingError{Field: "entries.stored_size"}
		}
		if len(x.SHA256) != sha256.Size || e.SHA256 != hex.EncodeToString(x.SHA256) {
			return &BindingError{Field: "entries.sha256"}
		}
		if len(x.RawSHA256) != 0 && e.RawSHA256 != hex.EncodeToString(x.RawSHA256) {
			return &BindingError{Field: "entries.raw_sha256"}
		}
	}
	return nil
}

// ManifestWriteResult is the outcome of WriteManifest.
type ManifestWriteResult struct {
	// File is the manifest.json content; SHA256 its digest.
	File         []byte
	SHA256       []byte
	Destinations []DestinationResult
}

// WriteManifest signs m (see SignManifest) and Puts manifest.json to every
// target concurrently with the same atomic, never-overwrite semantics as
// the artifacts (and an expected SHA-256). Call it only after every entry
// is finalized, and only for targets that hold all of the run's entries.
// It fails with ErrAllDestinationsFailed when no target succeeded.
func WriteManifest(ctx context.Context, targets []Target, key *crypto.SigningKey, m *Manifest) (ManifestWriteResult, error) {
	var res ManifestWriteResult
	if len(targets) == 0 || len(targets) > MaxTargets {
		return res, fmt.Errorf("%w: 1..%d targets required", ErrInvalidSpec, MaxTargets)
	}
	for i := range targets {
		t := &targets[i]
		if t.Dest == nil || t.Key.Base() != storage.ManifestFileName {
			return res, fmt.Errorf("%w: target %d needs a destination and a manifest.json key", ErrInvalidSpec, i+1)
		}
	}
	file, err := SignManifest(key, m)
	if err != nil {
		return res, err
	}
	sum := sha256.Sum256(file)
	res.File, res.SHA256 = file, sum[:]
	res.Destinations = make([]DestinationResult, len(targets))
	var wg sync.WaitGroup
	for i := range targets {
		t := &targets[i]
		wg.Go(func() {
			opts := t.Options
			opts.ContentType = ManifestContentType
			opts.ExpectedSHA256 = sum[:]
			opts.SizeHint = int64(len(file))
			info, err := t.Dest.Put(ctx, t.Key, bytes.NewReader(file), opts)
			r := DestinationResult{Name: t.Name, Key: t.Key}
			if err != nil {
				r.Outcome, r.Reason, r.Code, r.Err = OutcomeDropped, ReasonError, storage.Classify(err), err
			} else {
				r.Outcome, r.Code, r.Info = OutcomeOK, storage.CodeOK, info
			}
			res.Destinations[i] = r
		})
	}
	wg.Wait()
	for i := range res.Destinations {
		if res.Destinations[i].Outcome == OutcomeOK {
			return res, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, ErrAllDestinationsFailed
}

// EncodeName percent-encodes a database name reversibly: bytes in
// [A-Za-z0-9_-] are kept, every other byte becomes %XX (upper-case hex).
// Unlike storage.EncodeDBName it never falls back to a hash.
func EncodeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if nameKeep(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0x0f])
	}
	return b.String()
}

const upperHex = "0123456789ABCDEF"

func nameKeep(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// DecodeName reverses EncodeName, accepting only the canonical encoding of
// a 1..1024-byte name.
func DecodeName(enc string) (string, error) {
	if enc == "" || len(enc) > 3*1024 {
		return "", malformed("invalid encoded name length")
	}
	out := make([]byte, 0, len(enc))
	for i := 0; i < len(enc); i++ {
		c := enc[i]
		if nameKeep(c) {
			out = append(out, c)
			continue
		}
		if c != '%' || i+2 >= len(enc) {
			return "", malformed("invalid encoded name")
		}
		hi, lo := strings.IndexByte(upperHex, enc[i+1]), strings.IndexByte(upperHex, enc[i+2])
		if hi < 0 || lo < 0 {
			return "", malformed("invalid escape in name")
		}
		d := byte(hi<<4 | lo) //nolint:gosec // G115: hi, lo are 0..15
		if nameKeep(d) {
			return "", malformed("non-canonical escape in name")
		}
		out = append(out, d)
		i += 2
	}
	if len(out) > 1024 {
		return "", malformed("name too long")
	}
	return string(out), nil
}

func hexString(b []byte) string { return hex.EncodeToString(b) }
