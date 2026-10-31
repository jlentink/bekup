package setup

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/imageref"
)

// Proxy is the reverse-proxy choice (compose profile).
type Proxy string

// Reverse-proxy choices.
const (
	ProxyTraefik Proxy = "traefik"
	ProxyNginx   Proxy = "nginx"
	// ProxyNone runs no bundled proxy: the operator's own proxy attaches to
	// the frontend network and is named with TrustedProxies (variant full).
	ProxyNone Proxy = "none"
	// ProxyExternal is the operator's Traefik or Nginx Proxy Manager on the
	// bekup-proxy link network (variants traefik-external and npm, D-205).
	ProxyExternal Proxy = "external"
)

// TLSMode is the proxy's certificate source (PROXY_TLS_MODE).
type TLSMode string

// Proxy TLS modes.
const (
	TLSACME   TLSMode = "acme"
	TLSStatic TLSMode = "static"
)

// ACME directories. The staging directory is used with ACMEStaging.
const (
	ACMEProduction = "https://acme-v02.api.letsencrypt.org/directory"
	ACMEStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// DefaultKeysDir is the default secrets directory (relative to the
// deployment directory), the compose default of BEKUP_KEYS_DIR.
const DefaultKeysDir = "secrets"

// Bounds of list answers.
const (
	maxListItems = 64
	maxGID       = 1<<31 - 1
)

// Network holds the compose network addresses (compose variables
// FRONTEND_SUBNET, SERVE_FRONTEND_IP, ...). The zero value means the
// compose defaults.
type Network struct {
	FrontendSubnet  string
	ServeFrontendIP string
	ProxyFrontendIP string
	RunnerCtlSubnet string
	RunnerCtlIP     string
	MetricsSubnet   string
	ServeMetricsIP  string
}

// DefaultNetwork returns the compose defaults.
func DefaultNetwork() Network {
	return Network{
		FrontendSubnet:  "10.213.250.0/24",
		ServeFrontendIP: "10.213.250.10",
		ProxyFrontendIP: "10.213.250.5",
		RunnerCtlSubnet: "10.213.251.0/24",
		RunnerCtlIP:     "10.213.251.10",
		MetricsSubnet:   "10.213.252.0/24",
		ServeMetricsIP:  "10.213.252.10",
	}
}

// networkVars maps the compose variable names to the fields, in .env order.
func (n *Network) vars() []struct {
	name string
	val  *string
} {
	return []struct {
		name string
		val  *string
	}{
		{"FRONTEND_SUBNET", &n.FrontendSubnet},
		{"SERVE_FRONTEND_IP", &n.ServeFrontendIP},
		{"PROXY_FRONTEND_IP", &n.ProxyFrontendIP},
		{"RUNNER_CTL_SUBNET", &n.RunnerCtlSubnet},
		{"RUNNER_CTL_IP", &n.RunnerCtlIP},
		{"METRICS_SUBNET", &n.MetricsSubnet},
		{"SERVE_METRICS_IP", &n.ServeMetricsIP},
	}
}

// LinkNetwork is the bekup-proxy link network of the external-proxy
// variants (D-205): an external Docker network the operator creates once
// (CreateCommand: internal, the subnet and an --ip-range without the
// fixed addresses), joined by the operator's proxy at ProxyIP and by serve
// at ServeIP.
type LinkNetwork struct {
	Name    string
	Subnet  string
	ProxyIP string
	ServeIP string
}

// CreateCommand is the `docker network create` command for the link
// network: internal, with the subnet, its first host as the gateway and
// an --ip-range that excludes ProxyIP and ServeIP, so Docker never hands
// the fixed addresses to another container that joins first. l must be
// valid (validateLink).
func (l LinkNetwork) CreateCommand() string {
	cmd := "docker network create --internal --subnet " + l.Subnet
	p, err := netip.ParsePrefix(l.Subnet)
	if err != nil || !p.Addr().Is4() {
		return cmd + " " + l.Name
	}
	p = p.Masked()
	cmd += " --gateway " + p.Addr().Next().String()
	if r, ok := linkIPRange(p, l.ProxyIP, l.ServeIP); ok {
		cmd += " --ip-range " + r.String()
	}
	return cmd + " " + l.Name
}

// linkIPRange returns the largest aligned block of p (at most /30, the
// highest first) that contains none of the fixed addresses.
func linkIPRange(p netip.Prefix, fixed ...string) (netip.Prefix, bool) {
	var addrs []netip.Addr
	for _, s := range fixed {
		if a, err := netip.ParseAddr(s); err == nil {
			addrs = append(addrs, a)
		}
	}
	base := p.Addr().As4()
	start := binary.BigEndian.Uint32(base[:])
	for bits := p.Bits() + 1; bits <= 30; bits++ {
		size := uint32(1) << uint32(32-bits)    // #nosec G115 -- bits is 9..30
		n := uint32(1) << uint32(bits-p.Bits()) // #nosec G115 -- bits > p.Bits()
		for i := n; i > 0; i-- {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], start+(i-1)*size)
			blk := netip.PrefixFrom(netip.AddrFrom4(b), bits)
			if !slices.ContainsFunc(addrs, blk.Contains) {
				return blk, true
			}
		}
	}
	return netip.Prefix{}, false
}

// DefaultLinkNetwork returns the defaults of the external variants.
func DefaultLinkNetwork() LinkNetwork {
	return LinkNetwork{Name: "bekup-proxy", Subnet: "10.213.253.0/28", ProxyIP: "10.213.253.5", ServeIP: "10.213.253.10"}
}

func (l LinkNetwork) withDefaults() LinkNetwork {
	d := DefaultLinkNetwork()
	if l.Name == "" {
		l.Name = d.Name
	}
	if l.Subnet == "" {
		l.Subnet = d.Subnet
	}
	if l.ProxyIP == "" {
		l.ProxyIP = d.ProxyIP
	}
	if l.ServeIP == "" {
		l.ServeIP = d.ServeIP
	}
	return l
}

// Defaults of the Traefik labels (variant traefik-external).
const (
	DefaultTraefikEntrypoint   = "websecure"
	DefaultTraefikCertResolver = "letsencrypt"
)

// withDefaults fills empty fields with the compose defaults.
func (n Network) withDefaults() Network {
	d := DefaultNetwork()
	dv := d.vars()
	for i, v := range n.vars() {
		if *v.val == "" {
			*v.val = *dv[i].val
		}
	}
	return n
}

// Answers are the validated inputs of `bekup setup init`. Every string is
// restricted to a strict character set (no CR/LF, `$`, quotes, `#`,
// backslashes or spaces), so nothing can be injected into .env, TOML or
// YAML.
type Answers struct {
	Image imageref.Ref
	// Variant is the compose variant (default full); it decides Proxy for
	// every variant but full.
	Variant        Variant
	Proxy          Proxy
	Domain         string
	TLSMode        TLSMode // proxy traefik/nginx only
	ACMEEmail      string  // TLS mode acme only
	ACMEStaging    bool
	DockerGID      int
	TargetCIDRs    []string
	TargetHosts    []string
	TrustedProxies []string // proxy none only (CIDRs of the operator's proxy)
	KeysDir        string   // relative to the deployment directory
	Network        Network
	// Link is the bekup-proxy link network (external variants only).
	Link LinkNetwork
	// TraefikEntrypoint and TraefikCertResolver name the operator's
	// Traefik entrypoint and certificate resolver (traefik-external only).
	TraefikEntrypoint   string
	TraefikCertResolver string
	// ImageInEnv: compose.yaml takes the image from BEKUP_IMAGE (the
	// committed file from deploy/compose/, D-205 amendment 3); .env gets
	// BEKUP_IMAGE=<Image>. Set by Init from the compose file.
	ImageInEnv bool
	// DB is the app database: the bundled postgres service (zero value) or
	// the operator's PostgreSQL (D-208).
	DB Database
}

var (
	// domainRe is the rule of deploy/traefik/entrypoint.sh and
	// deploy/nginx/entrypoint.sh: DNS labels, at least one dot.
	domainRe = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	// emailRe is the ACME_EMAIL rule of the proxy entrypoints.
	emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\.)+[A-Za-z0-9-]+$`)
	// keysDirRe: relative path of plain segments.
	keysDirRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9_][A-Za-z0-9._-]{0,63}){0,7}$`)
	// prefixRe / ipRe: the characters of IPv4/IPv6 prefixes and addresses
	// (netip does the real parsing; this only bounds the alphabet).
	prefixRe = regexp.MustCompile(`^[0-9A-Fa-f.:]{2,45}/\d{1,3}$`)
	ipRe     = regexp.MustCompile(`^[0-9.]{7,15}$`)
	// hostRe bounds the alphabet of a target host name.
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	// networkNameRe is Docker's network name rule.
	networkNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	// traefikNameRe bounds Traefik entrypoint and resolver names.
	traefikNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
)

// reservedTopLevel are deployment-directory names the keys directory must
// not start with.
var reservedTopLevel = []string{"deploy", "reference", ComposeFile, OverrideFile, "compose.yml", "docker-compose.yml", "docker-compose.yaml", ".env", "bekup.toml", "runner.toml", StateFileName, "proxy-tls"}

// FieldError names the answer that failed validation.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// ValidateDomain checks a public host name with the proxy entrypoints' rule.
func ValidateDomain(s string) error {
	if len(s) > 253 || !domainRe.MatchString(s) {
		return fieldErr("domain", "must be a DNS host name such as backup.example.com")
	}
	return nil
}

// ValidateEmail checks an ACME account e-mail with the proxies' rule.
func ValidateEmail(s string) error {
	if len(s) > 254 || !emailRe.MatchString(s) {
		return fieldErr("acme-email", "must be an e-mail address such as ops@example.com")
	}
	return nil
}

// ValidateKeysDir checks the secrets directory: a clean relative path
// inside the deployment directory that does not collide with generated
// files.
func ValidateKeysDir(s string) error {
	if !keysDirRe.MatchString(s) || path.Clean(s) != s {
		return fieldErr("keys-dir", "must be a clean relative path inside the deployment directory (letters, digits, . _ -), e.g. secrets")
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || seg == ".." {
			return fieldErr("keys-dir", "must not contain . or .. segments")
		}
	}
	first := strings.SplitN(s, "/", 2)[0]
	for _, r := range reservedTopLevel {
		if first == r {
			return fieldErr("keys-dir", "%q is reserved for generated files", first)
		}
	}
	return nil
}

func parsePrefix(field, s string) (netip.Prefix, error) {
	if !prefixRe.MatchString(s) {
		return netip.Prefix{}, fieldErr(field, "%q is not a CIDR such as 10.20.0.0/16", bounded(s))
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fieldErr(field, "%q is not a CIDR such as 10.20.0.0/16", bounded(s))
	}
	if p.Masked() != p {
		return netip.Prefix{}, fieldErr(field, "%q has host bits set (use %s)", s, p.Masked())
	}
	return p, nil
}

// Width bounds of the CIDR answers (target-cidr, trusted-proxy): a prefix
// shorter than the minimum is refused (it would trust or reach most of the
// address space), one shorter than the warning bound is reported.
const (
	minBitsV4  = 8
	minBitsV6  = 32
	warnBitsV4 = 16
	warnBitsV6 = 48
)

// checkScopedPrefix validates a CIDR answer and refuses one wider than /8
// (IPv4) or /32 (IPv6), above all 0.0.0.0/0 and ::/0.
func checkScopedPrefix(field, s string) error {
	p, err := parsePrefix(field, s)
	if err != nil {
		return err
	}
	if minBits := prefixBounds(p, minBitsV4, minBitsV6); p.Bits() < minBits {
		return fieldErr(field, "%s is too wide (at most /%d for %s): name the networks you mean", p, minBits, family(p))
	}
	return nil
}

// isWidePrefix reports a prefix wider than the warning bound.
func isWidePrefix(p netip.Prefix) bool {
	return p.Bits() < prefixBounds(p, warnBitsV4, warnBitsV6)
}

func prefixBounds(p netip.Prefix, v4, v6 int) int {
	if p.Addr().Is4() {
		return v4
	}
	return v6
}

func family(p netip.Prefix) string {
	if p.Addr().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// wideCIDRWarnings names the very wide (but allowed) CIDR answers.
func wideCIDRWarnings(a *Answers) []string {
	var out []string
	for _, l := range []struct {
		field string
		items []string
	}{{"target-cidr", a.TargetCIDRs}, {"trusted-proxy", a.TrustedProxies}} {
		for _, c := range l.items {
			if p, err := netip.ParsePrefix(c); err == nil && isWidePrefix(p) {
				out = append(out, fmt.Sprintf("--%s %s is very wide (shorter than /%d): allow only the networks you need", l.field, p, prefixBounds(p, warnBitsV4, warnBitsV6)))
			}
		}
	}
	return out
}

// ValidateNetworkName checks a Docker network name (--proxy-network).
func ValidateNetworkName(s string) error {
	if !networkNameRe.MatchString(s) {
		return fieldErr("proxy-network", "must be a Docker network name (letters, digits, _ . -; at most 63 characters)")
	}
	return nil
}

// ValidateTraefikName checks a Traefik entrypoint or certificate resolver
// name (field names the flag).
func ValidateTraefikName(field, s string) error {
	if !traefikNameRe.MatchString(s) {
		return fieldErr(field, "must be a Traefik name (letters, digits, _ -; at most 63 characters)")
	}
	return nil
}

// checkSubnet validates a bekup network subnet: private IPv4 between /8
// and /29, overlapping none of others.
func checkSubnet(field, s string, others []netip.Prefix) (netip.Prefix, error) {
	p, err := parsePrefix(field, s)
	if err != nil {
		return p, err
	}
	if !p.Addr().Is4() || p.Bits() < 8 || p.Bits() > 29 {
		return p, fieldErr(field, "must be an IPv4 subnet between /8 and /29")
	}
	if !p.Addr().IsPrivate() {
		return p, fieldErr(field, "must be a private (RFC 1918) subnet")
	}
	for _, q := range others {
		if q.Overlaps(p) {
			return p, fieldErr(field, "overlaps another bekup network (%s)", q)
		}
	}
	return p, nil
}

// checkHostIP validates a fixed container address inside p: never the
// network, gateway (Docker's default: the first address) or broadcast
// address.
func checkHostIP(field, ip string, p netip.Prefix) error {
	if !ipRe.MatchString(ip) {
		return fieldErr(field, "%q is not an IPv4 address", bounded(ip))
	}
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return fieldErr(field, "%q is not an IPv4 address", bounded(ip))
	}
	if !p.Contains(a) {
		return fieldErr(field, "%s is not inside %s", a, p)
	}
	if a == p.Addr() || a == lastAddr(p) || a == p.Addr().Next() {
		return fieldErr(field, "%s is the network, gateway or broadcast address of %s", a, p)
	}
	return nil
}

// validateLink checks the bekup-proxy link network: a valid name, a
// private subnet between /8 and /29 that overlaps neither runner-ctl nor
// metrics, and two distinct host addresses inside it.
func validateLink(l LinkNetwork, n Network) error {
	if err := ValidateNetworkName(l.Name); err != nil {
		return err
	}
	var others []netip.Prefix
	for _, s := range []string{n.RunnerCtlSubnet, n.MetricsSubnet} {
		if p, err := netip.ParsePrefix(s); err == nil {
			others = append(others, p)
		}
	}
	p, err := checkSubnet("proxy-subnet", l.Subnet, others)
	if err != nil {
		return err
	}
	if err := checkHostIP("proxy-ip", l.ProxyIP, p); err != nil {
		return err
	}
	if err := checkHostIP("serve-ip", l.ServeIP, p); err != nil {
		return err
	}
	if l.ProxyIP == l.ServeIP {
		return fieldErr("proxy-ip", "must differ from serve-ip")
	}
	return nil
}

func validateNetwork(n Network) error {
	type sub struct {
		field  string
		prefix string
		ips    []struct{ field, ip string }
	}
	subs := []sub{
		{"frontend-subnet", n.FrontendSubnet, []struct{ field, ip string }{{"serve-ip", n.ServeFrontendIP}, {"proxy-ip", n.ProxyFrontendIP}}},
		{"runner-ctl-subnet", n.RunnerCtlSubnet, []struct{ field, ip string }{{"runner-ip", n.RunnerCtlIP}}},
		{"metrics-subnet", n.MetricsSubnet, []struct{ field, ip string }{{"metrics-ip", n.ServeMetricsIP}}},
	}
	var prefixes []netip.Prefix
	for _, s := range subs {
		p, err := checkSubnet(s.field, s.prefix, prefixes)
		if err != nil {
			return err
		}
		prefixes = append(prefixes, p)
		for _, ipf := range s.ips {
			if err := checkHostIP(ipf.field, ipf.ip, p); err != nil {
				return err
			}
		}
	}
	if n.ServeFrontendIP == n.ProxyFrontendIP {
		return fieldErr("proxy-ip", "must differ from serve-ip")
	}
	return nil
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	v := binary.BigEndian.Uint32(b[:])
	v |= ^uint32(0) >> uint32(p.Bits()) // #nosec G115 -- Bits() is 8..29 here
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}

func validateTargetHosts(hosts []string) error {
	for _, h := range hosts {
		if !hostRe.MatchString(h) {
			return fieldErr("target-host", "%q is not a host name", bounded(h))
		}
		if _, err := netip.ParseAddr(h); err == nil {
			return fieldErr("target-host", "%q is an IP address; use --target-cidr %s/32", h, h)
		}
		if err := hostname.Validate(h); err != nil {
			return fieldErr("target-host", "%q: %v", h, err)
		}
	}
	return nil
}

// Validate checks every answer and fills defaults (keys directory,
// network). It returns the first problem as a *FieldError.
func (a *Answers) Validate() error {
	if a.Image.Digest == "" {
		return fieldErr("image", "is required (a digest-pinned reference)")
	}
	if _, err := imageref.ParsePinned(a.Image.String()); err != nil {
		return fieldErr("image", "%v", err)
	}
	if a.ImageInEnv && !validEnvValue(a.Image.String()) {
		return fieldErr("image", "cannot be written to %s as a plain value", EnvFile)
	}
	if a.Variant == "" {
		a.Variant = VariantFull
	}
	if _, err := ParseVariant(string(a.Variant)); err != nil {
		return fieldErr("variant", "must be traefik, traefik-external, npm or full")
	}
	if want := a.Variant.proxy(); want != "" {
		if a.Proxy != "" && a.Proxy != want {
			return fieldErr("proxy", "--proxy applies to --variant full only (variant %s implies %s)", a.Variant, want)
		}
		a.Proxy = want
	}
	switch a.Proxy {
	case ProxyTraefik, ProxyNginx, ProxyNone:
	case ProxyExternal:
		if !a.Variant.External() {
			return fieldErr("proxy", "must be traefik, nginx or none")
		}
	default:
		return fieldErr("proxy", "must be traefik, nginx or none")
	}
	if err := ValidateDomain(a.Domain); err != nil {
		return err
	}
	if a.Variant == VariantTraefikExternal {
		if a.TraefikEntrypoint == "" {
			a.TraefikEntrypoint = DefaultTraefikEntrypoint
		}
		if a.TraefikCertResolver == "" {
			a.TraefikCertResolver = DefaultTraefikCertResolver
		}
		if err := ValidateTraefikName("traefik-entrypoint", a.TraefikEntrypoint); err != nil {
			return err
		}
		if err := ValidateTraefikName("traefik-certresolver", a.TraefikCertResolver); err != nil {
			return err
		}
	} else if a.TraefikEntrypoint != "" || a.TraefikCertResolver != "" {
		return fieldErr("traefik-entrypoint", "--traefik-entrypoint and --traefik-certresolver apply to --variant traefik-external only")
	}
	if !a.Variant.External() && a.Link != (LinkNetwork{}) {
		return fieldErr("proxy-network", "--proxy-network and --proxy-subnet apply to the traefik-external and npm variants only")
	}
	if def := DefaultNetwork(); a.Variant.External() && (a.Network.FrontendSubnet != "" && a.Network.FrontendSubnet != def.FrontendSubnet ||
		a.Network.ServeFrontendIP != "" && a.Network.ServeFrontendIP != def.ServeFrontendIP ||
		a.Network.ProxyFrontendIP != "" && a.Network.ProxyFrontendIP != def.ProxyFrontendIP) {
		return fieldErr("frontend-subnet", "the traefik-external and npm variants have no frontend network (use --proxy-subnet, --proxy-ip, --serve-ip)")
	}
	switch a.Proxy {
	case ProxyExternal:
		if a.TLSMode != "" || a.ACMEEmail != "" || a.ACMEStaging {
			return fieldErr("tls-mode", "--tls-mode, --acme-email and --acme-staging apply to the bundled proxy only (your proxy terminates TLS)")
		}
		if len(a.TrustedProxies) > 0 {
			return fieldErr("trusted-proxy", "applies to --proxy none only (the link network's proxy address is trusted automatically)")
		}
	case ProxyNone:
		if a.TLSMode != "" || a.ACMEEmail != "" || a.ACMEStaging {
			return fieldErr("tls-mode", "--tls-mode, --acme-email and --acme-staging apply to the traefik and nginx proxies only")
		}
		if len(a.TrustedProxies) == 0 {
			return fieldErr("trusted-proxy", "is required with --proxy none (the CIDR of your own reverse proxy on the frontend network)")
		}
	default:
		if len(a.TrustedProxies) > 0 {
			return fieldErr("trusted-proxy", "applies to --proxy none only (the bundled proxy is trusted automatically)")
		}
		switch a.TLSMode {
		case TLSACME:
			if err := ValidateEmail(a.ACMEEmail); err != nil {
				return err
			}
		case TLSStatic:
			if a.ACMEEmail != "" || a.ACMEStaging {
				return fieldErr("acme-email", "--acme-email and --acme-staging apply to --tls-mode acme only")
			}
		default:
			return fieldErr("tls-mode", "must be acme or static")
		}
	}
	if a.DockerGID < 0 || a.DockerGID > maxGID {
		return fieldErr("docker-gid", "must be between 0 and %d", maxGID)
	}
	if len(a.TargetCIDRs) > maxListItems || len(a.TargetHosts) > maxListItems || len(a.TrustedProxies) > maxListItems {
		return fieldErr("target-cidr", "at most %d entries per list", maxListItems)
	}
	for _, c := range a.TargetCIDRs {
		if err := checkScopedPrefix("target-cidr", c); err != nil {
			return err
		}
	}
	if err := validateTargetHosts(a.TargetHosts); err != nil {
		return err
	}
	for _, c := range a.TrustedProxies {
		if err := checkScopedPrefix("trusted-proxy", c); err != nil {
			return err
		}
	}
	if a.KeysDir == "" {
		a.KeysDir = DefaultKeysDir
	}
	if err := ValidateKeysDir(a.KeysDir); err != nil {
		return err
	}
	a.Network = a.Network.withDefaults()
	if err := validateNetwork(a.Network); err != nil {
		return err
	}
	if err := a.DB.validate(); err != nil {
		return err
	}
	if a.Proxy == ProxyNone {
		// The own proxy must not claim serve's or the disabled bundled
		// proxy's fixed frontend address (the compose lint refuses it too).
		for _, c := range a.TrustedProxies {
			p, err := netip.ParsePrefix(c)
			if err != nil {
				return fieldErr("trusted-proxy", "is not a CIDR")
			}
			for _, ip := range []string{a.Network.ProxyFrontendIP, a.Network.ServeFrontendIP} {
				if addr, err := netip.ParseAddr(ip); err == nil && p.Contains(addr) {
					return fieldErr("trusted-proxy", "must not include %s (reserved for bekup on the frontend network)", ip)
				}
			}
		}
	}
	if a.Variant.External() {
		a.Link = a.Link.withDefaults()
		return validateLink(a.Link, a.Network)
	}
	return nil
}

// ImpliedProxy is the Proxy a variant implies ("" for full: --proxy
// decides); the wizard fills it like Complete does.
func (v Variant) ImpliedProxy() Proxy { return v.proxy() }

// proxy is the Proxy a variant implies ("" for full: --proxy decides).
func (v Variant) proxy() Proxy {
	switch v {
	case VariantTraefik:
		return ProxyTraefik
	case VariantTraefikExternal, VariantNPM:
		return ProxyExternal
	default:
		return ""
	}
}

// trustedProxies returns the trusted proxy CIDRs bekup.toml must name.
func (a *Answers) trustedProxies() []string {
	switch a.Proxy {
	case ProxyNone:
		return a.TrustedProxies
	case ProxyExternal:
		return []string{a.Link.ProxyIP + "/32"}
	default:
		return []string{a.Network.ProxyFrontendIP + "/32"}
	}
}

// serveIP is serve's address on the network it shares with the proxy.
func (a *Answers) serveIP() string {
	if a.Proxy == ProxyExternal {
		return a.Link.ServeIP
	}
	return a.Network.ServeFrontendIP
}

// bundledProxy reports whether the deployment runs a bundled proxy
// (traefik or nginx) with its own TLS.
func (a *Answers) bundledProxy() bool { return a.Proxy == ProxyTraefik || a.Proxy == ProxyNginx }

// acmeCA returns the ACME directory.
func (a *Answers) acmeCA() string {
	if a.ACMEStaging {
		return ACMEStagingURL
	}
	return ACMEProduction
}

func bounded(s string) string {
	const n = 80
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ValidateTargetCIDR checks one --target-cidr answer.
func ValidateTargetCIDR(s string) error { return checkScopedPrefix("target-cidr", s) }

// ValidateTrustedProxy checks one --trusted-proxy answer.
func ValidateTrustedProxy(s string) error { return checkScopedPrefix("trusted-proxy", s) }

// ValidateTargetHost checks one --target-host answer.
func ValidateTargetHost(s string) error { return validateTargetHosts([]string{s}) }

// ValidateDockerGID checks a --docker-gid answer.
func ValidateDockerGID(gid int) error {
	if gid < 0 || gid > maxGID {
		return fieldErr("docker-gid", "must be between 0 and %d", maxGID)
	}
	return nil
}

// ValidateDatabase checks the database answers d (a copy; defaults are
// not returned). The *FieldError names the flag of the first problem.
func ValidateDatabase(d Database) error { return d.validate() }
