//go:build integration

package setup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	vctx, vcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer vcancel()
	if out, err := exec.CommandContext(vctx, "docker", "compose", "version").CombinedOutput(); err != nil {
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
		// dbOnProxy: the database container is on the proxy's own network
		// "web" (join mode, D-216).
		dbOnProxy bool
		// ext enables the external API (D-220 amendment 4).
		ext bool
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
	cases["compose-first-own-proxy-db-on-proxy-network"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy,
		mut: labels, db: DBExternal, dbDocker: true, tls: DBTLSRequire, dbOnProxy: true}
	cases["setup-first-full-db-container"] = tc{answers: func() Answers { return testAnswers(t, ProxyTraefik) }, db: DBExternal, dbDocker: true}
	// The opt-in external API (D-220 amendment 4) in every release file,
	// with the bundled proxies, your own proxy (join, link, labels) and a
	// database container.
	for _, v := range ReleaseVariants {
		for _, db := range DBModes {
			suffix := string(v)
			if db == DBExternal {
				suffix += "-external-db"
			}
			cases["compose-first-"+suffix+"-external-api"] = tc{answers: func() Answers { return variantAnswers(v) }, variant: v, db: db, ext: true}
		}
	}
	cases["setup-first-nginx-external-api"] = tc{answers: func() Answers { return testAnswers(t, ProxyNginx) }, ext: true}
	cases["setup-first-none-external-api"] = tc{answers: func() Answers { return testAnswers(t, ProxyNone) }, ext: true}
	cases["compose-first-own-proxy-link-labels-external-api"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy,
		mut: func(a *Answers) { link(a); labels(a) }, ext: true}
	cases["placeholder-own-proxy-db-container-external-api"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy, placeholder: true,
		db: DBExternal, dbDocker: true, tls: DBTLSRequire, ext: true}
	cases["compose-first-own-proxy-db-on-proxy-network-external-api"] = tc{answers: func() Answers { return variantAnswers(VariantOwnProxy) }, variant: VariantOwnProxy,
		db: DBExternal, dbDocker: true, tls: DBTLSRequire, dbOnProxy: true, ext: true}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// A deadline per case: one for the whole table runs out under
			// load as cases are added.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
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
					if c.dbOnProxy {
						dbAnswers.Network = "web"
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
				o.Answers.ExternalAPI = c.ext
				if c.mut != nil {
					c.mut(&o.Answers)
				}
			}
			switch {
			case c.variant == "":
				o := testOptions(t, dir, c.answers())
				withDB(&o)
				if err := Init(o); err != nil {
					t.Fatalf("init: %v", err)
				}
			case c.placeholder:
				writePlaceholderDB(t, dir, c.variant, c.db)
				t.Setenv(ImageEnvVar, image)
				if out, err := composeFirstInit(t, dir, c.variant, withDB); err != nil {
					t.Fatalf("init: %v\n%s", err, out)
				}
			default:
				writeReleaseDB(t, dir, c.variant, c.db, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest)
				if out, err := composeFirstInit(t, dir, c.variant, withDB); err != nil {
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
					Labels   map[string]string `json:"labels"`
					Ports    []any             `json:"ports"`
					Profiles []string          `json:"profiles"`
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
				case "migrate", "serve", "runner", "setup-web-db", "external-api":
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
			// D-220 amendment 4: the external API runs only with its
			// profile (COMPOSE_PROFILES in .env), which setup writes with
			// BEKUP_EXTERNAL_API=enabled for the proxy route, the role and
			// serve; never a published port.
			extSvc, extOn := project.Services["external-api"]
			if extOn != c.ext {
				t.Fatalf("external-api in the default profiles: %v, enabled %v", extOn, c.ext)
			}
			for name, svc := range project.Services {
				v, ok := svc.Environment["BEKUP_EXTERNAL_API"]
				if name == "postgres" {
					v, ok = svc.Environment["POSTGRES_BEKUP_EXTERNAL_API"]
				}
				switch name {
				case "serve", "postgres", "traefik", "nginx":
					if !ok || (*v == "enabled") != c.ext {
						t.Fatalf("%s: BEKUP_EXTERNAL_API %v, enabled %v", name, v, c.ext)
					}
				default:
					if ok && name != "external-api" {
						t.Fatalf("%s receives BEKUP_EXTERNAL_API", name)
					}
				}
			}
			if extOn {
				if len(extSvc.Ports) > 0 || len(extSvc.Profiles) != 1 || extSvc.Profiles[0] != "external-api" {
					t.Fatalf("external-api ports %v profiles %v", extSvc.Ports, extSvc.Profiles)
				}
				st := mustState(t, dir)
				wantNets := []string{"backend"}
				if st.Variant.OwnProxy() {
					p := extSvc.Networks["proxy"]
					if p == nil || !slices.Contains(p.Aliases, "bekup-external-api") || p.Ipv4Address != "" {
						t.Fatalf("external-api on the proxy network: %+v", p)
					}
					wantNets = append(wantNets, "proxy")
				} else if f := extSvc.Networks["frontend"]; f == nil || f.Ipv4Address != "10.213.250.11" {
					t.Fatalf("external-api on frontend: %+v", f)
				} else {
					wantNets = append(wantNets, "frontend")
				}
				if ov := st.overlays(); ov.DBNetwork && !ov.DBOnProxy {
					wantNets = append(wantNets, "database")
				}
				got := make([]string, 0, len(extSvc.Networks))
				for n := range extSvc.Networks {
					got = append(got, n)
				}
				slices.Sort(got)
				slices.Sort(wantNets)
				if !slices.Equal(got, wantNets) {
					t.Fatalf("external-api networks %v, want %v", got, wantNets)
				}
			}
			// D-217: the browser setup publishes only its own port, from
			// both services, which run only on request (profile).
			cmd = exec.CommandContext(ctx, "docker", "compose", "--profile", "setup-web", "config", "--format", "json")
			cmd.Dir = dir
			cmd.Env = append(cmd.Env[:0:0], "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
			if out, err = cmd.Output(); err != nil {
				t.Fatalf("docker compose --profile setup-web config: %v\n%s", err, out)
			}
			var web struct {
				Services map[string]struct {
					Ports []struct {
						Target    int    `json:"target"`
						Published string `json:"published"`
					} `json:"ports"`
					Volumes []struct {
						Type     string `json:"type"`
						Source   string `json:"source"`
						Target   string `json:"target"`
						ReadOnly bool   `json:"read_only"`
					} `json:"volumes"`
				} `json:"services"`
			}
			if err := json.Unmarshal(out, &web); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"setup-web", "setup-web-db"} {
				ports := web.Services[name].Ports
				if len(ports) != 1 || ports[0].Target != 8443 || ports[0].Published != "8443" {
					t.Fatalf("%s ports %+v", name, ports)
				}
			}
			// D-217 amendment 1: the database run binds only the state
			// file, read-only (never the directory or the keys directory).
			vols := web.Services["setup-web-db"].Volumes
			realDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(vols) != 1 || vols[0].Type != "bind" || !vols[0].ReadOnly || vols[0].Target != "/deploy/"+StateFileName ||
				(vols[0].Source != filepath.Join(dir, StateFileName) && vols[0].Source != filepath.Join(realDir, StateFileName)) {
				t.Fatalf("setup-web-db volumes %+v", vols)
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
					// D-216: migrate and admin reach a database container
					// on the proxy's network there.
					shared := ov.DBOnProxy && slices.Contains(dbNetworkServices, name)
					if _, on := svc.Networks["proxy"]; on != (name == "serve" || name == "external-api" || shared) {
						t.Fatalf("%s on the proxy network: %v", name, on)
					}
				}
				if ov.DBOnProxy != c.dbOnProxy {
					t.Fatalf("shared network overlay %v", ov.DBOnProxy)
				}
			}
			if c.db == DBExternal {
				for name, svc := range project.Services {
					_, on := svc.Networks["database"]
					if want := ov.DBNetwork && !ov.DBOnProxy && (name == "serve" || name == "external-api" || slices.Contains(dbNetworkServices, name)); on != want {
						t.Fatalf("%s on the database network: %v", name, on)
					}
				}
				if project.Networks["backend"].Internal != ov.DBNetwork {
					t.Fatalf("backend internal = %v with a database container %v", project.Networks["backend"].Internal, ov.DBNetwork)
				}
				if _, ok := project.Networks["database"]; ok && ov.DBOnProxy {
					t.Fatal("a database network with the database on the proxy network")
				}
				if ov.DBNetwork && !ov.DBOnProxy && (!project.Networks["database"].External || project.Networks["database"].Name != "dbnet") {
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
