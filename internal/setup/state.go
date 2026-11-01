package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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

// ImageMode records how the bekup image is referenced (D-212).
type ImageMode string

// Image modes.
const (
	// ImageModePinned (empty): the image is pinned by digest.
	ImageModePinned ImageMode = ""
	// ImageModeFollowing: the image follows a tag of the official
	// repository (ghcr.io/jlentink/bekup:latest): an explicit weakening.
	ImageModeFollowing ImageMode = "following"
)

// imageModeOf is the mode of a bekup image reference.
func imageModeOf(img imageref.Ref) ImageMode {
	if img.IsMutable() {
		return ImageModeFollowing
	}
	return ImageModePinned
}

// State is bekup-setup.json: what setup generated, for upgrade and check.
// It holds no secrets (paths, versions and SHA-256 hashes of public files).
type State struct {
	Schema          int    `json:"schema"`
	ManifestVersion int    `json:"manifest_version"`
	BekupVersion    string `json:"bekup_version"`
	Image           string `json:"image"`
	// ImageMode is "following" when Image follows a tag (D-212); empty
	// for a digest-pinned image.
	ImageMode   ImageMode     `json:"image_mode,omitempty"`
	ComposeMode ComposeMode   `json:"compose_mode"`
	Variant     Variant       `json:"variant"`
	Proxy       Proxy         `json:"proxy"`
	TLSMode     TLSMode       `json:"tls_mode,omitempty"`
	KeysDir     string        `json:"keys_dir"`
	Ownership   OwnershipMode `json:"ownership"`
	// ProxyNetworkMode is how serve reaches the operator's own proxy
	// (variant own-proxy: join or link, D-215).
	ProxyNetworkMode ProxyNetworkMode `json:"proxy_network_mode,omitempty"`
	// ProxyNetwork is the operator's proxy network (join mode) or the
	// bekup-proxy link network (link mode).
	ProxyNetwork string `json:"proxy_network,omitempty"`
	// ProxyContainer is the proxy's container name, the one entry of
	// server.trusted_proxy_hosts (join mode).
	ProxyContainer string `json:"proxy_container,omitempty"`
	// ProxyLabels: the Traefik labels overlay on serve (own-proxy).
	ProxyLabels bool `json:"proxy_labels,omitempty"`
	// The link network's subnet and fixed addresses (link mode only).
	ProxySubnet string `json:"proxy_subnet,omitempty"`
	ProxyIP     string `json:"proxy_ip,omitempty"`
	ServeIP     string `json:"serve_ip,omitempty"`
	// ComposeSHA256 is the SHA-256 (hex) of the operator's compose.yaml at
	// the last init/upgrade (compose-first only).
	ComposeSHA256 string `json:"compose_sha256,omitempty"`
	// ImageSource is "env" when compose.yaml takes the image from
	// BEKUP_IMAGE in .env (compose-first only).
	ImageSource ImageSource `json:"image_source,omitempty"`
	// DatabaseMode is "external" for the operator's PostgreSQL (D-208);
	// empty for the bundled postgres service.
	DatabaseMode DBMode `json:"database_mode,omitempty"`
	// DatabaseAuth records how the external database's roles were set up
	// (bootstrap, existing-roles, dba-sql, single-account).
	DatabaseAuth DBAuth `json:"database_auth,omitempty"`
	// DatabaseLocation (external only): remote, or docker for a database
	// container on this host whose network DBNetwork migrate, serve and
	// admin join (D-215). Empty in older states means remote.
	DatabaseLocation DBLocation `json:"database_location,omitempty"`
	DBNetwork        string     `json:"db_network,omitempty"`
	// DatabaseTLS (external only) is the TLS choice setup wrote (empty in
	// older states: verify-ca-file).
	DatabaseTLS DBTLS `json:"database_tls,omitempty"`
	// ImportedFiles are the operator's secret source files that setup
	// copied below the keys directory (--db-password-file,
	// --db-owner-password-file, --import-master-key), relative to the
	// deployment directory; setup check warns while they still exist.
	ImportedFiles []string `json:"imported_files,omitempty"`
	// Assets maps every generated-upgradable file (deploy/**, reference/*
	// and, setup-first, compose.yaml) to the SHA-256 (hex) of the content
	// setup wrote.
	Assets map[string]string `json:"assets"`
	// SecretGroups lists the secret groups that existed when setup last
	// ran; upgrade generates only groups that are new in the manifest.
	SecretGroups []string `json:"secret_groups"`

	// legacy is the retired variant (traefik-external, npm) a state of an
	// earlier version recorded; ParseState maps it to own-proxy in link
	// mode (D-215) and upgrade rewrites the state.
	legacy Variant
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
	s.mapLegacy()
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", StateFileName, err)
	}
	return &s, nil
}

// mapLegacy maps a retired variant (D-215): traefik-external and npm ran
// serve on the bekup-proxy link network, which is the own-proxy variant's
// link mode now (traefik-external with the Traefik labels).
func (s *State) mapLegacy() {
	if s.Variant != legacyVariantTraefikExternal && s.Variant != legacyVariantNPM {
		return
	}
	s.legacy = s.Variant
	s.ProxyLabels = s.Variant == legacyVariantTraefikExternal
	s.Variant, s.ProxyNetworkMode = VariantOwnProxy, ProxyLink
}

// Legacy returns the retired variant this state was recorded with (""
// for a current one).
func (s *State) Legacy() Variant { return s.legacy }

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
	if _, err := s.image(); err != nil {
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
	if err := s.validateOwnProxy(); err != nil {
		return err
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
	switch s.DatabaseMode {
	case "":
		if s.DatabaseAuth != "" || s.DatabaseLocation != "" || s.DBNetwork != "" || s.DatabaseTLS != "" {
			return errors.New("database_auth, database_location, db_network and database_tls apply to database_mode external only")
		}
	case DBExternal:
		if _, err := ParseDBAuth(string(s.DatabaseAuth)); err != nil {
			return fmt.Errorf("invalid database_auth %q", bounded(string(s.DatabaseAuth)))
		}
		switch s.DatabaseLocation {
		case "", DBRemote:
			if s.DBNetwork != "" {
				return errors.New("db_network applies to database_location docker only")
			}
		case DBDocker:
			if err := ValidateDockerNetwork("db_network", s.DBNetwork); err != nil {
				return err
			}
			// The proxy's network (join) or the link network (link mode):
			// serve joins a network only once, and a proxy network should not
			// reach databases (D-215).
			if s.ProxyNetwork != "" && strings.EqualFold(s.DBNetwork, s.ProxyNetwork) {
				return errors.New("db_network must not be the proxy network")
			}
		default:
			return fmt.Errorf("invalid database_location %q", bounded(string(s.DatabaseLocation)))
		}
		if s.DatabaseTLS != "" {
			if _, err := ParseDBTLS(string(s.DatabaseTLS)); err != nil {
				return fmt.Errorf("invalid database_tls %q", bounded(string(s.DatabaseTLS)))
			}
		}
	default:
		return fmt.Errorf("invalid database_mode %q (empty for the bundled database, or external)", bounded(string(s.DatabaseMode)))
	}
	if len(s.ImportedFiles) > maxImportedFiles {
		return errors.New("too many imported_files")
	}
	seen := map[string]bool{}
	for _, f := range s.ImportedFiles {
		if checkSourcePath("imported_files", f) != nil || seen[f] {
			return fmt.Errorf("invalid imported_files entry %q", bounded(f))
		}
		seen[f] = true
	}
	for _, g := range s.SecretGroups {
		switch g {
		case groupMasterKey, groupDatabase, groupDatabaseExternal, groupCerts:
		default:
			return fmt.Errorf("unknown secret group %q", bounded(g))
		}
	}
	return nil
}

// image parses the recorded image: digest-pinned, or (image_mode
// following) a tag of the official repository.
func (s *State) image() (imageref.Ref, error) {
	switch s.ImageMode {
	case ImageModePinned:
		return imageref.ParsePinned(s.Image)
	case ImageModeFollowing:
		return imageref.ParseFollowing(s.Image)
	}
	return imageref.Ref{}, fmt.Errorf("invalid image_mode %q", bounded(string(s.ImageMode)))
}

// following reports image_mode following.
func (s *State) following() bool { return s.ImageMode == ImageModeFollowing }

// maxImportedFiles bounds State.ImportedFiles (two password files and a
// keyring).
const maxImportedFiles = 3

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

// link returns the recorded link network (link mode only).
func (s *State) link() LinkNetwork {
	if s.ProxyNetworkMode != ProxyLink {
		return LinkNetwork{}
	}
	return LinkNetwork{Name: s.ProxyNetwork, Subnet: s.ProxySubnet, ProxyIP: s.ProxyIP, ServeIP: s.ServeIP}
}

// ownProxyJoin reports the own-proxy variant in join mode.
func (s *State) ownProxyJoin() bool {
	return s.Variant.OwnProxy() && s.ProxyNetworkMode == ProxyJoin
}

// overlays are the recorded setup-only compose overlays (D-215).
func (s *State) overlays() Overlays {
	return Overlays{
		ProxyLabels: s.Variant.OwnProxy() && s.ProxyLabels,
		ProxyLink:   s.Variant.OwnProxy() && s.ProxyNetworkMode == ProxyLink,
		DBNetwork:   s.DatabaseMode == DBExternal && s.DatabaseLocation == DBDocker,
	}
}

// dbTLS is the recorded database TLS choice (verify-ca-file for older
// states).
func (s *State) dbTLS() DBTLS { return s.DatabaseTLS.OrDefault() }

// validateOwnProxy checks the own-proxy fields of the state.
func (s *State) validateOwnProxy() error {
	if !s.Variant.OwnProxy() {
		if s.ProxyNetworkMode != "" || s.ProxyNetwork != "" || s.ProxyContainer != "" || s.ProxyLabels ||
			s.ProxySubnet != "" || s.ProxyIP != "" || s.ServeIP != "" {
			return errors.New("the proxy_* fields apply to the own-proxy variant only")
		}
		return nil
	}
	switch s.ProxyNetworkMode {
	case ProxyJoin:
		if s.ProxySubnet != "" || s.ProxyIP != "" || s.ServeIP != "" {
			return errors.New("proxy_subnet, proxy_ip and serve_ip apply to proxy_network_mode link only")
		}
		if err := ValidateDockerNetwork("proxy_network", s.ProxyNetwork); err != nil {
			return err
		}
		return ValidateContainerName("proxy_container", s.ProxyContainer)
	case ProxyLink:
		if s.ProxyContainer != "" {
			return errors.New("proxy_container applies to proxy_network_mode join only")
		}
		return validateLink(s.link(), Network{})
	}
	return fmt.Errorf("invalid proxy_network_mode %q (join or link)", bounded(string(s.ProxyNetworkMode)))
}

// manifestOptions returns the manifest options of the deployment.
func (s *State) manifestOptions() ManifestOptions {
	return ManifestOptions{StaticTLS: s.staticTLS(), Overlays: s.overlays().Any(), ComposeOperator: s.ComposeMode == ComposeOperator, ExternalDB: s.DatabaseMode == DBExternal}
}

// dbMode returns the recorded database mode (DBBundled for an empty one).
func (s *State) dbMode() DBMode { return s.DatabaseMode.OrBundled() }

// stateDBMode is the recorded form of a database mode: empty for the
// bundled database, so states of bundled deployments stay unchanged.
func stateDBMode(d DBMode) DBMode {
	if d == DBExternal {
		return DBExternal
	}
	return ""
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

// ReadState reads and validates bekup-setup.json of the deployment
// directory dir.
func ReadState(dir string) (*State, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return readState(root)
}
