package sshpolicy

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func pemOf(t *testing.T, priv any, passphrase string) []byte {
	t.Helper()
	var (
		block *pem.Block
		err   error
	)
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block)
}

func TestParseClientKey(t *testing.T) {
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	big, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	small, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // G403: a weak key on purpose
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		pem, pass []byte
		typ       string
	}{
		"ed25519":           {pemOf(t, ed, ""), nil, ssh.KeyAlgoED25519},
		"ed25519 encrypted": {pemOf(t, ed, "s3cret-pass"), []byte("s3cret-pass"), ssh.KeyAlgoED25519},
		"ecdsa":             {pemOf(t, ec, ""), nil, ssh.KeyAlgoECDSA384},
		"rsa":               {pemOf(t, big, ""), nil, ssh.KeyAlgoRSA},
	} {
		t.Run(name, func(t *testing.T) {
			k, err := ParseClientKey(tc.pem, tc.pass)
			if err != nil {
				t.Fatal(err)
			}
			if k.Signer.PublicKey().Type() != tc.typ {
				t.Errorf("type = %q", k.Signer.PublicKey().Type())
			}
			if !strings.HasPrefix(k.AuthorizedKey(), tc.typ+" ") || !strings.HasPrefix(k.Fingerprint(), FingerprintPrefix) {
				t.Errorf("public key = %q %q", k.AuthorizedKey(), k.Fingerprint())
			}
			if tc.typ == ssh.KeyAlgoRSA {
				ms, ok := k.Signer.(ssh.MultiAlgorithmSigner)
				if !ok {
					t.Fatal("RSA signer is not restricted to SHA-2 algorithms")
				}
				for _, a := range ms.Algorithms() {
					if a != ssh.KeyAlgoRSASHA512 && a != ssh.KeyAlgoRSASHA256 {
						t.Errorf("RSA signature algorithm %q offered", a)
					}
				}
			}
			k.Destroy()
			k.Destroy()
			if k.Signer != nil {
				t.Error("signer kept after Destroy")
			}
		})
	}

	for name, tc := range map[string]struct {
		pem, pass []byte
		want      error
	}{
		"malformed":        {[]byte("-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n"), nil, ErrKeyMalformed},
		"passphrase need":  {pemOf(t, ed, "pw-123456"), nil, ErrKeyPassphraseRequired},
		"passphrase wrong": {pemOf(t, ed, "pw-123456"), []byte("nope-nope"), ErrKeyPassphraseIncorrect},
		"small rsa":        {pemOf(t, small, ""), nil, ErrKeyRSATooShort},
		"too large":        {make([]byte, MaxPrivateKeyLen+1), nil, ErrKeyTooLarge},
		"long passphrase":  {pemOf(t, ed, ""), make([]byte, MaxPasswordLen+1), ErrPassphraseTooLong},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseClientKey(tc.pem, tc.pass)
			if !errors.Is(err, tc.want) || !errors.Is(err, ErrInvalidClientKey) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "nope") || strings.Contains(err.Error(), "asn1") {
				t.Errorf("error leaks details: %v", err)
			}
		})
	}
}

func TestZeroPrivateKey(t *testing.T) {
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ZeroPrivateKey(&ed)
	for _, b := range ed {
		if b != 0 {
			t.Fatal("ed25519 key not zeroed")
		}
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ZeroPrivateKey(k)
	for _, w := range append(k.D.Bits(), k.Primes[0].Bits()...) {
		if w != 0 {
			t.Fatal("RSA key not zeroed")
		}
	}
	ZeroPrivateKey(nil)
	ZeroPrivateKey((*rsa.PrivateKey)(nil))
}

func TestValidateUser(t *testing.T) {
	for _, ok := range []string{"bekup", "user@host", "a.b-c_d"} {
		if err := ValidateUser(ok); err != nil {
			t.Errorf("ValidateUser(%q) = %v", ok, err)
		}
	}
	for bad, msg := range map[string]string{
		"": "required", strings.Repeat("u", MaxUserLen+1): "too long", "-oProxyCommand": "must not start with '-'",
		"a b": "invalid character", "a\x7f": "invalid character", "ü": "invalid character",
	} {
		if err := ValidateUser(bad); err == nil || err.Error() != msg {
			t.Errorf("ValidateUser(%q) = %v, want %q", bad, err, msg)
		}
	}
}
