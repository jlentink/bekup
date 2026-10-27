package sftp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"

	pkgsftp "github.com/pkg/sftp"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// mapErr wraps an SFTP error with op and attaches a storage class so that
// storage.Classify yields a meaningful code. The raw error (which may name
// server paths) stays in the chain for admin logs only.
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("%s: %w", op, err)
	var se *pkgsftp.StatusError
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, os.ErrClosed):
		return wrapped // storage.Classify maps these
	case errors.Is(err, pkgsftp.ErrSSHFxConnectionLost), errors.Is(err, pkgsftp.ErrSSHFxNoConnection),
		errors.Is(err, net.ErrClosed):
		return storage.WithCode(storage.CodeNetwork, wrapped)
	case errors.As(err, &se):
		switch se.FxCode() { //nolint:exhaustive // other codes keep the generic class
		case pkgsftp.ErrSSHFxOpUnsupported:
			return fmt.Errorf("%w: %w", storage.ErrUnsupported, wrapped)
		case pkgsftp.ErrSSHFxNoSuchFile:
			return fmt.Errorf("%w: %w", storage.ErrNotFound, wrapped)
		case pkgsftp.ErrSSHFxPermissionDenied:
			return fmt.Errorf("%w: %w", storage.ErrPermission, wrapped)
		}
	}
	return wrapped
}

// dialError classifies connection and handshake failures.
func dialError(op string, err error) error {
	wrapped := fmt.Errorf("sftp: %s: %w", op, err)
	var ne net.Error
	switch {
	case errors.Is(err, netguard.ErrBlockedAddress), errors.Is(err, netguard.ErrNoAddresses):
		// Not reachable by policy; the admin log has the netguard reason.
		return storage.WithCode(storage.CodeNetwork, wrapped)
	case isAuthFailure(err):
		return storage.WithCode(storage.CodePermission, wrapped)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return storage.WithCode(storage.CodeTimeout, wrapped)
	case errors.As(err, &ne):
		if ne.Timeout() {
			return storage.WithCode(storage.CodeTimeout, wrapped)
		}
		return storage.WithCode(storage.CodeNetwork, wrapped)
	}
	// Other handshake failures (no common algorithm, protocol errors,
	// connection reset during the handshake).
	return storage.WithCode(storage.CodeNetwork, wrapped)
}

// isAuthFailure detects x/crypto/ssh's client authentication failure,
// which has no exported type.
func isAuthFailure(err error) bool {
	return strings.Contains(err.Error(), "ssh: unable to authenticate")
}
