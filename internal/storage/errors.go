package storage

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"syscall"
)

// Sentinel errors returned (wrapped) by every backend. Compare with
// errors.Is.
var (
	// ErrInvalidKey reports a key, prefix or key component that failed
	// validation.
	ErrInvalidKey = errors.New("storage: invalid key")
	// ErrInvalidArgument reports an invalid option (for example a
	// malformed expected checksum).
	ErrInvalidArgument = errors.New("storage: invalid argument")
	// ErrNotFound reports a missing object.
	ErrNotFound = errors.New("storage: object not found")
	// ErrAlreadyExists reports a Put to a key that already holds an object.
	// Backends never overwrite objects.
	ErrAlreadyExists = errors.New("storage: object already exists")
	// ErrDeleteNotPermitted reports a Delete refused by no-delete mode or
	// by an active object lock (retention or legal hold).
	ErrDeleteNotPermitted = errors.New("storage: delete not permitted")
	// ErrChecksumMismatch reports that the streamed bytes did not match
	// PutOptions.ExpectedSHA256; the object was not finalized.
	ErrChecksumMismatch = errors.New("storage: checksum mismatch")
	// ErrUnsupported reports an option the backend cannot honor (for
	// example a retention date on a backend without object lock). Backends
	// fail closed instead of silently ignoring such options.
	ErrUnsupported = errors.New("storage: operation not supported by this destination")
	// ErrInsufficientSpace reports that a free-space check failed.
	ErrInsufficientSpace = errors.New("storage: insufficient free space")
	// ErrPermission reports that the destination refused access.
	ErrPermission = errors.New("storage: permission denied")
	// ErrClosed reports use of a closed destination.
	ErrClosed = errors.New("storage: destination closed")
)

// ErrorCode is a normalized, user-presentable error class. Raw backend
// errors may contain host names, paths or provider messages; only the code
// and its fixed message are shown in the UI (Test results, run logs).
type ErrorCode string

// Error codes.
const (
	CodeOK                 ErrorCode = "ok"
	CodeInvalidKey         ErrorCode = "invalid_key"
	CodeInvalidArgument    ErrorCode = "invalid_argument"
	CodeNotFound           ErrorCode = "not_found"
	CodeAlreadyExists      ErrorCode = "already_exists"
	CodeDeleteNotPermitted ErrorCode = "delete_not_permitted"
	CodeChecksumMismatch   ErrorCode = "checksum_mismatch"
	CodeUnsupported        ErrorCode = "unsupported"
	CodeNoSpace            ErrorCode = "no_space"
	CodePermission         ErrorCode = "permission_denied"
	// CodeHostKeyMismatch: the server presented another SSH host key than
	// the pinned one (or does not offer the pinned key type). A security
	// event (possible impersonation), never an ordinary failure.
	CodeHostKeyMismatch ErrorCode = "host_key_mismatch"
	CodeTimeout         ErrorCode = "timeout"
	CodeCanceled        ErrorCode = "canceled"
	CodeNetwork         ErrorCode = "network"
	CodeClosed          ErrorCode = "closed"
	CodeInternal        ErrorCode = "internal"
)

// codeError lets backends (S3 auth/TLS failures, SFTP host-key mismatch)
// attach a code to an error without inventing a new sentinel for each.
type codeError struct {
	code ErrorCode
	err  error
}

func (e *codeError) Error() string { return e.err.Error() }
func (e *codeError) Unwrap() error { return e.err }

// WithCode wraps err so that Classify returns code. Backends use it for
// classes that have no sentinel (for example authentication or TLS errors).
func WithCode(code ErrorCode, err error) error {
	if err == nil {
		return nil
	}
	return &codeError{code: code, err: err}
}

// Classify maps an error from any backend to a normalized ErrorCode.
func Classify(err error) ErrorCode {
	var ce *codeError
	switch {
	case err == nil:
		return CodeOK
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, ErrInvalidKey):
		return CodeInvalidKey
	case errors.Is(err, ErrInvalidArgument):
		return CodeInvalidArgument
	case errors.Is(err, ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return CodeNotFound
	case errors.Is(err, ErrAlreadyExists), errors.Is(err, fs.ErrExist):
		return CodeAlreadyExists
	case errors.Is(err, ErrDeleteNotPermitted):
		return CodeDeleteNotPermitted
	case errors.Is(err, ErrChecksumMismatch):
		return CodeChecksumMismatch
	case errors.Is(err, ErrUnsupported), errors.Is(err, errors.ErrUnsupported):
		return CodeUnsupported
	case errors.Is(err, ErrInsufficientSpace), errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return CodeNoSpace
	case errors.Is(err, ErrPermission), errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return CodePermission
	case errors.Is(err, ErrClosed), errors.Is(err, os.ErrClosed):
		return CodeClosed
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return CodeTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return CodeTimeout
		}
		return CodeNetwork
	}
	return CodeInternal
}

// Message returns a fixed, secret-free description of the code for users.
func (c ErrorCode) Message() string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeInvalidKey:
		return "the object key is invalid"
	case CodeInvalidArgument:
		return "an option of the request is invalid"
	case CodeNotFound:
		return "the object or location does not exist"
	case CodeAlreadyExists:
		return "an object with this key already exists"
	case CodeDeleteNotPermitted:
		return "deleting is not permitted (no-delete mode or object lock)"
	case CodeChecksumMismatch:
		return "the data did not match the expected checksum"
	case CodeUnsupported:
		return "the destination does not support this operation"
	case CodeNoSpace:
		return "the destination is out of space or over quota"
	case CodePermission:
		return "the destination refused access (check permissions or credentials)"
	case CodeHostKeyMismatch:
		return "the server's SSH host key does not match the pinned key (possible impersonation); verify the new key out of band before confirming it"
	case CodeTimeout:
		return "the operation timed out"
	case CodeCanceled:
		return "the operation was canceled"
	case CodeNetwork:
		return "the destination could not be reached"
	case CodeClosed:
		return "the destination connection is closed"
	case CodeInternal:
		return "an unexpected storage error occurred"
	default:
		return "an unexpected storage error occurred"
	}
}
