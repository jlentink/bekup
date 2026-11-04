package webdav

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Flavor selects the server dialect.
type Flavor string

// Flavors.
const (
	// FlavorGeneric is plain RFC 4918 WebDAV: URL is the collection that
	// holds the backups and uploads are one streaming PUT.
	FlavorGeneric Flavor = "generic"
	// FlavorNextcloud is Nextcloud or ownCloud (TransIP STACK included): URL
	// is the server root, files live below
	// /remote.php/dav/files/<user>/ and large uploads use the chunked
	// upload API below /remote.php/dav/uploads/<user>/.
	FlavorNextcloud Flavor = "nextcloud"
)

// Valid reports whether f is a known flavor.
func (f Flavor) Valid() bool { return f == FlavorGeneric || f == FlavorNextcloud }

// Defaults and limits of Config.
const (
	// DefaultRequestTimeout bounds one metadata request (PROPFIND, MKCOL,
	// MOVE, DELETE, a chunk PUT's response). Streaming uploads and
	// downloads are bounded by the caller's context instead.
	DefaultRequestTimeout = 60 * time.Second
	MinRequestTimeout     = 5 * time.Second
	MaxRequestTimeout     = 10 * time.Minute
	// DefaultChunkSize is the chunk size of chunked uploads (64 MiB).
	DefaultChunkSize int64 = 64 << 20
	// MinChunkSize and MaxChunkSize bound Config.ChunkSize.
	MinChunkSize int64 = 1 << 20
	MaxChunkSize int64 = 1 << 30
	// MaxChunks is the largest number of chunks of one upload (the
	// Nextcloud limit).
	MaxChunks = 10000

	maxURLLen        = 2048
	maxUserLen       = 256
	maxPasswordLen   = 1024
	maxCABundleBytes = 1 << 20
	maxPinCertBytes  = 16 << 10
)

// ErrInvalidConfig reports an invalid Config. It wraps
// storage.ErrInvalidArgument.
var ErrInvalidConfig = fmt.Errorf("%w: invalid webdav configuration", storage.ErrInvalidArgument)

func invalid(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, msg)
}

// Pin is a pinned server certificate (D-222): the certificate itself is
// the only trust anchor (no system roots, no CA bundle) and the leaf's
// SPKI must equal SPKISHA256. Host name and validity are still checked.
type Pin struct {
	// CertificatePEM is the pinned (leaf) certificate.
	CertificatePEM []byte
	// SPKISHA256 is the SHA-256 of its SubjectPublicKeyInfo (32 bytes).
	SPKISHA256 []byte
}

// Config configures a WebDAV destination.
type Config struct {
	// URL is an https URL without user info, query or fragment. Generic:
	// the existing collection that holds the backups. Nextcloud: the
	// server root (where status.php and remote.php live).
	URL string
	// Flavor is FlavorGeneric (default) or FlavorNextcloud.
	Flavor Flavor
	// Username for HTTP Basic authentication (no ':' or control
	// characters).
	Username string
	// Password (or app password). Open takes ownership: it is destroyed
	// by Close, or by Open on failure.
	Password *crypto.Secret
	// Path is the optional folder below the base (generic: below URL;
	// Nextcloud: below the user's files), validated like a key prefix; it
	// is created if missing.
	Path string
	// CABundlePEM, if set, replaces the system roots.
	CABundlePEM []byte
	// Pin, if set, replaces both the system roots and the CA bundle.
	Pin *Pin
	// NoDelete refuses every Delete with storage.ErrDeleteNotPermitted.
	NoDelete bool
	// RequestTimeout bounds metadata requests (default 60s, 5s..10m).
	RequestTimeout time.Duration
	// ChunkSize is the chunk size of Nextcloud chunked uploads (default
	// 64 MiB, 1 MiB..1 GiB). One Put holds one chunk in memory.
	ChunkSize int64
	// AllowHTTP permits an http:// URL. Set it only in dev mode; Open logs
	// a warning, and Basic credentials then travel unencrypted.
	AllowHTTP bool
	// Guard is the mandatory SSRF guard every connection is dialed
	// through.
	Guard *netguard.Guard
	// Logger receives backend events (never secrets); default
	// slog.Default().
	Logger *slog.Logger
}

// settings is the validated form of Config.
type settings struct {
	scheme   string
	host     string // host without brackets (TLS server name)
	hostPort string // host[:port] as in the URL
	basePath []string
	flavor   Flavor
	user     string
	dir      storage.Key
	roots    *x509.CertPool
	pin      *validPin
	noDelete bool
	reqTO    time.Duration
	chunk    int64
	guard    *netguard.Guard
	logger   *slog.Logger
}

type validPin struct {
	cert *x509.Certificate
	spki []byte
}

func (c *Config) validate() (*settings, error) {
	u, err := ParseURL(c.URL, c.AllowHTTP)
	if err != nil {
		return nil, err
	}
	s := &settings{
		scheme: u.scheme, host: u.host, hostPort: u.hostPort, basePath: u.segments,
		noDelete: c.NoDelete,
	}
	s.flavor = c.Flavor
	if s.flavor == "" {
		s.flavor = FlavorGeneric
	}
	if !s.flavor.Valid() {
		return nil, invalid("unknown flavor")
	}
	if s.flavor == FlavorNextcloud && containsSegment(s.basePath, "remote.php") {
		return nil, invalid("for Nextcloud and ownCloud enter the server address (for example https://cloud.example.com), not the WebDAV URL")
	}
	if err := ValidateUser(c.Username); err != nil {
		return nil, err
	}
	s.user = c.Username
	switch n := c.Password.Len(); {
	case n == 0:
		return nil, invalid("the password is required")
	case n > maxPasswordLen:
		return nil, invalid("the password is too long")
	}
	dir, err := storage.ParsePrefix(c.Path)
	if err != nil {
		return nil, fmt.Errorf("webdav path: %w", err)
	}
	s.dir = dir
	if len(c.CABundlePEM) > 0 {
		if len(c.CABundlePEM) > maxCABundleBytes {
			return nil, invalid("CA bundle too large")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(c.CABundlePEM) {
			return nil, invalid("the CA bundle contains no PEM certificate")
		}
		s.roots = pool
	}
	if c.Pin != nil {
		p, err := parsePin(c.Pin)
		if err != nil {
			return nil, err
		}
		s.pin = p
	}
	switch {
	case c.RequestTimeout == 0:
		s.reqTO = DefaultRequestTimeout
	case c.RequestTimeout < MinRequestTimeout || c.RequestTimeout > MaxRequestTimeout:
		return nil, invalid("request timeout out of range")
	default:
		s.reqTO = c.RequestTimeout
	}
	switch {
	case c.ChunkSize == 0:
		s.chunk = DefaultChunkSize
	case c.ChunkSize < MinChunkSize || c.ChunkSize > MaxChunkSize:
		return nil, invalid("chunk size out of range")
	default:
		s.chunk = c.ChunkSize
	}
	if c.Guard == nil {
		return nil, invalid("the netguard guard is required")
	}
	s.guard = c.Guard
	s.logger = c.Logger
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s, nil
}

func containsSegment(segs []string, s string) bool {
	for _, x := range segs {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// parsePin checks a pin: exactly one PEM certificate whose SPKI matches.
func parsePin(p *Pin) (*validPin, error) {
	if len(p.CertificatePEM) == 0 || len(p.CertificatePEM) > maxPinCertBytes {
		return nil, invalid("the pinned certificate is missing or too large")
	}
	cert, err := ParseCertificatePEM(p.CertificatePEM)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if len(p.SPKISHA256) != sha256.Size || subtle.ConstantTimeCompare(sum[:], p.SPKISHA256) != 1 {
		return nil, invalid("the pinned certificate does not match the pinned key fingerprint")
	}
	return &validPin{cert: cert, spki: sum[:]}, nil
}

// ParseCertificatePEM parses exactly one PEM CERTIFICATE block (nothing
// else in the input).
func ParseCertificatePEM(b []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return nil, invalid("the pinned certificate must be exactly one PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, invalid("the pinned certificate cannot be parsed")
	}
	return cert, nil
}

// ParsedURL is a validated destination URL.
type ParsedURL struct {
	scheme   string
	host     string
	hostPort string
	segments []string
}

// Scheme is "https" (or "http" in dev mode).
func (u ParsedURL) Scheme() string { return u.scheme }

// Host is the host without brackets and port.
func (u ParsedURL) Host() string { return u.host }

// HostPort is the host with the port if the URL has one (IPv6 in
// brackets).
func (u ParsedURL) HostPort() string { return u.hostPort }

// Segments are the decoded path segments of the URL.
func (u ParsedURL) Segments() []string { return append([]string(nil), u.segments...) }

// ParseURL validates a destination URL: https (http only with
// allowHTTP), a host accepted by internal/hostname, an optional port, no
// user info, query or fragment, and a path without "." or ".." segments,
// empty segments in the middle, control characters or backslashes.
// Errors never repeat the input.
func ParseURL(raw string, allowHTTP bool) (ParsedURL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedURL{}, invalid("the URL is required")
	}
	if len(raw) > maxURLLen {
		return ParsedURL{}, invalid("the URL is too long")
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == '\\' || unicode.IsSpace(r) {
			return ParsedURL{}, invalid("the URL contains a space, control character or backslash (percent-encode spaces as %20)")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedURL{}, invalid("the URL cannot be parsed")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return ParsedURL{}, invalid("the URL must use https (http only in dev mode): Basic credentials are never sent unencrypted")
		}
	default:
		return ParsedURL{}, invalid("the URL must be an https URL")
	}
	switch {
	case u.User != nil:
		return ParsedURL{}, invalid("the URL must not contain a user name or password (use the username and password fields)")
	case u.Opaque != "":
		return ParsedURL{}, invalid("the URL cannot be parsed")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#"):
		return ParsedURL{}, invalid("the URL must not have a query or fragment")
	}
	host := u.Hostname()
	if err := hostname.Validate(host); err != nil {
		return ParsedURL{}, invalid("host: " + err.Error())
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return ParsedURL{}, invalid("invalid port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return ParsedURL{}, invalid("invalid port")
	}
	hostPort := u.Host // brackets around IPv6 kept
	segs, err := splitURLPath(u.EscapedPath())
	if err != nil {
		return ParsedURL{}, err
	}
	return ParsedURL{scheme: u.Scheme, host: host, hostPort: hostPort, segments: segs}, nil
}

// splitURLPath decodes the escaped path into segments ("" and "/" give
// none; one trailing slash is allowed).
func splitURLPath(escaped string) ([]string, error) {
	p := strings.TrimPrefix(escaped, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil, nil
	}
	raw := strings.Split(p, "/")
	if len(raw) > storage.MaxSegments {
		return nil, invalid("the URL path has too many segments")
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		seg, err := url.PathUnescape(r)
		if err != nil {
			return nil, invalid("the URL path has an invalid percent-encoding")
		}
		switch {
		case seg == "":
			return nil, invalid("the URL path must not contain empty segments")
		case seg == "." || seg == "..":
			return nil, invalid("the URL path must not contain . or .. segments")
		case strings.ContainsAny(seg, "/\\") || !utf8.ValidString(seg):
			return nil, invalid("the URL path contains an encoded slash, backslash or invalid UTF-8")
		}
		for _, c := range seg {
			if unicode.IsControl(c) {
				return nil, invalid("the URL path contains a control character")
			}
		}
		out = append(out, seg)
	}
	return out, nil
}

// ValidateUser checks a Basic authentication user name.
func ValidateUser(u string) error {
	switch {
	case u == "":
		return invalid("the username is required")
	case len(u) > maxUserLen || !utf8.ValidString(u):
		return invalid("the username is too long or not valid UTF-8")
	case strings.Contains(u, ":"):
		return invalid("the username must not contain ':' (HTTP Basic authentication)")
	}
	for _, r := range u {
		if unicode.IsControl(r) {
			return invalid("the username must not contain control characters")
		}
	}
	return nil
}

// ValidateTarget checks URL, flavor, username and path exactly like Open
// does, without credentials or a connection. internal/destinations uses
// it to reject an invalid destination when it is saved.
func ValidateTarget(rawURL string, flavor Flavor, user, path string, allowHTTP bool) error {
	u, err := ParseURL(rawURL, allowHTTP)
	if err != nil {
		return err
	}
	if flavor == "" {
		flavor = FlavorGeneric
	}
	if !flavor.Valid() {
		return invalid("unknown flavor")
	}
	if flavor == FlavorNextcloud && containsSegment(u.segments, "remote.php") {
		return invalid("for Nextcloud and ownCloud enter the server address (for example https://cloud.example.com), not the WebDAV URL")
	}
	if err := ValidateUser(user); err != nil {
		return err
	}
	if _, err := storage.ParsePrefix(path); err != nil {
		return fmt.Errorf("webdav path: %w", err)
	}
	return nil
}

// errMessage strips the sentinel prefix of a validation error for
// presentation.
func errMessage(err error) string {
	msg := err.Error()
	if _, rest, ok := strings.Cut(msg, "invalid webdav configuration: "); ok {
		return rest
	}
	if _, rest, ok := strings.Cut(msg, "webdav path: "); ok {
		return "path: " + rest
	}
	return "invalid settings"
}

// Message returns the presentable text of a validation error from
// ParseURL, ValidateUser, ValidateTarget or Open ("invalid settings" for
// anything else).
func Message(err error) string {
	if !errors.Is(err, storage.ErrInvalidArgument) && !errors.Is(err, storage.ErrInvalidKey) {
		return "invalid settings"
	}
	return errMessage(err)
}
