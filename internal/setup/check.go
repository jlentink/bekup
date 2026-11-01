package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/imageref"
)

// Severity of a check finding.
type Severity string

// Severities.
const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARN"
)

// Finding is one problem found by Check.
type Finding struct {
	Severity Severity
	Path     string
	Msg      string
}

func (f Finding) String() string {
	if f.Path == "" {
		return fmt.Sprintf("%-5s %s", f.Severity, f.Msg)
	}
	return fmt.Sprintf("%-5s %s: %s", f.Severity, f.Path, f.Msg)
}

// CheckOptions configure Check.
type CheckOptions struct {
	Dir     string
	Version string
	// FixOwnership re-applies the manifest (modes, then owners) first.
	FixOwnership bool
	Ownership    OwnershipMode
	// WarnDays warns about certificates expiring within this many days.
	WarnDays int
	// LookupEnv reads the environment (shell overrides of .env); nil means
	// os.LookupEnv.
	LookupEnv func(string) (string, bool)
	Out       io.Writer
	Now       func() time.Time

	assets fs.FS
	sys    *sysEnv
}

// ErrCheckFailed is returned when Check found at least one error.
var ErrCheckFailed = errors.New("setup check found errors")

// maxWalkEntries bounds the symlink scan.
const maxWalkEntries = 100000

// Check verifies a deployment directory read-only (unless FixOwnership): the
// manifest (presence, type, mode, owner, no symlinks), digest-pinned
// images, generated files unchanged, configuration consistency, certificate
// expiry and the CA key's location. It never reads secret contents. It
// prints every finding and returns ErrCheckFailed when one is an error.
func Check(opts CheckOptions) ([]Finding, error) {
	if opts.Ownership == "" {
		opts.Ownership = OwnershipAuto
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.assets == nil {
		opts.assets = bekup.Assets()
	}
	if opts.sys == nil {
		s := defaultSysEnv()
		opts.sys = &s
	}
	if opts.WarnDays <= 0 {
		opts.WarnDays = certs.ExpiryWarnDays
	}
	root, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("deployment directory %s is missing or not a directory", root)
	}
	st, err := readState(root)
	if err != nil {
		return nil, err
	}
	var installed []string
	for _, p := range st.assetPaths() {
		if strings.HasPrefix(p, DeployDir+"/") {
			installed = append(installed, p)
		}
	}
	layout := Layout{KeysDir: st.KeysDir}
	entries := Manifest(layout, installed, st.manifestOptions())
	mode, why := resolveOwnership(opts.Ownership, root, *opts.sys)
	if opts.Ownership == OwnershipAuto && st.Ownership == OwnershipNone {
		// Set up with ownership none (Docker Desktop): owners are mapped.
		mode, why = OwnershipNone, "recorded by setup init"
	}

	c := &checker{root: root, st: st, opts: &opts, layout: layout}
	if opts.FixOwnership {
		if err := fixOwnership(root, entries, mode, *opts.sys, opts.Out); err != nil {
			return nil, err
		}
		fmt.Fprintf(opts.Out, "Ownership re-applied: %s (%s)\n", mode, why)
	}
	if st.Legacy() != "" {
		how := fmt.Sprintf("replace %s with %s of the new release and run `docker compose run --rm setup upgrade`", ComposeFile, VariantOwnProxy.AssetName(st.dbMode()))
		if st.ComposeMode == ComposeGenerated {
			how = "run `docker compose run --rm setup upgrade --write-compose --image <image>`"
		}
		c.add(SeverityError, StateFileName, "this deployment was set up with the retired %s variant (D-215): %s (it maps the deployment to the own-proxy variant in link mode and keeps your link network)", st.Legacy(), how)
	}
	env := c.dotenv()
	c.env = env
	c.manifest(entries, mode != OwnershipNone)
	c.symlinks()
	c.generated()
	c.composeFiles()
	c.configs(env)
	c.shellOverrides(env)
	c.certificates(entries)
	c.caKey()
	c.rolesSQL()
	c.importedFiles()

	sort.SliceStable(c.findings, func(i, j int) bool {
		return c.findings[i].Severity == SeverityError && c.findings[j].Severity != SeverityError
	})
	var b strings.Builder
	errs, warns := 0, 0
	for _, f := range c.findings {
		b.WriteString(f.String() + "\n")
		if f.Severity == SeverityError {
			errs++
		} else {
			warns++
		}
	}
	fmt.Fprintf(&b, "bekup setup check: %s: %d error(s), %d warning(s) (ownership %s: %s)\n", root, errs, warns, mode, why)
	if st.dbMode() == DBExternal {
		b.WriteString("Your PostgreSQL itself (reachability, TLS, roles, citext) is checked from the migrate container:\n  docker compose run --rm migrate migrate check\n")
	}
	if _, err := io.WriteString(opts.Out, b.String()); err != nil {
		return c.findings, err
	}
	if errs > 0 {
		return c.findings, ErrCheckFailed
	}
	return c.findings, nil
}

// fixOwnership re-applies the manifest through an os.Root on the
// deployment directory and prints the warnings.
func fixOwnership(root string, entries []Entry, mode OwnershipMode, env sysEnv, out io.Writer) (retErr error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("fixing ownership: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, r.Close()) }()
	warnings, err := applyOwnership(r, entries, mode, env, out)
	for _, w := range warnings {
		fmt.Fprintf(out, "WARN  %s\n", w)
	}
	if err != nil {
		return fmt.Errorf("fixing ownership: %w", err)
	}
	return nil
}

type checker struct {
	root     string
	st       *State
	opts     *CheckOptions
	layout   Layout
	findings []Finding
	// env is the parsed .env (the compose interpolation environment).
	env map[string]string
}

func (c *checker) add(sev Severity, path, format string, args ...any) {
	c.findings = append(c.findings, Finding{Severity: sev, Path: path, Msg: fmt.Sprintf(format, args...)})
}

// manifest checks presence, type, mode and (optionally) owner. Secret
// entries are only Lstat'ed, never opened.
func (c *checker) manifest(entries []Entry, owners bool) {
	var wrong []string
	caOnMount := false
	if dir, err := resolve(c.root, c.layout.keys(certsDirName, caDirName)); err == nil {
		caOnMount = onMount(*c.opts.sys, dir)
	}
	for _, e := range entries {
		// The offline CA media may be FAT/exFAT without Unix modes/owners
		// (setup leaves them with a warning; the CA key is age-encrypted).
		sev, note := SeverityError, " (setup check --fix-ownership)"
		if e.Offline && caOnMount {
			sev, note = SeverityWarning, " (on the offline CA media; a POSIX file system such as ext4 on LUKS is recommended)"
		}
		fi, err := checkNoSymlinks(c.root, e.Path)
		if err != nil {
			c.add(SeverityError, e.Path, "%v", err)
			continue
		}
		if fi == nil {
			if !e.Optional {
				c.add(SeverityError, e.Path, "missing")
			}
			continue
		}
		if e.Kind == KindDir && !fi.IsDir() || e.Kind == KindFile && !fi.Mode().IsRegular() {
			c.add(SeverityError, e.Path, "is not a %s", e.Kind)
			continue
		}
		switch got := fi.Mode().Perm(); {
		case e.ModeIsMax && got&^e.Mode != 0:
			c.add(SeverityError, e.Path, "mode %04o is group- or world-writable (at most %04o): whoever can write the deployment directory can replace the compose file (setup check --fix-ownership)", got, e.Mode)
		case !e.ModeIsMax && got != e.Mode:
			c.add(sev, e.Path, "mode %04o, want %04o%s", got, e.Mode, note)
		}
		if fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			c.add(SeverityError, e.Path, "has setuid/setgid/sticky bits")
		}
		if n, ok := linkCount(fi); ok && e.Kind == KindFile && n != 1 {
			c.add(SeverityError, e.Path, "has %d hard links: a hard link could be another file of the host, which setup would chown and chmod (replace it with a copy)", n)
		}
		if owners {
			if uid, gid, ok := c.opts.sys.owner(nil, e.Path, fi); ok && (uid != e.UID || gid != e.GID) {
				switch {
				case e.Path == ".":
					c.add(SeverityError, e.Path, "the deployment directory belongs to %d:%d, want %d:%d (root): its owner can replace every file in it (setup check --fix-ownership)", uid, gid, e.UID, e.GID)
				case sev == SeverityWarning:
					c.add(sev, e.Path, "owner %d:%d, want %d:%d%s", uid, gid, e.UID, e.GID, note)
				default:
					wrong = append(wrong, fmt.Sprintf("%s is %d:%d, want %d:%d", e.Path, uid, gid, e.UID, e.GID))
				}
			}
		}
	}
	switch {
	case len(wrong) == 0:
	case len(wrong) <= 5:
		for _, w := range wrong {
			c.add(SeverityError, "", "owner: %s (setup check --fix-ownership)", w)
		}
	default:
		c.add(SeverityError, "", "%d entries have the wrong owner, e.g. %s (setup check --fix-ownership, or run the chown commands setup init printed)", len(wrong), strings.Join(wrong[:3], "; "))
	}
}

// symlinks refuses symlinks anywhere below the deployment directory.
func (c *checker) symlinks() {
	n := 0
	err := filepath.WalkDir(c.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		n++
		if n > maxWalkEntries {
			return fmt.Errorf("more than %d entries", maxWalkEntries)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			rel, err := filepath.Rel(c.root, p)
			if err != nil {
				return err
			}
			c.add(SeverityError, filepath.ToSlash(rel), "is a symlink (refused below the deployment directory)")
		}
		return nil
	})
	if err != nil {
		c.add(SeverityError, "", "scanning for symlinks: %v", err)
	}
}

var imageLineRe = regexp.MustCompile(`^\s*(?:-\s+)?image:\s*(?:&[A-Za-z0-9_-]+\s+)?(\S+)\s*$`)

// generated checks the compose file and the assets against the state and
// this binary's embedded files, and that every image is digest-pinned.
func (c *checker) generated() {
	if c.st.ComposeMode == ComposeOperator {
		c.operatorCompose()
	}
	for _, p := range c.st.assetPaths() {
		cur, err := readManaged(c.root, p)
		if errors.Is(err, fs.ErrNotExist) {
			c.add(SeverityError, p, "missing (bekup setup upgrade restores it)")
			continue
		}
		if err != nil {
			c.add(SeverityError, p, "%v", err)
			continue
		}
		if hashOf(cur) != c.st.Assets[p] {
			c.add(SeverityError, p, "edited locally; put changes into %s (bekup setup upgrade --force keeps a .bak copy)", OverrideFile)
		}
	}
	compose, err := readManaged(c.root, ComposeFile)
	if err != nil {
		if c.st.ComposeMode == ComposeOperator {
			c.add(SeverityError, ComposeFile, "%v", err)
		}
		return
	}
	if c.st.ComposeMode == ComposeGenerated {
		if _, placeholder, err := composeImage(compose); err != nil {
			c.add(SeverityError, ComposeFile, "%v", err)
		} else if placeholder {
			c.add(SeverityError, ComposeFile, "setup writes this file pinned (setup-first), but its x-bekup-image line takes the image from %s: restore it with `bekup setup upgrade --write-compose --force --image <pinned image>`", ImageEnvVar)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(compose))
	line := 0
	for sc.Scan() {
		line++
		m := imageLineRe.FindStringSubmatch(sc.Text())
		if len(m) < 2 || strings.HasPrefix(m[1], "*") {
			continue
		}
		if _, err := imageref.ParsePinned(m[1]); err != nil {
			c.add(SeverityError, ComposeFile, "line %d: image %q is not digest-pinned", line, bounded(m[1]))
		}
	}
	img, err := c.st.image()
	if err != nil {
		return
	}
	gen, err := generatedFiles(c.opts.assets, img, c.st.Variant, c.st.dbMode(), c.st.ComposeMode == ComposeGenerated)
	if err != nil {
		c.add(SeverityError, "", "rendering the embedded files: %v", err)
		return
	}
	var drift []string
	for p, data := range gen {
		if c.st.Assets[p] != hashOf(data) {
			drift = append(drift, p)
		}
	}
	for p := range c.st.Assets {
		if _, ok := gen[p]; !ok {
			drift = append(drift, p)
		}
	}
	if len(drift) > 0 {
		sev := SeverityWarning
		if c.opts.Version == c.st.BekupVersion {
			sev = SeverityError
		}
		sort.Strings(drift)
		c.add(sev, "", "the generated files differ from those of bekup %s (%s); %s", c.opts.Version, strings.Join(drift[:min(len(drift), 5)], ", "), c.upgradeHint())
	}
}

// upgradeHint names the upgrade command of the deployment's mode.
func (c *checker) upgradeHint() string {
	if c.st.ComposeMode == ComposeOperator {
		return "run `docker compose run --rm setup upgrade` (with the release compose.yaml of the image you deploy)"
	}
	return "run `bekup setup upgrade --write-compose --image <pinned image>` with the image you deploy"
}

// operatorCompose checks the operator's compose.yaml (compose-first): the
// recognized variant equals the recorded one, the image is pinned (and the
// one setup last ran with, or newer), and the resolved project
// (compose.yaml + compose.override.yaml + .env) passes the hardening lint.
func (c *checker) operatorCompose() {
	info, err := inspectCompose(c.root, c.opts.assets, c.st.Variant, c.st.dbMode(), c.st.overlays(), c.env, c.opts.LookupEnv, c.st.following())
	if err != nil {
		c.add(SeverityError, ComposeFile, "%v", err)
		return
	}
	c.findings = append(c.findings, info.Findings...)
	c.overlayHint(info.Findings)
	if info.Level == RecognizedCustom {
		c.add(SeverityWarning, ComposeFile, "customized (based on the %s variant): the hardening lint applies; prefer the unchanged release file plus %s", c.st.Variant, OverrideFile)
	}
	if info.FromEnv != (c.st.ImageSource == ImageSourceEnv) {
		if info.FromEnv {
			c.add(SeverityError, ComposeFile, "takes the image from %s (the committed file from deploy/compose/), but setup last ran with a release file pinning it: run `docker compose run --rm setup upgrade` (it records the mode and writes %s to %s)", ImageEnvVar, ImageEnvVar, EnvFile)
		} else {
			c.add(SeverityError, ComposeFile, "pins the image (a release file), but setup last ran with the committed file taking it from %s: run `docker compose run --rm setup upgrade`", ImageEnvVar)
		}
		return
	}
	if info.FromEnv {
		c.envImage(info)
		return
	}
	switch rec, err := c.st.image(); {
	case err != nil:
	case info.Image.String() == rec.String():
		if info.Hash != c.st.ComposeSHA256 && info.Level == RecognizedCustom {
			c.add(SeverityWarning, ComposeFile, "changed since setup last ran")
		}
	case IsDowngrade(rec.Tag, info.Image.Tag):
		c.add(SeverityError, ComposeFile, "the image %s is older than the one setup last ran with (%s): downgrades can break the database schema; restore the matching compose.yaml", info.Image, rec)
	default:
		c.add(SeverityWarning, ComposeFile, "the image %s differs from the one setup last ran with (%s): run `docker compose run --rm setup upgrade`, then `docker compose pull && docker compose up -d`", info.Image, rec)
	}
}

// envImage checks a compose.yaml that takes the image from BEKUP_IMAGE
// (placeholder mode, D-205 amendment 3): .env names the recorded,
// digest-pinned image, the environment does not override it, and every
// bekup service resolves to it (info.Image is the resolved value; the lint
// has required every bekup service to use it).
func (c *checker) envImage(info *ComposeInfo) {
	rec, err := c.st.image()
	if err != nil {
		return // reported by the state validation
	}
	dot, ok := c.env[ImageEnvVar]
	switch {
	case !ok || dot == "":
		c.add(SeverityError, EnvFile, "%s is missing: %s takes the bekup image from it; add %s=%s (the recorded image)", ImageEnvVar, ComposeFile, ImageEnvVar, rec)
		return
	case dot != rec.String():
		img, err := imageref.ParseBekup(dot)
		switch {
		case err != nil:
			c.add(SeverityError, EnvFile, "%s=%q is not digest-pinned (want %s)", ImageEnvVar, bounded(dot), rec)
		case img.IsMutable() != rec.IsMutable():
			c.add(SeverityError, EnvFile, "%s=%s, but setup last ran with %s: run `docker compose run --rm setup upgrade` before `docker compose up -d` (it records whether the image follows a tag, D-212)", ImageEnvVar, img, rec)
		case IsDowngrade(rec.Tag, img.Tag):
			c.add(SeverityError, EnvFile, "%s=%s is older than the image setup last ran with (%s): downgrades can break the database schema; restore %s=%s", ImageEnvVar, img, rec, ImageEnvVar, rec)
		default:
			c.add(SeverityError, EnvFile, "%s=%s differs from the image setup last ran with (%s): run `docker compose run --rm setup upgrade` before `docker compose up -d`", ImageEnvVar, img, rec)
		}
		return
	}
	if got, ok := c.opts.LookupEnv(ImageEnvVar); ok && got != dot {
		c.add(SeverityError, "", "%s is set in the environment (%s) and overrides %s (%s): docker compose would run that image; unset it where docker compose runs, or upgrade with `%s=<new image> docker compose run --rm setup upgrade`", ImageEnvVar, bounded(got), EnvFile, dot, ImageEnvVar)
		return
	}
	if info.Image.String() != rec.String() {
		c.add(SeverityError, ComposeFile, "the bekup services resolve to %s, not the recorded image %s", info.Image, rec)
	}
}

// composeFiles refuses compose files next to the pinned one that docker
// compose would also read and, setup-first, lints the project resolved
// from the generated compose.yaml and the override (compose-first:
// operatorCompose).
func (c *checker) composeFiles() {
	for _, name := range composeShadowFiles {
		fi, err := checkNoSymlinks(c.root, name)
		if err != nil {
			c.add(SeverityError, name, "%v", err)
			continue
		}
		if fi != nil {
			c.add(SeverityError, name, "docker compose reads this file instead of or next to %s, bypassing the pinned images: remove it (put local changes into %s)", ComposeFile, OverrideFile)
		}
	}
	if c.st.ComposeMode != ComposeGenerated {
		return // compose-first: operatorCompose lints compose.yaml + the override
	}
	// Setup-first: compose.yaml is generated (its hash is checked); lint
	// the project it resolves to together with the override.
	in := composeInput{root: c.root, dotenv: c.env, lookup: c.opts.LookupEnv}
	var err error
	if in.compose, err = readManaged(c.root, ComposeFile); err != nil {
		return // reported by generated()
	}
	switch in.override, err = readManaged(c.root, OverrideFile); {
	case errors.Is(err, fs.ErrNotExist):
		in.override = nil
	case err != nil:
		c.add(SeverityError, OverrideFile, "%v", err)
		return
	case in.override == nil:
		in.override = []byte{}
	}
	img, err := c.st.image()
	if err != nil {
		c.add(SeverityError, StateFileName, "the recorded image is not digest-pinned")
		return
	}
	in.following = c.st.following()
	expected, err := RenderGenerated(c.opts.assets, c.st.Variant, c.st.dbMode(), img)
	if err != nil {
		c.add(SeverityError, "", "rendering the embedded compose file: %v", err)
		return
	}
	if in.override != nil {
		if err := singleDocument(OverrideFile, in.override); err != nil {
			c.add(SeverityError, OverrideFile, "%v", err)
			return
		}
	}
	env := projectEnv(in.dotenv, in.lookup, in.docs()...)
	findings := lintComposeFiles(&in, env, c.st.Variant, img.String(), expectedDocs(expected, c.st.overlays()), false)
	c.findings = append(c.findings, findings...)
	c.overlayHint(findings)
}

// overlayFinding reports a lint error about what the setup-only overlays
// change: the shipped services serve, migrate, admin or a shipped network.
func overlayFinding(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity != SeverityError {
			continue
		}
		for _, s := range []string{`service "serve"`, `service "migrate"`, `service "admin"`, `network "`} {
			if strings.Contains(f.Msg, s) {
				return true
			}
		}
	}
	return false
}

// overlayHint names the blocks compose.override.yaml must carry when the
// lint failed in a deployment with setup-only overlays (D-215).
func (c *checker) overlayHint(findings []Finding) {
	ov := c.st.overlays()
	if !ov.Any() || !overlayFinding(findings) {
		return
	}
	c.add(SeverityError, OverrideFile, "this deployment's %s must carry the blocks setup wrote (D-215; merged into its services: and networks: keys):\n%s", OverrideFile, overlayDoc(ov, ""))
}

// deploymentEnv checks the deployment lines of .env (D-212): the setup
// version migrate/serve/worker compare with, the image mode, and that a
// followed image runs the version setup prepared.
func (c *checker) deploymentEnv(env map[string]string) {
	want := strings.TrimPrefix(c.st.BekupVersion, "v")
	switch got, ok := env[deployguard.EnvSetupVersion]; {
	case (!ok || got == "") && c.st.following():
		c.add(SeverityError, EnvFile, "%s is missing, but the image follows a tag: migrate, serve, worker and runner refuse to start without it (D-212); run `docker compose run --rm setup upgrade` to add %s=%s", deployguard.EnvSetupVersion, deployguard.EnvSetupVersion, want)
	case !ok || got == "":
		c.add(SeverityWarning, EnvFile, "%s is missing: migrate, serve, worker and runner cannot tell whether setup prepared the deployment for their version (they only warn); run `docker compose run --rm setup upgrade` to add %s=%s", deployguard.EnvSetupVersion, deployguard.EnvSetupVersion, want)
	case strings.TrimPrefix(got, "v") != want:
		c.add(SeverityError, EnvFile, "%s=%s, but setup last ran as bekup %s: migrate, serve, worker and runner refuse to start with another version than %s; run `docker compose run --rm setup upgrade`", deployguard.EnvSetupVersion, bounded(got), want, deployguard.EnvSetupVersion)
	}
	mode := env[deployguard.EnvImageMode]
	switch {
	case c.st.following() && mode != deployguard.ImageModeFollowing:
		c.add(SeverityError, EnvFile, "%s=%q, but the image follows %s: serve would warn, audit and alert about the unpinned image only once the runner reports it; set %s=%s (setup upgrade writes it)", deployguard.EnvImageMode, bounded(mode), c.st.Image, deployguard.EnvImageMode, deployguard.ImageModeFollowing)
	case !c.st.following() && mode != "" && mode != deployguard.ImageModePinned:
		c.add(SeverityWarning, EnvFile, "%s=%q, but the image is pinned by digest: remove the line (setup upgrade does)", deployguard.EnvImageMode, bounded(mode))
	}
	if c.st.following() {
		c.add(SeverityWarning, "", "the bekup image follows %s (NOT pinned by digest, D-212): whoever moves the tag decides what runs; upgrade with `docker compose pull`, `docker compose run --rm setup upgrade`, `docker compose up -d`, verify releases with cosign, or pin a digest (docs/installation/upgrades.md)", c.st.Image)
	}
	// The check container runs the deployment's image (the setup service
	// uses the x-bekup-image): another version than the recorded one is
	// what migrate/serve/worker/runner would run, and refuse.
	if v := strings.TrimPrefix(c.opts.Version, "v"); v != want && env[deployguard.EnvSetupVersion] != "" {
		why := "compose.yaml names another image"
		if c.st.following() {
			why = "the followed tag has moved"
		}
		c.add(SeverityError, "", "this image is bekup %s, but setup prepared the deployment for %s (%s): run `docker compose run --rm setup upgrade` before `docker compose up -d` (migrate, serve, worker and runner refuse to start until then)", v, want, why)
	}
}

// dotenv parses .env (KEY=value lines only).
func (c *checker) dotenv() map[string]string {
	env := map[string]string{}
	data, err := readManaged(c.root, EnvFile)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.add(SeverityError, EnvFile, "%v", err)
		}
		return env
	}
	env, bad := parseDotEnv(data)
	for _, n := range bad {
		c.add(SeverityError, EnvFile, "line %d is not a plain KEY=value line", n)
	}
	for _, k := range dockerEnvErrors {
		if _, ok := env[k]; ok {
			c.add(SeverityError, EnvFile, "%s points docker compose at another Docker daemon, context or client configuration: remove it (set it in the shell if you really mean it)", k)
		}
	}
	for _, k := range composeEnvErrors {
		if _, ok := env[k]; ok {
			c.add(SeverityError, EnvFile, "%s selects other compose files or another project directory, bypassing the pinned %s: remove it", k, ComposeFile)
		}
	}
	if _, ok := env[composeEnvWarning]; ok {
		c.add(SeverityWarning, EnvFile, "%s renames the compose project: its volumes (the bekup database) are new and empty under another name", composeEnvWarning)
	}
	for _, k := range sortedKeys(env) {
		if strings.HasPrefix(k, "PG") || strings.HasPrefix(k, "BEKUP_DATABASE_") {
			c.add(SeverityError, EnvFile, "%s: .env holds compose variables only (no secrets, and bekup ignores PG* variables); the database belongs into bekup.toml [database] and its passwords into the files below %s/", k, c.st.KeysDir)
		}
	}
	if env[ImageEnvVar] != "" && c.st.ImageSource != ImageSourceEnv {
		c.add(SeverityWarning, EnvFile, "%s has no effect: the image is pinned in %s", ImageEnvVar, ComposeFile)
	}
	c.deploymentEnv(env)
	want := ""
	if c.st.Variant == VariantFull && c.st.Proxy != ProxyNone {
		want = string(c.st.Proxy)
	}
	if got := env["COMPOSE_PROFILES"]; got != want {
		if c.st.Variant == VariantFull {
			c.add(SeverityWarning, EnvFile, "COMPOSE_PROFILES=%q, but setup configured proxy %q", got, c.st.Proxy)
		} else {
			c.add(SeverityWarning, EnvFile, "COMPOSE_PROFILES=%q: the %s variant uses no profiles (`docker compose up` would start the one-shot services of that profile)", got, c.st.Variant)
		}
	}
	if got := env["BEKUP_KEYS_DIR"]; got != "./"+c.st.KeysDir && got != c.st.KeysDir {
		c.add(SeverityError, EnvFile, "BEKUP_KEYS_DIR=%q, but the secrets are in ./%s", got, c.st.KeysDir)
	}
	return env
}

func envOr(env map[string]string, key, def string) string {
	if v, ok := env[key]; ok && v != "" {
		return v
	}
	return def
}

// configs validates the TOML files and their consistency with .env.
func (c *checker) configs(env map[string]string) {
	opts := config.Options{LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}}
	opts.ConfigPath = filepath.Join(c.root, BekupTOML)
	bres, err := config.LoadBekup(opts)
	if err != nil {
		c.add(SeverityError, BekupTOML, "%v", err)
	}
	opts.ConfigPath = filepath.Join(c.root, RunnerTOML)
	rres, err := config.LoadRunner(opts)
	if err != nil {
		c.add(SeverityError, RunnerTOML, "%v", err)
	}
	def := DefaultNetwork()
	serveVar, serveIP := "SERVE_FRONTEND_IP", envOr(env, "SERVE_FRONTEND_IP", def.ServeFrontendIP)
	proxyVar, proxyIP := "PROXY_FRONTEND_IP", envOr(env, "PROXY_FRONTEND_IP", def.ProxyFrontendIP)
	wantListen, wantTrusted := serveIP+":8080", []string{proxyIP + "/32"}
	switch {
	case c.st.ownProxyJoin():
		c.joinNetwork(env)
		serveVar, wantListen = "the own-proxy join shape, D-215", config.OwnProxyJoinListen
		proxyVar, wantTrusted = "join mode trusts the proxy by its container name only", []string{}
	case c.st.Proxy == ProxyExternal:
		c.linkNetwork(env)
		dl := DefaultLinkNetwork()
		serveVar, serveIP = "SERVE_PROXY_IP", envOr(env, "SERVE_PROXY_IP", dl.ServeIP)
		proxyVar, proxyIP = "the recorded proxy address of the link network", c.st.ProxyIP
		wantListen, wantTrusted = serveIP+":8080", []string{proxyIP + "/32"}
	}
	c.traefikLabelsEnv(env)
	c.dbNetworkEnv(env)
	bundled := c.st.Proxy == ProxyTraefik || c.st.Proxy == ProxyNginx
	needDomain := bundled || c.st.ProxyLabels
	metricsIP := envOr(env, "SERVE_METRICS_IP", def.ServeMetricsIP)
	runnerIP := envOr(env, "RUNNER_CTL_IP", def.RunnerCtlIP)
	if bres != nil {
		b := bres.Config
		if u, err := url.Parse(b.Server.BaseURL); err == nil {
			switch domain := env["BEKUP_DOMAIN"]; {
			case domain == "" && needDomain:
				c.add(SeverityError, EnvFile, "BEKUP_DOMAIN is missing (the proxy needs it)")
			case domain != "" && !strings.EqualFold(u.Hostname(), domain):
				c.add(SeverityError, BekupTOML, "server.base_url host %q differs from BEKUP_DOMAIN %q in .env", u.Hostname(), domain)
			}
			if u.Port() != "" && u.Port() != "443" && bundled {
				c.add(SeverityWarning, BekupTOML, "server.base_url has port %s, but the bundled proxy publishes 443", u.Port())
			}
		}
		if c.st.Proxy != ProxyNone && !slices.Equal(b.Server.TrustedProxies, wantTrusted) {
			c.add(SeverityError, BekupTOML, "server.trusted_proxies = %q, want exactly %q (%s)", b.Server.TrustedProxies, wantTrusted, proxyVar)
		}
		c.wideCIDRs(BekupTOML, "server.trusted_proxies", b.Server.TrustedProxies)
		c.trustedHosts(b)
		if b.Server.Listen != wantListen {
			c.add(SeverityError, BekupTOML, "server.listen = %q, want %q (%s)", b.Server.Listen, wantListen, serveVar)
		}
		if want := metricsIP + ":9464"; b.Metrics.Enabled && b.Metrics.Listen != want {
			c.add(SeverityError, BekupTOML, "metrics.listen = %q, want %q (SERVE_METRICS_IP)", b.Metrics.Listen, want)
		}
		c.database(&b.Database)
	}
	if rres != nil {
		r := rres.Config
		c.wideCIDRs(RunnerTOML, "targets.allow_cidrs", r.Targets.AllowCIDRs)
		if want := runnerIP + ":7443"; r.Listen.Address != want {
			c.add(SeverityError, RunnerTOML, "listen.address = %q, want %q (RUNNER_CTL_IP)", r.Listen.Address, want)
		}
		gid, err := strconv.Atoi(env["DOCKER_GID"])
		switch {
		case err != nil:
			c.add(SeverityError, EnvFile, "DOCKER_GID is missing or not a number")
		case gid != r.Docker.GID:
			c.add(SeverityError, RunnerTOML, "docker.gid = %d, but DOCKER_GID in .env is %d (both must be the Docker socket's group)", r.Docker.GID, gid)
		}
	}
}

// database checks bekup.toml [database] against the recorded database
// mode (D-208): the bundled database uses the URL files, an external one
// fields mode with the password files and the CA bundle that setup wrote
// and compose mounts (a weaker tls_mode is reported, not refused).
func (c *checker) database(d *config.Database) {
	if c.st.dbMode() == DBBundled {
		if d.FieldsMode() {
			c.add(SeverityError, BekupTOML, "[database] uses fields mode (database.host), but this deployment runs the bundled postgres service: remove the database.host/port/name/user/password_file keys (switching to an external database needs a new deployment directory, docs/installation/upgrades.md)")
		}
		return
	}
	if !d.FieldsMode() {
		c.add(SeverityError, BekupTOML, "[database] has no host, but this deployment uses your PostgreSQL (database_mode external): restore the fields-mode keys setup wrote (host, port, name, user, password_file, ...)")
		return
	}
	single := c.st.DatabaseAuth == DBAuthSingleAccount
	switch {
	case d.SingleAccount && !single:
		c.add(SeverityError, BekupTOML, "database.single_account = true, but setup configured the two roles bekup_app/bekup_owner: single-account mode is an explicit weakening (D-207); remove it, or set the deployment up again with --db-auth single-account")
	case !d.SingleAccount && single:
		c.add(SeverityError, BekupTOML, "database.single_account is not set, but setup configured single-account mode (no owner password exists): set it back, or create the two roles (bekup db bootstrap) and run setup again")
	}
	if d.PasswordFile != dbPasswordTarget {
		c.add(SeverityError, BekupTOML, "database.password_file = %q, want %q (the bekup_database_password secret that compose mounts)", bounded(d.PasswordFile), dbPasswordTarget)
	}
	if !d.SingleAccount && d.OwnerPasswordFile != dbOwnerPasswordTarget {
		c.add(SeverityError, BekupTOML, "database.owner_password_file = %q, want %q (the bekup_database_owner_password secret, mounted into migrate only)", bounded(d.OwnerPasswordFile), dbOwnerPasswordTarget)
	}
	switch d.TLSMode {
	case config.DBTLSVerifyFull:
	case config.DBTLSRequire:
		c.add(SeverityWarning, BekupTOML, "database.tls_mode = \"require\" is weaker than verify-full: the server's host name (and without tls_ca_file its certificate) is not verified (D-100); serve warns at every start")
	default:
		c.add(SeverityWarning, BekupTOML, "database.tls_mode = %q is weaker than verify-full: traffic to your PostgreSQL is not protected (D-100); serve warns at every start", bounded(d.TLSMode))
	}
	if want := c.st.dbTLS().configMode(); d.TLSMode != want && d.TLSMode == config.DBTLSVerifyFull {
		c.add(SeverityWarning, BekupTOML, "database.tls_mode = %q, but setup configured %q (--db-tls %s): fine if you now verify the server; setup check keeps reporting the recorded choice", d.TLSMode, want, c.st.dbTLS())
	}
	if d.TLSMode != config.DBTLSDisable && d.TLSCAFile != "" && d.TLSCAFile != dbCATarget && d.TLSCAFile != config.SystemCABundle {
		c.add(SeverityError, BekupTOML, "database.tls_ca_file = %q, want %q (compose mounts %s/%s there) or the system bundle %q", bounded(d.TLSCAFile), dbCATarget, c.st.KeysDir, dbCAName, config.SystemCABundle)
	}
}

// rolesSQL warns while the roles SQL for a DBA (dba-sql) is still there.
func (c *checker) rolesSQL() {
	rel := c.layout.keys(dbRolesSQLName)
	if fi, err := checkNoSymlinks(c.root, rel); err == nil && fi != nil {
		c.add(SeverityWarning, rel, "the roles SQL for your DBA (SCRAM verifiers of the database passwords) is still here: delete it once it ran")
	}
}

// importedFiles warns while a secret source file that setup copied
// (password files, an imported keyring) is still in the deployment
// directory.
func (c *checker) importedFiles() {
	for _, f := range c.st.ImportedFiles {
		if fi, err := checkNoSymlinks(c.root, f); err == nil && fi != nil {
			c.add(SeverityWarning, f, "setup copied this secret into %s/ during init: delete the source file (the copy there is the one bekup uses)", c.st.KeysDir)
		}
	}
}

// joinNetwork checks the own-proxy join mode (D-215): .env names the
// recorded proxy network.
func (c *checker) joinNetwork(env map[string]string) {
	switch name := env["BEKUP_PROXY_NETWORK"]; {
	case name == "":
		c.add(SeverityError, EnvFile, "BEKUP_PROXY_NETWORK is missing: serve would join %q instead of your proxy's network %q", DefaultLinkNetwork().Name, c.st.ProxyNetwork)
	case name != c.st.ProxyNetwork:
		c.add(SeverityError, EnvFile, "BEKUP_PROXY_NETWORK=%q, but setup configured your proxy's network %q (set it up again to switch networks)", bounded(name), c.st.ProxyNetwork)
	}
	if _, ok := env["SERVE_PROXY_IP"]; ok {
		c.add(SeverityWarning, EnvFile, "SERVE_PROXY_IP has no effect in the join mode (Docker assigns serve's address on your proxy's network)")
	}
}

// trustedHosts checks server.trusted_proxy_hosts (D-215): in the own-proxy
// join mode exactly the recorded proxy container, behind a bundled proxy
// or in link mode none (an added name widens the trust).
func (c *checker) trustedHosts(b *config.Bekup) {
	got := b.Server.TrustedProxyHosts
	switch {
	case c.st.ownProxyJoin():
		if len(got) != 1 || !strings.EqualFold(got[0], c.st.ProxyContainer) {
			c.add(SeverityError, BekupTOML, "server.trusted_proxy_hosts = %q, want exactly [%q] (your proxy's container)", got, c.st.ProxyContainer)
		}
		if !b.Server.AllowWildcardListen {
			c.add(SeverityError, BekupTOML, "server.allow_wildcard_listen must be true in the own-proxy join shape (serve listens on %s, D-215)", config.OwnProxyJoinListen)
		}
	case c.st.Proxy == ProxyNone:
		// Your own proxy on the frontend network may be named either way.
	case len(got) > 0:
		c.add(SeverityError, BekupTOML, "server.trusted_proxy_hosts = %q: this deployment trusts its proxy by address only; remove them", got)
	}
}

// traefikLabelsEnv checks the Traefik label variables (D-215: only with
// the labels overlay).
func (c *checker) traefikLabelsEnv(env map[string]string) {
	for _, k := range []string{"TRAEFIK_ENTRYPOINT", "TRAEFIK_CERTRESOLVER"} {
		v, ok := env[k]
		switch {
		case !ok:
		case !c.st.ProxyLabels:
			c.add(SeverityWarning, EnvFile, "%s has no effect: this deployment has no Traefik labels", k)
		default:
			if err := ValidateTraefikName(k, v); err != nil {
				c.add(SeverityError, EnvFile, "%v", err)
			}
		}
	}
}

// dbNetworkEnv checks BEKUP_DB_NETWORK against the recorded database
// container network (D-215).
func (c *checker) dbNetworkEnv(env map[string]string) {
	got, ok := env["BEKUP_DB_NETWORK"]
	switch {
	case c.st.overlays().DBNetwork && got != c.st.DBNetwork:
		c.add(SeverityError, EnvFile, "BEKUP_DB_NETWORK=%q, but setup configured your database container's network %q", bounded(got), c.st.DBNetwork)
	case !c.st.overlays().DBNetwork && ok:
		c.add(SeverityWarning, EnvFile, "BEKUP_DB_NETWORK has no effect: this deployment's database is not a container on a Docker network of this host")
	}
}

// linkNetwork checks the own-proxy link mode: the compose variables
// against the recorded link network (subnet, proxy address).
func (c *checker) linkNetwork(env map[string]string) {
	l := c.st.link()
	if name := envOr(env, "BEKUP_PROXY_NETWORK", DefaultLinkNetwork().Name); name != l.Name {
		c.add(SeverityError, EnvFile, "BEKUP_PROXY_NETWORK=%q, but setup configured the link network %q", name, l.Name)
	}
	l.Name = envOr(env, "BEKUP_PROXY_NETWORK", l.Name)
	l.ServeIP = envOr(env, "SERVE_PROXY_IP", DefaultLinkNetwork().ServeIP)
	if err := validateLink(l, Network{}); err != nil {
		c.add(SeverityError, EnvFile, "link network: %v", err)
	}
}

// wideCIDRs reports CIDRs wider than setup init accepts (error) or very
// wide ones (warning).
func (c *checker) wideCIDRs(file, key string, cidrs []string) {
	for _, s := range cidrs {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			continue // the config validators report it
		}
		switch {
		case p.Bits() < prefixBounds(p, minBitsV4, minBitsV6):
			c.add(SeverityError, file, "%s contains %s: wider than /%d; name only the networks you mean", key, p, prefixBounds(p, minBitsV4, minBitsV6))
		case isWidePrefix(p):
			c.add(SeverityWarning, file, "%s contains the very wide %s; name only the networks you mean", key, p)
		}
	}
}

// shellOverrides warns when the environment sets a compose variable that
// .env also sets: the environment wins over .env.
func (c *checker) shellOverrides(env map[string]string) {
	keys := sortedKeys(env)
	netVars := (&Network{}).vars()
	more := make([]string, 0, 12+len(netVars))
	more = append(more, "BEKUP_PROXY_NETWORK", "SERVE_PROXY_IP", "BEKUP_DB_NETWORK", "TRAEFIK_ENTRYPOINT", "TRAEFIK_CERTRESOLVER", "BEKUP_DOMAIN", "DOCKER_GID", "BEKUP_KEYS_DIR",
		deployguard.EnvSetupVersion, deployguard.EnvImageMode)
	for _, v := range netVars {
		more = append(more, v.name)
	}
	for _, k := range more {
		if _, ok := env[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if k == ImageEnvVar {
			continue // operatorCompose / envImage (an error in placeholder mode)
		}
		if got, ok := c.opts.LookupEnv(k); ok && got != env[k] {
			c.add(SeverityWarning, EnvFile, "%s is set in the environment and overrides .env; unset it where docker compose runs", k)
		}
	}
	for _, k := range composeEnvErrors {
		if _, ok := c.opts.LookupEnv(k); ok {
			c.add(SeverityError, "", "%s is set in the environment: it selects other compose files or another project directory, bypassing the pinned %s; unset it where docker compose runs", k, ComposeFile)
		}
	}
	if _, ok := c.opts.LookupEnv(composeEnvWarning); ok {
		c.add(SeverityWarning, "", "%s is set in the environment: it renames the compose project (new, empty volumes); unset it where docker compose runs", composeEnvWarning)
	}
}

// certificates checks expiry of every certificate in the manifest.
func (c *checker) certificates(entries []Entry) {
	now := c.opts.Now()
	warn := time.Duration(c.opts.WarnDays) * 24 * time.Hour
	for _, e := range entries {
		if e.Kind != KindFile || e.Secret || !strings.HasSuffix(e.Path, ".crt") {
			continue
		}
		abs, err := resolve(c.root, e.Path)
		if err != nil {
			continue
		}
		fi, err := checkNoSymlinks(c.root, e.Path)
		if err != nil || fi == nil {
			continue
		}
		if e.Path == c.layout.keys(dbCAName) && fi.Size() == 0 {
			// Empty by design for a TLS choice without a CA file (D-215).
			if c.st.dbTLS() == DBTLSVerifyCAFile {
				c.add(SeverityError, e.Path, "is empty, but setup configured --db-tls verify-ca-file: restore your PostgreSQL's CA bundle")
			}
			continue
		}
		infos, err := certs.InspectFile(abs)
		if err != nil {
			c.add(SeverityError, e.Path, "%v", err)
			continue
		}
		if e.Path == c.layout.keys(dbCAName) {
			c.dbCA(e.Path, infos, now, warn)
			continue
		}
		for i := range infos {
			in := &infos[i]
			switch {
			case in.Expired(now):
				c.add(SeverityError, e.Path, "certificate %q expired on %s: renew it with `bekup setup renew`", in.Subject, in.NotAfter.Format("2006-01-02"))
			case in.NotYetValid(now):
				c.add(SeverityError, e.Path, "certificate %q is not valid before %s", in.Subject, in.NotBefore.Format("2006-01-02"))
			case in.ExpiresWithin(now, warn):
				c.add(SeverityWarning, e.Path, "certificate %q expires on %s: renew it with `bekup setup renew` (docs/security/mtls-certs.md)", in.Subject, in.NotAfter.Format("2006-01-02"))
			}
		}
	}
}

// dbCA checks the external database's CA bundle: provider bundles carry
// many certificates, so an expired one is a warning while another is still
// valid, and an error only when none is.
func (c *checker) dbCA(rel string, infos []certs.ExpiryInfo, now time.Time, warn time.Duration) {
	if !dbCAExpiry(infos, now) {
		c.add(SeverityError, rel, "no certificate in the CA bundle is valid now: bekup cannot verify your PostgreSQL (replace it with the provider's current bundle)")
		return
	}
	for i := range infos {
		in := &infos[i]
		switch {
		case in.Expired(now):
			c.add(SeverityWarning, rel, "certificate %q in the CA bundle expired on %s", in.Subject, in.NotAfter.Format("2006-01-02"))
		case in.ExpiresWithin(now, warn):
			c.add(SeverityWarning, rel, "certificate %q in the CA bundle expires on %s (get the provider's new bundle)", in.Subject, in.NotAfter.Format("2006-01-02"))
		}
	}
}

// caKey warns while the CA private key lies on the host's disk.
func (c *checker) caKey() {
	rel := c.layout.keys(certsDirName, caDirName, caKeyName)
	fi, err := checkNoSymlinks(c.root, rel)
	if err != nil || fi == nil {
		return
	}
	dir, err := resolve(c.root, c.layout.keys(certsDirName, caDirName))
	if err != nil {
		return
	}
	if onMount(*c.opts.sys, dir) {
		c.add(SeverityWarning, rel, "the CA key is on a separate mount: unmount the offline media when you are done")
		return
	}
	c.add(SeverityWarning, rel, "the CA private key is on this host's disk: move %s/ to offline media (docs/security/mtls-certs.md)", c.layout.keys(certsDirName, caDirName))
}
