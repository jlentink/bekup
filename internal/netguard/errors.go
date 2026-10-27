package netguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"
)

// Errors returned by the guard. They are safe to show to admins.
var (
	// ErrBlockedAddress: the connection would reach a denied address. The
	// concrete error is a *BlockedAddressError.
	ErrBlockedAddress = errors.New("netguard: destination address is not allowed")
	// ErrRedirect: an HTTP client from NewHTTPClient got a redirect, which
	// is never followed.
	ErrRedirect = errors.New("netguard: redirects are not followed")
	// ErrResponseTooLarge: a response exceeded the caller's size limit.
	ErrResponseTooLarge = errors.New("netguard: response too large")
	// ErrUnsupportedNetwork: only tcp, tcp4 and tcp6 can be dialed.
	ErrUnsupportedNetwork = errors.New("netguard: unsupported network")
	// ErrNoAddresses: the host name resolved to no usable address.
	ErrNoAddresses = errors.New("netguard: host has no addresses")
)

// Generic categories returned by Normalize for non-admin callers. They do
// not tell refused, timed-out and unresolvable destinations apart, so a
// "test connection" feature cannot be used as a port scanner.
var (
	ErrNotAllowed  = errors.New("destination not allowed")
	ErrUnreachable = errors.New("destination unreachable")
	ErrTLS         = errors.New("TLS handshake failed")
	ErrFailed      = errors.New("connection failed")
)

// BlockedAddressError reports the denied address and the reason. It
// unwraps to ErrBlockedAddress. It names the IP the host resolved to, so
// show it to admins only (Normalize for everyone else).
type BlockedAddressError struct {
	IP     netip.Addr
	Reason Reason
}

func (e *BlockedAddressError) Error() string {
	return fmt.Sprintf("netguard: address %s is not allowed (%s)", e.IP, e.Reason)
}

// Unwrap returns ErrBlockedAddress.
func (e *BlockedAddressError) Unwrap() error { return ErrBlockedAddress }

// Normalize maps an error from a guarded dial or HTTP request to one of
// ErrNotAllowed, ErrUnreachable, ErrTLS, ErrRedirect, ErrResponseTooLarge
// or ErrFailed. context.Canceled passes through; nil stays nil.
func Normalize(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, ErrBlockedAddress), errors.Is(err, ErrUnsupportedNetwork):
		return ErrNotAllowed
	case errors.Is(err, ErrRedirect):
		return ErrRedirect
	case errors.Is(err, ErrResponseTooLarge):
		return ErrResponseTooLarge
	case isTLSError(err):
		return ErrTLS
	case isUnreachable(err):
		return ErrUnreachable
	default:
		return ErrFailed
	}
}

func isTLSError(err error) bool {
	var (
		rh  tls.RecordHeaderError
		ae  tls.AlertError
		cv  *tls.CertificateVerificationError
		ua  x509.UnknownAuthorityError
		he  x509.HostnameError
		ci  x509.CertificateInvalidError
		ech *tls.ECHRejectionError
	)
	return errors.As(err, &rh) || errors.As(err, &ae) || errors.As(err, &cv) ||
		errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) ||
		errors.As(err, &ech)
}

func isUnreachable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, ErrNoAddresses) {
		return true
	}
	for _, e := range []error{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED,
		syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ETIMEDOUT, syscall.EPIPE,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	var (
		dnsErr *net.DNSError
		opErr  *net.OpError
		netErr net.Error
	)
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		return true
	}
	return errors.As(err, &netErr) && netErr.Timeout()
}
