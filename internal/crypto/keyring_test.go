package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

const (
	testKeyA = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=" // bytes 0..31
	testKeyB = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=" // bytes 32..63
)

func TestKeyringMarshalParseRoundTrip(t *testing.T) {
	k := newTestKeyring(t)
	if _, err := k.AddPrimary(testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	data, err := k.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := ParseKeyring(data)
	if err != nil {
		t.Fatal(err)
	}
	if k2.PrimaryID() != k.PrimaryID() || len(k2.IDs()) != 2 || k2.IDs()[0] != k.IDs()[0] {
		t.Fatal("keyring changed in round trip")
	}
	if c, ok := k2.Created(k.IDs()[0]); !ok || !c.Equal(testNow) {
		t.Fatalf("created %v", c)
	}
	aad := mustAAD(t, "t", "1", "f")
	ct := must(k.Seal([]byte("v"), aad))
	if pt, err := k2.Open(ct, aad); err != nil || string(pt) != "v" {
		t.Fatal("parsed keyring cannot open")
	}
	if k.KeyCheckValue() != k2.KeyCheckValue() {
		t.Fatal("KCV differs after round trip")
	}
}

func TestParseKeyring(t *testing.T) {
	hdr := keyringHeader + "\n"
	good := "# comment\n\n" + hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\r\n" +
		"key 01020304 2026-10-27T12:00:00Z " + testKeyB + "\nprimary 01020304\n"
	k, err := ParseKeyring([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if k.PrimaryID().String() != "01020304" || len(k.IDs()) != 2 {
		t.Fatal(k)
	}

	short := base64.StdEncoding.EncodeToString(make([]byte, 31))
	cases := map[string]string{
		"empty":             "",
		"no header":         "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\n",
		"wrong header":      "bekup-keyring v2\n",
		"no keys":           hdr + "primary 0a0b0c0d\n",
		"no primary":        hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\n",
		"two primaries":     hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\nprimary 0a0b0c0d\n",
		"dangling primary":  hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 01020304\n",
		"duplicate id":      hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nkey 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyB + "\nprimary 0a0b0c0d\n",
		"duplicate key":     hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nkey 01020304 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\n",
		"short key":         hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + short + "\nprimary 0a0b0c0d\n",
		"bad base64":        hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + strings.Repeat("!", 44) + "\nprimary 0a0b0c0d\n",
		"urlsafe base64":    hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + "____" + testKeyA[4:] + "\nprimary 0a0b0c0d\n",
		"bad id":            hdr + "key 0A0B0C0D 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0A0B0C0D\n",
		"short id":          hdr + "key 0a0b 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b\n",
		"bad time":          hdr + "key 0a0b0c0d yesterday " + testKeyA + "\nprimary 0a0b0c0d\n",
		"missing field":     hdr + "key 0a0b0c0d " + testKeyA + "\nprimary 0a0b0c0d\n",
		"extra field":       hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + " x\nprimary 0a0b0c0d\n",
		"unknown directive": hdr + "key 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\nretired 0a0b0c0d\n",
		"long line":         hdr + strings.Repeat("x", 5000) + "\n",
		"raw key file":      testKeyA + "\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseKeyring([]byte(in))
			if !errors.Is(err, ErrKeyringFormat) {
				t.Fatalf("got %v", err)
			}
			// Errors never echo key material.
			for _, secret := range []string{testKeyA, testKeyB, testKeyA[:16]} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaks key material: %v", err)
				}
			}
		})
	}
}

func TestKeyringStringRedacts(t *testing.T) {
	k, err := ParseKeyring([]byte(keyringHeader + "\nkey 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{k.String(), k.GoString()} {
		if strings.Contains(s, testKeyA[:10]) || !strings.Contains(s, "0a0b0c0d") {
			t.Fatal(s)
		}
	}
}

func TestKeyringDestroy(t *testing.T) {
	k := newTestKeyring(t)
	mk := k.keys[k.primary]
	raw := mk.raw
	k.Destroy()
	if !bytes.Equal(raw, make([]byte, KeySize)) {
		t.Fatal("Destroy did not zero the key")
	}
	if _, err := k.Seal([]byte("x"), AAD{Table: "t", RowID: "1", Field: "f"}); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("destroyed keyring sealed: %v", err)
	}
}

func TestKeyringFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	k := newTestKeyring(t)

	if err := WriteKeyringFile(path, k, false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	// Refuses to overwrite.
	if err := WriteKeyringFile(path, k, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("overwrite: %v", err)
	}
	// No temporary files left behind.
	entries := must(os.ReadDir(dir))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}

	loaded, err := LoadKeyringFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.KeyCheckValue() != k.KeyCheckValue() {
		t.Fatal("loaded keyring differs")
	}

	// Rotation writes over the file atomically.
	if _, err := loaded.AddPrimary(testNow); err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyringFile(path, loaded, true); err != nil {
		t.Fatal(err)
	}
	again, err := LoadKeyringFile(path)
	if err != nil || len(again.IDs()) != 2 {
		t.Fatalf("after rotate: %v", err)
	}

	// Symlinked key file: rotation replaces the target, keeps the link.
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyringFile(link, again, true); err != nil {
		t.Fatal(err)
	}
	if li := must(os.Lstat(link)); li.Mode()&fs.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}

	// Permission checks come from config.ReadSecretFile.
	for _, mode := range []os.FileMode{0o644, 0o660, 0o604} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKeyringFile(path); !errors.Is(err, config.ErrUnsafeFile) {
			t.Errorf("mode %04o: got %v", mode, err)
		}
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(path); err != nil {
		t.Fatalf("0400: %v", err)
	}

	// Parse errors include the path but not the content.
	bad := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(bad, []byte(testKeyA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyringFile(bad); !errors.Is(err, ErrKeyringFormat) || strings.Contains(err.Error(), testKeyA) {
		t.Fatalf("got %v", err)
	}
	if _, err := LoadKeyringFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err := WriteKeyringFile(filepath.Join(dir, "nodir", "k"), k, false); err == nil {
		t.Fatal("write into missing dir succeeded")
	}
}
