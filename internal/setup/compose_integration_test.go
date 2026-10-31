//go:build integration

package setup

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// TestIntegrationComposeConfig writes a deployment directory per proxy
// choice (setup-first) and per release variant (compose-first, D-205), each
// with the bundled and an external database (D-208: all six release and
// committed files), and lets docker compose validate it with every profile
// enabled.
func TestIntegrationComposeConfig(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		dockertest.Skipf(t, "docker CLI not found: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "compose", "version").CombinedOutput(); err != nil {
		dockertest.Skipf(t, "docker compose not available: %v: %s", err, out)
	}
	variants := map[string]func() Answers{
		"traefik-acme": func() Answers { return testAnswers(t, ProxyTraefik) },
		"nginx-static": func() Answers {
			a := testAnswers(t, ProxyNginx)
			a.TLSMode, a.ACMEEmail = TLSStatic, ""
			return a
		},
		"traefik-static": func() Answers {
			a := testAnswers(t, ProxyTraefik)
			a.TLSMode, a.ACMEEmail = TLSStatic, ""
			return a
		},
		"none-custom-network": func() Answers {
			a := testAnswers(t, ProxyNone)
			a.KeysDir = "vault/keys"
			a.Network = Network{RunnerCtlSubnet: "172.30.2.0/24", RunnerCtlIP: "172.30.2.10"}
			return a
		},
	}
	type tc struct {
		answers func() Answers
		variant Variant // compose-first release file; "" = setup-first
		// placeholder: the committed file (deploy/compose/), image from
		// BEKUP_IMAGE in .env (D-205 amendment 3).
		placeholder bool
		db          DBMode
	}
	cases := map[string]tc{}
	for name, mk := range variants {
		cases["setup-first-"+name] = tc{answers: mk}
	}
	cases["setup-first-external-db"] = tc{answers: func() Answers { return testAnswers(t, ProxyNginx) }, db: DBExternal}
	cases["setup-first-external-db-single-account"] = tc{answers: func() Answers { return testAnswers(t, ProxyNone) }, db: DBExternal}
	for _, v := range ReleaseVariants {
		for _, db := range DBModes {
			suffix := string(v)
			if db == DBExternal {
				suffix += "-external-db"
			}
			cases["compose-first-"+suffix] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, db: db}
			cases["placeholder-"+suffix] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, placeholder: true, db: db}
		}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var dbAnswers Database
			if c.db == DBExternal {
				testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
				dbAnswers = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem"}
				if strings.HasSuffix(name, "single-account") {
					dbAnswers.Auth, dbAnswers.User, dbAnswers.PasswordFile = DBAuthSingleAccount, "acct", "acct.pw"
					if err := os.WriteFile(pathIn(dir, "acct.pw"), []byte(testAppPW), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			withDB := func(o *Options) { o.Answers.DB = dbAnswers }
			switch {
			case c.variant == "":
				o := testOptions(t, dir, c.answers())
				withDB(&o)
				if err := Init(o); err != nil { //nolint:contextcheck // Init has no context (synchronous CLI)
					t.Fatalf("init: %v", err)
				}
			case c.placeholder:
				writePlaceholderDB(t, dir, c.variant, c.db)
				t.Setenv(ImageEnvVar, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest)
				if out, err := composeFirstInit(t, dir, c.variant, withDB); err != nil { //nolint:contextcheck // Init has no context (synchronous CLI)
					t.Fatalf("init: %v\n%s", err, out)
				}
			default:
				writeReleaseDB(t, dir, c.variant, c.db, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest)
				if out, err := composeFirstInit(t, dir, c.variant, withDB); err != nil { //nolint:contextcheck // Init has no context (synchronous CLI)
					t.Fatalf("init: %v\n%s", err, out)
				}
			}
			cmd := exec.CommandContext(ctx, "docker", "compose", "--profile", "*", "config", "-q")
			cmd.Dir = dir
			// Only what compose needs: no inherited compose variables.
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
			if v, ok := os.LookupEnv("DOCKER_HOST"); ok {
				cmd.Env = append(cmd.Env, "DOCKER_HOST="+v)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("docker compose config: %v\n%s", err, out)
			}
			// The resolved image is the pinned literal (a release file
			// ignores a shell BEKUP_IMAGE); the committed file resolves it
			// from .env.
			cmd = exec.CommandContext(ctx, "docker", "compose", "config", "--images")
			cmd.Dir = dir
			cmd.Env = append(cmd.Env[:0:0], "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
			if !c.placeholder {
				cmd.Env = append(cmd.Env, "BEKUP_IMAGE=evil:latest")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("docker compose config --images: %v\n%s", err, out)
			}
			if !containsLine(string(out), "ghcr.io/jlentink/bekup:1.2.3@"+testDigest) || containsLine(string(out), "evil:latest") {
				t.Fatalf("images:\n%s", out)
			}
			if !c.placeholder {
				return
			}
			// Without BEKUP_IMAGE in .env docker compose refuses clearly.
			env := readFile(t, dir, EnvFile)
			stripped, err := setEnvImageLine(env, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pathIn(dir, EnvFile), stripped, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd = exec.CommandContext(ctx, "docker", "compose", "config", "-q")
			cmd.Dir = dir
			cmd.Env = append(cmd.Env[:0:0], "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "required variable BEKUP_IMAGE is missing a value: set BEKUP_IMAGE to the digest-pinned bekup image") {
				t.Fatalf("config without BEKUP_IMAGE: %v\n%s", err, out)
			}
		})
	}
}

func containsLine(s, l string) bool { return hasLine(s, l) }
