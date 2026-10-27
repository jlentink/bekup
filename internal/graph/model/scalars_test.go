package model

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

func TestUUID(t *testing.T) {
	u := uuid.MustParse("0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a90")
	var b bytes.Buffer
	MarshalUUID(u).MarshalGQL(&b)
	if b.String() != `"0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a90"` {
		t.Fatalf("marshal %s", b.String())
	}
	if got, err := UnmarshalUUID(u.String()); err != nil || got != u {
		t.Fatalf("unmarshal %v %v", got, err)
	}
	for _, bad := range []any{"x", 1, "{0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a90}", "0b9e4a526f0e4c0e9d6a3c1f2b7e8a90", "urn:uuid:0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a90", "0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a9g"} {
		if _, err := UnmarshalUUID(bad); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func TestTime(t *testing.T) {
	ts := time.Date(2026, 10, 26, 12, 0, 0, 5, time.FixedZone("x", 3600))
	var b bytes.Buffer
	MarshalTime(ts).MarshalGQL(&b)
	if b.String() != `"2026-10-26T11:00:00.000000005Z"` {
		t.Fatalf("marshal %s", b.String())
	}
	if got, err := UnmarshalTime("2026-10-26T12:00:00+01:00"); err != nil || !got.Equal(ts.Truncate(time.Second)) {
		t.Fatalf("unmarshal %v %v", got, err)
	}
	for _, bad := range []any{"yesterday", 5, "2026-10-26"} {
		if _, err := UnmarshalTime(bad); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func FuzzUnmarshalUUID(f *testing.F) {
	f.Add("0b9e4a52-6f0e-4c0e-9d6a-3c1f2b7e8a90")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		u, err := UnmarshalUUID(s)
		if err == nil && u.String() != s && u.String() != toLower(s) {
			t.Fatalf("accepted non-canonical %q as %s", s, u)
		}
	})
}

func toLower(s string) string { return string(bytes.ToLower([]byte(s))) }
