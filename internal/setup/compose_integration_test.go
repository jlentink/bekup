//go:build integration

package setup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
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
		// image is the bekup image ("" = the pinned test image); a tag of
		// the official repository follows the releases (D-212).
		image string
		// mut changes the answers (D-215: own-proxy modes, labels).
		mut func(*Answers)
		// dbDocker: the external database is a container on a Docker
		// network (D-215), with tls as the TLS choice.
		dbDocker bool
		tls      DBTLS
	}
	cases := map[string]tc{}
	for name, mk := range variants {
		cases["setup-first-"+name] = tc{answers: mk}
	}
	cases["setup-first-external-db"] = tc{answers: func() Answers { return testAnswers(t, ProxyNginx) }, db: DBExternal}
	cases["setup-first-external-db-single-account"] = tc{answers: func() Answers { return testAnswers(t, ProxyNone) }, db: DBExternal}
	following := func() Answers {
		a := testAnswers(t, ProxyTraefik)
		a.Image = mustRef(t, "ghcr.io/jlentink/bekup:latest")
		return a
	}
	cases["setup-first-following"] = tc{answers: following, image: "ghcr.io/jlentink/bekup:latest"}
	cases["placeholder-following"] = tc{answers: func() Answers { return variantAnswers(VariantTraefik) }, variant: VariantTraefik, placeholder: true, image: "ghcr.io/jlentink/bekup:latest"}
	for _, v := range ReleaseVariants {
		for _, db := range DBModes {
			suffix := string(v)
			if db == DBExternal {
				suffix += "-external-db"
			}
			cases["compose-first-"+suffix] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, db: db}
			cases["placeholder-"+suffix] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, placeholder: true, db: db}
			if db == DBExternal {
				// Every TLS choice with a database container (D-215).
				for _, tls := range DBTLSChoices {
					cases["compose-first-"+suffix+"-db-container-"+string(tls)] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, db: db, dbDocker: true, tls: tls}
				}
			}
		}
	}
	// The own-proxy overlays (D-215): labels, link mode, both, with a
	// database container.
	labels := func(a *Answers) { a.ProxyLabels = true }
	link := func(a *Answers) { a.ProxyNetwork, a.ProxyContainer, a.ProxyMode = "", "", ProxyLink }
	cases["compose-first-own-proxy-labels"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy, mut: labels}
	cases["compose-first-own-proxy-link"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy, mut: link}
	cases["placeholder-own-proxy-link-labels-db-container"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy, placeholder: true,
		mut: func(a *Answers) { link(a); labels(a) }, db: DBExternal, dbDocker: true, tls: DBTLSRequire}
	cases["setup-first-own-proxy-join-db-container"] = tc{answers: func() Answers {
		a := testAnswers(t, ProxyNone)
		a.Variant, a.Proxy, a.TrustedProxies = VariantOwnProxy, "", nil
		a.ProxyNetwork, a.ProxyContainer = "web", "npm"
		return a
	}, db: DBExternal, dbDocker: true, tls: DBTLSVerifySystem}
	cases["setup-first-full-db-container"] = tc{answers: func() Answers { return testAnswers(t, ProxyTraefik) }, db: DBExternal, dbDocker: true}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			image := c.image
			if image == "" {
				image = "ghcr.io/jlentink/bekup:1.2.3@" + testDigest
			}
			var dbAnswers Database
			if c.db == DBExternal {
				testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
				dbAnswers = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem"}
				if c.dbDocker {
					dbAnswers.Location, dbAnswers.Network, dbAnswers.Host, dbAnswers.TLS = DBDocker, "dbnet", "postgres", c.tls
					if c.tls.OrDefault() != DBTLSVerifyCAFile {
						dbAnswers.CAFile = ""
					}
				}
				if strings.HasSuffix(name, "single-account") {
					dbAnswers.Auth, dbAnswers.User, dbAnswers.PasswordFile = DBAuthSingleAccount, "acct", "acct.pw"
					if err := os.WriteFile(pathIn(dir, "acct.pw"), []byte(testAppPW), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			withDB := func(o *Options) {
				o.Answers.DB = dbAnswers
				if c.mut != nil {
					c.mut(&o.Answers)
				}
			}
			switch {
			case c.variant == "":
				o := testOptions(t, dir, c.answers())
				withDB(&o)
				if err := Init(o); err != nil { //nolint:contextcheck // Init has no context (synchronous CLI)
					t.Fatalf("init: %v", err)
				}
			case c.placeholder:
				writePlaceholderDB(t, dir, c.variant, c.db)
				t.Setenv(ImageEnvVar, image)
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
			if !containsLine(string(out), image) || containsLine(string(out), "evil:latest") {
				t.Fatalf("images:\n%s", out)
			}
			// migrate, serve and runner receive the deployment facts
			// (D-212); other services do not.
			cmd = exec.CommandContext(ctx, "docker", "compose", "config", "--format", "json")
			cmd.Dir = dir
			cmd.Env = append(cmd.Env[:0:0], "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
			if out, err = cmd.Output(); err != nil {
				t.Fatalf("docker compose config --format json: %v\n%s", err, out)
			}
			var project struct {
				Services map[string]struct {
					Environment map[string]*string `json:"environment"`
					Networks    map[string]*struct {
						Aliases     []string `json:"aliases"`
						Ipv4Address string   `json:"ipv4_address"`
					} `json:"networks"`
					Labels map[string]string `json:"labels"`
				} `json:"services"`
				Networks map[string]struct {
					Name     string `json:"name"`
					External bool   `json:"external"`
					Internal bool   `json:"internal"`
				} `json:"networks"`
			}
			if err := json.Unmarshal(out, &project); err != nil {
				t.Fatal(err)
			}
			wantMode := ""
			if c.image != "" {
				wantMode = "following"
			}
			for name, svc := range project.Services {
				ver, mode := svc.Environment["BEKUP_SETUP_VERSION"], svc.Environment["BEKUP_IMAGE_MODE"]
				switch name {
				case "migrate", "serve", "runner":
					if ver == nil || *ver != "1.2.3" || mode == nil || *mode != wantMode {
						t.Fatalf("%s: BEKUP_SETUP_VERSION %v, BEKUP_IMAGE_MODE %v (want 1.2.3, %q)", name, ver, mode, wantMode)
					}
				default:
					if ver != nil || mode != nil {
						t.Fatalf("%s receives the deployment facts", name)
					}
				}
			}
			for _, name := range []string{"migrate", "serve", "runner"} {
				if _, ok := project.Services[name]; !ok {
					t.Fatalf("no %s service", name)
				}
			}
			// D-215: the own-proxy shapes and the database container's
			// network as docker compose resolves them (with the override).
			st := mustState(t, dir)
			ov := st.overlays()
			if st.Variant == VariantOwnProxy {
				proxy := project.Services["serve"].Networks["proxy"]
				net := project.Networks["proxy"]
				if proxy == nil || !slices.Contains(proxy.Aliases, "bekup-serve") || !net.External || net.Name != st.ProxyNetwork {
					t.Fatalf("serve on the proxy network: %+v %+v", proxy, net)
				}
				if (proxy.Ipv4Address != "") != ov.ProxyLink {
					t.Fatalf("link address %q, link mode %v", proxy.Ipv4Address, ov.ProxyLink)
				}
				if (project.Services["serve"].Labels["traefik.enable"] == "true") != ov.ProxyLabels {
					t.Fatalf("labels %v, overlay %v", project.Services["serve"].Labels, ov.ProxyLabels)
				}
				for name, svc := range project.Services {
					if _, on := svc.Networks["proxy"]; on && name != "serve" {
						t.Fatalf("%s on the proxy network", name)
					}
				}
			}
			if c.db == DBExternal {
				for name, svc := range project.Services {
					_, on := svc.Networks["database"]
					if want := ov.DBNetwork && slices.Contains([]string{"migrate", "serve", "admin"}, name); on != want {
						t.Fatalf("%s on the database network: %v", name, on)
					}
				}
				if project.Networks["backend"].Internal != ov.DBNetwork {
					t.Fatalf("backend internal = %v with a database container %v", project.Networks["backend"].Internal, ov.DBNetwork)
				}
				if ov.DBNetwork && (!project.Networks["database"].External || project.Networks["database"].Name != "dbnet") {
					t.Fatalf("database network %+v", project.Networks["database"])
				}
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
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "required variable BEKUP_IMAGE is missing a value: set BEKUP_IMAGE to the bekup image") {
				t.Fatalf("config without BEKUP_IMAGE: %v\n%s", err, out)
			}
		})
	}
}

func containsLine(s, l string) bool { return hasLine(s, l) }
