package main

import (
	"bytes"
	"encoding/json"
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
)

func runIn(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("BEKUP_CONFIG", "")
	t.Setenv("BEKUP_DEV", "")
	cmd := newRootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func writeTestFile(t *testing.T, p string, b []byte) string {
	t.Helper()
	if err := os.WriteFile(filepath.Clean(p), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readTestFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var appDump = bytes.Repeat([]byte("PGDMP offline test "), 3000)

func recipientFixture(t *testing.T) (*offlinetest.Run, *crypto.AgeKeypair, string) {
	t.Helper()
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kp.Identity.Destroy)
	run := offlinetest.Write(t, t.TempDir(), offlinetest.Spec{
		Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs:        []offlinetest.DB{{Name: "app", Format: pipeline.FormatCustom, Data: appDump}},
		Globals:    []byte("CREATE ROLE app;\n"),
		Warnings:   []string{"globals dumped without role passwords"},
	})
	return run, kp, offlinetest.WritePublicKey(t, t.TempDir(), run.Key)
}

func TestVerifyCommand(t *testing.T) {
	run, _, pub := recipientFixture(t)
	out, _, err := runIn(t, "", "verify", run.Dir, "--pubkey", pub)
	if err != nil || !strings.Contains(out, "result: OK") || !strings.Contains(out, run.Manifest.RunID) {
		t.Fatalf("verify dir: %v\n%s", err, out)
	}
	if _, _, err := runIn(t, "", "verify", run.ManifestPath(), "--pubkey", offlinetest.PublicKeyBase64(run.Key)); err != nil {
		t.Fatalf("verify manifest path, inline key: %v", err)
	}
	out, _, err = runIn(t, "", "verify", run.Dir, "--pubkey", pub, "--json")
	var rep struct {
		OK      bool `json:"ok"`
		Entries []struct {
			Status string `json:"status"`
		} `json:"entries"`
	}
	if err != nil || json.Unmarshal([]byte(out), &rep) != nil || !rep.OK || len(rep.Entries) != 2 {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if _, _, err := runIn(t, "", "verify", run.Dir); err == nil || !strings.Contains(err.Error(), "--pubkey is required") {
		t.Fatalf("no key: %v", err)
	}
	other, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runIn(t, "", "verify", run.Dir, "--pubkey", offlinetest.PublicKeyBase64(other)); !errors.Is(err, pipeline.ErrManifestUnknownKey) {
		t.Fatalf("wrong key: %v", err)
	}
	// --artifact: a copy elsewhere, matched by name; the other entry skipped.
	cp := writeTestFile(t, filepath.Join(t.TempDir(), filepath.Base(run.Paths["app"])), readTestFile(t, run.Paths["app"]))
	out, _, err = runIn(t, "", "verify", run.ManifestPath(), "--pubkey", pub, "--artifact", cp)
	if err != nil || !strings.Contains(out, "[SKIPPED]") || !strings.Contains(out, "others skipped") {
		t.Fatalf("artifact: %v\n%s", err, out)
	}
	if _, _, err := runIn(t, "", "verify", run.Dir, "--pubkey", pub, "--artifact", filepath.Join(t.TempDir(), "other.dump.zst.age")); err == nil {
		t.Fatal("unlisted artifact accepted")
	}
	// A tampered artifact fails with a non-zero exit and a report.
	data := readTestFile(t, run.Paths["app"])
	data[len(data)-10] ^= 1
	writeTestFile(t, run.Paths["app"], data)
	out, _, err = runIn(t, "", "verify", run.Dir, "--pubkey", pub)
	if !errors.Is(err, errVerifyFailed) || !strings.Contains(out, "SHA256_MISMATCH") || !strings.Contains(out, "result: FAILED") {
		t.Fatalf("tampered: %v\n%s", err, out)
	}
	// A tampered manifest fails before any artifact is read.
	writeTestFile(t, run.ManifestPath(), bytes.Replace(run.File, []byte(`"bekup_version":"0.0.0-test"`), []byte(`"bekup_version":"0.0.0-evil"`), 1))
	if _, _, err := runIn(t, "", "verify", run.Dir, "--pubkey", pub); !errors.Is(err, pipeline.ErrManifestBadSignature) {
		t.Fatalf("tampered manifest: %v", err)
	}
}

func TestDecryptFlagValidation(t *testing.T) {
	dir := t.TempDir()
	art := writeTestFile(t, filepath.Join(dir, "app.dump.zst.age"), []byte("x"))
	out := filepath.Join(dir, "out.dump")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"decrypt", art, "--out", out, "--passphrase", "hunter2", "--no-verify"}, "never accepted as flag values"},
		{[]string{"decrypt", art, "--out", out, "--password", "hunter2", "--no-verify"}, "never accepted as flag values"},
		{[]string{"decrypt", art, "--out", out, "--identity", "AGE-SECRET-KEY-1X", "--no-verify"}, "never accepted as flag values"},
		{[]string{"decrypt", art, "--stdin-passphrase"}, "--out is required"},
		{[]string{"decrypt", art, "--out", out, "--stdin-passphrase"}, "refusing to decrypt unverified content"},
		{[]string{"decrypt", art, "--out", out, "--stdin-passphrase", "--manifest", "m.json"}, "--manifest needs --pubkey"},
		{[]string{"decrypt", art, "--out", out, "--stdin-passphrase", "--no-verify", "--pubkey", "k"}, "contradict"},
		{[]string{"decrypt", art, "--out", out, "--stdin-passphrase", "--identity-file", "f", "--no-verify"}, "only one of"},
		{[]string{"decrypt", art, "--out", out, "--stdin-passphrase", "--no-verify", "--max-work-factor", "22"}, "--max-work-factor"},
		{[]string{"decrypt", art, "--out", out, "--no-verify"}, "the artifact is encrypted"},
	}
	for _, c := range cases {
		_, _, err := runIn(t, "", c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %v, want %q", c.args, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("secret echoed: %v", err)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("output created by a refused command")
	}
}

func TestDecryptCommandIdentity(t *testing.T) {
	run, kp, pub := recipientFixture(t)
	dir := t.TempDir()
	idFile := writeTestFile(t, filepath.Join(dir, "job.agekey"), append(kp.Identity.Bytes(), '\n'))
	out := filepath.Join(dir, "app.dump")
	args := []string{"decrypt", run.Paths["app"], "--out", out, "--identity-file", idFile, "--manifest", run.ManifestPath(), "--pubkey", pub}
	_, stderr, err := runIn(t, "", args...)
	if err != nil || !strings.Contains(stderr, "verified: signed manifest") || !strings.Contains(stderr, "pg_restore") {
		t.Fatalf("decrypt: %v\n%s", err, stderr)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Mode().Perm() != 0o600 || !bytes.Equal(readTestFile(t, out), appDump) {
		t.Fatalf("output: %v %v", fi, err)
	}
	// Overwrite protection.
	if _, _, err := runIn(t, "", args...); !errors.Is(err, offline.ErrOutputExists) {
		t.Fatalf("overwrite: %v", err)
	}
	if _, _, err := runIn(t, "", append(args, "--force")...); err != nil {
		t.Fatalf("force: %v", err)
	}
	// Stdout.
	stdout, _, err := runIn(t, "", "decrypt", run.Paths["app"], "--out", "-", "--identity-file", idFile, "--manifest", run.ManifestPath(), "--pubkey", pub)
	if err != nil || stdout != string(appDump) {
		t.Fatalf("stdout: %v (%d bytes)", err, len(stdout))
	}
	// The wrong key type for the mode.
	pf := writeTestFile(t, filepath.Join(dir, "pass"), []byte("some passphrase"))
	if _, _, err := runIn(t, "", "decrypt", run.Paths["app"], "--out", filepath.Join(dir, "x"), "--passphrase-file", pf,
		"--manifest", run.ManifestPath(), "--pubkey", pub); err == nil || !strings.Contains(err.Error(), "--identity-file") {
		t.Fatalf("passphrase for recipients: %v", err)
	}
	// An insecure identity file is refused.
	loose := writeTestFile(t, filepath.Join(dir, "loose.agekey"), kp.Identity.Bytes())
	if err := os.Chmod(loose, 0o644); err != nil { //nolint:gosec // G302: the test needs a world-readable key file
		t.Fatal(err)
	}
	if _, _, err := runIn(t, "", "decrypt", run.Paths["app"], "--out", filepath.Join(dir, "y"), "--identity-file", loose,
		"--manifest", run.ManifestPath(), "--pubkey", pub); err == nil || !strings.Contains(err.Error(), "world-readable") {
		t.Fatalf("loose identity file: %v", err)
	}
	// A tampered artifact is refused and nothing is written.
	data := readTestFile(t, run.Paths["app"])
	data[len(data)/2] ^= 1
	writeTestFile(t, run.Paths["app"], data)
	tampered := filepath.Join(dir, "tampered.dump")
	if _, _, err := runIn(t, "", "decrypt", run.Paths["app"], "--out", tampered, "--identity-file", idFile,
		"--manifest", run.ManifestPath(), "--pubkey", pub); !errors.Is(err, offline.ErrDigestMismatch) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := os.Stat(tampered); !os.IsNotExist(err) {
		t.Fatal("output written for a tampered artifact")
	}
	// --no-verify decrypts what age accepts (here it fails: the chunk is
	// authenticated) and prints the warning.
	_, stderr, err = runIn(t, "", "decrypt", run.Paths["app"], "--out", tampered, "--identity-file", idFile, "--no-verify")
	if err == nil || !strings.Contains(stderr, "WARNING: --no-verify") {
		t.Fatalf("no-verify: %v\n%s", err, stderr)
	}
	if _, err := os.Stat(tampered); !os.IsNotExist(err) {
		t.Fatal("output written for a failed decryption")
	}
}

func TestDecryptCommandPassphrase(t *testing.T) {
	pass := crypto.NewSecret([]byte("alpha bravo charlie delta echo foxtrot"))
	defer pass.Destroy()
	dump := []byte("-- MariaDB dump\nCREATE TABLE t (id int);\n")
	job := uuid.New()
	run := offlinetest.Write(t, t.TempDir(), offlinetest.Spec{
		JobID:      job.String(),
		Engine:     pipeline.EngineInfo{Kind: "mariadb", VersionKey: "11.4", ServerVersion: "11.4.8-MariaDB"},
		Encryption: pipeline.PassphraseEncryption(pass, 2),
		DBs:        []offlinetest.DB{{Name: "shop", Format: pipeline.FormatSQL, Data: dump}},
	})
	pub := offlinetest.WritePublicKey(t, t.TempDir(), run.Key)
	dir := t.TempDir()

	// A passphrase export: the manifest picks version 2.
	ex := &jobkeys.Export{JobID: job, ExportedAt: time.Now(), Versions: []jobkeys.ExportedPassphrase{
		{PassphraseInfo: jobkeys.PassphraseInfo{Version: 1, CreatedAt: time.Now()}, Passphrase: crypto.NewSecret([]byte("an old retired passphrase"))},
		{PassphraseInfo: jobkeys.PassphraseInfo{Version: 2, CreatedAt: time.Now()}, Passphrase: crypto.NewSecret(bytes.Clone(pass.Bytes()))},
	}}
	defer ex.Destroy()
	file := ex.File()
	exportPath := writeTestFile(t, filepath.Join(dir, "export.txt"), file.Bytes())
	file.Destroy()
	out := filepath.Join(dir, "shop.sql")
	_, stderr, err := runIn(t, "", "decrypt", run.Paths["shop"], "--out", out, "--passphrase-file", exportPath,
		"--manifest", run.ManifestPath(), "--pubkey", pub)
	if err != nil || !bytes.Equal(readTestFile(t, out), dump) || !strings.Contains(stderr, "mariadb <target>") {
		t.Fatalf("export: %v\n%s", err, stderr)
	}
	// Without a manifest the version must be chosen; the wrong one fails.
	if _, _, err := runIn(t, "", "decrypt", run.Paths["shop"], "--out", filepath.Join(dir, "a.sql"), "--passphrase-file", exportPath,
		"--no-verify"); err == nil || !strings.Contains(err.Error(), "--passphrase-version") {
		t.Fatalf("ambiguous version: %v", err)
	}
	if _, _, err := runIn(t, "", "decrypt", run.Paths["shop"], "--out", filepath.Join(dir, "b.sql"), "--passphrase-file", exportPath,
		"--no-verify", "--passphrase-version", "1"); err == nil {
		t.Fatal("wrong version decrypted")
	}
	// A plain passphrase on stdin.
	stdout, _, err := runIn(t, string(pass.Bytes())+"\n", "decrypt", run.Paths["shop"], "--out", "-", "--stdin-passphrase",
		"--manifest", run.ManifestPath(), "--pubkey", pub)
	if err != nil || stdout != string(dump) {
		t.Fatalf("stdin: %v %q", err, stdout)
	}
	// An export of another job is refused.
	other := &jobkeys.Export{JobID: uuid.New(), ExportedAt: time.Now(), Versions: []jobkeys.ExportedPassphrase{
		{PassphraseInfo: jobkeys.PassphraseInfo{Version: 2, CreatedAt: time.Now()}, Passphrase: crypto.NewSecret([]byte("x y z"))},
	}}
	defer other.Destroy()
	of := other.File()
	otherPath := writeTestFile(t, filepath.Join(dir, "other.txt"), of.Bytes())
	of.Destroy()
	if _, _, err := runIn(t, "", "decrypt", run.Paths["shop"], "--out", filepath.Join(dir, "c.sql"), "--passphrase-file", otherPath,
		"--manifest", run.ManifestPath(), "--pubkey", pub); err == nil || !strings.Contains(err.Error(), "the export is for job") {
		t.Fatalf("other job: %v", err)
	}
}

func TestReindexFlagValidation(t *testing.T) {
	cfg := writeTestFile(t, filepath.Join(t.TempDir(), "dest.toml"), []byte("type = \"disk\"\n[disk]\npath = \"/nonexistent\"\n"))
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"reindex"}, "destination-config"},
		{[]string{"reindex", "--destination-config", cfg}, "--pubkey is required"},
		{[]string{"reindex", "--destination-config", cfg, "--pubkey", "k", "--dry-run"}, "--dry-run only applies"},
		{[]string{"reindex", "--destination-config", cfg, "--import", "--job-slug", "a"}, "--destination-id"},
		{[]string{"reindex", "--destination-config", cfg, "--import", "--destination-id", uuid.NewString()}, "--job-slug"},
		{[]string{"reindex", "--destination-config", cfg, "--import", "--job-slug", "a", "--destination-id", "x", "--pubkey", "k"}, "omit --pubkey"},
		{[]string{"reindex", "--destination-config", cfg, "--pubkey", "k", "--destination-id", uuid.NewString()}, "only applies to --import"},
		{[]string{"reindex", "--destination-config", cfg, "--pubkey", "k", "--job-slug", "Bad Slug"}, "--job-slug"},
	}
	for _, c := range cases {
		_, _, err := runIn(t, "", c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %v, want %q", c.args, err, c.want)
		}
	}
}

func TestReindexInventory(t *testing.T) {
	run, _, pub := recipientFixture(t)
	cfg := writeTestFile(t, filepath.Join(t.TempDir(), "dest.toml"), []byte("type = \"disk\"\n[disk]\npath = \""+run.Root+"\"\n"))
	out, _, err := runIn(t, "", "reindex", "--destination-config", cfg, "--pubkey", pub)
	if err != nil || !strings.Contains(out, "[VERIFIED]") || !strings.Contains(out, run.Manifest.RunID) {
		t.Fatalf("inventory: %v\n%s", err, out)
	}
	out, _, err = runIn(t, "", "reindex", "--destination-config", cfg, "--pubkey", pub, "--job-slug", "app-db", "--json")
	var res []struct {
		Verified bool `json:"verified"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || len(res) != 1 || !res[0].Verified {
		t.Fatalf("json: %v\n%s", err, out)
	}
	data := readTestFile(t, run.Paths["app"])
	writeTestFile(t, run.Paths["app"], data[:len(data)-1])
	out, _, err = runIn(t, "", "reindex", "--destination-config", cfg, "--pubkey", pub)
	if err == nil || !strings.Contains(out, "[FAILED]") || !strings.Contains(out, "SIZE_MISMATCH") {
		t.Fatalf("tampered: %v\n%s", err, out)
	}
}
