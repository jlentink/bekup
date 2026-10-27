package connections

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

func credsEqual(a, b *Credentials) bool {
	af, bf := a.fields(), b.fields()
	for i := range af {
		if !bytes.Equal(*af[i], *bf[i]) {
			return false
		}
	}
	return true
}

func TestCredentialsRoundTrip(t *testing.T) {
	for name, c := range map[string]Credentials{
		"password": {Password: []byte("db-password")},
		"ssh password": {
			Password: []byte("db"), SSHPassword: []byte("ssh-pw"),
		},
		"ssh key": {
			SSHPrivateKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n"), SSHPassphrase: []byte("pp"),
			ClientKey: []byte("-----BEGIN PRIVATE KEY-----\nx\n"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := unmarshalCredentials(c.marshal())
			if err != nil {
				t.Fatal(err)
			}
			if !credsEqual(got, &c) {
				t.Fatalf("round trip: got %+v want %+v", got, c)
			}
			got.Zero()
			if !got.empty() {
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
	f.Add((&Credentials{Password: []byte("x"), SSHPrivateKey: []byte("k")}).marshal())
	f.Add([]byte{1, 5, 0, 0, 0, 1, 'z'})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := unmarshalCredentials(b)
		if err != nil {
			return
		}
		again, err := unmarshalCredentials(c.marshal())
		if err != nil || !credsEqual(c, again) {
			t.Fatalf("re-encoding changed the credentials: %v", err)
		}
	})
}

func TestSealBindsRow(t *testing.T) {
	kr := testKeyring(t)
	id, other := uuid.New(), uuid.New()
	sealed, err := sealCredentials(kr, id, &Credentials{Password: []byte("pw-value")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("pw-value")) {
		t.Fatal("sealed blob contains the plaintext")
	}
	got, err := openCredentials(kr, id, sealed)
	if err != nil || string(got.Password) != "pw-value" {
		t.Fatalf("open: %v %q", err, got.Password)
	}
	// The AAD binds the blob to connections:<id>:secret: another row (or
	// another table using the same id) cannot open it.
	if _, err := openCredentials(kr, other, sealed); err == nil {
		t.Fatal("opened under another row id")
	}
	if _, err := kr.Open(sealed, crypto.AAD{Table: "destinations", RowID: id.String(), Field: "secret"}); err == nil {
		t.Fatal("opened under another table")
	}
	if s, err := sealCredentials(kr, id, &Credentials{}); err != nil || s != nil {
		t.Fatalf("empty credentials: %v %v", s, err)
	}
	if c, err := openCredentials(kr, id, nil); err != nil || !c.empty() {
		t.Fatalf("open empty: %v", err)
	}
}
