package runner

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/spec"
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
