package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/network"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// LabelRunner names the runner container that owns a per-run network or
// container; the reaper only removes another runner's leftovers when that
// runner no longer exists.
const LabelRunner = "bekup.runner"

// NetworkPrefix is the name prefix of every per-run network and container.
const NetworkPrefix = "bekup-run-"

// Timing defaults.
const (
	// KillGrace is the time between SIGTERM and SIGKILL on a timeout.
	KillGrace = spec.StopTimeoutSeconds * time.Second
	// cleanupTimeout bounds every cleanup and kill call.
	cleanupTimeout = 30 * time.Second
	// drainAfterKill bounds the wait for the output reader after a kill.
	drainAfterKill = 5 * time.Second
	// initRetry is the delay between initialization attempts.
	initRetry = 15 * time.Second
	// ReapInterval is the period of the orphan reaper.
	ReapInterval = 5 * time.Minute
	// ReapGrace is the minimum age of a leftover before the periodic
	// reaper removes it (a run being set up has no active entry yet for a
	// moment, and other runners' runs are only reaped once they are gone).
	ReapGrace = 2 * time.Minute
	// statusTimeout bounds the Docker ping of Status.
	statusTimeout = 2 * time.Second
	// NetworkRefreshInterval is the period at which the Docker networks
	// are handed to the target guard again (also before every run).
	NetworkRefreshInterval = time.Minute
)

// networkOptions harden every per-run bridge (D-132): inhibit_ipv4 keeps
// the gateway address off the host's bridge (so the host is unreachable
// and, for a /29 built around an IP-literal target, the host's routing to
// the real address is untouched), no masquerading, and inter-container
// traffic only between the run's two endpoints.
var networkOptions = map[string]string{
	"com.docker.network.bridge.inhibit_ipv4":         "true",
	"com.docker.network.bridge.enable_ip_masquerade": "false",
	"com.docker.network.bridge.enable_icc":           "true",
}

// NetworkOptions returns a copy of the fixed per-run network options.
func NetworkOptions() map[string]string {
	out := make(map[string]string, len(networkOptions))
	for k, v := range networkOptions {
		out[k] = v
	}
	return out
}

var (
	runIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	stepRE  = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

// Target is the single database (or, behind a tunnel, the database as the
// bastion sees it) a run may reach.
type Target struct {
	Host string
	Port int
	// VerifyHost: the client verifies the server certificate against Host
	// (TLS pinned / verify-full). IP-literal and localhost targets then
	// need the address mapping of D-132.
	VerifyHost bool
}

// ClientEndpoint is what the dump container's client must dial: the
// executor maps it to the per-run proxy.
type ClientEndpoint struct {
	Host string
	Port int
}

// RunInfo describes a started run (RunSpec.OnStart).
type RunInfo struct {
	Network     string
	Subnet      netip.Prefix
	Proxy       netip.AddrPort
	ClientIP    netip.Addr
	ClientHost  string
	ContainerID string
}

// RunSpec is one container run. Every field is produced by the runner's
// RPC handlers from a validated request; nothing here is trusted from
// serve directly.
type RunSpec struct {
	// RunID is the run's lowercase UUID.
	RunID string
	// Step optionally names one of several sequential containers of the
	// same run (e.g. the steps of a rename swap): [a-z0-9]{1,16}. The
	// per-run network and container are named bekup-run-<run id>-<step>,
	// so a step never collides with a predecessor whose cleanup is still
	// pending.
	Step string
	// Entry is the allowlisted catalog entry.
	Entry engine.Entry
	// Target is the one database the run may reach (zero when Offline).
	Target Target
	// Offline runs the container without any network: no per-run network,
	// proxy or tunnel (VerifyDump's pg_restore --list reads stdin only).
	// Target and Tunnel must be empty and the command takes no preamble
	// files.
	Offline bool
	// Tunnel optionally reaches Target through an SSH bastion. Run sets
	// its Dialer to the target allowlist guard and always destroys its
	// credentials.
	Tunnel *tunnel.Config
	// Command builds the engine invocation for the endpoint the client
	// must dial (the target's name, or the address chosen by D-132).
	Command func(ClientEndpoint) (engine.Command, error)
	// Material holds the preamble file contents. Run consumes it: every
	// slice is zeroed before Run returns.
	Material *preamble.Material
	// Stdin is the payload written after the preamble (restores); nil
	// closes stdin after the preamble. It must return once the RPC's
	// context is done. A read error other than io.EOF kills the container
	// so the tool never sees a clean end of input.
	Stdin io.Reader
	// Stdout receives output chunks of at most DefaultChunkBytes; it may
	// block (backpressure) and must not retain the slice. Stdout and Log
	// are called from one goroutine, never concurrently.
	Stdout func([]byte) error
	// Log receives sanitized, bounded and redacted stderr lines.
	Log func(string)
	// Redact lists known secret values removed from log lines.
	Redact [][]byte
	// Timeout bounds the run (0 or more than limits.max_run_duration =
	// limits.max_run_duration).
	Timeout time.Duration
	// OnStart is called once the container runs (optional).
	OnStart func(RunInfo)
}

// Result is the outcome of a run that finished without an executor error.
type Result struct {
	ExitCode int
	// PreambleFailed: the wrapper exited with preamble.ExitCode.
	PreambleFailed bool
	StdoutBytes    int64
	Log            LogStats
	Duration       time.Duration
}

// Options configures an Executor.
type Options struct {
	Config *config.Runner
	Docker Docker
	// Guard is the target allowlist the validator uses; the executor feeds
	// it the runner's own networks and every active per-run subnet.
	Guard  *targets.Guard
	Logger *slog.Logger
}

// Executor runs dump and restore containers on per-run internal networks.
type Executor struct {
	d               Docker
	guard           *targets.Guard
	log             *slog.Logger
	limits          spec.Limits
	maxRun          time.Duration
	maxLogLines     int
	maxLogLineBytes int
	pullTimeout     time.Duration
	configuredSelf  string
	listenIP        netip.Addr
	sem             chan struct{}
	dockerOK        atomic.Bool

	mu    sync.Mutex
	ready bool
	self  selfInfo
	base  []netip.Prefix
	pool  *subnetPool
	runs  map[string]*activeRun
	// offline holds the active offline runs (no network), keyed by name.
	offline map[string]string
	zombie  map[string]netip.Prefix // per-run subnets whose cleanup failed

	// Hooks, replaced by tests.
	now func() time.Time
	// hostInfra returns the runner's resolv.conf name servers and default
	// gateways (never usable as a /29 around an IP literal).
	hostInfra      func() []netip.Addr
	mountinfo      func() (io.ReadCloser, error)
	hostname       func() (string, error)
	localAddrs     func() ([]netip.Addr, error)
	killGrace      time.Duration
	cleanupTimeout time.Duration
	drainAfterKill time.Duration
	listen         func(ctx context.Context, addr netip.AddrPort) (net.Listener, error)
	chunkBytes     int
}

type activeRun struct {
	runID    string
	net      runNet
	upstream []netip.Prefix
}

// New builds an Executor; it does not contact Docker (Init / Start do).
func New(opts Options) (*Executor, error) {
	if opts.Config == nil || opts.Docker == nil || opts.Guard == nil || opts.Logger == nil {
		return nil, errors.New("executor: config, docker, guard and logger are required")
	}
	cfg := opts.Config
	poolStr := cfg.Docker.RunSubnetPool
	if poolStr == "" {
		poolStr = config.DefaultRunSubnetPool
	}
	poolPrefix, err := netip.ParsePrefix(poolStr)
	if err != nil {
		return nil, fmt.Errorf("executor: docker.run_subnet_pool: %w", err)
	}
	pool, err := newSubnetPool(poolPrefix)
	if err != nil {
		return nil, err
	}
	if cfg.Limits.MaxConcurrentRuns <= 0 || cfg.Limits.MaxRunDuration.D() <= 0 {
		return nil, errors.New("executor: limits must be positive")
	}
	e := &Executor{
		d:               opts.Docker,
		guard:           opts.Guard,
		log:             opts.Logger,
		limits:          spec.LimitsFromConfig(cfg.Docker),
		maxRun:          cfg.Limits.MaxRunDuration.D(),
		maxLogLines:     max(cfg.Limits.MaxLogLines, 1),
		maxLogLineBytes: max(cfg.Limits.MaxLogLineBytes, 256),
		pullTimeout:     cfg.Docker.PullTimeout.D(),
		configuredSelf:  cfg.Docker.SelfContainer,
		sem:             make(chan struct{}, cfg.Limits.MaxConcurrentRuns),
		pool:            pool,
		runs:            map[string]*activeRun{},
		offline:         map[string]string{},
		zombie:          map[string]netip.Prefix{},
		now:             time.Now,
		hostInfra:       hostInfraAddrs,
		mountinfo:       readMountinfo,
		hostname:        os.Hostname,
		localAddrs:      localAddrs,
		killGrace:       KillGrace,
		cleanupTimeout:  cleanupTimeout,
		drainAfterKill:  drainAfterKill,
		chunkBytes:      DefaultChunkBytes,
	}
	e.listen = func(ctx context.Context, addr netip.AddrPort) (net.Listener, error) {
		return listenRetry(ctx, addr, listenRetryFor)
	}
	if e.pullTimeout <= 0 {
		e.pullTimeout = 10 * time.Minute
	}
	if host, _, err := net.SplitHostPort(cfg.Listen.Address); err == nil {
		if a, err := netip.ParseAddr(host); err == nil && !a.IsUnspecified() {
			e.listenIP = a.Unmap()
		}
	}
	// Until Init knows the runner's networks, the pool alone is "own".
	e.base = []netip.Prefix{pool.pool}
	e.mu.Lock()
	err = e.applyOwnLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return e, nil
}

// applyOwnLocked publishes base + active + zombie subnets and upstream
// reservations to the guard as the runner's own networks.
func (e *Executor) applyOwnLocked() error {
	own := slices.Clone(e.base)
	for _, r := range e.runs {
		own = append(own, r.net.Subnet)
	}
	for _, p := range e.zombie {
		own = append(own, p)
	}
	return e.guard.SetOwnNetworks(own)
}

// IsRunAddress reports whether a lies in the subnet pool or an active (or
// not yet cleaned up) per-run network. The gRPC listener refuses such
// peers, so a dump container can never talk to the runner's API.
func (e *Executor) IsRunAddress(a netip.Addr) bool {
	a = a.Unmap()
	if e.pool.contains(a) {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range e.runs {
		if r.net.Subnet.Contains(a) {
			return true
		}
	}
	for _, p := range e.zombie {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// SelfID returns the runner's own container ID ("" before Init).
func (e *Executor) SelfID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.self.ID
}

// Ready reports whether Init succeeded.
func (e *Executor) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}

// Status reports the Docker state for Health.
func (e *Executor) Status(ctx context.Context) runnerpb.DockerStatus {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	if err := e.d.Ping(ctx); err != nil {
		e.dockerOK.Store(false)
		return runnerpb.DockerStatus_DOCKER_STATUS_UNREACHABLE
	}
	e.dockerOK.Store(true)
	if !e.Ready() {
		return runnerpb.DockerStatus_DOCKER_STATUS_UNKNOWN
	}
	return runnerpb.DockerStatus_DOCKER_STATUS_OK
}

// Init pings Docker, detects and verifies the runner's own container,
// derives the runner's own networks (the control network that holds
// listen.address, every own address and the subnet pool) and reaps the
// leftovers of earlier runs.
func (e *Executor) Init(ctx context.Context) error {
	if err := e.d.Ping(ctx); err != nil {
		e.dockerOK.Store(false)
		return fmt.Errorf("%w: ping: %w", ErrNotReady, err)
	}
	e.dockerOK.Store(true)
	ver, err := e.d.ServerVersion(ctx)
	if err != nil {
		return fmt.Errorf("%w: docker version: %w", ErrNotReady, err)
	}
	if err := CheckDockerVersion(ver); err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	var mi io.Reader
	if rc, err := e.mountinfo(); err == nil {
		defer func() { discardErr(rc.Close()) }()
		mi = rc
	}
	host, err := e.hostname()
	if err != nil {
		host = ""
	}
	local, err := e.localAddrs()
	if err != nil {
		return fmt.Errorf("%w: interface addresses: %w", ErrNotReady, err)
	}
	self, err := detectSelf(ctx, e.d, selfCandidates(e.configuredSelf, mi, host), local)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	base := []netip.Prefix{e.pool.pool}
	for name, ep := range self.Networks {
		if ep.IP.IsValid() {
			base = append(base, netip.PrefixFrom(ep.IP, ep.IP.BitLen()))
		}
		if strings.HasPrefix(name, NetworkPrefix) || !e.listenIP.IsValid() || ep.NetworkID == "" {
			continue
		}
		ni, err := e.d.NetworkInspect(ctx, ep.NetworkID)
		if err != nil {
			return fmt.Errorf("%w: inspect own network: %w", ErrNotReady, err)
		}
		for _, s := range ni.Subnets {
			if s.Contains(e.listenIP) {
				base = append(base, s)
			}
		}
	}
	e.mu.Lock()
	e.self = self
	e.base = base
	err = e.applyOwnLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := e.refreshDockerNetworks(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	// The startup reap runs before any run is admitted (ready is still
	// false, so Run and makePlan refuse): nothing it removes can belong
	// to a run of this process.
	if err := e.reap(ctx, true); err != nil {
		e.log.WarnContext(ctx, "runner startup reap incomplete", slog.String("error", err.Error()))
	}
	e.mu.Lock()
	e.ready = true
	e.mu.Unlock()
	e.log.InfoContext(ctx, "runner executor ready",
		slog.String("self_container", shortID(self.ID)),
		slog.String("docker_version", ver),
		slog.Int("own_networks", len(base)))
	return nil
}

// refreshDockerNetworks hands every Docker network's subnets and
// gateways to the target guard (D-138): they are denied unless an
// allow_cidrs entry inside the subnet (or the gateway's exact /32)
// allows them.
func (e *Executor) refreshDockerNetworks(ctx context.Context) error {
	nets, err := e.d.NetworkList(ctx, "")
	if err != nil {
		return fmt.Errorf("list networks: %w", err)
	}
	e.setDockerNetworks(nets)
	return nil
}

func (e *Executor) setDockerNetworks(nets []NetworkInfo) {
	var subnets []netip.Prefix
	var gateways []netip.Addr
	for i := range nets {
		subnets = append(subnets, nets[i].Subnets...)
		gateways = append(gateways, nets[i].Gateways...)
	}
	e.guard.SetDockerNetworks(subnets, gateways)
}

// Start initializes the executor (retrying while Docker is unavailable)
// and then runs the orphan reaper every ReapInterval until ctx is done.
func (e *Executor) Start(ctx context.Context) {
	var last string
	for {
		err := e.Init(ctx)
		if err == nil {
			break
		}
		if msg := err.Error(); msg != last {
			e.log.WarnContext(ctx, "runner executor not ready; retrying", slog.String("error", msg))
			last = msg
		}
		t := time.NewTimer(initRetry)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	tick := time.NewTicker(ReapInterval)
	defer tick.Stop()
	refresh := time.NewTicker(NetworkRefreshInterval)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			if err := e.refreshDockerNetworks(ctx); err != nil {
				e.log.WarnContext(ctx, "runner docker network refresh failed", slog.String("error", err.Error()))
			}
		case <-tick.C:
			if err := e.Reap(ctx, false); err != nil {
				e.log.WarnContext(ctx, "runner reap incomplete", slog.String("error", err.Error()))
			}
		}
	}
}

// PrePull makes sure the entry's image is present (by digest only) and
// reports whether it already was.
func (e *Executor) PrePull(ctx context.Context, entry engine.Entry) (alreadyPresent bool, err error) {
	if entry.IsZero() {
		return false, fmt.Errorf("%w: catalog entry required", ErrInvalidSpec)
	}
	ref := spec.ImageRef(entry)
	if strings.Contains(ref, "@sha256:") {
		if info, err := e.d.ImageInspect(ctx, ref); err == nil && info.Present {
			if err := spec.CheckImageVolumes(info.Volumes); err != nil {
				return true, fmt.Errorf("%w: %w", ErrImage, err)
			}
			return true, nil
		}
	}
	_, err = e.ensureImage(ctx, entry)
	return false, err
}

// ensureImage makes sure the digest-pinned image is present and returns
// its declared VOLUME paths (each gets a read-only tmpfs in the spec, so
// no anonymous volume is created).
func (e *Executor) ensureImage(ctx context.Context, entry engine.Entry) ([]string, error) {
	ref := spec.ImageRef(entry)
	if !strings.Contains(ref, "@sha256:") {
		return nil, fmt.Errorf("%w: image reference is not digest-pinned", ErrImage)
	}
	info, err := e.d.ImageInspect(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect: %w", ErrImage, err)
	}
	if !info.Present {
		pctx, cancel := context.WithTimeout(ctx, e.pullTimeout)
		defer cancel()
		e.log.InfoContext(ctx, "runner pulling image", slog.String("image", ref))
		if err := e.d.ImagePull(pctx, ref); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: pull: %w", ErrImage, err)
		}
		if info, err = e.d.ImageInspect(ctx, ref); err != nil || !info.Present {
			return nil, fmt.Errorf("%w: missing after pull", ErrImage)
		}
	}
	if err := spec.CheckImageVolumes(info.Volumes); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImage, err)
	}
	return info.Volumes, nil
}

func (rs *RunSpec) check() error {
	switch {
	case !runIDRE.MatchString(rs.RunID):
		return fmt.Errorf("%w: run id", ErrInvalidSpec)
	case rs.Step != "" && !stepRE.MatchString(rs.Step):
		return fmt.Errorf("%w: step", ErrInvalidSpec)
	case rs.Entry.IsZero():
		return fmt.Errorf("%w: catalog entry", ErrInvalidSpec)
	case rs.Offline && (rs.Target != Target{} || rs.Tunnel != nil):
		return fmt.Errorf("%w: an offline run has no target", ErrInvalidSpec)
	case rs.Offline && (rs.Command == nil || rs.Stdout == nil || rs.Timeout < 0):
		return fmt.Errorf("%w: command and stdout are required", ErrInvalidSpec)
	case rs.Offline:
		return nil
	case engine.ValidateHost(rs.Target.Host) != nil:
		return fmt.Errorf("%w: target host", ErrInvalidSpec)
	case rs.Target.Port < 1 || rs.Target.Port > 65535:
		return fmt.Errorf("%w: target port", ErrInvalidSpec)
	case rs.Command == nil || rs.Stdout == nil:
		return fmt.Errorf("%w: command and stdout are required", ErrInvalidSpec)
	case rs.Timeout < 0:
		return fmt.Errorf("%w: timeout", ErrInvalidSpec)
	}
	if t := rs.Tunnel; t != nil && (t.TargetHost != rs.Target.Host || t.TargetPort != rs.Target.Port) {
		return fmt.Errorf("%w: tunnel target differs from the run target", ErrInvalidSpec)
	}
	return nil
}

// destroyMaterial zeroes every preamble input of the run.
func destroyMaterial(m *preamble.Material) {
	if m == nil {
		return
	}
	for _, b := range [][]byte{m.PGPass, m.MyCnf, m.CA, m.ClientCert, m.ClientKey} {
		crypto.Zero(b)
	}
}

func destroyTunnelAuth(t *tunnel.Config) {
	if t == nil {
		return
	}
	t.Auth.PrivateKey.Destroy()
	t.Auth.Passphrase.Destroy()
	t.Auth.Password.Destroy()
}

// bg returns a context for cleanup calls that survives the run's
// cancellation but is bounded.
func (e *Executor) bg(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), e.cleanupTimeout)
}

// plan is the network layout of one run.
type plan struct {
	net        runNet
	clientHost string
	alias      *spec.HostAlias
}

// isLoopbackName reports names that resolve to loopback inside any
// container regardless of ExtraHosts.
func isLoopbackName(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// makePlan chooses the per-run network and what the client dials (D-132):
//   - a DNS name: a /28 from the pool, ExtraHosts name -> proxy address;
//     the client dials the name and verifies the certificate against it;
//   - an IP literal or localhost without host verification: a /28 from
//     the pool; the client dials the proxy address directly;
//   - an IP literal with host verification behind an SSH tunnel: a /29
//     built around the literal, the runner's proxy holds that exact
//     address, so the client dials the literal and verifies the IP SAN;
//   - an IP literal (or localhost) with host verification and no tunnel:
//     ErrUnmappableTarget (the runner cannot hold an address it must
//     also dial).
//
// The chosen subnet and the upstream addresses are registered as the
// runner's own networks until release.
func (e *Executor) makePlan(ctx context.Context, rs *RunSpec, name string) (plan, func(failed bool), error) {
	host := strings.TrimSuffix(strings.TrimPrefix(rs.Target.Host, "["), "]")
	literal, err := netip.ParseAddr(host)
	isLiteral := err == nil
	loopName := !isLiteral && isLoopbackName(host)

	var dockerSubnets []netip.Prefix
	nets, err := e.d.NetworkList(ctx, "")
	if err != nil {
		return plan{}, nil, fmt.Errorf("%w: list networks: %w", ErrDocker, err)
	}
	for i := range nets {
		dockerSubnets = append(dockerSubnets, nets[i].Subnets...)
		for _, g := range nets[i].Gateways {
			dockerSubnets = append(dockerSubnets, netip.PrefixFrom(g, g.BitLen()))
		}
	}
	// The guard sees the current Docker networks before the upstream is
	// checked.
	e.setDockerNetworks(nets)

	// Upstream addresses this run will dial itself (direct target or the
	// bastion); validated by the guard now and re-checked at every dial.
	upHost := rs.Target.Host
	if rs.Tunnel != nil {
		upHost = rs.Tunnel.Host
	}
	addrs, err := e.guard.Resolve(ctx, upHost)
	if err != nil {
		return plan{}, nil, err
	}
	upstream := make([]netip.Prefix, 0, len(addrs))
	for _, a := range addrs {
		a = a.WithZone("").Unmap()
		upstream = append(upstream, netip.PrefixFrom(a, a.BitLen()))
	}

	var infra []netip.Prefix
	if isLiteral && rs.Target.VerifyHost && rs.Tunnel != nil {
		for _, a := range e.hostInfra() {
			a = a.WithZone("").Unmap()
			infra = append(infra, netip.PrefixFrom(a, a.BitLen()))
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ready {
		return plan{}, nil, ErrNotReady
	}
	if _, dup := e.runs[name]; dup || e.offline[name] != "" {
		return plan{}, nil, ErrDuplicateRun
	}
	var busy []netip.Prefix
	for _, r := range e.runs {
		busy = append(busy, r.net.Subnet)
		busy = append(busy, r.upstream...)
	}
	for _, p := range e.zombie {
		busy = append(busy, p)
	}
	// A direct or bastion address inside another run's /29 is refused by
	// the guard (own networks); check it here too for a clear error.
	for _, u := range upstream {
		for _, r := range e.runs {
			if r.net.around && r.net.Subnet.Overlaps(u) {
				return plan{}, nil, ErrTargetBusy
			}
		}
	}

	var p plan
	switch {
	case !isLiteral && !loopName:
		rn, err := e.pool.acquire(append(dockerSubnets, busy...))
		if err != nil {
			return plan{}, nil, err
		}
		p = plan{net: rn, clientHost: host, alias: &spec.HostAlias{Name: host, IP: rn.Proxy}}
	case !rs.Target.VerifyHost:
		rn, err := e.pool.acquire(append(dockerSubnets, busy...))
		if err != nil {
			return plan{}, nil, err
		}
		p = plan{net: rn, clientHost: rn.Proxy.String()}
	case rs.Tunnel == nil || loopName:
		return plan{}, nil, ErrUnmappableTarget
	default:
		// The run's own bastion inside the /29 would be routed into the
		// per-run network: that is permanent, not busy. So would the
		// runner's name servers and default gateways (D-138).
		forbidden := append(append(append(slices.Clone(e.base), dockerSubnets...), upstream...), infra...)
		rn, err := aroundTarget(literal, forbidden, busy)
		if err != nil {
			return plan{}, nil, err
		}
		p = plan{net: rn, clientHost: rn.Proxy.String()}
	}
	e.runs[name] = &activeRun{runID: rs.RunID, net: p.net, upstream: upstream}
	if err := e.applyOwnLocked(); err != nil {
		delete(e.runs, name)
		e.pool.release(p.net)
		return plan{}, nil, err
	}
	release := func(failed bool) {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.runs, name)
		if failed {
			// The network may still exist (or the runner may still be
			// attached): keep the subnet reserved and denied until the
			// reaper removed it.
			e.zombie[name] = p.net.Subnet
		}
		e.pool.release(p.net)
		if err := e.applyOwnLocked(); err != nil {
			e.log.ErrorContext(ctx, "runner own networks update failed", slog.String("error", err.Error()))
		}
	}
	return p, release, nil
}

// Run executes one container run: per-run internal network, self-join,
// proxy (or SSH tunnel) to the one target, pull-if-missing, hardened
// container with ExtraHosts, preamble + payload on stdin, bounded output
// streaming, timeout/cancel kill, and cleanup of every resource on every
// path (also on panics). A non-zero exit code is not an error.
func (e *Executor) Run(ctx context.Context, rs RunSpec) (res Result, err error) {
	defer destroyMaterial(rs.Material)
	defer destroyTunnelAuth(rs.Tunnel)
	if err := rs.check(); err != nil {
		return Result{}, err
	}
	if !e.Ready() {
		return Result{}, ErrNotReady
	}
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	default:
		return Result{}, ErrBusy
	}
	started := e.now()
	timeout := rs.Timeout
	if timeout <= 0 || timeout > e.maxRun {
		timeout = e.maxRun
	}
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, ErrTimeout)
	defer cancel()

	vols, err := e.ensureImage(ctx, rs.Entry)
	if err != nil {
		return Result{}, err
	}

	netName := rs.name()
	var (
		p       plan
		release func(failed bool)
	)
	if rs.Offline {
		release, err = e.reserveOffline(rs.RunID, netName)
	} else {
		p, release, err = e.makePlan(ctx, &rs, netName)
	}
	if err != nil {
		return Result{}, err
	}
	cleanupFailed := false
	defer func() { release(cleanupFailed) }()

	ep := ClientEndpoint{Host: p.clientHost, Port: rs.Target.Port}
	cmd, err := rs.Command(ep)
	if err != nil {
		return Result{}, fmt.Errorf("%w: command: %w", ErrInvalidSpec, err)
	}
	// The psql restrict line is consumed with the preamble (zeroed).
	defer crypto.Zero(cmd.StdinPrefix)
	params := spec.Params{
		RunID: rs.RunID, Entry: rs.Entry, Command: cmd, Network: netName,
		HostAlias: p.alias, Limits: e.limits, ImageVolumes: vols,
	}
	if rs.Offline {
		params.Network, params.Offline = "", true
	}
	sp, err := spec.Build(params)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}
	e.mu.Lock()
	selfID := e.self.ID
	e.mu.Unlock()
	sp.Config.Labels[LabelRunner] = selfID
	if !rs.Offline {
		sp.Networking.EndpointsConfig[netName].IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: p.net.Client}
	}

	recs, err := preamble.ForCommand(cmd, rs.Material)
	if err != nil {
		return Result{}, fmt.Errorf("%w: preamble: %w", ErrInvalidSpec, err)
	}
	enc, err := preamble.Encode(recs)
	if err != nil {
		return Result{}, fmt.Errorf("%w: preamble: %w", ErrInvalidSpec, err)
	}
	// stdin: the preamble, then the command's prefix (psql \restrict),
	// then the payload.
	pre := make([]byte, 0, len(enc)+len(cmd.StdinPrefix))
	pre = append(append(pre, enc...), cmd.StdinPrefix...)
	crypto.Zero(enc)
	preOwned := true // false once the stdin writer owns pre
	defer func() {
		if preOwned {
			crypto.Zero(pre)
		}
	}()
	destroyMaterial(rs.Material)

	var proxyAddr netip.AddrPort
	if !rs.Offline {
		var cleanup func()
		proxyAddr, cleanup, err = e.connectRun(ctx, &rs, p, netName, selfID, &cleanupFailed)
		defer cleanup()
		if err != nil {
			return Result{}, err
		}
	}

	// (5) the hardened container.
	id, err := e.d.ContainerCreate(ctx, netName, sp)
	if err != nil {
		return Result{}, fmt.Errorf("%w: create container: %w", ErrDocker, err)
	}
	defer func() {
		cctx, ccancel := e.bg(ctx)
		defer ccancel()
		if err := e.d.ContainerRemove(cctx, id); err != nil && !IsNotFound(err) {
			cleanupFailed = true
			e.log.ErrorContext(ctx, "runner container cleanup failed", slog.String("run_id", rs.RunID), slog.String("error", err.Error()))
		}
	}()

	// (6) attach before start.
	att, err := e.d.ContainerAttach(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("%w: attach: %w", ErrDocker, err)
	}
	var attOnce sync.Once
	closeAtt := func() { attOnce.Do(func() { discardErr(att.Close()) }) }
	defer closeAtt()

	if err := e.d.ContainerStart(ctx, id); err != nil {
		return Result{}, fmt.Errorf("%w: start: %w", ErrDocker, err)
	}
	if rs.OnStart != nil {
		rs.OnStart(RunInfo{Network: netName, Subnet: p.net.Subnet, Proxy: proxyAddr, ClientIP: p.net.Client, ClientHost: p.clientHost, ContainerID: id})
	}

	res, err = e.stream(ctx, &rs, id, att, closeAtt, pre, &preOwned)
	res.Duration = e.now().Sub(started)
	e.log.InfoContext(ctx, "runner run finished",
		slog.String("run_id", rs.RunID), slog.String("image", rs.Entry.Key()),
		slog.Int("exit_code", res.ExitCode), slog.Duration("duration", res.Duration),
		slog.Bool("offline", rs.Offline), slog.Bool("failed", err != nil))
	return res, err
}

// reserveOffline registers an offline run (duplicate check, reaper).
func (e *Executor) reserveOffline(runID, name string) (func(failed bool), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ready {
		return nil, ErrNotReady
	}
	if _, dup := e.runs[name]; dup || e.offline[name] != "" {
		return nil, ErrDuplicateRun
	}
	e.offline[name] = runID
	return func(bool) {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.offline, name)
	}, nil
}

// name is the run's network and container name.
func (rs *RunSpec) name() string {
	if rs.Step != "" {
		return NetworkPrefix + rs.RunID + "-" + rs.Step
	}
	return NetworkPrefix + rs.RunID
}

// connectRun sets up the run's upstream (direct or SSH tunnel), the
// per-run network, the runner's membership and the proxy. cleanup must be
// called (also on error); it tears down what was set up, in reverse order.
func (e *Executor) connectRun(ctx context.Context, rs *RunSpec, p plan, netName, selfID string, cleanupFailed *bool) (netip.AddrPort, func(), error) {
	var stack []func()
	cleanup := func() {
		for i := len(stack) - 1; i >= 0; i-- {
			stack[i]()
		}
	}
	// Upstream: direct through the guard, or the SSH tunnel whose bastion
	// leg is dialed through the guard.
	var dial dialFunc
	if rs.Tunnel != nil {
		tcfg := *rs.Tunnel
		tcfg.Dialer = e.guard
		if tcfg.Logger == nil {
			tcfg.Logger = e.log
		}
		rs.Tunnel.Auth = tunnel.Auth{} // ownership moves to tcfg; Dial destroys it
		tun, err := tunnel.Dial(ctx, &tcfg)
		if err != nil {
			return netip.AddrPort{}, cleanup, err
		}
		stack = append(stack, func() { discardErr(tun.Close()) })
		dial = tun.DialTarget
	} else {
		addr := net.JoinHostPort(rs.Target.Host, strconv.Itoa(rs.Target.Port))
		dial = func(ctx context.Context) (net.Conn, error) { return e.guard.DialContext(ctx, "tcp", addr) }
	}

	// (1) per-run internal network.
	netID, err := e.d.NetworkCreate(ctx, NetworkRequest{
		Name: netName, Subnet: p.net.Subnet, Gateway: p.net.Gateway, Internal: true,
		Options: NetworkOptions(), Labels: map[string]string{
			spec.LabelManaged: "true", spec.LabelRunID: rs.RunID, LabelRunner: selfID,
		},
	})
	if err != nil {
		return netip.AddrPort{}, cleanup, fmt.Errorf("%w: create network: %w", ErrDocker, err)
	}
	stack = append(stack, func() {
		cctx, ccancel := e.bg(ctx)
		defer ccancel()
		if err := e.d.NetworkRemove(cctx, netID); err != nil && !IsNotFound(err) {
			*cleanupFailed = true
			e.log.ErrorContext(ctx, "runner network cleanup failed", slog.String("run_id", rs.RunID), slog.String("error", err.Error()))
		}
	})

	// (2) self-join with the fixed proxy address.
	if err := e.d.NetworkConnect(ctx, netID, selfID, p.net.Proxy); err != nil {
		return netip.AddrPort{}, cleanup, fmt.Errorf("%w: join network: %w", ErrDocker, err)
	}
	stack = append(stack, func() {
		cctx, ccancel := e.bg(ctx)
		defer ccancel()
		if err := e.d.NetworkDisconnect(cctx, netID, selfID); err != nil && !IsNotFound(err) {
			*cleanupFailed = true
			e.log.ErrorContext(ctx, "runner network leave failed", slog.String("run_id", rs.RunID), slog.String("error", err.Error()))
		}
	})

	// (3) per-run proxy, reachable from the dump container only.
	ln, err := e.listen(ctx, netip.AddrPortFrom(p.net.Proxy, uint16(rs.Target.Port))) //nolint:gosec // port validated 1-65535
	if err != nil {
		return netip.AddrPort{}, cleanup, err
	}
	px := startProxy(ctx, ln, p.net.Client, dial, e.log)
	stack = append(stack, px.Close)
	return px.Addr(), cleanup, nil
}

type waitResult struct {
	code int64
	err  error
}

// stream feeds stdin, demultiplexes the output, waits for the exit code
// and kills the container on timeout, cancellation, stdin or output
// failure.
func (e *Executor) stream(ctx context.Context, rs *RunSpec, id string, att Attachment, closeAtt func(), pre []byte, preOwned *bool) (Result, error) {
	reg := logging.NewRegistry()
	longest := 0
	unregister := make([]func(), 0, len(rs.Redact))
	defer func() {
		for _, u := range unregister {
			u()
		}
	}()
	for _, s := range rs.Redact {
		unregister = append(unregister, reg.Register(s))
		longest = max(longest, len(s))
	}
	finished := &atomic.Bool{}
	out := newChunkWriter(e.chunkBytes, rs.Stdout, finished)
	logw := newLineLogger(rs.Log, reg, e.maxLogLines, e.maxLogLineBytes, longest)
	logw.finished = finished

	stdinErr := make(chan error, 1)
	stdinStop := make(chan struct{})
	defer close(stdinStop)
	*preOwned = false
	go writeStdin(att, pre, rs.Stdin, stdinStop, stdinErr)

	readerDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(out, logw, att)
		if err == nil {
			err = out.flush()
		}
		logw.Flush()
		readerDone <- err
	}()

	waitCtx, cancelWait := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWait()
	waitDone := make(chan waitResult, 1)
	go func() {
		code, err := e.d.ContainerWait(waitCtx, id)
		waitDone <- waitResult{code: code, err: err}
	}()

	var (
		code      int64 = -1
		exited    bool
		readerEnd bool
		runErr    error
	)
loop:
	for {
		select {
		case w := <-waitDone:
			waitDone = nil
			if w.err != nil {
				runErr = fmt.Errorf("%w: wait: %w", ErrDocker, w.err)
				e.kill(ctx, id, false, nil, closeAtt)
				break loop
			}
			code, exited = w.code, true
			if readerEnd {
				break loop
			}
		case err := <-readerDone:
			readerDone = nil
			readerEnd = true
			if err != nil {
				if !errors.Is(err, ErrOutput) {
					err = fmt.Errorf("%w: output: %w", ErrDocker, err)
				}
				runErr = err
				if !exited {
					e.kill(ctx, id, false, waitDone, closeAtt)
				}
				break loop
			}
			if exited {
				break loop
			}
		case err := <-stdinErr:
			runErr = err
			if !exited {
				e.kill(ctx, id, false, waitDone, closeAtt)
			}
			break loop
		case <-ctx.Done():
			cause := context.Cause(ctx)
			if errors.Is(cause, ErrTimeout) || errors.Is(cause, context.DeadlineExceeded) {
				runErr = ErrTimeout
				if !exited {
					e.kill(ctx, id, true, waitDone, closeAtt)
				}
			} else {
				runErr = ctx.Err()
				if !exited {
					e.kill(ctx, id, false, waitDone, closeAtt)
				}
			}
			break loop
		}
	}
	finished.Store(true)
	closeAtt()
	readerGone := readerDone == nil
	if !readerGone {
		t := time.NewTimer(e.drainAfterKill)
		select {
		case <-readerDone:
			readerGone = true
		case <-t.C:
			e.log.WarnContext(ctx, "runner output reader still blocked after the run", slog.String("run_id", rs.RunID))
		}
		t.Stop()
	}
	res := Result{ExitCode: int(code)}
	if readerGone { // the reader owns the counters until it returned
		res.StdoutBytes, res.Log = out.total, logw.stats
	}
	if runErr != nil {
		return res, runErr
	}
	res.PreambleFailed = code == preamble.ExitCode
	return res, nil
}

// writeStdin writes the preamble, zeroes it, then streams the payload.
// stdin is closed only after a clean end of the payload; on a read error
// the run is killed instead, so the tool never sees a truncated input as
// complete.
func writeStdin(att Attachment, pre []byte, in io.Reader, stop <-chan struct{}, errc chan<- error) {
	_, err := att.Write(pre)
	crypto.Zero(pre)
	if err != nil {
		return // the container exited or the run is being torn down
	}
	if in != nil {
		buf := make([]byte, 64<<10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, rerr := in.Read(buf)
			if n > 0 {
				if _, werr := att.Write(buf[:n]); werr != nil {
					return
				}
			}
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				errc <- fmt.Errorf("%w: %w", ErrStdin, rerr)
				return
			}
		}
	}
	discardErr(att.CloseWrite())
}

// kill stops the container: SIGTERM, the grace period, then SIGKILL when
// graceful, else SIGKILL at once; it waits (bounded) for the exit. The
// attach connection is closed right before SIGKILL: Docker's SIGKILL waits
// for the container to stop, which blocks while the attached output is not
// drained (the output reader may already have stopped on an output error,
// or be stuck in a slow consumer), and the output is not needed any more.
func (e *Executor) kill(parent context.Context, id string, graceful bool, waitDone <-chan waitResult, closeAtt func()) {
	ctx, cancel := e.bg(parent)
	defer cancel()
	if graceful {
		if err := e.d.ContainerKill(ctx, id, "SIGTERM"); err == nil && waitDone != nil {
			t := time.NewTimer(e.killGrace)
			select {
			case <-waitDone:
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
	closeAtt()
	if err := e.d.ContainerKill(ctx, id, "SIGKILL"); err != nil && !IsNotFound(err) {
		e.log.WarnContext(ctx, "runner container kill failed", slog.String("error", err.Error()))
	}
	if waitDone != nil {
		select {
		case <-waitDone:
		case <-ctx.Done():
		}
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
