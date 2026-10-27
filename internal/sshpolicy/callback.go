package sshpolicy

import (
	"errors"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// MismatchError describes a host-key pin violation. It matches
// ErrHostKeyMismatch with errors.Is. The fingerprints are public data and
// may be logged and shown to admins (so they can verify a legitimate key
// rotation) and in security alerts.
type MismatchError struct {
	// Addr is the host:port that presented the key ("" if unknown).
	Addr              string
	PinnedType        string
	PinnedFingerprint string
	// PresentedType is empty if the server does not offer the pinned type.
	PresentedType        string
	PresentedFingerprint string
}

func (e *MismatchError) Error() string {
	var b strings.Builder
	b.WriteString("ssh")
	if e.Addr != "" {
		b.WriteString(" ")
		b.WriteString(e.Addr)
	}
	b.WriteString(": ")
	b.WriteString(ErrHostKeyMismatch.Error())
	b.WriteString(": ")
	b.WriteString(e.Detail())
	return b.String()
}

// Detail describes the violation without the address (public data).
func (e *MismatchError) Detail() string {
	if e.PresentedType == "" {
		return "the server does not offer a " + e.PinnedType + " host key"
	}
	return "pinned " + e.PinnedType + " " + e.PinnedFingerprint +
		", presented " + e.PresentedType + " " + e.PresentedFingerprint
}

// Is reports ErrHostKeyMismatch.
func (e *MismatchError) Is(target error) bool { return target == ErrHostKeyMismatch }

// errHostKeyRejected aborts the handshake after the callback recorded why.
var errHostKeyRejected = errors.New("host key rejected")

// PinCheck records what the host-key callback of one handshake saw.
type PinCheck struct {
	pin  NormalizedPin
	addr string

	mu          sync.Mutex
	mismatch    *MismatchError
	unsupported error
}

// NewPinCallback returns a host-key callback that accepts exactly the
// pinned key (and only an accepted key type) and the PinCheck that
// records a violation. Use pin.Algorithms() as the client's
// HostKeyAlgorithms so the server cannot switch to another key type.
func NewPinCallback(pin NormalizedPin, addr string) (ssh.HostKeyCallback, *PinCheck) {
	pc := &PinCheck{pin: pin, addr: addr}
	return pc.callback, pc
}

func (pc *PinCheck) callback(_ string, _ net.Addr, key ssh.PublicKey) error {
	match := !pc.pin.IsZero() && pc.pin.Matches(key)
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !match {
		pc.mismatch = &MismatchError{
			Addr:                 pc.addr,
			PinnedType:           pc.pin.typ,
			PinnedFingerprint:    pc.pin.Fingerprint(),
			PresentedType:        key.Type(),
			PresentedFingerprint: ssh.FingerprintSHA256(key),
		}
		return errHostKeyRejected
	}
	if err := CheckHostKey(key); err != nil {
		pc.unsupported = err
		return errHostKeyRejected
	}
	return nil
}

// Violation interprets a failed handshake: a *MismatchError when the
// server presented another key or does not offer the pinned key type, an
// error wrapping ErrUnsupportedHostKey for an unacceptable key, or nil
// when the failure was not caused by the host key (the caller classifies
// handshakeErr itself).
func (pc *PinCheck) Violation(handshakeErr error) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	switch {
	case pc.mismatch != nil:
		m := *pc.mismatch
		return &m
	case pc.unsupported != nil:
		return pc.unsupported
	case handshakeErr != nil && IsHostKeyNegotiation(handshakeErr):
		// The server does not offer the pinned key type: a pin violation
		// like a different key.
		return &MismatchError{Addr: pc.addr, PinnedType: pc.pin.typ, PinnedFingerprint: pc.pin.Fingerprint()}
	}
	return nil
}

// IsHostKeyNegotiation reports whether err is a failure to agree on a host
// key algorithm (the server does not offer the pinned key type).
func IsHostKeyNegotiation(err error) bool {
	var neg *ssh.AlgorithmNegotiationError
	return errors.As(err, &neg) && strings.Contains(neg.What, "host key")
}

// IsAlgorithmNegotiation reports a failure to agree on any algorithm.
func IsAlgorithmNegotiation(err error) bool {
	var neg *ssh.AlgorithmNegotiationError
	return errors.As(err, &neg)
}

// IsAuthFailure detects x/crypto/ssh's client authentication failure,
// which has no exported type.
func IsAuthFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ssh: unable to authenticate")
}
