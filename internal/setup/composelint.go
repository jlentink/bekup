package setup

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/jlentink/bekup/internal/imageref"
)

// bekupServices are the services that run the bekup image.
var bekupServices = []string{"setup", "certs-init", "migrate", "admin", "serve", "runner"}

// rootInitServices are the shipped one-shot services that run as root to
// chown a named volume for the bundled proxy (no binds, no network).
var rootInitServices = []string{"traefik-init", "nginx-init"}

// proxyServices may publish ports.
func proxyServices(v Variant) []string {
	switch v {
	case VariantTraefik:
		return []string{"traefik"}
	case VariantFull:
		return []string{"traefik", "nginx"}
	default:
		return nil
	}
}

// allowedCaps are the capabilities a service may add (everything else is
// refused): the setup container chowns the deployment directory, the root
// init services chown their volume, and any other service may at most bind
// a low port.
func allowedCaps(name string) []string {
	switch {
	case name == "setup":
		return []string{"CHOWN"}
	case slices.Contains(rootInitServices, name):
		return []string{"CHOWN", "FOWNER"}
	default:
		return []string{"NET_BIND_SERVICE"}
	}
}

// dockerSocketRe matches the Docker sockets the runner may mount: the
// rootful socket and the rootless one (docs/installation/rootless-docker.md).
var dockerSocketRe = regexp.MustCompile(`^(?:/var/run/docker\.sock|/run/docker\.sock|/run/user/[0-9]{1,10}/docker\.sock)$`)

// runnerSocketTarget is where the runner expects the socket.
const runnerSocketTarget = "/var/run/docker.sock"

type lintOptions struct {
	// label names the linted files in findings.
	label   string
	variant Variant
	// root is the directory the project is resolved in (lintRoot); realRoot
	// the deployment directory on disk ("" when not on disk).
	root     string
	realRoot string
	// image is the x-bekup-image reference: every bekup service must use it.
	image string
	// keysDir is the keys directory from BEKUP_KEYS_DIR (when relative).
	keysDir string
	// expected is the shipped variant resolved with the same environment,
	// expectedRaw the same without interpolation; raw is the linted project
	// without interpolation (nil when it does not load); own is
	// compose.yaml alone (nil without an override), to blame the override.
	expected    *types.Project
	expectedRaw *types.Project
	raw         *types.Project
	own         *types.Project
	override    bool
}

// lintComposeFiles resolves compose.yaml (+ compose.override.yaml) like
// docker compose and runs the hardening lint on the result. expected is
// the shipped variant's compose file: every service, network, volume,
// secret and config it ships is pinned to it. placeholder: compose.yaml
// takes the image from BEKUP_IMAGE (the committed files in deploy/compose/).
func lintComposeFiles(in *composeInput, env map[string]string, v Variant, image string, expected []byte, placeholder bool) []Finding {
	label := ComposeFile
	if in.override != nil {
		label = ComposeFile + " + " + OverrideFile
	}
	var out []Finding
	out = append(out, interpolationFindings(ComposeFile, in.compose, placeholder)...)
	out = append(out, pidsLimitFindings(ComposeFile, in.compose)...)
	if in.override != nil {
		out = append(out, interpolationFindings(OverrideFile, in.override, false)...)
		out = append(out, pidsLimitFindings(OverrideFile, in.override)...)
	}
	p, err := loadProject(in.root, env, in.docs()...)
	if err != nil {
		return append(out, Finding{Severity: SeverityError, Path: label, Msg: err.Error()})
	}
	expSrc := composeSrc{name: ComposeFile, data: expected}
	exp, err := loadProject(in.root, env, expSrc)
	if err != nil {
		return append(out, Finding{Severity: SeverityError, Path: label, Msg: "resolving the shipped " + string(v) + " variant: " + err.Error()})
	}
	expRaw, err := loadRawProject(in.root, expSrc)
	if err != nil {
		return append(out, Finding{Severity: SeverityError, Path: label, Msg: "resolving the shipped " + string(v) + " variant: " + err.Error()})
	}
	o := lintOptions{
		label: label, variant: v, root: lintRoot, realRoot: in.realRoot(), image: image,
		keysDir: keysDirRel(env), expected: exp, expectedRaw: expRaw, override: in.override != nil,
	}
	if o.raw, err = loadRawProject(in.root, in.docs()...); err != nil {
		out = append(out, Finding{Severity: SeverityError, Path: label, Msg: "cannot be checked for ${...} variables (docker compose takes them from the shell where it runs, which the lint cannot see; write the values literally): " + err.Error()})
	}
	if in.override != nil {
		if own, err := loadProject(in.root, env, composeSrc{name: ComposeFile, data: in.compose}); err == nil {
			o.own = own
		}
	}
	out = append(out, lintProject(p, o)...)
	return append(out, lintPathSymlinks(p, o.realRoot, label)...)
}

// serviceOf returns the named service (enabled or not), or nil.
func serviceOf(p *types.Project, name string) *types.ServiceConfig {
	if s, ok := p.Services[name]; ok {
		return &s
	}
	if s, ok := p.DisabledServices[name]; ok {
		return &s
	}
	return nil
}

// lintProject is the hardening lint on the resolved project (allowlist,
// D-205 amendment). Every service: a digest-pinned image (the bekup
// services the x-bekup-image), no build; read-only root, cap_drop ALL,
// no-new-privileges and nothing else in security_opt, a numeric non-root
// user (except setup and the root init services), cap_add only from
// allowedCaps; no privileged mode (hooks included), host or shared
// namespaces, devices, volumes_from, runtime, cgroup_parent, sysctls,
// providers, pre_start containers or the API socket; binds only from
// inside the deployment directory (the runner also its Docker socket),
// named volumes without driver options; ports only on the bundled proxy;
// top-level secrets/configs files only inside the deployment directory;
// the setup service exactly as shipped.
func lintProject(p *types.Project, o lintOptions) []Finding {
	var out []Finding
	add := func(sev Severity, format string, args ...any) {
		out = append(out, Finding{Severity: sev, Path: o.label, Msg: fmt.Sprintf(format, args...)})
	}
	if len(p.Jobs)+len(p.DisabledJobs) > 0 {
		add(SeverityError, "`jobs` start containers outside the checked services; not allowed")
	}
	if len(p.Models) > 0 {
		add(SeverityError, "`models` are not allowed")
	}
	img, imgErr := imageref.ParsePinned(o.image)
	for _, svc := range allServices(p) {
		lintService(svc, o, imgErr == nil, img, add)
		if o.expected == nil {
			continue
		}
		if exp := serviceOf(o.expected, svc.Name); exp != nil {
			lintShipped(svc, exp, &o, add)
		} else {
			lintAdded(svc, &o, add)
		}
	}
	lintTopLevel(p, o, add)
	lintShippedTopLevel(p, &o, add)
	if o.expected != nil && serviceOf(o.expected, "setup") != nil && serviceOf(p, "setup") == nil {
		add(SeverityError, "the setup service is missing; restore it exactly as shipped with the %s variant", o.variant)
	}
	return out
}

func lintService(svc *types.ServiceConfig, o lintOptions, haveImage bool, bekupImage imageref.Ref, add func(Severity, string, ...any)) {
	name := svc.Name
	shown := bounded(name)
	deny := func(cond bool, key string) {
		if cond {
			add(SeverityError, "service %q: `%s` is not allowed (host access or a bypass of the hardening)", shown, key)
		}
	}
	// Image.
	ref, err := imageref.ParsePinned(svc.Image)
	isBekup := slices.Contains(bekupServices, name) || haveImage && err == nil && ref.Repo == bekupImage.Repo
	switch {
	case svc.Image == "":
		add(SeverityError, "service %q has no image (a digest-pinned reference is required)", shown)
	case err != nil:
		add(SeverityError, "service %q: image %q is not digest-pinned", shown, bounded(svc.Image))
	case isBekup && haveImage && svc.Image != bekupImage.String():
		add(SeverityError, "service %q: image %q is not the x-bekup-image %q", shown, bounded(svc.Image), o.image)
	}
	deny(svc.Build != nil || svc.Dockerfile != "", "build")
	deny(svc.Privileged, "privileged")
	deny(len(svc.Devices) > 0, "devices")
	deny(len(svc.DeviceCgroupRules) > 0, "device_cgroup_rules")
	deny(len(svc.VolumesFrom) > 0, "volumes_from")
	deny(len(svc.Gpus) > 0, "gpus")
	deny(len(svc.PreStart) > 0, "pre_start")
	deny(svc.Provider != nil, "provider")
	deny(svc.Develop != nil, "develop")
	deny(svc.UseAPISocket, "use_api_socket")
	deny(svc.Runtime != "", "runtime")
	deny(svc.CgroupParent != "", "cgroup_parent")
	deny(svc.Isolation != "", "isolation")
	deny(svc.CredentialSpec != nil, "credential_spec")
	deny(len(svc.StorageOpt) > 0, "storage_opt")
	deny(svc.VolumeDriver != "", "volume_driver")
	deny(len(svc.Models) > 0, "models")
	deny(len(svc.Sysctls) > 0, "sysctls")
	if d := svc.Deploy; d != nil {
		for _, r := range []*types.Resource{d.Resources.Limits, d.Resources.Reservations} {
			if r != nil {
				deny(len(r.Devices) > 0, "deploy.resources.*.devices")
				deny(len(r.GenericResources) > 0, "deploy.resources.*.generic_resources")
			}
		}
	}
	// Namespaces.
	for _, ns := range []struct{ key, val string }{{"network_mode", svc.NetworkMode}, {"net", svc.Net}} {
		if ns.val != "" && ns.val != "none" {
			add(SeverityError, "service %q: `%s: %s` shares another network namespace; only none (or the declared networks) is allowed", shown, ns.key, bounded(ns.val))
		}
	}
	if svc.Pid != "" {
		add(SeverityError, "service %q: `pid: %s` shares another process namespace; not allowed", shown, bounded(svc.Pid))
	}
	if !slices.Contains([]string{"", "none", "private"}, svc.Ipc) {
		add(SeverityError, "service %q: `ipc: %s` shares another IPC namespace; not allowed", shown, bounded(svc.Ipc))
	}
	if svc.Uts != "" {
		add(SeverityError, "service %q: `uts: %s` shares another UTS namespace; not allowed", shown, bounded(svc.Uts))
	}
	if !slices.Contains([]string{"", "private"}, svc.Cgroup) {
		add(SeverityError, "service %q: `cgroup: %s` shares the host's cgroup namespace; not allowed", shown, bounded(svc.Cgroup))
	}
	rootAllowed := name == "setup" || slices.Contains(rootInitServices, name)
	switch {
	case svc.UserNSMode == "":
	case svc.UserNSMode == "host" && name == "runner":
		add(SeverityWarning, "service %q: `userns_mode: host` leaves the daemon's user-namespace remapping (documented for the runner on userns-remap daemons, docs/installation/rootless-docker.md)", shown)
	case svc.UserNSMode == "host":
		add(SeverityError, "service %q: `userns_mode: host` is allowed only on the runner (docs/installation/rootless-docker.md)", shown)
	default:
		add(SeverityError, "service %q: `userns_mode: %s` is not allowed", shown, bounded(svc.UserNSMode))
	}
	// Hardening baseline.
	if !svc.ReadOnly {
		add(SeverityError, "service %q: the root file system must be read-only (`read_only: true`)", shown)
	}
	if !slices.ContainsFunc(svc.CapDrop, func(c string) bool { return normCap(c) == "ALL" }) {
		add(SeverityError, "service %q: `cap_drop` must contain ALL", shown)
	}
	for _, c := range svc.CapAdd {
		if !slices.Contains(allowedCaps(name), normCap(c)) {
			add(SeverityError, "service %q: capability %s is not allowed (allowed: %s)", shown, bounded(normCap(c)), strings.Join(allowedCaps(name), ", "))
		}
	}
	nnp := false
	for _, s := range svc.SecurityOpt {
		if isNoNewPrivileges(s) {
			nnp = true
			continue
		}
		add(SeverityError, "service %q: security_opt %q is not allowed (only no-new-privileges:true)", shown, bounded(s))
	}
	if !nnp {
		add(SeverityError, "service %q: security_opt must contain no-new-privileges:true", shown)
	}
	if !rootAllowed && !nonRootUser(svc.User) {
		add(SeverityError, "service %q: user %q is not a numeric non-root UID[:GID] (the image default may be root)", shown, bounded(svc.User))
	}
	if rootAllowed && name != "setup" && svc.NetworkMode != "none" {
		add(SeverityError, "service %q runs as root: it must keep `network_mode: none`", shown)
	}
	for _, h := range append(slices.Clone(svc.PostStart), svc.PreStop...) {
		if h.Privileged {
			add(SeverityError, "service %q: a privileged post_start/pre_stop hook is not allowed", shown)
		}
		if h.User != "" && !rootAllowed && !nonRootUser(h.User) {
			add(SeverityError, "service %q: a post_start/pre_stop hook runs as user %q (root or not numeric); not allowed", shown, bounded(h.User))
		}
	}
	// Mounts.
	for _, v := range svc.Volumes {
		lintVolume(svc, v, o, rootAllowed, add)
	}
	for _, f := range svc.EnvFiles {
		if !insideDir(o.root, f.Path) {
			add(SeverityError, "service %q: env_file %q is outside the deployment directory", shown, bounded(f.Path))
		}
	}
	for _, f := range svc.LabelFiles {
		if !insideDir(o.root, f) {
			add(SeverityError, "service %q: label_file %q is outside the deployment directory", shown, bounded(f))
		}
	}
	if len(svc.Ports) > 0 && !slices.Contains(proxyServices(o.variant), name) {
		add(SeverityError, "service %q publishes ports; in the %s variant only the bundled proxy may (serve, runner and postgres stay unreachable from outside)", shown, o.variant)
	}
	if rootAllowed && name != "setup" && len(svc.Ports) > 0 {
		add(SeverityError, "service %q runs as root and may not publish ports", shown)
	}
}

func lintVolume(svc *types.ServiceConfig, v types.ServiceVolumeConfig, o lintOptions, rootAllowed bool, add func(Severity, string, ...any)) {
	shown := bounded(svc.Name)
	switch v.Type {
	case types.VolumeTypeBind:
		src := filepath.Clean(v.Source)
		switch {
		case svc.Name == "runner" && dockerSocketRe.MatchString(src) && v.Target == runnerSocketTarget:
			if !v.ReadOnly {
				add(SeverityError, "service %q: mount the Docker socket read-only (`:ro`)", shown)
			}
			if v.Bind != nil && v.Bind.SELinux != "" {
				add(SeverityError, "service %q: no SELinux relabeling (`z`/`Z`) of the Docker socket", shown)
			}
		case strings.Contains(src, "docker.sock") || strings.Contains(src, "podman.sock"):
			add(SeverityError, "service %q mounts the container engine socket %q (root on the host); only the runner may, at %s", shown, bounded(src), runnerSocketTarget)
		case svc.Name == "setup" && src == filepath.Clean(o.root):
		case rootAllowed && svc.Name != "setup":
			add(SeverityError, "service %q runs as root and may only mount named volumes (bind %q)", shown, bounded(src))
		case !insideDir(o.root, src):
			add(SeverityError, "service %q bind-mounts %q from outside the deployment directory (host paths are not allowed)", shown, bounded(src))
		}
		if v.Bind != nil && !slices.Contains([]string{"", "private", "rprivate"}, v.Bind.Propagation) {
			add(SeverityError, "service %q: bind propagation %q is not allowed", shown, bounded(v.Bind.Propagation))
		}
		if v.Bind != nil && v.Bind.SELinux != "" && (svc.Name != "runner" || v.Target != runnerSocketTarget) {
			add(SeverityError, "service %q: SELinux relabeling (`z`/`Z`) of %s is not allowed (it relabels files on the host)", shown, shownPath(src))
		}
	case types.VolumeTypeVolume, types.VolumeTypeTmpfs:
	default:
		add(SeverityError, "service %q: a %q mount is not allowed (only binds inside the deployment directory, named volumes and tmpfs)", shown, bounded(v.Type))
	}
}

func lintTopLevel(p *types.Project, o lintOptions, add func(Severity, string, ...any)) {
	for _, name := range sortedKeys(p.Volumes) {
		v := p.Volumes[name]
		if v.Driver != "" && v.Driver != "local" || len(v.DriverOpts) > 0 {
			add(SeverityError, "volume %q: a volume driver or driver_opts (e.g. a bind of a host path or device) is not allowed", bounded(name))
		}
		if bool(v.External) {
			add(SeverityError, "volume %q: external volumes are not allowed (one could bind any host path)", bounded(name))
		}
	}
	for _, name := range sortedKeys(p.Networks) {
		n := p.Networks[name]
		if n.Driver != "" && n.Driver != "bridge" || len(n.DriverOpts) > 0 {
			add(SeverityError, "network %q: only bridge networks without driver_opts are allowed", bounded(name))
		}
		if n.Ipam.Driver != "" && n.Ipam.Driver != "default" {
			add(SeverityError, "network %q: IPAM driver %q is not allowed", bounded(name), bounded(n.Ipam.Driver))
		}
		if strings.EqualFold(n.Name, "host") || strings.EqualFold(n.Name, "none") || strings.EqualFold(name, "host") {
			add(SeverityError, "network %q: the %q network shares the host's or no network; use network_mode none or a declared network", bounded(name), bounded(n.Name))
		}
	}
	files := func(kind string, objs map[string]types.FileObjectConfig) {
		for _, name := range sortedKeys(objs) {
			c := objs[name]
			switch {
			case bool(c.External):
				add(SeverityError, "%s %q: external %s are not allowed", kind, bounded(name), kind)
			case c.Driver != "" || c.TemplateDriver != "" || len(c.DriverOpts) > 0:
				add(SeverityError, "%s %q: drivers are not allowed", kind, bounded(name))
			case c.File != "" && !insideDir(o.root, c.File):
				add(SeverityError, "%s %q: file %q is outside the deployment directory (host paths are not allowed)", kind, bounded(name), bounded(c.File))
			}
		}
	}
	secrets := make(map[string]types.FileObjectConfig, len(p.Secrets))
	for k := range p.Secrets {
		secrets[k] = types.FileObjectConfig(p.Secrets[k])
	}
	configs := make(map[string]types.FileObjectConfig, len(p.Configs))
	for k := range p.Configs {
		configs[k] = types.FileObjectConfig(p.Configs[k])
	}
	files("secret", secrets)
	files("config", configs)
}

// insideDir reports whether path (absolute after compose's path
// resolution) lies strictly below root.
func insideDir(root, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func normCap(c string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
}

func isNoNewPrivileges(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "no-new-privileges", "no-new-privileges:true", "no-new-privileges=true":
		return true
	default:
		return false
	}
}

// nonRootUser accepts only numeric "uid" or "uid:gid" with uid (and gid)
// other than 0: a name could resolve to root inside the image.
func nonRootUser(u string) bool {
	uid, gid, hasGID := strings.Cut(strings.TrimSpace(u), ":")
	ok := func(s string) bool {
		n, err := strconv.ParseUint(s, 10, 32)
		return err == nil && n != 0
	}
	return ok(uid) && (!hasGID || ok(gid))
}
