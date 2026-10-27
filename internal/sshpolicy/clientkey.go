package sshpolicy

import (
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrInvalidClientKey is wrapped by every ParseClientKey error. The
// specific Err*Key* values below say why; their messages are static and
// never describe key material (parser errors are never attached).
var ErrInvalidClientKey = errors.New("invalid ssh private key")

// Reasons of an ErrInvalidClientKey (compare with errors.Is).
var (
	ErrKeyTooLarge            = errorf(ErrInvalidClientKey, "the private key is too large")
	ErrPassphraseTooLong      = errorf(ErrInvalidClientKey, "the passphrase is too long")
	ErrKeyPassphraseRequired  = errorf(ErrInvalidClientKey, "the private key is encrypted; a passphrase is required")
	ErrKeyPassphraseIncorrect = errorf(ErrInvalidClientKey, "cannot decrypt the private key with this passphrase")
	ErrKeyMalformed           = errorf(ErrInvalidClientKey, "malformed or unsupported private key (OpenSSH or PEM format expected)")
	ErrKeyRSATooShort         = errorf(ErrInvalidClientKey, "RSA keys shorter than 2048 bits are not accepted")
	ErrKeyTypeNotAccepted     = errorf(ErrInvalidClientKey, "key type not accepted (use ed25519, ECDSA or RSA ≥ 2048 bits)")
)

// clientKeyTypes are the accepted client key types (no DSA, no
// security keys).
var clientKeyTypes = map[string]struct{}{
	ssh.KeyAlgoED25519: {}, ssh.KeyAlgoECDSA256: {}, ssh.KeyAlgoECDSA384: {},
	ssh.KeyAlgoECDSA521: {}, ssh.KeyAlgoRSA: {},
}

// ClientKey is a parsed client private key. Call Destroy when it is no
// longer needed.
type ClientKey struct {
	// Signer signs with the key (RSA keys only with SHA-2 signatures).
	Signer ssh.Signer
	raw    any
}

// AuthorizedKey is the public key as an authorized_keys line (no comment).
func (k *ClientKey) AuthorizedKey() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.Signer.PublicKey())))
}

// Fingerprint is the public key's OpenSSH SHA-256 fingerprint.
func (k *ClientKey) Fingerprint() string { return ssh.FingerprintSHA256(k.Signer.PublicKey()) }

// Destroy zeroes the parsed key (best effort, see ZeroPrivateKey) and
// drops the signer. It is safe on nil and more than once.
func (k *ClientKey) Destroy() {
	if k == nil {
		return
	}
	ZeroPrivateKey(k.raw)
	k.raw = nil
	k.Signer = nil
}

// ParseClientKey parses a PEM/OpenSSH private key, decrypting it with
// passphrase when one is given, and enforces the client key policy:
// ed25519, ECDSA (P-256/384/521) or RSA ≥ 2048 bits (signing with
// rsa-sha2-512/256 only). The caller still owns (and zeroes) pemBytes and
// passphrase. Errors wrap ErrInvalidClientKey and one of the ErrKey*
// reasons.
func ParseClientKey(pemBytes, passphrase []byte) (*ClientKey, error) {
	if len(pemBytes) > MaxPrivateKeyLen {
		return nil, ErrKeyTooLarge
	}
	if len(passphrase) > MaxPasswordLen {
		return nil, ErrPassphraseTooLong
	}
	var (
		raw any
		err error
	)
	if len(passphrase) > 0 {
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, passphrase)
	} else {
		raw, err = ssh.ParseRawPrivateKey(pemBytes)
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		switch {
		case errors.As(err, &missing):
			return nil, ErrKeyPassphraseRequired
		case errors.Is(err, x509.IncorrectPasswordError):
			return nil, ErrKeyPassphraseIncorrect
		default:
			return nil, ErrKeyMalformed
		}
	}
	if k, ok := raw.(*rsa.PrivateKey); ok && (k.N == nil || k.N.BitLen() < MinRSABits) {
		ZeroPrivateKey(raw)
		return nil, ErrKeyRSATooShort
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		ZeroPrivateKey(raw)
		return nil, ErrKeyTypeNotAccepted
	}
	if _, ok := clientKeyTypes[signer.PublicKey().Type()]; !ok {
		ZeroPrivateKey(raw)
		return nil, ErrKeyTypeNotAccepted
	}
	if signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		as, ok := signer.(ssh.AlgorithmSigner)
		if !ok {
			ZeroPrivateKey(raw)
			return nil, ErrKeyTypeNotAccepted
		}
		// SHA-2 signatures only (no ssh-rsa / SHA-1).
		ms, err := ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256})
		if err != nil {
			ZeroPrivateKey(raw)
			return nil, ErrKeyTypeNotAccepted
		}
		signer = ms
	}
	return &ClientKey{Signer: signer, raw: raw}, nil
}

// ZeroPrivateKey overwrites the private parts of a parsed key. Copies made
// by the standard library (RSA precomputed values held in internal form,
// the ECDSA scalar, whose big.Int field is deprecated and must not be
// modified) cannot be reached; see crypto.Zero for the limits.
func ZeroPrivateKey(raw any) {
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		if k != nil {
			clear(*k)
		}
	case ed25519.PrivateKey:
		clear(k)
	case *rsa.PrivateKey:
		if k == nil {
			return
		}
		if k.D != nil {
			clear(k.D.Bits())
		}
		for _, p := range k.Primes {
			if p != nil {
				clear(p.Bits())
			}
		}
		for _, v := range []*big.Int{k.Precomputed.Dp, k.Precomputed.Dq, k.Precomputed.Qinv} {
			if v != nil {
				clear(v.Bits())
			}
		}
	}
}
