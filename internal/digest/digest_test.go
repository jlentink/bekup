package digest

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestReader(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	tests := []struct {
		name    string
		in      string
		limit   int64
		wantErr error
		wantN   int64
		match   bool
	}{
		{"exact", "hello", 5, nil, 5, true},
		{"over the limit", "hello world", 5, ErrTooLong, 11, false},
		{"unlimited", "hello", -1, nil, 5, true},
		{"short", "hell", 5, nil, 4, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewReader(strings.NewReader(tc.in), tc.limit)
			_, err := io.ReadAll(d)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && d.N() != tc.wantN {
				t.Fatalf("N = %d, want %d", d.N(), tc.wantN)
			}
			if d.Matches(5, sum[:]) != tc.match {
				t.Fatalf("Matches = %v, want %v", !tc.match, tc.match)
			}
		})
	}
	d := NewReader(strings.NewReader("hello"), 5)
	if _, err := io.ReadAll(d); err != nil {
		t.Fatal(err)
	}
	if d.Matches(4, sum[:]) || d.Matches(5, sum[:31]) || d.Matches(5, make([]byte, 32)) {
		t.Fatal("a wrong size or digest matched")
	}
}

func TestCtxReader(t *testing.T) {
	mark := errors.New("mark")
	boom := errors.New("boom")

	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("cause")
	cancel(cause)
	if _, err := NewCtxReader(ctx, strings.NewReader("x"), mark).Read(make([]byte, 1)); !errors.Is(err, cause) {
		t.Fatalf("canceled: %v", err)
	}

	r := NewCtxReader(context.Background(), iotest.ErrReader(boom), mark)
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, mark) || !errors.Is(err, boom) {
		t.Fatalf("marked read error: %v", err)
	}
	r = NewCtxReader(context.Background(), iotest.ErrReader(boom), nil)
	if _, err := r.Read(make([]byte, 1)); err != boom { //nolint:errorlint // the unmarked error is passed through unchanged
		t.Fatalf("unmarked read error: %v", err)
	}
	b, err := io.ReadAll(NewCtxReader(context.Background(), strings.NewReader("abc"), mark))
	if err != nil || string(b) != "abc" {
		t.Fatalf("plain read: %q %v", b, err)
	}
}

func FuzzReader(f *testing.F) {
	f.Add([]byte("hello"), int64(5))
	f.Add([]byte(""), int64(-1))
	f.Fuzz(func(t *testing.T, in []byte, limit int64) {
		d := NewReader(iotest.OneByteReader(strings.NewReader(string(in))), limit)
		_, err := io.ReadAll(d)
		over := limit >= 0 && int64(len(in)) > limit
		if over != errors.Is(err, ErrTooLong) {
			t.Fatalf("len %d limit %d: %v", len(in), limit, err)
		}
		if !over {
			sum := sha256.Sum256(in)
			if !d.Matches(int64(len(in)), sum[:]) {
				t.Fatal("digest mismatch")
			}
		}
	})
}
