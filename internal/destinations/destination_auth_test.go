package destinations

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
)

func testKeyring(t testing.TB) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	return kr
}

func TestCredentialsRoundTrip(t *testing.T) {
	for name, c := range map[string]Credentials{
		"s3":       {SecretAccessKey: []byte("s3-secret-value")},
		"password": {Password: []byte("pw")},
		"key":      {PrivateKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n"), Passphrase: []byte("pp")},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := unmarshalCredentials(c.marshal())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.SecretAccessKey, c.SecretAccessKey) || !bytes.Equal(got.Password, c.Password) ||
				!bytes.Equal(got.PrivateKey, c.PrivateKey) || !bytes.Equal(got.Passphrase, c.Passphrase) {
				t.Fatalf("round trip: got %+v want %+v", got, c)
			}
			got.Zero()
			if got.SecretAccessKey != nil || got.Password != nil || got.PrivateKey != nil || got.Passphrase != nil {
				t.Fatal("Zero left data")
			}
		})
	}
}

func TestCredentialsCorrupt(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":         nil,
		"version":       {2},
		"short header":  {1, 1, 0, 0},
		"length beyond": {1, 1, 0, 0, 0, 9, 'a'},
		"zero length":   {1, 1, 0, 0, 0, 0},
		"unknown tag":   {1, 9, 0, 0, 0, 1, 'a'},
		"duplicate":     {1, 2, 0, 0, 0, 1, 'a', 2, 0, 0, 0, 1, 'b'},
	} {
		if _, err := unmarshalCredentials(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzUnmarshalCredentials(f *testing.F) {
	f.Add((&Credentials{Password: []byte("x"), PrivateKey: []byte("y")}).marshal())
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := unmarshalCredentials(b)
		if err != nil {
			return
		}
		again, err := unmarshalCredentials(c.marshal())
		if err != nil {
			t.Fatalf("re-decode: %v", err)
		}
		if !bytes.Equal(again.Password, c.Password) || !bytes.Equal(again.PrivateKey, c.PrivateKey) {
			t.Fatal("unstable encoding")
		}
	})
}

// The sealed credentials are bound to their row: opening them under the
// AAD of another destination fails.
func TestSealedCredentialsAAD(t *testing.T) {
	kr := testKeyring(t)
	id, other := uuid.New(), uuid.New()
	sealed, err := sealCredentials(kr, id, &Credentials{SecretAccessKey: []byte("very-secret-key")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("very-secret-key")) {
		t.Fatal("plaintext in ciphertext")
	}
	got, err := openCredentials(kr, id, sealed)
	if err != nil || string(got.SecretAccessKey) != "very-secret-key" {
		t.Fatalf("open: %v %q", err, got.SecretAccessKey)
	}
	if _, err := openCredentials(kr, other, sealed); err == nil {
		t.Fatal("opened under another row's AAD")
	}
	wrong, err := kr.Open(sealed, crypto.AAD{Table: "destinations", RowID: id.String(), Field: "password"})
	if err == nil {
		t.Fatalf("opened under another field's AAD: %q", wrong)
	}
	if want := (crypto.AAD{Table: "destinations", RowID: id.String(), Field: "secret"}); credentialsAAD(id) != want {
		t.Fatalf("AAD %v", credentialsAAD(id))
	}
	if s, err := sealCredentials(kr, id, &Credentials{}); err != nil || s != nil {
		t.Fatalf("empty credentials: %v %v", s, err)
	}
	if c, err := openCredentials(kr, id, nil); err != nil || !c.empty() {
		t.Fatalf("no sealed blob: %v %+v", err, c)
	}
}
