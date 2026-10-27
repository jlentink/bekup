package sftp

import (
	"errors"

	"github.com/jlentink/bekup/internal/sshpolicy"
)

// The host-key and transport policy lives in internal/sshpolicy (D-140),
// shared with internal/tunnel; the names below keep this package's API.

// HostKeyPin pins the SFTP server's host key. Set Type plus
// FingerprintSHA256 (OpenSSH format "SHA256:<base64>"), or AuthorizedKey
// (an authorized_keys style line "<type> <base64> [comment]"), or both
// (they must agree). It is the same type as tunnel.HostKeyPin.
type HostKeyPin = sshpolicy.Pin

// ErrHostKeyMismatch reports that the server presented a host key other
// than the pinned one (or does not offer the pinned key type). The
// concrete error is a *HostKeyMismatchError; storage.Classify reports
// storage.CodeHostKeyMismatch (a security event).
var ErrHostKeyMismatch = sshpolicy.ErrHostKeyMismatch

// HostKeyMismatchError carries the pinned and presented keys (public data,
// shown to admins so they can verify a legitimate key rotation).
type HostKeyMismatchError = sshpolicy.MismatchError

// normalizePin validates the pin as an ErrInvalidConfig error.
func normalizePin(p HostKeyPin) (sshpolicy.NormalizedPin, error) {
	n, err := p.Normalize()
	if err != nil {
		var msg string
		if errors.Is(err, sshpolicy.ErrUnsupportedHostKey) {
			msg = "host key pin: " + err.Error()
		} else {
			msg = err.Error()
		}
		return n, invalid(msg)
	}
	return n, nil
}
