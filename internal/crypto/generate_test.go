package crypto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/logging"
)

func TestWordlist(t *testing.T) {
	wl := wordlist()
	if len(wl) != WordlistSize {
		t.Fatalf("%d words", len(wl))
	}
	seen := make(map[string]bool, len(wl))
	for _, w := range wl {
		if seen[w] || strings.ContainsAny(w, PassphraseSeparator) {
			t.Fatalf("duplicate or separator-containing word %q", w)
		}
		seen[w] = true
	}
}

func TestGeneratePassphrase(t *testing.T) {
	if PassphraseEntropyBits(MinPassphraseWords) < 128 {
		t.Fatalf("default passphrase entropy %.1f < 128 bits", PassphraseEntropyBits(MinPassphraseWords))
	}
	inList := make(map[string]bool)
	for _, w := range wordlist() {
		inList[w] = true
	}
	seen := map[string]bool{}
	for range 50 {
		p, err := GeneratePassphrase(MinPassphraseWords)
		if err != nil {
			t.Fatal(err)
		}
		words := strings.Split(string(p.Bytes()), PassphraseSeparator)
		if len(words) != MinPassphraseWords {
			t.Fatalf("%d words", len(words))
		}
		for _, w := range words {
			if !inList[w] {
				t.Fatalf("word %q not in list", w)
			}
		}
		if seen[string(p.Bytes())] {
			t.Fatal("repeated passphrase")
		}
		seen[string(p.Bytes())] = true
		p.Destroy()
	}
	for _, n := range []int{0, MinPassphraseWords - 1, MaxPassphraseWords + 1} {
		if _, err := GeneratePassphrase(n); err == nil {
			t.Fatalf("%d words accepted", n)
		}
	}
}

func TestGenerateAgeKeypair(t *testing.T) {
	kp, err := GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	if !strings.HasPrefix(kp.Recipient, "age1") || !strings.HasPrefix(string(kp.Identity.Bytes()), "AGE-SECRET-KEY-1") {
		t.Fatal("unexpected encodings")
	}
	id, err := age.ParseX25519Identity(string(kp.Identity.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if id.Recipient().String() != kp.Recipient {
		t.Fatal("identity does not match recipient")
	}
	// Round trip through the helpers.
	var buf bytes.Buffer
	w, err := EncryptToRecipients(&buf, kp.Recipient)
	if err != nil {
		t.Fatal(err)
	}
	must(w.Write([]byte("dump bytes")))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := DecryptWithIdentities(&buf, append([]byte("# identity file\n"), kp.Identity.Bytes()...))
	if err != nil {
		t.Fatal(err)
	}
	if got := must(io.ReadAll(r)); string(got) != "dump bytes" {
		t.Fatalf("got %q", got)
	}
}

func TestGenerateSSHKeypair(t *testing.T) {
	kp, err := GenerateSSHKeypair("bekup@example")
	if err != nil {
		t.Fatal(err)
	}
	defer kp.PrivateKeyPEM.Destroy()
	if !bytes.HasPrefix(kp.PrivateKeyPEM.Bytes(), []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		t.Fatal("not an OpenSSH private key")
	}
	signer, err := ssh.ParsePrivateKey(kp.PrivateKeyPEM.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatal(signer.PublicKey().Type())
	}
	pub, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(kp.AuthorizedKey))
	if err != nil || len(rest) != 0 || comment != "bekup@example" {
		t.Fatalf("authorized key: %v %q", err, comment)
	}
	if !bytes.Equal(pub.Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal("public key mismatch")
	}
	if kp.Fingerprint != ssh.FingerprintSHA256(pub) || !strings.HasPrefix(kp.Fingerprint, "SHA256:") {
		t.Fatal(kp.Fingerprint)
	}
	if _, ok := pub.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey); !ok {
		t.Fatal("not ed25519")
	}
	if _, err := GenerateSSHKeypair("a\nb"); err == nil {
		t.Fatal("multi-line comment accepted")
	}
	noComment, err := GenerateSSHKeypair("")
	if err != nil || strings.Count(noComment.AuthorizedKey, " ") != 1 {
		t.Fatalf("no comment: %q %v", noComment.AuthorizedKey, err)
	}
}

func TestSecretRedaction(t *testing.T) {
	const value = "super-secret-value"
	s := NewSecret([]byte(value))
	type wrapper struct {
		S  *Secret `json:"S"`
		V  Secret  `json:"V"`
		Ok string  `json:"Ok"`
	}
	w := wrapper{S: s, V: *s, Ok: "visible"}

	outs := make([]string, 0, 32)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		outs = append(outs, fmt.Sprintf(verb, s), fmt.Sprintf(verb, *s), fmt.Sprintf(verb, w))
	}
	outs = append(outs, s.String(), s.GoString(), fmt.Sprint(w))

	j, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	outs = append(outs, string(j))
	if !strings.Contains(string(j), `"S":"[REDACTED]"`) || !strings.Contains(string(j), "visible") {
		t.Fatal(string(j))
	}

	var logBuf bytes.Buffer
	// Plain slog handlers (no redaction layer) still redact via LogValuer.
	for _, h := range []slog.Handler{slog.NewJSONHandler(&logBuf, nil), slog.NewTextHandler(&logBuf, nil)} {
		l := slog.New(h)
		l.Info("event", slog.Any("value", s), slog.Any("value_by_val", *s), slog.Any("wrapper", w))
	}
	outs = append(outs, logBuf.String())

	for _, o := range outs {
		if strings.Contains(o, value) || strings.Contains(o, fmt.Sprintf("%x", value)) {
			t.Fatalf("secret leaked: %s", o)
		}
	}
	if !strings.Contains(logBuf.String(), logging.Redacted) {
		t.Fatal(logBuf.String())
	}

	b := s.Bytes()
	if s.Len() != len(value) {
		t.Fatal("len")
	}
	s.Destroy()
	s.Destroy()
	if !bytes.Equal(b, make([]byte, len(value))) || s.Bytes() != nil || s.Len() != 0 {
		t.Fatal("Destroy did not zero")
	}
	var nilSecret *Secret
	nilSecret.Destroy()
	if nilSecret.Bytes() != nil || nilSecret.Len() != 0 {
		t.Fatal("nil secret")
	}

	buf := []byte("abc")
	Zero(buf)
	if !bytes.Equal(buf, []byte{0, 0, 0}) {
		t.Fatal("Zero")
	}
}
