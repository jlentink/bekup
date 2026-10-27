package spec

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/preamble"
)

// Labels set on every container (and, by task 2.6, every per-run network);
// the orphan reaper selects by LabelManaged.
const (
	LabelManaged = "bekup.managed"
	LabelRunID   = "bekup.run_id"
)

// Fixed container settings.
const (
	// TmpfsSizeMiB is the size of the /tmp tmpfs (counted against memory).
	TmpfsSizeMiB = 64
	// ShmSizeMiB is the size of the private /dev/shm.
	ShmSizeMiB = 16
	// StopTimeoutSeconds is the grace period between SIGTERM and SIGKILL.
	StopTimeoutSeconds = 10
	// NoFile is the open-files limit (soft = hard).
	NoFile = 1024
	// NetworkNone is the network mode of offline runs.
	NetworkNone = "none"
	// Hostname is the container's fixed host name.
	Hostname = "bekup-run"
	// pathEnv is the fixed PATH (covers all official images).
	pathEnv = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	// maxArgs / maxArgBytes bound the argv from internal/engine.
	maxArgs     = 64
	maxArgBytes = 8 << 10
	// maxCPUs bounds docker.cpus.
	maxCPUs = 1024
	// MaxImageVolumes bounds the VOLUME paths an image may declare.
	MaxImageVolumes = 32
	// volumeTmpfsBytes is the size of the read-only tmpfs over an image
	// VOLUME path (one page).
	volumeTmpfsBytes = 4096
	// maxVolumePathBytes bounds one VOLUME path.
	maxVolumePathBytes = 4096
)

// Env is the complete environment the spec sets: constants only, never
// request data or secrets. It points every client at the tmpfs (HOME) and
// disables history files, login-path and service files the image might
// provide. Variables defined by the image itself (PATH is overridden;
// e.g. PG_VERSION, LANG) remain; they are image constants.
var env = []string{
	"HOME=/tmp",
	"PATH=" + pathEnv,
	"TZ=UTC",
	"PGPASSFILE=" + string(engine.FilePGPass),
	"PGSYSCONFDIR=" + engine.TmpDir,
	"PGSERVICEFILE=" + engine.TmpDir + "/pg_service.conf",
	"PSQLRC=/dev/null",
	"PSQL_HISTORY=/dev/null",
	"MYSQL_HISTFILE=/dev/null",
	"MYSQL_TEST_LOGIN_FILE=/dev/null",
}

// Env returns a copy of the fixed environment.
func Env() []string { return slices.Clone(env) }

// Limits are the per-container resource limits (runner.toml [docker]).
type Limits struct {
	MemoryMiB int
	CPUs      float64
	PidsLimit int
}

// LimitsFromConfig takes the limits from runner.toml.
func LimitsFromConfig(d config.RunnerDocker) Limits {
	return Limits{MemoryMiB: d.MemoryMiB, CPUs: d.CPUs, PidsLimit: d.PidsLimit}
}

// HostAlias maps the target's DNS name to the runner's per-run proxy
// address (ExtraHosts), so the client dials the proxy while TLS host-name
// verification still sees the real name (D-029).
type HostAlias struct {
	Name string
	IP   netip.Addr
}

// Params are the inputs of Build; all are produced by the runner itself.
type Params struct {
	// RunID is the run's UUID (bekup.run_id label).
	RunID string
	// Entry is the allowlisted catalog entry; Command must have been built
	// from it.
	Entry engine.Entry
	// Command is the internal/engine invocation (argv only is used here;
	// its Files go into the preamble).
	Command engine.Command
	// Network is the per-run internal network (task 2.6). Empty when
	// Offline.
	Network string
	// Offline runs the container without any network (NetworkMode
	// "none"): commands that only read stdin, such as pg_restore --list.
	Offline bool
	// HostAlias is optional: absent for IP-literal targets and offline
	// runs.
	HostAlias *HostAlias
	Limits    Limits
	// ImageVolumes are the image's declared VOLUME paths (Config.Volumes
	// of the image inspect). Each one is covered by a read-only, one-page
	// tmpfs (noexec, nosuid, nodev by Docker's tmpfs defaults), so Docker
	// never creates an anonymous volume that would outlive the run on the
	// host's disk and give the tool a writable, persistent directory.
	ImageVolumes []string
}

// Spec is the container specification for ContainerCreate.
type Spec struct {
	Config     *container.Config
	HostConfig *container.HostConfig
	Networking *network.NetworkingConfig
}

// ErrInvalidParams is wrapped by every Build error.
var ErrInvalidParams = errors.New("spec: invalid parameters")

var (
	runIDRe   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	networkRe = regexp.MustCompile(`^bekup-[a-z0-9][a-z0-9_.-]{0,62}$`)
)

func invalid(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidParams, msg)
}

// ImageRef returns the digest-only reference of an entry
// ("repository@sha256:...").
func ImageRef(e engine.Entry) string { return e.Repository() + "@" + e.Digest() }

// Build validates p and returns the hardened spec.
func Build(p Params) (Spec, error) {
	if err := check(p); err != nil {
		return Spec{}, err
	}
	uid := strconv.Itoa(p.Entry.UID())
	stop := StopTimeoutSeconds
	noInit := false
	pids := int64(p.Limits.PidsLimit)
	mem := int64(p.Limits.MemoryMiB) << 20

	cfg := &container.Config{
		Hostname:     Hostname,
		User:         uid + ":" + uid,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
		OpenStdin:    true,
		StdinOnce:    true,
		Env:          Env(),
		Cmd:          slices.Clone(p.Command.Argv),
		Healthcheck:  &container.HealthConfig{Test: []string{"NONE"}},
		Image:        ImageRef(p.Entry),
		WorkingDir:   "/tmp",
		Entrypoint:   preamble.Entrypoint(),
		Labels:       Labels(p.RunID),
		StopSignal:   "SIGTERM",
		StopTimeout:  &stop,
	}
	hc := &container.HostConfig{
		// Tool output is streamed over the attach connection only; no log
		// file on the daemon's disk keeps it.
		LogConfig:      container.LogConfig{Type: "none"},
		NetworkMode:    container.NetworkMode(p.Network),
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
		AutoRemove:     false,
		CapDrop:        []string{"ALL"},
		CgroupnsMode:   container.CgroupnsModePrivate,
		IpcMode:        container.IPCModePrivate,
		Privileged:     false,
		ReadonlyRootfs: true,
		// The default seccomp (and AppArmor/SELinux) profiles stay active:
		// no seccomp=, apparmor= or label= override is ever set.
		SecurityOpt: []string{"no-new-privileges:true"},
		Tmpfs: map[string]string{
			"/tmp": fmt.Sprintf("rw,noexec,nosuid,nodev,size=%dm,mode=0700,uid=%s,gid=%s", TmpfsSizeMiB, uid, uid),
		},
		ShmSize: ShmSizeMiB << 20,
		Init:    &noInit,
		Resources: container.Resources{
			Memory:     mem,
			MemorySwap: mem, // no swap
			NanoCPUs:   int64(math.Round(p.Limits.CPUs * 1e9)),
			PidsLimit:  &pids,
			Ulimits: []*container.Ulimit{
				{Name: "core", Soft: 0, Hard: 0}, // no core dumps with secrets
				{Name: "nofile", Soft: NoFile, Hard: NoFile},
			},
		},
	}
	for _, v := range volumeTargets(p.ImageVolumes) {
		hc.Mounts = append(hc.Mounts, mount.Mount{
			Type:     mount.TypeTmpfs,
			Target:   v,
			ReadOnly: true,
			TmpfsOptions: &mount.TmpfsOptions{
				SizeBytes: volumeTmpfsBytes,
				Mode:      0o555,
			},
		})
	}
	if p.Offline {
		hc.NetworkMode = container.NetworkMode(NetworkNone)
		return Spec{Config: cfg, HostConfig: hc, Networking: &network.NetworkingConfig{}}, nil
	}
	if p.HostAlias != nil {
		hc.ExtraHosts = []string{p.HostAlias.Name + ":" + p.HostAlias.IP.Unmap().String()}
	}
	nc := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{p.Network: {}},
	}
	return Spec{Config: cfg, HostConfig: hc, Networking: nc}, nil
}

// Labels returns the labels of a run's containers.
func Labels(runID string) map[string]string {
	return map[string]string{LabelManaged: "true", LabelRunID: runID}
}

func check(p Params) error {
	if !runIDRe.MatchString(p.RunID) {
		return invalid("run id must be a lowercase UUID")
	}
	e := p.Entry
	if e.IsZero() {
		return invalid("catalog entry required")
	}
	if e.UID() <= 0 {
		return invalid("catalog entry has no non-root uid")
	}
	if p.Command.Image != e.Image() {
		return invalid("command was not built from the catalog entry")
	}
	if err := checkArgv(e, p.Command.Argv); err != nil {
		return err
	}
	// Every psql run reads the \restrict line first (D-138); no other
	// tool gets a stdin prefix.
	if e.Kind() == engine.KindPostgres && p.Command.Argv[0] == e.Binaries().Client {
		if !engine.IsPSQLRestrictPrefix(p.Command.StdinPrefix) {
			return invalid("a psql command needs the restrict prefix")
		}
	} else if p.Command.StdinPrefix != nil {
		return invalid("only psql takes a stdin prefix")
	}
	if err := checkVolumes(p.ImageVolumes); err != nil {
		return err
	}
	for _, f := range p.Command.Files {
		if _, ok := preamble.TypeForFile(f); !ok {
			return invalid("command needs a file the preamble cannot write")
		}
	}
	if p.Offline {
		if p.Network != "" || p.HostAlias != nil {
			return invalid("an offline run has no network and no host alias")
		}
		if len(p.Command.Files) != 0 {
			return invalid("an offline run takes no preamble files")
		}
	} else if !networkRe.MatchString(p.Network) {
		return invalid("network must be a bekup per-run network name")
	}
	if a := p.HostAlias; a != nil {
		if err := engine.ValidateHost(a.Name); err != nil {
			return invalid("host alias name")
		}
		if _, err := netip.ParseAddr(a.Name); err == nil {
			return invalid("host alias name must be a DNS name")
		}
		ip := a.IP.Unmap()
		if !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
			return invalid("host alias address")
		}
	}
	l := p.Limits
	if l.MemoryMiB < 64 || l.MemoryMiB > 1<<20 {
		return invalid("memory limit out of range")
	}
	if !(l.CPUs > 0 && l.CPUs <= maxCPUs) {
		return invalid("cpu limit out of range")
	}
	if l.PidsLimit <= 0 || l.PidsLimit > 1<<22 {
		return invalid("pids limit out of range")
	}
	return nil
}

// CheckImageVolumes accepts at most MaxImageVolumes absolute, clean VOLUME
// paths other than "/" (an image declaring others is refused).
func CheckImageVolumes(vols []string) error { return checkVolumes(vols) }

// checkVolumes accepts absolute, clean VOLUME paths other than "/".
func checkVolumes(vols []string) error {
	if len(vols) > MaxImageVolumes {
		return invalid("the image declares too many volumes")
	}
	for _, v := range vols {
		if len(v) > maxVolumePathBytes || !strings.HasPrefix(v, "/") || path.Clean(v) != v || v == "/" ||
			strings.ContainsAny(v, "\x00\n") {
			return invalid("the image declares an unsupported volume path")
		}
	}
	return nil
}

// volumeTargets returns the sorted, deduplicated VOLUME paths that need
// a tmpfs: /tmp already is one.
func volumeTargets(vols []string) []string {
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		if v != "/tmp" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// checkArgv accepts only an engine-built argv: the tool is one of the
// entry's binaries, and no argument is empty, oversized or contains NUL.
func checkArgv(e engine.Entry, argv []string) error {
	if len(argv) == 0 || len(argv) > maxArgs {
		return invalid("argv length")
	}
	b := e.Binaries()
	tools := []string{b.Dump, b.DumpGlobals, b.Restore, b.Client}
	if argv[0] == "" || !slices.Contains(tools, argv[0]) {
		return invalid("argv tool is not a binary of the catalog entry")
	}
	for _, a := range argv {
		if len(a) > maxArgBytes || strings.IndexByte(a, 0) >= 0 {
			return invalid("argv argument")
		}
	}
	return nil
}
