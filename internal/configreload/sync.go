package configreload

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Sync is the long-running component (Name, Run) that answers reload
// broadcasts in one process. It acts only on its own payloads, only when a
// matching "ui" request row younger than RequestMaxAge exists (a forged
// NOTIFY does nothing) and, at start and after possibly lost
// notifications (Resync), catches up on the unanswered requests it is
// responsible for (StartupGrace).
type Sync struct {
	r      *Reloader
	db     sqlc.DBTX
	bus    *pubsub.Bus
	logger *slog.Logger
}

// NewSync returns the component; bus delivers pubsub.SettingsEvents.
func NewSync(r *Reloader, dbtx sqlc.DBTX, bus *pubsub.Bus, logger *slog.Logger) *Sync {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Sync{r: r, db: dbtx, bus: bus, logger: logger}
}

// Name implements the app component.
func (s *Sync) Name() string { return "config reload sync" }

// Run answers reload requests until ctx ends.
func (s *Sync) Run(ctx context.Context) error {
	if s.bus == nil {
		<-ctx.Done()
		return nil
	}
	sub := s.bus.Subscribe(pubsub.SettingsEvents.Name, 16)
	defer sub.Close()
	// The listener may have connected (and signaled Resync) before this
	// subscription: catch up once now.
	s.CatchUp(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-sub.C():
			id, ok := ParsePayload(m.Payload)
			if !ok {
				continue
			}
			s.handle(ctx, id)
		case <-sub.Resync():
			s.CatchUp(ctx)
		}
	}
}

// ParsePayload returns the request id of a reload payload.
func ParsePayload(p string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(p, PayloadPrefix)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	if err != nil || id.String() != rest {
		return uuid.Nil, false
	}
	return id, true
}

// handle reloads for request id when it is a recent UI request.
func (s *Sync) handle(ctx context.Context, id uuid.UUID) {
	_, err := sqlc.New(s.db).GetRecentConfigReloadUIRequest(ctx, sqlc.GetRecentConfigReloadUIRequestParams{
		ID: id, MaxAgeSeconds: RequestMaxAge.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.logger.WarnContext(ctx, "ignoring a config reload notification without a matching recent request",
			slog.String("request_id", id.String()))
		return
	}
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "checking a config reload request failed", slog.Any("error", err))
		}
		return
	}
	s.reload(ctx, id)
}

func (s *Sync) reload(ctx context.Context, id uuid.UUID) {
	rctx, cancel := context.WithTimeout(ctx, LocalTimeout)
	defer cancel()
	// The Reloader logs and counts the outcome and dedups by id; an
	// unrecorded outcome is retried by the next catch-up.
	if _, err := s.r.Reload(rctx, Request{ID: id, Trigger: TriggerUI}); err != nil {
		return
	}
}

// CatchUp answers the UI requests of the last RequestMaxAge that this
// process has not answered yet and is responsible for: made at most
// StartupGrace before its startup row (DB time on both sides; the rule
// RequestView.Expected uses).
func (s *Sync) CatchUp(ctx context.Context) {
	reqs, err := sqlc.New(s.db).UnansweredConfigReloadRequests(ctx, sqlc.UnansweredConfigReloadRequestsParams{
		GraceSeconds: StartupGrace.Seconds(), InstanceID: s.r.InstanceID(),
	})
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "listing unanswered config reload requests failed", slog.Any("error", err))
		}
		return
	}
	for _, r := range reqs {
		if ctx.Err() != nil {
			return
		}
		s.reload(ctx, r.ID)
	}
}

// PruneInterval is how often Pruner deletes old reload rows.
const PruneInterval = time.Hour

// Pruner deletes reload requests, results and startup rows older than 30
// days, except the newest row of every process listening now (serve only;
// a long-running component).
type Pruner struct {
	db       sqlc.DBTX
	logger   *slog.Logger
	interval time.Duration
}

// NewPruner returns the component (interval 0: PruneInterval).
func NewPruner(dbtx sqlc.DBTX, logger *slog.Logger, interval time.Duration) *Pruner {
	if interval <= 0 {
		interval = PruneInterval
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Pruner{db: dbtx, logger: logger, interval: interval}
}

// Name implements the app component.
func (p *Pruner) Name() string { return "config reload pruning" }

// Run prunes every interval until ctx ends.
func (p *Pruner) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			p.Prune(ctx)
		}
	}
}

// Prune deletes the old rows once (failures are logged).
func (p *Pruner) Prune(ctx context.Context) {
	q := sqlc.New(p.db)
	// The newest row of a process listening now is kept at any age, so a
	// long-running process stays on the status page.
	listening, err := Listening(ctx, q)
	if err != nil {
		if ctx.Err() == nil {
			p.logger.WarnContext(ctx, "pruning config reloads skipped: listing the listening processes failed", slog.Any("error", err))
		}
		return
	}
	if _, err := q.PruneConfigReloadRequests(ctx, listening); err != nil && ctx.Err() == nil {
		p.logger.WarnContext(ctx, "pruning config reload requests failed", slog.Any("error", err))
	}
	if _, err := q.PruneConfigReloadResults(ctx, listening); err != nil && ctx.Err() == nil {
		p.logger.WarnContext(ctx, "pruning config reload results failed", slog.Any("error", err))
	}
}
