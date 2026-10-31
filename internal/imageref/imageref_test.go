package imageref

import (
	"errors"
	"strings"
	"testing"
)

const dg = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParsePinned(t *testing.T) {
	tests := []struct {
		in        string
		repo, tag string
		wantErr   bool
		notPinned bool
	}{
		{in: "ghcr.io/jlentink/bekup:1.0.0@" + dg, repo: "ghcr.io/jlentink/bekup", tag: "1.0.0"},
		{in: "ghcr.io/jlentink/bekup@" + dg, repo: "ghcr.io/jlentink/bekup"},
		{in: "127.0.0.1:5001/bekup:0.0.0@" + dg, repo: "127.0.0.1:5001/bekup", tag: "0.0.0"},
		{in: "localhost:5000/team/bekup:v1.2.3-rc.1@" + dg, repo: "localhost:5000/team/bekup", tag: "v1.2.3-rc.1"},
		{in: "bekup:dev@" + dg, repo: "bekup", tag: "dev"},
		{in: "postgres:17-alpine@" + dg, repo: "postgres", tag: "17-alpine"},
		{in: "", wantErr: true},
		{in: "bekup:dev", wantErr: true, notPinned: true},
		{in: "ghcr.io/jlentink/bekup:1.0.0", wantErr: true, notPinned: true},
		{in: "Ghcr.io/bekup@" + dg, wantErr: true},
		{in: "bekup@sha256:abc", wantErr: true},
		{in: "bekup@sha512:" + strings.Repeat("a", 128), wantErr: true, notPinned: true},
		{in: "bekup@" + strings.ToUpper(dg), wantErr: true, notPinned: true},
		{in: " bekup@" + dg, wantErr: true},
		{in: "bekup@" + dg + "\n", wantErr: true},
		{in: "https://ghcr.io/bekup@" + dg, wantErr: true},
		{in: "bekup:${X}@" + dg, wantErr: true},
		{in: "bekup:a b@" + dg, wantErr: true},
		{in: "bekup:" + strings.Repeat("a", 129) + "@" + dg, wantErr: true},
		{in: strings.Repeat("a", MaxLen+1), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			r, err := ParsePinned(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePinned(%q) = %+v, want error", tc.in, r)
				}
				if tc.notPinned != errors.Is(err, ErrNotPinned) {
					t.Fatalf("ErrNotPinned = %v, want %v (%v)", errors.Is(err, ErrNotPinned), tc.notPinned, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePinned(%q): %v", tc.in, err)
			}
			if r.Repo != tc.repo || r.Tag != tc.tag || r.Digest != dg {
				t.Fatalf("got %+v", r)
			}
			if r.String() != tc.in {
				t.Fatalf("String() = %q, want %q", r.String(), tc.in)
			}
		})
	}
}

func FuzzParsePinned(f *testing.F) {
	f.Add("ghcr.io/jlentink/bekup:1.0.0@" + dg)
	f.Add("bekup@" + dg)
	f.Add("a:b@sha256:")
	f.Add("x\n@" + dg)
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParsePinned(s)
		if err != nil {
			return
		}
		if r.String() != s {
			t.Fatalf("round trip: %q -> %q", s, r.String())
		}
		if strings.ContainsAny(s, " \t\r\n\"'$#`\\") {
			t.Fatalf("accepted unsafe characters: %q", s)
		}
		if again, err := ParsePinned(r.String()); err != nil || again != r {
			t.Fatalf("re-parse: %+v %v", again, err)
		}
	})
}

func TestParseFollowing(t *testing.T) {
	tests := []struct {
		in, tag string
		wantErr bool
	}{
		{in: "ghcr.io/jlentink/bekup:latest", tag: "latest"},
		{in: "ghcr.io/jlentink/bekup:0.0.3", tag: "0.0.3"},
		{in: "ghcr.io/jlentink/bekup:v1.2.3-rc.1", tag: "v1.2.3-rc.1"},
		{in: "", wantErr: true},
		{in: "ghcr.io/jlentink/bekup", wantErr: true},
		{in: "ghcr.io/jlentink/bekup:", wantErr: true},
		{in: "ghcr.io/jlentink/bekup:latest@" + dg, wantErr: true},
		{in: "ghcr.io/other/bekup:latest", wantErr: true},
		{in: "ghcr.io/jlentink/bekup-fork:latest", wantErr: true},
		{in: "evil.example/ghcr.io/jlentink/bekup:latest", wantErr: true},
		{in: "ghcr.io/jlentink/bekup/sub:latest", wantErr: true},
		{in: "postgres:latest", wantErr: true},
		{in: "bekup:latest", wantErr: true},
		{in: "GHCR.IO/jlentink/bekup:latest", wantErr: true},
		{in: "ghcr.io/jlentink/bekup:latest\n", wantErr: true},
		{in: "ghcr.io/jlentink/bekup:${TAG}", wantErr: true},
		{in: "ghcr.io/jlentink/bekup:" + strings.Repeat("a", 129), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			r, err := ParseFollowing(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFollowing(%q) = %+v, want error", tc.in, r)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFollowing(%q): %v", tc.in, err)
			}
			if r.Repo != OfficialRepo || r.Tag != tc.tag || r.Digest != "" || !r.IsMutable() || r.String() != tc.in {
				t.Fatalf("got %+v (%q)", r, r.String())
			}
		})
	}
}

func TestParseBekup(t *testing.T) {
	pinned := "ghcr.io/jlentink/bekup:1.0.0@" + dg
	if r, err := ParseBekup(pinned); err != nil || r.IsMutable() || r.String() != pinned {
		t.Fatalf("pinned: %+v %v", r, err)
	}
	if r, err := ParseBekup("ghcr.io/jlentink/bekup:latest"); err != nil || !r.IsMutable() || r.Tag != LatestTag {
		t.Fatalf("latest: %+v %v", r, err)
	}
	// Third-party images and other repositories need a digest.
	for _, s := range []string{"postgres:17", "ghcr.io/someone/bekup:latest", "127.0.0.1:5001/bekup:0.0.3"} {
		if _, err := ParseBekup(s); !errors.Is(err, ErrNotPinned) {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if (Ref{}).IsMutable() || !(Ref{}).IsZero() || (Ref{}).String() != "" {
		t.Fatal("zero Ref")
	}
}

func FuzzParseFollowing(f *testing.F) {
	f.Add("ghcr.io/jlentink/bekup:latest")
	f.Add("ghcr.io/jlentink/bekup:1@" + dg)
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseFollowing(s)
		if err != nil {
			return
		}
		if r.String() != s || r.Repo != OfficialRepo || r.Digest != "" {
			t.Fatalf("round trip: %q -> %+v", s, r)
		}
		if strings.ContainsAny(s, " \t\r\n\"'$#`\\@") {
			t.Fatalf("accepted unsafe characters: %q", s)
		}
	})
}
