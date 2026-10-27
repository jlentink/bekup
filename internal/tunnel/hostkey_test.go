package tunnel

import (
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/sshpolicy"
)

// The pin format, the pin callback and the algorithm policy are tested in
// internal/sshpolicy; these tests cover the tunnel's view of them.

func TestHostKeyPinKinds(t *testing.T) {
	ed, _ := ed25519Signer(t)
	small := rsaSigner(t, 1024)
	edFP := ssh.FingerprintSHA256(ed.PublicKey())

	if _, err := normalizePin(HostKeyPin{AuthorizedKey: authorizedLine(ed.PublicKey())}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		pin  HostKeyPin
		kind error
	}{
		"missing":   {HostKeyPin{}, ErrInvalidConfig},
		"type only": {HostKeyPin{Type: ssh.KeyAlgoED25519}, ErrInvalidConfig},
		"dss type":  {HostKeyPin{Type: "ssh-dss", FingerprintSHA256: edFP}, ErrUnsupportedHostKey},
		"small rsa": {HostKeyPin{AuthorizedKey: authorizedLine(small.PublicKey())}, ErrUnsupportedHostKey},
	} {
		_, err := normalizePin(tc.pin)
		var te *Error
		if !errors.As(err, &te) || !errors.Is(err, tc.kind) {
			t.Errorf("%s: err = %v, want *Error of kind %v", name, err, tc.kind)
		}
	}
	// The aliases share the sentinels with sshpolicy.
	if !errors.Is(ErrHostKeyMismatch, sshpolicy.ErrHostKeyMismatch) || !errors.Is(ErrUnsupportedHostKey, sshpolicy.ErrUnsupportedHostKey) {
		t.Fatal("tunnel and sshpolicy sentinels differ")
	}
	if !errors.Is(&HostKeyMismatchError{}, ErrHostKeyMismatch) {
		t.Fatal("HostKeyMismatchError does not match ErrHostKeyMismatch")
	}
}
