package pipeline

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/storage"
)

// DumpFormat is the format of the dump inside an artifact.
type DumpFormat string

// Dump formats.
const (
	// FormatCustom is pg_dump's custom archive format (".dump").
	FormatCustom DumpFormat = "dump"
	// FormatSQL is a plain SQL script (".sql"): mysqldump, mariadb-dump,
	// pg_dumpall --globals-only.
	FormatSQL DumpFormat = "sql"
)

// GlobalsFileBase is the database part of the globals artifact name. A
// database may be called "globals" too, so the globals artifact also
// carries the extra extension part "globals"
// ("globals.globals.sql.zst.age"), which a database artifact never has.
const GlobalsFileBase = "globals"

// Artifact errors.
var (
	// ErrArtifact reports an unreadable or invalid artifact.
	ErrArtifact = errors.New("pipeline: invalid artifact")
)

// MaxDecoderWindow bounds the zstd window accepted when reading an
// artifact (bekup writes at most 8 MiB windows); a crafted frame cannot
// make the decoder allocate more.
const MaxDecoderWindow = 64 << 20

// ArtifactFileName returns the file name of an artifact:
// "{enc(db)}.{dump|sql}.zst[.age]" for a database and
// "globals.globals.sql.zst[.age]" for globals (dbName is ignored). The
// ".age" suffix is omitted only for EncryptNone.
func ArtifactFileName(kind EntryKind, dbName string, format DumpFormat, mode EncryptionMode) (string, error) {
	switch format {
	case FormatCustom, FormatSQL:
	default:
		return "", fmt.Errorf("%w: unknown dump format", ErrInvalidSpec)
	}
	ext := string(format) + ".zst"
	switch mode {
	case EncryptPassphrase, EncryptRecipients:
		ext += ".age"
	case EncryptNone:
	default:
		return "", fmt.Errorf("%w: unknown encryption mode", ErrInvalidSpec)
	}
	switch kind {
	case KindDatabase:
		return storage.ArtifactFileName(dbName, ext)
	case KindGlobals:
		if format != FormatSQL {
			return "", fmt.Errorf("%w: globals are plain SQL", ErrInvalidSpec)
		}
		return storage.ArtifactFileName(GlobalsFileBase, "globals."+ext)
	default:
		return "", fmt.Errorf("%w: unknown entry kind", ErrInvalidSpec)
	}
}

// ArtifactKey returns {prefix}/{jobSlug}/{timestamp}/{fileName}.
func ArtifactKey(prefix, jobSlug string, runTimestamp time.Time, fileName string) (storage.Key, error) {
	return storage.BuildKey(prefix, jobSlug, runTimestamp, fileName)
}

// ManifestKey returns {prefix}/{jobSlug}/{timestamp}/manifest.json.
func ManifestKey(prefix, jobSlug string, runTimestamp time.Time) (storage.Key, error) {
	return storage.BuildKey(prefix, jobSlug, runTimestamp, storage.ManifestFileName)
}

// Decryption holds what is needed to read an artifact. Exactly one of
// Passphrase, Identities or None must be set. The byte slices are borrowed;
// the caller zeroes them.
type Decryption struct {
	// Passphrase decrypts EncryptPassphrase artifacts. MaxWorkFactor
	// bounds the scrypt work factor a header may ask for (0 =
	// crypto.DefaultScryptWorkFactor, never above crypto.MaxScryptWorkFactor).
	Passphrase    []byte
	MaxWorkFactor int
	// Identities is age identity-file content (X25519 only) for
	// EncryptRecipients artifacts.
	Identities []byte
	// None reads an unencrypted (opt-out) artifact.
	None bool
}

// String never prints key material.
func (Decryption) String() string { return "Decryption{redacted}" }

// GoString never prints key material.
func (d Decryption) GoString() string { return d.String() }

// OpenArtifact returns a reader of the raw dump inside an artifact:
// age decryption (bounded scrypt work factor, X25519 identities only) and
// zstd decompression (bounded window, one decoder goroutine). The caller
// must close the reader. Verify the artifact's SHA-256 against the signed
// manifest before trusting its content.
func OpenArtifact(src io.Reader, d Decryption) (io.ReadCloser, error) {
	set := 0
	if len(d.Passphrase) > 0 {
		set++
	}
	if len(d.Identities) > 0 {
		set++
	}
	if d.None {
		set++
	}
	if set != 1 {
		return nil, fmt.Errorf("%w: exactly one of passphrase, identities or none is required", ErrArtifact)
	}
	var plain io.Reader
	var err error
	switch {
	case len(d.Passphrase) > 0:
		plain, err = crypto.DecryptWithPassphrase(src, d.Passphrase, d.MaxWorkFactor)
	case len(d.Identities) > 0:
		plain, err = crypto.DecryptWithIdentities(src, d.Identities)
	default:
		plain = src
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArtifact, err)
	}
	zr, err := zstd.NewReader(plain,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(MaxDecoderWindow),
		zstd.WithDecoderLowmem(true),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %w", ErrArtifact, err)
	}
	return zstdReadCloser{zr}, nil
}

type zstdReadCloser struct{ d *zstd.Decoder }

func (z zstdReadCloser) Read(p []byte) (int, error) { return z.d.Read(p) }

func (z zstdReadCloser) Close() error {
	z.d.Close()
	return nil
}
