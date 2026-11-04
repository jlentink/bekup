package runner

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// stmtOutcome classifies the stderr of a failed statement run (psql /
// mysql --execute) without returning server text.
type stmtOutcome struct {
	mu         sync.Mutex
	permission bool
	exists     bool
	missing    bool
}

func (s *stmtOutcome) Log(line string) {
	l := strings.ToLower(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.Contains(l, "permission denied"), strings.Contains(l, "access denied"),
		strings.Contains(l, "must be owner"), strings.Contains(l, "must be superuser"):
		s.permission = true
	case strings.Contains(l, "already exists"), strings.Contains(l, "database exists"):
		s.exists = true
	case strings.Contains(l, "does not exist"), strings.Contains(l, "unknown database"):
		s.missing = true
	}
}

// status is the normalized error of a statement that exited non-zero.
func (s *stmtOutcome) status() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.permission:
		return runnerpb.ReasonPermission.Status()
	case s.exists:
		return runnerpb.ReasonDatabaseExists.Status()
	case s.missing:
		return runnerpb.ReasonDatabaseNotFound.Status()
	default:
		return runnerpb.ReasonFailed.Status()
	}
}

// statement runs one engine statement command in a container and maps a
// non-zero exit to a normalized status. step names the container of a
// multi-step operation ("" for a single statement): each step gets its
// own network and container name.
func (o *operations) statement(ctx context.Context, runID, step string, t *validate.Target, rt *runnerpb.Target,
	server engine.Version, build func(engine.Target) (engine.Command, error),
) (int, error) {
	so := &stmtOutcome{}
	rs := o.runSpec(runID, t.Entry, t, rt, build, server)
	rs.Step = step
	// psql / mysql print nothing useful on stdout for these statements
	// (pg_terminate_backend rows); it is discarded.
	rs.Stdout = func([]byte) error { return nil }
	rs.Log, rs.Timeout = so.Log, remaining(ctx)
	res, err := o.exec.Run(ctx, rs)
	if err != nil {
		return -1, targetStatus(err)
	}
	if res.ExitCode != 0 {
		return res.ExitCode, so.status()
	}
	return 0, nil
}

// createDatabase implements CreateDatabase.
func (o *operations) createDatabase(ctx context.Context, req *runnerpb.CreateDatabaseRequest) (resp *runnerpb.CreateDatabaseResponse, err error) {
	a := newAudit("CreateDatabase", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	d, err := o.val.CreateDatabase(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(d.Target)
	a.database = d.Database
	if o.exec == nil {
		return nil, errNoExecutor
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	chk, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{absentDB: d.Database})
	if err != nil {
		o.logCause(ctx, "CreateDatabase", err)
		return nil, targetStatus(err)
	}
	code, err := o.statement(ctx, req.GetCtx().GetRunId(), "", d.Target, req.GetTarget(), chk.version,
		func(et engine.Target) (engine.Command, error) {
			return d.Engine.CreateDatabase(d.Entry, et, d.Database)
		})
	a.exitCode = &code
	if err != nil {
		return nil, err
	}
	return &runnerpb.CreateDatabaseResponse{}, nil
}

// renameSwap implements RenameSwapDatabase (PostgreSQL): block new
// connections to the database and terminate its sessions, swap the names
// in one transaction (retried while terminated sessions exit), and re-allow
// connections if the swap finally fails. The names are derived by the
// runner from temp_id (D-123).
func (o *operations) renameSwap(ctx context.Context, req *runnerpb.RenameSwapDatabaseRequest) (resp *runnerpb.RenameSwapDatabaseResponse, err error) {
	a := newAudit("RenameSwapDatabase", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	// MySQL and MariaDB have no database rename: a precondition of the
	// engine, not a malformed request.
	if e := req.GetTarget().GetEngine(); e == runnerpb.Engine_ENGINE_MYSQL || e == runnerpb.Engine_ENGINE_MARIADB {
		return nil, status.Error(codes.FailedPrecondition, "rename swap is PostgreSQL only")
	}
	sw, err := o.val.RenameSwapDatabase(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(sw.Target)
	a.database = sw.Database
	if o.exec == nil {
		return nil, errNoExecutor
	}
	temp, err := engine.TempDatabaseName(sw.Database, sw.TempID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request: temp_id: cannot derive the temporary names")
	}
	old, err := engine.OldDatabaseName(sw.Database, sw.TempID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request: temp_id: cannot derive the temporary names")
	}
	ctx, cancel := context.WithTimeout(ctx, swapTimeout)
	defer cancel()
	chk, err := o.precheck(ctx, sw.Target, req.GetTarget(), checkOpts{needDB: []string{sw.Database, temp}, absentDB: old})
	if err != nil {
		o.logCause(ctx, "RenameSwapDatabase", err)
		return nil, targetStatus(err)
	}
	runID, rt := req.GetCtx().GetRunId(), req.GetTarget()
	step := func(name string, pick func(engine.SwapPlan) engine.Command) (int, error) {
		return o.statement(ctx, runID, name, sw.Target, rt, chk.version, func(et engine.Target) (engine.Command, error) {
			plan, err := sw.Engine.RenameSwap(sw.Entry, et, sw.Database, sw.TempID)
			if err != nil {
				return engine.Command{}, err
			}
			return pick(plan), nil
		})
	}
	rollback := func() {
		// A fresh context: the rollback must run even when the call was
		// canceled, bounded by its own timeout.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), createTimeout)
		defer rcancel()
		_, rerr := o.statement(rctx, runID, "rollback", sw.Target, rt, chk.version, func(et engine.Target) (engine.Command, error) {
			plan, err := sw.Engine.RenameSwap(sw.Entry, et, sw.Database, sw.TempID)
			return plan.Rollback, err
		})
		if rerr != nil {
			o.log.ErrorContext(ctx, "runner swap rollback failed", slog.String("run_id", runID), slog.String("code", status.Code(rerr).String()))
		}
	}
	if _, err := step("prepare", func(p engine.SwapPlan) engine.Command { return p.Prepare }); err != nil {
		rollback()
		return nil, err
	}
	var swapErr error
	for i := range swapAttempts {
		if i > 0 {
			t := time.NewTimer(o.swapGap)
			select {
			case <-ctx.Done():
				t.Stop()
				rollback()
				return nil, targetStatus(ctx.Err())
			case <-t.C:
			}
		}
		code, err := step("swap"+strconv.Itoa(i+1), func(p engine.SwapPlan) engine.Command { return p.Swap })
		a.exitCode = &code
		if err == nil {
			return &runnerpb.RenameSwapDatabaseResponse{}, nil
		}
		swapErr = err
		if status.Code(err) != codes.FailedPrecondition || runnerpb.ErrorReason(err) != runnerpb.ReasonFailed {
			break // only a failed transaction is retried
		}
	}
	rollback()
	return nil, swapErr
}

// dropTemp implements DropTempDatabase: the derived bekup temporary
// database (restore staging or replaced old database) is dropped with
// DROP DATABASE IF EXISTS (WITH (FORCE) on PostgreSQL 13+). The engine
// builder refuses any name that is not a bekup temporary name.
func (o *operations) dropTemp(ctx context.Context, req *runnerpb.DropTempDatabaseRequest) (resp *runnerpb.DropTempDatabaseResponse, err error) {
	a := newAudit("DropTempDatabase", req.GetCtx().GetRunId())
	defer func() { o.audit(ctx, a, err) }()
	d, err := o.val.DropTempDatabase(ctx, req)
	if err != nil {
		return nil, err
	}
	a.target(d.Target)
	a.database = d.Name
	if o.exec == nil {
		return nil, errNoExecutor
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	chk, err := o.precheck(ctx, d.Target, req.GetTarget(), checkOpts{})
	if err != nil {
		o.logCause(ctx, "DropTempDatabase", err)
		return nil, targetStatus(err)
	}
	existed := slices.Contains(chk.databases, d.Name)
	// Its own step: the drop often follows a canceled Restore of the same
	// run whose container and network are still being removed.
	step := "droptemp"
	if req.GetRole() == runnerpb.TempDatabaseRole_TEMP_DATABASE_ROLE_OLD {
		step = "dropold"
	}
	code, err := o.statement(ctx, req.GetCtx().GetRunId(), step, d.Target, req.GetTarget(), chk.version,
		func(et engine.Target) (engine.Command, error) {
			return d.Engine.DropDatabase(d.Entry, et, d.Name)
		})
	a.exitCode = &code
	if err != nil {
		return nil, err
	}
	return &runnerpb.DropTempDatabaseResponse{Existed: existed}, nil
}

// prePull implements PrePull: the allowlisted image of the engine version
// is pulled by digest only.
func (o *operations) prePull(ctx context.Context, req *runnerpb.PrePullRequest) (resp *runnerpb.PrePullResponse, err error) {
	a := newAudit("PrePull", "")
	defer func() { o.audit(ctx, a, err) }()
	ent, err := o.val.PrePull(req)
	if err != nil {
		return nil, err
	}
	a.engine, a.version = ent.Kind(), ent.Key()
	if o.exec == nil {
		return nil, errNoExecutor
	}
	present, err := o.exec.PrePull(ctx, ent)
	if err != nil {
		return nil, executor.Status(err)
	}
	return &runnerpb.PrePullResponse{ImageRef: spec.ImageRef(ent), AlreadyPresent: present}, nil
}

// listImages implements ListImages: every entry of the merged catalog
// (embedded + images.extra_file) with its digest-pinned reference.
func (o *operations) listImages(ctx context.Context, req *runnerpb.ListImagesRequest) (resp *runnerpb.ListImagesResponse, err error) {
	a := newAudit("ListImages", "")
	defer func() { o.audit(ctx, a, err) }()
	if err := o.val.ListImages(req); err != nil {
		return nil, err
	}
	entries := o.val.Catalog().Entries()
	resp = &runnerpb.ListImagesResponse{Images: make([]*runnerpb.AllowlistedImage, 0, len(entries))}
	for i := range entries {
		e := &entries[i]
		eng, ok := apiEngine(e.Kind())
		if !ok {
			continue
		}
		resp.Images = append(resp.Images, &runnerpb.AllowlistedImage{
			Engine: eng, VersionKey: e.Key(), ImageRef: spec.ImageRef(*e),
			ClientVersion: e.ClientVersion().String(), Legacy: e.Legacy(), Extra: e.Extra(),
		})
	}
	a.extra = append(a.extra, slog.Int("images", len(resp.Images)))
	return resp, nil
}

// apiEngine maps an engine kind to the API enum.
func apiEngine(k engine.Kind) (runnerpb.Engine, bool) {
	switch k {
	case engine.KindPostgres:
		return runnerpb.Engine_ENGINE_POSTGRES, true
	case engine.KindMySQL:
		return runnerpb.Engine_ENGINE_MYSQL, true
	case engine.KindMariaDB:
		return runnerpb.Engine_ENGINE_MARIADB, true
	default:
		return runnerpb.Engine_ENGINE_UNSPECIFIED, false
	}
}

// maxPolicyEntries bounds every list of a GetTargetPolicy answer (and the
// bastion targets in all), so the answer stays far below
// runnerpb.MaxMessageBytes even with the longest configurable lists.
const maxPolicyEntries = 256

// targetPolicyInfo is what GetTargetPolicy needs besides the guard.
type targetPolicyInfo struct {
	// runPool is docker.run_subnet_pool.
	runPool netip.Prefix
	// envKeys are the [targets] keys set by environment variables.
	envKeys []runnerpb.TargetsKey
	// ready reports whether the executor knows the runner's own
	// networks (nil: never).
	ready func() bool
}

// targetsKeyPaths maps the config paths of [targets] to the API enum.
var targetsKeyPaths = []struct {
	path string
	key  runnerpb.TargetsKey
}{
	{"targets.allow_cidrs", runnerpb.TargetsKey_TARGETS_KEY_ALLOW_CIDRS},
	{"targets.allow_hosts", runnerpb.TargetsKey_TARGETS_KEY_ALLOW_HOSTS},
	{"targets.deny_cidrs", runnerpb.TargetsKey_TARGETS_KEY_DENY_CIDRS},
	{"targets.deny_hosts", runnerpb.TargetsKey_TARGETS_KEY_DENY_HOSTS},
	{"targets.bastion_targets", runnerpb.TargetsKey_TARGETS_KEY_BASTION_TARGETS},
}

// targetEnvOverrides lists the [targets] keys whose value came from the
// environment (config.Result.Sources).
func targetEnvOverrides(sources map[string]config.Source) []runnerpb.TargetsKey {
	var out []runnerpb.TargetsKey
	for _, k := range targetsKeyPaths {
		if sources[k.path] == config.SourceEnv {
			out = append(out, k.key)
		}
	}
	return out
}

var deniedCategories = map[targets.DeniedCategory]runnerpb.DeniedCategory{
	targets.DeniedLoopback:      runnerpb.DeniedCategory_DENIED_CATEGORY_LOOPBACK,
	targets.DeniedUnspecified:   runnerpb.DeniedCategory_DENIED_CATEGORY_UNSPECIFIED_ADDRESS,
	targets.DeniedLinkLocal:     runnerpb.DeniedCategory_DENIED_CATEGORY_LINK_LOCAL,
	targets.DeniedCloudMetadata: runnerpb.DeniedCategory_DENIED_CATEGORY_CLOUD_METADATA,
	targets.DeniedMulticast:     runnerpb.DeniedCategory_DENIED_CATEGORY_MULTICAST_BROADCAST,
	targets.DeniedReserved:      runnerpb.DeniedCategory_DENIED_CATEGORY_RESERVED,
	targets.DeniedIPv4InIPv6:    runnerpb.DeniedCategory_DENIED_CATEGORY_IPV4_IN_IPV6,
	targets.DeniedTeredo:        runnerpb.DeniedCategory_DENIED_CATEGORY_TEREDO,
	targets.DeniedEmbeddedIPv4:  runnerpb.DeniedCategory_DENIED_CATEGORY_EMBEDDED_DENIED_IPV4,
	targets.DeniedNonGlobalIPv6: runnerpb.DeniedCategory_DENIED_CATEGORY_NON_GLOBAL_IPV6,
}

var policyWarnings = map[targets.WarningCode]runnerpb.TargetPolicyWarning{
	targets.WarningEmptyAllowlist:       runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_EMPTY_ALLOWLIST,
	targets.WarningBastionsUnrestricted: runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED,
}

// getTargetPolicy implements GetTargetPolicy (D-223): the target
// allowlist as the guard applies it, the runner's own networks, the
// always-denied categories and the startup warnings. Read-only.
func (o *operations) getTargetPolicy(ctx context.Context, req *runnerpb.GetTargetPolicyRequest) (resp *runnerpb.GetTargetPolicyResponse, err error) {
	a := newAudit("GetTargetPolicy", "")
	defer func() { o.audit(ctx, a, err) }()
	if err := o.val.GetTargetPolicy(req); err != nil {
		return nil, err
	}
	p := o.val.Guard().Policy()
	resp = &runnerpb.GetTargetPolicyResponse{
		EnvOverrides:         slices.Clone(o.policy.envKeys),
		ControlNetworksKnown: o.policy.ready != nil && o.policy.ready(),
	}
	limit := func(in []string) []string {
		if len(in) > maxPolicyEntries {
			resp.Truncated = true
			return in[:maxPolicyEntries]
		}
		return in
	}
	resp.AllowCidrs = limit(prefixStrings(p.AllowCIDRs))
	resp.AllowHosts = limit(p.AllowHosts)
	resp.DenyCidrs = limit(prefixStrings(p.DenyCIDRs))
	resp.DenyHosts = limit(p.DenyHosts)
	budget := maxPolicyEntries
	for _, b := range p.Bastions {
		if budget == 0 {
			resp.Truncated = true
			break
		}
		list := append(prefixStrings(b.CIDRs), b.Hosts...)
		if len(list) > budget {
			list, resp.Truncated = list[:budget], true
		}
		budget -= len(list)
		resp.BastionTargets = append(resp.BastionTargets, &runnerpb.BastionTargetList{Bastion: b.Bastion, Targets: list})
	}
	pool := o.policy.runPool
	if pool.IsValid() {
		resp.RunSubnetPool = pool.String()
	}
	resp.ControlNetworks = limit(prefixStrings(controlNetworks(p.OwnNetworks, pool)))
	for _, r := range targets.AlwaysDenied() {
		if c, ok := deniedCategories[r.Category]; ok {
			resp.AlwaysDenied = append(resp.AlwaysDenied, &runnerpb.AlwaysDeniedRange{Category: c, Cidrs: prefixStrings(r.Prefixes)})
		}
	}
	for _, w := range p.Warnings {
		if c, ok := policyWarnings[w]; ok {
			resp.Warnings = append(resp.Warnings, c)
		}
	}
	a.extra = append(a.extra,
		slog.Int("allow_entries", len(p.AllowCIDRs)+len(p.AllowHosts)),
		slog.Int("bastions", len(p.Bastions)),
		slog.Bool("truncated", resp.Truncated))
	return resp, nil
}

// controlNetworks are the own networks outside the run subnet pool (the
// pool and the per-run networks inside it are reported as the pool),
// deduplicated and sorted.
func controlNetworks(own []netip.Prefix, pool netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(own))
	for _, p := range own {
		if pool.IsValid() && p.Bits() >= pool.Bits() && pool.Contains(p.Addr()) {
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	return out
}

func prefixStrings(in []netip.Prefix) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = p.String()
	}
	return out
}
