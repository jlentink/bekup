package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

func TestCheckPutOptions(t *testing.T) {
	plain := storage.Capabilities{Type: "disk"}
	locking := storage.Capabilities{Type: "s3", SupportsObjectLock: true, MaxObjectSize: 10}
	sum := sha256.Sum256(nil)
	later := time.Now().Add(time.Hour)
	tests := []struct {
		name string
		caps storage.Capabilities
		opts storage.PutOptions
		want error
	}{
		{"empty", plain, storage.PutOptions{}, nil},
		{"sha", plain, storage.PutOptions{ExpectedSHA256: sum[:]}, nil},
		{"short sha", plain, storage.PutOptions{ExpectedSHA256: sum[:5]}, storage.ErrInvalidArgument},
		{"negative size", plain, storage.PutOptions{SizeHint: -1}, storage.ErrInvalidArgument},
		{"too large", locking, storage.PutOptions{SizeHint: 11}, storage.ErrUnsupported},
		{"bad mode", locking, storage.PutOptions{LockMode: "x", RetainUntil: later}, storage.ErrInvalidArgument},
		{"mode without date", locking, storage.PutOptions{LockMode: storage.LockGovernance}, storage.ErrInvalidArgument},
		{"date without mode", locking, storage.PutOptions{RetainUntil: later}, storage.ErrInvalidArgument},
		{"lock on plain", plain, storage.PutOptions{LockMode: storage.LockCompliance, RetainUntil: later}, storage.ErrUnsupported},
		{"hold on plain", plain, storage.PutOptions{LegalHold: true}, storage.ErrUnsupported},
		{"lock ok", locking, storage.PutOptions{LockMode: storage.LockCompliance, RetainUntil: later, LegalHold: true}, nil},
	}
	for _, tc := range tests {
		err := storage.CheckPutOptions(tc.caps, tc.opts)
		if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestCopyHashed(t *testing.T) {
	data := bytes.Repeat([]byte("abc"), 1<<20)
	var buf bytes.Buffer
	n, sum, err := storage.CopyHashed(t.Context(), &buf, bytes.NewReader(data), 0)
	want := sha256.Sum256(data)
	if err != nil || n != int64(len(data)) || !bytes.Equal(sum, want[:]) || !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("copy: n=%d err=%v", n, err)
	}
	if _, _, err := storage.CopyHashed(t.Context(), &buf, bytes.NewReader(data), 100); !errors.Is(err, storage.ErrUnsupported) {
		t.Errorf("limit: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := storage.CopyHashed(ctx, &buf, bytes.NewReader(data), 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
	if err := storage.VerifySHA256(nil, sum); err != nil {
		t.Error(err)
	}
	if err := storage.VerifySHA256(want[:], make([]byte, 32)); !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Error(err)
	}
}

func TestAllStopsEarly(t *testing.T) {
	d := openDisk(t, t.TempDir(), false)
	for _, k := range []string{"a", "b", "c"} {
		if err := diskPut(t, d, k, k); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, err := range storage.All(t.Context(), d, storage.Key{}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if n == 2 {
			break
		}
	}
	if n != 2 {
		t.Errorf("n = %d", n)
	}
}

func TestLockModeValid(t *testing.T) {
	for m, want := range map[storage.LockMode]bool{"": true, "governance": true, "compliance": true, "GOVERNANCE": false} {
		if m.Valid() != want {
			t.Errorf("%q.Valid() = %v", m, !want)
		}
	}
}
