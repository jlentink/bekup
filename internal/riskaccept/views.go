package riskaccept

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// acceptRow is the common shape of the acceptance reads.
type acceptRow struct {
	ID            uuid.UUID
	Kind          sqlc.RiskKind
	ConnectionID  *uuid.UUID
	JobID         *uuid.UUID
	ConditionHash []byte
	Condition     string
	Duration      sqlc.RiskDuration
	Reason        string
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	WarnedAt      *time.Time
	EndedAt       *time.Time
	EndedBy       *uuid.UUID
	EndReason     *sqlc.RiskEndReason
	Active        bool
}

func rowOfGet(r sqlc.GetRiskAcceptanceRow) acceptRow { return acceptRow(r) }

// rowOfLive converts a live row.
func rowOfLive(r sqlc.ListLiveRiskAcceptancesRow) acceptRow {
	return acceptRow{
		ID: r.ID, Kind: r.Kind, ConnectionID: r.ConnectionID, JobID: r.JobID, ConditionHash: r.ConditionHash,
		Condition: r.Condition, Duration: r.Duration, Reason: r.Reason, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt, WarnedAt: r.WarnedAt, Active: r.Unexpired,
	}
}

// views converts rows to what the caller may see: the subject names (one
// query per subject type), the acceptor (staff, or the acceptor
// themselves; the e-mail address for admins only), the reason and the
// status.
func (s *Service) views(ctx context.Context, q *sqlc.Queries, pr auth.Principal, rows []acceptRow) ([]Acceptance, error) {
	out := make([]Acceptance, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	var conns, jobs, users []uuid.UUID
	for i := range rows {
		r := &rows[i]
		if r.ConnectionID != nil {
			conns = append(conns, *r.ConnectionID)
		}
		if r.JobID != nil {
			jobs = append(jobs, *r.JobID)
		}
		users = append(users, r.CreatedBy)
	}
	type subject struct {
		name    string
		deleted bool
	}
	connName := map[uuid.UUID]subject{}
	if len(conns) > 0 {
		cs, err := q.ListRiskConnections(ctx, conns)
		if err != nil {
			return nil, fmt.Errorf("riskaccept: connection names: %w", err)
		}
		for i := range cs {
			connName[cs[i].ID] = subject{cs[i].Name, cs[i].Deleted}
		}
	}
	jobName := map[uuid.UUID]subject{}
	if len(jobs) > 0 {
		js, err := q.ListRiskJobs(ctx, jobs)
		if err != nil {
			return nil, fmt.Errorf("riskaccept: job names: %w", err)
		}
		for _, j := range js {
			jobName[j.ID] = subject{j.Name, j.Deleted}
		}
	}
	people := map[uuid.UUID]Person{}
	us, err := q.ListRiskAcceptanceUsers(ctx, users)
	if err != nil {
		return nil, fmt.Errorf("riskaccept: acceptors: %w", err)
	}
	for _, u := range us {
		people[u.ID] = Person{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Deleted: u.Deleted}
	}
	staff := pr.Role == auth.RoleAdmin || pr.Role == auth.RoleMaintainer
	for i := range rows {
		r := &rows[i]
		a := Acceptance{
			ID: r.ID, Kind: Kind(r.Kind), Condition: r.Condition, Duration: Duration(r.Duration), Reason: r.Reason,
			CreatedByCaller: r.CreatedBy == pr.UserID, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt,
		}
		if r.ConnectionID != nil {
			a.ConnectionID = *r.ConnectionID
			if n, ok := connName[a.ConnectionID]; ok {
				a.SubjectName, a.SubjectDeleted = n.name, n.deleted
			}
		}
		if r.JobID != nil {
			a.JobID = *r.JobID
			if n, ok := jobName[a.JobID]; ok {
				a.SubjectName, a.SubjectDeleted = n.name, n.deleted
			}
		}
		if p, ok := people[r.CreatedBy]; ok && (staff || a.CreatedByCaller) {
			if pr.Role != auth.RoleAdmin && !a.CreatedByCaller {
				p.Email = ""
			}
			a.CreatedBy = &p
		}
		if r.EndReason != nil {
			a.EndReason = EndReason(*r.EndReason)
		}
		switch {
		case r.EndedAt != nil && a.EndReason == EndRevoked:
			a.Status = StatusRevoked
		case r.EndedAt != nil:
			a.Status = StatusEnded
		case !r.Active:
			a.Status = StatusExpired
		default:
			a.Status = StatusActive
		}
		out[i] = a
	}
	return out, nil
}
