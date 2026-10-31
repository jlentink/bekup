package db

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// TestSCRAMRFC7677Vector checks the key derivation against the
// SCRAM-SHA-256 exchange of RFC 7677 section 3 (user "user", password
// "pencil"): the client proof and the server signature computed from our
// StoredKey/ServerKey must match the RFC's values.
func TestSCRAMRFC7677Vector(t *testing.T) {
	salt, err := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	if err != nil {
		t.Fatal(err)
	}
	clientKey, stored, server := scramKeys([]byte("pencil"), salt, 4096)
	authMessage := "n=user,r=rOprNGfwEbeRWgbNEkqO," +
		"r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096," +
		"c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	clientSig := refHMAC(stored, []byte(authMessage))
	proof := make([]byte, len(clientKey))
	for i := range proof {
		proof[i] = clientKey[i] ^ clientSig[i]
	}
	if got := base64.StdEncoding.EncodeToString(proof); got != "dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=" {
		t.Errorf("client proof = %s", got)
	}
	if got := base64.StdEncoding.EncodeToString(refHMAC(server, []byte(authMessage))); got != "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Errorf("server signature = %s", got)
	}
	// StoredKey is SHA-256(ClientKey).
	if sum := sha256.Sum256(clientKey); !hmac.Equal(sum[:], stored) {
		t.Error("stored key mismatch")
	}
}

func TestSCRAMVerifierFormat(t *testing.T) {
	pw := []byte(`p@ss'w"o\rd $1;--x` + "%:/?#")
	v, err := NewSCRAMVerifier(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !validSCRAMVerifier(v) || !strings.HasPrefix(v, "SCRAM-SHA-256$4096:") {
		t.Fatalf("verifier %q", v)
	}
	if strings.Contains(v, string(pw)) || strings.ContainsAny(v, `'"\;`) {
		t.Fatal("verifier contains the password or quotes")
	}
	v2, err := NewSCRAMVerifier(pw)
	if err != nil || v2 == v {
		t.Fatalf("salt not random: %v", err)
	}
	salt := bytes.Repeat([]byte{7}, scramSaltLen)
	a, errA := SCRAMVerifier(pw, salt, 4096)
	b, errB := SCRAMVerifier(pw, salt, 4096)
	if errA != nil || errB != nil || a != b {
		t.Fatal("not deterministic for a fixed salt")
	}
}

func TestCheckRolePassword(t *testing.T) {
	for _, bad := range [][]byte{
		[]byte("short"), []byte(strings.Repeat("x", 15)), []byte(strings.Repeat("x", 1025)),
		[]byte("sixteen-chars-ok\n"), []byte("password-with-\x00-nul"), []byte("pässwörd-with-umlauts"),
	} {
		err := CheckRolePassword(bad)
		if !errors.Is(err, ErrRolePassword) {
			t.Errorf("%q accepted", bad)
			continue
		}
		if len(bad) > 5 && strings.Contains(err.Error(), string(bad)) {
			t.Errorf("error leaks the password: %v", err)
		}
	}
	if err := CheckRolePassword([]byte(" a-good password with spaces ~!@#$%^&*()")); err != nil {
		t.Error(err)
	}
}

func TestValidSCRAMVerifier(t *testing.T) {
	for _, bad := range []string{
		"", "md5abc", "SCRAM-SHA-256$4096:abc$def:ghi",
		"SCRAM-SHA-256$4096:AAAAAAAAAAAAAAAAAAAAAA==$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='",
		"SCRAM-SHA-256$99:AAAAAAAAAAAAAAAAAAAAAA==$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	} {
		if validSCRAMVerifier(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzCheckRolePassword(f *testing.F) {
	f.Add([]byte("a-sixteen-char-pw"))
	f.Add([]byte("x"))
	f.Fuzz(func(t *testing.T, pw []byte) {
		if CheckRolePassword(pw) != nil {
			return
		}
		v, err := SCRAMVerifier(pw, bytes.Repeat([]byte{1}, scramSaltLen), SCRAMIterations)
		if err != nil || !validSCRAMVerifier(v) {
			t.Fatalf("accepted password gives no valid verifier: %v", err)
		}
	})
}

// TestHMACSHA256MatchesStdlib: the own HMAC equals crypto/hmac for short,
// block-sized and long keys and for several message splits.
func TestHMACSHA256MatchesStdlib(t *testing.T) {
	for _, keyLen := range []int{0, 1, 32, 63, 64, 65, 200} {
		key := bytes.Repeat([]byte{0xa5}, keyLen)
		msg := []byte("The quick brown fox jumps over the lazy dog")
		ref := hmac.New(sha256.New, key)
		ref.Write(msg)
		want := ref.Sum(nil)
		m := newHMACSHA256(key)
		if got := m.sum(msg); !bytes.Equal(got, want) {
			t.Errorf("key length %d: %x, want %x", keyLen, got, want)
		}
		if got := m.sum(msg[:10], msg[10:]); !bytes.Equal(got, want) {
			t.Errorf("key length %d (split): %x, want %x", keyLen, got, want)
		}
		m.destroy()
		if m.ipad != [sha256.BlockSize]byte{} || m.opad != [sha256.BlockSize]byte{} {
			t.Errorf("key length %d: pads not zeroed", keyLen)
		}
	}
}

// refHMAC is crypto/hmac, the independent reference for the tests.
func refHMAC(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
