package serviceutil

import (
	"errors"

	"github.com/jlentink/bekup/internal/sshpolicy"
)

// CheckSSHPrivateKey parses an SSH private key entered through the API
// with the shared client key policy (sshpolicy.ParseClientKey: ed25519,
// ECDSA or RSA ≥ 2048 bits, the passphrase is verified) and returns its
// authorized_keys line. Problems are *FieldError on keyField, or on
// passField when the passphrase is missing, wrong or too long. The parsed
// key is zeroed before it returns; the caller zeroes its inputs.
func CheckSSHPrivateKey(pemBytes, passphrase []byte, keyField, passField string) (string, error) {
	key, err := sshpolicy.ParseClientKey(pemBytes, passphrase)
	if err != nil {
		field := keyField
		if errors.Is(err, sshpolicy.ErrKeyPassphraseRequired) || errors.Is(err, sshpolicy.ErrKeyPassphraseIncorrect) ||
			errors.Is(err, sshpolicy.ErrPassphraseTooLong) {
			field = passField
		}
		// The sshpolicy messages are static and never describe the key.
		return "", FieldErr(field, "%s", err.Error())
	}
	defer key.Destroy()
	return key.AuthorizedKey(), nil
}

// HostKeyLine parses an admin-entered SSH host key (an authorized_keys
// line, sshpolicy.ParseHostKeyLine: one line of at most
// sshpolicy.MaxAuthorizedKeyLineLen bytes, no options, an accepted key
// type) into its canonical form. Problems are *FieldError on field.
func HostKeyLine(line, field string) (sshpolicy.HostKeyInfo, error) {
	info, err := sshpolicy.ParseHostKeyLine(line)
	switch {
	case err == nil:
		return info, nil
	case errors.Is(err, sshpolicy.ErrUnsupportedHostKey):
		return info, FieldErr(field, "host key type not accepted (ssh-ed25519, ECDSA or RSA ≥ 2048 bits required)")
	default:
		return info, FieldErr(field, "%s", err.Error())
	}
}
