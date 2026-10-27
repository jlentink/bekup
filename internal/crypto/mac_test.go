package crypto

import (
	"bytes"
	"crypto/hmac"
	"errors"
	"testing"
	"time"
)

func TestKeyringMAC(t *testing.T) {
	k := newTestKeyring(t)
	id, sum, err := k.MAC("recovery-code", []byte("msg"))
	if err != nil {
		t.Fatal(err)
	}
	if id != k.PrimaryID() || len(sum) != 32 {
		t.Fatalf("id %s len %d", id, len(sum))
	}
	again, err := k.MACWith(id, "recovery-code", []byte("msg"))
	if err != nil || !hmac.Equal(sum, again) {
		t.Fatal("MAC not deterministic")
	}
	other := must(k.MACWith(id, "other-purpose", []byte("msg")))
	if hmac.Equal(sum, other) {
		t.Fatal("purposes share a subkey")
	}
	diff := must(k.MACWith(id, "recovery-code", []byte("msh")))
	if bytes.Equal(sum, diff) {
		t.Fatal("different messages, same MAC")
	}

	// After a rotation the old key still verifies; new MACs use the new key.
	if _, err := k.AddPrimary(testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	old, err := k.MACWith(id, "recovery-code", []byte("msg"))
	if err != nil || !hmac.Equal(sum, old) {
		t.Fatal("old key no longer verifies")
	}
	id2, sum2, err := k.MAC("recovery-code", []byte("msg"))
	if err != nil || id2 == id || hmac.Equal(sum, sum2) {
		t.Fatal("rotation did not change the MAC key")
	}

	for _, p := range []string{"", "Upper", "with space", "-lead"} {
		if _, _, err := k.MAC(p, nil); !errors.Is(err, ErrInvalidMACPurpose) {
			t.Errorf("purpose %q: %v", p, err)
		}
	}
	if _, err := k.MACWith(KeyID{9, 9, 9, 9}, "recovery-code", nil); !errors.Is(err, ErrUnknownKeyID) {
		t.Errorf("unknown key: %v", err)
	}
	other2 := newTestKeyring(t)
	if _, err := other2.AddPrimary(testNow); err != nil {
		t.Fatal(err)
	}
	_, sum3, err := other2.MAC("recovery-code", []byte("msg"))
	if err != nil {
		t.Fatal(err)
	}
	if hmac.Equal(sum, sum3) {
		t.Fatal("different keys, same MAC")
	}
}
