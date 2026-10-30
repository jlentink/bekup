package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/jlentink/bekup/internal/imageref"
)

// StateSchema is the version of bekup-setup.json.
const StateSchema = 1

// maxStateBytes bounds bekup-setup.json.
const maxStateBytes = 1 << 20

// ComposeMode records who owns compose.yaml (D-205).
type ComposeMode string

// Compose modes.
const (
	// ComposeOperator: compose-first. The operator downloads compose.yaml
	// (a release asset) and replaces it to upgrade; setup never writes it.
	ComposeOperator ComposeMode = "operator"
	// ComposeGenerated: setup-first (--write-compose). setup writes and
	// upgrades compose.yaml.
	ComposeGenerated ComposeMode = "generated"
)

// ImageSource records where compose.yaml takes the bekup image from
// (compose-first, D-205 amendment 3).
type ImageSource string

// Image sources.
const (
	// ImageSourceCompose (empty): the x-bekup-image line pins it (a
	// release file).
	ImageSourceCompose ImageSource = ""
	// ImageSourceEnv: the committed file from deploy/compose/; the image
	// comes from BEKUP_IMAGE, which setup keeps in .env.
	ImageSourceEnv ImageSource = "env"
)

// State is bekup-setup.json: what setup generated, for upgrade and check.
// It holds no secrets (paths, versions and SHA-256 hashes of public files).
type State struct {
	Schema          int           `json:"schema"`
	ManifestVersion int           `json:"manifest_version"`
	BekupVersion    string        `json:"bekup_version"`
	Image           string        `json:"image"`
	ComposeMode     ComposeMode   `json:"compose_mode"`
	Variant         Variant       `json:"variant"`
	Proxy           Proxy         `json:"proxy"`
	TLSMode         TLSMode       `json:"tls_mode,omitempty"`
	KeysDir         string        `json:"keys_dir"`
	Ownership       OwnershipMode `json:"ownership"`
	// The bekup-proxy link network (external variants only).
	ProxyNetwork string `json:"proxy_network,omitempty"`
	ProxySubnet  string `json:"proxy_subnet,omitempty"`
	ProxyIP      string `json:"proxy_ip,omitempty"`
	ServeIP      string `json:"serve_ip,omitempty"`
	// ComposeSHA256 is the SHA-256 (hex) of the operator's compose.yaml at
	// the last init/upgrade (compose-first only).
	ComposeSHA256 string `json:"compose_sha256,omitempty"`
	// ImageSource is "env" when compose.yaml takes the image from
	// BEKUP_IMAGE in .env (compose-first only).
	ImageSource ImageSource `json:"image_source,omitempty"`
	// Assets maps every generated-upgradable file (deploy/**, reference/*
	// and, setup-first, compose.yaml) to the SHA-256 (hex) of the content
	// setup wrote.
	Assets map[string]string `json:"assets"`
	// SecretGroups lists the secret groups that existed when setup last
	// ran; upgrade generates only groups that are new in the manifest.
	SecretGroups []string `json:"secret_groups"`
}

var (
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	hashRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Marshal encodes the state as indented JSON with sorted keys.
func (s *State) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ParseState decodes and validates bekup-setup.json (unknown fields are
// refused).
func ParseState(data []byte) (*State, error) {
	if len(data) > maxStateBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", StateFileName, maxStateBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s State
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", StateFileName, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: trailing data", StateFileName)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", StateFileName, err)
	}
	return &s, nil
}

func (s *State) validate() error {
	if s.Schema != StateSchema {
		return fmt.Errorf("unsupported schema %d (this bekup understands %d)", s.Schema, StateSchema)
	}
	if s.ManifestVersion < 1 {
		return errors.New("invalid manifest_version")
	}
	if !versionRe.MatchString(s.BekupVersion) {
		return fmt.Errorf("invalid bekup_version %q", bounded(s.BekupVersion))
	}
	if _, err := imageref.ParsePinned(s.Image); err != nil {
		return err
	}
	if _, err := ParseVariant(string(s.Variant)); err != nil {
		return fmt.Errorf("invalid variant %q", bounded(string(s.Variant)))
	}
	switch s.ComposeMode {
	case ComposeOperator:
		if s.Variant == VariantFull {
			return errors.New("variant full needs compose_mode generated")
		}
		if !hashRe.MatchString(s.ComposeSHA256) {
			return errors.New("invalid compose_sha256")
		}
	case ComposeGenerated:
		if s.ComposeSHA256 != "" {
			return errors.New("compose_sha256 applies to compose_mode operator only")
		}
		if s.ImageSource != ImageSourceCompose {
			return errors.New("image_source applies to compose_mode operator only")
		}
	default:
		return fmt.Errorf("invalid compose_mode %q", bounded(string(s.ComposeMode)))
	}
	switch s.ImageSource {
	case ImageSourceCompose, ImageSourceEnv:
	default:
		return fmt.Errorf("invalid image_source %q", bounded(string(s.ImageSource)))
	}
	switch s.Proxy {
	case ProxyTraefik, ProxyNginx, ProxyNone, ProxyExternal:
	default:
		return fmt.Errorf("invalid proxy %q", bounded(string(s.Proxy)))
	}
	if want := s.Variant.proxy(); want != "" && s.Proxy != want || want == "" && s.Proxy == ProxyExternal {
		return fmt.Errorf("proxy %q does not fit variant %q", bounded(string(s.Proxy)), s.Variant)
	}
	link := s.link()
	if s.Variant.External() {
		if err := validateLink(link, Network{}); err != nil {
			return err
		}
	} else if link != (LinkNetwork{}) {
		return errors.New("the proxy_* link network fields apply to the external variants only")
	}
	switch s.TLSMode {
	case "", TLSACME, TLSStatic:
	default:
		return fmt.Errorf("invalid tls_mode %q", bounded(string(s.TLSMode)))
	}
	if err := ValidateKeysDir(s.KeysDir); err != nil {
		return err
	}
	if _, err := ParseOwnershipMode(string(s.Ownership)); err != nil || s.Ownership == OwnershipAuto {
		return fmt.Errorf("invalid ownership %q", bounded(string(s.Ownership)))
	}
	if len(s.Assets) == 0 || len(s.Assets) > 10000 {
		return errors.New("assets missing or too many")
	}
	for p, h := range s.Assets {
		if !isAssetPath(p) || !hashRe.MatchString(h) {
			return fmt.Errorf("invalid asset entry %q", bounded(p))
		}
	}
	if _, ok := s.Assets[ComposeFile]; ok != (s.ComposeMode == ComposeGenerated) {
		return fmt.Errorf("assets must list %s exactly with compose_mode generated", ComposeFile)
	}
	for _, g := range s.SecretGroups {
		switch g {
		case groupMasterKey, groupDatabase, groupCerts:
		default:
			return fmt.Errorf("unknown secret group %q", bounded(g))
		}
	}
	return nil
}

// assetPaths returns the asset paths in sorted order.
func (s *State) assetPaths() []string {
	out := make([]string, 0, len(s.Assets))
	for p := range s.Assets {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// staticTLS reports whether the deployment has the static-TLS override.
func (s *State) staticTLS() bool {
	return (s.Proxy == ProxyTraefik || s.Proxy == ProxyNginx) && s.TLSMode == TLSStatic
}

// link returns the recorded link network.
func (s *State) link() LinkNetwork {
	return LinkNetwork{Name: s.ProxyNetwork, Subnet: s.ProxySubnet, ProxyIP: s.ProxyIP, ServeIP: s.ServeIP}
}

// manifestOptions returns the manifest options of the deployment.
func (s *State) manifestOptions() ManifestOptions {
	return ManifestOptions{StaticTLS: s.staticTLS(), ComposeOperator: s.ComposeMode == ComposeOperator}
}

// readState reads bekup-setup.json below root.
func readState(root string) (*State, error) {
	fi, err := checkNoSymlinks(root, StateFileName)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return nil, fmt.Errorf("%s not found: this directory was not created by `bekup setup init` (adopting an existing install is not supported yet)", StateFileName)
	}
	abs, err := resolve(root, StateFileName)
	if err != nil {
		return nil, err
	}
	data, err := readRegular(abs, maxStateBytes)
	if err != nil {
		return nil, err
	}
	return ParseState(data)
}

// secretGroups returns the secret groups of a manifest, sorted.
func secretGroups(entries []Entry) []string {
	set := map[string]bool{}
	for _, e := range entries {
		if e.Group != "" {
			set[e.Group] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}
