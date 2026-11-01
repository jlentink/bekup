package setup

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/imageref"
)

// Variant is a deployment variant of the compose file (D-205). Variants
// are overlays of the repository compose file (an edit list applied by
// the line editor in outline.go), never copies, and overlays never name
// an image: the bekup image comes only from the x-bekup-image line.
type Variant string

// Variants.
const (
	// VariantTraefik runs bekup with its own Traefik (release asset
	// compose.yaml).
	VariantTraefik Variant = "traefik"
	// VariantOwnProxy runs bekup behind the operator's own reverse proxy
	// on this Docker host (D-215, compose.own-proxy.yaml): serve joins the
	// proxy's Docker network (external, BEKUP_PROXY_NETWORK) under the
	// alias bekup-serve and trusts the proxy by its container name
	// (server.trusted_proxy_hosts). The bekup-proxy link network of D-205
	// is the advanced link mode (an overlay).
	VariantOwnProxy Variant = "own-proxy"
	// VariantFull is the repository compose file itself (every service,
	// proxy profiles traefik | nginx); setup-first only.
	VariantFull Variant = "full"
)

// Retired variants (D-215): recorded by setup states of earlier versions
// and mapped to own-proxy in link mode (with the Traefik labels for
// traefik-external) when such a state is read.
const (
	legacyVariantTraefikExternal Variant = "traefik-external"
	legacyVariantNPM             Variant = "npm"
)

// ReleaseVariants are the variants published as release assets, in
// recognition order.
var ReleaseVariants = []Variant{VariantTraefik, VariantOwnProxy}

// AllVariants lists every variant.
var AllVariants = []Variant{VariantTraefik, VariantOwnProxy, VariantFull}

// ParseVariant validates a --variant value.
func ParseVariant(s string) (Variant, error) {
	for _, v := range AllVariants {
		if string(v) == s {
			return v, nil
		}
	}
	if Variant(s) == legacyVariantTraefikExternal || Variant(s) == legacyVariantNPM {
		return "", fmt.Errorf("--variant %s was retired (D-215): use own-proxy (any reverse proxy on this Docker host; --proxy-labels adds the Traefik labels, --proxy-network-mode link keeps the bekup-proxy link network)", s)
	}
	return "", fmt.Errorf("--variant must be traefik, own-proxy or full (got %q)", bounded(s))
}

// OwnProxy reports whether the variant sits behind the operator's own
// reverse proxy on this Docker host.
func (v Variant) OwnProxy() bool { return v == VariantOwnProxy }

// DBMode is the second overlay dimension (D-208): bekup's own app database
// is the bundled postgres service or the operator's PostgreSQL (fields
// mode, D-207).
type DBMode string

// Database modes.
const (
	// DBBundled runs the bundled postgres service (the default).
	DBBundled DBMode = "bundled"
	// DBExternal connects to the operator's PostgreSQL: no postgres
	// service, a non-internal backend network, the bekup_app/bekup_owner
	// password files and the server's CA bundle as secrets.
	DBExternal DBMode = "external"
)

// DBModes lists the database modes in recognition order.
var DBModes = []DBMode{DBBundled, DBExternal}

// ParseDBMode validates a --database value ("" is bundled).
func ParseDBMode(s string) (DBMode, error) {
	switch DBMode(s) {
	case "", DBBundled:
		return DBBundled, nil
	case DBExternal:
		return DBExternal, nil
	}
	return "", fmt.Errorf("--database must be bundled or external (got %q)", bounded(s))
}

// OrBundled maps the zero value to DBBundled.
func (d DBMode) OrBundled() DBMode {
	if d == "" {
		return DBBundled
	}
	return d
}

// AssetName is the release asset name of a variant with a database mode:
// compose.yaml and compose.own-proxy.yaml and, for an external database,
// compose.external-db.yaml and compose.own-proxy.external-db.yaml.
func (v Variant) AssetName(db DBMode) string {
	name := "compose"
	if v != VariantTraefik {
		name += "." + string(v)
	}
	if db == DBExternal {
		name += ".external-db"
	}
	return name + ".yaml"
}

// assetDirs returns the embedded deploy directories the variant mounts
// (an external database needs no deploy/postgres).
func (v Variant) assetDirs(db DBMode) []string {
	var dirs []string
	switch v {
	case VariantTraefik:
		dirs = []string{"deploy/postgres", "deploy/traefik"}
	case VariantOwnProxy:
		dirs = []string{"deploy/postgres"}
	default:
		dirs = assetDirs
	}
	if db != DBExternal {
		return dirs
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != "deploy/postgres" {
			out = append(out, d)
		}
	}
	return out
}

//go:embed variants
var variantFS embed.FS

type editKind int

const (
	editHeader editKind = iota
	editRemove
	editReplace
	editInsertAfter
	editReplaceLead
	editReplaceComment
	editReplaceLine
	editRenameInComment
)

// edit is one overlay step: a path (or comment marker) and a fragment
// file below variants/ ("" for removals).
type edit struct {
	kind     editKind
	path     string
	fragment string
}

func header(frag string) edit                 { return edit{editHeader, "", frag} }
func remove(path string) edit                 { return edit{editRemove, path, ""} }
func replace(path, frag string) edit          { return edit{editReplace, path, frag} }
func insertAfter(path, frag string) edit      { return edit{editInsertAfter, path, frag} }
func replaceLead(path, frag string) edit      { return edit{editReplaceLead, path, frag} }
func replaceComment(marker, frag string) edit { return edit{editReplaceComment, marker, frag} }
func replaceLine(line, frag string) edit      { return edit{editReplaceLine, line, frag} }

// renameInComment replaces old with repl in the one comment line holding
// it (the path field carries old, the fragment field repl).
func renameInComment(old, repl string) edit { return edit{editRenameInComment, old, repl} }

// proxyBanner marks the comment block that introduces the reverse-proxy
// services in the repository compose file.
const proxyBanner = "Reverse proxy (task 0.7b)"

// ownProxyEdits turn the repository compose file into the own-proxy
// variant (D-215): no bundled proxy, no frontend/edge network, serve on the
// operator's proxy network (external, BEKUP_PROXY_NETWORK) under the alias
// bekup-serve, listening on 0.0.0.0:8080 (BEKUP_LISTEN_SHAPE).
var ownProxyEdits = []edit{
	header("own-proxy/header.yaml"),
	replaceComment(proxyBanner, ""),
	remove("services/traefik-init"),
	remove("services/traefik"),
	remove("services/nginx-init"),
	remove("services/nginx"),
	remove("services/certbot"),
	replaceLead("services/serve", "own-proxy/serve-lead.yaml"),
	insertAfter("services/serve/environment/BEKUP_IMAGE_MODE", "own-proxy/serve-environment.yaml"),
	replace("services/serve/networks/frontend", "own-proxy/serve-network.yaml"),
	replaceLead("networks", "own-proxy/networks-lead.yaml"),
	replace("networks/frontend", "own-proxy/network.yaml"),
	remove("networks/edge"),
	remove("volumes/traefik-acme"),
	remove("volumes/nginx-certs"),
	remove("volumes/nginx-acme-webroot"),
}

// variantEdits are the overlays (applied in order).
var variantEdits = map[Variant][]edit{
	VariantTraefik: {
		header("traefik/header.yaml"),
		replaceComment(proxyBanner, "traefik/proxy-banner.yaml"),
		remove("services/traefik-init/profiles"),
		remove("services/traefik/profiles"),
		remove("services/nginx-init"),
		remove("services/nginx"),
		remove("services/certbot"),
		replaceLead("networks/edge", "traefik/edge-lead.yaml"),
		replaceLead("volumes/traefik-acme", "traefik/acme-lead.yaml"),
		remove("volumes/nginx-certs"),
		remove("volumes/nginx-acme-webroot"),
	},
	VariantOwnProxy: ownProxyEdits,
	VariantFull: {
		header("full/header.yaml"),
	},
}

// Anchors of the external-database overlay in the repository compose file.
const (
	// dbNoteMarker starts the free-standing comment block about the app
	// database (above `services:`).
	dbNoteMarker = "App database: the bundled"
	// trustLinePostgres is the postgres line of the trust-boundary
	// diagram in every variant header.
	trustLinePostgres = "#   serve ──backend (TLS, verify-full)──> postgres  (role bekup_app; migrations as bekup_owner)"
	// installUpLine is the `up -d` step of the release variants' headers.
	installUpLine = "#   3. docker compose up -d"
	// saveStepLine is the first install step of the release variants'
	// headers.
	saveStepLine = "#   1. Save this file there as compose.yaml."
)

// externalDBEdits turn a variant into its external-database form (D-208):
// no postgres service (or its volume, init assets and secrets), serve,
// admin and migrate get the password files of database.password_file /
// owner_password_file and the operator's CA bundle, the certs-init service
// issues no postgres certificate, and the backend network is not internal
// (it must reach the operator's PostgreSQL).
func externalDBEdits(v Variant) []edit {
	edits := []edit{replaceLine(trustLinePostgres, "external-db/trust-line.yaml")}
	if v != VariantFull {
		bundled, external := v.AssetName(DBBundled), v.AssetName(DBExternal)
		edits = append(edits,
			replaceLine(saveStepLine, "external-db/save-step.yaml"),
			replaceLine(installUpLine, "external-db/install-step.yaml"),
			// The header names the release asset and the committed file.
			renameInComment("release asset "+bundled+":", "release asset "+external+":"),
			renameInComment("deploy/compose/"+bundled+" in the repository", "deploy/compose/"+external+" in the repository"),
		)
	}
	return append(edits,
		replaceComment(dbNoteMarker, "external-db/db-note.yaml"),
		replaceLead("services/certs-init", "external-db/certs-init-lead.yaml"),
		replace("services/certs-init/command", "external-db/certs-init-command.yaml"),
		replace("services/migrate/environment", "external-db/migrate-environment.yaml"),
		replace("services/migrate/secrets", "external-db/migrate-mounts.yaml"),
		remove("services/migrate/depends_on"),
		replace("services/admin/secrets", "external-db/admin-mounts.yaml"),
		replace("services/serve/secrets", "external-db/serve-mounts.yaml"),
		remove("services/serve/depends_on/postgres"),
		remove("services/postgres"),
		replace("networks/backend", "external-db/backend-network.yaml"),
		remove("volumes/postgres-data"),
		remove("secrets/bekup_database_url"),
		remove("secrets/bekup_database_owner_url"),
		remove("secrets/postgres_password"),
		remove("secrets/bekup_owner_password"),
		remove("secrets/bekup_app_password"),
		insertAfter("secrets/bekup_master_key", "external-db/password-files.yaml"),
		remove("secrets/postgres_crt"),
		remove("secrets/postgres_key"),
		replace("secrets/bekup_db_ca_crt", "external-db/db-ca-file.yaml"),
		replaceLead("secrets", "external-db/secrets-lead.yaml"),
	)
}

// ComposeImageVar is the value of the x-bekup-image line in the
// repository compose file.
const ComposeImageVar = "${BEKUP_IMAGE:-bekup:dev}"

// ImageEnvVar names the bekup image in the committed compose files and in
// .env (placeholder mode, D-205 amendment 3).
const ImageEnvVar = "BEKUP_IMAGE"

// ComposeImagePlaceholder is the value of the x-bekup-image line of the
// committed compose files in deploy/compose/ (D-205 amendment 3): no
// default, so docker compose refuses to run without BEKUP_IMAGE; setup
// requires it to be digest-pinned (or a followed tag of the official
// repository, D-212) and writes it to .env.
const ComposeImagePlaceholder = "${BEKUP_IMAGE:?set BEKUP_IMAGE to the bekup image, digest-pinned repo:tag@sha256:digest or ghcr.io/jlentink/bekup:latest, see the README}"

// composeImagePrefix starts the single line of a compose file that names
// the bekup image. Release and setup replace exactly that line.
const composeImagePrefix = "x-bekup-image: &bekup-image "

// ComposeImageLine is the image line of the repository compose file.
const ComposeImageLine = composeImagePrefix + ComposeImageVar

// ComposePlaceholderLine is the image line of the committed compose files
// (deploy/compose/*, `bekup setup compose` without --image).
const ComposePlaceholderLine = composeImagePrefix + ComposeImagePlaceholder

var imageLineAnyRe = regexp.MustCompile(`(?m)^x-bekup-image:`)

// imageVarRefRe matches a reference to the BEKUP_IMAGE compose variable
// (not BEKUP_IMAGE_MODE).
var imageVarRefRe = regexp.MustCompile(`\$\{BEKUP_IMAGE(?:[^A-Za-z0-9_]|$)`)

// applyEdits renders the overlay of v (and the database mode db) onto src.
func applyEdits(src []byte, v Variant, db DBMode) (string, error) {
	edits, ok := variantEdits[v]
	if !ok {
		return "", fmt.Errorf("unknown variant %q", bounded(string(v)))
	}
	switch db.OrBundled() {
	case DBBundled:
	case DBExternal:
		edits = append(slices.Clone(edits), externalDBEdits(v)...)
	default:
		return "", fmt.Errorf("unknown database mode %q", bounded(string(db)))
	}
	o, err := newOutline(string(src))
	if err != nil {
		return "", err
	}
	for _, e := range edits {
		frag := ""
		if e.fragment != "" && e.kind != editRenameInComment {
			b, err := fs.ReadFile(variantFS, "variants/"+e.fragment)
			if err != nil {
				return "", fmt.Errorf("variant %s: %w", v, err)
			}
			frag = string(b)
		}
		switch e.kind {
		case editHeader:
			err = o.replaceHeader(frag)
		case editRemove:
			err = o.remove(e.path)
		case editReplace:
			err = o.replace(e.path, frag)
		case editInsertAfter:
			err = o.insertAfter(e.path, frag)
		case editReplaceLead:
			err = o.replaceLead(e.path, frag)
		case editReplaceComment:
			err = o.replaceComment(e.path, frag)
		case editReplaceLine:
			err = o.replaceLine(e.path, frag)
		case editRenameInComment:
			err = o.renameInComment(e.path, e.fragment)
		}
		if err != nil {
			return "", fmt.Errorf("variant %s: %w", v, err)
		}
	}
	return o.String(), nil
}

// RenderVariant renders a variant with a database mode from the
// repository compose file src.
// With img set, the single x-bekup-image line becomes the digest-pinned
// reference as a literal (a shell variable can never override it; the
// release assets); without, it becomes ComposePlaceholderLine (the
// committed files in deploy/compose/: the image comes from BEKUP_IMAGE,
// which setup requires to be pinned and writes to .env). It refuses a
// source with any other ${BEKUP_IMAGE use, an image line that is not
// exactly the expected one, or a build: key.
func RenderVariant(src []byte, v Variant, db DBMode, img *imageref.Ref) ([]byte, error) {
	out, err := applyEdits(src, v, db)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\n")
	n := 0
	for i, l := range lines {
		if l == ComposeImageLine {
			n++
			lines[i] = ComposePlaceholderLine
			if img != nil {
				lines[i] = composeImagePrefix + img.String()
			}
		}
	}
	if n != 1 || len(imageLineAnyRe.FindAllStringIndex(out, -1)) != 1 {
		return nil, fmt.Errorf("compose source: expected exactly one %q line", ComposeImageLine)
	}
	if len(imageVarRefRe.FindAllStringIndex(out, -1)) != 1 {
		return nil, errors.New("compose source: a further ${BEKUP_IMAGE...} reference exists")
	}
	if img != nil {
		if _, err := imageref.ParseBekup(img.String()); err != nil {
			return nil, err
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "build:") {
			return nil, errors.New("compose source: build: is not allowed in the production compose file")
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}
