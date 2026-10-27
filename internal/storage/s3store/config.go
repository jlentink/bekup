package s3store

import (
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Part size and concurrency limits of the multipart upload. One Put holds at
// most PartSize × Concurrency bytes of part buffers in memory.
const (
	// DefaultPartSize is the production part size (64 MiB, the plan's
	// floor for production use).
	DefaultPartSize int64 = 64 << 20
	// MinPartSize is the S3 protocol minimum (5 MiB); only tests and
	// memory-constrained installations should go below DefaultPartSize.
	MinPartSize int64 = 5 << 20
	// MaxPartSize is the S3 protocol maximum (5 GiB).
	MaxPartSize int64 = 5 << 30
	// MaxParts is the S3 limit on parts per upload.
	MaxParts = 10000
	// MaxObjectSize is the S3 limit on object size (5 TiB).
	MaxObjectSize int64 = 5 << 40
	// DefaultConcurrency is the number of parts uploaded in parallel.
	DefaultConcurrency = 2
	// MaxConcurrency bounds Concurrency.
	MaxConcurrency = 16
	// MaxRetentionDays bounds the default retention (100 years).
	MaxRetentionDays = 36500
	// maxCABundleBytes bounds a custom CA bundle.
	maxCABundleBytes = 1 << 20
)

// SSE is the server-side encryption mode.
type SSE string

// Server-side encryption modes.
const (
	SSENone SSE = ""
	SSES3   SSE = "AES256"  // SSE-S3 (provider-managed keys)
	SSEKMS  SSE = "aws:kms" // SSE-KMS (KMSKeyID optional: the account default key)
)

// ChecksumMode selects how integrity checksums are sent.
type ChecksumMode string

// Checksum modes.
const (
	// ChecksumWhenSupported sends a SHA-256 checksum with every PutObject
	// and UploadPart (multipart objects get a composite SHA-256) and
	// validates response checksums. Use it for AWS S3 and stores that
	// implement the flexible checksum headers.
	ChecksumWhenSupported ChecksumMode = "when_supported"
	// ChecksumWhenRequired sends checksums only where the protocol needs
	// them (Content-MD5 on Object Lock buckets); for S3-compatible stores
	// without the flexible checksum headers. This is the SDK's
	// RequestChecksumCalculation=WhenRequired.
	ChecksumWhenRequired ChecksumMode = "when_required"
)

// allowedStorageClasses are the classes whose objects can be read
// immediately (restores must not wait for an archive retrieval).
var allowedStorageClasses = map[string]struct{}{
	"STANDARD": {}, "STANDARD_IA": {}, "ONEZONE_IA": {}, "INTELLIGENT_TIERING": {},
	"GLACIER_IR": {}, "REDUCED_REDUNDANCY": {},
}

// Config configures an S3 destination.
type Config struct {
	// Endpoint is the base URL of an S3-compatible service
	// ("https://minio.example.com:9000"); empty means AWS S3. It must be
	// https unless AllowHTTP is set; no user info, path, query or fragment.
	Endpoint string
	// Region is the signing region (for example "eu-central-1"; minio
	// uses "us-east-1").
	Region string
	// Bucket is the bucket name.
	Bucket string
	// Prefix is the optional key prefix inside the bucket (validated like
	// storage.ParsePrefix). Every object lives below it.
	Prefix string
	// PathStyle addresses the bucket as {endpoint}/{bucket} instead of
	// {bucket}.{endpoint} (needed for most S3-compatible stores).
	PathStyle bool

	// AccessKeyID and SecretAccessKey are static credentials (the only
	// supported kind: no environment, shared files or instance metadata).
	// The destination keeps a reference to SecretAccessKey and never logs
	// it; the caller destroys it after Close.
	AccessKeyID     string
	SecretAccessKey *crypto.Secret

	// CABundlePEM, if set, replaces the system roots: only these CAs are
	// trusted for the endpoint (private S3 such as minio).
	CABundlePEM []byte

	// SSE and KMSKeyID select server-side encryption.
	SSE      SSE
	KMSKeyID string
	// StorageClass is an optional immediately readable storage class.
	StorageClass string
	// Checksum selects the checksum mode (default ChecksumWhenSupported).
	Checksum ChecksumMode

	// LockMode and RetentionDays set a default Object Lock retention for
	// every Put that does not request one. They require a bucket with
	// Object Lock enabled (Open fails with storage.ErrUnsupported
	// otherwise).
	LockMode      storage.LockMode
	RetentionDays int
	// NoDelete refuses every Delete (retention is left to lifecycle rules).
	NoDelete bool
	// ConditionalWrites sends "If-None-Match: *" so the store refuses to
	// overwrite atomically (AWS S3 and current minio support it). When
	// false, Put relies on a Stat pre-check (racy, see the package doc).
	// Nil means true.
	ConditionalWrites *bool

	// PartSize is the multipart part size (default DefaultPartSize, at
	// least MinPartSize). Objects up to PartSize use a single PutObject.
	PartSize int64
	// Concurrency is the number of parts uploaded in parallel (default
	// DefaultConcurrency). Memory per Put is PartSize × Concurrency.
	Concurrency int

	// AllowHTTP permits an http:// endpoint. Set it only in dev mode
	// (dev.enabled); Open logs a warning.
	AllowHTTP bool
	// Guard is the SSRF guard every connection is dialed through
	// (required).
	Guard *netguard.Guard
	// Logger receives warnings (default slog.Default()).
	Logger *slog.Logger
}

var (
	regionRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	bucketRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	// kmsKeyRE accepts key IDs, aliases and ARNs.
	kmsKeyRE = regexp.MustCompile(`^[A-Za-z0-9:/_+=.@-]+$`)
	// accessKeyRE is deliberately loose (providers differ) but excludes
	// whitespace and control characters.
	accessKeyRE = regexp.MustCompile(`^[\x21-\x7e]{3,128}$`)
)

// conditional reports whether conditional writes are enabled.
func (c *Config) conditional() bool {
	return c.ConditionalWrites == nil || *c.ConditionalWrites
}

// validated is the checked form of Config.
type validated struct {
	endpoint *url.URL // nil for AWS
	prefix   storage.Key
	roots    *x509.CertPool
}

// validate checks the configuration and fills defaults in place.
func (c *Config) validate() (*validated, error) {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf(format, a...))
	}
	v := &validated{}

	if c.Guard == nil {
		bad("a netguard is required")
	}
	if c.Endpoint != "" {
		u, err := parseEndpoint(c.Endpoint, c.AllowHTTP)
		if err != nil {
			errs = append(errs, err)
		}
		v.endpoint = u
	}
	if !regionRE.MatchString(c.Region) {
		bad("invalid region")
	}
	if !bucketRE.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") {
		bad("invalid bucket name")
	} else if net.ParseIP(c.Bucket) != nil {
		bad("bucket name must not be an IP address")
	}
	if p, err := storage.ParsePrefix(c.Prefix); err != nil {
		bad("invalid prefix: %v", err)
	} else {
		v.prefix = p
	}
	if !accessKeyRE.MatchString(c.AccessKeyID) {
		bad("invalid access key id")
	}
	if c.SecretAccessKey.Len() == 0 {
		bad("the secret access key is required")
	}
	if len(c.CABundlePEM) > 0 {
		if len(c.CABundlePEM) > maxCABundleBytes {
			bad("CA bundle too large")
		} else {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(c.CABundlePEM) {
				bad("CA bundle contains no PEM certificate")
			}
			v.roots = pool
		}
	}
	switch c.SSE {
	case SSENone, SSES3:
		if c.KMSKeyID != "" {
			bad("a KMS key id requires SSE-KMS")
		}
	case SSEKMS:
		if c.KMSKeyID != "" && (len(c.KMSKeyID) > 2048 || !kmsKeyRE.MatchString(c.KMSKeyID)) {
			bad("invalid KMS key id")
		}
	default:
		bad("unknown server-side encryption mode")
	}
	if c.StorageClass != "" {
		if _, ok := allowedStorageClasses[c.StorageClass]; !ok {
			bad("storage class %q is not allowed (archive classes cannot be restored directly)", c.StorageClass)
		}
	}
	switch c.Checksum {
	case "":
		c.Checksum = ChecksumWhenSupported
	case ChecksumWhenSupported, ChecksumWhenRequired:
	default:
		bad("unknown checksum mode")
	}
	switch c.LockMode {
	case storage.LockNone:
		if c.RetentionDays != 0 {
			bad("retention days require a lock mode")
		}
	case storage.LockGovernance, storage.LockCompliance:
		if c.RetentionDays < 1 || c.RetentionDays > MaxRetentionDays {
			bad("retention days must be 1..%d", MaxRetentionDays)
		}
	default:
		bad("unknown lock mode")
	}
	switch {
	case c.PartSize == 0:
		c.PartSize = DefaultPartSize
	case c.PartSize < MinPartSize || c.PartSize > MaxPartSize:
		bad("part size must be %d..%d bytes", MinPartSize, MaxPartSize)
	}
	switch {
	case c.Concurrency == 0:
		c.Concurrency = DefaultConcurrency
	case c.Concurrency < 1 || c.Concurrency > MaxConcurrency:
		bad("concurrency must be 1..%d", MaxConcurrency)
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: s3 destination: %w", storage.ErrInvalidArgument, errors.Join(errs...))
	}
	return v, nil
}

// parseEndpoint validates an endpoint URL.
func parseEndpoint(s string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, errors.New("invalid endpoint URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return nil, errors.New("endpoint must use https (http only in dev mode)")
		}
	default:
		return nil, errors.New("endpoint must be an https URL")
	}
	if u.User != nil {
		return nil, errors.New("endpoint must not contain user info")
	}
	if u.Hostname() == "" {
		return nil, errors.New("endpoint has no host")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("endpoint must not have a path, query or fragment")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.ParseUint(p, 10, 16); err != nil || n == 0 {
			return nil, errors.New("invalid endpoint port")
		}
	}
	u.Path = ""
	return u, nil
}

// maxObjectSize is the largest object this configuration can upload.
func (c *Config) maxObjectSize() int64 {
	return min(MaxObjectSize, c.PartSize*MaxParts)
}

// Validate checks the configuration exactly like Open does (including the
// required Guard and SecretAccessKey) without connecting. It works on a
// copy, so defaults are not filled into c. internal/destinations uses it to
// reject an invalid destination when it is saved (task 2.12).
func (c Config) Validate() error {
	_, err := c.validate()
	return err
}
