package tunnel

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/sshpolicy"
)

// Defaults and limits of Config.
const (
	DefaultDialTimeout       = 15 * time.Second
	MaxDialTimeout           = 2 * time.Minute
	DefaultKeepaliveInterval = 15 * time.Second
	MinKeepaliveInterval     = time.Second
	DefaultMaxConns          = 8
	MaxMaxConns              = 256

	// maxPrivateKeyLen bounds the PEM private key (a 16 kbit RSA key is
	// about 12 KiB).
	maxPrivateKeyLen = sshpolicy.MaxPrivateKeyLen
	// maxPasswordLen bounds the SSH password.
	maxPasswordLen = sshpolicy.MaxPasswordLen
)

// Dialer opens the TCP connection to the bastion. It is mandatory: the
// runner passes its target-allowlist dialer (task 2.5), so a missing dialer
// can never silently fall back to an unchecked direct connection.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// Auth holds the client credentials: exactly one of PrivateKey (PEM,
// optionally encrypted with Passphrase) or Password.
//
// Dial consumes the secrets: it destroys (zeroes) every non-nil field
// before it returns, on success and on failure alike.
//
// Limitation: x/crypto/ssh takes the password as a Go string, which cannot
// be zeroed. The string copy is created only during the handshake and is
// left to the garbage collector; prefer key authentication.
type Auth struct {
	PrivateKey *crypto.Secret
	Passphrase *crypto.Secret
	Password   *crypto.Secret
}

// destroy zeroes all secrets of a.
func (a *Auth) destroy() {
	a.PrivateKey.Destroy()
	a.Passphrase.Destroy()
	a.Password.Destroy()
}

// Config describes one tunnel: the bastion (Host, Port, User, Auth,
// HostKeyPin) and the single target behind it (TargetHost, TargetPort).
type Config struct {
	// Host and Port of the SSH bastion (Port defaults to 22).
	Host string
	Port int
	// User is the SSH user name.
	User string
	// Auth holds the credentials; Dial destroys them.
	Auth Auth
	// HostKeyPin is mandatory: Dial fails with ErrInvalidConfig without it.
	HostKeyPin HostKeyPin
	// TargetHost and TargetPort: the only destination the tunnel forwards
	// to (resolved by the bastion).
	TargetHost string
	TargetPort int
	// DialTimeout bounds the TCP dial plus the SSH handshake (default 15s,
	// at most 2m).
	DialTimeout time.Duration
	// KeepaliveInterval between keepalive@openssh.com requests (default
	// 15s); the tunnel is closed after 3 intervals without a reply.
	KeepaliveInterval time.Duration
	// MaxConns bounds the concurrent forwarded connections of Forward
	// (default 8).
	MaxConns int
	// Dialer is mandatory; see Dialer.
	Dialer Dialer
	// Logger receives tunnel events (never secrets); default slog.Default().
	Logger *slog.Logger
}

// settings is the validated, normalized form of Config.
type settings struct {
	addr       string
	targetAddr string
	user       string
	pin        sshpolicy.NormalizedPin
	dialTO     time.Duration
	keepalive  time.Duration
	maxConns   int
	dialer     Dialer
	logger     *slog.Logger
}

func (c *Config) validate() (*settings, error) {
	s := &settings{}
	port := c.Port
	if port == 0 {
		port = 22
	}
	if err := hostname.Validate(c.Host); err != nil {
		return nil, invalid("host: " + err.Error())
	}
	if !validPort(port) {
		return nil, invalid("port out of range")
	}
	s.addr = net.JoinHostPort(c.Host, strconv.Itoa(port))
	if err := hostname.Validate(c.TargetHost); err != nil {
		return nil, invalid("target host: " + err.Error())
	}
	if !validPort(c.TargetPort) {
		return nil, invalid("target port out of range")
	}
	s.targetAddr = net.JoinHostPort(c.TargetHost, strconv.Itoa(c.TargetPort))
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
	case c.MaxConns == 0:
		s.maxConns = DefaultMaxConns
	case c.MaxConns < 0 || c.MaxConns > MaxMaxConns:
		return nil, invalid("max conns out of range")
	default:
		s.maxConns = c.MaxConns
	}
	if c.Dialer == nil {
		return nil, invalid("dialer is required")
	}
	s.dialer = c.Dialer
	s.logger = c.Logger
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s, nil
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

func invalid(msg string) error {
	return newError(ErrInvalidConfig, "dial", "", plainError(msg))
}

// plainError is a static, secret-free error message.
type plainError string

func (e plainError) Error() string { return string(e) }

func validPort(p int) bool { return p >= 1 && p <= 65535 }
