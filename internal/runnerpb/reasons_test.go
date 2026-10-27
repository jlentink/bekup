package runnerpb

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReasonStatusRoundTrip(t *testing.T) {
	msgs := map[string]Reason{}
	for r, info := range reasonInfo {
		err := r.Status()
		if status.Code(err) != info.code || status.Convert(err).Message() != info.msg {
			t.Errorf("%s: %v", r, err)
		}
		if got := ErrorReason(fmt.Errorf("wrapped: %w", err)); got != r {
			t.Errorf("%s: ErrorReason = %q", r, got)
		}
		if prev, dup := msgs[info.msg]; dup {
			t.Errorf("%s and %s share a message", r, prev)
		}
		msgs[info.msg] = r
	}
	if status.Code(Reason("NOPE").Status()) != codes.Internal {
		t.Error("unknown reason")
	}
	if ErrorReason(errors.New("plain")) != "" || ErrorReason(status.Error(codes.Unavailable, "x")) != "" {
		t.Error("reason from an error without details")
	}
}

func TestPinMismatchStatus(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", PinMismatchStatus("leaf_spki"))
	if status.Code(err) != codes.FailedPrecondition || ErrorReason(err) != ReasonPinMismatch {
		t.Fatalf("status %v reason %q", err, ErrorReason(err))
	}
	if check, ok := PinMismatchCheck(err); !ok || check != "leaf_spki" {
		t.Fatalf("check %q %v", check, ok)
	}
	if check, ok := PinMismatchCheck(ReasonPinMismatch.Status()); !ok || check != "" {
		t.Fatalf("plain pin reason: %q %v", check, ok)
	}
	for _, e := range []error{nil, errors.New("x"), ReasonTLSFailed.Status(), status.Error(codes.FailedPrecondition, "x")} {
		if _, ok := PinMismatchCheck(e); ok {
			t.Fatalf("%v reported as a pin mismatch", e)
		}
	}
}
