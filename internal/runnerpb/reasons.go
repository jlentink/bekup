package runnerpb

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorDomain is the errdetails.ErrorInfo domain of the runner's
// normalized target errors.
const ErrorDomain = "runner.bekup"

// Reason is a normalized target-connection error category (D-134). The
// runner attaches it as an errdetails.ErrorInfo to the gRPC status; the
// status message is a fixed text per reason. Connection refused, filtered
// ports, timeouts before the server answered, and non-database services
// all yield ReasonUnreachable, so the runner cannot be used as a port
// scanner.
type Reason string

// The reasons.
const (
	// ReasonUnreachable: no database answered (refused, filtered, timed out,
	// DNS, not a database protocol, SSH target leg failed).
	ReasonUnreachable Reason = "TARGET_UNREACHABLE"
	// ReasonAuthFailed: the database rejected the credentials (or the
	// client host / authentication method).
	ReasonAuthFailed Reason = "TARGET_AUTH_FAILED"
	// ReasonTLSFailed: the TLS upgrade or handshake failed (server refused
	// TLS, host-name mismatch, expired certificate, TLS required).
	ReasonTLSFailed Reason = "TARGET_TLS_FAILED"
	// ReasonPinMismatch: the certificate does not match the pin (security
	// event).
	ReasonPinMismatch Reason = "TLS_PIN_MISMATCH"
	// ReasonTimeout: the database answered but an operation timed out.
	ReasonTimeout Reason = "TARGET_TIMEOUT"
	// ReasonPermission: authenticated, but the user lacks a privilege.
	ReasonPermission Reason = "TARGET_PERMISSION_DENIED"
	// ReasonUnsupportedServer: the server is too old, of another engine, or
	// newer than every allowlisted client (PostgreSQL).
	ReasonUnsupportedServer Reason = "TARGET_UNSUPPORTED_SERVER"
	// ReasonDatabaseNotFound: the database is not in the server-reported
	// list.
	ReasonDatabaseNotFound Reason = "TARGET_DATABASE_NOT_FOUND"
	// ReasonDatabaseExists: CreateDatabase found the name already taken.
	ReasonDatabaseExists Reason = "TARGET_DATABASE_EXISTS"
	// ReasonSSHHostKey: the bastion's host key does not match the pin
	// (security event).
	ReasonSSHHostKey Reason = "SSH_HOST_KEY_MISMATCH"
	// ReasonSSHAuthFailed: the bastion rejected the SSH credentials.
	ReasonSSHAuthFailed Reason = "SSH_AUTH_FAILED"
	// ReasonSSHHostKeyUnsupported: FetchHostKey got a host key whose type
	// or size is not accepted (ssh-ed25519, ECDSA or RSA >= 2048 bits).
	ReasonSSHHostKeyUnsupported Reason = "SSH_HOST_KEY_UNSUPPORTED"
	// ReasonFailed: the database answered with another error.
	ReasonFailed Reason = "TARGET_FAILED"
)

// reasonInfo is the fixed code and message of each reason.
var reasonInfo = map[Reason]struct {
	code codes.Code
	msg  string
}{
	ReasonUnreachable:           {codes.Unavailable, "target unreachable"},
	ReasonAuthFailed:            {codes.FailedPrecondition, "target authentication failed"},
	ReasonTLSFailed:             {codes.FailedPrecondition, "target tls handshake failed"},
	ReasonPinMismatch:           {codes.FailedPrecondition, "target certificate does not match the pin"},
	ReasonTimeout:               {codes.DeadlineExceeded, "target operation timed out"},
	ReasonPermission:            {codes.FailedPrecondition, "target user lacks a required privilege"},
	ReasonUnsupportedServer:     {codes.FailedPrecondition, "target server version is not supported by the allowlisted clients"},
	ReasonDatabaseNotFound:      {codes.NotFound, "database not found on the target"},
	ReasonDatabaseExists:        {codes.AlreadyExists, "database already exists on the target"},
	ReasonSSHHostKey:            {codes.FailedPrecondition, "ssh host key does not match the pinned key"},
	ReasonSSHAuthFailed:         {codes.FailedPrecondition, "ssh authentication failed"},
	ReasonSSHHostKeyUnsupported: {codes.FailedPrecondition, "ssh host key type is not supported"},
	ReasonFailed:                {codes.FailedPrecondition, "target operation failed"},
}

// Status returns the normalized status of reason r (fixed code and
// message, ErrorInfo detail).
func (r Reason) Status() error {
	info, ok := reasonInfo[r]
	if !ok {
		return status.Error(codes.Internal, "internal error")
	}
	st := status.New(info.code, info.msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Reason: string(r), Domain: ErrorDomain}); err == nil {
		st = d
	}
	return st.Err()
}

// ErrorReason extracts the normalized reason of a runner status error
// ("" when the error carries none).
func ErrorReason(err error) Reason {
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return ""
	}
	for _, d := range se.GRPCStatus().Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetDomain() == ErrorDomain {
			if _, known := reasonInfo[Reason(ei.GetReason())]; known {
				return Reason(ei.GetReason())
			}
		}
	}
	return ""
}

// MetadataCheck is the ErrorInfo metadata key of a ReasonPinMismatch
// status: which check failed (leaf_spki, untrusted_chain, hostname,
// expired, no_certificate; D-133).
const MetadataCheck = "check"

// PinMismatchStatus is the ReasonPinMismatch status with the failed
// check in the ErrorInfo metadata (serve raises the security alert from
// it, task 5.5). No certificate content is ever included.
func PinMismatchStatus(check string) error {
	info := reasonInfo[ReasonPinMismatch]
	st := status.New(info.code, info.msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: string(ReasonPinMismatch), Domain: ErrorDomain,
		Metadata: map[string]string{MetadataCheck: check},
	}); err == nil {
		st = d
	}
	return st.Err()
}

// PinMismatchCheck reports whether err is a ReasonPinMismatch status and
// returns its check ("" when the status carries none).
func PinMismatchCheck(err error) (string, bool) {
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return "", false
	}
	for _, d := range se.GRPCStatus().Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetDomain() == ErrorDomain && ei.GetReason() == string(ReasonPinMismatch) {
			return ei.GetMetadata()[MetadataCheck], true
		}
	}
	return "", false
}
