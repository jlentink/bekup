package setup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
)

// Options configure Init.
type Options struct {
	// Dir is the deployment directory (it must exist).
	Dir string
	// Version is the version of this bekup binary.
	Version string
	// Answers are validated by Init.
	Answers Answers
	// Ownership is the --ownership mode (default auto).
	Ownership OwnershipMode
	// KeepExisting keeps existing files and creates only missing ones.
	KeepExisting bool
	// Force rewrites existing non-secret files (never secrets).
	Force bool
	// RegenerateSecrets deletes and regenerates every secret (the caller
	// has obtained the typed confirmation on a terminal).
	RegenerateSecrets bool
	// WriteCompose writes compose.yaml from Answers.Variant (setup-first).
	// Without it compose.yaml must exist (compose-first, D-205): Init reads
	// it, requires its variant and pinned image to equal the answers' and
	// never writes it.
	WriteCompose bool
	// CAPassphrase supplies the passphrase that seals the new CA key. It
	// is called only when certificates are generated, before any file is
	// written; Init zeroes the result.
	CAPassphrase func() ([]byte, error)
	// Out receives the report (never a secret value).
	Out io.Writer
	// Now defaults to time.Now.
	Now func() time.Time

	assets    fs.FS
	sys       *sysEnv
	certsInit certsInitFunc
}

func (o *Options) defaults() {
	if o.Ownership == "" {
		o.Ownership = OwnershipAuto
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.assets == nil {
		o.assets = bekup.Assets()
	}
	if o.sys == nil {
		s := defaultSysEnv()
		o.sys = &s
	}
	if o.certsInit == nil {
		o.certsInit = certs.Init
	}
}

// plan is what one run writes.
type plan struct {
	entries   []Entry
	generated map[string][]byte
	operator  map[string][]byte
	assets    []string
}

func newPlan(fsys fs.FS, a *Answers, writeCompose bool) (*plan, error) {
	assets, err := assetFiles(fsys, a.Variant.assetDirs())
	if err != nil {
		return nil, err
	}
	gen, err := generatedFiles(fsys, a.Image, a.Variant, writeCompose)
	if err != nil {
		return nil, err
	}
	static := a.bundledProxy() && a.TLSMode == TLSStatic
	return &plan{
		entries:   Manifest(Layout{KeysDir: a.KeysDir}, assets, ManifestOptions{StaticTLS: static, ComposeOperator: !writeCompose}),
		generated: gen,
		operator:  operatorFiles(a),
		assets:    assets,
	}, nil
}

// groupState is the presence of one secret group's required files.
type groupState struct {
	present, total int
	paths          []string
}

func (g groupState) complete() bool { return g.total > 0 && g.present == g.total }
func (g groupState) empty() bool    { return g.present == 0 }

func secretGroupStates(root string, entries []Entry) (map[string]*groupState, error) {
	out := map[string]*groupState{}
	for _, e := range entries {
		if e.Group == "" || e.Kind != KindFile || e.Optional {
			continue
		}
		g := out[e.Group]
		if g == nil {
			g = &groupState{}
			out[e.Group] = g
		}
		g.total++
		fi, err := checkNoSymlinks(root, e.Path)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			g.present++
			g.paths = append(g.paths, e.Path)
		}
	}
	return out, nil
}

// Init writes a new deployment directory (see the package documentation).
// On any error everything this run created is removed again.
func Init(opts Options) (retErr error) {
	opts.defaults()
	a := &opts.Answers
	var compose *ComposeInfo
	if !opts.WriteCompose {
		// Compose-first: the operator's compose.yaml decides the variant
		// and the image (both must match when the caller set them).
		root, err := filepath.Abs(opts.Dir)
		if err != nil {
			return err
		}
		if compose, err = inspectCompose(root, opts.assets, a.Variant, nil, os.LookupEnv); err != nil {
			return err
		}
		if a.Variant == "" {
			a.Variant = compose.Variant
		}
		if a.Image.Digest == "" {
			a.Image = compose.Image
		}
		a.ImageInEnv = compose.FromEnv
	}
	if err := a.Validate(); err != nil {
		return err
	}
	w, err := newWriter(opts.Dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, w.close()) }()
	root := w.root
	if err := refuseShadowFiles(root); err != nil {
		return err
	}
	if compose != nil {
		if err := composeMatches(compose, a); err != nil {
			return err
		}
	}
	p, err := newPlan(opts.assets, a, opts.WriteCompose)
	if err != nil {
		return err
	}
	layout := Layout{KeysDir: a.KeysDir}

	// What exists already?
	var existing []string
	for _, e := range p.entries {
		if e.Kind != KindFile || e.Class == ClassSecret || e.Path == ComposeFile && !opts.WriteCompose {
			continue
		}
		fi, err := checkNoSymlinks(root, e.Path)
		if err != nil {
			return err
		}
		if fi != nil {
			existing = append(existing, e.Path)
		}
	}
	groups, err := secretGroupStates(root, p.entries)
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(groups) {
		g := groups[name]
		if !g.empty() {
			existing = append(existing, g.paths...)
		}
		if !g.empty() && !g.complete() && !opts.RegenerateSecrets {
			return fmt.Errorf("the %s secrets are incomplete (%d of %d files exist: %s); restore the missing files from your escrow, or regenerate all secrets with --regenerate-secrets",
				name, g.present, g.total, strings.Join(g.paths, ", "))
		}
	}
	if len(existing) > 0 && !opts.KeepExisting && !opts.Force && !opts.RegenerateSecrets {
		return fmt.Errorf("%s is not empty (%d managed files exist, e.g. %s): use `bekup setup check` / `bekup setup upgrade` for an existing deployment, --keep-existing to create only the missing files, or --force to rewrite the non-secret files",
			root, len(existing), existing[0])
	}
	if opts.RegenerateSecrets && opts.KeepExisting {
		return errors.New("--regenerate-secrets and --keep-existing are mutually exclusive")
	}
	needCerts := opts.RegenerateSecrets || groups[groupCerts].empty()
	if needCerts && !opts.RegenerateSecrets {
		// Before anything is written: an existing (offline) CA is never
		// reused, overwritten or touched by a rollback.
		if err := refuseExistingCerts(w, layout); err != nil {
			return err
		}
	}
	var pass []byte
	if needCerts {
		if opts.CAPassphrase == nil {
			return errors.New("a CA key passphrase is required to create the certificates")
		}
		if pass, err = opts.CAPassphrase(); err != nil {
			return err
		}
		defer crypto.Zero(pass)
		if err := certs.ValidatePassphrase(pass); err != nil {
			return err
		}
	}
	mode, why := resolveOwnership(opts.Ownership, root, *opts.sys)
	warnings := rootWarnings(w, mode, *opts.sys)

	committed := false
	defer func() {
		if retErr != nil && !committed {
			if rbErr := w.rollback(); rbErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback incomplete: %w", rbErr))
			}
		}
	}()

	if opts.RegenerateSecrets {
		// The old secrets are moved aside, not deleted: a failure below
		// moves them back; they are deleted only after the run succeeded.
		if err := setAsideSecrets(w, layout, p.entries); err != nil {
			return err
		}
		for _, g := range groups {
			g.present = 0
		}
	}

	// Directories first, in manifest order, with their final modes.
	for _, e := range p.entries {
		if e.Kind != KindDir || (e.Offline && !needCerts) {
			continue
		}
		if err := w.mkdirAll(e.Path, e.Mode); err != nil {
			return err
		}
	}
	// Generated and operator files.
	write := func(files map[string][]byte, entries []Entry) error {
		for _, e := range entries {
			data, ok := files[e.Path]
			if !ok {
				continue
			}
			fi, err := checkNoSymlinks(root, e.Path)
			if err != nil {
				return err
			}
			switch {
			case fi == nil:
				err = w.create(e.Path, data, e.Mode)
			case opts.Force:
				err = w.replace(e.Path, data, e.Mode)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(p.generated, p.entries); err != nil {
		return err
	}
	if err := write(p.operator, p.entries); err != nil {
		return err
	}
	if a.ImageInEnv {
		// compose.yaml takes the image from BEKUP_IMAGE: .env must carry
		// it for `docker compose up`, also when .env existed already.
		if _, err := syncEnvImage(w, a.Image.String()); err != nil {
			return err
		}
	}

	// Secrets: only groups that are entirely missing.
	var keyID string
	if groups[groupMasterKey].empty() {
		if keyID, err = generateMasterKey(w, layout, opts.Now()); err != nil {
			return err
		}
	}
	if groups[groupDatabase].empty() {
		if err := generateDatabase(w, layout); err != nil {
			return err
		}
	}
	if needCerts {
		if err := generateCerts(w, layout, pass, opts.certsInit); err != nil {
			return err
		}
	}

	if err := validateConfigs(root); err != nil {
		return err
	}

	// State file (rewritten only when missing or forced).
	st := &State{
		Schema: StateSchema, ManifestVersion: ManifestVersion, BekupVersion: opts.Version,
		Image: a.Image.String(), ComposeMode: ComposeGenerated, Variant: a.Variant,
		Proxy: a.Proxy, TLSMode: a.TLSMode, KeysDir: a.KeysDir,
		Ownership: mode, Assets: map[string]string{}, SecretGroups: secretGroups(p.entries),
	}
	if compose != nil {
		st.ComposeMode, st.ComposeSHA256 = ComposeOperator, compose.Hash
		if compose.FromEnv {
			st.ImageSource = ImageSourceEnv
		}
	}
	if a.Variant.External() {
		st.ProxyNetwork, st.ProxySubnet, st.ProxyIP, st.ServeIP = a.Link.Name, a.Link.Subnet, a.Link.ProxyIP, a.Link.ServeIP
	}
	for path, data := range p.generated {
		st.Assets[path] = hashOf(data)
	}
	if err := writeState(w, st, opts.Force); err != nil {
		return err
	}

	var chownCmds strings.Builder
	ownWarnings, err := applyOwnership(w.r, p.entries, mode, *opts.sys, &chownCmds)
	if err != nil {
		return err
	}
	warnings = append(warnings, ownWarnings...)
	warnings = append(warnings, wideCIDRWarnings(a)...)
	if compose != nil {
		for _, f := range compose.Findings {
			warnings = append(warnings, f.String())
		}
		if compose.Level == RecognizedCustom {
			warnings = append(warnings, fmt.Sprintf("%s is customized (based on the %s variant): the hardening lint found no errors, but bekup cannot vouch for your changes; `setup check` lints it on every run", ComposeFile, a.Variant))
		}
	}
	rd := &reportData{
		root: root, a: a, l: layout, entries: p.entries, w: w, mode: mode, why: why,
		chownCmds: chownCmds.String(), keyID: keyID, newCerts: needCerts, env: *opts.sys,
		created: countCreatedFiles(w),
	}
	// The run succeeded: delete the old secrets that were moved aside. From
	// here on nothing is rolled back.
	committed = true
	if left, err := w.commit(); err != nil {
		warnings = append(warnings, fmt.Sprintf("could not delete the previous secrets (%s): %v; delete them by hand", strings.Join(left, ", "), err))
	}
	rd.warnings = warnings
	return report(opts.Out, rd)
}

// composeMatches requires the operator's compose.yaml (compose-first) to
// be the variant and image of the answers.
func composeMatches(c *ComposeInfo, a *Answers) error {
	if c.Image.String() != a.Image.String() {
		if c.FromEnv {
			return fmt.Errorf("the image %s does not match %s=%s, which the x-bekup-image line of %s resolves to: run setup with that variable (`%s=<image> docker compose run --rm setup`)", a.Image, ImageEnvVar, c.Image, ComposeFile, ImageEnvVar)
		}
		return fmt.Errorf("the image %s does not match the x-bekup-image line of %s (%s): run setup from that file (`docker compose run --rm setup`)", a.Image, ComposeFile, c.Image)
	}
	if c.Variant != a.Variant {
		return fmt.Errorf("%s is the %s variant, not %s", ComposeFile, c.Variant, a.Variant)
	}
	if c.HasErrors() {
		var b strings.Builder
		for _, f := range c.Findings {
			if f.Severity == SeverityError {
				b.WriteString("\n  " + f.String())
			}
		}
		return fmt.Errorf("%s (with %s, if present) fails the hardening lint:%s", ComposeFile, OverrideFile, b.String())
	}
	return nil
}

// refuseShadowFiles refuses compose files that docker compose would read
// next to (or instead of) compose.yaml.
func refuseShadowFiles(root string) error {
	for _, name := range composeShadowFiles {
		fi, err := checkNoSymlinks(root, name)
		if err != nil {
			return err
		}
		if fi != nil {
			return fmt.Errorf("%s exists: docker compose would read it next to or instead of %s; move it away first", name, ComposeFile)
		}
	}
	return nil
}

// countCreatedFiles counts the regular files this run created.
func countCreatedFiles(w *writer) int {
	n := 0
	for _, p := range w.created {
		if fi, err := w.r.Lstat(osRel(p)); err == nil && fi.Mode().IsRegular() {
			n++
		}
	}
	return n
}

// rootWarnings warns when the deployment directory may be written by
// someone else than root (setup check reports it as an error; with
// --ownership chown setup fixes it).
func rootWarnings(w *writer, mode OwnershipMode, env sysEnv) []string {
	fi, err := w.r.Lstat(".")
	if err != nil {
		return nil
	}
	var out []string
	if fi.Mode().Perm()&0o022 != 0 {
		out = append(out, fmt.Sprintf("the deployment directory %s is group- or world-writable (mode %04o): whoever can write it can replace the compose file; setup narrows it to at most 0755", w.root, fi.Mode().Perm()))
	}
	if mode != OwnershipNone {
		if uid, _, ok := env.owner(w.r, ".", fi); ok && uid != 0 {
			out = append(out, fmt.Sprintf("the deployment directory %s belongs to UID %d, not root: that user can replace every file in it (chown root:root it; setup check reports it)", w.root, uid))
		}
	}
	return out
}

func writeState(w *writer, st *State, force bool) error {
	data, err := st.Marshal()
	if err != nil {
		return err
	}
	fi, err := checkNoSymlinks(w.root, StateFileName)
	if err != nil {
		return err
	}
	switch {
	case fi == nil:
		return w.create(StateFileName, data, 0o644)
	case force:
		return w.replace(StateFileName, data, 0o644)
	}
	return nil
}

// setAsideSecrets moves every existing secret file of the manifest and
// every file certs.Init writes (including an offline CA mounted at the CA
// directory) aside for --regenerate-secrets: restored by the rollback,
// deleted only after the run succeeded.
func setAsideSecrets(w *writer, l Layout, entries []Entry) error {
	seen := map[string]bool{}
	var paths []string
	for _, e := range entries {
		if e.Class == ClassSecret && e.Kind == KindFile {
			paths = append(paths, e.Path)
		}
	}
	targets, err := certTargets(w, l)
	if err != nil {
		return err
	}
	paths = append(paths, targets...)
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if err := w.setAside(p); err != nil {
			return err
		}
	}
	return nil
}

// noEnv makes config validation independent of the environment.
func noEnv(string) (string, bool) { return "", false }

// validateConfigs loads bekup.toml and runner.toml with the bekup
// validators (D-041 file checks included) and an empty environment.
func validateConfigs(root string) error {
	opts := config.Options{LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}}
	opts.ConfigPath = filepath.Join(root, BekupTOML)
	if _, err := config.LoadBekup(opts); err != nil {
		return fmt.Errorf("%s: %w", BekupTOML, err)
	}
	opts.ConfigPath = filepath.Join(root, RunnerTOML)
	if _, err := config.LoadRunner(opts); err != nil {
		return fmt.Errorf("%s: %w", RunnerTOML, err)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// report prints the summary: what was written, the evidence (certificate
// fingerprints, master key ID) and the next steps. It never prints a secret.
type reportData struct {
	root      string
	a         *Answers
	l         Layout
	entries   []Entry
	w         *writer
	mode      OwnershipMode
	why       string
	chownCmds string
	keyID     string
	newCerts  bool
	env       sysEnv
	created   int
	warnings  []string
}

func report(out io.Writer, r *reportData) error {
	root, a, l, entries, w := r.root, r.a, r.l, r.entries, r.w
	mode, why, keyID, newCerts, env := r.mode, r.why, r.keyID, r.newCerts, r.env
	var b strings.Builder
	fmt.Fprintf(&b, "\nbekup setup: deployment directory %s\n", root)
	fmt.Fprintf(&b, "  image:      %s\n", a.Image)
	fmt.Fprintf(&b, "  variant:    %s\n", a.Variant)
	proxy := string(a.Proxy)
	switch {
	case a.bundledProxy():
		proxy += " (TLS " + string(a.TLSMode)
		if a.ACMEStaging {
			proxy += ", ACME staging"
		}
		proxy += ")"
	case a.Proxy == ProxyExternal:
		proxy = fmt.Sprintf("your proxy at %s on the external network %s (%s); serve at %s:8080", a.Link.ProxyIP, a.Link.Name, a.Link.Subnet, a.Link.ServeIP)
	}
	fmt.Fprintf(&b, "  proxy:      %s\n", proxy)
	fmt.Fprintf(&b, "  domain:     https://%s\n", a.Domain)
	fmt.Fprintf(&b, "  ownership:  %s (%s)\n", mode, why)
	fmt.Fprintf(&b, "  files:      %d created, %d replaced\n", r.created, len(w.replaced))
	for _, msg := range r.warnings {
		fmt.Fprintf(&b, "\nWARNING: %s\n", msg)
	}
	if r.chownCmds != "" {
		b.WriteString("\nOwnership is NOT applied yet (--ownership print). " + r.chownCmds)
	}
	if keyID != "" {
		fmt.Fprintf(&b, "\nMaster key: %s (primary key ID %s)\n", l.keys(masterKeyName), keyID)
		b.WriteString(`  ESCROW IT NOW: store a copy offline (password manager, encrypted USB stick
  in a safe), separate from this host and from the backups. Without it every
  stored credential is lost. docs/security/key-management.md
`)
	}
	if newCerts {
		b.WriteString("\nCertificates (SHA-256 fingerprints; record them as evidence):\n")
		for _, e := range entries {
			if e.Group != groupCerts || e.Kind != KindFile || !strings.HasSuffix(e.Path, ".crt") || strings.HasSuffix(e.Path, "/ca.crt") && !e.Offline {
				continue
			}
			abs, err := resolve(root, e.Path)
			if err != nil {
				return err
			}
			infos, err := certs.InspectFile(abs)
			if err != nil {
				return err
			}
			if len(infos) > 0 {
				fmt.Fprintf(&b, "  %-40s %s (until %s)\n", e.Path, infos[0].SHA256, infos[0].NotAfter.Format("2006-01-02"))
			}
		}
		caDir := l.keys(certsDirName, caDirName)
		caAbs, err := resolve(root, caDir)
		if err != nil {
			return err
		}
		if onMount(env, caAbs) {
			fmt.Fprintf(&b, "\nCA key: written to %s, a separate mount (offline media). Unmount and store it\n  offline now; it is needed again only for certificate rotation.\n", caDir)
		} else {
			fmt.Fprintf(&b, `
WARNING: the CA private key %s/%s is on this host's disk. Whoever holds it
  can mint certificates the runner accepts (the runner controls Docker). Copy
  %s/ to offline media and delete it here (bekup setup check warns until
  then); next time mount the offline media at %s while running setup.
  docs/security/mtls-certs.md
`, caDir, caKeyName, caDir, caDir)
		}
	}
	b.WriteString("\nNext steps (in the deployment directory on the host):\n")
	step := 1
	if a.Proxy == ProxyExternal {
		fmt.Fprintf(&b, `  %d. Once: the link network and your proxy on it (skip what exists):
       %s
       In your proxy's compose file add %s to its networks with
       ipv4_address: %s, keep every network it already has (a list becomes a
       map: "- web" turns into "web: {}"; the implicit network is "default: {}"),
       declare %s as external, then recreate the proxy
       (docs/installation/reverse-proxy.md)
`, step, a.Link.CreateCommand(), a.Link.Name, a.Link.ProxyIP, a.Link.Name)
		step++
	}
	fmt.Fprintf(&b, "  %d. docker compose config -q && docker compose up -d\n", step)
	fmt.Fprintf(&b, "  %d. docker compose run --rm admin create --email you@example.com --display-name \"You\"\n", step+1)
	if a.Variant == VariantNPM {
		fmt.Fprintf(&b, "  %d. In Nginx Proxy Manager: a Proxy Host for %s -> http://%s:8080 (Websockets off, SSL + Force SSL + HTTP/2, HSTS off, the Advanced block from the docs)\n", step+2, a.Domain, a.Link.ServeIP)
		step++
	}
	fmt.Fprintf(&b, "  %d. docker compose -f compose.yaml run --rm setup check   (re-run after every change;\n     -f compose.yaml keeps compose.override.yaml away from the checking container, check still lints it)\ndocs/installation/compose.md\n", step+2)
	_, err := io.WriteString(out, b.String())
	return err
}

// onMount reports whether abs is a mount point of its own.
func onMount(env sysEnv, abs string) bool {
	mounts, err := env.mountinfo()
	if err != nil {
		return false
	}
	_, _, isPoint := mountOf(mounts, abs)
	return isPoint
}
