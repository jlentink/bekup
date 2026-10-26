package crypto

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

func TestSigningKeys(t *testing.T) {
	kr := newTestKeyring(t)
	sk, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(sk.ID) != 16 || sk.ID != SigningKeyID(sk.Public) {
		t.Fatalf("id %q", sk.ID)
	}
	if fp := FingerprintHex(sk.Public); len(fp) != 64 || !strings.HasPrefix(fp, sk.ID) {
		t.Fatalf("hex fingerprint %q", fp)
	}
	if fp := FingerprintBase64(sk.Public); !strings.HasPrefix(fp, "SHA256:") || len(fp) != 7+43 {
		t.Fatalf("base64 fingerprint %q", fp)
	}

	msg := []byte(`{"artifacts":[]}`)
	sig, err := sk.Sign("manifest", msg)
	if err != nil {
		t.Fatal(err)
	}

	v := NewVerifier()
	if _, err := v.Add(sk.Public); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(sk.ID, "manifest", msg, sig); err != nil {
		t.Fatal(err)
	}
	// Domain separation: the same signature is not valid for another purpose.
	if err := v.Verify(sk.ID, "audit-head", msg, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("cross-purpose: %v", err)
	}
	// A raw ed25519 signature over msg is not accepted.
	if err := v.Verify(sk.ID, "manifest", msg, ed25519.Sign(sk.priv, msg)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("raw signature: %v", err)
	}
	if err := v.Verify(sk.ID, "manifest", append(bytes.Clone(msg), ' '), sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered message: %v", err)
	}
	badSig := bytes.Clone(sig)
	badSig[0] ^= 1
	if err := v.Verify(sk.ID, "manifest", msg, badSig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered sig: %v", err)
	}
	if err := v.Verify(sk.ID, "manifest", msg, sig[:10]); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("short sig: %v", err)
	}
	if err := v.Verify("0000000000000000", "manifest", msg, sig); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := sk.Sign("Bad Purpose", msg); !errors.Is(err, ErrInvalidPurpose) {
		t.Fatalf("purpose: %v", err)
	}

	// Rotation of instance keys: old public keys keep verifying.
	sk2 := must(GenerateSigningKey())
	if _, err := v.Add(sk2.Public); err != nil {
		t.Fatal(err)
	}
	sig2 := must(sk2.Sign("manifest", msg))
	if v.Len() != 2 || v.Verify(sk.ID, "manifest", msg, sig) != nil || v.Verify(sk2.ID, "manifest", msg, sig2) != nil {
		t.Fatal("multi-key verification failed")
	}
	if err := v.Verify(sk.ID, "manifest", msg, sig2); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := v.Add(sk.Public[:31]); err == nil {
		t.Fatal("short public key accepted")
	}

	// Sealed private key round trip.
	sealed, err := sk.SealPrivate(kr)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSigningKey(kr, sk.ID, sk.Public, sealed)
	if err != nil {
		t.Fatal(err)
	}
	sig3 := must(opened.Sign("manifest", msg))
	if err := v.Verify(sk.ID, "manifest", msg, sig3); err != nil {
		t.Fatal(err)
	}
	// Bound to its own row: cannot be opened as another key.
	if _, err := OpenSigningKey(kr, sk2.ID, sk2.Public, sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("swapped id: %v", err)
	}
	if _, err := OpenSigningKey(kr, sk.ID, sk2.Public, sealed); !errors.Is(err, ErrSigningKey) {
		t.Fatalf("mismatched public key: %v", err)
	}
	if _, err := OpenSigningKey(newTestKeyring(t), sk.ID, sk.Public, sealed); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("wrong keyring: %v", err)
	}
	// A sealed seed of a different key under the right AAD is detected.
	other := must(kr.Seal(sk2.priv.Seed(), SigningKeyAAD(sk.ID)))
	if _, err := OpenSigningKey(kr, sk.ID, sk.Public, other); !errors.Is(err, ErrSigningKey) {
		t.Fatalf("substituted seed: %v", err)
	}

	if s := sk.String() + sk.GoString(); strings.Contains(s, string(sk.priv.Seed())) || !strings.Contains(s, sk.ID) {
		t.Fatal(s)
	}
	opened.Destroy()
	if _, err := opened.Sign("manifest", msg); !errors.Is(err, ErrSigningKey) {
		t.Fatal("destroyed key signed")
	}
	if _, err := opened.SealPrivate(kr); !errors.Is(err, ErrSigningKey) {
		t.Fatal("destroyed key sealed")
	}
}
