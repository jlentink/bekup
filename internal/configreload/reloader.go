package configreload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Status is the outcome of one reload in one process.
type Status string

// Statuses (config_reload_results.status).
const (
	// StatusLoaded is the startup row of a process (no request).
	StatusLoaded Status = "loaded"
	// StatusApplied: live keys changed and were applied.
	StatusApplied Status = "applied"
	// StatusUnchanged: the file matches the running config.
	StatusUnchanged Status = "unchanged"
	// StatusRestartRequired: restart-only keys changed (live keys, if
	// any, were applied).
	StatusRestartRequired Status = "restart_required"
	// StatusRejected: the file could not be read or is invalid; nothing
	// changed.
	StatusRejected Status = "rejected"
	// StatusFailed: an applier refused the change or the outcome could not
	// be recorded; nothing changed.
	StatusFailed Status = "failed"
)

// Statuses lists every reload outcome (metric labels).
func Statuses() []Status {
	return []Status{StatusApplied, StatusUnchanged, StatusRestartRequired, StatusRejected, StatusFailed}
}

// Error kinds beyond config.ErrorKind*: an applier refused the change, or
// the audit entry / result row could not be written (never stored: the
// write failed).
const (
	ErrorKindApply  = "apply"
	ErrorKindRecord = "record"
)

// Trigger says what started a reload.
type Trigger string

// Triggers (config_reload_requests.trigger).
const (
	TriggerUI     Trigger = "ui"
	TriggerSignal Trigger = "signal"
)

// ProcessKind is the kind of process a Reloader runs in.
type ProcessKind string

// Process kinds.
const (
	KindServe  ProcessKind = "serve"
	KindWorker ProcessKind = "worker"
)

// Request is one reload request.
type Request struct {
	ID      uuid.UUID
	Trigger Trigger
}

// Result is the outcome of one reload in this process (key names only).
type Result struct {
	RequestID uuid.UUID
	Trigger   Trigger
	Status    Status
	// ErrorKind is set for StatusRejected (config.ErrorKind*) and
	// StatusFailed (ErrorKindApply, ErrorKindRecord).
	ErrorKind string
	// Applied are the live keys applied (a changed live key this process
	// does not use, Applier.Prepare nil, is not listed); Restart the changed
	// keys that need a restart; ErrorKeys the keys a rejected file's errors
	// name; Weakened the applied keys whose change weakens security.
	Applied   []string
	Restart   []string
	ErrorKeys []string
	Weakened  []string
	// AlreadyAnswered: the database already holds this process's answer
	// to the request (e.g. a replayed request id whose earlier result this
	// process no longer remembers). Nothing was recorded or applied and
	// Status is empty.
	AlreadyAnswered bool
}

// errAlreadyAnswered: the result row exists already (recordDB rolls back).
var errAlreadyAnswered = errors.New("configreload: request already answered by this process")

// Applier applies the live keys of one component. Prepare validates the
// new config and returns a commit that only does atomic stores and cannot
// fail; nothing may change before the commit runs. A nil Prepare declares
// keys that this process does not use (e.g. server.* in a worker): a
// change of them is taken into the running config but never reported as
// applied or weakened by this process (its result does not list them).
type Applier struct {
	Name    string
	Keys    []string
	Prepare func(next *config.Bekup) (commit func(), err error)
}

// Metrics counts reload outcomes (nil: none).
type Metrics interface {
	ConfigReloadFinished(status string)
}

// DB is what the Reloader needs from the pool.
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Options configure New.
type Options struct {
	DB      DB
	Auditor *audit.Auditor
	// Load are the startup load options (flags, env lookup); ConfigPath
	// overrides their path.
	Load config.Options
	// ConfigPath is the file loaded at startup ("" when dev mode ran on
	// defaults; a reload then never searches for a file). It is made
	// absolute.
	ConfigPath string
	// Dev is the startup dev mode: the merged config is validated with it.
	Dev bool
	// Running is the startup config (copied).
	Running *config.Bekup
	// Warnings are the startup warnings (only new ones are logged after a
	// reload).
	Warnings []string
	// Appliers must cover every live key exactly once.
	Appliers []Applier

	InstanceID uuid.UUID
	Kind       ProcessKind
	Hostname   string
	Version    string
	StartedAt  time.Time

	Metrics Metrics
	Logger  *slog.Logger
}

// handledCap bounds the remembered request ids (dedup).
const handledCap = 64

// Reloader reloads the config of one process; see the package comment.
// It is safe for concurrent use (reloads are serialized).
type Reloader struct {
	o Options

	// recordFn writes the outcome (recordDB; tests replace it).
	recordFn func(ctx context.Context, req Request, res Result) error

	mu       sync.Mutex
	running  *config.Bekup
	warnings []string
	handled  map[uuid.UUID]Result
	order    []uuid.UUID
}

// New validates o.
func New(o Options) (*Reloader, error) {
	if o.DB == nil || o.Auditor == nil || o.Running == nil {
		return nil, errors.New("configreload: DB, Auditor and Running are required")
	}
	if o.InstanceID == uuid.Nil || (o.Kind != KindServe && o.Kind != KindWorker) {
		return nil, errors.New("configreload: InstanceID and Kind are required")
	}
	r, err := build(o)
	if err != nil {
		return nil, err
	}
	r.recordFn = r.recordDB
	return r, nil
}

// build validates the appliers and normalizes o (no database checks).
func build(o Options) (*Reloader, error) {
	if err := CheckAppliers(o.Appliers); err != nil {
		return nil, err
	}
	if o.ConfigPath != "" {
		abs, err := filepath.Abs(o.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("configreload: config path: %w", err)
		}
		o.ConfigPath = abs
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.StartedAt.IsZero() {
		o.StartedAt = time.Now()
	}
	o.Hostname = SafeHostname(o.Hostname)
	o.Version = safeVersion(o.Version)
	return &Reloader{
		o: o, running: o.Running.Clone(), warnings: slices.Clone(o.Warnings),
		handled: map[uuid.UUID]Result{},
	}, nil
}

// CheckAppliers reports a live key without an applier, a key covered
// twice and a key that is not live.
func CheckAppliers(appliers []Applier) error {
	seen := map[string]string{}
	for _, a := range appliers {
		for _, k := range a.Keys {
			if !config.IsLiveKey(k) {
				return fmt.Errorf("configreload: applier %s covers %s, which is not a live key", a.Name, k)
			}
			if other, dup := seen[k]; dup {
				return fmt.Errorf("configreload: %s is covered by %s and %s", k, other, a.Name)
			}
			seen[k] = a.Name
		}
	}
	for _, k := range config.LiveKeys() {
		if _, ok := seen[k]; !ok {
			return fmt.Errorf("configreload: live key %s has no applier", k)
		}
	}
	return nil
}

// InstanceID is this process's id.
func (r *Reloader) InstanceID() uuid.UUID { return r.o.InstanceID }

// StartedAt is when this process started.
func (r *Reloader) StartedAt() time.Time { return r.o.StartedAt }

// Running returns a copy of the running config.
func (r *Reloader) Running() *config.Bekup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running.Clone()
}

// RecordStartup inserts the startup ("loaded") row of this process: the
// registry the status page lists processes from.
func (r *Reloader) RecordStartup(ctx context.Context) error {
	_, err := sqlc.New(r.o.DB).InsertConfigReloadResult(ctx, r.resultParams(nil, Result{Status: StatusLoaded}))
	if err != nil {
		return fmt.Errorf("configreload: record startup: %w", err)
	}
	return nil
}

// Reload runs one reload (see the package comment). A request id that
// this process already handled returns the earlier result without
// reloading again. The error is non-nil only when the outcome could not
// be recorded (StatusFailed, ErrorKindRecord; nothing was applied).
func (r *Reloader) Reload(ctx context.Context, req Request) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.ID == uuid.Nil {
		req.ID = uuid.New()
	}
	if res, ok := r.handled[req.ID]; ok {
		return res, nil
	}
	res, commit, err := r.reload(ctx, req)
	if errors.Is(err, errAlreadyAnswered) {
		// Answered before: the transaction rolled back, nothing is applied.
		r.o.Logger.DebugContext(ctx, "config reload request already answered by this process; ignored",
			slog.String("request_id", req.ID.String()))
		res = Result{RequestID: req.ID, Trigger: req.Trigger, AlreadyAnswered: true,
			Applied: []string{}, Restart: []string{}, ErrorKeys: []string{}, Weakened: []string{}}
		r.remember(res)
		return res, nil
	}
	if err != nil {
		res = Result{RequestID: req.ID, Trigger: req.Trigger, Status: StatusFailed, ErrorKind: ErrorKindRecord,
			Applied: []string{}, Restart: []string{}, ErrorKeys: []string{}, Weakened: []string{}}
		r.o.Logger.ErrorContext(ctx, "config reload could not be recorded; the running configuration is unchanged",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		r.finish(ctx, res)
		return res, err
	}
	if commit != nil {
		commit()
	}
	r.finish(ctx, res)
	return res, nil
}

func (r *Reloader) finish(ctx context.Context, res Result) {
	r.remember(res)
	if r.o.Metrics != nil {
		r.o.Metrics.ConfigReloadFinished(string(res.Status))
	}
	level := slog.LevelInfo
	if res.Status == StatusRejected || res.Status == StatusFailed || len(res.Weakened) > 0 {
		level = slog.LevelWarn
	}
	r.o.Logger.Log(ctx, level, "config reload finished",
		slog.String("request_id", res.RequestID.String()), slog.String("trigger", string(res.Trigger)),
		slog.String("status", string(res.Status)), slog.String("error_kind", res.ErrorKind),
		slog.Any("applied", res.Applied), slog.Any("restart_required", res.Restart),
		// Never a "*_keys" name: the log handler redacts those as secrets.
		slog.Any("error_settings", res.ErrorKeys), slog.Any("weakened", res.Weakened))
}

func (r *Reloader) remember(res Result) {
	if _, ok := r.handled[res.RequestID]; ok || res.ErrorKind == ErrorKindRecord {
		// An unrecorded outcome may be retried (catch-up).
		return
	}
	r.handled[res.RequestID] = res
	r.order = append(r.order, res.RequestID)
	if len(r.order) > handledCap {
		delete(r.handled, r.order[0])
		r.order = r.order[1:]
	}
}

// reload computes the outcome, records it and returns the commit of the
// appliers (nil when nothing is to be applied). Callers hold r.mu.
func (r *Reloader) reload(ctx context.Context, req Request) (Result, func(), error) {
	res := Result{RequestID: req.ID, Trigger: req.Trigger,
		Applied: []string{}, Restart: []string{}, ErrorKeys: []string{}, Weakened: []string{}}
	loaded, err := r.load()
	if err != nil {
		// The full error (it may contain values) goes to the redacting log
		// only; the result carries the kind and key names.
		r.o.Logger.WarnContext(ctx, "config reload rejected the file; the running configuration is unchanged",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		res.Status, res.ErrorKind, res.ErrorKeys = StatusRejected, config.ErrorKindOf(err), boundKeys(config.ErrorKeys(err))
		return res, nil, r.record(ctx, req, res)
	}
	changed := config.Diff(r.running, loaded.Config)
	var live []string
	for _, k := range changed {
		if config.IsLiveKey(k) {
			live = append(live, k)
		} else {
			res.Restart = append(res.Restart, k)
		}
	}
	res.Restart = boundKeys(res.Restart)
	merged := config.WithLive(r.running, loaded.Config)
	if errs := config.ValidateBekup(merged, r.o.Dev); len(errs) > 0 {
		verr := errors.Join(errs...)
		r.o.Logger.WarnContext(ctx, "config reload rejected the merged configuration; the running configuration is unchanged",
			slog.String("request_id", req.ID.String()), slog.Any("error", verr))
		res.Status, res.ErrorKind, res.ErrorKeys = StatusRejected, config.ErrorKindInvalid, boundKeys(config.ErrorKeys(verr))
		res.Restart = []string{}
		return res, nil, r.record(ctx, req, res)
	}
	commits, err := r.prepare(live, merged)
	if err != nil {
		r.o.Logger.WarnContext(ctx, "config reload could not prepare a component; the running configuration is unchanged",
			slog.String("request_id", req.ID.String()), slog.Any("error", err))
		res.Status, res.ErrorKind = StatusFailed, ErrorKindApply
		res.Restart = []string{}
		return res, nil, r.record(ctx, req, res)
	}
	// Keys this process does not use change the running config only: they
	// are neither applied nor weakened here (another process reports them).
	applied := slices.DeleteFunc(slices.Clone(live), r.unused)
	res.Applied = boundKeys(applied)
	res.Weakened = boundKeys(Weakened(r.running, merged, applied))
	switch {
	case len(res.Restart) > 0:
		res.Status = StatusRestartRequired
	case len(res.Applied) > 0:
		res.Status = StatusApplied
	default:
		res.Status = StatusUnchanged
	}
	if err := r.record(ctx, req, res); err != nil {
		return res, nil, err
	}
	// Warnings of the config that will run (the loaded file's own warnings
	// may name restart-only values that do not apply until a restart).
	newWarnings := newOnly(merged.Warnings(), r.warnings)
	return res, func() {
		for _, c := range commits {
			c()
		}
		r.running = merged
		r.warnings = append(r.warnings, newWarnings...)
		for _, w := range newWarnings {
			r.o.Logger.WarnContext(ctx, "configuration warning after reload", slog.String("warning", w))
		}
	}, nil
}

// load re-reads the file with the startup options and the pinned path.
func (r *Reloader) load() (*config.Result[config.Bekup], error) {
	opts := r.o.Load
	opts.ConfigPath = r.o.ConfigPath
	if r.o.ConfigPath == "" {
		// Dev mode on defaults: never pick up a file that appeared since.
		opts.SearchPaths = []string{}
		opts.LookupEnv = withoutConfigEnv(opts.LookupEnv)
	}
	return config.LoadBekup(opts)
}

// withoutConfigEnv hides BEKUP_CONFIG (the process started without a file).
func withoutConfigEnv(lookup func(string) (string, bool)) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == config.EnvConfig {
			return "", false
		}
		if lookup == nil {
			return lookupOS(k)
		}
		return lookup(k)
	}
}

// unused reports whether key belongs to an applier without Prepare (a key
// this process does not use).
func (r *Reloader) unused(key string) bool {
	for _, a := range r.o.Appliers {
		if slices.Contains(a.Keys, key) {
			return a.Prepare == nil
		}
	}
	return false
}

// prepare prepares the appliers whose keys changed; nothing is applied.
func (r *Reloader) prepare(live []string, merged *config.Bekup) ([]func(), error) {
	var commits []func()
	for _, a := range r.o.Appliers {
		if a.Prepare == nil || !slices.ContainsFunc(a.Keys, func(k string) bool { return slices.Contains(live, k) }) {
			continue
		}
		c, err := a.Prepare(merged)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		if c != nil {
			commits = append(commits, c)
		}
	}
	return commits, nil
}

func (r *Reloader) record(ctx context.Context, req Request, res Result) error {
	return r.recordFn(ctx, req, res)
}

// recordDB writes the audit entry and the result row in one transaction
// (and, for a signal, the request row first). A result row of this
// process for the request that exists already rolls everything back
// (errAlreadyAnswered: a replay is neither audited nor applied).
func (r *Reloader) recordDB(ctx context.Context, req Request, res Result) error {
	return db.Tx(ctx, r.o.DB, func(q *sqlc.Queries) error {
		if req.Trigger == TriggerSignal {
			if _, err := q.InsertConfigReloadSignalRequest(ctx, sqlc.InsertConfigReloadSignalRequestParams{
				ID: req.ID, InstanceID: r.o.InstanceID,
			}); err != nil {
				return fmt.Errorf("record signal request: %w", err)
			}
		}
		if _, err := r.o.Auditor.RecordTx(ctx, q, r.auditEvent(req, res)); err != nil {
			return err
		}
		id := req.ID
		n, err := q.InsertConfigReloadResult(ctx, r.resultParams(&id, res))
		if err != nil {
			return fmt.Errorf("record result: %w", err)
		}
		if n == 0 {
			return errAlreadyAnswered
		}
		return nil
	})
}

func (r *Reloader) auditEvent(req Request, res Result) audit.Event {
	d := audit.Details{
		"request_id": req.ID.String(), "trigger": string(req.Trigger),
		"instance_id": r.o.InstanceID.String(), "process_kind": string(r.o.Kind), "status": string(res.Status),
	}
	action := audit.ActionConfigReloaded
	if res.Status == StatusRejected || res.Status == StatusFailed {
		action = audit.ActionConfigReloadFailed
		d["error_kind"] = res.ErrorKind
		// Never a "*_keys" name: audit.Redact treats those as secrets.
		d["error_settings"] = res.ErrorKeys
	} else {
		d["applied"] = res.Applied
		d["restart_required"] = res.Restart
		d["weakened"] = res.Weakened
	}
	return audit.Event{Actor: audit.SystemActor(), Action: action, TargetType: "config",
		TargetID: r.o.InstanceID.String(), Details: d}
}

func (r *Reloader) resultParams(requestID *uuid.UUID, res Result) sqlc.InsertConfigReloadResultParams {
	var kind *string
	if res.ErrorKind != "" {
		k := res.ErrorKind
		kind = &k
	}
	return sqlc.InsertConfigReloadResultParams{
		RequestID: requestID, InstanceID: r.o.InstanceID, ProcessKind: string(r.o.Kind),
		Hostname: r.o.Hostname, Version: r.o.Version, ProcessStartedAt: r.o.StartedAt,
		Status: string(res.Status), ErrorKind: kind,
		AppliedKeys: nonNil(res.Applied), RestartKeys: nonNil(res.Restart),
		ErrorKeys: nonNil(res.ErrorKeys), WeakenedKeys: nonNil(res.Weakened),
	}
}

var lookupOS = os.LookupEnv

// maxKeys bounds every key list (the table's CHECK).
const maxKeys = 256

func boundKeys(keys []string) []string {
	out := []string{}
	for _, k := range keys {
		if len(out) == maxKeys {
			break
		}
		if keyNameRE.MatchString(k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func newOnly(next, have []string) []string {
	var out []string
	for _, w := range next {
		if !slices.Contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

// keyNameRE is the shape of a key path ("section.key").
var keyNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}$`)

var (
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	versionRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

// SafeHostname maps a host name onto the stored shape (letters, digits,
// '.', '_', '-', at most 63 characters); anything else becomes "unknown".
func SafeHostname(h string) string {
	h = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, h)
	if len(h) > 63 {
		h = h[:63]
	}
	if !hostnameRE.MatchString(h) {
		return "unknown"
	}
	return h
}

func safeVersion(v string) string {
	if len(v) > 64 {
		v = v[:64]
	}
	if !versionRE.MatchString(v) {
		return "unknown"
	}
	return v
}
