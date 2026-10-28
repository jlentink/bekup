package connections

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestCleanupReason(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Second)
	tests := []struct {
		name   string
		st     cleanupState
		reason string
		ok     bool
	}{
		{"unexpired, no restore", cleanupState{expiresAt: &future}, "", false},
		{"expired", cleanupState{expiresAt: &past}, ReasonExpired, true},
		{"expires exactly now", cleanupState{expiresAt: &now}, ReasonExpired, true},
		{"no expiry (fail closed)", cleanupState{}, ReasonExpired, true},
		{"expired but a run holds a slot", cleanupState{expiresAt: &past, slotHeld: true}, "", false},
		{"restore still running", cleanupState{expiresAt: &future, restores: 1, activeRestores: 1, lastFinished: now.Add(-time.Hour)}, "", false},
		{"restore finished within the grace", cleanupState{expiresAt: &future, restores: 1, lastFinished: now.Add(-RestoreGrace + time.Second)}, "", false},
		{"restore finished after the grace", cleanupState{expiresAt: &future, restores: 2, lastFinished: now.Add(-RestoreGrace)}, ReasonRestoreFinished, true},
		{"retry still running", cleanupState{expiresAt: &future, restores: 2, activeRestores: 1, lastFinished: now.Add(-time.Hour)}, "", false},
		{"finished restore, slot held", cleanupState{expiresAt: &future, restores: 1, lastFinished: now.Add(-time.Hour), slotHeld: true}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := cleanupReason(tc.st, now)
			if r != tc.reason || ok != tc.ok {
				t.Fatalf("got %q %v, want %q %v", r, ok, tc.reason, tc.ok)
			}
		})
	}
}

func TestTemporaryName(t *testing.T) {
	if got := temporaryName(" db.example.com "); got != "Temporary: db.example.com" {
		t.Fatalf("name: %q", got)
	}
	long := temporaryName(strings.Repeat("é", 300))
	if n := utf8.RuneCountInString(long); n != maxNameRunes || !utf8.ValidString(long) {
		t.Fatalf("long name: %d runes, valid %v", n, utf8.ValidString(long))
	}
	if _, err := normalizeName(long); err != nil {
		t.Fatalf("default name rejected: %v", err)
	}
}

func TestStoredCredentialFields(t *testing.T) {
	cert := "pem"
	row := &sqlc.Connection{HasPassword: true, SshAuth: sqlc.ConnectionSshAuthPrivateKey, SshHasPassphrase: true, ClientCertPem: &cert}
	if got := strings.Join(storedCredentialFields(row), ","); got != "password,ssh_private_key,ssh_private_key_passphrase,client_key" {
		t.Fatalf("fields: %s", got)
	}
	row = &sqlc.Connection{SshAuth: sqlc.ConnectionSshAuthPassword}
	if got := strings.Join(storedCredentialFields(row), ","); got != "ssh_password" {
		t.Fatalf("fields: %s", got)
	}
	if got := storedCredentialFields(&sqlc.Connection{SshAuth: sqlc.ConnectionSshAuthNone}); len(got) != 0 {
		t.Fatalf("fields: %v", got)
	}
}

// nopDB satisfies DB; New never calls it.
type nopDB struct{ DB }

func TestTemporaryTTLOptions(t *testing.T) {
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	base := Options{DB: nopDB{}, Keyring: kr, Auditor: &audit.Auditor{}, Runner: &chainRunner{}}
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if s.o.TempTTL != DefaultTempTTL || s.o.TempMaxTTL != DefaultTempMaxTTL || s.TempMaxTTL() != DefaultTempMaxTTL {
		t.Fatalf("defaults: %v %v", s.o.TempTTL, s.o.TempMaxTTL)
	}
	o := base
	o.TempMaxTTL = time.Hour
	if s, err = New(o); err != nil || s.o.TempTTL != time.Hour {
		t.Fatalf("ttl capped by max: %v %v", err, s.o.TempTTL)
	}
	o.TempTTL = 2 * time.Hour
	if _, err := New(o); err == nil {
		t.Fatal("TempTTL above TempMaxTTL accepted")
	}
}
