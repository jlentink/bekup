package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/imageref"
)

// ReleaseURL is where the compose files are published (D-205).
const ReleaseURL = "https://github.com/jlentink/bekup/releases/latest"

// Recognition is how closely the operator's compose.yaml matches a
// variant rendered by this binary with the same image (compose-first).
type Recognition string

// Recognition levels.
const (
	// RecognizedExact: byte-equal to the release file (or, for a file
	// that takes the image from BEKUP_IMAGE, to the committed file in
	// deploy/compose/).
	RecognizedExact Recognition = "exact"
	// RecognizedModel: the same YAML model (only comments or formatting
	// differ).
	RecognizedModel Recognition = "model"
	// RecognizedCustom: customized; the operator names the variant it is
	// based on (--variant) and the hardening lint decides.
	RecognizedCustom Recognition = "customized"
)

// ComposeInfo describes the operator's compose.yaml (compose-first).
type ComposeInfo struct {
	// Image is the digest-pinned x-bekup-image value (FromEnv: the
	// BEKUP_IMAGE value docker compose resolves it to).
	Image imageref.Ref
	// FromEnv: the x-bekup-image line is ComposeImagePlaceholder (the
	// committed copy in deploy/compose/): the image comes from BEKUP_IMAGE
	// (the setup container's environment, else .env) and setup keeps it in
	// .env (D-205 amendment 3).
	FromEnv bool
	Variant Variant
	// DB is the database mode of the file (D-208): the bundled postgres
	// service or the operator's PostgreSQL.
	DB    DBMode
	Level Recognition
	// Hash is the SHA-256 (hex) of the file.
	Hash string
	// Findings are the lint findings (hardening, pins, setup service).
	Findings []Finding
}

// HasErrors reports an ERROR finding.
func (c *ComposeInfo) HasErrors() bool { return hasErrors(c.Findings) }

func hasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// ErrNoCompose means the deployment directory has no compose.yaml.
var ErrNoCompose = errors.New("no " + ComposeFile)

func noComposeError(root string) error {
	return fmt.Errorf(`%w in %s: download it from %s
(compose.yaml: bekup with its own Traefik; compose.traefik-external.yaml or
compose.npm.yaml behind your proxy; the *.external-db.yaml files use your own
PostgreSQL; save it as compose.yaml), verify it with
'gh attestation verify compose.yaml --repo jlentink/bekup' and run
'docker compose run --rm setup' next to it. To let setup write the compose
file instead (setup-first), run the wizard ('bekup setup' on a terminal) or
pass --write-compose --variant traefik|traefik-external|npm|full [--database external]`,
		ErrNoCompose, root, ReleaseURL)
}

// InspectCompose reads compose.yaml below dir (compose-first) and returns
// its pinned image, the recognized variant and the lint findings of the
// resolved project (compose.yaml, compose.override.yaml when present, and
// .env). A customized file needs requested (the variant it is based on);
// a recognized file must match requested (and requestedDB) when given.
func InspectCompose(dir string, requested Variant, requestedDB DBMode) (*ComposeInfo, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return inspectCompose(root, bekup.Assets(), requested, requestedDB, nil, os.LookupEnv)
}

// composeInput is what docker compose would read in the deployment
// directory.
type composeInput struct {
	root     string
	compose  []byte
	override []byte // nil: no compose.override.yaml
	dotenv   map[string]string
	lookup   func(string) (string, bool)
}

// realRoot is the deployment directory on disk (for the symlink scan).
func (in *composeInput) realRoot() string { return in.root }

func (in *composeInput) docs() []composeSrc {
	docs := []composeSrc{{name: ComposeFile, data: in.compose}}
	if in.override != nil {
		docs = append(docs, composeSrc{name: OverrideFile, data: in.override})
	}
	return docs
}

// inspectCompose reads compose.yaml, compose.override.yaml and (when
// dotenv is nil) .env below root.
func inspectCompose(root string, fsys fs.FS, requested Variant, requestedDB DBMode, dotenv map[string]string, lookup func(string) (string, bool)) (*ComposeInfo, error) {
	data, err := readManaged(root, ComposeFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, noComposeError(root)
	}
	if err != nil {
		return nil, err
	}
	override, err := readManaged(root, OverrideFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		override = nil
	case err != nil:
		return nil, err
	case override == nil:
		override = []byte{}
	}
	if dotenv == nil {
		if dotenv, err = readDotEnv(root); err != nil {
			return nil, err
		}
	}
	return inspectComposeData(fsys, composeInput{root: root, compose: data, override: override, dotenv: dotenv, lookup: lookup}, requested, requestedDB)
}

var composeImageLineRe = regexp.MustCompile(`^x-bekup-image: &bekup-image (\S.*)$`)

// composeImage returns the pinned reference of the single x-bekup-image
// line, which must also be the YAML value of the top-level key of the
// file's only YAML document. A line that is exactly ComposePlaceholderLine
// (the committed files in deploy/compose/) returns placeholder true and no
// reference: the image then comes from BEKUP_IMAGE.
func composeImage(data []byte) (img imageref.Ref, placeholder bool, err error) {
	if err := singleDocument(ComposeFile, data); err != nil {
		return imageref.Ref{}, false, err
	}
	var line string
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "x-bekup-image:") {
			n++
			line = strings.TrimSuffix(l, "\r")
		}
	}
	if n != 1 {
		return imageref.Ref{}, false, fmt.Errorf("%s: expected exactly one top-level x-bekup-image line, found %d (use the release file unchanged)", ComposeFile, n)
	}
	m := composeImageLineRe.FindStringSubmatch(line)
	if m == nil {
		return imageref.Ref{}, false, fmt.Errorf("%s: the x-bekup-image line must read 'x-bekup-image: &bekup-image <repo>:<tag>@sha256:<digest>' (a release file) or be the line of the committed file in deploy/compose/", ComposeFile)
	}
	value := m[1]
	switch {
	case value == ComposeImagePlaceholder:
		placeholder = true
	case value == ComposeImageVar:
		return imageref.Ref{}, false, fmt.Errorf("%s: the x-bekup-image line is the development default of the repository's docker-compose.yml: use a release file (%s) or the committed file from deploy/compose/", ComposeFile, ReleaseURL)
	case strings.Contains(value, "$"):
		return imageref.Ref{}, false, fmt.Errorf("%s: the x-bekup-image line must be a digest-pinned reference or exactly the line of the committed file in deploy/compose/ (`%s`)", ComposeFile, ComposePlaceholderLine)
	default:
		if img, err = imageref.ParsePinned(value); err != nil {
			return imageref.Ref{}, false, fmt.Errorf("%s: the x-bekup-image %q is not digest-pinned: %w", ComposeFile, bounded(value), err)
		}
	}
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return imageref.Ref{}, false, fmt.Errorf("%s is not valid YAML: %w", ComposeFile, err)
	}
	if v, ok := top["x-bekup-image"].(string); !ok || v != value {
		return imageref.Ref{}, false, fmt.Errorf("%s: the x-bekup-image line is not the value of the top-level x-bekup-image key", ComposeFile)
	}
	return img, placeholder, nil
}

// envImage returns the BEKUP_IMAGE value docker compose resolves the
// placeholder line to (env: .env overridden by the environment), which
// must be digest-pinned.
func envImage(env map[string]string) (imageref.Ref, error) {
	raw, ok := env[ImageEnvVar]
	if !ok || raw == "" {
		return imageref.Ref{}, fmt.Errorf("%s takes the bekup image from %s (the committed file from deploy/compose/), but it is not set: run `%s=<repo>:<tag>@sha256:<digest> docker compose run --rm setup` (docker compose itself refuses to start without it); setup then keeps it in %s", ComposeFile, ImageEnvVar, ImageEnvVar, EnvFile)
	}
	img, err := imageref.ParsePinned(raw)
	if err != nil {
		return imageref.Ref{}, pinHelp(fmt.Errorf("%s=%q (used by the x-bekup-image line of %s) is not digest-pinned: %w", ImageEnvVar, bounded(raw), ComposeFile, err))
	}
	return img, nil
}

// inspectComposeData recognizes the variant of compose.yaml and lints the
// resolved project. Recognition and lint use the same model: a file is
// *model*-equal only when both its YAML model and the project docker
// compose resolves from it (with the same .env) equal a release file's.
// Every release variant is tried with both database modes (D-208).
func inspectComposeData(fsys fs.FS, in composeInput, requested Variant, requestedDB DBMode) (*ComposeInfo, error) {
	if requested != "" && !slices.Contains(ReleaseVariants, requested) {
		return nil, fmt.Errorf("--variant %s: a compose-first deployment uses one of the release files (traefik, traefik-external, npm); the full variant is setup-first only (--write-compose)", requested)
	}
	if in.root == "" {
		in.root = "/deploy"
	}
	data := in.compose
	img, placeholder, err := composeImage(data)
	if err != nil {
		return nil, err
	}
	if in.override != nil {
		if err := singleDocument(OverrideFile, in.override); err != nil {
			return nil, err
		}
	}
	src, err := fs.ReadFile(fsys, composeSource)
	if err != nil {
		return nil, fmt.Errorf("reading the embedded compose file: %w", err)
	}
	env := projectEnv(in.dotenv, in.lookup, in.docs()...)
	// The release files are rendered with the pinned literal; the
	// committed files keep the placeholder (the image comes from env).
	renderImg := &img
	if placeholder {
		if img, err = envImage(env); err != nil {
			return nil, err
		}
		renderImg = nil
	}
	info := &ComposeInfo{Image: img, FromEnv: placeholder, Hash: hashOf(data)}
	var model any
	modelErr := yaml.Unmarshal(data, &model)
	own, ownErr := loadProject(in.root, env, composeSrc{name: ComposeFile, data: data})
recognize:
	for _, v := range ReleaseVariants {
		for _, db := range DBModes {
			r, err := RenderVariant(src, v, db, renderImg)
			if err != nil {
				return nil, err
			}
			if bytes.Equal(r, data) {
				info.Variant, info.DB, info.Level = v, db, RecognizedExact
				break recognize
			}
			if modelErr != nil || ownErr != nil {
				continue
			}
			var want any
			if err := yaml.Unmarshal(r, &want); err != nil {
				return nil, err
			}
			if !reflect.DeepEqual(want, model) {
				continue
			}
			rp, err := loadProject(in.root, env, composeSrc{name: ComposeFile, data: r})
			if err != nil {
				return nil, fmt.Errorf("resolving the %s variant: %w", v, err)
			}
			if reflect.DeepEqual(rp, own) {
				info.Variant, info.DB, info.Level = v, db, RecognizedModel
				break recognize
			}
		}
	}
	switch {
	case info.Variant != "" && requested != "" && requested != info.Variant:
		return nil, fmt.Errorf("%s is the %s variant, not %s", ComposeFile, info.Variant, requested)
	case info.Variant != "" && requestedDB != "" && requestedDB != info.DB:
		return nil, fmt.Errorf("%s uses the %s database, not %s (%s)", ComposeFile, info.DB, requestedDB, dbFileHint(info.DB))
	case info.Variant == "" && requested == "":
		return nil, fmt.Errorf("%s is customized: it matches none of the release files of bekup %s. Pass --variant traefik|traefik-external|npm (and --database bundled|external) to name the file it is based on (the hardening lint then checks it), or start from the unchanged release file and put changes into %s", ComposeFile, img.Tag, OverrideFile)
	case info.Variant == "":
		info.Variant, info.Level = requested, RecognizedCustom
		info.DB = requestedDB
		if info.DB == "" {
			// Not named: a file with the postgres service is based on the
			// bundled form (the lint then holds it to that form).
			info.DB = DBExternal
			if own != nil && serviceOf(own, "postgres") != nil {
				info.DB = DBBundled
			}
		}
	}
	if modelErr != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %w", ComposeFile, modelErr)
	}
	expected, err := RenderVariant(src, info.Variant, info.DB, renderImg)
	if err != nil {
		return nil, err
	}
	info.Findings = lintComposeFiles(&in, env, info.Variant, img.String(), expected, placeholder)
	return info, nil
}

// dbFileHint names the compose files of a database mode.
func dbFileHint(db DBMode) string {
	if db == DBExternal {
		return "an *.external-db.yaml release file"
	}
	return "a release file without .external-db"
}
