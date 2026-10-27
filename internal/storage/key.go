package storage

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Key limits. They are the strictest common denominator of the backends:
// 255 bytes is NAME_MAX on Linux file systems and SFTP servers, 1024 bytes
// is the S3 key limit.
const (
	MaxSegmentLen = 255
	MaxKeyLen     = 1024
	MaxSegments   = 32
	// MaxJobSlugLen bounds the job-slug segment.
	MaxJobSlugLen = 100
	// MaxEncodedDBNameLen bounds the percent-encoded database name; longer
	// encodings fall back to the hashed form (see EncodeDBName).
	MaxEncodedDBNameLen = 180
	// ManifestFileName is the name of the run manifest next to the
	// artifacts of one run.
	ManifestFileName = "manifest.json"
	// TimestampLayout formats the run timestamp segment (UTC).
	TimestampLayout = "2006-01-02T150405Z"
)

// ErrDBNameHashed is returned by DecodeDBName for the hashed (non-reversible)
// form of an over-long database name; the real name is in the manifest.
var ErrDBNameHashed = errors.New("storage: database name is stored hashed")

// Key is a validated object key: one or more slash-separated segments,
// relative, without empty, "." or ".." segments. The zero Key is the
// destination root; it is only valid as a List prefix.
//
// Each segment is 1..MaxSegmentLen bytes from the set [A-Za-z0-9._~%+=@-]
// and must not start with "." (dot names are reserved for backend
// temporary files such as "*.partial").
type Key struct {
	s string
}

// ParseKey validates s as an object key (at least one segment).
func ParseKey(s string) (Key, error) {
	if s == "" {
		return Key{}, fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	return parse(s)
}

// ParsePrefix validates s as a list prefix or destination prefix. The empty
// string is the root; one trailing slash is tolerated.
func ParsePrefix(s string) (Key, error) {
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return Key{}, nil
	}
	return parse(s)
}

// MustParseKey is ParseKey for constants in tests; it panics on error.
func MustParseKey(s string) Key {
	k, err := ParseKey(s)
	if err != nil {
		panic(err)
	}
	return k
}

func parse(s string) (Key, error) {
	if len(s) > MaxKeyLen {
		return Key{}, fmt.Errorf("%w: longer than %d bytes", ErrInvalidKey, MaxKeyLen)
	}
	n := 0
	for seg := range strings.SplitSeq(s, "/") {
		n++
		if n > MaxSegments {
			return Key{}, fmt.Errorf("%w: more than %d segments", ErrInvalidKey, MaxSegments)
		}
		if err := ValidateSegment(seg); err != nil {
			return Key{}, err
		}
	}
	return Key{s: s}, nil
}

// ValidateSegment checks a single key segment (see Key).
func ValidateSegment(seg string) error {
	switch {
	case seg == "":
		return fmt.Errorf("%w: empty segment (absolute path or double slash)", ErrInvalidKey)
	case len(seg) > MaxSegmentLen:
		return fmt.Errorf("%w: segment longer than %d bytes", ErrInvalidKey, MaxSegmentLen)
	case seg[0] == '.':
		return fmt.Errorf("%w: segment starts with a dot", ErrInvalidKey)
	}
	for i := 0; i < len(seg); i++ {
		if !segmentByte(seg[i]) {
			return fmt.Errorf("%w: character %q not allowed at byte %d", ErrInvalidKey, seg[i], i)
		}
	}
	return nil
}

func segmentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '~', '%', '+', '=', '@':
		return true
	}
	return false
}

// String returns the slash-separated key ("" for the root).
func (k Key) String() string { return k.s }

// IsZero reports whether k is the root (zero) key.
func (k Key) IsZero() bool { return k.s == "" }

// Segments returns a copy of the key's segments (nil for the root).
func (k Key) Segments() []string {
	if k.s == "" {
		return nil
	}
	return strings.Split(k.s, "/")
}

// Base returns the last segment ("" for the root).
func (k Key) Base() string {
	if i := strings.LastIndexByte(k.s, '/'); i >= 0 {
		return k.s[i+1:]
	}
	return k.s
}

// Dir returns the key without its last segment (the root for one segment).
func (k Key) Dir() Key {
	if i := strings.LastIndexByte(k.s, '/'); i >= 0 {
		return Key{s: k.s[:i]}
	}
	return Key{}
}

// Join appends validated segments (each may itself contain slashes).
func (k Key) Join(parts ...string) (Key, error) {
	all := make([]string, 0, len(parts)+1)
	if k.s != "" {
		all = append(all, k.s)
	}
	all = append(all, parts...)
	return ParseKey(strings.Join(all, "/"))
}

// HasPrefix reports whether p is k or a segment-aligned ancestor of k
// ("a/b" is a prefix of "a/b/c" but not of "a/bc"). The root is a prefix of
// every key.
func (k Key) HasPrefix(p Key) bool {
	if p.s == "" || k.s == p.s {
		return true
	}
	return strings.HasPrefix(k.s, p.s+"/")
}

// MarshalText implements encoding.TextMarshaler.
func (k Key) MarshalText() ([]byte, error) { return []byte(k.s), nil }

// UnmarshalText implements encoding.TextUnmarshaler; it validates like
// ParsePrefix (so a stored key can never bypass validation).
func (k *Key) UnmarshalText(b []byte) error {
	p, err := ParsePrefix(string(b))
	if err != nil {
		return err
	}
	*k = p
	return nil
}

// ValidateJobSlug checks a job slug: 1..MaxJobSlugLen bytes of [a-z0-9_-],
// starting with a letter or digit.
func ValidateJobSlug(slug string) error {
	if slug == "" || len(slug) > MaxJobSlugLen {
		return fmt.Errorf("%w: job slug must be 1..%d bytes", ErrInvalidKey, MaxJobSlugLen)
	}
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alnum && (i == 0 || (c != '-' && c != '_')) {
			return fmt.Errorf("%w: job slug has character %q at byte %d", ErrInvalidKey, c, i)
		}
	}
	return nil
}

// TimestampSegment formats a run timestamp as a key segment (UTC, seconds).
func TimestampSegment(t time.Time) string { return t.UTC().Format(TimestampLayout) }

// ParseTimestampSegment parses a segment produced by TimestampSegment.
func ParseTimestampSegment(seg string) (time.Time, error) {
	t, err := time.Parse(TimestampLayout, seg)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: not a run timestamp", ErrInvalidKey)
	}
	return t, nil
}

// BuildKey builds the artifact layout key
// {prefix}/{jobSlug}/{timestamp}/{fileName}. prefix is the destination's
// optional key prefix (validated like ParsePrefix); fileName is a single
// segment, normally from ArtifactFileName or ManifestFileName.
func BuildKey(prefix, jobSlug string, runTimestamp time.Time, fileName string) (Key, error) {
	p, err := ParsePrefix(prefix)
	if err != nil {
		return Key{}, fmt.Errorf("prefix: %w", err)
	}
	if err := ValidateJobSlug(jobSlug); err != nil {
		return Key{}, err
	}
	if runTimestamp.IsZero() {
		return Key{}, fmt.Errorf("%w: zero run timestamp", ErrInvalidKey)
	}
	if err := ValidateSegment(fileName); err != nil {
		return Key{}, fmt.Errorf("file name: %w", err)
	}
	return p.Join(jobSlug, TimestampSegment(runTimestamp), fileName)
}

// ValidateExtension checks an artifact extension such as "dump.zst.age":
// 1..4 dot-separated parts of [a-z0-9], at most 32 bytes.
func ValidateExtension(ext string) error {
	if ext == "" || len(ext) > 32 {
		return fmt.Errorf("%w: extension must be 1..32 bytes", ErrInvalidKey)
	}
	parts := strings.Split(ext, ".")
	if len(parts) > 4 {
		return fmt.Errorf("%w: extension has too many parts", ErrInvalidKey)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("%w: empty extension part", ErrInvalidKey)
		}
		for i := 0; i < len(p); i++ {
			if c := p[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return fmt.Errorf("%w: extension has character %q", ErrInvalidKey, c)
			}
		}
	}
	return nil
}

// ArtifactFileName returns "{EncodeDBName(dbName)}.{ext}". The encoded name
// never contains a dot, so the database part is everything before the first
// dot. The manifest file name is refused.
func ArtifactFileName(dbName, ext string) (string, error) {
	enc, err := EncodeDBName(dbName)
	if err != nil {
		return "", err
	}
	if err := ValidateExtension(ext); err != nil {
		return "", err
	}
	name := enc + "." + ext
	if name == ManifestFileName {
		return "", fmt.Errorf("%w: artifact name collides with the manifest", ErrInvalidKey)
	}
	if err := ValidateSegment(name); err != nil {
		return "", err
	}
	return name, nil
}

const upperHex = "0123456789ABCDEF"

var hashEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeDBName encodes a database name into a key-safe, dot-free segment.
//
// Bytes in [A-Za-z0-9_-] are kept; every other byte (including '.', '%',
// '~', '/', control characters and UTF-8 multibyte sequences) becomes %XX
// with upper-case hex. The encoding is injective and reversible with
// DecodeDBName. When the encoding would exceed MaxEncodedDBNameLen bytes,
// the result is "~" followed by the lower-case unpadded base32 SHA-256 of
// the name; '~' never appears in the reversible form, so the two forms
// cannot collide. Names of any bytes are accepted (PostgreSQL SQL_ASCII
// databases may hold non-UTF-8 names) except the empty name and names over
// 1024 bytes.
func EncodeDBName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty database name", ErrInvalidKey)
	}
	if len(name) > 1024 {
		return "", fmt.Errorf("%w: database name too long", ErrInvalidKey)
	}
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if dbNameKeep(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0x0f])
	}
	if b.Len() <= MaxEncodedDBNameLen {
		return b.String(), nil
	}
	sum := sha256.Sum256([]byte(name))
	return "~" + strings.ToLower(hashEncoding.EncodeToString(sum[:])), nil
}

func dbNameKeep(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// DecodeDBName reverses EncodeDBName. It only accepts the canonical
// encoding (upper-case hex, no needless escapes), so every name has exactly
// one encoded form. The hashed form returns ErrDBNameHashed.
func DecodeDBName(enc string) (string, error) {
	if strings.HasPrefix(enc, "~") {
		return "", ErrDBNameHashed
	}
	if enc == "" || len(enc) > MaxEncodedDBNameLen {
		return "", fmt.Errorf("%w: invalid encoded database name length", ErrInvalidKey)
	}
	out := make([]byte, 0, len(enc))
	for i := 0; i < len(enc); i++ {
		c := enc[i]
		if dbNameKeep(c) {
			out = append(out, c)
			continue
		}
		if c != '%' || i+2 >= len(enc) {
			return "", fmt.Errorf("%w: invalid encoded database name", ErrInvalidKey)
		}
		hi, lo := strings.IndexByte(upperHex, enc[i+1]), strings.IndexByte(upperHex, enc[i+2])
		if hi < 0 || lo < 0 {
			return "", fmt.Errorf("%w: invalid escape in database name", ErrInvalidKey)
		}
		d := byte(hi<<4 | lo) //nolint:gosec // G115: hi, lo are 0..15
		if dbNameKeep(d) {
			return "", fmt.Errorf("%w: non-canonical escape in database name", ErrInvalidKey)
		}
		out = append(out, d)
		i += 2
	}
	return string(out), nil
}

// ValidUTF8DBName reports whether a decoded name is valid UTF-8 (for
// display); non-UTF-8 names are legal but should be shown escaped.
func ValidUTF8DBName(name string) bool { return utf8.ValidString(name) }
