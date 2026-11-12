package destinations

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/sshpolicy"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
	"github.com/jlentink/bekup/internal/storage/webdav"
)

// Type is a destination type.
type Type string

// Destination types (the destination_type enum).
const (
	TypeDisk   Type = "disk"
	TypeS3     Type = "s3"
	TypeSFTP   Type = "sftp"
	TypeWebDAV Type = "webdav"
)

// Valid reports whether t is a known type.
func (t Type) Valid() bool {
	return t == TypeDisk || t == TypeS3 || t == TypeSFTP || t == TypeWebDAV
}

// SFTP authentication methods (SFTPConfig.AuthMethod).
const (
	AuthNone       = ""
	AuthPassword   = "password"
	AuthPrivateKey = "private_key"
)

// Limits of the stored configuration.
const (
	maxNameRunes      = 100
	maxCABundleBytes  = 256 << 10
	maxPrivateKeyLen  = sshpolicy.MaxPrivateKeyLen
	maxPassphraseLen  = sshpolicy.MaxPasswordLen
	maxPasswordLen    = sshpolicy.MaxPasswordLen
	maxSecretKeyLen   = 256
	maxEndpointLen    = 2048
	maxStorageClass   = 64
	maxKMSKeyIDLen    = 2048
	defaultSFTPPort   = 22
	maxSFTPUserLen    = sshpolicy.MaxUserLen
	maxSFTPHostLen    = hostname.MaxLen
	maxSFTPBasePathLn = 512
	maxWebDAVURLLen   = 2048
	maxWebDAVUserLen  = 256
	maxWebDAVPassLen  = 1024
	// WebDAV request timeout bounds (seconds; 0 = the default).
	minWebDAVTimeout = 5
	maxWebDAVTimeout = 600
)

// DiskConfig is the stored configuration of a disk destination.
type DiskConfig struct {
	// Path is the destination's directory below storage.disk_root (key
	// prefix syntax: relative, no "..", no dot segments).
	Path string `json:"path"`
}

// S3Config is the stored, non-secret configuration of an S3 destination.
// The secret access key is in the sealed credentials; Object Lock and
// no-delete live in their own columns.
type S3Config struct {
	Endpoint          string `json:"endpoint,omitempty"`
	Region            string `json:"region"`
	Bucket            string `json:"bucket"`
	Prefix            string `json:"prefix,omitempty"`
	PathStyle         bool   `json:"path_style"`
	SSE               string `json:"sse,omitempty"` // "", "AES256", "aws:kms"
	KMSKeyID          string `json:"kms_key_id,omitempty"`
	StorageClass      string `json:"storage_class,omitempty"`
	Checksum          string `json:"checksum,omitempty"` // "when_supported" (default) or "when_required"
	ConditionalWrites bool   `json:"conditional_writes"`
	AccessKeyID       string `json:"access_key_id"`
	// CABundlePEM, if set, replaces the system roots for the endpoint.
	CABundlePEM string `json:"ca_bundle_pem,omitempty"`
}

// SFTPConfig is the stored, non-secret configuration of an SFTP
// destination. Credentials are sealed; the host-key pin lives in its own
// columns.
type SFTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	BasePath string `json:"base_path"`
	Dir      string `json:"dir,omitempty"`
	// AuthMethod records which credential is stored ("", "password" or
	// "private_key"); HasPassphrase whether the private key has one.
	AuthMethod    string `json:"auth_method,omitempty"`
	HasPassphrase bool   `json:"has_passphrase,omitempty"`
	// PublicKey is the authorized_keys line of the stored private key
	// (public data, shown so the admin can install it on the server).
	PublicKey string `json:"public_key,omitempty"`
}

// WebDAVConfig is the stored, non-secret configuration of a WebDAV
// destination (D-222). The password is in the sealed credentials; the
// certificate pin lives in its own columns.
type WebDAVConfig struct {
	// URL is the https collection URL (generic) or the server root
	// (nextcloud), without a trailing slash.
	URL string `json:"url"`
	// Flavor is "generic" or "nextcloud".
	Flavor   string `json:"flavor"`
	Username string `json:"username"`
	// Path is the folder below the base (key prefix syntax).
	Path string `json:"path,omitempty"`
	// CABundlePEM, if set, replaces the system roots (unused while a
	// certificate is pinned).
	CABundlePEM string `json:"ca_bundle_pem,omitempty"`
	// TimeoutSeconds bounds one metadata request (0: 60 s).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// UserID is the Nextcloud/ownCloud user id the server resolved for
	// Username (current-user-principal) when the destination was saved;
	// files live below it, and it may differ from the login name (e-mail
	// or LDAP logins). Only for the location overlap check (D-222
	// amendment); empty when unknown (generic flavor, or the server could
	// not be asked while saving). Never an input.
	UserID string `json:"user_id,omitempty"`
}

// TLSPin is a WebDAV destination's pinned server certificate (public
// data, D-222).
type TLSPin struct {
	// SPKISHA256 is the SHA-256 of the certificate's public key (hex).
	SPKISHA256 string
	// CertificatePEM is the pinned certificate.
	CertificatePEM string
	// Certificate describes it (nil if it cannot be parsed).
	Certificate *webdav.Certificate
	SetAt       time.Time
}

// HostKey is a confirmed SFTP host-key pin (public data).
type HostKey struct {
	Type          string
	Fingerprint   string // OpenSSH "SHA256:..."
	AuthorizedKey string // "<type> <base64>"
	ConfirmedAt   time.Time
}

// Destination is the caller-facing view of a destination. It never holds
// credentials, only flags telling which ones are stored.
type Destination struct {
	ID       uuid.UUID
	Name     string
	Type     Type
	NoDelete bool
	// ObjectLockMode and ObjectLockRetentionDays are the S3 default
	// retention applied to every upload ("" and 0 when off).
	ObjectLockMode          storage.LockMode
	ObjectLockRetentionDays int

	Disk   *DiskConfig
	S3     *S3Config
	SFTP   *SFTPConfig
	WebDAV *WebDAVConfig

	HasSecretAccessKey bool
	// HasPassword: an SFTP or WebDAV password is stored.
	HasPassword   bool
	HasPrivateKey bool
	HasPassphrase bool
	HostKey       *HostKey
	// TLSPin is the WebDAV certificate pin (nil: none).
	TLSPin *TLSPin

	// Ready reports whether the destination can be opened; otherwise
	// NotReadyReason says what is missing (a fixed, presentable text).
	Ready          bool
	NotReadyReason string
	// NotReadyMsg is NotReadyReason translatable (D-238).
	NotReadyMsg i18n.Msg

	CreatedBy *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time

	// LastTest is the stored outcome of the newest Test (D-187); nil when
	// never tested.
	LastTest *LastTest
	// Space is the stored outcome of the newest space check (D-227; not
	// Checked when never checked or when the settings changed since).
	Space StoredSpace
}

// LastTest is the stored outcome of a Test (D-187): normalized codes only.
type LastTest struct {
	At time.Time
	OK bool
	// Code is the storage error class of the first failed step ("" when OK).
	Code string
	// Stale: the destination changed after the test.
	Stale bool
}

// DiskInput sets the configuration of a disk destination.
type DiskInput struct {
	Path string
}

// S3Input sets the configuration of an S3 destination. It replaces the
// whole non-secret configuration; SecretAccessKey nil keeps the stored key
// (it is required when creating).
type S3Input struct {
	Endpoint                string
	Region                  string
	Bucket                  string
	Prefix                  string
	PathStyle               bool
	SSE                     string
	KMSKeyID                string
	StorageClass            string
	Checksum                string
	ConditionalWrites       *bool // nil = true
	ObjectLockMode          storage.LockMode
	ObjectLockRetentionDays int
	AccessKeyID             string
	SecretAccessKey         []byte // write-only; nil keeps the stored key
	CABundlePEM             string
}

// SFTPInput sets the configuration of an SFTP destination. It replaces the
// whole non-secret configuration. Password and PrivateKey (with an
// optional Passphrase) are write-only and mutually exclusive: setting one
// removes the other; both nil keep the stored credential.
type SFTPInput struct {
	Host       string
	Port       int
	User       string
	BasePath   string
	Dir        string
	Password   []byte
	PrivateKey []byte
	Passphrase []byte
}

// WebDAVInput sets the configuration of a WebDAV destination. It replaces
// the whole non-secret configuration. Password is write-only; nil keeps
// the stored one, except when the URL, flavor or username changes (D-140:
// it must be entered again then).
type WebDAVInput struct {
	URL            string
	Flavor         string // "" = generic
	Username       string
	Path           string
	CABundlePEM    string
	TimeoutSeconds int
	Password       []byte
}

// CreateInput creates a destination: exactly the input matching Type is
// set.
type CreateInput struct {
	Name     string
	Type     Type
	NoDelete bool
	Disk     *DiskInput
	S3       *S3Input
	SFTP     *SFTPInput
	WebDAV   *WebDAVInput
}

// UpdateInput changes a destination; nil fields stay unchanged. At most
// the input matching the destination's type may be set (the type cannot
// change).
type UpdateInput struct {
	Name     *string
	NoDelete *bool
	Disk     *DiskInput
	S3       *S3Input
	SFTP     *SFTPInput
	WebDAV   *WebDAVInput
}

// inputs are the per-type inputs of a create or update (at most one set).
type inputs struct {
	disk   *DiskInput
	s3     *S3Input
	sftp   *SFTPInput
	webdav *WebDAVInput
}

// zero overwrites the secret fields of the inputs.
func (in inputs) zero() {
	if in.s3 != nil {
		crypto.Zero(in.s3.SecretAccessKey)
	}
	if in.sftp != nil {
		crypto.Zero(in.sftp.Password)
		crypto.Zero(in.sftp.PrivateKey)
		crypto.Zero(in.sftp.Passphrase)
	}
	if in.webdav != nil {
		crypto.Zero(in.webdav.Password)
	}
}

// set reports which inputs are present, by type.
func (in inputs) set() map[Type]bool {
	return map[Type]bool{TypeDisk: in.disk != nil, TypeS3: in.s3 != nil, TypeSFTP: in.sftp != nil, TypeWebDAV: in.webdav != nil}
}

// FieldError is an invalid input; Message is presentable (shared with
// internal/connections, D-140).
type FieldError = serviceutil.FieldError

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// normalizeName trims and checks a destination name.
func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fieldErr("name", "the name is required")
	case !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxNameRunes:
		return "", fieldErr("name", "the name must be at most %d characters", maxNameRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fieldErr("name", "the name must not contain control characters")
		}
	}
	return s, nil
}

// validateDisk checks a disk input.
func validateDisk(in *DiskInput) (DiskConfig, error) {
	p := strings.TrimSpace(in.Path)
	if p == "" {
		return DiskConfig{}, fieldErr("disk.path", "a directory below the disk root is required")
	}
	k, err := storage.ParsePrefix(p)
	if err != nil || k.IsZero() {
		return DiskConfig{}, fieldErr("disk.path",
			"must be a relative path below the disk root (letters, digits and ._~%%+=@- in each segment; no '..' or segments starting with a dot)")
	}
	return DiskConfig{Path: k.String()}, nil
}

// s3ConfigOf turns an S3 input into the stored config (without secrets).
func s3ConfigOf(in *S3Input) (S3Config, error) {
	c := S3Config{
		Endpoint:          strings.TrimSpace(in.Endpoint),
		Region:            strings.TrimSpace(in.Region),
		Bucket:            strings.TrimSpace(in.Bucket),
		Prefix:            strings.Trim(strings.TrimSpace(in.Prefix), "/"),
		PathStyle:         in.PathStyle,
		SSE:               in.SSE,
		KMSKeyID:          strings.TrimSpace(in.KMSKeyID),
		StorageClass:      strings.TrimSpace(in.StorageClass),
		Checksum:          in.Checksum,
		ConditionalWrites: in.ConditionalWrites == nil || *in.ConditionalWrites,
		AccessKeyID:       strings.TrimSpace(in.AccessKeyID),
		CABundlePEM:       strings.TrimSpace(in.CABundlePEM),
	}
	switch {
	case len(c.Endpoint) > maxEndpointLen:
		return c, fieldErr("s3.endpoint", "endpoint too long")
	case len(c.CABundlePEM) > maxCABundleBytes:
		return c, fieldErr("s3.caBundlePem", "CA bundle too large (max %d KiB)", maxCABundleBytes>>10)
	case len(c.StorageClass) > maxStorageClass:
		return c, fieldErr("s3.storageClass", "unknown storage class")
	case len(c.KMSKeyID) > maxKMSKeyIDLen:
		return c, fieldErr("s3.kmsKeyId", "KMS key id too long")
	case len(in.SecretAccessKey) > maxSecretKeyLen:
		return c, fieldErr("s3.secretAccessKey", "secret access key too long")
	}
	if c.Checksum == "" {
		c.Checksum = string(s3store.ChecksumWhenSupported)
	}
	if c.CABundlePEM != "" {
		if err := checkCABundle([]byte(c.CABundlePEM), "s3.caBundlePem"); err != nil {
			return c, err
		}
		c.CABundlePEM += "\n"
	}
	return c, nil
}

// webdavConfigOf turns a WebDAV input into the stored config (without the
// password). The URL is trimmed and loses a trailing slash.
func webdavConfigOf(in *WebDAVInput) (WebDAVConfig, error) {
	c := WebDAVConfig{
		URL:            strings.TrimSuffix(strings.TrimSpace(in.URL), "/"),
		Flavor:         strings.TrimSpace(in.Flavor),
		Username:       strings.TrimSpace(in.Username),
		Path:           strings.Trim(strings.TrimSpace(in.Path), "/"),
		CABundlePEM:    strings.TrimSpace(in.CABundlePEM),
		TimeoutSeconds: in.TimeoutSeconds,
	}
	if c.Flavor == "" {
		c.Flavor = string(webdav.FlavorGeneric)
	}
	switch {
	case len(c.URL) > maxWebDAVURLLen:
		return c, fieldErr("webdav.url", "URL too long")
	case len(c.Username) > maxWebDAVUserLen:
		return c, fieldErr("webdav.username", "username too long")
	case len(in.Password) > maxWebDAVPassLen:
		return c, fieldErr("webdav.password", "password too long")
	case len(c.CABundlePEM) > maxCABundleBytes:
		return c, fieldErr("webdav.caBundlePem", "CA bundle too large (max %d KiB)", maxCABundleBytes>>10)
	case !webdav.Flavor(c.Flavor).Valid():
		return c, fieldErr("webdav.flavor", "unknown flavor")
	case c.TimeoutSeconds != 0 && (c.TimeoutSeconds < minWebDAVTimeout || c.TimeoutSeconds > maxWebDAVTimeout):
		return c, fieldErr("webdav.timeoutSeconds", "the request timeout must be %d..%d seconds", minWebDAVTimeout, maxWebDAVTimeout)
	}
	if c.CABundlePEM != "" {
		if err := checkCABundle([]byte(c.CABundlePEM), "webdav.caBundlePem"); err != nil {
			return c, err
		}
		c.CABundlePEM += "\n"
	}
	return c, nil
}

// checkCABundle requires PEM certificates only (no private keys or other
// blocks pasted by mistake); field names the input in errors.
func checkCABundle(b []byte, field string) error {
	n := 0
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fieldErr(field, "the CA bundle may only contain CERTIFICATE blocks")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fieldErr(field, "the CA bundle contains an invalid certificate")
		}
		n++
	}
	if n == 0 || strings.TrimSpace(string(b)) != "" {
		return fieldErr(field, "the CA bundle must consist of PEM certificates")
	}
	return nil
}

// sftpConfigOf turns an SFTP input into the stored config (without
// credential state, which the caller merges).
func sftpConfigOf(in *SFTPInput) (SFTPConfig, error) {
	c := SFTPConfig{
		Host:     strings.TrimSpace(in.Host),
		Port:     in.Port,
		User:     strings.TrimSpace(in.User),
		BasePath: strings.TrimSpace(in.BasePath),
		Dir:      strings.Trim(strings.TrimSpace(in.Dir), "/"),
	}
	if c.Port == 0 {
		c.Port = defaultSFTPPort
	}
	switch {
	case len(in.Password) > 0 && len(in.PrivateKey) > 0:
		return c, fieldErr("sftp", "set either a password or a private key, not both")
	case len(in.Passphrase) > 0 && len(in.PrivateKey) == 0:
		return c, fieldErr("sftp.privateKeyPassphrase", "a passphrase can only be set together with a private key")
	case len(in.Password) > maxPasswordLen:
		return c, fieldErr("sftp.password", "password too long")
	case len(in.PrivateKey) > maxPrivateKeyLen:
		return c, fieldErr("sftp.privateKey", "private key too large")
	case len(in.Passphrase) > maxPassphraseLen:
		return c, fieldErr("sftp.privateKeyPassphrase", "passphrase too long")
	case len(c.Host) > maxSFTPHostLen, len(c.User) > maxSFTPUserLen, len(c.BasePath) > maxSFTPBasePathLn:
		return c, fieldErr("sftp", "host, user or base path too long")
	}
	return c, nil
}

// checkPrivateKey parses an SSH private key (with its passphrase) with
// the shared client key policy and returns its authorized_keys line
// (serviceutil.CheckSSHPrivateKey: ed25519, ECDSA and RSA ≥ 2048 keys, the
// SFTP backend's policy). Parser errors are never attached.
func checkPrivateKey(pemBytes, passphrase []byte) (string, error) {
	return serviceutil.CheckSSHPrivateKey(pemBytes, passphrase, "sftp.privateKey", "sftp.privateKeyPassphrase")
}
