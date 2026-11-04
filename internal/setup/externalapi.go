package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/logging"
)

// The opt-in external API for CI pipelines (D-220, amendment 4). One .env
// line is the source of truth: BEKUP_EXTERNAL_API=enabled selects the
// compose profile's companion settings: the bundled proxy's /api/v1/ route
// (deploy/traefik, deploy/nginx), the role on a new bundled database volume
// (deploy/postgres/init/20-bekup-external.sh) and serve's
// serverInfo.externalApiEnabled; COMPOSE_PROFILES carries external-api so
// `docker compose up -d` starts the service. setup writes both lines,
// external-api.toml (the same trusted proxy as serve's bekup.toml) and,
// always, the external API's secret group (the bundled postgres service and
// an external database's migrate service mount it whether the API is
// enabled or not; the role exists only once it is enabled).

// Names of the external API's compose settings.
const (
	// ExternalAPIProfile is the compose profile of the external-api
	// service.
	ExternalAPIProfile = "external-api"
	// ExternalAPIEnvVar is the .env line (and the variable compose passes
	// to postgres, serve and the bundled proxies).
	ExternalAPIEnvVar = "BEKUP_EXTERNAL_API"
	// ExternalAPIEnabled is its only value.
	ExternalAPIEnabled = "enabled"
	// externalAPIPort is the external API's listen port.
	externalAPIPort = "8081"
	// externalAPIAlias is its network alias on the own proxy's network
	// (httpserver.ExternalAPIAlias).
	externalAPIAlias = "bekup-external-api"
)

// Container paths of the external API's secrets (paths, not secrets).
const (
	extURLTarget = "/run/secrets/bekup_external_database_url"
	// ExternalDBPasswordTarget is the bekup_external password file of an
	// external database in migrate, setup-web-db and the external API.
	ExternalDBPasswordTarget = "/run/secrets/bekup_external_database_password"
)

// externalAPIShape is what external-api.toml must say for a deployment:
// the listener and the trusted proxy (the same proxy serve trusts) and,
// for an external database, the server (bekup.toml's [database]).
type externalAPIShape struct {
	listen         string
	wildcard       bool
	trustedProxies []string
	trustedHosts   []string
	// db is the external database (nil: the bundled postgres service, URL
	// mode).
	db *externalAPIDB
}

// externalAPIDB is the external database in external-api.toml (fields
// mode, user bekup_external).
type externalAPIDB struct {
	host          string
	port          int
	name          string
	tlsMode       string
	tlsCAFile     string
	tlsServerName string
}

// externalAPIShape derives external-api.toml from the init answers.
func (a *Answers) externalAPIShape() externalAPIShape {
	s := externalAPIShape{trustedProxies: a.trustedProxies(), trustedHosts: a.trustedProxyHosts()}
	if a.Variant.OwnProxy() {
		s.listen, s.wildcard = config.ExternalAPIJoinListen, true
	} else {
		s.listen = a.Network.ExternalAPIFrontendIP + ":" + externalAPIPort
	}
	if a.DB.External() {
		mode, ca := a.DB.TLS.configKeys()
		s.db = &externalAPIDB{host: a.DB.Host, port: a.DB.Port, name: a.DB.Name, tlsMode: mode, tlsCAFile: ca, tlsServerName: a.DB.TLSServerName}
	}
	return s
}

// stateExternalAPIShape derives external-api.toml for an existing
// deployment (upgrade --external-api, check): the recorded proxy, the
// addresses of .env and, for --proxy none and an external database,
// bekup.toml (b; nil when it does not load).
func stateExternalAPIShape(st *State, env map[string]string, b *config.Bekup) externalAPIShape {
	def := DefaultNetwork()
	s := externalAPIShape{trustedProxies: []string{}}
	switch {
	case st.ownProxyJoin():
		s.listen, s.wildcard = config.ExternalAPIJoinListen, true
		s.trustedHosts = []string{st.ProxyContainer}
	case st.Proxy == ProxyExternal:
		s.listen, s.wildcard = config.ExternalAPIJoinListen, true
		s.trustedProxies = []string{st.ProxyIP + "/32"}
	default:
		s.listen = envOr(env, "EXTERNAL_API_FRONTEND_IP", def.ExternalAPIFrontendIP) + ":" + externalAPIPort
		switch {
		case st.Proxy == ProxyNone && b != nil:
			s.trustedProxies = slices.Clone(b.Server.TrustedProxies)
		case st.Proxy != ProxyNone:
			s.trustedProxies = []string{envOr(env, "PROXY_FRONTEND_IP", def.ProxyFrontendIP) + "/32"}
		}
	}
	if st.dbMode() == DBExternal && b != nil {
		d := &b.Database
		s.db = &externalAPIDB{host: d.Host, port: d.Port, name: d.Name, tlsMode: d.TLSMode, tlsCAFile: d.TLSCAFile, tlsServerName: d.TLSServerName}
	}
	return s
}

// configKeys are tls_mode and tls_ca_file of the database TLS choice
// (bekup.toml and external-api.toml alike).
func (t DBTLS) configKeys() (mode, caFile string) {
	switch t.OrDefault() {
	case DBTLSVerifySystem:
		return config.DBTLSVerifyFull, config.SystemCABundle
	case DBTLSRequire:
		return config.DBTLSRequire, ""
	case DBTLSDisable:
		return config.DBTLSDisable, ""
	default:
		return config.DBTLSVerifyFull, dbCATarget
	}
}

// renderExternalAPITOML writes external-api.toml for the shape: only what
// the deployment decides; every other key keeps its built-in default.
func renderExternalAPITOML(s externalAPIShape) []byte {
	var b bytes.Buffer
	b.WriteString(`# external-api.toml for the opt-in external API (D-220): written by
# ` + "`bekup setup init|upgrade --external-api`" + `. Operator file: edit it freely; setup
# never rewrites it, ` + "`bekup setup check`" + ` validates it. Keys that are not set keep
# their built-in defaults; reference/external-api.example.toml documents every
# key. No secrets belong here (only *_file paths).

[listen]
`)
	if s.wildcard {
		b.WriteString("# The external API joins your proxy's network, where Docker assigns its\n")
		b.WriteString("# address, so it binds every interface of its container (no port is\n")
		b.WriteString("# published). In the own-proxy compose shape (BEKUP_LISTEN_SHAPE) it accepts\n")
		b.WriteString("# connections only on its own address on your proxy's network (the alias\n")
		b.WriteString("# " + externalAPIAlias + ") and on loopback.\n")
	} else {
		b.WriteString("# Only its fixed address on the frontend network (EXTERNAL_API_FRONTEND_IP).\n")
	}
	fmt.Fprintf(&b, "address = %s\n", quoteTOML(s.listen))
	if s.wildcard {
		b.WriteString("allow_wildcard = true\n")
	}
	switch {
	case len(s.trustedHosts) > 0:
		b.WriteString("# Your reverse proxy by its container name, exactly as serve trusts it\n")
		b.WriteString("# (bekup.toml [server] trusted_proxy_hosts): X-Forwarded-For only from it.\n")
	default:
		b.WriteString("# The reverse proxy serve trusts (bekup.toml [server] trusted_proxies): its\n")
		b.WriteString("# X-Forwarded-For is the client address every token's allowlist is checked\n")
		b.WriteString("# against; X-Forwarded-For from any other peer is ignored.\n")
	}
	fmt.Fprintf(&b, "trusted_proxies = %s\n", quoteTOMLList(nonNil(s.trustedProxies)))
	if len(s.trustedHosts) > 0 {
		fmt.Fprintf(&b, "trusted_proxy_hosts = %s\n", quoteTOMLList(s.trustedHosts))
	}
	b.WriteString("\n[database]\n")
	if s.db == nil {
		b.WriteString("# The bundled postgres service as bekup_external: the URL secret compose\n")
		b.WriteString("# mounts, TLS verify-full against the bundled CA (the defaults).\n")
		fmt.Fprintf(&b, "url_file = %s\n", quoteTOML(extURLTarget))
		return b.Bytes()
	}
	b.WriteString("# Your PostgreSQL (fields mode, D-207), the server and TLS choice of\n")
	b.WriteString("# bekup.toml [database], as bekup_external with the password file compose mounts.\n")
	b.WriteString("url_file = \"\"\n")
	fmt.Fprintf(&b, "host = %s\n", quoteTOML(s.db.host))
	fmt.Fprintf(&b, "port = %d\n", s.db.port)
	fmt.Fprintf(&b, "name = %s\n", quoteTOML(s.db.name))
	fmt.Fprintf(&b, "user = %s\n", quoteTOML(config.DBExternalRole))
	fmt.Fprintf(&b, "password_file = %s\n", quoteTOML(ExternalDBPasswordTarget))
	if s.db.tlsMode != config.DBTLSVerifyFull {
		b.WriteString("# EXPLICIT WEAKENING (D-100, D-215), as in bekup.toml: warned at every start.\n")
	}
	fmt.Fprintf(&b, "tls_mode = %s\n", quoteTOML(s.db.tlsMode))
	fmt.Fprintf(&b, "tls_ca_file = %s\n", quoteTOML(s.db.tlsCAFile))
	if s.db.tlsServerName != "" {
		fmt.Fprintf(&b, "tls_server_name = %s\n", quoteTOML(s.db.tlsServerName))
	}
	return b.Bytes()
}

// composeProfiles is the COMPOSE_PROFILES value of a deployment: the full
// variant's bundled proxy and the external API's profile ("" for none).
func composeProfiles(v Variant, p Proxy, externalAPI bool) string {
	var out []string
	if v == VariantFull && p != ProxyNone && p != "" {
		out = append(out, string(p))
	}
	if externalAPI {
		out = append(out, ExternalAPIProfile)
	}
	return strings.Join(out, ",")
}

// profileList splits a COMPOSE_PROFILES value (comma-separated, blanks
// ignored) into its sorted, de-duplicated profiles.
func profileList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// withProfile returns COMPOSE_PROFILES cur with profile added (the
// operator's other profiles are kept, in their order).
func withProfile(cur, profile string) string {
	if slices.Contains(profileList(cur), profile) {
		return cur
	}
	if strings.TrimSpace(cur) == "" {
		return profile
	}
	return strings.TrimRight(strings.TrimSpace(cur), ",") + "," + profile
}

// generateExternalAPI writes the external API's secret group (D-220):
// bundled, the role password (read by the postgres init script) and the
// URL the external API connects with; external, the password file (migrate
// creates the role with it, the external API logs in with it). keep, when
// set, receives a copy of the password for the roles SQL of dba-sql (the
// caller zeroes it).
func generateExternalAPI(w *writer, l Layout, db DBMode, keep func([]byte)) (retErr error) {
	pw, err := randomPassword()
	if err != nil {
		return err
	}
	un := logging.DefaultRegistry.Register(pw)
	defer func() { crypto.Zero(pw); un() }()
	if keep != nil {
		keep(bytes.Clone(pw))
	}
	if db == DBExternal {
		return w.create(l.keys(extDBPasswordName), pw, 0o400)
	}
	u := databaseURL(config.DBExternalRole, pw)
	unURL := logging.DefaultRegistry.Register(u)
	defer func() { crypto.Zero(u); unURL() }()
	if err := w.create(l.keys(extPassName), pw, 0o400); err != nil {
		return err
	}
	return w.create(l.keys(extURLName), u, 0o400)
}

// externalAPIProxyHints are the /api/v1/ route settings of the common
// reverse proxies for the external API at target (own proxy and --proxy
// none), indented for the reports.
func externalAPIProxyHints(target string, labels bool) string {
	hostPort := strings.TrimPrefix(target, "http://")
	traefik := `       Traefik (file provider): a second router for the domain with the rule
         Host(` + "`<domain>`" + `) && PathPrefix(` + "`/api/v1/`" + `), priority above the UI router,
         a buffering middleware with maxRequestBodyBytes 16384 and a service whose
         loadBalancer url is ` + target + `.
`
	if labels {
		traefik = `       Traefik (Docker provider): the labels on external-api in ` + OverrideFile + `
         route it (router bekup-api, priority 400, bodies at most 16 KiB).
`
	}
	return `       Nginx Proxy Manager: on the Proxy Host, a Custom Location /api/v1/ with
         scheme http, forward hostname ` + strings.Split(hostPort, ":")[0] + `, port ` + externalAPIPort + `, and in its
         Advanced field: client_max_body_size 16k;
` + traefik + `       Caddy: handle /api/v1/* { request_body { max_size 16KB }
         reverse_proxy ` + hostPort + ` } before the UI's reverse_proxy
       nginx: location ^~ /api/v1/ { client_max_body_size 16k;
         proxy_pass ` + target + `; X-Forwarded-For $remote_addr; X-Forwarded-Proto https }
       Keep /api/download/ and everything else on the UI's route.
       docs/installation/reverse-proxy.md#external-api
`
}

// externalAPITarget is the URL the operator's own proxy forwards /api/v1/
// to ("" with a bundled proxy).
func (a *Answers) externalAPITarget() string {
	switch {
	case a.Variant.OwnProxy():
		return "http://" + externalAPIAlias + ":" + externalAPIPort
	case a.Proxy == ProxyNone:
		return "http://" + a.Network.ExternalAPIFrontendIP + ":" + externalAPIPort
	}
	return ""
}

// stateExternalAPITarget is externalAPITarget for an existing deployment.
func stateExternalAPITarget(st *State, env map[string]string) string {
	switch {
	case st.Variant.OwnProxy():
		return "http://" + externalAPIAlias + ":" + externalAPIPort
	case st.Proxy == ProxyNone:
		return "http://" + envOr(env, "EXTERNAL_API_FRONTEND_IP", DefaultNetwork().ExternalAPIFrontendIP) + ":" + externalAPIPort
	}
	return ""
}

// bundledRoleCommand is the documented command that creates bekup_external
// on an existing bundled database volume (D-220 item 11): it runs the init
// script in the running postgres container as its user (UID 70), which
// reads the superuser password from its secret for the local socket login
// (PGPASSWORD, never on a command line of the host) and the role password
// from the bekup_external_password secret; --reload applies the pg_hba
// rule. The container needs BEKUP_EXTERNAL_API=enabled (recreated by `up
// -d` after the .env change).
const bundledRoleCommand = `docker compose exec postgres sh -c 'PGPASSWORD="$(cat /run/secrets/postgres_password)" /docker-entrypoint-initdb.d/20-bekup-external.sh --reload'`

// externalRoleFlag is the bootstrap flag that creates bekup_external with
// the password file compose mounts into migrate.
const externalRoleFlag = "--external-password-file " + ExternalDBPasswordTarget

// externalRoleBootstrapStep creates only bekup_external on an external
// database whose bekup roles exist already (existing roles, or an
// existing deployment enabling the external API): bootstrap adopts and
// checks the existing roles without changing them.
func externalRoleBootstrapStep(d *Database, insecure, insecureNote string) string {
	return fmt.Sprintf("docker compose run --rm migrate db bootstrap --admin-user <admin role> --adopt-existing-roles%s %s\n     (an account with CREATEROLE, e.g. your DBA's: creates bekup_external, the external API's\n     role, with the generated password; bekup_owner, bekup_app and the database %s exist\n     already and are only checked)%s", insecure, externalRoleFlag, d.Name, insecureNote)
}

// externalAPI checks external-api.toml (D-220 amendment 4): with the
// external API enabled it must load and trust exactly the proxy serve
// trusts, listen where the compose shape expects and reach the database
// with the login compose mounts; without, it has no effect.
func (c *checker) externalAPI(env map[string]string, b *config.Bekup) {
	fi, err := checkNoSymlinks(c.root, ExternalAPITOML)
	if err != nil {
		return // reported by the manifest
	}
	if !c.st.ExternalAPI {
		if fi != nil {
			c.add(SeverityWarning, ExternalAPITOML, "has no effect: the external API is not enabled (enable it with `docker compose run --rm setup upgrade --external-api`)")
		}
		return
	}
	if fi == nil {
		return // reported by the manifest
	}
	opts := config.Options{LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}, ConfigPath: filepath.Join(c.root, ExternalAPITOML)}
	res, err := config.LoadExternalAPI(opts)
	if err != nil {
		c.add(SeverityError, ExternalAPITOML, "%v", err)
		return
	}
	e, want := res.Config, stateExternalAPIShape(c.st, env, b)
	if e.Listen.Address != want.listen {
		why := "EXTERNAL_API_FRONTEND_IP"
		if want.wildcard {
			why = "the own-proxy join shape, D-220"
		}
		c.add(SeverityError, ExternalAPITOML, "listen.address = %q, want %q (%s)", e.Listen.Address, want.listen, why)
	}
	if want.wildcard && !e.Listen.AllowWildcard {
		c.add(SeverityError, ExternalAPITOML, "listen.allow_wildcard must be true in the own-proxy join shape (the external API listens on %s)", config.ExternalAPIJoinListen)
	}
	if (c.st.Proxy != ProxyNone || b != nil) && !slices.Equal(e.Listen.TrustedProxies, want.trustedProxies) {
		c.add(SeverityError, ExternalAPITOML, "listen.trusted_proxies = %q, want exactly %q (the proxy serve trusts: the client address every token's allowlist is checked against)", e.Listen.TrustedProxies, want.trustedProxies)
	}
	c.wideCIDRs(ExternalAPITOML, "listen.trusted_proxies", e.Listen.TrustedProxies)
	got := e.Listen.TrustedProxyHosts
	switch {
	case len(want.trustedHosts) > 0:
		if len(got) != 1 || !strings.EqualFold(got[0], want.trustedHosts[0]) {
			c.add(SeverityError, ExternalAPITOML, "listen.trusted_proxy_hosts = %q, want exactly %q (your proxy's container)", got, want.trustedHosts)
		}
	case len(got) > 0 && c.st.Proxy != ProxyNone:
		c.add(SeverityError, ExternalAPITOML, "listen.trusted_proxy_hosts = %q: this deployment trusts its proxy by address only; remove them", got)
	}
	d := &e.Database
	if want.db == nil {
		if d.FieldsMode() || d.URLFile != extURLTarget {
			c.add(SeverityError, ExternalAPITOML, "[database] must use url_file = %q (the bekup_external_database_url secret compose mounts) with the bundled database", extURLTarget)
		}
		if d.TLSMode != config.DBTLSVerifyFull {
			c.add(SeverityWarning, ExternalAPITOML, "database.tls_mode = %q is weaker than verify-full against the bundled postgres CA (D-100)", bounded(d.TLSMode))
		}
		return
	}
	if !d.FieldsMode() {
		c.add(SeverityError, ExternalAPITOML, "[database] has no host, but this deployment uses your PostgreSQL: use the fields mode setup wrote (host, port, name, user, password_file)")
		return
	}
	if d.Host != want.db.host || d.Port != want.db.port || d.Name != want.db.name {
		c.add(SeverityError, ExternalAPITOML, "[database] names %s:%d/%s, but bekup.toml names %s:%d/%s", bounded(d.Host), d.Port, bounded(d.Name), bounded(want.db.host), want.db.port, bounded(want.db.name))
	}
	if d.PasswordFile != ExternalDBPasswordTarget {
		c.add(SeverityError, ExternalAPITOML, "database.password_file = %q, want %q (the bekup_external_database_password secret compose mounts)", bounded(d.PasswordFile), ExternalDBPasswordTarget)
	}
	if d.TLSMode != want.db.tlsMode || d.TLSCAFile != want.db.tlsCAFile || d.TLSServerName != want.db.tlsServerName {
		sev := SeverityError
		if d.TLSMode == config.DBTLSVerifyFull && want.db.tlsMode != config.DBTLSVerifyFull {
			sev = SeverityWarning // stricter than bekup.toml
		}
		c.add(sev, ExternalAPITOML, "database.tls_mode/tls_ca_file/tls_server_name = %q/%q/%q differ from bekup.toml's %q/%q/%q", bounded(d.TLSMode), bounded(d.TLSCAFile), bounded(d.TLSServerName), want.db.tlsMode, want.db.tlsCAFile, want.db.tlsServerName)
	}
}

// externalAPIPlan is what an upgrade does for the external API.
type externalAPIPlan struct {
	// toml is external-api.toml to create (nil: it exists, or the external
	// API stays off).
	toml []byte
	// env are the .env lines to set (enabling only).
	env []EnvVar
	// profiles is the COMPOSE_PROFILES value written.
	profiles string
	// steps are the remaining steps printed after enabling.
	steps string
}

// planExternalAPI prepares the external API part of an upgrade: enable
// (`setup upgrade --external-api`) writes external-api.toml when missing,
// BEKUP_EXTERNAL_API=enabled and the profile, and returns the steps that
// remain (the database role, migrate, a block for an existing
// compose.override.yaml, your proxy's route); an enabled deployment gets
// a missing external-api.toml back.
func planExternalAPI(root string, st *State, enable bool, opts UpgradeOptions, gen map[string][]byte, compose *ComposeInfo, hasOverride bool) (externalAPIPlan, error) {
	var p externalAPIPlan
	if !enable && !st.ExternalAPI {
		return p, nil
	}
	env, err := readDotEnv(root)
	if err != nil {
		return p, err
	}
	bres, err := config.LoadBekup(config.Options{LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}, ConfigPath: filepath.Join(root, BekupTOML)})
	if err != nil {
		return p, fmt.Errorf("%s: %w", BekupTOML, err)
	}
	next := *st
	next.ExternalAPI = true
	if !st.Variant.OwnProxy() {
		def := DefaultNetwork()
		n := Network{
			FrontendSubnet:        envOr(env, "FRONTEND_SUBNET", def.FrontendSubnet),
			ServeFrontendIP:       envOr(env, "SERVE_FRONTEND_IP", def.ServeFrontendIP),
			ProxyFrontendIP:       envOr(env, "PROXY_FRONTEND_IP", def.ProxyFrontendIP),
			ExternalAPIFrontendIP: envOr(env, "EXTERNAL_API_FRONTEND_IP", def.ExternalAPIFrontendIP),
		}
		if err := checkExternalAPIIP(n); err != nil {
			return p, fmt.Errorf("--external-api: %w; set EXTERNAL_API_FRONTEND_IP in %s", err, EnvFile)
		}
	}
	fi, err := checkNoSymlinks(root, ExternalAPITOML)
	if err != nil {
		return p, err
	}
	if fi == nil {
		p.toml = renderExternalAPITOML(stateExternalAPIShape(&next, env, bres.Config))
	}
	if !enable {
		return p, nil
	}
	p.profiles = withProfile(env["COMPOSE_PROFILES"], ExternalAPIProfile)
	p.env = []EnvVar{{ExternalAPIEnvVar, ExternalAPIEnabled}, {"COMPOSE_PROFILES", p.profiles}}

	var b strings.Builder
	step := 1
	b.WriteString("\nThe external API is enabled in the files (D-220). Finish it in the deployment directory:\n")
	newOv := next.overlays()
	if block := externalAPIOverlay(newOv); block != "" && hasOverride && !overrideCarries(root, st, opts, gen, compose, newOv) {
		fmt.Fprintf(&b, "  %d. Add this block to %s (setup never rewrites it; merge it into its\n     services: key), so the external API reaches your database container / gets\n     its Traefik route:\n%s", step, OverrideFile, indentBlock(block, "       "))
		step++
	}
	if st.dbMode() == DBExternal {
		d := Database{Name: bres.Config.Database.Name, TLS: st.dbTLS()}
		insecure, note := insecureAdmin(d.TLS)
		fmt.Fprintf(&b, "  %d. %s\n", step, externalRoleBootstrapStep(&d, insecure, note))
		step++
	} else {
		fmt.Fprintf(&b, "  %d. docker compose up -d postgres\n     (recreates postgres with BEKUP_EXTERNAL_API=enabled; the data volume is kept)\n", step)
		fmt.Fprintf(&b, "  %d. %s\n     (creates bekup_external on the existing volume as the postgres user; a new volume\n     creates it by itself)\n", step+1, bundledRoleCommand)
		step += 2
	}
	fmt.Fprintf(&b, "  %d. docker compose run --rm migrate\n     (grants bekup_external its api functions; every `migrate up` re-applies them)\n", step)
	fmt.Fprintf(&b, "  %d. docker compose up -d\n     (starts the external API; it refuses to start, and restarts, until the role exists and migrate ran)\n", step+1)
	step += 2
	if t := stateExternalAPITarget(st, env); t != "" {
		fmt.Fprintf(&b, "  %d. Route https://%s/api/v1/ to the external API at %s:\n%s", step, envOr(env, "BEKUP_DOMAIN", "<domain>"), t, externalAPIProxyHints(t, st.ProxyLabels))
		step++
	}
	fmt.Fprintf(&b, "  %d. docker compose -f compose.yaml run --rm setup check\n  docs/guides/external-api.md\n", step)
	p.steps = b.String()
	return p, nil
}

// overrideCarries reports whether the existing compose.override.yaml
// already carries the overlays ov (the lint passes with them).
func overrideCarries(root string, st *State, opts UpgradeOptions, gen map[string][]byte, compose *ComposeInfo, ov Overlays) bool {
	if compose != nil {
		info, err := inspectCompose(root, opts.assets, st.Variant, "", ov, nil, os.LookupEnv, true)
		return err == nil && !info.HasErrors()
	}
	override, err := readManaged(root, OverrideFile)
	if err != nil {
		return false
	}
	_, err = checkSetupFirstOverride(root, gen[ComposeFile], override, st.Variant, opts.Image, ov, os.LookupEnv)
	return err == nil
}

// indentBlock indents every line of s with prefix.
func indentBlock(s, prefix string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l != "" {
			b.WriteString(prefix + l)
		}
	}
	return b.String()
}
