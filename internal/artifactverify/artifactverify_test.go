package artifactverify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
)

func TestDigestReader(t *testing.T) {
	d := NewDigestReader(strings.NewReader("hello world"), 5)
	if _, err := io.ReadAll(d); !errors.Is(err, ErrTooLong) {
		t.Fatalf("over the limit: %v", err)
	}
	d = NewDigestReader(strings.NewReader("hello"), 5)
	if _, err := io.ReadAll(d); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	if d.N() != 5 || !d.Matches(5, sum[:]) {
		t.Fatal("digest does not match")
	}
	if d.Matches(4, sum[:]) || d.Matches(5, sum[:31]) || d.Matches(5, make([]byte, 32)) {
		t.Fatal("a wrong size or digest matched")
	}
}

func TestMatchIdentity(t *testing.T) {
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	other, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Identity.Destroy()
	if err := MatchIdentity(kp.Identity.Bytes(), kp.Recipient); err != nil {
		t.Fatal(err)
	}
	both := "# keys\n" + string(other.Identity.Bytes()) + "\n" + string(kp.Identity.Bytes()) + "\n"
	if err := MatchIdentity([]byte(both), kp.Recipient); err != nil {
		t.Fatalf("identity file with two keys: %v", err)
	}
	if err := MatchIdentity(other.Identity.Bytes(), kp.Recipient); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("other key: %v", err)
	}
	secret := "AGE-SECRET-KEY-1NOTAKEY"
	err = MatchIdentity([]byte(secret), kp.Recipient)
	if !errors.Is(err, ErrIdentityInvalid) || strings.Contains(err.Error(), secret) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestCheckEntry(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	m := &pipeline.Manifest{Entries: []pipeline.Entry{{
		Path: "app.sql.zst.age", SHA256: hex.EncodeToString(sum[:]), StoredSize: 10,
		Encryption: pipeline.EntryEncryption{Mode: pipeline.EncryptPassphrase, PassphraseVersion: 2},
	}}}
	good := Artifact{SHA256: sum[:], Size: 10, EncryptionMode: "passphrase", PassphraseVersion: 2}
	if err := checkEntry(m, "app.sql.zst.age", good); err != nil {
		t.Fatal(err)
	}
	bad := []Artifact{
		{SHA256: sum[:], Size: 10, EncryptionMode: "passphrase", PassphraseVersion: 1},
		{SHA256: sum[:], Size: 10, EncryptionMode: "none"},
		{SHA256: sum[:], Size: 10, EncryptionMode: "recipients", RecipientVersion: 2},
		{SHA256: sum[:], Size: 11, EncryptionMode: "passphrase", PassphraseVersion: 2},
		{SHA256: make([]byte, 32), Size: 10, EncryptionMode: "passphrase", PassphraseVersion: 2},
		{SHA256: sum[:], Size: 10, EncryptionMode: "bogus"},
	}
	for i, a := range bad {
		if err := checkEntry(m, "app.sql.zst.age", a); !errors.Is(err, ErrManifest) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if err := checkEntry(m, "other.sql.zst.age", good); !errors.Is(err, ErrManifest) {
		t.Errorf("unlisted path: %v", err)
	}
}

func TestStoredFormat(t *testing.T) {
	cases := []struct {
		key        string
		enc        bool
		plain, ext string
		ok         bool
	}{
		{"a/b/app.dump.zst.age", true, "dump", "dump.zst.age", true},
		{"a/b/globals.globals.sql.zst", false, "sql", "sql.zst", true},
		{"a/b/app.sql.zst", true, "", "", false},
		{"a/b/app.sql.zst.age", false, "", "", false},
		{"a/b/app.txt.zst", false, "", "", false},
	}
	for _, c := range cases {
		p, s, err := StoredFormat(c.key, c.enc)
		if (err == nil) != c.ok || p != c.plain || s != c.ext {
			t.Errorf("StoredFormat(%q, %v) = %q %q %v", c.key, c.enc, p, s, err)
		}
		if err != nil && !errors.Is(err, ErrArtifactName) {
			t.Errorf("error type %v", err)
		}
	}
}
