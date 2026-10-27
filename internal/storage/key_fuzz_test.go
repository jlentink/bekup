package storage_test

import (
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/storage"
)

func FuzzParseKey(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "/a", "a//b", ".x", "a\\b", "a\x00", "a/./b", "ok%2E.dump"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, err := storage.ParseKey(s)
		if err != nil {
			return
		}
		if k.String() != s || len(s) > storage.MaxKeyLen {
			t.Fatalf("accepted %q as %q", s, k)
		}
		if strings.HasPrefix(s, "/") || strings.Contains(s, "//") || strings.HasSuffix(s, "/") ||
			strings.Contains(s, "\\") || strings.Contains(s, ":") {
			t.Fatalf("accepted unsafe key %q", s)
		}
		for _, seg := range strings.Split(s, "/") {
			if seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
				t.Fatalf("accepted dot segment in %q", s)
			}
			for _, c := range []byte(seg) {
				if c < 0x21 || c > 0x7e {
					t.Fatalf("accepted byte %#x in %q", c, s)
				}
			}
		}
		if back, err := storage.ParseKey(k.String()); err != nil || back != k {
			t.Fatalf("round trip of %q failed", s)
		}
	})
}

func FuzzDBName(f *testing.F) {
	for _, s := range []string{"app", "my.db", "50%", "~", "café", "\x00", strings.Repeat("x.", 100)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		enc, err := storage.EncodeDBName(name)
		if err != nil {
			if name != "" && len(name) <= 1024 {
				t.Fatalf("EncodeDBName(%q): %v", name, err)
			}
			return
		}
		if strings.Contains(enc, ".") || storage.ValidateSegment(enc) != nil || len(enc) > storage.MaxEncodedDBNameLen {
			t.Fatalf("encoding %q of %q is not a dot-free segment", enc, name)
		}
		dec, err := storage.DecodeDBName(enc)
		if strings.HasPrefix(enc, "~") {
			if err == nil {
				t.Fatalf("hashed form decoded")
			}
			return
		}
		if err != nil || dec != name {
			t.Fatalf("round trip %q -> %q -> %q, %v", name, enc, dec, err)
		}
	})
}

func FuzzDecodeDBName(f *testing.F) {
	for _, s := range []string{"app", "%2E", "%", "a%2", "%41", "%2e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, enc string) {
		name, err := storage.DecodeDBName(enc)
		if err != nil {
			return
		}
		// Canonical: re-encoding yields the same string.
		if again, err := storage.EncodeDBName(name); err != nil || again != enc {
			t.Fatalf("non-canonical %q decoded to %q (re-encoded %q, %v)", enc, name, again, err)
		}
	})
}
