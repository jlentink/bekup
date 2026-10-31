package main

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

const (
	wizAppPW   = "app-password-0123456789-abcdef"
	wizOwnerPW = "owner-password-0123456789-abcdef"
	wizPass    = "correct horse battery staple x"
)

// wizardCase is one combination of wizard answers; files are put into the
// deployment directory first (the CA bundle, password files for the flag
// run).
type wizardCase struct {
	name    string
	answers []wizard.Answer
	// files maps a relative path to its content.
	files map[string]string
	// flagExtra are the flags the flag run needs for what the wizard asked
	// interactively (password files).
	flagExtra []string
	// stdin feeds an entered CA passphrase to the flag run.
	stdin string
	// real runs setup.Init for both and compares the files.
	real bool
}

func commonTail(passMode string) []wizard.Answer {
	out := []wizard.Answer{
		{Title: "Master encryption key", Value: "generate"},
		{Title: "Internal CA", Value: "generate"},
		{Title: "CA key passphrase", Value: passMode},
	}
	if passMode == "enter" {
		out = append(out, wizard.Answer{Title: "New CA key passphrase", Value: wizPass}, wizard.Answer{Title: "Repeat the passphrase", Value: wizPass})
	}
	return out
}

func commonEnd(bundled, generated bool) []wizard.Answer {
	var out []wizard.Answer
	if bundled {
		out = append(out, wizard.Answer{Title: "Bundled database secrets", Value: "yes"})
	}
	out = append(out,
		wizard.Answer{Title: "Docker socket group", Value: "999"},
		wizard.Answer{Title: "Target database networks", Value: "10.20.0.0/16, 10.30.0.0/16"},
		wizard.Answer{Title: "Target database host names", Value: "db.internal.example.com"},
		wizard.Answer{Title: "Summary"},
		wizard.Answer{Title: "Write the deployment now?", Value: "yes"},
	)
	if generated {
		out = append(out, wizard.Answer{Title: "CA key passphrase (shown ONCE)"})
	}
	return out
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	ca, err := certs.NewCA(certs.CAOptions{ServerNames: []string{"db.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	clear(ca.Key)
	return string(ca.CertPEM)
}

// composeFile renders a release compose file with the test image.
func composeFile(t *testing.T, v setup.Variant, db setup.DBMode) string {
	t.Helper()
	img, err := imageref.ParsePinned(setupTestImage)
	if err != nil {
		t.Fatal(err)
	}
	data, err := setup.RenderRelease(v, db, &img)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mergeFiles(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		maps.Copy(out, m)
	}
	return out
}

// existingCA returns the CA files of an earlier deployment (passphrase
// wizPass) as they appear on mounted media at secrets/certs/ca.
func existingCA(t *testing.T) map[string]string {
	t.Helper()
	src := t.TempDir()
	if _, stderr, err := runIn(t, wizPass+"\n", setupArgs(src)...); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	out := map[string]string{}
	for _, f := range []string{"ca.crt", "ca.key"} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(src, "secrets", "certs", "ca", f)))
		if err != nil {
			t.Fatal(err)
		}
		out["secrets/certs/ca/"+f] = string(b)
	}
	return out
}

func wizardCases(t *testing.T) []wizardCase {
	ca := testCAPEM(t)
	k, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Destroy)
	kdata, err := k.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	keyring, kcv := string(kdata), k.KeyCheckValue()
	caFiles := existingCA(t)
	cat := func(parts ...[]wizard.Answer) []wizard.Answer {
		var out []wizard.Answer
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	extDB := func(auth string) []wizard.Answer {
		return []wizard.Answer{
			{Title: "Database", Value: "external"},
			{Title: "PostgreSQL host", Value: "db.example.com"},
			{Title: "PostgreSQL port", Value: "5433"},
			{Title: "Database name"},
			{Title: "CA bundle", Value: "db-ca.pem"},
			{Title: "TLS server name"},
			{Title: "Roles and database", Value: auth},
		}
	}
	return []wizardCase{
		{
			name: "own-traefik-bundled-generated",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "own-traefik"},
				{Title: "Domain", Value: "backup.example.com"},
				{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
				{Title: "Let's Encrypt staging?", Value: "yes"},
				{Title: "Database", Value: "bundled"},
			}, commonTail("generate"), commonEnd(true, true)),
			real: true,
		},
		{
			name: "existing-traefik-bundled-entered",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "existing-traefik"},
				{Title: "Traefik entrypoint", Value: "https"},
				{Title: "Traefik certificate resolver", Value: "le"},
				{Title: "Domain", Value: "backup.example.com"},
				{Title: "Database", Value: "bundled"},
			}, commonTail("enter"), commonEnd(true, false)),
			stdin: wizPass + "\n",
		},
		{
			name: "compose-first-traefik-external",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Traefik entrypoint"},
				{Title: "Traefik certificate resolver", Value: "myresolver"},
				{Title: "Domain", Value: "backup.example.com"},
			}, commonTail("generate"), commonEnd(true, true)),
			files: map[string]string{setup.ComposeFile: composeFile(t, setup.VariantTraefikExternal, setup.DBBundled)},
			real:  true,
		},
		{
			name: "import-master-key-existing-ca",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "npm"},
				{Title: "Domain", Value: "backup.example.com"},
				{Title: "Database", Value: "bundled"},
				{Title: "Master encryption key", Value: "import"},
				{Title: "Key file", Value: "old_master_key"},
				{Title: "Key-check value", Value: kcv},
				{Title: "Key imported"},
				{Title: "Internal CA", Value: "existing"},
				{Title: "Passphrase of the existing CA key", Value: wizPass},
			}, commonEnd(true, false)),
			files: mergeFiles(map[string]string{"old_master_key": keyring}, caFiles),
			stdin: wizPass + "\n",
			real:  true,
		},
		{
			name: "npm-external-bootstrap",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "npm"},
				{Title: "Domain", Value: "backup.example.com"},
			}, extDB("bootstrap"), []wizard.Answer{{Title: "Admin role", Value: "postgres"}}, commonTail("enter"), commonEnd(false, false)),
			files: map[string]string{"db-ca.pem": ca},
			stdin: wizPass + "\n",
			real:  true,
		},
		{
			name: "none-external-existing-roles",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "none"},
				{Title: "Your proxy's address", Value: "10.213.250.6/32"},
				{Title: "Domain", Value: "backup.example.com"},
			}, extDB("existing-roles"), []wizard.Answer{
				{Title: "Password of bekup_app", Value: wizAppPW},
				{Title: "Repeat the password of bekup_app", Value: wizAppPW},
				{Title: "Password of bekup_owner", Value: wizOwnerPW},
				{Title: "Repeat the password of bekup_owner", Value: wizOwnerPW},
			}, commonTail("generate"), commonEnd(false, true)),
			files:     map[string]string{"db-ca.pem": ca, "app.pw": wizAppPW, "owner.pw": wizOwnerPW},
			flagExtra: []string{"--db-password-file", "app.pw", "--db-owner-password-file", "owner.pw"},
			real:      true,
		},
		{
			name: "own-traefik-external-single-account",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "own-traefik"},
				{Title: "Domain", Value: "backup.example.com"},
				{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
				{Title: "Let's Encrypt staging?", Value: "no"},
			}, extDB("single-account"), []wizard.Answer{
				{Title: "Single account: an explicit weakening", Value: "yes"},
				{Title: "The single account", Value: "acct_bekup"},
				{Title: "Password of acct_bekup", Value: wizAppPW},
				{Title: "Repeat the password of acct_bekup", Value: wizAppPW},
			}, commonTail("generate"), commonEnd(false, true)),
			files:     map[string]string{"db-ca.pem": ca, "app.pw": wizAppPW},
			flagExtra: []string{"--db-password-file", "app.pw"},
		},
		{
			name: "npm-external-dba-sql",
			answers: cat([]wizard.Answer{
				{Title: "bekup setup"},
				{Title: "Reverse proxy", Value: "npm"},
				{Title: "Domain", Value: "backup.example.com"},
			}, extDB("dba-sql"), commonTail("generate"), commonEnd(false, true)),
			files: map[string]string{"db-ca.pem": ca},
		},
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// normalize drops what legitimately differs between the two runs: the
// output writer, the callbacks and the password sources.
func normalize(o setup.Options) setup.Options {
	o.Out, o.CAPassphrase, o.DBPassword = nil, nil, nil
	o.Answers.DB.PasswordFile, o.Answers.DB.OwnerPasswordFile = "", ""
	return o
}

// TestSetupWizardMatchesFlags: for every combination of answers the wizard
// produces the options of the equivalent `setup init` flags (InitPlan.Flags),
// and for some of them setup.Init writes byte-identical non-secret files.
func TestSetupWizardMatchesFlags(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", setupTestImage)
	t.Setenv("DOCKER_GID", "")
	oldTerm, oldInit := stderrIsTerminal, setupInit
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal, setupInit = oldTerm, oldInit })
	for _, tc := range wizardCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			dirW, dirF := t.TempDir(), t.TempDir()
			writeFiles(t, dirW, tc.files)
			writeFiles(t, dirF, tc.files)
			script := &wizard.Script{Answers: tc.answers}
			plan, err := wizard.RunInit(wizard.Env{
				Dir: dirW, Version: version, Image: setupTestImage, AllowVersionMismatch: true,
				Ownership: setup.OwnershipNone, UI: script,
			})
			if err != nil {
				t.Fatalf("wizard: %v\n%v", err, script.Screens)
			}
			defer plan.Destroy()
			if !script.Done() {
				t.Fatalf("unused answers after %v", script.Screens)
			}
			args := append(append([]string{"setup", "init"}, plan.Flags[3:]...), "--dir", dirF, "--ownership", "none", "--allow-version-mismatch")
			args = append(args, tc.flagExtra...)
			var flagOpts setup.Options
			setupInit = func(o setup.Options) error { flagOpts = o; return nil }
			if _, stderr, err := runIn(t, tc.stdin, args...); err != nil {
				t.Fatalf("flags %v: %v\n%s", args, err, stderr)
			}
			w, f := normalize(plan.Options), normalize(flagOpts)
			w.Dir, f.Dir = "", ""
			if !reflect.DeepEqual(w, f) {
				t.Fatalf("wizard and flags differ:\nwizard %+v\nflags  %+v", w, f)
			}
			if !tc.real {
				return
			}
			// Both write the same deployment (secrets aside).
			setupInit = setup.Init
			wo := plan.Options
			wo.Out = &bytes.Buffer{}
			if err := setup.Init(wo); err != nil {
				t.Fatalf("wizard init: %v", err)
			}
			if _, stderr, err := runIn(t, tc.stdin, args...); err != nil {
				t.Fatalf("flag init: %v\n%s", err, stderr)
			}
			compareDeployments(t, dirW, dirF)
		})
	}
}

// compareDeployments requires the same paths and modes everywhere and the
// same content outside the secrets (the CA bundle copy included).
func compareDeployments(t *testing.T, a, b string) {
	t.Helper()
	walk := func(root string) map[string]string {
		out := map[string]string{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			fi, err := d.Info()
			if err != nil {
				return err
			}
			v := fi.Mode().String()
			if d.Type().IsRegular() && (!strings.HasPrefix(rel, "secrets/") || rel == "secrets/bekup_database_ca.crt") {
				data, err := os.ReadFile(filepath.Clean(p)) //nolint:gosec // G122: a test tree below t.TempDir()
				if err != nil {
					return err
				}
				if rel == setup.StateFileName {
					// The flag run copies password files the wizard asked
					// for: only it records them as imported.
					st, err := setup.ParseState(data)
					if err != nil {
						return err
					}
					st.ImportedFiles = nil
					if data, err = st.Marshal(); err != nil {
						return err
					}
				}
				v += "|" + string(data)
			}
			out[rel] = v
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	wa, wb := walk(a), walk(b)
	for k, v := range wa {
		if wb[k] != v {
			t.Errorf("%s differs between the wizard and the flag run", k)
		}
	}
	for k := range wb {
		if _, ok := wa[k]; !ok {
			t.Errorf("%s only in the flag run", k)
		}
	}
}

// TestSetupWizardCommand: `bekup setup` runs the wizard on a terminal and
// refuses without one; in an existing deployment it offers the hub.
func TestSetupWizardCommand(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", setupTestImage)
	t.Setenv("DOCKER_GID", "999")
	oldUI, oldInit := setupUI, setupInit
	t.Cleanup(func() { setupUI, setupInit = oldUI, oldInit })
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return nil, false }
	if _, _, err := run(t, "setup", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("no terminal: %v", err)
	}
	script := &wizard.Script{Answers: wizardCases(t)[0].answers}
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return script, true }
	var got setup.Options
	setupInit = func(o setup.Options) error { got = o; return nil }
	dir := t.TempDir()
	if _, _, err := run(t, "setup", "--dir", dir, "--ownership", "none", "--allow-version-mismatch"); err != nil {
		t.Fatalf("wizard: %v\n%v", err, script.Screens)
	}
	if got.Dir != dir || got.Answers.Variant != setup.VariantTraefik || got.Answers.DockerGID != 999 || !got.WriteCompose {
		t.Fatalf("options %+v", got)
	}
	// An existing deployment: the hub (quit changes nothing).
	if err := os.WriteFile(filepath.Join(dir, setup.StateFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := &wizard.Script{Answers: []wizard.Answer{{Title: "bekup setup", Value: "quit"}}}
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return hub, true }
	if _, _, err := run(t, "setup", "--dir", dir); err != nil || !hub.Done() || hub.Screens[0] != "select: bekup setup" {
		t.Fatalf("hub: %v %v", err, hub.Screens)
	}
}

// TestSetupRenewAndGuidedUpgradeCLI: renew with flags (list, all, bad
// phase) and the guided upgrade's backup question.
func TestSetupRenewAndGuidedUpgradeCLI(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	oldUI := setupUI
	t.Cleanup(func() { setupUI = oldUI })
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return nil, false }
	dir := t.TempDir()
	if _, stderr, err := runIn(t, wizPass+"\n", setupArgs(dir)...); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	out, _, err := run(t, "setup", "renew", "--dir", dir, "--list", "--ownership", "none")
	if err != nil || !strings.Contains(out, "CA rotation: none") || !strings.Contains(out, "secrets/certs/postgres/postgres.crt") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if out, _, err = run(t, "setup", "renew", "--dir", dir, "--ownership", "none", "--non-interactive"); err != nil || !strings.Contains(out, "Nothing to renew") {
		t.Fatalf("nothing due: %v\n%s", err, out)
	}
	before, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "secrets", "certs", "serve", "serve.crt")))
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runIn(t, wizPass+"\n", "setup", "renew", "--dir", dir, "--all", "--ca-passphrase-stdin", "--ownership", "none")
	if err != nil || !strings.Contains(out, "--force-recreate runner serve postgres") {
		t.Fatalf("renew --all: %v\n%s%s", err, out, stderr)
	}
	if after, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "secrets", "certs", "serve", "serve.crt"))); err != nil || bytes.Equal(before, after) {
		t.Fatal("serve certificate not renewed")
	}
	if _, _, err := run(t, "setup", "renew", "--dir", dir, "--ca", "rotate"); err == nil || !strings.Contains(err.Error(), "--ca must be stage, promote or finish") {
		t.Fatalf("bad phase: %v", err)
	}
	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err != nil {
		t.Fatalf("check after renew: %v\n%s", err, out)
	}
	// Guided upgrade on a "terminal": no backup, no change.
	script := &wizard.Script{Answers: []wizard.Answer{{Title: "Upgrade"}, {Title: "Backup", Value: "no"}}}
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return script, true }
	_, _, err = run(t, "setup", "upgrade", "--dir", dir, "--write-compose", "--image", setupTestImage, "--allow-version-mismatch", "--ownership", "none")
	if err == nil || !strings.Contains(err.Error(), "back up first") {
		t.Fatalf("guided upgrade without backup: %v %v", err, script.Screens)
	}
	if out, _, err := run(t, "setup", "upgrade", "--dir", dir, "--write-compose", "--image", setupTestImage, "--allow-version-mismatch", "--ownership", "none", "--non-interactive"); err != nil || !strings.Contains(out, "no changes") {
		t.Fatalf("non-interactive upgrade: %v\n%s", err, out)
	}
}

// TestSetupHubUpgrade: the hub's upgrade passes --allow-version-mismatch,
// labels a setup-first deployment correctly and asks a new CA passphrase
// twice.
func TestSetupHubUpgrade(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", setupTestImage)
	oldUI, oldUp := setupUI, setupUpgrade
	t.Cleanup(func() { setupUI, setupUpgrade = oldUI, oldUp })
	dir := t.TempDir()
	if _, stderr, err := runIn(t, wizPass+"\n", setupArgs(dir)...); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	hub := &wizard.Script{Answers: []wizard.Answer{
		{Title: "bekup setup", Value: "upgrade"},
		{Title: "New CA key passphrase", Value: wizPass},
		{Title: "Repeat the passphrase", Value: wizPass + "!"},
		{Title: "Passphrases differ"},
		{Title: "New CA key passphrase", Value: wizPass},
		{Title: "Repeat the passphrase", Value: wizPass},
	}}
	setupUI = func(*cobra.Command, bool) (wizard.UI, bool) { return hub, true }
	var got setup.UpgradeOptions
	setupUpgrade = func(o setup.UpgradeOptions) error {
		got = o
		pass, err := o.CAPassphrase()
		if err != nil || string(pass) != wizPass {
			t.Errorf("passphrase %q %v", pass, err)
		}
		return nil
	}
	if _, _, err := run(t, "setup", "--dir", dir, "--allow-version-mismatch", "--ownership", "none"); err != nil {
		t.Fatalf("hub: %v %v", err, hub.Screens)
	}
	if !got.AllowVersionMismatch || !got.WriteCompose || !hub.Done() {
		t.Fatalf("upgrade options %+v, screens %v", got, hub.Screens)
	}
	if !strings.Contains(hub.Text.String(), "setup rewrites its compose.yaml") {
		t.Fatalf("label:\n%s", hub.Text.String())
	}
}

func TestSetupInitDBPortZero(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	dir := t.TempDir()
	_, _, err := runIn(t, wizPass+"\n", setupArgs(dir, "--database", "external", "--db-host", "db.example.com", "--db-port", "0")...)
	if err == nil || !strings.Contains(err.Error(), "--db-port must be between 1 and 65535") {
		t.Fatalf("port 0: %v", err)
	}
}
