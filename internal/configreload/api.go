package configreload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/pubsub"
)

// PayloadPrefix starts the settings-event payload of a reload request
// ("config.reload:<request id>"; identifiers only).
const PayloadPrefix = "config.reload:"

// Request timings.
const (
	// RequestInterval is the minimum gap between two UI requests
	// (instance-wide; RATE_LIMITED otherwise).
	RequestInterval = 10 * time.Second
	// RequestMaxAge is how long a UI request is acted on (a NOTIFY or a
	// catch-up naming an older request is ignored).
	RequestMaxAge = 10 * time.Minute
	// LocalTimeout bounds the reload of the requesting process.
	LocalTimeout = 10 * time.Second
	// StartupGrace decides which processes answer a UI request (D-200):
	// a process is responsible for a request made at most StartupGrace
	// before its startup row was written (DB time; the row is written
	// after the file was read, so a request in between may postdate the
	// file the process runs). A process started later already loaded a
	// newer file and never answers it; Sync.CatchUp and
	// RequestView.Expected apply the same rule in SQL.
	StartupGrace = time.Minute
)

// ErrRateLimited means another reload was requested within
// RequestInterval.
var ErrRateLimited = errors.New("configreload: a reload was requested less than 10 seconds ago")

// APIOptions configure NewAPI.
type APIOptions struct {
	DB       DB
	Auditor  *audit.Auditor
	Policy   *authz.Policy
	Reloader *Reloader
	// Database is this process's app database posture (restart-only
	// settings, D-207).
	Database DatabasePosture
	Logger   *slog.Logger
}

// DatabasePosture is the app database posture shown on the configuration
// page (D-100, D-207): no host names or credentials.
type DatabasePosture struct {
	// Mode is config.Database.DatabaseMode(): url, fields or single_account.
	Mode          string
	TLSMode       string
	TLSWeakened   bool
	SingleAccount bool
}

// DatabasePostureOf derives the posture from the [database] section.
func DatabasePostureOf(d *config.Database) DatabasePosture {
	return DatabasePosture{Mode: d.DatabaseMode(), TLSMode: d.TLSMode, TLSWeakened: d.TLSWeakened(), SingleAccount: d.SingleAccount}
}

// API backs the GraphQL fields (configStatus, configReloadRequest,
// reloadConfig). Every method re-authorizes the caller.
type API struct {
	o APIOptions
	// publisher sends the NOTIFY only: the local bus would deliver before
	// the transaction commits (the local process reloads after it).
	publisher *pubsub.Publisher
}

// NewAPI validates o.
func NewAPI(o APIOptions) (*API, error) {
	if o.DB == nil || o.Auditor == nil || o.Policy == nil || o.Reloader == nil {
		return nil, errors.New("configreload: DB, Auditor, Policy and Reloader are required")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &API{o: o, publisher: &pubsub.Publisher{}}, nil
}

// ProcessResult is one process's answer (or its startup row).
type ProcessResult struct {
	InstanceID       uuid.UUID
	Kind             ProcessKind
	Hostname         string
	Version          string
	ProcessStartedAt time.Time
	FinishedAt       time.Time
	RequestID        *uuid.UUID
	// Trigger is the trigger of the answered request ("" for a startup row).
	Trigger Trigger
	// Listening: the process's LISTEN connection is up now (it can receive
	// a broadcast). False for a stopped process; from application_name,
	// so display only (ParseListenerApplicationName).
	Listening bool
	Status    Status
	ErrorKind string
	Applied   []string
	Restart   []string
	ErrorKeys []string
	Weakened  []string
}

// RequestView is one UI request with the answers so far.
type RequestView struct {
	ID          uuid.UUID
	RequestedAt time.Time
	RequestedBy *uuid.UUID
	Trigger     Trigger
	Results     []ProcessResult
	// Listening is the number of processes that can receive the
	// broadcast right now.
	Listening int
	// Expected is the number of processes the request waits for: those
	// that answered plus those listening now that are responsible for it
	// (startup row at most StartupGrace after the request, DB time; a
	// process started later never answers it).
	Expected int
}

// StatusView is the configuration page: the live keys, the newest row of
// every process listening now (any age) or seen in the last 7 days, and the
// newest UI request.
type StatusView struct {
	Database    DatabasePosture
	LiveKeys    []string
	Processes   []ProcessResult
	Listening   int
	LastRequest *RequestView
}

// Request asks every serve and worker process to reload (admin, fresh
// step-up): the request row, the audit entry and the NOTIFY commit
// together; then this process reloads at once (bounded by LocalTimeout)
// and the request is returned with the answers so far.
func (a *API) Request(ctx context.Context) (*RequestView, error) {
	if err := a.o.Policy.Authorize(ctx, authz.OpConfigReload); err != nil {
		return nil, err
	}
	p, ok := auth.FromContext(ctx)
	if !ok {
		return nil, apierror.New(apierror.Unauthenticated, "authentication required")
	}
	meta := auth.RequestMetaFrom(ctx)
	var req sqlc.ConfigReloadRequest
	err := db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		if err := q.LockConfigReloadRequests(ctx); err != nil {
			return fmt.Errorf("lock reload requests: %w", err)
		}
		var err error
		req, err = q.InsertConfigReloadUIRequest(ctx, sqlc.InsertConfigReloadUIRequestParams{
			RequestedBy: p.UserID, InstanceID: a.o.Reloader.InstanceID(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRateLimited
		}
		if err != nil {
			return fmt.Errorf("insert reload request: %w", err)
		}
		if _, err := a.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(p.UserID), Action: audit.ActionConfigReloadRequested, IP: meta.IP,
			TargetType: "config", TargetID: req.ID.String(), Details: audit.Details{"request_id": req.ID.String()},
		}); err != nil {
			return err
		}
		return a.publisher.PublishTx(ctx, q, pubsub.SettingsEvents.Name, PayloadPrefix+req.ID.String())
	})
	if errors.Is(err, ErrRateLimited) {
		return nil, apierror.New(apierror.RateLimited, "a reload was requested less than 10 seconds ago; wait and try again")
	}
	if err != nil {
		return nil, err
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), LocalTimeout)
	defer cancel()
	if _, err := a.o.Reloader.Reload(lctx, Request{ID: req.ID, Trigger: TriggerUI}); err != nil {
		// Logged by the Reloader; the other processes still answer.
		a.o.Logger.WarnContext(ctx, "the local config reload could not be recorded", slog.String("request_id", req.ID.String()))
	}
	return a.view(ctx, sqlc.New(a.o.DB), req)
}

// RequestByID returns one UI request with its answers (admin);
// NOT_FOUND for an unknown id or a signal request.
func (a *API) RequestByID(ctx context.Context, id uuid.UUID) (*RequestView, error) {
	if err := a.o.Policy.Authorize(ctx, authz.OpConfigStatusView); err != nil {
		return nil, err
	}
	q := sqlc.New(a.o.DB)
	req, err := q.GetConfigReloadRequest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && Trigger(req.Trigger) != TriggerUI) {
		return nil, apierror.New(apierror.NotFound, "reload request not found")
	}
	if err != nil {
		return nil, fmt.Errorf("configreload: load request: %w", err)
	}
	return a.view(ctx, q, req)
}

// Status returns the configuration page (admin).
func (a *API) Status(ctx context.Context) (*StatusView, error) {
	if err := a.o.Policy.Authorize(ctx, authz.OpConfigStatusView); err != nil {
		return nil, err
	}
	q := sqlc.New(a.o.DB)
	listening, err := Listening(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := q.LatestConfigReloadResultPerInstance(ctx, listening)
	if err != nil {
		return nil, fmt.Errorf("configreload: list processes: %w", err)
	}
	out := &StatusView{Database: a.o.Database, LiveKeys: config.LiveKeys(), Processes: make([]ProcessResult, 0, len(rows)), Listening: len(listening)}
	for i := range rows {
		out.Processes = append(out.Processes, processResult(&rows[i].ConfigReloadResult, rows[i].RequestTrigger, listening))
	}
	last, err := q.LatestConfigReloadRequest(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("configreload: latest request: %w", err)
	default:
		if out.LastRequest, err = a.view(ctx, q, last); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (a *API) view(ctx context.Context, q *sqlc.Queries, req sqlc.ConfigReloadRequest) (*RequestView, error) {
	rows, err := q.ConfigReloadResultsByRequest(ctx, &req.ID)
	if err != nil {
		return nil, fmt.Errorf("configreload: load results: %w", err)
	}
	listening, err := Listening(ctx, q)
	if err != nil {
		return nil, err
	}
	before, err := q.ConfigReloadInstancesResponsible(ctx, sqlc.ConfigReloadInstancesResponsibleParams{
		InstanceIds: listening, RequestedAt: req.RequestedAt, GraceSeconds: StartupGrace.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("configreload: listening processes: %w", err)
	}
	v := &RequestView{ID: req.ID, RequestedAt: req.RequestedAt, RequestedBy: req.RequestedBy, Trigger: Trigger(req.Trigger),
		Results: make([]ProcessResult, 0, len(rows)), Listening: len(listening)}
	expected := map[uuid.UUID]bool{}
	for _, id := range before {
		expected[id] = true
	}
	for i := range rows {
		v.Results = append(v.Results, processResult(&rows[i].ConfigReloadResult, rows[i].RequestTrigger, listening))
		expected[rows[i].ConfigReloadResult.InstanceID] = true
	}
	v.Expected = len(expected)
	return v, nil
}

// maxListeners bounds the listener ids taken from pg_stat_activity.
const maxListeners = 1000

// Listening returns the instance ids of the processes whose LISTEN
// connection is up now (deduplicated; never nil). The ids come from
// application_name: display and progress only (D-200).
func Listening(ctx context.Context, q *sqlc.Queries) ([]uuid.UUID, error) {
	names, err := q.ConfigReloadListeners(ctx)
	if err != nil {
		return nil, fmt.Errorf("configreload: list listeners: %w", err)
	}
	out := []uuid.UUID{}
	for _, n := range names {
		id, ok := ParseListenerApplicationName(n)
		if ok && !slices.Contains(out, id) && len(out) < maxListeners {
			out = append(out, id)
		}
	}
	return out, nil
}

func processResult(r *sqlc.ConfigReloadResult, trigger *string, listening []uuid.UUID) ProcessResult {
	out := ProcessResult{
		InstanceID: r.InstanceID, Kind: ProcessKind(r.ProcessKind), Hostname: r.Hostname, Version: r.Version,
		ProcessStartedAt: r.ProcessStartedAt, FinishedAt: r.FinishedAt, RequestID: r.RequestID,
		Listening: slices.Contains(listening, r.InstanceID),
		Status:    Status(r.Status), Applied: nonNil(r.AppliedKeys), Restart: nonNil(r.RestartKeys),
		ErrorKeys: nonNil(r.ErrorKeys), Weakened: nonNil(r.WeakenedKeys),
	}
	if r.ErrorKind != nil {
		out.ErrorKind = *r.ErrorKind
	}
	if trigger != nil && r.RequestID != nil {
		out.Trigger = Trigger(*trigger)
	}
	return out
}
