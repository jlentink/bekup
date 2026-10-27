package sftp

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/sshpolicy"
	"github.com/jlentink/bekup/internal/storage"
)

// Defaults and limits of Config.
const (
	DefaultPort                  = 22
	DefaultDialTimeout           = 15 * time.Second
	MaxDialTimeout               = 2 * time.Minute
	DefaultKeepaliveInterval     = 15 * time.Second
	MinKeepaliveInterval         = time.Second
	DefaultMaxConcurrentRequests = 32
	MaxMaxConcurrentRequests     = 128

	// maxPrivateKeyLen bounds the PEM private key.
	maxPrivateKeyLen = sshpolicy.MaxPrivateKeyLen
	maxPasswordLen   = sshpolicy.MaxPasswordLen
	maxBasePathLen   = 512
)

// ErrInvalidConfig reports an invalid Config. It wraps
// storage.ErrInvalidArgument.
var ErrInvalidConfig = fmt.Errorf("%w: invalid sftp configuration", storage.ErrInvalidArgument)

func invalid(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, msg)
}

// Auth holds the client credentials: exactly one of PrivateKey (PEM,
// optionally encrypted with Passphrase) or Password.
//
// Open takes ownership of all three secrets. The private key and the
// passphrase are destroyed (zeroed) before Open returns, on success and
// failure alike; the parsed key is kept in memory for reconnects. The
// password is kept for reconnects and destroyed by Close (or by Open on
// failure).
//
// Limitation: x/crypto/ssh takes the password as a Go string, which cannot
// be zeroed; the copy exists only during a handshake and is left to the
// garbage collector. Prefer key authentication.
type Auth struct {
	PrivateKey *crypto.Secret
	Passphrase *crypto.Secret
	Password   *crypto.Secret
}

func (a *Auth) destroy() {
	a.PrivateKey.Destroy()
	a.Passphrase.Destroy()
	a.Password.Destroy()
}

func (a *Auth) validate() error {
	hasKey := a.PrivateKey.Len() > 0
	hasPassword := a.Password.Len() > 0
	switch {
	case hasKey && hasPassword:
		return invalid("auth: set either a private key or a password, not both")
	case !hasKey && !hasPassword:
		return invalid("auth: a private key or a password is required")
	case hasPassword && a.Passphrase.Len() > 0:
		return invalid("auth: a passphrase requires a private key")
	case hasKey && a.PrivateKey.Len() > maxPrivateKeyLen:
		return invalid("auth: private key too large")
	case hasPassword && a.Password.Len() > maxPasswordLen:
		return invalid("auth: password too long")
	}
	return nil
}

// Config configures an SFTP destination.
type Config struct {
	// Host and Port of the SFTP server (Port defaults to 22).
	Host string
	Port int
	// User is the SSH user name.
	User string
	// Auth holds the credentials; see Auth for ownership.
	Auth Auth
	// HostKeyPin is mandatory.
	HostKeyPin HostKeyPin
	// BasePath is the absolute, clean path of an existing directory on the
	// server (for a chrooted account usually "/upload" or similar). It is
	// resolved once per connection with SSH_FXP_REALPATH.
	BasePath string
	// Dir is the destination's optional subdirectory below BasePath,
	// validated like a key prefix; it is created (0700) if missing.
	Dir string
	// NoDelete refuses every Delete with storage.ErrDeleteNotPermitted.
	// Enforce it on the server too (an account without delete rights).
	NoDelete bool
	// DialTimeout bounds the guarded TCP dial, the SSH handshake, the SFTP
	// initialization and the base path resolution (default 15s, at most
	// 2m).
	DialTimeout time.Duration
	// KeepaliveInterval between keepalive@openssh.com requests (default
	// 15s); the connection is closed after 3 intervals without a reply,
	// which fails all pending operations.
	KeepaliveInterval time.Duration
	// MaxConcurrentRequests bounds the in-flight SFTP requests per open
	// file (default 32, at most 128).
	MaxConcurrentRequests int
	// Guard is the mandatory SSRF guard every connection is dialed through.
	Guard *netguard.Guard
	// Logger receives backend events (never secrets); default
	// slog.Default().
	Logger *slog.Logger
}

// settings is the validated form of Config.
type settings struct {
	addr        string
	user        string
	pin         sshpolicy.NormalizedPin
	basePath    string
	dir         storage.Key
	noDelete    bool
	dialTO      time.Duration
	keepalive   time.Duration
	maxRequests int
	guard       *netguard.Guard
	logger      *slog.Logger
}

func (c *Config) validate() (*settings, error) {
	s := &settings{noDelete: c.NoDelete}
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	host, err := hostname.ValidateBracketed(c.Host)
	if err != nil {
		return nil, invalid("host: " + err.Error())
	}
	if port < 1 || port > 65535 {
		return nil, invalid("port out of range")
	}
	s.addr = net.JoinHostPort(host, strconv.Itoa(port))
	if err := sshpolicy.ValidateUser(c.User); err != nil {
		return nil, invalid("user: " + err.Error())
	}
	s.user = c.User
	pin, err := normalizePin(c.HostKeyPin)
	if err != nil {
		return nil, err
	}
	s.pin = pin
	if err := c.Auth.validate(); err != nil {
		return nil, err
	}
	if err := validateBasePath(c.BasePath); err != nil {
		return nil, invalid("base path: " + err.Error())
	}
	s.basePath = c.BasePath
	dir, err := storage.ParsePrefix(c.Dir)
	if err != nil {
		return nil, fmt.Errorf("sftp directory: %w", err)
	}
	s.dir = dir
	switch {
	case c.DialTimeout == 0:
		s.dialTO = DefaultDialTimeout
	case c.DialTimeout < 0 || c.DialTimeout > MaxDialTimeout:
		return nil, invalid("dial timeout out of range")
	default:
		s.dialTO = c.DialTimeout
	}
	switch {
	case c.KeepaliveInterval == 0:
		s.keepalive = DefaultKeepaliveInterval
	case c.KeepaliveInterval < MinKeepaliveInterval || c.KeepaliveInterval > MaxDialTimeout:
		return nil, invalid("keepalive interval out of range")
	default:
		s.keepalive = c.KeepaliveInterval
	}
	switch {
	case c.MaxConcurrentRequests == 0:
		s.maxRequests = DefaultMaxConcurrentRequests
	case c.MaxConcurrentRequests < 1 || c.MaxConcurrentRequests > MaxMaxConcurrentRequests:
		return nil, invalid("max concurrent requests out of range")
	default:
		s.maxRequests = c.MaxConcurrentRequests
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

// validateBasePath requires an absolute, clean path without control bytes.
func validateBasePath(p string) error {
	switch {
	case p == "":
		return errors.New("required")
	case len(p) > maxBasePathLen:
		return errors.New("too long")
	case p[0] != '/':
		return errors.New("must be absolute")
	case path.Clean(p) != p:
		return errors.New("must be clean (no '..', '.', duplicate or trailing slashes)")
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < ' ' || c == 0x7f || c == '\\' {
			return errors.New("contains a control character or backslash")
		}
	}
	return nil
}

// credentials is the long-lived form of Auth.
type credentials struct {
	key      *sshpolicy.ClientKey // key auth, destroyed on Close
	password *crypto.Secret       // password auth, destroyed on Close
}

func (c *credentials) method() ssh.AuthMethod {
	if c.key != nil {
		return ssh.PublicKeys(c.key.Signer)
	}
	pw := c.password
	// x/crypto/ssh needs a string; see the limitation on Auth.
	return ssh.PasswordCallback(func() (string, error) {
		if pw.Len() == 0 {
			return "", errors.New("password unavailable")
		}
		return string(pw.Bytes()), nil
	})
}

func (c *credentials) destroy() {
	c.key.Destroy()
	c.key = nil
	c.password.Destroy()
}

// parseAuth turns Auth into credentials. The private key and passphrase
// are destroyed by the caller (Open); the password moves into the result.
func parseAuth(a *Auth) (*credentials, error) {
	if a.Password.Len() > 0 {
		pw := a.Password
		a.Password = nil
		return &credentials{password: pw}, nil
	}
	key, err := sshpolicy.ParseClientKey(a.PrivateKey.Bytes(), a.Passphrase.Bytes())
	if err != nil {
		// Static reasons only; never the parser error.
		return nil, invalid("auth: " + err.Error())
	}
	return &credentials{key: key}, nil
}

// ValidateTarget checks the server address, user name, base path and
// optional subdirectory exactly like Open does, without credentials, a
// host-key pin or a connection (port 0 means DefaultPort).
// internal/destinations uses it to reject an invalid destination when it is
// saved, before the host key is confirmed (task 2.12).
func ValidateTarget(host string, port int, user, basePath, dir string) error {
	if port == 0 {
		port = DefaultPort
	}
	if _, err := hostname.ValidateBracketed(host); err != nil {
		return invalid("host: " + err.Error())
	}
	if port < 1 || port > 65535 {
		return invalid("port out of range")
	}
	if err := sshpolicy.ValidateUser(user); err != nil {
		return invalid("user: " + err.Error())
	}
	if err := validateBasePath(basePath); err != nil {
		return invalid("base path: " + err.Error())
	}
	if _, err := storage.ParsePrefix(dir); err != nil {
		return fmt.Errorf("sftp directory: %w", err)
	}
	return nil
}
