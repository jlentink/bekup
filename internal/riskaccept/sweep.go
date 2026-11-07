package riskaccept

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Sweep runs from the hourly security check (system actor). It announces
// acceptances that expire within WarnBefore (once) and ends the live
// acceptances that cover nothing any more: expired, the acceptor lost the
// right, and for connection and job subjects a changed condition, a risk
// that is gone (resolved) or a deleted subject; SMTP the same way (its
// settings are in the database). A connection without a stored privilege
// verdict (a failed test) keeps its privileged-role acceptance. The
// instance posture kinds never end on a different condition: processes
// may run with different settings, and each checks Covered against its
// own. Every change is a conditional
// update, so concurrent processes audit it once. A failing row is logged
// and retried next hour.
func (s *Service) Sweep(ctx context.Context) error {
	lv, err := s.loadLive(ctx, s.o.DB)
	if err != nil {
		return err
	}
	var conns, jobs []uuid.UUID
	for k := range lv {
		if k.conn != uuid.Nil {
			conns = append(conns, k.conn)
		}
		if k.job != uuid.Nil {
			jobs = append(jobs, k.job)
		}
	}
	q := sqlc.New(s.o.DB)
	connRows := map[uuid.UUID]sqlc.ListRiskConnectionsRow{}
	if len(conns) > 0 {
		cs, err := q.ListRiskConnections(ctx, conns)
		if err != nil {
			return fmt.Errorf("riskaccept: sweep connections: %w", err)
		}
		for i := range cs {
			connRows[cs[i].ID] = cs[i]
		}
	}
	jobRows := map[uuid.UUID]sqlc.ListRiskJobsRow{}
	if len(jobs) > 0 {
		js, err := q.ListRiskJobs(ctx, jobs)
		if err != nil {
			return fmt.Errorf("riskaccept: sweep jobs: %w", err)
		}
		for _, j := range js {
			jobRows[j.ID] = j
		}
	}
	// SMTP lives in the database, so every process with the settings sees
	// the same condition: it is swept like a subject (D-228 review). A
	// process without them (a worker) leaves SMTP acceptances alone.
	var smtp *Condition
	smtpKnown := false
	if s.o.SMTP != nil {
		f, err := s.o.SMTP(ctx)
		switch {
		case errors.Is(err, ErrSMTPUnavailable):
		case err != nil:
			return fmt.Errorf("riskaccept: sweep SMTP settings: %w", err)
		default:
			smtpKnown = true
			if f.Active {
				if c, ok := SMTPUnencrypted(f.Mode, f.Host, f.Port, f.Username); ok {
					smtp = &c
				}
			}
		}
	}
	var errs []error
	for k := range lv {
		r := lv[k].row
		reason := EndReason("")
		switch {
		case !r.Unexpired:
			reason = EndExpired
		case !r.AcceptorAllowed:
			reason = EndAcceptorLostRights
		case k.kind == KindSMTPUnencrypted:
			if smtpKnown {
				if smtp == nil {
					reason = compare(false, Condition{}, r.ConditionHash)
				} else {
					reason = compare(true, *smtp, r.ConditionHash)
				}
			}
		case k.kind.OnConnection():
			c, ok := connRows[k.conn]
			switch {
			case !ok || c.Deleted:
				reason = EndSubjectDeleted
			case k.kind == KindPrivilegedRole && c.LastTestPrivileges == nil:
				// A failed (or no) test stores no verdict: that proves
				// nothing, so the acceptance stays (review finding).
			default:
				cond, risk := connectionCondition(k.kind, listedConn(&c))
				reason = compare(risk, cond, r.ConditionHash)
			}
		case k.kind.OnJob():
			j, ok := jobRows[k.job]
			switch {
			case !ok || j.Deleted:
				reason = EndSubjectDeleted
			default:
				cond, risk := EncryptionOptOut(j.ID, j.OptedOutAt)
				reason = compare(risk, cond, r.ConditionHash)
			}
		}
		if reason != "" {
			if err := s.end(ctx, r.ID, reason); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if r.ExpiresAt != nil && r.WarnedAt == nil && r.ExpiresAt.Sub(s.o.Now()) <= WarnBefore {
			if err := s.warn(ctx, r.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		s.o.Logger.WarnContext(ctx, "risk acceptance sweep incomplete", slog.String("error", err.Error()))
		return err
	}
	return nil
}

// compare is the end reason of a subject's acceptance: resolved when the
// risk is gone, condition_changed when it differs, "" when it still holds.
func compare(risk bool, cond Condition, hash []byte) EndReason {
	switch {
	case !risk:
		return EndResolved
	case len(hash) != 32 || [32]byte(hash) != cond.Hash:
		return EndConditionChanged
	default:
		return ""
	}
}

// end ends one acceptance with the system actor and audits it.
func (s *Service) end(ctx context.Context, id uuid.UUID, reason EndReason) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		e, err := q.EndRiskAcceptance(ctx, sqlc.EndRiskAcceptanceParams{ID: id, EndReason: sqlc.RiskEndReason(reason)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // another process ended it
		}
		if err != nil {
			return fmt.Errorf("riskaccept: end acceptance: %w", err)
		}
		d := endDetails(e)
		d["end_reason"] = string(reason)
		return s.record(ctx, q, audit.SystemActor(), audit.ActionRiskAcceptanceEnded, id, d)
	})
}

// warn records the expiry warning once.
func (s *Service) warn(ctx context.Context, id uuid.UUID) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		e, err := q.MarkRiskAcceptanceWarned(ctx, sqlc.MarkRiskAcceptanceWarnedParams{
			ID: id, WarnWithin: pgtype.Interval{Microseconds: WarnBefore.Microseconds(), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("riskaccept: warn acceptance: %w", err)
		}
		return s.record(ctx, q, audit.SystemActor(), audit.ActionRiskAcceptanceExpiring, id,
			details(e.ID, Kind(e.Kind), deref(e.ConnectionID), deref(e.JobID), e.Condition, Duration(e.Duration), e.ExpiresAt, e.CreatedBy))
	})
}

// EndForConnectionTx ends, in the caller's transaction, the live
// acceptances of a connection whose TLS mode (tls true) or endpoint or
// role (endpoint true) changes, audited with the caller as actor: a
// change and a change back must never silently re-cover an old risk.
func (s *Service) EndForConnectionTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, tls, endpoint bool) error {
	var kinds []string
	switch {
	case endpoint:
		kinds = []string{string(KindConnectionTLS), string(KindPrivilegedRole)}
	case tls:
		kinds = []string{string(KindConnectionTLS)}
	default:
		return nil
	}
	rows, err := q.EndLiveRiskAcceptancesForConnection(ctx, sqlc.EndLiveRiskAcceptancesForConnectionParams{ConnectionID: id, Kinds: kinds})
	if err != nil {
		return fmt.Errorf("riskaccept: end connection acceptances: %w", err)
	}
	actor := audit.SystemActor()
	if pr, ok := auth.FromContext(ctx); ok {
		actor = audit.UserActor(pr.UserID)
	}
	for _, r := range rows {
		d := endDetails(sqlc.EndRiskAcceptanceRow(r))
		d["end_reason"] = string(EndConditionChanged)
		if err := s.record(ctx, q, actor, audit.ActionRiskAcceptanceEnded, r.ID, d); err != nil {
			return err
		}
	}
	return nil
}
