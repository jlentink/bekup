package pinning

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// Upstream describes how the runner reaches a target for a capture or a
// pre-flight: directly through the allowlist guard or through an SSH
// tunnel whose bastion leg uses the guard.
type Upstream struct {
	// Host and Port of the target (resolved by the bastion with a tunnel).
	Host string
	Port int
	// Guard is the target allowlist dialer (mandatory).
	Guard tunnel.Dialer
	// SSH is the validated tunnel (nil = direct).
	SSH *validate.SSH
	// Tunnel carries the tunnel credentials. Connect copies them; the
	// caller still owns (and zeroes) the request.
	Tunnel *runnerpb.SshTunnel
	Logger *slog.Logger
}

// Connect returns a DialFunc for u and a close function that tears down
// the tunnel (always call it).
func Connect(ctx context.Context, u Upstream) (DialFunc, func(), error) {
	if u.Guard == nil {
		return nil, func() {}, errors.New("pinning: guard is required")
	}
	addr := net.JoinHostPort(u.Host, strconv.Itoa(u.Port))
	if u.SSH == nil {
		return func(ctx context.Context) (net.Conn, error) {
			return u.Guard.DialContext(ctx, "tcp", addr)
		}, func() {}, nil
	}
	if u.Tunnel == nil {
		return nil, func() {}, errors.New("pinning: tunnel credentials are required")
	}
	cfg := &tunnel.Config{
		Host:       u.SSH.Bastion.Host,
		Port:       u.SSH.Bastion.Port,
		User:       u.SSH.User,
		HostKeyPin: u.SSH.Pin,
		TargetHost: u.Host,
		TargetPort: u.Port,
		Dialer:     u.Guard,
		Logger:     u.Logger,
		MaxConns:   2,
	}
	if b := u.Tunnel.GetPrivateKeyPem(); len(b) > 0 {
		cfg.Auth.PrivateKey = crypto.NewSecret(bytes.Clone(b))
		if p := u.Tunnel.GetPassphrase(); len(p) > 0 {
			cfg.Auth.Passphrase = crypto.NewSecret(bytes.Clone(p))
		}
	} else {
		cfg.Auth.Password = crypto.NewSecret(bytes.Clone(u.Tunnel.GetPassword()))
	}
	tun, err := tunnel.Dial(ctx, cfg) // destroys cfg.Auth
	if err != nil {
		return nil, func() {}, err
	}
	return tun.DialTarget, func() { discardErr(tun.Close()) }, nil
}
