package dbprobe

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"

	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// UnverifiedTLS is the configuration of TLS mode UNVERIFIED (the audited,
// step-up admin downgrade of D-123 / D-135, Go-driver side of D-134):
// encrypted, the server is not authenticated. The SNI still carries the
// host name. One of the three InsecureSkipVerify uses in bekup (with the
// pin capture in pinning.Checker.Fetch and internal/db's tls_mode require).
func UnverifiedTLS(host string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec // G402: TLS mode UNVERIFIED is an explicit, audited admin downgrade (D-123, D-134, D-135)
	}
}

// tlsReason classifies TLS failures. Certificate verification failures in
// pinned mode are pin mismatches (the pinned anchor is the only root);
// they are returned as *pinning.MismatchError causes so the runner maps
// them like a failed pre-flight. A refused TLS upgrade is a TLS failure.
// Other handshake failures are "unreachable": a non-database service on
// the port must look like a closed port.
func tlsReason(err error) (runnerpb.Reason, string, bool) {
	var me *pinning.MismatchError
	if errors.As(err, &me) {
		return runnerpb.ReasonPinMismatch, me.Reason, true
	}
	var (
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &unknown):
		return runnerpb.ReasonPinMismatch, pinning.ReasonUntrusted, true
	case errors.As(err, &hostname):
		return runnerpb.ReasonPinMismatch, pinning.ReasonHostname, true
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return runnerpb.ReasonPinMismatch, pinning.ReasonExpired, true
		}
		return runnerpb.ReasonPinMismatch, pinning.ReasonUntrusted, true
	}
	// pgconn's answer to an 'N' reply to the SSLRequest (no exported
	// sentinel): only a PostgreSQL server sends it.
	if strings.Contains(err.Error(), "server refused TLS connection") {
		return runnerpb.ReasonTLSFailed, "tls refused", true
	}
	return "", "", false
}

// MismatchCause returns the pin-mismatch error behind a probe error with
// ReasonPinMismatch (a *pinning.MismatchError), so callers can map it
// to runnerpb.PinMismatchStatus.
func MismatchCause(err error) (*pinning.MismatchError, bool) {
	var pe *Error
	if !errors.As(err, &pe) || pe.Reason != runnerpb.ReasonPinMismatch {
		return nil, false
	}
	var me *pinning.MismatchError
	if errors.As(err, &me) {
		return me, true
	}
	return &pinning.MismatchError{Reason: pe.Detail}, true
}
