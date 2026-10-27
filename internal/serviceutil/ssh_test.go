package serviceutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/crypto"
)

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("want a FieldError, got %v", err)
	}
	return fe.Field
}

func TestHostKeyLine(t *testing.T) {
	kp, err := crypto.GenerateSSHKeypair("host comment")
	if err != nil {
		t.Fatal(err)
	}
	kp.PrivateKeyPEM.Destroy()
	info, err := HostKeyLine(kp.AuthorizedKey, "authorizedKey")
	if err != nil || info.Type != ssh.KeyAlgoED25519 || info.FingerprintSHA256 != kp.Fingerprint || strings.Contains(info.AuthorizedKey, "comment") {
		t.Fatalf("%+v %v", info, err)
	}
	for _, bad := range []string{"", "ssh-ed25519", "from=\"x\" " + kp.AuthorizedKey, kp.AuthorizedKey + "\n" + kp.AuthorizedKey} {
		if _, err := HostKeyLine(bad, "authorizedKey"); fieldOf(t, err) != "authorizedKey" {
			t.Errorf("%q accepted", bad)
		}
	}
	small, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // G403: a weak key on purpose
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&small.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = HostKeyLine(string(ssh.MarshalAuthorizedKey(pub)), "authorizedKey")
	var fe *FieldError
	if !errors.As(err, &fe) || !strings.Contains(fe.Message, "not accepted") {
		t.Fatalf("small RSA host key: %v", err)
	}
}

func TestCheckSSHPrivateKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("pass phrase"))
	if err != nil {
		t.Fatal(err)
	}
	enc := pem.EncodeToMemory(block)
	line, err := CheckSSHPrivateKey(enc, []byte("pass phrase"), "k", "p")
	if err != nil || !strings.HasPrefix(line, "ssh-ed25519 ") {
		t.Fatalf("%q %v", line, err)
	}
	if _, err := CheckSSHPrivateKey(enc, nil, "k", "p"); fieldOf(t, err) != "p" {
		t.Fatalf("missing passphrase: %v", err)
	}
	if _, err := CheckSSHPrivateKey(enc, []byte("wrong"), "k", "p"); fieldOf(t, err) != "p" {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, err := CheckSSHPrivateKey([]byte("garbage"), nil, "k", "p"); fieldOf(t, err) != "k" {
		t.Fatalf("garbage: %v", err)
	}
}
