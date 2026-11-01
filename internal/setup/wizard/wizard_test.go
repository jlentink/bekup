package wizard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
)

const (
	testImage   = "ghcr.io/jlentink/bekup:1.2.3@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	genPass     = "generated passphrase for the test ca"
	ownPass     = "my own long passphrase"
	testAppPW   = "app-password-0123456789-abcdef"
	testOwnerPW = "owner-password-0123456789-abcdef"
)

func testEnv(t *testing.T, s *Script) Env {
	t.Helper()
	dir := t.TempDir()
	return Env{
		Dir: dir, DefaultDir: dir, Version: "1.2.3", Image: testImage, DockerGID: "998", UI: s,
		Ownership:          setup.OwnershipNone,
		GeneratePassphrase: func() ([]byte, error) { return []byte(genPass), nil },
		LoadCA:             func(string, string, []byte) error { return nil },
	}
}

// tail are the common key and CA answers.
func tail() []Answer {
	return []Answer{
		{Title: "Master encryption key", Value: choiceGenerate},
		{Title: "Internal CA", Value: choiceGenerate},
		{Title: "CA key passphrase", Value: choiceGenerate},
	}
}

func finish(extra ...Answer) []Answer {
	out := append([]Answer{}, extra...)
	return append(out,
		Answer{Title: "Docker socket group", Value: "998"},
		Answer{Title: "Target database networks", Value: "10.20.0.0/16"},
		Answer{Title: "Target database host names"},
		Answer{Title: "Summary"},
		Answer{Title: "Write the deployment now?", Value: "yes"},
	)
}

func run(t *testing.T, env Env) *InitPlan {
	t.Helper()
	s, ok := env.UI.(*Script)
	if !ok {
		t.Fatal("not a script")
	}
	p, err := RunInit(env)
	if err != nil {
		t.Fatalf("RunInit: %v\nscreens: %v", err, s.Screens)
	}
	if !s.Done() {
		t.Fatalf("unused answers after %v", s.Screens)
	}
	t.Cleanup(p.Destroy)
	return p
}

func TestInitWizardOwnTraefik(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	s.Answers = append([]Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwnTraefik},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
		{Title: "Let's Encrypt staging?", Value: "no"},
		{Title: "Database", Value: "bundled"},
	}, tail()...)
	s.Answers = append(s.Answers, finish(Answer{Title: "Bundled database secrets", Value: "yes"})...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	p := run(t, env)
	o := p.Options
	a := o.Answers
	if a.Variant != setup.VariantTraefik || a.Proxy != setup.ProxyTraefik || a.TLSMode != setup.TLSACME || a.ACMEEmail != "ops@example.com" ||
		a.Domain != "backup.example.com" || a.DockerGID != 998 || a.Image.String() != testImage || a.DB.Mode != "" || a.KeysDir != "secrets" ||
		!slices.Equal(a.TargetCIDRs, []string{"10.20.0.0/16"}) || a.TargetHosts != nil {
		t.Fatalf("answers %+v", a)
	}
	if !o.WriteCompose || o.UseExistingCA || o.ImportMasterKey != "" || o.Ownership != setup.OwnershipNone {
		t.Fatalf("options %+v", o)
	}
	pass, err := o.CAPassphrase()
	if err != nil || string(pass) != genPass {
		t.Fatalf("passphrase %q %v", pass, err)
	}
	if !strings.Contains(s.Text.String(), genPass) || s.Screens[len(s.Screens)-1] != "sensitive: CA key passphrase (shown ONCE)" {
		t.Fatal("the generated passphrase must be shown once, last, on the sensitive screen")
	}
	want := "bekup setup init --non-interactive --ownership none --write-compose --variant traefik --domain backup.example.com --tls-mode acme --acme-email ops@example.com --generate-ca-passphrase --docker-gid 998 --target-cidr 10.20.0.0/16"
	if got := strings.Join(p.Flags, " "); got != want {
		t.Fatalf("flags\n got %s\nwant %s", got, want)
	}
	p.Destroy()
	if pass, err := o.CAPassphrase(); err == nil && string(pass) == genPass {
		t.Fatal("Destroy did not zero the passphrase")
	}
}

func TestInitWizardBackNavigation(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	s.Answers = append([]Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwnTraefik},
		{Title: "Domain", Back: true}, // back to the proxy
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "npm"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Back: true}, // back to the domain (no ACME step for an own proxy)
		{Title: "Domain", Value: "bekup.example.org"},
		{Title: "Database", Value: "bundled"},
	}, tail()...)
	s.Answers = append(s.Answers, finish(Answer{Title: "Bundled database secrets", Value: "yes"})...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	a := run(t, env).Options.Answers
	if a.Variant != setup.VariantOwnProxy || a.Proxy != setup.ProxyExternal || a.Domain != "bekup.example.org" || a.TLSMode != "" || a.ACMEEmail != "" ||
		a.ProxyNetwork != "web" || a.ProxyContainer != "npm" || a.ProxyLabels || a.ProxyMode != "" {
		t.Fatalf("answers %+v", a)
	}
	// Back on the first screen stays there; Ctrl+C aborts.
	s2 := &Script{Answers: []Answer{{Title: "bekup setup", Back: true}}}
	env2 := testEnv(t, s2)
	if _, err := RunInit(env2); !errors.Is(err, ErrAborted) || !slices.Equal(s2.Screens, []string{"note: bekup setup", "note: bekup setup"}) {
		t.Fatalf("back on the first screen: %v %v", err, s2.Screens)
	}
}

func TestInitWizardExternalDatabase(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	writeCA(t, env.Dir)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "caddy"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?", Value: dbRemote},
		{Title: "PostgreSQL host", Value: "10.0.5.4"},
		{Title: "PostgreSQL port", Value: "6543"},
		{Title: "Database name", Value: "bekup_prod"},
		{Title: "TLS to your PostgreSQL", Value: "verify-ca-file"},
		{Title: "CA bundle", Value: "db-ca.pem"},
		{Title: "TLS server name", Value: "db.example.com"},
		{Title: "Roles and database", Value: "existing-roles"},
		{Title: "Password of bekup_app", Value: testAppPW},
		{Title: "Repeat the password of bekup_app", Value: "something else entirely"},
		{Title: "Passwords differ"},
		{Title: "Password of bekup_app", Value: testAppPW},
		{Title: "Repeat the password of bekup_app", Value: testAppPW},
		{Title: "Password of bekup_owner", Value: testOwnerPW},
		{Title: "Repeat the password of bekup_owner", Value: testOwnerPW},
		{Title: "Master encryption key", Value: choiceGenerate},
		{Title: "Internal CA", Value: choiceGenerate},
		{Title: "CA key passphrase", Value: choiceEnter},
		{Title: "New CA key passphrase", Value: ownPass},
		{Title: "Repeat the passphrase", Value: ownPass},
	}
	s.Answers = append(s.Answers, finish()...)
	p := run(t, env)
	a := p.Options.Answers
	want := setup.Database{Mode: setup.DBExternal, Host: "10.0.5.4", Port: 6543, Name: "bekup_prod", CAFile: "db-ca.pem", TLSServerName: "db.example.com", Auth: setup.DBAuthExistingRoles}
	if a.DB != want || a.Variant != setup.VariantOwnProxy || a.Proxy != setup.ProxyExternal || a.ProxyContainer != "caddy" || len(a.TrustedProxies) != 0 {
		t.Fatalf("answers %+v", a)
	}
	for role, pw := range map[string]string{"bekup_app": testAppPW, "bekup_owner": testOwnerPW} {
		if got, err := p.Options.DBPassword(role); err != nil || string(got) != pw {
			t.Fatalf("%s: %q %v", role, got, err)
		}
	}
	if pass, err := p.Options.CAPassphrase(); err != nil || string(pass) != ownPass {
		t.Fatal("entered passphrase not used")
	}
	if strings.Contains(strings.Join(p.Flags, " "), testAppPW) || strings.Contains(s.Text.String(), testAppPW) {
		t.Fatal("a password reached the flags or a screen")
	}
	if got := strings.Join(p.Flags, " "); !strings.Contains(got, "--database external --domain") && !strings.Contains(got, "--db-host 10.0.5.4 --db-port 6543 --db-name bekup_prod --db-ca-file db-ca.pem --db-tls-server-name db.example.com --db-auth existing-roles") {
		t.Fatalf("flags %s", got)
	}
}

func TestInitWizardSingleAccount(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	writeCA(t, env.Dir)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "traefik_web"},
		{Title: "Your proxy's container name", Value: "edge-traefik"},
		{Title: "Traefik Docker labels?", Value: "yes"},
		{Title: "Traefik entrypoint", Value: "https"},
		{Title: "Traefik certificate resolver"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?", Value: dbRemote},
		{Title: "PostgreSQL host", Value: "db.example.com"},
		{Title: "PostgreSQL port"},
		{Title: "Database name"},
		{Title: "TLS to your PostgreSQL"},
		{Title: "CA bundle", Value: "db-ca.pem"},
		{Title: "TLS server name"},
		{Title: "Roles and database", Value: "single-account"},
		{Title: "Single account: an explicit weakening", Value: "no"}, // declined: asked again
		{Title: "Roles and database", Value: "single-account"},
		{Title: "Single account: an explicit weakening", Value: "yes"},
		{Title: "The single account", Value: "acct_bekup"},
		{Title: "Password of acct_bekup", Value: testAppPW},
		{Title: "Repeat the password of acct_bekup", Value: testAppPW},
	}
	s.Answers = append(s.Answers, tail()...)
	s.Answers = append(s.Answers, finish()...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	p := run(t, env)
	d := p.Options.Answers.DB
	if d.Auth != setup.DBAuthSingleAccount || d.User != "acct_bekup" || d.Port != 0 || d.Name != "" || !strings.Contains(s.Text.String(), "rewrite the\naudit history") {
		t.Fatalf("db %+v", d)
	}
	if got, err := p.Options.DBPassword("acct_bekup"); err != nil || string(got) != testAppPW {
		t.Fatalf("account password %q %v", got, err)
	}
	if a := p.Options.Answers; a.TraefikEntrypoint != "https" || a.TraefikCertResolver != "" {
		t.Fatalf("traefik names %q %q", a.TraefikEntrypoint, a.TraefikCertResolver)
	}
	got := strings.Join(p.Flags, " ")
	if !strings.Contains(got, "--proxy-network traefik_web --proxy-container edge-traefik --proxy-labels --traefik-entrypoint https --domain") || strings.Contains(got, "--traefik-certresolver") || !strings.Contains(got, "--db-password-file PASSWORD_FILE") || strings.Contains(got, "OWNER_PASSWORD_FILE") {
		t.Fatalf("flags %s", got)
	}
	if !strings.Contains(p.FlagsNote, "PASSWORD_FILE: a file in the deployment directory") {
		t.Fatalf("note %q", p.FlagsNote)
	}
}

func TestInitWizardBootstrapAndComposeFirst(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	img, err := imageref.ParsePinned(testImage)
	if err != nil {
		t.Fatal(err)
	}
	data, err := setup.RenderRelease(setup.VariantTraefik, setup.DBExternal, &img)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Dir, setup.ComposeFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	writeCA(t, env.Dir)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
		{Title: "Let's Encrypt staging?", Value: "yes"},
		{Title: "Where does your PostgreSQL run?"},
		{Title: "PostgreSQL host", Value: "db.example.com"},
		{Title: "PostgreSQL port"},
		{Title: "Database name"},
		{Title: "TLS to your PostgreSQL"},
		{Title: "CA bundle", Value: "db-ca.pem"},
		{Title: "TLS server name"},
		{Title: "Roles and database", Value: "bootstrap"},
		{Title: "Admin role", Value: "postgres"},
	}
	s.Answers = append(s.Answers, tail()...)
	s.Answers = append(s.Answers, finish()...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	p := run(t, env)
	o := p.Options
	if o.WriteCompose || o.Answers.Variant != setup.VariantTraefik || o.Answers.DB.Mode != setup.DBExternal || o.Answers.DB.Auth != "" || o.Answers.DB.AdminUser != "postgres" || !o.Answers.ACMEStaging {
		t.Fatalf("options %+v", o)
	}
	if !strings.Contains(s.Text.String(), "compose.yaml found: the traefik variant with your own PostgreSQL") {
		t.Fatalf("welcome:\n%s", s.Text.String())
	}
	if got := strings.Join(p.Flags, " "); strings.Contains(got, "--write-compose") || !strings.Contains(got, "--db-admin-user postgres") {
		t.Fatalf("flags %s", got)
	}
	// A mismatching BEKUP_IMAGE refuses before the first question.
	env.Image = strings.Replace(testImage, "0123", "4567", 1)
	if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "does not match the image of compose.yaml") {
		t.Fatalf("image mismatch: %v", err)
	}
}

func TestInitWizardConsentPaths(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	k, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Destroy()
	kdata, err := k.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Dir, "old_master_key"), kdata, 0o600); err != nil {
		t.Fatal(err)
	}
	tries := 0
	env.LoadCA = func(_, _ string, p []byte) error {
		tries++
		if string(p) != ownPass {
			return errors.New("wrong passphrase")
		}
		return nil
	}
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "npm"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "bundled"},
		{Title: "Master encryption key", Value: choiceImport},
		{Title: "Key file", Value: "old_master_key"},
		{Title: "Key-check value", Value: k.KeyCheckValue()},
		{Title: "Key imported"},
		{Title: "Internal CA", Value: choiceExisting},
		{Title: "No CA mounted"}, // nothing mounted yet: back to the CA choice
		{Title: "Internal CA", Value: choiceExisting},
		{Title: "Passphrase of the existing CA key", Value: "a wrong passphrase!"},
		{Title: "Wrong passphrase?"},
		{Title: "Passphrase of the existing CA key", Value: ownPass},
		{Title: "Bundled database secrets", Value: "no"},
		{Title: "Needed for the bundled database"}, // back to the CA step
		{Title: "Internal CA", Value: choiceExisting},
		{Title: "Passphrase of the existing CA key", Value: ownPass},
		{Title: "Bundled database secrets", Value: "yes"},
	}
	s.Answers = append(s.Answers, finish()...)
	// Mount the CA after the "No CA mounted" screen.
	ui := &mountingScript{Script: s, dir: env.Dir, after: "No CA mounted"}
	env.UI = ui
	p, err := RunInit(env)
	if err != nil {
		t.Fatalf("%v\n%v", err, s.Screens)
	}
	defer p.Destroy()
	o := p.Options
	if o.ImportMasterKey != "old_master_key" || o.MasterKeyCheckValue != k.KeyCheckValue() || !o.UseExistingCA || tries != 3 {
		t.Fatalf("options %+v tries %d", o, tries)
	}
	if !strings.Contains(s.Text.String(), "Primary key ID "+k.PrimaryID().String()) {
		t.Error("imported key ID not shown")
	}
	if got := strings.Join(p.Flags, " "); !strings.Contains(got, "--import-master-key old_master_key --master-key-check-value kcv1:") || !strings.Contains(got, "--use-existing-ca --ca-passphrase-stdin") {
		t.Fatalf("flags %s", got)
	}
}

// mountingScript mounts a CA (writes the two files) after the screen
// titled after.
type mountingScript struct {
	*Script
	dir, after string
}

func (m *mountingScript) Note(title, body string) error {
	err := m.Script.Note(title, body)
	if title == m.after {
		ca := filepath.Join(m.dir, "secrets", "certs", "ca")
		if err := os.MkdirAll(ca, 0o700); err != nil {
			return err
		}
		for _, f := range []string{"ca.crt", "ca.key"} {
			if err := os.WriteFile(filepath.Join(ca, f), []byte("x"), 0o600); err != nil {
				return err
			}
		}
	}
	return err
}

// writeCA writes a database CA bundle as db-ca.pem below dir.
func writeCA(t *testing.T, dir string) {
	t.Helper()
	ca, err := certs.NewCA(certs.CAOptions{ServerNames: []string{"db.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	clear(ca.Key)
	if err := os.WriteFile(filepath.Join(dir, "db-ca.pem"), ca.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlainInterface(t *testing.T) {
	// Answers in order: the note, an invalid then a valid choice, back on
	// an input, a refused and an accepted input, yes, an empty secret
	// (back), a secret and the sensitive screen.
	in := strings.NewReader(strings.Join([]string{
		"",
		"9", "2",
		"<",
		"bad host!", "ok.ex",
		"y",
		"",
		"a secret one",
		"",
	}, "\n") + "\n")
	var out bytes.Buffer
	p := NewPlain(in, &out, nil)
	if err := p.Note("Hello", "body"); err != nil {
		t.Fatal(err)
	}
	if v, err := p.Select("Pick", "", []Option{{"a", "A"}, {"b", "B"}}, "a"); err != nil || v != "b" {
		t.Fatalf("select %q %v", v, err)
	}
	if _, err := p.Input("Host", "", "", nil); !errors.Is(err, ErrBack) {
		t.Fatalf("back: %v", err)
	}
	v, err := p.Input("Host", "", "", func(s string) error {
		if strings.ContainsAny(s, " !") {
			return errors.New("no spaces")
		}
		return nil
	})
	if err != nil || v != "ok.ex" {
		t.Fatalf("input %q %v", v, err)
	}
	if ok, err := p.Confirm("Sure", "", false); err != nil || !ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
	if _, err := p.Secret("Pass", "", nil); !errors.Is(err, ErrBack) {
		t.Fatalf("empty secret: %v", err)
	}
	if b, err := p.Secret("Pass", "", nil); err != nil || string(b) != "a secret one" {
		t.Fatalf("secret %q %v", b, err)
	}
	if err := p.Sensitive("Shown once", []byte("xyz"), "body"); err != nil {
		t.Fatal(err)
	}
	if err := p.Note("End", ""); !errors.Is(err, ErrAborted) {
		t.Fatalf("end of input must abort: %v", err)
	}
	text := out.String()
	for _, want := range []string{"  1) A (default)", "  2) B", "error: type the number", "error: no spaces", "[y/N, <: back]"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Error("the plain interface printed an escape sequence")
	}
}

func TestInitWizardSamePasswordsAskedAgain(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	writeCA(t, env.Dir)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "npm"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?"},
		{Title: "PostgreSQL host", Value: "db.example.com"},
		{Title: "PostgreSQL port"},
		{Title: "Database name"},
		{Title: "TLS to your PostgreSQL"},
		{Title: "CA bundle", Value: "db-ca.pem"},
		{Title: "TLS server name"},
		{Title: "Roles and database", Value: "existing-roles"},
		{Title: "Password of bekup_app", Value: testAppPW},
		{Title: "Repeat the password of bekup_app", Value: testAppPW},
		{Title: "Password of bekup_owner", Value: testAppPW},
		{Title: "Repeat the password of bekup_owner", Value: testAppPW},
		{Title: "Same password"}, // both are asked again
		{Title: "Password of bekup_app", Value: testAppPW},
		{Title: "Repeat the password of bekup_app", Value: testAppPW},
		{Title: "Password of bekup_owner", Value: testOwnerPW},
		{Title: "Repeat the password of bekup_owner", Value: testOwnerPW},
	}
	s.Answers = append(s.Answers, tail()...)
	s.Answers = append(s.Answers, finish()...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	p := run(t, env)
	for role, pw := range map[string]string{"bekup_app": testAppPW, "bekup_owner": testOwnerPW} {
		if got, err := p.Options.DBPassword(role); err != nil || string(got) != pw {
			t.Fatalf("%s: %q %v", role, got, err)
		}
	}
	got := strings.Join(p.Flags, " ")
	if !strings.Contains(got, "--db-password-file PASSWORD_FILE --db-owner-password-file OWNER_PASSWORD_FILE") || !strings.Contains(p.FlagsNote, "OWNER_PASSWORD_FILE") {
		t.Fatalf("flags %s / %s", got, p.FlagsNote)
	}
}

func TestInitWizardComposeFirstVersion(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	img, err := imageref.ParsePinned(testImage) // tag 1.2.3
	if err != nil {
		t.Fatal(err)
	}
	data, err := setup.RenderRelease(setup.VariantOwnProxy, setup.DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Dir, setup.ComposeFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	env.Version = "1.3.0"
	if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "compose.yaml") {
		t.Fatalf("version mismatch of compose.yaml accepted: %v", err)
	}
	// Allowed: the questions start; the flag line carries the flag.
	env.AllowVersionMismatch = true
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "npm"},
		{Title: "Traefik Docker labels?"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Master encryption key", Value: choiceGenerate},
		{Title: "Internal CA", Value: choiceGenerate},
		{Title: "CA key passphrase", Value: choiceGenerate},
		{Title: "Bundled database secrets", Value: "yes"},
	}
	s.Answers = append(s.Answers, finish()...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	p := run(t, env)
	if got := strings.Join(p.Flags, " "); !strings.Contains(got, "--allow-version-mismatch") || strings.Contains(got, "--write-compose") || strings.Contains(got, "--dir") {
		t.Fatalf("flags %s", got)
	}
	env.DefaultDir = "/deploy"
	s.Screens, s.next = nil, 0
	p = run(t, env)
	if got := strings.Join(p.Flags, " "); !strings.Contains(got, "--dir "+env.Dir) {
		t.Fatalf("flags without --dir: %s", got)
	}
}

func TestInitWizardPortAndPlainLabel(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	writeCA(t, env.Dir)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "npm"},
		{Title: "Traefik Docker labels?"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?"},
		{Title: "PostgreSQL host", Value: "db.example.com"},
		{Title: "PostgreSQL port", Value: "0"},
	}
	if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
		t.Fatalf("port 0: %v", err)
	}
	// The plain interface warns that a generated passphrase stays in the
	// scrollback.
	in := strings.NewReader("\n")
	var out bytes.Buffer
	pl := NewPlain(in, &out, nil)
	if hidesSensitive(pl) || !hidesSensitive(s) {
		t.Fatal("HidesSensitive")
	}
	st := &initState{env: env, ui: pl, caMode: choiceGenerate}
	st.env.UI = pl
	if err := st.ca(); err == nil {
		t.Fatal("expected the end of input")
	}
	if !strings.Contains(out.String(), "scrollback") {
		t.Fatalf("no scrollback warning:\n%s", out.String())
	}
}

func TestNewPassphraseAskedTwice(t *testing.T) {
	s := &Script{Answers: []Answer{
		{Title: "New CA key passphrase", Value: ownPass},
		{Title: "Repeat the passphrase", Value: ownPass + "x"},
		{Title: "Passphrases differ"},
		{Title: "New CA key passphrase", Value: ownPass},
		{Title: "Repeat the passphrase", Value: ownPass},
	}}
	pass, err := NewPassphrase(s, "body")
	if err != nil || string(pass) != ownPass || !s.Done() {
		t.Fatalf("%q %v %v", pass, err, s.Screens)
	}
}

// D-212: a followed tag gets one explanation screen; following is the
// default, pinning a digest asks for it.
func TestInitWizardFollowLatest(t *testing.T) {
	const latest = "ghcr.io/jlentink/bekup:latest"
	head := func(mode ...Answer) []Answer {
		out := append([]Answer{{Title: "bekup setup"}}, mode...)
		out = append(out,
			Answer{Title: "Reverse proxy", Value: proxyOwnTraefik},
			Answer{Title: "Domain", Value: "backup.example.com"},
			Answer{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
			Answer{Title: "Let's Encrypt staging?", Value: "no"},
			Answer{Title: "Database", Value: "bundled"},
		)
		out = append(out, tail()...)
		out = append(out, finish(Answer{Title: "Bundled database secrets", Value: "yes"})...)
		return append(out, Answer{Title: "CA key passphrase (shown ONCE)"})
	}
	t.Run("follow", func(t *testing.T) {
		s := &Script{}
		env := testEnv(t, s)
		env.Image = latest
		s.Answers = head(Answer{Title: "Image updates", Value: imageFollow})
		p := run(t, env)
		if a := p.Options.Answers; a.Image.String() != latest || !a.Image.IsMutable() {
			t.Fatalf("image %v", a.Image)
		}
		if slices.Contains(p.Flags, "--image") {
			t.Fatalf("flags %v", p.Flags)
		}
		if !strings.Contains(s.Text.String(), "Whoever moves the tag decides") || !strings.Contains(s.Text.String(), "NOT pinned by digest") {
			t.Fatal("the explanation or the summary note is missing")
		}
	})
	t.Run("pin", func(t *testing.T) {
		s := &Script{}
		env := testEnv(t, s)
		env.Image = latest
		s.Answers = head(Answer{Title: "Image updates", Value: imagePin}, Answer{Title: "Pinned bekup image", Value: testImage})
		p := run(t, env)
		if a := p.Options.Answers; a.Image.String() != testImage {
			t.Fatalf("image %v", a.Image)
		}
		if i := slices.Index(p.Flags, "--image"); i < 0 || p.Flags[i+1] != testImage {
			t.Fatalf("flags %v", p.Flags)
		}
		// The setup container has no Docker access: the digest lookup
		// runs in a second terminal on the host.
		if txt := s.Text.String(); !strings.Contains(txt, "second terminal") ||
			!strings.Contains(txt, "docker image inspect --format '{{index .RepoDigests 0}}' "+latest) {
			t.Fatalf("pin screen lacks the host lookup:\n%s", txt)
		}
	})
	t.Run("digest: no question", func(t *testing.T) {
		s := &Script{}
		env := testEnv(t, s)
		s.Answers = head()
		run(t, env)
		for _, sc := range s.Screens {
			if strings.Contains(sc, "Image updates") {
				t.Fatalf("asked about image updates for a digest: %v", s.Screens)
			}
		}
	})
}

// D-215: own reverse proxy on this host (network + container name), a
// database container on its own Docker network, and a weak TLS choice that
// needs an explicit confirmation (declined first: asked again).
func TestInitWizardOwnProxyAndDatabaseContainer(t *testing.T) {
	s := &Script{}
	env := testEnv(t, s)
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "bridge"}, // refused: Docker's default bridge
	}
	if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "predefined") {
		t.Fatalf("default bridge: %v", err)
	}
	// D-215 review M3: bekup's own names and dotted names are refused.
	for _, bad := range []string{"serve", "bekup-serve", "npm.app"} {
		s.Answers = []Answer{
			{Title: "bekup setup"},
			{Title: "Reverse proxy", Value: proxyOwn},
			{Title: "Your proxy's Docker network", Value: "web"},
			{Title: "Your proxy's container name", Value: bad},
		}
		s.Screens, s.next = nil, 0
		if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "proxy-container") {
			t.Fatalf("container %q: %v", bad, err)
		}
	}
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "caddy"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?", Value: dbHere},
		{Title: "The database's Docker network", Value: "web"}, // the proxy's: refused
	}
	s.Screens, s.next = nil, 0
	if _, err := RunInit(env); err == nil || !strings.Contains(err.Error(), "your proxy's network") {
		t.Fatalf("same network: %v", err)
	}
	s.Answers = []Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: proxyOwn},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "caddy"},
		{Title: "Traefik Docker labels?", Value: "no"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?", Value: dbHere},
		{Title: "The database's Docker network", Value: "dbnet"},
		{Title: "The database's container name", Value: "postgres"},
		{Title: "PostgreSQL port"},
		{Title: "Database name"},
		{Title: "TLS to your PostgreSQL", Value: "require"},
		{Title: "An explicit weakening", Value: "no"},
		{Title: "TLS to your PostgreSQL", Value: "disable"},
		{Title: "An explicit weakening", Value: "yes"},
		{Title: "Roles and database", Value: "bootstrap"},
		{Title: "Admin role"},
	}
	s.Answers = append(s.Answers, tail()...)
	s.Answers = append(s.Answers, finish()...)
	s.Answers = append(s.Answers, Answer{Title: "CA key passphrase (shown ONCE)"})
	s.Screens, s.next = nil, 0
	p := run(t, env)
	a := p.Options.Answers
	if a.Variant != setup.VariantOwnProxy || a.ProxyNetwork != "web" || a.ProxyContainer != "caddy" || a.ProxyLabels {
		t.Fatalf("proxy answers %+v", a)
	}
	want := setup.Database{Mode: setup.DBExternal, Location: setup.DBDocker, Network: "dbnet", Host: "postgres", TLS: setup.DBTLSDisable}
	if a.DB != want {
		t.Fatalf("database %+v", a.DB)
	}
	text := s.Text.String()
	for _, w := range []string{"CAP_NET_RAW", "Prefer require over disable", "--allow-insecure-admin-connection", "point it at http://bekup-serve:8080", "DB TLS:     disable (WEAKENED)"} {
		if !strings.Contains(text, w) {
			t.Errorf("screens lack %q", w)
		}
	}
	got := strings.Join(p.Flags, " ")
	if !strings.Contains(got, "--proxy-network web --proxy-container caddy") || !strings.Contains(got, "--db-location docker --db-network dbnet --db-host postgres") ||
		!strings.Contains(got, "--db-tls disable") || strings.Contains(got, "--db-ca-file") {
		t.Fatalf("flags %s", got)
	}
}
