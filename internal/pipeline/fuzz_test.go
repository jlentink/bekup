package pipeline_test

import (
	"bytes"
	"testing"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
)

func FuzzParseManifestFile(f *testing.F) {
	key, err := crypto.GenerateSigningKey()
	if err != nil {
		f.Fatal(err)
	}
	file, err := pipeline.SignManifest(key, sampleManifest())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(file)
	f.Add([]byte(`{"key_id":"0123456789abcdef","manifest":{},"signature":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	keys := pipeline.PublicKeys{key.ID: key.Public}
	f.Fuzz(func(t *testing.T, data []byte) {
		sm, err := pipeline.ParseManifestFile(data)
		if err != nil {
			return
		}
		again, err := pipeline.Canonicalize(sm.Canonical)
		if err != nil || !bytes.Equal(again, sm.Canonical) {
			t.Fatal("parsed manifest is not canonical")
		}
		if m, err := pipeline.VerifySignature(data, keys); err == nil && !bytes.Equal(data, file) {
			// Only re-formattings of the signed file may verify.
			c, cerr := pipeline.Canonicalize(data)
			if cerr != nil || !bytes.Equal(c, file) || m.RunID != sampleManifest().RunID {
				t.Fatalf("a different document verified: %q", data)
			}
		}
	})
}

func FuzzCanonicalize(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[1,"x",null,true]}`, `" <>"`, `-12`, `{"a":{"b":{}}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := pipeline.Canonicalize(data)
		if err != nil {
			return
		}
		again, err := pipeline.Canonicalize(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", data, out, again, err)
		}
	})
}

func FuzzDecodeName(f *testing.F) {
	for _, s := range []string{"app", "my%2Edb", "%C3%BC", "%2e", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, enc string) {
		name, err := pipeline.DecodeName(enc)
		if err != nil {
			return
		}
		if pipeline.EncodeName(name) != enc {
			t.Fatalf("not canonical: %q", enc)
		}
	})
}
