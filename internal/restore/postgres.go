package restore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// The PostgreSQL restorer (task 4.2, D-166).
//
//   - New database: CreateDatabase(target) (TEMPLATE template0), then
//     pg_restore --exit-on-error [--no-owner] into it. A failure leaves the
//     new, partially restored database (it never held other data); the log
//     says so.
//   - Overwrite: CreateDatabase(<target>__bekup_restore_<temp id>),
//     pg_restore --exit-on-error into that temporary database, then
//     RenameSwapDatabase (the runner blocks connections to the target,
//     terminates its sessions and renames target -> <target>__bekup_old_<id>
//     and temp -> target in one transaction) and finally DropTempDatabase
//     of the old database. Until the swap the target is never touched: a
//     failed, canceled or timed-out restore drops the temporary database
//     and leaves the target as it was.
//   - Both database modes neutralize the restored database before the
//     swap or the success (NeutralizeRestored, D-171): every SECURITY
//     DEFINER routine becomes SECURITY INVOKER (after --no-owner they
//     would run with the restore role's rights) and PUBLIC has no CONNECT
//     or TEMPORARY (revoked right after CREATE DATABASE as well). A
//     failure stops the restore before the swap. When a swap call fails,
//     the runner's database list decides between "swapped" and
//     "unchanged"; anything else is reported as unknown (swapStateOf).
//   - Globals: psql --no-psqlrc -v ON_ERROR_STOP=1 under the runner's own
//     \restrict key (D-138), against the maintenance database; never
//     combined with a database restore.
//
// The runner picks the client image max(dump client, target server)
// (D-036) from the source version key in the signed manifest.

// Warning codes of the PostgreSQL restorer.
const (
	// WarnOldDatabaseKept: the swap succeeded but the replaced database
	// (<target>__bekup_old_<id>) could not be dropped.
	WarnOldDatabaseKept = "old_database_kept"
	// WarnTempDatabaseKept: a failed overwrite could not drop its
	// temporary database (<target>__bekup_restore_<id>).
	WarnTempDatabaseKept = "temp_database_kept"
	// WarnSecdefNeutralized: SECURITY DEFINER functions of the restored
	// database were switched to SECURITY INVOKER (D-171).
	WarnSecdefNeutralized = "secdef_neutralized"
)

func init() { RegisterEngine(string(engine.KindPostgres), pgRestorer{}) }

type pgRestorer struct{}

// Restore implements EngineRestorer.
func (pgRestorer) Restore(ctx context.Context, req EngineRequest, src io.Reader, logs LogSink) (EngineResult, error) {
	if req.TargetEngine != engine.KindPostgres || req.SourceEngine != engine.KindPostgres {
		return EngineResult{}, ErrEngineMismatch
	}
	switch req.Kind {
	case KindGlobals:
		if req.TargetDB != "" || req.Format != pipeline.FormatSQL {
			return EngineResult{}, fmt.Errorf("%w: a globals restore takes a SQL dump and no database", ErrUnsupported)
		}
		return pgGlobals(ctx, &req, src, logs)
	case KindDatabase:
		if req.TargetDB == "" || req.Format != pipeline.FormatCustom {
			return EngineResult{}, fmt.Errorf("%w: a database restore takes a custom-format archive and a database", ErrUnsupported)
		}
		if err := engine.ValidateDBName(req.TargetDB); err != nil || engine.IsBekupTempDatabase(req.TargetDB) {
			return EngineResult{}, fmt.Errorf("%w: invalid target database name", ErrUnsupported)
		}
	default:
		return EngineResult{}, fmt.Errorf("%w: unknown kind", ErrUnsupported)
	}
	switch req.Mode {
	case ModeNewDB:
		return pgNewDB(ctx, &req, src, logs)
	case ModeOverwrite:
		return pgOverwrite(ctx, &req, src, logs)
	default:
		return EngineResult{}, fmt.Errorf("%w: unknown mode", ErrUnsupported)
	}
}

// pgNewDB creates the target database and restores into it, then
// neutralizes it (D-171). A test-restore scratch database (bekup_test_)
// is neutralized as such.
func pgNewDB(ctx context.Context, req *EngineRequest, src io.Reader, logs LogSink) (EngineResult, error) {
	logs.Line(LogInfo, "creating the new database "+req.TargetDB+" (PUBLIC gets no CONNECT or TEMPORARY on it)")
	if err := CreateDatabase(ctx, req, req.TargetDB); err != nil {
		logs.Line(LogError, "the new database could not be created")
		return EngineResult{}, err
	}
	// A test restore's scratch database skips the dump's GRANT/REVOKE
	// (they could name roles of the source server; nothing reads them).
	scratch := engine.IsBekupScratchDatabase(req.TargetDB)
	note := noOwnerNote(req.NoOwner)
	if scratch {
		note += " --no-privileges"
	}
	logs.Line(LogInfo, fmt.Sprintf("restoring into %s with pg_restore --exit-on-error%s", req.TargetDB, note))
	res, err := RunRestoreStream(ctx, req, StreamOptions{
		Database: req.TargetDB, Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE, NoOwner: req.NoOwner, NoPrivileges: scratch,
	}, src, logs)
	if err != nil {
		logs.Line(LogError, fmt.Sprintf("the restore failed; the new database %s is incomplete and should be dropped", req.TargetDB))
		return EngineResult{Warnings: []string{WarnPartialNewDatabase}}, err
	}
	out := EngineResult{Warnings: res.GetWarnings()}
	kind := runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE
	if scratch {
		kind = runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH
	}
	n, err := pgNeutralize(ctx, req, kind, logs)
	if err != nil {
		logs.Line(LogError, fmt.Sprintf("the restored database %s could not be neutralized; it stays on the server without PUBLIC access "+
			"and may contain SECURITY DEFINER functions: drop it", req.TargetDB))
		return EngineResult{Warnings: append(out.Warnings, WarnPartialNewDatabase)}, err
	}
	out.Neutralized = n
	if n.Count > 0 {
		out.Warnings = append(out.Warnings, WarnSecdefNeutralized)
	}
	return out, nil
}

// pgNeutralize neutralizes the restored database (NeutralizeRestored),
// logs the result and calls req.OnNeutralized.
func pgNeutralize(ctx context.Context, req *EngineRequest, kind runnerpb.NeutralizeKind, logs LogSink) (*Neutralized, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	logs.Line(LogInfo, "hardening the restored database: SECURITY DEFINER functions become SECURITY INVOKER, PUBLIC loses CONNECT and TEMPORARY")
	n, err := NeutralizeRestored(ctx, req, kind)
	if err != nil {
		return nil, err
	}
	if n.Count > 0 {
		logs.Line(LogWarn, fmt.Sprintf("%d SECURITY DEFINER function(s) or procedure(s) were switched to SECURITY INVOKER: after a restore "+
			"they would run with the restore role's rights for every caller. Review them before re-enabling SECURITY DEFINER deliberately "+
			"(ALTER FUNCTION ... SECURITY DEFINER) and before granting CONNECT.", n.Count))
		for _, f := range n.Functions {
			logs.Line(LogWarn, "switched to SECURITY INVOKER: "+f)
		}
		if n.Count > len(n.Functions) {
			logs.Line(LogWarn, fmt.Sprintf("... and %d more", n.Count-len(n.Functions)))
		}
	} else {
		logs.Line(LogInfo, "no SECURITY DEFINER functions found; PUBLIC has no CONNECT or TEMPORARY on the restored database")
	}
	if req.OnNeutralized != nil {
		if err := req.OnNeutralized(ctx, n); err != nil {
			return nil, err
		}
	}
	return &n, nil
}

// pgOverwrite restores into a temporary database, neutralizes it and
// swaps it in.
func pgOverwrite(ctx context.Context, req *EngineRequest, src io.Reader, logs LogSink) (out EngineResult, err error) {
	temp, err := engine.TempDatabaseName(req.TargetDB, req.TempID())
	if err != nil {
		return EngineResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	old, err := engine.OldDatabaseName(req.TargetDB, req.TempID())
	if err != nil {
		return EngineResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	logs.Line(LogInfo, fmt.Sprintf("overwrite: restoring into the temporary database %s; %s stays untouched until the swap", temp, req.TargetDB))
	if err := CreateDatabase(ctx, req, temp); err != nil {
		logs.Line(LogError, "the temporary database could not be created")
		return EngineResult{}, err
	}
	swapped, unknown := false, false
	defer func() {
		if swapped {
			return
		}
		// Not swapped (or unknown): dropping the derived temporary name
		// is safe either way (after a swap the name no longer exists), and
		// whether it still existed tells whether the swap happened.
		existed, derr := DropTempDatabase(ctx, req, runnerpb.TempDatabaseRole_TEMP_DATABASE_ROLE_RESTORE)
		switch {
		case derr != nil:
			logs.Line(LogError, fmt.Sprintf("the temporary database %s could not be dropped; drop it manually", temp))
			out.Warnings = append(out.Warnings, WarnTempDatabaseKept)
		case existed:
			logs.Line(LogInfo, fmt.Sprintf("the temporary database %s was dropped; %s is unchanged", temp, req.TargetDB))
		case unknown:
			logs.Line(LogError, fmt.Sprintf("the temporary database %s no longer exists, so the swap has probably happened: %s may hold "+
				"the restored data and the replaced database may be kept as %s; check both", temp, req.TargetDB, old))
		}
	}()
	logs.Line(LogInfo, "restoring with pg_restore --exit-on-error"+noOwnerNote(req.NoOwner))
	res, err := RunRestoreStream(ctx, req, StreamOptions{Database: temp, Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE, NoOwner: req.NoOwner}, src, logs)
	if err != nil {
		logs.Line(LogError, "the restore into the temporary database failed")
		return EngineResult{}, err
	}
	out.Warnings = append(out.Warnings, res.GetWarnings()...)
	n, err := pgNeutralize(ctx, req, runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING, logs)
	if err != nil {
		logs.Line(LogError, "the restored temporary database could not be neutralized; it is not swapped in")
		return EngineResult{Warnings: out.Warnings}, err
	}
	out.Neutralized = n
	if n.Count > 0 {
		out.Warnings = append(out.Warnings, WarnSecdefNeutralized)
	}
	if err := ctx.Err(); err != nil {
		return EngineResult{Warnings: out.Warnings}, context.Cause(ctx)
	}
	logs.Line(LogInfo, fmt.Sprintf("swapping %s into place (sessions on %s are terminated)", temp, req.TargetDB))
	if serr := RenameSwap(ctx, req); serr != nil {
		switch swapStateOf(ctx, req, temp) {
		case swapDone:
			logs.Line(LogWarn, fmt.Sprintf("the swap call reported an error, but the server shows it happened: %s holds the restored data", req.TargetDB))
		case swapNotDone:
			logs.Line(LogError, "the swap failed; the target database is unchanged")
			return EngineResult{Warnings: out.Warnings}, serr
		default:
			unknown = true
			logs.Line(LogError, fmt.Sprintf("the swap call reported an error and the state of %s could not be read", req.TargetDB))
			return EngineResult{Warnings: out.Warnings}, fmt.Errorf("%w: %w", ErrSwapStateUnknown, serr)
		}
	}
	swapped = true
	logs.Line(LogInfo, fmt.Sprintf("%s now holds the restored data; dropping the replaced database %s", req.TargetDB, old))
	if _, err := DropTempDatabase(ctx, req, runnerpb.TempDatabaseRole_TEMP_DATABASE_ROLE_OLD); err != nil {
		logs.Line(LogWarn, fmt.Sprintf("the replaced database %s could not be dropped; drop it manually", old))
		out.Warnings = append(out.Warnings, WarnOldDatabaseKept)
	}
	return out, nil
}

// swapState is what the server shows after a failed swap call.
type swapState int

const (
	swapUnknown swapState = iota
	swapDone
	swapNotDone
)

// swapStateOf asks the runner for the target's databases after a failed
// swap call (on its own bounded context: the call may have failed because
// the restore was canceled). The list holds the databases that accept
// connections: the replaced database never does after a swap (the runner
// blocked connections to it before renaming it), the target does again
// after the runner's rollback. So: swapped when the target is listed and
// the temporary database is gone (it took the target's name); not swapped
// when both are listed; unknown otherwise (for example the target still
// refusing connections after a failed rollback).
func swapStateOf(ctx context.Context, req *EngineRequest, temp string) swapState {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	target, release, err := req.Target(pctx)
	if err != nil {
		return swapUnknown
	}
	resp, err := req.Runner.Probe(pctx, &runnerpb.ProbeRequest{Ctx: req.RunContext(), Target: target})
	release()
	if err != nil {
		return swapUnknown
	}
	dbs := resp.GetDatabases()
	hasTarget, hasTemp := slices.Contains(dbs, req.TargetDB), slices.Contains(dbs, temp)
	switch {
	case hasTarget && !hasTemp:
		return swapDone
	case hasTarget && hasTemp:
		return swapNotDone
	default:
		return swapUnknown
	}
}

// MaxGlobalsBytes bounds a globals dump (read into memory to make its
// CREATE ROLE statements idempotent).
const MaxGlobalsBytes = 32 << 20

// pgGlobals restores the cluster globals with the hardened psql. The dump
// is read in full first (bounded; its final digest check runs before any
// byte reaches the runner), and pg_dumpall's "CREATE ROLE <name>;" lines
// become DO blocks that skip an existing role: pg_dumpall relies on that
// CREATE failing harmlessly for roles that exist (always the connected
// role), which ON_ERROR_STOP would turn into a failed restore. The ALTER
// ROLE that follows sets the attributes either way. Everything else
// (tablespaces, memberships, settings) still stops at its first error.
func pgGlobals(ctx context.Context, req *EngineRequest, src io.Reader, logs LogSink) (EngineResult, error) {
	dump, err := io.ReadAll(io.LimitReader(src, MaxGlobalsBytes+1))
	if err != nil {
		return EngineResult{}, err
	}
	if len(dump) > MaxGlobalsBytes {
		return EngineResult{}, fmt.Errorf("%w: the globals dump exceeds %d bytes", ErrUnsupported, MaxGlobalsBytes)
	}
	if uint64(len(dump)) != req.TotalBytes {
		return EngineResult{}, fmt.Errorf("%w: the dump size differs from the record", ErrRestoreIncomplete)
	}
	out, n := IdempotentCreateRoles(dump)
	clear(dump)
	defer clear(out)
	r := *req
	r.TotalBytes = uint64(len(out))
	logs.Line(LogInfo, fmt.Sprintf("restoring the globals (roles, tablespaces) with psql --no-psqlrc -v ON_ERROR_STOP=1 under a \\restrict key; %d CREATE ROLE statement(s) skip roles that already exist", n))
	res, err := RunRestoreStream(ctx, &r, StreamOptions{Kind: runnerpb.DumpKind_DUMP_KIND_GLOBALS}, bytes.NewReader(out), logs)
	if err != nil {
		if errors.Is(err, ErrRestoreFailed) {
			logs.Line(LogError, "psql stopped at the first error (e.g. an existing tablespace); statements before it were applied")
		}
		return EngineResult{}, err
	}
	return EngineResult{Warnings: res.GetWarnings()}, nil
}

// createRoleLine is pg_dumpall's "CREATE ROLE <name>;" line: a lower-case
// identifier or a quoted one (fmtId) without "$", so the dollar-quoted DO
// body below can never be closed early.
var createRoleLine = regexp.MustCompile(`^CREATE ROLE ("(?:[^"$\x00-\x1f]|"")+"|[a-z_][a-z0-9_]*);$`)

// IdempotentCreateRoles rewrites every "CREATE ROLE <name>;" line of a
// globals dump into a DO block that ignores duplicate_object, and returns
// the new dump and the number of rewritten lines. Other lines are kept
// byte for byte.
func IdempotentCreateRoles(dump []byte) ([]byte, int) {
	var out bytes.Buffer
	out.Grow(len(dump) + 1024)
	n := 0
	for line := range bytes.Lines(dump) {
		body, nl := bytes.CutSuffix(line, []byte("\n"))
		if m := createRoleLine.FindSubmatch(body); m != nil {
			out.WriteString("DO $bekup_role$BEGIN CREATE ROLE ")
			out.Write(m[1])
			out.WriteString("; EXCEPTION WHEN duplicate_object THEN NULL; END$bekup_role$;")
			if nl {
				out.WriteByte('\n')
			}
			n++
			continue
		}
		out.Write(line)
	}
	return out.Bytes(), n
}

func noOwnerNote(noOwner bool) string {
	if noOwner {
		return " --no-owner"
	}
	return ""
}
