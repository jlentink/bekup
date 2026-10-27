package sshpolicy

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func ed25519Signer(t testing.TB) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func ecdsaSigner(t testing.TB) ssh.Signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rsaSigner(t testing.TB, bits int) ssh.Signer {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func authorizedLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestPinNormalize(t *testing.T) {
	ed, _ := ed25519Signer(t)
	other, _ := ed25519Signer(t)
	ec := ecdsaSigner(t)
	small := rsaSigner(t, 1024)
	edLine := authorizedLine(ed.PublicKey())
	edFP := ssh.FingerprintSHA256(ed.PublicKey())

	ok := []struct {
		name string
		pin  Pin
		typ  string
	}{
		{"authorized key", Pin{AuthorizedKey: edLine}, ssh.KeyAlgoED25519},
		{"authorized key with comment", Pin{AuthorizedKey: edLine + " root@bastion"}, ssh.KeyAlgoED25519},
		{"fingerprint", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: edFP}, ssh.KeyAlgoED25519},
		{"padded fingerprint", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: edFP + "="}, ssh.KeyAlgoED25519},
		{"both agree", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: edFP, AuthorizedKey: edLine}, ssh.KeyAlgoED25519},
		{"ecdsa", Pin{AuthorizedKey: authorizedLine(ec.PublicKey())}, ssh.KeyAlgoECDSA256},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.pin.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.pin.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if p.Type() != tc.typ {
				t.Errorf("type = %q", p.Type())
			}
			if tc.typ == ssh.KeyAlgoED25519 && p.Fingerprint() != edFP {
				t.Errorf("fingerprint = %q, want %q", p.Fingerprint(), edFP)
			}
		})
	}

	bad := []struct {
		name string
		pin  Pin
		kind error
	}{
		{"missing", Pin{}, ErrInvalidPin},
		{"type only", Pin{Type: ssh.KeyAlgoED25519}, ErrInvalidPin},
		{"fingerprint without type", Pin{FingerprintSHA256: edFP}, ErrInvalidPin},
		{"md5 fingerprint", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: "MD5:aa:bb"}, ErrInvalidPin},
		{"bad base64", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: "SHA256:!!!"}, ErrInvalidPin},
		{"short fingerprint", Pin{Type: ssh.KeyAlgoED25519, FingerprintSHA256: "SHA256:" + base64.RawStdEncoding.EncodeToString([]byte("short"))}, ErrInvalidPin},
		{"malformed line", Pin{AuthorizedKey: "ssh-ed25519 AAAA"}, ErrInvalidPin},
		{"two lines", Pin{AuthorizedKey: edLine + "\n" + authorizedLine(other.PublicKey())}, ErrInvalidPin},
		{"too long", Pin{AuthorizedKey: edLine + " " + strings.Repeat("c", MaxAuthorizedKeyLineLen)}, ErrInvalidPin},
		{"fingerprint disagrees", Pin{FingerprintSHA256: ssh.FingerprintSHA256(other.PublicKey()), AuthorizedKey: edLine}, ErrInvalidPin},
		{"type disagrees", Pin{Type: ssh.KeyAlgoECDSA256, AuthorizedKey: edLine}, ErrInvalidPin},
		{"dss type", Pin{Type: "ssh-dss", FingerprintSHA256: edFP}, ErrUnsupportedHostKey},
		{"rsa-sha2 as type", Pin{Type: ssh.KeyAlgoRSASHA256, FingerprintSHA256: edFP}, ErrUnsupportedHostKey},
		{"cert type", Pin{Type: ssh.CertAlgoED25519v01, FingerprintSHA256: edFP}, ErrUnsupportedHostKey},
		{"small rsa", Pin{AuthorizedKey: authorizedLine(small.PublicKey())}, ErrUnsupportedHostKey},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.pin.Normalize(); !errors.Is(err, tc.kind) {
				t.Fatalf("err = %v, want %v", err, tc.kind)
			}
			if err := tc.pin.Validate(); !errors.Is(err, tc.kind) {
				t.Fatalf("Validate err = %v, want %v", err, tc.kind)
			}
		})
	}
}

func TestPinCallback(t *testing.T) {
	ed, _ := ed25519Signer(t)
	other, _ := ed25519Signer(t)
	ec := ecdsaSigner(t)
	pin, err := Pin{AuthorizedKey: authorizedLine(ed.PublicKey())}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	addr := &net.TCPAddr{}

	cb, check := NewPinCallback(pin, "h:22")
	if err := cb("h:22", addr, ed.PublicKey()); err != nil {
		t.Fatalf("pinned key rejected: %v", err)
	}
	if v := check.Violation(errors.New("later failure")); v != nil {
		t.Fatalf("violation after a match: %v", v)
	}
	for name, key := range map[string]ssh.PublicKey{"other key": other.PublicKey(), "other type": ec.PublicKey()} {
		t.Run(name, func(t *testing.T) {
			cb, check := NewPinCallback(pin, "h:22")
			if err := cb("h:22", addr, key); err == nil {
				t.Fatal("wrong key accepted")
			}
			var m *MismatchError
			if v := check.Violation(errHostKeyRejected); !errors.As(v, &m) || !errors.Is(v, ErrHostKeyMismatch) {
				t.Fatalf("violation = %v", v)
			}
			if m.Addr != "h:22" || m.PresentedType != key.Type() || m.PresentedFingerprint != ssh.FingerprintSHA256(key) ||
				m.PinnedFingerprint != ssh.FingerprintSHA256(ed.PublicKey()) {
				t.Fatalf("mismatch = %+v", m)
			}
		})
	}
	// Same digest, different type claim: never accepted.
	forged := NormalizedPin{typ: ssh.KeyAlgoECDSA256, sum: sha256.Sum256(ed.PublicKey().Marshal())}
	if cb, _ := NewPinCallback(forged, "h:22"); cb("h:22", addr, ed.PublicKey()) == nil {
		t.Fatal("type confusion accepted")
	}
	// A zero pin accepts nothing.
	if cb, _ := NewPinCallback(NormalizedPin{}, "h:22"); cb("h:22", addr, ed.PublicKey()) == nil {
		t.Fatal("zero pin accepted a key")
	}
	// A pinned but unacceptable key (cannot be produced by Normalize).
	small := rsaSigner(t, 1024)
	weak := NormalizedPin{typ: ssh.KeyAlgoRSA, sum: sha256.Sum256(small.PublicKey().Marshal())}
	cb, check = NewPinCallback(weak, "h:22")
	if cb("h:22", addr, small.PublicKey()) == nil {
		t.Fatal("small RSA key accepted")
	}
	if v := check.Violation(errHostKeyRejected); !errors.Is(v, ErrUnsupportedHostKey) {
		t.Fatalf("violation = %v", v)
	}
}

func TestViolationNegotiation(t *testing.T) {
	ed, _ := ed25519Signer(t)
	pin, err := Pin{AuthorizedKey: authorizedLine(ed.PublicKey())}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	_, check := NewPinCallback(pin, "h:22")
	neg := &ssh.AlgorithmNegotiationError{What: "host key"}
	var m *MismatchError
	if v := check.Violation(neg); !errors.As(v, &m) || m.PresentedType != "" || m.PinnedType != ssh.KeyAlgoED25519 {
		t.Fatalf("violation = %v", v)
	}
	if !strings.Contains(m.Error(), "does not offer") {
		t.Errorf("Error() = %q", m.Error())
	}
	if v := check.Violation(&ssh.AlgorithmNegotiationError{What: "cipher"}); v != nil {
		t.Fatalf("cipher negotiation reported as %v", v)
	}
	m.PresentedType, m.PresentedFingerprint = "ssh-ed25519", "SHA256:b"
	if !strings.Contains(m.Error(), "presented ssh-ed25519 SHA256:b") || !strings.Contains(m.Error(), "h:22") {
		t.Errorf("Error() = %q", m.Error())
	}
}

func TestAlgorithmsSupported(t *testing.T) {
	sup := ssh.SupportedAlgorithms()
	cfg := Algorithms()
	check := func(what string, want, have []string) {
		for _, w := range want {
			found := false
			for _, h := range have {
				found = found || h == w
			}
			if !found {
				t.Errorf("%s %q not supported by x/crypto/ssh", what, w)
			}
		}
	}
	check("kex", cfg.KeyExchanges, sup.KeyExchanges)
	check("cipher", cfg.Ciphers, sup.Ciphers)
	check("mac", cfg.MACs, sup.MACs)
	check("fetch host keys", FetchHostKeyAlgorithms(), sup.HostKeys)
	for typ, algos := range hostKeyAlgos {
		check("host key "+typ, algos, sup.HostKeys)
		for _, a := range algos {
			if a == ssh.KeyAlgoRSA {
				t.Error("ssh-rsa (SHA-1) signatures must not be offered")
			}
		}
	}
	for _, a := range FetchHostKeyAlgorithms() {
		if a == ssh.KeyAlgoRSA || a == "ssh-dss" {
			t.Errorf("weak algorithm %q offered for host-key capture", a)
		}
	}
	if HostKeyAlgorithms("ssh-dss") != nil {
		t.Error("algorithms for ssh-dss")
	}
}

func TestParseHostKeyLine(t *testing.T) {
	ed, _ := ed25519Signer(t)
	line := authorizedLine(ed.PublicKey())
	info, err := ParseHostKeyLine("  " + line + " admin@laptop  ")
	if err != nil {
		t.Fatal(err)
	}
	if info.AuthorizedKey != line || info.Type != ssh.KeyAlgoED25519 || info.FingerprintSHA256 != ssh.FingerprintSHA256(ed.PublicKey()) {
		t.Fatalf("info = %+v", info)
	}
	if p, err := info.Pin().Normalize(); err != nil || !p.Matches(ed.PublicKey()) {
		t.Fatalf("pin of the parsed line: %v", err)
	}
	small := rsaSigner(t, 1024)
	for name, tc := range map[string]struct {
		line string
		kind error
	}{
		"empty":       {"", ErrMalformedHostKeyLine},
		"two lines":   {line + "\n" + line, ErrMalformedHostKeyLine},
		"options":     {`command="x" ` + line, ErrMalformedHostKeyLine},
		"garbage":     {"ssh-ed25519 !!!", ErrMalformedHostKeyLine},
		"too long":    {line + " " + strings.Repeat("c", MaxAuthorizedKeyLineLen), ErrMalformedHostKeyLine},
		"small rsa":   {authorizedLine(small.PublicKey()), ErrUnsupportedHostKey},
		"cr":          {line + "\r comment", ErrMalformedHostKeyLine},
		"only a type": {"ssh-ed25519", ErrMalformedHostKeyLine},
	} {
		if _, err := ParseHostKeyLine(tc.line); !errors.Is(err, tc.kind) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.kind)
		}
	}
}

func FuzzPin(f *testing.F) {
	ed, _ := ed25519Signer(f)
	line := authorizedLine(ed.PublicKey())
	f.Add("", ssh.FingerprintSHA256(ed.PublicKey()), line)
	f.Add(ssh.KeyAlgoED25519, ssh.FingerprintSHA256(ed.PublicKey()), "")
	f.Add("ssh-rsa", "SHA256:", "ssh-rsa AAAAB3NzaC1yc2E")
	f.Fuzz(func(t *testing.T, typ, fp, authorized string) {
		p, err := Pin{Type: typ, FingerprintSHA256: fp, AuthorizedKey: authorized}.Normalize()
		if err != nil {
			if !errors.Is(err, ErrInvalidPin) && !errors.Is(err, ErrUnsupportedHostKey) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		if !AcceptedHostKeyType(p.Type()) {
			t.Fatalf("accepted type %q", p.Type())
		}
		if !strings.HasPrefix(p.Fingerprint(), FingerprintPrefix) {
			t.Fatal("bad fingerprint")
		}
	})
}

func FuzzParseHostKeyLine(f *testing.F) {
	ed, _ := ed25519Signer(f)
	f.Add(authorizedLine(ed.PublicKey()))
	f.Add("ssh-dss AAAA")
	f.Fuzz(func(t *testing.T, line string) {
		info, err := ParseHostKeyLine(line)
		if err != nil {
			return
		}
		if !AcceptedHostKeyType(info.Type) || strings.ContainsAny(info.AuthorizedKey, "\r\n") {
			t.Fatalf("accepted %+v", info)
		}
		if again, err := ParseHostKeyLine(info.AuthorizedKey); err != nil || again != info {
			t.Fatalf("canonical line does not round-trip: %v", err)
		}
	})
}
