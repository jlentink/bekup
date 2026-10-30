package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
)

// UpgradeOptions configure Upgrade.
type UpgradeOptions struct {
	Dir     string
	Version string
	// Image is the new digest-pinned image: required setup-first; in a
	// compose-first deployment it comes from compose.yaml and must match
	// when set (BEKUP_IMAGE of the setup service).
	Image imageref.Ref
	// WriteCompose rewrites compose.yaml (setup-first deployments only;
	// required there, refused compose-first).
	WriteCompose bool
	// AllowVersionMismatch accepts an image tag that names another version
	// than this binary.
	AllowVersionMismatch bool
	// AllowDowngrade permits a binary older than the recorded version.
	AllowDowngrade bool
	// Force replaces locally edited generated files (each is first copied
	// to <path>.bak-<timestamp>).
	Force     bool
	Ownership OwnershipMode
	// CAPassphrase is called only if a new manifest adds certificates.
	CAPassphrase func() ([]byte, error)
	Out          io.Writer
	Now          func() time.Time

	assets    fs.FS
	sys       *sysEnv
	certsInit certsInitFunc
}

// CheckVersionTag refuses an image tag that names another version than the
// running binary (a container cannot verify its own digest; D-203), and an
// image without a tag (nothing to compare; --allow-version-mismatch).
func CheckVersionTag(img imageref.Ref, version string) error {
	if img.Tag == "" {
		return fmt.Errorf("the image reference has no tag, so this bekup %s cannot check that it names the same version: use <repo>:%s@sha256:<digest> (or pass --allow-version-mismatch)", version, strings.TrimPrefix(version, "v"))
	}
	if strings.TrimPrefix(img.Tag, "v") == strings.TrimPrefix(version, "v") {
		return nil
	}
	return fmt.Errorf("the image tag %q does not match this bekup version %s: run setup from the image you deploy (or pass --allow-version-mismatch)", img.Tag, version)
}

// IsDowngrade reports whether next is an older SemVer than prev.
func IsDowngrade(prev, next string) bool {
	p, n := "v"+strings.TrimPrefix(prev, "v"), "v"+strings.TrimPrefix(next, "v")
	if !semver.IsValid(p) || !semver.IsValid(n) {
		return false
	}
	return semver.Compare(n, p) < 0
}

// Upgrade rewrites the generated files for a new image: deploy/**
// (removing stale assets), reference/*, the state file and, setup-first,
// the compose file. Compose-first (D-205) the operator has replaced
// compose.yaml with the new release file: its image is the new one, its
// variant must be the recorded one, and compose.yaml is never written.
// With the committed file (placeholder mode, D-205 amendment 3) the new
// image is the BEKUP_IMAGE docker compose resolved for the setup container
// and upgrade rewrites the BEKUP_IMAGE line of .env. Otherwise it never
// touches .env, the TOML files (it re-validates them) or existing secrets,
// and generates only secret groups that are new in the manifest.
func Upgrade(opts UpgradeOptions) (retErr error) {
	if opts.Ownership == "" {
		opts.Ownership = OwnershipAuto
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.assets == nil {
		opts.assets = bekup.Assets()
	}
	if opts.sys == nil {
		s := defaultSysEnv()
		opts.sys = &s
	}
	if opts.certsInit == nil {
		opts.certsInit = certs.Init
	}
	w, err := newWriter(opts.Dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, w.close()) }()
	root := w.root
	st, err := readState(root)
	if err != nil {
		return err
	}
	if err := refuseShadowFiles(root); err != nil {
		return err
	}
	var compose *ComposeInfo
	switch st.ComposeMode {
	case ComposeOperator:
		if opts.WriteCompose {
			return fmt.Errorf("this deployment is compose-first: setup never writes %s. Replace it with the new release file (%s) and run `docker compose run --rm setup upgrade`", ComposeFile, ReleaseURL)
		}
		if compose, err = inspectCompose(root, opts.assets, st.Variant, nil, os.LookupEnv); err != nil {
			return err
		}
		if compose.Variant != st.Variant {
			return fmt.Errorf("%s is now the %s variant, but this deployment was set up as %s: switching variants is not supported by upgrade (set up a new directory and move the data, docs/installation/upgrades.md)", ComposeFile, compose.Variant, st.Variant)
		}
		if compose.FromEnv && opts.Image.Digest != "" && opts.Image.String() != compose.Image.String() {
			return fmt.Errorf("--image %s does not match %s=%s, which docker compose resolved for this setup container (the old image keeps running setup): run `%s=%s docker compose run --rm setup upgrade`, so the new image's setup runs and writes it to %s", opts.Image, ImageEnvVar, compose.Image, ImageEnvVar, opts.Image, EnvFile)
		}
		if opts.Image.Digest != "" && opts.Image.String() != compose.Image.String() {
			return fmt.Errorf("the image %s does not match the x-bekup-image line of %s (%s): run setup from that file (`docker compose run --rm setup upgrade`)", opts.Image, ComposeFile, compose.Image)
		}
		if err := composeMatches(compose, &Answers{Image: compose.Image, Variant: st.Variant, ImageInEnv: compose.FromEnv}); err != nil {
			return err
		}
		opts.Image = compose.Image
	default:
		if !opts.WriteCompose {
			return fmt.Errorf("this deployment is setup-first (setup writes %s): pass --write-compose --image <new pinned image>", ComposeFile)
		}
		if opts.Image.Digest == "" {
			return errors.New("--image is required (the new digest-pinned image; or BEKUP_IMAGE)")
		}
	}
	if _, err := imageref.ParsePinned(opts.Image.String()); err != nil {
		return pinHelp(err)
	}
	if !opts.AllowVersionMismatch {
		if err := CheckVersionTag(opts.Image, opts.Version); err != nil {
			return err
		}
	}
	if IsDowngrade(st.BekupVersion, opts.Version) && !opts.AllowDowngrade {
		return fmt.Errorf("this bekup (%s) is older than the one that set up this directory (%s): downgrades can break the database schema; pass --allow-downgrade only after restoring a matching database backup", opts.Version, st.BekupVersion)
	}
	// The operator's TOMLs must still pass the (new) validators.
	if err := validateConfigs(root); err != nil {
		return fmt.Errorf("fix the configuration before upgrading: %w", err)
	}

	gen, err := generatedFiles(opts.assets, opts.Image, st.Variant, st.ComposeMode == ComposeGenerated)
	if err != nil {
		return err
	}
	assets, err := assetFiles(opts.assets, st.Variant.assetDirs())
	if err != nil {
		return err
	}
	layout := Layout{KeysDir: st.KeysDir}
	entries := Manifest(layout, assets, st.manifestOptions())

	// Local edits: compare every recorded file with its recorded hash.
	stamp := opts.Now().UTC().Format("20060102T150405Z")
	var edited []string
	for _, p := range st.assetPaths() {
		cur, err := readManaged(root, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if hashOf(cur) != st.Assets[p] {
			if newData, ok := gen[p]; ok && hashOf(newData) == hashOf(cur) {
				continue // already the new content
			}
			edited = append(edited, p)
		}
	}
	if len(edited) > 0 && !opts.Force {
		return fmt.Errorf("generated files were edited locally: %s; move the changes to %s (or your own files) and re-run, or pass --force to keep a copy as <file>.bak-<timestamp> and replace them", strings.Join(edited, ", "), OverrideFile)
	}
	// Files at paths that become generated with this version but that
	// setup did not write (the operator's own files): never overwritten
	// silently.
	var foreign []string
	for _, p := range sortedKeys(gen) {
		if _, known := st.Assets[p]; known {
			continue
		}
		cur, err := readManaged(root, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if hashOf(cur) != hashOf(gen[p]) {
			foreign = append(foreign, p)
		}
	}
	if len(foreign) > 0 && !opts.Force {
		return fmt.Errorf("this version adds generated files where files of your own exist: %s; move them away and re-run, or pass --force to keep a copy as <file>.bak-<timestamp> and replace them", strings.Join(foreign, ", "))
	}
	backups := append(append([]string(nil), edited...), foreign...)

	defer func() {
		if retErr != nil {
			if rbErr := w.rollback(); rbErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback incomplete: %w", rbErr))
			}
		}
	}()

	for _, p := range backups {
		cur, err := readManaged(root, p)
		if err != nil {
			return err
		}
		if err := w.create(p+".bak-"+stamp, cur, 0o644); err != nil {
			return err
		}
	}

	var oldCompose []byte
	if compose == nil {
		if oldCompose, err = readManaged(root, ComposeFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	changed := 0
	for _, e := range entries {
		if e.Kind == KindDir && e.Class == ClassGenerated {
			if err := w.mkdirAll(e.Path, e.Mode); err != nil {
				return err
			}
		}
	}
	for _, e := range entries {
		data, ok := gen[e.Path]
		if !ok {
			continue
		}
		cur, err := readManaged(root, e.Path)
		if err == nil && hashOf(cur) == hashOf(data) {
			continue
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := w.replace(e.Path, data, e.Mode); err != nil {
			return err
		}
		changed++
	}
	// Stale assets: recorded before, not shipped any more.
	var stale []string
	for _, p := range st.assetPaths() {
		if _, ok := gen[p]; !ok {
			stale = append(stale, p)
		}
	}
	for _, p := range stale {
		if err := w.remove(p); err != nil {
			return err
		}
		changed++
	}

	// compose.yaml taking the image from BEKUP_IMAGE (the committed file):
	// .env carries the new image; a release file pinning it: no line.
	envChanged := false
	if compose != nil {
		img := ""
		if compose.FromEnv {
			img = opts.Image.String()
		}
		if envChanged, err = syncEnvImage(w, img); err != nil {
			return err
		}
	}

	// Secret groups that are new in this manifest.
	known := map[string]bool{}
	for _, g := range st.SecretGroups {
		known[g] = true
	}
	groups, err := secretGroupStates(root, entries)
	if err != nil {
		return err
	}
	var newGroups []string
	for _, g := range secretGroups(entries) {
		if known[g] {
			continue
		}
		gs := groups[g]
		if gs != nil && !gs.empty() {
			return fmt.Errorf("the new %s secrets partly exist already (%s); remove them or restore the rest", g, strings.Join(gs.paths, ", "))
		}
		newGroups = append(newGroups, g)
	}
	for _, g := range newGroups {
		if err := generateGroup(w, layout, entries, g, opts); err != nil {
			return err
		}
		changed++
	}

	next := *st
	next.ManifestVersion = ManifestVersion
	next.BekupVersion = opts.Version
	next.Image = opts.Image.String()
	if compose != nil {
		next.ComposeSHA256 = compose.Hash
		next.ImageSource = ImageSourceCompose
		if compose.FromEnv {
			next.ImageSource = ImageSourceEnv
		}
	}
	next.Assets = map[string]string{}
	for p, data := range gen {
		next.Assets[p] = hashOf(data)
	}
	next.SecretGroups = secretGroups(entries)
	mode, why := resolveOwnership(opts.Ownership, root, *opts.sys)
	next.Ownership = mode
	newState, err := next.Marshal()
	if err != nil {
		return err
	}
	oldState, err := st.Marshal()
	if err != nil {
		return err
	}
	if !bytes.Equal(newState, oldState) {
		if err := w.replace(StateFileName, newState, 0o644); err != nil {
			return err
		}
	}

	warnings, err := applyOwnership(w.r, entries, mode, *opts.sys, opts.Out)
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\nbekup setup upgrade: %s\n  image:     %s -> %s\n  ownership: %s (%s)\n", root, st.Image, opts.Image, mode, why)
	for _, msg := range warnings {
		fmt.Fprintf(&b, "  WARNING: %s\n", msg)
	}
	if changed == 0 && !envChanged && bytes.Equal(newState, oldState) {
		b.WriteString("  no changes\n")
	} else {
		fmt.Fprintf(&b, "  %d generated file(s) changed, %d removed, %d backed up\n", changed-len(stale)-len(newGroups), len(stale), len(backups))
		for _, p := range stale {
			fmt.Fprintf(&b, "  removed stale asset %s\n", p)
		}
		for _, g := range newGroups {
			fmt.Fprintf(&b, "  generated new %s secrets\n", g)
		}
		if compose == nil {
			if diff := LineDiff(string(oldCompose), string(gen[ComposeFile]), 40); diff != "" {
				fmt.Fprintf(&b, "\nCompose changes (%s):\n%s", ComposeFile, diff)
			}
		}
	}
	if envChanged {
		if compose.FromEnv {
			fmt.Fprintf(&b, "  %s: %s=%s (compose.yaml takes the image from it)\n", EnvFile, ImageEnvVar, opts.Image)
		} else {
			fmt.Fprintf(&b, "  %s: removed %s (compose.yaml pins the image)\n", EnvFile, ImageEnvVar)
		}
	}
	if compose != nil {
		fmt.Fprintf(&b, "  %s: the %s variant (%s), never written by setup\n", ComposeFile, compose.Variant, compose.Level)
		for _, f := range compose.Findings {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	b.WriteString("\nNext: back up (docs/installation/upgrades.md), then `docker compose pull && docker compose up -d` and `docker compose -f compose.yaml run --rm setup check`.\n")
	_, err = io.WriteString(opts.Out, b.String())
	return err
}

// generateGroup creates the secrets of one group (upgrade: groups new in
// the manifest).
func generateGroup(w *writer, l Layout, entries []Entry, group string, opts UpgradeOptions) error {
	for _, e := range entries {
		if e.Group == group && e.Kind == KindDir {
			if err := w.mkdirAll(e.Path, e.Mode); err != nil {
				return err
			}
		}
	}
	switch group {
	case groupMasterKey:
		_, err := generateMasterKey(w, l, opts.Now())
		return err
	case groupDatabase:
		return generateDatabase(w, l)
	case groupCerts:
		if opts.CAPassphrase == nil {
			return errors.New("new certificates need a CA key passphrase")
		}
		pass, err := opts.CAPassphrase()
		if err != nil {
			return err
		}
		defer crypto.Zero(pass)
		return generateCerts(w, l, pass, opts.certsInit)
	}
	return fmt.Errorf("unknown secret group %q", group)
}

// readManaged reads a non-secret managed file (no symlinks).
func readManaged(root, rel string) ([]byte, error) {
	fi, err := checkNoSymlinks(root, rel)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return nil, fs.ErrNotExist
	}
	abs, err := resolve(root, rel)
	if err != nil {
		return nil, err
	}
	return readRegular(abs, maxManagedFile)
}

// LineDiff returns a minimal line diff ("- old" / "+ new" lines with two
// lines of context markers omitted) of a and b, at most maxLines lines.
func LineDiff(a, b string, maxLines int) string {
	if a == b {
		return ""
	}
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	// LCS table (compose files are small).
	n, m := len(al), len(bl)
	if n*m > 4_000_000 {
		return fmt.Sprintf("  (%d -> %d lines; too large to diff)\n", n, m)
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "  + "+bl[j])
			j++
		default:
			out = append(out, "  - "+al[i])
			i++
		}
	}
	if len(out) > maxLines {
		extra := len(out) - maxLines
		out = append(out[:maxLines], fmt.Sprintf("  ... %d more changed line(s)", extra))
	}
	return strings.Join(out, "\n") + "\n"
}
