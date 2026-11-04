package apiaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Consumer defaults (D-220).
const (
	// DefaultSweepInterval is the fallback poll for pending requests (a
	// lost or never sent notification delays a request by at most this).
	DefaultSweepInterval = 5 * time.Second
	// MaxRequestAge: older pending requests are rejected as expired.
	MaxRequestAge = 10 * time.Minute
	// PassSize bounds the requests decided in one pass; a full pass is
	// followed by another at once.
	PassSize = 20
	// minWakeGap spaces the passes of every wake path (notifications,
	// resync signals, sweeps), so a flood of (possibly forged)
	// notifications costs at most a few claim queries per second.
	minWakeGap = 200 * time.Millisecond
	wakeBuffer = 16
)

// Reason is why a request was rejected (run_requests.reason).
type Reason string

// Rejection reasons.
const (
	ReasonRunActive    Reason = "run_active"
	ReasonJobDisabled  Reason = "job_disabled"
	ReasonJobGone      Reason = "job_gone"
	ReasonTokenInvalid Reason = "token_invalid"
	ReasonExpired      Reason = "expired"
)

// OutcomeAccepted is the metrics outcome of an accepted request; rejected
// ones use their Reason.
const OutcomeAccepted = "accepted"

// Outcomes returns every decision outcome (metric label values).
func Outcomes() []string {
	return []string{OutcomeAccepted, string(ReasonRunActive), string(ReasonJobDisabled), string(ReasonJobGone),
		string(ReasonTokenInvalid), string(ReasonExpired)}
}

// reasonOf maps a RunFromAPI refusal to its rejection reason; ok is false
// for other errors (the request stays pending and is retried).
func reasonOf(err error) (Reason, bool) {
	switch {
	case errors.Is(err, jobs.ErrRunActive):
		return ReasonRunActive, true
	case errors.Is(err, jobs.ErrDisabled):
		return ReasonJobDisabled, true
	case errors.Is(err, jobs.ErrNotFound), errors.Is(err, jobs.ErrAPIJobOutOfScope):
		return ReasonJobGone, true
	case errors.Is(err, jobs.ErrAPITokenInvalid):
		return ReasonTokenInvalid, true
	default:
		return "", false
	}
}

// Runner starts the run of a request in the consumer's transaction
// (*jobs.Service).
type Runner interface {
	RunFromAPI(ctx context.Context, tx pgx.Tx, actor jobs.APIActor, jobID uuid.UUID) (jobs.RunRequest, error)
}

// Metrics receives the decisions after their commit (*monitor.Metrics).
type Metrics interface {
	RunRequestDecided(outcome string)
}

// Auditor records the access denials in the consumer's transaction
// (*audit.Auditor with the notify.ObserveAudit observer, which raises the
// security alerts in the same transaction).
type Auditor interface {
	RecordTx(ctx context.Context, q *sqlc.Queries, ev audit.Event) (audit.Entry, error)
}

// AuditTargetType is the audit target type of API token entries.
const AuditTargetType = "api_token"

// DB is the app pool.
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// ConsumerOptions configure NewConsumer.
type ConsumerOptions struct {
	DB     DB
	Runner Runner
	// Auditor records api_token.ip_denied for every access denial.
	Auditor Auditor
	// Bus delivers the bekup_api_requests notifications (nil: sweep
	// only).
	Bus *pubsub.Bus
	// SweepInterval overrides DefaultSweepInterval (tests).
	SweepInterval time.Duration
	// Metrics may be nil.
	Metrics Metrics
	Logger  *slog.Logger
}

// Consumer decides pending run requests; see the package documentation.
type Consumer struct {
	o ConsumerOptions
}

// NewConsumer validates o.
func NewConsumer(o ConsumerOptions) (*Consumer, error) {
	if o.DB == nil || o.Runner == nil || o.Auditor == nil {
		return nil, errors.New("apiaccess: DB, Runner and Auditor are required")
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = DefaultSweepInterval
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Consumer{o: o}, nil
}

// Name implements the app component.
func (c *Consumer) Name() string { return "api run request consumer" }

// Run decides pending requests at start, on every notification and on
// every sweep until ctx ends.
func (c *Consumer) Run(ctx context.Context) error {
	var wake <-chan pubsub.Message
	var resync <-chan struct{}
	if c.o.Bus != nil {
		sub := c.o.Bus.Subscribe(pubsub.APIRequests.Name, wakeBuffer)
		defer sub.Close()
		wake, resync = sub.C(), sub.Resync()
	}
	t := time.NewTicker(c.o.SweepInterval)
	defer t.Stop()
	last := time.Now()
	c.Drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-wake:
		case <-resync:
		}
		// Every wake path is spaced: a flood of (possibly forged)
		// notifications overflows the buffer into resync signals, which
		// must not bypass the gap. The payload is never used: coalesce
		// what queued up meanwhile.
		if !sleepCtx(ctx, minWakeGap-time.Since(last)) {
			return nil
		}
		drainWake(wake)
		drainResync(resync)
		last = time.Now()
		c.Drain(ctx)
	}
}

func drainWake(c <-chan pubsub.Message) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

func drainResync(c <-chan struct{}) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Drain decides pending requests, then processes new access denials, in
// passes of at most PassSize until a pass finds fewer or fails on one
// (failed rows stay pending and are retried by the next sweep).
func (c *Consumer) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		decided, failed := c.pass(ctx)
		if failed || decided < PassSize {
			break
		}
	}
	for ctx.Err() == nil {
		done, failed := c.denialPass(ctx)
		if failed || done < PassSize {
			return
		}
	}
}

// pass decides up to PassSize requests, one transaction each. A request
// whose decision fails is skipped for the rest of the pass.
func (c *Consumer) pass(ctx context.Context) (decided int, failed bool) {
	var skip []uuid.UUID
	for range PassSize {
		id, outcome, found, err := c.decideOne(ctx, skip)
		if !found && err == nil {
			return decided, failed
		}
		if err != nil {
			if ctx.Err() != nil {
				return decided, true
			}
			failed = true
			if id == uuid.Nil {
				c.o.Logger.WarnContext(ctx, "claiming API run requests failed", slog.Any("error", err))
				return decided, true
			}
			c.o.Logger.ErrorContext(ctx, "deciding an API run request failed; it stays pending",
				slog.String("request_id", id.String()), slog.Any("error", err))
			skip = append(skip, id)
			continue
		}
		decided++
		if c.o.Metrics != nil {
			c.o.Metrics.RunRequestDecided(outcome)
		}
	}
	return decided, failed
}

// decideOne claims the oldest pending request not in skip and decides it
// in one transaction. found is false when none is pending; id is set once
// a request was claimed.
func (c *Consumer) decideOne(ctx context.Context, skip []uuid.UUID) (id uuid.UUID, outcome string, found bool, err error) {
	var attrs []slog.Attr
	err = db.TxWith(ctx, c.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		id, outcome, found, attrs = uuid.Nil, "", false, nil
		rows, err := q.ClaimPendingRunRequests(ctx, sqlc.ClaimPendingRunRequestsParams{
			MaxAgeSeconds: MaxRequestAge.Seconds(), Exclude: skip, MaxRows: 1,
		})
		if err != nil {
			return fmt.Errorf("apiaccess: claim run requests: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		r := rows[0]
		id, found = r.ID, true
		attrs = make([]slog.Attr, 0, 5)
		attrs = append(attrs, slog.String("request_id", r.ID.String()), slog.String("job_id", r.JobID.String()),
			slog.String("api_token_id", r.TokenID.String()))
		if r.Expired {
			outcome = string(ReasonExpired)
			return reject(ctx, q, r.ID, ReasonExpired)
		}
		res, reason, err := c.start(ctx, tx, r)
		if err != nil {
			return err
		}
		if reason != "" {
			outcome = string(reason)
			return reject(ctx, q, r.ID, reason)
		}
		outcome = OutcomeAccepted
		attrs = append(attrs, slog.String("run_id", res.RunID.String()), slog.Int64("queue_job_id", res.QueueJobID))
		n, err := q.AcceptRunRequest(ctx, sqlc.AcceptRunRequestParams{ID: r.ID, RiverJobID: res.QueueJobID, RunID: res.RunID})
		if err != nil {
			return fmt.Errorf("apiaccess: accept run request: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("apiaccess: run request %s was decided concurrently", r.ID)
		}
		return nil
	})
	if err != nil {
		return id, "", found, err
	}
	if found {
		c.o.Logger.LogAttrs(ctx, slog.LevelInfo, "API run request decided", append(attrs, slog.String("outcome", outcome))...)
	}
	return id, outcome, found, nil
}

// start runs RunFromAPI under a savepoint: a refusal rolls back whatever
// it wrote and returns the rejection reason; any other error fails the
// request's transaction.
func (c *Consumer) start(ctx context.Context, tx pgx.Tx, r sqlc.ClaimPendingRunRequestsRow) (jobs.RunRequest, Reason, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return jobs.RunRequest{}, "", fmt.Errorf("apiaccess: savepoint: %w", err)
	}
	actor := jobs.APIActor{TokenID: r.TokenID, RequestID: r.ID}
	if r.Ip != nil {
		actor.IP = *r.Ip
	}
	res, runErr := c.o.Runner.RunFromAPI(ctx, sp, actor, r.JobID)
	if runErr == nil {
		if err := sp.Commit(ctx); err != nil {
			return jobs.RunRequest{}, "", fmt.Errorf("apiaccess: release savepoint: %w", err)
		}
		return res, "", nil
	}
	if err := sp.Rollback(ctx); err != nil {
		return jobs.RunRequest{}, "", errors.Join(runErr, fmt.Errorf("apiaccess: roll back to savepoint: %w", err))
	}
	reason, ok := reasonOf(runErr)
	if !ok {
		return jobs.RunRequest{}, "", runErr
	}
	return jobs.RunRequest{}, reason, nil
}

func reject(ctx context.Context, q *sqlc.Queries, id uuid.UUID, reason Reason) error {
	n, err := q.RejectRunRequest(ctx, sqlc.RejectRunRequestParams{ID: id, Reason: string(reason)})
	if err != nil {
		return fmt.Errorf("apiaccess: reject run request: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("apiaccess: run request %s was decided concurrently", id)
	}
	return nil
}

// denialPass audits up to PassSize access denials, one transaction each.
// A denial whose processing fails is skipped for the rest of the pass.
func (c *Consumer) denialPass(ctx context.Context) (done int, failed bool) {
	var skip []uuid.UUID
	for range PassSize {
		id, found, err := c.processDenial(ctx, skip)
		if !found && err == nil {
			return done, failed
		}
		if err != nil {
			if ctx.Err() != nil {
				return done, true
			}
			failed = true
			if id == uuid.Nil {
				c.o.Logger.WarnContext(ctx, "claiming API access denials failed", slog.Any("error", err))
				return done, true
			}
			c.o.Logger.ErrorContext(ctx, "processing an API access denial failed; it stays pending",
				slog.String("denial_id", id.String()), slog.Any("error", err))
			skip = append(skip, id)
			continue
		}
		done++
	}
	return done, failed
}

// processDenial claims the oldest unprocessed access denial not in skip
// and, in one transaction, audits it as api_token.ip_denied (actor kind
// api, the client address as the entry's IP; the audit observer raises the
// security alerts to the admins and the token's creator) and marks it
// processed. found is false when none is pending; id is set once a denial
// was claimed.
func (c *Consumer) processDenial(ctx context.Context, skip []uuid.UUID) (id uuid.UUID, found bool, err error) {
	var attrs []slog.Attr
	err = db.TxWith(ctx, c.o.DB, func(_ pgx.Tx, q *sqlc.Queries) error {
		id, found, attrs = uuid.Nil, false, nil
		rows, err := q.ClaimAPIAccessDenials(ctx, sqlc.ClaimAPIAccessDenialsParams{Exclude: skip, MaxRows: 1})
		if err != nil {
			return fmt.Errorf("apiaccess: claim access denials: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		r := rows[0]
		id, found = r.ID, true
		clientIP := ""
		ev := audit.Event{
			Actor: audit.APIActor(r.TokenID, r.TokenName, uuid.Nil), Action: audit.ActionAPIAddressDenied,
			TargetType: AuditTargetType, TargetID: r.TokenID.String(),
			Details: audit.Details{"denial_id": r.ID.String(), "creator_id": r.CreatedBy.String()},
		}
		if r.ClientIp != nil {
			ev.IP = *r.ClientIp
			clientIP = r.ClientIp.String()
		}
		ev.Details["client_ip"] = clientIP
		if _, err := c.o.Auditor.RecordTx(ctx, q, ev); err != nil {
			return fmt.Errorf("apiaccess: audit access denial: %w", err)
		}
		n, err := q.MarkAPIAccessDenialProcessed(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("apiaccess: mark access denial: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("apiaccess: access denial %s was processed concurrently", r.ID)
		}
		attrs = []slog.Attr{slog.String("denial_id", r.ID.String()), slog.String("api_token_id", r.TokenID.String()),
			slog.String("client_ip", clientIP)}
		return nil
	})
	if err != nil {
		return id, found, err
	}
	if found {
		c.o.Logger.LogAttrs(ctx, slog.LevelWarn, "API token used from an address outside its allowlist", attrs...)
	}
	return id, found, nil
}
