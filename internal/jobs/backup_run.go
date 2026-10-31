package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/schemaversion"
	"github.com/jlentink/bekup/internal/storage"
)

// destState is one job destination during a run.
type destState struct {
	id   uuid.UUID
	d    storage.Destination // nil when it could not be opened
	lost bool                // unavailable or dropped: skipped for the rest of the run
	// manifest: manifest.json was finalized there.
	manifest bool
}

// itemState is one database (or the globals) of a run.
type itemState struct {
	kind     pipeline.EntryKind
	db       string
	status   sqlc.RunItemStatus
	failure  *runstate.Failure
	warnings []runstate.Warning
	// entry is set when the dump was verified and stored on at least one
	// destination (okDests).
	entry   *pipeline.Entry
	okDests map[string]uuid.UUID // destination id → artifact id of the verified object stored there
	stats   json.RawMessage
	raw     int64
	stored  int64
	rawSum  []byte
	sum     []byte
	// versionStatus / version: the schema-version capture (D-210).
	versionStatus schemaversion.Status
	version       string
}

func (it *itemState) label() string {
	if it.kind == pipeline.KindGlobals {
		return "the globals"
	}
	return "database " + it.db
}

// execResult is the outcome of a started run.
type execResult struct {
	status   sqlc.RunStatus
	failure  *runstate.Failure
	complete int
	// fatal is the run-level failure that stopped the run (if any).
	fatal *runstate.Failure
	// recordErr: the terminal state could not be stored.
	recordErr error
}

// runExec carries one started run.
type runExec struct {
	w       *BackupWorker
	o       *BackupWorkerOptions
	run     runstate.Run
	job     runstate.Job
	attempt int
	log     *slog.Logger

	logs     *runstate.LogWriter
	ts       time.Time
	enc      pipeline.Encryption
	dests    []*destState
	server   string
	warnings []runstate.Warning
	// notices are informational findings (not warnings): the databases a
	// mode-all job excludes on purpose.
	notices []runstate.Warning
	// excluded are those databases (for the manifest's stats).
	excluded []string
	items    []*itemState
	unreg    []func()
}

func newRunExec(w *BackupWorker, run runstate.Run, job runstate.Job, attempt int, log *slog.Logger) *runExec {
	return &runExec{w: w, o: &w.o, run: run, job: job, attempt: attempt, log: log}
}

// scrub redacts registered secret values and credentials in URLs/DSNs.
func (e *runExec) scrub(s string) string {
	return e.o.Registry.Scrub(logging.RedactCredentials(s))
}

// register adds the target's credentials to the redaction registry until
// the run ends (they are zeroed in the request by its release function).
func (e *runExec) register(t *runnerpb.Target) {
	reg := func(b []byte) {
		if len(b) > 0 {
			e.unreg = append(e.unreg, e.o.Registry.Register(b))
		}
	}
	reg(t.GetPassword())
	reg(t.GetClientCertificate().GetKeyPem())
	if st := t.GetSshTunnel(); st != nil {
		reg(st.GetPassword())
		reg(st.GetPrivateKeyPem())
		reg(st.GetPassphrase())
	}
}

func (e *runExec) cleanup() {
	for _, u := range e.unreg {
		u()
	}
	e.unreg = nil
	if e.enc.Passphrase != nil {
		e.enc.Passphrase.Destroy()
	}
	for _, d := range e.dests {
		if d.d != nil {
			if err := d.d.Close(); err != nil {
				e.log.Warn("closing a destination failed", slog.String("destination_id", d.id.String()), slog.Any("error", err))
			}
		}
	}
}

// execute runs a run that holds its slot, records the outcome and returns
// it.
func (e *runExec) execute(parent context.Context) execResult {
	now := e.o.Now()
	e.ts = now.UTC().Truncate(time.Second)
	if err := e.o.Store.Start(parent, e.run.Ref, e.attempt, now, e.ts); err != nil {
		if errors.Is(err, runstate.ErrNotActive) {
			// Finished meanwhile (canceled before it started).
			return execResult{status: runstate.StatusCanceled}
		}
		return execResult{recordErr: err}
	}
	timeout := e.o.QueueTimeout
	if e.job.Timeout > 0 && e.job.Timeout < timeout {
		timeout = e.job.Timeout
	}
	tctx, tcancel := context.WithTimeoutCause(parent, timeout, errJobTimeout)
	defer tcancel()
	ctx, cancel := context.WithCancelCause(tctx)
	defer cancel(nil)
	e.logs = runstate.StartLogWriter(ctx, runstate.LogWriterOptions{
		Append: func(c context.Context, lines []runstate.LogLine) error {
			return e.o.Store.AppendLogs(c, e.run.Ref, lines)
		},
		Scrub:         e.scrub,
		MaxLines:      e.o.LogMaxLines,
		MaxLineBytes:  e.o.LogMaxLineBytes,
		FlushInterval: e.o.LogFlushInterval,
		Now:           e.o.Now,
		OnError: func(err error) {
			e.log.WarnContext(ctx, "storing run log lines failed", slog.Any("error", err))
		},
	})
	stopWatch := e.watch(ctx, cancel)
	defer e.cleanup()

	e.logs.Logf(runstate.LevelInfo, "backup run started (attempt %d, trigger %s, timeout %s)", e.attempt, e.run.Trigger, timeout)
	fatal := e.backup(ctx)
	stopWatch()

	fctx, fcancel := finalCtx(parent)
	defer fcancel()
	stop := stopFailure(ctx)
	if stop != nil {
		e.logs.Logf(runstate.LevelWarn, "%s", stop.Message)
	}
	e.writeManifest(fctx)
	res := e.outcome(stop, fatal)
	e.logs.Logf(levelOf(res.status), "backup run finished: %s", res.status)
	lines, dropped := e.logs.Close()
	var raw, stored int64
	for _, it := range e.items {
		if it.entry != nil {
			raw += it.raw
			stored += it.stored
		}
	}
	err := e.o.Store.Finish(fctx, e.run.Ref, runstate.Result{
		Status: res.status, Failure: res.failure, Warnings: e.allWarnings(), Notices: e.notices,
		BytesRaw: raw, BytesStored: stored, LogLines: lines, LogLinesDropped: dropped,
	})
	if err != nil && !errors.Is(err, runstate.ErrNotActive) {
		res.recordErr = err
	}
	e.log.InfoContext(fctx, "backup run finished", slog.String("status", string(res.status)), slog.Int("items", len(e.items)),
		slog.Int("complete_items", res.complete), slog.Int64("bytes_stored", stored))
	return res
}

func levelOf(st sqlc.RunStatus) sqlc.RunLogLevel {
	switch st {
	case runstate.StatusSuccess:
		return runstate.LevelInfo
	case runstate.StatusFailed:
		return runstate.LevelError
	default:
		return runstate.LevelWarn
	}
}

// watch polls cancel_requested_at and refreshes the slot until stopped.
func (e *runExec) watch(ctx context.Context, cancel context.CancelCauseFunc) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		poll := time.NewTicker(e.o.CancelPoll)
		defer poll.Stop()
		beat := time.NewTicker(e.o.SlotHeartbeat)
		defer beat.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-poll.C:
				ok, err := e.o.Store.CancelRequested(ctx, e.run.RunID)
				if err != nil {
					if ctx.Err() == nil {
						e.log.WarnContext(ctx, "checking for a cancel request failed", slog.Any("error", err))
					}
					continue
				}
				if ok {
					e.log.InfoContext(ctx, "backup run cancel requested")
					cancel(errRunCanceled)
					return
				}
			case <-beat.C:
				err := e.o.Slots.Heartbeat(ctx, e.run.RunID)
				if errors.Is(err, runstate.ErrSlotLost) {
					e.log.ErrorContext(ctx, "the run slot expired; stopping the run")
					cancel(errSlotLost)
					return
				}
				if err != nil && ctx.Err() == nil {
					e.log.WarnContext(ctx, "run slot heartbeat failed", slog.Any("error", err))
				}
			}
		}
	}()
	return func() {
		select {
		case <-done:
		default:
			close(done)
		}
		<-finished
	}
}

// backup performs the run; it returns a run-level failure that prevented
// (further) items.
func (e *runExec) backup(ctx context.Context) *runstate.Failure {
	enc, err := e.o.Keys.Encryptor(e.job.ID).BackupEncryption(ctx)
	if err != nil {
		if f := stopFailure(ctx); f != nil {
			return f
		}
		e.log.ErrorContext(ctx, "loading the job's encryption failed", slog.Any("error", err))
		return e.fatal(fail(CodeEncryption, "the job's encryption keys could not be loaded"))
	}
	e.enc = enc
	if enc.Passphrase != nil {
		e.unreg = append(e.unreg, e.o.Registry.Register(enc.Passphrase.Bytes()))
	}
	if f := e.openDestinations(ctx); f != nil {
		return e.fatal(f)
	}
	probe, f := e.probe(ctx)
	if f != nil {
		return e.fatal(f)
	}
	dbs, missing := selectDatabases(e.job, probe.GetDatabases())
	if n, ex, ok := excludedNotice(e.job, probe.GetDatabases()); ok {
		e.notices = append(e.notices, n)
		e.excluded = ex
		e.logs.Logf(runstate.LevelInfo, "%s", n.Message)
	}
	globals := e.job.IncludeGlobals && e.job.Engine == engine.KindPostgres
	if len(dbs) == 0 && len(missing) == 0 && !globals {
		return e.fatal(fail(CodeNoDatabases, "there is no database to back up (all are excluded or the server has none)"))
	}
	e.logs.Logf(runstate.LevelInfo, "%d database(s) to back up%s", len(dbs), map[bool]string{true: " plus the globals", false: ""}[globals])
	pos := 0
	for _, name := range orderSelection(e.job, dbs) {
		if ctx.Err() != nil {
			break
		}
		if slices.Contains(missing, name) {
			e.missingItem(ctx, name, pos)
		} else if f := e.dumpItem(ctx, pipeline.KindDatabase, name, pos); f != nil {
			return e.fatal(f)
		}
		pos++
	}
	if globals && ctx.Err() == nil {
		if f := e.dumpItem(ctx, pipeline.KindGlobals, "", pos); f != nil {
			return e.fatal(f)
		}
	}
	return nil
}

// fatal logs a run-level failure.
func (e *runExec) fatal(f *runstate.Failure) *runstate.Failure {
	e.logs.Logf(runstate.LevelError, "%s", f.Message)
	return f
}

// selectDatabases resolves the job's selection against the server's list:
// mode all = every listed database except the excludes; mode selected =
// the configured names present on the server (missing = the others).
func selectDatabases(job runstate.Job, listed []string) (dbs, missing []string) {
	if job.AllDatabases {
		for _, d := range listed {
			if !slices.Contains(job.Exclude, d) {
				dbs = append(dbs, d)
			}
		}
		slices.Sort(dbs)
		return slices.Compact(dbs), nil
	}
	for _, d := range job.Databases {
		if slices.Contains(listed, d) {
			dbs = append(dbs, d)
		} else {
			missing = append(missing, d)
		}
	}
	return dbs, missing
}

// excludedNotice is the informational notice of mode all: databases on the
// server that the job's exclude list leaves out on purpose (task 3.10). It
// is a notice, not a warning, so has_warnings keeps flagging real problems
// (task 3.11); it also returns the excluded names.
func excludedNotice(job runstate.Job, listed []string) (runstate.Warning, []string, bool) {
	if !job.AllDatabases {
		return runstate.Warning{}, nil, false
	}
	var ex []string
	for _, d := range listed {
		if slices.Contains(job.Exclude, d) {
			ex = append(ex, d)
		}
	}
	if len(ex) == 0 {
		return runstate.Warning{}, nil, false
	}
	slices.Sort(ex)
	ex = slices.Compact(ex)
	w := statsWarning("", "databases_excluded", int64(len(ex)), ex)
	return w, ex, true
}

// orderSelection is the item order: the configured order for selected
// databases (missing ones in place), sorted for mode all.
func orderSelection(job runstate.Job, dbs []string) []string {
	if job.AllDatabases {
		return dbs
	}
	return job.Databases
}

// missingItem records a selected database the server does not have.
func (e *runExec) missingItem(ctx context.Context, name string, pos int) {
	it := &itemState{kind: pipeline.KindDatabase, db: name, status: runstate.ItemFailed,
		failure: fail(CodeDatabaseMissing, "the database does not exist on the server")}
	it.warnings = append(it.warnings, runstate.Warning{Code: CodeDatabaseMissing, Message: "a selected database does not exist on the server", Database: name})
	e.items = append(e.items, it)
	e.logs.Logf(runstate.LevelError, "database %s: %s", name, it.failure.Message)
	item, err := e.o.Store.AddItem(ctx, e.run.Ref, sqlc.RunItemKindDatabase, name, pos)
	if err != nil {
		e.log.ErrorContext(ctx, "recording a run item failed", slog.Any("error", err))
		return
	}
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	if err := e.o.Store.FinishItem(fctx, e.run.Ref, item.ID, runstate.ItemResult{
		Status: runstate.ItemFailed, Failure: it.failure, Warnings: it.warnings,
	}); err != nil {
		e.log.ErrorContext(ctx, "recording a run item failed", slog.Any("error", err))
	}
}

// openDestinations opens every job destination. One that cannot be opened
// is lost for the run (warning); none at all fails the run.
func (e *runExec) openDestinations(ctx context.Context) *runstate.Failure {
	for _, jd := range e.job.Destinations {
		ds := &destState{id: jd.ID}
		e.dests = append(e.dests, ds)
		d, err := e.o.Destinations.Open(ctx, jd.ID)
		if err != nil {
			ds.lost = true
			code := storage.Classify(err)
			msg := code.Message()
			var nr *destinations.NotReadyError
			if errors.As(err, &nr) {
				msg = "the destination is not ready: " + nr.Reason
			}
			e.log.WarnContext(ctx, "opening a destination failed", slog.String("destination_id", jd.ID.String()), slog.Any("error", err))
			e.warnings = append(e.warnings, runstate.Warning{Code: CodeDestinations, Message: "the destination could not be opened: " + msg, DestinationID: jd.ID.String()})
			e.logs.Logf(runstate.LevelWarn, "destination %s could not be opened: %s", jd.ID, msg)
			e.reportDestination(ctx, jd.ID, code)
			continue
		}
		ds.d = d
	}
	for _, d := range e.dests {
		if !d.lost {
			return nil
		}
	}
	return fail(CodeDestinations, "no destination of the job could be opened")
}

// reportDestination records the security event of a destination failure.
func (e *runExec) reportDestination(ctx context.Context, id uuid.UUID, code storage.ErrorCode) {
	if err := e.o.Destinations.RecordRunFailure(ctx, id, code); err != nil {
		e.log.ErrorContext(ctx, "recording a destination security event failed", slog.Any("error", err))
	}
}

// probe connects through the runner: server version and database list.
func (e *runExec) probe(ctx context.Context) (*runnerpb.ProbeResponse, *runstate.Failure) {
	pctx, cancel := context.WithTimeout(ctx, e.o.ProbeTimeout)
	defer cancel()
	target, release, err := e.o.Targets.Target(pctx, e.job.ConnectionID)
	if err != nil {
		return nil, e.targetFailure(ctx, err)
	}
	e.register(target)
	resp, err := e.o.Runner.Probe(pctx, &runnerpb.ProbeRequest{Ctx: e.runContext(), Target: target})
	release()
	if err != nil {
		e.securityEvent(ctx, runOpProbe, err)
		return nil, runnerFailure(ctx, err)
	}
	e.server = serverVersion(resp.GetServerVersion())
	for _, w := range resp.GetWarnings() {
		e.warnings = append(e.warnings, runnerWarning(w, ""))
	}
	e.logs.Logf(runstate.LevelInfo, "connected: server version %s", e.server)
	return resp, nil
}

// targetFailure maps an error of Targets.Target.
func (e *runExec) targetFailure(ctx context.Context, err error) *runstate.Failure {
	if f := stopFailure(ctx); f != nil {
		return f
	}
	var nr *connections.NotReadyError
	if errors.As(err, &nr) {
		return fail("connection_not_ready", "the connection is not ready: %s", nr.Reason)
	}
	e.log.ErrorContext(ctx, "building the runner request failed", slog.Any("error", err))
	return fail(CodeInternal, "the connection's credentials could not be loaded")
}

// securityEvent audits and alerts pin / host-key mismatches.
func (e *runExec) securityEvent(ctx context.Context, op string, err error) {
	if !securityReason(err) {
		return
	}
	if rerr := e.o.Targets.RecordRunFailure(ctx, e.job.ConnectionID, op, err); rerr != nil {
		e.log.ErrorContext(ctx, "recording a connection security event failed", slog.Any("error", rerr))
	}
}

func (e *runExec) runContext() *runnerpb.RunContext {
	return &runnerpb.RunContext{RunId: e.run.RunID.String()}
}

// serverVersion keeps printable ASCII (the manifest's rule), bounded.
func serverVersion(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && b.Len() < maxServerVersionLen {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "unknown"
	}
	return out
}

// targets returns the live destinations as pipeline targets for key.
func (e *runExec) targets(key storage.Key) []pipeline.Target {
	var out []pipeline.Target
	for _, d := range e.dests {
		if !d.lost && d.d != nil {
			out = append(out, pipeline.Target{Name: d.id.String(), Dest: d.d, Key: key})
		}
	}
	return out
}

func (e *runExec) dest(name string) *destState {
	for _, d := range e.dests {
		if d.id.String() == name {
			return d
		}
	}
	return nil
}

// outcome derives the run status (D-147): canceled; success (every item
// stored and verified on every job destination, the manifest on all of
// them); partial (at least one complete artifact); failed (none).
func (e *runExec) outcome(stop, fatal *runstate.Failure) execResult {
	res := execResult{fatal: fatal}
	allGood := stop == nil && fatal == nil && len(e.items) > 0
	for _, it := range e.items {
		if it.entry != nil {
			res.complete++
		}
		if it.status != runstate.ItemSuccess {
			allGood = false
		}
	}
	for _, d := range e.dests {
		if d.lost || !d.manifest {
			allGood = false
		}
	}
	switch {
	case stop != nil && stop.Code == CodeCanceled:
		res.status, res.failure = runstate.StatusCanceled, stop
	case res.complete == 0:
		res.status = runstate.StatusFailed
		res.failure = firstFailure(stop, fatal, e.items)
	case allGood:
		res.status = runstate.StatusSuccess
	default:
		res.status = runstate.StatusPartial
		res.failure = stop
		if res.failure == nil {
			res.failure = fatal
		}
		if res.failure == nil {
			res.failure = fail(CodeIncomplete, "%d of %d item(s) were backed up completely; see the item and destination warnings", countSuccess(e.items), len(e.items))
		}
	}
	return res
}

func countSuccess(items []*itemState) int {
	n := 0
	for _, it := range items {
		if it.status == runstate.ItemSuccess {
			n++
		}
	}
	return n
}

func firstFailure(stop, fatal *runstate.Failure, items []*itemState) *runstate.Failure {
	if fatal != nil {
		return fatal
	}
	if stop != nil {
		return stop
	}
	for _, it := range items {
		if it.failure != nil {
			return &runstate.Failure{Code: it.failure.Code, Message: it.label() + ": " + it.failure.Message}
		}
	}
	return fail(CodeIncomplete, "nothing was backed up")
}

// allWarnings returns the run warnings followed by the item warnings.
func (e *runExec) allWarnings() []runstate.Warning {
	out := slices.Clone(e.warnings)
	for _, it := range e.items {
		out = append(out, it.warnings...)
	}
	return out
}

// riverResult maps the outcome to River (see Work).
func (e *runExec) riverResult(res execResult) error {
	if res.recordErr != nil {
		return fmt.Errorf("jobs: record backup run %s: %w", e.run.RunID, res.recordErr)
	}
	switch {
	case res.status == runstate.StatusCanceled:
		return river.JobCancel(errRunCanceled)
	case res.failure != nil && res.failure.Code == CodeInterrupted:
		return fmt.Errorf("jobs: backup run %s interrupted", e.run.RunID)
	case res.complete == 0 && retryable(res.fatal):
		return fmt.Errorf("jobs: backup run %s failed: %s", e.run.RunID, res.fatal.Code)
	default:
		return nil
	}
}
