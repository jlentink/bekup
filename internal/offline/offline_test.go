package offline_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/offline"
	"github.com/jlentink/bekup/internal/offline/offlinetest"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

const statsJSON = `{"databases":{"app":{"consistency":"snapshot","level":"deep","objects":{"schemas":2,"tables":3},` +
	`"tables":{"public.users":{"count":"exact","rows":1500},"private.keys":{"count":"unknown"}},"tables_total":3,` +
	`"warnings":[{"code":"insufficient_privileges","count":1,"objects":["private.keys"]}]}},` +
	`"globals":{"consistency":"approximate","level":"deep","objects":{"roles":7,"tablespaces":0}}}`

func newIdentity(t *testing.T) *crypto.AgeKeypair {
	t.Helper()
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kp.Identity.Destroy)
	return kp
}

func recipientRun(t *testing.T, root string, kp *crypto.AgeKeypair) *offlinetest.Run {
	t.Helper()
	return offlinetest.Write(t, root, offlinetest.Spec{
		Encryption: pipeline.RecipientEncryption(2, kp.Recipient),
		DBs: []offlinetest.DB{
			{Name: "app", Format: pipeline.FormatCustom, Data: bytes.Repeat([]byte("PGDMP app data "), 5000)},
			{Name: "my.db\x1b[31m", Format: pipeline.FormatCustom, Data: []byte("PGDMP other")},
		},
		Globals:  []byte("CREATE ROLE app;\n"),
		Stats:    []byte(statsJSON),
		Warnings: []string{"a warning with \x1b]0;evil\x07 escape"},
	})
}

func keySet(k *crypto.SigningKey) *offline.KeySet {
	return &offline.KeySet{Keys: []offline.PublicKey{{ID: k.ID, Key: k.Public}}}
}

func TestPublicKeys(t *testing.T) {
	k1, k2 := newSigningKey(t), newSigningKey(t)
	retired := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	keys := []offline.PublicKey{
		{ID: k1.ID, Key: k1.Public, CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), RetiredAt: &retired},
		{ID: k2.ID, Key: k2.Public, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	var b bytes.Buffer
	if err := offline.FormatPublicKeys(&b, keys, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), crypto.FingerprintBase64(k1.Public)) {
		t.Fatalf("no fingerprint:\n%s", b.String())
	}
	got, err := offline.ParsePublicKeys(b.Bytes())
	if err != nil || len(got) != 2 || got[0].ID != k1.ID || got[0].RetiredAt == nil || !got[0].RetiredAt.Equal(retired) ||
		got[1].RetiredAt != nil || !bytes.Equal(got[1].Key, k2.Public) {
		t.Fatalf("parse: %+v %v", got, err)
	}
	bare, err := offline.ParsePublicKeys([]byte("# escrow\n" + offlinetest.PublicKeyBase64(k1) + "\n"))
	if err != nil || len(bare) != 1 || bare[0].ID != k1.ID {
		t.Fatalf("bare: %+v %v", bare, err)
	}
	bad := []string{
		"",
		"garbage",
		offline.PublicKeysHeader + "\n",
		offline.PublicKeysHeader + "\nkey 0000000000000000 " + offlinetest.PublicKeyBase64(k1),
		offline.PublicKeysHeader + "\nkey " + k1.ID + " " + offlinetest.PublicKeyBase64(k1) + " created nope",
		offline.PublicKeysHeader + "\nkey " + k1.ID + " " + offlinetest.PublicKeyBase64(k1) + "\nkey " + k1.ID + " " + offlinetest.PublicKeyBase64(k1),
	}
	for _, in := range bad {
		if _, err := offline.ParsePublicKeys([]byte(in)); !errors.Is(err, offline.ErrPublicKeys) {
			t.Errorf("%q: %v", in, err)
		}
	}
	// --pubkey values: bare base64 or a file.
	dir := t.TempDir()
	p := filepath.Join(dir, "keys.pub")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := offline.LoadPublicKeys([]string{offlinetest.PublicKeyBase64(k2), p})
	if err != nil || len(set.Keys) != 2 {
		t.Fatalf("load: %+v %v", set, err)
	}
	if _, err := offline.LoadPublicKeys([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestVerifyRun(t *testing.T) {
	ctx := context.Background()
	kp := newIdentity(t)
	run := recipientRun(t, t.TempDir(), kp)
	keys := keySet(run.Key)

	m, err := offline.VerifyManifest(run.File, keys.Verifier())
	if err != nil {
		t.Fatal(err)
	}
	src, err := offline.OpenDir(run.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer offline.CloseQuietly(src)
	rep := offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{})
	if !rep.OK || rep.Partial || len(rep.Entries) != 3 || rep.KeyFingerprint != crypto.FingerprintBase64(run.Key.Public) {
		t.Fatalf("report: %+v", rep)
	}
	var out bytes.Buffer
	offline.WriteReport(&out, rep)
	text := out.String()
	for _, want := range []string{"signature: VALID", "app-db", "postgres", "insufficient_privileges", "recipient key version 2",
		"result: OK", `my.db\x1b[31m`, `\x1b]0;evil\x07`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if strings.ContainsRune(text, 0x1b) || strings.ContainsRune(text, 0x07) {
		t.Fatal("control characters reached the report")
	}

	// Only some artifacts.
	rep = offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{Only: map[string]bool{m.Entries[0].Path: true}})
	if !rep.OK || !rep.Partial || rep.Entries[1].Status != offline.StatusSkipped {
		t.Fatalf("partial: %+v", rep)
	}

	// Wrong or missing keys.
	other := newSigningKey(t)
	if _, err := offline.VerifyManifest(run.File, keySet(other).Verifier()); !errors.Is(err, pipeline.ErrManifestUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := offline.VerifyManifest(run.File, pipeline.PublicKeys{}); !errors.Is(err, offline.ErrNoKeys) {
		t.Fatalf("no keys: %v", err)
	}
	// A key claiming another key's id.
	if _, err := offline.VerifyManifest(run.File, pipeline.PublicKeys{run.Key.ID: other.Public}); err == nil {
		t.Fatal("mismatched key id accepted")
	}
	// Tampered manifest: one changed byte in a signed field.
	tampered := bytes.Replace(run.File, []byte(`"server_version":"17.6"`), []byte(`"server_version":"17.7"`), 1)
	if _, err := offline.VerifyManifest(tampered, keys.Verifier()); !errors.Is(err, pipeline.ErrManifestBadSignature) {
		t.Fatalf("tampered manifest: %v", err)
	}

	// Tampered, truncated and missing artifacts.
	app := run.Paths["app"]
	data := readFile(t, app)
	data[len(data)/2] ^= 1
	writeFile(t, app, data)
	rep = offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{})
	if rep.OK || rep.Entries[0].Status != offline.StatusDigestMismatch {
		t.Fatalf("tampered artifact: %+v", rep.Entries[0])
	}
	writeFile(t, app, data[:len(data)-1])
	rep = offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{})
	if rep.Entries[0].Status != offline.StatusSizeMismatch {
		t.Fatalf("truncated artifact: %+v", rep.Entries[0])
	}
	writeFile(t, app, append(data, 'x', 'y'))
	rep = offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{})
	if rep.Entries[0].Status != offline.StatusSizeMismatch || rep.Entries[0].BytesRead != rep.Entries[0].Entry.StoredSize+1 {
		t.Fatalf("grown artifact: %+v", rep.Entries[0])
	}
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}
	rep = offline.VerifyRun(ctx, run.File, m, src, keys, offline.VerifyOptions{})
	if rep.OK || rep.Entries[0].Status != offline.StatusMissing {
		t.Fatalf("missing artifact: %+v", rep.Entries[0])
	}
	out.Reset()
	offline.WriteReport(&out, rep)
	if !strings.Contains(out.String(), "result: FAILED") {
		t.Fatalf("report:\n%s", out.String())
	}

	if err := offline.CheckLocation(m, "app-db", storage.TimestampSegment(m.RunTimestamp)); err != nil {
		t.Fatal(err)
	}
	if err := offline.CheckLocation(m, "other", storage.TimestampSegment(m.RunTimestamp)); !errors.Is(err, offline.ErrLocation) {
		t.Fatalf("slug: %v", err)
	}
	if err := offline.CheckLocation(m, "app-db", "2020-01-01T000000Z"); !errors.Is(err, offline.ErrLocation) {
		t.Fatalf("timestamp: %v", err)
	}
}

func writeFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Clean(p), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newSigningKey(t *testing.T) *crypto.SigningKey {
	t.Helper()
	k, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func decryptFile(t *testing.T, path string, d pipeline.Decryption, e *pipeline.Entry) ([]byte, error) {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer offline.CloseQuietly(f)
	var out bytes.Buffer
	_, err = offline.Decrypt(context.Background(), &out, f, d, e)
	return out.Bytes(), err
}

func TestDecryptRecipients(t *testing.T) {
	kp := newIdentity(t)
	run := recipientRun(t, t.TempDir(), kp)
	e := &run.Manifest.Entries[0]
	want := bytes.Repeat([]byte("PGDMP app data "), 5000)
	got, err := decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: kp.Identity.Bytes()}, e)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("decrypt: %v (%d bytes)", err, len(got))
	}
	// Without an entry: no checks, same output.
	got, err = decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: kp.Identity.Bytes()}, nil)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("decrypt without entry: %v", err)
	}
	// The wrong identity fails.
	other := newIdentity(t)
	if _, err := decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: other.Identity.Bytes()}, e); err == nil {
		t.Fatal("wrong identity accepted")
	}
	// The wrong entry (another artifact's digests) fails.
	if _, err := decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: kp.Identity.Bytes()}, &run.Manifest.Entries[1]); err == nil {
		t.Fatal("wrong entry accepted")
	}
	// A manifest whose raw digest differs (as if the dump were swapped).
	bad := *e
	bad.RawSHA256 = strings.Repeat("0", 64)
	if _, err := decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: kp.Identity.Bytes()}, &bad); !errors.Is(err, offline.ErrRawMismatch) {
		t.Fatalf("raw mismatch: %v", err)
	}
	bad = *e
	bad.RawSize--
	if _, err := decryptFile(t, run.Paths["app"], pipeline.Decryption{Identities: kp.Identity.Bytes()}, &bad); !errors.Is(err, offline.ErrRawMismatch) {
		t.Fatalf("raw size: %v", err)
	}
}

func TestDecryptPassphraseAndNone(t *testing.T) {
	pass := crypto.NewSecret([]byte("correct horse battery staple and more words"))
	defer pass.Destroy()
	dump := []byte("-- MySQL dump\nCREATE TABLE t (id int);\n")
	run := offlinetest.Write(t, t.TempDir(), offlinetest.Spec{
		Engine:     pipeline.EngineInfo{Kind: "mysql", VersionKey: "8.4", ServerVersion: "8.4.6"},
		Encryption: pipeline.PassphraseEncryption(pass, 3),
		DBs:        []offlinetest.DB{{Name: "shop", Format: pipeline.FormatSQL, Data: dump}},
	})
	e := &run.Manifest.Entries[0]
	got, err := decryptFile(t, run.Paths["shop"], pipeline.Decryption{Passphrase: pass.Bytes()}, e)
	if err != nil || !bytes.Equal(got, dump) {
		t.Fatalf("passphrase: %v", err)
	}
	if _, err := decryptFile(t, run.Paths["shop"], pipeline.Decryption{Passphrase: []byte("wrong")}, e); err == nil {
		t.Fatal("wrong passphrase accepted")
	}

	plain := offlinetest.Write(t, t.TempDir(), offlinetest.Spec{
		Encryption: pipeline.OptedOutEncryption(),
		DBs:        []offlinetest.DB{{Name: "x", Format: pipeline.FormatCustom, Data: []byte("PGDMP x")}},
	})
	got, err = decryptFile(t, plain.Paths["x"], pipeline.Decryption{None: true}, &plain.Manifest.Entries[0])
	if err != nil || string(got) != "PGDMP x" {
		t.Fatalf("none: %v %q", err, got)
	}
}

func TestPassphraseExport(t *testing.T) {
	job := uuid.New()
	retired := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ex := &jobkeys.Export{JobID: job, ExportedAt: time.Now(), Versions: []jobkeys.ExportedPassphrase{
		{PassphraseInfo: jobkeys.PassphraseInfo{Version: 1, CreatedAt: time.Now(), RetiredAt: &retired}, Passphrase: crypto.NewSecret([]byte("old words here"))},
		{PassphraseInfo: jobkeys.PassphraseInfo{Version: 2, CreatedAt: time.Now()}, Passphrase: crypto.NewSecret([]byte("  new  words  "))},
	}}
	defer ex.Destroy()
	file := ex.File()
	defer file.Destroy()
	if !offline.IsPassphraseExport(file.Bytes()) {
		t.Fatal("not detected")
	}
	parsed, err := offline.ParsePassphraseExport(file.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Destroy()
	p1, ok1 := parsed.Passphrase(1)
	p2, ok2 := parsed.Passphrase(2)
	if parsed.JobID != job || !ok1 || !ok2 || string(p1.Bytes()) != "old words here" || string(p2.Bytes()) != "  new  words  " ||
		len(parsed.Versions()) != 2 {
		t.Fatalf("parsed %+v", parsed.Versions())
	}
	if offline.IsPassphraseExport([]byte("just a passphrase")) {
		t.Fatal("plain passphrase detected as export")
	}
	bad := []string{
		offline.PassphraseExportHeader + "\njob " + job.String() + "\nversion 1 created x\n",
		offline.PassphraseExportHeader + "\npassphrase SECRETVALUE\n",
		offline.PassphraseExportHeader + "\njob nope\n",
		offline.PassphraseExportHeader + "\njob " + job.String() + "\nversion 1\npassphrase a\nversion 1\npassphrase SECRETVALUE\n",
		offline.PassphraseExportHeader + "\njob " + job.String() + "\nbogus SECRETVALUE\n",
	}
	for _, in := range bad {
		_, err := offline.ParsePassphraseExport([]byte(in))
		if !errors.Is(err, offline.ErrPassphraseExport) || strings.Contains(err.Error(), "SECRETVALUE") {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestOutputFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.dump")
	o, err := offline.CreateOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.WriteString("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("target visible before commit")
	}
	if err := o.Commit(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(target)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
	if _, err := offline.CreateOutput(target, false); !errors.Is(err, offline.ErrOutputExists) {
		t.Fatalf("overwrite: %v", err)
	}
	// Committing over a target created in between is refused too.
	o2, err := offline.CreateOutput(filepath.Join(dir, "race.dump"), false)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "race.dump"), []byte("other"))
	if err := o2.Commit(); !errors.Is(err, offline.ErrOutputExists) {
		t.Fatalf("race: %v", err)
	}
	o3, err := offline.CreateOutput(target, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o3.WriteString("two"); err != nil {
		t.Fatal(err)
	}
	if err := o3.Commit(); err != nil {
		t.Fatal(err)
	}
	if b := readFile(t, target); string(b) != "two" {
		t.Fatalf("force: %q", b)
	}
	o4, err := offline.CreateOutput(filepath.Join(dir, "aborted.dump"), false)
	if err != nil {
		t.Fatal(err)
	}
	o4.Abort()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") || e.Name() == "aborted.dump" {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
	if _, err := offline.CreateOutput(dir, true); err == nil {
		t.Fatal("directory accepted as output")
	}
}

func TestScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	kp := newIdentity(t)
	a := recipientRun(t, root, kp)
	b := offlinetest.Write(t, root, offlinetest.Spec{
		Key: a.Key, Slug: "app-db", Timestamp: a.Manifest.RunTimestamp.Add(24 * time.Hour), JobID: a.Manifest.JobID,
		Encryption: pipeline.RecipientEncryption(2, kp.Recipient),
		DBs:        []offlinetest.DB{{Name: "app", Format: pipeline.FormatCustom, Data: []byte("PGDMP day two")}},
	})
	_ = b
	// An incomplete run (no manifest) and a replayed manifest in another
	// run's directory.
	inc := filepath.Join(root, "app-db", "2026-10-29T030000Z")
	if err := os.MkdirAll(inc, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(inc, "app.dump.zst.age"), []byte("partial"))
	replay := filepath.Join(root, "app-db", "2026-10-30T030000Z")
	if err := os.MkdirAll(replay, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(replay, storage.ManifestFileName), a.File)
	// Objects outside the layout are ignored.
	writeFile(t, filepath.Join(root, "README"), []byte("x"))

	disk, err := storage.OpenDisk(storage.DiskConfig{Root: root, NoDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.CloseQuietly(disk)
	keys := keySet(a.Key)
	runs, err := offline.Scan(ctx, disk, offline.ScanOptions{Keys: keys.Verifier(), KeySet: keys})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("runs: %d", len(runs))
	}
	if !runs[0].Verified() || !runs[1].Verified() || runs[0].Report.Manifest.RunID != a.Manifest.RunID {
		t.Fatalf("verified runs: %+v %+v", runs[0].Err, runs[1].Err)
	}
	if !errors.Is(runs[2].Err, offline.ErrIncomplete) || !errors.Is(runs[3].Err, offline.ErrLocation) {
		t.Fatalf("incomplete %v, replay %v", runs[2].Err, runs[3].Err)
	}
	// Narrowed to a job; manifests only.
	runs, err = offline.Scan(ctx, disk, offline.ScanOptions{JobSlug: "other-job", Keys: keys.Verifier()})
	if err != nil || len(runs) != 0 {
		t.Fatalf("other job: %v %d", err, len(runs))
	}
	runs, err = offline.Scan(ctx, disk, offline.ScanOptions{JobSlug: "app-db", Keys: keys.Verifier(), SkipArtifacts: true})
	if err != nil || len(runs) != 4 || runs[0].Report == nil || !runs[0].Report.Partial {
		t.Fatalf("skip artifacts: %v", err)
	}
	if _, err := offline.Scan(ctx, disk, offline.ScanOptions{}); !errors.Is(err, offline.ErrNoKeys) {
		t.Fatalf("no keys: %v", err)
	}
	// A tampered artifact fails the run.
	data := readFile(t, a.Paths["app"])
	data[0] ^= 0xff
	writeFile(t, a.Paths["app"], data)
	runs, err = offline.Scan(ctx, disk, offline.ScanOptions{Keys: keys.Verifier()})
	if err != nil || runs[0].Verified() || runs[0].Report.Entries[0].Status != offline.StatusDigestMismatch {
		t.Fatalf("tampered: %v", err)
	}
}

func TestSafeText(t *testing.T) {
	cases := map[string]string{
		"plain":           "plain",
		"a\x1b[2Jb":       `a\x1b[2Jb`,
		"bell\x07":        `bell\x07`,
		"back\\slash":     `back\\slash`,
		"bidi\u202eevil":  `bidi\u{202e}evil`,
		"bad\xffutf8":     `bad\xffutf8`,
		"ünïcødé":         "ünïcødé",
		"c1\u009bcontrol": `c1\x9bcontrol`,
		"line\u2028break": `line\u{2028}break`,
	}
	for in, want := range cases {
		if got := offline.SafeText(in); got != want {
			t.Errorf("SafeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummarizeStats(t *testing.T) {
	m := &pipeline.Manifest{Stats: []byte(statsJSON)}
	s, err := offline.SummarizeStats(m)
	if err != nil || len(s.Databases) != 1 || s.Globals == nil || s.WarningCount != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	d := s.Databases[0]
	if d.Name != "app" || d.ExactRows != 1500 || d.UnknownTables != 1 || d.TablesTotal != 3 || d.Objects["tables"] != 3 {
		t.Fatalf("db: %+v", d)
	}
	if s, err := offline.SummarizeStats(&pipeline.Manifest{}); s != nil || err != nil {
		t.Fatal("empty stats")
	}
}

func TestDestinationConfig(t *testing.T) {
	good := map[string]string{
		"disk": "type = \"disk\"\n[disk]\npath = \"/srv/backups\"\n",
		"s3": "type = \"s3\"\n[s3]\nregion = \"eu-central-1\"\nbucket = \"backups\"\naccess_key_id = \"AKIA1234\"\n" +
			"secret_access_key_file = \"/run/s3key\"\n[netguard]\nallow_cidrs = [\"10.0.0.0/8\"]\n",
		"sftp": "type = \"sftp\"\n[sftp]\nhost = \"backup.example.com\"\nuser = \"bekup\"\nbase_path = \"/upload\"\n" +
			"host_key_fingerprint = \"SHA256:abc\"\nprivate_key_file = \"/run/sshkey\"\n",
	}
	for name, in := range good {
		c, err := offline.ParseDestinationConfig([]byte(in))
		if err != nil || c.Type != name {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := []string{
		"type = \"disk\"\n",
		"type = \"disk\"\n[disk]\npath = \"relative\"\n",
		"type = \"s3\"\n[disk]\npath = \"/x\"\n",
		"type = \"disk\"\n[disk]\npath = \"/x\"\nbogus = 1\n",
		"type = \"s3\"\n[s3]\nregion = \"r\"\nsecret_access_key = \"inline\"\n",
		"type = \"sftp\"\n[sftp]\nhost = \"h\"\nprivate_key_file = \"/a\"\npassword_file = \"/b\"\n",
		"type = \"sftp\"\n[sftp]\nhost = \"h\"\npassword_file = \"/b\"\nprivate_key_passphrase_file = \"/c\"\n",
		"type = \"disk\"\n[disk]\npath = \"/x\"\n[s3]\nbucket = \"b\"\n",
	}
	for _, in := range bad {
		if _, err := offline.ParseDestinationConfig([]byte(in)); err == nil {
			t.Errorf("accepted: %q", in)
		}
	}
	// A disk description opens read-only.
	root := t.TempDir()
	c, err := offline.ParseDestinationConfig([]byte("type = \"disk\"\n[disk]\npath = \"" + root + "\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Open(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer offline.CloseQuietly(d)
	if !d.Capabilities().NoDeleteMode {
		t.Fatal("not opened in no-delete mode")
	}
}
