package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/riskaccept"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Accepted security risks (D-228): the resolvers only translate; the
// service authorizes every call.

var errRisksUnconfigured = errors.New("resolvers: risk acceptances are not configured")

// riskError maps the service's errors to API codes.
func riskError(err error) error {
	var fe *serviceutil.FieldError
	switch {
	case errors.As(err, &fe):
		return apierror.InvalidMsg(fe.Field, fe.Msg)
	case errors.Is(err, riskaccept.ErrConditionChanged):
		return apierror.New(apierror.Conflict, "the condition changed; review the risk again")
	case errors.Is(err, riskaccept.ErrNotARisk):
		return apierror.New(apierror.Validation, "there is no such risk to accept now")
	case errors.Is(err, riskaccept.ErrNotFound):
		return apierror.New(apierror.NotFound, "acceptance not found")
	case errors.Is(err, riskaccept.ErrEnded):
		return apierror.New(apierror.Conflict, "the acceptance has already ended")
	case errors.Is(err, riskaccept.ErrForbidden):
		return apierror.New(apierror.Forbidden, "not allowed")
	default:
		return err
	}
}

// Enum mappings (GraphQL upper case, the service lower case).
func riskKindIn(k model.RiskKind) riskaccept.Kind { return riskaccept.Kind(strings.ToLower(string(k))) }

func riskKindOut(k riskaccept.Kind) model.RiskKind { return model.RiskKind(strings.ToUpper(string(k))) }

func riskDurationIn(d model.RiskDuration) riskaccept.Duration {
	switch d {
	case model.RiskDurationOneMonth:
		return riskaccept.DurationMonth
	case model.RiskDurationOneYear:
		return riskaccept.DurationYear
	case model.RiskDurationForever:
		return riskaccept.DurationForever
	default:
		return riskaccept.Duration(strings.ToLower(string(d)))
	}
}

func riskDurationOut(d riskaccept.Duration) model.RiskDuration {
	switch d {
	case riskaccept.DurationMonth:
		return model.RiskDurationOneMonth
	case riskaccept.DurationYear:
		return model.RiskDurationOneYear
	default:
		return model.RiskDurationForever
	}
}

func optUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func riskOptString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// riskAcceptanceModel converts a view.
func riskAcceptanceModel(a *riskaccept.Acceptance) model.RiskAcceptance {
	m := model.RiskAcceptance{
		ID: a.ID, Kind: riskKindOut(a.Kind), ConnectionID: optUUID(a.ConnectionID), JobID: optUUID(a.JobID),
		SubjectName: riskOptString(a.SubjectName), SubjectDeleted: a.SubjectDeleted, Condition: a.Condition,
		Duration: riskDurationOut(a.Duration), Reason: a.Reason, CreatedByMe: a.CreatedByCaller,
		CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt, EndedAt: a.EndedAt,
		Status: model.RiskAcceptanceStatus(strings.ToUpper(string(a.Status))),
	}
	if a.EndReason != "" {
		r := model.RiskEndReason(strings.ToUpper(string(a.EndReason)))
		m.EndReason = &r
	}
	if p := a.CreatedBy; p != nil {
		m.CreatedBy = &model.RiskAcceptor{ID: p.ID, DisplayName: p.DisplayName, Email: riskOptString(p.Email), Deleted: p.Deleted}
	}
	return m
}

func (r *Resolver) securityRisks(ctx context.Context) ([]model.SecurityRisk, error) {
	if r.Risks == nil {
		return nil, errRisksUnconfigured
	}
	fs, err := r.Risks.Findings(ctx)
	if err != nil {
		return nil, riskError(err)
	}
	out := make([]model.SecurityRisk, len(fs))
	for i := range fs {
		f := &fs[i]
		out[i] = model.SecurityRisk{
			Kind: riskKindOut(f.Condition.Kind), ConditionID: f.Condition.ID(), Condition: f.Condition.Display,
			ConnectionID: optUUID(f.Condition.ConnectionID), JobID: optUUID(f.Condition.JobID),
			SubjectName: riskOptString(f.SubjectName), CanAccept: f.CanAccept,
		}
		if f.Acceptance != nil {
			m := riskAcceptanceModel(f.Acceptance)
			out[i].Acceptance = &m
		}
	}
	return out, nil
}

func (r *Resolver) riskAcceptances(ctx context.Context, filter *model.RiskAcceptanceFilter, first *int) ([]model.RiskAcceptance, error) {
	if r.Risks == nil {
		return nil, errRisksUnconfigured
	}
	f := riskaccept.ListActive
	if filter != nil {
		switch *filter {
		case model.RiskAcceptanceFilterEnded:
			f = riskaccept.ListEnded
		case model.RiskAcceptanceFilterAll:
			f = riskaccept.ListAll
		default:
		}
	}
	n := 200
	if first != nil {
		if *first < 1 || *first > riskaccept.MaxListRows {
			return nil, apierror.Invalid("first", "must be between 1 and 500")
		}
		n = *first
	}
	list, err := r.Risks.List(ctx, f, n)
	if err != nil {
		return nil, riskError(err)
	}
	out := make([]model.RiskAcceptance, len(list))
	for i := range list {
		out[i] = riskAcceptanceModel(&list[i])
	}
	return out, nil
}

func (r *Resolver) acceptRisk(ctx context.Context, in model.AcceptRiskInput) (*model.RiskAcceptance, error) {
	if r.Risks == nil {
		return nil, errRisksUnconfigured
	}
	ai := riskaccept.AcceptInput{
		Kind: riskKindIn(in.Kind), ConditionID: in.ConditionID, Duration: riskDurationIn(in.Duration), Reason: in.Reason,
	}
	if in.ConnectionID != nil {
		ai.ConnectionID = *in.ConnectionID
	}
	if in.JobID != nil {
		ai.JobID = *in.JobID
	}
	a, err := r.Risks.Accept(ctx, ai)
	if err != nil {
		return nil, riskError(err)
	}
	m := riskAcceptanceModel(&a)
	return &m, nil
}

func (r *Resolver) revokeRiskAcceptance(ctx context.Context, id uuid.UUID) (*model.RiskAcceptance, error) {
	if r.Risks == nil {
		return nil, errRisksUnconfigured
	}
	a, err := r.Risks.Revoke(ctx, id)
	if err != nil {
		return nil, riskError(err)
	}
	m := riskAcceptanceModel(&a)
	return &m, nil
}
