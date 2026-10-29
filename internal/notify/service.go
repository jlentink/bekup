package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
)

// DB is what the service needs (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Inserter enqueues River jobs (*queue.Client).
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error)
}

// WebhookSender posts one webhook attempt (*webhook.Sender).
type WebhookSender interface {
	Send(ctx context.Context, d webhook.Delivery) webhook.Result
}

// Defaults of the noise rules.
const (
	DefaultCollapseWindow    = 15 * time.Minute
	DefaultMaxPerUserPerHour = 30
	// FloodWindow is the window of the per-user flood rule.
	FloodWindow = time.Hour
	// EventRetention and InboxRetention bound how long events with their
	// deliveries and inbox rows are kept.
	EventRetention = 90 * 24 * time.Hour
	InboxRetention = 180 * 24 * time.Hour
	// DigestDays is how many past days the retention digest (re)checks, so
	// a few days of downtime do not lose a digest.
	DigestDays = 3
	// DispatchInterval is how often the dispatcher polls the outbox.
	DispatchInterval = 2 * time.Second
	dispatchBatch    = 100
)

// Options configure New.
type Options struct {
	DB DB
	// Policy re-checks every recipient's access at fan-out and delivery
	// time.
	Policy *authz.Policy
	// Keyring opens the sealed webhook URLs and HMAC keys.
	Keyring *crypto.Keyring
	// Mailer sends e-mail (the 5.1 Sender); nil skips e-mail deliveries
	// (recorded as skipped).
	Mailer email.Mailer
	// Webhooks posts shared-channel webhooks (the 5.2 Sender behind the
	// shared_webhooks guard); nil skips them.
	Webhooks WebhookSender
	// PersonalWebhooks posts users' personal webhooks (a Sender behind the
	// personal_webhooks guard, which honors only allowlist entries an
	// admin scoped to personal webhooks, D-198); nil skips them. It is
	// never defaulted to Webhooks.
	PersonalWebhooks WebhookSender
	// Queue enqueues fan-out and delivery jobs; resolved late (the queue
	// client is built after the workers).
	Queue func() Inserter
	// BaseURL is server.base_url (links in notifications).
	BaseURL string
	// CollapseWindow collapses *.started events per job
	// (notifications.collapse_window).
	CollapseWindow time.Duration
	// MaxPerUserPerHour is the per-user, per-channel flood threshold of
	// e-mail and webhook deliveries (notifications.max_per_user_per_hour).
	MaxPerUserPerHour int
	// FailurePolicy auto-disables failing webhooks (D-177).
	FailurePolicy webhook.FailurePolicy
	// Publisher announces new inbox rows on pubsub.NotificationEvents
	// (IDs only, in the inserting transaction) for the
	// accountEvents stream (task 5.3b); nil: no live inbox.
	Publisher *pubsub.Publisher
	Logger    *slog.Logger
	Now       func() time.Time
}

// Service is the notification core: fan-out, deliveries, the dispatcher,
// the retention digest and cleanup.
type Service struct {
	o    Options
	base *url.URL
	// limits are the noise rules; SetLimits swaps them at runtime (config
	// reload, D-200).
	limits atomic.Pointer[Limits]
}

// Limits are the notifications.* rules a config reload may change
// (D-200).
type Limits struct {
	// CollapseWindow is notifications.collapse_window (> 0).
	CollapseWindow time.Duration
	// MaxPerUserPerHour is notifications.max_per_user_per_hour (> 0).
	MaxPerUserPerHour int
	// FailurePolicy auto-disables failing webhooks (MaxPermanent is
	// notifications.webhook_failure_threshold).
	FailurePolicy webhook.FailurePolicy
}

// CurrentLimits returns the rules in effect.
func (s *Service) CurrentLimits() Limits { return *s.limits.Load() }

// PrepareLimits validates l and returns the commit that applies it (the
// commit only does an atomic store).
func (s *Service) PrepareLimits(l Limits) (func(), error) {
	if l.CollapseWindow <= 0 || l.MaxPerUserPerHour <= 0 {
		return nil, errors.New("notify: CollapseWindow and MaxPerUserPerHour must be positive")
	}
	if err := l.FailurePolicy.Validate(); err != nil {
		return nil, err
	}
	return func() { s.limits.Store(&l) }, nil
}

// SetLimits validates and applies l at once.
func (s *Service) SetLimits(l Limits) error {
	commit, err := s.PrepareLimits(l)
	if err != nil {
		return err
	}
	commit()
	return nil
}

// New validates o.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Policy == nil || o.Keyring == nil || o.Queue == nil {
		return nil, errors.New("notify: DB, Policy, Keyring and Queue are required")
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("notify: BaseURL must be an absolute http(s) URL")
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimSuffix(u.Path, "/")
	if o.CollapseWindow <= 0 {
		o.CollapseWindow = DefaultCollapseWindow
	}
	if o.MaxPerUserPerHour <= 0 {
		o.MaxPerUserPerHour = DefaultMaxPerUserPerHour
	}
	if o.FailurePolicy == (webhook.FailurePolicy{}) {
		o.FailurePolicy = webhook.DefaultFailurePolicy()
	}
	if err := o.FailurePolicy.Validate(); err != nil {
		return nil, err
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	svc := &Service{o: o, base: u}
	svc.limits.Store(&Limits{CollapseWindow: o.CollapseWindow, MaxPerUserPerHour: o.MaxPerUserPerHour, FailurePolicy: o.FailurePolicy})
	return svc, nil
}

// EmitNow emits ev in its own transaction: for producers whose hook runs
// after their state change committed (restore and test-restore workers,
// retention anomalies, destination tests). Prefer Emit inside the
// producer's transaction where one is at hand.
func (s *Service) EmitNow(ctx context.Context, ev Event) error {
	_, err := Emit(ctx, sqlc.New(s.o.DB), ev)
	return err
}

func (s *Service) inserter() (Inserter, error) {
	ins := s.o.Queue()
	if ins == nil {
		return nil, errors.New("notify: no queue client")
	}
	return ins, nil
}

// Register registers the fan-out, delivery, digest and cleanup workers
// (the digest and cleanup run daily).
func Register(ws *queue.Workers, s *Service) error {
	if err := queue.Register(ws, &FanoutWorker{s: s}); err != nil {
		return err
	}
	if err := queue.Register(ws, &DeliverWorker{s: s}); err != nil {
		return err
	}
	if err := queue.Register(ws, &DigestWorker{s: s}); err != nil {
		return err
	}
	if err := queue.Register(ws, &CleanupWorker{s: s}); err != nil {
		return err
	}
	if err := ws.AddPeriodic(24*time.Hour, queue.NotifyRetentionDigestArgs{}); err != nil {
		return err
	}
	return ws.AddPeriodic(24*time.Hour, queue.NotifyCleanupArgs{})
}

// Name implements the app component interface (the dispatcher).
func (s *Service) Name() string { return "notification dispatcher" }

// Run polls the outbox every DispatchInterval until ctx is canceled.
func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(DispatchInterval)
	defer t.Stop()
	for {
		if _, err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
			s.o.Logger.WarnContext(ctx, "notification dispatch failed", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Dispatch enqueues the fan-out job of every event not yet dispatched (in
// batches, in the transaction that marks them) and returns how many it
// dispatched.
func (s *Service) Dispatch(ctx context.Context) (int, error) {
	ins, err := s.inserter()
	if err != nil {
		return 0, err
	}
	total := 0
	for range 20 {
		n := 0
		err := db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
			ids, err := q.ClaimUndispatchedEvents(ctx, dispatchBatch)
			if err != nil {
				return fmt.Errorf("notify: claim events: %w", err)
			}
			for _, id := range ids {
				if _, err := ins.InsertTx(ctx, tx, queue.NotifyFanoutArgs{EventID: id}, queue.InsertOptions{}); err != nil {
					return err
				}
			}
			n = len(ids)
			if n == 0 {
				return nil
			}
			return q.MarkEventsDispatched(ctx, sqlc.MarkEventsDispatchedParams{At: s.o.Now(), Ids: ids})
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < dispatchBatch {
			break
		}
	}
	return total, nil
}

// DispatchEvent enqueues the fan-out of one event at once (tests and
// callers that want no dispatcher latency).
func (s *Service) DispatchEvent(ctx context.Context, id uuid.UUID) error {
	ins, err := s.inserter()
	if err != nil {
		return err
	}
	return db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := ins.InsertTx(ctx, tx, queue.NotifyFanoutArgs{EventID: id}, queue.InsertOptions{}); err != nil {
			return err
		}
		return q.MarkEventsDispatched(ctx, sqlc.MarkEventsDispatchedParams{At: s.o.Now(), Ids: []uuid.UUID{id}})
	})
}
