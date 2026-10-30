package setup

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"
)

// The structural part of the hardening lint (D-205 amendment 2): every
// service bekup ships is pinned to the shipped variant's resolved
// definition, except for a small, explicit set of operator-tunable
// fields; services the operator adds keep the hardening allowlist and may
// not touch bekup's files, volumes, secrets or internal networks; and
// compose variables are limited to bekup's own, used only where the
// shipped file uses them (the lint cannot see variables of the host
// shell where docker compose runs).

// composeVarAllowlist are the compose variables of the shipped variants
// (TestComposeVarAllowlist derives them from the shipped files). Any other
// ${VAR} in compose.yaml or compose.override.yaml is an error.
var composeVarAllowlist = []string{
	"ACME_CA_SERVER", "ACME_EMAIL", "BEKUP_DOMAIN", "BEKUP_KEYS_DIR",
	"BEKUP_PROXY_NETWORK", "DOCKER_GID", "FRONTEND_SUBNET", "METRICS_SUBNET",
	"PROXY_FRONTEND_IP", "PROXY_TLS_MODE", "RUNNER_CTL_IP", "RUNNER_CTL_SUBNET",
	"SERVE_FRONTEND_IP", "SERVE_METRICS_IP", "SERVE_PROXY_IP",
	"TRAEFIK_CERTRESOLVER", "TRAEFIK_ENTRYPOINT",
}

var interpRefRe = regexp.MustCompile(`^\$(?:\{([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*))`)

// scanInterpolation reports the variables a string references and whether
// it has a `$` that is neither `$$` (a literal `$`) nor a reference.
func scanInterpolation(s string, vars map[string]bool) (bad bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			i++
			continue
		}
		m := interpRefRe.FindStringSubmatch(s[i:])
		if m == nil {
			bad = true
			continue
		}
		vars[m[1]+m[2]] = true
	}
	return bad
}

// allowedYAMLTags are the explicit YAML tags a compose file may carry:
// the core kinds whose node text is what compose-go interpolates, and
// compose's merge tags. Any other tag (`!!binary` decodes base64, so the
// text scan would not see a ${VAR} in it; custom `!` tags) is refused.
var allowedYAMLTags = []string{"!!str", "!!int", "!!bool", "!!float", "!!null", "!!map", "!!seq", "!reset", "!override"}

// yamlTagError reports an explicit YAML tag outside allowedYAMLTags.
type yamlTagError struct {
	tag  string
	line int
}

func (e *yamlTagError) Error() string {
	return fmt.Sprintf("line %d: the YAML tag %s is not allowed (only %s): the lint reads values as written", e.line, bounded(e.tag), strings.Join(allowedYAMLTags, ", "))
}

// composeVarRefs returns the variables referenced in the YAML scalars of
// data (keys and values; comments are not values) and whether a `$` is
// not a reference. A node with an explicit tag outside allowedYAMLTags is
// a *yamlTagError: the scan assumes the node text is the value. With
// placeholder, the value of the top-level x-bekup-image key is skipped
// when it is exactly ComposeImagePlaceholder (BEKUP_IMAGE is allowed only
// there; composeImage has checked the line).
func composeVarRefs(data []byte, placeholder bool) (map[string]bool, bool, error) {
	vars := map[string]bool{}
	bad := false
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	visits := 0
	var skip *yaml.Node
	var walk func(n *yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n == skip {
			return nil
		}
		visits++
		if visits > maxComposeNodes {
			return errors.New("too many YAML nodes")
		}
		if n.Style&yaml.TaggedStyle != 0 && !slices.Contains(allowedYAMLTags, n.ShortTag()) {
			return &yamlTagError{tag: n.ShortTag(), line: n.Line}
		}
		switch n.Kind {
		case yaml.ScalarNode:
			if scanInterpolation(n.Value, vars) {
				bad = true
			}
		case yaml.AliasNode:
			// The anchored node is scanned where it is defined.
		default:
			for _, c := range n.Content {
				if err := walk(c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return vars, bad, nil
		}
		if err != nil {
			return nil, false, err
		}
		if placeholder && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
			top := doc.Content[0]
			for i := 0; i+1 < len(top.Content); i += 2 {
				k, v := top.Content[i], top.Content[i+1]
				if k.Kind == yaml.ScalarNode && k.Value == "x-bekup-image" && v.Kind == yaml.ScalarNode &&
					v.Style&yaml.TaggedStyle == 0 && v.Value == ComposeImagePlaceholder {
					skip = v
				}
			}
		}
		if err := walk(&doc); err != nil {
			return nil, false, err
		}
	}
}

// interpolationFindings checks the compose variables of one file against
// composeVarAllowlist. placeholder allows BEKUP_IMAGE in exactly the
// ComposePlaceholderLine of compose.yaml (never in the override).
func interpolationFindings(name string, data []byte, placeholder bool) []Finding {
	vars, bad, err := composeVarRefs(data, placeholder)
	if te := (*yamlTagError)(nil); errors.As(err, &te) {
		return []Finding{{Severity: SeverityError, Path: name, Msg: te.Error()}}
	}
	if err != nil {
		return []Finding{{Severity: SeverityError, Path: name, Msg: "not valid YAML: " + bounded(err.Error())}}
	}
	var out []Finding
	if bad {
		out = append(out, Finding{Severity: SeverityError, Path: name, Msg: "a `$` that is not a variable reference: write `$$` for a literal `$`"})
	}
	for _, v := range sortedKeys(vars) {
		if !slices.Contains(composeVarAllowlist, v) {
			out = append(out, Finding{Severity: SeverityError, Path: name, Msg: fmt.Sprintf("${%s} is not one of bekup's compose variables: docker compose takes it from the shell where it runs, which the lint cannot see; write the value literally", bounded(v))})
		}
	}
	return out
}

// pidsLimitFindings refuses a `pids_limit` of 0 or less written in a
// service of data (also through merge keys): compose-go reads 0 as unset,
// so the resolved model alone cannot tell `pids_limit: 0` from no key.
func pidsLimitFindings(name string, data []byte) []Finding {
	deref := func(n *yaml.Node) *yaml.Node {
		for i := 0; n != nil && n.Kind == yaml.AliasNode && i < 64; i++ {
			n = n.Alias
		}
		return n
	}
	var lookup func(m *yaml.Node, key string, depth int) []*yaml.Node
	lookup = func(m *yaml.Node, key string, depth int) []*yaml.Node {
		m = deref(m)
		if m == nil || m.Kind != yaml.MappingNode || depth > 16 {
			return nil
		}
		var out []*yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i], m.Content[i+1]
			switch k.Value {
			case key:
				out = append(out, deref(v))
			case "<<":
				v = deref(v)
				if v != nil && v.Kind == yaml.SequenceNode {
					for _, c := range v.Content {
						out = append(out, lookup(c, key, depth+1)...)
					}
				} else {
					out = append(out, lookup(v, key, depth+1)...)
				}
			}
		}
		return out
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var out []Finding
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			return out // invalid YAML is reported by the other checks
		}
		if len(doc.Content) == 0 {
			continue
		}
		for _, svcs := range lookup(doc.Content[0], "services", 0) {
			if svcs == nil || svcs.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(svcs.Content); i += 2 {
				for _, v := range lookup(svcs.Content[i+1], "pids_limit", 0) {
					if v == nil || v.Kind != yaml.ScalarNode {
						continue
					}
					n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(v.Value), "_", ""), 0, 64)
					if err == nil && n <= 0 {
						out = append(out, Finding{Severity: SeverityError, Path: name, Msg: fmt.Sprintf("service %q: pids_limit must be above 0 (0 and -1 lift the limit)", bounded(svcs.Content[i].Value))})
					}
				}
			}
		}
	}
}

// hasInterpolation reports a `$` reference anywhere in v (strings, map
// keys, nested structs) of a project loaded without interpolation.
func hasInterpolation(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		vars := map[string]bool{}
		return scanInterpolation(v.String(), vars) || len(vars) > 0
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && hasInterpolation(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() && hasInterpolation(v.Field(i)) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if hasInterpolation(v.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if hasInterpolation(it.Key()) || hasInterpolation(it.Value()) {
				return true
			}
		}
	default:
		// Numbers, booleans: no text.
	}
	return false
}

// newInterpolation reports a ${VAR} in the parts of act (uninterpolated)
// that differ from exp: map entries and list elements the shipped value
// does not have, recursively. Unchanged shipped parts keep their
// variables.
func newInterpolation(act, exp reflect.Value) bool {
	if !exp.IsValid() {
		return hasInterpolation(act)
	}
	if reflect.DeepEqual(act.Interface(), exp.Interface()) {
		return false
	}
	switch act.Kind() {
	case reflect.Pointer, reflect.Interface:
		if act.IsNil() {
			return false
		}
		if exp.IsNil() {
			return hasInterpolation(act.Elem())
		}
		return newInterpolation(act.Elem(), exp.Elem())
	case reflect.Struct:
		for i := range act.NumField() {
			if act.Type().Field(i).IsExported() && newInterpolation(act.Field(i), exp.Field(i)) {
				return true
			}
		}
		return false
	case reflect.Map:
		it := act.MapRange()
		for it.Next() {
			e := exp.MapIndex(it.Key())
			if hasInterpolation(it.Key()) || newInterpolation(it.Value(), e) {
				return true
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		for i := range act.Len() {
			found := false
			for j := range exp.Len() {
				if reflect.DeepEqual(act.Index(i).Interface(), exp.Index(j).Interface()) {
					found = true
					break
				}
			}
			if !found && hasInterpolation(act.Index(i)) {
				return true
			}
		}
		return false
	default:
		return hasInterpolation(act)
	}
}

// serviceField is one compose attribute of a service (yaml key).
type serviceField struct {
	key string
	v   reflect.Value
}

// serviceFields lists the compose attributes of s in declaration order,
// flattening the inline specs; "#extensions" holds the x- keys.
func serviceFields(s *types.ServiceConfig) []serviceField {
	var out []serviceField
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			switch {
			case f.Anonymous:
				walk(v.Field(i))
			case !f.IsExported() || tag == "-":
			case tag == "":
			default:
				out = append(out, serviceField{key: tag, v: v.Field(i)})
			}
		}
	}
	walk(reflect.ValueOf(s).Elem())
	return out
}

// tunableCheck validates an operator-tunable attribute of a shipped
// service; it returns "" when the value is acceptable.
type tunableCheck func(act, exp *types.ServiceConfig, o *lintOptions) string

// operatorTunable lists, per shipped service, the attributes an operator
// may change (everything else stays exactly as shipped). Documented in
// docs/installation/compose.md and tested in TestOperatorTunables:
//   - every service but setup: deploy resource limits/reservations (cpus,
//     memory, pids > 0; nothing else under deploy), the legacy cpus,
//     mem_limit, mem_reservation and pids_limit (> 0), restart and
//     stop_grace_period;
//   - the bundled proxy services (traefik, traefik-init, nginx,
//     nginx-init, certbot): profiles (e.g. certbot off in static TLS mode);
//   - traefik and nginx: PROXY_TLS_MODE (acme|static) and
//     PROXY_HTTPS_PUBLIC_PORT in environment, a read-only ./proxy-tls bind
//     at /certs, and the published side of ports (same container ports);
//   - runner: the Docker socket source (rootful or rootless path, still
//     read-only at /var/run/docker.sock) and `userns_mode: host` (a
//     warning, for userns-remap daemons);
//   - serve: the source of its backups/scratch mounts and extra mounts
//     below /var/lib/bekup/backups/ (disk destinations), from named
//     volumes that are not bekup's other volumes or binds inside the
//     deployment directory outside bekup's files.
func operatorTunable(name string) map[string]tunableCheck {
	if name == "setup" {
		return nil
	}
	t := map[string]tunableCheck{
		"deploy": checkDeploy, "cpus": anyValue, "mem_limit": anyValue,
		"mem_reservation": anyValue, "pids_limit": checkPidsLimit,
		"restart": checkRestart, "stop_grace_period": anyValue,
	}
	switch name {
	case "traefik", "nginx":
		t["profiles"] = anyValue
		t["environment"] = checkProxyEnv
		t["volumes"] = checkProxyVolumes
		t["ports"] = checkProxyPorts
	case "traefik-init", "nginx-init", "certbot":
		t["profiles"] = anyValue
	case "runner":
		t["userns_mode"] = checkRunnerUserNS
		t["volumes"] = checkRunnerVolumes
	case "serve":
		t["volumes"] = checkServeVolumes
	}
	return t
}

func anyValue(_, _ *types.ServiceConfig, _ *lintOptions) string { return "" }

// checkPidsLimit: a pids_limit must be above 0 (0 is unset in compose-go;
// pidsLimitFindings refuses a written 0).
func checkPidsLimit(act, _ *types.ServiceConfig, _ *lintOptions) string {
	if act.PidsLimit < 0 {
		return "an unlimited pids_limit is not allowed"
	}
	return ""
}

var restartRe = regexp.MustCompile(`^(?:|no|always|unless-stopped|on-failure(?::\d{1,4})?)$`)

func checkRestart(act, _ *types.ServiceConfig, _ *lintOptions) string {
	if !restartRe.MatchString(act.Restart) {
		return "not a restart policy"
	}
	return ""
}

// checkDeploy accepts changed cpus/memory/pids under deploy.resources
// limits and reservations; everything else under deploy stays.
func checkDeploy(act, exp *types.ServiceConfig, _ *lintOptions) string {
	norm := func(d *types.DeployConfig) (types.DeployConfig, bool) {
		if d == nil {
			return types.DeployConfig{}, true
		}
		c := *d
		ok := true
		for _, pp := range []**types.Resource{&c.Resources.Limits, &c.Resources.Reservations} {
			if *pp == nil {
				continue
			}
			x := **pp
			if x.Pids < 0 {
				ok = false
			}
			x.NanoCPUs, x.MemoryBytes, x.Pids = 0, 0, 0
			if reflect.DeepEqual(x, types.Resource{}) {
				*pp = nil
			} else {
				*pp = &x
			}
		}
		return c, ok
	}
	a, ok := norm(act.Deploy)
	if !ok {
		return "unlimited pids are not allowed"
	}
	e, _ := norm(exp.Deploy)
	if !reflect.DeepEqual(a, e) {
		return "only the cpus, memory and pids of deploy.resources may change"
	}
	return checkDeployLimitsKept(act.Deploy, exp.Deploy)
}

// checkDeployLimitsKept: every cpus/memory/pids limit the shipped service
// sets stays set; only its value may change (`deploy: !reset null` or a 0
// would lift it).
func checkDeployLimitsKept(act, exp *types.DeployConfig) string {
	if exp == nil || exp.Resources.Limits == nil {
		return ""
	}
	el := exp.Resources.Limits
	var al types.Resource
	if act != nil && act.Resources.Limits != nil {
		al = *act.Resources.Limits
	}
	var missing []string
	if el.NanoCPUs > 0 && al.NanoCPUs <= 0 {
		missing = append(missing, "cpus")
	}
	if el.MemoryBytes > 0 && al.MemoryBytes <= 0 {
		missing = append(missing, "memory")
	}
	if el.Pids > 0 && al.Pids <= 0 {
		missing = append(missing, "pids")
	}
	if len(missing) > 0 {
		return "the shipped deploy.resources.limits " + strings.Join(missing, ", ") + " must stay set (above 0)"
	}
	return ""
}

func checkProxyEnv(act, exp *types.ServiceConfig, _ *lintOptions) string {
	var probs []string
	for _, k := range sortedKeys(act.Environment) {
		v := act.Environment[k]
		switch k {
		case "PROXY_TLS_MODE":
			if v == nil || *v != "acme" && *v != "static" {
				probs = append(probs, "PROXY_TLS_MODE must be acme or static")
			}
		case "PROXY_HTTPS_PUBLIC_PORT":
			if n, err := strconv.Atoi(deref(v)); err != nil || n < 1 || n > 65535 {
				probs = append(probs, "PROXY_HTTPS_PUBLIC_PORT must be a port number")
			}
		default:
			if e, ok := exp.Environment[k]; !ok || deref(e) != deref(v) || (e == nil) != (v == nil) {
				probs = append(probs, bounded(k)+" may not be set or changed")
			}
		}
	}
	for _, k := range sortedKeys(exp.Environment) {
		if _, ok := act.Environment[k]; !ok && k != "PROXY_TLS_MODE" && k != "PROXY_HTTPS_PUBLIC_PORT" {
			probs = append(probs, bounded(k)+" may not be removed")
		}
	}
	return strings.Join(probs, "; ")
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// bindOptionsOK: no SELinux relabeling, private propagation, no
// recursive-mount options.
func bindOptionsOK(b *types.ServiceVolumeBind) bool {
	return b == nil || b.SELinux == "" && slices.Contains([]string{"", "private", "rprivate"}, b.Propagation) && b.Recursive == ""
}

// diffMounts compares the mounts of a shipped service by target: shipped
// targets must stay unchanged unless extra accepts the replacement, and
// extra decides about every other target ("" accepts).
func diffMounts(act, exp []types.ServiceVolumeConfig, extra func(v types.ServiceVolumeConfig, shipped bool) string) string {
	byTarget := map[string]types.ServiceVolumeConfig{}
	for _, v := range exp {
		byTarget[path.Clean(v.Target)] = v
	}
	var probs []string
	seen := map[string]bool{}
	for _, v := range act {
		t := path.Clean(v.Target)
		seen[t] = true
		if e, ok := byTarget[t]; ok && reflect.DeepEqual(v, e) {
			continue
		}
		_, shipped := byTarget[t]
		if p := extra(v, shipped); p != "" {
			probs = append(probs, p)
		}
	}
	for t := range byTarget {
		if !seen[t] {
			probs = append(probs, "the mount at "+bounded(t)+" may not be removed")
		}
	}
	sort.Strings(probs)
	return strings.Join(slices.Compact(probs), "; ")
}

func checkProxyVolumes(act, exp *types.ServiceConfig, _ *lintOptions) string {
	return diffMounts(act.Volumes, exp.Volumes, func(v types.ServiceVolumeConfig, shipped bool) string {
		switch {
		case shipped:
			return "the mount at " + bounded(v.Target) + " may not change"
		case path.Clean(v.Target) != "/certs":
			return "only a read-only ./" + ProxyTLSDir + " bind at /certs may be added (mount at " + bounded(v.Target) + ")"
		case v.Type != types.VolumeTypeBind || v.Source != filepath.Join(lintRoot, ProxyTLSDir) || !v.ReadOnly || !bindOptionsOK(v.Bind):
			return "/certs must be a read-only bind of ./" + ProxyTLSDir + " without bind options"
		}
		return ""
	})
}

func checkProxyPorts(act, exp *types.ServiceConfig, _ *lintOptions) string {
	targets := map[uint32]bool{}
	for _, p := range exp.Ports {
		targets[p.Target] = true
	}
	for _, p := range act.Ports {
		if !targets[p.Target] || p.Protocol != "" && p.Protocol != "tcp" || p.Mode != "" && p.Mode != "ingress" && p.Mode != "host" {
			return "only the published address/port of the shipped container ports (tcp) may change"
		}
	}
	return ""
}

func checkRunnerUserNS(act, _ *types.ServiceConfig, _ *lintOptions) string {
	if act.UserNSMode != "" && act.UserNSMode != "host" {
		return "only `userns_mode: host` is allowed"
	}
	return ""
}

func checkRunnerVolumes(act, exp *types.ServiceConfig, _ *lintOptions) string {
	sockets := 0
	for _, v := range act.Volumes {
		if path.Clean(v.Target) == runnerSocketTarget {
			sockets++
		}
	}
	if sockets != 1 {
		return "the runner mounts exactly one Docker socket at " + runnerSocketTarget
	}
	return diffMounts(act.Volumes, exp.Volumes, func(v types.ServiceVolumeConfig, _ bool) string {
		if path.Clean(v.Target) != runnerSocketTarget {
			return "the runner may mount nothing but the Docker socket (mount at " + bounded(v.Target) + ")"
		}
		if v.Type != types.VolumeTypeBind || !dockerSocketRe.MatchString(filepath.Clean(v.Source)) || !v.ReadOnly || !bindOptionsOK(v.Bind) {
			return "the Docker socket must be a read-only bind of /var/run/docker.sock, /run/docker.sock or /run/user/<uid>/docker.sock without bind options (no SELinux relabeling)"
		}
		return ""
	})
}

// serveBackupsDir is where disk destinations live in serve.
const serveBackupsDir = "/var/lib/bekup/backups"

func checkServeVolumes(act, exp *types.ServiceConfig, o *lintOptions) string {
	return diffMounts(act.Volumes, exp.Volumes, func(v types.ServiceVolumeConfig, shipped bool) string {
		t := path.Clean(v.Target)
		if !shipped && !strings.HasPrefix(t, serveBackupsDir+"/") {
			return "extra serve mounts must target a directory below " + serveBackupsDir + "/ (mount at " + bounded(t) + ")"
		}
		switch v.Type {
		case types.VolumeTypeVolume:
			if o.shippedVolume(v.Source) && !slices.ContainsFunc(exp.Volumes, func(e types.ServiceVolumeConfig) bool { return e.Source == v.Source }) {
				return "the named volume " + bounded(v.Source) + " belongs to another bekup service"
			}
		case types.VolumeTypeBind:
			rel := relInLintRoot(v.Source)
			switch {
			case rel == "":
				return "a serve bind must come from inside the deployment directory (" + shownPath(v.Source) + ")"
			case o.protectedRel(rel):
				return "./" + bounded(rel) + " holds bekup's own files or secrets"
			case !bindOptionsOK(v.Bind):
				return "a serve bind may not set SELinux, propagation or recursive options"
			}
		default:
			return "a " + bounded(v.Type) + " mount is not allowed on serve"
		}
		return ""
	})
}

// shippedVolume reports a top-level volume of the shipped variant.
func (o *lintOptions) shippedVolume(name string) bool {
	if o.expected == nil {
		return false
	}
	_, ok := o.expected.Volumes[name]
	return ok
}

// protectedRel reports a path inside the deployment directory that holds
// bekup's own files: the keys directory, the state file, .env, the TOML
// and compose files, deploy/ and proxy-tls/ (or a parent of one). File
// systems may be case-insensitive (Docker Desktop), so the comparison is.
func (o *lintOptions) protectedRel(rel string) bool {
	rel = strings.ToLower(path.Clean(rel))
	prot := []string{DefaultKeysDir, StateFileName, EnvFile, BekupTOML, RunnerTOML, ComposeFile, OverrideFile, DeployDir, ProxyTLSDir}
	prot = append(prot, composeShadowFiles...)
	if o.keysDir != "" {
		prot = append(prot, o.keysDir)
	}
	for _, p := range prot {
		p = strings.ToLower(p)
		if rel == p || strings.HasPrefix(rel, p+"/") || strings.HasPrefix(p, rel+"/") {
			return true
		}
	}
	return false
}

// keysDirRel returns the keys directory of BEKUP_KEYS_DIR when it is a
// clean relative path inside the deployment directory.
func keysDirRel(env map[string]string) string {
	k := strings.TrimPrefix(env["BEKUP_KEYS_DIR"], "./")
	if k == "" || checkRel(path.Clean(k)) != nil || path.Clean(k) == "." {
		return ""
	}
	return path.Clean(k)
}

// shippedDiff lists the attributes in which act differs from the shipped
// service exp (tunable attributes only when their check fails).
func shippedDiff(act, exp *types.ServiceConfig, o *lintOptions) []string {
	tun := operatorTunable(exp.Name)
	ef := serviceFields(exp)
	var probs []string
	for i, f := range serviceFields(act) {
		if check, ok := tun[f.key]; ok {
			if p := check(act, exp, o); p != "" {
				probs = append(probs, f.key+" ("+p+")")
			}
			continue
		}
		if !reflect.DeepEqual(f.v.Interface(), ef[i].v.Interface()) {
			probs = append(probs, displayKey(f.key))
		}
	}
	return probs
}

func displayKey(k string) string {
	if k == "#extensions" {
		return "x-* keys"
	}
	return k
}

// rawDiff lists the attributes in which the uninterpolated act differs
// from the shipped service by using a ${VAR}.
func rawDiff(act, exp *types.ServiceConfig) []string {
	ef := serviceFields(exp)
	var keys []string
	for i, f := range serviceFields(act) {
		if newInterpolation(f.v, ef[i].v) {
			keys = append(keys, displayKey(f.key))
		}
	}
	return keys
}

const tunableSummary = "operator-tunable: resource limits, restart, stop_grace_period, and the documented proxy, runner and serve overrides (docs/installation/compose.md)"

// lintShipped pins a shipped service to the shipped variant's definition.
func lintShipped(svc, exp *types.ServiceConfig, o *lintOptions, add func(Severity, string, ...any)) {
	name := svc.Name
	shown := bounded(name)
	if probs := shippedDiff(svc, exp, o); len(probs) > 0 {
		blame := false
		if o.override && o.own != nil {
			if own := serviceOf(o.own, name); own != nil && len(shippedDiff(own, exp, o)) == 0 {
				blame = true
			}
		}
		list := strings.Join(probs, ", ")
		switch {
		case name == "setup" && blame:
			add(SeverityError, "the override changes the setup service; it must stay exactly as shipped (root with CAP_CHOWN only, no network, no logs)")
		case name == "setup":
			add(SeverityError, "the setup service differs from the one shipped with the %s variant; restore it exactly (root with CAP_CHOWN only, no network, no logs)", o.variant)
		case blame:
			add(SeverityError, "the override changes bekup's service %q in: %s; bekup's services stay as shipped (%s)", shown, list, tunableSummary)
		default:
			add(SeverityError, "service %q differs from the one shipped with the %s variant in: %s; restore it (%s)", shown, o.variant, list, tunableSummary)
		}
	}
	if o.raw == nil || o.expectedRaw == nil {
		return
	}
	rs, re := serviceOf(o.raw, name), serviceOf(o.expectedRaw, name)
	if rs == nil || re == nil {
		return
	}
	if keys := rawDiff(rs, re); len(keys) > 0 {
		add(SeverityError, "service %q: %s use ${...} variables where the shipped file does not; docker compose takes them from the shell where it runs, which the lint cannot see: write the values literally", shown, strings.Join(keys, ", "))
	}
}

// allowedLogDrivers keep logs on the host, bounded.
var (
	allowedLogDrivers = []string{"", "json-file", "local", "none"}
	allowedLogOptions = []string{"max-size", "max-file", "compress", "mode", "max-buffer-size"}
)

// ulimitBounds are the only ulimits an added service may set.
var ulimitBounds = map[string]int{"nofile": 1 << 20, "nproc": 65536}

// maxAddedShm bounds shm_size of an added service (1 GiB).
const maxAddedShm = 1 << 30

// lintAdded applies the rules for services the operator adds on top of
// the hardening allowlist of lintService.
func lintAdded(svc *types.ServiceConfig, o *lintOptions, add func(Severity, string, ...any)) {
	shown := bounded(svc.Name)
	errf := func(format string, args ...any) {
		add(SeverityError, "added service %q: "+format, append([]any{shown}, args...)...)
	}
	if len(svc.GroupAdd) > 0 {
		errf("`group_add` is not allowed (supplementary groups such as the Docker socket's group)")
	}
	driver, opts := svc.LogDriver, map[string]string{}
	for k, v := range svc.LogOpt {
		opts[k] = v
	}
	if svc.Logging != nil {
		if svc.Logging.Driver != "" {
			driver = svc.Logging.Driver
		}
		for k, v := range svc.Logging.Options {
			opts[k] = v
		}
	}
	if !slices.Contains(allowedLogDrivers, driver) {
		errf("logging driver %q is not allowed (only json-file, local or none: logs stay on this host)", bounded(driver))
	}
	for _, k := range sortedKeys(opts) {
		if !slices.Contains(allowedLogOptions, k) {
			errf("logging option %q is not allowed (only %s)", bounded(k), strings.Join(allowedLogOptions, ", "))
		}
	}
	labels := map[string]string{}
	for k, v := range svc.Labels {
		labels[k] = v
	}
	if svc.Deploy != nil {
		for k, v := range svc.Deploy.Labels {
			labels[k] = v
		}
	}
	for _, k := range sortedKeys(labels) {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "traefik.") || strings.HasPrefix(lk, "com.docker.") {
			errf("label %q is not allowed (traefik.* labels could route traffic of an operator Traefik; com.docker.* labels are Docker's)", bounded(k))
		}
	}
	if svc.OomScoreAdj < 0 {
		errf("a negative oom_score_adj is not allowed (the kernel would kill other processes first)")
	}
	if svc.OomKillDisable {
		errf("`oom_kill_disable` is not allowed")
	}
	if svc.ShmSize > maxAddedShm {
		errf("shm_size above 1 GiB is not allowed")
	}
	if svc.CPURTRuntime != 0 || svc.CPURTPeriod != 0 {
		errf("real-time CPU scheduling (cpu_rt_runtime, cpu_rt_period) is not allowed")
	}
	if svc.PidsLimit < 0 {
		errf("an unlimited pids_limit is not allowed")
	}
	if d := svc.Deploy; d != nil {
		for _, r := range []*types.Resource{d.Resources.Limits, d.Resources.Reservations} {
			if r != nil && r.Pids < 0 {
				errf("unlimited deploy pids are not allowed")
			}
		}
	}
	for _, k := range sortedKeys(svc.Ulimits) {
		u := svc.Ulimits[k]
		bound, ok := ulimitBounds[k]
		switch {
		case !ok:
			errf("ulimit %q is not allowed (only nofile and nproc)", bounded(k))
		case u != nil && (u.Single < 0 || u.Soft < 0 || u.Hard < 0 || max(u.Single, u.Soft, u.Hard) > bound):
			errf("ulimit %q must be between 0 and %d", k, bound)
		}
	}
	if len(svc.ExternalLinks) > 0 {
		errf("`external_links` is not allowed (containers outside this project)")
	}
	joinsShipped := false
	for _, n := range sortedKeys(svc.Networks) {
		if o.expected == nil {
			break
		}
		if _, shipped := o.expected.Networks[n]; !shipped {
			continue
		}
		joinsShipped = true
		if n != "metrics" && (n != "frontend" || o.variant != VariantFull) {
			errf("may not join bekup's network %q (only its own networks, metrics for a Prometheus, and frontend for your proxy in the full variant)", bounded(n))
		}
		c := svc.Networks[n]
		if c == nil {
			continue
		}
		if len(c.Aliases) > 0 {
			errf("network aliases on bekup's network %q are not allowed (an alias could take over a bekup service name)", bounded(n))
		}
		fixed := []string{c.Ipv4Address, c.Ipv6Address, c.MacAddress}
		if !slices.ContainsFunc(fixed, func(a string) bool { return a != "" }) && len(c.LinkLocalIPs) == 0 {
			continue
		}
		switch {
		case n != "frontend" || o.variant != VariantFull:
			errf("fixed addresses (ipv4_address, ipv6_address, mac_address, link_local_ips) on bekup's network %q are not allowed (they could take a bekup service's address)", bounded(n))
		case len(c.LinkLocalIPs) > 0:
			errf("link_local_ips on bekup's network %q are not allowed", bounded(n))
		default:
			taken := shippedAddresses(o.expected, n)
			for _, a := range fixed {
				if a != "" && taken[normalizeAddr(a)] {
					errf("the address %s on bekup's network %q belongs to a bekup service; pick another one", bounded(a), bounded(n))
				}
			}
		}
	}
	if joinsShipped && svc.ContainerName != "" {
		errf("`container_name` is not allowed on bekup's networks (it is resolvable like a service name)")
	}
	if joinsShipped && svc.MacAddress != "" {
		errf("`mac_address` is not allowed on bekup's networks (it could take a bekup service's address)")
	}
	for _, v := range svc.Volumes {
		switch v.Type {
		case types.VolumeTypeBind:
			if rel := relInLintRoot(v.Source); rel != "" && o.protectedRel(rel) {
				errf("may not mount ./%s (bekup's own files or secrets)", bounded(rel))
			}
		case types.VolumeTypeVolume:
			if o.shippedVolume(v.Source) {
				errf("may not mount bekup's volume %q", bounded(v.Source))
			}
		}
	}
	for _, f := range svc.EnvFiles {
		if rel := relInLintRoot(f.Path); rel != "" && o.protectedRel(rel) {
			errf("env_file ./%s is one of bekup's own files or secrets", bounded(rel))
		}
	}
	for _, f := range svc.LabelFiles {
		if rel := relInLintRoot(f); rel != "" && o.protectedRel(rel) {
			errf("label_file ./%s is one of bekup's own files or secrets", bounded(rel))
		}
	}
	if o.expected != nil {
		for _, s := range svc.Secrets {
			if _, ok := o.expected.Secrets[s.Source]; ok {
				errf("may not use bekup's secret %q", bounded(s.Source))
			}
		}
		for _, c := range svc.Configs {
			if _, ok := o.expected.Configs[c.Source]; ok {
				errf("may not use bekup's config %q", bounded(c.Source))
			}
		}
	}
	if o.raw != nil {
		if rs := serviceOf(o.raw, svc.Name); rs != nil && hasInterpolation(reflect.ValueOf(rs).Elem()) {
			errf("uses ${...} variables; docker compose takes them from the shell where it runs, which the lint cannot see: write the values literally")
		}
	}
}

// shippedAddresses returns the fixed addresses (normalized) the shipped
// services of exp use on network n, enabled or not (a proxy of another
// profile keeps its address reserved).
func shippedAddresses(exp *types.Project, n string) map[string]bool {
	out := map[string]bool{}
	for _, s := range allServices(exp) {
		if s.MacAddress != "" {
			out[normalizeAddr(s.MacAddress)] = true
		}
		c := s.Networks[n]
		if c == nil {
			continue
		}
		for _, a := range append([]string{c.Ipv4Address, c.Ipv6Address, c.MacAddress}, c.LinkLocalIPs...) {
			if a != "" {
				out[normalizeAddr(a)] = true
			}
		}
	}
	return out
}

// normalizeAddr canonicalizes an IP or MAC address for comparison.
func normalizeAddr(a string) string {
	a = strings.TrimSpace(a)
	if ip, err := netip.ParseAddr(a); err == nil {
		return ip.Unmap().String()
	}
	if hw, err := net.ParseMAC(a); err == nil {
		return hw.String()
	}
	return strings.ToLower(a)
}

// lintShippedTopLevel pins the shipped networks, volumes, secrets and
// configs and restricts the ones the operator adds.
func lintShippedTopLevel(p *types.Project, o *lintOptions, add func(Severity, string, ...any)) {
	if o.expected == nil {
		return
	}
	switch {
	case p.Name != o.expected.Name:
		add(SeverityError, "the project name %q differs from the shipped %q: docker compose would use the volumes and networks of another project", bounded(p.Name), o.expected.Name)
	case o.raw != nil && o.expectedRaw != nil && o.raw.Name != o.expectedRaw.Name:
		// The same name only through variables (`name: ${VAR:-bekup}`):
		// the shell where docker compose runs can set them.
		add(SeverityError, "the project name is written differently from the shipped `name: %s` (variables in it come from the shell where docker compose runs, which the lint cannot see): write it literally", o.expectedRaw.Name)
	}
	rawOf := func(kind, name string) (reflect.Value, reflect.Value, bool) {
		if o.raw == nil || o.expectedRaw == nil {
			return reflect.Value{}, reflect.Value{}, false
		}
		var a, e any
		var okA, okE bool
		switch kind {
		case "network":
			a, okA = o.raw.Networks[name]
			e, okE = o.expectedRaw.Networks[name]
		case "volume":
			a, okA = o.raw.Volumes[name]
			e, okE = o.expectedRaw.Volumes[name]
		case "secret":
			a, okA = o.raw.Secrets[name]
			e, okE = o.expectedRaw.Secrets[name]
		case "config":
			a, okA = o.raw.Configs[name]
			e, okE = o.expectedRaw.Configs[name]
		}
		if !okA {
			return reflect.Value{}, reflect.Value{}, false
		}
		if !okE {
			return reflect.ValueOf(a), reflect.Value{}, true
		}
		return reflect.ValueOf(a), reflect.ValueOf(e), true
	}
	check := func(kind, name string, act, exp any, shipped bool, extra func()) {
		shown := bounded(name)
		if shipped && !reflect.DeepEqual(act, exp) {
			add(SeverityError, "%s %q differs from the one shipped with the %s variant (its driver, internal flag, IPAM, name, external flag, file and labels stay as shipped)", kind, shown, o.variant)
		}
		if !shipped {
			extra()
		}
		ra, re, ok := rawOf(kind, name)
		if ok && newInterpolation(ra, re) {
			add(SeverityError, "%s %q uses ${...} variables where the shipped file does not; docker compose takes them from the shell where it runs, which the lint cannot see: write the values literally", kind, shown)
		}
	}
	defaultName := func(kind, name, got string) {
		if got != "" && got != p.Name+"_"+name {
			add(SeverityError, "%s %q: `name: %s` is not allowed on an added %s (it could alias another project's or bekup's %s)", kind, bounded(name), bounded(got), kind, kind)
		}
	}
	for _, name := range sortedKeys(p.Networks) {
		n := p.Networks[name]
		e, shipped := o.expected.Networks[name]
		check("network", name, n, e, shipped, func() {
			if bool(n.External) {
				add(SeverityError, "network %q: external networks are not allowed on added networks (they could join bekup's services to another project's or the host's network)", bounded(name))
				return
			}
			defaultName("network", name, n.Name)
		})
	}
	for _, name := range sortedKeys(p.Volumes) {
		v := p.Volumes[name]
		e, shipped := o.expected.Volumes[name]
		check("volume", name, v, e, shipped, func() {
			if !bool(v.External) {
				defaultName("volume", name, v.Name)
			}
		})
	}
	added := func(kind, name string, c types.FileObjectConfig) {
		if c.Environment != "" {
			add(SeverityError, "%s %q: `environment:` is not allowed (it copies a variable of the shell where docker compose runs into the container)", kind, bounded(name))
		}
		if rel := relInLintRoot(c.File); rel != "" && o.protectedRel(rel) {
			add(SeverityError, "%s %q: ./%s is one of bekup's own files or secrets", kind, bounded(name), bounded(rel))
		}
	}
	for _, name := range sortedKeys(p.Secrets) {
		s := p.Secrets[name]
		e, shipped := o.expected.Secrets[name]
		check("secret", name, s, e, shipped, func() { added("secret", name, types.FileObjectConfig(s)) })
	}
	for _, name := range sortedKeys(p.Configs) {
		c := p.Configs[name]
		e, shipped := o.expected.Configs[name]
		check("config", name, c, e, shipped, func() { added("config", name, types.FileObjectConfig(c)) })
	}
}

// lintPathSymlinks refuses symlinks on the way to every bind source,
// env_file, label_file and secret/config file inside the deployment
// directory (init and upgrade do not run check's full scan): a bind of
// ./data where data -> / would mount the host's root.
func lintPathSymlinks(p *types.Project, realRoot, label string) []Finding {
	if realRoot == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []Finding
	visit := func(abs string) {
		rel := relInLintRoot(abs)
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		if _, err := checkNoSymlinks(realRoot, rel); err != nil {
			out = append(out, Finding{Severity: SeverityError, Path: label, Msg: fmt.Sprintf("./%s: %s", bounded(rel), bounded(err.Error()))})
		}
	}
	for _, svc := range allServices(p) {
		for _, v := range svc.Volumes {
			if v.Type == types.VolumeTypeBind {
				visit(v.Source)
			}
		}
		for _, f := range svc.EnvFiles {
			visit(f.Path)
		}
		for _, f := range svc.LabelFiles {
			visit(f)
		}
	}
	for _, name := range sortedKeys(p.Secrets) {
		visit(p.Secrets[name].File)
	}
	for _, name := range sortedKeys(p.Configs) {
		visit(p.Configs[name].File)
	}
	return out
}
