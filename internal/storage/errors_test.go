package storage_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/jlentink/bekup/internal/storage"
)

type timeoutError struct{ timeout bool }

func (e timeoutError) Error() string   { return "net" }
func (e timeoutError) Timeout() bool   { return e.timeout }
func (e timeoutError) Temporary() bool { return false }

var _ net.Error = timeoutError{}

func TestClassify(t *testing.T) {
	tests := []struct {
		err  error
		want storage.ErrorCode
	}{
		{nil, storage.CodeOK},
		{fmt.Errorf("x: %w", storage.ErrInvalidKey), storage.CodeInvalidKey},
		{storage.ErrInvalidArgument, storage.CodeInvalidArgument},
		{storage.ErrNotFound, storage.CodeNotFound},
		{fs.ErrNotExist, storage.CodeNotFound},
		{storage.ErrAlreadyExists, storage.CodeAlreadyExists},
		{storage.ErrDeleteNotPermitted, storage.CodeDeleteNotPermitted},
		{storage.ErrChecksumMismatch, storage.CodeChecksumMismatch},
		{storage.ErrUnsupported, storage.CodeUnsupported},
		{&os.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC}, storage.CodeNoSpace},
		{storage.ErrInsufficientSpace, storage.CodeNoSpace},
		{fs.ErrPermission, storage.CodePermission},
		{storage.ErrClosed, storage.CodeClosed},
		{context.Canceled, storage.CodeCanceled},
		{fmt.Errorf("%w: %w", storage.ErrSource, context.Canceled), storage.CodeCanceled},
		{context.DeadlineExceeded, storage.CodeTimeout},
		{timeoutError{timeout: true}, storage.CodeTimeout},
		{timeoutError{}, storage.CodeNetwork},
		{storage.WithCode(storage.CodePermission, errors.New("403 from provider")), storage.CodePermission},
		{fmt.Errorf("op: %w", storage.WithCode(storage.CodeHostKeyMismatch, errors.New("pin"))), storage.CodeHostKeyMismatch},
		{errors.New("other"), storage.CodeInternal},
	}
	for _, tc := range tests {
		if got := storage.Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %s, want %s", tc.err, got, tc.want)
		}
		if tc.want.Message() == "" {
			t.Errorf("empty message for %s", tc.want)
		}
	}
	if storage.WithCode(storage.CodeNetwork, nil) != nil {
		t.Error("WithCode(nil) != nil")
	}
	if storage.ErrorCode("bogus").Message() != storage.CodeInternal.Message() {
		t.Error("unknown code message")
	}
}

func TestDetailAndDescribe(t *testing.T) {
	base := fmt.Errorf("%w: the server reported 0 of 10 bytes", storage.ErrChecksumMismatch)
	err := fmt.Errorf("sftp: put: %w", storage.WithDetail(base, "the server reported 0 B of 10.0 MiB after closing"))
	if !errors.Is(err, storage.ErrChecksumMismatch) || storage.Classify(err) != storage.CodeChecksumMismatch {
		t.Fatalf("class lost: %v", err)
	}
	if err.Error() != "sftp: put: "+base.Error() {
		t.Fatalf("text changed: %q", err.Error())
	}
	want := "the data did not match the expected checksum: the server reported 0 B of 10.0 MiB after closing"
	if got := storage.Describe(storage.Classify(err), err); got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	// The outermost detail wins; a code keeps its class under a detail.
	outer := storage.WithDetail(storage.WithDetail(storage.WithCode(storage.CodePermission, errors.New("raw server text /srv/x")), "inner"), "outer")
	if storage.Detail(outer) != "outer" || storage.Classify(outer) != storage.CodePermission {
		t.Fatalf("outer detail = %q, class %s", storage.Detail(outer), storage.Classify(outer))
	}
	// No detail: the fixed message only (raw text never shown).
	raw := storage.WithCode(storage.CodeHostKeyMismatch, errors.New("pinned SHA256:abc presented SHA256:def"))
	if got := storage.Describe(storage.Classify(raw), raw); got != storage.CodeHostKeyMismatch.Message() {
		t.Fatalf("Describe without detail = %q", got)
	}
	if storage.Describe(storage.CodeTimeout, nil) != storage.CodeTimeout.Message() {
		t.Fatal("Describe(nil)")
	}
	if storage.WithDetail(nil, "x") != nil {
		t.Fatal("WithDetail(nil) != nil")
	}
	// Control characters are replaced and the length is capped.
	long := storage.WithDetail(errors.New("x"), "a\nb\x1b[31m"+strings.Repeat("z", 500))
	d := storage.Detail(long)
	if strings.ContainsAny(d, "\n\x1b") || len(d) > 210 || !strings.HasPrefix(d, "a b [31m") || !strings.HasSuffix(d, "…") {
		t.Fatalf("sanitized detail = %q", d)
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 43202150: "41.2 MiB", 5 << 30: "5.0 GiB"}
	for n, want := range cases {
		if got := storage.FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
	if got := storage.FormatSizes(0, 43202150); got != "0 B of 41.2 MiB" {
		t.Errorf("FormatSizes = %q", got)
	}
	if got := storage.FormatSizes(43202149, 43202150); got != "43202149 of 43202150 bytes" {
		t.Errorf("FormatSizes same text = %q", got)
	}
}
