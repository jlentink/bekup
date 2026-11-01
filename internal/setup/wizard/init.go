package wizard

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
)

// Env is what the wizard knows before the first question.
type Env struct {
	// Dir is the deployment directory (the bind mount, /deploy).
	Dir string
	// DefaultDir is the default of `setup init --dir`: the flag line names
	// --dir only when Dir differs.
	DefaultDir string
	Version    string
	// Image is $BEKUP_IMAGE ("" when unset): a container cannot know its
	// own digest.
	Image string
	// DockerGID is $DOCKER_GID ("" when unset).
	DockerGID            string
	AllowVersionMismatch bool
	Ownership            setup.OwnershipMode
	UI                   UI
	// GeneratePassphrase generates the CA key passphrase (default: a
	// crypto.GeneratePassphrase diceware phrase).
	GeneratePassphrase func() ([]byte, error)
	// LoadCA checks the passphrase of an existing CA (default certs.LoadCA).
	LoadCA func(certFile, keyFile string, pass []byte) error
}

// InitPlan is the result of the init wizard: the options of setup.Init,
// exactly what the equivalent `setup init` flags produce. Destroy zeroes
// the secrets it holds.
type InitPlan struct {
	Options setup.Options
	// Flags is the equivalent `bekup setup init` command line (secrets
	// never appear in it; files the operator must provide instead of the
	// entered passwords are named by the placeholders in FlagsNote).
	Flags []string
	// FlagsNote explains the placeholders of Flags ("" when there are
	// none).
	FlagsNote string
	secrets   [][]byte
}

// Destroy zeroes the passphrase and passwords of the plan.
func (p *InitPlan) Destroy() {
	if p == nil {
		return
	}
	for _, s := range p.secrets {
		crypto.Zero(s)
	}
	p.secrets = nil
}

// Proxy choices of the wizard (each maps to a variant). A proxy on
// another machine is not a wizard choice (D-215 amendment): the flags
// --variant full --proxy none --trusted-proxy stay the advanced path.
const (
	proxyOwnTraefik = "own-traefik"
	proxyOwn        = "own"
)

// Database location choices.
const (
	dbHere   = "docker"
	dbRemote = "remote"
)

// Consent choices.
const (
	choiceGenerate = "generate"
	choiceImport   = "import"
	choiceExisting = "existing"
	choiceEnter    = "enter"
)

type initState struct {
	env     Env
	ui      UI
	compose *setup.ComposeInfo
	a       setup.Answers
	proxy   string
	// master key: generate | import (path, optional key-check value)
	keyMode, importKey, kcv string
	// CA: generate | existing; passphrase: generate | enter
	caMode, passMode string
	pass             []byte
	// pw holds the asked database passwords by role.
	pw          map[string][]byte
	targets     string
	targetHosts string
}

func (st *initState) dropSecrets() {
	crypto.Zero(st.pass)
	st.pass = nil
	for k, v := range st.pw {
		crypto.Zero(v)
		delete(st.pw, k)
	}
}

// RunInit asks the init questions and returns the plan; nothing is
// written. ErrAborted when the operator quits.
func RunInit(env Env) (_ *InitPlan, retErr error) {
	if env.GeneratePassphrase == nil {
		env.GeneratePassphrase = generatePassphrase
	}
	if env.LoadCA == nil {
		env.LoadCA = func(c, k string, p []byte) error {
			ca, err := certs.LoadCA(c, k, p)
			if err == nil {
				clear(ca.Key)
			}
			return err
		}
	}
	if env.Ownership == "" {
		env.Ownership = setup.OwnershipAuto
	}
	st := &initState{env: env, ui: env.UI, pw: map[string][]byte{}}
	st.a.KeysDir = setup.DefaultKeysDir // the flag's default
	defer func() {
		if retErr != nil {
			st.dropSecrets()
		}
	}()
	if err := st.detectCompose(); err != nil {
		return nil, err
	}
	if err := runSteps(st, initSteps()); err != nil {
		return nil, err
	}
	return st.plan(), nil
}

func generatePassphrase() ([]byte, error) {
	s, err := crypto.GeneratePassphrase(crypto.MinPassphraseWords)
	if err != nil {
		return nil, err
	}
	defer s.Destroy()
	return bytes.Clone(s.Bytes()), nil
}

// detectCompose reads an existing compose.yaml (compose-first): it then
// decides the variant, the database mode and the image.
func (st *initState) detectCompose() error {
	if _, err := os.Lstat(filepath.Join(st.env.Dir, setup.ComposeFile)); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	info, err := setup.InspectCompose(st.env.Dir, "", "")
	if err != nil {
		return err
	}
	if st.env.Image != "" && st.env.Image != info.Image.String() {
		return fmt.Errorf("BEKUP_IMAGE %s does not match the image of compose.yaml (%s): run the wizard with the image named there", st.env.Image, info.Image)
	}
	if !st.env.AllowVersionMismatch {
		// The same check as the image screen and `setup init`: the image
		// of compose.yaml must be this bekup version.
		if err := setup.CheckVersionTag(info.Image, st.env.Version); err != nil {
			return fmt.Errorf("compose.yaml: %w", err)
		}
	}
	st.compose = info
	st.a.Variant, st.a.Image, st.a.DB.Mode = info.Variant, info.Image, info.DB
	st.a.Proxy = info.Variant.ImpliedProxy() // as setup.Complete
	if info.Variant == setup.VariantTraefik {
		st.a.TLSMode = setup.TLSACME
	}
	return nil
}

func initSteps() []step[*initState] {
	setupFirst := func(st *initState) bool { return st.compose != nil }
	return []step[*initState]{
		{name: "welcome", run: (*initState).welcome},
		{name: "image", skip: func(st *initState) bool { return st.compose != nil }, run: (*initState).image},
		{name: "proxy", skip: setupFirst, run: (*initState).askProxy},
		{name: "own-proxy", skip: func(st *initState) bool { return !st.a.Variant.OwnProxy() }, run: (*initState).ownProxy},
		{name: "proxy-labels", skip: func(st *initState) bool { return !st.a.Variant.OwnProxy() }, run: (*initState).proxyLabels},
		{name: "traefik-names", skip: func(st *initState) bool { return !st.a.ProxyLabels }, run: (*initState).traefikNames},
		{name: "domain", run: (*initState).domain},
		{name: "acme", skip: func(st *initState) bool { return st.a.Variant != setup.VariantTraefik }, run: (*initState).acme},
		{name: "database", skip: setupFirst, run: (*initState).database},
		{name: "db-location", skip: notExternal, run: (*initState).dbLocation},
		{name: "db-server", skip: notExternal, run: (*initState).dbServer},
		{name: "db-tls", skip: notExternal, run: (*initState).dbTLS},
		{name: "db-auth", skip: notExternal, run: (*initState).dbAuth},
		{name: "db-passwords", skip: func(st *initState) bool {
			auth := st.auth()
			return notExternal(st) || auth == setup.DBAuthBootstrap || auth == setup.DBAuthDBASQL
		}, run: (*initState).dbPasswords},
		{name: "master-key", run: (*initState).masterKey},
		{name: "ca", run: (*initState).ca},
		{name: "bundled-db", skip: func(st *initState) bool { return st.a.DB.External() }, run: (*initState).bundledDB},
		{name: "docker-gid", run: (*initState).dockerGID},
		{name: "targets", run: (*initState).askTargets},
		{name: "summary", run: (*initState).summary},
		{name: "passphrase", skip: func(st *initState) bool { return st.passMode != choiceGenerate }, run: (*initState).showPassphrase},
	}
}

func notExternal(st *initState) bool { return !st.a.DB.External() }

// auth is the effective authentication choice (empty is bootstrap, like
// the flag's default).
func (st *initState) auth() setup.DBAuth {
	if st.a.DB.Auth == "" {
		return setup.DBAuthBootstrap
	}
	return st.a.DB.Auth
}

func (st *initState) welcome() error {
	body := fmt.Sprintf(`This wizard writes a complete bekup deployment to %s: compose.yaml (unless
it exists), .env, bekup.toml, runner.toml, the secrets and certificates.
Nothing is written before the summary. Esc (plain interface: <) goes back,
Ctrl+C quits.
The setup container has no network: it never connects anywhere.`, st.env.Dir)
	if c := st.compose; c != nil {
		db := "the bundled database"
		if c.DB == setup.DBExternal {
			db = "your own PostgreSQL"
		}
		body += fmt.Sprintf("\n\ncompose.yaml found: the %s variant with %s (%s), image\n%s. It is kept as it is.", c.Variant, db, c.Level, c.Image)
	}
	return st.ui.Note("bekup setup", body)
}

// checkImage accepts the bekup image: digest-pinned (its tag must name
// this version unless AllowVersionMismatch) or a followed tag of the
// official repository (D-212).
func (st *initState) checkImage(s string) error {
	ref, err := imageref.ParseBekup(s)
	if err != nil {
		return err
	}
	if !st.env.AllowVersionMismatch {
		return setup.CheckVersionTag(ref, st.env.Version)
	}
	return nil
}

// checkPinned accepts a digest-pinned image only (the "pin" choice).
func (st *initState) checkPinned(s string) error {
	if _, err := imageref.ParsePinned(s); err != nil {
		return err
	}
	return st.checkImage(s)
}

// Image mode choices (D-212).
const (
	imageFollow = "follow"
	imagePin    = "pin"
)

func (st *initState) image() error {
	if st.env.Image != "" {
		if err := st.checkImage(st.env.Image); err != nil {
			return fmt.Errorf("BEKUP_IMAGE: %w", err)
		}
		ref, err := imageref.ParseBekup(st.env.Image)
		if err != nil {
			return err
		}
		if ref.IsMutable() {
			return st.imageMode(ref)
		}
		st.a.Image = ref
		return nil
	}
	s, err := st.ui.Input("bekup image", "The image you run, pinned by digest: repo:tag@sha256:<digest>\n(in a second terminal on the host: docker image inspect --format\n'{{index .RepoDigests 0}}' <image>; this container has no Docker access),\nor "+imageref.OfficialRepo+":"+imageref.LatestTag+" to follow the releases", st.a.Image.String(), st.checkImage)
	if err != nil {
		return err
	}
	ref, err := imageref.ParseBekup(s)
	if err != nil {
		return err
	}
	if ref.IsMutable() {
		return st.imageMode(ref)
	}
	st.a.Image = ref
	return nil
}

// imageMode explains following a tag (D-212) and lets the operator pin a
// digest instead. Following is the default for the tag they started with.
func (st *initState) imageMode(ref imageref.Ref) error {
	def := imageFollow
	if !st.a.Image.IsZero() && !st.a.Image.IsMutable() {
		def = imagePin // back from a later screen after pinning
	}
	body := fmt.Sprintf(`The image is %s: a tag, not a digest.

Follow it (simpler): compose.yaml names the tag and `+"`docker compose pull`"+`
fetches whatever release the tag points to. Whoever moves the tag decides
what runs. Upgrades stay a deliberate step: migrate, serve, worker and
runner refuse to start on a version setup has not prepared, so after a pull run
`+"`docker compose run --rm setup upgrade`"+`, then `+"`docker compose up -d`"+`.
bekup warns at every start, audits it and alerts every admin daily.

Pin a digest (strict change control): compose.yaml names exactly one image;
an upgrade changes it. To pin later, run setup upgrade --image <pinned> from
the pinned image (docs/installation/upgrades.md).`, ref)
	v, err := st.ui.Select("Image updates", body, []Option{
		{imageFollow, "follow " + ref.String() + " (simpler; not pinned)"},
		{imagePin, "pin a digest: repo:tag@sha256:<digest>"},
	}, def)
	if err != nil {
		return err
	}
	if v == imageFollow {
		st.a.Image = ref
		return nil
	}
	cur := ""
	if !st.a.Image.IsMutable() {
		cur = st.a.Image.String()
	}
	s, err := st.ui.Input("Pinned bekup image", "The image pinned by digest: repo:tag@sha256:<digest>.\n"+
		"This setup container has no Docker or network access: in a second terminal\n"+
		"on the host, run\n"+
		"  docker image inspect --format '{{index .RepoDigests 0}}' "+ref.String()+"\n"+
		"(after docker pull "+ref.String()+" if the image is not there yet). It prints\n"+
		"repo@sha256:<digest>; keep :"+ref.Tag+" in front of the @.", cur, st.checkPinned)
	if errors.Is(err, ErrBack) {
		return errRepeat
	}
	if err != nil {
		return err
	}
	st.a.Image, err = imageref.ParsePinned(s)
	return err
}

func (st *initState) askProxy() error {
	def := st.proxy
	if def == "" {
		def = proxyOwnTraefik
	}
	v, err := st.ui.Select("Reverse proxy", "Who terminates HTTPS for the web UI?", []Option{
		{proxyOwnTraefik, "bekup's own Traefik (Let's Encrypt; needs ports 80 and 443)"},
		{proxyOwn, "my own reverse proxy on this Docker host (Nginx Proxy Manager, Traefik,\nCaddy, nginx, ...)"},
	}, def)
	if err != nil {
		return err
	}
	st.proxy = v
	st.a.TLSMode, st.a.Proxy, st.a.TrustedProxies = "", "", nil
	if v != proxyOwn {
		st.a.ProxyNetwork, st.a.ProxyContainer, st.a.ProxyLabels = "", "", false
		st.a.TraefikEntrypoint, st.a.TraefikCertResolver = "", ""
	}
	if v == proxyOwn {
		st.a.Variant, st.a.Proxy = setup.VariantOwnProxy, setup.ProxyExternal
		return nil
	}
	st.a.Variant, st.a.Proxy, st.a.TLSMode = setup.VariantTraefik, setup.ProxyTraefik, setup.TLSACME
	return nil
}

// ownProxy asks for the Docker network the operator's proxy is on and its
// container name (D-215): serve joins that network and trusts the proxy
// by its name.
func (st *initState) ownProxy() error {
	if st.a.ProxyMode == setup.ProxyLink {
		return nil // advanced, flag-only (--proxy-network-mode link)
	}
	net, err := st.ui.Input("Your proxy's Docker network", `serve joins the Docker network your reverse proxy is on, so the proxy reaches
it as http://bekup-serve:8080 (nothing to add to the proxy's own setup).
This container has no Docker access: find the network in a second terminal
on the host:
  docker network ls
  docker inspect <proxy container> --format '{{json .NetworkSettings.Networks}}'`, st.a.ProxyNetwork, func(s string) error {
		return setup.ValidateDockerNetwork("proxy-network", s)
	})
	if err != nil {
		return err
	}
	name, err := st.ui.Input("Your proxy's container name", `bekup trusts the client addresses (X-Forwarded-For) only from this
container: serve resolves its name on that network (docker ps shows it).`, st.a.ProxyContainer, func(s string) error {
		return setup.ValidateContainerName("proxy-container", s)
	})
	if err != nil {
		return err
	}
	st.a.ProxyNetwork, st.a.ProxyContainer = net, name
	return nil
}

// proxyLabels offers the Traefik Docker-provider labels (default no).
func (st *initState) proxyLabels() error {
	labels, err := st.ui.Confirm("Traefik Docker labels?", `Only for a Traefik that reads container labels (Docker provider): add the
routing labels to serve. Every other proxy (and Traefik with the file
provider) is pointed at http://bekup-serve:8080 by hand.`, st.a.ProxyLabels)
	if err != nil {
		return err
	}
	st.a.ProxyLabels = labels
	if !labels {
		st.a.TraefikEntrypoint, st.a.TraefikCertResolver = "", ""
	}
	return nil
}

// traefikNames asks for the entrypoint and certificate resolver of the
// operator's Traefik (--proxy-labels; the defaults are the flags').
func (st *initState) traefikNames() error {
	ep := st.a.TraefikEntrypoint
	if ep == "" {
		ep = setup.DefaultTraefikEntrypoint
	}
	ep, err := st.ui.Input("Traefik entrypoint", "The name of your Traefik's HTTPS entrypoint (its static configuration,\nentryPoints.<name>); bekup's router labels use it.", ep, func(s string) error {
		return setup.ValidateTraefikName("traefik-entrypoint", s)
	})
	if err != nil {
		return err
	}
	cr := st.a.TraefikCertResolver
	if cr == "" {
		cr = setup.DefaultTraefikCertResolver
	}
	cr, err = st.ui.Input("Traefik certificate resolver", "The name of your Traefik's certificate resolver for the domain\n(certificatesResolvers.<name>).", cr, func(s string) error {
		return setup.ValidateTraefikName("traefik-certresolver", s)
	})
	if err != nil {
		return err
	}
	// The defaults stay empty, like the flags that were not given.
	st.a.TraefikEntrypoint, st.a.TraefikCertResolver = "", ""
	if ep != setup.DefaultTraefikEntrypoint {
		st.a.TraefikEntrypoint = ep
	}
	if cr != setup.DefaultTraefikCertResolver {
		st.a.TraefikCertResolver = cr
	}
	return nil
}

func (st *initState) domain() error {
	s, err := st.ui.Input("Domain", "The public host name of the web UI (a DNS name pointing at your proxy),\ne.g. backup.example.com.", st.a.Domain, setup.ValidateDomain)
	if err != nil {
		return err
	}
	st.a.Domain = s
	return nil
}

func (st *initState) acme() error {
	s, err := st.ui.Input("Let's Encrypt e-mail", "Traefik requests the certificate for the domain from Let's Encrypt;\nexpiry notices go to this address.", st.a.ACMEEmail, setup.ValidateEmail)
	if err != nil {
		return err
	}
	st.a.ACMEEmail = s
	staging, err := st.ui.Confirm("Let's Encrypt staging?", "Use the staging CA while testing (browsers do not trust its certificates).", st.a.ACMEStaging)
	if err != nil {
		return err
	}
	st.a.ACMEStaging = staging
	return nil
}

func (st *initState) database() error {
	def := string(st.a.DB.Mode.OrBundled())
	v, err := st.ui.Select("Database", "bekup keeps its users, jobs, run history and audit log in PostgreSQL.", []Option{
		{string(setup.DBBundled), "bundled: bekup's own postgres container (recommended)"},
		{string(setup.DBExternal), "existing: my PostgreSQL 15-17 (managed service or own server)"},
	}, def)
	if err != nil {
		return err
	}
	switch {
	case setup.DBMode(v) == setup.DBBundled:
		st.a.DB = setup.Database{} // the flag's default
	case !st.a.DB.External():
		st.a.DB = setup.Database{Mode: setup.DBExternal}
	}
	return nil
}

// dbField validates one database answer through setup's rules.
func dbField(d setup.Database, field string) error {
	d.Mode = setup.DBExternal
	if d.Host == "" {
		d.Host = "db.invalid"
	}
	if d.CAFile == "" {
		d.CAFile = "x"
	}
	err := setup.ValidateDatabase(d)
	var fe *setup.FieldError
	if errors.As(err, &fe) && fe.Field == field {
		return err
	}
	return nil
}

// dbLocation asks whether the database is a container on this Docker
// host (D-215): then its Docker network and container name.
func (st *initState) dbLocation() error {
	d := &st.a.DB
	def := dbRemote
	if d.Location == setup.DBDocker {
		def = dbHere
	}
	v, err := st.ui.Select("Where does your PostgreSQL run?", "Is it a container on this Docker host?", []Option{
		{dbHere, "a container on this Docker host (bekup joins its Docker network)"},
		{dbRemote, "another server or a managed service (host name or IP address)"},
	}, def)
	if err != nil {
		return err
	}
	if v == dbRemote {
		if d.Location == setup.DBDocker {
			d.Host = ""
		}
		d.Location, d.Network = "", "" // remote is the flag's default
		return nil
	}
	net, err := st.ui.Input("The database's Docker network", `migrate, serve and admin join it (setup adds it in compose.override.yaml;
bekup's own backend network stays internal). In a second terminal on the
host:
  docker inspect <database container> --format '{{json .NetworkSettings.Networks}}'`, d.Network, func(s string) error {
		if err := setup.ValidateDockerNetwork("db-network", s); err != nil {
			return err
		}
		if st.a.Variant.OwnProxy() && strings.EqualFold(s, st.a.ProxyNetwork) {
			return errors.New("that is your proxy's network: put the database on a network of its own (a proxy network should not reach databases)")
		}
		return nil
	})
	if err != nil {
		return err
	}
	host, err := st.ui.Input("The database's container name", "Its container name or a network alias on that network (a name with _ is no\nvalid host name: use its compose service name instead).", d.Host, func(s string) error {
		if s == "" {
			return errors.New("required")
		}
		return dbField(setup.Database{Host: s}, "db-host")
	})
	if err != nil {
		return err
	}
	if d.Location != setup.DBDocker {
		d.Port, d.Name = 0, ""
	}
	d.Location, d.Network, d.Host = setup.DBDocker, net, host
	return nil
}

func (st *initState) dbServer() error {
	d := &st.a.DB
	host := d.Host
	if d.Location != setup.DBDocker {
		var err error
		host, err = st.ui.Input("PostgreSQL host", "Host name (or IP address) bekup connects to; connect directly, not through\na pooler (SCRAM channel binding).", d.Host, func(s string) error {
			if s == "" {
				return errors.New("required")
			}
			return dbField(setup.Database{Host: s}, "db-host")
		})
		if err != nil {
			return err
		}
	}
	port := "5432"
	if d.Port != 0 {
		port = strconv.Itoa(d.Port)
	}
	ps, err := st.ui.Input("PostgreSQL port", "", port, func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("a port number between 1 and 65535")
		}
		return dbField(setup.Database{Port: n}, "db-port")
	})
	if err != nil {
		return err
	}
	name := d.Name
	if name == "" {
		name = "bekup"
	}
	name, err = st.ui.Input("Database name", "bekup's own database (created by the bootstrap).", name, func(s string) error {
		return dbField(setup.Database{Name: s}, "db-name")
	})
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(ps)
	if err != nil {
		return err
	}
	d.Host, d.Name = host, name
	d.Port = p
	if p == 5432 {
		d.Port = 0 // the default, like the flag
	}
	if name == "bekup" {
		d.Name = ""
	}
	return nil
}

// dbTLS asks for the TLS choice (D-215): verified against a CA file
// (default) or the system bundle, or one of the explicit weakenings
// (confirmed after a warning).
func (st *initState) dbTLS() error {
	d := &st.a.DB
	for {
		v, err := st.ui.Select("TLS to your PostgreSQL", "Username and password (SCRAM) work in every mode; only the first two verify\nthat bekup talks to your server.", []Option{
			{string(setup.DBTLSVerifyCAFile), "verify with a CA file (recommended): your server's CA bundle (PEM)"},
			{string(setup.DBTLSVerifySystem), "verify with the system CA bundle (a certificate from a public CA)"},
			{string(setup.DBTLSRequire), "encrypted, but not verified (WEAKENED)"},
			{string(setup.DBTLSDisable), "no TLS (WEAKENED; prefer the one above, even on a private Docker network)"},
		}, string(d.TLS.OrDefault()))
		if err != nil {
			return err
		}
		choice := setup.DBTLS(v)
		if choice.Weak() {
			warn := setup.DBTLSRequireWarning
			if choice == setup.DBTLSDisable {
				warn = setup.DBTLSDisableWarning
			}
			ok, err := st.ui.Confirm("An explicit weakening", strings.TrimSpace(warn)+`
bekup warns at every start, audits it and alerts every admin daily; the
admin pages show it. The printed bootstrap command needs
--allow-insecure-admin-connection. Use it anyway?`, false)
			if errors.Is(err, ErrBack) || err == nil && !ok {
				continue
			}
			if err != nil {
				return err
			}
			d.TLS, d.CAFile, d.TLSServerName = choice, "", ""
			return nil
		}
		if choice == setup.DBTLSVerifyCAFile {
			ca, err := st.ui.Input("CA bundle", "The PEM file with the CA that signed the server certificate (your\nprovider's bundle), saved inside the deployment directory, e.g. db-ca.pem.\nOnly this CA is trusted (TLS verify-full); setup copies it.", d.CAFile, func(s string) error {
				if s == "" {
					return errors.New("required")
				}
				if err := dbField(setup.Database{CAFile: s}, "db-ca-file"); err != nil {
					return err
				}
				if _, err := os.Stat(filepath.Join(st.env.Dir, filepath.FromSlash(s))); err != nil {
					return fmt.Errorf("%s: not found in the deployment directory", s)
				}
				return nil
			})
			if errors.Is(err, ErrBack) {
				continue
			}
			if err != nil {
				return err
			}
			d.CAFile = ca
		} else {
			d.CAFile = ""
		}
		sni, err := st.ui.Input("TLS server name (optional)", "The name in the server certificate, when the host is an IP address.\nEmpty: the host itself.", d.TLSServerName, func(s string) error {
			if s == "" {
				return nil
			}
			return dbField(setup.Database{TLSServerName: s}, "db-tls-server-name")
		})
		if errors.Is(err, ErrBack) {
			continue
		}
		if err != nil {
			return err
		}
		d.TLSServerName = sni
		d.TLS = choice
		if choice == setup.DBTLSVerifyCAFile {
			d.TLS = "" // the flag's default
		}
		return nil
	}
}

func (st *initState) dbAuth() error {
	d := &st.a.DB
	for {
		def := string(st.auth())
		v, err := st.ui.Select("Roles and database", "How do bekup's roles bekup_owner and bekup_app come to exist?", []Option{
			{string(setup.DBAuthBootstrap), "bootstrap: an admin account creates them once (recommended)"},
			{string(setup.DBAuthExistingRoles), "they exist already: I enter their passwords"},
			{string(setup.DBAuthDBASQL), "my DBA runs an SQL file that setup writes"},
			{string(setup.DBAuthSingleAccount), "single account for everything (WEAKENED)"},
		}, def)
		if err != nil {
			return err
		}
		auth := setup.DBAuth(v)
		if auth != st.auth() {
			for k, pw := range st.pw {
				crypto.Zero(pw)
				delete(st.pw, k)
			}
			d.User, d.AdminUser = "", ""
		}
		if auth == setup.DBAuthSingleAccount {
			ok, err := st.ui.Confirm("Single account: an explicit weakening", `One account would run the migrations and the app. A compromised serve could
then change the schema, disable the audit-log protections and rewrite the
audit history. bekup warns at every start, audits it and alerts every admin
daily. Prefer bootstrap. Use single-account mode anyway?`, false)
			if errors.Is(err, ErrBack) || err == nil && !ok {
				continue
			}
			if err != nil {
				return err
			}
			user, err := st.ui.Input("The single account", "Its role name (not a superuser); it needs CREATEROLE and CREATEDB once.", d.User, func(s string) error {
				return dbField(setup.Database{Auth: setup.DBAuthSingleAccount, User: s}, "db-user")
			})
			if errors.Is(err, ErrBack) {
				continue
			}
			if err != nil {
				return err
			}
			d.Auth, d.User = auth, user
			return nil
		}
		d.Auth = auth
		if auth == setup.DBAuthBootstrap {
			admin, err := st.ui.Input("Admin role (optional)", "The administrator that runs the bootstrap (CREATEROLE + CREATEDB, or the\nprovider's admin), only to print the exact command; its password is asked\nwhen the bootstrap runs and never stored.", d.AdminUser, func(s string) error {
				if s == "" {
					return nil
				}
				return dbField(setup.Database{Auth: setup.DBAuthBootstrap, AdminUser: s}, "db-admin-user")
			})
			if errors.Is(err, ErrBack) {
				continue
			}
			if err != nil {
				return err
			}
			d.AdminUser = admin
		}
		if auth == setup.DBAuthBootstrap {
			d.Auth = "" // the default, like the flag
		}
		return nil
	}
}

// askPassword asks twice for a role's password.
func (st *initState) askPassword(role string) ([]byte, error) {
	for {
		first, err := st.ui.Secret("Password of "+role, "The existing password of the database role "+role+" (16 or more printable\nASCII characters).", setup.ValidateDBPassword)
		if err != nil {
			return nil, err
		}
		second, err := st.ui.Secret("Repeat the password of "+role, "", nil)
		if err != nil {
			crypto.Zero(first)
			return nil, err
		}
		same := bytes.Equal(first, second)
		crypto.Zero(second)
		if same {
			return first, nil
		}
		crypto.Zero(first)
		if err := st.ui.Note("Passwords differ", "The two entries did not match; enter it again."); err != nil {
			return nil, err
		}
	}
}

func (st *initState) dbPasswords() error {
	d := &st.a.DB
	roles := []string{"bekup_app", "bekup_owner"}
	if d.Auth == setup.DBAuthSingleAccount {
		roles = []string{d.User}
	}
	for _, r := range roles {
		if old := st.pw[r]; old != nil {
			crypto.Zero(old)
		}
		pw, err := st.askPassword(r)
		if err != nil {
			return err
		}
		st.pw[r] = pw
	}
	if d.Auth != setup.DBAuthSingleAccount && bytes.Equal(st.pw["bekup_app"], st.pw["bekup_owner"]) {
		for _, r := range roles {
			crypto.Zero(st.pw[r])
			delete(st.pw, r)
		}
		if err := st.ui.Note("Same password", "bekup_owner and bekup_app need different passwords: enter both again."); err != nil {
			return err
		}
		return errRepeat
	}
	return nil
}

func (st *initState) masterKey() error {
	def := st.keyMode
	if def == "" {
		def = choiceGenerate
	}
	v, err := st.ui.Select("Master encryption key", "It encrypts every stored credential (connections, destinations, job\npassphrases, 2FA secrets).", []Option{
		{choiceGenerate, "generate a new key (new installation)"},
		{choiceImport, "import my existing key file (reinstall; restored from escrow)"},
	}, def)
	if err != nil {
		return err
	}
	st.keyMode = v
	if v == choiceGenerate {
		st.importKey, st.kcv = "", ""
		return nil
	}
	path, err := st.ui.Input("Key file", "The keyring file inside the deployment directory, e.g. old_master_key.\nsetup copies it to secrets/ (delete the copy afterwards).", st.importKey, func(s string) error {
		_, err := setup.InspectKeyring(st.env.Dir, s, "")
		return err
	})
	if err != nil {
		return err
	}
	kcv, err := st.ui.Input("Key-check value (optional)", "The kcv1:... value of that installation, if you have it (empty: skip; serve\nverifies the stored values at startup anyway).", st.kcv, func(s string) error {
		if s == "" {
			return nil
		}
		_, err := setup.InspectKeyring(st.env.Dir, path, s)
		return err
	})
	if err != nil {
		return err
	}
	id, err := setup.InspectKeyring(st.env.Dir, path, kcv)
	if err != nil {
		return err
	}
	st.importKey, st.kcv = path, kcv
	return st.ui.Note("Key imported", "Primary key ID "+id+". Keep your escrow copy offline.")
}

func (st *initState) caFiles() (string, string) {
	keys := st.a.KeysDir
	if keys == "" {
		keys = setup.DefaultKeysDir
	}
	ca := filepath.Join(st.env.Dir, filepath.FromSlash(keys), "certs", "ca")
	return filepath.Join(ca, "ca.crt"), filepath.Join(ca, "ca.key")
}

func (st *initState) ca() error {
	crypto.Zero(st.pass)
	st.pass = nil
	def := st.caMode
	if def == "" {
		def = choiceGenerate
	}
	v, err := st.ui.Select("Internal CA and mTLS certificates", "serve and the runner (which controls Docker) authenticate each other with\ncertificates from a private CA. Its key is sealed with a passphrase and\nbelongs on offline media.", []Option{
		{choiceGenerate, "generate a new CA"},
		{choiceExisting, "use my existing CA, mounted at secrets/certs/ca"},
	}, def)
	if err != nil {
		return err
	}
	st.caMode = v
	if v == choiceExisting {
		crt, key := st.caFiles()
		for _, f := range []string{crt, key} {
			if _, err := os.Stat(f); err != nil {
				if err := st.ui.Note("No CA mounted", "Mount the CA media at secrets/certs/ca for this run (docker run ... -v\n/media/usb/bekup-ca:/deploy/secrets/certs/ca ...), or generate a new CA."); err != nil {
					return err
				}
				return errRepeat
			}
		}
		for {
			pass, err := st.ui.Secret("Passphrase of the existing CA key", "It is checked now and used only to issue the certificates.", certs.ValidatePassphrase)
			if err != nil {
				return err
			}
			if err := st.env.LoadCA(crt, key, pass); err != nil {
				crypto.Zero(pass)
				if err := st.ui.Note("Wrong passphrase?", "The CA could not be opened: "+err.Error()); err != nil {
					return err
				}
				continue
			}
			st.pass, st.passMode = pass, choiceEnter
			return nil
		}
	}
	pm := st.passMode
	if pm == "" {
		pm = choiceGenerate
	}
	genLabel := "generate one and show it once (store it in a password manager)"
	if !hidesSensitive(st.ui) {
		genLabel = "generate one and print it once (the plain interface leaves it in the\nterminal's scrollback: clear it afterwards)"
	}
	pm, err = st.ui.Select("CA key passphrase", "Needed again only to renew certificates (bekup setup renew).", []Option{
		{choiceGenerate, genLabel},
		{choiceEnter, "enter my own (twice, at least 16 characters)"},
	}, pm)
	if err != nil {
		return err
	}
	st.passMode = pm
	if pm == choiceGenerate {
		st.pass, err = st.env.GeneratePassphrase()
		return err
	}
	st.pass, err = NewPassphrase(st.ui, "At least 16 characters.")
	return err
}

// hidesSensitive reports whether the interface keeps a secret shown with
// Sensitive out of the scrollback (an interface without the method does).
func hidesSensitive(ui UI) bool {
	h, ok := ui.(interface{ HidesSensitive() bool })
	return !ok || h.HidesSensitive()
}

// NewPassphrase asks twice for a new CA key passphrase (at least 16
// characters) until both entries match; the caller zeroes the result.
func NewPassphrase(ui UI, body string) ([]byte, error) {
	for {
		first, err := ui.Secret("New CA key passphrase", body, certs.ValidatePassphrase)
		if err != nil {
			return nil, err
		}
		second, err := ui.Secret("Repeat the passphrase", "", nil)
		if err != nil {
			crypto.Zero(first)
			return nil, err
		}
		same := bytes.Equal(first, second)
		crypto.Zero(second)
		if same {
			return first, nil
		}
		crypto.Zero(first)
		if err := ui.Note("Passphrases differ", "The two entries did not match; enter it again."); err != nil {
			return nil, err
		}
	}
}

// noteBack shows why a choice cannot be used and goes back.
func (st *initState) noteBack(title, body string) error {
	if err := st.ui.Note(title, body); err != nil {
		return err
	}
	return ErrBack
}

func (st *initState) bundledDB() error {
	ok, err := st.ui.Confirm("Bundled database secrets", "Generate the TLS certificate of the bundled postgres (from the internal CA)\nand the three random role passwords (postgres, bekup_owner, bekup_app)?", true)
	if err != nil {
		return err
	}
	if !ok {
		return st.noteBack("Needed for the bundled database", `The bundled postgres cannot run without them. Either let setup generate them,
go back and choose your own PostgreSQL, or put the files into secrets/
(postgres_password, bekup_owner_password, bekup_app_password and the URLs)
yourself and run `+"`bekup setup init --keep-existing`"+` instead.`)
	}
	return nil
}

func (st *initState) dockerGID() error {
	def := st.env.DockerGID
	if st.a.DockerGID != 0 {
		def = strconv.Itoa(st.a.DockerGID)
	}
	s, err := st.ui.Input("Docker socket group", "The group ID of /var/run/docker.sock on the host (stat -c %g\n/var/run/docker.sock); only the runner gets it.", def, func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("a numeric group ID")
		}
		return setup.ValidateDockerGID(n)
	})
	if err != nil {
		return err
	}
	st.a.DockerGID, err = strconv.Atoi(s)
	return err
}

func splitList(s string) []string {
	var out []string
	for _, it := range strings.Split(s, ",") {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

func validList(check func(string) error) func(string) error {
	return func(s string) error {
		for _, it := range splitList(s) {
			if err := check(it); err != nil {
				return err
			}
		}
		return nil
	}
}

func (st *initState) askTargets() error {
	cidrs, err := st.ui.Input("Target database networks (optional)", "CIDRs of the databases bekup backs up (comma-separated), e.g. 10.20.0.0/16.\nEmpty: none yet (runner.toml [targets]; edit it later).", st.targets, validList(setup.ValidateTargetCIDR))
	if err != nil {
		return err
	}
	hosts, err := st.ui.Input("Target database host names (optional)", "Host names of those databases (comma-separated). Empty: none.", st.targetHosts, validList(setup.ValidateTargetHost))
	if err != nil {
		return err
	}
	st.targets, st.targetHosts = cidrs, hosts
	st.a.TargetCIDRs, st.a.TargetHosts = splitList(cidrs), splitList(hosts)
	return nil
}

func (st *initState) summary() error {
	a := st.a
	if err := (&a).Validate(); err != nil {
		return st.noteBack("Something does not fit", err.Error())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Directory:  %s\n", st.env.Dir)
	if a.Image.IsMutable() {
		fmt.Fprintf(&b, "Image:      %s (follows the tag; NOT pinned by digest)\n", a.Image)
	} else {
		fmt.Fprintf(&b, "Image:      %s\n", a.Image)
	}
	if st.compose != nil {
		fmt.Fprintf(&b, "Compose:    compose.yaml (the %s variant; kept)\n", a.Variant)
	} else {
		fmt.Fprintf(&b, "Compose:    compose.yaml, the %s variant (written)\n", a.Variant)
	}
	fmt.Fprintf(&b, "Domain:     https://%s\n", a.Domain)
	switch {
	case a.ProxyMode == setup.ProxyJoin:
		fmt.Fprintf(&b, "Proxy:      your %s on %s; point it at %s\n", a.ProxyContainer, a.ProxyNetwork, a.ProxyTarget())
	case a.Variant.OwnProxy():
		fmt.Fprintf(&b, "Proxy:      link network %s; point your proxy at %s\n", a.Link.Name, a.ProxyTarget())
	}
	if a.ProxyLabels {
		fmt.Fprintf(&b, "            Traefik labels on serve (%s, %s)\n", a.TraefikEntrypoint, a.TraefikCertResolver)
	}
	if a.DB.External() {
		where := ""
		if a.DB.Location == setup.DBDocker {
			where = " on the Docker network " + a.DB.Network
		}
		fmt.Fprintf(&b, "Database:   %s:%d/%s%s (%s)\n", a.DB.Host, a.DB.Port, a.DB.Name, where, a.DB.Auth)
		switch a.DB.TLS.OrDefault() {
		case setup.DBTLSVerifyCAFile:
			fmt.Fprintf(&b, "DB TLS:     verify-full against %s\n", a.DB.CAFile)
		case setup.DBTLSVerifySystem:
			b.WriteString("DB TLS:     verify-full against the system CA bundle\n")
		default:
			fmt.Fprintf(&b, "DB TLS:     %s (WEAKENED)\n", a.DB.TLS)
		}
	} else {
		b.WriteString("Database:   bundled postgres\n")
	}
	if st.keyMode == choiceImport {
		fmt.Fprintf(&b, "Master key: import %s\n", st.importKey)
	} else {
		b.WriteString("Master key: new\n")
	}
	switch {
	case st.caMode == choiceExisting:
		b.WriteString("CA:         existing (secrets/certs/ca)\n")
	case st.passMode == choiceGenerate:
		b.WriteString("CA:         new; passphrase generated (shown next, once)\n")
	default:
		b.WriteString("CA:         new; your passphrase\n")
	}
	fmt.Fprintf(&b, "Docker GID: %d\n", a.DockerGID)
	if len(a.TargetCIDRs)+len(a.TargetHosts) > 0 {
		fmt.Fprintf(&b, "Targets:    %s\n", strings.Join(append(append([]string{}, a.TargetCIDRs...), a.TargetHosts...), ", "))
	}
	if err := st.ui.Note("Summary", strings.TrimRight(b.String(), "\n")); err != nil {
		return err
	}
	ok, err := st.ui.Confirm("Write the deployment now?", "Existing files are never overwritten.", true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBack
	}
	return nil
}

func (st *initState) showPassphrase() error {
	return st.ui.Sensitive("CA key passphrase (shown ONCE)", st.pass, `Store it now, offline (password manager), next to the CA media. It is
needed to renew certificates. It is not shown again and not logged.
WARNING: Docker keeps container output in its logs unless the setup
container runs with --log-driver none (docker run --rm -it --log-driver
none ...); if it did not, delete this container's log.`)
}

// flags is the equivalent `bekup setup init` command line.
func (st *initState) flags() []string {
	a := st.a
	f := []string{"bekup", "setup", "init", "--non-interactive"}
	if st.env.Dir != st.env.DefaultDir {
		f = append(f, "--dir", st.env.Dir)
	}
	if st.env.Ownership != "" && st.env.Ownership != setup.OwnershipAuto {
		f = append(f, "--ownership", string(st.env.Ownership))
	}
	if st.env.AllowVersionMismatch {
		f = append(f, "--allow-version-mismatch")
	}
	if st.compose == nil {
		f = append(f, "--write-compose", "--variant", string(a.Variant))
		if a.DB.External() {
			f = append(f, "--database", "external")
		}
		if st.env.Image != a.Image.String() {
			f = append(f, "--image", a.Image.String())
		}
	}
	if a.Variant == setup.VariantFull {
		f = append(f, "--proxy", string(a.Proxy))
	}
	if a.Variant.OwnProxy() && a.ProxyMode != setup.ProxyLink {
		f = append(f, "--proxy-network", a.ProxyNetwork, "--proxy-container", a.ProxyContainer)
	}
	if a.ProxyLabels {
		f = append(f, "--proxy-labels")
	}
	for _, c := range a.TrustedProxies {
		f = append(f, "--trusted-proxy", c)
	}
	if a.TraefikEntrypoint != "" {
		f = append(f, "--traefik-entrypoint", a.TraefikEntrypoint)
	}
	if a.TraefikCertResolver != "" {
		f = append(f, "--traefik-certresolver", a.TraefikCertResolver)
	}
	f = append(f, "--domain", a.Domain)
	if a.Variant == setup.VariantTraefik {
		f = append(f, "--tls-mode", "acme", "--acme-email", a.ACMEEmail)
		if a.ACMEStaging {
			f = append(f, "--acme-staging")
		}
	}
	if d := a.DB; d.External() {
		if d.Location == setup.DBDocker {
			f = append(f, "--db-location", "docker", "--db-network", d.Network)
		}
		f = append(f, "--db-host", d.Host)
		if d.Port != 0 {
			f = append(f, "--db-port", strconv.Itoa(d.Port))
		}
		if d.Name != "" {
			f = append(f, "--db-name", d.Name)
		}
		if d.TLS != "" {
			f = append(f, "--db-tls", string(d.TLS))
		}
		if d.CAFile != "" {
			f = append(f, "--db-ca-file", d.CAFile)
		}
		if d.TLSServerName != "" {
			f = append(f, "--db-tls-server-name", d.TLSServerName)
		}
		if d.Auth != "" {
			f = append(f, "--db-auth", string(d.Auth))
		}
		if d.User != "" {
			f = append(f, "--db-user", d.User)
		}
		if d.AdminUser != "" {
			f = append(f, "--db-admin-user", d.AdminUser)
		}
		// Entered passwords: the flag mode reads them from files.
		switch d.Auth {
		case setup.DBAuthExistingRoles:
			f = append(f, "--db-password-file", flagAppPasswordFile, "--db-owner-password-file", flagOwnerPasswordFile)
		case setup.DBAuthSingleAccount:
			f = append(f, "--db-password-file", flagAppPasswordFile)
		case setup.DBAuthBootstrap, setup.DBAuthDBASQL: // generated passwords
		}
	}
	if st.keyMode == choiceImport {
		f = append(f, "--import-master-key", st.importKey)
		if st.kcv != "" {
			f = append(f, "--master-key-check-value", st.kcv)
		}
	}
	switch {
	case st.caMode == choiceExisting:
		f = append(f, "--use-existing-ca", "--ca-passphrase-stdin")
	case st.passMode == choiceGenerate:
		f = append(f, "--generate-ca-passphrase")
	default:
		f = append(f, "--ca-passphrase-stdin")
	}
	f = append(f, "--docker-gid", strconv.Itoa(a.DockerGID))
	for _, c := range a.TargetCIDRs {
		f = append(f, "--target-cidr", c)
	}
	for _, h := range a.TargetHosts {
		f = append(f, "--target-host", h)
	}
	return f
}

// Placeholders of the flag line for files that hold entered passwords.
const (
	flagAppPasswordFile   = "PASSWORD_FILE"
	flagOwnerPasswordFile = "OWNER_PASSWORD_FILE"
)

// flagsNote explains the placeholders and stdin of the flag line.
func (st *initState) flagsNote() string {
	var notes []string
	switch st.a.DB.Auth {
	case setup.DBAuthExistingRoles:
		notes = append(notes, flagAppPasswordFile+" and "+flagOwnerPasswordFile+": files in the deployment directory with the bekup_app and bekup_owner passwords (delete them afterwards)")
	case setup.DBAuthSingleAccount:
		notes = append(notes, flagAppPasswordFile+": a file in the deployment directory with the account's password (delete it afterwards)")
	case setup.DBAuthBootstrap, setup.DBAuthDBASQL: // generated passwords
	}
	if st.caMode == choiceExisting || st.passMode == choiceEnter {
		notes = append(notes, "--ca-passphrase-stdin reads the CA key passphrase from the first line of stdin")
	}
	return strings.Join(notes, "; ")
}

func (st *initState) plan() *InitPlan {
	p := &InitPlan{Flags: st.flags(), FlagsNote: st.flagsNote()}
	pass := st.pass
	pw := st.pw
	p.secrets = append(p.secrets, pass)
	for _, v := range pw {
		p.secrets = append(p.secrets, v)
	}
	p.Options = setup.Options{
		Dir: st.env.Dir, Version: st.env.Version, Answers: st.a, Ownership: st.env.Ownership,
		WriteCompose: st.compose == nil, UseExistingCA: st.caMode == choiceExisting,
		ImportMasterKey: st.importKey, MasterKeyCheckValue: st.kcv,
		CAPassphrase: func() ([]byte, error) {
			if len(pass) == 0 {
				return nil, errors.New("no CA key passphrase")
			}
			return bytes.Clone(pass), nil
		},
		DBPassword: func(role string) ([]byte, error) {
			if v, ok := pw[role]; ok {
				return bytes.Clone(v), nil
			}
			return nil, fmt.Errorf("no password was entered for %s", role)
		},
		Out: st.ui.Out(),
	}
	return p
}
