package setup

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
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
	// VariantTraefikExternal runs bekup behind the operator's Traefik on
	// the bekup-proxy link network (compose.traefik-external.yaml).
	VariantTraefikExternal Variant = "traefik-external"
	// VariantNPM runs bekup behind Nginx Proxy Manager on the bekup-proxy
	// link network (compose.npm.yaml).
	VariantNPM Variant = "npm"
	// VariantFull is the repository compose file itself (every service,
	// proxy profiles traefik | nginx); setup-first only.
	VariantFull Variant = "full"
)

// ReleaseVariants are the variants published as release assets, in
// recognition order.
var ReleaseVariants = []Variant{VariantTraefik, VariantTraefikExternal, VariantNPM}

// AllVariants lists every variant.
var AllVariants = []Variant{VariantTraefik, VariantTraefikExternal, VariantNPM, VariantFull}

// ParseVariant validates a --variant value.
func ParseVariant(s string) (Variant, error) {
	for _, v := range AllVariants {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("--variant must be traefik, traefik-external, npm or full (got %q)", bounded(s))
}

// External reports whether the variant sits behind the operator's own
// reverse proxy on the bekup-proxy link network.
func (v Variant) External() bool { return v == VariantTraefikExternal || v == VariantNPM }

// AssetName is the release asset name of a variant.
func (v Variant) AssetName() string {
	switch v {
	case VariantTraefik:
		return "compose.yaml"
	default:
		return "compose." + string(v) + ".yaml"
	}
}

// assetDirs returns the embedded deploy directories the variant mounts.
func (v Variant) assetDirs() []string {
	switch v {
	case VariantTraefik:
		return []string{"deploy/postgres", "deploy/traefik"}
	case VariantTraefikExternal, VariantNPM:
		return []string{"deploy/postgres"}
	default:
		return assetDirs
	}
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

// proxyBanner marks the comment block that introduces the reverse-proxy
// services in the repository compose file.
const proxyBanner = "Reverse proxy (task 0.7b)"

// externalEdits turn the repository compose file into a variant behind
// the operator's own proxy: no bundled proxy, no frontend/edge network,
// serve on the external link network.
var externalEdits = []edit{
	replaceComment(proxyBanner, ""),
	remove("services/traefik-init"),
	remove("services/traefik"),
	remove("services/nginx-init"),
	remove("services/nginx"),
	remove("services/certbot"),
	replace("services/serve/networks/frontend", "external/serve-network.yaml"),
	replaceLead("networks", "external/networks-lead.yaml"),
	replace("networks/frontend", "external/network.yaml"),
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
	VariantTraefikExternal: append([]edit{
		header("traefik-external/header.yaml"),
		insertAfter("services/serve/command", "traefik-external/labels.yaml"),
	}, externalEdits...),
	VariantNPM: append([]edit{
		header("npm/header.yaml"),
	}, externalEdits...),
	VariantFull: {
		header("full/header.yaml"),
	},
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
// requires it to be digest-pinned and writes it to .env.
const ComposeImagePlaceholder = "${BEKUP_IMAGE:?set BEKUP_IMAGE to the digest-pinned bekup image repo:tag@sha256:digest, see the README}"

// composeImagePrefix starts the single line of a compose file that names
// the bekup image. Release and setup replace exactly that line.
const composeImagePrefix = "x-bekup-image: &bekup-image "

// ComposeImageLine is the image line of the repository compose file.
const ComposeImageLine = composeImagePrefix + ComposeImageVar

// ComposePlaceholderLine is the image line of the committed compose files
// (deploy/compose/*, `bekup setup compose` without --image).
const ComposePlaceholderLine = composeImagePrefix + ComposeImagePlaceholder

var imageLineAnyRe = regexp.MustCompile(`(?m)^x-bekup-image:`)

// applyEdits renders the overlay of v onto src.
func applyEdits(src []byte, v Variant) (string, error) {
	edits, ok := variantEdits[v]
	if !ok {
		return "", fmt.Errorf("unknown variant %q", bounded(string(v)))
	}
	o, err := newOutline(string(src))
	if err != nil {
		return "", err
	}
	for _, e := range edits {
		frag := ""
		if e.fragment != "" {
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
		}
		if err != nil {
			return "", fmt.Errorf("variant %s: %w", v, err)
		}
	}
	return o.String(), nil
}

// RenderVariant renders a variant from the repository compose file src.
// With img set, the single x-bekup-image line becomes the digest-pinned
// reference as a literal (a shell variable can never override it; the
// release assets); without, it becomes ComposePlaceholderLine (the
// committed files in deploy/compose/: the image comes from BEKUP_IMAGE,
// which setup requires to be pinned and writes to .env). It refuses a
// source with any other ${BEKUP_IMAGE use, an image line that is not
// exactly the expected one, or a build: key.
func RenderVariant(src []byte, v Variant, img *imageref.Ref) ([]byte, error) {
	out, err := applyEdits(src, v)
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
	if strings.Count(out, "${BEKUP_IMAGE") != 1 {
		return nil, errors.New("compose source: a further ${BEKUP_IMAGE...} reference exists")
	}
	if img != nil {
		if _, err := imageref.ParsePinned(img.String()); err != nil {
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
