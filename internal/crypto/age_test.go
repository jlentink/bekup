package crypto

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// lowScrypt lets tests use cheap work factors.
func lowScrypt(t *testing.T) {
	t.Helper()
	prev := minScryptWorkFactor
	minScryptWorkFactor = 10
	t.Cleanup(func() { minScryptWorkFactor = prev })
}

func encryptPass(t *testing.T, pass string, wf int, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := EncryptWithPassphrase(&buf, []byte(pass), wf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAgePassphraseRoundTrip(t *testing.T) {
	lowScrypt(t)
	ct := encryptPass(t, "correct horse battery staple", 10, "backup")
	r, err := DecryptWithPassphrase(bytes.NewReader(ct), []byte("correct horse battery staple"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := must(io.ReadAll(r)); string(got) != "backup" {
		t.Fatalf("got %q", got)
	}
	if _, err := DecryptWithPassphrase(bytes.NewReader(ct), []byte("wrong"), 10); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 1
	if r, err := DecryptWithPassphrase(bytes.NewReader(tampered), []byte("correct horse battery staple"), 10); err == nil {
		if _, err := io.ReadAll(r); err == nil {
			t.Fatal("tampered stream accepted")
		}
	}
}

func TestAgeScryptWorkFactorBounds(t *testing.T) {
	lowScrypt(t)
	// A header asking for more than the bound is rejected quickly, before
	// scrypt runs (2^12 is cheap anyway; the point is the rejection).
	ct := encryptPass(t, "pw", 12, "x")
	start := time.Now()
	_, err := DecryptWithPassphrase(bytes.NewReader(ct), []byte("pw"), 11)
	if err == nil || !strings.Contains(err.Error(), "work factor") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("bounded decrypt took too long")
	}

	// A forged header with the maximum age allows (2^30 = 128 GiB scrypt)
	// must be rejected by the default bound without running scrypt.
	forged := bytes.Replace(ct, []byte(" 12\n"), []byte(" 30\n"), 1)
	if bytes.Equal(forged, ct) {
		t.Fatal("could not forge header")
	}
	start = time.Now()
	if _, err := DecryptWithPassphrase(bytes.NewReader(forged), []byte("pw"), 0); err == nil {
		t.Fatal("forged work factor accepted")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("forged header was not rejected early")
	}

	for _, wf := range []int{-1, 9, MaxScryptWorkFactor + 1} {
		if _, err := EncryptWithPassphrase(io.Discard, []byte("pw"), wf); err == nil {
			t.Errorf("encrypt work factor %d accepted", wf)
		}
	}
	for _, wf := range []int{-1, MaxScryptWorkFactor + 1, 30} {
		if _, err := DecryptWithPassphrase(bytes.NewReader(ct), []byte("pw"), wf); err == nil {
			t.Errorf("decrypt max work factor %d accepted", wf)
		}
	}
	minScryptWorkFactor = DefaultScryptWorkFactor
	if _, err := EncryptWithPassphrase(io.Discard, []byte("pw"), 12); err == nil {
		t.Error("production minimum not enforced")
	}
	if _, err := EncryptWithPassphrase(io.Discard, nil, 0); err == nil {
		t.Error("empty passphrase accepted")
	}
	if _, err := DecryptWithPassphrase(bytes.NewReader(ct), nil, 0); err == nil {
		t.Error("empty passphrase accepted for decrypt")
	}
}

func TestAgeRecipients(t *testing.T) {
	a := must(GenerateAgeKeypair())
	b := must(GenerateAgeKeypair())
	c := must(GenerateAgeKeypair())
	var buf bytes.Buffer
	w, err := EncryptToRecipients(&buf, a.Recipient, b.Recipient)
	if err != nil {
		t.Fatal(err)
	}
	must(io.WriteString(w, "payload"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ct := buf.Bytes()
	for _, kp := range []*AgeKeypair{a, b} {
		r, err := DecryptWithIdentities(bytes.NewReader(ct), kp.Identity.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if got := must(io.ReadAll(r)); string(got) != "payload" {
			t.Fatal(string(got))
		}
	}
	var noMatch *age.NoIdentityMatchError
	if _, err := DecryptWithIdentities(bytes.NewReader(ct), c.Identity.Bytes()); !errors.As(err, &noMatch) {
		t.Fatalf("wrong identity: %v", err)
	}
	if _, err := EncryptToRecipients(io.Discard); err == nil {
		t.Fatal("no recipients accepted")
	}
	if _, err := EncryptToRecipients(io.Discard, "age1bogus"); err == nil {
		t.Fatal("bad recipient accepted")
	}
	for _, ids := range []string{"", "# only a comment\n", "AGE-SECRET-KEY-1BOGUS\n", "pw\n"} {
		_, err := DecryptWithIdentities(bytes.NewReader(ct), []byte(ids))
		if !errors.Is(err, ErrAgeIdentity) {
			t.Fatalf("%q: %v", ids, err)
		}
		if ids != "" && strings.Contains(err.Error(), strings.TrimSpace(ids)) && !strings.HasPrefix(ids, "#") {
			t.Fatalf("error echoes identity: %v", err)
		}
	}
}
