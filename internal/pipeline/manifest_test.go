package pipeline_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	runTS     = time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	createdTS = time.Date(2026, 10, 27, 3, 4, 5, 123000000, time.UTC)
)

func sumHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func sampleManifest() *pipeline.Manifest {
	return &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion,
		BekupVersion:  "1.2.3",
		InstanceKeyID: "0123456789abcdef",
		JobID:         "6f1c1d2e-0b7a-4c55-9a43-0e6d1c9e7a10",
		JobSlug:       "nightly-app",
		RunID:         "a3b2c1d0-1111-4222-8333-944455556666",
		RunTimestamp:  runTS,
		CreatedAt:     createdTS,
		Engine:        pipeline.EngineInfo{Kind: "postgres", VersionKey: "18", ServerVersion: "18.1 (Debian 18.1-1.pgdg13+1)"},
		Entries: []pipeline.Entry{
			{
				DB: "app", Kind: pipeline.KindDatabase, Path: "app.dump.zst.age",
				RawSize: 1000, StoredSize: 420, SHA256: sumHex("stored-app"), RawSHA256: sumHex("raw-app"),
				Compression: "zstd",
				Encryption:  pipeline.EntryEncryption{Mode: pipeline.EncryptPassphrase, PassphraseVersion: 2},
			},
			{
				DB: "my%2Edb", Kind: pipeline.KindDatabase, Path: "my%2Edb.dump.zst.age",
				RawSize: 10, StoredSize: 300, SHA256: sumHex("stored-my.db"), RawSHA256: sumHex("raw-my.db"),
				Compression: "zstd",
				Encryption:  pipeline.EntryEncryption{Mode: pipeline.EncryptPassphrase, PassphraseVersion: 2},
			},
			{
				Kind: pipeline.KindGlobals, Path: "globals.globals.sql.zst.age",
				RawSize: 5, StoredSize: 250, SHA256: sumHex("stored-globals"), RawSHA256: sumHex("raw-globals"),
				Compression: "zstd",
				Encryption: pipeline.EntryEncryption{
					Mode: pipeline.EncryptRecipients, RecipientVersion: 4,
					RecipientFingerprints: []string{sumHex("age1example")},
				},
			},
		},
		Stats:    json.RawMessage(`{"verification":"deep","databases":{"app":{"tables":12,"rows":{"public.users":1500}}},"note":"<&> ü"}`),
		Warnings: []string{"globals dumped without role passwords"},
	}
}

func TestManifestCanonicalGolden(t *testing.T) {
	got, err := sampleManifest().CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "manifest_v1.canonical.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden) //nolint:gosec // G304: fixed test data path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical form changed:\n got %s\nwant %s", got, want)
	}
	// Canonicalization is idempotent.
	again, err := pipeline.Canonicalize(got)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatal("canonical form not stable")
	}
}

func TestCanonicalize(t *testing.T) {
	ok := map[string]string{
		`{ "b" : 1, "a" : [ true, null, "x" ] }`: `{"a":[true,null,"x"],"b":1}`,
		`{"é":1,"z":2,"A":3}`:                    `{"A":3,"z":2,"é":1}`,
		"\"<&>é\u2028\\n\\u0001\"":               "\"<&>é\\u2028\\n\\u0001\"",
		`-9223372036854775808`:                   `-9223372036854775808`,
		`{}`:                                     `{}`,
	}
	for in, want := range ok {
		got, err := pipeline.Canonicalize([]byte(in))
		if err != nil || string(got) != want {
			t.Fatalf("%s: got %s, %v; want %s", in, got, err, want)
		}
	}
	bad := []string{
		`{"a":1,"a":2}`, `1.5`, `1e3`, `-0`, `01`, `9223372036854775808`, `{"a":1} x`, "\"\xff\"", ``,
		strings.Repeat("[", 40) + strings.Repeat("]", 40), `{"a":}`, `[1,]`,
	}
	for _, in := range bad {
		if _, err := pipeline.Canonicalize([]byte(in)); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func newKey(t *testing.T) *crypto.SigningKey {
	t.Helper()
	k, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Destroy)
	return k
}

func bindingFor(m *pipeline.Manifest) pipeline.Binding {
	b := pipeline.Binding{JobID: m.JobID, RunID: m.RunID, JobSlug: m.JobSlug, RunTimestamp: m.RunTimestamp}
	for i := range m.Entries {
		e := &m.Entries[i]
		s, err := hex.DecodeString(e.SHA256)
		if err != nil {
			panic(err)
		}
		r, err := hex.DecodeString(e.RawSHA256)
		if err != nil {
			panic(err)
		}
		b.Entries = append(b.Entries, pipeline.ExpectedEntry{Path: e.Path, SHA256: s, StoredSize: e.StoredSize, RawSHA256: r})
	}
	return b
}

func TestSignVerify(t *testing.T) {
	key := newKey(t)
	m := sampleManifest()
	file, err := pipeline.SignManifest(key, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.InstanceKeyID != key.ID {
		t.Fatal("instance key id not set")
	}
	keys := pipeline.PublicKeys{key.ID: key.Public}
	got, err := pipeline.Verify(file, keys, bindingFor(m))
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != m.RunID || len(got.Entries) != 3 || !bytes.Equal(got.Stats, mustCanon(t, m.Stats)) {
		t.Fatalf("verified manifest differs: %+v", got)
	}
	// crypto.Verifier (instancekeys.Verifier) works too.
	v := crypto.NewVerifier()
	if _, err := v.Add(key.Public); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Verify(file, v, bindingFor(m)); err != nil {
		t.Fatal(err)
	}
	// A re-formatted (pretty-printed) file still verifies.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, file, "", "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Verify(pretty.Bytes(), keys, bindingFor(m)); err != nil {
		t.Fatalf("pretty-printed manifest: %v", err)
	}
}

func mustCanon(t *testing.T, b []byte) []byte {
	t.Helper()
	c, err := pipeline.Canonicalize(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerifyTamper(t *testing.T) {
	key := newKey(t)
	m := sampleManifest()
	file, err := pipeline.SignManifest(key, m)
	if err != nil {
		t.Fatal(err)
	}
	keys := pipeline.PublicKeys{key.ID: key.Public}
	b := bindingFor(m)
	for i := range file {
		for _, bit := range []byte{0x01, 0x20} {
			mod := bytes.Clone(file)
			mod[i] ^= bit
			if _, err := pipeline.Verify(mod, keys, b); err == nil {
				t.Fatalf("byte %d ^ %#x (%q -> %q) accepted", i, bit, file[i], mod[i])
			}
		}
	}
	// A semantic change with a valid structure is a bad signature.
	mod := bytes.Replace(file, []byte(`"stored_size":420`), []byte(`"stored_size":421`), 1)
	if _, err := pipeline.Verify(mod, keys, b); !errors.Is(err, pipeline.ErrManifestBadSignature) {
		t.Fatalf("want bad signature, got %v", err)
	}
	mod = bytes.Replace(file, []byte(`"rows":{"public.users":1500}`), []byte(`"rows":{"public.users":1501}`), 1)
	if _, err := pipeline.Verify(mod, keys, b); !errors.Is(err, pipeline.ErrManifestBadSignature) {
		t.Fatalf("stats tamper: want bad signature, got %v", err)
	}
}

func TestVerifyReplay(t *testing.T) {
	key := newKey(t)
	keys := pipeline.PublicKeys{key.ID: key.Public}
	expected := sampleManifest()

	otherRun := sampleManifest()
	otherRun.RunID = "b0000000-0000-4000-8000-000000000001"
	file, err := pipeline.SignManifest(key, otherRun)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pipeline.Verify(file, keys, bindingFor(expected))
	if !errors.Is(err, pipeline.ErrReplay) || !errors.Is(err, pipeline.ErrBindingMismatch) {
		t.Fatalf("other run: want replay, got %v", err)
	}

	otherJob := sampleManifest()
	otherJob.JobID = "c0000000-0000-4000-8000-000000000002"
	file, err = pipeline.SignManifest(key, otherJob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Verify(file, keys, bindingFor(expected)); !errors.Is(err, pipeline.ErrReplay) {
		t.Fatalf("other job: want replay, got %v", err)
	}

	// Same run, but the DB records other artifacts.
	file, err = pipeline.SignManifest(key, sampleManifest())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*pipeline.Binding){
		"sha":       func(b *pipeline.Binding) { b.Entries[0].SHA256 = make([]byte, 32) },
		"size":      func(b *pipeline.Binding) { b.Entries[1].StoredSize++ },
		"raw sha":   func(b *pipeline.Binding) { b.Entries[2].RawSHA256 = make([]byte, 32) },
		"path":      func(b *pipeline.Binding) { b.Entries[0].Path = "other.dump.zst.age" },
		"missing":   func(b *pipeline.Binding) { b.Entries = b.Entries[:2] },
		"duplicate": func(b *pipeline.Binding) { b.Entries[1] = b.Entries[0] },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			b := bindingFor(expected)
			mut(&b)
			_, err := pipeline.Verify(file, keys, b)
			if !errors.Is(err, pipeline.ErrBindingMismatch) || errors.Is(err, pipeline.ErrReplay) {
				t.Fatalf("want binding mismatch, got %v", err)
			}
		})
	}
	// A binding without the required fields fails closed.
	if _, err := pipeline.Verify(file, keys, pipeline.Binding{}); !errors.Is(err, pipeline.ErrBindingMismatch) {
		t.Fatalf("empty binding: %v", err)
	}
}

func TestVerifyKeys(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	m := sampleManifest()
	file, err := pipeline.SignManifest(oldKey, m)
	if err != nil {
		t.Fatal(err)
	}
	b := bindingFor(m)
	// Unknown key.
	if _, err := pipeline.Verify(file, pipeline.PublicKeys{newK.ID: newK.Public}, b); !errors.Is(err, pipeline.ErrManifestUnknownKey) {
		t.Fatalf("want unknown key, got %v", err)
	}
	if _, err := pipeline.Verify(file, nil, b); !errors.Is(err, pipeline.ErrManifestUnknownKey) {
		t.Fatalf("nil keys: %v", err)
	}
	// A retired key that is still known verifies.
	both := pipeline.PublicKeys{oldKey.ID: oldKey.Public, newK.ID: newK.Public}
	if _, err := pipeline.Verify(file, both, b); err != nil {
		t.Fatalf("old key: %v", err)
	}
	// Swapping the key id for another known key fails.
	swapped := bytes.Replace(file, []byte(`"key_id":"`+oldKey.ID), []byte(`"key_id":"`+newK.ID), 1)
	if _, err := pipeline.Verify(swapped, both, b); !errors.Is(err, pipeline.ErrManifestBadSignature) {
		t.Fatalf("swapped key id: %v", err)
	}
	// A map entry whose id does not match its key is refused.
	if _, err := pipeline.Verify(file, pipeline.PublicKeys{oldKey.ID: newK.Public}, b); err == nil {
		t.Fatal("mismatched id/key accepted")
	}
	// A manifest signature cannot come from another purpose.
	sm, err := pipeline.ParseManifestFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := oldKey.Sign("audit-head", sm.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sig, sm.Signature) {
		t.Fatal("purposes not separated")
	}
}

func TestManifestValidate(t *testing.T) {
	cases := map[string]func(*pipeline.Manifest){
		"version":       func(m *pipeline.Manifest) { m.FormatVersion = 2 },
		"bekup version": func(m *pipeline.Manifest) { m.BekupVersion = "" },
		"job id":        func(m *pipeline.Manifest) { m.JobID = "a/b" },
		"slug":          func(m *pipeline.Manifest) { m.JobSlug = "Bad Slug" },
		"ts not whole":  func(m *pipeline.Manifest) { m.RunTimestamp = runTS.Add(time.Millisecond) },
		"ts not utc":    func(m *pipeline.Manifest) { m.RunTimestamp = runTS.In(time.FixedZone("x", 3600)) },
		"engine":        func(m *pipeline.Manifest) { m.Engine.Kind = "oracle" },
		"no entries":    func(m *pipeline.Manifest) { m.Entries = nil },
		"dup path":      func(m *pipeline.Manifest) { m.Entries[1] = m.Entries[0] },
		"path vs db":    func(m *pipeline.Manifest) { m.Entries[0].DB = "other" },
		"bad db":        func(m *pipeline.Manifest) { m.Entries[0].DB = "a%2e" },
		"manifest path": func(m *pipeline.Manifest) { m.Entries[0].Path = "manifest.json" },
		"globals db":    func(m *pipeline.Manifest) { m.Entries[2].DB = "x" },
		"db as globals": func(m *pipeline.Manifest) { m.Entries[0].Path = "app.globals.sql.zst.age" },
		"sha":           func(m *pipeline.Manifest) { m.Entries[0].SHA256 = "ABC" },
		"compression":   func(m *pipeline.Manifest) { m.Entries[0].Compression = "gzip" },
		"enc mode":      func(m *pipeline.Manifest) { m.Entries[0].Encryption.Mode = "" },
		"none with .age": func(m *pipeline.Manifest) {
			m.Entries[0].Encryption = pipeline.EntryEncryption{Mode: pipeline.EncryptNone}
		},
		"recipient fp": func(m *pipeline.Manifest) {
			m.Entries[0].Encryption = pipeline.EntryEncryption{Mode: pipeline.EncryptRecipients, RecipientFingerprints: []string{"x"}}
		},
		"recipient v0":  func(m *pipeline.Manifest) { m.Entries[2].Encryption.RecipientVersion = 0 },
		"pass+recip v":  func(m *pipeline.Manifest) { m.Entries[0].Encryption.RecipientVersion = 1 },
		"stats array":   func(m *pipeline.Manifest) { m.Stats = json.RawMessage(`[1]`) },
		"stats float":   func(m *pipeline.Manifest) { m.Stats = json.RawMessage(`{"a":1.5}`) },
		"empty warning": func(m *pipeline.Manifest) { m.Warnings = []string{""} },
		"negative size": func(m *pipeline.Manifest) { m.Entries[0].RawSize = -1 },
		"key id":        func(m *pipeline.Manifest) { m.InstanceKeyID = "xyz" },
		"two globals": func(m *pipeline.Manifest) {
			m.Entries[1] = m.Entries[2]
			m.Entries[1].Path = "globals.globals.sql.zst2.age"
		},
		"server version":  func(m *pipeline.Manifest) { m.Engine.ServerVersion = "a\nb" },
		"version key":     func(m *pipeline.Manifest) { m.Engine.VersionKey = "x" },
		"created not utc": func(m *pipeline.Manifest) { m.CreatedAt = createdTS.Local() },
	}
	if err := sampleManifest().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest()
			mut(m)
			if name == "created not utc" && m.CreatedAt.Location() == time.UTC {
				t.Skip("local zone is UTC")
			}
			if err := m.Validate(); !errors.Is(err, pipeline.ErrManifestMalformed) {
				t.Fatalf("want malformed, got %v", err)
			}
		})
	}
}

func TestParseManifestFileStrict(t *testing.T) {
	key := newKey(t)
	file, err := pipeline.SignManifest(key, sampleManifest())
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string][]byte{
		"extra outer field": bytes.Replace(file, []byte(`{"key_id"`), []byte(`{"extra":1,"key_id"`), 1),
		"unknown field":     bytes.Replace(file, []byte(`"bekup_version"`), []byte(`"zzz":1,"bekup_version"`), 1),
		"case folded key":   bytes.Replace(file, []byte(`"job_slug"`), []byte(`"JOB_SLUG"`), 1),
		"non-canonical ts":  bytes.Replace(file, []byte(`"2026-10-27T03:00:00Z"`), []byte(`"2026-10-27T03:00:00.000Z"`), 1),
		"duplicate key":     bytes.Replace(file, []byte(`"bekup_version":"1.2.3"`), []byte(`"bekup_version":"1.2.3","bekup_version":"1.2.4"`), 1),
		"not object":        []byte(`[]`),
		"huge":              bytes.Repeat([]byte(" "), pipeline.MaxManifestBytes+1),
	}
	for name, data := range bad {
		if _, err := pipeline.ParseManifestFile(data); !errors.Is(err, pipeline.ErrManifestMalformed) {
			t.Fatalf("%s: want malformed, got %v", name, err)
		}
	}
}

func TestWriteManifestAndPipelineIntegration(t *testing.T) {
	recip, ident := recipients(t)
	key := newKey(t)
	a, b := newFake("a"), newFake("b")
	input := randomish(9, 300<<10)
	name, err := pipeline.ArtifactFileName(pipeline.KindDatabase, "shop", pipeline.FormatCustom, pipeline.EncryptRecipients)
	if err != nil {
		t.Fatal(err)
	}
	artKey, err := pipeline.ArtifactKey("pre", "nightly-app", runTS, name)
	if err != nil {
		t.Fatal(err)
	}
	spec := baseSpec(pipeline.RecipientEncryption(1, recip))
	spec.Targets = []pipeline.Target{{Name: "a", Dest: a, Key: artKey}, {Name: "b", Dest: b, Key: artKey}}
	res, err := pipeline.Run(t.Context(), bytes.NewReader(input), spec)
	if err != nil {
		t.Fatal(err)
	}
	m := sampleManifest()
	m.Entries = []pipeline.Entry{res.Entry(pipeline.KindDatabase, "shop", name)}
	mKey, err := pipeline.ManifestKey("pre", "nightly-app", runTS)
	if err != nil {
		t.Fatal(err)
	}
	if mKey.String() != "pre/nightly-app/2026-10-27T030000Z/manifest.json" {
		t.Fatalf("manifest key %s", mKey)
	}
	// b already holds a manifest: never overwritten, reported per target.
	if err := b.Delete(t.Context(), mKey); err != nil {
		t.Fatal(err)
	}
	b.objects[mKey.String()] = []byte("old")
	wr, err := pipeline.WriteManifest(t.Context(), []pipeline.Target{{Name: "a", Dest: a, Key: mKey}, {Name: "b", Dest: b, Key: mKey}}, key, m)
	if err != nil {
		t.Fatal(err)
	}
	if wr.Destinations[0].Outcome != pipeline.OutcomeOK || wr.Destinations[1].Code != storage.CodeAlreadyExists {
		t.Fatalf("write results: %+v", wr.Destinations)
	}
	file, _ := a.object(mKey)
	if !bytes.Equal(file, wr.File) {
		t.Fatal("stored manifest differs")
	}
	binding := pipeline.Binding{
		JobID: m.JobID, RunID: m.RunID,
		Entries: []pipeline.ExpectedEntry{{Path: name, SHA256: res.SHA256, StoredSize: res.StoredSize, RawSHA256: res.RawSHA256}},
	}
	got, err := pipeline.Verify(file, pipeline.PublicKeys{key.ID: key.Public}, binding)
	if err != nil {
		t.Fatal(err)
	}
	// Restore path: the stored artifact matches the signed hash and
	// decrypts to the input.
	stored, _ := a.object(artKey)
	sum := sha256.Sum256(stored)
	if hex.EncodeToString(sum[:]) != got.Entries[0].SHA256 {
		t.Fatal("artifact hash differs from the manifest")
	}
	if !bytes.Equal(decode(t, stored, pipeline.Decryption{Identities: ident}), input) {
		t.Fatal("round trip")
	}
	// All targets failing is an error.
	if _, err := pipeline.WriteManifest(t.Context(), []pipeline.Target{{Name: "a", Dest: a, Key: mKey}}, key, m); !errors.Is(err, pipeline.ErrAllDestinationsFailed) {
		t.Fatalf("want ErrAllDestinationsFailed, got %v", err)
	}
	// A non-manifest key is refused.
	if _, err := pipeline.WriteManifest(context.Background(), []pipeline.Target{{Name: "a", Dest: a, Key: artKey}}, key, m); !errors.Is(err, pipeline.ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec, got %v", err)
	}
}

func TestEncodeName(t *testing.T) {
	for _, name := range []string{"app", "my.db", "ünïcode", "a/b%c~", strings.Repeat("x.", 400), "\x00\xff"} {
		enc := pipeline.EncodeName(name)
		got, err := pipeline.DecodeName(enc)
		if err != nil || got != name {
			t.Fatalf("%q: %q, %v", name, got, err)
		}
	}
	for _, bad := range []string{"", "%41", "%2e", "%G0", "a%2", "a.b"} {
		if _, err := pipeline.DecodeName(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestRecipientFingerprint(t *testing.T) {
	recip, _ := recipients(t)
	fp := pipeline.RecipientFingerprint(recip)
	if fp != pipeline.RecipientFingerprint(strings.ToUpper(recip)) || fp != sumHex(recip) {
		t.Fatal("fingerprint not canonical")
	}
}
