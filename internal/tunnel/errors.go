package tunnel

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/jlentink/bekup/internal/sshpolicy"
)

// Error kinds. Every error returned by this package (except a plain
// context.Canceled) is an *Error whose Kind is one of these sentinels, so
// callers can use errors.Is.
var (
	// ErrInvalidConfig: the configuration is incomplete or malformed (this
	// includes a missing host-key pin and an undecryptable private key).
	ErrInvalidConfig = errors.New("invalid ssh tunnel configuration")
	// ErrHostKeyMismatch: the server presented a host key other than the
	// pinned one, or does not offer the pinned key type at all. Treat it as
	// a security event (possible impersonation / MITM).
	ErrHostKeyMismatch = sshpolicy.ErrHostKeyMismatch
	// ErrUnsupportedHostKey: the server's host key type or size is not
	// accepted (for example ssh-dss or RSA below 2048 bits).
	ErrUnsupportedHostKey = sshpolicy.ErrUnsupportedHostKey
	// ErrAuthFailed: the server rejected the credentials.
	ErrAuthFailed = errors.New("ssh authentication failed")
	// ErrUnreachable: the bastion (or, for DialTarget, the target behind
	// it) could not be reached, or forwarding was refused.
	ErrUnreachable = errors.New("ssh endpoint unreachable")
	// ErrTimeout: the dial, handshake or a keepalive timed out.
	ErrTimeout = errors.New("ssh connection timed out")
	// ErrHandshake: the SSH handshake failed for another reason (no common
	// algorithm, protocol error, connection closed by the server).
	ErrHandshake = errors.New("ssh handshake failed")
	// ErrClosed: the tunnel is closed or the connection to the bastion was
	// lost.
	ErrClosed = errors.New("ssh tunnel closed")
)

// Error is a normalized tunnel error. Its message never contains secrets:
// causes are only attached when they are known to be free of credential
// material (network and SSH protocol errors).
type Error struct {
	// Kind is one of the Err* sentinels of this package.
	Kind error
	// Op is the operation: "dial", "fetch_host_key", "dial_target",
	// "forward" or "keepalive".
	Op string
	// Addr is the host:port the operation was talking to.
	Addr  string
	cause error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("tunnel: ")
	b.WriteString(e.Op)
	if e.Addr != "" {
		b.WriteString(" ")
		b.WriteString(e.Addr)
	}
	b.WriteString(": ")
	b.WriteString(e.Kind.Error())
	var mismatch *sshpolicy.MismatchError
	switch {
	case errors.As(e.cause, &mismatch):
		b.WriteString(": ")
		b.WriteString(mismatch.Detail())
	case e.cause != nil:
		b.WriteString(": ")
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Unwrap exposes both the kind and the cause to errors.Is / errors.As.
func (e *Error) Unwrap() []error {
	if e.cause == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.cause}
}

func newError(kind error, op, addr string, cause error) *Error {
	return &Error{Kind: kind, Op: op, Addr: addr, cause: cause}
}

// classifyNetErr maps a dial/IO error to a kind.
func classifyNetErr(ctx context.Context, err, fallback error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ErrTimeout
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return ErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	return fallback
}

// classifyHandshake maps an ssh.NewClientConn error to a kind.
func classifyHandshake(ctx context.Context, err error) error {
	if sshpolicy.IsAlgorithmNegotiation(err) {
		return ErrHandshake
	}
	if sshpolicy.IsAuthFailure(err) {
		return ErrAuthFailed
	}
	return classifyNetErr(ctx, err, ErrHandshake)
}

// discardErr explicitly ignores a teardown error (closing connections,
// half-closing, best-effort deadlines): it carries nothing to act on.
func discardErr(error) {}

// discardCopy ignores the result of a forwarding io.Copy: the copy ends
// with an error whenever either side closes, which is the normal case.
func discardCopy(int64, error) {}
