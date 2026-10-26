package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

const fuzzCiphertextHex = "010a0b0c0d51aa747a76d45e80db6f29f19326671abce83a7fad76c439758e8ac6768ad45f40c76f57"

func FuzzOpen(f *testing.F) {
	k, err := ParseKeyring([]byte(keyringHeader + "\nkey 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\n"))
	if err != nil {
		f.Fatal(err)
	}
	aad := AAD{Table: "connections", RowID: "1", Field: "secret_enc"}
	// A fixed ciphertext (Seal output under testKeyA): every fuzz worker
	// process must agree on the single input allowed to authenticate.
	ct, err := hex.DecodeString(fuzzCiphertextHex)
	if err != nil {
		f.Fatal(err)
	}
	if pt, err := k.Open(ct, aad); err != nil || string(pt) != "password" {
		f.Fatalf("fixed ciphertext: %v", err)
	}
	f.Add(ct)
	f.Add([]byte{})
	f.Add([]byte{1, 0x0a, 0x0b, 0x0c, 0x0d})
	f.Add(bytes.Repeat([]byte{1}, Overhead))
	f.Fuzz(func(t *testing.T, data []byte) {
		pt, err := k.Open(data, aad)
		if err == nil && !bytes.Equal(data, ct) {
			// Only the genuine ciphertext may authenticate.
			t.Fatalf("forged ciphertext accepted: %x -> %q", data, pt)
		}
	})
}

func FuzzParseKeyring(f *testing.F) {
	f.Add([]byte(keyringHeader + "\nkey 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nprimary 0a0b0c0d\n"))
	f.Add([]byte(keyringHeader + "\nkey 0a0b0c0d 2026-10-26T12:00:00Z " + testKeyA + "\nkey 01020304 2026-10-26T12:00:00Z " + testKeyB + "\nprimary 01020304\n"))
	f.Add([]byte("# c\n" + keyringHeader + "\nprimary 0a0b0c0d\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		k, err := ParseKeyring(data)
		if err != nil {
			return
		}
		// Anything accepted must round-trip and be usable.
		out, err := k.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		k2, err := ParseKeyring(out)
		if err != nil {
			t.Fatalf("re-parse: %v", err)
		}
		if k2.KeyCheckValue() != k.KeyCheckValue() || len(k2.IDs()) != len(k.IDs()) {
			t.Fatal("round trip changed the keyring")
		}
		aad := AAD{Table: "t", RowID: "1", Field: "f"}
		ct, err := k.Seal([]byte("x"), aad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := k2.Open(ct, aad); err != nil {
			t.Fatal(err)
		}
	})
}
