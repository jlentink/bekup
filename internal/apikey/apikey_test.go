package apikey

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Vectors computed independently (Python: base64.b32encode, hashlib).
var vectors = []struct {
	id     string
	secret []byte
	token  string
	hash   string
}{
	{
		id:     "00112233-4455-6677-8899-aabbccddeeff",
		secret: seq(32),
		token:  "bkp_aaisem2ekvthpcezvk54zxpo74_aaaqeayeaudaocajbifqydiob4ibceqtcqkrmfyydenbwha5dypq",
		hash:   "cdb5c4603c89e5bf6dc8c4b58c1e70ac2780de9928205134a7d5a22aa6d303cc",
	},
	{
		id:     "00000000-0000-0000-0000-000000000000",
		secret: make([]byte, 32),
		token:  "bkp_aaaaaaaaaaaaaaaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		hash:   "f40710458f9b68ed14d81ce9ad59a083f9f97789d8c1574f8984439ab075ad62",
	},
}

func seq(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestVectors(t *testing.T) {
	for _, v := range vectors {
		id := uuid.MustParse(v.id)
		if got := Format(id, v.secret); got != v.token {
			t.Errorf("Format = %s, want %s", got, v.token)
		}
		h := HashOf(id, v.secret)
		if got := hex.EncodeToString(h[:]); got != v.hash {
			t.Errorf("HashOf = %s, want %s", got, v.hash)
		}
		k, err := Parse(v.token)
		if err != nil {
			t.Fatalf("Parse(%s): %v", v.token, err)
		}
		if k.ID != id || k.Hash() != h {
			t.Errorf("Parse(%s) = %v", v.token, k.ID)
		}
		k.Zero()
		if k.Hash() != HashOf(id, make([]byte, SecretBytes)) {
			t.Error("Zero left the secret")
		}
	}
}

func TestDisplayPrefix(t *testing.T) {
	for _, v := range vectors {
		id := uuid.MustParse(v.id)
		p := DisplayPrefix(id)
		if len(p) != len(Prefix)+DisplayPrefixChars || !strings.HasPrefix(v.token, p) {
			t.Fatalf("DisplayPrefix = %q, token %q", p, v.token)
		}
		// Only the id part: the prefix ends before the separator.
		if strings.Contains(p[len(Prefix):], "_") {
			t.Fatalf("DisplayPrefix %q reaches into the secret", p)
		}
	}
}

func TestParseRejects(t *testing.T) {
	good := vectors[0].token
	cases := map[string]string{
		"empty":                 "",
		"upper case":            strings.ToUpper(good),
		"upper prefix":          "BKP_" + good[4:],
		"other prefix":          "bkq_" + good[4:],
		"missing separator":     good[:30] + "a" + good[31:],
		"short":                 good[:len(good)-1],
		"long":                  good + "a",
		"padding":               good[:len(good)-1] + "=",
		"digit 1":               good[:40] + "1" + good[41:],
		"digit 8":               good[:40] + "8" + good[41:],
		"space":                 good[:40] + " " + good[41:],
		"non-canonical id bits": "bkp_aaisem2ekvthpcezvk54zxpo75_" + good[31:],
		"non-canonical secret":  good[:len(good)-1] + "r",
		"bearer prefix":         "Bearer " + good[:len(good)-7],
		"unicode":               good[:40] + "é" + good[42:],
	}
	for name, s := range cases {
		if _, err := Parse(s); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: Parse(%q) = %v, want ErrMalformed", name, s, err)
		}
	}
}

func TestErrorNeverEchoesInput(t *testing.T) {
	_, err := Parse(vectors[0].token[:50])
	if err == nil || strings.Contains(err.Error(), "bkp_") {
		t.Fatalf("error %v", err)
	}
}

func TestGenerate(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		g, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Plaintext) != Len || seen[g.Plaintext] {
			t.Fatalf("token %d chars, duplicate %v", len(g.Plaintext), seen[g.Plaintext])
		}
		seen[g.Plaintext] = true
		k, err := Parse(g.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		if k.ID != g.ID || k.Hash() != g.Hash || g.ID.Version() != 4 {
			t.Fatalf("round trip: %v %v", k.ID, g.ID)
		}
		if !Contains(g.Plaintext) || !Contains("x"+strings.ToUpper(g.Plaintext)) {
			t.Fatal("Contains")
		}
	}
	if Contains("/api/v1/self") {
		t.Fatal("Contains on a path without a token")
	}
}

func FuzzParse(f *testing.F) {
	for _, v := range vectors {
		f.Add(v.token)
	}
	f.Add("bkp_")
	f.Add("bkp_aaisem2ekvthpcezvk54zxpo75_aaaqeayeaudaocajbifqydiob4ibceqtcqkrmfyydenbwha5dypq")
	f.Fuzz(func(t *testing.T, s string) {
		k, err := Parse(s)
		if err != nil {
			if !errors.Is(err, ErrMalformed) || k.ID != uuid.Nil {
				t.Fatalf("failed parse returned %v %v", k.ID, err)
			}
			return
		}
		// Only the canonical spelling parses: formatting the parsed key
		// gives the input back.
		if got := Format(k.ID, k.secret[:]); got != s {
			t.Fatalf("Parse accepted a non-canonical %q (canonical %q)", s, got)
		}
		if len(s) != Len || !Contains(s) {
			t.Fatalf("accepted %q", s)
		}
	})
}
