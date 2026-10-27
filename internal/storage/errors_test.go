package storage_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
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
