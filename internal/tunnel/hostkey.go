package tunnel

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/sshpolicy"
)

// The host-key and algorithm policy lives in internal/sshpolicy (D-140),
// shared with the SFTP backend; the names below keep this package's API.

// HostKeyPin pins the bastion's host key. Set Type plus FingerprintSHA256
// (OpenSSH format "SHA256:<base64>"), or AuthorizedKey (an authorized_keys
// style line "<type> <base64> [comment]"), or both (they must agree).
// Validate returns errors wrapping sshpolicy.ErrInvalidPin or
// ErrUnsupportedHostKey.
type HostKeyPin = sshpolicy.Pin

// HostKeyInfo is a host key presented by a server (public data).
type HostKeyInfo = sshpolicy.HostKeyInfo

// HostKeyMismatchError describes a host-key pin violation. It matches
// ErrHostKeyMismatch with errors.Is. The fingerprints are public data and
// may be logged and shown in the security alert (task 5.5).
type HostKeyMismatchError = sshpolicy.MismatchError

// errFetched aborts the FetchHostKey handshake once the key was captured.
var errFetched = errors.New("host key captured")

// normalizePin validates the pin as a tunnel error.
func normalizePin(p HostKeyPin) (sshpolicy.NormalizedPin, error) {
	n, err := p.Normalize()
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, sshpolicy.ErrUnsupportedHostKey):
		return n, newError(ErrUnsupportedHostKey, "dial", "", err)
	default:
		return n, newError(ErrInvalidConfig, "dial", "", err)
	}
}

// FetchHostKey connects to host:port through dialer, performs the key
// exchange only and returns the host key the server presents, aborting
// before any authentication. It is the trust-on-first-use step: the result
// must be confirmed by an admin and is never trusted automatically. Without
// a deadline on ctx, DefaultDialTimeout applies.
func FetchHostKey(ctx context.Context, host string, port int, dialer Dialer) (HostKeyInfo, error) {
	const op = "fetch_host_key"
	if port == 0 {
		port = 22
	}
	if err := hostname.Validate(host); err != nil {
		return HostKeyInfo{}, newError(ErrInvalidConfig, op, "", plainError("host: "+err.Error()))
	}
	if !validPort(port) {
		return HostKeyInfo{}, newError(ErrInvalidConfig, op, "", plainError("port out of range"))
	}
	if dialer == nil {
		return HostKeyInfo{}, newError(ErrInvalidConfig, op, "", plainError("dialer is required"))
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	var (
		mu       sync.Mutex
		captured ssh.PublicKey
	)
	cfg := &ssh.ClientConfig{
		Config: sshpolicy.Algorithms(),
		// A user name is part of the protocol, but authentication is never
		// reached: the callback aborts the handshake first.
		User:              "bekup",
		HostKeyAlgorithms: sshpolicy.FetchHostKeyAlgorithms(),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			mu.Lock()
			captured = key
			mu.Unlock()
			return errFetched
		},
	}
	timeout := DefaultDialTimeout
	if _, ok := ctx.Deadline(); ok {
		timeout = MaxDialTimeout
	}
	conn, err := handshake(ctx, op, addr, dialer, timeout, cfg)
	if err == nil {
		// Unreachable in practice: the callback always aborts.
		discardErr(conn.Close())
		return HostKeyInfo{}, newError(ErrHandshake, op, addr, plainError("handshake completed unexpectedly"))
	}
	mu.Lock()
	key := captured
	mu.Unlock()
	if key == nil || !errors.Is(err, errFetched) {
		return HostKeyInfo{}, err
	}
	if cerr := sshpolicy.CheckHostKey(key); cerr != nil {
		return HostKeyInfo{}, newError(ErrUnsupportedHostKey, op, addr, cerr)
	}
	return sshpolicy.InfoOf(key), nil
}
