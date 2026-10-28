//go:build integration

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/offline/offlinetest"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// TestOfflineRecoveryRestoresWithPgRestore is the task 4.7 acceptance
// criterion: real pg_dump output goes through the backup pipeline onto a
// disk destination (one passphrase job, one age recipient job); `bekup
// verify` passes, `bekup decrypt` (passphrase export / age identity, after
// verifying the signed manifest) yields dumps that the stock pg_restore and
// psql of the official image restore, and `bekup reindex` finds both runs.
func TestOfflineRecoveryRestoresWithPgRestore(t *testing.T) {
	ctx := t.Context()
	pg, err := postgres.Run(ctx, dbtest.PGImage, postgres.WithDatabase("src"), postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"), postgres.BasicWaitStrategies())
	if err != nil {
		dockertest.Skipf(t, "postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})
	sh := func(cmd ...string) string {
		t.Helper()
		code, out, err := pg.Exec(ctx, cmd, tcexec.Multiplexed())
		var b []byte
		if out != nil {
			var rerr error
			if b, rerr = io.ReadAll(out); rerr != nil {
				t.Fatal(rerr)
			}
		}
		if err != nil || code != 0 {
			t.Fatalf("%v: exit %d: %v: %s", cmd, code, err, b)
		}
		return string(b)
	}
	copyOut := func(p string) []byte {
		t.Helper()
		rc, err := pg.CopyFileFromContainer(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := rc.Close(); err != nil {
				t.Error(err)
			}
		}()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	sh("psql", "-U", "postgres", "-d", "src", "-v", "ON_ERROR_STOP=1", "-c",
		"CREATE ROLE reporting NOLOGIN; CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL); "+
			"INSERT INTO items SELECT g, 'item ' || g FROM generate_series(1, 1200) g;")
	sh("pg_dump", "-U", "postgres", "-d", "src", "--format=custom", "--file=/tmp/src.dump")
	sh("sh", "-c", "pg_dumpall -U postgres --globals-only --no-role-passwords > /tmp/globals.sql")
	dump, globals := copyOut("/tmp/src.dump"), copyOut("/tmp/globals.sql")

	root := t.TempDir()
	key, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	pass := crypto.NewSecret([]byte("tango uniform victor whiskey xray yankee"))
	defer pass.Destroy()
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	job := uuid.New()
	passRun := offlinetest.Write(t, root, offlinetest.Spec{
		Key: key, Slug: "pass-job", JobID: job.String(), Encryption: pipeline.PassphraseEncryption(pass, 1),
		DBs: []offlinetest.DB{{Name: "src", Format: pipeline.FormatCustom, Data: dump}}, Globals: globals,
	})
	idRun := offlinetest.Write(t, root, offlinetest.Spec{
		Key: key, Slug: "age-job", Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs: []offlinetest.DB{{Name: "src", Format: pipeline.FormatCustom, Data: dump}},
	})
	keys := t.TempDir()
	pubFile := offlinetest.WritePublicKey(t, keys, key)

	for _, r := range []*offlinetest.Run{passRun, idRun} {
		if out, _, err := runIn(t, "", "verify", r.Dir, "--pubkey", pubFile); err != nil || !strings.Contains(out, "result: OK") {
			t.Fatalf("verify %s: %v\n%s", r.Dir, err, out)
		}
	}

	// Passphrase: from a bekup passphrase export file.
	work := t.TempDir()
	export := fmt.Sprintf("bekup-job-passphrases v1\n# test\njob %s\nexported 2026-10-28T00:00:00Z\nversion 1 created 2026-10-01T00:00:00Z\npassphrase %s\n",
		job, pass.Bytes())
	exportPath := writeTestFile(t, filepath.Join(work, "export.txt"), []byte(export))
	passOut, globalsOut := filepath.Join(work, "pass.dump"), filepath.Join(work, "globals.sql")
	if _, stderr, err := runIn(t, "", "decrypt", passRun.Paths["src"], "--out", passOut, "--passphrase-file", exportPath,
		"--manifest", passRun.ManifestPath(), "--pubkey", pubFile); err != nil {
		t.Fatalf("decrypt passphrase: %v\n%s", err, stderr)
	}
	if _, stderr, err := runIn(t, "", "decrypt", passRun.Paths[""], "--out", globalsOut, "--passphrase-file", exportPath,
		"--manifest", passRun.ManifestPath(), "--pubkey", pubFile); err != nil || !strings.Contains(stderr, "psql") {
		t.Fatalf("decrypt globals: %v\n%s", err, stderr)
	}
	// Identity.
	idFile := writeTestFile(t, filepath.Join(work, "job.agekey"), kp.Identity.Bytes())
	idOut := filepath.Join(work, "age.dump")
	if _, stderr, err := runIn(t, "", "decrypt", idRun.Paths["src"], "--out", idOut, "--identity-file", idFile,
		"--manifest", idRun.ManifestPath(), "--pubkey", pubFile); err != nil {
		t.Fatalf("decrypt identity: %v\n%s", err, stderr)
	}

	// Restore with the stock tools.
	sh("psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-c", "DROP ROLE reporting")
	if err := pg.CopyToContainer(ctx, readTestFile(t, globalsOut), "/tmp/globals-restore.sql", 0o644); err != nil {
		t.Fatal(err)
	}
	// The globals recreate the dropped role (the postgres role exists:
	// skip that statement's error by running without ON_ERROR_STOP).
	sh("psql", "-U", "postgres", "-d", "postgres", "-f", "/tmp/globals-restore.sql")
	if got := strings.TrimSpace(sh("psql", "-U", "postgres", "-At", "-c", "SELECT count(*) FROM pg_roles WHERE rolname = 'reporting'")); got != "1" {
		t.Fatalf("globals not restored: %q", got)
	}
	for i, p := range []string{passOut, idOut} {
		db := fmt.Sprintf("restored%d", i)
		if err := pg.CopyToContainer(ctx, readTestFile(t, p), "/tmp/"+db+".dump", 0o644); err != nil {
			t.Fatal(err)
		}
		sh("createdb", "-U", "postgres", db)
		sh("pg_restore", "-U", "postgres", "--exit-on-error", "--no-owner", "--dbname="+db, "/tmp/"+db+".dump")
		if got := strings.TrimSpace(sh("psql", "-U", "postgres", "-d", db, "-At", "-c", "SELECT count(*) || ':' || max(id) FROM items")); got != "1200:1200" {
			t.Fatalf("%s: restored rows %q", db, got)
		}
	}

	// reindex finds and verifies both runs.
	cfg := writeTestFile(t, filepath.Join(work, "dest.toml"), []byte("type = \"disk\"\n[disk]\npath = \""+root+"\"\n"))
	out, _, err := runIn(t, "", "reindex", "--destination-config", cfg, "--pubkey", pubFile)
	if err != nil || strings.Count(out, "[VERIFIED]") != 2 {
		t.Fatalf("reindex: %v\n%s", err, out)
	}

	// A tampered manifest makes verify exit non-zero.
	writeTestFile(t, idRun.ManifestPath(), bytes.Replace(idRun.File, []byte(`"age-job"`), []byte(`"age-jo0"`), 1))
	if _, _, err := runIn(t, "", "verify", idRun.Dir, "--pubkey", pubFile); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func runIn2(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := runIn(t, "", args...)
	return out, err
}

// TestReindexImport imports a destination's runs into a test database.
func TestReindexImport(t *testing.T) {
	ctx := t.Context()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	q := sqlc.New(pool)
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	key, _, err := instancekeys.Active(ctx, q, kr)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Destroy()
	destID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"x"}')`, destID); err != nil {
		t.Fatal(err)
	}
	jobID, err := dbtest.InsertJob(ctx, pool, dbtest.JobRow{DestinationIDs: []uuid.UUID{destID}})
	if err != nil {
		t.Fatal(err)
	}
	slug := "job-" + jobID.String()[:8]
	kp, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Identity.Destroy()
	root := t.TempDir()
	ts := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	good := offlinetest.Write(t, root, offlinetest.Spec{
		Key: key, Slug: slug, JobID: jobID.String(), Timestamp: ts, Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs:      []offlinetest.DB{{Name: "app", Format: pipeline.FormatCustom, Data: []byte("PGDMP one")}, {Name: "two", Format: pipeline.FormatCustom, Data: []byte("PGDMP two")}},
		Warnings: []string{"restricted backup role"},
	})
	bad := offlinetest.Write(t, root, offlinetest.Spec{
		Key: key, Slug: slug, JobID: jobID.String(), Timestamp: ts.Add(24 * time.Hour), Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs: []offlinetest.DB{{Name: "app", Format: pipeline.FormatCustom, Data: []byte("PGDMP three")}},
	})
	data := readTestFile(t, bad.Paths["app"])
	data[0] ^= 1
	writeTestFile(t, bad.Paths["app"], data)
	// A run of another job (unknown slug) is ignored by the job filter.
	offlinetest.Write(t, root, offlinetest.Spec{Key: key, Slug: "unrelated", Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs: []offlinetest.DB{{Name: "x", Format: pipeline.FormatCustom, Data: []byte("PGDMP x")}}})

	cfg := writeConfig(t, "bekup.toml", fmt.Sprintf(`
[database]
url = %q
tls_mode = "verify-full"
tls_ca_file = %q
`, d.AppURL, dbtest.CAFile()))
	destCfg := writeTestFile(t, filepath.Join(t.TempDir(), "dest.toml"), []byte("type = \"disk\"\n[disk]\npath = \""+root+"\"\n"))
	base := []string{"--dev", "--config", cfg, "reindex", "--destination-config", destCfg, "--job-slug", slug, "--import", "--destination-id", destID.String()}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	out, _, err := runIn(t, "", append(base, "--dry-run")...)
	if err == nil || !strings.Contains(out, "would import (2 artifact(s), all verified)") || !strings.Contains(out, "not imported") ||
		count(`SELECT count(*) FROM runs`) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}

	// The tampered run fails verification (non-zero exit) and is not
	// imported; the good run is.
	out, _, err = runIn(t, "", base...)
	if err == nil || strings.Count(out, "imported run") != 1 || !strings.Contains(out, "not imported") {
		t.Fatalf("import: %v\n%s", err, out)
	}
	goodRun := uuid.MustParse(good.Manifest.RunID)
	run, err := q.GetRun(ctx, goodRun)
	if err != nil || run.JobID != jobID || run.Status != sqlc.RunStatusSuccess || run.StorageTimestamp == nil || !run.StorageTimestamp.Equal(ts) ||
		!run.HasWarnings || !strings.Contains(string(run.Notices), "imported") {
		t.Fatalf("run: %+v %v", run, err)
	}
	if n := count(`SELECT count(*) FROM run_items WHERE run_id = $1 AND verified AND status = 'success'`, goodRun); n != 2 {
		t.Fatalf("items: %d", n)
	}
	if n := count(`SELECT count(*) FROM artifacts WHERE run_id = $1 AND verified AND signing_key_id = $2 AND destination_id = $3 AND state = 'present'`,
		goodRun, key.ID, destID); n != 3 {
		t.Fatalf("artifacts: %d", n)
	}
	if n := count(`SELECT count(*) FROM artifacts WHERE run_id = $1 AND kind = 'database' AND encryption_mode = 'recipients' AND recipient_version = 1
		AND object_key = $2`, goodRun, slug+"/2026-10-20T020000Z/app.dump.zst.age"); n != 1 {
		t.Fatalf("artifact key: %d", n)
	}
	if n := count(`SELECT count(*) FROM runs WHERE id = $1`, uuid.MustParse(bad.Manifest.RunID)); n != 0 {
		t.Fatal("tampered run imported")
	}
	if n := count(`SELECT count(*) FROM audit_log WHERE action = 'cli.run.imported' AND actor_kind = 'cli' AND target_id = $1`, goodRun.String()); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	// The imported artifacts pass the manifest binding of downloads and
	// restores (exactly the signed entries are recorded as signed).
	entries, err := q.ListDownloadManifestEntries(ctx, sqlc.ListDownloadManifestEntriesParams{RunID: goodRun, DestinationID: destID, SigningKeyID: key.ID})
	if err != nil || len(entries) != 2 {
		t.Fatalf("binding entries: %d %v", len(entries), err)
	}

	// A second import refuses the duplicate and changes nothing (the
	// tampered run still fails verification).
	if err := os.Remove(bad.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	out, err = runIn2(t, base...)
	if err != nil || !strings.Contains(out, "skipped: offline: run is already in the database") || count(`SELECT count(*) FROM runs`) != 1 {
		t.Fatalf("duplicate: %v\n%s", err, out)
	}

	// A job recreated with a new id (same slug) is refused: restores bind
	// to the manifest's job id.
	other := offlinetest.Write(t, t.TempDir(), offlinetest.Spec{Key: key, Slug: slug, Encryption: pipeline.RecipientEncryption(1, kp.Recipient),
		DBs: []offlinetest.DB{{Name: "app", Format: pipeline.FormatCustom, Data: []byte("PGDMP")}}})
	destCfg2 := writeTestFile(t, filepath.Join(t.TempDir(), "dest.toml"), []byte("type = \"disk\"\n[disk]\npath = \""+other.Root+"\"\n"))
	out, _, err = runIn(t, "", "--dev", "--config", cfg, "reindex", "--destination-config", destCfg2, "--job-slug", slug, "--import",
		"--destination-id", destID.String())
	if err == nil || !strings.Contains(out, "manifest job id differs") {
		t.Fatalf("job mismatch: %v\n%s", err, out)
	}

	// keys export-public writes the database's public keys for verify.
	pubPath := filepath.Join(t.TempDir(), "instance-keys.txt")
	if _, stderr, err := runIn(t, "", "--dev", "--config", cfg, "keys", "export-public", "--out", pubPath); err != nil ||
		!strings.Contains(stderr, crypto.FingerprintBase64(key.Public)) {
		t.Fatalf("export-public: %v\n%s", err, stderr)
	}
	if out, _, err := runIn(t, "", "verify", good.Dir, "--pubkey", pubPath); err != nil || !strings.Contains(out, "result: OK") {
		t.Fatalf("verify with exported keys: %v\n%s", err, out)
	}
	if _, _, err := runIn(t, "", "--dev", "--config", cfg, "keys", "export-public", "--out", pubPath); err == nil {
		t.Fatal("export-public overwrote its output")
	}
	if fi, err := os.Stat(pubPath); err != nil || fi.Size() == 0 {
		t.Fatalf("public keys file: %v", err)
	}
}
