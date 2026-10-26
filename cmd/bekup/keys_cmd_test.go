package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/crypto"
)

func TestKeysGenerateAndRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	out, _, err := run(t, "keys", "generate", "--out", path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	k, err := crypto.LoadKeyringFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first := k.PrimaryID().String()
	k.Destroy()
	for _, want := range []string{"ESCROW", "OFFLINE", "unrecoverable", "primary key ID: " + first} {
		if !strings.Contains(out, want) {
			t.Fatalf("guidance lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: test file in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	// The key material is never printed.
	for line := range strings.SplitSeq(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 4 && f[0] == "key" && strings.Contains(out, f[3]) {
			t.Fatal("key material printed")
		}
	}

	// Refuses to overwrite.
	if _, _, err := run(t, "keys", "generate", "--out", path); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("overwrite: %v", err)
	}
	if _, _, err := run(t, "keys", "generate"); err == nil {
		t.Fatal("missing --out accepted")
	}

	out, _, err = run(t, "keys", "rotate", "--keyring", path)
	if err != nil {
		t.Fatal(err)
	}
	k, err = crypto.LoadKeyringFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Destroy()
	if len(k.IDs()) != 2 || k.PrimaryID().String() == first || !k.Has(k.IDs()[0]) || k.IDs()[0].String() != first {
		t.Fatalf("rotate result: %v", k)
	}
	if !strings.Contains(out, "new primary key ID: "+k.PrimaryID().String()) || !strings.Contains(out, "escrow") {
		t.Fatal(out)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode after rotate: %v", err)
	}

	// Unsafe permissions are refused.
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // G302: deliberately unsafe mode under test
		t.Fatal(err)
	}
	if _, _, err := run(t, "keys", "rotate", "--keyring", path); err == nil || !strings.Contains(err.Error(), "world-readable") {
		t.Fatalf("unsafe file: %v", err)
	}
	if _, _, err := run(t, "keys", "rotate"); err == nil {
		t.Fatal("missing --keyring accepted")
	}
}
