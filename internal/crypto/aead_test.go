package crypto

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	k, err := GenerateKeyring(testNow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Destroy)
	return k
}

// must returns v or fails the test by panicking on err (usable inline with
// multi-value calls).
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func mustAAD(t *testing.T, table, row, field string) AAD {
	t.Helper()
	a, err := NewAAD(table, row, field)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := newTestKeyring(t)
	aad := mustAAD(t, "connections", "0192f0c4-7b1e-7c3a-9d2e-1f2a3b4c5d6e", "secret_enc")
	for _, pt := range [][]byte{{}, []byte("x"), []byte("hunter2-db-password"), bytes.Repeat([]byte{0xab}, 4096)} {
		ct, err := k.Seal(pt, aad)
		if err != nil {
			t.Fatal(err)
		}
		if len(ct) != len(pt)+Overhead {
			t.Fatalf("len %d, want %d", len(ct), len(pt)+Overhead)
		}
		if id := must(KeyIDOf(ct)); id != k.PrimaryID() {
			t.Fatalf("key id %s, want %s", id, k.PrimaryID())
		}
		got, err := k.Open(ct, aad)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatal("round trip mismatch")
		}
	}
}

func TestSealNonceUnique(t *testing.T) {
	k := newTestKeyring(t)
	aad := mustAAD(t, "t", "1", "f")
	a := must(k.Seal([]byte("same"), aad))
	b := must(k.Seal([]byte("same"), aad))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical")
	}
}

func TestOpenTamperEveryByte(t *testing.T) {
	k := newTestKeyring(t)
	aad := mustAAD(t, "destinations", "42", "secret_enc")
	ct, err := k.Seal([]byte("s3-secret-access-key"), aad)
	if err != nil {
		t.Fatal(err)
	}
	// Flip each bit of each section: version, key id, nonce, body, tag.
	for i := range ct {
		for bit := range 8 {
			bad := bytes.Clone(ct)
			bad[i] ^= 1 << bit
			if _, err := k.Open(bad, aad); err == nil {
				t.Fatalf("tampered byte %d bit %d accepted", i, bit)
			}
		}
	}
	// Truncation and extension.
	for n := range len(ct) {
		if _, err := k.Open(ct[:n], aad); err == nil {
			t.Fatalf("truncated to %d accepted", n)
		}
	}
	if _, err := k.Open(append(bytes.Clone(ct), 0), aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("extended: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	k := newTestKeyring(t)
	other := newTestKeyring(t)
	aad := mustAAD(t, "connections", "7", "secret_enc")
	ct := must(k.Seal([]byte("pw"), aad))

	t.Run("wrong key same id", func(t *testing.T) {
		// A keyring with the same key ID but different material.
		forged := newTestKeyring(t)
		mk := forged.keys[forged.primary]
		delete(forged.keys, forged.primary)
		mk.id = k.primary
		forged.keys[k.primary] = mk
		forged.primary = k.primary
		if _, err := forged.Open(ct, aad); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unknown key id", func(t *testing.T) {
		if _, err := other.Open(ct, aad); !errors.Is(err, ErrUnknownKeyID) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unsupported version", func(t *testing.T) {
		bad := bytes.Clone(ct)
		bad[0] = 2
		if _, err := k.Open(bad, aad); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("too short", func(t *testing.T) {
		if _, err := k.Open(ct[:Overhead-1], aad); !errors.Is(err, ErrMalformedCiphertext) {
			t.Fatalf("got %v", err)
		}
	})
	for name, swapped := range map[string]AAD{
		"other row":   {Table: "connections", RowID: "8", Field: "secret_enc"},
		"other table": {Table: "destinations", RowID: "7", Field: "secret_enc"},
		"other field": {Table: "connections", RowID: "7", Field: "ssh_key_enc"},
	} {
		t.Run("swapped AAD "+name, func(t *testing.T) {
			if _, err := k.Open(ct, swapped); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestAADValidation(t *testing.T) {
	bad := []AAD{
		{},
		{Table: "a:b", RowID: "1", Field: "f"},
		{Table: "t", RowID: "1:2", Field: "f"},
		{Table: "t", RowID: "1", Field: "f:g"},
		{Table: "T", RowID: "1", Field: "f"},
		{Table: "t", RowID: "", Field: "f"},
		{Table: "t", RowID: "-1", Field: "f"},
		{Table: "t", RowID: "1", Field: ""},
	}
	k := newTestKeyring(t)
	for _, a := range bad {
		if _, err := k.Seal([]byte("x"), a); !errors.Is(err, ErrInvalidAAD) {
			t.Errorf("%+v: got %v", a, err)
		}
		if _, err := k.Open(make([]byte, 64), a); !errors.Is(err, ErrInvalidAAD) {
			t.Errorf("%+v: open got %v", a, err)
		}
	}
	if s := mustAAD(t, "instance_keys", "abc", "private").String(); s != "instance_keys:abc:private" {
		t.Fatal(s)
	}
	if _, err := k.Seal(make([]byte, MaxPlaintextSize+1), mustAAD(t, "t", "1", "f")); err == nil {
		t.Fatal("oversized plaintext accepted")
	}
}

func TestRotation(t *testing.T) {
	k := newTestKeyring(t)
	oldID := k.PrimaryID()
	aad := mustAAD(t, "settings", "smtp", "secret_enc")
	oldCT := must(k.Seal([]byte("smtp-password"), aad))

	newID, err := k.AddPrimary(testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if newID == oldID || k.PrimaryID() != newID || len(k.IDs()) != 2 {
		t.Fatal("AddPrimary did not switch the primary")
	}
	// Old ciphertext still decrypts.
	if pt, err := k.Open(oldCT, aad); err != nil || string(pt) != "smtp-password" {
		t.Fatalf("old ciphertext: %q %v", pt, err)
	}
	if need := must(k.NeedsRotation(oldCT)); !need {
		t.Fatal("old ciphertext should need rotation")
	}
	newCT, rotated, err := k.Rotate(oldCT, aad)
	if err != nil || !rotated {
		t.Fatalf("rotate: %v %v", rotated, err)
	}
	if id := must(KeyIDOf(newCT)); id != newID {
		t.Fatalf("rotated ciphertext uses %s, want %s", id, newID)
	}
	if pt, err := k.Open(newCT, aad); err != nil || string(pt) != "smtp-password" {
		t.Fatalf("rotated ciphertext: %q %v", pt, err)
	}
	again, rotated, err := k.Rotate(newCT, aad)
	if err != nil || rotated || !bytes.Equal(again, newCT) {
		t.Fatal("rotating a current ciphertext must be a no-op")
	}
	if _, _, err := k.Rotate(newCT, mustAAD(t, "settings", "smtp", "other")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("rotate with wrong AAD: %v", err)
	}

	// RotateAll.
	k2 := must(k.AddPrimary(testNow.Add(2 * time.Hour)))
	fields := make([]SealedField, 0, 3)
	fields = append(fields, SealedField{AAD: aad, Ciphertext: oldCT}, SealedField{AAD: aad, Ciphertext: newCT})
	n, err := k.RotateAll(fields)
	if err != nil || n != 2 {
		t.Fatalf("RotateAll: %d %v", n, err)
	}
	for _, f := range fields {
		if id := must(KeyIDOf(f.Ciphertext)); id != k2 {
			t.Fatal("RotateAll left an old key")
		}
	}
	fields = append(fields, SealedField{AAD: aad, Ciphertext: []byte("garbage")})
	if _, err := k.RotateAll(fields); err == nil {
		t.Fatal("RotateAll accepted garbage")
	}
}

func TestKeyCheckValue(t *testing.T) {
	k := newTestKeyring(t)
	kcv := k.KeyCheckValue()
	if err := k.VerifyKeyCheckValue(kcv); err != nil {
		t.Fatal(err)
	}
	if kcv != k.KeyCheckValue() {
		t.Fatal("KCV not deterministic")
	}

	// After rotation the old KCV still verifies (old key retained).
	if _, err := k.AddPrimary(testNow); err != nil {
		t.Fatal(err)
	}
	if err := k.VerifyKeyCheckValue(kcv); err != nil {
		t.Fatal(err)
	}
	if k.KeyCheckValue() == kcv {
		t.Fatal("new primary has the same KCV")
	}

	// Same key ID, different material: mismatch.
	forged := newTestKeyring(t)
	mk := forged.keys[forged.primary]
	delete(forged.keys, forged.primary)
	id := must(ParseKeyID(kcv[5:13]))
	mk.id = id
	forged.keys[id] = mk
	forged.primary = id
	if err := forged.VerifyKeyCheckValue(kcv); !errors.Is(err, ErrKCVMismatch) {
		t.Fatalf("got %v", err)
	}
	// Missing key.
	if err := newTestKeyring(t).VerifyKeyCheckValue(kcv); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("got %v", err)
	}
	for _, bad := range []string{"", "kcv1", "kcv2:" + kcv[5:], kcv + "A", kcv[:len(kcv)-1], "kcv1:ZZZZZZZZ:" + kcv[14:], kcv + ":x"} {
		if err := k.VerifyKeyCheckValue(bad); !errors.Is(err, ErrMalformedKCV) {
			t.Errorf("%q: got %v", bad, err)
		}
	}
	// Flipping a character of the MAC is a mismatch, not a pass.
	b := []byte(kcv)
	if b[len(b)-2] == 'A' {
		b[len(b)-2] = 'B'
	} else {
		b[len(b)-2] = 'A'
	}
	if err := k.VerifyKeyCheckValue(string(b)); err == nil {
		t.Fatal("modified KCV accepted")
	}
}
